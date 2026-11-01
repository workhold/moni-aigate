package payments

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// This fixture deliberately injects the package-private runtime after the
// actual LOGIN/ACL fixture. It tests SQL/P/funding, not constructor attestation.
func processorFixture(t *testing.T, f *serviceFixture) *Processor {
	t.Helper()
	life, cancel := context.WithCancel(context.Background())
	out := &Processor{pool: f.worker, runtime: &paymentRuntime{pool: f.worker, options: f.workerOptions}, funding: billing.New(f.worker), seal: &processorSeal{1}, lifetime: life, cancel: cancel, closeDone: make(chan struct{})}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if e := out.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return out
}
func processorOrderFixture(t *testing.T, f *serviceFixture) OrderView {
	t.Helper()
	id := uuid.NewString()
	result, err := f.service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: id, ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if err != nil || result.Order == nil {
		t.Fatalf("create order: %+v %v", result, err)
	}
	return *result.Order
}
func processorNow(t *testing.T, f *serviceFixture) time.Time {
	t.Helper()
	var now time.Time
	if e := f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		t.Fatal(e)
	}
	return now.UTC()
}
func processorNotificationFixture(t *testing.T, f *serviceFixture, o OrderView, transaction, event, kind string, amount int64) NotificationInput {
	t.Helper()
	now := processorNow(t, f)
	n := p.Notification{SchemaVersion: 1, EventID: event, EventType: kind, EnvironmentID: f.env.ID, MerchantID: f.env.MerchantID, OrderID: o.ID, ProviderTransactionID: &transaction, Amount: processorInt(amount), Currency: "CNY", OccurredAt: processorTime(now), ProviderOrderRevision: "2"}
	raw, e := p.CanonicalNotification(n)
	if e != nil {
		t.Fatal(e)
	}
	headers, e := f.keyring.SignNotification(f.env, p.SignedHeaders{KeyID: f.keyring.ActiveID(), DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if e != nil {
		t.Fatal(e)
	}
	return NotificationInput{Headers: headers, Body: raw}
}
func processorReceive(t *testing.T, f *serviceFixture, in NotificationInput) string {
	t.Helper()
	ack, _, err := f.service.ReceiveNotification(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var evidence string
	if err = f.app.QueryRow(f.ctx, `SELECT e.id FROM payment_verified_evidence e JOIN payment_inbox i ON i.id=e.inbox_id WHERE i.id=$1`, ack.ReceiptID).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}
func processorQuerySigned(t *testing.T, f *serviceFixture, w QueryWork, state string) (p.SignedHeaders, []byte) {
	t.Helper()
	q := w.request
	r := p.QueryReply{SchemaVersion: 1, QueryID: q.QueryID, EnvironmentID: q.EnvironmentID, MerchantID: q.MerchantID, MerchantOrderID: q.MerchantOrderID, CommandID: q.CommandID, CommandKind: q.CommandKind, ParameterDigest: q.ParameterDigest, CommandState: state}
	now := processorNow(t, f)
	raw, e := p.CanonicalQueryReply(r)
	if e != nil {
		t.Fatal(e)
	}
	headers, e := f.keyring.SignQuery(f.env, q, p.SignedHeaders{KeyID: f.keyring.ActiveID(), DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if e != nil {
		t.Fatal(e)
	}
	return headers, raw
}
func processorUnknown(t *testing.T, f *serviceFixture, p *Processor) MutationLease {
	t.Helper()
	l, ok, e := p.ClaimMutation(f.ctx)
	if e != nil || !ok {
		t.Fatal("claim mutation", ok, e)
	}
	w, e := p.PrepareMutation(f.ctx, l)
	if e != nil {
		t.Fatal(e)
	}
	e = p.RecordMutation(f.ctx, l, MutationResponse{seal: p.seal, binding: w.binding, requestDigest: w.digest, failure: "network"})
	if e != nil {
		t.Fatal(e)
	}
	return l
}

func TestProcessorOpaqueWorkRejection(t *testing.T) {
	p1, p2 := &Processor{seal: &processorSeal{1}}, &Processor{seal: &processorSeal{1}}
	if _, e := p1.PrepareMutation(context.Background(), MutationLease{}); !errors.Is(e, ErrPaymentsInvalidWork) {
		t.Fatal(e)
	}
	lease := MutationLease{binding: leaseBinding{seal: p2.seal, id: uuid.NewString(), kind: "checkout", generation: 1, until: time.Now().Add(time.Minute)}}
	if _, e := p1.PrepareMutation(context.Background(), lease); !errors.Is(e, ErrPaymentsInvalidWork) {
		t.Fatal(e)
	}
	if e := p1.RecordQuery(context.Background(), QueryWork{}, p.SignedHeaders{}, nil); !errors.Is(e, ErrPaymentsInvalidWork) {
		t.Fatal(e)
	}
	if _, _, f, e := p1.sendQuery(context.Background(), QueryWork{}); !errors.Is(e, ErrPaymentsInvalidWork) || f != nil {
		t.Fatal(f, e)
	}
}
func TestProcessorActualWorkerCaptureDedupAndRestore(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := processorOrderFixture(t, f)
	in := processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", int64(o.Amount))
	evidence := processorReceive(t, f, in)
	lease, ok, err := processor.ClaimEvidence(f.ctx)
	if err != nil || !ok || lease.binding.id != evidence {
		t.Fatal(ok, err)
	}
	result, err := processor.ProcessEvidence(f.ctx, lease)
	if err != nil || result.Outcome != "credited" || result.CaptureID == nil {
		t.Fatal(result, err)
	}
	again, err := processor.ProcessEvidence(f.ctx, lease)
	if err != nil || again.Outcome != "existing" || again.CaptureID == nil || *again.CaptureID != *result.CaptureID {
		t.Fatal(again, err)
	}
	var count int
	var balance int64
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM journals WHERE kind='sandbox_capture'),balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'`, f.tenant).Scan(&count, &balance); err != nil || count != 1 || balance != int64(o.Amount) {
		t.Fatal(count, balance, err)
	}
	// A second signed event for the same transaction is a separate evidence
	// claim, but must resolve to the exact original funding graph.
	n, err := p.ParseNotification(in.Body)
	if err != nil {
		t.Fatal(err)
	}
	n.EventID = "event:" + uuid.NewString()
	raw, err := p.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	now := processorNow(t, f)
	h, err := f.keyring.SignNotification(f.env, p.SignedHeaders{KeyID: f.keyring.ActiveID(), DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if err != nil {
		t.Fatal(err)
	}
	processorReceive(t, f, NotificationInput{Headers: h, Body: raw})
	l2, ok, err := processor.ClaimEvidence(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	r2, err := processor.ProcessEvidence(f.ctx, l2)
	if err != nil || r2.Outcome != "existing" || *r2.CaptureID != *result.CaptureID {
		t.Fatal(r2, err)
	}
}
func TestProcessorQueryOriginalSignatureAndUnknownReuse(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	processorOrderFixture(t, f)
	processorUnknown(t, f, processor)
	lease, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w, err := processor.PrepareQuery(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err = processor.RecordQuery(f.ctx, w, p.SignedHeaders{}, []byte(`{}`)); !errors.Is(err, ErrPaymentsProtocol) {
		t.Fatal(err)
	}
	var originals int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM payment_verified_queries`).Scan(&originals); err != nil || originals != 0 {
		t.Fatal(originals, err)
	}
	time.Sleep(1100 * time.Millisecond)
	next, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w2, err := processor.PrepareQuery(f.ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if w2.child != w.child || w2.createdGeneration != w.createdGeneration || next.binding.generation == lease.binding.generation {
		t.Fatal("unknown query did not reuse original child")
	}
	headers, raw := processorQuerySigned(t, f, w2, "not_seen")
	if err = processor.RecordQuery(f.ctx, w2, headers, raw); err != nil {
		t.Fatal(err)
	}
	var mutationState, queryState string
	if err = f.app.QueryRow(f.ctx, `SELECT b.state,s.state FROM payment_command_outbox s JOIN payment_command_outbox b ON b.id=s.original_outbox_id WHERE s.id=$1`, w2.binding.id).Scan(&mutationState, &queryState); err != nil || mutationState != "retry_wait" || queryState != "retry_wait" {
		t.Fatal(mutationState, queryState, err)
	}
	// A stale authentic snapshot can be retained, but cannot ACK the new claim.
	if err = processor.RecordQuery(f.ctx, w, headers, raw); !errors.Is(err, ErrPaymentsLeaseLost) {
		t.Fatal(err)
	}
}
func TestProcessorConcurrentClaimsAreDistinct(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	for i := 0; i < 6; i++ {
		processorOrderFixture(t, f)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, ok, e := processor.ClaimMutation(f.ctx)
			if e != nil {
				errs <- e
				return
			}
			if ok {
				ids <- l.binding.id
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate claim")
		}
		seen[id] = true
	}
	if len(seen) != 6 {
		t.Fatal("claimed", len(seen))
	}
}
