package c0candidate_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

const schemaSHA256 = "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047"
const permissionsSHA256 = "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae"
const candidateTables = `funding_lot_epoch funding_lot_runtime_attestations funding_lot_legacy_receipts funding_lot_legacy_sandbox_receipts funding_lot_legacy_settlements funding_lot_legacy_approved_refunds funding_lot_legacy_openings funding_wallet_legacy_sources funding_wallet_operations funding_wallet_heads funding_lots funding_lot_events funding_lot_balances funding_reservation_lots funding_settlement_lots funding_consumption_refund_lots`

type candidateFixture struct {
	t                                                          *testing.T
	ctx                                                        context.Context
	admin, owner, app, worker, projector                       *pgxpool.Pool
	cfg                                                        *pgxpool.Config
	schema                                                     string
	roles, createdRoles                                        []string
	passwords                                                  []string
	tenant, user, operator, approver, key, model, price, epoch string
	accounts                                                   map[string]string
	sandbox, preCutover, installed                             bool
	skipReserveAppend, skipTail, skipRefundSettlementUpdate    bool
}

func (f *candidateFixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *candidateFixture) exec(tx pgx.Tx, query string, args ...any) {
	f.t.Helper()
	_, err := tx.Exec(f.ctx, query, args...)
	f.must(err)
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code {
		t.Fatalf("expected SQLSTATE %s, got %v", code, err)
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal("generate isolated fixture identity")
	}
	return hex.EncodeToString(bytes[:])
}

func candidateFile(t *testing.T, name, expected string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "022-self-service-payments", name))
	if err != nil {
		t.Fatal("read fixed candidate file", err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		t.Fatalf("candidate %s changed; root review and focused regression required", name)
	}
	return raw
}

// Merely parsing a DSN must never authorize a write to another database.
func dedicatedConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	raw := os.Getenv("C0_CANDIDATE_DATABASE_URL")
	if raw == "" {
		t.Skip("C0_CANDIDATE_DATABASE_URL required; candidate PostgreSQL mechanisms were not exercised")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("candidate tests require only TCP 127.0.0.1:55439/moni_test without fallback hosts")
	}
	// Reject service routing and multiple raw hosts even when parsing resolves a
	// convenient first address. Never print the supplied connection string.
	if strings.Contains(strings.ToLower(raw), "service=") || strings.Contains(strings.ToLower(raw), "service%3d") {
		t.Fatal("service indirection is forbidden for candidate tests")
	}
	cfg.MaxConns = 3
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	return cfg
}

func newCandidateFixture(t *testing.T, sandbox bool) *candidateFixture {
	t.Helper()
	// Detect candidate/base drift before even opening the dedicated connection.
	candidateFile(t, "c0-production-schema.sql", schemaSHA256)
	candidateFile(t, "c0-production-permissions.sql", permissionsSHA256)
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) != 22 {
		t.Fatal("candidate requires reviewed exact 001--022 embedded migration baseline")
	}
	sort.Strings(names)
	if names[0] != "001_core.sql" || names[21] != "022_payment_runtime_attestation.sql" {
		t.Fatal("candidate migration baseline identity changed")
	}
	cfg := dedicatedConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	f := &candidateFixture{t: t, ctx: ctx, sandbox: sandbox, tenant: uuid.NewString(), user: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(), epoch: uuid.NewString(), accounts: make(map[string]string), schema: "flqa_" + randomSuffix(t)}
	defer func() {
		if f.admin == nil {
			cancel()
		}
	}()
	f.admin, err = pgxpool.NewWithConfig(ctx, cfg)
	f.must(err)
	t.Cleanup(func() { f.cleanup(); cancel() })
	var database, addr, actual, session string
	var port int
	f.must(f.admin.QueryRow(ctx, `SELECT current_database(),inet_server_addr()::text,inet_server_port(),current_user,session_user`).Scan(&database, &addr, &port, &actual, &session))
	if database != "moni_test" || addr != "127.0.0.1/32" && addr != "127.0.0.1" || port != 55439 || actual != session {
		t.Fatal("actual candidate connection failed dedicated database or LOGIN check")
	}
	var capable bool
	f.must(f.admin.QueryRow(ctx, `SELECT (rolsuper OR (rolcreaterole AND has_database_privilege(current_user,current_database(),'CREATE'))) FROM pg_roles WHERE rolname=current_user`).Scan(&capable))
	if !capable {
		t.Fatal("dedicated owner must be able to install isolated schema and temporary LOGINs")
	}
	_, err = f.admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{f.schema}.Sanitize())
	f.must(err)
	ownerCfg := cfg.Copy()
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog"
	f.cfg = ownerCfg.Copy()
	f.owner, err = pgxpool.NewWithConfig(ctx, ownerCfg)
	f.must(err)
	var selectedSchema string
	f.must(f.owner.QueryRow(ctx, `SELECT current_schema()`).Scan(&selectedSchema))
	if selectedSchema != f.schema {
		t.Fatal("owner connection did not select the exact isolated schema")
	}
	f.must(migrations.Apply(ctx, f.owner))
	var count int
	f.must(f.owner.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count))
	if count != 22 {
		t.Fatal("unexpected applied migration count")
	}
	for _, prefix := range []string{"flqa_a_", "flqa_w_", "flqa_p_"} {
		role, secret := prefix+randomSuffix(t), randomSuffix(t)
		_, err = f.admin.Exec(ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '`+secret+`'`)
		f.must(err)
		f.createdRoles = append(f.createdRoles, role)
		f.roles = append(f.roles, role)
		f.passwords = append(f.passwords, secret)
	}
	f.must(operations.ProvisionFor(ctx, f.owner, f.schema, f.roles[0], f.roles[2]))
	if sandbox {
		environment := uuid.NewString()
		_, err = f.owner.Exec(ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT $1,'c0:qa','C0 SQL ACL fixture',(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,repeat('a',64),$2,$3,$4`, environment, f.roles[0], f.roles[1], f.roles[2])
		f.must(err)
		f.must(operations.ProvisionPaymentWorkerFor(ctx, f.owner, f.schema, f.roles[0], f.roles[1], f.roles[2]))
		// Explicit fixture canonical, not a runtime-issued eight-role proof.
		_, err = f.owner.Exec(ctx, `INSERT INTO payment_runtime_attestations(environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256) SELECT $1,(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,(SELECT oid FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_roles WHERE rolname=$2),(SELECT oid FROM pg_roles WHERE rolname=$3),(SELECT oid FROM pg_roles WHERE rolname=$4),repeat('a',64),repeat('b',64),1,'{}'::bytea,encode(sha256('{}'::bytea),'hex')`, environment, f.roles[0], f.roles[1], f.roles[2])
		f.must(err)
	} else {
		_, err = f.owner.Exec(ctx, `GRANT USAGE ON SCHEMA `+pgx.Identifier{f.schema}.Sanitize()+` TO `+pgx.Identifier{f.roles[1]}.Sanitize())
		f.must(err)
	}
	f.seed()
	f.openLogins()
	if sandbox {
		for i, role := range []operations.PaymentDatabaseRole{operations.PaymentApplicationRole, operations.PaymentWorkerRole, operations.PaymentProjectionRole} {
			f.must(operations.VerifyPaymentRole(ctx, []*pgxpool.Pool{f.app, f.worker, f.projector}[i], role))
		}
	}
	return f
}

func (f *candidateFixture) seed() {
	tx, err := f.owner.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(f.ctx)
	f.exec(tx, `INSERT INTO tenants(id,name) VALUES($1,'C0 SQL fixture')`, f.tenant)
	for _, role := range []struct{ id, role string }{{f.user, "admin"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		f.exec(tx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'isolated-only',$3)`, role.id, role.id+"@fixture.invalid", role.role)
	}
	f.exec(tx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user)
	f.exec(tx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$1,'{"version":"v1","model":"model","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}','v1')`, f.model)
	f.exec(tx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model)
	f.exec(tx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'fixture','fixture',$1,$4,1000000)`, f.key, f.tenant, f.user, []string{f.model})
	f.exec(tx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1000000,1000000,clock_timestamp()-interval '1 day',$3)`, f.price, f.model, f.user)
	for _, kind := range []string{"AVAILABLE", "HELD", "REVENUE", "FUNDING_CLEARING", "OPERATING_GRANT_CLEARING"} {
		id := uuid.NewString()
		f.accounts[kind] = id
		var tenant any
		if kind == "AVAILABLE" || kind == "HELD" {
			tenant = f.tenant
		}
		f.exec(tx, `INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,$2,$3)`, id, tenant, kind)
	}
	f.must(tx.Commit(f.ctx))
}

func (f *candidateFixture) openLogins() {
	for i, role := range f.roles {
		cfg := f.cfg.Copy()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role, f.passwords[i]
		pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
		f.must(err)
		switch i {
		case 0:
			f.app = pool
		case 1:
			f.worker = pool
		case 2:
			f.projector = pool
		}
		var actual, session, schema string
		var safe bool
		f.must(pool.QueryRow(f.ctx, `SELECT current_user,session_user,current_schema(),NOT rolsuper AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolbypassrls AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=(SELECT oid FROM pg_roles WHERE rolname=current_user)) FROM pg_roles WHERE rolname=current_user`).Scan(&actual, &session, &schema, &safe))
		if actual != role || session != role || schema != f.schema || !safe {
			f.t.Fatal("fixture is not a distinct restricted actual LOGIN")
		}
	}
}

type originalFunction struct {
	OID  uint32
	Body string
}

func (f *candidateFixture) functions() map[string]originalFunction {
	rows, err := f.owner.Query(f.ctx, `SELECT p.oid::oid,p.proname||'('||p.proargtypes::text||')',pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1`, f.schema)
	f.must(err)
	defer rows.Close()
	result := make(map[string]originalFunction)
	for rows.Next() {
		var key string
		var value originalFunction
		f.must(rows.Scan(&value.OID, &key, &value.Body))
		result[key] = value
	}
	f.must(rows.Err())
	return result
}

func (f *candidateFixture) install() {
	f.t.Helper()
	before := f.functions()
	_, err := f.owner.Exec(f.ctx, string(candidateFile(f.t, "c0-production-schema.sql", schemaSHA256)))
	f.must(err)
	grants := string(candidateFile(f.t, "c0-production-permissions.sql", permissionsSHA256))
	for key, value := range map[string]string{"fl_schema": f.schema, "fl_app": f.roles[0], "fl_worker": f.roles[1], "fl_projector": f.roles[2]} {
		grants = strings.ReplaceAll(grants, `:"`+key+`"`, pgx.Identifier{value}.Sanitize())
	}
	if strings.Contains(grants, `:"`) {
		f.t.Fatal("unresolved candidate ACL identifier")
	}
	_, err = f.owner.Exec(f.ctx, grants)
	f.must(err)
	after := f.functions()
	for key, value := range before {
		if after[key] != value {
			f.t.Fatalf("candidate changed original function OID/body: %s", key)
		}
	}
	var count int
	f.must(f.owner.QueryRow(f.ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count))
	if count != 22 {
		f.t.Fatal("candidate changed migration history")
	}
	f.installed = true
}

func (f *candidateFixture) cleanup() {
	// Stop all owned connections before independent cleanup. Nothing is dropped
	// by prefix, and cancellation of the test never disables its cleanup.
	for _, pool := range []*pgxpool.Pool{f.app, f.worker, f.projector, f.owner} {
		if pool != nil {
			pool.Close()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer f.admin.Close()
	if _, err := f.admin.Exec(ctx, `DROP SCHEMA IF EXISTS `+pgx.Identifier{f.schema}.Sanitize()+` CASCADE`); err != nil {
		f.t.Error("cleanup isolated candidate schema", err)
	}
	for _, role := range f.createdRoles {
		if _, err := f.admin.Exec(ctx, `DROP ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
			f.t.Error("cleanup isolated candidate LOGIN", role, err)
		}
	}
	var schemas, roles int
	err := f.admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_namespace WHERE nspname=$1),(SELECT count(*) FROM pg_roles WHERE rolname=ANY($2::text[]))`, f.schema, f.createdRoles).Scan(&schemas, &roles)
	if err != nil || schemas != 0 || roles != 0 {
		f.t.Errorf("candidate cleanup incomplete: schema=%d LOGINs=%d error=%v", schemas, roles, err)
	}
}

func (f *candidateFixture) snapshot() map[string][32]byte {
	f.t.Helper()
	names := strings.Fields(`tenants ledger_accounts journals journal_lines request_records reservations settlements credit_orders refund_orders funding_receipts api_keys audit_events projection_outbox`)
	if f.installed {
		names = append(names, strings.Fields(candidateTables)...)
	}
	result := make(map[string][32]byte, len(names))
	for _, name := range names {
		var value string
		f.must(f.owner.QueryRow(f.ctx, `SELECT coalesce(jsonb_agg(r ORDER BY r::text),'[]'::jsonb)::text FROM (SELECT to_jsonb(x) r FROM `+pgx.Identifier{name}.Sanitize()+` x) q`).Scan(&value))
		result[name] = sha256.Sum256([]byte(value))
	}
	return result
}

func (f *candidateFixture) unchanged(before map[string][32]byte) {
	f.t.Helper()
	after := f.snapshot()
	if !reflect.DeepEqual(before, after) {
		for table, hash := range before {
			if after[table] != hash {
				f.t.Errorf("failed transaction changed %s", table)
			}
		}
		f.t.FailNow()
	}
}

func (f *candidateFixture) run(fn func(pgx.Tx), want string) {
	f.t.Helper()
	var before map[string][32]byte
	if want != "" {
		before = f.snapshot()
	}
	base := f.ctx
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	f.ctx = ctx
	defer func() { f.ctx = base; cancel() }()
	tx, err := f.app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	f.must(err)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	fn(tx)
	err = tx.Commit(ctx)
	f.ctx = base
	if want == "" {
		f.must(err)
	} else {
		requireCode(f.t, err, want)
		f.unchanged(before)
	}
}

func (f *candidateFixture) initZero(kind string, opening bool, want string) {
	before := f.snapshot()
	tx, err := f.owner.Begin(f.ctx)
	f.must(err)
	defer tx.Rollback(f.ctx)
	var at time.Time
	f.must(tx.QueryRow(f.ctx, `SELECT date_trunc('microseconds',clock_timestamp())`).Scan(&at))
	count := int64(0)
	if opening {
		count = 1
		f.exec(tx, `INSERT INTO funding_lot_legacy_openings(id,epoch_id,tenant_id,kind,account_id,amount,observed_at) VALUES($1,$2,$3,'available',$4,0,$5)`, uuid.NewString(), f.epoch, f.tenant, f.accounts["AVAILABLE"], at)
	}
	genesis := uuid.NewString()
	f.exec(tx, `INSERT INTO funding_wallet_operations(id,tenant_id,epoch_id,seq,kind,source_id,event_count,available_delta,held_delta,consumed_delta,issued_delta) VALUES($1,$2,$3,0,$4,$2,0,0,0,0,0)`, genesis, f.tenant, f.epoch, kind)
	f.exec(tx, `INSERT INTO funding_wallet_heads VALUES($1,$2,0,$3)`, f.tenant, f.epoch, genesis)
	f.proofFixture(tx, at)
	f.exec(tx, `INSERT INTO funding_lot_epoch(singleton,id,schema_version,database_oid,schema_oid,basis_digest,manifest_digest,descriptor_digest,runtime_proof_digest,tenant_count,receipt_count,settlement_count,approved_refund_count,opening_count,legacy_source_count,available_total,held_total,actor,created_at) SELECT true,$1,1,(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,repeat('c',64),repeat('a',64),repeat('b',64),encode(sha256('{}'::bytea),'hex'),$3::bigint,0,0,0,$3::bigint,0,0,0,'offline-maintenance',$2`, f.epoch, at, count)
	err = tx.Commit(f.ctx)
	if want == "" {
		f.must(err)
	} else {
		requireCode(f.t, err, want)
		f.unchanged(before)
	}
}

func (f *candidateFixture) proofFixture(tx pgx.Tx, at time.Time) {
	// Marker digests are intentionally obvious test values; no Permit, Go
	// runtime attestation parser or listener is exercised by this fixture.
	if f.sandbox {
		f.exec(tx, `INSERT INTO funding_lot_runtime_attestations(singleton,epoch_id,proof_version,mode,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,predecessor_payment_digest,proof_canonical,proof_sha256,manifest_digest,descriptor_digest,created_at) SELECT true,$1,2,'sandbox',(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,(SELECT oid FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_roles WHERE rolname=$2),(SELECT oid FROM pg_roles WHERE rolname=$3),(SELECT oid FROM pg_roles WHERE rolname=$4),encode(sha256('{}'::bytea),'hex'),'{}'::bytea,encode(sha256('{}'::bytea),'hex'),repeat('a',64),repeat('b',64),$5`, f.epoch, f.roles[0], f.roles[1], f.roles[2], at)
	} else {
		f.exec(tx, `INSERT INTO funding_lot_runtime_attestations(singleton,epoch_id,proof_version,mode,database_oid,schema_oid,schema_owner_oid,app_role_oid,projector_role_oid,proof_canonical,proof_sha256,manifest_digest,descriptor_digest,created_at) SELECT true,$1,2,'ordinary',(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,(SELECT oid FROM pg_roles WHERE rolname=current_user),(SELECT oid FROM pg_roles WHERE rolname=$2),(SELECT oid FROM pg_roles WHERE rolname=$3),'{}'::bytea,encode(sha256('{}'::bytea),'hex'),repeat('a',64),repeat('b',64),$4`, f.epoch, f.roles[0], f.roles[2], at)
	}
}
