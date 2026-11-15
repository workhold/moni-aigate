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

func TestHTTPWalletOperatorSettingsAndAdminFinance(t *testing.T) {
	s, pool := newAuthFixture(t)
	admin, _ := authUser(t, s, pool, "admin")
	operator, _ := authUser(t, s, pool, "finance_operator")
	tenant := uuid.NewString()
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'operator settings HTTP')", tenant)
	adminRaw, adminCSRF := authSession(t, s, pool, admin, "", false, true)
	staleRaw, staleCSRF := authSession(t, s, pool, admin, "", false, false)
	operatorRaw, _ := authSession(t, s, pool, operator, "", false, true)
	call := func(method, path, raw, csrf, idempotency string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		var err error
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, s.Config.Origin+path, bytes.NewReader(data))
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		if idempotency != "" {
			req.Header.Set("Idempotency-Key", idempotency)
		}
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}

	creditBody := map[string]any{"tenant_id": tenant, "amount": "12", "basis_kind": "paid", "fiat_currency": "CNY", "fiat_amount": "12", "bonus_amount": "0", "business_ref": "admin-single-http", "proof_ref": "proof:admin-single", "transfer_ref": "transfer:admin-single"}
	created := call(http.MethodPost, "/api/platform/credits", adminRaw, adminCSRF, uuid.NewString(), creditBody)
	requireAuthStatus(t, created, http.StatusOK)
	var order billing.CreditOrder
	if err := json.Unmarshal(authData(t, created), &order); err != nil || order.State != "pending" {
		t.Fatalf("admin maker: %+v %v", order, err)
	}
	approvedResponse := call(http.MethodPost, "/api/platform/credits/"+order.ID+"/approve", adminRaw, adminCSRF, uuid.NewString(), map[string]any{})
	requireAuthStatus(t, approvedResponse, http.StatusOK)
	var approved billing.CreditOrder
	if err := json.Unmarshal(authData(t, approvedResponse), &approved); err != nil || approved.State != "approved" || !approved.SingleOperator || approved.Requester != admin || approved.Approver != admin {
		t.Fatalf("admin single approval: %+v %v", approved, err)
	}
	var exceptions int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='credit.approved' AND target=$1 AND sanitized_diff->>'single_operator_exception'='true'`, order.ID).Scan(&exceptions); err != nil || exceptions != 1 {
		t.Fatalf("single operator exception audit=%d %v", exceptions, err)
	}

	requireAuthStatus(t, call(http.MethodGet, "/api/platform/wallet-operator-settings", staleRaw, "", "", nil), http.StatusOK)
	requireAuthStatus(t, call(http.MethodGet, "/api/platform/wallet-operator-settings", operatorRaw, "", "", nil), http.StatusForbidden)
	command := map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "single_operator": false, "reason": "restore dual control", "confirmation": billing.WalletOperatorConfirmation}
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/wallet-operator-settings", staleRaw, staleCSRF, "", command), http.StatusForbidden)
	invalid := map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "single_operator": false, "reason": "missing confirmation", "confirmation": "no"}
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, adminCSRF, "", invalid), http.StatusBadRequest)
	disabled := call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, adminCSRF, "", command)
	requireAuthStatus(t, disabled, http.StatusOK)
	replayed := call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, adminCSRF, "", command)
	requireAuthStatus(t, replayed, http.StatusOK)
	if !bytes.Equal(authData(t, disabled), authData(t, replayed)) {
		t.Fatal("HTTP operation replay returned a different setting")
	}
	conflict := map[string]any{"operation_id": command["operation_id"], "expected_revision": "1", "single_operator": false, "reason": "changed intent", "confirmation": billing.WalletOperatorConfirmation}
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, adminCSRF, "", conflict), http.StatusConflict)
	stale := map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "single_operator": true, "reason": "stale revision", "confirmation": billing.WalletOperatorConfirmation}
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, adminCSRF, "", stale), http.StatusConflict)
	requireAuthStatus(t, call(http.MethodPut, "/api/platform/wallet-operator-settings", adminRaw, "", "", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "2", "single_operator": true, "reason": "missing csrf", "confirmation": billing.WalletOperatorConfirmation}), http.StatusForbidden)

	creditBody["business_ref"] = "admin-disabled-http"
	requireAuthStatus(t, call(http.MethodPost, "/api/platform/credits", adminRaw, adminCSRF, uuid.NewString(), creditBody), http.StatusForbidden)
	viewResponse := call(http.MethodGet, "/api/platform/wallet-operator-settings", adminRaw, "", "", nil)
	requireAuthStatus(t, viewResponse, http.StatusOK)
	var view billing.WalletOperatorSettingsView
	if err := json.Unmarshal(authData(t, viewResponse), &view); err != nil || view.Current.Version != 2 || view.Current.SingleOperator || len(view.Versions) != 2 {
		t.Fatalf("operator history: %+v %v", view, err)
	}
	var audits int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action='wallet.operator_settings.updated'`).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("operator audit count=%d %v", audits, err)
	}
}
