package billing

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type refundBucketOrder struct {
	id, lot string
	amount  int64
}

func refundBucketOrderTx(t *testing.T, f *fixture, tx pgx.Tx, lot, state string, amount int64) refundBucketOrder {
	t.Helper()
	o := refundBucketOrder{id: uuid.NewString(), lot: lot, amount: amount}
	if _, err := tx.Exec(f.ctx, `INSERT INTO cash_refund_orders(id,tenant_id,lot_id,business_ref,coin_amount,reason,state,requested_by,maker,approver) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, o.id, f.tenant, lot, uuid.NewString(), amount, "034.3 accounting fixture", state, f.user, f.operator, f.approver); err != nil {
		t.Fatal(err)
	}
	return o
}

func refundBucketWrite(t *testing.T, f *fixture, tx pgx.Tx, o refundBucketOrder, kind string, revision int64, journalDeltas, eventDeltas map[string]int64) string {
	t.Helper()
	operation := uuid.NewString()
	journalKind := map[string]string{"refund_hold": "cash_refund_hold", "refund_retire": "cash_refund_retire", "refund_release": "cash_refund_release"}[kind]
	journal, err := post(f.ctx, tx, f.tenant, operation, journalKind, o.id, f.approver, journalDeltas)
	if err != nil {
		t.Logf("refund journal stage: %v", err)
		return ""
	}
	balances, err := lockFundingLotBalancesTx(f.ctx, tx, f.tenant, []string{o.lot})
	if err != nil {
		t.Logf("refund balance lock stage: %v", err)
		return ""
	}
	balance := balances[o.lot]
	next, err := projectFundingLotChange(balance, eventDeltas["available"], eventDeltas["held"], eventDeltas["consumed"], eventDeltas["refundHeld"], eventDeltas["retired"])
	if err != nil {
		t.Logf("refund projection stage: %v", err)
		return ""
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed,delta_refund_held,delta_retired,refund_order_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, uuid.NewString(), f.tenant, o.lot, operation, journal, kind, revision, eventDeltas["available"], eventDeltas["held"], eventDeltas["consumed"], eventDeltas["refundHeld"], eventDeltas["retired"], o.id); err != nil {
		t.Logf("refund event stage: %v", err)
		return ""
	}
	if _, err = tx.Exec(f.ctx, `UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,refund_held_amount=$6,retired_amount=$7,revision=$8 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$9 AND available_amount=$10 AND held_amount=$11 AND consumed_amount=$12 AND refund_held_amount=$13 AND retired_amount=$14`, f.tenant, o.lot, next.available, next.held, next.consumed, next.refundHeld, next.retired, next.revision, balance.revision, balance.available, balance.held, balance.consumed, balance.refundHeld, balance.retired); err != nil {
		t.Logf("refund balance update stage: %v", err)
		return ""
	}
	return journal
}

func refundBucketHold(t *testing.T, f *fixture, tx pgx.Tx, o refundBucketOrder, revision int64) string {
	t.Helper()
	return refundBucketWrite(t, f, tx, o, "refund_hold", revision, map[string]int64{"AVAILABLE": -o.amount, "REFUND_HELD": o.amount}, map[string]int64{"available": -o.amount, "refundHeld": o.amount})
}
func refundBucketRetire(t *testing.T, f *fixture, tx pgx.Tx, o refundBucketOrder, revision int64) string {
	t.Helper()
	return refundBucketWrite(t, f, tx, o, "refund_retire", revision, map[string]int64{"REFUND_HELD": -o.amount, "FUNDING_CLEARING": o.amount}, map[string]int64{"refundHeld": -o.amount, "retired": o.amount})
}
func refundBucketRelease(t *testing.T, f *fixture, tx pgx.Tx, o refundBucketOrder, revision int64) string {
	t.Helper()
	return refundBucketWrite(t, f, tx, o, "refund_release", revision, map[string]int64{"REFUND_HELD": -o.amount, "AVAILABLE": o.amount}, map[string]int64{"available": o.amount, "refundHeld": -o.amount})
}

func refundBucketSetPointer(t *testing.T, f *fixture, tx pgx.Tx, o refundBucketOrder, column, journal string) {
	t.Helper()
	if column != "hold_journal_id" && column != "retire_journal_id" && column != "release_journal_id" {
		t.Fatal("invalid test pointer")
	}
	if _, err := tx.Exec(f.ctx, `UPDATE cash_refund_orders SET `+column+`=$2 WHERE id=$1`, o.id, journal); err != nil {
		t.Logf("refund pointer stage: %v", err)
	}
}

func refundBucketCommitRejected(t *testing.T, f *fixture, write func(pgx.Tx) error) {
	t.Helper()
	var before int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM cash_refund_orders`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	err = write(tx)
	if err == nil {
		err = tx.Commit(f.ctx)
	}
	if err == nil {
		t.Fatal("invalid refund accounting committed")
	}
	var orders int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM cash_refund_orders`).Scan(&orders); err != nil || orders != before {
		t.Fatalf("rejected fixture changed orders=%d before=%d err=%v", orders, before, err)
	}
	check, err := f.store.CheckWalletSources(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("rejected fixture changed existing sources: %+v %v", check, err)
	}
}

func refundBucketOnlyLot(t *testing.T, f *fixture) string {
	t.Helper()
	var lot string
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM funding_lots WHERE tenant_id=$1 AND source_kind='manual_credit' AND refund_class='cash_original' ORDER BY fifo_at,id LIMIT 1`, f.tenant).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	return lot
}

func refundBucketBalanced(t *testing.T, f *fixture) {
	t.Helper()
	check, err := f.store.CheckWalletSources(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("five-bucket check failed: %+v %v", check, err)
	}
	funding, err := f.store.VerifyFunding(f.ctx, f.tenant)
	if err != nil || !funding.Balanced {
		t.Fatalf("five-bucket funding check failed: %+v %v", funding, err)
	}
}

func TestPostgresRefundBucketsHoldRetireReleaseConservation(t *testing.T) {
	t.Run("completed", func(t *testing.T) {
		f := fundingFixture(t)
		lot := refundBucketOnlyLot(t, f)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		o := refundBucketOrderTx(t, f, tx, lot, "approved", 40)
		hold := refundBucketHold(t, f, tx, o, 2)
		refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		refundBucketBalanced(t, f)
		tx, err = f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(f.ctx, `UPDATE cash_refund_orders SET state='completed' WHERE id=$1`, o.id); err != nil {
			t.Fatal(err)
		}
		retire := refundBucketRetire(t, f, tx, o, 3)
		refundBucketSetPointer(t, f, tx, o, "retire_journal_id", retire)
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		var available, held, consumed, refundHeld, retired int64
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,held_amount,consumed_amount,refund_held_amount,retired_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &held, &consumed, &refundHeld, &retired); err != nil || available != 960 || held != 0 || consumed != 0 || refundHeld != 0 || retired != 40 {
			t.Fatalf("completed bucket state=%d/%d/%d/%d/%d %v", available, held, consumed, refundHeld, retired, err)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("cancelled", func(t *testing.T) {
		f := fundingFixture(t)
		lot := refundBucketOnlyLot(t, f)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		o := refundBucketOrderTx(t, f, tx, lot, "approved", 60)
		hold := refundBucketHold(t, f, tx, o, 2)
		refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		tx, err = f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(f.ctx, `UPDATE cash_refund_orders SET state='cancelled' WHERE id=$1`, o.id); err != nil {
			t.Fatal(err)
		}
		release := refundBucketRelease(t, f, tx, o, 3)
		refundBucketSetPointer(t, f, tx, o, "release_journal_id", release)
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		var available, retired, refundHeld int64
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,retired_amount,refund_held_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &retired, &refundHeld); err != nil || available != 1000 || retired != 0 || refundHeld != 0 {
			t.Fatalf("release did not restore buckets: %d/%d/%d %v", available, retired, refundHeld, err)
		}
		refundBucketBalanced(t, f)
	})
}

func TestPostgresRefundBucketsRejectsEarlyRetireAndNonCash(t *testing.T) {
	t.Run("terminal before own hold", func(t *testing.T) {
		f := fundingFixture(t)
		lot := refundBucketOnlyLot(t, f)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		o := refundBucketOrderTx(t, f, tx, lot, "approved", 60)
		refundBucketSetPointer(t, f, tx, o, "hold_journal_id", refundBucketHold(t, f, tx, o, 2))
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		refundBucketCommitRejected(t, f, func(tx pgx.Tx) error {
			other := refundBucketOrderTx(t, f, tx, lot, "completed", 40)
			retire := refundBucketRetire(t, f, tx, other, 3)
			hold := refundBucketHold(t, f, tx, other, 4)
			refundBucketSetPointer(t, f, tx, other, "retire_journal_id", retire)
			refundBucketSetPointer(t, f, tx, other, "hold_journal_id", hold)
			return nil
		})
	})
	t.Run("opening is not cash original", func(t *testing.T) {
		f := fundingFixture(t)
		tenant := accountBatchTenant(t, f)
		f.tenant = tenant
		opened, err := f.store.OpenWallet(f.ctx, openingInput(f))
		if err != nil {
			t.Fatal(err)
		}
		refundBucketCommitRejected(t, f, func(tx pgx.Tx) error {
			o := refundBucketOrderTx(t, f, tx, opened.Receipt.ID, "approved", 10)
			hold := refundBucketHold(t, f, tx, o, 2)
			refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
			return nil
		})
	})
}

func TestPostgresRefundBucketsRejectsMalformedFacts(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(*fixture, pgx.Tx) error
	}{
		{"missing hold pointer", func(f *fixture, tx pgx.Tx) error {
			o := refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "approved", 40)
			refundBucketHold(t, f, tx, o, 2)
			return nil
		}},
		{"wrong delta", func(f *fixture, tx pgx.Tx) error {
			o := refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "approved", 40)
			hold := refundBucketWrite(t, f, tx, o, "refund_hold", 2, map[string]int64{"AVAILABLE": -40, "REFUND_HELD": 40}, map[string]int64{"available": -39, "refundHeld": 40})
			refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
			return nil
		}},
		{"duplicate order kind", func(f *fixture, tx pgx.Tx) error {
			o := refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "approved", 40)
			refundBucketSetPointer(t, f, tx, o, "hold_journal_id", refundBucketHold(t, f, tx, o, 2))
			refundBucketHold(t, f, tx, o, 3)
			return nil
		}},
		{"orphan order", func(f *fixture, tx pgx.Tx) error {
			refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "approved", 40)
			return nil
		}},
		{"orphan journal", func(f *fixture, tx pgx.Tx) error {
			_, err := post(f.ctx, tx, f.tenant, uuid.NewString(), "cash_refund_hold", uuid.NewString(), f.approver, map[string]int64{"AVAILABLE": -1, "REFUND_HELD": 1})
			return err
		}},
		{"extra unrelated lines", func(f *fixture, tx pgx.Tx) error {
			o := refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "approved", 40)
			hold := refundBucketHold(t, f, tx, o, 2)
			refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
			accounts, err := lockAccounts(f.ctx, tx, f.tenant, "HELD", "REVENUE")
			if err != nil {
				return err
			}
			if _, err = tx.Exec(f.ctx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,1),($1,$3,-1)`, hold, accounts[0].id, accounts[1].id); err != nil {
				return err
			}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fundingFixture(t)
			refundBucketCommitRejected(t, f, func(tx pgx.Tx) error { return tc.write(f, tx) })
		})
	}
}

func TestPostgresRefundBucketsLegacyDeltaAndOperationIdentity(t *testing.T) {
	f := fundingFixture(t)
	lot := refundBucketOnlyLot(t, f)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	reservation, request := uuid.NewString(), uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming) VALUES($1,$2,$3,$4,$5,$6,'policy',$7,1,clock_timestamp()+interval '1 minute',$8,false)`, request, f.tenant, f.key, f.user, f.model, fixtureProfileVersion, fixtureRequestHMAC, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,status,deadline) VALUES($1,$2,$3,1,'reserved',clock_timestamp()+interval '1 minute')`, reservation, f.tenant, request); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_reservation_lots(tenant_id,reservation_id,request_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,1,1)`, f.tenant, reservation, request, lot); err != nil {
		t.Fatal(err)
	}
	journal, err := post(f.ctx, tx, f.tenant, reservation, "reserve", request, f.operator, map[string]int64{"AVAILABLE": 1, "REVENUE": -1})
	if err != nil {
		t.Fatal(err)
	}
	event := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed,delta_refund_held,delta_retired) VALUES($1,$2,$3,$4,$5,'reserve',2,-1,1,0,1,0)`, event, f.tenant, lot, reservation, journal); err != nil {
		t.Fatal(err)
	}
	var valid bool
	if err = tx.QueryRow(f.ctx, `SELECT wallet_event_valid($1)`, event).Scan(&valid); err != nil || valid {
		t.Fatalf("legacy reserve accepted nonzero refund delta: valid=%t err=%v", valid, err)
	}
}

func TestPostgresRefundBucketsRequestedAndIdentity(t *testing.T) {
	f := fundingFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	o := refundBucketOrderTx(t, f, tx, refundBucketOnlyLot(t, f), "requested", 0)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE cash_refund_orders SET business_ref='rewrite' WHERE id=$1`, o.id); err == nil {
		t.Fatal("cash refund business identity was mutable")
	}
	if _, err = f.pool.Exec(f.ctx, `DELETE FROM cash_refund_orders WHERE id=$1`, o.id); err == nil {
		t.Fatal("cash refund order was deletable")
	}
	var state string
	if err = f.pool.QueryRow(f.ctx, `SELECT state FROM cash_refund_orders WHERE id=$1`, o.id).Scan(&state); err != nil || state != "requested" {
		t.Fatalf("requested order changed after rejected mutations: %s %v", state, err)
	}
	refundBucketBalanced(t, f)
}

func TestPostgresRefundBucketsRejectsReusedOperation(t *testing.T) {
	f := fundingFixture(t)
	lot := refundBucketOnlyLot(t, f)
	refundBucketCommitRejected(t, f, func(tx pgx.Tx) error {
		o := refundBucketOrderTx(t, f, tx, lot, "completed", 40)
		operation := uuid.NewString()
		hold, err := post(f.ctx, tx, f.tenant, operation, "cash_refund_hold", o.id, f.approver, map[string]int64{"AVAILABLE": -40, "REFUND_HELD": 40})
		if err != nil {
			return err
		}
		refundBucketSetPointer(t, f, tx, o, "hold_journal_id", hold)
		_, err = post(f.ctx, tx, f.tenant, operation, "cash_refund_retire", o.id, f.approver, map[string]int64{"REFUND_HELD": -40, "FUNDING_CLEARING": 40})
		return err
	})
}
