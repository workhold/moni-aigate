package billing

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func cashRefundSingleLotTenant(t *testing.T, f *fixture, amount Money) string {
	t.Helper()
	f.tenant = accountBatchTenant(t, f)
	f.key = uuid.NewString()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, []any{f.tenant, f.user}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{f.tenant, f.model}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'cash-refund','cash-key',$4,$5,1000000000000)`, []any{f.key, f.tenant, f.user, f.key, []string{f.model}}},
	} {
		if _, err := f.pool.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	f.fund(t, amount)
	return refundBucketOnlyLot(t, f)
}

func TestPostgresCashRefundEligibilityAndUSD(t *testing.T) {
	t.Run("approval rereads available after reserve and release", func(t *testing.T) {
		f := fundingFixture(t)
		lot := cashRefundSingleLotTenant(t, f, 120)
		order := createCashRefundForTest(t, f, lot, "cash-refund:reserve-first")
		reservation := f.reserve(t)
		if reservation.Amount != 120 {
			t.Fatalf("reservation amount=%d", reservation.Amount)
		}
		if _, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver)); err == nil {
			t.Fatal("approval ignored a fully reserved source")
		}
		current, err := f.store.CashRefund(f.ctx, order.TenantID, order.ID)
		if err != nil || current.State != "requested" || current.HoldJournalID != "" {
			t.Fatalf("failed approval left effects: %+v %v", current, err)
		}
		if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: reservation.RequestID, Reason: "no_delivery"}); err != nil {
			t.Fatal(err)
		}
		approved := approveCashRefundForTest(t, f, order)
		if approved.CoinAmount == nil || *approved.CoinAmount != 120 {
			t.Fatalf("approval did not reread released availability: %+v", approved)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("noncash bases are rejected", func(t *testing.T) {
		f := fundingFixture(t)
		grantTenant := accountBatchTenant(t, f)
		grantIn := creditInputForTest(grantTenant, f.operator, "cash-refund-ineligible-grant", 0, 50, "grant", 0)
		grantIn.FiatCurrency = ""
		credit, err := f.store.CreateCredit(f.ctx, grantIn)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: grantTenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
			t.Fatal(err)
		}
		var lot string
		if err = f.pool.QueryRow(f.ctx, `SELECT id FROM funding_lots WHERE tenant_id=$1 AND source_kind='manual_bonus'`, grantTenant).Scan(&lot); err != nil {
			t.Fatal(err)
		}
		oldTenant := f.tenant
		f.tenant = grantTenant
		if _, err = f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString(), TenantID: grantTenant, ActorID: f.operator, LotID: lot, BusinessRef: "cash-refund:grant-ineligible", Reason: "must reject"}); err == nil {
			t.Fatal("grant source was accepted for cash refund")
		}
		f.tenant = oldTenant
	})
	t.Run("explicit USD version preserves discounted original ratio", func(t *testing.T) {
		f := fundingFixture(t)
		in := creditInputForTest(f.tenant, f.operator, "cash-refund:usd-source", 100, 0, "paid", 90)
		in.FiatCurrency = "USD"
		credit, err := f.store.CreateCredit(f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver}); err == nil {
			t.Fatal("USD credit succeeded without a coin-rate version")
		}
		insertCoinRateFixture(t, f, uuid.NewString(), "USD", 7, 10)
		if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
			t.Fatal(err)
		}
		var lot string
		if err = f.pool.QueryRow(f.ctx, `SELECT id FROM funding_lots WHERE source_kind='manual_credit' AND source_id=$1`, credit.ID).Scan(&lot); err != nil {
			t.Fatal(err)
		}
		order := createCashRefundForTest(t, f, lot, "cash-refund:usd-discount")
		if order.PreviewFiatAmount == nil || *order.PreviewFiatAmount != 90 || order.PreviewFiatCurrency != "USD" {
			t.Fatalf("USD preview mismatch: %+v", order)
		}
		approved := approveCashRefundForTest(t, f, order)
		if approved.FiatAmount == nil || *approved.FiatAmount != 90 || approved.FiatCurrency != "USD" || approved.BuyRateVersionID != approved.RefundRateVersionID {
			t.Fatalf("USD refund mismatch: %+v", approved)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("missing current rate helper fails closed", func(t *testing.T) {
		f := fundingFixture(t)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		if _, err = coinRateAtTime(f.ctx, tx, "USD", time.Now().UTC()); err == nil {
			t.Fatal("missing USD rate did not fail closed")
		} else {
			assertCode(t, err, "coin_rate_unavailable")
		}
	})
}

func TestPostgresCashRefundConcurrentCreateApproveAndComplete(t *testing.T) {
	f := fundingFixture(t)
	lot := refundBucketOnlyLot(t, f)
	type createResult struct {
		order CashRefundOrder
		err   error
	}
	results := make(chan createResult, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			key := uuid.NewString()
			order, err := f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, ActorID: f.operator, LotID: lot, BusinessRef: "cash-refund:concurrent:" + uuid.NewString(), Reason: "one open per lot"})
			results <- createResult{order, err}
		}(i)
	}
	var order CashRefundOrder
	var successes, conflicts int
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.err == nil {
				successes++
				order = result.order
			} else {
				conflicts++
				assertCode(t, result.err, "cash_refund_open_exists")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent creates did not finish")
		}
	}
	if successes != 1 || conflicts != 1 || order.ID == "" {
		t.Fatalf("one-open race successes=%d conflicts=%d order=%+v", successes, conflicts, order)
	}
	var orders int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM cash_refund_orders WHERE lot_id=$1`, lot).Scan(&orders); err != nil || orders != 1 {
		t.Fatalf("concurrent creates left %d orders %v", orders, err)
	}

	approved, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var events int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("concurrent approvals wrote %d holds %v", events, err)
	}
	execute := cashRefundActionForTest(approved, f.operator)
	execute.TransferOrderRef = "transfer:concurrent"
	executed, err := f.store.ExecuteCashRefund(f.ctx, execute)
	if err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			action := cashRefundActionForTest(executed, f.approver)
			action.TransferProofRef = "transfer:concurrent-proof"
			paid := FiatMoney(1000)
			action.PaidFiatAmount = &paid
			_, err := f.store.CompleteCashRefund(f.ctx, action)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("concurrent completions wrote %d events %v", events, err)
	}
	refundBucketBalanced(t, f)
}

func TestPostgresCashRefundReserveSettleAndConsumptionRefundRaces(t *testing.T) {
	t.Run("reserve versus approval", func(t *testing.T) {
		f := fundingFixture(t)
		lot := cashRefundSingleLotTenant(t, f, 120)
		order := createCashRefundForTest(t, f, lot, "cash-refund:reserve-race")
		errs := make(chan error, 2)
		go func() { _, err := f.store.Reserve(f.ctx, f.input()); errs <- err }()
		go func() {
			_, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
			errs <- err
		}()
		var successes int
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				if err == nil {
					successes++
				}
			case <-time.After(10 * time.Second):
				t.Fatal("reserve/approval race deadlocked")
			}
		}
		if successes != 1 {
			t.Fatalf("reserve and full cash approval both succeeded or both failed: %d", successes)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("settlement versus approval", func(t *testing.T) {
		f := fundingFixture(t)
		lot := cashRefundSingleLotTenant(t, f, 1000)
		reservation := f.reserve(t)
		f.final(t, reservation, confirmed(), "completed", "done", "none", false)
		order := createCashRefundForTest(t, f, lot, "cash-refund:settle-race")
		errs := make(chan error, 2)
		go func() {
			_, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: reservation.RequestID})
			errs <- err
		}()
		go func() {
			_, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
			errs <- err
		}()
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("settlement/approval race deadlocked")
			}
		}
		approved, err := f.store.CashRefund(f.ctx, order.TenantID, order.ID)
		if err != nil || approved.State != "approved" || approved.CoinAmount == nil || *approved.CoinAmount != 880 && *approved.CoinAmount != 985 {
			t.Fatalf("settlement/approval result mismatch: %+v %v", approved, err)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("consumption refund versus approval", func(t *testing.T) {
		f := fundingFixture(t)
		lot := cashRefundSingleLotTenant(t, f, 1000)
		reservation := f.reserve(t)
		f.final(t, reservation, confirmed(), "completed", "done", "none", false)
		settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: reservation.RequestID})
		if err != nil {
			t.Fatal(err)
		}
		order := createCashRefundForTest(t, f, lot, "cash-refund:consumption-race")
		ref := uuid.NewString()
		consumption, err := f.store.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: ref, Reason: "race with cash approval", Amount: 10, IdempotencyKey: ref, RequestHMAC: ref})
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		go func() {
			_, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
			errs <- err
		}()
		go func() {
			_, err := f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: consumption.ID, ActorID: f.approver})
			errs <- err
		}()
		for i := 0; i < 2; i++ {
			select {
			case err := <-errs:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("consumption-refund/approval race deadlocked")
			}
		}
		approved, err := f.store.CashRefund(f.ctx, order.TenantID, order.ID)
		if err != nil || approved.State != "approved" || approved.CoinAmount == nil || *approved.CoinAmount != 985 && *approved.CoinAmount != 995 {
			t.Fatalf("consumption-refund/approval result mismatch: %+v %v", approved, err)
		}
		var available, consumed, refundHeld int64
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,consumed_amount,refund_held_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &consumed, &refundHeld); err != nil || available < 0 || consumed != 5 || refundHeld != int64(*approved.CoinAmount) {
			t.Fatalf("consumption/cash buckets=%d/%d/%d approved=%d err=%v", available, consumed, refundHeld, *approved.CoinAmount, err)
		}
		refundBucketBalanced(t, f)
	})
}

func TestPostgresCashRefundRejectsWrongFrozenAmount(t *testing.T) {
	f := fundingFixture(t)
	order := createCashRefundForTest(t, f, refundBucketOnlyLot(t, f), "cash-refund:wrong-frozen")
	approved := approveCashRefundForTest(t, f, order)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `UPDATE cash_refund_orders SET fiat_amount=fiat_amount+1 WHERE id=$1`, approved.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("direct SQL changed the frozen fiat amount")
	}
	var valid bool
	if err = f.pool.QueryRow(f.ctx, `SELECT wallet_cash_refund_order_valid($1)`, order.ID).Scan(&valid); err != nil || !valid {
		t.Fatalf("rejected direct mutation changed the order: %v valid=%v", err, valid)
	}
}
