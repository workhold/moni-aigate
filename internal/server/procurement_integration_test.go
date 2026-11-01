package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

func procurementJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func procurementReceipt(t *testing.T, response *httptest.ResponseRecorder) procurement.Command {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("procurement command HTTP %d %s", response.Code, response.Body.String())
	}
	var value procurement.Command
	if err := json.Unmarshal(authData(t, response), &value); err != nil || value.OperationID == "" || (value.Status != "applied" && value.Status != "rejected") {
		t.Fatal("invalid procurement receipt", err, response.Body.String())
	}
	return value
}

func procurementAccountFromReceipt(t *testing.T, receipt procurement.Command) procurement.Account {
	t.Helper()
	var account procurement.Account
	if err := json.Unmarshal(receipt.After, &account); err != nil || account.ID == "" {
		t.Fatal("account receipt has no safe after snapshot", err, string(receipt.After))
	}
	return account
}

func TestProcurementHTTPAdministrationReceiptsAndBoundedSafePages(t *testing.T) {
	s, pool := newAuthFixture(t)
	actor, _ := authUser(t, s, pool, "admin")
	session, csrf := authSession(t, s, pool, actor, "", false, true)
	call := func(method, path, raw string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, s.Engine(), method, "/api/platform/"+path, raw, session, csrf, s.Config.Origin, "", nil)
	}
	in := procurement.CreateInput{OperationID: uuid.NewString(), Label: "aggregator", Provider: "aggregate", State: "active", LimitAmount: "9007199254740993"}
	created := procurementReceipt(t, call("POST", "procurement-accounts", procurementJSON(t, in)))
	account := procurementAccountFromReceipt(t, created)
	if account.LimitAmount != in.LimitAmount || account.HeldAmount != "0" || account.SpentAmount != "0" || account.Revision != "1" || created.Actor != actor {
		t.Fatal("unsafe account projection or precision loss", account, created)
	}
	if repeated := procurementReceipt(t, call("POST", "procurement-accounts", procurementJSON(t, in))); repeated.OperationID != created.OperationID || repeated.CreatedAt != created.CreatedAt {
		t.Fatal("same operation did not return immutable receipt")
	}
	in.Label = "different payload"
	if got := call("POST", "procurement-accounts", procurementJSON(t, in)); got.Code != 409 {
		t.Fatal("different payload reused procurement operation", got.Code)
	}
	if got := call("GET", "procurement-operations/"+created.OperationID, ""); got.Code != 200 || strings.Contains(got.Body.String(), "input_digest") {
		t.Fatal("receipt query omitted result or leaked digest", got.Code, got.Body.String())
	}
	config := procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: "1", Label: "paused supplier", Provider: "aggregate", State: "paused", LimitAmount: "0"}
	configured := procurementReceipt(t, call("PUT", "procurement-accounts/"+account.ID+"/configuration", procurementJSON(t, config)))
	if configured.Status != "applied" || procurementAccountFromReceipt(t, configured).Revision != "2" {
		t.Fatal("configuration did not advance metadata CAS", configured)
	}
	config.OperationID = uuid.NewString()
	if rejected := procurementReceipt(t, call("PUT", "procurement-accounts/"+account.ID+"/configuration", procurementJSON(t, config))); rejected.Status != "rejected" || rejected.Reason != "revision_conflict" {
		t.Fatal("stale CAS did not persist rejected decision", rejected)
	}
	amount := "9007199254740993"
	observation := procurement.ObservationInput{OperationID: uuid.NewString(), Status: "known", Amount: &amount, Currency: "CNY", Source: "manual_evidence", Semantic: "reported_balance", ObservedAt: time.Now().UTC(), EvidenceRef: "invoice:observation"}
	observed := procurementReceipt(t, call("POST", "procurement-accounts/"+account.ID+"/balance-observations", procurementJSON(t, observation)))
	if observed.Status != "applied" {
		t.Fatal("manual balance observation not accepted", observed)
	}
	observation.OperationID = uuid.NewString()
	observation.Status = "unknown"
	if got := call("POST", "procurement-accounts/"+account.ID+"/balance-observations", procurementJSON(t, observation)); got.Code != 400 {
		t.Fatal("unknown balance was represented as a monetary fact", got.Code)
	}
	for _, suffix := range []string{"configuration", "events", "reservations", "balance-observations"} {
		got := call("GET", "procurement-accounts/"+account.ID+"/"+suffix, "")
		if got.Code != 200 || strings.Contains(got.Body.String(), "input_digest") || strings.Contains(got.Body.String(), "password_hash") {
			t.Fatal("unsafe or unavailable account projection", suffix, got.Code, got.Body.String())
		}
	}
	for range 50 {
		in.OperationID = uuid.NewString()
		if _, err := s.Procurement.CreateAccount(context.Background(), actor, in); err != nil {
			t.Fatal(err)
		}
	}
	var first struct {
		Data []procurement.Account `json:"data"`
		Next string                `json:"next_cursor"`
	}
	page := call("GET", "procurement-accounts", "")
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &first) != nil || len(first.Data) != 50 || first.Next == "" {
		t.Fatal("unbounded or missing procurement page", page.Code, page.Body.String())
	}
	second := call("GET", "procurement-accounts?cursor="+first.Next, "")
	var next struct {
		Data []procurement.Account `json:"data"`
	}
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &next) != nil || len(next.Data) != 1 {
		t.Fatal("procurement cursor lost an account", second.Code, second.Body.String())
	}
	for _, path := range []string{"procurement-accounts?cursor=invalid", "procurement-bindings?account_id=invalid", "procurement-bindings?channel_id=" + uuid.NewString() + "&channel_id=" + uuid.NewString(), "procurement-cost-proposals?operation_id=invalid", "procurement-accounts/invalid/configuration", "procurement-operations/invalid"} {
		if got := call("GET", path, ""); got.Code != 400 {
			t.Fatal("invalid resource/filter reached database", path, got.Code)
		}
	}
}

func TestProcurementHTTPPlatformGatesAndFinanceSeparation(t *testing.T) {
	s, pool := newAuthFixture(t)
	id := uuid.NewString()
	reads := []string{"procurement-accounts", "procurement-bindings", "procurement-cost-proposals", "procurement-cost-proposals/" + id, "procurement-accounts/" + id + "/configuration", "procurement-accounts/" + id + "/events", "procurement-accounts/" + id + "/reservations", "procurement-accounts/" + id + "/balance-observations", "procurement-operations/" + id}
	writes := []struct{ method, path string }{{"POST", "procurement-accounts"}, {"PUT", "procurement-accounts/" + id + "/configuration"}, {"POST", "procurement-bindings"}, {"POST", "procurement-accounts/" + id + "/balance-observations"}, {"POST", "procurement-cost-proposals"}, {"POST", "procurement-cost-proposals/" + id + "/approve"}}
	var admin string
	for _, role := range []string{"none", "admin", "finance_operator", "finance_approver"} {
		user, _ := authUser(t, s, pool, role)
		if role == "admin" {
			admin = user
		}
		session, csrf := authSession(t, s, pool, user, "", false, true)
		for _, path := range reads {
			got := keyPolicyHTTP(t, s.Engine(), "GET", "/api/platform/"+path, "", session, "", "", "", nil)
			if (role == "none" && got.Code != 403) || (role != "none" && got.Code != 200 && got.Code != 404) {
				t.Fatal("procurement read crossed platform boundary", role, path, got.Code, got.Body.String())
			}
		}
		for _, operation := range writes {
			allowed := role == "admin" && !strings.Contains(operation.path, "cost-proposals") || role == "finance_operator" && operation.path == "procurement-cost-proposals" || role == "finance_approver" && strings.HasSuffix(operation.path, "/approve")
			got := keyPolicyHTTP(t, s.Engine(), operation.method, "/api/platform/"+operation.path, `{}`, session, csrf, s.Config.Origin, "", nil)
			want := 403
			if allowed {
				want = 400
			}
			if got.Code != want {
				t.Fatal("procurement maker/checker/admin separation failed", role, operation.path, got.Code, got.Body.String())
			}
		}
	}
	session, csrf := authSession(t, s, pool, admin, "", false, true)
	unrecent, unrecentCSRF := authSession(t, s, pool, admin, "", false, false)
	path := "/api/platform/procurement-accounts"
	for _, tc := range []struct{ session, csrf, origin string }{{unrecent, unrecentCSRF, s.Config.Origin}, {session, "wrong", s.Config.Origin}, {session, csrf, "https://foreign.invalid"}} {
		if got := keyPolicyHTTP(t, s.Engine(), "POST", path, `{}`, tc.session, tc.csrf, tc.origin, "", nil); got.Code != 403 {
			t.Fatal("procurement mutation skipped sensitive gate", got.Code)
		}
	}
	s.Config.RecoveryMode = true
	if got := keyPolicyHTTP(t, s.Engine(), "POST", path, `{}`, session, csrf, s.Config.Origin, "", nil); got.Code != 503 {
		t.Fatal("recovery accepted mutation", got.Code)
	}
	if got := keyPolicyHTTP(t, s.Engine(), "GET", path, "", unrecent, "", "", "", nil); got.Code != 200 {
		t.Fatal("recovery blocked read-only inspection", got.Code)
	}
	s.Config.RecoveryMode = false
	s.Guard = func(context.Context) error { return errors.New("private guard failure") }
	if got := keyPolicyHTTP(t, s.Engine(), "GET", path, "", session, "", "", "", nil); got.Code != 503 || strings.Contains(got.Body.String(), "private guard failure") {
		t.Fatal("guard bypassed or leaked")
	}
	s.Guard = nil
	s.Ready(false)
	if got := keyPolicyHTTP(t, s.Engine(), "GET", path, "", session, "", "", "", nil); got.Code != 503 {
		t.Fatal("unready procurement accepted")
	}
	s.Ready(true)
	authExec(t, pool, "UPDATE users SET mfa_enabled=true WHERE id=$1", admin)
	if got := keyPolicyHTTP(t, s.Engine(), "GET", path, "", session, "", "", "", nil); got.Code != 403 {
		t.Fatal("enabled MFA bypassed")
	}
}

func TestProcurementHTTPManagedCostWorkflowAndCustomerIsolation(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	session, csrf := probeAdmin(t, f)
	call := func(method, path, raw, sid, token string, headers http.Header) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, f.server.Engine(), method, "/api/platform/"+path, raw, sid, token, f.server.Config.Origin, "", headers)
	}
	create := procurement.CreateInput{OperationID: uuid.NewString(), Label: "shared supplier", Provider: "aggregate", State: "active", LimitAmount: "1000"}
	account := procurementAccountFromReceipt(t, procurementReceipt(t, call("POST", "procurement-accounts", procurementJSON(t, create), session, csrf, nil)))
	channel := probeChannels(t, f)[0]
	var credential, revision string
	if err := f.pool.QueryRow(context.Background(), `SELECT v.credential_id,c.management_revision::text FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, channel).Scan(&credential, &revision); err != nil {
		t.Fatal(err)
	}
	binding := procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: channel, CredentialID: credential, AccountID: account.ID, ExpectedChannelRevision: revision}
	if receipt := procurementReceipt(t, call("POST", "procurement-bindings", procurementJSON(t, binding), session, csrf, nil)); receipt.Status != "applied" {
		t.Fatal("credential not bound", receipt)
	}
	authExec(t, f.pool, `INSERT INTO cost_versions(channel_id,upstream_model,input_rate,output_rate,basis) VALUES($1,'provider-model',1000000,1000000,'contract')`, channel)
	response := f.request(t, context.Background(), false, false)
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || f.posts.Load() != 1 {
		t.Fatal("managed request failed", response.StatusCode, f.posts.Load())
	}
	var attempt string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM upstream_attempts WHERE request_id=$1`, response.Header.Get("X-Request-ID")).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	var operator, approver string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_operator'`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_approver'`).Scan(&approver); err != nil {
		t.Fatal(err)
	}
	operatorSession, operatorCSRF := authSession(t, f.server, f.pool, operator, "", false, true)
	approverSession, approverCSRF := authSession(t, f.server, f.pool, approver, "", false, true)
	legacy := fmt.Sprintf(`{"tenant_id":%q,"attempt_id":%q,"amount":"17","basis":"contract","evidence_ref":"contract:test"}`, f.tenant, attempt)
	if got := call("POST", "cost-corrections", legacy, operatorSession, operatorCSRF, http.Header{"Idempotency-Key": []string{"managed-correction"}}); got.Code != 409 || !strings.Contains(got.Body.String(), "procurement_approval_required") {
		t.Fatal("managed costs bypassed dual approval", got.Code, got.Body.String())
	}
	operation := uuid.NewString()
	proposal := fmt.Sprintf(`{"operation_id":%q,"tenant_id":%q,"attempt_id":%q,"amount":"17","basis":"contract","evidence_ref":"contract:test","reason":"verified supplier bill"}`, operation, f.tenant, attempt)
	got := call("POST", "procurement-cost-proposals", proposal, operatorSession, operatorCSRF, nil)
	var proposed billing.ProcurementCostProposal
	if got.Code != 200 || json.Unmarshal(authData(t, got), &proposed) != nil || proposed.ID != operation || proposed.State != "proposed" {
		t.Fatal("proposal not persisted", got.Code, got.Body.String())
	}
	if got = call("GET", "procurement-cost-proposals/"+operation, "", approverSession, "", nil); got.Code != 200 {
		t.Fatal("checker cannot inspect original operation", got.Code)
	}
	if got = call("GET", "procurement-cost-proposals?operation_id="+operation, "", session, "", nil); got.Code != 200 || !strings.Contains(got.Body.String(), operation) {
		t.Fatal("proposal operation query failed", got.Code, got.Body.String())
	}
	approvalOperation := uuid.NewString()
	approval := fmt.Sprintf(`{"operation_id":%q}`, approvalOperation)
	if got = call("POST", "procurement-cost-proposals/"+operation+"/approve", approval, operatorSession, operatorCSRF, nil); got.Code != 403 {
		t.Fatal("maker approved own proposal", got.Code)
	}
	for range 2 {
		got = call("POST", "procurement-cost-proposals/"+operation+"/approve", approval, approverSession, approverCSRF, nil)
		if got.Code != 200 || json.Unmarshal(authData(t, got), &proposed) != nil || proposed.State != "approved" || proposed.ApprovalOperationID == nil || *proposed.ApprovalOperationID != approvalOperation {
			t.Fatal("approval/retry did not return unique decision", got.Code, got.Body.String())
		}
	}
	var held, spent string
	if err := f.pool.QueryRow(context.Background(), `SELECT held_amount::text,spent_amount::text FROM procurement_accounts WHERE id=$1`, account.ID).Scan(&held, &spent); err != nil || held != "0" || spent != "17" {
		t.Fatal("dual approval did not post one supplier delta", held, spent, err)
	}
	state := f.state(t, response.Header.Get("X-Request-ID"))
	if state.charge != 20 || state.spent != 20 || state.held != 0 {
		t.Fatal("supplier correction changed customer funds", state)
	}
	for _, path := range []string{"procurement-bindings?account_id=" + account.ID, "procurement-accounts/" + account.ID + "/events", "procurement-accounts/" + account.ID + "/reservations"} {
		got = call("GET", path, "", operatorSession, "", nil)
		if got.Code != 200 || strings.Contains(got.Body.String(), "local-fixture-secret") || strings.Contains(got.Body.String(), "base_url") || strings.Contains(got.Body.String(), "digest") {
			t.Fatal("financial projection unsafe", path, got.Code, got.Body.String())
		}
	}
	if verify, err := f.server.Billing.VerifyLedger(context.Background(), billing.Query{}); err != nil || !verify.Balanced {
		t.Fatal("customer ledger changed", verify, err)
	}
	var owner string
	if err := f.pool.QueryRow(context.Background(), `SELECT owner_id FROM api_keys WHERE id=$1`, f.key).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	customer, _ := authSession(t, f.server, f.pool, owner, f.tenant, false, false)
	public := keyPolicyHTTP(t, f.server.Engine(), "GET", "/api/requests/"+response.Header.Get("X-Request-ID"), "", customer, "", "", "", nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), account.ID) || strings.Contains(public.Body.String(), "procurement") || strings.Contains(public.Body.String(), "cost_amount") {
		t.Fatal("customer request exposed supplier accounting", public.Code, public.Body.String())
	}
	// Lowering an account budget below existing costs is an allowed business
	// decision. It must deny a new attempt without changing old supplier facts
	// or stranding the customer's independent reservation.
	config := procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: "1", Label: create.Label, Provider: create.Provider, State: "active", LimitAmount: "0"}
	if receipt := procurementReceipt(t, call("PUT", "procurement-accounts/"+account.ID+"/configuration", procurementJSON(t, config), session, csrf, nil)); receipt.Status != "applied" {
		t.Fatal("budget tightening rejected below already spent", receipt)
	}
	blocked := f.request(t, context.Background(), false, false)
	_, _ = io.Copy(io.Discard, blocked.Body)
	_ = blocked.Body.Close()
	if blocked.StatusCode != 503 || f.posts.Load() != 1 {
		t.Fatal("exhausted supplier budget reached provider", blocked.StatusCode, f.posts.Load())
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT held_amount::text,spent_amount::text FROM procurement_accounts WHERE id=$1`, account.ID).Scan(&held, &spent); err != nil || held != "0" || spent != "17" {
		t.Fatal("failed procurement admission changed supplier facts", held, spent, err)
	}
	if state = f.state(t, response.Header.Get("X-Request-ID")); state.spent != 20 || state.held != 0 {
		t.Fatal("failed procurement admission stranded customer funds", state)
	}
}
