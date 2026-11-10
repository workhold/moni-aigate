package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestPaymentRoleStaticReviewedFunctionBaseline(t *testing.T) {
	// Later reviewed definitions replace earlier bodies by the same name. These
	// expected bytes come only from source, never from the database under test.
	functions := make(map[string]string)
	definitions := regexp.MustCompile(`(?s)CREATE(?: OR REPLACE)? FUNCTION ([a-z_]+)\([^$]*?\$\$(.*?)\$\$;`)
	for _, source := range []struct{ file, digest string }{
		{"021_payment_sandbox.sql", paymentRoleMigrationSHA256},
		{"023_wallet_sources.sql", walletSourcesRoleMigrationSHA256},
	} {
		body, e := migrations.Files.ReadFile(source.file)
		if e != nil {
			t.Fatal("reviewed migration unavailable")
		}
		if fmt.Sprintf("%x", sha256.Sum256(body)) != source.digest {
			t.Fatal("reviewed payment/wallet function baseline requires explicit review after migration change")
		}
		if source.file == "023_wallet_sources.sql" {
			// CREATE OR REPLACE does not retain SET configuration. The current
			// policy must cover the replaced dispatcher as well as new wallet_*.
			policy := regexp.MustCompile(`(?s)DO \$\$ DECLARE f RECORD; BEGIN.*?END \$\$;`).Find(body)
			for _, required := range []string{"left(p.proname,7)='wallet_'", "p.proname='funding_journal_valid'", "SECURITY INVOKER", "SET search_path TO %I,pg_catalog,pg_temp", "FROM PUBLIC"} {
				if !strings.Contains(string(policy), required) {
					t.Fatal("replaced funding dispatcher must restore the fixed invoker/search-path/PUBLIC policy")
				}
			}
		}
		for _, match := range definitions.FindAllSubmatch(body, -1) {
			functions[string(match[1])] = fmt.Sprintf("%x", sha256.Sum256(match[2]))
		}
	}
	// 024 is the unissued current baseline. It deliberately replaces the
	// wallet event dispatcher to validate media lot edges while preserving the
	// same payment-worker role contract.
	current, e := migrations.Files.ReadFile("024_unified_text_metering.sql")
	if e != nil {
		t.Fatal("current media wallet override unavailable")
	}
	currentWallet := regexp.MustCompile(`(?s)CREATE OR REPLACE FUNCTION wallet_event_valid\([^$]*?\$\$(.*?)\$\$;`).FindSubmatch(current)
	if len(currentWallet) != 2 {
		t.Fatal("current media wallet dispatcher unavailable")
	}
	functions["wallet_event_valid"] = fmt.Sprintf("%x", sha256.Sum256(currentWallet[1]))
	currentFunding := regexp.MustCompile(`(?s)CREATE FUNCTION payment_funding_journal_valid\([^$]*?\$\$(.*?)\$\$;`).FindSubmatch(current)
	if len(currentFunding) != 2 {
		t.Fatal("current funding validator unavailable")
	}
	functions["payment_funding_journal_valid"] = fmt.Sprintf("%x", sha256.Sum256(currentFunding[1]))
	currentDispatcher := regexp.MustCompile(`(?s)CREATE OR REPLACE FUNCTION funding_journal_valid\([^$]*?\$\$(.*?)\$\$;`).FindSubmatch(current)
	if len(currentDispatcher) != 2 {
		t.Fatal("current funding dispatcher unavailable")
	}
	functions["funding_journal_valid"] = fmt.Sprintf("%x", sha256.Sum256(currentDispatcher[1]))
	delete(functions, "payment_legacy_funding_journal_valid")
	if len(paymentFunctionSpecs) != 55 || len(functions) != len(paymentFunctionSpecs) {
		t.Fatal("current payment/wallet function allowlist incomplete")
	}
	worker := 0
	for _, f := range paymentFunctionSpecs {
		if f.worker {
			worker++
		}
		if len(f.bodySHA) != 64 || functions[f.name] != f.bodySHA {
			t.Fatal("function body digest differs from reviewed source")
		}
	}
	if worker != 38 || paymentFunctionSpecs["payment_funding_journal_valid"].worker || paymentFunctionSpecs["payment_check_human"].worker || paymentFunctionSpecs["payment_audit_guard"].worker || paymentFunctionSpecs["payment_audit_complete"].worker {
		t.Fatal("worker function allowlist differs from frozen matrix")
	}
	for _, name := range []string{"wallet_opening_journal_valid", "wallet_reservation_fifo_guard", "wallet_reservation_valid", "wallet_settlement_valid", "wallet_refund_valid"} {
		if paymentFunctionSpecs[name].worker {
			t.Fatal("worker may not execute opening or customer consumption validators")
		}
	}
	for _, role := range []PaymentDatabaseRole{PaymentApplicationRole, PaymentWorkerRole, PaymentProjectionRole, "invalid"} {
		if e := VerifyPaymentRole(context.Background(), nil, role); e != ErrPaymentRolePrivileges {
			t.Fatal("nil role verification did not return safe sentinel")
		}
	}
}

type paymentRoleFixture struct {
	ctx                                         context.Context
	owner, app, worker, projection              *pgxpool.Pool
	schema, appRole, workerRole, projectionRole string
}

func paymentRoleMust(t *testing.T, label string, e error) {
	t.Helper()
	if e != nil {
		t.Fatalf("%s failed", label)
	}
}

func newPaymentRoleFixture(t *testing.T, historical bool) *paymentRoleFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for actual three-LOGIN payment role tests")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	paymentRoleMust(t, "dedicated configuration", e)
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("payment role tests require dedicated local moni_test port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	paymentRoleMust(t, "owner connection", e)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	f := &paymentRoleFixture{ctx: ctx, schema: "payment_roles_" + suffix, appRole: "pay_app_" + suffix, workerRole: "pay_worker_" + suffix, projectionRole: "pay_projection_" + suffix}
	t.Cleanup(func() {
		for _, p := range []*pgxpool.Pool{f.app, f.worker, f.projection, f.owner} {
			if p != nil {
				p.Close()
			}
		}
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("payment schema cleanup failed")
		}
		for _, role := range []string{f.appRole, f.workerRole, f.projectionRole} {
			q := pgx.Identifier{role}.Sanitize()
			if _, e := admin.Exec(cleanup, "DROP OWNED BY "+q); e != nil {
				t.Error("payment ACL cleanup failed")
			}
			if _, e := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+q); e != nil {
				t.Error("payment LOGIN cleanup failed")
			}
		}
		admin.Close()
		cancel()
	})
	_, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	paymentRoleMust(t, "schema creation", e)
	secrets := []string{strings.ReplaceAll(uuid.NewString(), "-", ""), strings.ReplaceAll(uuid.NewString(), "-", ""), strings.ReplaceAll(uuid.NewString(), "-", "")}
	roles := []string{f.appRole, f.workerRole, f.projectionRole}
	for i, role := range roles {
		_, e = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+secrets[i]+"'")
		paymentRoleMust(t, "LOGIN creation", e)
	}
	cfg.MaxConns = 3
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	f.owner, e = pgxpool.NewWithConfig(ctx, cfg.Copy())
	paymentRoleMust(t, "schema owner", e)
	if historical {
		e = migrations.ApplyThrough(ctx, f.owner, "020_periodic_budgets.sql")
	} else {
		e = migrations.Apply(ctx, f.owner)
	}
	paymentRoleMust(t, "schema installation", e)
	if err := ProvisionFor(ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
		paymentRoleDiagnostics(t, f)
		paymentRoleMust(t, "initial ProvisionFor", err)
	}
	for i, role := range roles {
		login := cfg.Copy()
		login.ConnConfig.User = role
		login.ConnConfig.Password = secrets[i]
		p, e := pgxpool.NewWithConfig(ctx, login)
		paymentRoleMust(t, "restricted connection", e)
		if i == 0 {
			f.app = p
		} else if i == 1 {
			f.worker = p
		} else {
			f.projection = p
		}
		var current, session string
		paymentRoleMust(t, "actual LOGIN identity", p.QueryRow(ctx, "SELECT current_user,session_user").Scan(&current, &session))
		if current != role || session != role {
			t.Fatal("restricted fixture did not use actual LOGIN")
		}
	}
	return f
}
func (f *paymentRoleFixture) table(name string) string {
	return pgx.Identifier{f.schema, name}.Sanitize()
}
func (f *paymentRoleFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, e := f.owner.Exec(f.ctx, q, args...)
	paymentRoleMust(t, "owner-only fixture mutation", e)
}
func (f *paymentRoleFixture) marker(t *testing.T) {
	t.Helper()
	f.exec(t, "INSERT INTO "+f.table("payment_sandbox_environment")+"(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT $1,'roles-merchant','roles-payee',d.oid,n.oid,$2,$3,$4,$5 FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_namespace n WHERE d.datname=current_database() AND n.nspname=$6", uuid.NewString(), strings.Repeat("1", 64), f.appRole, f.workerRole, f.projectionRole, f.schema)
	paymentRoleMust(t, "explicit worker provisioning", ProvisionPaymentWorkerFor(f.ctx, f.owner, f.schema, f.appRole, f.workerRole, f.projectionRole))
}

func TestPaymentRolesImplicitDatabaseOwnerSchema(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.exec(t, "ALTER SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" OWNER TO pg_database_owner")
	paymentRoleMust(t, "implicit database owner provisioning", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
	f.valid(t, false)
	// Objects in such a namespace still have one precise owner. A different
	// NOLOGIN role cannot qualify merely because runtimes cannot reach it.
	group := f.group(t)
	f.exec(t, "ALTER TABLE "+f.table("payment_policy")+" OWNER TO "+pgx.Identifier{group}.Sanitize())
	f.rejected(t, PaymentApplicationRole)
	f.exec(t, "ALTER TABLE "+f.table("payment_policy")+" OWNER TO "+pgx.Identifier{f.owner.Config().ConnConfig.User}.Sanitize())
	fn := f.table("payment_ref") + "(text)"
	f.exec(t, "ALTER FUNCTION "+fn+" OWNER TO "+pgx.Identifier{group}.Sanitize())
	f.rejected(t, PaymentApplicationRole)
	f.exec(t, "ALTER FUNCTION "+fn+" OWNER TO "+pgx.Identifier{f.owner.Config().ConnConfig.User}.Sanitize())
	f.valid(t, false)
}
func (f *paymentRoleFixture) valid(t *testing.T, marker bool) {
	t.Helper()
	paymentRoleMust(t, "actual application verification", VerifyPaymentRole(f.ctx, f.app, PaymentApplicationRole))
	paymentRoleMust(t, "actual projector verification", VerifyPaymentRole(f.ctx, f.projection, PaymentProjectionRole))
	worker := ""
	if marker {
		worker = f.workerRole
		paymentRoleMust(t, "actual worker verification", VerifyPaymentRole(f.ctx, f.worker, PaymentWorkerRole))
		paymentRoleMust(t, "worker complete migration verification", migrations.Verify(f.ctx, f.worker))
	}
	paymentRoleMust(t, "offline named-role verification", VerifyPaymentRolesFor(f.ctx, f.owner, f.schema, f.appRole, worker, f.projectionRole))
}
func (f *paymentRoleFixture) rejected(t *testing.T, role PaymentDatabaseRole) {
	t.Helper()
	p := f.app
	if role == PaymentWorkerRole {
		p = f.worker
	}
	if role == PaymentProjectionRole {
		p = f.projection
	}
	if e := VerifyPaymentRole(f.ctx, p, role); e != ErrPaymentRolePrivileges {
		t.Fatal("runtime accepted invalid payment boundary or leaked unsafe error")
	}
	if e := VerifyPaymentRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.workerRole, f.projectionRole); e != ErrPaymentRolePrivileges {
		t.Fatal("offline accepted invalid payment boundary or leaked unsafe error")
	}
}
func (f *paymentRoleFixture) group(t *testing.T) string {
	t.Helper()
	name := "pay_group_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	q := pgx.Identifier{name}.Sanitize()
	f.exec(t, "CREATE ROLE "+q+" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEROLE NOCREATEDB NOREPLICATION NOBYPASSRLS")
	t.Cleanup(func() { f.exec(t, "DROP OWNED BY "+q+" CASCADE"); f.exec(t, "DROP ROLE "+q) })
	return name
}

func TestPaymentRolesEmptyOffActualLoginsAndRepeatedProvision(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.valid(t, false)
	if e := VerifyPaymentRole(f.ctx, f.worker, PaymentWorkerRole); e != ErrPaymentRolePrivileges {
		t.Fatal("worker accepted absent marker")
	}
	if e := ProvisionPaymentWorkerFor(f.ctx, f.owner, f.schema, f.appRole, f.workerRole, f.projectionRole); e != ErrPaymentRolePrivileges {
		t.Fatal("worker provision accepted absent marker")
	}
	if e := VerifyPaymentRole(f.ctx, f.owner, PaymentApplicationRole); e != ErrPaymentRolePrivileges {
		t.Fatal("owner qualified as runtime")
	}
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, e := c.Exec(ctx, "SET ROLE "+pgx.Identifier{f.appRole}.Sanitize())
		return e
	}
	impersonated, e := pgxpool.NewWithConfig(f.ctx, cfg)
	paymentRoleMust(t, "privileged wrapper", e)
	defer impersonated.Close()
	if e = VerifyPaymentRole(f.ctx, impersonated, PaymentApplicationRole); e != ErrPaymentRolePrivileges {
		t.Fatal("owner SET ROLE qualified as runtime")
	}
	qa := pgx.Identifier{f.appRole}.Sanitize()
	qp := pgx.Identifier{f.projectionRole}.Sanitize()
	f.exec(t, "GRANT UPDATE(amount) ON "+f.table("payment_captures")+" TO "+qa+"; GRANT SELECT(id) ON "+f.table("payment_inbox")+" TO "+qp)
	if e := VerifyPaymentRole(f.ctx, f.app, PaymentApplicationRole); e != ErrPaymentRolePrivileges {
		t.Fatal("unrestricted column ACL qualified")
	}
	for i := 0; i < 2; i++ {
		paymentRoleMust(t, "repeat empty-marker provisioning", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		f.valid(t, false)
	}
	f.marker(t)
	f.valid(t, true)
	if _, e := f.projection.Exec(f.ctx, "SELECT * FROM payment_sandbox_environment"); e == nil {
		t.Fatal("catalog-only projector verification exposed the marker")
	}
	for i := 0; i < 2; i++ {
		paymentRoleMust(t, "repeat marked provisioning", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		f.valid(t, true)
	}
	var ok bool
	paymentRoleMust(t, "legacy PUBLIC invoker remains usable", f.worker.QueryRow(f.ctx, "SELECT funding_uuid($1)", uuid.NewString()).Scan(&ok))
	if !ok {
		t.Fatal("legacy public invoker was unexpectedly revoked")
	}
	for _, q := range []string{"SELECT * FROM users LIMIT 0", "SELECT name FROM tenants LIMIT 0", "SELECT * FROM vouchers LIMIT 0", "SELECT * FROM periodic_budget_controls LIMIT 0", "SELECT * FROM payment_policy LIMIT 0", "SELECT payment_funding_journal_valid('missing')", "SELECT payment_check_human('missing')"} {
		if _, e := f.worker.Exec(f.ctx, q); e == nil {
			t.Fatal("worker exceeded frozen boundary")
		}
	}
	_, e = f.worker.Exec(f.ctx, "SELECT id FROM tenants LIMIT 0")
	paymentRoleMust(t, "tenant lock-only SELECT column", e)
	tenant := uuid.NewString()
	f.exec(t, "INSERT INTO tenants(id,name) VALUES($1,'roles-tenant')", tenant)
	if _, e = f.worker.Exec(f.ctx, "UPDATE tenants SET id=id WHERE id=$1", tenant); e == nil {
		t.Fatal("worker lock permission enabled actual tenant mutation")
	}
}

func TestPaymentRolesColumnPublicInheritanceSetAndAdminEscalation(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	qw, qa, qp := pgx.Identifier{f.workerRole}.Sanitize(), pgx.Identifier{f.appRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize()
	tests := []struct {
		name, grant, revoke string
		role                PaymentDatabaseRole
	}{
		{"worker forbidden column", "GRANT SELECT(name) ON " + f.table("tenants") + " TO " + qw, "REVOKE SELECT(name) ON " + f.table("tenants") + " FROM " + qw, PaymentWorkerRole},
		{"worker forbidden legacy table", "GRANT SELECT ON " + f.table("users") + " TO " + qw, "REVOKE SELECT ON " + f.table("users") + " FROM " + qw, PaymentWorkerRole},
		{"PUBLIC column", "GRANT SELECT(name) ON " + f.table("tenants") + " TO PUBLIC", "REVOKE SELECT(name) ON " + f.table("tenants") + " FROM PUBLIC", PaymentWorkerRole},
		{"PUBLIC allowed B table", "GRANT SELECT ON " + f.table("payment_orders") + " TO PUBLIC", "REVOKE SELECT ON " + f.table("payment_orders") + " FROM PUBLIC", PaymentWorkerRole},
		{"table grant option", "GRANT SELECT ON " + f.table("journals") + " TO " + qw + " WITH GRANT OPTION", "REVOKE GRANT OPTION FOR SELECT ON " + f.table("journals") + " FROM " + qw, PaymentWorkerRole},
		{"column grant option", "GRANT SELECT(id) ON " + f.table("tenants") + " TO " + qw + " WITH GRANT OPTION", "REVOKE GRANT OPTION FOR SELECT(id) ON " + f.table("tenants") + " FROM " + qw, PaymentWorkerRole},
		{"projector B column", "GRANT SELECT(id) ON " + f.table("payment_orders") + " TO " + qp, "REVOKE SELECT(id) ON " + f.table("payment_orders") + " FROM " + qp, PaymentProjectionRole},
		{"app worker-only query conflict", "GRANT SELECT ON " + f.table("payment_query_conflicts") + " TO " + qa, "REVOKE SELECT ON " + f.table("payment_query_conflicts") + " FROM " + qa, PaymentApplicationRole},
		{"schema CREATE", "GRANT CREATE ON SCHEMA " + pgx.Identifier{f.schema}.Sanitize() + " TO " + qw, "REVOKE CREATE ON SCHEMA " + pgx.Identifier{f.schema}.Sanitize() + " FROM " + qw, PaymentWorkerRole},
		{"elevated role", "ALTER ROLE " + qw + " CREATEDB", "ALTER ROLE " + qw + " NOCREATEDB", PaymentWorkerRole},
		{"cross identity SET", "GRANT " + qa + " TO " + qw + " WITH INHERIT FALSE, SET TRUE", "REVOKE " + qa + " FROM " + qw, PaymentWorkerRole},
		{"required worker metadata revoked", "REVOKE SELECT ON " + f.table("schema_migrations") + " FROM " + qw, "GRANT SELECT ON " + f.table("schema_migrations") + " TO " + qw, PaymentWorkerRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { f.exec(t, tt.grant); defer f.exec(t, tt.revoke); f.rejected(t, tt.role) })
	}
	f.valid(t, true)
	group := f.group(t)
	qg := pgx.Identifier{group}.Sanitize()
	f.exec(t, "GRANT SELECT(name) ON "+f.table("tenants")+" TO "+qg)
	for _, opts := range []string{"INHERIT TRUE, SET FALSE", "INHERIT FALSE, SET TRUE", "INHERIT FALSE, SET FALSE, ADMIN TRUE"} {
		f.exec(t, "GRANT "+qg+" TO "+qw+" WITH "+opts)
		f.rejected(t, PaymentWorkerRole)
		f.exec(t, "REVOKE "+qg+" FROM "+qw)
	}
	f.exec(t, "REVOKE SELECT(name) ON "+f.table("tenants")+" FROM "+qg)
	f.exec(t, "GRANT SELECT(id) ON "+f.table("tenants")+" TO "+qg)
	f.exec(t, "GRANT "+qg+" TO "+qw+" WITH INHERIT TRUE, SET TRUE")
	f.valid(t, true)
	f.exec(t, "REVOKE "+qg+" FROM "+qw)
	f.valid(t, true)
	adminGroup, finalGroup := f.group(t), f.group(t)
	qAdmin, qFinal := pgx.Identifier{adminGroup}.Sanitize(), pgx.Identifier{finalGroup}.Sanitize()
	f.exec(t, "GRANT SELECT ON "+f.table("users")+" TO "+qFinal)
	f.exec(t, "GRANT "+qFinal+" TO "+qAdmin+" WITH INHERIT FALSE, SET TRUE")
	f.exec(t, "GRANT "+qAdmin+" TO "+qg+" WITH INHERIT FALSE, SET FALSE, ADMIN TRUE")
	f.exec(t, "GRANT "+qg+" TO "+qw+" WITH INHERIT TRUE, SET FALSE")
	f.rejected(t, PaymentWorkerRole)
	f.exec(t, "REVOKE "+qg+" FROM "+qw)
	f.valid(t, true)
}

func TestPaymentRolesFunctionDefinitionAndExecutionBoundary(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	qw, qp := pgx.Identifier{f.workerRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize()
	fn := f.table("payment_ref") + "(text)"
	legacy := f.table("funding_uuid") + "(text)"
	tests := []struct {
		name, grant, revoke string
		role                PaymentDatabaseRole
	}{
		{"worker current funding predicate", "GRANT EXECUTE ON FUNCTION " + f.table("payment_funding_journal_valid") + "(text) TO " + qw, "REVOKE EXECUTE ON FUNCTION " + f.table("payment_funding_journal_valid") + "(text) FROM " + qw, PaymentWorkerRole},
		{"projector execute", "GRANT EXECUTE ON FUNCTION " + fn + " TO " + qp, "REVOKE EXECUTE ON FUNCTION " + fn + " FROM " + qp, PaymentProjectionRole},
		{"PUBLIC execute", "GRANT EXECUTE ON FUNCTION " + fn + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + fn + " FROM PUBLIC", PaymentApplicationRole},
		{"function grant option", "GRANT EXECUTE ON FUNCTION " + legacy + " TO " + qw + " WITH GRANT OPTION", "REVOKE ALL ON FUNCTION " + legacy + " FROM " + qw, PaymentWorkerRole},
		{"legacy SECURITY DEFINER", "ALTER FUNCTION " + legacy + " SECURITY DEFINER", "ALTER FUNCTION " + legacy + " SECURITY INVOKER", PaymentWorkerRole},
		{"wrong search_path", "ALTER FUNCTION " + fn + " SET search_path TO pg_catalog", "ALTER FUNCTION " + fn + " SET search_path TO " + pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp", PaymentApplicationRole},
		{"wrong volatility", "ALTER FUNCTION " + fn + " VOLATILE", "ALTER FUNCTION " + fn + " IMMUTABLE", PaymentApplicationRole},
		{"wrong strictness", "ALTER FUNCTION " + fn + " STRICT", "ALTER FUNCTION " + fn + " CALLED ON NULL INPUT", PaymentApplicationRole},
		{"unknown payment function", "CREATE FUNCTION " + f.table("payment_unknown") + "() RETURNS boolean LANGUAGE sql AS 'SELECT true'", "DROP FUNCTION " + f.table("payment_unknown") + "()", PaymentApplicationRole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { f.exec(t, tt.grant); defer f.exec(t, tt.revoke); f.rejected(t, tt.role) })
	}
	f.valid(t, true)
	body, e := migrations.Files.ReadFile("021_payment_sandbox.sql")
	paymentRoleMust(t, "reviewed static SQL", e)
	matches := regexp.MustCompile(`(?s)CREATE FUNCTION payment_ref\(.*?\$\$;`).FindString(string(body))
	if matches == "" {
		t.Fatal("reviewed function definition unavailable")
	}
	f.exec(t, "CREATE OR REPLACE FUNCTION "+f.table("payment_ref")+"(v text) RETURNS boolean LANGUAGE sql IMMUTABLE AS 'SELECT true'")
	f.rejected(t, PaymentApplicationRole)
	if e = ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); !errors.Is(e, ErrPaymentRolePrivileges) {
		t.Fatal("provision trusted replaced function body")
	}
	f.exec(t, strings.Replace(matches, "CREATE FUNCTION payment_ref(", "CREATE OR REPLACE FUNCTION "+f.table("payment_ref")+"(", 1))
	f.exec(t, "ALTER FUNCTION "+fn+" SET search_path TO "+pgx.Identifier{f.schema}.Sanitize()+",pg_catalog,pg_temp")
	f.valid(t, true)
}

func TestPaymentRolesMarkerIdentityAndHistoricalPartialSchema(t *testing.T) {
	t.Run("marker identity", func(t *testing.T) {
		f := newPaymentRoleFixture(t, false)
		f.marker(t)
		f.valid(t, true)
		if e := VerifyPaymentRolesFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole, f.workerRole); e != ErrPaymentRolePrivileges {
			t.Fatal("swapped pinned identities qualified")
		}
		qw := pgx.Identifier{f.workerRole}.Sanitize()
		f.exec(t, "ALTER ROLE "+qw+" NOLOGIN")
		f.rejected(t, PaymentWorkerRole)
		f.exec(t, "ALTER ROLE "+qw+" LOGIN")
		f.valid(t, true)
		f.exec(t, "ALTER TABLE "+f.table("payment_sandbox_environment")+" DISABLE TRIGGER payment_environment_immutable")
		f.exec(t, "UPDATE "+f.table("payment_sandbox_environment")+" SET database_oid=0")
		f.rejected(t, PaymentWorkerRole)
		f.exec(t, "UPDATE "+f.table("payment_sandbox_environment")+" SET database_oid=(SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()),schema_oid=0")
		f.rejected(t, PaymentWorkerRole)
		f.exec(t, "UPDATE "+f.table("payment_sandbox_environment")+" SET schema_oid=(SELECT oid FROM pg_catalog.pg_namespace WHERE nspname=$1)", f.schema)
		f.valid(t, true)
		group := f.group(t)
		f.exec(t, "ALTER TABLE "+f.table("payment_sandbox_environment")+" OWNER TO "+pgx.Identifier{group}.Sanitize())
		f.rejected(t, PaymentWorkerRole)
		f.exec(t, "ALTER TABLE "+f.table("payment_sandbox_environment")+" OWNER TO "+pgx.Identifier{f.owner.Config().ConnConfig.User}.Sanitize())
		f.valid(t, true)
	})
	t.Run("historical zero tables", func(t *testing.T) {
		f := newPaymentRoleFixture(t, true)
		paymentRoleMust(t, "historical repeat provision", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		f.exec(t, "CREATE TABLE "+f.table("payment_policy")+"(id text)")
		if e := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); e != ErrPaymentRolePrivileges {
			t.Fatal("partial B schema qualified as historical prefix")
		}
	})
	t.Run("historical partial view", func(t *testing.T) {
		f := newPaymentRoleFixture(t, true)
		f.exec(t, "CREATE VIEW "+f.table("payment_policy")+" AS SELECT 'x'::text id")
		if e := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); e != ErrPaymentRolePrivileges {
			t.Fatal("partial view qualified as absent B schema")
		}
	})
}
