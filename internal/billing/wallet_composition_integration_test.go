package billing

import "testing"

func TestPostgresWalletCompositionIncludesAllSourcesAndRefundHold(t *testing.T) {
	f := fundingFixture(t)
	composition, err := f.store.WalletComposition(f.ctx, Query{TenantID: f.tenant})
	if err != nil || composition.Available != 1000 || composition.PaidAvailable != 1000 || composition.GrantAvailable != 0 || composition.OtherAvailable != 0 || composition.Held != 0 || composition.RefundHeld != 0 {
		t.Fatalf("initial composition mismatch: %+v %v", composition, err)
	}
	credit, err := f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.operator, "composition-paid-bonus", 100, 20, "paid", 90))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	grantInput := creditInputForTest(f.tenant, f.operator, "composition-grant", 0, 50, "grant", 0)
	grantInput.FiatCurrency = ""
	grant, err := f.store.CreateCredit(f.ctx, grantInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: grant.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	unknownInput := creditInputForTest(f.tenant, f.operator, "composition-unknown", 70, 0, "unknown", 0)
	unknownInput.FiatCurrency = ""
	unknown, err := f.store.CreateCredit(f.ctx, unknownInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: unknown.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = f.store.Reserve(f.ctx, f.input()); err != nil {
			t.Fatal(err)
		}
	}
	composition, err = f.store.WalletComposition(f.ctx, Query{TenantID: f.tenant})
	if err != nil || composition.Available != 1000 || composition.PaidAvailable != 860 || composition.GrantAvailable != 70 || composition.OtherAvailable != 70 || composition.Held != 240 || composition.RefundHeld != 0 {
		t.Fatalf("composition after reserves mismatch: %+v %v", composition, err)
	}
	order := createCashRefundForTest(t, f, refundBucketOnlyLot(t, f), "composition-hold")
	approved := approveCashRefundForTest(t, f, order)
	if approved.CoinAmount == nil || *approved.CoinAmount != 760 {
		t.Fatalf("unexpected hold amount: %+v", approved)
	}
	composition, err = f.store.WalletComposition(f.ctx, Query{TenantID: f.tenant})
	if err != nil || composition.Available != 240 || composition.PaidAvailable != 100 || composition.GrantAvailable != 70 || composition.OtherAvailable != 70 || composition.Held != 240 || composition.RefundHeld != 760 || int64(composition.PaidAvailable)+int64(composition.GrantAvailable)+int64(composition.OtherAvailable) != int64(composition.Available) {
		t.Fatalf("composition after cash hold mismatch: %+v %v", composition, err)
	}
	refundBucketBalanced(t, f)
}

func TestPostgresWalletCompositionOpeningIsOther(t *testing.T) {
	f := fundingFixture(t)
	f.tenant = accountBatchTenant(t, f)
	opened, err := f.store.OpenWallet(f.ctx, openingInput(f))
	if err != nil {
		t.Fatal(err)
	}
	if opened.Receipt.Amount != 157 {
		t.Fatal("opening fixture failed")
	}
	composition, err := f.store.WalletComposition(f.ctx, Query{TenantID: f.tenant})
	if err != nil || composition.Available != 157 || composition.PaidAvailable != 0 || composition.GrantAvailable != 0 || composition.OtherAvailable != 157 || composition.Held != 0 || composition.RefundHeld != 0 {
		t.Fatalf("opening composition mismatch: %+v %v", composition, err)
	}
}
