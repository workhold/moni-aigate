package billing

import (
	"testing"

	"github.com/google/uuid"
)

// The fixture profile pins InputUpperBound to its published input bound, so
// every reservation below is 100 input tokens plus 20 output tokens at the
// fixture's 1:1 retail price.
func paymentBasisReserve(t *testing.T, f *fixture) Reservation {
	t.Helper()
	r, err := f.store.Reserve(f.ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func paymentBasisFund(t *testing.T, f *fixture, amount, bonus Money, basis, fiatCurrency string, fiat FiatMoney) {
	t.Helper()
	key := uuid.NewString()
	o, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: key, ProofRef: "receipt:basis", Amount: amount, BonusAmount: bonus, BasisKind: basis, FiatCurrency: fiatCurrency, FiatAmount: fiat, TransferRef: "transfer:basis", FxNote: "manual settlement", IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
}

func paymentBasisCheck(t *testing.T, f *fixture, reservation Reservation, coin int64, wantBasis int64, wantCovered bool) {
	t.Helper()
	basis, covered, err := f.store.ReservationCNYBasis(f.ctx, f.tenant, reservation.ID, coin)
	if err != nil {
		t.Fatal(err)
	}
	if basis != wantBasis || covered != wantCovered {
		t.Fatalf("reservation %s basis=%d covered=%v, want %d/%v", reservation.ID, basis, covered, wantBasis, wantCovered)
	}
}

// The basis_kind full set is fixed by funding_receipts_basis_check:
// paid (manual_credit/sandbox_capture), grant (manual_bonus/voucher_grant),
// opening (wallet_opening) and unknown (manual_unknown). Paid CNY contributes
// its floored fiat share, grant and opening contribute zero without erasing
// proven paid parts, unknown and USD fail the whole basis closed.
func TestPostgresReservationCNYBasisPaidGrantUnknownUSDMix(t *testing.T) {
	f := walletSourcesFixture(t)
	paymentBasisFund(t, f, 140, 0, "paid", "CNY", 70)
	// Partial paid allocation: floor(120*70/140) = 60.
	r1 := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r1.RequestID)
	paymentBasisCheck(t, f, r1, 120, 60, true)

	// FIFO keeps the paid remainder ahead of the grant lot; the grant coins add
	// zero without erasing the proven paid part: floor(20*70/140) = 10.
	paymentBasisFund(t, f, 0, 240, "grant", "", 0)
	r2 := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r2.RequestID)
	paymentBasisCheck(t, f, r2, 120, 10, true)
	var paidSum, grantSum int64
	if err := f.pool.QueryRow(f.ctx, `SELECT coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_credit'),0),coalesce(sum(a.amount) FILTER(WHERE l.source_kind='manual_bonus'),0) FROM funding_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id WHERE a.reservation_id=$1`, r2.ID).Scan(&paidSum, &grantSum); err != nil || paidSum != 20 || grantSum != 100 {
		t.Fatal(paidSum, grantSum, err)
	}

	// Grant-only remainder: a positive reservation with no proven cash.
	r3 := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r3.RequestID)
	paymentBasisCheck(t, f, r3, 120, 0, false)

	// Unknown basis fails the whole reservation closed.
	paymentBasisFund(t, f, 120, 0, "unknown", "", 0)
	r4 := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r4.RequestID)
	paymentBasisCheck(t, f, r4, 120, 0, false)

	// USD is never converted by the routing gate without an FX policy.
	insertCoinRateFixture(t, f, uuid.NewString(), "USD", 8, 10)
	paymentBasisFund(t, f, 120, 0, "paid", "USD", 120)
	r5 := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r5.RequestID)
	paymentBasisCheck(t, f, r5, 120, 0, false)
}

func TestPostgresReservationCNYBasisOpeningOnlyFailsClosed(t *testing.T) {
	f := walletSourcesFixture(t)
	out, err := f.store.OpenWallet(f.ctx, WalletOpeningInput{TenantID: f.tenant, ActorID: f.admin, ExternalRef: "opening:" + uuid.NewString(), Amount: 240, MaintenanceMode: true})
	if err != nil || out.Receipt.SourceKind != FundingWalletOpening {
		t.Fatal(out, err)
	}
	r := paymentBasisReserve(t, f)
	defer f.store.EndRequest(r.RequestID)
	paymentBasisCheck(t, f, r, 120, 0, false)
}
