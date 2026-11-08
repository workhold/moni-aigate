package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

const fixtureProfileVersion = "10000000-0000-4000-8000-000000000001"
const fixtureRequestHMAC = "abababababababababababababababababababababababababababababababab"
const fixtureRouteRevision int64 = 99

func TestMoney(t *testing.T) {
	for _, tc := range []struct {
		i, o      int64
		ir, or, w Money
	}{{1000, 500, 2000000, 8000000, 6000}, {1, 1, 1, 1, 1}, {1, 1, 500000, 500000, 1}, {1, 1, 500001, 500001, 2}, {0, 0, Money(math.MaxInt64), Money(math.MaxInt64), 0}} {
		got, err := Calculate(tc.i, tc.o, tc.ir, tc.or)
		if err != nil || got != tc.w {
			t.Fatalf("Calculate=%v,%v want %v", got, err, tc.w)
		}
	}
	if _, err := Calculate(math.MaxInt64, math.MaxInt64, Money(math.MaxInt64), Money(math.MaxInt64)); err == nil {
		t.Fatal("overflow accepted")
	}
	if _, err := Calculate(-1, 0, 1, 1); err == nil {
		t.Fatal("negative accepted")
	}
	b, err := json.Marshal(Money(math.MaxInt64))
	if err != nil || string(b) != `"9223372036854775807"` {
		t.Fatalf("money JSON %s %v", b, err)
	}
	var m Money
	if err = json.Unmarshal([]byte(`123`), &m); err == nil {
		t.Fatal("numeric JSON accepted")
	}
	if err = validateUsage(Usage{Status: "confirmed_final", InputTokens: 1, OutputTokens: 2, TotalTokens: 3}); err != nil {
		t.Fatal(err)
	}
	if err = validateUsage(Usage{Status: "confirmed_final", InputTokens: math.MaxInt64, OutputTokens: 1, TotalTokens: 0}); err == nil {
		t.Fatal("overflow usage total accepted")
	}
}

type fixture struct {
	pool                                                                              *pgxpool.Pool
	store                                                                             *Store
	ctx                                                                               context.Context
	tenant, user, key, model, operator, approver, admin, channel, version, credential string
}

func integration(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required for real PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "billing_test_" + uuid.NewString()[:8]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 24
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	f := &fixture{pool: pool, store: New(pool), ctx: ctx, tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), admin: uuid.NewString(), channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString()}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO tenants(id,name) VALUES($1,'test')", f.tenant)
	for _, u := range []struct{ id, role string }{{f.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}, {f.admin, "admin"}} {
		exec("INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)", u.id, u.id+"@test.invalid", u.role)
	}
	exec("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, f.user)
	exec("SELECT meter_insert_text_model($1,'model',$2,$3)", f.model, `{"version":"`+fixtureProfileVersion+`","model":"model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`, fixtureProfileVersion)
	exec("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	exec("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'test','key','digest',$4,1000)", f.key, f.tenant, f.user, []string{f.model})
	meterquote.PublishDefaultPrice(t, ctx, tx, meterquote.DefaultPrice{ModelID: f.model, Actor: f.operator, InputRate: 1000000, OutputRate: 1000000, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	exec("INSERT INTO channels(id,name,current_version) VALUES($1,'test',$2)", f.channel, f.version)
	exec("INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'not-a-real-secret')", f.credential, f.channel)
	exec("INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)", f.version, f.channel, f.credential)
	exec("INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1000)", f.version, f.channel, f.credential)
	policy := uuid.NewString()
	exec("INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,$3,'manual','allow')", policy, f.model, fixtureRouteRevision)
	for _, upstream := range []string{"upstream", "unknown", "missing", "unpriced-upstream", "unpriced"} {
		exec("INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,$4,0,1,10,'default')", policy, f.channel, f.version, upstream)
	}
	exec("UPDATE models SET active_revision=$2 WHERE id=$1", f.model, fixtureRouteRevision)
	meterquote.PublishCost(t, ctx, tx, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(2000000), OutputRate: billingRate(2000000)})
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	f.fund(t, 1000)
	return f
}
func (f *fixture) fund(t *testing.T, amount Money) {
	t.Helper()
	key := uuid.NewString()
	o, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: key, ProofRef: "receipt:test", Amount: amount, IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
}
func (f *fixture) input() ReserveInput {
	return ReserveInput{Principal: Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, RouteRevision: fixtureRouteRevision, IdempotencyKey: uuid.NewString(), RequestHMAC: fixtureRequestHMAC, ProfileVersion: fixtureProfileVersion, InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(300 * time.Second)}
}

type fixtureRouteBinding struct {
	channel, version, upstream string
}

func publishFixtureRouteWithBindings(t *testing.T, f *fixture, revision int64, bindings ...fixtureRouteBinding) string {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	policy := uuid.NewString()
	if _, err = tx.Exec(f.ctx, "INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,$3)", policy, f.model, revision); err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if _, err = tx.Exec(f.ctx, "INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,$4,1,10)", policy, binding.channel, binding.version, binding.upstream); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, "UPDATE models SET active_revision=$2 WHERE id=$1", f.model, revision); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return policy
}

func publishFixtureRoute(t *testing.T, f *fixture, revision int64, upstreams ...string) string {
	bindings := make([]fixtureRouteBinding, 0, len(upstreams))
	for _, upstream := range upstreams {
		bindings = append(bindings, fixtureRouteBinding{channel: f.channel, version: f.version, upstream: upstream})
	}
	return publishFixtureRouteWithBindings(t, f, revision, bindings...)
}
func (f *fixture) reserve(t *testing.T) Reservation {
	t.Helper()
	r, err := f.store.Reserve(f.ctx, f.input())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *fixture) ensureAttempt(t *testing.T, request string) string {
	t.Helper()
	var id string
	err := f.pool.QueryRow(f.ctx, `SELECT id FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2 ORDER BY sequence LIMIT 1`, f.tenant, request).Scan(&id)
	if err == nil {
		return id
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	attempt, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: request, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	return attempt.ID
}
func (f *fixture) final(t *testing.T, r Reservation, usage Usage, protocol, reason, stage string, content bool) Finalization {
	t.Helper()
	v, err := f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: f.ensureAttempt(t, r.RequestID), Source: "gateway", SourceEventID: uuid.NewString(), Usage: usage, ProtocolOutcome: protocol, TransportEndReason: reason, DeliveryStage: stage, ContentWritten: content})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func confirmed() Usage {
	return Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
}
func (f *fixture) wallet(t *testing.T, available, held Money) {
	t.Helper()
	w, err := f.store.Wallet(f.ctx, Query{TenantID: f.tenant})
	if err != nil || w.Available != available || w.Held != held {
		t.Fatalf("wallet %+v err=%v want %v/%v", w, err, available, held)
	}
}
func (f *fixture) check(t *testing.T) {
	t.Helper()
	v, err := f.store.VerifyLedger(f.ctx, Query{})
	if err != nil || !v.Balanced {
		t.Fatalf("ledger %+v %v", v, err)
	}
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var v *Error
	if !errors.As(err, &v) || v.Code != code {
		t.Fatalf("error %v want %s", err, code)
	}
}

func TestPostgresLifecycle(t *testing.T) {
	f := integration(t)
	in := f.input()
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 880, 120)
	replay := in
	replay.RequestID = uuid.NewString()
	_, err = f.store.Reserve(f.ctx, replay)
	assertCode(t, err, "request_exists")
	replay.RequestHMAC = "changed"
	_, err = f.store.Reserve(f.ctx, replay)
	assertCode(t, err, "idempotency_conflict")
	f.final(t, r, confirmed(), "completed", "done", "none", false)
	var requestPlan, attemptPlan, parentPlan []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT s.plan_digest,a.plan_digest,a.parent_plan_digest FROM request_meter_snapshots s JOIN attempt_meter_snapshots a ON a.request_id=s.request_id WHERE s.request_id=$1`, r.RequestID).Scan(&requestPlan, &attemptPlan, &parentPlan); err != nil || len(requestPlan) != 32 || len(attemptPlan) != 32 || !bytes.Equal(requestPlan, parentPlan) || bytes.Equal(requestPlan, attemptPlan) {
		t.Fatal("request and attempt plans were not stored", err)
	}
	var requestLines, attemptLines int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_meter_bound_lines WHERE request_id=$1 AND quantity IN (100,20)), (SELECT count(*) FROM attempt_meter_bound_lines b JOIN attempt_meter_snapshots a ON a.attempt_id=b.attempt_id WHERE a.request_id=$1 AND b.quantity IN (100,20))`, r.RequestID).Scan(&requestLines, &attemptLines); err != nil || requestLines != 4 || attemptLines != 4 {
		t.Fatal("bound lines were not stored", requestLines, attemptLines, err)
	}
	var retailQuote, supplierQuote []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT s.retail_quote_digest,a.supplier_quote_digest FROM request_meter_snapshots s JOIN attempt_meter_snapshots a ON a.request_id=s.request_id WHERE s.request_id=$1`, r.RequestID).Scan(&retailQuote, &supplierQuote); err != nil || len(retailQuote) != 32 || len(supplierQuote) != 32 || bytes.Equal(retailQuote, supplierQuote) {
		t.Fatal("request and attempt quotes were not stored", err)
	}
	var requestVectors, attemptVectors bool
	if err = f.pool.QueryRow(f.ctx, `SELECT
 s.retail_bounds_digest=meter_bounds_digest(s.schema_digest,'retail',s.retail_bounds_canonical)
 AND s.supplier_bounds_digest=meter_bounds_digest(s.schema_digest,'supplier',s.supplier_bounds_canonical)
 AND s.retail_rates_digest=meter_rates_digest(s.schema_digest,'retail',s.retail_rates_canonical)
 AND s.retail_meter_quote_digest=meter_pure_quote_digest(s.schema_digest,'retail',s.retail_bounds_canonical,s.retail_rates_canonical),
 a.retail_bounds_digest=meter_bounds_digest(a.schema_digest,'retail',a.retail_bounds_canonical)
 AND a.supplier_bounds_digest=meter_bounds_digest(a.schema_digest,'supplier',a.supplier_bounds_canonical)
 AND a.supplier_rates_digest=meter_rates_digest(a.schema_digest,'supplier',a.supplier_rates_canonical)
 AND a.supplier_meter_quote_digest=meter_pure_quote_digest(a.schema_digest,'supplier',a.supplier_bounds_canonical,a.supplier_rates_canonical)
 FROM request_meter_snapshots s JOIN attempt_meter_snapshots a ON a.request_id=s.request_id WHERE s.request_id=$1`, r.RequestID).Scan(&requestVectors, &attemptVectors); err != nil || !requestVectors || !attemptVectors {
		t.Fatal("pure metering vectors were not stored or did not match their digests", err)
	}
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 15 {
		t.Fatalf("settlement %+v %v", settled, err)
	}
	f.wallet(t, 985, 0)
	again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || again.ID != settled.ID {
		t.Fatal(again, err)
	}
	again, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "no_delivery"})
	if err != nil || again.ID != settled.ID {
		t.Fatal(again, err)
	}
	ri := RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: "refund:test", Reason: "support-approved", Amount: 10, IdempotencyKey: "refund-key", RequestHMAC: "refund-hmac"}
	refund, err := f.store.CreateRefund(f.ctx, ri)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.admin})
	assertCode(t, err, "forbidden")
	for i := 0; i < 2; i++ {
		if _, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver}); err != nil {
			t.Fatal(err)
		}
	}
	repeated, err := f.store.CreateRefund(f.ctx, ri)
	if err != nil || repeated.ID != refund.ID {
		t.Fatal(repeated, err)
	}
	ri.BusinessRef = "new-ref"
	ri.RequestHMAC = "changed"
	_, err = f.store.CreateRefund(f.ctx, ri)
	assertCode(t, err, "idempotency_conflict")
	ri.IdempotencyKey = "new-key"
	ri.Amount = 6
	other, err := f.store.CreateRefund(f.ctx, ri)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: other.ID, ActorID: f.approver})
	assertCode(t, err, "refund_exceeds_charge")
	f.wallet(t, 995, 0)
	var spent int64
	if err = f.pool.QueryRow(f.ctx, "SELECT spent FROM api_keys WHERE id=$1", f.key).Scan(&spent); err != nil || spent != 15 {
		t.Fatalf("spent %d %v", spent, err)
	}
	n, err := f.store.Project(f.ctx, ProjectInput{})
	if err != nil || n == 0 {
		t.Fatalf("project %d %v", n, err)
	}
	n, err = f.store.Project(f.ctx, ProjectInput{})
	if err != nil || n != 0 {
		t.Fatalf("replay %d %v", n, err)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1000" || report.Charged != "15" || report.Refunded != "10" || report.Requests != 1 {
		t.Fatalf("report %+v %v", report, err)
	}
	f.check(t)
}

func TestPostgresConcurrentReservations(t *testing.T) {
	for _, sameKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared_wallet", true: "shared_key"}[sameKey], func(t *testing.T) {
			f := integration(t)
			key2 := f.key
			if !sameKey {
				key2 = uuid.NewString()
				if _, err := f.pool.Exec(f.ctx, "INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'second','second','second',$4,1000)", key2, f.tenant, f.user, []string{f.model}); err != nil {
					t.Fatal(err)
				}
			}
			// Seven valid charges leave 160, enough for only one 120 reservation.
			if !sameKey {
				for i := 0; i < 7; i++ {
					r := f.reserve(t)
					f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 100, OutputTokens: 20, TotalTokens: 120}, "completed", "done", "content", true)
					if _, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET lifetime_limit=120 WHERE id=$1", f.key); err != nil {
					t.Fatal(err)
				}
			}
			inputs := []ReserveInput{f.input(), f.input()}
			inputs[1].Principal.KeyID = key2
			var wg sync.WaitGroup
			errs := make([]error, 2)
			start := make(chan struct{})
			for i := range inputs {
				wg.Add(1)
				go func(i int) { defer wg.Done(); <-start; _, errs[i] = f.store.Reserve(f.ctx, inputs[i]) }(i)
			}
			close(start)
			wg.Wait()
			success := 0
			for _, err := range errs {
				if err == nil {
					success++
				} else {
					var e *Error
					if !errors.As(err, &e) || !oneOf(e.Code, "insufficient_balance", "key_budget_exceeded") {
						t.Fatal(err)
					}
				}
			}
			if success != 1 {
				t.Fatalf("success=%d errors=%v", success, errs)
			}
			f.check(t)
		})
	}
}

func TestPostgresTransactionRollback(t *testing.T) {
	for _, tc := range []struct{ table, event string }{
		{"request_records", "INSERT"}, {"reservations", "INSERT"},
		{"journals", "INSERT"}, {"ledger_accounts", "UPDATE"}, {"journal_lines", "INSERT"},
		{"api_keys", "UPDATE"}, {"recovery_jobs", "INSERT"}, {"audit_events", "INSERT"}, {"projection_outbox", "INSERT"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			f := integration(t)
			type state struct {
				Spent, Held, Requests, Reservations, Journals, Lines, Recovery, Audit, Outbox, Idempotency int64
			}
			snapshot := func() state {
				t.Helper()
				var v state
				err := f.pool.QueryRow(f.ctx, `SELECT spent,held,
 (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations),
 (SELECT count(*) FROM journals),(SELECT count(*) FROM journal_lines),
 (SELECT count(*) FROM recovery_jobs),(SELECT count(*) FROM audit_events),
 (SELECT count(*) FROM projection_outbox),(SELECT count(*) FROM request_idempotency)
 FROM api_keys WHERE id=$1`, f.key).Scan(&v.Spent, &v.Held, &v.Requests, &v.Reservations, &v.Journals, &v.Lines, &v.Recovery, &v.Audit, &v.Outbox, &v.Idempotency)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			before := snapshot()
			if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_injected() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected failure'; END$$`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "CREATE TRIGGER injected BEFORE "+tc.event+" ON "+tc.table+" FOR EACH ROW EXECUTE FUNCTION fail_injected()"); err != nil {
				t.Fatal(err)
			}
			input := f.input()
			_, err := f.store.Reserve(f.ctx, input)
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "P0001" {
				t.Fatalf("injected database error was lost: %v", err)
			}
			f.wallet(t, 1000, 0)
			if after := snapshot(); after != before {
				t.Fatalf("partial reservation commit before=%+v after=%+v", before, after)
			}
			f.check(t)
			if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER injected ON "+tc.table); err != nil {
				t.Fatal(err)
			}
			// The same request and idempotency key can succeed after rollback;
			// this also exercises a usable connection after BatchResults.Close.
			if _, err = f.store.Reserve(f.ctx, input); err != nil {
				t.Fatal(err)
			}
			f.wallet(t, 880, 120)
			f.check(t)
		})
	}
}

func TestPostgresSettlementPolicy(t *testing.T) {
	cases := []struct {
		name, mode, protocol, reason, stage, status string
		usage                                       Usage
		content, expired                            bool
		charge                                      Money
	}{
		{name: "live_complete_before_delivery", mode: "live", protocol: "completed", reason: "done", stage: "none", usage: confirmed(), status: "settled", charge: 15},
		{name: "recovery_no_output", mode: "recovery", protocol: "completed", reason: "done", stage: "none", usage: confirmed(), status: "released"},
		{name: "partial_final_usage", mode: "recovery", protocol: "incomplete", reason: "connection_error", stage: "content", usage: confirmed(), content: true, status: "settled", charge: 15},
		{name: "cancel_with_usage", mode: "recovery", protocol: "cancelled", reason: "client_cancelled", stage: "none", usage: confirmed(), status: "settled", charge: 15},
		{name: "partial_missing_usage", mode: "recovery", protocol: "incomplete", reason: "connection_error", stage: "content", usage: Usage{Status: "missing"}, content: true, status: "pending_reconciliation"},
		{name: "trusted_zero", mode: "live", protocol: "completed", reason: "done", stage: "none", usage: Usage{Status: "confirmed_final"}, status: "settled"},
		{name: "unknown_delivery", mode: "recovery", protocol: "unknown", reason: "unknown", stage: "unknown", usage: confirmed(), status: "pending_reconciliation"},
		{name: "hard24h_earlier_trusted", mode: "recovery", protocol: "completed", reason: "done", stage: "content", usage: confirmed(), content: true, expired: true, status: "released"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			f.final(t, r, tc.usage, tc.protocol, tc.reason, tc.stage, tc.content)
			if tc.expired {
				setFixtureReservationDeadline(t, f, r.RequestID, "-1 second")
			}
			settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: tc.mode})
			if err != nil || settled.Status != tc.status || settled.ChargedAmount != tc.charge {
				t.Fatalf("settlement %+v err=%v want %s/%d", settled, err, tc.status, tc.charge)
			}
			f.check(t)
		})
	}
}

func TestPostgresDeadlineAfterAccountLock(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	f.final(t, r, confirmed(), "completed", "done", "content", true)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM ledger_accounts WHERE tenant_id=$1 FOR UPDATE", f.tenant); err != nil {
		t.Fatal(err)
	}
	setFixtureReservationDeadline(t, f, r.RequestID, "500 milliseconds")
	type output struct {
		s   Settlement
		err error
	}
	done := make(chan output, 1)
	go func() {
		s, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
		done <- output{s, err}
	}()
	time.Sleep(700 * time.Millisecond)
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.s.Status != "released" || got.s.ChargedAmount != 0 || got.s.Reason != "released_deadline" {
		t.Fatalf("deadline settlement %+v %v", got.s, got.err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresEvidenceAndReconciliation(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err == nil {
		t.Fatal("dispatch replay allowed")
	}
	input := FinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: a.ID + ":final", Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}
	for i := 0; i < 2; i++ {
		if _, err = f.store.RecordFinalization(f.ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	var retailState, supplierState string
	var retailDigest, supplierDigest []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT retail_usage_state,supplier_usage_state,retail_quantities_digest,supplier_quantities_digest FROM finalization_records WHERE id=$1`, input.ID).Scan(&retailState, &supplierState, &retailDigest, &supplierDigest); err != nil {
		t.Fatal(err)
	}
	if retailState != "confirmed_final" || supplierState != "confirmed_final" || len(retailDigest) != 32 || len(supplierDigest) != 32 || string(retailDigest) == string(supplierDigest) {
		t.Fatalf("final projection retail=%s supplier=%s", retailState, supplierState)
	}
	var observation []byte
	var observationDigest []byte
	if err = f.pool.QueryRow(f.ctx, `SELECT e.observation_canonical,e.observation_digest FROM metering_events e WHERE e.id=(SELECT event_id FROM finalization_records WHERE id=$1)`, input.ID).Scan(&observation, &observationDigest); err != nil || len(observation) == 0 || len(observationDigest) != 32 || !strings.Contains(string(observation), `"quantities"`) || strings.Contains(string(observation), `"input_tokens"`) {
		t.Fatalf("inference observation was not persisted: digest=%d body=%s err=%v", len(observationDigest), observation, err)
	}
	var costFinalization, costCorrection *string
	if err = f.pool.QueryRow(f.ctx, `SELECT latest_cost_finalization_id,latest_cost_correction_id FROM upstream_attempts WHERE id=$1`, a.ID).Scan(&costFinalization, &costCorrection); err != nil || costFinalization == nil || *costFinalization != input.ID || costCorrection != nil {
		t.Fatalf("cost source pointers finalization=%v correction=%v err=%v", costFinalization, costCorrection, err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE upstream_attempts SET cost_amount=cost_amount+1 WHERE id=$1`, a.ID); err == nil {
		err = tx.Commit(f.ctx)
	}
	if err == nil || !strings.Contains(err.Error(), "supplier cost does not match final usage") {
		t.Fatal("altered supplier cost committed", err)
	}
	_ = tx.Rollback(f.ctx)
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO finalization_records(id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,policy_version,attempt_id,retail_usage_state,supplier_usage_state,retail_quantities_canonical,retail_quantities_digest,supplier_quantities_canonical,supplier_quantities_digest)
 SELECT $1,e.tenant_id,e.request_id,e.id,'bad','completed','done','content',true,'stop','r3',e.attempt_id,'confirmed_final','confirmed_final',convert_to('{}','UTF8'),decode(repeat('ab',32),'hex'),convert_to('{}','UTF8'),decode(repeat('cd',32),'hex')
 FROM metering_events e WHERE e.id=(SELECT event_id FROM finalization_records WHERE id=$2)`, uuid.NewString(), input.ID); err == nil {
		err = tx.Commit(f.ctx)
	}
	if err == nil || !strings.Contains(err.Error(), "finalization does not match event") {
		t.Fatal("mismatched finalization committed", err)
	}
	_ = tx.Rollback(f.ctx)
	input.ID = uuid.NewString()
	input.ContentWritten = false
	input.DeliveryStage = "none"
	if _, err = f.store.RecordFinalization(f.ctx, input); err != nil {
		t.Fatal(err)
	}
	var delivered bool
	if err = f.pool.QueryRow(f.ctx, "SELECT content_written FROM finalization_records WHERE id=$1", input.ID).Scan(&delivered); err != nil || !delivered {
		t.Fatal("delivery evidence regressed", err)
	}
	input.ID = uuid.NewString()
	input.Usage.InputTokens = 11
	input.Usage.TotalTokens = 16
	_, err = f.store.RecordFinalization(f.ctx, input)
	assertCode(t, err, "metering_conflict")
	pending, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || pending.Status != "pending_reconciliation" {
		t.Fatal(pending, err)
	}
	f.wallet(t, 880, 120)
	proposal, err := f.store.Reconcile(f.ctx, ReconcileInput{TenantID: f.tenant, RequestID: r.RequestID, ActorID: f.operator, Action: "propose", EvidenceRef: "invoice:verified", Reason: "provider-confirmed", RetailQuantitiesCanonical: retailQuantitiesForRequest(t, f, r.RequestID, 10, 5)})
	if err != nil || proposal.State != "proposed" {
		t.Fatal(proposal, err)
	}
	f.wallet(t, 880, 120)
	approved, err := f.store.Reconcile(f.ctx, ReconcileInput{TenantID: f.tenant, RequestID: r.RequestID, ActorID: f.approver, Action: "approve"})
	if err != nil || approved.Settlement == nil || approved.Settlement.ChargedAmount != 15 {
		t.Fatal(approved, err)
	}
	var approvedSupplier string
	var approvedSupplierNull bool
	var approvedAttempt string
	if err = f.pool.QueryRow(f.ctx, `SELECT f.supplier_usage_state, f.supplier_quantities_canonical IS NULL, e.attempt_id
 FROM finalization_records f JOIN metering_events e ON e.id=f.event_id
 WHERE f.request_id=$1 AND e.source='approved_reconciliation'`, r.RequestID).Scan(&approvedSupplier, &approvedSupplierNull, &approvedAttempt); err != nil || approvedSupplier != "missing" || !approvedSupplierNull || approvedAttempt != a.ID {
		t.Fatal("approved reconciliation confirmed supplier", approvedSupplier, approvedSupplierNull, approvedAttempt, err)
	}
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	missingEvent := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO metering_events(id,tenant_id,request_id,source,source_event_id,happened_at,profile_version,schema_id,schema_digest,variant_key,protocol_outcome,transport_end_reason,delivery_stage,content_written,finish_reason,retail_usage_state,supplier_usage_state)
 SELECT $1,s.tenant_id,s.request_id,'adapter','missing-attempt',clock_timestamp(),s.profile_version,s.schema_id,s.schema_digest,'default','failed','not_sent','none',false,'','missing','missing'
 FROM request_meter_snapshots s WHERE s.request_id=$2`, missingEvent, r.RequestID); err != nil {
		_ = tx.Rollback(f.ctx)
		if !strings.Contains(err.Error(), "null value in column") || !strings.Contains(err.Error(), "attempt_id") {
			t.Fatal(err)
		}
		err = nil
	} else {
		if _, err = tx.Exec(f.ctx, `SELECT meter_record_event_quantities($1,$2,'missing',0,0)`, missingEvent, f.tenant); err == nil {
			err = tx.Commit(f.ctx)
		}
		if err == nil || !strings.Contains(err.Error(), "observation requires the original attempt") {
			t.Fatal("observation without its attempt committed", err)
		}
		_ = tx.Rollback(f.ctx)
	}
	f.wallet(t, 985, 0)
	correction := CostCorrectionInput{TenantID: f.tenant, AttemptID: a.ID, OperationID: "invoice:1", EvidenceRef: "invoice:controlled", Basis: "invoice", Amount: 20}
	for i := 0; i < 2; i++ {
		if err = f.store.RecordCostCorrection(f.ctx, correction); err != nil {
			t.Fatal(err)
		}
	}
	f.wallet(t, 985, 0)
	correction.Amount = 21
	assertCode(t, f.store.RecordCostCorrection(f.ctx, correction), "idempotency_conflict")
	var correctedFinalization, correctedCorrection *string
	if err = f.pool.QueryRow(f.ctx, `SELECT a.latest_cost_finalization_id,a.latest_cost_correction_id FROM upstream_attempts a WHERE a.id=$1`, a.ID).Scan(&correctedFinalization, &correctedCorrection); err != nil || correctedFinalization != nil || correctedCorrection == nil {
		t.Fatalf("cost correction pointers finalization=%v correction=%v err=%v", correctedFinalization, correctedCorrection, err)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.KnownProviderCost != "20" || report.UnknownProviderCosts != 0 {
		t.Fatal(report, err)
	}
	f.check(t)
}

func TestPostgresRecoveryAndFreeModel(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	f.store.EndRequest(r.RequestID)
	if _, err := f.pool.Exec(f.ctx, "UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1", r.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(f.ctx, "DELETE FROM recovery_jobs WHERE request_id=$1", r.RequestID); err != nil {
		t.Fatal(err)
	}
	worker := New(f.pool)
	for i := 0; i < 2; i++ {
		v, err := worker.Recover(f.ctx, RecoverInput{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && v.Released != 1 {
			t.Fatalf("recovered %+v", v)
		}
		if i == 1 && v.Processed != 0 {
			t.Fatalf("recovery replay %+v", v)
		}
	}
	f.wallet(t, 1000, 0)
	publishBillingDefault(t, f, f.model, 0, 0, time.Now().UTC())
	free := f.reserve(t)
	if free.Amount != 0 {
		t.Fatal("free price did not apply", free)
	}
	f.final(t, free, confirmed(), "completed", "done", "none", false)
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: free.RequestID})
	if err != nil || settled.ChargedAmount != 0 || settled.Status != "settled" {
		t.Fatal(settled, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresJournalConstraintsAndProjectionRollback(t *testing.T) {
	f := integration(t)
	if _, err := f.pool.Exec(f.ctx, "UPDATE journals SET actor='changed'"); err == nil {
		t.Fatal("journal mutation allowed")
	}
	if _, err := f.pool.Exec(f.ctx, "UPDATE journal_lines SET signed_delta=0"); err == nil {
		t.Fatal("line mutation allowed")
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(f.ctx, "INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$1,'invalid','invalid','test')", id, f.tenant); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("empty unbalanced journal accepted")
	}
	var journal, account string
	if err = f.pool.QueryRow(f.ctx, "SELECT j.id,a.id FROM journals j JOIN ledger_accounts a ON a.tenant_id=j.tenant_id LIMIT 1").Scan(&journal, &account); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,0)", journal, account); err == nil {
		t.Fatal("append to committed journal allowed")
	}
	if _, err = f.pool.Exec(f.ctx, `CREATE FUNCTION projection_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'projection crash'; END$$; CREATE TRIGGER injected BEFORE INSERT ON report_totals FOR EACH ROW EXECUTE FUNCTION projection_fail()`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Project(f.ctx, ProjectInput{}); err == nil {
		t.Fatal("projection crash ignored")
	}
	var receipts int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM projection_receipts").Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("receipt committed without report", receipts, err)
	}
	if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER injected ON report_totals"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Project(f.ctx, ProjectInput{}); err != nil {
		t.Fatal(err)
	}
	f.check(t)
}

func TestPostgresCreditIdempotencyAndRoles(t *testing.T) {
	f := integration(t)
	in := CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: "bank:second", ProofRef: "receipt:verified", Amount: 100, IdempotencyKey: "credit-key", RequestHMAC: "credit-hmac"}
	order, err := f.store.CreateCredit(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	same, err := f.store.CreateCredit(f.ctx, in)
	if err != nil || same.ID != order.ID {
		t.Fatal(same, err)
	}
	in.BusinessRef = "bank:changed"
	in.RequestHMAC = "changed"
	_, err = f.store.CreateCredit(f.ctx, in)
	assertCode(t, err, "idempotency_conflict")
	in.IdempotencyKey = "other"
	in.ActorID = f.admin
	_, err = f.store.CreateCredit(f.ctx, in)
	assertCode(t, err, "forbidden")
	if _, err = f.pool.Exec(f.ctx, "UPDATE users SET status='suspended' WHERE id=$1", f.operator); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver})
	assertCode(t, err, "forbidden")
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresCapSnapshotAndRebuild(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	// Editing the catalog after T1 cannot change the retail snapshot in this request.
	publishBillingDefault(t, f, f.model, 9000000, 9000000, time.Now().UTC())
	f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 200, OutputTokens: 20, TotalTokens: 220}, "completed", "done", "content", true)
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 120 || settled.RawRetailAmount != "220" || settled.WaivedRetailAmount != "100" {
		t.Fatal(settled, err)
	}
	_, err = f.store.Reserve(f.ctx, f.input())
	assertCode(t, err, "profile_blocked")
	f.wallet(t, 880, 0)
	// Fresh consumer is built solely from retained outbox and verified against facts.
	rebuilt, err := f.store.Rebuild(f.ctx, "reports-rebuild-test")
	if err != nil || rebuilt == 0 {
		t.Fatalf("rebuild %d %v", rebuilt, err)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Charged != "120" || report.Credited != "1000" || report.Requests != 1 {
		t.Fatal(report, err)
	}
	// Routine worker follows the atomically switched consumer.
	f.fund(t, 50)
	if _, err = f.store.Project(f.ctx, ProjectInput{}); err != nil {
		t.Fatal(err)
	}
	report, err = f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1050" {
		t.Fatal(report, err)
	}
	// An inconsistent inactive projection can never become active.
	if _, err = f.pool.Exec(f.ctx, "INSERT INTO report_totals(consumer,tenant_id,credited) VALUES('bad-rebuild',$1,100)", f.tenant); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Rebuild(f.ctx, "bad-rebuild")
	assertCode(t, err, "projection_mismatch")
	var active string
	if err = f.pool.QueryRow(f.ctx, "SELECT consumer FROM active_report_projection WHERE singleton").Scan(&active); err != nil || active != "reports-rebuild-test" {
		t.Fatal(active, err)
	}
	f.check(t)
}

func TestPostgresTenantJournalAndQueries(t *testing.T) {
	f := integration(t)
	otherTenant := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO tenants(id,name) VALUES($1,'other')", otherTenant); err != nil {
		t.Fatal(err)
	}
	account := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,$2,'AVAILABLE')", account, otherTenant); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	journal := uuid.NewString()
	if _, err = tx.Exec(f.ctx, "INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$1,'invalid','invalid','test')", journal, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,0)", journal, account); err == nil {
		t.Fatal("cross-tenant line accepted")
	}
	_ = tx.Rollback(f.ctx)
	journals, err := f.store.Journals(f.ctx, Query{TenantID: otherTenant})
	if err != nil || len(journals) != 0 {
		t.Fatal(journals, err)
	}
	requests, err := f.store.Requests(f.ctx, Query{TenantID: otherTenant})
	if err != nil || len(requests) != 0 {
		t.Fatal(requests, err)
	}
	f.check(t)
}

func TestPostgresPendingManualRelease(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	f.final(t, r, Usage{Status: "missing"}, "unknown", "unknown", "unknown", false)
	if err := f.store.MarkPending(f.ctx, PendingInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "usage_unconfirmed"}); err != nil {
		t.Fatal(err)
	}
	released, err := f.store.Reconcile(f.ctx, ReconcileInput{TenantID: f.tenant, RequestID: r.RequestID, ActorID: f.approver, Action: "release", Reason: "customer-favorable closure"})
	if err != nil || released.Settlement == nil || released.Settlement.Status != "released" {
		t.Fatal(released, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresFinancialBoundaryRollback(t *testing.T) {
	for _, tc := range []struct{ operation, table, event string }{{"credit", "projection_outbox", "INSERT"}, {"settle", "api_keys", "UPDATE"}, {"settle", "audit_events", "INSERT"}, {"settle", "projection_outbox", "INSERT"}, {"refund", "projection_outbox", "INSERT"}} {
		t.Run(tc.operation+"_"+tc.table, func(t *testing.T) {
			f := integration(t)
			var execute func() error
			switch tc.operation {
			case "credit":
				id := uuid.NewString()
				order, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: id, ProofRef: "receipt:verified", Amount: 200, IdempotencyKey: id, RequestHMAC: id})
				if err != nil {
					t.Fatal(err)
				}
				execute = func() error {
					_, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver})
					return err
				}
			case "settle":
				r := f.reserve(t)
				f.final(t, r, confirmed(), "completed", "done", "content", true)
				execute = func() error {
					_, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
					return err
				}
			case "refund":
				r := f.reserve(t)
				f.final(t, r, confirmed(), "completed", "done", "content", true)
				settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
				if err != nil {
					t.Fatal(err)
				}
				id := uuid.NewString()
				order, err := f.store.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: id, Reason: "approved", Amount: 10, IdempotencyKey: id, RequestHMAC: id})
				if err != nil {
					t.Fatal(err)
				}
				execute = func() error {
					_, err := f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver})
					return err
				}
			}
			type state struct {
				Available, Held                  Money
				Spent, KeyHeld, Journals, Outbox int64
			}
			snapshot := func() state {
				t.Helper()
				w, err := f.store.Wallet(f.ctx, Query{TenantID: f.tenant})
				if err != nil {
					t.Fatal(err)
				}
				v := state{Available: w.Available, Held: w.Held}
				if err = f.pool.QueryRow(f.ctx, "SELECT spent,held,(SELECT count(*) FROM journals),(SELECT count(*) FROM projection_outbox) FROM api_keys WHERE id=$1", f.key).Scan(&v.Spent, &v.KeyHeld, &v.Journals, &v.Outbox); err != nil {
					t.Fatal(err)
				}
				return v
			}
			before := snapshot()
			if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_boundary() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected boundary crash'; END$$`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "CREATE TRIGGER injected BEFORE "+tc.event+" ON "+tc.table+" FOR EACH ROW EXECUTE FUNCTION fail_boundary()"); err != nil {
				t.Fatal(err)
			}
			if err := execute(); err == nil {
				t.Fatal("transaction unexpectedly succeeded")
			}
			after := snapshot()
			if before != after {
				t.Fatalf("partial financial commit before=%+v after=%+v", before, after)
			}
			f.check(t)
			if _, err := f.pool.Exec(f.ctx, "DROP TRIGGER injected ON "+tc.table); err != nil {
				t.Fatal(err)
			}
			if err := execute(); err != nil {
				t.Fatal(err)
			}
			f.check(t)
		})
	}
}

func TestPostgresRestrictedProjectionRole(t *testing.T) {
	f := integration(t)
	roleName := "billing_projector_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{roleName}.Sanitize()
	if _, err := f.pool.Exec(f.ctx, "CREATE ROLE "+quoted+" NOLOGIN"); err != nil {
		t.Fatalf("test database must allow creating a restricted projector role: %v", err)
	}
	var schema string
	if err := f.pool.QueryRow(f.ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	grants := []string{"GRANT USAGE ON SCHEMA " + pgx.Identifier{schema}.Sanitize() + " TO " + quoted, "GRANT SELECT ON active_report_projection,projection_outbox TO " + quoted, "GRANT SELECT,INSERT ON projection_receipts TO " + quoted, "GRANT SELECT,INSERT,UPDATE ON report_totals TO " + quoted}
	for _, sql := range grants {
		if _, err := f.pool.Exec(f.ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	cfg := f.pool.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+quoted)
		return err
	}
	restricted, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		restricted.Close()
		_, _ = f.pool.Exec(f.ctx, "DROP OWNED BY "+quoted)
		_, _ = f.pool.Exec(f.ctx, "DROP ROLE "+quoted)
	})
	projector := New(restricted)
	if _, err = projector.Project(f.ctx, ProjectInput{}); err != nil {
		t.Fatalf("minimal permission projector: %v", err)
	}
	if _, err = restricted.Exec(f.ctx, "UPDATE api_keys SET spent=0"); err == nil {
		t.Fatal("projector can mutate key budgets")
	}
	if _, err = restricted.Exec(f.ctx, "UPDATE ledger_accounts SET balance=0"); err == nil {
		t.Fatal("projector can mutate money")
	}
	if _, err = restricted.Exec(f.ctx, "INSERT INTO journals(tenant_id,operation_id,kind,reference,actor) VALUES($1,'forged','invalid','invalid','projector')", f.tenant); err == nil {
		t.Fatal("projector can create financial journals")
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresRetryNeedsLatestSafeEvidence(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	a, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1})
	if err != nil {
		t.Fatal(err)
	}
	final := FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: a.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"}
	if _, err = f.store.RecordFinalization(f.ctx, final); err != nil {
		t.Fatal(err)
	}
	// A newer ambiguous transport observation invalidates the older no-send retry proof.
	final.SourceEventID = uuid.NewString()
	final.TransportEndReason = "unknown"
	final.ProtocolOutcome = "unknown"
	final.DeliveryStage = "unknown"
	if _, err = f.store.RecordFinalization(f.ctx, final); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 2})
	assertCode(t, err, "unsafe_retry")
	// A committed response can never become uncommitted in later appended local facts.
	final.SourceEventID = uuid.NewString()
	final.DeliveryStage = "committed"
	if _, err = f.store.RecordFinalization(f.ctx, final); err != nil {
		t.Fatal(err)
	}
	final.SourceEventID = uuid.NewString()
	final.DeliveryStage = "none"
	final.TransportEndReason = "not_sent"
	got, err := f.store.RecordFinalization(f.ctx, final)
	if err != nil || got.DeliveryStage != "committed" {
		t.Fatal(got, err)
	}
	_, err = f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 2})
	assertCode(t, err, "unsafe_retry")
	f.check(t)
}

func TestPostgresAppendReplayAfterDeliveryProgress(t *testing.T) {
	f := integration(t)
	r := f.reserve(t)
	original := FinalizationInput{ID: uuid.NewString(), TenantID: f.tenant, RequestID: r.RequestID, AttemptID: f.ensureAttempt(t, r.RequestID), Source: "adapter", SourceEventID: "stable-usage", Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none"}
	if _, err := f.store.RecordFinalization(f.ctx, original); err != nil {
		t.Fatal(err)
	}
	progress := original
	progress.ID = uuid.NewString()
	progress.ContentWritten = true
	progress.DeliveryStage = "content"
	if _, err := f.store.RecordFinalization(f.ctx, progress); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.store.RecordFinalization(f.ctx, original)
	if err != nil || replayed.ID != original.ID || replayed.ContentWritten {
		t.Fatal(replayed, err)
	}
	var latest string
	if err = f.pool.QueryRow(f.ctx, "SELECT latest_finalization_id FROM request_records WHERE id=$1", r.RequestID).Scan(&latest); err != nil || latest != progress.ID {
		t.Fatal("append replay changed latest facts", latest, err)
	}
	original.ProtocolOutcome = "failed"
	_, err = f.store.RecordFinalization(f.ctx, original)
	assertCode(t, err, "idempotency_conflict")
	f.check(t)
}

func TestPostgresConcurrentProjectionAndRecovery(t *testing.T) {
	f := integration(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = f.store.Project(f.ctx, ProjectInput{}) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1000" {
		t.Fatal(report, err)
	}
	r := f.reserve(t)
	f.store.EndRequest(r.RequestID)
	if _, err = f.pool.Exec(f.ctx, "UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1", r.RequestID); err != nil {
		t.Fatal(err)
	}
	results := make([]RecoveryResult, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = New(f.pool).Recover(f.ctx, RecoverInput{}) }(i)
	}
	wg.Wait()
	total := 0
	for i, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
		total += results[i].Processed
	}
	if total != 1 {
		t.Fatalf("concurrent recovery processed %d (%+v)", total, results)
	}
	var attempts int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE request_id=$1", r.RequestID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("recovery created upstream generation", attempts, err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}

func TestPostgresNarrowAccountAndSharedModelLocks(t *testing.T) {
	f := integration(t)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM ledger_accounts WHERE tenant_id IS NULL AND kind='FUNDING_CLEARING' FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM models WHERE id=$1 FOR SHARE", f.model); err != nil {
		t.Fatal(err)
	}
	// Normal inference neither changes the funding-clearing account nor tightens
	// model policy, so unrelated funding and shared model locks must not serialize it.
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	r, err := f.store.Reserve(ctx, f.input())
	if err != nil {
		t.Fatal("reservation acquired unrelated global funding lock", err)
	}
	if _, err = f.store.RecordFinalization(ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: f.ensureAttempt(t, r.RequestID), Source: "adapter", SourceEventID: "normal", Usage: confirmed(), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal("normal finalization required exclusive model lock", err)
	}
	settled, err := f.store.Settle(ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 15 {
		t.Fatal("settlement acquired unrelated funding or exclusive model lock", settled, err)
	}
	_ = blocker.Rollback(f.ctx)
	f.wallet(t, 985, 0)
	f.check(t)
}

func TestPostgresPublishedRouteSnapshot(t *testing.T) {
	f := integration(t)
	publishFixtureRoute(t, f, 1, "upstream")
	policy2 := uuid.NewString()
	in := f.input()
	in.RouteRevision = 1
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	attempt := AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "unpublished", Sequence: 1}
	_, err = f.store.RecordAttempt(f.ctx, attempt)
	assertCode(t, err, "unpublished_binding")
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,2)", policy2, f.model); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,10)", policy2, f.channel, f.version); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE models SET active_revision=2 WHERE id=$1", f.model); err != nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// An already reserved request still uses its immutable revision 1 snapshot.
	attempt.UpstreamModel = "upstream"
	if _, err = f.store.RecordAttempt(f.ctx, attempt); err != nil {
		t.Fatal(err)
	}
	stale := f.input()
	stale.RouteRevision = 1
	_, err = f.store.Reserve(f.ctx, stale)
	assertCode(t, err, "route_changed")
	var saved int64
	if err = f.pool.QueryRow(f.ctx, "SELECT route_revision FROM request_records WHERE id=$1", r.RequestID).Scan(&saved); err != nil || saved != 1 {
		t.Fatal(saved, err)
	}
	f.check(t)
}

func TestPostgresExplicitlyAbortedTransactionRetries(t *testing.T) {
	for _, code := range []string{"40001", "40P01"} {
		t.Run(code, func(t *testing.T) {
			f := integration(t)
			if _, err := f.pool.Exec(f.ctx, `CREATE SEQUENCE retry_attempt;
CREATE FUNCTION abort_twice() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN
 IF NEW.action='billing.reserve' AND nextval('retry_attempt')<3 THEN RAISE EXCEPTION 'explicitly aborted retry fixture' USING ERRCODE='`+code+`'; END IF;
 RETURN NEW; END$$;
CREATE TRIGGER injected BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION abort_twice()`); err != nil {
				t.Fatal(err)
			}
			r, err := f.store.Reserve(f.ctx, f.input())
			if err != nil {
				t.Fatal(err)
			}
			f.wallet(t, 880, 120)
			var attempts, journals, requests int
			if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT last_value FROM retry_attempt),(SELECT count(*) FROM journals),(SELECT count(*) FROM request_records)").Scan(&attempts, &journals, &requests); err != nil || attempts != 3 || journals != 2 || requests != 1 {
				t.Fatal("retry duplicated or omitted facts", attempts, journals, requests, err)
			}
			if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
				t.Fatal(err)
			}
			f.check(t)
		})
	}
	t.Run("retry_is_bounded", func(t *testing.T) {
		f := integration(t)
		if _, err := f.pool.Exec(f.ctx, `CREATE SEQUENCE retry_attempt;
CREATE FUNCTION always_abort() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN
 IF NEW.action='billing.reserve' THEN PERFORM nextval('retry_attempt'); RAISE EXCEPTION 'explicitly aborted bounded fixture' USING ERRCODE='40001'; END IF;
 RETURN NEW; END$$;
CREATE TRIGGER injected BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION always_abort()`); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.Reserve(f.ctx, f.input()); err == nil {
			t.Fatal("unbounded transaction unexpectedly succeeded")
		}
		f.wallet(t, 1000, 0)
		var attempts int
		if err := f.pool.QueryRow(f.ctx, "SELECT last_value FROM retry_attempt").Scan(&attempts); err != nil || attempts != 3 {
			t.Fatal("retry exceeded two retries", attempts, err)
		}
		f.check(t)
	})
}

func TestPostgresPreciseBindingIsolationAdmission(t *testing.T) {
	f := integration(t)
	publishFixtureRoute(t, f, 1, "upstream", "unaffected-model")
	reserveAt := func(revision int64) Reservation {
		t.Helper()
		in := f.input()
		in.RouteRevision = revision
		r, err := f.store.Reserve(f.ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before, newAttempt, otherModel := reserveAt(1), reserveAt(1), reserveAt(1)
	input := AttemptInput{TenantID: f.tenant, RequestID: before.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
	prepared, err := f.store.RecordAttempt(f.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "SELECT id FROM channels WHERE id=$1 FOR UPDATE", f.channel); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", f.version); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	input.RequestID = newAttempt.RequestID
	_, err = f.store.RecordAttempt(f.ctx, input)
	assertCode(t, err, "binding_unavailable")
	assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: prepared.ID}), "binding_unavailable")
	input.RequestID = otherModel.RequestID
	input.UpstreamModel = "unaffected-model"
	allowed, err := f.store.RecordAttempt(f.ctx, input)
	if err != nil {
		t.Fatal("isolation affected a different model", err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: allowed.ID}); err != nil {
		t.Fatal(err)
	}
	newVersion, newCredential, newPolicy := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, "INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture-new')", newCredential, f.channel); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `WITH version AS (INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3) RETURNING id,channel_id,credential_id) INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) SELECT id,channel_id,credential_id,1,'primary',1,1000 FROM version`, newVersion, f.channel, newCredential); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE channels SET current_version=$2 WHERE id=$1", f.channel, newVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,2)", newPolicy, f.model); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,10)", newPolicy, f.channel, newVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE models SET active_revision=2 WHERE id=$1", f.model); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	newer := reserveAt(2)
	input.RequestID = newer.RequestID
	input.UpstreamModel = "upstream"
	input.ChannelVersionID = newVersion
	input.CredentialID = newCredential
	fixed, err := f.store.RecordAttempt(f.ctx, input)
	if err != nil {
		t.Fatal("isolation incorrectly inherited by new version", err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: fixed.ID}); err != nil {
		t.Fatal(err)
	}
	assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: prepared.ID}), "binding_unavailable")
	f.check(t)
}

func TestPostgresBindingIsolationSharesChannelLock(t *testing.T) {
	for _, mode := range []string{"record_attempt", "prepare_dispatch"} {
		t.Run(mode, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			input := AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
			var attempt Attempt
			var err error
			if mode == "prepare_dispatch" {
				attempt, err = f.store.RecordAttempt(f.ctx, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			if _, err = tx.Exec(f.ctx, "SELECT id FROM channels WHERE id=$1 FOR UPDATE", f.channel); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(f.ctx, "INSERT INTO route_binding_isolations(channel_version_id,upstream_model,reason) VALUES($1,'upstream','model_configuration_error')", f.version); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if mode == "record_attempt" {
					_, err := f.store.RecordAttempt(f.ctx, input)
					done <- err
				} else {
					done <- f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: attempt.ID})
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("admission bypassed an uncommitted isolation parent lock: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			assertCode(t, <-done, "binding_unavailable")
			f.check(t)
		})
	}
}
