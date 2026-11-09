package billing

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresMediaReserveFinalizeUsesWalletLotsAndIsIdempotent(t *testing.T) {
	f := integration(t)
	requestID := uuid.NewString()
	attemptID := uuid.NewString()
	digest := sha256.Sum256([]byte("media-image-request"))
	in := MediaReserveInput{TenantID: f.tenant, UserID: f.user, KeyID: f.key, RequestID: requestID, AttemptID: attemptID, Operation: "image_generation", ModelKey: "mock-image-generation", ProfileVersion: "11111111-1111-4111-8111-111111111111", IdempotencyKey: "media-image-1", RequestDigest: digest[:], QuantityBound: 1, Deadline: time.Now().UTC().Add(time.Minute)}
	reserved, err := f.store.ReserveMedia(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if reserved.Amount != 1 || reserved.RatePerMillion != 1000000 || reserved.SupplierRatePerMillion != 500000 {
		t.Fatalf("unexpected media reservation %+v", reserved)
	}
	f.wallet(t, 999, 1)
	replayed, err := f.store.ReserveMedia(f.ctx, in)
	if err != nil || replayed.ID != reserved.ID {
		t.Fatalf("reservation replay=%+v err=%v", replayed, err)
	}
	f.wallet(t, 999, 1)

	settlement, err := f.store.FinalizeMedia(f.ctx, MediaFinalizationInput{TenantID: f.tenant, RequestID: requestID, AttemptID: attemptID, Source: "adapter", SourceEventID: uuid.NewString(), Operation: "image_generation", Outcome: "completed", UsageState: "confirmed_final", Quantity: 1})
	if err != nil {
		t.Fatal(err)
	}
	if settlement.Status != "settled" || settlement.ChargedAmount != 1 || settlement.SupplierCostStatus != "known" || settlement.SupplierCostAmount == nil || *settlement.SupplierCostAmount != 1 {
		t.Fatalf("unexpected media settlement %+v", settlement)
	}
	f.wallet(t, 999, 0)
	if _, err = f.store.FinalizeMedia(f.ctx, MediaFinalizationInput{TenantID: f.tenant, RequestID: requestID, AttemptID: attemptID, Source: "adapter", SourceEventID: uuid.NewString(), Operation: "image_generation", Outcome: "completed", UsageState: "confirmed_final", Quantity: 1}); err != nil {
		t.Fatal("settlement replay failed: ", err)
	}
	f.wallet(t, 999, 0)
	f.check(t)

	var reservations, settlements, observations, lots int
	if err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM media_reservations WHERE request_id=$1),
 (SELECT count(*) FROM media_settlements WHERE request_id=$1),
 (SELECT count(*) FROM media_observations WHERE request_id=$1),
 (SELECT count(*) FROM media_settlement_lots WHERE settlement_id=$2)`, requestID, settlement.ID).Scan(&reservations, &settlements, &observations, &lots); err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || settlements != 1 || observations != 1 || lots != 1 {
		t.Fatalf("media facts duplicated reservations=%d settlements=%d observations=%d lots=%d", reservations, settlements, observations, lots)
	}
}

func TestPostgresMediaUnknownFinalizationReleasesWithoutCharge(t *testing.T) {
	f := integration(t)
	requestID := uuid.NewString()
	attemptID := uuid.NewString()
	digest := sha256.Sum256([]byte("media-unknown"))
	reserved, err := f.store.ReserveMedia(f.ctx, MediaReserveInput{TenantID: f.tenant, UserID: f.user, KeyID: f.key, RequestID: requestID, AttemptID: attemptID, Operation: "tts", ModelKey: "mock-tts", ProfileVersion: "22222222-2222-4222-8222-222222222222", IdempotencyKey: "media-tts-unknown", RequestDigest: digest[:], QuantityBound: 4, Deadline: time.Now().UTC().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 996, 4)
	settlement, err := f.store.FinalizeMedia(f.ctx, MediaFinalizationInput{TenantID: f.tenant, RequestID: requestID, AttemptID: attemptID, Source: "adapter", SourceEventID: uuid.NewString(), Operation: "tts", Outcome: "unknown", UsageState: "unknown", Quantity: 0})
	if err != nil {
		t.Fatal(err)
	}
	if settlement.Status != "released" || settlement.ChargedAmount != 0 || settlement.RawAmount != "0" || settlement.SupplierCostStatus != "unknown" || settlement.SupplierCostAmount != nil {
		t.Fatalf("unexpected release %+v", settlement)
	}
	f.wallet(t, 1000, 0)
	if _, err = f.pool.Exec(f.ctx, `SELECT 1 FROM media_reservations WHERE id=$1`, reserved.ID); err != nil {
		t.Fatal(err)
	}
	f.check(t)
}

func TestMediaRequestDigestConflictIsRejected(t *testing.T) {
	f := integration(t)
	requestID := uuid.NewString()
	attemptID := uuid.NewString()
	first := sha256.Sum256(bytes.Repeat([]byte{'a'}, 8))
	in := MediaReserveInput{TenantID: f.tenant, UserID: f.user, KeyID: f.key, RequestID: requestID, AttemptID: attemptID, Operation: "image_generation", ModelKey: "mock-image-generation", IdempotencyKey: "media-conflict", RequestDigest: first[:], QuantityBound: 1, Deadline: time.Now().UTC().Add(time.Minute)}
	if _, err := f.store.ReserveMedia(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	second := sha256.Sum256(bytes.Repeat([]byte{'b'}, 8))
	in.RequestID, in.AttemptID, in.RequestDigest = uuid.NewString(), uuid.NewString(), second[:]
	if _, err := f.store.ReserveMedia(f.ctx, in); err == nil {
		t.Fatal("media idempotency digest conflict was accepted")
	}
}
