package alerts

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

type fixture struct {
	t                                                                                            *testing.T
	ctx                                                                                          context.Context
	owner, app, projector                                                                        *pgxpool.Pool
	store                                                                                        *Store
	schema, appRole, projectorRole, admin, finance, approver, customer, tenant, account, channel string
}

func integration(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for real PostgreSQL alert integration")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("requires dedicated moni_test database on port 55439")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &fixture{t: t, ctx: ctx, schema: "alerts_it_" + suffix, appRole: "alerts_app_" + suffix, projectorRole: "alerts_proj_" + suffix, admin: uuid.NewString(), finance: uuid.NewString(), approver: uuid.NewString(), customer: uuid.NewString(), tenant: uuid.NewString(), account: uuid.NewString(), channel: uuid.NewString()}
	t.Cleanup(func() {
		for _, p := range []*pgxpool.Pool{f.app, f.projector, f.owner} {
			if p != nil {
				p.Close()
			}
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := admin.Exec(cleanup, `DROP SCHEMA IF EXISTS `+pgx.Identifier{f.schema}.Sanitize()+` CASCADE`); e != nil {
			t.Error(e)
		}
		for _, r := range []string{f.appRole, f.projectorRole} {
			_, _ = admin.Exec(cleanup, `DROP OWNED BY `+pgx.Identifier{r}.Sanitize())
			if _, e := admin.Exec(cleanup, `DROP ROLE IF EXISTS `+pgx.Identifier{r}.Sanitize()); e != nil {
				t.Error(e)
			}
		}
		admin.Close()
	})
	for _, sql := range []string{`CREATE SCHEMA ` + pgx.Identifier{f.schema}.Sanitize(), `CREATE ROLE ` + pgx.Identifier{f.appRole}.Sanitize() + ` NOLOGIN NOINHERIT NOSUPERUSER`, `CREATE ROLE ` + pgx.Identifier{f.projectorRole}.Sanitize() + ` NOLOGIN NOINHERIT NOSUPERUSER`} {
		if _, e = admin.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10s"
	cfg.MaxConns = 12
	f.owner, e = pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrations.Apply(ctx, f.owner); e != nil {
		t.Fatal(e)
	}
	if e = operations.ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectorRole); e != nil {
		t.Fatal(e)
	}
	as := func(role string) *pgxpool.Pool {
		cfg := f.owner.Config()
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, e := c.Exec(ctx, `SET ROLE `+pgx.Identifier{role}.Sanitize())
			return e
		}
		p, e := pgxpool.NewWithConfig(ctx, cfg)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	f.app, f.projector = as(f.appRole), as(f.projectorRole)
	var current string
	var super bool
	if e = f.app.QueryRow(ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &super); e != nil || current != f.appRole || super {
		t.Fatal(current, super, e)
	}
	f.store, e = NewStore(f.app, RuntimeOptions{DeploymentEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, u := range []struct{ id, role string }{{f.admin, "admin"}, {f.finance, "finance_operator"}, {f.approver, "finance_approver"}, {f.customer, "none"}} {
		f.exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, u.id, u.id+"@alerts.invalid", u.role)
	}
	f.exec(`INSERT INTO tenants(id,name) VALUES($1,'alerts fixture')`, f.tenant)
	f.exec(`INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'alerts fixture','fixture','active',100)`, f.account)
	tx, e := f.owner.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	version, credential := uuid.NewString(), uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{{`INSERT INTO channels(id,name,current_version) VALUES($1,'alerts fixture',$2)`, []any{f.channel, version}}, {`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused')`, []any{credential, f.channel}}, {`INSERT INTO channel_versions(id,channel_id,credential_id,base_url) VALUES($1,$2,$3,'https://example.invalid')`, []any{version, f.channel, credential}}, {`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1)`, []any{version, f.channel, credential}}} {
		if _, e = tx.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, e := f.owner.Exec(f.ctx, sql, args...); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) reader() Reader { return Reader{f.admin} }
func (f *fixture) create(kind Kind, scope ScopeType, id ID, p Parameters, state RuleState) CommandReceipt {
	f.t.Helper()
	v, e := f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), kind, scope, id, "fixture", state, p})
	if e != nil || v.Status != "applied" {
		f.t.Fatal(v, e)
	}
	return v
}
func requireCode(t *testing.T, e error, want string) {
	t.Helper()
	var a *Error
	if !errors.As(e, &a) || a.Code != want {
		t.Fatalf("want %s got %v", want, e)
	}
}
func requireApplied(t *testing.T, r CommandReceipt, e error) {
	t.Helper()
	if e != nil || r.Status != "applied" {
		t.Fatal(r, e)
	}
}
func requireRejected(t *testing.T, r CommandReceipt, e error, want string) {
	t.Helper()
	if e != nil || r.Status != "rejected" || r.Reason != want || r.ResultRevision != nil {
		t.Fatal(r, e, want)
	}
}

// Only the scheduler eligibility is advanced by fixture owner. All subsequent
// application source/decision/episode/event/runtime writes run with live guards.
func (f *fixture) due(id ID) {
	f.t.Helper()
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		f.t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if _, e = tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); e != nil {
		f.t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `UPDATE alert_rule_runtime SET next_evaluation_at=clock_timestamp()-interval '1 second' WHERE rule_id=$1`, id); e != nil {
		f.t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		f.t.Fatal(e)
	}
}
func (f *fixture) evaluate() EvaluationResult {
	f.t.Helper()
	v, e := f.store.EvaluateDue(f.ctx)
	if e != nil {
		f.t.Fatal(v, e)
	}
	return v
}

func TestPostgresAlertLifecycleCommandsQueriesAndOriginalReceipts(t *testing.T) {
	f := integration(t)
	created := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "enabled")
	id := *created.ResourceID
	if v := f.evaluate(); v.Outcome != "evaluated" {
		t.Fatal(v)
	}
	rule, e := f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || rule.Runtime.Freshness != "fresh" || rule.Runtime.CurrentEpisodeID != nil {
		t.Fatal(rule, e)
	}
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	changed, e := f.store.Configure(f.ctx, f.reader(), id, ConfigureRule{uuid.NewString(), "1", "open label", "enabled", EmptyParameters{}})
	requireApplied(t, changed, e)
	if v := f.evaluate(); v.Outcome != "evaluated" {
		t.Fatal(v)
	}
	rule, e = f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || rule.ConfigurationRevision != "2" || rule.Runtime.CurrentEpisodeID == nil {
		t.Fatal(rule, e)
	}
	episode := *rule.Runtime.CurrentEpisodeID
	before := rule.Runtime
	noop, e := f.store.Configure(f.ctx, f.reader(), id, ConfigureRule{uuid.NewString(), "2", "open label", "enabled", EmptyParameters{}})
	requireApplied(t, noop, e)
	rule, e = f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || rule.Runtime.LastSuccessAt == nil || !rule.Runtime.LastSuccessAt.Equal(before.LastSuccessAt.Time) || *rule.Runtime.CurrentEpisodeID != episode || *noop.ResultRevision != "2" {
		t.Fatal("no-op reset/closed runtime", rule, noop, e)
	}
	stale, e := f.store.Configure(f.ctx, f.reader(), id, ConfigureRule{uuid.NewString(), "1", "open label", "enabled", EmptyParameters{}})
	requireRejected(t, stale, e, "alert_revision_stale")
	ackInput := Acknowledge{uuid.NewString(), "1"}
	ack, e := f.store.Acknowledge(f.ctx, Reader{f.finance}, episode, ackInput)
	requireApplied(t, ack, e)
	replay, e := f.store.Acknowledge(f.ctx, Reader{f.finance}, episode, ackInput)
	if e != nil || !reflect.DeepEqual(ack, replay) {
		t.Fatal(ack, replay, e)
	}
	_, e = f.store.Operation(f.ctx, f.reader(), ack.OperationID)
	requireCode(t, e, "alert_not_found")
	closed, e := f.store.Configure(f.ctx, f.reader(), id, ConfigureRule{uuid.NewString(), "2", "renamed", "enabled", EmptyParameters{}})
	requireApplied(t, closed, e)
	old, e := f.store.Episode(f.ctx, f.reader(), episode)
	if e != nil || old.Status != "resolved" || old.ActionRevision != "3" || old.ResolveReason == nil || *old.ResolveReason != "rule_changed" || old.RuleSnapshot.Label != "open label" || old.AcknowledgedBy == nil || *old.AcknowledgedBy != f.finance {
		t.Fatal(old, e)
	}
	events, e := f.store.Events(f.ctx, f.reader(), episode, EventFilter{})
	if e != nil || len(events.Data) != 3 || events.NextCursor != nil || events.Data[0].Type != "rule_changed" || events.Data[0].SafeSample != nil || events.Data[2].Type != "opened" {
		t.Fatal(events, e)
	}
	f.evaluate()
	rule, e = f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || rule.Runtime.CurrentEpisodeID == nil || *rule.Runtime.CurrentEpisodeID == episode {
		t.Fatal(rule, e)
	}
	newEpisode, e := f.store.Episode(f.ctx, f.reader(), *rule.Runtime.CurrentEpisodeID)
	if e != nil || newEpisode.Generation != "2" || newEpisode.AcknowledgedBy != nil {
		t.Fatal(newEpisode, e)
	}
	retired, e := f.store.Retire(f.ctx, f.reader(), id, RetireRule{uuid.NewString(), "3"})
	requireApplied(t, retired, e)
	rule, e = f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || rule.State != "retired" || rule.ConfigurationRevision != "4" || rule.Runtime.LastSample != nil || rule.Runtime.NextEvaluationAt != nil || rule.Runtime.Freshness != "retired" {
		t.Fatal(rule, e)
	}
	receipt, e := f.store.Operation(f.ctx, f.reader(), created.OperationID)
	if e != nil || !reflect.DeepEqual(created, receipt) {
		t.Fatal("resource changes rewrote original command receipt", created, receipt, e)
	}
	f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "disabled")
}

func TestPostgresAlertSourcesFailurePreservationRecoveryAndCAS(t *testing.T) {
	f := integration(t)
	f.exec(`UPDATE channels SET quarantined=true,enabled=false WHERE id=$1`, f.channel)
	created := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "enabled")
	id := *created.ResourceID
	f.evaluate()
	before, e := f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || before.Runtime.CurrentEpisodeID == nil {
		t.Fatal(before, e)
	}
	episode := *before.Runtime.CurrentEpisodeID
	// A missing logical parent does not clear an existing episode. Only this
	// disposable source fixture bypasses the business FK cycle for deletion.
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `DELETE FROM channels WHERE id=$1`, f.channel); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	f.due(id)
	result := f.evaluate()
	if result.Outcome != "failed_recorded" || result.ErrorCode != "source_missing" {
		t.Fatal(result)
	}
	after, e := f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || after.Runtime.LastSuccessAt == nil || !after.Runtime.LastSuccessAt.Equal(before.Runtime.LastSuccessAt.Time) || *after.Runtime.CurrentEpisodeID != episode || after.Runtime.LastErrorCode == nil || *after.Runtime.LastErrorCode != "source_missing" {
		t.Fatal("failure lost success/episode", after, e)
	}
	// Recreate the same logical source without touching alert history.
	var version string
	if e = f.owner.QueryRow(f.ctx, `SELECT id FROM channel_versions WHERE channel_id=$1`, f.channel).Scan(&version); e != nil {
		t.Fatal(e)
	}
	f.exec(`INSERT INTO channels(id,name,current_version,quarantined) VALUES($1,'restored',$2,false)`, f.channel, version)
	var pre evaluationPreimage
	if e = f.owner.QueryRow(f.ctx, `SELECT r.id,r.configuration_revision,t.write_revision FROM alert_rules r JOIN alert_rule_runtime t ON t.rule_id=r.id WHERE r.id=$1`, id).Scan(&pre.id, &pre.revision, &pre.write); e != nil {
		t.Fatal(e)
	}
	f.due(id)
	f.evaluate()
	result, e = f.store.recordFailure(f.ctx, pre, "source_unavailable")
	if e != nil || result.Outcome != "stale_discarded" {
		t.Fatal("older failure replaced newer same-configuration success", result, e)
	}
	ep, e := f.store.Episode(f.ctx, f.reader(), episode)
	if e != nil || ep.Status != "resolved" || ep.ResolveReason == nil || *ep.ResolveReason != "recovered" {
		t.Fatal(ep, e)
	}
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	f.due(id)
	f.evaluate()
	after, e = f.store.Rule(f.ctx, f.reader(), id)
	if e != nil || after.Runtime.CurrentEpisodeID == nil || *after.Runtime.CurrentEpisodeID == episode || after.Runtime.LastErrorCode != nil {
		t.Fatal(after, e)
	}
	budget := f.create(ProcurementBudgetLow, "procurement_account", f.account, BudgetParameters{"100", "110"}, "enabled")
	f.evaluate()
	v, e := f.store.Rule(f.ctx, f.reader(), *budget.ResourceID)
	if e != nil || v.Runtime.LastSample.(BudgetSample).AvailableAmount != "100" || v.Runtime.CurrentEpisodeID == nil {
		t.Fatal(v, e)
	}
	aging := f.create(CustomerReconciliationAged, "tenant", f.tenant, AgingParameters{"600", "1", "0"}, "enabled")
	f.evaluate()
	v, e = f.store.Rule(f.ctx, f.reader(), *aging.ResourceID)
	if e != nil || v.Runtime.CurrentEpisodeID != nil || v.Runtime.LastSample.(AgingSample).ObservedCount != "0" {
		t.Fatal(v, e)
	}
	procurement := f.create(ProcurementUnresolvedAged, "procurement_account", f.account, AgingParameters{"600", "1", "0"}, "enabled")
	f.evaluate()
	v, e = f.store.Rule(f.ctx, f.reader(), *procurement.ResourceID)
	if e != nil || v.Runtime.CurrentEpisodeID != nil || v.Runtime.LastSample.(AgingSample).ObservedCount != "0" {
		t.Fatal(v, e)
	}
}

func TestPostgresAlertOperationAuthorizationPaginationAndRollback(t *testing.T) {
	f := integration(t)
	missing, e := f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), ChannelQuarantined, "channel", uuid.NewString(), "missing", "enabled", EmptyParameters{}})
	requireRejected(t, missing, e, "alert_source_missing")
	for i := 0; i < 52; i++ {
		id := uuid.NewString()
		f.exec(`INSERT INTO tenants(id,name) VALUES($1,'page')`, id)
		f.create(CustomerReconciliationAged, "tenant", id, AgingParameters{"600", "1", "0"}, "disabled")
	}
	first, e := f.store.Rules(f.ctx, Reader{f.finance}, RuleFilter{})
	if e != nil || len(first.Data) != 50 || first.NextCursor == nil {
		t.Fatal(len(first.Data), e)
	}
	cursor, e := DecodeCursor(*first.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.store.Rules(f.ctx, Reader{f.finance}, RuleFilter{Cursor: cursor})
	if e != nil || len(second.Data) != 2 || second.NextCursor != nil {
		t.Fatal(second, e)
	}
	state := RuleState("disabled")
	_, e = f.store.Rules(f.ctx, f.reader(), RuleFilter{State: &state, Cursor: cursor})
	requireCode(t, e, "invalid_request")
	_, e = f.store.Rule(f.ctx, Reader{f.customer}, first.Data[0].ID)
	requireCode(t, e, "forbidden")
	_, e = f.store.Operation(f.ctx, Reader{f.finance}, missing.OperationID)
	requireCode(t, e, "alert_not_found")
	f.exec(`UPDATE users SET platform_role='finance_operator' WHERE id=$1`, f.admin)
	_, e = f.store.Operation(f.ctx, f.reader(), missing.OperationID)
	requireCode(t, e, "forbidden")
	f.exec(`UPDATE users SET platform_role='admin' WHERE id=$1`, f.admin)
	f.exec(`CREATE FUNCTION alerts_fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='alerts.rule.create' THEN RAISE EXCEPTION 'synthetic audit fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER alerts_fail_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION alerts_fail_audit()`)
	op := uuid.NewString()
	_, e = f.store.Create(f.ctx, f.reader(), CreateRule{op, ChannelQuarantined, "channel", f.channel, "rollback", "enabled", EmptyParameters{}})
	requireCode(t, e, "alert_unavailable")
	var facts int
	if e = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM alert_commands WHERE operation_id=$1)+(SELECT count(*) FROM alert_rules WHERE label='rollback')`, op).Scan(&facts); e != nil || facts != 0 {
		t.Fatal(facts, e)
	}
}

func TestPostgresAlertConcurrentCommandsAndWorkerSlots(t *testing.T) {
	f := integration(t)
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	in := CreateRule{uuid.NewString(), ChannelQuarantined, "channel", f.channel, "concurrent", "enabled", EmptyParameters{}}
	var wg sync.WaitGroup
	receipts := make(chan CommandReceipt, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := f.store.Create(f.ctx, f.reader(), in); receipts <- r; errs <- e }()
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first CommandReceipt
	for r := range receipts {
		if first.OperationID == "" {
			first = r
		} else if !reflect.DeepEqual(first, r) {
			t.Fatal(first, r)
		}
	}
	results := make(chan EvaluationResult, 2)
	errs = make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); r, e := f.store.EvaluateDue(f.ctx); results <- r; errs <- e }()
	}
	wg.Wait()
	close(errs)
	close(results)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var evaluated, idle int
	for r := range results {
		switch r.Outcome {
		case "evaluated":
			evaluated++
		case "idle":
			idle++
		default:
			t.Fatal(r)
		}
	}
	var episodes, events int
	if e := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM alert_episodes),(SELECT count(*) FROM alert_episode_events)`).Scan(&episodes, &events); e != nil || evaluated != 1 || idle != 1 || episodes != 1 || events != 1 {
		t.Fatal(evaluated, idle, episodes, events, e)
	}
	changed := in
	changed.Label = "different"
	_, e := f.store.Create(f.ctx, f.reader(), changed)
	requireCode(t, e, "alert_operation_conflict")
}

func TestPostgresAlertRuleLimits(t *testing.T) {
	f := integration(t)
	ids := make([]string, 257)
	for i := range ids {
		ids[i] = uuid.NewString()
		f.exec(`INSERT INTO tenants(id,name) VALUES($1,'limit')`, ids[i])
	}
	rules := []CommandReceipt{}
	for i := 0; i < 64; i++ {
		rules = append(rules, f.create(CustomerReconciliationAged, "tenant", ids[i], AgingParameters{"600", "1", "0"}, "enabled"))
	}
	rejected, e := f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), CustomerReconciliationAged, "tenant", ids[64], "limit", "enabled", AgingParameters{"600", "1", "0"}})
	requireRejected(t, rejected, e, "alert_rule_limit")
	for i := 64; i < 256; i++ {
		rules = append(rules, f.create(CustomerReconciliationAged, "tenant", ids[i], AgingParameters{"600", "1", "0"}, "disabled"))
	}
	rejected, e = f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), CustomerReconciliationAged, "tenant", ids[256], "limit", "disabled", AgingParameters{"600", "1", "0"}})
	requireRejected(t, rejected, e, "alert_rule_limit")
	retired, e := f.store.Retire(f.ctx, f.reader(), *rules[0].ResourceID, RetireRule{uuid.NewString(), "1"})
	requireApplied(t, retired, e)
	f.create(CustomerReconciliationAged, "tenant", ids[256], AgingParameters{"600", "1", "0"}, "enabled")
}

func TestPostgresAlertSQLGuardsAndRoleMatrix(t *testing.T) {
	f := integration(t)
	for pass := 0; pass < 2; pass++ {
		if e := operations.ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectorRole); e != nil {
			t.Fatal(e)
		}
		for _, table := range []string{"alert_control", "alert_commands", "alert_rules", "alert_rule_runtime", "alert_episodes", "alert_episode_events"} {
			for _, priv := range []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
				var allowed bool
				if e := f.app.QueryRow(f.ctx, `SELECT has_table_privilege(current_user,$1,$2)`, table, priv).Scan(&allowed); e != nil || allowed {
					t.Fatal(table, priv, allowed, e)
				}
			}
			_, e := f.projector.Exec(f.ctx, `SELECT * FROM `+table+` LIMIT 1`)
			var pg *pgconn.PgError
			if !errors.As(e, &pg) || pg.Code != "42501" {
				t.Fatal(table, e)
			}
		}
	}
	_, e := f.app.Exec(f.ctx, `UPDATE alert_episode_events SET actor=actor WHERE false`)
	var pg *pgconn.PgError
	if !errors.As(e, &pg) || pg.Code != "42501" {
		t.Fatal(e)
	}
	tx, e := f.app.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `INSERT INTO alert_commands(operation_id,actor,kind,canonical_digest,status) VALUES($1,$2,'rule_create',repeat('a',64),'claimed')`, uuid.NewString(), f.admin); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e == nil {
		t.Fatal("claimed command committed")
	}
	_ = tx.Rollback(f.ctx)
	created := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "enabled")
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	f.evaluate()
	rule, e := f.store.Rule(f.ctx, f.reader(), *created.ResourceID)
	if e != nil {
		t.Fatal(e)
	}
	episode := *rule.Runtime.CurrentEpisodeID
	for _, statement := range []string{`UPDATE alert_episodes SET initial_sample=jsonb_set(initial_sample,'{enabled}','false') WHERE id=$1`, `UPDATE alert_episodes SET acknowledged_by='` + f.admin + `',acknowledged_at='infinity',acknowledged_command_id='` + created.OperationID + `' WHERE id=$1`, `DELETE FROM alert_episodes WHERE id=$1`} {
		_, e = f.app.Exec(f.ctx, statement, episode)
		if e == nil {
			t.Fatal("invalid history SQL accepted", statement)
		}
	}
	// Missing event and orphan runtime pointer must fail at COMMIT, not merely
	// through repository validation. Explicit SQL uses the actual APP role.
	tx, e = f.app.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(f.ctx, `UPDATE alert_rule_runtime SET current_episode_id=NULL WHERE rule_id=$1`, *created.ResourceID)
	if e == nil {
		e = tx.Commit(f.ctx)
	}
	if e == nil {
		t.Fatal("orphan open episode committed")
	}
	_ = tx.Rollback(f.ctx)
}

func TestPostgresAlertTempShadowCannotEvaluateDisabledRule(t *testing.T) {
	f := integration(t)
	var count int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'alert_%' AND NOT p.prosecdef AND p.proconfig=ARRAY['search_path='||$1||', pg_catalog, pg_temp']`, f.schema).Scan(&count); err != nil || count != 20 {
		t.Fatal("all alert functions must fix their invoker resolution path", count, err)
	}
	created := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "disabled")
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	realRules := pgx.Identifier{f.schema, "alert_rules"}.Sanitize()
	realRuntime := pgx.Identifier{f.schema, "alert_rule_runtime"}.Sanitize()
	if _, err = tx.Exec(f.ctx, `CREATE TEMP TABLE alert_rules AS SELECT * FROM `+realRules+` WHERE id=$1`, *created.ResourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE pg_temp.alert_rules SET state='enabled'`); err != nil {
		t.Fatal(err)
	}
	at, err := databaseTime(f.ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	sample, err := encode(ChannelSample{ChannelQuarantined, Time{at}, true, false})
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, `UPDATE `+realRuntime+` SET last_attempt_at=$2,last_success_at=$2,evaluated_revision=1,last_sample=$3,next_evaluation_at=$2::timestamptz+interval '60 seconds' WHERE rule_id=$1`, *created.ResourceID, at, sample)
	if err == nil {
		err = tx.Commit(f.ctx)
	}
	if err == nil {
		t.Fatal("TEMP shadow made a disabled real rule commit a successful runtime evaluation")
	}
}
