package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/alerts"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func TestAlertHTTPStateHistoryReceiptsAndAccessBoundaries(t *testing.T) {
	s, pool := newAuthFixture(t)
	s.Config.OperationsAlertsEnabled = true
	if err := s.InitializeAlerts(); err != nil {
		t.Fatal(err)
	}
	actor := func(role string) commerceActor {
		id := uuid.NewString()
		authExec(t, pool, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)`, id, id+"@alert.invalid", role)
		raw, csrf := authSession(t, s, pool, id, "", false, true)
		return commerceActor{id, raw, csrf}
	}
	admin, finance, checker, customer := actor("admin"), actor("finance_operator"), actor("finance_approver"), actor("none")
	call := func(a commerceActor, method, path string, body any) *httptest.ResponseRecorder {
		s.alertAdmission = alertAdmission{}
		return authHTTP(t, s, method, path, a.raw, a.csrf, s.Config.Origin, body)
	}
	receipt := func(w *httptest.ResponseRecorder, status, reason string) alerts.CommandReceipt {
		t.Helper()
		requireAuthStatus(t, w, 200)
		var v alerts.CommandReceipt
		if err := json.Unmarshal(authData(t, w), &v); err != nil {
			t.Fatal(err)
		}
		if v.Status != status || v.Reason != reason {
			t.Fatalf("receipt %+v", v)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private response cached")
		}
		return v
	}
	channel, err := s.Catalog.SaveChannel(context.Background(), admin.id, "", catalog.ChannelInput{Name: "alert source", BaseURL: "http://127.0.0.1:9", Secret: "synthetic", AllowedCIDRs: []string{"127.0.0.1/32"}})
	if err != nil {
		t.Fatal(err)
	}
	authExec(t, pool, `UPDATE channels SET quarantined=true WHERE id=$1`, channel)
	body := map[string]any{"operation_id": uuid.NewString(), "kind": "channel_quarantined", "scope_type": "channel", "scope_id": channel, "label": "渠道隔离", "state": "enabled", "parameters": map[string]any{}}
	creation := receipt(call(admin, "POST", "/api/platform/alert-rules", body), "applied", "")
	if creation.ResourceID == nil || creation.ResultRevision == nil || *creation.ResultRevision != "1" {
		t.Fatal(creation)
	}
	id := *creation.ResourceID
	if replay := receipt(call(admin, "POST", "/api/platform/alert-rules", body), "applied", ""); !reflect.DeepEqual(creation, replay) {
		t.Fatal("creation replay changed")
	}
	for _, a := range []commerceActor{finance, checker} {
		requireAuthStatus(t, call(a, "GET", "/api/platform/alert-rules/"+id, nil), 200)
		requireAuthStatus(t, call(a, "POST", "/api/platform/alert-rules", body), 403)
	}
	requireAuthStatus(t, call(customer, "GET", "/api/platform/alerts", nil), 403)
	requireAuthStatus(t, call(commerceActor{}, "GET", "/api/platform/alerts", nil), 401)
	other := actor("admin")
	requireAuthStatus(t, call(other, "GET", "/api/platform/alert-operations/"+creation.OperationID, nil), 404)
	changed := map[string]any{}
	for k, v := range body {
		changed[k] = v
	}
	changed["label"] = "different"
	requireAuthStatus(t, call(admin, "POST", "/api/platform/alert-rules", changed), 409)
	changed["operation_id"] = uuid.NewString()
	receipt(call(admin, "POST", "/api/platform/alert-rules", changed), "rejected", "alert_rule_exists")

	repository, err := alerts.NewStore(pool, alerts.RuntimeOptions{DeploymentEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.EvaluateDue(context.Background())
	if err != nil || result.Outcome != "evaluated" {
		t.Fatalf("evaluate %+v %v", result, err)
	}
	var page struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
		Next *string `json:"next_cursor"`
	}
	w := call(finance, "GET", "/api/platform/alerts?status=open", nil)
	requireAuthStatus(t, w, 200)
	if json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Next != nil || page.Data[0].Status != "open" {
		t.Fatal(w.Body.String())
	}
	episode := page.Data[0].ID
	ackBody := map[string]any{"operation_id": uuid.NewString(), "expected_action_revision": "1"}
	ack := receipt(call(finance, "POST", "/api/platform/alerts/"+episode+"/acknowledge", ackBody), "applied", "")
	if ack.ResultRevision == nil || *ack.ResultRevision != "2" {
		t.Fatal(ack)
	}
	if replay := receipt(call(finance, "POST", "/api/platform/alerts/"+episode+"/acknowledge", ackBody), "applied", ""); !reflect.DeepEqual(ack, replay) {
		t.Fatal("ACK replay changed")
	}
	var quarantined bool
	if err := pool.QueryRow(context.Background(), `SELECT quarantined FROM channels WHERE id=$1`, channel).Scan(&quarantined); err != nil || !quarantined {
		t.Fatal("ACK changed source", err)
	}

	configure := map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1", "label": "修订规则", "state": "enabled", "parameters": map[string]any{}}
	receipt(call(admin, "PUT", "/api/platform/alert-rules/"+id, configure), "applied", "")
	w = call(finance, "GET", "/api/platform/alerts/"+episode, nil)
	requireAuthStatus(t, w, 200)
	var closed struct {
		Status         string  `json:"status"`
		ResolveReason  *string `json:"resolve_reason"`
		AcknowledgedBy *string `json:"acknowledged_by"`
		RuleSnapshot   struct {
			Label string `json:"label"`
		} `json:"rule_snapshot"`
	}
	if json.Unmarshal(authData(t, w), &closed) != nil || closed.Status != "resolved" || closed.ResolveReason == nil || *closed.ResolveReason != "rule_changed" || closed.RuleSnapshot.Label != "渠道隔离" || closed.AcknowledgedBy == nil || *closed.AcknowledgedBy != finance.id {
		t.Fatal(w.Body.String())
	}
	w = call(checker, "GET", "/api/platform/alerts/"+episode+"/events", nil)
	requireAuthStatus(t, w, 200)
	var events struct {
		Data []struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &events) != nil || len(events.Data) != 3 || events.Data[0].Type != "rule_changed" || events.Data[1].Type != "acknowledged" || events.Data[2].Type != "opened" {
		t.Fatal(w.Body.String())
	}
	if result, err = repository.EvaluateDue(context.Background()); err != nil || result.Outcome != "evaluated" {
		t.Fatalf("reopen %+v %v", result, err)
	}
	receipt(call(admin, "POST", "/api/platform/alert-rules/"+id+"/retire", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "2"}), "applied", "")
	w = call(admin, "GET", "/api/platform/alert-operations/"+creation.OperationID, nil)
	if original := receipt(w, "applied", ""); !reflect.DeepEqual(original, creation) {
		t.Fatal("retirement rewrote original receipt")
	}
	var rules, episodes, journal int
	if err = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM alert_rules),(SELECT count(*) FROM alert_episodes),(SELECT count(*) FROM journals)`).Scan(&rules, &episodes, &journal); err != nil || rules != 1 || episodes != 2 || journal != 0 {
		t.Fatal("unexpected business side effects", rules, episodes, journal, err)
	}
	authExec(t, pool, `UPDATE users SET platform_role='none' WHERE id=$1`, finance.id)
	requireAuthStatus(t, call(finance, "GET", "/api/platform/alerts/"+episode, nil), 403)
}

func TestAlertHTTPStrictValidationDefaultOffRecoveryAndSessionGuards(t *testing.T) {
	s, pool := newAuthFixture(t)
	if err := s.InitializeAlerts(); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	authExec(t, pool, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, id, id+"@alerts.invalid")
	raw, csrf := authSession(t, s, pool, id, "", false, true)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		s.alertAdmission = alertAdmission{}
		return authHTTP(t, s, method, path, raw, csrf, s.Config.Origin, body)
	}
	w := call("GET", "/api/platform/alerts/capabilities", nil)
	requireAuthStatus(t, w, 200)
	var cap alerts.Capabilities
	if json.Unmarshal(authData(t, w), &cap) != nil || cap.DeploymentEnabled || cap.EvaluationAvailable || cap.Reason != "deployment_disabled" {
		t.Fatal(w.Body.String())
	}
	tenant := uuid.NewString()
	authExec(t, pool, `INSERT INTO tenants(id,name) VALUES($1,'strict alerts')`, tenant)
	body := map[string]any{"operation_id": uuid.NewString(), "kind": "customer_reconciliation_aged", "scope_type": "tenant", "scope_id": tenant, "label": "旧未决", "state": "enabled", "parameters": map[string]any{"minimum_age_seconds": "600", "trigger_count": "1", "recovery_count": "0"}}
	w = call("POST", "/api/platform/alert-rules", body)
	requireAuthStatus(t, w, 200)
	var created alerts.CommandReceipt
	if json.Unmarshal(authData(t, w), &created) != nil || created.Status != "applied" || created.ResourceID == nil {
		t.Fatal(w.Body.String())
	}
	w = call("GET", "/api/platform/alert-rules/"+*created.ResourceID, nil)
	requireAuthStatus(t, w, 200)
	if !strings.Contains(w.Body.String(), `"freshness":"paused"`) {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/api/platform/alerts?", "/api/platform/alert-rules?scope_id=" + tenant, "/api/platform/alerts?status=all", "/api/platform/alerts?cursor=eA", "/api/platform/alerts?status=open&status=resolved", "/api/platform/alerts?limit=50"} {
		requireAuthStatus(t, call("GET", path, nil), 400)
	}
	for _, bad := range []string{
		`{"operation_id":"` + uuid.NewString() + `","expected_revision":1}`,
		`{"operation_id":"` + uuid.NewString() + `","expected_revision":"01"}`,
		`{"operation_id":"` + uuid.NewString() + `","expected_revision":"1","extra":false}`,
		`{"operation_id":"` + uuid.NewString() + `","expected_revision":"1","expected_revision":"1"}`,
		`{"operation_id":"` + uuid.NewString() + `","expected_revision":null}`,
	} {
		requireAuthStatus(t, call("POST", "/api/platform/alert-rules/"+*created.ResourceID+"/retire", json.RawMessage(bad)), 400)
	}
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/alert-rules", raw, "", s.Config.Origin, body), 403)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/alert-rules", raw, csrf, "http://other.invalid", body), 403)
	old, oldCSRF := authSession(t, s, pool, id, "", false, false)
	requireAuthStatus(t, authHTTP(t, s, "POST", "/api/platform/alert-rules", old, oldCSRF, s.Config.Origin, body), 403)
	s.Config.RecoveryMode = true
	if err := s.InitializeAlerts(); err != nil {
		t.Fatal(err)
	}
	w = call("GET", "/api/platform/alerts/capabilities", nil)
	requireAuthStatus(t, w, 200)
	if json.Unmarshal(authData(t, w), &cap) != nil || !cap.RecoveryMode || cap.EvaluationAvailable {
		t.Fatal(w.Body.String())
	}
	requireAuthStatus(t, call("POST", "/api/platform/alert-rules/"+*created.ResourceID+"/retire", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1"}), 503)
}
