package payments

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This fixture upgrades the real M/Q QA environment, including retained A
// funding, rather than substituting empty peer namespaces for their proof.
// Only the owner installs the new immutable proof; runtimes use actual LOGINs.
type paymentRuntimeFixture struct {
	ctx                                            context.Context
	qa                                             *paymentsandboxqa.Environment
	admin, owner, app, projector                   *pgxpool.Pool
	a, w                                           Options
	aPath, wPath, appDSN, projectorDSN, workerRole string
}

func runtimeRandom(t *testing.T) (v [32]byte) {
	t.Helper()
	if _, e := rand.Read(v[:]); e != nil {
		t.Fatal("runtime fixture entropy")
	}
	return v
}
func runtimePGFixture(t *testing.T, seed bool) *paymentRuntimeFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for dedicated payment runtime PostgreSQL tests")
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("dedicated payment runtime test database required")
	}
	ctx := context.Background()
	qa, e := paymentsandboxqa.Create(ctx, dsn, t.TempDir())
	if e != nil {
		t.Fatal("create full M/Q environment", e)
	}
	f := &paymentRuntimeFixture{ctx: ctx, qa: qa}
	var pools []*pgxpool.Pool
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := qa.Close(cleanup); e != nil {
			t.Error("runtime QA cleanup", e)
		}
		if f.workerRole != "" && f.admin != nil {
			if _, e = f.admin.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{f.workerRole}.Sanitize()); e != nil {
				t.Error("runtime worker cleanup")
			}
		}
		if f.admin != nil {
			f.admin.Close()
		}
	})
	f.admin, e = pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal("open runtime fixture admin")
	}
	f.owner, e = qa.OpenPool(ctx, "app_owner")
	if e != nil {
		t.Fatal(e)
	}
	pools = append(pools, f.owner)
	if e = migrations.Apply(ctx, f.owner); e != nil {
		t.Fatal("upgrade actual QA APP to current migrations", e)
	}
	// Reapply the isolated QA namespace boundary after 020 creates new public
	// invoker functions, then let ProvisionFor restore the exact payment matrix.
	serviceExec(t, f.owner, ctx, "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA "+pgx.Identifier{qa.Schemas["app"]}.Sanitize()+" FROM PUBLIC")
	serviceExec(t, f.owner, ctx, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "+pgx.Identifier{qa.Schemas["app"]}.Sanitize()+" TO "+pgx.Identifier{qa.Roles["app_runtime"]}.Sanitize()+","+pgx.Identifier{qa.Roles["app_projector"]}.Sanitize())
	if e = operations.ProvisionFor(ctx, f.owner, qa.Schemas["app"], qa.Roles["app_runtime"], qa.Roles["app_projector"]); e != nil {
		t.Fatal("provision current APP/projector", e)
	}
	oldApp, e := qa.OpenPool(ctx, "app")
	if e != nil {
		t.Fatal(e)
	}
	pools = append(pools, oldApp)
	oldProjection, e := qa.OpenPool(ctx, "projector")
	if e != nil {
		t.Fatal(e)
	}
	pools = append(pools, oldProjection)
	f.app = oldApp
	f.projector = oldProjection
	makeDSN := func(pool *pgxpool.Pool) string {
		c := pool.Config().ConnConfig
		u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(c.User, c.Password), RawQuery: "sslmode=disable"}
		return u.String()
	}
	f.appDSN = makeDSN(oldApp)
	f.projectorDSN = makeDSN(oldProjection)
	if !seed {
		return f
	}
	raw, e := os.ReadFile(qa.MockManifestPath)
	if e != nil {
		t.Fatal("read existing QA mock manifest")
	}
	var old struct {
		EnvironmentID string             `json:"environment_id"`
		MerchantID    string             `json:"merchant_id"`
		PayeeLabel    string             `json:"payee_label"`
		Database      runtimeDatabasePin `json:"database"`
		App           runtimeSchemaPin   `json:"app"`
		Mock          runtimeSchemaPin   `json:"mock"`
		Receiver      runtimeSchemaPin   `json:"receiver"`
		AppProjector  runtimeRolePin     `json:"app_projector"`
		Keys          []struct {
			ID        string `json:"id"`
			Digest    string `json:"digest"`
			Material  string `json:"material_base64_url"`
			NotBefore string `json:"not_before"`
			NotAfter  string `json:"not_after"`
		} `json:"keys"`
	}
	if json.Unmarshal(raw, &old) != nil {
		t.Fatal("decode existing QA manifest")
	}
	suffix := strings.TrimPrefix(qa.Schemas["app"], "payment_sandbox_")
	f.workerRole = "pm_app_worker_" + suffix
	passwordMaterial := runtimeRandom(t)
	password := base64.RawURLEncoding.EncodeToString(passwordMaterial[:])
	serviceExec(t, f.admin, ctx, "CREATE ROLE "+pgx.Identifier{f.workerRole}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'")
	var workerOID uint32
	if f.admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1`, f.workerRole).Scan(&workerOID) != nil {
		t.Fatal("read worker OID")
	}
	appNonce, workerNonce := runtimeRandom(t), runtimeRandom(t)
	token, e := runtimeMaterial32([]byte(qa.APIToken()))
	if e != nil {
		t.Fatal(e)
	}
	p := runtimeProof{ProofVersion: 1, ProtocolVersion: 1, EnvironmentID: old.EnvironmentID, MerchantID: old.MerchantID, PayeeLabel: old.PayeeLabel, Database: old.Database, App: old.App, Mock: old.Mock, Receiver: old.Receiver, Worker: runtimeRolePin{f.workerRole, workerOID, true}, Projector: old.AppProjector, AppNonceDigest: runtimeDomainDigest("moni-payment-application-state-v1", appNonce), WorkerNonceDigest: runtimeDomainDigest("moni-payment-worker-state-v1", workerNonce), APITokenDigest: runtimeDomainDigest("moni-mock-api-token-v1", token), APIOrigin: qa.APIOrigin, ControlOrigin: qa.ControlOrigin, AppListenOrigin: qa.ReceiverOrigin, CallbackURL: qa.ReceiverOrigin + "/api/payments/sandbox/notifications"}
	var materials []runtimeMaterial
	for _, k := range old.Keys {
		p.Keys = append(p.Keys, runtimeKeyPin{k.ID, k.Digest, k.NotBefore, k.NotAfter})
		materials = append(materials, runtimeMaterial{k.ID, k.Material})
	}
	if e = runtimeValidateProof(p); e != nil {
		t.Fatal("QA public proof", e)
	}
	canonical, e := runtimeCanonicalProof(p)
	if e != nil {
		t.Fatal(e)
	}
	serviceExec(t, f.owner, ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.EnvironmentID, p.MerchantID, p.PayeeLabel, p.Database.OID, p.App.OID, p.AppNonceDigest, p.App.Runtime.Name, p.Worker.Name, p.Projector.Name)
	serviceExec(t, f.owner, ctx, `INSERT INTO payment_runtime_attestations(environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$11)`, p.EnvironmentID, p.Database.OID, p.App.OID, p.App.Owner.OID, p.App.Runtime.OID, p.Worker.OID, p.Projector.OID, p.AppNonceDigest, p.WorkerNonceDigest, canonical, runtimeSHA(canonical))
	if e = operations.ProvisionPaymentWorkerFor(ctx, f.owner, p.App.Name, p.App.Runtime.Name, p.Worker.Name, p.Projector.Name); e != nil {
		t.Fatal("offline NOLOGIN owner worker provision", e)
	}
	// Root reviewed this one legacy CHECK dependency. No pb_* or other
	// legacy capability is added to the worker's 31 payment EXECUTE grants.
	serviceExec(t, f.owner, ctx, "GRANT EXECUTE ON FUNCTION "+pgx.Identifier{p.App.Name, "funding_uuid"}.Sanitize()+"(text) TO "+pgx.Identifier{p.Worker.Name}.Sanitize())
	f.aPath = runtimeWriteOptions(t, p, applicationComponent, appNonce, materials, [32]byte{}, "")
	workerDSN := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(p.Worker.Name, password), RawQuery: "sslmode=disable"}
	f.wPath = runtimeWriteOptions(t, p, workerComponent, workerNonce, materials, token, workerDSN.String())
	f.a, e = LoadSandboxOptions(ctx, f.aPath)
	if e != nil {
		t.Fatal("load actual APP options", e)
	}
	f.w, e = LoadWorkerOptions(ctx, f.wPath)
	if e != nil {
		t.Fatal("load actual worker options", e)
	}
	if ValidateOptionsPair(f.a, f.w) != nil {
		t.Fatal("actual option pair")
	}
	appConfig, e := ApplicationPoolConfig(ctx, f.appDSN, f.a)
	if e != nil {
		t.Fatal(e)
	}
	f.app, e = pgxpool.NewWithConfig(ctx, appConfig)
	if e != nil {
		t.Fatal("open actual pinned APP")
	}
	pools = append(pools, f.app)
	projectorConfig, e := ProjectionPoolConfig(ctx, f.projectorDSN, f.a)
	if e != nil {
		t.Fatal(e)
	}
	f.projector, e = pgxpool.NewWithConfig(ctx, projectorConfig)
	if e != nil {
		t.Fatal("open actual pinned projector")
	}
	pools = append(pools, f.projector)
	life := Lifecycle{Mode: ModeSandbox, Guard: func(context.Context) error { return nil }}
	f.a, e = f.a.BindLifecycle(life)
	if e != nil {
		t.Fatal(e)
	}
	f.w, e = f.w.BindLifecycle(life)
	if e != nil {
		t.Fatal(e)
	}
	for _, component := range []paymentruntime.Component{paymentruntime.Mock, paymentruntime.Receiver} {
		path := qa.MockManifestPath
		if component == paymentruntime.Receiver {
			path = qa.ReceiverManifestPath
		}
		o, e := paymentruntime.Load(ctx, path, component)
		if e != nil {
			t.Fatal("original M/Q options", e)
		}
		r, e := paymentruntime.Open(ctx, o)
		if e != nil {
			t.Fatal("full original M/Q startup proof", e)
		}
		if e = r.Close(ctx); e != nil {
			t.Fatal(e)
		}
	}
	return f
}
func TestRuntimeActualEightRolesFilesAttestation(t *testing.T) {
	f := runtimePGFixture(t, true)
	if e := operations.VerifyPaymentRole(f.ctx, f.projector, operations.PaymentProjectionRole); e != nil {
		t.Fatal("actual projector role", e)
	}
	for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
		var n int
		if f.projector.QueryRow(f.ctx, "SELECT count(*) FROM "+pgx.Identifier{f.a.proof.App.Name, table}.Sanitize()).Scan(&n) == nil {
			t.Fatal("projector read private proof")
		}
	}
	s, e := NewService(f.ctx, f.app, f.a)
	if e != nil || s == nil {
		t.Fatal("actual APP constructor", e)
	}
	worker, e := OpenProcessor(f.ctx, f.w)
	if e != nil {
		t.Fatal("actual worker constructor", e)
	}
	defer worker.Close(f.ctx)
	if !s.runtime.hasEnvironment() || worker.runtime.environment() != s.runtime.environment() {
		t.Fatal("environment binding")
	}
	var workerUser, workerSession string
	if worker.pool.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&workerUser, &workerSession) != nil || workerUser != f.w.proof.Worker.Name || workerSession != workerUser {
		t.Fatal("worker not actual LOGIN")
	}
	if e = f.qa.VerifyFundsUnchanged(f.ctx); e != nil {
		t.Fatal("constructor changed retained A funding", e)
	}
	if _, e = NewService(f.ctx, f.app, DisabledOptions()); e != ErrPaymentsConfiguration {
		t.Fatal("unbound disabled accepted")
	}
	off, _ := DisabledOptions().BindLifecycle(Lifecycle{Guard: func(context.Context) error { return nil }})
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("marker/history discarded by ordinary off", e)
	}
	var schemaName string
	if f.owner.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&schemaName) != nil {
		t.Fatal("owner schema")
	}
	for _, mutation := range []struct {
		sql         string
		args        []any
		restore     string
		restoreArgs []any
	}{
		{`ALTER TABLE payment_runtime_attestations DISABLE TRIGGER payment_runtime_attestation_immutable`, nil, `ALTER TABLE payment_runtime_attestations ENABLE TRIGGER payment_runtime_attestation_immutable`, nil},
	} {
		serviceExec(t, f.owner, f.ctx, mutation.sql, mutation.args...)
		serviceExec(t, f.owner, f.ctx, `UPDATE payment_runtime_attestations SET worker_nonce_digest=$1`, strings.Repeat("0", 64))
		if _, e = NewService(f.ctx, f.app, f.a); e != ErrPaymentsEnvironment {
			t.Fatal("typed attestation mismatch accepted", e)
		}
		serviceExec(t, f.owner, f.ctx, `UPDATE payment_runtime_attestations SET worker_nonce_digest=$1`, f.a.proof.WorkerNonceDigest)
		serviceExec(t, f.owner, f.ctx, mutation.restore, mutation.restoreArgs...)
	}
	// A structurally legal canonical proof with a changed endpoint and matching
	// SHA is still rejected against the immutable file identity.
	serviceExec(t, f.owner, f.ctx, `ALTER TABLE payment_runtime_attestations DISABLE TRIGGER payment_runtime_attestation_immutable`)
	bad := f.a.proof
	bad.APIOrigin = "http://127.0.0.1:19091"
	raw, _ := runtimeCanonicalProof(bad)
	serviceExec(t, f.owner, f.ctx, `UPDATE payment_runtime_attestations SET proof_canonical=$1,proof_sha256=$2`, raw, runtimeSHA(raw))
	if _, e = NewService(f.ctx, f.app, f.a); e != ErrPaymentsEnvironment {
		t.Fatal("wrong DB proof accepted", e)
	}
	serviceExec(t, f.owner, f.ctx, `UPDATE payment_runtime_attestations SET proof_canonical=$1,proof_sha256=$2`, f.a.proofBytes, runtimeSHA(f.a.proofBytes))
	serviceExec(t, f.owner, f.ctx, `ALTER TABLE payment_runtime_attestations ENABLE TRIGGER payment_runtime_attestation_immutable`)
	// Prove the one explicit legacy CHECK dependency and deny a different
	// legacy namespace function before running the restricted funding path.
	var validUUID bool
	if worker.pool.QueryRow(f.ctx, `SELECT funding_uuid($1)`, uuid.NewString()).Scan(&validUUID) != nil || !validUUID {
		t.Fatal("worker CHECK dependency missing")
	}
	if worker.pool.QueryRow(f.ctx, `SELECT pb_uuid($1)`, uuid.NewString()).Scan(&validUUID) == nil {
		t.Fatal("worker acquired unrelated pb function")
	}
	runtimeCaptureActual(t, f, s, worker)
	// Wrong actual target is caught on each newly opened physical connection.
	altered := f.a
	altered.proof.Database.OID++
	cfg, e := ApplicationPoolConfig(f.ctx, f.appDSN, altered)
	if e != nil {
		t.Fatal(e)
	}
	p, e := pgxpool.NewWithConfig(f.ctx, cfg)
	if e != nil {
		t.Fatal("construct bad pin pool")
	}
	if p.Ping(f.ctx) == nil {
		p.Close()
		t.Fatal("actual DB OID mismatch accepted")
	}
	p.Close()
}
func TestRuntimeOrdinaryOffFullHistoryAndConflictFKProof(t *testing.T) {
	f := runtimePGFixture(t, false)
	off, e := DisabledOptions().BindLifecycle(Lifecycle{Guard: func(context.Context) error { return nil }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewService(f.ctx, f.app, off); e != nil {
		t.Fatal("normal off with retained A facts rejected", e)
	}
	var fk string
	if f.owner.QueryRow(f.ctx, `SELECT conname FROM pg_catalog.pg_constraint WHERE conrelid='payment_query_conflicts'::regclass AND contype='f' AND confrelid='payment_verified_queries'::regclass`).Scan(&fk) != nil {
		t.Fatal("find expected FK")
	}
	serviceExec(t, f.owner, f.ctx, "ALTER TABLE payment_query_conflicts DROP CONSTRAINT "+pgx.Identifier{fk}.Sanitize())
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("missing conflict FK accepted", e)
	}
	serviceExec(t, f.owner, f.ctx, "ALTER TABLE payment_query_conflicts ADD CONSTRAINT "+pgx.Identifier{fk}.Sanitize()+" FOREIGN KEY(verified_query_id) REFERENCES payment_verified_queries(id) NOT VALID")
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("unvalidated conflict FK accepted", e)
	}
	serviceExec(t, f.owner, f.ctx, "ALTER TABLE payment_query_conflicts VALIDATE CONSTRAINT "+pgx.Identifier{fk}.Sanitize())
	serviceExec(t, f.owner, f.ctx, `ALTER TABLE payment_query_conflicts ALTER COLUMN verified_query_id DROP NOT NULL`)
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("nullable conflict FK accepted", e)
	}
	serviceExec(t, f.owner, f.ctx, `ALTER TABLE payment_query_conflicts ALTER COLUMN verified_query_id SET NOT NULL`)
	serviceExec(t, f.owner, f.ctx, "ALTER TABLE payment_query_conflicts ALTER CONSTRAINT "+pgx.Identifier{fk}.Sanitize()+" DEFERRABLE INITIALLY DEFERRED")
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("deferrable conflict FK accepted", e)
	}
	serviceExec(t, f.owner, f.ctx, "ALTER TABLE payment_query_conflicts ALTER CONSTRAINT "+pgx.Identifier{fk}.Sanitize()+" NOT DEFERRABLE")
	if _, e = NewService(f.ctx, f.app, off); e != nil {
		t.Fatal("restored ordinary off rejected", e)
	}
	serviceExec(t, f.owner, f.ctx, `UPDATE payment_policy SET enabled=true`)
	if _, e = NewService(f.ctx, f.app, off); e != ErrPaymentsEnvironment {
		t.Fatal("non-default policy discarded", e)
	}
	if e = f.qa.VerifyFundsUnchanged(f.ctx); e != nil {
		t.Fatal("off classification changed retained A funding", e)
	}
}

// This is a signed sandbox evidence/capture transaction on the fully proved
// runtimes. No HTTP listener or PSP process is implied by this database test.
func runtimeCaptureActual(t *testing.T, f *paymentRuntimeFixture, s *Service, p *Processor) {
	t.Helper()
	ctx := f.ctx
	actor, tenant, admin := uuid.NewString(), uuid.NewString(), uuid.NewString()
	serviceExec(t, f.app, ctx, `INSERT INTO tenants(id,name) VALUES($1,'full runtime restricted capture')`, tenant)
	for _, u := range []struct{ id, role string }{{actor, "none"}, {admin, "admin"}} {
		serviceExec(t, f.app, ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-runtime-fixture',$3)`, u.id, u.id+"@runtime-qa.invalid", u.role)
	}
	serviceExec(t, f.app, ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, tenant, actor)
	sku := uuid.NewString()
	published, e := s.PublishSKU(ctx, PublishSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: admin}, SKUID: sku, ExpectedRevision: 0, DisplayName: "Runtime QA credit", Amount: 200})
	if e != nil || published.Receipt.ResourceID == nil {
		t.Fatal("actual runtime publish", e)
	}
	version := *published.Receipt.ResourceID
	if _, e = s.ActivateSKU(ctx, ActivateSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: admin}, SKUID: sku, ExpectedRevision: 1, VersionID: &version, Enabled: true}); e != nil {
		t.Fatal("actual runtime activate", e)
	}
	if _, e = s.UpdatePolicy(ctx, UpdatePolicyInput{ActorID: admin, ExpectedRevision: 1, Enabled: true}); e != nil {
		t.Fatal("actual runtime enable", e)
	}
	order, e := s.CreateOrder(ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: actor, TenantID: tenant}, SKUVersionID: version})
	if e != nil || order.Order == nil {
		t.Fatal("actual runtime order", e)
	}
	var now time.Time
	if f.app.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		t.Fatal("fresh capture clock")
	}
	now = now.UTC()
	transaction := "transaction:" + uuid.NewString()
	n := paymentproto.Notification{SchemaVersion: 1, EventID: "event:" + uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: f.a.proof.EnvironmentID, MerchantID: f.a.proof.MerchantID, OrderID: order.Order.ID, ProviderTransactionID: &transaction, Amount: "200", Currency: "CNY", OccurredAt: now.Format(paymentTimeLayout), ProviderOrderRevision: "2"}
	raw, e := paymentproto.CanonicalNotification(n)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := f.qa.Keyring()
	if e != nil {
		t.Fatal(e)
	}
	headers, e := keys.SignNotification(s.runtime.environment(), paymentproto.SignedHeaders{KeyID: "qa-key", DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.ReceiveNotification(ctx, NotificationInput{Headers: headers, Body: raw}); e != nil {
		t.Fatal("actual runtime notification", e)
	}
	lease, ok, e := p.ClaimEvidence(ctx)
	if e != nil || !ok {
		t.Fatal("actual restricted worker claim", e)
	}
	result, e := p.ProcessEvidence(ctx, lease)
	if e != nil || result.Outcome != "credited" || result.CaptureID == nil {
		t.Fatal("actual restricted capture", e)
	}
	again, e := p.ProcessEvidence(ctx, lease)
	if e != nil || again.Outcome != "existing" || again.CaptureID == nil || *again.CaptureID != *result.CaptureID {
		t.Fatal("actual capture dedup", e)
	}
	var captures, receipts, journals, legacy int
	var available int64
	if f.app.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_captures),(SELECT count(*) FROM funding_receipts WHERE source_kind='sandbox_capture'),(SELECT count(*) FROM journals WHERE kind='sandbox_capture'),(SELECT count(*) FROM funding_receipts WHERE source_kind='manual_credit' AND amount=1000),balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'`, tenant).Scan(&captures, &receipts, &journals, &legacy, &available) != nil || captures != 1 || receipts != 1 || journals != 1 || legacy != 1 || available != 200 {
		t.Fatal("actual restricted unique funding graph")
	}
}
