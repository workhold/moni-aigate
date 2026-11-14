package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestHTTPCashRefundLifecycleStrictInputAndReadback(t *testing.T) {
	s, pool := newAuthFixture(t)
	tenant := uuid.NewString()
	operator, _ := authUser(t, s, pool, "finance_operator")
	approver, _ := authUser(t, s, pool, "finance_approver")
	owner, _ := authUser(t, s, pool, "none")
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'cash refund HTTP')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", tenant, owner)
	operatorRaw, operatorCSRF := authSession(t, s, pool, operator, "", false, true)
	approverRaw, approverCSRF := authSession(t, s, pool, approver, "", false, true)
	ownerRaw, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	key := uuid.NewString()
	credit, err := s.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: tenant, ActorID: operator, BusinessRef: key, ProofRef: "proof:cash-http", Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	var lot string
	if err = pool.QueryRow(context.Background(), `SELECT id FROM funding_lots WHERE source_kind='manual_credit' AND source_id=$1`, credit.ID).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, raw, csrf, operation string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, s.Config.Origin+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", operation)
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}
	createBody := map[string]any{"tenant_id": tenant, "lot_id": lot, "business_ref": "cash-refund:http/lifecycle", "reason": "verified offline refund"}
	createKey := uuid.NewString()
	created := call(http.MethodPost, "/api/platform/cash-refunds", operatorRaw, operatorCSRF, createKey, createBody)
	requireAuthStatus(t, created, http.StatusOK)
	var order billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, created), &order); err != nil || order.State != "requested" || order.PreviewCoinAmount == nil || *order.PreviewCoinAmount != 1000 || order.PreviewFiatAmount == nil || *order.PreviewFiatAmount != 1000 || order.CoinAmount != nil {
		t.Fatalf("cash-refund create DTO: %+v %v", order, err)
	}
	replayed := call(http.MethodPost, "/api/platform/cash-refunds", operatorRaw, operatorCSRF, createKey, createBody)
	requireAuthStatus(t, replayed, http.StatusOK)
	var replayedOrder billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, replayed), &replayedOrder); err != nil || replayedOrder.ID != order.ID {
		t.Fatalf("create replay changed order: %+v %v", replayedOrder, err)
	}
	var orders int
	if err = pool.QueryRow(context.Background(), `SELECT count(*) FROM cash_refund_orders WHERE id=$1`, order.ID).Scan(&orders); err != nil || orders != 1 {
		t.Fatalf("create replay duplicated order: %d %v", orders, err)
	}

	requireAuthStatus(t, call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/approve", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{}), http.StatusForbidden)
	approvedResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/approve", approverRaw, approverCSRF, uuid.NewString(), map[string]any{})
	requireAuthStatus(t, approvedResponse, http.StatusOK)
	var approved billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, approvedResponse), &approved); err != nil || approved.State != "approved" || approved.CoinAmount == nil || *approved.CoinAmount != 1000 || approved.ApprovedAt == nil {
		t.Fatalf("cash-refund approval DTO: %+v %v", approved, err)
	}
	executedResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/execute", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{"transfer_order_ref": "transfer:http-order"})
	requireAuthStatus(t, executedResponse, http.StatusOK)
	var executed billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, executedResponse), &executed); err != nil || executed.State != "transfer_executed" || executed.TransferOrderRef != "transfer:http-order" {
		t.Fatalf("cash-refund execute DTO: %+v %v", executed, err)
	}
	requireAuthStatus(t, call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/complete", approverRaw, approverCSRF, uuid.NewString(), map[string]any{"transfer_proof_ref": "transfer:http-proof", "paid_fiat_amount": "999"}), http.StatusConflict)
	completedResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/complete", approverRaw, approverCSRF, uuid.NewString(), map[string]any{"transfer_proof_ref": "transfer:http-proof", "paid_fiat_amount": "1000"})
	requireAuthStatus(t, completedResponse, http.StatusOK)
	var completed billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, completedResponse), &completed); err != nil || completed.State != "completed" || completed.RetireJournalID == "" || completed.PaidFiatAmount == nil || *completed.PaidFiatAmount != 1000 {
		t.Fatalf("cash-refund complete DTO: %+v %v", completed, err)
	}
	readback := call(http.MethodGet, "/api/platform/cash-refunds/by-business/"+url.PathEscape(order.BusinessRef), operatorRaw, "", "", nil)
	requireAuthStatus(t, readback, http.StatusOK)
	var readbackOrder billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, readback), &readbackOrder); err != nil || readbackOrder.State != "completed" || readbackOrder.RetireJournalID != completed.RetireJournalID {
		t.Fatalf("business readback mismatch: %+v %v", readbackOrder, err)
	}
	list := call(http.MethodGet, "/api/platform/cash-refunds?tenant_id="+tenant+"&state=completed", approverRaw, "", "", nil)
	requireAuthStatus(t, list, http.StatusOK)
	if !strings.Contains(list.Body.String(), order.ID) || !strings.Contains(list.Body.String(), `"next_cursor":""`) {
		t.Fatalf("cash-refund list missing completed order: %s", list.Body.String())
	}
	requireAuthStatus(t, call(http.MethodGet, "/api/platform/cash-refunds/by-business/cash-refund:missing", operatorRaw, "", "", nil), http.StatusNotFound)
	requireAuthStatus(t, call(http.MethodPost, "/api/platform/cash-refunds", ownerRaw, ownerCSRF, uuid.NewString(), createBody), http.StatusForbidden)
	for _, body := range []any{
		map[string]any{"tenant_id": tenant, "lot_id": lot, "business_ref": "cash-refund:amount-input", "reason": "amount forbidden", "coin_amount": "1"},
		map[string]any{"tenant_id": tenant, "lot_id": lot, "business_ref": "cash-refund:fiat-input", "reason": "fiat forbidden", "fiat_amount": "1"},
		map[string]any{"tenant_id": tenant, "lot_id": lot, "business_ref": "cash-refund:json-number", "reason": "number forbidden", "paid_fiat_amount": 1},
	} {
		requireAuthStatus(t, call(http.MethodPost, "/api/platform/cash-refunds", operatorRaw, operatorCSRF, uuid.NewString(), body), http.StatusBadRequest)
	}
	requireAuthStatus(t, call(http.MethodGet, "/api/platform/cash-refunds?state=unknown", operatorRaw, "", "", nil), http.StatusBadRequest)
	var valid bool
	if err = pool.QueryRow(context.Background(), `SELECT wallet_cash_refund_order_valid($1)`, order.ID).Scan(&valid); err != nil || !valid {
		t.Fatalf("HTTP completed order invalid: %v valid=%v", err, valid)
	}
	check, err := s.Billing.CheckWalletSources(context.Background(), tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("HTTP wallet sources unbalanced: %+v %v", check, err)
	}
}
