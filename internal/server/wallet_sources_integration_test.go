package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/reporting"
)

type walletSourcesHTTPRow struct {
	ID        string  `json:"id"`
	LotID     string  `json:"lot_id"`
	Kind      string  `json:"source_kind"`
	Issued    string  `json:"issued_amount"`
	Available string  `json:"available_amount"`
	Held      string  `json:"held_amount"`
	Consumed  string  `json:"consumed_amount"`
	Reserved  string  `json:"reserved_amount"`
	Charged   *string `json:"charged_amount"`
	Released  *string `json:"released_amount"`
	Refunded  string  `json:"refunded_amount"`
}

func TestWalletSourcesHTTPOpeningReceiptsAndNoSalesRevenue(t *testing.T) {
	s, pool := newAuthFixture(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	admin, _ := authUser(t, s, pool, "admin")
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'opening HTTP')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", tenant, admin)
	opening, err := s.Billing.OpenWallet(ctx, billing.WalletOpeningInput{TenantID: tenant, ActorID: admin, ExternalRef: "opening-http-" + uuid.NewString(), Amount: 700, MaintenanceMode: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InitializeVouchers(ctx); err != nil {
		t.Fatal(err)
	}
	raw, _ := authSession(t, s, pool, admin, tenant, false, true)
	w := authHTTP(t, s, "GET", "/api/wallet/funding-summary", raw, "", "", nil)
	requireAuthStatus(t, w, 200)
	var summary map[string]any
	if err = json.Unmarshal(authData(t, w), &summary); err != nil || summary["total_amount"] != "700" || summary["wallet_opening_amount"] != "700" || summary["manual_credit_amount"] != "0" || summary["voucher_grant_amount"] != "0" || summary["sandbox_capture_amount"] != "0" {
		t.Fatal("opening missing from four-source summary", summary, err)
	}
	for _, filter := range []string{"", "?source_kind=wallet_opening"} {
		w = authHTTP(t, s, "GET", "/api/wallet/funding-receipts"+filter, raw, "", "", nil)
		requireAuthStatus(t, w, 200)
		var receipts []billing.FundingReceiptView
		if err = json.Unmarshal(authData(t, w), &receipts); err != nil || len(receipts) != 1 || receipts[0].ID != opening.Receipt.ID || receipts[0].SourceKind != billing.FundingWalletOpening || receipts[0].Amount != 700 {
			t.Fatal("opening receipt missing from current list/filter", receipts, err)
		}
	}
	w = authHTTP(t, s, "GET", "/api/wallet/funding-receipts?source_kind=unknown", raw, "", "", nil)
	requireAuthStatus(t, w, 400)
	from, to := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	w = authHTTP(t, s, "GET", "/api/platform/reports/operations?from="+from.Format(time.RFC3339)+"&to="+to.Format(time.RFC3339), raw, "", "", nil)
	requireAuthStatus(t, w, 200)
	var report reporting.Report
	if err = json.Unmarshal(authData(t, w), &report); err != nil || report.Summary.Charged != "0" || report.Summary.NetRevenue != "0" || report.Summary.RequestCount != 0 || report.Summary.AttemptCount != 0 {
		t.Fatal("noncash wallet opening became sales revenue", report.Summary, err)
	}
}

func TestWalletSourcesHTTPCommercialFlowAndAuthorization(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	response := f.request(t, context.Background(), false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	requestID := response.Header.Get("X-Request-ID")
	var owner, operator, approver, settlement string
	for role, target := range map[string]*string{"none": &owner, "finance_operator": &operator, "finance_approver": &approver} {
		if err := f.pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role=$1 LIMIT 1", role).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM settlements WHERE request_id=$1", requestID).Scan(&settlement); err != nil {
		t.Fatal(err)
	}
	ref := uuid.NewString()
	order, err := f.server.Billing.CreateRefund(context.Background(), billing.RefundInput{TenantID: f.tenant, ActorID: operator, SettlementID: settlement, BusinessRef: ref, Amount: 5, Reason: "source HTTP", IdempotencyKey: ref, RequestHMAC: ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Billing.ApproveRefund(context.Background(), billing.ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	ownerSession, _ := authSession(t, f.server, f.pool, owner, f.tenant, false, true)
	read := func(path, session string) []walletSourcesHTTPRow {
		t.Helper()
		w := authHTTP(t, f.server, "GET", path, session, "", "", nil)
		requireAuthStatus(t, w, 200)
		if strings.Contains(w.Body.String(), "source_id") || strings.Contains(w.Body.String(), "external_ref") || strings.Contains(w.Body.String(), "actor") {
			t.Fatal("private source original leaked")
		}
		var rows []walletSourcesHTTPRow
		if err := json.Unmarshal(authData(t, w), &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	t.Run("current_wallet_and_original_request_edges", func(t *testing.T) {
		rows := read("/api/wallet/sources", ownerSession)
		if len(rows) != 1 || rows[0].ID != rows[0].LotID || rows[0].Kind != "manual_credit" || rows[0].Issued != "1000" || rows[0].Available != "985" || rows[0].Held != "0" || rows[0].Consumed != "15" {
			t.Fatal(rows)
		}
		edges := read("/api/requests/"+requestID+"/funding", ownerSession)
		if len(edges) != 1 || edges[0].LotID != rows[0].LotID || edges[0].Reserved != "140" || edges[0].Charged == nil || *edges[0].Charged != "20" || edges[0].Released == nil || *edges[0].Released != "120" || edges[0].Refunded != "5" {
			t.Fatal(edges)
		}
	})
	t.Run("tenant_member_and_request_owner", func(t *testing.T) {
		viewer, _ := authUser(t, f.server, f.pool, "none")
		member, _ := authUser(t, f.server, f.pool, "none")
		other := uuid.NewString()
		authExec(t, f.pool, "INSERT INTO tenants(id,name) VALUES($1,'other sources')", other)
		authExec(t, f.pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'billing_viewer'),($1,$3,'member'),($4,$5,'owner')", f.tenant, viewer, member, other, owner)
		viewerSession, _ := authSession(t, f.server, f.pool, viewer, f.tenant, false, true)
		memberSession, _ := authSession(t, f.server, f.pool, member, f.tenant, false, true)
		otherSession, _ := authSession(t, f.server, f.pool, owner, other, false, true)
		if len(read("/api/wallet/sources", viewerSession)) != 1 || len(read("/api/wallet/sources?tenant_id="+f.tenant, otherSession)) != 0 {
			t.Fatal("tenant scope changed")
		}
		requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/wallet/sources", memberSession, "", "", nil), 403)
		requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/requests/"+requestID+"/funding", memberSession, "", "", nil), 404)
		requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/requests/"+requestID+"/funding", otherSession, "", "", nil), 404)
		authExec(t, f.pool, "UPDATE memberships SET role='member' WHERE tenant_id=$1 AND user_id=$2", f.tenant, owner)
		if len(read("/api/requests/"+requestID+"/funding", ownerSession)) != 1 {
			t.Fatal("member cannot view own request")
		}
		authExec(t, f.pool, "UPDATE memberships SET role='owner' WHERE tenant_id=$1 AND user_id=$2", f.tenant, owner)
	})
	t.Run("platform_scope_and_current_read_only_check", func(t *testing.T) {
		for _, role := range []string{"admin", "finance_operator", "finance_approver"} {
			user, _ := authUser(t, f.server, f.pool, role)
			session, _ := authSession(t, f.server, f.pool, user, "", false, true)
			if len(read("/api/platform/wallet-sources?tenant_id="+f.tenant, session)) != 1 {
				t.Fatal(role)
			}
			w := authHTTP(t, f.server, "GET", "/api/platform/wallet-sources/check?tenant_id="+f.tenant, session, "", "", nil)
			requireAuthStatus(t, w, 200)
			var check billing.WalletSourceCheck
			if err := json.Unmarshal(authData(t, w), &check); err != nil || !check.Balanced {
				t.Fatal(check, err)
			}
			for _, query := range []string{"", "?tenant_id=bad", "?tenant_id=" + f.tenant + "&tenant_id=" + f.tenant} {
				requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/platform/wallet-sources"+query, session, "", "", nil), 400)
			}
		}
		requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/platform/wallet-sources?tenant_id="+f.tenant, ownerSession, "", "", nil), 403)
		requireAuthStatus(t, authHTTP(t, f.server, "GET", "/api/wallet/sources?cursor=bad", ownerSession, "", "", nil), 400)
	})
}

func TestWalletSourcesHTTPPaginationAndExactMoney(t *testing.T) {
	s, pool := newAuthFixture(t)
	tenant := uuid.NewString()
	owner, _ := authUser(t, s, pool, "none")
	operator, _ := authUser(t, s, pool, "finance_operator")
	approver, _ := authUser(t, s, pool, "finance_approver")
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'source pages')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", tenant, owner)
	raw, _ := authSession(t, s, pool, owner, tenant, false, true)
	for i := 0; i < 51; i++ {
		ref := uuid.NewString()
		amount := billing.Money(1)
		if i == 0 {
			amount = 9007199254740993
		}
		order, err := s.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: tenant, ActorID: operator, BusinessRef: ref, ProofRef: "current-source-page", Amount: amount, IdempotencyKey: ref, RequestHMAC: ref})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: tenant, OrderID: order.ID, ActorID: approver}); err != nil {
			t.Fatal(err)
		}
	}
	first := authHTTP(t, s, "GET", "/api/wallet/sources", raw, "", "", nil)
	requireAuthStatus(t, first, 200)
	var envelope struct {
		Data []walletSourcesHTTPRow `json:"data"`
		Next string                 `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil || len(envelope.Data) != 50 || envelope.Next == "" {
		t.Fatal(envelope, err)
	}
	seen := map[string]bool{}
	for _, row := range envelope.Data {
		if seen[row.ID] {
			t.Fatal("duplicate source")
		}
		seen[row.ID] = true
	}
	second := authHTTP(t, s, "GET", "/api/wallet/sources?cursor="+envelope.Next, raw, "", "", nil)
	requireAuthStatus(t, second, 200)
	if err := json.Unmarshal(second.Body.Bytes(), &envelope); err != nil || len(envelope.Data) != 1 || envelope.Next != "" || seen[envelope.Data[0].ID] || envelope.Data[0].Issued != "9007199254740993" {
		t.Fatal(envelope, err)
	}
}
