package billing

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/reporting"
)

// Reuse the actual APP/worker/projector LOGIN fixture without starting an HTTP
// listener or payment network. Its original tenant retains the unrelated 1000
// seed; the separate target tenant must have exactly the four sources below.
func TestPostgresWalletSourcesSummaryFourSourcesAndOpeningProjection(t *testing.T) {
	x := sandboxFundingTest(t)
	seedTenant := x.tenant
	f := *x.fixture
	f.tenant = uuid.NewString()
	x.fixture = &f
	x.exec(t, f.pool, `INSERT INTO tenants(id,name) VALUES($1,'four source summary')`, f.tenant)
	x.exec(t, f.pool, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user)
	opening, err := f.store.OpenWallet(f.ctx, openingInput(&f))
	if err != nil || opening.Receipt.SourceKind != FundingWalletOpening || opening.Receipt.Amount != 157 {
		t.Fatal("real noncash opening for source summary", opening, err)
	}
	f.store = New(x.app)
	v := voucherTestService(t, &f)
	reader := VoucherReader{ActorID: f.user, TenantID: f.tenant}
	initial, err := v.GetFundingSummary(f.ctx, reader)
	if err != nil || initial.TotalAmount != "157" || initial.WalletOpeningAmount != "157" || initial.ManualCreditAmount != "0" || initial.VoucherGrantAmount != "0" || initial.SandboxCaptureAmount != "0" {
		t.Fatal("opening must be a complete, separate summary classification", initial, err)
	}
	credit := fundingApprove(t, &f, fundingCredit(t, &f, f.tenant, 31))
	issued := voucherTestCreate(t, &f, v, 1, 17, true)
	redemption, err := v.RedeemVoucher(f.ctx, voucherTestRedeem(&f, issued.Codes[0].Code))
	if err != nil || redemption.Status != "accepted" {
		t.Fatal("real voucher source for summary", redemption, err)
	}
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	captured, err := New(x.worker).ApplyFundingTx(f.ctx, tx, FundingSourceRef{Kind: FundingSandboxCapture, ID: capture}, FundingScope{TenantID: f.tenant, ActorID: sandboxFundingActor})
	if err != nil || !captured.Applied || captured.Receipt.Amount != 100 {
		t.Fatal("actual worker capture source for summary", captured, err)
	}
	x.finish(t, tx, capture, evidence)

	summary, err := v.GetFundingSummary(f.ctx, reader)
	if err != nil || summary.Currency != "COIN" || summary.TotalAmount != "305" || summary.ManualCreditAmount != "31" || summary.VoucherGrantAmount != "17" || summary.SandboxCaptureAmount != "100" || summary.WalletOpeningAmount != "157" || summary.AsOf.IsZero() {
		t.Fatal("four source summary", summary, err)
	}
	classified := new(big.Int)
	for _, amount := range []string{summary.ManualCreditAmount, summary.VoucherGrantAmount, summary.SandboxCaptureAmount, summary.WalletOpeningAmount} {
		n, ok := new(big.Int).SetString(amount, 10)
		if !ok {
			t.Fatal("summary classification is not exact decimal")
		}
		classified.Add(classified, n)
	}
	if classified.String() != summary.TotalAmount {
		t.Fatal("summary total includes an unclassified funding amount", summary)
	}
	wire, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(wire, &fields); err != nil || len(fields) != 9 || string(fields["wallet_opening_amount"]) != `"157"` || string(fields["manual_bonus_amount"]) != `"0"` || string(fields["manual_unknown_amount"]) != `"0"` {
		t.Fatal("current summary DTO did not expose the fourth exact amount", string(wire), err)
	}
	seedSummary, err := v.GetFundingSummary(f.ctx, VoucherReader{ActorID: f.user, TenantID: seedTenant})
	if err != nil || seedSummary.TotalAmount != "1000" || seedSummary.ManualCreditAmount != "1000" || seedSummary.WalletOpeningAmount != "0" {
		t.Fatal("four source summary crossed tenant scope", seedSummary, err)
	}

	expected := map[FundingSourceKind]struct {
		source string
		amount Money
	}{
		FundingManualCredit:   {credit.ID, 31},
		FundingVoucherGrant:   {issued.Codes[0].VoucherID, 17},
		FundingSandboxCapture: {capture, 100},
		FundingWalletOpening:  {opening.OpeningID, 157},
	}
	all, err := v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{})
	if err != nil || len(all.Data) != 4 || all.NextCursor != nil {
		t.Fatal("current unfiltered receipt list omitted a source", all, err)
	}
	seen := map[FundingSourceKind]bool{}
	for _, receipt := range all.Data {
		want, ok := expected[receipt.SourceKind]
		if !ok || seen[receipt.SourceKind] || receipt.TenantID != f.tenant || receipt.SourceID != want.source || receipt.Amount != want.amount || receipt.Currency != "COIN" || receipt.JournalID == "" {
			t.Fatal("current receipt list has a wrong source identity or amount", receipt)
		}
		seen[receipt.SourceKind] = true
	}
	for kind, want := range expected {
		page, err := v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: kind})
		if err != nil || len(page.Data) != 1 || page.NextCursor != nil || page.Data[0].SourceKind != kind || page.Data[0].SourceID != want.source || page.Data[0].Amount != want.amount {
			t.Fatal("current source filter failed", kind, page, err)
		}
	}
	if _, err = v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingSourceKind("cash")}); err == nil {
		t.Fatal("unrecognized fifth funding source was accepted")
	}

	// Opening is noncash funding and emits one ordinary credited event. The
	// original projection may count it as wallet funding, never as a sale.
	var count int
	var amount, kind string
	if err = x.app.QueryRow(f.ctx, `SELECT count(*),min(o.sanitized_payload->>'amount'),min(j.kind)
 FROM projection_outbox o JOIN journals j ON j.operation_id=o.operation_id AND j.tenant_id=o.tenant_id
 WHERE o.tenant_id=$1 AND o.operation_id=$2 AND o.kind='credited'`, f.tenant, opening.OpeningID).Scan(&count, &amount, &kind); err != nil || count != 1 || amount != "157" || kind != "wallet_opening" {
		t.Fatal("opening credited event is missing, duplicated, or misclassified", count, amount, kind, err)
	}
	if count, err = New(x.projector).Project(f.ctx, ProjectInput{Limit: 200}); err != nil || count != 5 {
		t.Fatal("actual projector omitted or duplicated the opening credited event", count, err)
	}
	if count, err = New(x.projector).Project(f.ctx, ProjectInput{Limit: 200}); err != nil || count != 0 {
		t.Fatal("credited projection replay changed totals", count, err)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "305" || report.Charged != "0" || report.Refunded != "0" || report.Released != "0" || report.Requests != 0 {
		t.Fatal("noncash opening became a sale or was omitted from wallet funding", report, err)
	}
	now := time.Now().UTC()
	operating, err := reporting.Read(f.ctx, x.app, reporting.Filter{From: now.Add(-time.Hour), To: now.Add(time.Hour), TenantID: f.tenant})
	if err != nil || operating.Summary.RequestCount != 0 || operating.Summary.Charged != "0" || operating.Summary.NetRevenue != "0" || operating.Summary.KnownProviderCost != "0" || operating.Summary.GrossMargin == nil || *operating.Summary.GrossMargin != "0" {
		t.Fatal("opening or other funding entered request sales income", operating.Summary, err)
	}
	f.wallet(t, 305, 0)
	walletSourcesAssertBalanced(t, &f, f.tenant)
	fundingCheck(t, &f, f.tenant)
}
