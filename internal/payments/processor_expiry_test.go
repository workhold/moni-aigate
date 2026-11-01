package payments

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProcessorMutationTenthUnknownNeverReclaimsEleventh(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET generation=9,attempt_count=9`)
	lease, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok || lease.binding.generation != 10 {
		t.Fatal(lease, ok, err)
	}
	work, err := processor.PrepareMutation(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.RecordMutation(f.ctx, lease, MutationResponse{seal: processor.seal, binding: work.binding, requestDigest: work.digest, failure: "network"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = processor.ClaimMutation(f.ctx); err != nil || ok {
		t.Fatal(ok, err)
	}
	var state, code string
	var supervisors int
	if err = f.worker.QueryRow(f.ctx, `SELECT b.state,b.last_error_code,(SELECT count(*) FROM payment_command_outbox WHERE original_outbox_id=b.id AND kind='query') FROM payment_command_outbox b WHERE b.id=$1`, lease.binding.id).Scan(&state, &code, &supervisors); err != nil || state != "submission_unknown" || code != "attempts_exhausted" || supervisors != 1 {
		t.Fatal(state, code, supervisors, err)
	}
}
func TestProcessorExpiryUsesOneHundredTotalAcrossKinds(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	for i := 0; i < 51; i++ {
		o := f.order(t)
		processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	}
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET state='leased',generation=1,attempt_count=1,lease_until=clock_timestamp()-interval '1 second'`)
	processorOwnerMutation(t, f, "payment_evidence_processing", `UPDATE payment_evidence_processing SET state='leased',generation=1,attempt_count=1,lease_until=clock_timestamp()-interval '1 second'`)
	if err := processor.ExpireLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	var expired int
	err := f.worker.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_command_outbox WHERE kind<>'query' AND state='leased')+(SELECT count(*) FROM payment_evidence_processing WHERE state='leased')`).Scan(&expired)
	if err != nil || expired != 2 {
		t.Fatal("total limit left", expired, err)
	}
}
func TestProcessorExpiryOrderFirstAvoidsSupervisorInsertCycle(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	lease, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	work, err := processor.PrepareMutation(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	// The advisory lock only pauses an isolated test trigger; the production
	// Processor itself never takes advisory locks or keeps a lease across IO.
	key := int64(time.Now().UnixNano()&0x7fffffff) + 1
	serviceExec(t, f.owner, f.ctx, `CREATE FUNCTION test_pause_supervisor() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='query' THEN PERFORM pg_advisory_xact_lock(`+strconv.FormatInt(key, 10)+`::bigint);END IF;RETURN NEW;END $$;CREATE TRIGGER z_test_pause_supervisor BEFORE INSERT ON payment_command_outbox FOR EACH ROW EXECUTE FUNCTION test_pause_supervisor()`)
	blocker, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	serviceExec(t, blocker, f.ctx, `SELECT pg_advisory_xact_lock($1)`, key)
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET lease_until=clock_timestamp()+interval '500 milliseconds' WHERE id=$1`, lease.binding.id)
	record := make(chan error, 1)
	go func() {
		record <- processor.RecordMutation(f.ctx, lease, MutationResponse{seal: processor.seal, binding: work.binding, requestDigest: work.digest, failure: "network"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		if err = f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1::oid AND NOT granted)`, key).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("Record did not enter supervisor insertion")
	}
	time.Sleep(550 * time.Millisecond)
	expiry := make(chan error, 1)
	go func() { expiry <- processor.ExpireLeases(f.ctx) }()
	time.Sleep(50 * time.Millisecond)
	if err = blocker.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-record; !errors.Is(err, ErrPaymentsLeaseLost) {
		t.Fatal("record", err)
	}
	if err = <-expiry; err != nil {
		t.Fatal("expiry", err)
	}
	var state string
	var supervisors int
	if err = f.worker.QueryRow(f.ctx, `SELECT b.state,(SELECT count(*) FROM payment_command_outbox WHERE original_outbox_id=b.id AND kind='query') FROM payment_command_outbox b WHERE b.id=$1`, lease.binding.id).Scan(&state, &supervisors); err != nil || state != "submission_unknown" || supervisors != 1 {
		t.Fatal(state, supervisors, err)
	}
}
