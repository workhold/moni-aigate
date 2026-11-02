package maintenancetrust

// These handwritten business transactions exercise the original constraints
// with C0 installed. They are a test fixture, not a production lot writer.
import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
)

type fundingLotsObservationBusiness struct {
	t                                *testing.T
	ctx                              context.Context
	pool                             *pgxpool.Pool
	tenant, user, operator, approver string
	model, price, key, epoch         string
	accounts                         map[string]string
}

func (f *fundingLotsObservationBusiness) must(label string, err error) {
	f.t.Helper()
	if err != nil {
		predecessorPGFailure(f.t, "observer business fixture "+label, err)
	}
}

func (f *fundingLotsObservationBusiness) exec(tx pgx.Tx, sql string, args ...any) {
	f.t.Helper()
	_, err := tx.Exec(f.ctx, sql, args...)
	f.must("statement", err)
}

func (f *fundingLotsObservationBusiness) run(fn func(pgx.Tx)) {
	f.t.Helper()
	tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	f.must("read committed begin", err)
	defer tx.Rollback(context.Background())
	fn(tx)
	f.must("original deferred constraints at commit", tx.Commit(f.ctx))
}

func (f *fundingLotsObservationBusiness) seed(tx pgx.Tx) {
	for _, u := range []struct{ id, role string }{{f.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		f.exec(tx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'isolated-observer-only',$3)`, u.id, u.id+"@example.invalid", u.role)
	}
	// The original birth/genesis triggers issue seq0 in this real INSERT; the
	// fixture never supplies fl_created_xid or manufactures a genesis operation.
	f.exec(tx, `INSERT INTO tenants(id,name) VALUES($1,'post-cutover observer business')`, f.tenant)
	f.exec(tx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user)
	f.exec(tx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$1,'{"version":"v1","model":"observer-business","input_token_upper_bound":120,"context_limit":121,"output_limit":1,"default_output_limit":1,"output_limit_field":"max_tokens"}','v1')`, f.model)
	f.exec(tx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model)
	f.exec(tx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'observer business',$1,$1,$4,1000)`, f.key, f.tenant, f.user, []string{f.model})
	f.exec(tx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1000000,0,clock_timestamp()-interval '1 day',$3)`, f.price, f.model, f.operator)
	for _, kind := range []string{"AVAILABLE", "HELD", "REVENUE", "FUNDING_CLEARING"} {
		var tenant any
		if kind == "AVAILABLE" || kind == "HELD" {
			tenant = f.tenant
		}
		f.accounts[kind] = uuid.NewString()
		f.exec(tx, `INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,$2,$3)`, f.accounts[kind], tenant, kind)
	}
}

func (f *fundingLotsObservationBusiness) lock(tx pgx.Tx) {
	rows, err := tx.Query(f.ctx, `SELECT id FROM ledger_accounts ORDER BY id COLLATE "C" FOR UPDATE`)
	f.must("ordered account locks", err)
	defer rows.Close()
	for rows.Next() {
		var id string
		f.must("account lock row", rows.Scan(&id))
	}
	rows.Close()
	f.must("account lock EOF", rows.Err())
}

func (f *fundingLotsObservationBusiness) post(tx pgx.Tx, operation, kind, reference, actor string, deltas map[string]int64) string {
	journal := uuid.NewString()
	f.exec(tx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,$4,$5,$6)`, journal, f.tenant, operation, kind, reference, actor)
	for _, kind := range []string{"AVAILABLE", "HELD", "REVENUE", "FUNDING_CLEARING"} {
		if delta, ok := deltas[kind]; ok {
			f.exec(tx, `UPDATE ledger_accounts SET balance=balance+$2 WHERE id=$1`, f.accounts[kind], delta)
			f.exec(tx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3)`, journal, f.accounts[kind], delta)
		}
	}
	return journal
}

func (f *fundingLotsObservationBusiness) append(tx pgx.Tx, kind, source, journal, journalOperation string, available, held, consumed, issued int64) (string, int64) {
	var seq int64
	var previous string
	f.must("original wallet head", tx.QueryRow(f.ctx, `SELECT tail_seq,tail_operation_id FROM funding_wallet_heads WHERE tenant_id=$1 FOR UPDATE`, f.tenant).Scan(&seq, &previous))
	id := uuid.NewString()
	f.exec(tx, `INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,previous_seq,previous_id,kind,source_id,journal_id,journal_operation_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,$11,$12,$13,$14)`, id, f.tenant, f.epoch, seq+1, seq, previous, kind, source, journal, journalOperation, available, held, consumed, issued)
	return id, seq + 1
}

func (f *fundingLotsObservationBusiness) event(tx pgx.Tx, operation string, seq int64, lot, kind, journal, journalOperation string, available, held, consumed, issued int64) {
	var beforeA, beforeH, beforeC, revision int64
	var previous string
	err := tx.QueryRow(f.ctx, `SELECT available_amount,inference_held_amount,consumed_amount,revision,last_event_id FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&beforeA, &beforeH, &beforeC, &revision, &previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		f.must("original lot balance", err)
	}
	var previousID any
	if revision > 0 {
		previousID = previous
	}
	id := uuid.NewString()
	f.exec(tx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,operation_id,journal_id,previous_event_id,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`, id, f.tenant, lot, operation, seq, kind, journalOperation, journal, previousID, revision, revision+1, beforeA, beforeH, beforeC, beforeA+available, beforeH+held, beforeC+consumed, available, held, consumed, issued)
	if revision == 0 {
		f.exec(tx, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,inference_held_amount,consumed_amount,revision,last_event_id) VALUES($1,$2,$3,$4,$5,1,$6)`, lot, f.tenant, available, held, consumed, id)
	} else {
		f.exec(tx, `UPDATE funding_lot_balances SET available_amount=$2,inference_held_amount=$3,consumed_amount=$4,revision=$5,last_event_id=$6 WHERE lot_id=$1`, lot, beforeA+available, beforeH+held, beforeC+consumed, revision+1, id)
	}
	tag, err := tx.Exec(f.ctx, `UPDATE funding_wallet_heads SET tail_seq=$1,tail_operation_id=$2 WHERE tenant_id=$3 AND tail_seq=$4`, seq, operation, f.tenant, seq-1)
	f.must("wallet head CAS", err)
	if tag.RowsAffected() != 1 {
		f.t.Fatal("observer business fixture wallet head CAS did not affect one row")
	}
}

func (f *fundingLotsObservationBusiness) audit(tx pgx.Tx, actor, action, target, request, payload string) {
	f.exec(tx, `INSERT INTO audit_events(actor,tenant_scope,action,target,request_id,sanitized_diff) VALUES($1,$2,$3,$4,$5,$6::jsonb)`, actor, f.tenant, action, target, request, payload)
}

func (f *fundingLotsObservationBusiness) outbox(tx pgx.Tx, operation, kind, payload string) {
	f.exec(tx, `INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,$3,$4::jsonb)`, f.tenant, operation, kind, payload)
}

func (f *fundingLotsObservationBusiness) fund(tx pgx.Tx, lot string) {
	f.lock(tx)
	credit, receipt := uuid.NewString(), uuid.NewString()
	f.exec(tx, `INSERT INTO credit_orders(id,tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,$1,200,$3,'receipt:observer-business')`, credit, f.tenant, f.operator)
	f.audit(tx, f.operator, "credit.created", credit, "", `{"amount":"200"}`)
	journal := f.post(tx, credit, "credit", credit, f.approver, map[string]int64{"AVAILABLE": 200, "FUNDING_CLEARING": -200})
	f.exec(tx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, credit, f.approver, journal)
	var receiptAt time.Time
	f.must("funding receipt timestamp", tx.QueryRow(f.ctx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,journal_id) VALUES($1,$2,'manual_credit',$3,200,$4) RETURNING created_at`, receipt, f.tenant, credit, journal).Scan(&receiptAt))
	f.audit(tx, f.approver, "credit.approved", credit, "", `{"amount":"200"}`)
	f.outbox(tx, credit, "credited", `{"amount":"200"}`)
	operation, seq := f.append(tx, "manual_credit", receipt, journal, credit, 200, 0, 0, 200)
	f.exec(tx, `INSERT INTO funding_lots(id,tenant_id,origin_kind,funding_receipt_id,issued_amount,currency,refund_class,fifo_at) VALUES($1,$2,'funding_receipt',$3,200,'CNY','nonrefundable',$4)`, lot, f.tenant, receipt, receiptAt)
	f.event(tx, operation, seq, lot, "funding", journal, credit, 200, 0, 0, 200)
}

func (f *fundingLotsObservationBusiness) reserve(tx pgx.Tx, lot string) (request, reservation, allocation string) {
	f.lock(tx)
	request, reservation, allocation = uuid.NewString(), uuid.NewString(), uuid.NewString()
	// One unit per input token: ceil((120*1e6 + 1*0)/1e6)=120.
	f.exec(tx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,pricing_group_id,tenant_pricing_revision,retail_priced_at) VALUES($1,$2,$3,$4,$5,$6,1000000,0,120,1,'v1','r3','observer-business',clock_timestamp()+interval '300 seconds','observer-business',false,NULL,1,clock_timestamp())`, request, f.tenant, f.key, f.user, f.model, f.price)
	f.exec(tx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,120,clock_timestamp()+interval '300 seconds')`, reservation, f.tenant, request)
	journal := f.post(tx, reservation, "reserve", request, f.user, map[string]int64{"AVAILABLE": -120, "HELD": 120})
	f.audit(tx, f.user, "billing.reserve", journal, request, `{"amount":"120"}`)
	f.outbox(tx, reservation, "reserved", fmt.Sprintf(`{"amount":"120","request_id":"%s"}`, request))
	f.exec(tx, `UPDATE api_keys SET held=held+120 WHERE id=$1`, f.key)
	operation, seq := f.append(tx, "reserve", reservation, journal, reservation, -120, 120, 0, 0)
	f.exec(tx, `INSERT INTO funding_reservation_lots(id,tenant_id,reservation_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,1,120)`, allocation, f.tenant, reservation, lot)
	f.event(tx, operation, seq, lot, "reserve", journal, reservation, -120, 120, 0, 0)
	return
}

func (f *fundingLotsObservationBusiness) finish(tx pgx.Tx, lot, request, reservation, allocation, finalization string) (settlement, settlementLot string) {
	f.lock(tx)
	settlement, settlementLot = uuid.NewString(), uuid.NewString()
	journal := f.post(tx, settlement, "settled", request, "system", map[string]int64{"AVAILABLE": 40, "HELD": -120, "REVENUE": 80})
	f.exec(tx, `INSERT INTO settlements(id,tenant_id,request_id,finalization_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4,80,80,0,'settled','charged_usage',$5)`, settlement, f.tenant, request, finalization, journal)
	f.exec(tx, `UPDATE reservations SET status='settled' WHERE id=$1`, reservation)
	f.exec(tx, `UPDATE request_records SET billing_status='settled' WHERE id=$1`, request)
	f.exec(tx, `UPDATE api_keys SET held=held-120,spent=spent+80 WHERE id=$1`, f.key)
	f.audit(tx, "system", "billing.settled", settlement, request, `{"amount":"80","reason":"charged_usage"}`)
	f.outbox(tx, settlement, "settled", fmt.Sprintf(`{"amount":"80","released":"40","request_id":"%s"}`, request))
	operation, seq := f.append(tx, "finish", settlement, journal, settlement, 40, -120, 80, 0)
	f.exec(tx, `INSERT INTO funding_settlement_lots(id,tenant_id,settlement_id,reservation_lot_id,charged_amount,released_amount) VALUES($1,$2,$3,$4,80,40)`, settlementLot, f.tenant, settlement, allocation)
	f.event(tx, operation, seq, lot, "finish", journal, settlement, 40, -120, 80, 0)
	return
}

func (f *fundingLotsObservationBusiness) refund(tx pgx.Tx, lot, request, settlement, settlementLot string, amount int64) {
	f.lock(tx)
	id := uuid.NewString()
	payload := fmt.Sprintf(`{"amount":"%d"}`, amount)
	f.exec(tx, `INSERT INTO refund_orders(id,tenant_id,settlement_id,business_ref,amount,reason,requester) VALUES($1,$2,$3,$1,$4,'observer split consumption refund',$5)`, id, f.tenant, settlement, amount, f.operator)
	f.audit(tx, f.operator, "refund.created", id, request, payload)
	journal := f.post(tx, id, "refund", settlement, f.approver, map[string]int64{"AVAILABLE": amount, "REVENUE": -amount})
	f.exec(tx, `UPDATE refund_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, id, f.approver, journal)
	f.exec(tx, `UPDATE settlements SET refunded_amount=refunded_amount+$2 WHERE id=$1`, settlement, amount)
	f.audit(tx, f.approver, "refund.approved", id, request, payload)
	f.outbox(tx, id, "refunded", payload)
	operation, seq := f.append(tx, "consumption_refund", id, journal, id, amount, 0, -amount, 0)
	f.exec(tx, `INSERT INTO funding_consumption_refund_lots(id,tenant_id,refund_order_id,lot_id,settlement_lot_id,allocation_index,amount,origin) VALUES($1,$2,$3,$4,$5,1,$6,'allocated')`, uuid.NewString(), f.tenant, id, lot, settlementLot, amount)
	f.event(tx, operation, seq, lot, "consumption_refund", journal, id, amount, 0, -amount, 0)
}

func TestFundingLotsObservationPostgresOperatingGraphKeepsHistoricalBasis(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	if plan.Metadata.TenantCount != 0 || plan.Metadata.ReceiptCount != 0 || plan.Metadata.SettlementCount != 0 || plan.Metadata.ApprovedRefundCount != 0 || plan.Metadata.OpeningCount != 0 || plan.Metadata.LegacySourceCount != 0 || plan.Metadata.AvailableTotal != "0" || plan.Metadata.HeldTotal != "0" {
		t.Fatal("business fixture did not start from a genuine empty cutover")
	}
	fundingLotsInitOrdinary(t, f, plan.Expected)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("observer business pool config")
	}
	config.ConnConfig = f.cfg.Copy()
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	config.MinConns, config.MaxConns = 0, 1
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("observer business actual owner pool")
	}
	defer pool.Close()
	b := &fundingLotsObservationBusiness{t: t, ctx: ctx, pool: pool, tenant: uuid.NewString(), user: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(), key: uuid.NewString(), epoch: plan.Expected.EpochID, accounts: map[string]string{}}
	var owner, session, schema string
	b.must("actual owner LOGIN", pool.QueryRow(ctx, `SELECT current_user::text,session_user::text,current_schema()::text`).Scan(&owner, &session, &schema))
	if owner != f.owner || session != f.owner || schema != f.schema {
		t.Fatal("business writer did not use the original actual schema owner LOGIN")
	}
	b.run(b.seed)
	lot := uuid.NewString()
	b.run(func(tx pgx.Tx) { b.fund(tx, lot) })
	var request, reservation, allocation string
	b.run(func(tx pgx.Tx) { request, reservation, allocation = b.reserve(tx, lot) })
	// Use the original API for digest, metering raw JSON and latest-finalization
	// identity; the fixture does not invent a matching evidence report.
	finalization, err := billing.New(pool).RecordFinalization(ctx, billing.FinalizationInput{TenantID: b.tenant, RequestID: request, Source: "gateway", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 80, TotalTokens: 80}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none"})
	if err != nil || finalization.ID == "" {
		t.Fatal("original post-cutover finalization failed")
	}
	var settlement, settlementLot string
	b.run(func(tx pgx.Tx) {
		settlement, settlementLot = b.finish(tx, lot, request, reservation, allocation, finalization.ID)
	})
	for _, amount := range []int64{30, 20} {
		b.run(func(tx pgx.Tx) { b.refund(tx, lot, request, settlement, settlementLot, amount) })
	}
	// Independent arithmetic: 200-120+(120-80)+30+20=170 available;
	// consumed=80-30-20=30; key spent stays 80 across consumption refunds.
	var available, held, consumed, spent, keyHeld, seq, refunds int64
	var births, tenants, receipts, settlements, settlementLots, refundLots int64
	var historicalZero, nativeGenesis, triggersOn bool
	var storedBasis string
	b.must("independent operating balances", pool.QueryRow(ctx, `SELECT a.balance,h.balance,l.consumed_amount,k.spent,k.held,w.tail_seq,s.refunded_amount,
 (SELECT count(*) FROM tenants),(SELECT count(*) FROM funding_receipts),(SELECT count(*) FROM settlements),
 (SELECT count(*) FROM funding_settlement_lots),(SELECT count(*) FROM funding_consumption_refund_lots),
 (SELECT count(*) FROM funding_wallet_operations WHERE kind='tenant_genesis'),
 (SELECT tenant_count=0 AND receipt_count=0 AND settlement_count=0 AND approved_refund_count=0 AND opening_count=0 AND legacy_source_count=0 AND available_total=0 AND held_total=0 FROM funding_lot_epoch),
 (SELECT t.fl_created_xid IS NOT NULL AND t.fl_created_xid=o.created_xid AND o.seq=0 AND o.kind='tenant_genesis' FROM tenants t JOIN funding_wallet_operations o ON o.tenant_id=t.id AND o.seq=0 WHERE t.id=$1),
 NOT EXISTS(SELECT 1 FROM pg_catalog.pg_trigger g JOIN pg_catalog.pg_class c ON c.oid=g.tgrelid WHERE c.relnamespace=$3::oid AND NOT g.tgisinternal AND g.tgenabled<>'O'),
 (SELECT basis_digest FROM funding_lot_epoch)
 FROM ledger_accounts a JOIN ledger_accounts h ON h.tenant_id=a.tenant_id AND h.kind='HELD'
 JOIN funding_lot_balances l ON l.tenant_id=a.tenant_id AND l.lot_id=$2
 JOIN api_keys k ON k.tenant_id=a.tenant_id JOIN funding_wallet_heads w ON w.tenant_id=a.tenant_id
 CROSS JOIN settlements s WHERE a.tenant_id=$1 AND a.kind='AVAILABLE'`, b.tenant, lot, f.pins.AppSchema.OID).Scan(&available, &held, &consumed, &spent, &keyHeld, &seq, &refunds, &tenants, &receipts, &settlements, &settlementLots, &refundLots, &births, &historicalZero, &nativeGenesis, &triggersOn, &storedBasis))
	if available != 170 || held != 0 || consumed != 30 || spent != 80 || keyHeld != 0 || seq != 5 || refunds != 50 || tenants != 1 || receipts != 1 || settlements != 1 || settlementLots != 1 || refundLots != 2 || births != 1 || !historicalZero || !nativeGenesis || !triggersOn || storedBasis != plan.Expected.BasisDigest {
		t.Fatal("operating graph lost immutable cutover counts, native genesis or independently calculated money")
	}
	pool.Close()
	c := f.connect(t)
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("current business basis read-only begin")
	}
	defer tx.Rollback(context.Background())
	current, err := fundinglotbasis.Read(ctx, fundingLotsBasisObserver{fundingLotsObserver{ctx, tx}}, f.schema)
	if err != nil || current.Summary().BasisSHA256 == plan.Expected.BasisDigest || current.Summary().ManifestSHA256 != plan.Expected.ManifestDigest || current.Summary().DescriptorSHA256 != plan.Expected.DescriptorDigest {
		t.Fatal("current 109-table business basis did not differ from its historical cutover digest")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("current business basis read-only rollback")
	}
	result := observeFundingLotsOrdinary(t, f, plan.Expected)
	if result.Status != FundingLotsObservationMatched || result.Observed != plan.Expected || result.SourceRows == 0 || result.SourceBytes == 0 {
		t.Fatal("valid post-cutover operating graph did not match the immutable epoch")
	}
}
