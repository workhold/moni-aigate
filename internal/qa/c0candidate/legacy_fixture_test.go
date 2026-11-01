package c0candidate_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestCandidateLegacyRefundCumulative(t *testing.T) {
	f := newCandidateFixture(t, false)
	f.preCutover = true
	f.run(func(tx pgx.Tx) { f.fund(tx, 140, "") }, "")
	var request, reservation, settlement string
	f.run(func(tx pgx.Tx) { request, reservation = f.reserve(tx, 100, nil) }, "")
	f.run(func(tx pgx.Tx) { settlement = f.finish(tx, request, reservation, 100) }, "")
	f.run(func(tx pgx.Tx) { f.refund(tx, settlement, 20, nil) }, "")
	f.preCutover = false
	f.install()
	f.initLegacy()
	f.run(func(tx pgx.Tx) { f.legacyRefund(tx, settlement, 30) }, "")
	f.assertLegacy(settlement, 50, 90, 1)
	f.skipRefundSettlementUpdate = true
	f.run(func(tx pgx.Tx) { f.legacyRefund(tx, settlement, 1) }, "23514")
	f.skipRefundSettlementUpdate = false
	f.assertLegacy(settlement, 50, 90, 1)
	f.run(func(tx pgx.Tx) { f.legacyRefund(tx, settlement, 50) }, "")
	f.assertLegacy(settlement, 100, 140, 2)
	var oldRefund string
	f.must(f.app.QueryRow(f.ctx, `SELECT refund_order_id FROM funding_lot_legacy_approved_refunds`).Scan(&oldRefund))
	before := f.snapshot()
	_, err := f.app.Exec(f.ctx, `SELECT fl_check_refund($1)`, oldRefund)
	f.must(err)
	f.unchanged(before)
	var oldLots int
	f.must(f.app.QueryRow(f.ctx, `SELECT count(*) FROM funding_lots WHERE legacy_refund_order_id=$1`, oldRefund).Scan(&oldLots))
	if oldLots != 0 {
		t.Fatal("legacy approved reread created a new lot")
	}
	store := billing.New(f.app)
	over, err := store.CreateRefund(f.ctx, billing.RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settlement, Amount: 1, BusinessRef: uuid.NewString(), Reason: "fixture", IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture"})
	f.must(err)
	before = f.snapshot()
	_, err = store.ApproveRefund(f.ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: over.ID, ActorID: f.approver})
	if business, ok := err.(*billing.Error); !ok || business.Code != "refund_exceeds_charge" {
		t.Fatalf("original approval must reject cumulative overflow, got %v", err)
	}
	f.unchanged(before)
	_, err = f.owner.Exec(f.ctx, `SELECT fl_check_epoch()`)
	f.must(err)
	t.Log("actual APP before/after cutover cumulative 20->50->100, counter omission atomicity, original business bound, old reread and unchanged spent passed")
}

func (f *candidateFixture) assertLegacy(settlement string, refunded, available, seq int64) {
	f.t.Helper()
	var total, approved, spent, a, h, tail int64
	f.must(f.app.QueryRow(f.ctx, `SELECT refunded_amount,(SELECT sum(amount) FROM refund_orders WHERE settlement_id=$1 AND state='approved')::bigint,(SELECT spent FROM api_keys WHERE id=$2),(SELECT balance FROM ledger_accounts WHERE id=$3),(SELECT balance FROM ledger_accounts WHERE id=$4),(SELECT tail_seq FROM funding_wallet_heads WHERE tenant_id=$5) FROM settlements WHERE id=$1`, settlement, f.key, f.accounts["AVAILABLE"], f.accounts["HELD"], f.tenant).Scan(&total, &approved, &spent, &a, &h, &tail))
	if total != refunded || approved != refunded || spent != 100 || a != available || h != 0 || tail != seq {
		f.t.Fatalf("legacy expected refund/spent/A/H/tail=%d/100/%d/0/%d, got %d (approved %d)/%d/%d/%d/%d", refunded, available, seq, total, approved, spent, a, h, tail)
	}
}

func (f *candidateFixture) initLegacy() {
	tx, err := f.owner.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(f.ctx)
	var at time.Time
	f.must(tx.QueryRow(f.ctx, `SELECT date_trunc('microseconds',clock_timestamp())`).Scan(&at))
	opening, lot, genesis := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(tx, `INSERT INTO funding_lot_legacy_receipts(receipt_id,epoch_id,tenant_id,source_kind,source_id,amount,journal_id) SELECT id,$1,tenant_id,source_kind,source_id,amount,journal_id FROM funding_receipts`, f.epoch)
	f.exec(tx, `INSERT INTO funding_lot_legacy_settlements(settlement_id,epoch_id,tenant_id,reservation_id,request_id,charged_amount,refunded_amount_at_cutover,journal_id) SELECT s.id,$1,s.tenant_id,v.id,s.request_id,s.charged_amount,s.refunded_amount,s.journal_id FROM settlements s JOIN reservations v ON v.request_id=s.request_id`, f.epoch)
	f.exec(tx, `INSERT INTO funding_lot_legacy_approved_refunds(refund_order_id,epoch_id,tenant_id,settlement_id,amount,journal_id) SELECT id,$1,tenant_id,settlement_id,amount,journal_id FROM refund_orders WHERE state='approved'`, f.epoch)
	f.exec(tx, `INSERT INTO funding_wallet_legacy_sources(epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount) SELECT $1,f.tenant_id,f.source_kind,f.id,j.id,j.operation_id,f.amount FROM funding_receipts f JOIN journals j ON j.id=f.journal_id`, f.epoch)
	f.exec(tx, `INSERT INTO funding_wallet_legacy_sources(epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount,request_id,reservation_id) SELECT $1,v.tenant_id,'reserve',v.id,j.id,j.operation_id,v.amount,v.request_id,v.id FROM reservations v JOIN journals j ON j.operation_id=v.id AND j.kind='reserve'`, f.epoch)
	f.exec(tx, `INSERT INTO funding_wallet_legacy_sources(epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount,request_id,reservation_id,settlement_id) SELECT $1,s.tenant_id,'finish',s.id,j.id,j.operation_id,s.charged_amount,s.request_id,v.id,s.id FROM settlements s JOIN reservations v ON v.request_id=s.request_id JOIN journals j ON j.id=s.journal_id`, f.epoch)
	f.exec(tx, `INSERT INTO funding_wallet_legacy_sources(epoch_id,tenant_id,kind,source_id,journal_id,journal_operation_id,amount,request_id,reservation_id,settlement_id) SELECT $1,f.tenant_id,'consumption_refund',f.id,j.id,j.operation_id,f.amount,s.request_id,v.id,s.id FROM refund_orders f JOIN settlements s ON s.id=f.settlement_id JOIN reservations v ON v.request_id=s.request_id JOIN journals j ON j.id=f.journal_id WHERE f.state='approved'`, f.epoch)
	f.exec(tx, `INSERT INTO funding_lot_legacy_openings(id,epoch_id,tenant_id,kind,account_id,amount,observed_at) VALUES($1,$2,$3,'available',$4,60,$5)`, opening, f.epoch, f.tenant, f.accounts["AVAILABLE"], at)
	f.exec(tx, `INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,kind,source_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,0,'epoch_opening',$2,1,60,0,0,60)`, genesis, f.tenant, f.epoch)
	f.exec(tx, `INSERT INTO funding_wallet_heads VALUES($1,$2,0,$3)`, f.tenant, f.epoch, genesis)
	f.exec(tx, `INSERT INTO funding_lots(id,tenant_id,origin_kind,legacy_opening_id,issued_amount,currency,refund_class,fifo_at) VALUES($1,$2,'legacy_available',$3,60,'CNY','nonrefundable',$4)`, lot, f.tenant, opening, at)
	event := uuid.NewString()
	f.exec(tx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,wallet_operation_id,wallet_seq,kind,before_revision,after_revision,before_available,before_held,before_consumed,after_available,after_held,after_consumed,available_delta,inference_held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,$4,0,'legacy_opening',0,1,0,0,0,60,0,0,60,0,0,60)`, event, f.tenant, lot, genesis)
	f.exec(tx, `INSERT INTO funding_lot_balances VALUES($1,$2,60,0,0,1,$3)`, lot, f.tenant, event)
	f.proofFixture(tx, at)
	f.exec(tx, `INSERT INTO funding_lot_epoch(singleton,id,schema_version,database_oid,schema_oid,basis_digest,manifest_digest,descriptor_digest,runtime_proof_digest,tenant_count,receipt_count,settlement_count,approved_refund_count,opening_count,legacy_source_count,available_total,held_total,actor,created_at) SELECT true,$1,1,(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,repeat('c',64),repeat('a',64),repeat('b',64),encode(sha256('{}'::bytea),'hex'),1,1,1,1,1,4,60,0,'offline-maintenance',$2`, f.epoch, at)
	f.must(tx.Commit(f.ctx))
}

func (f *candidateFixture) legacyRefund(tx pgx.Tx, sid string, n int64) {
	f.lock(tx)
	id, lot := uuid.NewString(), uuid.NewString()
	var request string
	f.must(tx.QueryRow(f.ctx, `SELECT request_id FROM settlements WHERE id=$1`, sid).Scan(&request))
	j := f.post(tx, id, "refund", sid, f.approver, map[string]int64{"AVAILABLE": n, "REVENUE": -n})
	f.exec(tx, `INSERT INTO refund_orders(id,tenant_id,settlement_id,business_ref,amount,reason,state,requester,approver,journal_id) VALUES($1,$2,$3,$1,$4,'fixture','approved',$5,$6,$7)`, id, f.tenant, sid, n, f.operator, f.approver, j)
	if !f.skipRefundSettlementUpdate {
		f.exec(tx, `UPDATE settlements SET refunded_amount=refunded_amount+$2 WHERE id=$1`, sid, n)
	}
	f.audit(tx, f.approver, "refund.approved", id, request, fmt.Sprintf(`{"amount":"%d"}`, n))
	f.out(tx, id, "refunded", fmt.Sprintf(`{"amount":"%d"}`, n))
	op := f.append(tx, "legacy_consumption_refund", id, j, id, 1, n, 0, 0, n)
	f.exec(tx, `INSERT INTO funding_lots(id,tenant_id,origin_kind,legacy_refund_order_id,issued_amount,currency,refund_class,fifo_at) VALUES($1,$2,'legacy_consumption_refund',$3,$4,'CNY','nonrefundable',$5)`, lot, f.tenant, id, n, op.at)
	f.exec(tx, `INSERT INTO funding_consumption_refund_lots(id,tenant_id,refund_order_id,lot_id,allocation_index,amount,origin) VALUES($1,$2,$3,$4,1,$5,'legacy_settlement')`, uuid.NewString(), f.tenant, id, lot, n)
	f.event(tx, op, lot, "legacy_consumption_refund", n, 0, 0, n)
	f.tail(tx, op)
}
