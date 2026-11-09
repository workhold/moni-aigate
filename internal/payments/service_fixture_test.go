package payments

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/migrations"
)

// SQL/domain fixture only. Its real LOGINs prove transaction/ACL behavior;
// private runtime assembly deliberately does not claim NewService's complete
// 022 eight-role, file, endpoint, keyset and attestation startup proof.
type serviceFixture struct {
	ctx                                                                context.Context
	owner, app, worker, projector                                      *pgxpool.Pool
	env                                                                p.Environment
	tenant, user, admin, viewer, member, finance, sku, version, schema string
	keyring                                                            *p.Keyring
	appOptions, workerOptions                                          Options
	service                                                            *Service
}

func paymentServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated payment domain PostgreSQL tests")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("dedicated payment test database required")
	}
	ctx := context.Background()
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal("open dedicated test database")
	}
	f := &serviceFixture{ctx: ctx, tenant: uuid.NewString(), user: uuid.NewString(), admin: uuid.NewString(), viewer: uuid.NewString(), member: uuid.NewString(), finance: uuid.NewString(), sku: uuid.NewString(), schema: "b3svc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16], env: p.Environment{ID: uuid.NewString(), MerchantID: "service:qa", PayeeLabel: "service fixture"}}
	var roles []string
	var pools []*pgxpool.Pool
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE")
		for _, role := range roles {
			if _, e := admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("cleanup role objects", e)
			}
			if _, e := admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("cleanup role", e)
			}
		}
		admin.Close()
	})
	serviceExec(t, admin, ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	ownerCfg := cfg.Copy()
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp"
	f.owner, e = pgxpool.NewWithConfig(ctx, ownerCfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = migrations.ApplyCurrent(ctx, f.owner); e != nil {
		t.Fatal("apply complete payment fixture migrations", e)
	}
	for _, suffix := range []string{"app", "worker", "projector"} {
		role := f.schema + "_" + suffix
		secret := make([]byte, 32)
		if _, e = rand.Read(secret); e != nil {
			t.Fatal(e)
		}
		password := hex.EncodeToString(secret)
		serviceExec(t, admin, ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'")
		roles = append(roles, role)
		loginCfg := ownerCfg.Copy()
		loginCfg.ConnConfig.User = role
		loginCfg.ConnConfig.Password = password
		loginCfg.MaxConns = 12
		pool, e := pgxpool.NewWithConfig(ctx, loginCfg)
		if e != nil {
			t.Fatal(e)
		}
		pools = append(pools, pool)
	}
	f.app, f.worker, f.projector = pools[0], pools[1], pools[2]
	if e = operations.ProvisionFor(ctx, f.owner, f.schema, roles[0], roles[2]); e != nil {
		t.Fatal("provision domain app/projector", e)
	}
	serviceExec(t, f.owner, ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT $1,$2,$3,(SELECT oid FROM pg_database WHERE datname=current_database()),current_schema()::regnamespace::oid,$4,$5,$6,$7`, f.env.ID, f.env.MerchantID, f.env.PayeeLabel, strings.Repeat("a", 64), roles[0], roles[1], roles[2])
	if e = operations.ProvisionPaymentWorkerFor(ctx, f.owner, f.schema, roles[0], roles[1], roles[2]); e != nil {
		t.Fatal("provision domain worker", e)
	}
	for i, kind := range []operations.PaymentDatabaseRole{operations.PaymentApplicationRole, operations.PaymentWorkerRole, operations.PaymentProjectionRole} {
		if e = operations.VerifyPaymentRole(ctx, pools[i], kind); e != nil {
			t.Fatal("actual domain LOGIN ACL", e)
		}
	}
	serviceExec(t, f.app, ctx, `INSERT INTO tenants(id,name) VALUES($1,'service fixture')`, f.tenant)
	for _, u := range []struct{ id, role string }{{f.admin, "admin"}, {f.user, "none"}, {f.viewer, "none"}, {f.member, "none"}, {f.finance, "finance_operator"}} {
		serviceExec(t, f.app, ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, u.id, u.id+"@test.invalid", u.role)
	}
	for _, u := range []struct{ id, role string }{{f.user, "owner"}, {f.viewer, "billing_viewer"}, {f.member, "member"}} {
		serviceExec(t, f.app, ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3)`, f.tenant, u.id, u.role)
	}
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.keyring, e = p.NewKeyring("v1", []p.KeySpec{{ID: "v1", Key: key, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}})
	if e != nil {
		t.Fatal(e)
	}
	lifecycle := Lifecycle{Mode: ModeSandbox, Guard: func(context.Context) error { return nil }}
	f.appOptions = Options{valid: true, bound: true, component: applicationComponent, proof: runtimeProof{EnvironmentID: f.env.ID, MerchantID: f.env.MerchantID, PayeeLabel: f.env.PayeeLabel}, keyring: f.keyring, lifecycle: lifecycle}
	f.workerOptions = f.appOptions
	f.workerOptions.component = workerComponent
	f.service = &Service{pool: f.app, runtime: &paymentRuntime{pool: f.app, options: f.appOptions}}
	published, e := f.service.PublishSKU(ctx, PublishSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: f.sku, ExpectedRevision: 0, DisplayName: "QA credit", Amount: 100})
	if e != nil || !published.Created || published.Receipt.ResourceID == nil {
		t.Fatal("publish service fixture SKU", e)
	}
	f.version = *published.Receipt.ResourceID
	activated, e := f.service.ActivateSKU(ctx, ActivateSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: f.sku, ExpectedRevision: 1, VersionID: &f.version, Enabled: true})
	if e != nil || activated.Status != "accepted" {
		t.Fatal("activate service fixture SKU", e)
	}
	policy, e := f.service.UpdatePolicy(ctx, UpdatePolicyInput{ActorID: f.admin, ExpectedRevision: 1, Enabled: true})
	if e != nil || !policy.EffectiveEnabled {
		t.Fatal("enable service fixture policy", e)
	}
	return f
}
func serviceExec(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, e := db.Exec(ctx, sql, args...); e != nil {
		t.Fatal("fixture SQL", e)
	}
}
func (f *serviceFixture) order(t *testing.T) OrderView {
	t.Helper()
	result, e := f.service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if e != nil || !result.Created || result.Order == nil {
		t.Fatal("service order", e)
	}
	return *result.Order
}
func serviceCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || err.Error() != want {
		t.Fatalf("safe error got %v want %s", err, want)
	}
}
