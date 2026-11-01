package operations

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// This is a DDL/ACL test, not an assertion that {} is a valid runtime proof.
// Runtime constructors must independently reject a structurally stored but
// semantically invalid owner-installed proof before opening any listener.
func TestPaymentAttestationReadOnlyIdentitiesAndImmutability(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	insert := `INSERT INTO payment_runtime_attestations
 (singleton,environment_id,database_oid,schema_oid,schema_owner_oid,
 app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,
 proof_version,proof_canonical,proof_sha256)
 SELECT e.singleton,e.id,e.database_oid,e.schema_oid,n.nspowner,a.oid,w.oid,p.oid,
 e.nonce_digest,$1,1,$2,$3 FROM payment_sandbox_environment e
 JOIN pg_catalog.pg_namespace n ON n.oid=e.schema_oid
 JOIN pg_catalog.pg_roles a ON a.rolname=e.app_role
 JOIN pg_catalog.pg_roles w ON w.rolname=e.worker_role
 JOIN pg_catalog.pg_roles p ON p.rolname=e.projector_role`
	proof := []byte(`{}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(proof))
	_, err := f.owner.Exec(f.ctx, insert, strings.Repeat("2", 64), proof, strings.Repeat("0", 64))
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23514" {
		t.Fatal("owner-installed proof with mismatched digest was accepted")
	}
	f.exec(t, insert, strings.Repeat("2", 64), proof, digest)
	for _, identity := range []PaymentDatabaseRole{PaymentApplicationRole, PaymentWorkerRole, PaymentProjectionRole} {
		pool := f.app
		if identity == PaymentWorkerRole {
			pool = f.worker
		} else if identity == PaymentProjectionRole {
			pool = f.projection
		}
		var n int
		err := pool.QueryRow(f.ctx, `SELECT count(*) FROM payment_runtime_attestations`).Scan(&n)
		if identity == PaymentProjectionRole {
			if !errors.As(err, &pg) || pg.Code != "42501" {
				t.Fatal("projector could read runtime proof")
			}
		} else if err != nil || n != 1 {
			t.Fatal("application/worker could not read installed attestation")
		}
		for _, query := range []string{
			`UPDATE payment_runtime_attestations SET proof_version=proof_version`,
			`DELETE FROM payment_runtime_attestations`,
			`TRUNCATE payment_runtime_attestations`,
			`INSERT INTO payment_runtime_attestations DEFAULT VALUES`,
		} {
			_, err = pool.Exec(f.ctx, query)
			if !errors.As(err, &pg) || pg.Code != "42501" {
				t.Fatal("online identity obtained attestation write permission")
			}
		}
	}
	for _, query := range []string{
		`UPDATE payment_runtime_attestations SET proof_version=proof_version`,
		`DELETE FROM payment_runtime_attestations`,
	} {
		if _, err := f.owner.Exec(f.ctx, query); err == nil {
			t.Fatal("installed attestation history was mutable")
		}
	}
	f.valid(t, true)
}
