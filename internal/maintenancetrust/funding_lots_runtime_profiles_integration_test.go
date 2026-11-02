package maintenancetrust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
)

const runtimeProfileReadPermissionsSHA = "97abd87df7e5d0e7a03adae4158470e76ab94d58c9e9d8b92ae792aeb84867c2"

// Expected originals are captured during owner-only fixture preparation and
// checked against the sole protected Plan result. This is test evidence, not a
// production expected-file issuer or runtime adoption of database claims.
func runtimeProfileExpected(t *testing.T, owner *pgxpool.Pool, pins fundinglotcatalog.Pins, expected FundingLotsExpected) *fundinglotproof.CanonicalV2 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var raw []byte
	var digest string
	if owner.QueryRow(ctx, "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{pins.AppSchema.Name, "funding_lot_runtime_attestations"}.Sanitize()).Scan(&raw, &digest) != nil {
		t.Fatal("runtime profile owner preparation original V2")
	}
	p, err := fundinglotproof.DecodeV2(raw)
	if err != nil || p == nil || p.SHA256() != digest || digest != expected.RuntimeProofDigest {
		t.Fatal("runtime profile original V2 must equal protected Plan.Expected")
	}
	b, err := p.Bindings()
	if err != nil || b.EpochID != expected.EpochID || b.BasisDigest != expected.BasisDigest || b.ManifestDigest != expected.ManifestDigest || b.BasisDescriptorDigest != expected.DescriptorDigest {
		t.Fatal("runtime profile prepared V2 full protected bindings")
	}
	return p
}

func runtimeProfileInstallDelta(t *testing.T, owner *pgxpool.Pool, pins fundinglotcatalog.Pins) {
	t.Helper()
	raw := snapshotFixedSource(t, snapshotRepositoryRoot(t), "specs/022-self-service-payments/c0-runtime-read-permissions.sql", runtimeProfileReadPermissionsSHA)
	text := string(raw)
	if strings.Count(text, "{{schema}}") != 1 || strings.Count(text, "{{app}}") != 1 {
		t.Fatal("runtime profile fixed delta placeholders")
	}
	text = strings.ReplaceAll(text, "{{schema}}", pgx.Identifier{pins.AppSchema.Name}.Sanitize())
	text = strings.ReplaceAll(text, "{{app}}", pgx.Identifier{pins.App.Name}.Sanitize())
	if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
		t.Fatal("runtime profile unresolved fixed delta")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := owner.Exec(ctx, text); err != nil {
		t.Fatal("runtime profile offline owner exact four-column delta")
	}
}

func runtimeProfilePublicPool(t *testing.T, f *authorityPublicProfile, role, label string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("runtime profile public pool configuration")
	}
	config.ConnConfig, config.MinConns, config.MaxConns = f.config(role, label), 0, 1
	config.AfterConnect = f.capture
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("runtime profile public fresh role pool")
	}
	t.Cleanup(pool.Close)
	return pool
}

func runtimeProfileInitializePublic(t *testing.T, f *authorityPublicProfile) *fundinglotproof.CanonicalV2 {
	t.Helper()
	epoch := uuid.NewString()
	g := f.acquire(t)
	s, err := g.BeginPlan(context.Background())
	if err != nil {
		t.Fatal("runtime profile public plan snapshot")
	}
	if p, err := s.VerifyInstallation(context.Background()); err != nil || p == nil {
		t.Fatal("runtime profile public original installation")
	}
	p, err := Attest(context.Background(), s)
	if err != nil || p == nil {
		t.Fatal("runtime profile public protected plan authority")
	}
	plan, err := p.PlanFundingLots(context.Background(), epoch)
	if err != nil || s.Outcome() != OutcomeCommitted || plan.Metadata.ReceiptCount == 0 || plan.Metadata.SettlementCount == 0 || plan.Metadata.ApprovedRefundCount == 0 || g.Close(nil) != nil {
		t.Fatal("runtime profile public nonempty protected plan")
	}
	g = f.acquire(t)
	s, err = g.BeginInit(context.Background())
	if err != nil {
		t.Fatal("runtime profile public init snapshot")
	}
	if installed, err := s.VerifyInstallation(context.Background()); err != nil || installed == nil {
		t.Fatal("runtime profile public init installation")
	}
	p, err = Attest(context.Background(), s)
	if err != nil || p == nil {
		t.Fatal("runtime profile public protected init authority")
	}
	result, err := p.InitializeFundingLots(context.Background(), plan.Expected)
	if err != nil || s.Outcome() != OutcomeCommitted || result.RuntimeProofDigest != plan.Expected.RuntimeProofDigest || g.Close(nil) != nil {
		t.Fatal("runtime profile public protected init")
	}
	owner := runtimeProfilePublicPool(t, f, f.owner, "runtime_original")
	original := runtimeProfileExpected(t, owner, f.pins, plan.Expected)
	runtimeProfileInstallDelta(t, owner, f.pins)
	owner.Close()
	return original
}

func runtimeProfileInitializeSandbox(t *testing.T, f *authoritySandboxFixture) (*fundinglotproof.CanonicalV2, *fundinglotruntime.ApplicationState) {
	t.Helper()
	g, s, _, backend := f.installed(t, false)
	p, err := Attest(context.Background(), s)
	if err != nil || p == nil {
		t.Fatal("runtime profile genuine sandbox plan authority")
	}
	plan, err := p.PlanFundingLots(context.Background(), uuid.NewString())
	if err != nil || s.Outcome() != OutcomeCommitted || plan.Metadata.ReceiptCount == 0 || g.Close(nil) != nil {
		t.Fatal("runtime profile genuine sandbox nonempty plan")
	}
	f.gone(t, backend)
	g, s, _, backend = f.installed(t, true)
	p, err = Attest(context.Background(), s)
	if err != nil || p == nil {
		t.Fatal("runtime profile genuine sandbox init authority")
	}
	result, err := p.InitializeFundingLots(context.Background(), plan.Expected)
	if err != nil || s.Outcome() != OutcomeCommitted || result.RuntimeProofDigest != plan.Expected.RuntimeProofDigest || g.Close(nil) != nil {
		t.Fatal("runtime profile genuine sandbox protected init")
	}
	f.gone(t, backend)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("runtime profile genuine owner preparation pool")
	}
	f.pools = append(f.pools, owner)
	b := f.observePool(t, ctx, owner, f.env.Roles["app_owner"], "moni_test")
	original := runtimeProfileExpected(t, owner, f.pins, plan.Expected)
	runtimeProfileInstallDelta(t, owner, f.pins)
	owner.Close()
	f.gone(t, b)
	local, err := fundinglotruntime.LoadSandboxApplicationState(ctx, f.env.ApplicationManifestPath)
	if err != nil || local == nil {
		t.Fatal("runtime profile genuine original APP private manifest")
	}
	return original, local
}

// Each call owns its actual APP RR RO wrapper through cleanup. Failures cannot
// return a busy connection to the pool. No owner/worker/peer connection is used
// by the composer, and a completed transaction cannot extend proof lifetime.
func runtimeProfileInvoke(t *testing.T, pool *pgxpool.Pool, pins fundinglotcatalog.Pins, expected *fundinglotproof.CanonicalV2, local *fundinglotruntime.ApplicationState, want bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("runtime profile actual APP RR RO begin")
	}
	conn := tx.Conn()
	closed := false
	finish := func() {
		if closed {
			return
		}
		closed = true
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		err := tx.Rollback(cleanup)
		if err != nil || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
			_ = conn.Close(cleanup)
		}
		if !conn.IsClosed() && (conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I') {
			t.Error("runtime profile caller did not establish idle-or-closed cleanup")
		}
	}
	defer finish()
	var actual bool
	if conn.Config().User != pins.App.Name || tx.QueryRow(ctx, `SELECT current_user=session_user AND current_user=$1::name AND r.oid=$2::oid AND r.rolcanlogin AND NOT r.rolsuper AND current_setting('transaction_isolation')='repeatable read' AND current_setting('transaction_read_only')='on' FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, pins.App.Name, pins.App.OID).Scan(&actual) != nil || !actual {
		t.Fatal("runtime profile composer must run on actual APP LOGIN RR RO")
	}
	var proof *fundinglotruntime.RuntimeProof
	if pins.Mode == fundinglotcatalog.Sandbox {
		proof, err = fundinglotruntime.VerifySandboxApplicationTx(ctx, tx, pins, expected, local)
	} else {
		proof, err = fundinglotruntime.VerifyOrdinaryApplicationTx(ctx, tx, pins, expected)
	}
	if !want {
		if proof != nil || err != fundinglotruntime.ErrRuntime {
			t.Fatalf("runtime profile invalid fixture must return nil proof and fixed error: proof=%t error=%v", proof != nil, err)
		}
		return
	}
	if err != nil || proof == nil || proof.VerifyTx(ctx, tx) != nil {
		t.Fatalf("runtime profile complete nonempty actual APP verification: proof=%t error=%v", proof != nil, err)
	}
	copyOfProof := *proof
	if copyOfProof.VerifyTx(ctx, tx) != nil {
		t.Fatal("runtime profile read-only proof copy lost original wrapper")
	}
	finish()
	if conn.IsClosed() {
		t.Fatal("runtime profile successful caller rollback must confirm idle connection")
	}
	if proof.VerifyTx(context.Background(), tx) != fundinglotruntime.ErrRuntime || copyOfProof.VerifyTx(context.Background(), tx) != fundinglotruntime.ErrRuntime {
		t.Fatal("runtime profile proof survived its original ended transaction")
	}
}

func TestFundingLotsRuntimePostgresPublicProfiles(t *testing.T) {
	gateTestConfig(t) // explicit opt-in before creating any resource
	var previous []fundinglotcatalog.Pins
	for round := 0; round < 2; round++ {
		for _, image := range []bool{false, true} {
			t.Run(fmt.Sprintf("round_%d/image_%t", round+1, image), func(t *testing.T) {
				f := newAuthorityPublicProfilePrepared(t, image, func(pool *pgxpool.Pool) {
					// Nonempty legacy business must precede C0's new-tenant
					// epoch trigger, using the original APIs and typed owner pool.
					fundingLotsMixedHistory(t, nil, pool)
				})
				for _, old := range previous {
					if old.Database.Name == f.pins.Database.Name || old.Database.OID == f.pins.Database.OID || old.App.Name == f.pins.App.Name || old.App.OID == f.pins.App.OID || old.AppSchema.ObjectOwner.OID == f.pins.AppSchema.ObjectOwner.OID {
						t.Fatal("runtime public profiles reused dynamic names/OIDs")
					}
				}
				previous = append(previous, f.pins)
				expected := runtimeProfileInitializePublic(t, f)
				app := runtimeProfilePublicPool(t, f, f.app, "runtime_actual_app")
				runtimeProfileInvoke(t, app, f.pins, expected, nil, true)
				app.Close()
			})
		}
	}
}

func TestFundingLotsRuntimePostgresGenuineSandboxProfiles(t *testing.T) {
	gateTestConfig(t)
	var previous *fundinglotcatalog.Pins
	for round := 0; round < 2; round++ {
		t.Run(fmt.Sprintf("round_%d", round+1), func(t *testing.T) {
			f := newAuthoritySandboxFixture(t)
			if previous != nil && (previous.AppSchema.Name == f.pins.AppSchema.Name || previous.AppSchema.OID == f.pins.AppSchema.OID || previous.App.Name == f.pins.App.Name || previous.App.OID == f.pins.App.OID) {
				t.Fatal("runtime genuine sandbox reused schema/role names or OIDs")
			}
			copyPins := f.pins
			previous = &copyPins
			expected, local := runtimeProfileInitializeSandbox(t, f)
			app, backend := f.runtime(t, "app", "app_runtime")
			runtimeProfileInvoke(t, app, f.pins, expected, local, true)
			for _, peer := range []struct{ schema, table string }{{f.pins.Sandbox.Mock.Name, "mock_environment"}, {f.pins.Sandbox.Receiver.Name, "receiver_environment"}} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_, err := app.Exec(ctx, "SELECT * FROM "+pgx.Identifier{peer.schema, peer.table}.Sanitize())
				cancel()
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
					t.Fatal("runtime APP unexpectedly read peer contents")
				}
			}
			for _, tc := range []struct {
				name  string
				local *fundinglotruntime.ApplicationState
			}{{"nil", nil}, {"zero", &fundinglotruntime.ApplicationState{}}, {"different_valid_private_snapshot", runtimeProfileDifferentLocal(t, f.env.ApplicationManifestPath)}} {
				t.Run("local/"+tc.name, func(t *testing.T) { runtimeProfileInvoke(t, app, f.pins, expected, tc.local, false) })
			}
			runtimeProfileSandboxScalarRefusals(t, f, app, expected, local)
			runtimeProfileSandboxCanonicalRefusals(t, f, app, expected, local)
			// Restoration must leave the original real material usable. The
			// original QA funding witness also detects accidental business writes.
			runtimeProfileInvoke(t, app, f.pins, expected, local, true)
			app.Close()
			f.gone(t, backend)
			if f.env.VerifyLegacyFundingUnchanged(context.Background()) != nil {
				t.Fatal("runtime sandbox verification changed genuine legacy funds")
			}
		})
	}
}

// A second legitimate local load has the same real material and pins but a
// different signed-format merchant claim. It must not authenticate DB V1/V2.
func runtimeProfileDifferentLocal(t *testing.T, originalPath string) *fundinglotruntime.ApplicationState {
	t.Helper()
	raw, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal("runtime profile original private manifest read")
	}
	var m struct {
		ManifestVersion int                     `json:"manifest_version"`
		Component       string                  `json:"component"`
		Proof           fundinglotproof.V1Input `json:"proof"`
		ProofSHA256     string                  `json:"proof_sha256"`
		NonceFile       struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"nonce_file"`
		Keys []struct {
			ID       string `json:"id"`
			Material string `json:"material_base64_url"`
		} `json:"keys"`
	}
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("runtime profile original manifest fixture shape")
	}
	m.Proof.MerchantID = "runtime-profile-different-merchant"
	canonical, err := fundinglotproof.EncodeV1(m.Proof)
	if err != nil {
		t.Fatal("runtime profile changed local claim format")
	}
	m.ProofSHA256 = canonical.SHA256()
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("runtime profile alternate private directory")
	}
	nonce, err := os.ReadFile(filepath.Join(filepath.Dir(originalPath), m.NonceFile.Name))
	if err != nil || os.WriteFile(filepath.Join(dir, m.NonceFile.Name), nonce, 0600) != nil {
		t.Fatal("runtime profile alternate nonce fixture")
	}
	raw, err = json.Marshal(m)
	path := filepath.Join(dir, "manifest.json")
	if err != nil || os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("runtime profile alternate local fixture manifest")
	}
	local, err := fundinglotruntime.LoadSandboxApplicationState(context.Background(), path)
	if err != nil || local == nil {
		t.Fatal("runtime profile alternate manifest must independently load")
	}
	return local
}

func runtimeProfileSandboxScalarRefusals(t *testing.T, f *authoritySandboxFixture, app *pgxpool.Pool, expected *fundinglotproof.CanonicalV2, local *fundinglotruntime.ApplicationState) {
	t.Helper()
	for _, tc := range []struct{ table, column, expression, cast string }{
		{"payment_sandbox_environment", "id", "'" + uuid.NewString() + "'", "text"},
		{"payment_sandbox_environment", "merchant_id", "'different-merchant'", "text"},
		{"payment_sandbox_environment", "payee_label", "'different-payee'", "text"},
		{"payment_sandbox_environment", "database_oid", "1::oid", "oid"},
		{"payment_sandbox_environment", "schema_oid", "1::oid", "oid"},
		{"payment_sandbox_environment", "nonce_digest", "repeat('f',64)", "text"},
		{"payment_sandbox_environment", "app_role", "'different_app'::name", "name"},
		{"payment_sandbox_environment", "worker_role", "'different_worker'::name", "name"},
		{"payment_sandbox_environment", "projector_role", "'different_projector'::name", "name"},
		{"payment_sandbox_environment", "created_at", "'10000-01-01 00:00:00+00'::timestamptz", "timestamptz"},
		{"payment_runtime_attestations", "environment_id", "'" + uuid.NewString() + "'", "text"},
		{"payment_runtime_attestations", "database_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "schema_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "schema_owner_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "app_role_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "worker_role_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "projector_role_oid", "1::oid", "oid"},
		{"payment_runtime_attestations", "app_nonce_digest", "repeat('f',64)", "text"},
		{"payment_runtime_attestations", "worker_nonce_digest", "repeat('f',64)", "text"},
		{"payment_runtime_attestations", "created_at", "'0001-01-01 00:00:00+00'::timestamptz", "timestamptz"},
	} {
		t.Run("binding/"+tc.table+"/"+tc.column, func(t *testing.T) {
			relation := pgx.Identifier{f.pins.AppSchema.Name, tc.table}.Sanitize()
			column := pgx.Identifier{tc.column}.Sanitize()
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			var original string
			if f.admin.QueryRow(ctx, "SELECT "+column+"::text FROM "+relation).Scan(&original) != nil {
				t.Fatal("runtime profile binding original witness")
			}
			// This committed corrupt-history fixture uses only a transaction-local
			// administrator replication setting. Frozen catalog/ACL/trigger
			// definitions stay identical for the subsequent actual APP scan.
			f.replaceScalar(t, relation, column, tc.expression)
			defer f.restoreScalar(t, relation, column, original, tc.cast)
			runtimeProfileInvoke(t, app, f.pins, expected, local, false)
		})
	}
}

func runtimeProfileSandboxCanonicalRefusals(t *testing.T, f *authoritySandboxFixture, app *pgxpool.Pool, expected *fundinglotproof.CanonicalV2, local *fundinglotruntime.ApplicationState) {
	t.Helper()
	relation := pgx.Identifier{f.pins.AppSchema.Name, "payment_runtime_attestations"}.Sanitize()
	var original []byte
	var originalSHA string
	if f.admin.QueryRow(context.Background(), "SELECT proof_canonical,proof_sha256 FROM "+relation).Scan(&original, &originalSHA) != nil {
		t.Fatal("runtime profile genuine DB V1 original")
	}
	canonical, err := fundinglotproof.DecodeV1(original)
	if err != nil || canonical.SHA256() != originalSHA {
		t.Fatal("runtime profile genuine DB V1 strict format")
	}
	for _, field := range []string{"environment", "merchant", "nonce", "api_token", "origins", "keyset"} {
		t.Run("canonical/"+field, func(t *testing.T) {
			b, err := canonical.Bindings()
			if err != nil {
				t.Fatal("runtime profile original V1 bindings")
			}
			switch field {
			case "environment":
				b.EnvironmentID = uuid.NewString()
			case "merchant":
				b.MerchantID = "different-canonical-merchant"
			case "nonce":
				b.AppNonceDigest = strings.Repeat("f", 64)
			case "api_token":
				b.APITokenDigest = strings.Repeat("e", 64)
			case "origins":
				b.APIOrigin, b.ControlOrigin = b.ControlOrigin, b.APIOrigin
			case "keyset":
				b.Keys[0].Digest = strings.Repeat("d", 64)
			}
			changed, err := fundinglotproof.EncodeV1(b)
			if err != nil {
				t.Fatal("runtime profile alternate V1 must remain strict-valid")
			}
			runtimeProfileReplaceCanonical(t, f, relation, changed.Bytes(), changed.SHA256())
			defer runtimeProfileReplaceCanonical(t, f, relation, original, originalSHA)
			runtimeProfileInvoke(t, app, f.pins, expected, local, false)
		})
	}
	t.Run("canonical/v2_different_valid_predecessor", func(t *testing.T) {
		v1, err := canonical.Bindings()
		if err != nil {
			t.Fatal("runtime profile V1 predecessor fixture")
		}
		v1.MerchantID = "different-v2-predecessor"
		changedV1, err := fundinglotproof.EncodeV1(v1)
		if err != nil {
			t.Fatal("runtime profile alternate predecessor format")
		}
		v2, err := expected.Bindings()
		if err != nil {
			t.Fatal("runtime profile original expected bindings")
		}
		digest, encoded := changedV1.SHA256(), base64.RawURLEncoding.EncodeToString(changedV1.Bytes())
		v2.PredecessorPaymentDigest, v2.PredecessorPaymentCanonicalBase64URL = &digest, &encoded
		changedV2, err := fundinglotproof.EncodeV2(v2)
		if err != nil {
			t.Fatal("runtime profile alternate V2 must remain strict-valid")
		}
		v2relation := pgx.Identifier{f.pins.AppSchema.Name, "funding_lot_runtime_attestations"}.Sanitize()
		epoch := pgx.Identifier{f.pins.AppSchema.Name, "funding_lot_epoch"}.Sanitize()
		runtimeProfileReplaceCanonicalEpoch(t, f, v2relation, epoch, changedV2.Bytes(), changedV2.SHA256())
		defer runtimeProfileReplaceCanonicalEpoch(t, f, v2relation, epoch, expected.Bytes(), expected.SHA256())
		runtimeProfileInvoke(t, app, f.pins, expected, local, false)
	})
	var after []byte
	var afterSHA string
	if f.admin.QueryRow(context.Background(), "SELECT proof_canonical,proof_sha256 FROM "+relation).Scan(&after, &afterSHA) != nil || afterSHA != originalSHA || !bytes.Equal(after, original) {
		t.Fatal("runtime profile canonical refusal failed to restore original V1")
	}
}

func runtimeProfileReplaceCanonical(t *testing.T, f *authoritySandboxFixture, relation string, raw []byte, digest string) {
	t.Helper()
	runtimeProfileReplaceCanonicalEpoch(t, f, relation, "", raw, digest)
}

func runtimeProfileReplaceCanonicalEpoch(t *testing.T, f *authoritySandboxFixture, relation, epoch string, raw []byte, digest string) {
	t.Helper()
	h := sha256.Sum256(raw)
	if hex.EncodeToString(h[:]) != digest {
		t.Fatal("runtime profile canonical mutation SHA fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal("runtime profile canonical mutation begin")
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role='replica'"); err != nil {
		t.Fatal("runtime profile canonical test-only immutable bypass")
	}
	result, err := tx.Exec(ctx, "UPDATE "+relation+" SET proof_canonical=$1::bytea,proof_sha256=$2::text", pgx.QueryExecModeSimpleProtocol, raw, digest)
	if err != nil || result.RowsAffected() != 1 {
		t.Fatal("runtime profile exact canonical row mutation")
	}
	if epoch != "" {
		result, err = tx.Exec(ctx, "UPDATE "+epoch+" SET runtime_proof_digest=$1::text", digest)
		if err != nil || result.RowsAffected() != 1 {
			t.Fatal("runtime profile exact epoch digest mutation")
		}
	}
	if tx.Commit(ctx) != nil {
		t.Fatal("runtime profile committed canonical mutation")
	}
}
