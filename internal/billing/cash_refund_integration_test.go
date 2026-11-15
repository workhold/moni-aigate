package billing

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

func cashRefundActionForTest(order CashRefundOrder, actor string) CashRefundActionInput {
	key := uuid.NewString()
	return CashRefundActionInput{IdempotencyKey: key, RequestHMAC: key, TenantID: order.TenantID, OrderID: order.ID, ActorID: actor, Reason: "cash refund release evidence"}
}

func createCashRefundForTest(t *testing.T, f *fixture, lot, business string) CashRefundOrder {
	t.Helper()
	key := uuid.NewString()
	order, err := f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, ActorID: f.operator, LotID: lot, BusinessRef: business, Reason: "customer requested cash refund"})
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func approveCashRefundForTest(t *testing.T, f *fixture, order CashRefundOrder) CashRefundOrder {
	t.Helper()
	approved, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.approver))
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func TestCashRefundFiatBigIntVectors(t *testing.T) {
	cny := cashRefundRate{id: "buy", currency: "CNY", num: 1, den: 1}
	for _, tc := range []struct {
		name         string
		k, f, c      int64
		buy, current cashRefundRate
		want         FiatMoney
	}{
		{"full discounted payment", 100, 90, 100, cny, cny, 90},
		{"partial available is independently floored", 40, 90, 100, cny, cny, 36},
		{"round down never overpays", 1, 90, 100, cny, cny, 0},
		{"lower current rate", 100, 90, 100, cny, cashRefundRate{id: "cur", currency: "CNY", num: 1, den: 2}, 45},
		{"higher current rate is capped by original payment", 100, 90, 100, cny, cashRefundRate{id: "cur", currency: "CNY", num: 2, den: 1}, 90},
		{"five int64 factors do not overflow", math.MaxInt64, math.MaxInt64, math.MaxInt64, cny, cashRefundRate{id: "cur", currency: "CNY", num: 1, den: math.MaxInt64}, 1},
		{"max int64 base remains exact", math.MaxInt64, math.MaxInt64, math.MaxInt64, cny, cashRefundRate{id: "cur", currency: "CNY", num: math.MaxInt64, den: 1}, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := calculateCashRefundFiat(tc.k, tc.f, tc.c, tc.buy, tc.current)
			if err != nil || got != tc.want {
				t.Fatalf("amount=%d want=%d err=%v", got, tc.want, err)
			}
		})
	}
	for _, tc := range []struct {
		k, f, c      int64
		buy, current cashRefundRate
	}{
		{101, 90, 100, cny, cny},
		{100, 90, 100, cny, cashRefundRate{id: "cur", currency: "USD", num: 1, den: 1}},
		{100, 90, 100, cny, cashRefundRate{id: "cur", currency: "CNY", num: 0, den: 1}},
	} {
		if _, err := calculateCashRefundFiat(tc.k, tc.f, tc.c, tc.buy, tc.current); err == nil {
			t.Fatal("invalid cash-refund vector was accepted")
		}
	}
}

func TestPostgresCashRefundFiatSQLMatchesBigInt(t *testing.T) {
	f := fundingFixture(t)
	vectors := []struct {
		k, f, c, bn, bd, cn, cd int64
	}{
		{100, 90, 100, 1, 1, 1, 1},
		{100, 90, 100, 1, 1, 1, 2},
		{100, 90, 100, 1, 1, 2, 1},
		{math.MaxInt64, math.MaxInt64, math.MaxInt64, 1, 1, 1, math.MaxInt64},
	}
	for _, v := range vectors {
		buy := cashRefundRate{id: "buy", currency: "CNY", num: v.bn, den: v.bd}
		current := cashRefundRate{id: "cur", currency: "CNY", num: v.cn, den: v.cd}
		want, err := calculateCashRefundFiat(v.k, v.f, v.c, buy, current)
		if err != nil {
			t.Fatal(err)
		}
		var got FiatMoney
		if err = f.pool.QueryRow(f.ctx, `SELECT wallet_cash_refund_fiat_amount($1,$2,$3,$4,$5,$6,$7)`, v.k, v.f, v.c, v.bn, v.bd, v.cn, v.cd).Scan(&got); err != nil || got != want {
			t.Fatalf("SQL amount=%d Go=%d err=%v", got, want, err)
		}
	}
}

func TestPostgresCashRefundLifecycleAndReplay(t *testing.T) {
	f := fundingFixture(t)
	lot := refundBucketOnlyLot(t, f)
	order := createCashRefundForTest(t, f, lot, "cash-refund:lifecycle")
	if order.State != "requested" || order.CoinAmount != nil || order.ApprovedAt != nil || order.PreviewCoinAmount == nil || *order.PreviewCoinAmount != 1000 || order.PreviewFiatAmount == nil || *order.PreviewFiatAmount != 1000 || order.PreviewFiatCurrency != "CNY" {
		t.Fatalf("requested preview mismatch: %+v", order)
	}
	var events int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("requested order has wallet effects: %d %v", events, err)
	}
	approved := approveCashRefundForTest(t, f, order)
	if approved.State != "approved" || approved.CoinAmount == nil || *approved.CoinAmount != 1000 || approved.FiatAmount == nil || *approved.FiatAmount != 1000 || approved.ApprovedAt == nil || approved.HoldJournalID == "" || approved.BuyRateVersionID == "" || approved.RefundRateVersionID == "" {
		t.Fatalf("approval mismatch: %+v", approved)
	}
	var available, held, consumed, refundHeld, retired int64
	if err := f.pool.QueryRow(f.ctx, `SELECT available_amount,held_amount,consumed_amount,refund_held_amount,retired_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &held, &consumed, &refundHeld, &retired); err != nil || available != 0 || held != 0 || consumed != 0 || refundHeld != 1000 || retired != 0 {
		t.Fatalf("hold buckets=%d/%d/%d/%d/%d %v", available, held, consumed, refundHeld, retired, err)
	}
	replayed := approveCashRefundForTest(t, f, order)
	if replayed.HoldJournalID != approved.HoldJournalID {
		t.Fatalf("approval replay changed hold journal: %+v", replayed)
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("approval replay duplicated hold: %d %v", events, err)
	}
	execute := cashRefundActionForTest(approved, f.operator)
	execute.TransferOrderRef = "transfer:lifecycle-order"
	executed, err := f.store.ExecuteCashRefund(f.ctx, execute)
	if err != nil || executed.State != "transfer_executed" || executed.TransferExecutor != f.operator || executed.TransferOrderRef != execute.TransferOrderRef {
		t.Fatalf("execute mismatch: %+v %v", executed, err)
	}
	complete := cashRefundActionForTest(executed, f.approver)
	complete.TransferProofRef = "transfer:lifecycle-proof"
	paid := FiatMoney(1000)
	complete.PaidFiatAmount = &paid
	completed, err := f.store.CompleteCashRefund(f.ctx, complete)
	if err != nil || completed.State != "completed" || completed.RetireJournalID == "" || completed.PaidFiatAmount == nil || *completed.PaidFiatAmount != 1000 {
		t.Fatalf("complete mismatch: %+v %v", completed, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,held_amount,consumed_amount,refund_held_amount,retired_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &held, &consumed, &refundHeld, &retired); err != nil || available != 0 || held != 0 || consumed != 0 || refundHeld != 0 || retired != 1000 {
		t.Fatalf("retire buckets=%d/%d/%d/%d/%d %v", available, held, consumed, refundHeld, retired, err)
	}
	completedReplay, err := f.store.CompleteCashRefund(f.ctx, complete)
	if err != nil || completedReplay.RetireJournalID != completed.RetireJournalID {
		t.Fatalf("complete replay mismatch: %+v %v", completedReplay, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 2 {
		t.Fatalf("completion replay duplicated events: %d %v", events, err)
	}
	byBusiness, err := f.store.CashRefundByBusinessRef(f.ctx, order.BusinessRef)
	if err != nil || byBusiness.State != "completed" || byBusiness.RetireJournalID != completed.RetireJournalID {
		t.Fatalf("business readback mismatch: %+v %v", byBusiness, err)
	}
	var valid bool
	if err = f.pool.QueryRow(f.ctx, `SELECT wallet_cash_refund_order_valid($1)`, order.ID).Scan(&valid); err != nil || !valid {
		t.Fatalf("completed order invalid: %v valid=%v", err, valid)
	}
	refundBucketBalanced(t, f)
}

func TestPostgresCashRefundCancelFailAndRequestedCancel(t *testing.T) {
	t.Run("approved cancel and subsequent fail", func(t *testing.T) {
		f := fundingFixture(t)
		lot := refundBucketOnlyLot(t, f)
		order := createCashRefundForTest(t, f, lot, "cash-refund:cancel")
		approved := approveCashRefundForTest(t, f, order)
		cancelled, err := f.store.CancelCashRefund(f.ctx, cashRefundActionForTest(approved, f.operator))
		if err != nil || cancelled.State != "cancelled" || cancelled.ReleaseJournalID == "" {
			t.Fatalf("cancel mismatch: %+v %v", cancelled, err)
		}
		var available, refundHeld, retired int64
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,refund_held_amount,retired_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &refundHeld, &retired); err != nil || available != 1000 || refundHeld != 0 || retired != 0 {
			t.Fatalf("cancel buckets=%d/%d/%d %v", available, refundHeld, retired, err)
		}
		second := createCashRefundForTest(t, f, lot, "cash-refund:fail-after-cancel")
		approved = approveCashRefundForTest(t, f, second)
		executed := cashRefundActionForTest(approved, f.operator)
		executed.TransferOrderRef = "transfer:failed-order"
		executedOrder, err := f.store.ExecuteCashRefund(f.ctx, executed)
		if err != nil || executedOrder.State != "transfer_executed" {
			t.Fatal(executedOrder, err)
		}
		if _, err = f.store.CancelCashRefund(f.ctx, cashRefundActionForTest(executedOrder, f.operator)); err == nil {
			t.Fatal("executed cash refund was incorrectly cancelled")
		}
		failed, err := f.store.FailCashRefund(f.ctx, cashRefundActionForTest(executedOrder, f.approver))
		if err != nil || failed.State != "failed" || failed.ReleaseJournalID == "" {
			t.Fatalf("failure release mismatch: %+v %v", failed, err)
		}
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,refund_held_amount,retired_amount FROM funding_lot_balances WHERE lot_id=$1`, lot).Scan(&available, &refundHeld, &retired); err != nil || available != 1000 || refundHeld != 0 || retired != 0 {
			t.Fatalf("failure release buckets=%d/%d/%d %v", available, refundHeld, retired, err)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("requested cancel has no wallet facts", func(t *testing.T) {
		f := fundingFixture(t)
		order := createCashRefundForTest(t, f, refundBucketOnlyLot(t, f), "cash-refund:requested-cancel")
		cancelled, err := f.store.CancelCashRefund(f.ctx, cashRefundActionForTest(order, f.operator))
		if err != nil || cancelled.State != "cancelled" || cancelled.HoldJournalID != "" || cancelled.ReleaseJournalID != "" {
			t.Fatalf("requested cancel mismatch: %+v %v", cancelled, err)
		}
		var events int
		if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE refund_order_id=$1`, order.ID).Scan(&events); err != nil || events != 0 {
			t.Fatalf("requested cancel wrote events: %d %v", events, err)
		}
		refundBucketBalanced(t, f)
	})
}

func TestPostgresCashRefundCustomerRequestAndSingleOperatorSnapshot(t *testing.T) {
	t.Run("customer request closes only at terminal", func(t *testing.T) {
		f := fundingFixture(t)
		request, err := f.store.CreateWalletRequest(f.ctx, WalletRequestInput{TenantID: f.tenant, ActorID: f.user, Kind: "cash_refund", Note: "please refund my remaining paid coins", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		key := uuid.NewString()
		order, err := f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, ActorID: f.operator, LotID: refundBucketOnlyLot(t, f), BusinessRef: "cash-refund:customer-request", Reason: "verified customer request", RequestID: request.ID})
		if err != nil || order.RequestedBy != f.user || order.RequestID != request.ID {
			t.Fatalf("customer link mismatch: %+v %v", order, err)
		}
		approved := approveCashRefundForTest(t, f, order)
		var requestState string
		if err = f.pool.QueryRow(f.ctx, `SELECT state FROM wallet_requests WHERE id=$1`, request.ID).Scan(&requestState); err != nil || requestState != "linked" {
			t.Fatalf("approval closed request too early: %s %v", requestState, err)
		}
		executed := cashRefundActionForTest(approved, f.operator)
		executed.TransferOrderRef = "transfer:customer-order"
		executedOrder, err := f.store.ExecuteCashRefund(f.ctx, executed)
		if err != nil {
			t.Fatal(err)
		}
		complete := cashRefundActionForTest(executedOrder, f.approver)
		complete.TransferProofRef = "transfer:customer-proof"
		paid := FiatMoney(1000)
		complete.PaidFiatAmount = &paid
		if _, err = f.store.CompleteCashRefund(f.ctx, complete); err != nil {
			t.Fatal(err)
		}
		if err = f.pool.QueryRow(f.ctx, `SELECT state FROM wallet_requests WHERE id=$1`, request.ID).Scan(&requestState); err != nil || requestState != "closed" {
			t.Fatalf("terminal did not close request: %s %v", requestState, err)
		}
		refundBucketBalanced(t, f)
	})
	t.Run("admin snapshot survives later switch disable", func(t *testing.T) {
		f := fundingFixture(t)
		key := uuid.NewString()
		order, err := f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, ActorID: f.admin, LotID: refundBucketOnlyLot(t, f), BusinessRef: "cash-refund:single-admin", Reason: "single operator approval"})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := f.store.ApproveCashRefund(f.ctx, cashRefundActionForTest(order, f.admin))
		if err != nil || !approved.SingleOperator || !approved.SingleOperatorException {
			t.Fatalf("single admin approval mismatch: %+v %v", approved, err)
		}
		if _, err = f.pool.Exec(f.ctx, `INSERT INTO wallet_operator_settings(id,version,single_operator,actor,reason) VALUES($1,2,false,$2,'disable after approval')`, uuid.NewString(), f.admin); err != nil {
			t.Fatal(err)
		}
		executed := cashRefundActionForTest(approved, f.admin)
		executed.TransferOrderRef = "transfer:single-admin"
		executedOrder, err := f.store.ExecuteCashRefund(f.ctx, executed)
		if err != nil || executedOrder.State != "transfer_executed" {
			t.Fatalf("snapshot did not allow later execution: %+v %v", executedOrder, err)
		}
		complete := cashRefundActionForTest(executedOrder, f.admin)
		complete.TransferProofRef = "transfer:single-proof"
		paid := FiatMoney(1000)
		complete.PaidFiatAmount = &paid
		completed, err := f.store.CompleteCashRefund(f.ctx, complete)
		if err != nil || completed.State != "completed" {
			t.Fatalf("snapshot did not allow completion: %+v %v", completed, err)
		}
		if _, err = f.store.CreateCashRefund(f.ctx, CashRefundCreateInput{IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString(), TenantID: f.tenant, ActorID: f.admin, LotID: refundBucketOnlyLot(t, f), BusinessRef: "cash-refund:admin-disabled", Reason: "must reject after disabling"}); err == nil {
			t.Fatal("admin created a new wallet order after single-operator disable")
		}
		refundBucketBalanced(t, f)
	})
}
