package mockpsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestDeliveryObservationRequiresCurrentEventACKAndExactUnion(t *testing.T) {
	r, claim, _ := deliveryUnitRepository(t)
	raw, err := json.Marshal(ReceiptEnvelope{Data: ReceiptData{
		ReceiptID: uuid.NewString(), EventID: claim.eventID, Status: "received", Environment: "sandbox", ReceivedAt: timeText(claim.claimedAt),
	}, RequestID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	safe, err := ParseACK(202, raw, claim.eventID, claim.claimedAt)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := ACKObservation(safe)
	if err != nil {
		t.Fatal(err)
	}
	if !deliveryObservationValid(claim, ack) {
		t.Fatal("valid owned ACK union rejected")
	}
	for _, mutate := range []func(*Observation){
		func(o *Observation) { *o = Observation{} },
		func(o *Observation) { o.httpStatus = nil },
		func(o *Observation) { v := 204; o.httpStatus = &v },
		func(o *Observation) { o.eventID = "evt:another" },
		func(o *Observation) { o.errorCode = FailureTimeout },
		func(o *Observation) { o.receiptData = nil },
		func(o *Observation) { o.receiptData = make([]byte, 8193) },
	} {
		o := ack
		mutate(&o)
		if deliveryObservationValid(claim, o) {
			t.Fatal("incomplete, wrong-status or foreign ACK union accepted")
		}
		if result, err := r.RecordObservation(context.Background(), claim, o); result != 0 || ErrorCode(err) != "invalid_request" {
			t.Fatalf("invalid observation reached runtime/database: result=%d err=%v", result, err)
		}
	}
	for _, code := range []FailureCode{FailureNetwork, FailureTimeout, FailureAuth, FailureProtocol, FailureEnvironment, FailureTemporary, FailurePermanent} {
		failure, err := FailedObservation(code, nil)
		if err != nil || !deliveryObservationValid(claim, failure) {
			t.Fatal("controlled failure union rejected", code, err)
		}
		failure.receiptData = []byte("receipt")
		if deliveryObservationValid(claim, failure) {
			t.Fatal("failed IO retained ACK evidence")
		}
	}
}

func TestDeliveryObservationDuplicateComparisonUsesOnlyImmutableBusinessEvidence(t *testing.T) {
	status := 202
	a := Observation{outcome: "ack", httpStatus: &status, receiptData: []byte("canonical durable receipt data")}
	bStatus := status
	b := Observation{outcome: "ack", httpStatus: &bStatus, receiptData: append([]byte(nil), a.receiptData...)}
	if !sameDeliveryObservation(a, b) {
		t.Fatal("independent copies of the same evidence were considered different")
	}
	for _, mutate := range []func(*Observation){
		func(o *Observation) { o.outcome = "failed" },
		func(o *Observation) { o.httpStatus = nil },
		func(o *Observation) { v := 200; o.httpStatus = &v },
		func(o *Observation) { o.errorCode = FailureProtocol },
		func(o *Observation) { o.receiptData = []byte("other receipt") },
	} {
		changed := b
		mutate(&changed)
		if sameDeliveryObservation(a, changed) {
			t.Fatal("conflicting immutable IO evidence was treated as replay")
		}
	}
	a = Observation{outcome: "failed", errorCode: FailureTimeout}
	b = Observation{outcome: "failed", errorCode: FailureTimeout}
	if !sameDeliveryObservation(a, b) {
		t.Fatal("NULL-status failed observations cannot be replayed")
	}
}

func TestDeliveryObservationLeaseRaceRequiresExactExpiredLeaseAndKnownTriggerFailure(t *testing.T) {
	_, claim, _ := deliveryUnitRepository(t)
	triggerErr := &pgconn.PgError{Code: "P0001", Message: "untrusted trigger message"}
	for _, nextState := range []string{"delivered", "retry_wait"} {
		if !deliveryLeaseRaceCandidate(fmt.Errorf("wrapped: %w", triggerErr), nextState) {
			t.Fatal("known transition trigger failure was not eligible for exact lease recheck", nextState)
		}
	}
	for _, tc := range []struct {
		err       error
		nextState string
	}{
		{triggerErr, "review_required"},
		{triggerErr, "leased"},
		{&pgconn.PgError{Code: "40001"}, "delivered"},
		{&pgconn.PgError{Code: "08006"}, "retry_wait"},
		{errors.New("P0001: mock delivery transition"), "delivered"},
		{context.DeadlineExceeded, "delivered"},
		{nil, "delivered"},
	} {
		if deliveryLeaseRaceCandidate(tc.err, tc.nextState) {
			t.Fatal("ordinary, unknown or final-review failure was treated as an expired lease race", tc.err, tc.nextState)
		}
	}
	lease := claim.leaseUntil
	if !deliveryLeaseExpired("leased", claim.generation, &lease, lease, claim) ||
		!deliveryLeaseExpired("leased", claim.generation, &lease, lease.Add(time.Microsecond), claim) {
		t.Fatal("the exact expiry boundary was rejected")
	}
	otherLease := lease.Add(-time.Microsecond)
	for _, tc := range []struct {
		state      string
		generation int64
		lease      *time.Time
		now        time.Time
	}{
		{"leased", claim.generation, &lease, lease.Add(-time.Microsecond)},
		{"delivered", claim.generation, &lease, lease},
		{"review_required", claim.generation, &lease, lease},
		{"leased", claim.generation + 1, &lease, lease},
		{"leased", claim.generation, &otherLease, lease},
		{"leased", claim.generation, nil, lease},
	} {
		if deliveryLeaseExpired(tc.state, tc.generation, tc.lease, tc.now, claim) {
			t.Fatal("a different generation, lease, state or still-live claim authorized stale recovery")
		}
	}
}
