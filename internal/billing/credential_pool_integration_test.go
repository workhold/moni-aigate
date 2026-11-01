package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Pool membership is born with its immutable version in one transaction. These
// fixtures never append a credential to a previously committed version.
func billingPoolVersion(t *testing.T, f *fixture, credentials ...string) string {
	t.Helper()
	version := uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, credential := range credentials {
		if _, err = tx.Exec(f.ctx, "INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'local-pool-fixture') ON CONFLICT(id) DO NOTHING", credential, f.channel); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)", version, f.channel, credentials[0]); err != nil {
		t.Fatal(err)
	}
	for i, credential := range credentials {
		if _, err = tx.Exec(f.ctx, "INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,$4,$5,1,1)", version, f.channel, credential, i+1, fmt.Sprintf("member-%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, "UPDATE channels SET current_version=$2 WHERE id=$1", f.channel, version); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return version
}

func billingPoolPublish(t *testing.T, f *fixture, version string, revision int64) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	policy := uuid.NewString()
	if _, err = tx.Exec(f.ctx, "INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,$3)", policy, f.model, revision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,10)", policy, f.channel, version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, "UPDATE models SET active_revision=$2 WHERE id=$1", f.model, revision); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func billingPoolReserve(t *testing.T, f *fixture, revision int64) Reservation {
	t.Helper()
	in := f.input()
	in.RouteRevision = revision
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.EndRequest(r.RequestID) })
	return r
}

func billingPoolAttemptInput(f *fixture, r Reservation, version, credential string) AttemptInput {
	in := dispatchBatchAttemptInput(f, r)
	in.ChannelVersionID, in.CredentialID = version, credential
	return in
}

func billingPoolNoAttempt(t *testing.T, f *fixture, r Reservation) {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM upstream_attempts WHERE request_id=$1", r.RequestID).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid pool membership inserted an attempt", count, err)
	}
	dispatchBatchFinancials(t, f, r, "accepted", "reserved")
}

func TestPostgresCredentialPoolRecordAttemptRequiresFrozenMemberAndDrains(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprint(published), func(t *testing.T) {
			f := integration(t)
			dispatchBatchSingleConnectionStore(t, f)
			secondary, nonmember := uuid.NewString(), uuid.NewString()
			version := billingPoolVersion(t, f, f.credential, secondary)
			var revision int64
			if published {
				revision = 1
				billingPoolPublish(t, f, version, revision)
			}
			r := billingPoolReserve(t, f, revision)
			// This credential belongs to the same channel, but a different
			// immutable version. It must not borrow the current pool's identity.
			_ = billingPoolVersion(t, f, f.credential, nonmember)
			for _, credential := range []string{nonmember, uuid.NewString()} {
				_, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, version, credential))
				assertCode(t, err, "invalid_attempt")
				billingPoolNoAttempt(t, f, r)
			}
			in := billingPoolAttemptInput(f, r, version, secondary)
			a, err := f.store.RecordAttempt(f.ctx, in)
			if err != nil || a.DeliveryState != "prepared" {
				t.Fatal("membership refusal left the single-connection batch busy", a, err)
			}
			var actualVersion, actualCredential string
			if err = f.pool.QueryRow(f.ctx, "SELECT channel_version_id,credential_id FROM upstream_attempts WHERE id=$1", a.ID).Scan(&actualVersion, &actualCredential); err != nil || actualVersion != version || actualCredential != secondary {
				t.Fatal("recorded attempt lost frozen secondary identity", actualVersion, actualCredential, err)
			}
			replayed, err := f.store.RecordAttempt(f.ctx, in)
			if err != nil || replayed.ID != a.ID {
				t.Fatal("same member attempt was not idempotent", replayed, err)
			}
			in.CredentialID = f.credential
			_, err = f.store.RecordAttempt(f.ctx, in)
			assertCode(t, err, "idempotency_conflict")
			dispatchBatchState(t, f, r, a, "prepared", "accepted")
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal("valid secondary could not reuse the business connection", err)
			}
			dispatchBatchState(t, f, r, a, "sending", "dispatching")
		})
	}
}

func TestPostgresCredentialPoolDispatchAllowsSecondaryAfterPrimaryRevoked(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	secondary := uuid.NewString()
	version := billingPoolVersion(t, f, f.credential, secondary)
	billingPoolPublish(t, f, version, 1)
	r := billingPoolReserve(t, f, 1)
	a, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, version, secondary))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", f.credential); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal("revoked primary prevented live secondary dispatch", err)
	}
	dispatchBatchState(t, f, r, a, "sending", "dispatching")
	var actual string
	if err = f.pool.QueryRow(f.ctx, "SELECT credential_id FROM upstream_attempts WHERE id=$1", a.ID).Scan(&actual); err != nil || actual != secondary {
		t.Fatal("dispatch replaced the actual secondary identity", actual, err)
	}
}

func TestPostgresCredentialPoolDispatchLocksActualSecondaryAndSeesRevocation(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	secondary := uuid.NewString()
	version := billingPoolVersion(t, f, f.credential, secondary)
	billingPoolPublish(t, f, version, 1)
	r := billingPoolReserve(t, f, 1)
	a, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, version, secondary))
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	var pid int
	if err = blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(f.ctx, "UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1", secondary); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
	// pg_blocking_pids proves the actual secondary's lock is the dispatch
	// barrier, rather than relying on a sleep or checking the primary's state.
	waitForBatchBlocker(t, f, pid)
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	assertCode(t, dispatchBatchAwait(t, done), "channel_unavailable")
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
	var primaryLive, secondaryRevoked bool
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT revoked_at IS NULL FROM channel_credentials WHERE id=$1),(SELECT revoked_at IS NOT NULL FROM channel_credentials WHERE id=$2)", f.credential, secondary).Scan(&primaryLive, &secondaryRevoked); err != nil || !primaryLive || !secondaryRevoked {
		t.Fatal("revocation fixture did not isolate the secondary", primaryLive, secondaryRevoked, err)
	}
	// Retrying preparation cannot bypass permanent revocation or leave a prior
	// batch's connection busy; it still never changes the attempt to sending.
	assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), "channel_unavailable")
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
}

func TestPostgresCredentialPoolFrozenVersionCannotBeReplacedByNewPool(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	secondary, replacement := uuid.NewString(), uuid.NewString()
	oldVersion := billingPoolVersion(t, f, f.credential, secondary)
	billingPoolPublish(t, f, oldVersion, 1)
	r := billingPoolReserve(t, f, 1)
	newVersion := billingPoolVersion(t, f, f.credential, replacement)
	// Even after a later complete route is published, the accepted request
	// keeps revision 1, its old version and that version's complete member set.
	billingPoolPublish(t, f, newVersion, 2)
	_, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, oldVersion, replacement))
	assertCode(t, err, "invalid_attempt")
	billingPoolNoAttempt(t, f, r)
	_, err = f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, newVersion, replacement))
	assertCode(t, err, "unpublished_binding")
	billingPoolNoAttempt(t, f, r)
	a, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, oldVersion, secondary))
	if err != nil {
		t.Fatal("new pool replaced a member of the accepted frozen version", err)
	}
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal("dispatch consulted current pool instead of frozen membership", err)
	}
	dispatchBatchState(t, f, r, a, "sending", "dispatching")
	var savedVersion, savedCredential string
	var savedRevision int64
	if err = f.pool.QueryRow(f.ctx, "SELECT a.channel_version_id,a.credential_id,r.route_revision FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id WHERE a.id=$1", a.ID).Scan(&savedVersion, &savedCredential, &savedRevision); err != nil || savedVersion != oldVersion || savedCredential != secondary || savedRevision != 1 {
		t.Fatal("new publication rewrote admitted identities", savedVersion, savedCredential, savedRevision, err)
	}
}
