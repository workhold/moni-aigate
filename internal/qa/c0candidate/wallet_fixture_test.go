package c0candidate_test

// Handwritten SQL fixtures exercise candidate commit constraints. These are not
// production helpers and are not an independent VerifyLots implementation.
import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type candidateOperation struct {
	id, source, journal, journalop string
	seq                            int64
	at                             time.Time
}
type lotAllocation struct {
	lot string
	q   int64
}

func (f *candidateFixture) lock(t pgx.Tx) {
	rows, e := t.Query(f.ctx, `SELECT id FROM ledger_accounts ORDER BY id COLLATE "C" FOR UPDATE`)
	f.must(e)
	defer rows.Close()
	for rows.Next() {
	}
	f.must(rows.Err())
	rows.Close()
}
func (f *candidateFixture) post(t pgx.Tx, operation, kind, reference, actor string, delta map[string]int64) string {
	j := uuid.NewString()
	f.exec(t, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,$4,$5,$6)`, j, f.tenant, operation, kind, reference, actor)
	for k, d := range delta {
		f.exec(t, `UPDATE ledger_accounts SET balance=balance+$2 WHERE id=$1`, f.accounts[k], d)
		f.exec(t, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3)`, j, f.accounts[k], d)
	}
	return j
}
func (f *candidateFixture) append(t pgx.Tx, k, sid, jid, jop string, n int, da, dh, ds, di int64) candidateOperation {
	var seq int64
	var prev string
	f.must(t.QueryRow(f.ctx, `SELECT tail_seq,tail_operation_id FROM funding_wallet_heads WHERE tenant_id=$1`, f.tenant).Scan(&seq, &prev))
	o := candidateOperation{id: uuid.NewString(), source: sid, journal: jid, journalop: jop, seq: seq + 1}
	f.must(t.QueryRow(f.ctx, `INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,previous_seq,previous_id,kind,source_id,journal_id,journal_operation_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING created_at`, o.id, f.tenant, f.epoch, o.seq, seq, prev, k, sid, jid, jop, n, da, dh, ds, di).Scan(&o.at))
	return o
}
func (f *candidateFixture) tail(t pgx.Tx, o candidateOperation) {
	if f.skipTail {
		return
	}
	tag, e := t.Exec(f.ctx, `UPDATE funding_wallet_heads SET tail_seq=$1,tail_operation_id=$2 WHERE tenant_id=$3 AND tail_seq=$4`, o.seq, o.id, f.tenant, o.seq-1)
	f.must(e)
	if tag.RowsAffected() != 1 {
		f.t.Fatal("fixture wallet head CAS did not affect exactly one row")
	}
}
func (f *candidateFixture) event(t pgx.Tx, o candidateOperation, lot, k string, da, dh, ds, di int64) {
	var a, h, s, rev int64
	var prev string
	e := t.QueryRow(f.ctx, `SELECT available_amount,inference_held_amount,consumed_amount,revision,last_event_id FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&a, &h, &s, &rev, &prev)
	if e != nil && e != pgx.ErrNoRows {
		f.must(e)
	}
	var pp any
	if rev > 0 {
		pp = prev
	}
	eid := uuid.NewString()
	f.exec(t, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,operation_id,journal_id,previous_event_id,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, eid, f.tenant, lot, o.id, o.seq, k, o.journalop, o.journal, pp, rev, rev+1, a, h, s, a+da, h+dh, s+ds, da, dh, ds, di)
	if rev == 0 {
		f.exec(t, `INSERT INTO funding_lot_balances VALUES($1,$2,$3,$4,$5,1,$6)`, lot, f.tenant, a+da, h+dh, s+ds, eid)
	} else {
		f.exec(t, `UPDATE funding_lot_balances SET available_amount=$2,inference_held_amount=$3,consumed_amount=$4,revision=$5,last_event_id=$6 WHERE lot_id=$1`, lot, a+da, h+dh, s+ds, rev+1, eid)
	}
}
func (f *candidateFixture) audit(t pgx.Tx, actor, action, target, request string, payload string) {
	f.exec(t, `INSERT INTO audit_events(actor,tenant_scope,action,target,request_id,sanitized_diff) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, actor, f.tenant, action, target, request, payload)
}
func (f *candidateFixture) out(t pgx.Tx, op, k, payload string) {
	f.exec(t, `INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,$3,$4::jsonb)`, f.tenant, op, k, payload)
}
func (f *candidateFixture) fund(t pgx.Tx, amount int64, lot string) {
	f.lock(t)
	credit, receipt := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO credit_orders(id,tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,$1,$3,$4,'fixture')`, credit, f.tenant, amount, f.operator)
	j := f.post(t, credit, "credit", credit, f.approver, map[string]int64{"AVAILABLE": amount, "FUNDING_CLEARING": -amount})
	f.exec(t, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, credit, f.approver, j)
	var at time.Time
	f.must(t.QueryRow(f.ctx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,journal_id) VALUES($1,$2,'manual_credit',$3,$4,$5) RETURNING created_at`, receipt, f.tenant, credit, amount, j).Scan(&at))
	f.audit(t, f.approver, "credit.approved", credit, "", fmt.Sprintf(`{"amount":"%d"}`, amount))
	f.out(t, credit, "credited", fmt.Sprintf(`{"amount":"%d"}`, amount))
	if f.preCutover {
		return
	}
	o := f.append(t, "manual_credit", receipt, j, credit, 1, amount, 0, 0, amount)
	f.exec(t, `INSERT INTO funding_lots(id,tenant_id,origin_kind,funding_receipt_id,issued_amount,currency,refund_class,fifo_at) VALUES($1,$2,'funding_receipt',$3,$4,'CNY','nonrefundable',$5)`, lot, f.tenant, receipt, amount, at)
	f.event(t, o, lot, "funding", amount, 0, 0, amount)
	f.tail(t, o)
}
func (f *candidateFixture) reserve(t pgx.Tx, amount int64, alloc []lotAllocation) (string, string) {
	f.lock(t)
	request, reservation := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,pricing_group_id,tenant_pricing_revision,retail_priced_at) VALUES($1,$2,$3,$4,$5,$6,1000000,1000000,100,20,'v1','r3','fixture',clock_timestamp()+interval '60 seconds','fixture',false,NULL,1,clock_timestamp())`, request, f.tenant, f.key, f.user, f.model, f.price)
	f.exec(t, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,$4,clock_timestamp()+interval '24 hours')`, reservation, f.tenant, request, amount)
	j := f.post(t, reservation, "reserve", request, f.user, map[string]int64{"AVAILABLE": -amount, "HELD": amount})
	f.audit(t, f.user, "billing.reserve", j, request, fmt.Sprintf(`{"amount":"%d"}`, amount))
	f.out(t, reservation, "reserved", fmt.Sprintf(`{"amount":"%d","request_id":"%s"}`, amount, request))
	f.exec(t, `UPDATE api_keys SET held=held+$2 WHERE id=$1`, f.key, amount)
	if f.skipReserveAppend || f.preCutover {
		return request, reservation
	}
	o := f.append(t, "reserve", reservation, j, reservation, len(alloc), -amount, amount, 0, 0)
	for i, a := range alloc {
		f.exec(t, `INSERT INTO funding_reservation_lots(id,tenant_id,reservation_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), f.tenant, reservation, a.lot, i+1, a.q)
		f.event(t, o, a.lot, "reserve", -a.q, a.q, 0, 0)
	}
	f.tail(t, o)
	return request, reservation
}
func (f *candidateFixture) finish(t pgx.Tx, request, reservation string, charge int64) string {
	f.lock(t)
	var r int64
	f.must(t.QueryRow(f.ctx, `SELECT amount FROM reservations WHERE id=$1`, reservation).Scan(&r))
	type edge struct {
		id, lot string
		q       int64
	}
	var es []edge
	if !f.preCutover {
		rows, e := t.Query(f.ctx, `SELECT id,lot_id,amount FROM funding_reservation_lots WHERE reservation_id=$1 ORDER BY allocation_index`, reservation)
		f.must(e)
		defer rows.Close()
		for rows.Next() {
			var a edge
			f.must(rows.Scan(&a.id, &a.lot, &a.q))
			es = append(es, a)
		}
		f.must(rows.Err())
		rows.Close()
	}
	sid := uuid.NewString()
	status, reason := "settled", "charged_usage"
	if charge == 0 {
		status, reason = "released", "released_not_sent"
	}
	d := map[string]int64{"AVAILABLE": r - charge, "HELD": -r}
	if charge > 0 {
		d["REVENUE"] = charge
	}
	j := f.post(t, sid, status, request, "system", d)
	f.exec(t, `INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4::bigint,$4::bigint::numeric,0,$5,$6,$7)`, sid, f.tenant, request, charge, status, reason, j)
	f.exec(t, `UPDATE reservations SET status=$2 WHERE id=$1`, reservation, status)
	f.exec(t, `UPDATE request_records SET billing_status=$2 WHERE id=$1`, request, status)
	f.exec(t, `UPDATE api_keys SET held=held-$2,spent=spent+$3 WHERE id=$1`, f.key, r, charge)
	f.audit(t, "system", "billing."+status, sid, request, fmt.Sprintf(`{"amount":"%d","reason":"%s"}`, charge, reason))
	f.out(t, sid, status, fmt.Sprintf(`{"amount":"%d","released":"%d","request_id":"%s"}`, charge, r-charge, request))
	if f.preCutover {
		return sid
	}
	o := f.append(t, "finish", sid, j, sid, len(es), r-charge, -r, charge, 0)
	rem := charge
	for _, a := range es {
		c := a.q
		if c > rem {
			c = rem
		}
		rem -= c
		f.exec(t, `INSERT INTO funding_settlement_lots(id,tenant_id,settlement_id,reservation_lot_id,charged_amount,released_amount) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), f.tenant, sid, a.id, c, a.q-c)
		f.event(t, o, a.lot, "finish", a.q-c, -a.q, c, 0)
	}
	f.tail(t, o)
	return sid
}
func (f *candidateFixture) refund(t pgx.Tx, sid string, amount int64, edges []lotAllocation) {
	f.lock(t)
	fid := uuid.NewString()
	var request string
	f.must(t.QueryRow(f.ctx, `SELECT request_id FROM settlements WHERE id=$1`, sid).Scan(&request))
	j := f.post(t, fid, "refund", sid, f.approver, map[string]int64{"AVAILABLE": amount, "REVENUE": -amount})
	f.exec(t, `INSERT INTO refund_orders(id,tenant_id,settlement_id,business_ref,amount,reason,state,requester,approver,journal_id) VALUES($1,$2,$3,$1,$4,'fixture','approved',$5,$6,$7)`, fid, f.tenant, sid, amount, f.operator, f.approver, j)
	if !f.skipRefundSettlementUpdate {
		f.exec(t, `UPDATE settlements SET refunded_amount=refunded_amount+$2 WHERE id=$1`, sid, amount)
	}
	f.audit(t, f.approver, "refund.approved", fid, request, fmt.Sprintf(`{"amount":"%d"}`, amount))
	f.out(t, fid, "refunded", fmt.Sprintf(`{"amount":"%d"}`, amount))
	if f.preCutover {
		return
	}
	o := f.append(t, "consumption_refund", fid, j, fid, len(edges), amount, 0, -amount, 0)
	for i, a := range edges {
		var eid string
		f.must(t.QueryRow(f.ctx, `SELECT b.id FROM funding_settlement_lots b JOIN funding_reservation_lots r ON r.id=b.reservation_lot_id WHERE b.settlement_id=$1 AND r.lot_id=$2`, sid, a.lot).Scan(&eid))
		f.exec(t, `INSERT INTO funding_consumption_refund_lots(id,tenant_id,refund_order_id,lot_id,settlement_lot_id,allocation_index,amount,origin) VALUES($1,$2,$3,$4,$5,$6,$7,'allocated')`, uuid.NewString(), f.tenant, fid, a.lot, eid, i+1, a.q)
		f.event(t, o, a.lot, "consumption_refund", a.q, 0, -a.q, 0)
	}
	f.tail(t, o)
}
