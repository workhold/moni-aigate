package paymentsandboxqa

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

// ApplicationEnvironment is an offline QA capability. No ordinary gateway
// process imports this package or receives its administrator credentials.
type ApplicationEnvironment struct {
	*Environment
	ApplicationManifestPath, WorkerManifestPath string
	ApplicationTenantID                         string
	ActorIDs                                    map[string]string
	appNonce, workerNonce                       [32]byte
	credentials                                 map[string]applicationCredential
	legacyDigest                                string
	appMu                                       sync.RWMutex
}
type applicationCredential struct{ email, password string }

func (*ApplicationEnvironment) String() string {
	return "paymentsandboxqa.ApplicationEnvironment(redacted)"
}
func (e *ApplicationEnvironment) GoString() string           { return e.String() }
func (*ApplicationEnvironment) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }

func CreateApplication(ctx context.Context, adminDSN, privateRoot string) (_ *ApplicationEnvironment, result error) {
	base, err := Create(ctx, adminDSN, privateRoot)
	if err != nil {
		return nil, err
	}
	e := &ApplicationEnvironment{Environment: base, appNonce: fresh(), workerNonce: fresh(), ActorIDs: map[string]string{}, credentials: map[string]applicationCredential{}}
	defer func() {
		if result != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = e.Close(cleanup)
		}
	}()
	owner, err := base.OpenPool(ctx, "app_owner")
	if err != nil {
		return nil, err
	}
	defer owner.Close()
	if migrations.VerifyCurrent(ctx, owner) != nil {
		return nil, stage("application_upgrade")
	}
	schema := ident(e.Schemas["app"])
	if _, err = owner.Exec(ctx, "REVOKE ALL ON ALL FUNCTIONS IN SCHEMA "+schema+" FROM PUBLIC"); err != nil {
		return nil, stage("application_public_revoke")
	}
	if _, err = owner.Exec(ctx, "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA "+schema+" TO "+ident(e.Roles["app_runtime"])+","+ident(e.Roles["app_projector"])); err != nil {
		return nil, stage("application_legacy_grant")
	}
	if operations.ProvisionFor(ctx, owner, e.Schemas["app"], e.Roles["app_runtime"], e.Roles["app_projector"]) != nil {
		return nil, stage("application_provision")
	}
	admin, err := e.adminPool(ctx)
	if err != nil {
		return nil, err
	}
	defer admin.Close()
	role := "pm_app_worker_" + strings.TrimPrefix(e.Schemas["app"], "payment_sandbox_")
	password := encoded(fresh())
	if _, err = admin.Exec(ctx, "CREATE ROLE "+ident(role)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD "+literal(password)); err != nil {
		return nil, stage("application_worker_create")
	}
	e.createdRoles = append(e.createdRoles, role)
	e.Roles["app_worker"] = role
	e.passwords["app_worker"] = password
	var oid uint32
	if admin.QueryRow(ctx, `SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1`, role).Scan(&oid) != nil {
		return nil, stage("application_worker_oid")
	}
	e.roleOIDs["app_worker"] = oid
	proof := e.proof()
	canonical, err := applicationCanonical(proof)
	if err != nil {
		return nil, err
	}
	tx, err := owner.Begin(ctx)
	if err != nil {
		return nil, stage("application_proof_transaction")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, proof.EnvironmentID, proof.MerchantID, proof.PayeeLabel, proof.Database.OID, proof.App.OID, proof.AppNonceDigest, proof.App.Runtime.Name, proof.Worker.Name, proof.Projector.Name); err != nil {
		return nil, stage("application_marker")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO payment_runtime_attestations(environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,1,$10,$11)`, proof.EnvironmentID, proof.Database.OID, proof.App.OID, proof.App.Owner.OID, proof.App.Runtime.OID, proof.Worker.OID, proof.Projector.OID, proof.AppNonceDigest, proof.WorkerNonceDigest, canonical, applicationSHA(canonical)); err != nil {
		return nil, stage("application_attestation")
	}
	if tx.Commit(ctx) != nil {
		return nil, stage("application_proof_commit")
	}
	if operations.ProvisionPaymentWorkerFor(ctx, owner, proof.App.Name, proof.App.Runtime.Name, proof.Worker.Name, proof.Projector.Name) != nil {
		return nil, stage("application_worker_provision")
	}
	if _, err = owner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+schema+"."+ident("funding_uuid")+"(text) TO "+ident(role)); err != nil {
		return nil, stage("application_worker_leaf")
	}
	if err = e.writeApplicationManifests(proof, canonical); err != nil {
		return nil, err
	}
	if err = e.seedApplicationUsers(ctx); err != nil {
		return nil, err
	}
	for component, role := range map[string]operations.PaymentDatabaseRole{"app": operations.PaymentApplicationRole, "worker": operations.PaymentWorkerRole, "projector": operations.PaymentProjectionRole} {
		pool, err := e.OpenPool(ctx, component)
		if err != nil {
			return nil, err
		}
		verifyErr := operations.VerifyPaymentRole(ctx, pool, role)
		pool.Close()
		if verifyErr != nil {
			return nil, stage("application_actual_roles")
		}
	}
	if err = e.VerifyFundsUnchanged(ctx); err != nil {
		return nil, err
	}
	e.legacyDigest, err = e.LegacyFundingSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (e *ApplicationEnvironment) OpenPool(ctx context.Context, component string) (*pgxpool.Pool, error) {
	if e == nil || e.Environment == nil {
		return nil, ErrEnvironment
	}
	if component == "worker" {
		return e.pool(ctx, "app_worker", "app", false)
	}
	return e.Environment.OpenPool(ctx, component)
}

// RuntimeDSN may only be handed to an isolated QA subprocess or constructor.
func (e *ApplicationEnvironment) RuntimeDSN(component string) (string, error) {
	if e == nil || e.Environment == nil {
		return "", ErrEnvironment
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", ErrEnvironment
	}
	role := ""
	switch component {
	case "app":
		role = "app_runtime"
	case "projector":
		role = "app_projector"
	case "worker":
		role = "app_worker"
	default:
		return "", ErrEnvironment
	}
	if e.Roles[role] == "" || e.passwords[role] == "" {
		return "", ErrEnvironment
	}
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(e.Roles[role], e.passwords[role]), RawQuery: "sslmode=disable"}
	return u.String(), nil
}
func (e *ApplicationEnvironment) Credentials(actor string) (email, password string, err error) {
	if e == nil {
		return "", "", ErrEnvironment
	}
	e.appMu.RLock()
	defer e.appMu.RUnlock()
	c, ok := e.credentials[actor]
	if !ok {
		return "", "", ErrEnvironment
	}
	return c.email, c.password, nil
}
func (e *ApplicationEnvironment) Close(ctx context.Context) error {
	if e == nil || e.Environment == nil {
		return nil
	}
	if err := e.Environment.Close(ctx); err != nil {
		return err
	}
	e.appMu.Lock()
	defer e.appMu.Unlock()
	e.credentials = nil
	e.appNonce = [32]byte{}
	e.workerNonce = [32]byte{}
	return nil
}
