package mockpsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Each case owns a complete attested QA environment. These are repository
// tests, not evidence of HTTP delivery, process recovery or twenty real claims.
func callbackPGCapture(t *testing.T, r *Repository, ctx context.Context) string {
	t.Helper()
	in := repositoryPGCheckout(t, r, ctx, "100")
	out, err := r.Checkout(ctx, in)
	if err != nil || out.Body.request.Result == nil || out.Body.request.Result.Status != "ready" {
		t.Fatalf("checkout did not create a ready order: code=%s", ErrorCode(err))
	}
	control := repositoryPGControl(t, r, ctx, ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: strptr(in.request.MerchantOrderID)})
	if control.Reply.Status != "accepted" || control.Reply.ResourceID == nil {
		t.Fatal("normal capture did not persist accepted control")
	}
	var eventID string
	if err = r.pool.QueryRow(ctx, `SELECT event_id FROM mock_notification_events WHERE canonical_capture_id=$1`, *control.Reply.ResourceID).Scan(&eventID); err != nil {
		t.Fatal("canonical capture has no readable notification")
	}
	return eventID
}

func callbackPGACK(t *testing.T, r *Repository, ctx context.Context, eventID string) Observation {
	t.Helper()
	now, err := r.ObserveReceivedAt(ctx)
	if err != nil {
		t.Fatalf("DB response observation failed: code=%s", ErrorCode(err))
	}
	raw, err := json.Marshal(ReceiptEnvelope{Data: ReceiptData{
		ReceiptID: uuid.NewString(), EventID: eventID, Status: "received", Environment: "sandbox", ReceivedAt: timeText(now),
	}, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal("safe receipt encoding failed")
	}
	safe, err := ParseACK(202, raw, eventID, now)
	if err != nil {
		t.Fatalf("safe receipt parsing failed: code=%s", ErrorCode(err))
	}
	o, err := ACKObservation(safe)
	if err != nil {
		t.Fatalf("ACK observation construction failed: code=%s", ErrorCode(err))
	}
	return o
}

func TestRepositoryDeliveryPGConcurrentClaimPrepareAndACK(t *testing.T) {
	r, env, _, ctx := newRepositoryPG(t)
	eventID := callbackPGCapture(t, r, ctx)
	type result struct {
		claim DeliveryClaim
		found bool
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			claim, found, err := r.ClaimDue(ctx)
			results <- result{claim: claim, found: found, err: err}
		}()
	}
	close(start)
	var claim DeliveryClaim
	claimed := 0
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent claim failed: code=%s", ErrorCode(got.err))
		}
		if got.found {
			claim, claimed = got.claim, claimed+1
		}
	}
	if claimed != 1 || claim.eventID != eventID || claim.generation != 1 {
		t.Fatal("two workers did not produce exactly one first-generation claim")
	}
	prepared, err := r.Prepare(ctx, claim)
	if err != nil {
		t.Fatalf("prepare failed: code=%s", ErrorCode(err))
	}
	body, err := prepared.BodyBytes()
	if err != nil {
		t.Fatal("prepared body was unavailable")
	}
	headers, err := prepared.Headers()
	if err != nil {
		t.Fatal("prepared signed headers were unavailable")
	}
	var retained []byte
	if err = r.pool.QueryRow(ctx, `SELECT canonical_body FROM mock_notification_events WHERE event_id=$1`, eventID).Scan(&retained); err != nil || !bytes.Equal(body, retained) {
		t.Fatal("prepare changed the retained canonical business body")
	}
	keys, err := env.Keyring()
	if err != nil {
		t.Fatal("independent QA verifier configuration failed")
	}
	now, err := r.ObserveReceivedAt(ctx)
	if err != nil {
		t.Fatal("signature verification DB clock failed")
	}
	verified, err := keys.VerifyNotification(env.Protocol, now, headers, body)
	if err != nil {
		t.Fatal("prepared signature was rejected by independent P verifier")
	}
	canonical, err := verified.Canonical()
	if err != nil || !bytes.Equal(body, canonical) {
		t.Fatal("verified notification did not retain the exact business body")
	}
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] ^= 1
	if _, err = keys.VerifyNotification(env.Protocol, now, headers, tampered); err == nil {
		t.Fatal("notification HMAC accepted altered business bytes")
	}
	ack := callbackPGACK(t, r, ctx, eventID)
	if result, err := r.RecordObservation(ctx, claim, ack); err != nil || result != CurrentRecorded {
		t.Fatalf("current ACK was not recorded: outcome=%d code=%s", result, ErrorCode(err))
	}
	if result, err := r.RecordObservation(ctx, claim, ack); err != nil || result != AlreadyRecorded {
		t.Fatalf("exact ACK replay was not idempotent: outcome=%d code=%s", result, ErrorCode(err))
	}
	conflicting := callbackPGACK(t, r, ctx, eventID)
	if result, err := r.RecordObservation(ctx, claim, conflicting); result != 0 || ErrorCode(err) != "mock_unavailable" {
		t.Fatalf("conflicting immutable ACK was admitted: outcome=%d code=%s", result, ErrorCode(err))
	}
	var state string
	var attempts, observations int
	if err = r.pool.QueryRow(ctx, `SELECT d.state,d.attempt_count,
 (SELECT count(*) FROM mock_delivery_observations x WHERE x.event_id=d.event_id)
 FROM mock_notification_delivery d WHERE d.event_id=$1`, claim.eventRowID).Scan(&state, &attempts, &observations); err != nil || state != "delivered" || attempts != 1 || observations != 1 {
		t.Fatal("delivered state and unique observation were not durable")
	}
	if _, found, err := r.ClaimDue(ctx); err != nil || found {
		t.Fatalf("delivered event became claimable: found=%t code=%s", found, ErrorCode(err))
	}
}

func TestRepositoryDeliveryPGFailureBackoffAndSecondClaim(t *testing.T) {
	r, _, _, ctx := newRepositoryPG(t)
	eventID := callbackPGCapture(t, r, ctx)
	first, found, err := r.ClaimDue(ctx)
	if err != nil || !found || first.eventID != eventID || first.generation != 1 {
		t.Fatalf("first claim failed: found=%t code=%s", found, ErrorCode(err))
	}
	failure, err := FailedObservation(FailureNetwork, nil)
	if err != nil {
		t.Fatal("controlled network failure construction failed")
	}
	if result, err := r.RecordObservation(ctx, first, failure); err != nil || result != CurrentRecorded {
		t.Fatalf("first failure was not current: outcome=%d code=%s", result, ErrorCode(err))
	}
	var state, lastError string
	var attempts int
	var due, dbAt, observedAt time.Time
	if err = r.pool.QueryRow(ctx, `SELECT state,last_error_code,attempt_count,next_attempt_at,clock_timestamp()
	 ,(SELECT observed_at FROM mock_delivery_observations x WHERE x.event_id=d.event_id AND x.generation=1)
 FROM mock_notification_delivery d WHERE event_id=$1`, first.eventRowID).Scan(&state, &lastError, &attempts, &due, &dbAt, &observedAt); err != nil || state != "retry_wait" || lastError != "network" || attempts != 1 || due.Sub(observedAt) < time.Second {
		t.Fatal("failed generation did not persist the real one-second first backoff")
	}
	// Wait the actual database backoff without mutating its deadline or clock.
	ready := time.NewTimer(due.Sub(dbAt) + 25*time.Millisecond)
	defer ready.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("test context expired waiting for the first retry")
	case <-ready.C:
	}
	second, found, err := r.ClaimDue(ctx)
	if err != nil || !found || second.generation != 2 || second.eventID != eventID {
		t.Fatalf("due second generation was not claimed: found=%t code=%s", found, ErrorCode(err))
	}
	if second.attemptID == first.attemptID || second.deliveryID == first.deliveryID || second.bodyDigest != first.bodyDigest || !second.claimedAt.After(first.claimedAt) {
		t.Fatal("retry did not preserve its business body with new attempt identities")
	}
	ack := callbackPGACK(t, r, ctx, eventID)
	if result, err := r.RecordObservation(ctx, second, ack); err != nil || result != CurrentRecorded {
		t.Fatalf("second-generation ACK failed: outcome=%d code=%s", result, ErrorCode(err))
	}
	if result, err := r.RecordObservation(ctx, first, failure); err != nil || result != AlreadyRecorded {
		t.Fatalf("old exact failure replay changed its evidence: outcome=%d code=%s", result, ErrorCode(err))
	}
	var observationCount int
	if err = r.pool.QueryRow(ctx, `SELECT state,attempt_count,
 (SELECT count(*) FROM mock_delivery_observations x WHERE x.event_id=d.event_id)
 FROM mock_notification_delivery d WHERE event_id=$1`, first.eventRowID).Scan(&state, &attempts, &observationCount); err != nil || state != "delivered" || attempts != 2 || observationCount != 2 {
		t.Fatal("two real claims and their unique evidence were not retained")
	}
}

func TestRepositoryDeliveryPGBeforeSendFaultAndLiveArmRejection(t *testing.T) {
	r, _, _, ctx := newRepositoryPG(t)
	eventID := callbackPGCapture(t, r, ctx)
	armed := repositoryPGControl(t, r, ctx, ControlInput{Action: "arm_fault", Scenario: "before_callback_send_once", EventID: strptr(eventID)})
	if armed.Reply.Status != "accepted" {
		t.Fatal("pending callback fault was not armed")
	}
	claim, found, err := r.ClaimDue(ctx)
	if err != nil || !found || claim.eventID != eventID {
		t.Fatalf("armed callback was not claimed: found=%t code=%s", found, ErrorCode(err))
	}
	prepared, err := r.Prepare(ctx, claim)
	if !errors.Is(err, ErrDeliveryDeferred) {
		t.Fatalf("before-send fault did not defer IO: code=%s", ErrorCode(err))
	}
	if _, err = prepared.BodyBytes(); ErrorCode(err) != "invalid_request" {
		t.Fatal("deferred prepare returned an authorized body")
	}
	var remaining, attempts, observations int
	var consumed bool
	var state string
	if err = r.pool.QueryRow(ctx, `SELECT p.remaining,p.consumed_at IS NOT NULL,d.state,d.attempt_count,
 (SELECT count(*) FROM mock_delivery_observations x WHERE x.event_id=d.event_id)
 FROM mock_fault_plans p JOIN mock_notification_events n ON n.event_id=p.target_id
 JOIN mock_notification_delivery d ON d.event_id=n.id WHERE p.control_operation_id=$1`, armed.Reply.OperationID).Scan(&remaining, &consumed, &state, &attempts, &observations); err != nil || remaining != 0 || !consumed || state != "leased" || attempts != 1 || observations != 0 {
		t.Fatal("deferred IO did not independently consume the plan while preserving its lease and budget")
	}
	// The first plan is already consumed, so this refusal proves live-lease
	// reachability, rather than the unique-active-plan constraint.
	secondArm := repositoryPGControl(t, r, ctx, ControlInput{Action: "arm_fault", Scenario: "before_callback_send_once", EventID: strptr(eventID)})
	if secondArm.Reply.Status != "rejected" || secondArm.Reply.Reason != "state_conflict" || secondArm.Reply.ResourceID != nil {
		t.Fatal("a live claimed callback accepted a new before-send plan")
	}
	var plans int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM mock_fault_plans WHERE target_kind='callback' AND target_id=$1`, eventID).Scan(&plans); err != nil || plans != 1 {
		t.Fatal("rejected live-lease arm persisted a second plan")
	}
	if _, found, err = r.ClaimDue(ctx); err != nil || found {
		t.Fatalf("deferred live lease was immediately reclaimed: found=%t code=%s", found, ErrorCode(err))
	}
}

// Multi-connection contention is deliberately independent of the network-slot
// test's single-connection scheduling fixture. A real holder makes Prepare's
// try-lock fail; the durable claim must remain untouched and cannot authorize IO.
func TestRepositoryDeliveryPGPrepareContentionPreservesLease(t *testing.T) {
	r, _, _, ctx := newRepositoryPG(t)
	if r.pool.Config().MaxConns < 2 {
		t.Fatal("standard runtime must retain its multi-connection pool")
	}
	eventID := callbackPGCapture(t, r, ctx)
	claim, found, err := r.ClaimDue(ctx)
	if err != nil || !found || claim.eventID != eventID || claim.generation != 1 {
		t.Fatalf("first claim unavailable: found=%t code=%s", found, ErrorCode(err))
	}
	type snapshot struct {
		delivery, attempt                   string
		state                               string
		generation                          int64
		attempts, attemptRows, observations int
		lease                               time.Time
	}
	readSnapshot := func() snapshot {
		t.Helper()
		var s snapshot
		err := r.pool.QueryRow(ctx, `SELECT row_to_json(d)::text,row_to_json(a)::text,d.state,d.generation,d.attempt_count,d.lease_until,
   (SELECT count(*) FROM mock_delivery_attempts x WHERE x.event_id=d.event_id),
   (SELECT count(*) FROM mock_delivery_observations x WHERE x.event_id=d.event_id)
   FROM mock_notification_delivery d JOIN mock_delivery_attempts a ON a.event_id=d.event_id AND a.generation=d.generation WHERE d.event_id=$1`, claim.eventRowID).Scan(&s.delivery, &s.attempt, &s.state, &s.generation, &s.attempts, &s.lease, &s.attemptRows, &s.observations)
		if err != nil {
			t.Fatal("durable contention snapshot unavailable")
		}
		return s
	}
	before := readSnapshot()
	if before.state != "leased" || before.generation != 1 || before.attempts != 1 || before.attemptRows != 1 || before.observations != 0 || !before.lease.Equal(claim.leaseUntil) {
		t.Fatal("unexpected initial durable claim")
	}
	holder, err := r.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("independent actual LOGIN holder unavailable")
	}
	holderActive := true
	held := false
	key := lockHash(r.env.ID, lockKind(lockCallback), eventID)
	t.Cleanup(func() {
		if !holderActive {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if held {
			var unlocked bool
			if holder.QueryRow(cleanup, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, lockCallback, key).Scan(&unlocked) != nil || !unlocked {
				_ = holder.Conn().PgConn().Conn().Close()
				_ = holder.Hijack().Close(cleanup)
				holderActive = false
				t.Error("exact contention gate cleanup failed")
				return
			}
		}
		holder.Release()
		holderActive = false
	})
	var actualCurrent, actualSession string
	var holderPID uint32
	if holder.QueryRow(ctx, `SELECT current_user::text,session_user::text,pg_backend_pid()`).Scan(&actualCurrent, &actualSession, &holderPID) != nil || actualCurrent != actualSession || actualCurrent != holder.Conn().Config().User || holderPID == 0 {
		t.Fatal("holder is not the actual runtime LOGIN")
	}
	if holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1::integer,$2::integer)`, lockCallback, key).Scan(&held) != nil || !held {
		t.Fatal("test did not actually acquire its callback-target gate")
	}
	var exactLocks int
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND locktype='advisory' AND mode='ExclusiveLock' AND granted AND classid=$2::oid AND objid=$3::oid AND objsubid=2`, holderPID, uint32(lockCallback), uint32(key)).Scan(&exactLocks) != nil || exactLocks != 1 {
		t.Fatal("independent holder gate was not physically observed")
	}
	prepared, err := r.Prepare(ctx, claim)
	if !errors.Is(err, ErrDeliveryDeferred) {
		t.Fatalf("held callback gate did not defer Prepare: code=%s", ErrorCode(err))
	}
	if _, err = prepared.BodyBytes(); ErrorCode(err) != "invalid_request" {
		t.Fatal("deferred Prepare returned a body")
	}
	if _, err = prepared.Headers(); ErrorCode(err) != "invalid_request" {
		t.Fatal("deferred Prepare returned signed headers")
	}
	if _, err = prepared.EventID(); ErrorCode(err) != "invalid_request" {
		t.Fatal("deferred Prepare returned an IO identity")
	}
	after := readSnapshot()
	if before.delivery != after.delivery || before.attempt != after.attempt || after.attemptRows != before.attemptRows || after.observations != 0 {
		t.Fatal("gate contention mutated the original lease/attempt or fabricated IO evidence")
	}
	var unlocked bool
	if holder.QueryRow(ctx, `SELECT pg_advisory_unlock($1::integer,$2::integer)`, lockCallback, key).Scan(&unlocked) != nil || !unlocked {
		t.Fatal("exact holder unlock failed")
	}
	held = false
	if r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND classid=$2::oid AND objid=$3::oid AND objsubid=2`, holderPID, uint32(lockCallback), uint32(key)).Scan(&exactLocks) != nil || exactLocks != 0 {
		t.Fatal("holder gate survived explicit unlock")
	}
	holder.Release()
	holderActive = false
	if _, found, err = r.ClaimDue(ctx); err != nil || found {
		t.Fatalf("deferred live claim was immediately reclaimed: found=%t code=%s", found, ErrorCode(err))
	}
	final := readSnapshot()
	if final.delivery != before.delivery || final.attempt != before.attempt || final.attemptRows != before.attemptRows || final.observations != 0 {
		t.Fatal("live-lease recovery changed the preserved claim")
	}
}
