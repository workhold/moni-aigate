package maintenancetrust

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// These are isolated business-history fixtures. All money and operation rows
// come from the real restricted APP APIs. The owner only supplies the original
// catalog/identities, installs the frozen candidate and fixes the test clock
// before source birth; it never rewrites a historical row or supplies a report.
func periodicPrefixBusinessAPP(t *testing.T, ctx context.Context, f *snapshotFixture) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("prefix business APP pool configuration")
	}
	cfg.ConnConfig = f.cfg.Copy()
	cfg.ConnConfig.User = f.app
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	cfg.MinConns, cfg.MaxConns = 0, 3
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("prefix business actual APP pool")
	}
	t.Cleanup(app.Close)
	var actual bool
	if app.QueryRow(ctx, `SELECT current_user=session_user AND current_user=$1::name`, f.app).Scan(&actual) != nil || !actual {
		t.Fatal("prefix business must use the actual restricted APP LOGIN")
	}
	return app
}

const periodicPrefixBusinessUTC6 = "2006-01-02T15:04:05.000000Z"

func periodicPrefixBusinessClock(t *testing.T, ctx context.Context, owner *pgxpool.Pool, schema string, at time.Time) {
	t.Helper()
	stamp := at.UTC().Format(periodicPrefixBusinessUTC6)
	if !periodicbudget.ValidUTC6(stamp) {
		t.Fatal("prefix business invalid isolated clock")
	}
	sql := "CREATE OR REPLACE FUNCTION " + pgx.Identifier{schema, "pb_now"}.Sanitize() +
		"() RETURNS timestamptz LANGUAGE sql VOLATILE SET search_path TO " + pgx.Identifier{schema}.Sanitize() +
		",pg_catalog,pg_temp AS $$ SELECT '" + stamp + "'::timestamptz $$"
	if _, err := owner.Exec(ctx, sql); err != nil {
		t.Fatal("prefix business birth-only isolated clock", err)
	}
}

func periodicPrefixBusinessRestoreClock(t *testing.T, ctx context.Context, owner *pgxpool.Pool, schema string) {
	t.Helper()
	// Restoration comes from authenticated original020 bytes, never a live
	// pg_proc export used to change the expected catalog after the fact.
	raw := snapshotFixedSource(t, snapshotRepositoryRoot(t), "migrations/020_periodic_budgets.sql", "bf5ca4007356fe6b560a7ee263d11b7e7cf4b49bffac11f7a8efb7cb28d52c99")
	const prefix = "CREATE FUNCTION pb_now()"
	start := strings.Index(string(raw), prefix)
	if start < 0 || strings.Count(string(raw), prefix) != 1 {
		t.Fatal("prefix business original020 clock statement")
	}
	end := strings.Index(string(raw[start:]), ";\n")
	if end < 0 {
		t.Fatal("prefix business original020 clock terminator")
	}
	statement := string(raw[start : start+end+1])
	statement = strings.Replace(statement, prefix, "CREATE OR REPLACE FUNCTION "+pgx.Identifier{schema, "pb_now"}.Sanitize()+"()", 1)
	if _, err := owner.Exec(ctx, statement); err != nil {
		t.Fatal("prefix business restore authenticated original clock body", err)
	}
	if _, err := owner.Exec(ctx, "ALTER FUNCTION "+pgx.Identifier{schema, "pb_now"}.Sanitize()+"() SET search_path TO "+pgx.Identifier{schema}.Sanitize()+",pg_catalog,pg_temp"); err != nil {
		t.Fatal("prefix business restore original020 fixed search path")
	}
}

type periodicPrefixBusinessHistory struct {
	runtimeProcurementHistory
	user, key, admin, model, channel, version, credential, upstream string
	controls                                                        map[string]string
	commands                                                        uint64
}

func periodicPrefixBusinessConfigure(t *testing.T, ctx context.Context, app *pgxpool.Pool, h *periodicPrefixBusinessHistory, revision string) map[string]periodicbudget.ConfigureResult {
	t.Helper()
	commander, err := periodicbudget.NewCommander(app)
	if err != nil {
		t.Fatal("prefix business actual APP Commander")
	}
	out := map[string]periodicbudget.ConfigureResult{}
	for _, scope := range []string{"tenant", "key", "procurement"} {
		in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: revision, PeriodKind: "utc_day", Enabled: true, LimitAmount: "1000000"}
		var result periodicbudget.ConfigureResult
		switch scope {
		case "tenant":
			result, err = commander.ConfigurePlatformTenant(ctx, h.admin, h.tenant, in)
		case "key":
			result, err = commander.ConfigureKey(ctx, h.user, h.tenant, h.key, in)
		case "procurement":
			result, err = commander.ConfigureProcurement(ctx, h.admin, h.account, in)
		}
		if err != nil || result.Receipt.Operation.Revision != "1" || result.Created != (revision == "0") {
			t.Fatalf("prefix business original %s configuration/no-op: %v", scope, err)
		}
		if h.controls == nil {
			h.controls = map[string]string{}
		}
		if revision == "0" {
			h.controls[scope] = result.Receipt.Operation.ResourceID
		} else if result.Receipt.Operation.ResourceID != h.controls[scope] {
			t.Fatal("prefix business no-op changed the original control")
		}
		out[scope] = result
		h.commands++
	}
	return out
}

func periodicPrefixBusinessBase(t *testing.T, ctx context.Context, f *snapshotFixture, owner, app *pgxpool.Pool, crossDay bool) periodicPrefixBusinessHistory {
	t.Helper()
	h := periodicPrefixBusinessHistory{runtimeProcurementHistory: runtimeProcurementHistory{tenant: uuid.NewString()},
		user: uuid.NewString(), key: uuid.NewString(), admin: uuid.NewString(), model: uuid.NewString(),
		channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString(), upstream: "upstream"}
	operator, approver := uuid.NewString(), uuid.NewString()
	var today time.Time
	if owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&today) != nil {
		t.Fatal("prefix business original database clock")
	}
	// This local catalog is born with an explicit historical-effective quote.
	// The existing same-day runtime fixture must remain unchanged; its quote
	// does not exist at the simulated admission time of a cross-day source.
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal("prefix business original identity/catalog transaction")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal("prefix business original identity/catalog birth", err)
		}
	}
	exec(`INSERT INTO tenants(id,name) VALUES($1,'prefix business')`, h.tenant)
	for _, identity := range []struct{ id, role string }{{h.user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}, {h.admin, "admin"}} {
		exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-local-fixture',$3)`, identity.id, identity.id+"@example.invalid", identity.role)
	}
	exec(`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, h.tenant, h.user)
	exec(`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'prefix-business',$2,'v1')`, h.model,
		`{"version":"v1","model":"prefix-business","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`)
	exec(`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, h.tenant, h.model)
	exec(`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'prefix business',$1,$1,$4,1000)`, h.key, h.tenant, h.user, []string{h.model})
	exec(`INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1000000,1000000,clock_timestamp()-interval '7 days',$2)`, h.model, operator)
	exec(`INSERT INTO channels(id,name,current_version) VALUES($1,'prefix business',$2)`, h.channel, h.version)
	exec(`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-local-fixture')`, h.credential, h.channel)
	exec(`INSERT INTO channel_versions(id,channel_id,base_url,credential_id,region) VALUES($1,$2,'https://provider.invalid',$3,'global')`, h.version, h.channel, h.credential)
	exec(`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, h.version, h.channel, h.credential)
	exec(`INSERT INTO cost_versions(channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,'upstream',2000000,2000000,'contract',clock_timestamp()-interval '7 days')`, h.channel)
	if tx.Commit(ctx) != nil {
		t.Fatal("prefix business original catalog commit")
	}
	store := billing.New(app)
	creditID := uuid.NewString()
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: h.tenant, ActorID: operator, BusinessRef: creditID,
		ProofRef: "receipt:prefix-business", Amount: 1000, IdempotencyKey: creditID, RequestHMAC: creditID})
	if err != nil {
		t.Fatal("prefix business actual APP credit proposal", err)
	}
	if _, err = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: h.tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal("prefix business actual APP different-actor credit approval", err)
	}
	p := procurement.New(app)
	created, err := p.CreateAccount(ctx, h.admin, procurement.CreateInput{OperationID: uuid.NewString(), Label: "prefix business supplier", Provider: "isolated-local", State: "active", LimitAmount: "1000"})
	if err != nil || created.Status != "applied" || created.TargetID == "" {
		t.Fatal("prefix business actual APP supplier account", err)
	}
	h.account = created.TargetID
	var revision string
	if app.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, h.channel).Scan(&revision) != nil {
		t.Fatal("prefix business original channel revision")
	}
	bound, err := p.BindCredential(ctx, h.admin, procurement.BindingInput{OperationID: uuid.NewString(), ChannelID: h.channel,
		CredentialID: h.credential, AccountID: h.account, ExpectedChannelRevision: revision})
	if err != nil || bound.Status != "applied" {
		t.Fatal("prefix business actual APP supplier credential binding", err)
	}
	if crossDay {
		periodicPrefixBusinessClock(t, ctx, owner, f.schema, today.AddDate(0, 0, -2))
	}
	periodicPrefixBusinessConfigure(t, ctx, app, &h, "0")
	if crossDay {
		periodicPrefixBusinessClock(t, ctx, owner, f.schema, today.AddDate(0, 0, -1))
	}
	source := periodicPrefixBusinessReserve(t, ctx, app, store, h, true)
	h.request, h.attempt = source.request, source.attempt
	final, err := store.RecordFinalization(ctx, billing.FinalizationInput{ID: uuid.NewString(), TenantID: h.tenant, RequestID: h.request, AttemptID: h.attempt,
		Source: "prefix-business", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true})
	if err != nil {
		t.Fatal("prefix business actual APP baseline supplier cost24", err)
	}
	h.finalization, h.event = final.ID, final.EventID
	settled, err := store.Settle(ctx, billing.SettleInput{TenantID: h.tenant, RequestID: h.request})
	if err != nil || settled.ChargedAmount != 12 {
		t.Fatal("prefix business actual APP baseline customer charge12", err)
	}
	store.EndRequest(h.request)
	return h
}

type periodicPrefixBusinessSource struct{ request, attempt string }

func periodicPrefixBusinessReserve(t *testing.T, ctx context.Context, app *pgxpool.Pool, store *billing.Store, h periodicPrefixBusinessHistory, dispatch bool) periodicPrefixBusinessSource {
	t.Helper()
	var now time.Time
	if app.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		t.Fatal("prefix business actual request deadline clock")
	}
	source := periodicPrefixBusinessSource{request: uuid.NewString()}
	reserved, err := store.Reserve(ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: h.tenant, UserID: h.user, KeyID: h.key},
		RequestID: source.request, ModelID: h.model, IdempotencyKey: source.request, RequestHMAC: source.request,
		ProfileVersion: "v1", PolicyVersion: "r3", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: now.Add(2 * time.Minute)})
	if err != nil || reserved.Amount != 120 {
		t.Fatalf("prefix business original customer upper120: amount=%d error=%v", reserved.Amount, err)
	}
	t.Cleanup(func() { store.EndRequest(source.request) })
	var quote routing.CostQuote
	if app.QueryRow(ctx, `WITH at AS MATERIALIZED (SELECT clock_timestamp() AS stamp)
 SELECT id,input_rate,output_rate,basis FROM cost_versions,at
 WHERE channel_id=$1 AND upstream_model=$2 AND effective_at<=at.stamp
 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, h.channel, h.upstream).
		Scan(&quote.VersionID, &quote.InputRate, &quote.OutputRate, &quote.Basis) != nil {
		t.Fatal("prefix business original current supplier quote")
	}
	attempt, err := store.RecordAttempt(ctx, billing.AttemptInput{TenantID: h.tenant, RequestID: source.request, ChannelID: h.channel,
		ChannelVersionID: h.version, CredentialID: h.credential, UpstreamModel: h.upstream, Sequence: 1, CostQuote: &quote})
	if err != nil {
		t.Fatal("prefix business original supplier upper240", err)
	}
	source.attempt = attempt.ID
	if dispatch {
		if err = store.PrepareDispatch(ctx, billing.DispatchInput{TenantID: h.tenant, AttemptID: source.attempt}); err != nil {
			t.Fatal("prefix business actual supplier dispatch permit", err)
		}
	}
	return source
}

func periodicPrefixBusinessFinish(t *testing.T, ctx context.Context, store *billing.Store, h periodicPrefixBusinessHistory, source periodicPrefixBusinessSource, release bool) {
	t.Helper()
	in := billing.FinalizationInput{ID: uuid.NewString(), TenantID: h.tenant, RequestID: source.request, AttemptID: source.attempt,
		Source: "prefix-business", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true}
	if release {
		in.Usage = billing.Usage{Status: "missing"}
		in.ProtocolOutcome, in.TransportEndReason, in.DeliveryStage, in.ContentWritten = "failed", "rejected_no_consumption", "none", false
	}
	if _, err := store.RecordFinalization(ctx, in); err != nil {
		t.Fatal("prefix business original supplier finalization", err)
	}
	if release {
		if _, err := store.Release(ctx, billing.ReleaseInput{TenantID: h.tenant, RequestID: source.request, Reason: "not_sent"}); err != nil {
			t.Fatal("prefix business actual customer/key release", err)
		}
	} else {
		result, err := store.Settle(ctx, billing.SettleInput{TenantID: h.tenant, RequestID: source.request})
		if err != nil || result.ChargedAmount != 15 {
			t.Fatal("prefix business actual customer/key settle15", err)
		}
	}
	store.EndRequest(source.request)
}

func periodicPrefixBusinessStage(t *testing.T, ctx context.Context, app *pgxpool.Pool, h *periodicPrefixBusinessHistory, customerCarry, supplierCarry string, current bool) {
	t.Helper()
	for scope, out := range periodicPrefixBusinessConfigure(t, ctx, app, h, "1") {
		carry, held := customerCarry, "120"
		if scope == "procurement" {
			carry, held = supplierCarry, "240"
		}
		view := out.Receipt.Result
		if view.CarryHeld != carry || (view.CurrentWindow != nil) != current || current && (view.CurrentWindow.HeldAmount != held || view.CurrentWindow.SpentAmount != "0") {
			t.Fatalf("prefix business %s snapshot moved historical carry into the new window", scope)
		}
	}
}

const periodicPrefixBusinessNoopSQL = `INSERT INTO periodic_budget_operations
 (operation_id,kind,actor,scope,target,resource_id,safe_input)
 VALUES($1,'tenant_periodic_budget.configure',$2,'platform',
 '/api/platform/tenants/'||$3||'/periodic-budget',$4,
 jsonb_build_object('expected_revision','1','period_kind','utc_day',
 'enabled',true,'limit_amount','1000000')) RETURNING operation_id`

func periodicPrefixBusinessWait(t *testing.T, ctx context.Context, f *snapshotFixture, worker, blocker observedBackend, done <-chan error) {
	t.Helper()
	watch, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.admin.QueryRow(watch, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1::integer
 AND backend_start=$2 AND usename=$3 AND state='active' AND wait_event_type='Lock')
 AND EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$4::integer AND backend_start=$5 AND usename=$3)
 AND $4::integer=ANY(pg_catalog.pg_blocking_pids($1::integer))`, worker.pid, worker.start, f.app, blocker.pid, blocker.start).Scan(&waiting); err != nil {
			t.Fatal("prefix business independent exact backend lock observation", err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("prefix business command completed before independent lock barrier (%T)", err)
		case <-watch.Done():
			t.Fatal("prefix business did not observe the real SQL lock barrier")
		case <-ticker.C:
		}
	}
}

func periodicPrefixBusinessConcurrentWindow(t *testing.T, ctx context.Context, f *snapshotFixture, app *pgxpool.Pool, h *periodicPrefixBusinessHistory) {
	t.Helper()
	blocker, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal("prefix business separate APP window transaction")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = blocker.Rollback(cleanup)
	}()
	var window string
	if blocker.QueryRow(ctx, `SELECT pb_observe($1,pb_now())`, h.controls["tenant"]).Scan(&window) != nil || window == "" {
		t.Fatal("prefix business original uncommitted current-day window")
	}
	worker, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal("prefix business separate APP command transaction")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = worker.Rollback(cleanup)
	}()
	identity := func(tx pgx.Tx) observedBackend {
		var backend observedBackend
		var actual bool
		if tx.QueryRow(ctx, `SELECT current_user=session_user AND current_user=$1::name,pg_backend_pid(),backend_start
 FROM pg_catalog.pg_stat_activity WHERE pid=pg_backend_pid()`, f.app).Scan(&actual, &backend.pid, &backend.start) != nil || !actual {
			t.Fatal("prefix business lock participants must be actual APP LOGINs")
		}
		return backend
	}
	blockerID, workerID := identity(blocker), identity(worker)
	if blockerID.pid == workerID.pid {
		t.Fatal("prefix business lock participants share a backend")
	}
	op := uuid.NewString()
	work, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	done, joined := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(joined)
		var returned string
		err := worker.QueryRow(work, periodicPrefixBusinessNoopSQL, op, h.admin, h.tenant, h.controls["tenant"]).Scan(&returned)
		if err == nil && returned != op {
			err = fmt.Errorf("prefix business returned a different operation")
		}
		done <- err
	}()
	// Always cancel and join before rolling back/releasing either connection.
	defer func() { cancel(); <-joined }()
	periodicPrefixBusinessWait(t, ctx, f, workerID, blockerID, done)
	if blocker.Commit(ctx) != nil {
		t.Fatal("prefix business publish independently observed window birth")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("prefix business original command after real lock wait", err)
		}
	case <-work.Done():
		t.Fatal("prefix business blocked command did not complete")
	}
	if worker.Commit(ctx) != nil {
		t.Fatal("prefix business original post-wait receipt commit")
	}
	var exact bool
	if app.QueryRow(ctx, `SELECT o.result_snapshot->'current_window'->>'id'=$2
 AND o.result_snapshot->'current_window'->>'held_amount'='0'
 AND o.result_snapshot->>'carry_held'='240'
 AND j.event_seq=b.event_seq AND j.ordinal>b.ordinal
 FROM periodic_budget_operations o JOIN periodic_budget_observation_journal j ON j.id=o.observation_id
 JOIN periodic_budget_observation_journal b ON b.control_id=j.control_id AND b.kind='window_birth' AND b.source_id=$2
 WHERE o.operation_id=$1`, op, window).Scan(&exact) != nil || !exact {
		t.Fatal("prefix business receipt reused a pre-wait window snapshot or lost the same-cut order")
	}
	h.commands++
}

func periodicPrefixBusinessReturningWindow(t *testing.T, ctx context.Context, app *pgxpool.Pool, h *periodicPrefixBusinessHistory) {
	t.Helper()
	op := uuid.NewString()
	var window string
	sql := `INSERT INTO periodic_budget_operations(operation_id,kind,actor,scope,target,resource_id,safe_input)
 VALUES($1,'key_periodic_budget.configure',$2,$3,'/api/keys/'||$4||'/periodic-budget',$5,
 jsonb_build_object('expected_revision','1','period_kind','utc_day','enabled',true,'limit_amount','1000000'))
 RETURNING pb_observe(resource_id,pb_now())`
	if app.QueryRow(ctx, sql, op, h.user, h.tenant, h.key, h.controls["key"]).Scan(&window) != nil || window == "" {
		t.Fatal("prefix business one-statement RETURNING window birth")
	}
	var exact bool
	if app.QueryRow(ctx, `SELECT o.result_snapshot->'current_window'='null'::jsonb AND j.window_id IS NULL
 AND j.event_seq=b.event_seq AND j.ordinal<b.ordinal AND o.result_snapshot->>'carry_held'='240'
 FROM periodic_budget_operations o JOIN periodic_budget_observation_journal j ON j.id=o.observation_id
 JOIN periodic_budget_observation_journal b ON b.control_id=j.control_id AND b.kind='window_birth' AND b.source_id=$2
 WHERE o.operation_id=$1`, op, window).Scan(&exact) != nil || !exact {
		t.Fatal("prefix business RETURNING birth changed the preceding same-cut command snapshot")
	}
	h.commands++
}

func TestPeriodicPrefixMaintenancePostgresBusinessCrossWindowAndConcurrentObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var history periodicPrefixBusinessHistory
	f := newPeriodicPrefixSnapshotFixture(t, func(f *snapshotFixture, owner *pgxpool.Pool) {
		app := periodicPrefixBusinessAPP(t, ctx, f)
		defer app.Close()
		defer periodicPrefixBusinessRestoreClock(t, ctx, owner, f.schema)
		h := periodicPrefixBusinessBase(t, ctx, f, owner, app, true)
		store := billing.New(app)
		settled := periodicPrefixBusinessReserve(t, ctx, app, store, h, true)
		released := periodicPrefixBusinessReserve(t, ctx, app, store, h, false)
		windowFor := func(scope string, source periodicPrefixBusinessSource) string {
			var window string
			if app.QueryRow(ctx, `SELECT window_id FROM periodic_budget_allocations WHERE control_id=$1 AND subject_id IN ($2,$3)`, h.controls[scope], source.request, source.attempt).Scan(&window) != nil || window == "" {
				t.Fatal("prefix business original source attribution window")
			}
			return window
		}
		oldWindows := map[string]string{}
		for _, scope := range []string{"tenant", "key", "procurement"} {
			oldWindows[scope] = windowFor(scope, settled)
			if oldWindows[scope] != windowFor(scope, released) {
				t.Fatal("prefix business old sources did not share their original scope window")
			}
		}
		var today time.Time
		if owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&today) != nil {
			t.Fatal("prefix business current database day")
		}
		periodicPrefixBusinessClock(t, ctx, owner, f.schema, today)
		periodicPrefixBusinessStage(t, ctx, app, &h, "240", "480", false)
		periodicPrefixBusinessReturningWindow(t, ctx, app, &h)
		periodicPrefixBusinessConcurrentWindow(t, ctx, f, app, &h)
		current := periodicPrefixBusinessReserve(t, ctx, app, store, h, false)
		currentWindows := map[string]string{}
		for _, scope := range []string{"tenant", "key", "procurement"} {
			currentWindows[scope] = windowFor(scope, current)
			if currentWindows[scope] == oldWindows[scope] {
				t.Fatal("prefix business new source reused an old day")
			}
		}
		periodicPrefixBusinessStage(t, ctx, app, &h, "240", "480", true)
		periodicPrefixBusinessFinish(t, ctx, store, h, settled, false)
		periodicPrefixBusinessStage(t, ctx, app, &h, "120", "240", true)
		periodicPrefixBusinessFinish(t, ctx, store, h, released, true)
		periodicPrefixBusinessStage(t, ctx, app, &h, "0", "0", true)
		for _, scope := range []string{"tenant", "key", "procurement"} {
			held, spent := "120", "27" // original12 + prior-day15; current120 remains held
			if scope == "procurement" {
				held, spent = "240", "54" // original24 + prior-day30; current240 remains held
			}
			var exact bool
			if app.QueryRow(ctx, `SELECT
 (SELECT held_amount=0 AND spent_amount=$3::numeric AND revision=6 FROM periodic_budget_windows WHERE id=$1)
 AND (SELECT held_amount=$4::numeric AND spent_amount=0 AND revision=1 FROM periodic_budget_windows WHERE id=$2)
 AND (SELECT held_amount=$4::numeric AND spent_amount=$3::numeric FROM periodic_budget_controls WHERE id=$5)`, oldWindows[scope], currentWindows[scope], spent, held, h.controls[scope]).Scan(&exact) != nil || !exact ||
				windowFor(scope, settled) != oldWindows[scope] || windowFor(scope, released) != oldWindows[scope] {
				t.Fatal("prefix business settlement/release moved original attribution or changed the new-day amounts")
			}
		}
		var independent bool
		wallet, err := store.Wallet(ctx, billing.Query{TenantID: h.tenant})
		if err != nil || wallet.Available != 853 || wallet.Held != 120 {
			t.Fatal("prefix business independent 1000-12-15-120 customer arithmetic", err)
		}
		if app.QueryRow(ctx, `SELECT (SELECT held_amount=240 AND spent_amount=54 FROM procurement_accounts WHERE id=$1)
 AND (SELECT count(*) FROM procurement_budget_events WHERE attempt_id=$2 AND type='released_no_consumption' AND held_delta=-240 AND spent_delta=0)=1
 AND (SELECT count(*) FROM procurement_reservations WHERE attempt_id=$2 AND state='released' AND held_amount=0 AND spent_amount=0)=1`, h.account, released.attempt).Scan(&independent) != nil || !independent {
			t.Fatal("prefix business independent 1000-12-15-120 arithmetic or supplier release differs")
		}
		if h.commands != 17 {
			t.Fatal("prefix business did not execute the 17 real three-scope/same-cut/concurrent commands")
		}
		store.EndRequest(current.request)
		history = h
	})
	owner := f.connect(t)
	runtimeFixtureGrant(t, owner, f.pins)
	var witness int64
	if owner.QueryRow(ctx, `SELECT count(*) FROM periodic_budget_observation_journal`).Scan(&witness) != nil || witness <= int64(history.commands) {
		t.Fatal("prefix business persisted nonempty financial and observation history")
	}
	conn, app := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	facts, err := fundinglotscan.ReadPrefix(ctx, fundingLotsObserver{ctx, app}, f.schema, fundinglotscan.ModeOrdinary)
	r := facts.Summary()
	if err != nil || r.Scope != "complete_business_source_prefix_graphs_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 71 || r.ScannedColumns != 635 {
		t.Fatalf("prefix business actual APP complete source71/635: %v / %+v", err, r)
	}
	runtimeAssertReadOnly(t, app, false)
	runtimeFixtureCleanup(t, conn, app.Tx)
	t.Run("periodic_prefix_proves_cross_window_and_lock_barrier", func(t *testing.T) {
		// The real window/RETURNING/lock-barrier operations above are retained
		// unchanged. Their independently qualified PB result cannot grant full
		// procurement or maintenance qualification to the enclosing history.
		if !r.PeriodicPrefix.Balanced || !r.PeriodicPrefix.CoverageComplete || r.PeriodicPrefix.Base != r.Periodic || r.PeriodicPrefix.CertifiedCommands != history.commands ||
			r.Periodic.Unproven != (fundinglotverify.PeriodicBudgetGraphGaps{OperationSnapshotPrefixUnproven: 17}) || r.Periodic.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) ||
			r.Periodic.Scanned.Controls != 3 || r.Periodic.Scanned.Windows != 6 || r.Periodic.Scanned.Allocations != 12 || r.Periodic.Scanned.Events != 21 ||
			r.PeriodicPrefix.Scanned.Observations != 23 || r.PeriodicPrefix.Scanned.OperationBindings != 17 || r.PeriodicPrefix.Scanned.WindowBindings != 6 {
			t.Fatalf("prefix business independent cross-window/17-command PB proof incomplete: %+v", r.PeriodicPrefix)
		}
	})
	t.Run("shared_account_admission_order_abandons_original_plan", func(t *testing.T) {
		// Four sources share one unchanged account and three have terminal
		// effects. The old core intentionally cannot prove their admission
		// order from current rows; do not erase that gap to obtain an epoch.
		if r.Status != "indeterminate" || r.CoverageComplete ||
			r.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{AccountAdmissionOrderUnproven: 1}) ||
			r.Procurement.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{}) || r.Procurement.Structural != (fundinglotverify.ProcurementEvidenceGraphStructural{}) ||
			r.Procurement.Scanned.Attempts != 4 || r.Procurement.Scanned.Reservations != 4 || r.Procurement.Scanned.BudgetEvents != 7 || !r.Customer.Balanced {
			t.Fatalf("prefix business original shared-account admission gap differs: %+v", r.Procurement)
		}
		if projected, err := fundinglotscan.ProjectLegacySeedPrefix(ctx, facts); err != fundinglotscan.ErrInput || !reflect.DeepEqual(projected, fundinglotscan.LegacySeedRows{}) {
			t.Fatal("prefix business unproven shared account became a qualified seed")
		}
		g, snapshot, wire, backend := periodicPrefixInstalled(t, f, false)
		permit, err := Attest(ctx, snapshot)
		if err != nil {
			t.Fatal("prefix business original Plan authority", err)
		}
		plan, err := permit.PlanFundingLots(ctx, uuid.NewString())
		if err == nil || plan != (FundingLotsPlanResult{}) || snapshot.Outcome() != OutcomeAbandoned || permit.state.scanAttempted&fundingLotsOnce == 0 || permit.Verify(ctx) == nil {
			t.Fatal("prefix business shared-account gap did not consume and revoke original Plan authority")
		}
		pre := snapshot.state.fundingLots.prefixFacts.Summary()
		if !pre.PhysicalEOFComplete || pre.ScannedTables != 71 || pre.ScannedColumns != 635 || pre.Status != "indeterminate" || pre.CoverageComplete ||
			pre.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{AccountAdmissionOrderUnproven: 1}) ||
			!pre.PeriodicPrefix.Balanced || !pre.PeriodicPrefix.CoverageComplete || pre.PeriodicPrefix != r.PeriodicPrefix || snapshot.state.fundingLots.basis.ScannedTables != 0 {
			t.Fatal("prefix business protected Plan did not independently stop after the complete shared-account source gap")
		}
		queries, _ := wire.recorded()
		for _, sql := range queries {
			if strings.EqualFold(strings.TrimSpace(sql), "COMMIT") {
				t.Fatal("prefix business shared-account gap published a commit")
			}
		}
		f.gone(t, backend)
		for _, table := range authorityEmptyTables {
			var exists bool
			if owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM ONLY "+pgx.Identifier{f.schema, table}.Sanitize()+")").Scan(&exists) != nil || exists {
				t.Fatal("prefix business shared-account gap left epoch or initialization output")
			}
		}
		var after int64
		if owner.QueryRow(ctx, `SELECT count(*) FROM periodic_budget_observation_journal`).Scan(&after) != nil || after != witness {
			t.Fatal("prefix business rejected Plan changed original PB observation evidence")
		}
		if g.Close(nil) == nil {
			t.Fatal("prefix business shared-account abandoned owner lost its terminal failure")
		}
	})
}

func TestPeriodicPrefixMaintenancePostgresAbsoluteCostCorrectionRemainsUnproven(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := newPeriodicPrefixSnapshotFixture(t, func(f *snapshotFixture, owner *pgxpool.Pool) {
		app := periodicPrefixBusinessAPP(t, ctx, f)
		defer app.Close()
		h := periodicPrefixBusinessBase(t, ctx, f, owner, app, false)
		var operator, approver string
		if app.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='finance_operator' ORDER BY id LIMIT 1`).Scan(&operator) != nil ||
			app.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='finance_approver' ORDER BY id LIMIT 1`).Scan(&approver) != nil || operator == approver {
			t.Fatal("prefix correction requires distinct genuine maker/checker identities")
		}
		store := billing.New(app)
		proposal, err := store.ProposeProcurementCost(ctx, billing.ProcurementCostProposalInput{OperationID: uuid.NewString(), TenantID: h.tenant, AttemptID: h.attempt, ActorID: operator,
			Amount: 25, Basis: "invoice", EvidenceRef: "invoice:prefix-business", Reason: "actual supplier statement absolute correction"})
		if err != nil || proposal.State != "proposed" || proposal.ExpectedRevision == "" {
			t.Fatal("prefix correction real APP historical proposal", err)
		}
		approval := uuid.NewString()
		approved, err := store.ApproveProcurementCost(ctx, billing.ProcurementCostApprovalInput{ProposalID: proposal.ID, OperationID: approval, ActorID: approver})
		if err != nil || approved.State != "approved" || approved.ExpectedRevision != proposal.ExpectedRevision || approved.ExpectedEvidenceCount != proposal.ExpectedEvidenceCount {
			t.Fatal("prefix correction real approval changed the proposal's historical barrier", err)
		}
		var exact bool
		if app.QueryRow(ctx, `SELECT p.expected_reservation_revision=$3::bigint AND p.expected_evidence_count=$4::bigint
 AND r.revision=p.expected_reservation_revision+1 AND r.held_amount=0 AND r.spent_amount=25 AND r.state='settled'
 AND a.cost_amount=25 AND a.cost_status='invoice' AND ac.held_amount=0 AND ac.spent_amount=25
 AND p.requester=$5 AND p.approver=$6 AND p.requester<>p.approver
 AND (SELECT count(*) FROM provider_cost_corrections c WHERE c.attempt_id=a.id AND c.operation_id='procurement:'||$7 AND c.amount=25 AND c.basis='invoice')=1
 AND (SELECT count(*) FROM procurement_budget_events e WHERE e.attempt_id=a.id AND e.event_key='correction:'||$7 AND e.type='cost_corrected' AND e.held_delta=0 AND e.spent_delta=1)=1
 FROM procurement_cost_proposals p JOIN procurement_reservations r ON r.attempt_id=p.attempt_id
 JOIN upstream_attempts a ON a.id=p.attempt_id JOIN procurement_accounts ac ON ac.id=r.account_id
 WHERE p.id=$1 AND a.id=$2`, proposal.ID, h.attempt, proposal.ExpectedRevision, proposal.ExpectedEvidenceCount, operator, approver, approval).Scan(&exact) != nil || !exact {
			t.Fatal("prefix correction must preserve old barrier and append a real nonzero absolute cost change")
		}
		periodicPrefixBusinessConfigure(t, ctx, app, &h, "1")
	})
	owner := f.connect(t)
	runtimeFixtureGrant(t, owner, f.pins)
	conn, app := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	facts, err := fundinglotscan.ReadPrefix(ctx, fundingLotsObserver{ctx, app}, f.schema, fundinglotscan.ModeOrdinary)
	r := facts.Summary()
	if err != nil || r.Scope != "complete_business_source_prefix_graphs_v1" || !r.PhysicalEOFComplete || r.ScannedTables != 71 || r.ScannedColumns != 635 || r.Status != "indeterminate" || r.CoverageComplete ||
		r.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{ProposalBarrierUnproven: 1, CostRecordedEligibilityUnproven: 1}) || r.Procurement.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{}) ||
		!r.PeriodicPrefix.Balanced || !r.PeriodicPrefix.CoverageComplete || r.PeriodicPrefix.Base != r.Periodic || r.PeriodicPrefix.CertifiedCommands != 6 || r.Periodic.Unproven.OperationSnapshotPrefixUnproven != 6 {
		t.Fatalf("prefix correction full physical scan must preserve the procurement barrier gap: %v / %+v", err, r)
	}
	for _, algorithm := range r.Algorithms {
		if algorithm == "" {
			t.Fatal("prefix correction skipped an original source core")
		}
	}
	if projected, err := fundinglotscan.ProjectLegacySeedPrefix(ctx, facts); err != fundinglotscan.ErrInput || !reflect.DeepEqual(projected, fundinglotscan.LegacySeedRows{}) {
		t.Fatal("prefix correction unproven source must not become a qualified seed")
	}
	runtimeAssertReadOnly(t, app, false)
	runtimeFixtureCleanup(t, conn, app.Tx)
	g, snapshot, wire, backend := periodicPrefixInstalled(t, f, false)
	permit, err := Attest(ctx, snapshot)
	if err != nil {
		t.Fatal("prefix correction original owned authority", err)
	}
	plan, err := permit.PlanFundingLots(ctx, uuid.NewString())
	if err == nil || plan != (FundingLotsPlanResult{}) || snapshot.Outcome() != OutcomeAbandoned || permit.state.scanAttempted&fundingLotsOnce == 0 || permit.Verify(ctx) == nil {
		t.Fatal("prefix correction unproven source did not consume and revoke original Plan authority")
	}
	pre := snapshot.state.fundingLots.prefixFacts.Summary()
	if !pre.PhysicalEOFComplete || pre.ScannedTables != 71 || pre.ScannedColumns != 635 || pre.Status != "indeterminate" || pre.CoverageComplete ||
		pre.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{ProposalBarrierUnproven: 1, CostRecordedEligibilityUnproven: 1}) || !pre.PeriodicPrefix.Balanced {
		t.Fatal("prefix correction protected Plan did not independently reach the complete source gap")
	}
	queries, _ := wire.recorded()
	for _, sql := range queries {
		if strings.EqualFold(strings.TrimSpace(sql), "COMMIT") {
			t.Fatal("prefix correction published a commit after unproven procurement history")
		}
	}
	f.gone(t, backend)
	for _, table := range authorityEmptyTables {
		var exists bool
		if owner.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM ONLY "+pgx.Identifier{f.schema, table}.Sanitize()+")").Scan(&exists) != nil || exists {
			t.Fatal("prefix correction fail-closed Plan left epoch or initialization output")
		}
	}
	if g.Close(nil) == nil {
		t.Fatal("prefix correction abandoned owner lost its terminal failure")
	}
	// This passing refusal is deliberately not the B-P3 section6 successful
	// corrected-procurement gate. The separate SDD records the missing immutable
	// approval barrier evidence; the original checker and historical rows stay.
}
