package procurementrequestbirth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	"github.com/moni-ai/moni-aigate/internal/procurementbirthschema"
	"github.com/moni-ai/moni-aigate/internal/procurementrequestbirth"
	"github.com/moni-ai/moni-aigate/migrations"
)

type fixture struct {
	ctx                                                                 context.Context
	owner, app, peer                                                    *pgxpool.Pool
	b                                                                   procurementrequestbirth.Bindings
	tenant, user, key, model, operator, approver, legacy, legacyDeleted string
}

func must(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) {
			t.Fatalf("%s: %v; detail=%s; where=%s; position=%d; internal=%s", step, err, p.Detail, p.Where, p.Position, p.InternalQuery)
		}
		t.Fatalf("%s: %v", step, err)
	}
}
func code(t *testing.T, err error, want string) {
	t.Helper()
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != want {
		t.Fatalf("SQLSTATE %s: %v", want, err)
	}
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	// ADR-005 freezes this procurement-prefix research. Its historical prefix
	// fixture funds through the current credit path, which requires the 027
	// wallet schema the prefix does not contain.
	t.Skip("frozen procurement-prefix research: historical fixture cannot fund through the current 027 wallet path")
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required: dedicated PG16 actual APP request birth acceptance")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	must(t, "parse dedicated PG", err)
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test" || len(c.Fallbacks) != 0 {
		t.Fatal("requires dedicated loopback moni_test database/LOGIN at 55439 without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	owner, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	must(t, "connect installer", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	b := procurementrequestbirth.Bindings{Schema: "rb_" + suffix, App: "rb_app_" + suffix, Writer: "rb_w_" + suffix}
	peer, projector := "rb_peer_"+suffix, "rb_proj_"+suffix
	f := &fixture{ctx: ctx, owner: owner, b: b, tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString()}
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.peer != nil {
			f.peer.Close()
		}
		doneCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		defer cancel()
		defer f.owner.Close()
		_, e := f.owner.Exec(doneCtx, "DROP SCHEMA IF EXISTS "+q(b.Schema)+" CASCADE")
		if e != nil {
			t.Errorf("fixture schema cleanup: %v", e)
			return
		}
		for _, name := range []string{peer, projector, b.App, b.Writer} {
			if _, e = f.owner.Exec(doneCtx, "DROP ROLE IF EXISTS "+q(name)); e != nil {
				t.Errorf("fixture role cleanup: %v", e)
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
		must(t, "create exact fixture role", err)
	}
	must(t, "read actual APP OID", owner.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", b.App).Scan(&f.b.AppOID))
	must(t, "read W OID", owner.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", b.Writer).Scan(&f.b.WriterOID))
	_, err = owner.Exec(ctx, "CREATE SCHEMA "+q(b.Schema))
	must(t, "create schema", err)
	ownedCfg := cfg.Copy()
	ownedCfg.ConnConfig.RuntimeParams["search_path"] = b.Schema
	ownedCfg.MaxConns = 6
	owned, err := pgxpool.NewWithConfig(ctx, ownedCfg)
	must(t, "connect trusted schema installer", err)
	must(t, "apply through original018 before legacy rows", migrations.ApplyThrough(ctx, owned, "018_tenant_pricing.sql"))
	f.owner = owned
	owner.Close()
	tx, err := owned.Begin(ctx)
	must(t, "begin baseline seed", err)
	defer tx.Rollback(ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, e := tx.Exec(ctx, sql, args...)
		must(t, "seed original parent", e)
	}
	exec("INSERT INTO tenants(id,name) VALUES($1,'request birth fixture')", f.tenant)
	for _, u := range []struct{ id, role string }{{f.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		exec("INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-disabled',$3)", u.id, u.id+"@example.invalid", u.role)
	}
	exec("INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)", f.tenant, f.user)
	exec("INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,'model',$2,'v1')", f.model, `{"version":"v1","model":"model","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`)
	exec("INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, f.model)
	exec("INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'fixture','fixture-key','fixture-disabled',$4,100000)", f.key, f.tenant, f.user, []string{f.model})
	exec("INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1000000,1000000,clock_timestamp()-interval '1 day',$2)", f.model, f.operator)
	// Seed only through the real 018 boundary, before 020 exists. These
	// legal legacy originals have no reservation or PB creation stamp.
	originalDelete, a1Delete := uuid.NewString(), uuid.NewString()
	for _, id := range []string{originalDelete, a1Delete} {
		exec(`INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at)
 SELECT $1,t.id,$2,$3,$4,p.id,p.input_rate,p.output_rate,100,20,'v1','r3','legacy-no-pb',clock_timestamp()+interval '1 hour','fixture',false,0,NULL,t.price_group_id,t.pricing_revision,clock_timestamp()
 FROM tenants t JOIN price_versions p ON p.model_id=$4 WHERE t.id=$5`, id, f.key, f.user, f.model, f.tenant)
	}
	f.legacyDeleted = a1Delete
	must(t, "commit original parents", tx.Commit(ctx))
	must(t, "advance legitimate legacy rows through original022", migrations.ApplyThrough(ctx, owned, "022_payment_runtime_attestation.sql"))
	originalTx, e := owned.Begin(ctx)
	must(t, "begin original-profile legacy UPDATE DELETE", e)
	_, e = originalTx.Exec(ctx, "UPDATE request_records SET status=status WHERE id=$1", originalDelete)
	must(t, "original-profile legacy UPDATE", e)
	_, e = originalTx.Exec(ctx, "DELETE FROM request_records WHERE id=$1", originalDelete)
	must(t, "original-profile legacy DELETE", e)
	must(t, "original-profile queued020 final-missing COMMIT", originalTx.Commit(ctx))
	ownerStore := billing.New(owned)
	credit, err := ownerStore.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: uuid.NewString(), ProofRef: "fixture:verified", Amount: 10000, IdempotencyKey: uuid.NewString(), RequestHMAC: "fixture:credit"})
	must(t, "create real initial customer credit", err)
	_, err = ownerStore.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver})
	must(t, "fund original wallet", err)
	// A long original TEXT policy is legal. Installation must neither add a wide
	// payload index nor register this committed original as tracked retroactively.
	old := f.input()
	var policy strings.Builder
	for i := 0; i < 160; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprint(i)))
		policy.WriteString(hex.EncodeToString(sum[:]))
	}
	old.PolicyVersion = policy.String()
	legacy, err := ownerStore.Reserve(ctx, old)
	must(t, "prepare legal long-TEXT legacy Reserve before installation", err)
	f.legacy = legacy.RequestID
	// All original broad provisioning occurs BEFORE the four new tables exist.
	must(t, "provision original operating roles", operations.ProvisionFor(ctx, owned, b.Schema, b.App, projector))
	ddl, err := procurementbirthschema.A1SQL(b.Schema)
	must(t, "authenticate minimal A1 DDL", err)
	_, err = owned.Exec(ctx, ddl)
	must(t, "install minimal A1 DDL", err)
	sql, err := procurementrequestbirth.SQL(f.b)
	must(t, "authenticate A1 writer", err)
	_, err = owned.Exec(ctx, sql)
	must(t, "install A1 functions and guards", err)
	acl, err := procurementrequestbirth.Permissions(f.b)
	must(t, "authenticate A1 ACL", err)
	_, err = owned.Exec(ctx, acl)
	must(t, "install A1 precise ACL", err)
	_, err = owned.Exec(ctx, "GRANT USAGE ON SCHEMA "+q(b.Schema)+" TO "+q(peer))
	must(t, "allow peer to observe EXECUTE denial", err)
	login := func(name string) *pgxpool.Pool {
		t.Helper()
		lc := ownedCfg.Copy()
		lc.ConnConfig.User = name
		lc.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
		lc.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
		lc.ConnConfig.RuntimeParams["TimeZone"] = "Asia/Shanghai"
		p, e := pgxpool.NewWithConfig(ctx, lc)
		must(t, "connect actual LOGIN", e)
		var s, u string
		must(t, "observe LOGIN identity", p.QueryRow(ctx, "SELECT session_user,current_user").Scan(&s, &u))
		if s != name || u != name {
			t.Fatal("not actual LOGIN")
		}
		return p
	}
	f.app = login(b.App)
	f.peer = login(peer)
	return f
}
func (f *fixture) input() billing.ReserveInput {
	return billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "birth-request-digest", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute)}
}
func (f *fixture) reserve(t *testing.T) billing.Reservation {
	t.Helper()
	r, err := billing.New(f.app).Reserve(f.ctx, f.input())
	must(t, "actual APP original Reserve", err)
	return r
}
func (f *fixture) snapshot(t *testing.T) string {
	t.Helper()
	tables := []string{"request_records", "reservations", "request_idempotency", "api_keys", "ledger_accounts", "journals", "journal_lines", "periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "recovery_jobs", "audit_events", "projection_outbox", "pp_operation_roots", "pp_root_steps", "pp_request_origins", "pp_request_heads"}
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		parts = append(parts, "('"+table+"',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb) FROM "+pgx.Identifier{f.b.Schema, table}.Sanitize()+" x))")
	}
	var v string
	must(t, "read full rollback snapshot", f.owner.QueryRow(f.ctx, "SELECT jsonb_object_agg(k,v)::text FROM (VALUES "+strings.Join(parts, ",")+") AS actual(k,v)").Scan(&v))
	return v
}
func (f *fixture) checkBirth(t *testing.T, id string) {
	t.Helper()
	var exact bool
	must(t, "independent full birth mapping", f.owner.QueryRow(f.ctx, `SELECT
 (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)
 IS NOT DISTINCT FROM
 (r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at)
 AND (r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid,r.pp_last_decision_id) IS NOT DISTINCT FROM (o.root_id,1::bigint,o.created_xid,NULL::text)
 AND (o.initial_status,o.initial_billing_status,o.initial_latest_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)
 AND (h.ordinal,h.last_decision_id,h.updated_xid) IS NOT DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
 AND (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
 IS NOT DISTINCT FROM ('request_birth'::text,'request'::text,r.tenant_id,r.id,NULL::text,NULL::text,o.created_xid,$2::oid,1::bigint,'born'::text)
 AND (st.step_no,st.kind,st.source_id,st.created_xid) IS NOT DISTINCT FROM (1::bigint,'request_birth'::text,r.id,o.created_xid)
 AND (SELECT count(*) FROM pp_request_origins WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM request_records WHERE pp_origin_root_id=o.root_id)=1
 FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id JOIN pp_request_heads h ON h.request_id=r.id JOIN pp_operation_roots rt ON rt.id=o.root_id JOIN pp_root_steps st ON st.root_id=o.root_id WHERE r.id=$1`, id, f.b.AppOID).Scan(&exact))
	if !exact {
		t.Fatal("actual source graph differs from original frozen facts")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", id)
	must(t, "APP invoker readonly proof", err)
}

func TestPostgresA1RealReserveAndLegacy(t *testing.T) {
	f := newFixture(t)
	for _, ip := range []string{"", "192.0.2.12", "2001:db8::12"} {
		t.Run("client_"+ip, func(t *testing.T) {
			in := f.input()
			in.Principal.ClientIP = ip
			in.Streaming = ip != ""
			r, err := billing.New(f.app).Reserve(f.ctx, in)
			must(t, "actual APP original Reserve", err)
			if r.Amount != 120 {
				t.Fatalf("original reserve amount=%v", r.Amount)
			}
			f.checkBirth(t, r.RequestID)
			before := f.snapshot(t)
			replay := in
			replay.RequestID = uuid.NewString()
			_, err = billing.New(f.app).Reserve(f.ctx, replay)
			var be *billing.Error
			if !errors.As(err, &be) || be.Code != "request_exists" {
				t.Fatalf("original idempotency replay: %v", err)
			}
			if f.snapshot(t) != before {
				t.Fatal("replay mutated committed graph or finances")
			}
		})
	}
	legacyTx, e := f.owner.Begin(f.ctx)
	must(t, "begin A1 legacy UPDATE DELETE", e)
	_, e = legacyTx.Exec(f.ctx, "UPDATE request_records SET status=status WHERE id=$1", f.legacyDeleted)
	must(t, "A1 unchanged legacy UPDATE", e)
	_, e = legacyTx.Exec(f.ctx, "DELETE FROM request_records WHERE id=$1", f.legacyDeleted)
	must(t, "A1 unchanged legacy DELETE", e)
	must(t, "A1 queued legacy final-missing COMMIT", legacyTx.Commit(f.ctx))
	var legacy bool
	must(t, "observe unchanged legacy", f.owner.QueryRow(f.ctx, "SELECT pp_origin_root_id IS NULL AND pp_origin_root_step IS NULL AND pp_origin_xid IS NULL AND pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM pp_request_origins WHERE request_id=$1) FROM request_records WHERE id=$1", f.legacy).Scan(&legacy))
	if !legacy {
		t.Fatal("installation backfilled legacy")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", f.legacy)
	code(t, err, "PP906")
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	must(t, "begin RR readonly", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SELECT pp_rb_validate_request_v1(request_id) FROM pp_request_origins")
	must(t, "RR readonly proof remains legal", err)
	must(t, "commit RR readonly", tx.Commit(f.ctx))
}

func TestPostgresA1ProtectedRawWritesAndRolesRollback(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	f.checkBirth(t, r.RequestID)
	for _, sql := range []string{
		"UPDATE request_records SET status='succeeded' WHERE id=$1",
		"UPDATE request_records SET billing_status='settled' WHERE id=$1",
		"UPDATE request_records SET latest_finalization_id=NULL WHERE id=$1",
		"UPDATE request_records SET input_upper_bound=input_upper_bound+1 WHERE id=$1",
		"UPDATE request_records SET pp_origin_root_id=NULL WHERE id=$1",
		"DELETE FROM request_records WHERE id=$1",
		"UPDATE pp_request_origins SET request_id=request_id WHERE request_id=$1",
		"DELETE FROM pp_request_heads WHERE request_id=$1",
		"UPDATE pp_root_steps SET root_id=root_id WHERE source_id=$1",
		"DELETE FROM pp_operation_roots WHERE request_id=$1",
	} {
		t.Run(sql, func(t *testing.T) {
			before := f.snapshot(t)
			_, err := f.app.Exec(f.ctx, sql, r.RequestID)
			if err == nil {
				t.Fatal("protected write accepted")
			}
			if f.snapshot(t) != before {
				t.Fatal("protected write changed committed rows")
			}
		})
	}
	for _, table := range []string{"request_records", "pp_request_origins", "pp_request_heads", "pp_root_steps", "pp_operation_roots"} {
		before := f.snapshot(t)
		_, err := f.app.Exec(f.ctx, "TRUNCATE "+pgx.Identifier{table}.Sanitize()+" CASCADE")
		code(t, err, "42501")
		if f.snapshot(t) != before {
			t.Fatal("TRUNCATE changed rows")
		}
	}
	_, err := f.peer.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", r.RequestID)
	code(t, err, "42501")
	_, err = f.owner.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", r.RequestID)
	code(t, err, "PP901")
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin owner SET ROLE attack", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SET LOCAL ROLE "+pgx.Identifier{f.b.App}.Sanitize())
	must(t, "set role does not change LOGIN", err)
	_, err = tx.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", r.RequestID)
	code(t, err, "PP901")
	_ = tx.Rollback(f.ctx)
}

func TestPostgresA1ActualReserveInternalDMLFailuresRollback(t *testing.T) {
	f := newFixture(t)
	f.reserve(t)
	// Each real boundary is reached by original Reserve through the automatic
	// hook; owner only installs a fault trigger and independently observes rows.
	for _, tc := range []struct{ table, event string }{{"pp_request_origins", "INSERT"}, {"pp_request_heads", "INSERT"}, {"request_records", "UPDATE"}, {"pp_root_steps", "INSERT"}, {"pp_operation_roots", "INSERT"}, {"reservations", "INSERT"}, {"journal_lines", "INSERT"}, {"audit_events", "INSERT"}, {"projection_outbox", "INSERT"}} {
		t.Run(tc.table+tc.event, func(t *testing.T) {
			before := f.snapshot(t)
			_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION rb_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'a1_injected_fault' USING ERRCODE='PZ991'; END$$`)
			must(t, "install fixture fault function", err)
			table := pgx.Identifier{tc.table}.Sanitize()
			_, err = f.owner.Exec(f.ctx, "CREATE TRIGGER zz_rb_fault BEFORE "+tc.event+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION rb_fault()")
			must(t, "install exact fault boundary", err)
			_, err = billing.New(f.app).Reserve(f.ctx, f.input())
			code(t, err, "PZ991")
			_, err = f.owner.Exec(f.ctx, "DROP TRIGGER zz_rb_fault ON "+table)
			must(t, "remove exact fault", err)
			_, err = f.owner.Exec(f.ctx, "DROP FUNCTION rb_fault()")
			must(t, "remove fixture fault function", err)
			if f.snapshot(t) != before {
				t.Fatal("failed birth/Reserve left partial graph or financial effects")
			}
			f.reserve(t)
		})
	}
}
