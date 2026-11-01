package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	pb "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type periodicHTTPActor struct{ id, session, csrf string }
type periodicHTTPFixture struct {
	ctx                                                                                     context.Context
	owner, app                                                                              *pgxpool.Pool
	server                                                                                  *Server
	engine                                                                                  http.Handler
	schema, tenant, otherTenant, model, price, key, memberKey, viewerKey, otherKey, account string
	actors                                                                                  map[string]periodicHTTPActor
	writes                                                                                  map[string]int
	lastWrite                                                                               map[string]time.Time
}

func periodicHTTPMust(t *testing.T, step string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatalf("%s (SQLSTATE %s)", step, pg.Code)
	}
	var safe *billing.Error
	if errors.As(err, &safe) {
		t.Fatalf("%s (safe code %s)", step, safe.Code)
	}
	t.Fatalf("%s (%T)", step, err)
}

// The inherited fixture supplies only isolated DDL and static catalog/session
// seeds through its owner. HTTP, Commander, Reader and complete financial
// funding below authenticate through a distinct restricted APP LOGIN.
func newPeriodicHTTPFixture(t *testing.T) *periodicHTTPFixture {
	t.Helper()
	if raw := os.Getenv("TEST_DATABASE_URL"); raw != "" {
		cfg, err := pgxpool.ParseConfig(raw)
		if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
			t.Fatal("periodic HTTP tests require the dedicated local moni_test database")
		}
	}
	base, owner := newAuthFixture(t)
	f := &periodicHTTPFixture{ctx: context.Background(), owner: owner, tenant: uuid.NewString(), otherTenant: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(), key: uuid.NewString(), memberKey: uuid.NewString(), viewerKey: uuid.NewString(), otherKey: uuid.NewString(), account: uuid.NewString(), actors: map[string]periodicHTTPActor{}, writes: map[string]int{}, lastWrite: map[string]time.Time{}}
	periodicHTTPMust(t, "read exact isolated HTTP schema", owner.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&f.schema))
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	appRole, projectionRole := "pb_http_app_"+suffix, "pb_http_projection_"+suffix
	var createdRoles []string
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// These freshly generated roles own no catalog or financial objects;
		// revoke their isolated grants before the inherited owner drops schema.
		for _, role := range createdRoles {
			if _, err := owner.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("isolated HTTP role grants cleanup failed")
			}
			if _, err := owner.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("isolated HTTP LOGIN cleanup failed")
			}
		}
	})
	for _, role := range []string{appRole, projectionRole} {
		_, err := owner.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS")
		periodicHTTPMust(t, "create exact isolated HTTP runtime LOGIN", err)
		createdRoles = append(createdRoles, role)
	}
	periodicHTTPMust(t, "provision latest real restricted HTTP roles", operations.ProvisionFor(f.ctx, owner, f.schema, appRole, projectionRole))
	cfg := owner.Config().Copy()
	cfg.ConnConfig.User, cfg.ConnConfig.Password = appRole, ""
	var err error
	f.app, err = pgxpool.NewWithConfig(f.ctx, cfg)
	periodicHTTPMust(t, "open actual HTTP APP LOGIN", err)
	var current, session string
	periodicHTTPMust(t, "attest actual HTTP APP authentication", f.app.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session))
	if current != appRole || session != appRole {
		t.Fatal("HTTP runtime did not authenticate as the restricted LOGIN")
	}
	f.server = New(base.Config, f.app, base.assets, nil)
	periodicHTTPMust(t, "initialize actual periodic HTTP services", f.server.InitializePeriodicBudgets())
	f.server.Ready(true)
	f.engine = f.server.Engine()
	for _, tenant := range []string{f.tenant, f.otherTenant} {
		_, err = owner.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'periodic-http-fixture')`, tenant)
		periodicHTTPMust(t, "seed static HTTP tenant", err)
	}
	for _, entry := range []struct{ name, platform, role, tenant string }{
		{"admin", "admin", "owner", f.tenant}, {"owner", "none", "owner", f.tenant}, {"member", "none", "member", f.tenant}, {"viewer", "none", "billing_viewer", f.tenant},
		{"operator", "finance_operator", "", ""}, {"approver", "finance_approver", "", ""}, {"other", "none", "owner", f.otherTenant},
	} {
		f.actors[entry.name] = f.actor(t, entry.platform, entry.role, entry.tenant, true)
	}
	_, err = owner.Exec(f.ctx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$1,'{"version":"v1","model":"model","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}','v1')`, f.model)
	periodicHTTPMust(t, "seed static HTTP model", err)
	_, err = owner.Exec(f.ctx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$3),($2,$3)`, f.tenant, f.otherTenant, f.model)
	periodicHTTPMust(t, "seed static HTTP model grants", err)
	for _, key := range []struct{ id, tenant, actor string }{{f.key, f.tenant, "owner"}, {f.memberKey, f.tenant, "member"}, {f.viewerKey, f.tenant, "viewer"}, {f.otherKey, f.otherTenant, "other"}} {
		_, err = owner.Exec(f.ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'periodic-http','fixture',$1,$4,1000000)`, key.id, key.tenant, f.actors[key.actor].id, []string{f.model})
		periodicHTTPMust(t, "seed static HTTP Key", err)
	}
	_, err = owner.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1000000,1000000,clock_timestamp()-interval '1 day',$3)`, f.price, f.model, f.actors["admin"].id)
	periodicHTTPMust(t, "seed static HTTP retail price", err)
	_, err = owner.Exec(f.ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'periodic-http','fixture','active',1000000)`, f.account)
	periodicHTTPMust(t, "seed static HTTP procurement catalog", err)
	op := uuid.NewString()
	credit, err := f.server.Billing.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.actors["operator"].id, Amount: 10000, BusinessRef: "periodic-http:" + op, ProofRef: "fixture:verified", IdempotencyKey: op, RequestHMAC: op})
	periodicHTTPMust(t, "APP create complete HTTP fixture funding", err)
	_, err = f.server.Billing.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.actors["approver"].id, OrderID: credit.ID})
	periodicHTTPMust(t, "APP independently approve complete HTTP fixture funding", err)
	f.finance(t)
	return f
}

func (f *periodicHTTPFixture) actor(t *testing.T, platform, role, tenant string, recent bool) periodicHTTPActor {
	t.Helper()
	a := periodicHTTPActor{id: uuid.NewString()}
	_, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-fixture',$3)`, a.id, a.id+"@fixture.invalid", platform)
	periodicHTTPMust(t, "seed isolated static HTTP actor", err)
	if role != "" {
		_, err = f.owner.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,$3,true)`, tenant, a.id, role)
		periodicHTTPMust(t, "seed static HTTP membership", err)
	}
	a.session, a.csrf = security.Random(32), security.Random(24)
	var tenantArg any
	if tenant != "" {
		tenantArg = tenant
	}
	family := uuid.NewString()
	_, err = f.owner.Exec(f.ctx, `INSERT INTO session_families(id,user_id) VALUES($1,$2)`, family, a.id)
	periodicHTTPMust(t, "seed isolated static HTTP session family", err)
	_, err = f.owner.Exec(f.ctx, `INSERT INTO sessions(digest,user_id,tenant_id,csrf_token,mfa_verified,reauthenticated_at,expires_at,family_id) VALUES($1,$2,$3,$4,false,CASE WHEN $5 THEN clock_timestamp() ELSE NULL END,clock_timestamp()+interval '1 hour',$6)`, f.server.Config.Secrets.Digest(a.session), a.id, tenantArg, a.csrf, recent, family)
	periodicHTTPMust(t, "seed isolated static HTTP session without printing private row errors", err)
	return a
}

func (f *periodicHTTPFixture) call(t *testing.T, method, path, raw string, a periodicHTTPActor, headers http.Header, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://console.test"+path, strings.NewReader(raw))
	if raw != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header[k] = append([]string(nil), v...)
	}
	if a.session != "" {
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: a.session})
	}
	if a.csrf != "" {
		req.Header.Set("X-CSRF-Token", a.csrf)
	}
	req.Header.Set("Origin", f.server.Config.Origin)
	for _, change := range mutate {
		change(req)
	}
	w := httptest.NewRecorder()
	f.engine.ServeHTTP(w, req)
	return w
}

func periodicHTTPJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	periodicHTTPMust(t, "encode HTTP fixture JSON", err)
	return string(raw)
}
func periodicHTTPInput(expected, limit string) pb.PutPeriodicBudget {
	return pb.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: expected, PeriodKind: "utc_day", Enabled: true, LimitAmount: limit}
}

func (f *periodicHTTPFixture) put(t *testing.T, a periodicHTTPActor, path string, in pb.PutPeriodicBudget) *httptest.ResponseRecorder {
	t.Helper()
	// Preserve the actual actor bucket. Initial burst is two; later requests
	// wait for a real token rather than resetting admission to bypass limits.
	if f.writes[a.id] >= 2 {
		if delay := time.Until(f.lastWrite[a.id].Add(1050 * time.Millisecond)); delay > 0 {
			timer := time.NewTimer(delay)
			<-timer.C
		}
	}
	f.writes[a.id]++
	f.lastWrite[a.id] = time.Now()
	return f.call(t, http.MethodPut, path, periodicHTTPJSON(t, in), a, nil)
}

func periodicHTTPStatus(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("periodic HTTP status=%d want=%d or missing no-store", w.Code, status)
	}
	if code != "" {
		var v pb.ErrorResponse
		d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
		d.DisallowUnknownFields()
		periodicHTTPMust(t, "strictly decode safe HTTP failure", d.Decode(&v))
		if d.Decode(new(any)) != io.EOF {
			t.Fatal("HTTP failure envelope has a trailing value")
		}
		if v.Error.Code != code || v.RequestID == "" || v.Error.Message == "" {
			t.Fatalf("periodic HTTP safe code=%q want=%q or missing safe envelope", v.Error.Code, code)
		}
	}
}

func periodicHTTPData[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	periodicHTTPStatus(t, w, status, "")
	var v pb.SingleResponse[T]
	d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	d.DisallowUnknownFields()
	periodicHTTPMust(t, "strictly decode complete single HTTP response", d.Decode(&v))
	if d.Decode(new(any)) != io.EOF || v.RequestID == "" {
		t.Fatal("single HTTP envelope is truncated, trailing or missing request ID")
	}
	return v.Data
}

func periodicHTTPPage[T interface{ Validate() error }](t *testing.T, w *httptest.ResponseRecorder) pb.PageResponse[T] {
	t.Helper()
	periodicHTTPStatus(t, w, 200, "")
	var fields map[string]json.RawMessage
	periodicHTTPMust(t, "decode exact HTTP page field set", json.Unmarshal(w.Body.Bytes(), &fields))
	if len(fields) != 3 || fields["data"] == nil || fields["next_cursor"] == nil || fields["request_id"] == nil {
		t.Fatal("HTTP page envelope lost its exact field set")
	}
	var page pb.PageResponse[T]
	d := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	d.DisallowUnknownFields()
	periodicHTTPMust(t, "strictly decode complete HTTP page", d.Decode(&page))
	if d.Decode(new(any)) != io.EOF || page.RequestID == "" || page.Data == nil {
		t.Fatal("HTTP page is truncated, trailing, null or missing request ID")
	}
	for _, row := range page.Data {
		periodicHTTPMust(t, "validate every HTTP page row", row.Validate())
	}
	return page
}

// This exact-schema DDL is solely an explicit test clock. HTTP authorization,
// configuration, source funds and allocations still use the real APP LOGIN.
func (f *periodicHTTPFixture) setClock(t *testing.T, at time.Time) {
	t.Helper()
	query := "CREATE OR REPLACE FUNCTION " + pgx.Identifier{f.schema, "pb_now"}.Sanitize() + "() RETURNS timestamptz LANGUAGE sql VOLATILE SET search_path TO " + pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp AS $$ SELECT '" + at.UTC().Format("2006-01-02T15:04:05.000000Z") + "'::timestamptz $$"
	_, err := f.owner.Exec(f.ctx, query)
	periodicHTTPMust(t, "set only the isolated HTTP test clock", err)
}

func (f *periodicHTTPFixture) receipt(t *testing.T, w *httptest.ResponseRecorder, status int, a periodicHTTPActor, path, kind, scope string, in pb.PutPeriodicBudget) pb.CommandReceipt {
	t.Helper()
	v := periodicHTTPData[pb.CommandReceipt](t, w, status)
	periodicHTTPMust(t, "validate exact HTTP operation receipt", v.Validate())
	fp, err := pb.Fingerprint(kind, a.id, scope, path, in)
	periodicHTTPMust(t, "compute independently expected HTTP binding", err)
	if v.Operation.OperationID != in.OperationID || v.Operation.Actor != a.id || v.Operation.Target != path || v.Operation.Kind != kind || v.Operation.Scope != scope || v.Operation.InputFingerprint != fp || v.Operation.ResourceID == v.Result.ScopeID {
		t.Fatal("HTTP receipt lost operation identity, fingerprint or distinct stable control binding")
	}
	return v
}

func (f *periodicHTTPFixture) snapshot(t *testing.T) string {
	t.Helper()
	var snapshots []string
	for _, table := range []string{"request_idempotency", "request_records", "reservations", "settlements", "journals", "journal_lines", "ledger_accounts", "credit_orders", "funding_receipts", "api_keys", "recovery_jobs", "projection_outbox", "audit_events", "procurement_accounts", "procurement_reservations", "procurement_budget_events", "procurement_evidence_identities", "procurement_commands", "procurement_cost_proposals", "procurement_balance_observations", "periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "periodic_budget_operations", "periodic_budget_configuration_versions"} {
		var raw string
		periodicHTTPMust(t, "snapshot exact HTTP financial and operation facts", f.app.QueryRow(f.ctx, "SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb)::text FROM "+pgx.Identifier{table}.Sanitize()+" x").Scan(&raw))
		snapshots = append(snapshots, raw)
	}
	return strings.Join(snapshots, "\n")
}

func (f *periodicHTTPFixture) finance(t *testing.T) {
	t.Helper()
	v, err := f.server.Billing.VerifyLedger(f.ctx, billing.Query{})
	periodicHTTPMust(t, "verify actual HTTP fixture financial conservation", err)
	if !v.Balanced {
		t.Fatal("HTTP fixture financial sources and ledger do not conserve")
	}
}

func TestPeriodicBudgetHTTPRestrictedLifecycleAndOriginalReceipts(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	admin := f.actors["admin"]
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	view := periodicHTTPData[pb.PeriodicBudgetView](t, f.call(t, "GET", path, "", admin, nil), 200)
	periodicHTTPMust(t, "validate unconfigured HTTP view", view.Validate())
	if view.State != "unconfigured" {
		t.Fatal("new migration unexpectedly configured a budget")
	}
	firstIn := periodicHTTPInput("0", "9007199254740993")
	first := f.receipt(t, f.put(t, admin, path, firstIn), 201, admin, path, "tenant_periodic_budget.configure", "platform", firstIn)
	before := f.snapshot(t)
	replay := f.receipt(t, f.put(t, admin, path, firstIn), 200, admin, path, "tenant_periodic_budget.configure", "platform", firstIn)
	if !reflect.DeepEqual(first, replay) || before != f.snapshot(t) {
		t.Fatal("HTTP replay changed immutable original facts")
	}
	noopIn := periodicHTTPInput("1", "9007199254740993")
	noop := f.receipt(t, f.put(t, admin, path, noopIn), 200, admin, path, "tenant_periodic_budget.configure", "platform", noopIn)
	if noop.Operation.Revision != "1" || noop.Operation.ResourceID != first.Operation.ResourceID {
		t.Fatal("HTTP no-op changed revision or stable control")
	}
	changedIn := periodicHTTPInput("1", "999")
	changed := f.receipt(t, f.put(t, admin, path, changedIn), 200, admin, path, "tenant_periodic_budget.configure", "platform", changedIn)
	if changed.Operation.Revision != "2" {
		t.Fatal("actual HTTP change did not increment revision")
	}
	for _, in := range []pb.PutPeriodicBudget{periodicHTTPInput("1", "999"), func() pb.PutPeriodicBudget { v := periodicHTTPInput("2", "999"); v.PeriodKind = "utc_month"; return v }(), func() pb.PutPeriodicBudget { v := firstIn; v.LimitAmount = "1"; return v }()} {
		before = f.snapshot(t)
		code := "periodic_budget_conflict"
		if in.PeriodKind == "utc_month" {
			code = "periodic_calendar_immutable"
		}
		if in.OperationID == firstIn.OperationID {
			code = "policy_operation_conflict"
		}
		periodicHTTPStatus(t, f.put(t, admin, path, in), 409, code)
		if before != f.snapshot(t) {
			t.Fatal("HTTP known failure retained business facts")
		}
	}
	// Use the actual restricted APP billing path to change held totals. An
	// operation GET must still return the old immutable result, not current view.
	r, err := f.server.Billing.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.actors["owner"].id, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "periodic-http-flow", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(300 * time.Second)})
	periodicHTTPMust(t, "actual APP funds flow after original HTTP receipt", err)
	old := periodicHTTPData[pb.CommandReceipt](t, f.call(t, "GET", "/api/platform/periodic-budget-operations/"+firstIn.OperationID, "", admin, nil), 200)
	if !reflect.DeepEqual(old, first) {
		t.Fatal("operation HTTP lookup replaced original result with current configuration/traffic")
	}
	periodicHTTPStatus(t, f.call(t, "GET", "/api/periodic-budget-operations/"+firstIn.OperationID, "", f.actors["owner"], nil), 404, "not_found")
	_, err = f.server.Billing.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	periodicHTTPMust(t, "complete real HTTP fixture funds flow", err)
	f.finance(t)
}

func TestPeriodicBudgetHTTPStrictBodiesAndTransport(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	valid := periodicHTTPJSON(t, periodicHTTPInput("0", "100"))
	cases := []struct {
		name, body, path string
		headers          http.Header
		status           int
	}{
		{"array", "[]", path, nil, 400}, {"null", "null", path, nil, 400}, {"missing", "{}", path, nil, 400},
		{"duplicate", strings.Replace(valid, `"operation_id":`, `"operation_id":"11111111-1111-4111-8111-111111111111","operation_id":`, 1), path, nil, 400},
		{"escaped_duplicate", strings.Replace(valid, `"operation_id":`, `"oper\u0061tion_id":"11111111-1111-4111-8111-111111111111","operation_id":`, 1), path, nil, 400},
		{"alias", strings.Replace(valid, `"operation_id"`, `"Operation_id"`, 1), path, nil, 400},
		{"unknown", strings.Replace(valid, `{`, `{"actor":"11111111-1111-4111-8111-111111111111",`, 1), path, nil, 400},
		{"number_amount", strings.Replace(valid, `"limit_amount":"100"`, `"limit_amount":100`, 1), path, nil, 400},
		{"number_revision", strings.Replace(valid, `"expected_revision":"0"`, `"expected_revision":0`, 1), path, nil, 400},
		{"null_enabled", strings.Replace(valid, `"enabled":true`, `"enabled":null`, 1), path, nil, 400},
		{"noncanonical_amount", strings.Replace(valid, `"limit_amount":"100"`, `"limit_amount":"0100"`, 1), path, nil, 400},
		{"amount_overflow", strings.Replace(valid, `"limit_amount":"100"`, `"limit_amount":"9223372036854775808"`, 1), path, nil, 400},
		{"period_space", strings.Replace(valid, `"utc_day"`, `"utc_day "`, 1), path, nil, 400},
		{"surrogate", strings.Replace(valid, `"utc_day"`, `"\ud800"`, 1), path, nil, 400},
		{"bad_utf8", strings.Replace(valid, `"utc_day"`, "\"\xff\"", 1), path, nil, 400},
		{"trailing", valid + " {}", path, nil, 400},
		{"8193", valid + strings.Repeat(" ", 8193-len(valid)), path, nil, 413},
		{"wrong_media", valid, path, http.Header{"Content-Type": {"text/plain"}}, 400},
		{"missing_media", valid, path, http.Header{"Content-Type": {}}, 400},
		{"wrong_charset", valid, path, http.Header{"Content-Type": {"application/json; charset=latin1"}}, 400},
		{"unknown_media_param", valid, path, http.Header{"Content-Type": {"application/json; profile=test"}}, 400},
		{"duplicate_media", valid, path, http.Header{"Content-Type": {"application/json", "text/plain"}}, 400},
		{"compressed", valid, path, http.Header{"Content-Encoding": {"gzip"}}, 400},
		{"duplicate_encoding", valid, path, http.Header{"Content-Encoding": {"identity", "gzip"}}, 400},
		{"query", valid, path + "?tenant=x", nil, 400}, {"force_query", valid, path + "?", nil, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := f.actor(t, "admin", "", "", true)
			before := f.snapshot(t)
			w := f.call(t, "PUT", test.path, test.body, a, test.headers)
			code := "invalid_request"
			if test.status == 413 {
				code = "request_too_large"
			}
			periodicHTTPStatus(t, w, test.status, code)
			if before != f.snapshot(t) {
				t.Fatal("strictly rejected HTTP input left business facts")
			}
		})
	}
	a := f.actor(t, "admin", "", "", true)
	in := periodicHTTPInput("0", "9223372036854775807")
	raw := periodicHTTPJSON(t, in)
	raw += strings.Repeat(" ", 8192-len(raw))
	f.receipt(t, f.call(t, "PUT", path, raw, a, http.Header{"Content-Type": {"application/json; charset=utf-8"}, "Content-Encoding": {"identity"}}), 201, a, path, "tenant_periodic_budget.configure", "platform", in)
}

func TestPeriodicBudgetHTTPStrictQueriesEmptyPagesAndNoReadFacts(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	base := "/api/wallet/periodic-budget"
	cases := []struct {
		name, path, body, code string
		mutate                 func(*http.Request)
	}{
		{"view_query", base + "?cursor=a", "", "invalid_request", nil},
		{"empty_question", base + "?", "", "invalid_request", nil},
		{"empty_list_question", base + "/windows?", "", "invalid_request", nil},
		{"get_body", base, "x", "invalid_request", nil},
		{"get_transfer", base, "", "invalid_request", func(r *http.Request) { r.TransferEncoding = []string{"chunked"} }},
		{"hidden_body", base, "x", "invalid_request", func(r *http.Request) { r.ContentLength = 0 }},
		{"unknown_query", base + "/windows?limit=50", "", "invalid_request", nil},
		{"empty_cursor", base + "/windows?cursor=", "", "invalid_request", nil},
		{"duplicate_cursor", base + "/windows?cursor=a&cursor=b", "", "invalid_request", nil},
		{"bad_escape", base + "/windows?cursor=%xx", "", "invalid_request", nil},
		{"raw_bad_utf8", base + "/windows", "", "invalid_request", func(r *http.Request) { r.URL.RawQuery = "cursor=\xff" }},
		{"raw_control", base + "/windows", "", "invalid_request", func(r *http.Request) { r.URL.RawQuery = "cursor=\x01" }},
		{"decoded_bad_utf8", base + "/windows?cursor=%FF", "", "invalid_request", nil},
		{"decoded_control", base + "/windows?cursor=%00", "", "invalid_request", nil},
		{"decoded_key_control", base + "/windows?cur%00sor=x", "", "invalid_request", nil},
		{"4097_raw", base + "/windows?cursor=" + strings.Repeat("a", 4090), "", "invalid_request", nil},
		{"invalid_cursor", base + "/windows?cursor=pbc1.a.a", "", "invalid_cursor", nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			a := f.actor(t, "none", "owner", f.tenant, true)
			before := f.snapshot(t)
			var changes []func(*http.Request)
			if test.mutate != nil {
				changes = append(changes, test.mutate)
			}
			periodicHTTPStatus(t, f.call(t, "GET", test.path, test.body, a, nil, changes...), 400, test.code)
			if before != f.snapshot(t) {
				t.Fatal("HTTP read/query rejection changed business facts")
			}
		})
	}
	for _, suffix := range []string{"/windows", "/allocations"} {
		a := f.actor(t, "none", "owner", f.tenant, true)
		before := f.snapshot(t)
		w := f.call(t, "GET", base+suffix, "", a, nil)
		if suffix == "/windows" {
			page := periodicHTTPPage[pb.BudgetWindowView](t, w)
			if len(page.Data) != 0 || page.NextCursor != "" {
				t.Fatal("empty HTTP windows page lost []/empty cursor values")
			}
		} else {
			page := periodicHTTPPage[pb.BudgetAllocationView](t, w)
			if len(page.Data) != 0 || page.NextCursor != "" {
				t.Fatal("empty HTTP allocations page lost []/empty cursor values")
			}
		}
		if before != f.snapshot(t) {
			t.Fatal("empty HTTP history read created control/window/operation facts")
		}
	}
	admin := f.actor(t, "admin", "", "", true)
	periodicHTTPStatus(t, f.call(t, "GET", "/api/platform/tenants/INVALID/periodic-budget", "", admin, nil), 400, "invalid_request")
	periodicHTTPStatus(t, f.call(t, "GET", "/api/platform/periodic-budget-operations/"+uuid.NewString()+"?cursor=x", "", admin, nil), 400, "invalid_request")
}

func TestPeriodicBudgetHTTPAllocationPagesFromRealBillingSources(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	admin := f.actors["admin"]
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	var now time.Time
	periodicHTTPMust(t, "read real database clock for explicit HTTP test clock", f.app.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now))
	now = now.UTC().Truncate(time.Microsecond)
	f.setClock(t, now.AddDate(0, 0, -2))
	in := periodicHTTPInput("0", "100000")
	f.receipt(t, f.put(t, admin, path, in), 201, admin, path, "tenant_periodic_budget.configure", "platform", in)
	f.setClock(t, now.AddDate(0, 0, -1))
	requests := map[string]bool{}
	for i := 0; i < 51; i++ {
		r, err := f.server.Billing.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.actors["owner"].id, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "periodic-http-page", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(300 * time.Second)})
		periodicHTTPMust(t, "create complete real APP billing source for HTTP pagination", err)
		if r.Amount != 120 || requests[r.RequestID] {
			t.Fatal("HTTP pagination fixture lost exact complete R or source uniqueness")
		}
		requests[r.RequestID] = true
	}
	f.finance(t)
	before := f.snapshot(t)
	first := periodicHTTPPage[pb.BudgetAllocationView](t, f.call(t, "GET", path+"/allocations", "", admin, nil))
	if len(first.Data) != 50 || first.NextCursor == "" {
		t.Fatal("HTTP allocation first page did not retain the 51st-row boundary")
	}
	codec, err := pb.NewCursorCodec(f.server.Config.Secrets.Pepper)
	periodicHTTPMust(t, "construct verifier for the returned opaque HTTP cursor", err)
	position, err := codec.Decode(pb.CursorBinding{Route: "platform_tenant_allocations", Scope: "tenant", ScopeID: f.tenant, IdentityScope: "platform", Order: "created_at_desc_id_c_desc"}, first.NextCursor)
	periodicHTTPMust(t, "decode HTTP boundary only against its actual route", err)
	if position.ID != first.Data[49].ID || position.At != first.Data[49].CreatedAt {
		t.Fatal("HTTP cursor did not originate at the 50th returned row")
	}
	periodicHTTPStatus(t, f.call(t, "GET", "/api/wallet/periodic-budget/allocations?cursor="+first.NextCursor, "", f.actors["owner"], nil), 400, "invalid_cursor")
	periodicHTTPStatus(t, f.call(t, "GET", path+"/windows?cursor="+first.NextCursor, "", admin, nil), 400, "invalid_cursor")
	periodicHTTPStatus(t, f.call(t, "GET", "/api/platform/tenants/"+f.otherTenant+"/periodic-budget/allocations?cursor="+first.NextCursor, "", admin, nil), 400, "invalid_cursor")
	if before != f.snapshot(t) {
		t.Fatal("HTTP allocation first page or wrong-boundary requests changed business facts")
	}
	seen := map[string]bool{}
	for _, row := range first.Data {
		if !requests[row.SubjectID] || row.SubjectKind != "customer_request" || row.UpperAmount != "120" || row.HeldAmount != "120" || row.State != "held" || seen[row.SubjectID] {
			t.Fatal("HTTP page lost the real source identity, exact financial values or uniqueness")
		}
		seen[row.SubjectID] = true
	}
	var lastRequest string
	for request := range requests {
		if !seen[request] {
			if lastRequest != "" {
				t.Fatal("HTTP first page omitted more than the one next-page source")
			}
			lastRequest = request
		}
	}
	if lastRequest == "" {
		t.Fatal("HTTP first page unexpectedly consumed all 51 sources")
	}
	// The second RR page may observe a completed financial transition after
	// page one. The cursor is an ordering boundary, never a snapshot promise.
	_, err = f.server.Billing.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: lastRequest, Reason: "not_sent"})
	periodicHTTPMust(t, "release the exact source after first HTTP page", err)
	before = f.snapshot(t)
	last := periodicHTTPPage[pb.BudgetAllocationView](t, f.call(t, "GET", path+"/allocations?cursor="+first.NextCursor, "", admin, nil))
	if len(last.Data) != 1 || last.NextCursor != "" || last.Data[0].SubjectID != lastRequest || last.Data[0].State != "released" || last.Data[0].HeldAmount != "0" || last.Data[0].SpentAmount != "0" {
		t.Fatal("HTTP final page lost the remaining source or its fresh released state")
	}
	all := append(first.Data, last.Data...)
	for i := 1; i < len(all); i++ {
		if all[i].CreatedAt != all[0].CreatedAt || all[i].ID >= all[i-1].ID {
			t.Fatal("HTTP tied allocation timestamps lost descending C UUID order")
		}
	}
	if before != f.snapshot(t) {
		t.Fatal("HTTP final allocation page changed financial, procurement or operation facts")
	}
	for request := range requests {
		if request == lastRequest {
			continue
		}
		_, err = f.server.Billing.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: request, Reason: "not_sent"})
		periodicHTTPMust(t, "finish complete real APP pagination sources", err)
	}
	f.finance(t)
}

func TestPeriodicBudgetHTTPCurrentPermissionsAndIsolation(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	platformTenant := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	platformAccount := "/api/platform/procurement-accounts/" + f.account + "/periodic-budget"
	keyPath := func(id string) string { return "/api/keys/" + id + "/periodic-budget" }
	before := f.snapshot(t)
	for _, name := range []string{"admin", "operator", "approver"} {
		for _, path := range []string{platformTenant, platformAccount} {
			periodicHTTPStatus(t, f.call(t, "GET", path, "", f.actors[name], nil), 200, "")
		}
	}
	for _, test := range []struct {
		actor, path string
		status      int
	}{
		{"owner", "/api/wallet/periodic-budget", 200}, {"member", "/api/wallet/periodic-budget", 403}, {"viewer", "/api/wallet/periodic-budget", 200},
		{"owner", keyPath(f.memberKey), 200}, {"member", keyPath(f.memberKey), 200}, {"member", keyPath(f.key), 404},
		{"viewer", keyPath(f.viewerKey), 200}, {"viewer", keyPath(f.key), 404}, {"owner", keyPath(f.otherKey), 404},
		{"other", keyPath(f.key), 404}, {"owner", keyPath(uuid.NewString()), 404}, {"member", platformTenant, 403}, {"operator", "/api/wallet/periodic-budget", 403},
	} {
		code := ""
		if test.status == 403 {
			code = "forbidden"
		}
		if test.status == 404 {
			code = "not_found"
		}
		periodicHTTPStatus(t, f.call(t, "GET", test.path, "", f.actors[test.actor], nil), test.status, code)
	}
	if before != f.snapshot(t) {
		t.Fatal("authorized or denied HTTP reads created budget or business facts")
	}
	for _, name := range []string{"operator", "approver"} {
		periodicHTTPStatus(t, f.put(t, f.actors[name], platformTenant, periodicHTTPInput("0", "100")), 403, "forbidden")
	}
	owner, member, viewer := f.actors["owner"], f.actors["member"], f.actors["viewer"]
	in := periodicHTTPInput("0", "100")
	first := f.receipt(t, f.put(t, owner, keyPath(f.memberKey), in), 201, owner, keyPath(f.memberKey), "key_periodic_budget.configure", f.tenant, in)
	in = periodicHTTPInput("1", "100")
	f.receipt(t, f.put(t, member, keyPath(f.memberKey), in), 200, member, keyPath(f.memberKey), "key_periodic_budget.configure", f.tenant, in)
	periodicHTTPStatus(t, f.put(t, member, keyPath(f.key), periodicHTTPInput("0", "100")), 404, "not_found")
	periodicHTTPStatus(t, f.put(t, viewer, keyPath(f.viewerKey), periodicHTTPInput("0", "100")), 403, "forbidden")
	periodicHTTPStatus(t, f.put(t, f.actors["other"], keyPath(f.memberKey), periodicHTTPInput("1", "100")), 404, "not_found")
	periodicHTTPStatus(t, f.call(t, "GET", "/api/platform/periodic-budget-operations/"+first.Operation.OperationID, "", f.actors["admin"], nil), 404, "not_found")
	periodicHTTPStatus(t, f.call(t, "GET", "/api/periodic-budget-operations/"+first.Operation.OperationID, "", f.actors["other"], nil), 404, "not_found")
	_, err := f.app.Exec(f.ctx, `UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1`, f.memberKey)
	periodicHTTPMust(t, "revoke actual target Key", err)
	periodicHTTPStatus(t, f.call(t, "GET", keyPath(f.memberKey), "", member, nil), 200, "")
	periodicHTTPStatus(t, f.put(t, member, keyPath(f.memberKey), periodicHTTPInput("1", "100")), 403, "forbidden")
	_, err = f.app.Exec(f.ctx, `UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 day' WHERE id=$1`, f.key)
	periodicHTTPMust(t, "expire actual Key calling eligibility", err)
	periodicHTTPStatus(t, f.call(t, "GET", keyPath(f.key), "", owner, nil), 200, "")
	in = periodicHTTPInput("0", "100")
	f.receipt(t, f.put(t, owner, keyPath(f.key), in), 201, owner, keyPath(f.key), "key_periodic_budget.configure", f.tenant, in)
	_, err = f.app.Exec(f.ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, owner.id)
	periodicHTTPMust(t, "revoke current HTTP membership", err)
	periodicHTTPStatus(t, f.call(t, "GET", keyPath(f.key), "", owner, nil), 403, "forbidden")
	_, err = f.app.Exec(f.ctx, `UPDATE users SET status='suspended' WHERE id=$1`, member.id)
	periodicHTTPMust(t, "disable current session user", err)
	periodicHTTPStatus(t, f.call(t, "GET", keyPath(f.memberKey), "", member, nil), 401, "invalid_credentials")
}

func TestPeriodicBudgetHTTPSessionWriteGuardsAndFreshOwnership(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	admin := f.actors["admin"]
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	body := periodicHTTPJSON(t, periodicHTTPInput("0", "100"))
	before := f.snapshot(t)
	periodicHTTPStatus(t, f.call(t, "GET", path, "", periodicHTTPActor{}, http.Header{"Authorization": {"Bearer unused-fixture-api-key"}}), 401, "invalid_credentials")
	periodicHTTPStatus(t, f.call(t, "PUT", path, body, admin, nil, func(r *http.Request) { r.Header.Del("X-CSRF-Token") }), 403, "csrf_invalid")
	periodicHTTPStatus(t, f.call(t, "PUT", path, body, admin, nil, func(r *http.Request) { r.Header.Set("Origin", "http://other.invalid") }), 403, "csrf_invalid")
	unrecent := f.actor(t, "admin", "", "", false)
	periodicHTTPStatus(t, f.call(t, "PUT", path, body, unrecent, nil), 403, "reauth_required")
	// Default MFA remains optional. Enabling the existing deployment policy
	// exercises its normal session middleware without changing B authority.
	periodicHTTPStatus(t, f.call(t, "GET", path, "", admin, nil), 200, "")
	f.server.Identity.EnforcePlatformMFA = true
	periodicHTTPStatus(t, f.call(t, "GET", path, "", admin, nil), 403, "mfa_required")
	f.server.Identity.EnforcePlatformMFA = false
	f.server.Config.RecoveryMode = true
	periodicHTTPStatus(t, f.call(t, "PUT", path, body, admin, nil), 503, "recovery_mode")
	periodicHTTPStatus(t, f.call(t, "GET", path, "", admin, nil), 200, "")
	f.server.Config.RecoveryMode = false
	var calls atomic.Int32
	f.server.Guard = func(context.Context) error { calls.Add(1); return nil }
	a := f.actor(t, "admin", "", "", true)
	periodicHTTPStatus(t, f.call(t, "GET", path, "", a, nil), 200, "")
	if calls.Load() != 2 {
		t.Fatal("B read did not check ownership both before authentication and after its own admission")
	}
	calls.Store(0)
	f.server.Guard = func(context.Context) error {
		if calls.Add(1) == 2 {
			return errors.New("private ownership error")
		}
		return nil
	}
	a = f.actor(t, "admin", "", "", true)
	periodicHTTPStatus(t, f.call(t, "GET", path, "", a, nil), 503, "instance_unavailable")
	if calls.Load() != 2 || f.server.ready.Load() {
		t.Fatal("fresh B ownership failure did not fence before repository use")
	}
	f.server.Guard = nil
	f.server.Ready(false)
	periodicHTTPStatus(t, f.call(t, "GET", path, "", admin, nil), 503, "instance_unavailable")
	f.server.Ready(true)
	if before != f.snapshot(t) {
		t.Fatal("session, write or fresh ownership guard rejection created business facts")
	}
	f.server.PeriodicReader = nil
	f.server.PeriodicCommander = nil
	a = f.actor(t, "admin", "", "", true)
	periodicHTTPStatus(t, f.call(t, "GET", path, "", a, nil), 503, "periodic_budget_unavailable")
	periodicHTTPStatus(t, f.call(t, "PUT", path, body, a, nil), 503, "periodic_budget_unavailable")
}

type periodicHTTPBrokenResponse struct {
	header         http.Header
	status, writes int
}

func (w *periodicHTTPBrokenResponse) Header() http.Header    { return w.header }
func (w *periodicHTTPBrokenResponse) WriteHeader(status int) { w.status = status }
func (w *periodicHTTPBrokenResponse) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestPeriodicBudgetHTTPCommittedOperationCanBeCheckedAfterResponseFailure(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	admin := f.actors["admin"]
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	in := periodicHTTPInput("0", "100")
	req := httptest.NewRequest("PUT", "http://console.test"+path, strings.NewReader(periodicHTTPJSON(t, in)))
	req.AddCookie(&http.Cookie{Name: "moni_session", Value: admin.session})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", admin.csrf)
	req.Header.Set("Origin", f.server.Config.Origin)
	broken := &periodicHTTPBrokenResponse{header: make(http.Header)}
	f.engine.ServeHTTP(broken, req)
	if broken.status != 201 || broken.writes != 1 {
		t.Fatal("response failure did not occur on the first complete committed success response")
	}
	// This is a ResponseWriter fault after a confirmed repository COMMIT, not
	// a claim that a real database COMMIT reply was lost on the wire.
	w := f.call(t, "GET", "/api/platform/periodic-budget-operations/"+in.OperationID, "", admin, nil)
	receipt := periodicHTTPData[pb.CommandReceipt](t, w, 200)
	periodicHTTPMust(t, "validate original operation after failed HTTP delivery", receipt.Validate())
	fp, err := pb.Fingerprint("tenant_periodic_budget.configure", admin.id, "platform", path, in)
	periodicHTTPMust(t, "bind recovered original operation", err)
	if receipt.Operation.OperationID != in.OperationID || receipt.Operation.InputFingerprint != fp || receipt.Operation.Actor != admin.id || receipt.Operation.Target != path {
		t.Fatal("lost response lookup did not recover the original operation identity")
	}
	var exact bool
	periodicHTTPMust(t, "verify response failure never reran or rolled back committed business", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 FROM periodic_budget_operations) AND (SELECT count(*)=1 FROM periodic_budget_controls)
 AND (SELECT count(*)=1 FROM periodic_budget_configuration_versions)
 AND (SELECT count(*)=1 FROM audit_events WHERE action='periodic_budget.configured')`).Scan(&exact))
	if !exact {
		t.Fatal("HTTP response failure duplicated or lost already committed operation facts")
	}
}

func TestPeriodicBudgetHTTPDisconnectDuringCommitReturnsUnknownWithoutReceipt(t *testing.T) {
	f := newPeriodicHTTPFixture(t)
	admin := f.actors["admin"]
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pb_http_commit_pause() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_catalog.pg_sleep(30);RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER pb_http_commit_pause AFTER INSERT ON periodic_budget_operations DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_http_commit_pause()`)
	periodicHTTPMust(t, "install isolated explicit COMMIT fault boundary", err)
	before := f.snapshot(t)
	path := "/api/platform/tenants/" + f.tenant + "/periodic-budget"
	in := periodicHTTPInput("0", "100")
	raw := periodicHTTPJSON(t, in)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- f.call(t, "PUT", path, raw, admin, nil) }()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var pid int32
	for pid == 0 {
		// The schema and runtime role are known from the actual pool; no broad
		// termination or SQL/body text search can select another fixture.
		periodicHTTPMust(t, "observe exact runtime role COMMIT fault", f.owner.QueryRow(ctx, `SELECT coalesce((SELECT pid FROM pg_stat_activity WHERE usename=$1 AND upper(btrim(query))='COMMIT' AND state='active' AND wait_event_type='Timeout' AND wait_event='PgSleep' LIMIT 1),0)`, f.app.Config().ConnConfig.User).Scan(&pid))
		if pid == 0 {
			select {
			case <-ctx.Done():
				t.Fatal("HTTP command did not reach the observed COMMIT fault boundary")
			case <-tick.C:
			}
		}
	}
	var killed bool
	periodicHTTPMust(t, "disconnect only the observed actual HTTP COMMIT backend", f.owner.QueryRow(f.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&killed))
	if !killed {
		t.Fatal("observed HTTP COMMIT termination was not accepted")
	}
	select {
	case response := <-done:
		periodicHTTPStatus(t, response, 503, "periodic_budget_outcome_unknown")
		var data map[string]json.RawMessage
		periodicHTTPMust(t, "decode unknown HTTP envelope", json.Unmarshal(response.Body.Bytes(), &data))
		if _, ok := data["data"]; ok {
			t.Fatal("unknown COMMIT exposed an unconfirmed receipt")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("unknown HTTP commit did not return within its bounded deadline")
	}
	if before != f.snapshot(t) {
		t.Fatal("interrupted deferred COMMIT unexpectedly retained command facts")
	}
	periodicHTTPStatus(t, f.call(t, "GET", "/api/platform/periodic-budget-operations/"+in.OperationID, "", admin, nil), 404, "not_found")
	// This sleep occurs before commit completion. The result honestly remains
	// unknown to the handler; this test does not simulate a committed lost reply.
}
