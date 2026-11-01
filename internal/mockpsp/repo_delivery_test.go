package mockpsp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func deliveryUnitRepository(t *testing.T) (*Repository, DeliveryClaim, []byte) {
	t.Helper()
	now := time.Date(2026, 11, 1, 0, 0, 10, 123456000, time.UTC)
	env := paymentproto.Environment{ID: uuid.NewString(), MerchantID: "merchant:test", PayeeLabel: "Sandbox QA"}
	before, after := now.Add(-time.Hour).Truncate(time.Second), now.Add(time.Hour).Truncate(time.Second)
	keys, err := paymentproto.NewKeyring("test-key", []paymentproto.KeySpec{{ID: "test-key", Key: bytes.Repeat([]byte{7}, 32), NotBefore: before, NotAfter: after}})
	if err != nil {
		t.Fatal(err)
	}
	r := &Repository{env: env, identity: &repositoryIdentity{marker: 1}, keyring: keys, activeKeyID: "test-key", keyWindow: func(id string) (time.Time, time.Time, bool) {
		return before, after, id == "test-key"
	}}
	transaction := "txn:" + uuid.NewString()
	eventID := "evt:" + uuid.NewString()
	raw, err := paymentproto.CanonicalNotification(paymentproto.Notification{SchemaVersion: 1, EnvironmentID: env.ID, MerchantID: env.MerchantID,
		EventID: eventID, EventType: "capture.confirmed", OrderID: uuid.NewString(), ProviderTransactionID: &transaction,
		Amount: "100", Currency: "CNY", OccurredAt: timeText(now), ProviderOrderRevision: "2"})
	if err != nil {
		t.Fatal(err)
	}
	claim := DeliveryClaim{repo: r.identity, envID: env.ID, eventRowID: uuid.NewString(), eventID: eventID, generation: 1,
		attemptID: uuid.NewString(), deliveryID: uuid.NewString(), deliveryTime: "1793491210", keyID: "test-key", bodyDigest: paymentproto.RequestDigest(raw),
		claimedAt: now, leaseUntil: now.Add(time.Minute)}
	return r, claim, raw
}

func TestDeliveryClaimRejectsZeroCrossRepositoryAndInvalidLeaseBeforeDB(t *testing.T) {
	r, valid, _ := deliveryUnitRepository(t)
	if !r.deliveryClaimValid(valid) {
		t.Fatal("valid private claim was rejected")
	}
	for _, mutate := range []func(*DeliveryClaim){
		func(c *DeliveryClaim) { *c = DeliveryClaim{} },
		func(c *DeliveryClaim) { c.repo = &repositoryIdentity{marker: 1} },
		func(c *DeliveryClaim) { c.envID = uuid.NewString() },
		func(c *DeliveryClaim) { c.generation = 0 },
		func(c *DeliveryClaim) { c.generation = 21 },
		func(c *DeliveryClaim) { c.attemptID = "" },
		func(c *DeliveryClaim) { c.leaseUntil = c.leaseUntil.Add(time.Microsecond) },
	} {
		claim := valid
		mutate(&claim)
		if r.deliveryClaimValid(claim) {
			t.Fatal("invalid or foreign claim was accepted")
		}
		if _, err := r.Prepare(context.Background(), claim); ErrorCode(err) != "invalid_request" {
			t.Fatalf("claim rejection accessed unavailable runtime instead of input boundary: %v", err)
		}
	}
}

func TestDeliveryBodyRequiresPersistedCanonicalEnvironmentAndDigest(t *testing.T) {
	r, claim, raw := deliveryUnitRepository(t)
	if err := r.deliveryBody(raw, claim.eventID, claim.bodyDigest); err != nil {
		t.Fatal(err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		raw             []byte
		eventID, digest string
	}{
		{formatted.Bytes(), claim.eventID, paymentproto.RequestDigest(formatted.Bytes())},
		{raw, "evt:another", claim.bodyDigest},
		{raw, claim.eventID, paymentproto.RequestDigest([]byte("another body"))},
		{[]byte(`{"schema_version":1}`), claim.eventID, paymentproto.RequestDigest([]byte(`{"schema_version":1}`))},
	} {
		if err := r.deliveryBody(tc.raw, tc.eventID, tc.digest); ErrorCode(err) != "mock_unavailable" {
			t.Fatalf("malformed retained body was repaired or accepted: %v", err)
		}
	}
	r.env.MerchantID = "merchant:another"
	if err := r.deliveryBody(raw, claim.eventID, claim.bodyDigest); ErrorCode(err) != "mock_unavailable" {
		t.Fatal("retained body crossed the pinned merchant boundary", err)
	}
}

func TestDeliveryKeyChecksExactAttemptSecondAndActiveVersion(t *testing.T) {
	r, claim, _ := deliveryUnitRepository(t)
	if err := r.deliveryKey(claim.keyID, claim.deliveryTime); err != nil {
		t.Fatal(err)
	}
	for _, seconds := range []string{"-1", "01793491210", "1793491210.0", "253402300800"} {
		if err := r.deliveryKey(claim.keyID, seconds); ErrorCode(err) != "mock_unavailable" {
			t.Fatal("noncanonical or unsupported attempt second accepted", seconds, err)
		}
	}
	for _, endpoint := range []string{"1793487610", "1793494810"} {
		if err := r.deliveryKey(claim.keyID, endpoint); err != nil {
			t.Fatal("closed key-window endpoint rejected", endpoint, err)
		}
	}
	for _, outside := range []string{"1793487609", "1793494811"} {
		if err := r.deliveryKey(claim.keyID, outside); ErrorCode(err) != "payment_key_unavailable" {
			t.Fatal("attempt second outside key window accepted", outside, err)
		}
	}
	r.activeKeyID = "rotated-key"
	if err := r.deliveryKey(claim.keyID, claim.deliveryTime); ErrorCode(err) != "payment_key_unavailable" {
		t.Fatal("old claim was allowed to sign with a different current key", err)
	}
}

func TestExpireFinalClaimsRejectsUnboundedLimitBeforeDB(t *testing.T) {
	var r *Repository
	for _, limit := range []int{-1, 0, 101, 1000000} {
		if n, err := r.ExpireFinalClaims(context.Background(), limit); n != 0 || ErrorCode(err) != "invalid_request" {
			t.Fatalf("unbounded maintenance limit admitted: limit=%d n=%d err=%v", limit, n, err)
		}
	}
}
