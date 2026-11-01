package c0candidate_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (f *candidateFixture) checkWallet(lots []string, available, consumed, seq, refunds, spent int64) {
	f.t.Helper()
	var gotAvailable, gotHeld, gotConsumed, gotSeq, gotRefunds, gotSpent int64
	f.must(f.app.QueryRow(f.ctx, `SELECT (SELECT balance FROM ledger_accounts WHERE id=$1),(SELECT balance FROM ledger_accounts WHERE id=$2),coalesce((SELECT sum(consumed_amount) FROM funding_lot_balances WHERE tenant_id=$3),0)::bigint,(SELECT tail_seq FROM funding_wallet_heads WHERE tenant_id=$3),coalesce((SELECT sum(amount) FROM refund_orders WHERE tenant_id=$3 AND state='approved'),0)::bigint,(SELECT spent FROM api_keys WHERE id=$4)`, f.accounts["AVAILABLE"], f.accounts["HELD"], f.tenant, f.key).Scan(&gotAvailable, &gotHeld, &gotConsumed, &gotSeq, &gotRefunds, &gotSpent))
	if gotAvailable != available || gotHeld != 0 || gotConsumed != consumed || gotSeq != seq || gotRefunds != refunds || gotSpent != spent {
		f.t.Fatalf("wallet expected A/H/S/seq/refunds/spent=%d/0/%d/%d/%d/%d; got %d/%d/%d/%d/%d/%d", available, consumed, seq, refunds, spent, gotAvailable, gotHeld, gotConsumed, gotSeq, gotRefunds, gotSpent)
	}
	if len(lots) == 2 {
		var a, h, s int64
		f.must(f.app.QueryRow(f.ctx, `SELECT available_amount,inference_held_amount,consumed_amount FROM funding_lot_balances WHERE lot_id=$1`, lots[0]).Scan(&a, &h, &s))
		if a != 2 || h != 0 || s != 58 {
			f.t.Fatalf("first lot expected 2/0/58, got %d/%d/%d", a, h, s)
		}
		f.must(f.app.QueryRow(f.ctx, `SELECT available_amount,inference_held_amount,consumed_amount FROM funding_lot_balances WHERE lot_id=$1`, lots[1]).Scan(&a, &h, &s))
		if a != 40 || h != 0 || s != 0 {
			f.t.Fatalf("second lot expected 40/0/0, got %d/%d/%d", a, h, s)
		}
	}
}

func TestCandidateWalletGraph(t *testing.T) {
	f := newCandidateFixture(t, false)
	f.install()
	f.initZero("epoch_opening", true, "")
	// Deliberately opposing UUID order proves FIFO follows issue time first.
	lots := []string{"ffffffff-ffff-4fff-8fff-ffffffffffff", "11111111-1111-4111-8111-111111111111"}
	f.run(func(tx pgx.Tx) { f.fund(tx, 60, lots[0]) }, "")
	f.run(func(tx pgx.Tx) { f.fund(tx, 40, lots[1]) }, "")
	var request, reservation, settlement string
	f.run(func(tx pgx.Tx) {
		request, reservation = f.reserve(tx, 70, []lotAllocation{{lots[0], 60}, {lots[1], 10}})
	}, "")
	var allocationLots []string
	var allocationAmounts []int64
	f.must(f.app.QueryRow(f.ctx, `SELECT array_agg(lot_id ORDER BY allocation_index),array_agg(amount ORDER BY allocation_index) FROM funding_reservation_lots WHERE reservation_id=$1`, reservation).Scan(&allocationLots, &allocationAmounts))
	if len(allocationLots) != 2 || allocationLots[0] != lots[0] || allocationLots[1] != lots[1] || allocationAmounts[0] != 60 || allocationAmounts[1] != 10 {
		t.Fatal("reserve did not persist the exact 60+10 FIFO allocation")
	}
	f.run(func(tx pgx.Tx) { settlement = f.finish(tx, request, reservation, 65) }, "")
	var charged, released []int64
	f.must(f.app.QueryRow(f.ctx, `SELECT array_agg(s.charged_amount ORDER BY a.allocation_index),array_agg(s.released_amount ORDER BY a.allocation_index) FROM funding_settlement_lots s JOIN funding_reservation_lots a ON a.id=s.reservation_lot_id WHERE s.settlement_id=$1`, settlement).Scan(&charged, &released))
	if len(charged) != 2 || charged[0] != 60 || charged[1] != 5 || released[0] != 0 || released[1] != 5 {
		t.Fatal("finish did not persist exact charged 60+5 and released 0+5")
	}
	f.run(func(tx pgx.Tx) { f.refund(tx, settlement, 1, []lotAllocation{{lots[0], 1}}) }, "23514")
	f.run(func(tx pgx.Tx) { f.refund(tx, settlement, 3, []lotAllocation{{lots[1], 3}}) }, "")
	f.run(func(tx pgx.Tx) { f.refund(tx, settlement, 4, []lotAllocation{{lots[1], 2}, {lots[0], 2}}) }, "")
	f.run(func(tx pgx.Tx) { f.reserve(tx, 1, []lotAllocation{{lots[1], 1}}) }, "23514")
	f.skipRefundSettlementUpdate = true
	f.run(func(tx pgx.Tx) { f.refund(tx, settlement, 1, []lotAllocation{{lots[0], 1}}) }, "23514")
	f.skipRefundSettlementUpdate = false
	f.checkWallet(lots, 42, 58, 6, 7, 65)
	var cumulative int64
	f.must(f.app.QueryRow(f.ctx, `SELECT refunded_amount FROM settlements WHERE id=$1`, settlement).Scan(&cumulative))
	if cumulative != 7 {
		t.Fatal("refund omission changed stored cumulative amount")
	}
	var zeroRequest, zeroReservation, zeroSettlement string
	f.run(func(tx pgx.Tx) {
		zeroRequest, zeroReservation = f.reserve(tx, 0, nil)
		zeroSettlement = f.finish(tx, zeroRequest, zeroReservation, 0)
	}, "")
	var zeroOperations, zeroEvents, zeroAllocations int
	f.must(f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM funding_wallet_operations WHERE source_id IN ($1,$2) AND event_count=0),(SELECT count(*) FROM funding_lot_events e JOIN funding_wallet_operations o ON o.id=e.wallet_operation_id WHERE o.source_id IN ($1,$2)),(SELECT count(*) FROM funding_reservation_lots WHERE reservation_id=$1)`, zeroReservation, zeroSettlement).Scan(&zeroOperations, &zeroEvents, &zeroAllocations))
	if zeroOperations != 2 || zeroEvents != 0 || zeroAllocations != 0 {
		t.Fatal("zero reserve/finish must each have one operation and no event/allocation")
	}
	f.skipReserveAppend = true
	f.run(func(tx pgx.Tx) { f.reserve(tx, 0, nil) }, "23514")
	f.skipReserveAppend = false
	f.skipTail = true
	f.run(func(tx pgx.Tx) { f.reserve(tx, 0, nil) }, "23514")
	f.skipTail = false
	before := f.snapshot()
	_, err := f.app.Exec(f.ctx, `UPDATE funding_lot_balances SET available_amount=available_amount WHERE lot_id=$1`, lots[0])
	requireCode(t, err, "23514")
	f.unchanged(before)
	f.checkWallet(lots, 42, 58, 8, 7, 65)
	// Supplementary self-consistency check, never our amount/order oracle.
	_, err = f.owner.Exec(f.ctx, `SELECT fl_check_epoch()`)
	f.must(err)
	t.Log("real APP multi-lot, inverse refund, zero operations and rejected commit atomicity passed; SQL mechanisms only")
}

func TestCandidateOldZeroOpening(t *testing.T) {
	f := newCandidateFixture(t, false)
	f.install()
	f.initZero("epoch_opening", false, "23514")
	f.initZero("tenant_genesis", false, "23514")
	// The two rejected complete init attempts left no marker or partial graph.
	f.initZero("epoch_opening", true, "")
	t.Log("pre-C0 zero tenant requires explicit zero opening; omitted/misclassified init fully rolled back")
}

func TestCandidateSavepointTopXID(t *testing.T) {
	f := newCandidateFixture(t, false)
	f.install()
	f.initZero("epoch_opening", true, "")
	newTenant := uuid.NewString()
	f.run(func(outer pgx.Tx) {
		var top string
		f.must(outer.QueryRow(f.ctx, `SELECT pg_current_xact_id()::text`).Scan(&top))
		inner, err := outer.Begin(f.ctx)
		f.must(err)
		f.exec(inner, `INSERT INTO tenants(id,name) VALUES($1,'savepoint tenant')`, newTenant)
		var bound bool
		f.must(inner.QueryRow(f.ctx, `SELECT t.fl_created_xid=o.created_xid AND t.fl_created_xid::text=$2 AND t.xmin::text<>t.fl_created_xid::text FROM tenants t JOIN funding_wallet_operations o ON o.tenant_id=t.id AND o.seq=0 WHERE t.id=$1`, newTenant, top).Scan(&bound))
		if !bound {
			t.Fatal("SAVEPOINT tenant birth must use top xid while tuple xmin differs")
		}
		f.must(inner.Commit(f.ctx))
	}, "")
	lot := uuid.NewString()
	f.run(func(tx pgx.Tx) { f.fund(tx, 10, lot) }, "")
	var request, reservation, settlement string
	f.run(func(tx pgx.Tx) { request, reservation = f.reserve(tx, 5, []lotAllocation{{lot, 5}}) }, "")
	f.run(func(tx pgx.Tx) { settlement = f.finish(tx, request, reservation, 5) }, "")
	saveLot := uuid.NewString()
	f.run(func(outer pgx.Tx) {
		var top string
		f.must(outer.QueryRow(f.ctx, `SELECT pg_current_xact_id()::text`).Scan(&top))
		inner, err := outer.Begin(f.ctx)
		f.must(err)
		f.fund(inner, 1, saveLot)
		r, v := f.reserve(inner, 0, nil)
		f.finish(inner, r, v, 0)
		f.refund(inner, settlement, 1, []lotAllocation{{lot, 1}})
		var bound bool
		f.must(inner.QueryRow(f.ctx, `SELECT j.created_txid::numeric=o.created_xid::text::numeric AND o.created_xid::text=$2 AND j.xmin::text<>o.created_xid::text FROM funding_lots l JOIN funding_receipts f ON f.id=l.funding_receipt_id JOIN funding_wallet_operations o ON o.kind='manual_credit' AND o.source_id=f.id JOIN journals j ON j.id=o.journal_id WHERE l.id=$1`, saveLot, top).Scan(&bound))
		if !bound {
			t.Fatal("SAVEPOINT funding journal and operation lost the top xid binding")
		}
		f.must(inner.Commit(f.ctx))
	}, "")
	f.checkWallet(nil, 7, 4, 7, 1, 5)
	t.Log("actual SAVEPOINT tenant birth and manual/zero-reserve/finish/refund top-xid binding passed")
}

func TestCandidatePermissions(t *testing.T) {
	f := newCandidateFixture(t, true)
	f.install()
	f.initZero("epoch_opening", true, "")
	newTenant := uuid.NewString()
	_, err := f.app.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'new APP tenant')`, newTenant)
	f.must(err)
	var birth bool
	f.must(f.app.QueryRow(f.ctx, `SELECT t.fl_created_xid IS NOT NULL AND t.fl_created_xid=o.created_xid AND o.kind='tenant_genesis' AND o.seq=0 FROM tenants t JOIN funding_wallet_operations o ON o.tenant_id=t.id WHERE t.id=$1`, newTenant).Scan(&birth))
	if !birth {
		t.Fatal("new APP tenant did not atomically bind genesis")
	}
	for _, query := range []string{
		`UPDATE tenants SET fl_created_xid=pg_current_xact_id() WHERE id=$1`,
		`INSERT INTO tenants(id,name,fl_created_xid) VALUES($1,'bad',pg_current_xact_id())`,
		`UPDATE tenants SET id=id WHERE id=$1`,
		`UPDATE tenants SET created_at=created_at WHERE id=$1`,
	} {
		before := f.snapshot()
		_, err = f.app.Exec(f.ctx, query, newTenant)
		requireCode(t, err, "42501")
		f.unchanged(before)
	}
	for _, query := range []string{
		`UPDATE funding_wallet_operations SET created_xid=pg_current_xact_id()`,
		`UPDATE funding_lot_events SET after_revision=after_revision`,
		`DELETE FROM funding_lot_events`,
	} {
		before := f.snapshot()
		_, err = f.app.Exec(f.ctx, query)
		requireCode(t, err, "42501")
		f.unchanged(before)
	}
	before := f.snapshot()
	_, err = f.owner.Exec(f.ctx, `UPDATE tenants SET fl_created_xid=NULL WHERE id=$1`, newTenant)
	requireCode(t, err, "23514")
	f.unchanged(before)
	// Worker still has the original lock-only UPDATE(id), without modifying it.
	tx, err := f.worker.Begin(f.ctx)
	f.must(err)
	var tenant string
	f.must(tx.QueryRow(f.ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, f.tenant).Scan(&tenant))
	f.must(tx.Commit(f.ctx))
	if tenant != f.tenant {
		t.Fatal("original worker tenant lock privilege changed")
	}
	for _, query := range []string{
		`SELECT * FROM credit_orders`, `SELECT * FROM vouchers`, `SELECT * FROM users`, `SELECT * FROM request_records`, `SELECT * FROM refund_orders`,
		`SELECT * FROM funding_lot_legacy_receipts`, `SELECT * FROM funding_wallet_legacy_sources`, `SELECT * FROM funding_reservation_lots`,
		`UPDATE funding_lot_balances SET available_amount=available_amount`, `INSERT INTO funding_wallet_heads VALUES('x','x',0,'x')`,
		`UPDATE funding_wallet_operations SET created_xid=pg_current_xact_id()`, `SELECT fl_source_manual('x')`,
	} {
		_, err = f.worker.Exec(f.ctx, query)
		requireCode(t, err, "42501")
	}
	// Table, type and every candidate function must all remain unavailable.
	for _, query := range []string{`SELECT * FROM funding_lots`, `SELECT fl_require_epoch()`} {
		_, err = f.projector.Exec(f.ctx, query)
		requireCode(t, err, "42501")
	}
	var typeRights bool
	f.must(f.owner.QueryRow(f.ctx, `SELECT has_type_privilege($1,'fl_source_proof','USAGE')`, f.roles[2]).Scan(&typeRights))
	if typeRights {
		t.Fatal("projector gained candidate type usage")
	}
	var tableRights int
	f.must(f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=ANY($2::text[]) AND (has_table_privilege($3,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') OR has_any_column_privilege($3,c.oid,'SELECT,INSERT,UPDATE,REFERENCES'))`, f.schema, strings.Fields(candidateTables), f.roles[2]).Scan(&tableRights))
	if tableRights != 0 {
		t.Fatal("projector gained candidate table or column privileges")
	}
	var functionCount int
	f.must(f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'fl\_%' ESCAPE '\' AND has_function_privilege($2,p.oid,'EXECUTE')`, f.schema, f.roles[2]).Scan(&functionCount))
	if functionCount != 0 {
		t.Fatal("projector gained candidate function execution")
	}
	// The actual roles are available, not SET ROLE sessions or broken objects.
	for _, pool := range []*pgxpool.Pool{f.app, f.worker, f.projector} {
		var same bool
		f.must(pool.QueryRow(f.ctx, `SELECT current_user=session_user`).Scan(&same))
		if !same {
			t.Fatal("permissions were not tested using actual LOGIN")
		}
	}
	t.Log("original B worker ACL was provisioned and verified before C0; real LOGIN negative permissions and APP UPDATE narrowing passed; no worker/runtime invocation")
}
