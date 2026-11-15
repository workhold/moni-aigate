package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/alerts"
)

func TestHTTPCashRefundPendingAlertRuleContract(t *testing.T) {
	s, pool := newAuthFixture(t)
	s.Config.OperationsAlertsEnabled = true
	if err := s.InitializeAlerts(); err != nil {
		t.Fatal(err)
	}
	admin := uuid.NewString()
	authExec(t, pool, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, admin, admin+"@alert.invalid")
	raw, csrf := authSession(t, s, pool, admin, "", false, true)
	tenant := uuid.NewString()
	authExec(t, pool, `INSERT INTO tenants(id,name) VALUES($1,'cash alert tenant')`, tenant)
	body := map[string]any{"operation_id": uuid.NewString(), "kind": "cash_refund_pending_aged", "scope_type": "tenant", "scope_id": tenant, "label": "现金退款待处理", "state": "enabled", "parameters": map[string]any{"minimum_age_seconds": "600", "trigger_count": "1", "recovery_count": "0"}}
	call := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		s.alertAdmission = alertAdmission{}
		return authHTTP(t, s, http.MethodPost, "/api/platform/alert-rules", raw, csrf, s.Config.Origin, body)
	}
	var created alerts.CommandReceipt
	response := call(body)
	requireAuthStatus(t, response, http.StatusOK)
	if err := json.Unmarshal(authData(t, response), &created); err != nil || created.Status != "applied" || created.ResourceID == nil {
		t.Fatalf("fifth alert rule creation: %+v %v", created, err)
	}
	replayed := call(body)
	requireAuthStatus(t, replayed, http.StatusOK)
	var replay alerts.CommandReceipt
	if err := json.Unmarshal(authData(t, replayed), &replay); err != nil || replay.OperationID != created.OperationID {
		t.Fatalf("fifth alert replay mismatch: %+v %v", replay, err)
	}
	get := authHTTP(t, s, http.MethodGet, "/api/platform/alert-rules/"+*created.ResourceID, raw, "", s.Config.Origin, nil)
	requireAuthStatus(t, get, http.StatusOK)
	var rule struct {
		Kind       alerts.Kind      `json:"kind"`
		ScopeType  alerts.ScopeType `json:"scope_type"`
		ScopeID    string           `json:"scope_id"`
		Parameters json.RawMessage  `json:"parameters"`
	}
	if err := json.Unmarshal(authData(t, get), &rule); err != nil || rule.Kind != alerts.CashRefundPendingAged || rule.ScopeType != "tenant" || rule.ScopeID != tenant {
		t.Fatalf("fifth alert rule DTO: %+v %v", rule, err)
	}
	parameters, err := alerts.DecodeParameters(rule.Kind, rule.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parameters.(alerts.AgingParameters); !ok {
		t.Fatalf("fifth alert parameters type=%T", parameters)
	}
	bad := map[string]any{}
	for key, value := range body {
		bad[key] = value
	}
	bad["scope_type"] = "procurement_account"
	requireAuthStatus(t, call(bad), http.StatusBadRequest)
}
