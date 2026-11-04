package periodicbudget_test

import (
	"context"
	"encoding/json"
	"errors"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/funding019"
	"github.com/moni-ai/moni-aigate/migrations"
)

const utc6Layout = "2006-01-02T15:04:05.000000Z"

// Owner is used only for isolated DDL/catalog seeds and the explicit clock
// fixture. All financial sources, configuration and helpers use real APP LOGIN.
type pbTxFixture struct {
	ctx                                                  context.Context
	owner, app, projection                               *pgxpool.Pool
	schema, appRole, projectionRole                      string
	tenant, user, operator, approver, key, model, price  string
	channel, version, credential, account, binding, cost string
	profileVersion                                       string
	historical                                           bool
}

func pbTxMust(t *testing.T, step string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatalf("%s failed (SQLSTATE %s)", step, pg.Code)
	}
	t.Fatalf("%s failed (%T)", step, err)
}

func newPBTxFixture(t *testing.T) *pbTxFixture      { return newPBTxFixtureVariant(t, false) }
func newPBBillingFixture(t *testing.T) *pbTxFixture { return newPBTxFixtureVariant(t, true) }
func newPBTxFixtureVariant(t *testing.T, latest bool) *pbTxFixture {
	return newPBTxFixtureBaseline(t, latest, "")
}

// through is an explicit historical fixture boundary. The existing latest and
// pre-020 factories retain their original behavior; the prefix candidate never
// follows a future embedded migration through the latest branch.
func newPBTxFixtureBaseline(t *testing.T, latest bool, through string) *pbTxFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated periodic-budget Tx PostgreSQL tests")
	}
	base, err := pgxpool.ParseConfig(raw)
	if err != nil || base.ConnConfig.Host != "127.0.0.1" || base.ConnConfig.Port != 55439 || base.ConnConfig.Database != "moni_test" || base.ConnConfig.User != "moni_test" {
		t.Fatal("periodic-budget tests require the dedicated local moni_test database on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, base.Copy())
	pbTxMust(t, "open isolated administrator", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &pbTxFixture{ctx: ctx, historical: !latest, schema: "pb_tx_" + suffix, appRole: "pb_tx_app_" + suffix, projectionRole: "pb_tx_projection_" + suffix,
		tenant: uuid.NewString(), user: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), key: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(),
		channel: uuid.NewString(), version: uuid.NewString(), credential: uuid.NewString(), account: uuid.NewString(), binding: uuid.NewString(), cost: uuid.NewString(), profileVersion: "v1"}
	if latest {
		f.profileVersion = "10000000-0000-4000-8000-0000000000a1"
	}
	// Cleanup captures exact generated identities; no prefix deletion or preview DB.
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.projection != nil {
			f.projection.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE")
		if e != nil {
			t.Error("isolated schema cleanup failed")
		}
		for _, role := range []string{f.appRole, f.projectionRole} {
			if _, e = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("isolated LOGIN cleanup failed")
			}
		}
		admin.Close()
		cancel()
	})
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	pbTxMust(t, "create isolated schema", err)
	cfg := base.Copy()
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	f.owner, err = pgxpool.NewWithConfig(ctx, cfg.Copy())
	pbTxMust(t, "open schema owner", err)
	if through != "" {
		pbTxMust(t, "apply explicit historical application migrations", migrations.ApplyThrough(ctx, f.owner, through))
	} else if latest {
		pbTxMust(t, "apply actual latest application migrations", migrations.Apply(ctx, f.owner))
	} else {
		pbTxMust(t, "apply actual migrations 001–019", migrations.ApplyThrough(ctx, f.owner, "019_operations_alerts.sql"))
	}
	var count, max int
	pbTxMust(t, "verify migration baseline", f.owner.QueryRow(ctx, `SELECT count(*),max(left(name,3)::integer) FROM schema_migrations`).Scan(&count, &max))
	expected := 19
	if through == "022_payment_runtime_attestation.sql" {
		expected = 22
	} else if latest {
		names, e := fs.Glob(migrations.Files, "*.sql")
		pbTxMust(t, "read complete embedded migration manifest", e)
		expected = len(names)
	}
	if count != expected || max != expected {
		t.Fatal("fixture migration baseline does not match its explicit version")
	}
	f.seedCatalog(t)
	for _, role := range []string{f.appRole, f.projectionRole} {
		_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS")
		pbTxMust(t, "create isolated runtime LOGIN", err)
	}
	pbTxMust(t, "provision actual application/projection roles", operations.ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	openLogin := func(role string) *pgxpool.Pool {
		login := cfg.Copy()
		login.ConnConfig.User = role
		login.ConnConfig.Password = ""
		pool, e := pgxpool.NewWithConfig(ctx, login)
		pbTxMust(t, "open actual runtime LOGIN", e)
		var current, session string
		pbTxMust(t, "attest actual runtime LOGIN", pool.QueryRow(ctx, `SELECT current_user,session_user`).Scan(&current, &session))
		if current != role || session != role {
			pool.Close()
			t.Fatal("runtime connection did not authenticate as the isolated role")
		}
		return pool
	}
	f.app = openLogin(f.appRole)
	f.projection = openLogin(f.projectionRole)
	// Use the existing complete manual-credit path, including source, receipt,
	// clearing journal, approval, outbox and audit. No owner balance shortcut.
	store := billing.New(f.app)
	operation := uuid.NewString()
	credit, e := store.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 10000, BusinessRef: "pb-fixture:" + operation, ProofRef: "fixture:verified", IdempotencyKey: operation, RequestHMAC: operation})
	pbTxMust(t, "APP create funding source", e)
	_, e = store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: credit.ID})
	pbTxMust(t, "APP approve complete funding", e)
	if !latest {
		for _, name := range []string{"b-core.sql", "b-commands.sql"} {
			_, e = f.owner.Exec(ctx, pbTxTrackedSQL(t, name))
			pbTxMust(t, "install tracked periodic-budget SQL", e)
		}
		// ProvisionFor deliberately starts from a broad grant; the tracked B role
		// file must then remove it and install the exact column/table restrictions.
		pbTxMust(t, "reprovision actual roles with B tables", operations.ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		permissions := strings.NewReplacer(`:"app_role"`, pgx.Identifier{f.appRole}.Sanitize(), `:"projection_role"`, pgx.Identifier{f.projectionRole}.Sanitize()).Replace(pbTxTrackedSQL(t, "b-permissions.sql"))
		_, e = f.owner.Exec(ctx, permissions)
		pbTxMust(t, "apply tracked B permissions", e)
	}
	if !latest {
		f.setClock(t, time.Date(2026, 11, 10, 12, 0, 0, 123456000, time.UTC))
	}
	f.verifyFinance(t)
	return f
}

func pbTxTrackedSQL(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate tracked SQL")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "specs", "023-periodic-products", name))
	pbTxMust(t, "read tracked SQL", err)
	return string(raw)
}

func (f *pbTxFixture) seedCatalog(t *testing.T) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	pbTxMust(t, "begin static catalog seed", err)
	defer tx.Rollback(f.ctx)
	f.exec(t, tx, `INSERT INTO tenants(id,name) VALUES($1,'periodic-tx-fixture')`, f.tenant)
	for _, u := range []struct{ id, role string }{{f.user, "admin"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		f.exec(t, tx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, u.id, u.id+"@fixture.invalid", u.role)
	}
	f.exec(t, tx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user)
	if f.historical {
		f.exec(t, tx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$1,'{"version":"v1","model":"model","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}','v1')`, f.model)
	} else {
		f.exec(t, tx, `SELECT meter_insert_text_model($1,'model',$2::jsonb,$3)`, f.model, `{"version":"`+f.profileVersion+`","model":"model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`, f.profileVersion)
	}
	f.exec(t, tx, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, f.tenant, f.model)
	f.exec(t, tx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'fixture','fixture',$1,$4,1000000)`, f.key, f.tenant, f.user, []string{f.model})
	if f.historical {
		f.exec(t, tx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1000000,1000000,clock_timestamp()-interval '1 day',$3)`, f.price, f.model, f.user)
	} else {
		f.exec(t, tx, `SELECT meter_insert_token_price($1,$2,1000000,1000000,clock_timestamp()-interval '1 day',$3,NULL,NULL,NULL)`, f.price, f.model, f.user)
	}
	f.exec(t, tx, `INSERT INTO channels(id,name,current_version) VALUES($1,'fixture',$2)`, f.channel, f.version)
	f.exec(t, tx, `INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'unused-fixture')`, f.credential, f.channel)
	f.exec(t, tx, `INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, f.version, f.channel, f.credential)
	f.exec(t, tx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'fixture',1,10)`, f.version, f.channel, f.credential)
	if f.historical {
		f.exec(t, tx, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis) VALUES($1,$2,'upstream',2000000,2000000,'contract')`, f.cost, f.channel)
	} else {
		f.exec(t, tx, `SELECT meter_insert_token_cost($1,$2,'upstream',2000000,2000000,'contract',NULL)`, f.cost, f.channel)
	}
	f.exec(t, tx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'fixture','fixture','active',1000000)`, f.account)
	f.exec(t, tx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, f.binding, f.credential, f.channel, f.account, uuid.NewString())
	pbTxMust(t, "commit static catalog seed", tx.Commit(f.ctx))
}

func (f *pbTxFixture) begin(t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	pbTxMust(t, "begin APP transaction", err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	})
	return tx
}
func (f *pbTxFixture) exec(t *testing.T, tx pgx.Tx, q string, args ...any) {
	t.Helper()
	_, err := tx.Exec(f.ctx, q, args...)
	pbTxMust(t, "fixture SQL", err)
}

// This is only an owner-controlled test clock in the exact random schema.
// It does not claim production wall-clock or clock-regression certification.
func (f *pbTxFixture) setClock(t *testing.T, at time.Time) {
	t.Helper()
	if !ValidUTC6(at.UTC().Format(utc6Layout)) {
		t.Fatal("invalid fixture clock")
	}
	query := "CREATE OR REPLACE FUNCTION " + pgx.Identifier{f.schema, "pb_now"}.Sanitize() + "() RETURNS timestamptz LANGUAGE sql VOLATILE SET search_path TO " + pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp AS $$ SELECT '" + at.UTC().Format(utc6Layout) + "'::timestamptz $$"
	_, err := f.owner.Exec(f.ctx, query)
	pbTxMust(t, "set isolated owner clock fixture", err)
}

func (f *pbTxFixture) configure(t *testing.T, scope string, enabled bool, limit int64) string {
	t.Helper()
	tx := f.begin(t)
	var control string
	var expected int64
	var target, kind, operationScope string
	switch scope {
	case "tenant":
		f.exec(t, tx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, f.tenant)
		f.exec(t, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, f.user)
		target = "/api/platform/tenants/" + f.tenant + "/periodic-budget"
		kind = "tenant_periodic_budget.configure"
		operationScope = "platform"
	case "key":
		f.customerRoots(t, tx, "")
		target = "/api/keys/" + f.key + "/periodic-budget"
		kind = "key_periodic_budget.configure"
		operationScope = f.tenant
	case "procurement":
		f.exec(t, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, f.user)
		f.exec(t, tx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, f.account)
		target = "/api/platform/procurement-accounts/" + f.account + "/periodic-budget"
		kind = "procurement_periodic_budget.configure"
		operationScope = "platform"
	default:
		t.Fatal("unknown fixture scope")
	}
	err := tx.QueryRow(f.ctx, `SELECT id,revision FROM periodic_budget_controls WHERE scope=$1 AND coalesce(account_id,key_id,tenant_id)=$2 FOR NO KEY UPDATE`, scope, map[string]string{"tenant": f.tenant, "key": f.key, "procurement": f.account}[scope]).Scan(&control, &expected)
	operation := uuid.NewString()
	if errors.Is(err, pgx.ErrNoRows) {
		control = uuid.NewString()
		var tenant, key, account any
		if scope == "tenant" || scope == "key" {
			tenant = f.tenant
		}
		if scope == "key" {
			key = f.key
		}
		if scope == "procurement" {
			account = f.account
		}
		f.exec(t, tx, `INSERT INTO periodic_budget_controls(id,scope,tenant_id,key_id,account_id,period_kind,enabled,limit_amount,configuration_operation_id) VALUES($1,$2,$3,$4,$5,'utc_month',$6,$7,$8)`, control, scope, tenant, key, account, enabled, limit, operation)
		f.importLegacy(t, tx, control, scope)
	} else {
		pbTxMust(t, "read existing fixture control", err)
		f.exec(t, tx, `UPDATE periodic_budget_controls SET enabled=$2,limit_amount=$3,configuration_operation_id=CASE WHEN (enabled,limit_amount) IS DISTINCT FROM ($2,$3) THEN $4 ELSE configuration_operation_id END WHERE id=$1`, control, enabled, limit, operation)
	}
	f.exec(t, tx, `INSERT INTO periodic_budget_operations(operation_id,kind,actor,scope,target,resource_id,safe_input) VALUES($1,$2,$3,$4,$5,$6,jsonb_build_object('expected_revision',$7::text,'period_kind','utc_month','enabled',$8::boolean,'limit_amount',$9::text))`, operation, kind, f.user, operationScope, target, control, strconv.FormatInt(expected, 10), enabled, strconv.FormatInt(limit, 10))
	pbTxMust(t, "commit APP configuration/version/receipt/audit", tx.Commit(f.ctx))
	return control
}

func (f *pbTxFixture) importLegacy(t *testing.T, tx pgx.Tx, control, scope string) {
	t.Helper()
	query := `SELECT r.id FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.tenant_id=$1 AND ($2::text='tenant' OR r.key_id=$3) AND v.status IN ('reserved','pending_reconciliation') ORDER BY r.id`
	args := []any{f.tenant, scope, f.key}
	subjectType := "customer_request"
	if scope == "procurement" {
		query = `SELECT attempt_id FROM procurement_reservations WHERE account_id=$1 AND state IN ('prepared','sending','unknown','disputed') ORDER BY attempt_id`
		args = []any{f.account}
		subjectType = "supplier_attempt"
	}
	rows, err := tx.Query(f.ctx, query, args...)
	pbTxMust(t, "read bounded fixture legacy sources", err)
	var ids []string
	for rows.Next() {
		var id string
		pbTxMust(t, "scan legacy source", rows.Scan(&id))
		ids = append(ids, id)
	}
	rows.Close()
	pbTxMust(t, "finish legacy source scan", rows.Err())
	for _, id := range ids {
		allocation := uuid.NewString()
		f.exec(t, tx, `INSERT INTO periodic_budget_allocations(id,control_id,subject_type,subject_id,origin,origin_source_id,upper_amount,baseline,source_spent) VALUES($1,$2,$3,$4,'legacy_import',$4,0,0,0)`, allocation, control, subjectType, id)
		f.exec(t, tx, `INSERT INTO periodic_budget_events(id,control_id,allocation_id,source_kind,source_id) VALUES($1,$2,$3,'legacy_import',$4)`, uuid.NewString(), control, allocation, id)
	}
}

func (f *pbTxFixture) customerRoots(t *testing.T, tx pgx.Tx, request string) {
	t.Helper()
	f.exec(t, tx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, f.tenant)
	f.exec(t, tx, `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, f.tenant, f.user)
	f.exec(t, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, f.user)
	f.exec(t, tx, `SELECT id FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, f.tenant, f.key)
	f.exec(t, tx, `SELECT id FROM models WHERE id=$1 FOR SHARE`, f.model)
	if request != "" {
		f.exec(t, tx, `SELECT id FROM reservations WHERE request_id=$1 FOR UPDATE`, request)
	}
}
func (f *pbTxFixture) walletLocks(t *testing.T, tx pgx.Tx) {
	t.Helper()
	f.exec(t, tx, `INSERT INTO ledger_accounts(tenant_id,kind) VALUES($1,'AVAILABLE'),($1,'HELD'),(NULL,'REVENUE') ON CONFLICT DO NOTHING`, f.tenant)
	rows, err := tx.Query(f.ctx, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 OR (tenant_id IS NULL AND kind='REVENUE') ORDER BY id FOR UPDATE`, f.tenant)
	pbTxMust(t, "lock single wallet in stable order", err)
	for rows.Next() {
		var id string
		pbTxMust(t, "scan wallet locks", rows.Scan(&id))
	}
	rows.Close()
	pbTxMust(t, "finish wallet locks", rows.Err())
}
func (f *pbTxFixture) journal(t *testing.T, tx pgx.Tx, operation, request, kind string, available, held, revenue int64) string {
	t.Helper()
	journal := uuid.NewString()
	f.exec(t, tx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,$4,$5,$6)`, journal, f.tenant, operation, kind, request, f.user)
	for _, line := range []struct {
		kind  string
		delta int64
	}{{"AVAILABLE", available}, {"HELD", held}, {"REVENUE", revenue}} {
		if line.delta == 0 {
			continue
		}
		var account string
		pbTxMust(t, "read journal account", tx.QueryRow(f.ctx, `SELECT id FROM ledger_accounts WHERE kind=$1 AND (tenant_id=$2 OR (tenant_id IS NULL AND kind='REVENUE'))`, line.kind, f.tenant).Scan(&account))
		f.exec(t, tx, `UPDATE ledger_accounts SET balance=balance+$2 WHERE id=$1`, account, line.delta)
		f.exec(t, tx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3)`, journal, account, line.delta)
	}
	return journal
}

// Creates the same complete customer money source that billing Reserve owns;
// no B helper is hidden here so negative tests can exercise each entry point.
func (f *pbTxFixture) createCustomerSource(t *testing.T, tx pgx.Tx) string {
	t.Helper()
	request, reservation := uuid.NewString(), uuid.NewString()
	f.exec(t, tx, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,pricing_group_id,tenant_pricing_revision,retail_priced_at)
 SELECT $1,t.id,$2,$3,$4,$5,1000000,1000000,100,20,'v1','r3','fixture',clock_timestamp()+interval '5 minutes','fixture',false,t.price_group_id,t.pricing_revision,clock_timestamp() FROM tenants t WHERE t.id=$6`, request, f.key, f.user, f.model, f.price, f.tenant)
	f.exec(t, tx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,120,clock_timestamp()+interval '24 hours')`, reservation, f.tenant, request)
	f.journal(t, tx, reservation, request, "reserve", -120, 120, 0)
	f.exec(t, tx, `UPDATE api_keys SET held=held+120 WHERE id=$1`, f.key)
	return request
}
func (f *pbTxFixture) newCustomer(t *testing.T, tx pgx.Tx, locks CustomerLocks) (string, Admission) {
	t.Helper()
	f.walletLocks(t, tx)
	request := f.createCustomerSource(t, tx)
	admission, err := AdmitCustomerTx(f.ctx, tx, locks, request, Amount{Decimal: "120"})
	pbTxMust(t, "admit complete customer source", err)
	pbTxMust(t, "record complete customer reserve", RecordCustomerReserveTx(f.ctx, tx, locks, admission, request))
	return request, admission
}
func (f *pbTxFixture) finishCustomerSource(t *testing.T, tx pgx.Tx, request string, charge int64) string {
	t.Helper()
	var amount int64
	pbTxMust(t, "read original customer amount", tx.QueryRow(f.ctx, `SELECT amount FROM reservations WHERE request_id=$1`, request).Scan(&amount))
	id := uuid.NewString()
	state, reason := "settled", "charged_usage"
	if charge == 0 {
		state, reason = "released", "released_not_sent"
	}
	journal := f.journal(t, tx, id, request, state, amount-charge, -amount, charge)
	f.exec(t, tx, `INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,$4::bigint,$4::bigint,0,$5,$6,$7)`, id, f.tenant, request, charge, state, reason, journal)
	f.exec(t, tx, `UPDATE api_keys SET held=held-$2,spent=spent+$3 WHERE id=$1`, f.key, amount, charge)
	f.exec(t, tx, `UPDATE reservations SET status=$2 WHERE request_id=$1`, request, state)
	f.exec(t, tx, `UPDATE request_records SET billing_status=$2 WHERE id=$1`, request, state)
	return id
}
func (f *pbTxFixture) verifyFinance(t *testing.T) {
	t.Helper()
	if f.historical {
		check, err := funding019.Verify(f.ctx, f.app, "")
		pbTxMust(t, "verify explicitly historical 019 funding and ledger", err)
		if !check.Balanced {
			t.Fatal("historical fixture financial sources/journals/accounts are not balanced")
		}
		return
	}
	check, err := billing.New(f.app).VerifyLedger(f.ctx, billing.Query{})
	pbTxMust(t, "verify complete ledger and funding", err)
	if !check.Balanced {
		t.Fatal("fixture financial sources/journals/accounts are not balanced")
	}
}

func (f *pbTxFixture) snapshot(t *testing.T) string {
	t.Helper()
	var result string
	pbTxMust(t, "read single financial/budget snapshot", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'requests',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM request_records x),'[]'::jsonb),
 'reservations',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM reservations x),'[]'::jsonb),
 'settlements',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM settlements x),'[]'::jsonb),
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM ledger_accounts x),'[]'::jsonb),
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM journals x),'[]'::jsonb),
 'lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM journal_lines x),'[]'::jsonb),
 'keys',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM api_keys x),'[]'::jsonb),
 'controls',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM periodic_budget_controls x),'[]'::jsonb),
 'windows',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM periodic_budget_windows x),'[]'::jsonb),
 'allocations',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM periodic_budget_allocations x),'[]'::jsonb),
 'events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM periodic_budget_events x),'[]'::jsonb))::text`).Scan(&result))
	return result
}

// Keep the imported encoding contract checked without logging arbitrary rows.
func pbTxJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	pbTxMust(t, "encode test DTO", err)
	return b
}
