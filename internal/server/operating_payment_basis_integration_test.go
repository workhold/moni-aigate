package server

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/reporting"
)

func operatingCredit(t *testing.T, s *Server, tenant, actor, approver, business string, amount, bonus billing.Money, basis, currency string, fiat billing.FiatMoney) billing.CreditOrder {
	t.Helper()
	key := uuid.NewString()
	order, err := s.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: tenant, ActorID: actor, BusinessRef: business, ProofRef: "proof:report-basis", Amount: amount, BonusAmount: bonus, BasisKind: basis, FiatCurrency: currency, FiatAmount: fiat, IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: tenant, OrderID: order.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	return order
}

func operatingCashRefund(t *testing.T, f *gatewayFixture, lot string, paid billing.FiatMoney) {
	t.Helper()
	var operator, approver string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_approver' LIMIT 1`).Scan(&approver); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	order, err := f.server.Billing.CreateCashRefund(context.Background(), billing.CashRefundCreateInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, ActorID: operator, LotID: lot, BusinessRef: key, Reason: "report cash refund"})
	if err != nil {
		t.Fatal(err)
	}
	action := func(actor string) billing.CashRefundActionInput {
		key := uuid.NewString()
		return billing.CashRefundActionInput{IdempotencyKey: key, RequestHMAC: key, TenantID: f.tenant, OrderID: order.ID, ActorID: actor, Reason: "report cash release"}
	}
	if _, err = f.server.Billing.ApproveCashRefund(context.Background(), action(approver)); err != nil {
		t.Fatal(err)
	}
	execute := action(operator)
	execute.TransferOrderRef = "transfer:report"
	if _, err = f.server.Billing.ExecuteCashRefund(context.Background(), execute); err != nil {
		t.Fatal(err)
	}
	complete := action(approver)
	complete.TransferProofRef = "transfer:report-proof"
	complete.PaidFiatAmount = &paid
	if _, err = f.server.Billing.CompleteCashRefund(context.Background(), complete); err != nil {
		t.Fatal(err)
	}
}

func TestOperatingReportPaymentBasisPostgres(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	ctx := context.Background()
	operator, approver := "", ""
	if err := f.pool.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='finance_operator' LIMIT 1`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='finance_approver' LIMIT 1`).Scan(&approver); err != nil {
		t.Fatal(err)
	}
	var initialLot string
	if err := f.pool.QueryRow(ctx, `SELECT id FROM funding_lots WHERE tenant_id=$1 ORDER BY fifo_at,id LIMIT 1`, f.tenant).Scan(&initialLot); err != nil {
		t.Fatal(err)
	}
	operatingCashRefund(t, f, initialLot, 1000)
	discounted := operatingCredit(t, f.server, f.tenant, operator, approver, "report-discounted", 1000, 0, "paid", "CNY", 900)
	for i := 0; i < 3; i++ {
		response := f.request(t, ctx, false, false)
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("discounted request failed: %d", response.StatusCode)
		}
	}
	from := time.Now().Add(-time.Hour)
	to := time.Now().Add(time.Hour)
	report, err := reporting.Read(ctx, f.pool, reporting.Filter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.PaymentBasis["CNY"] != "54" || report.Summary.Charged != "60" || report.Summary.GrossMargin != nil {
		t.Fatalf("discounted payment basis mismatch: %+v", report.Summary)
	}
	var discountedLot string
	if err = f.pool.QueryRow(ctx, `SELECT id FROM funding_lots WHERE source_kind='manual_credit' AND source_id=$1`, discounted.ID).Scan(&discountedLot); err != nil {
		t.Fatal(err)
	}
	operatingCashRefund(t, f, discountedLot, 846)
	usdRate := uuid.NewString()
	if _, err = f.pool.Exec(ctx, `WITH n AS (SELECT clock_timestamp() AS at) INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver,single_operator,created_at) SELECT $1,'USD',7,10,n.at,'report explicit USD',$2,$3,false,n.at FROM n`, usdRate, operator, approver); err != nil {
		t.Fatal(err)
	}
	usd := operatingCredit(t, f.server, f.tenant, operator, approver, "report-usd", 1000, 0, "paid", "USD", 900)
	response := f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("USD request failed: %d", response.StatusCode)
	}
	var usdLot string
	if err = f.pool.QueryRow(ctx, `SELECT id FROM funding_lots WHERE source_kind='manual_credit' AND source_id=$1`, usd.ID).Scan(&usdLot); err != nil {
		t.Fatal(err)
	}
	operatingCashRefund(t, f, usdLot, 882)
	report, err = reporting.Read(ctx, f.pool, reporting.Filter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.PaymentBasis["CNY"] != "54" || report.Summary.PaymentBasis["USD"] != "18" || report.Summary.CashRefunded["CNY"] != "1846" || report.Summary.CashRefunded["USD"] != "882" || report.Summary.GrossMargin != nil {
		t.Fatalf("multi-currency or cash-refund columns mismatch: %+v", report.Summary)
	}
	otherTenant, err := reporting.Read(ctx, f.pool, reporting.Filter{From: from, To: to, TenantID: uuid.NewString()})
	if err != nil || len(otherTenant.Summary.CashRefunded) != 0 || len(otherTenant.Summary.PaymentBasis) != 0 || otherTenant.Summary.RequestCount != 0 {
		t.Fatalf("cash refunds crossed the tenant filter: %+v err=%v", otherTenant.Summary, err)
	}
	operatingCredit(t, f.server, f.tenant, operator, approver, "report-unknown", 1000, 0, "unknown", "", 0)
	response = f.request(t, ctx, false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("unknown-basis request failed: %d", response.StatusCode)
	}
	report, err = reporting.Read(ctx, f.pool, reporting.Filter{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.GrossMargin != nil {
		t.Fatalf("unknown basis produced a margin: %+v", report.Summary)
	}
	if report.Summary.PaymentBasis["CNY"] != "54" || report.Summary.PaymentBasis["USD"] != "18" || report.Summary.CashRefunded["CNY"] != "1846" || report.Summary.CashRefunded["USD"] != "882" {
		t.Fatalf("retirement changed original allocation facts: %+v", report.Summary)
	}
}
