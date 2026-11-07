package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

type procurementMeterRow struct {
	AttemptID      string          `json:"attempt_id"`
	AccountID      string          `json:"account_id"`
	TenantID       string          `json:"tenant_id"`
	RequestID      string          `json:"request_id"`
	ProfileVersion string          `json:"profile_version"`
	SchemaDigest   string          `json:"schema_digest"`
	VariantKey     string          `json:"variant_key"`
	Bounds         json.RawMessage `json:"supplier_bounds"`
	UpperAmount    string          `json:"upper_amount"`
	HeldAmount     string          `json:"held_amount"`
	SpentAmount    string          `json:"spent_amount"`
}

type procurementMeterPageResult struct {
	Data []procurementMeterRow `json:"data"`
	Next string                `json:"next_cursor"`
}

func procurementMeterPage(t *testing.T, response *httptest.ResponseRecorder) procurementMeterPageResult {
	t.Helper()
	var page procurementMeterPageResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Data == nil || len(page.Data) > 50 {
		t.Fatal("invalid procurement vector page", response.Code, response.Body.String())
	}
	for _, forbidden := range []string{"input_bound", "output_bound", "canonical", "plan_digest", "quote_digest", "input_hmac", "base_url", "local-fixture-secret"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("scalar or private original leaked", forbidden, response.Body.String())
		}
	}
	return page
}

func procurementMeterOriginal(t *testing.T, f *gatewayFixture, row procurementMeterRow) {
	t.Helper()
	var schemaRaw []byte
	var tenant, request, profile, digest, variant, input, output string
	err := f.pool.QueryRow(context.Background(), `SELECT s.tenant_id,s.request_id,s.profile_version,encode(s.schema_digest,'hex'),s.variant_key,m.canonical,
 (SELECT quantity::text FROM attempt_meter_bound_lines WHERE attempt_id=s.attempt_id AND side='supplier' AND dimension='text_input_token'),
 (SELECT quantity::text FROM attempt_meter_bound_lines WHERE attempt_id=s.attempt_id AND side='supplier' AND dimension='text_output_token')
 FROM attempt_meter_snapshots s JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest WHERE s.attempt_id=$1`, row.AttemptID).Scan(&tenant, &request, &profile, &digest, &variant, &schemaRaw, &input, &output)
	if err != nil || tenant != row.TenantID || request != row.RequestID || profile != row.ProfileVersion || digest != row.SchemaDigest || variant != row.VariantKey {
		t.Fatal("list is not bound to the original attempt", row, err)
	}
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = metering.ParseBounds(schema, metering.Supplier, row.Bounds); err != nil {
		t.Fatal("HTTP bounds are not the pure kernel wire", string(row.Bounds), err)
	}
	var bounds struct {
		Entries []struct{ Dimension, Unit, Quantity string } `json:"bounds"`
	}
	if json.Unmarshal(row.Bounds, &bounds) != nil || len(bounds.Entries) != 2 || bounds.Entries[0].Dimension != "text_input_token" || bounds.Entries[0].Unit != "token" || bounds.Entries[0].Quantity != input || bounds.Entries[1].Dimension != "text_output_token" || bounds.Entries[1].Unit != "token" || bounds.Entries[1].Quantity != output {
		t.Fatal("HTTP bounds differ from original lines", string(row.Bounds), input, output)
	}
}

// Only the new list/read boundary is exercised here, not production startup
// qualification: session_user remains the fixture owner after SET ROLE.
func procurementMeterApplication(t *testing.T, f *gatewayFixture) {
	t.Helper()
	ctx := context.Background()
	var schema string
	if err := f.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	appRole, projector := "proc_meter_app_"+suffix, "proc_meter_proj_"+suffix
	for _, role := range []string{appRole, projector} {
		authExec(t, f.pool, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOINHERIT NOSUPERUSER")
	}
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		for _, role := range []string{appRole, projector} {
			for _, sql := range []string{"DROP OWNED BY " + pgx.Identifier{role}.Sanitize(), "DROP ROLE " + pgx.Identifier{role}.Sanitize()} {
				if _, err := f.pool.Exec(ctx, sql); err != nil {
					t.Error(err)
				}
			}
		}
	})
	if err := operations.ProvisionFor(ctx, f.pool, schema, appRole, projector); err != nil {
		t.Fatal(err)
	}
	cfg := f.pool.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
		return err
	}
	var err error
	app, err = pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var current string
	var superuser bool
	if err = app.QueryRow(ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &superuser); err != nil || current != appRole || superuser {
		t.Fatal("not restricted APP", current, superuser, err)
	}
	f.server = New(f.server.Config, app, fstest.MapFS{}, nil)
	f.server.Ready(true)
}

func procurementMeterAccount(t *testing.T, f *gatewayFixture, actor, channel string) string {
	t.Helper()
	ctx := context.Background()
	receipt, err := f.server.Procurement.CreateAccount(ctx, actor, procurement.CreateInput{OperationID: uuid.NewString(), Label: "meter supplier", Provider: "fixture", State: "active", LimitAmount: "1000"})
	if err != nil {
		t.Fatal(err)
	}
	account := procurementAccountFromReceipt(t, receipt)
	var credential, revision string
	if err = f.pool.QueryRow(ctx, `SELECT v.credential_id,c.management_revision::text FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, channel).Scan(&credential, &revision); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Procurement.BindCredential(ctx, actor, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: channel, CredentialID: credential, AccountID: account.ID, ExpectedChannelRevision: revision}); err != nil {
		t.Fatal(err)
	}
	publishCostFixture(t, f, channel, "provider-model", "contract", costRate(0), costRate(0), time.Time{})
	return account.ID
}

func procurementMeterBirth(t *testing.T, f *gatewayFixture, tenant, key, user, channel string, output int64, ready bool) string {
	t.Helper()
	ctx := context.Background()
	hmac := "not-plan-ready"
	if ready {
		hmac = strings.Repeat("ab", 32)
	}
	r, err := f.server.Billing.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: tenant, UserID: user, KeyID: key}, RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: 1, IdempotencyKey: uuid.NewString(), RequestHMAC: hmac, InputUpperBound: 100, OutputLimit: output, RequestDeadline: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	defer f.server.Billing.EndRequest(r.RequestID)
	var version, credential string
	if err = f.pool.QueryRow(ctx, `SELECT c.current_version,v.credential_id FROM channels c JOIN channel_versions v ON v.id=c.current_version WHERE c.id=$1`, channel).Scan(&version, &credential); err != nil {
		t.Fatal(err)
	}
	q, err := meterquote.Select(ctx, f.pool, f.model, channel, "provider-model")
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.server.Billing.RecordAttempt(ctx, billing.AttemptInput{TenantID: tenant, RequestID: r.RequestID, Sequence: 1, ChannelID: channel, ChannelVersionID: version, CredentialID: credential, UpstreamModel: "provider-model", CostQuote: &q})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestProcurementHTTPMeterOriginalBoundsAndPagination(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t)}, nil)
	sid, _ := probeAdmin(t, f)
	var admin, owner, operator, approver string
	for _, row := range []struct {
		sql   string
		value *string
	}{
		{`SELECT id FROM users WHERE platform_role='admin'`, &admin},
		{`SELECT owner_id FROM api_keys WHERE id='` + f.key + `'`, &owner},
		{`SELECT id FROM users WHERE platform_role='finance_operator'`, &operator},
		{`SELECT id FROM users WHERE platform_role='finance_approver'`, &approver},
	} {
		if err := f.pool.QueryRow(context.Background(), row.sql).Scan(row.value); err != nil {
			t.Fatal(err)
		}
	}
	channels := probeChannels(t, f)
	account := procurementMeterAccount(t, f, admin, channels[0])
	otherAccount := procurementMeterAccount(t, f, admin, channels[1])
	publishDefaultFixture(t, f, f.model, operator, 0, 0, time.Now())
	otherTenant, otherKey := uuid.NewString(), uuid.NewString()
	authExec(t, f.pool, `INSERT INTO tenants(id,name) VALUES($1,'other meter tenant')`, otherTenant)
	authExec(t, f.pool, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, otherTenant, owner)
	authExec(t, f.pool, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, otherTenant, f.model)
	authExec(t, f.pool, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) SELECT $1,$2,owner_id,name,'other-meter',$3,scopes,lifetime_limit FROM api_keys WHERE id=$4`, otherKey, otherTenant, uuid.NewString(), f.key)
	credit, err := f.server.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: otherTenant, ActorID: operator, Amount: 1000, IdempotencyKey: uuid.NewString(), RequestHMAC: "other-meter-credit", BusinessRef: uuid.NewString(), ProofRef: "receipt:meter"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: otherTenant, ActorID: approver, OrderID: credit.ID}); err != nil {
		t.Fatal(err)
	}
	procurementMeterApplication(t, f)
	want := map[string]bool{}
	for i := range 51 {
		tenant, key := f.tenant, f.key
		if i%2 == 1 {
			tenant, key = otherTenant, otherKey
		}
		want[procurementMeterBirth(t, f, tenant, key, owner, channels[0], int64(1+i%20), i%2 == 0)] = true
	}
	otherAttempt := procurementMeterBirth(t, f, f.tenant, f.key, owner, channels[1], 7, true)
	// Later price publication and current-model availability must not rewrite
	// history. Disable scalar read functions to prove the list never calls them.
	publishCostFixture(t, f, channels[0], "provider-model", "invoice", costRate(9000000), costRate(9000000), time.Time{})
	authExec(t, f.pool, `UPDATE models SET enabled=false WHERE id=$1`, f.model)
	for _, signature := range []string{"meter_cost_million(cost_id text, dimension text)", "meter_price_million(price_id text, dimension text)"} {
		authExec(t, f.pool, `CREATE OR REPLACE FUNCTION `+signature+` RETURNS bigint LANGUAGE plpgsql STABLE AS $$ BEGIN RAISE EXCEPTION 'scalar read forbidden'; END $$`)
	}
	call := func(path string) *httptest.ResponseRecorder {
		return keyPolicyHTTP(t, f.server.Engine(), "GET", "/api/platform/procurement-accounts/"+path, "", sid, "", "", "", nil)
	}
	originals := procurementMeterFacts(t, f)
	first := procurementMeterPage(t, call(account+"/reservations"))
	if len(first.Data) != 50 || first.Next == "" {
		t.Fatal("vector aggregation changed first page cardinality", len(first.Data), first.Next)
	}
	second := procurementMeterPage(t, call(account+"/reservations?cursor="+first.Next))
	if len(second.Data) != 1 || second.Next != "" {
		t.Fatal("vector cursor lost the 51st original", second)
	}
	seen := map[string]bool{}
	for _, row := range append(first.Data, second.Data...) {
		if !want[row.AttemptID] || seen[row.AttemptID] || row.AccountID != account || row.UpperAmount != "0" || row.HeldAmount != "0" || row.SpentAmount != "0" {
			t.Fatal("page mixed accounts, duplicated attempts, or repriced money", row)
		}
		seen[row.AttemptID] = true
		procurementMeterOriginal(t, f, row)
	}
	if len(seen) != 51 {
		t.Fatal("page omitted original attempts", len(seen))
	}
	other := procurementMeterPage(t, call(otherAccount+"/reservations"))
	if len(other.Data) != 1 || other.Data[0].AttemptID != otherAttempt {
		t.Fatal("account filter leaked shared-account attempts", other)
	}
	for _, path := range []string{uuid.NewString() + "/reservations", account + "/reservations?cursor=" + secondCursorAfter(t, f, second.Data[0].AttemptID)} {
		if page := procurementMeterPage(t, call(path)); len(page.Data) != 0 || page.Next != "" {
			t.Fatal("empty page was not empty", page)
		}
	}
	after := procurementMeterFacts(t, f)
	for table, before := range originals {
		if after[table] != before {
			t.Fatal("list changed financial originals or filled unready snapshots", table)
		}
	}
	// A dependency error must discard the entire response, not return an empty
	// or partially decoded page; an unauthorized/invalid request fails earlier.
	authExec(t, f.pool, `CREATE OR REPLACE FUNCTION meter_attempt_supplier_bounds(tenant_id text,request_id text,attempt_id text) RETURNS jsonb LANGUAGE plpgsql STABLE SECURITY INVOKER AS $$ BEGIN RAISE EXCEPTION 'private original dependency failed'; END $$`)
	failed := call(account + "/reservations")
	if failed.Code != 503 || strings.Contains(failed.Body.String(), "private original") || strings.Contains(failed.Body.String(), `"data"`) {
		t.Fatal("failed original leaked a partial page or error", failed.Code, failed.Body.String())
	}
	if invalid := call(account + "/reservations?cursor=invalid"); invalid.Code != 400 {
		t.Fatal("invalid cursor reached original reader", invalid.Code)
	}
	customer, _ := authSession(t, f.server, f.pool, owner, f.tenant, false, false)
	denied := keyPolicyHTTP(t, f.server.Engine(), "GET", "/api/platform/procurement-accounts/"+account+"/reservations", "", customer, "", "", "", nil)
	if denied.Code != 403 {
		t.Fatal("tenant membership reached supplier original reader", denied.Code)
	}
	if f.posts.Load() != 0 {
		t.Fatal("read fixture sent a model request", f.posts.Load())
	}
}

func procurementMeterFacts(t *testing.T, f *gatewayFixture) map[string]string {
	t.Helper()
	facts := map[string]string{}
	for _, table := range []string{"request_meter_snapshots", "attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "procurement_reservations", "procurement_budget_events", "upstream_attempts", "reservations", "journals", "funding_reservation_lots"} {
		var raw string
		if err := f.pool.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{table}.Sanitize()+` t`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		facts[table] = raw
	}
	return facts
}

func secondCursorAfter(t *testing.T, f *gatewayFixture, attempt string) string {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT created_at FROM procurement_reservations WHERE attempt_id=$1`, attempt).Scan(&at); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cursor{CreatedAt: at, ID: attempt})
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestProcurementHTTPMeterInputOnlyBounds(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolEmbeddings, provider.ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{gatewaySuccess(t)}, nil, 8, protocol)
			sid, _ := probeAdmin(t, f)
			var admin, owner string
			if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='admin'`).Scan(&admin); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(context.Background(), `SELECT owner_id FROM api_keys WHERE id=$1`, f.key).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			channel := probeChannels(t, f)[0]
			account := procurementMeterAccount(t, f, admin, channel)
			procurementMeterApplication(t, f)
			attempt := procurementMeterBirth(t, f, f.tenant, f.key, owner, channel, 0, false)
			got := keyPolicyHTTP(t, f.server.Engine(), "GET", "/api/platform/procurement-accounts/"+account+"/reservations", "", sid, "", "", "", nil)
			page := procurementMeterPage(t, got)
			if len(page.Data) != 1 || page.Data[0].AttemptID != attempt || !bytes.Contains(page.Data[0].Bounds, []byte(`"quantity":"0"`)) && !bytes.Contains(page.Data[0].Bounds, []byte(`"quantity": "0"`)) {
				t.Fatal("input-only zero output disappeared", got.Body.String())
			}
			procurementMeterOriginal(t, f, page.Data[0])
		})
	}
}

func TestProcurementMeterReadRejectsIncompleteOriginal(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t), gatewaySuccess(t)}, nil)
	ctx := context.Background()
	// Use a genuine ready request/attempt as the immutable parent; negative
	// originals below are new uncommitted rows, never edits to stored history.
	response := f.request(t, ctx, false, false)
	_ = response.Body.Close()
	var original, tenant, request string
	if err := f.pool.QueryRow(ctx, `SELECT id,tenant_id,request_id FROM upstream_attempts WHERE request_id=$1`, response.Header.Get("X-Request-ID")).Scan(&original, &tenant, &request); err != nil {
		t.Fatal(err)
	}
	procurementMeterApplication(t, f)
	channel := probeChannels(t, f)[1]
	for _, tc := range []struct{ name, rows, tenant string }{
		{"missing_snapshot", "no_snapshot", tenant},
		{"missing", "", tenant},
		{"one_dimension", "input", tenant},
		{"wrong_tenant", "both", uuid.NewString()},
		{"above_schema_max", "excess", tenant},
		{"within_schema_but_not_original", "different", tenant},
		{"complete", "both", tenant},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.server.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			attempt := uuid.NewString()
			if _, err = tx.Exec(ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model)
 SELECT $1,a.tenant_id,a.request_id,2,c.id,c.current_version,v.credential_id,a.upstream_model FROM upstream_attempts a CROSS JOIN channels c JOIN channel_versions v ON v.id=c.current_version WHERE a.id=$2 AND c.id=$3`, attempt, original, channel); err != nil {
				t.Fatal(err)
			}
			if tc.rows != "no_snapshot" {
				if _, err = tx.Exec(ctx, `INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,input_bound,output_bound)
 SELECT tenant_id,$1,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,input_bound,output_bound FROM attempt_meter_snapshots WHERE attempt_id=$2`, attempt, original); err != nil {
					t.Fatal(err)
				}
			}
			if tc.rows != "" && tc.rows != "no_snapshot" {
				if _, err = tx.Exec(ctx, `INSERT INTO attempt_meter_bound_lines(tenant_id,attempt_id,side,dimension,unit,quantity)
 SELECT $1,$2,side,dimension,unit,CASE WHEN $4='excess' THEN quantity+1000 WHEN $4='different' THEN quantity-1 ELSE quantity END FROM attempt_meter_bound_lines
 WHERE attempt_id=$3 AND side='supplier' AND ($4<>'input' OR dimension='text_input_token')`, tc.tenant, attempt, original, tc.rows); err != nil {
					t.Fatal(err)
				}
			}
			var raw []byte
			err = tx.QueryRow(ctx, `SELECT meter_attempt_supplier_bounds($1,$2,$3)`, tenant, request, attempt).Scan(&raw)
			if (tc.name == "complete") != (err == nil) {
				t.Fatal("incomplete or misbound original accepted", tc.name, string(raw), err)
			}
			if tc.name != "complete" && !strings.Contains(err.Error(), "original supplier bounds are") {
				t.Fatal("negative failed outside the original-bound reader", err)
			}
		})
	}
	for _, ids := range [][3]string{{uuid.NewString(), request, original}, {tenant, uuid.NewString(), original}, {tenant, request, uuid.NewString()}} {
		var raw []byte
		if err := f.server.Pool.QueryRow(ctx, `SELECT meter_attempt_supplier_bounds($1,$2,$3)`, ids[0], ids[1], ids[2]).Scan(&raw); err == nil || !strings.Contains(err.Error(), "original supplier bounds are unavailable") {
			t.Fatal("cross-root original accepted", ids, string(raw))
		}
	}
}
