package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestHTTPWalletRequestLinkApprovalAndIsolation(t *testing.T) {
	s, pool := newAuthFixture(t)
	owner, _ := authUser(t, s, pool, "none")
	operator, _ := authUser(t, s, pool, "finance_operator")
	approver, _ := authUser(t, s, pool, "finance_approver")
	tenant, otherTenant := uuid.NewString(), uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'request HTTP'),($2,'other request HTTP')", tenant, otherTenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner'),($3,$2,'owner')", tenant, owner, otherTenant)
	ownerRaw, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	staleRaw, staleCSRF := authSession(t, s, pool, owner, tenant, false, false)
	otherRaw, otherCSRF := authSession(t, s, pool, owner, otherTenant, false, true)
	operatorRaw, operatorCSRF := authSession(t, s, pool, operator, "", false, true)
	approverRaw, approverCSRF := authSession(t, s, pool, approver, "", false, true)
	call := func(method, path, raw, csrf, key string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		var err error
		if body != nil {
			data, err = json.Marshal(body)
		}
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, s.Config.Origin+path, bytes.NewReader(data))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}
	body := map[string]any{"kind": "topup", "note": "bank-request-http"}
	key := uuid.NewString()
	requireAuthStatus(t, call(http.MethodPost, "/api/wallet/requests", staleRaw, staleCSRF, key, body), http.StatusForbidden)
	createdResponse := call(http.MethodPost, "/api/wallet/requests", ownerRaw, ownerCSRF, key, body)
	requireAuthStatus(t, createdResponse, http.StatusOK)
	replayed := call(http.MethodPost, "/api/wallet/requests", ownerRaw, ownerCSRF, key, body)
	requireAuthStatus(t, replayed, http.StatusOK)
	if !bytes.Equal(authData(t, createdResponse), authData(t, replayed)) {
		t.Fatal("request replay returned different data")
	}
	var request billing.WalletRequest
	if err := json.Unmarshal(authData(t, createdResponse), &request); err != nil || request.State != "open" || request.TenantID != tenant {
		t.Fatalf("created request: %+v %v", request, err)
	}
	conflictBody := map[string]any{"kind": "topup", "note": "changed-request-http"}
	requireAuthStatus(t, call(http.MethodPost, "/api/wallet/requests", ownerRaw, ownerCSRF, key, conflictBody), http.StatusConflict)
	var effects int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM credit_orders WHERE business_ref='request-http')+(SELECT count(*) FROM journals WHERE operation_id=$1)`, request.ID).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("request created wallet effects: %d %v", effects, err)
	}

	creditBody := map[string]any{"tenant_id": tenant, "request_id": request.ID, "amount": "25", "basis_kind": "paid", "fiat_currency": "CNY", "fiat_amount": "25", "business_ref": "request-http", "proof_ref": "proof:request-http"}
	pendingResponse := call(http.MethodPost, "/api/platform/credits", operatorRaw, operatorCSRF, uuid.NewString(), creditBody)
	requireAuthStatus(t, pendingResponse, http.StatusOK)
	var pending billing.CreditOrder
	if err := json.Unmarshal(authData(t, pendingResponse), &pending); err != nil || pending.State != "pending" || pending.RequestedBy != owner || pending.RequestID != request.ID {
		t.Fatalf("linked credit: %+v %v", pending, err)
	}
	var state string
	if err := pool.QueryRow(t.Context(), `SELECT state FROM wallet_requests WHERE id=$1 AND linked_order_id=$2`, request.ID, pending.ID).Scan(&state); err != nil || state != "linked" {
		t.Fatalf("request was not linked: %s %v", state, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM funding_receipts WHERE parent_ref=$1`, pending.ID).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("linked request had wallet effects before approval: %d %v", effects, err)
	}
	approvedResponse := call(http.MethodPost, "/api/platform/credits/"+pending.ID+"/approve", approverRaw, approverCSRF, uuid.NewString(), map[string]any{})
	requireAuthStatus(t, approvedResponse, http.StatusOK)
	if err := pool.QueryRow(t.Context(), `SELECT state FROM wallet_requests WHERE id=$1`, request.ID).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("approval did not close request: %s %v", state, err)
	}

	crossBody := map[string]any{"kind": "topup", "note": "cross-tenant-link"}
	crossResponse := call(http.MethodPost, "/api/wallet/requests", otherRaw, otherCSRF, uuid.NewString(), crossBody)
	requireAuthStatus(t, crossResponse, http.StatusOK)
	var cross billing.WalletRequest
	if err := json.Unmarshal(authData(t, crossResponse), &cross); err != nil {
		t.Fatal(err)
	}
	creditBody["tenant_id"] = tenant
	creditBody["request_id"] = cross.ID
	creditBody["business_ref"] = "cross-tenant-request"
	requireAuthStatus(t, call(http.MethodPost, "/api/platform/credits", operatorRaw, operatorCSRF, uuid.NewString(), creditBody), http.StatusBadRequest)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM credit_orders WHERE business_ref='cross-tenant-request'`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("cross-tenant link retained a credit: %d %v", effects, err)
	}

	cancelResponse := call(http.MethodPost, "/api/wallet/requests/"+cross.ID+"/close", otherRaw, otherCSRF, uuid.NewString(), map[string]any{"reason": "customer cancelled"})
	requireAuthStatus(t, cancelResponse, http.StatusOK)
	cashResponse := call(http.MethodPost, "/api/wallet/requests", ownerRaw, ownerCSRF, uuid.NewString(), map[string]any{"kind": "cash_refund", "note": "future cash request"})
	requireAuthStatus(t, cashResponse, http.StatusOK)
	var cash billing.WalletRequest
	if err := json.Unmarshal(authData(t, cashResponse), &cash); err != nil {
		t.Fatal(err)
	}
	creditBody["request_id"] = cash.ID
	creditBody["business_ref"] = "cash-request-too-early"
	requireAuthStatus(t, call(http.MethodPost, "/api/platform/credits", operatorRaw, operatorCSRF, uuid.NewString(), creditBody), http.StatusBadRequest)
	closeResponse := call(http.MethodPost, "/api/platform/wallet-requests/"+cash.ID+"/close", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{"reason": "payment evidence not found"})
	requireAuthStatus(t, closeResponse, http.StatusOK)
	if err := pool.QueryRow(t.Context(), `SELECT state FROM wallet_requests WHERE id=$1 AND linked_order_id IS NULL`, cash.ID).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("finance rejection did not close request: %s %v", state, err)
	}
	listResponse := call(http.MethodGet, "/api/platform/wallet-requests?tenant_id="+tenant, operatorRaw, "", "", nil)
	requireAuthStatus(t, listResponse, http.StatusOK)
	var listed []billing.WalletRequest
	if err := json.Unmarshal(authData(t, listResponse), &listed); err != nil || len(listed) != 2 {
		t.Fatalf("platform request list: %+v %v", listed, err)
	}
}
