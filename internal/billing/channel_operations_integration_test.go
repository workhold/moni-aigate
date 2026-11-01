package billing

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func billingChannelOperationInput(t *testing.T, f *fixture, action string) catalog.ChannelOperationInput {
	t.Helper()
	target := catalog.ChannelOperationTarget{ChannelID: f.channel}
	if err := f.pool.QueryRow(f.ctx, `SELECT current_version,management_revision::text FROM channels WHERE id=$1`, f.channel).Scan(&target.ExpectedVersion, &target.ExpectedRevision); err != nil {
		t.Fatal(err)
	}
	return catalog.ChannelOperationInput{Action: action, Targets: []catalog.ChannelOperationTarget{target}}
}

func billingApplyChannelOperation(t *testing.T, f *fixture, action string) catalog.ChannelOperationReceipt {
	t.Helper()
	result, err := catalog.New(f.pool, nil).ApplyChannelOperation(f.ctx, f.admin, uuid.NewString(), billingChannelOperationInput(t, f, action))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPostgresChannelOperationDisableBlocksPreparedDispatchAtActualLock(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	billingPoolPublish(t, f, f.version, 1)
	r := billingPoolReserve(t, f, 1)
	a := dispatchBatchPrepared(t, f, r)
	c := catalog.New(f.pool, nil)
	in := billingChannelOperationInput(t, f, "disable")
	operationID := uuid.NewString()
	// Pause the real batch operation after it has acquired the channel UPDATE
	// lock. The trigger is local to this disposable schema, and sends no HTTP.
	if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION pause_channel_operation_update() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF OLD.enabled AND NOT NEW.enabled THEN PERFORM pg_advisory_xact_lock(13314,1); END IF;
  RETURN NEW;
 END $$;
 CREATE TRIGGER zz_pause_channel_operation_update BEFORE UPDATE ON channels FOR EACH ROW EXECUTE FUNCTION pause_channel_operation_update()`); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	var blockerPID int
	if err = blocker.QueryRow(f.ctx, `SELECT pg_backend_pid() FROM pg_advisory_xact_lock(13314,1)`).Scan(&blockerPID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	type operationResult struct {
		receipt catalog.ChannelOperationReceipt
		err     error
	}
	operations := make(chan operationResult, 1)
	go func() {
		receipt, err := c.ApplyChannelOperation(ctx, f.admin, operationID, in)
		operations <- operationResult{receipt, err}
	}()
	waitForBatchBlocker(t, f, blockerPID)
	var operationPID int
	if err = f.pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&operationPID); err != nil {
		t.Fatal("did not identify the blocked batch operation backend", err)
	}
	dispatch := make(chan error, 1)
	go func() { dispatch <- f.store.PrepareDispatch(ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}) }()
	// This second observed waiter is PrepareDispatch waiting on the operation's
	// actual channel row lock, rather than merely waiting on our advisory lock.
	waitForBatchBlocker(t, f, operationPID)
	if err = blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var applied catalog.ChannelOperationReceipt
	select {
	case result := <-operations:
		if result.err != nil || result.receipt.Status != "applied" || result.receipt.ChangedCount != 1 {
			t.Fatal("real batch disable did not commit", result.receipt, result.err)
		}
		applied = result.receipt
	case <-ctx.Done():
		t.Fatal("batch operation remained blocked", ctx.Err())
	}
	assertCode(t, dispatchBatchAwait(t, dispatch), "channel_unavailable")
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
	var version, credential string
	var route int64
	if err = f.pool.QueryRow(f.ctx, `SELECT a.channel_version_id,a.credential_id,r.route_revision FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id WHERE a.id=$1`, a.ID).Scan(&version, &credential, &route); err != nil || version != f.version || credential != f.credential || route != 1 {
		t.Fatal("batch disable replaced the prepared snapshot", version, credential, route, err)
	}
	enabled := billingApplyChannelOperation(t, f, "enable")
	if enabled.Status != "applied" || enabled.ChangedCount != 1 {
		t.Fatal("batch enable did not restore the live gate", enabled)
	}
	beforeReplay := billingChannelOperationInput(t, f, "enable")
	replayed, err := c.ApplyChannelOperation(f.ctx, f.admin, operationID, in)
	if err != nil || !reflect.DeepEqual(applied, replayed) || !reflect.DeepEqual(beforeReplay, billingChannelOperationInput(t, f, "enable")) {
		t.Fatal("replaying a receipt performed another disable or revision update", replayed, err)
	}
	// No route publication or credential/pool rotation is needed to restore a
	// mutable enabled gate. The same previously prepared frozen attempt works.
	if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
		t.Fatal("batch enable did not restore the original prepared attempt", err)
	}
	dispatchBatchState(t, f, r, a, "sending", "dispatching")
	var attempts, finalizations int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM finalization_records WHERE request_id=$1)`, r.RequestID).Scan(&attempts, &finalizations); err != nil || attempts != 1 || finalizations != 0 {
		t.Fatal("status changes dispatched an extra attempt or fabricated usage", attempts, finalizations, err)
	}
}

func TestPostgresChannelOperationEnableCannotClearQuarantine(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	a := dispatchBatchPrepared(t, f, r)
	if _, err := f.pool.Exec(f.ctx, `UPDATE channels SET enabled=false,quarantined=true WHERE id=$1`, f.channel); err != nil {
		t.Fatal(err)
	}
	before := billingChannelOperationInput(t, f, "enable")
	receipt := billingApplyChannelOperation(t, f, "enable")
	if receipt.Status != "rejected" || receipt.ChangedCount != 0 || len(receipt.Items) != 1 || receipt.Items[0].Reason != "channel_quarantined" {
		t.Fatal("batch enable cleared or ignored channel quarantine", receipt)
	}
	if !reflect.DeepEqual(before, billingChannelOperationInput(t, f, "enable")) {
		t.Fatal("rejected batch mutated the channel revision or version")
	}
	var enabled, quarantined bool
	if err := f.pool.QueryRow(f.ctx, `SELECT enabled,quarantined FROM channels WHERE id=$1`, f.channel).Scan(&enabled, &quarantined); err != nil || enabled || !quarantined {
		t.Fatal("rejected enable changed quarantine flags", enabled, quarantined, err)
	}
	assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), "channel_unavailable")
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
}

func TestPostgresChannelOperationEnableDoesNotReviveFrozenRevokedCredential(t *testing.T) {
	f := integration(t)
	dispatchBatchSingleConnectionStore(t, f)
	secondary := uuid.NewString()
	version := billingPoolVersion(t, f, f.credential, secondary)
	billingPoolPublish(t, f, version, 1)
	r := billingPoolReserve(t, f, 1)
	a, err := f.store.RecordAttempt(f.ctx, billingPoolAttemptInput(f, r, version, f.credential))
	if err != nil {
		t.Fatal(err)
	}
	if receipt := billingApplyChannelOperation(t, f, "disable"); receipt.Status != "applied" || receipt.ChangedCount != 1 {
		t.Fatal(receipt)
	}
	var revokedAt time.Time
	if err = f.pool.QueryRow(f.ctx, `UPDATE channel_credentials SET revoked_at=clock_timestamp() WHERE id=$1 RETURNING revoked_at`, f.credential).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	// A live secondary permits enabling the channel, but does not grant an old
	// attempt permission to switch identity or undo primary revocation.
	receipt := billingApplyChannelOperation(t, f, "enable")
	if receipt.Status != "applied" || receipt.ChangedCount != 1 || receipt.Items[0].LiveCredentials != 1 {
		t.Fatal("live secondary could not enable the existing channel", receipt)
	}
	assertCode(t, f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}), "channel_unavailable")
	dispatchBatchState(t, f, r, a, "prepared", "accepted")
	var currentVersion, actualVersion, actualCredential string
	var stillRevoked time.Time
	var activeRevision int64
	if err = f.pool.QueryRow(f.ctx, `SELECT c.current_version,a.channel_version_id,a.credential_id,k.revoked_at,m.active_revision FROM upstream_attempts a JOIN channels c ON c.id=a.channel_id JOIN channel_credentials k ON k.id=a.credential_id JOIN request_records r ON r.id=a.request_id JOIN models m ON m.id=r.model_id WHERE a.id=$1`, a.ID).Scan(&currentVersion, &actualVersion, &actualCredential, &stillRevoked, &activeRevision); err != nil || currentVersion != version || actualVersion != version || actualCredential != f.credential || !stillRevoked.Equal(revokedAt) || activeRevision != 1 {
		t.Fatal("batch enable rewrote credential, version, revocation or route", currentVersion, actualVersion, actualCredential, stillRevoked, activeRevision, err)
	}
}
