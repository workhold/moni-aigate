package procurementattemptbirth_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptbirth"
	"github.com/moni-ai/moni-aigate/internal/procurementattemptrules"
	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
	"github.com/moni-ai/moni-aigate/migrations"
)

type fixture struct {
	ctx                                                 context.Context
	owner, app, peer                                    *pgxpool.Pool
	b                                                   procurementattemptbirth.Bindings
	tenant, user, key, model, admin, operator, approver string
	channel, version, credential, cost                  string
}

func must(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) {
			t.Fatalf("%s: %v; SQLSTATE=%s message=%s; detail=%s; where=%s; position=%d; internal=%s", step, err, p.Code, p.Message, p.Detail, p.Where, p.Position, p.InternalQuery)
		}
		t.Fatalf("%s: %v", step, err)
	}
}
func sqlstate(t *testing.T, err error, want string) {
	t.Helper()
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != want {
		t.Fatalf("want SQLSTATE %s, got %v", want, err)
	}
}

func newFixture(t *testing.T, beforeInstall ...func(*fixture)) *fixture {
	t.Helper()
	// ADR-005 freezes this procurement-prefix research. Its historical prefix
	// fixture funds through the current credit path, which requires the 027
	// wallet schema the prefix does not contain.
	t.Skip("frozen procurement-prefix research: historical fixture cannot fund through the current 027 wallet path")
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required: dedicated PG16 actual LOGIN A2 candidate acceptance")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	must(t, "parse dedicated DB", err)
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test" || len(c.Fallbacks) != 0 {
		t.Fatal("requires dedicated loopback moni_test LOGIN/database at 55439 without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	owner, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	must(t, "connect installer", err)
	var ownerPublicExecute bool
	must(t, "save installer global function default", owner.QueryRow(ctx, `SELECT coalesce((SELECT EXISTS(SELECT 1 FROM aclexplode(d.defaclacl) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_default_acl d WHERE d.defaclrole=current_user::regrole AND d.defaclnamespace=0 AND d.defaclobjtype='f'),true)`).Scan(&ownerPublicExecute))
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	b := procurementattemptbirth.Bindings{Schema: "ab_" + suffix, App: "ab_app_" + suffix, Writer: "ab_w_" + suffix}
	peer, projector := "ab_peer_"+suffix, "ab_proj_"+suffix
	f := &fixture{ctx: ctx, owner: owner, b: b, tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), admin: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString(), cost: uuid.NewString()}
	q := func(v string) string { return pgx.Identifier{v}.Sanitize() }
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.peer != nil {
			f.peer.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		defer cancel()
		defer f.owner.Close()
		defer func() {
			action := "REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC"
			if ownerPublicExecute {
				action = "GRANT EXECUTE ON FUNCTIONS TO PUBLIC"
			}
			if _, e := f.owner.Exec(cleanup, "ALTER DEFAULT PRIVILEGES "+action); e != nil {
				t.Errorf("restore installer global default ACL: %v", e)
			}
		}()
		_, e := f.owner.Exec(cleanup, "DROP SCHEMA IF EXISTS "+q(b.Schema)+" CASCADE")
		if e != nil {
			t.Errorf("cleanup schema: %v", e)
			return
		}
		for _, name := range []string{peer, projector, b.App, b.Writer} {
			_, e = f.owner.Exec(cleanup, "DROP OWNED BY "+q(name))
			if e != nil {
				t.Errorf("cleanup exact fixture role defaults/grants: %v", e)
			}
			_, e = f.owner.Exec(cleanup, "DROP ROLE IF EXISTS "+q(name))
			if e != nil {
				t.Errorf("cleanup role: %v", e)
			}
		}
	})
	var version int
	must(t, "read PG version", owner.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("requires PG16")
	}
	for _, r := range []struct{ name, login string }{{b.App, "LOGIN"}, {peer, "LOGIN"}, {b.Writer, "NOLOGIN"}, {projector, "NOLOGIN"}} {
		_, err = owner.Exec(ctx, "CREATE ROLE "+q(r.name)+" "+r.login+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
		must(t, "create isolated role", err)
	}
	must(t, "read APP OID", owner.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", b.App).Scan(&f.b.AppOID))
	must(t, "read W OID", owner.QueryRow(ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", b.Writer).Scan(&f.b.WriterOID))
	_, err = owner.Exec(ctx, "CREATE SCHEMA "+q(b.Schema))
	must(t, "create isolated schema", err)
	ownedCfg := cfg.Copy()
	ownedCfg.ConnConfig.RuntimeParams["search_path"] = b.Schema
	ownedCfg.MaxConns = 6
	owned, err := pgxpool.NewWithConfig(ctx, ownedCfg)
	must(t, "connect schema installer", err)
	f.owner = owned
	owner.Close()
	must(t, "apply original 001-022", migrations.ApplyThrough(ctx, owned, "022_payment_runtime_attestation.sql"))
	bp1, err := periodicbudgetprefixschema.SQL()
	must(t, "authenticate B-P1", err)
	_, err = owned.Exec(ctx, bp1)
	must(t, "install B-P1", err)
	tx, err := owned.Begin(ctx)
	must(t, "begin parent setup", err)
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, e := tx.Exec(ctx, sql, args...)
		must(t, "seed original business parent", e)
	}
	exec("INSERT INTO tenants(id,name) VALUES($1,'A2 fixture')", f.tenant)
	for _, u := range []struct{ id, role string }{{f.user, "none"}, {f.admin, "admin"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		exec("INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-disabled',$3)", u.id, u.id+"@example.invalid", u.role)
	}
	exec("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, f.user)
	exec(`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'model',$2,'v1')`, f.model, `{"version":"v1","model":"model","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`)
	exec("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	exec("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'fixture','fixture-key','fixture-disabled',$4,100000)", f.key, f.tenant, f.user, []string{f.model})
	exec("INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1000000,1000000,clock_timestamp()-interval '1 day',$2)", f.model, f.operator)
	exec("INSERT INTO channels(id,name,current_version) VALUES($1,'A2 upstream',$2)", f.channel, f.version)
	exec("INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-fixture')", f.credential, f.channel)
	exec("INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)", f.version, f.channel, f.credential)
	exec("INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'fixture',1,10)", f.version, f.channel, f.credential)
	exec("INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',2000000,2000000,'contract',clock_timestamp()-interval '1 day')", f.cost, f.channel)
	must(t, "commit business parents", tx.Commit(ctx))
	store := billing.New(owned)
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: uuid.NewString(), ProofRef: "fixture:verified", Amount: 10000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 10000, IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture:credit"})
	must(t, "create original credit", err)
	_, err = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver})
	must(t, "fund original wallet", err)
	// Optional acceptance setup executes genuine original business operations
	// before any A2 relation or guard exists. It must not synthesize tracked
	// provenance or disable protection to imitate legacy data.
	for _, prepare := range beforeInstall {
		prepare(f)
	}
	// Broad original provisioning runs before candidate tables exist. The new
	// profile narrows exact ordinary columns without changing original SQL.
	must(t, "provision original APP/projector", operations.ProvisionFor(ctx, owned, b.Schema, b.App, projector))
	for _, s := range []string{procurementidentity.SQLTemplate(), procurementattemptrules.SQLTemplate()} {
		_, err = owned.Exec(ctx, strings.ReplaceAll(s, "{{MONI_SCHEMA}}", q(b.Schema)))
		must(t, "install independent identity/A0 prerequisite", err)
	}
	sql, err := procurementattemptbirth.SQL(f.b)
	must(t, "authenticate A2 complete candidate", err)
	acl, err := procurementattemptbirth.Permissions(f.b)
	must(t, "authenticate A2 ACL", err)
	install, err := owned.Begin(ctx)
	must(t, "begin atomic A2 install", err)
	defer install.Rollback(ctx)
	_, err = install.Exec(ctx, sql)
	must(t, "install complete A2 relations/functions/hooks", err)
	_, err = install.Exec(ctx, acl)
	must(t, "install exact A2 ACL", err)
	abCatalogCheck(t, install, f.b)
	must(t, "commit candidate installation", install.Commit(ctx))
	_, err = owned.Exec(ctx, "GRANT USAGE ON SCHEMA "+q(b.Schema)+" TO "+q(peer))
	must(t, "allow peer namespace visibility", err)
	login := func(name string) *pgxpool.Pool {
		lc := ownedCfg.Copy()
		lc.ConnConfig.User = name
		lc.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
		lc.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
		lc.ConnConfig.RuntimeParams["TimeZone"] = "Asia/Shanghai"
		p, e := pgxpool.NewWithConfig(ctx, lc)
		must(t, "connect actual restricted LOGIN", e)
		var s, u string
		must(t, "prove actual LOGIN", p.QueryRow(ctx, "SELECT session_user,current_user").Scan(&s, &u))
		if s != name || u != name {
			t.Fatal("actual LOGIN mismatch")
		}
		return p
	}
	f.app = login(b.App)
	f.peer = login(peer)
	return f
}

type receipt struct {
	Outcome, OperationID, TargetID, Status, Reason, RootID string
	AccountJournalID                                       *string
}

func (r *receipt) UnmarshalJSON(b []byte) error {
	var x struct {
		Outcome     string  `json:"outcome"`
		OperationID string  `json:"operation_id"`
		TargetID    string  `json:"target_id"`
		Status      string  `json:"status"`
		Reason      string  `json:"reason"`
		RootID      string  `json:"root_id"`
		Journal     *string `json:"account_journal_id"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	*r = receipt{x.Outcome, x.OperationID, x.TargetID, x.Status, x.Reason, x.RootID, x.Journal}
	return nil
}
func (f *fixture) create(t *testing.T, limit int64) receipt {
	t.Helper()
	var raw []byte
	r := receipt{}
	must(t, "actual APP create account and COMMIT", f.app.QueryRow(f.ctx, "SELECT to_jsonb(x) FROM pp_create_account($1,$2,'supplier','fixture','active',$3) x", f.admin, uuid.NewString(), limit).Scan(&raw))
	must(t, "decode create receipt", json.Unmarshal(raw, &r))
	return r
}
func (f *fixture) configure(t *testing.T, account, op string, revision, limit int64) receipt {
	t.Helper()
	var raw []byte
	r := receipt{}
	must(t, "actual APP configure and COMMIT", f.app.QueryRow(f.ctx, "SELECT to_jsonb(x) FROM pp_configure_account($1,$2,$3,$4,'supplier','fixture','active',$5) x", f.admin, op, account, revision, limit).Scan(&raw))
	must(t, "decode configure receipt", json.Unmarshal(raw, &r))
	return r
}
func (f *fixture) bind(t *testing.T, account string) {
	t.Helper()
	_, err := f.owner.Exec(f.ctx, "INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)", uuid.NewString(), f.credential, f.channel, account, uuid.NewString())
	must(t, "prepare real original binding parent", err)
}
func (f *fixture) reserve(t *testing.T) billing.Reservation {
	t.Helper()
	r, err := billing.New(f.app).Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "a2-request", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute)})
	must(t, "actual APP original Reserve", err)
	return r
}

type attemptReceipt struct {
	Outcome  string  `json:"outcome"`
	Root     *string `json:"root_id"`
	Source   *string `json:"source_id"`
	Attempt  string  `json:"attempt_id"`
	Request  string  `json:"request_id"`
	Sequence int     `json:"sequence"`
	Delivery string  `json:"delivery_state"`
	Cost     string  `json:"cost_status"`
}

func (f *fixture) attempt(request string) (attemptReceipt, error) {
	var raw []byte
	r := attemptReceipt{}
	err := f.app.QueryRow(f.ctx, `SELECT to_jsonb(x) FROM pp_record_attempt($1,$2,1,$3,$4,$5,'upstream','[]',true,$6,2000000,2000000,$7) x`, f.tenant, request, f.channel, f.version, f.credential, []byte(f.cost), []byte("contract")).Scan(&raw)
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(raw, &r)
}
func (f *fixture) snapshot(t *testing.T) string {
	t.Helper()
	tables := []string{"request_records", "reservations", "request_idempotency", "api_keys", "ledger_accounts", "journals", "journal_lines", "upstream_attempts", "procurement_accounts", "procurement_reservations", "procurement_budget_events", "procurement_commands", "audit_events", "projection_outbox", "recovery_jobs", "periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "periodic_budget_observation_origins", "periodic_budget_observation_journal", "periodic_budget_observation_heads", "pp_operation_roots", "pp_root_steps", "pp_request_origins", "pp_request_heads", "pp_attempt_origins", "pp_attempt_heads", "pp_attempt_nodes", "pp_account_command_sources", "pp_account_origins", "pp_account_heads", "pp_account_journal", "pp_procurement_transitions"}
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		parts = append(parts, "('"+table+"',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb) FROM "+pgx.Identifier{f.b.Schema, table}.Sanitize()+" x))")
	}
	var value string
	must(t, "full rollback snapshot", f.owner.QueryRow(f.ctx, "SELECT jsonb_object_agg(k,v)::text FROM (VALUES "+strings.Join(parts, ",")+") x(k,v)").Scan(&value))
	return value
}

func TestPostgresA2CommandRequestManagedAndUnmanaged(t *testing.T) {
	f := newFixture(t)
	u := f.reserve(t)
	unmanaged, err := f.attempt(u.RequestID)
	must(t, "actual unmanaged birth", err)
	if unmanaged.Outcome != "applied" || unmanaged.Root == nil || unmanaged.Source == nil {
		t.Fatal("unmanaged receipt")
	}
	created := f.create(t, 1000)
	f.bind(t, created.TargetID)
	for i := 0; i < 2; i++ {
		if i == 1 {
			f.configure(t, created.TargetID, uuid.NewString(), 1, 2000)
		}
		r := f.reserve(t)
		a, err := f.attempt(r.RequestID)
		must(t, "actual managed birth", err)
		if a.Outcome != "applied" || a.Root == nil || a.Source == nil || a.Attempt != *a.Source || a.Request != r.RequestID || a.Delivery != "prepared" || a.Cost != "unknown" {
			t.Fatal("managed receipt")
		}
		before := f.snapshot(t)
		replay, err := f.attempt(r.RequestID)
		must(t, "four-identity replay", err)
		if replay.Outcome != "replay" || replay.Root != nil || replay.Source != nil || replay.Attempt != a.Attempt || f.snapshot(t) != before {
			t.Fatal("replay generated effects or a new source")
		}
		_, err = f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4)", r.RequestID, a.Attempt, created.TargetID, *a.Root)
		must(t, "APP complete current prefix", err)
	}
	var held, spent string
	var revision, ordinal int64
	must(t, "independent cumulative budget oracle", f.owner.QueryRow(f.ctx, "SELECT a.held_amount::text,a.spent_amount::text,a.revision,h.ordinal FROM procurement_accounts a JOIN pp_account_heads h ON h.account_id=a.id WHERE a.id=$1", created.TargetID).Scan(&held, &spent, &revision, &ordinal))
	if held != "480" || spent != "0" || revision != 2 || ordinal != 4 {
		t.Fatalf("budget=%s/%s revision=%d ordinal=%d", held, spent, revision, ordinal)
	}
	op := uuid.NewString()
	noop := f.configure(t, created.TargetID, op, 2, 2000)
	if noop.Status != "applied" || noop.AccountJournalID == nil {
		t.Fatal("no-op did not issue real command journal")
	}
	replay := f.configure(t, created.TargetID, op, 2, 2000)
	if replay.Outcome != "replay" || replay.RootID != noop.RootID {
		t.Fatal("command replay changed root")
	}
	conflict := f.configure(t, created.TargetID, uuid.NewString(), 1, 2000)
	if conflict.Status != "rejected" || conflict.Reason != "revision_conflict" {
		t.Fatal("revision conflict receipt")
	}
	missing := f.configure(t, uuid.NewString(), uuid.NewString(), 1, 2000)
	if missing.Status != "rejected" || missing.Reason != "not_found" || missing.AccountJournalID != nil {
		t.Fatal("not-found receipt")
	}
}

func TestPostgresA2SupplierPBAndLaterImport(t *testing.T) {
	for _, later := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured_before", true: "configured_after"}[later], func(t *testing.T) {
			f := newFixture(t)
			a := f.create(t, 2000)
			f.bind(t, a.TargetID)
			commander, err := periodicbudget.NewCommander(f.app)
			must(t, "construct original Commander", err)
			configure := func() {
				_, e := commander.ConfigureProcurement(f.ctx, f.admin, a.TargetID, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000"})
				must(t, "actual APP original supplier budget configuration", e)
			}
			if !later {
				configure()
			}
			r := f.reserve(t)
			attempt, err := f.attempt(r.RequestID)
			must(t, "actual birth supplier PB", err)
			if later {
				configure()
			}
			_, err = f.app.Exec(f.ctx, "SELECT pp_validate_attempt($1),pp_validate_account($2)", attempt.Attempt, a.TargetID)
			must(t, "complete new/import PB prefix", err)
			var origin, held, spent string
			must(t, "independent PB allocation oracle", f.owner.QueryRow(f.ctx, "SELECT origin,held_amount::text,spent_amount::text FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=$1", attempt.Attempt).Scan(&origin, &held, &spent))
			want := "new"
			if later {
				want = "legacy_import"
			}
			if origin != want || held != "240" || spent != "0" {
				t.Fatalf("allocation %s held=%s spent=%s", origin, held, spent)
			}
		})
	}
}
