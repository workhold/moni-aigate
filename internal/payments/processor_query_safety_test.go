package payments

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func processorSignedReply(t *testing.T, f *serviceFixture, w QueryWork, reply p.QueryReply) (p.SignedHeaders, []byte) {
	t.Helper()
	raw, err := p.CanonicalQueryReply(reply)
	if err != nil {
		t.Fatal(err)
	}
	now := processorNow(t, f)
	headers, err := f.keyring.SignQuery(f.env, w.request, p.SignedHeaders{KeyID: f.keyring.ActiveID(), DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return headers, raw
}
func processorBaseReply(w QueryWork, state string) p.QueryReply {
	q := w.request
	return p.QueryReply{SchemaVersion: 1, QueryID: q.QueryID, EnvironmentID: q.EnvironmentID, MerchantID: q.MerchantID, MerchantOrderID: q.MerchantOrderID, CommandID: q.CommandID, CommandKind: q.CommandKind, ParameterDigest: q.ParameterDigest, CommandState: state}
}
func TestProcessorSignedCompletedQueryConfirmsWithoutCommandObservation(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorUnknown(t, f, processor)
	l, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w, err := processor.PrepareQuery(f.ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	reply := processorBaseReply(w, "completed")
	revision, ref := "1", "checkout:"+uuid.NewString()
	reply.ProviderOrderRevision = &revision
	reply.Result = &p.CommandResult{Status: "ready", CheckoutRef: &ref}
	h, raw := processorSignedReply(t, f, w, reply)
	if err = processor.RecordQuery(f.ctx, w, h, raw); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, o.ID)
	if err != nil || view.CreationState != "confirmed" || view.CheckoutRef == nil || *view.CheckoutRef != ref {
		t.Fatal(view, err)
	}
	var observations int
	if err = f.app.QueryRow(f.ctx, `SELECT count(*) FROM payment_command_observations`).Scan(&observations); err != nil || observations != 0 {
		t.Fatal(observations, err)
	}
}
func TestProcessorStaleSignedCapturePersistsWithoutACKAndCredits(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorUnknown(t, f, processor)
	l, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w, err := processor.PrepareQuery(f.ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, l.binding.id)
	if err = processor.ExpireLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	newer, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w2, err := processor.PrepareQuery(f.ctx, newer)
	if err != nil || w2.child != w.child {
		t.Fatal(w2, err)
	}
	reply := processorBaseReply(w, "completed")
	revision := "2"
	reply.ProviderOrderRevision = &revision
	reply.Result = &p.CommandResult{Status: "captured", Capture: &p.CaptureFact{ProviderTransactionID: "tx:" + uuid.NewString(), Amount: "100", Currency: "CNY", OccurredAt: processorTime(processorNow(t, f)), ProviderOrderRevision: revision}}
	h, raw := processorSignedReply(t, f, w, reply)
	if err = processor.RecordQuery(f.ctx, w, h, raw); !errors.Is(err, ErrPaymentsLeaseLost) {
		t.Fatal(err)
	}
	var state string
	var generation int64
	if err = f.app.QueryRow(f.ctx, `SELECT state,generation FROM payment_command_outbox WHERE id=$1`, newer.binding.id).Scan(&state, &generation); err != nil || state != "leased" || generation != newer.binding.generation {
		t.Fatal(state, generation, err)
	}
	_, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "credited" {
		t.Fatal(r)
	}
	view, err := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, o.ID)
	if err != nil || view.FundingState != "credited" || view.FundingReceiptID == nil || view.JournalID == nil || view.WalletCreditAmount != 100 {
		t.Fatal(view, err)
	}
	// The current claim can finish from that already durable completed child,
	// without a second read of the provider and with no phantom Work returned.
	noWork, err := processor.PrepareQuery(f.ctx, newer)
	if !errors.Is(err, ErrPaymentsLeaseLost) || noWork.child != "" {
		t.Fatal(noWork, err)
	}
}
func TestProcessorQueryConflictPreservesFirstSnapshot(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	processorUnknown(t, f, processor)
	l, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	w, err := processor.PrepareQuery(f.ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	h, raw := processorQuerySigned(t, f, w, "not_seen")
	if err = processor.RecordQuery(f.ctx, w, h, raw); err != nil {
		t.Fatal(err)
	}
	other := processorBaseReply(w, "in_progress")
	h2, raw2 := processorSignedReply(t, f, w, other)
	if err = processor.RecordQuery(f.ctx, w, h2, raw2); !errors.Is(err, ErrPaymentsQueryConflict) {
		t.Fatal(err)
	}
	var snapshots, conflicts int
	var digest string
	if err = f.worker.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_verified_queries),(SELECT count(*) FROM payment_query_conflicts),(SELECT snapshot_digest FROM payment_verified_queries WHERE query_id=$1)`, w.child).Scan(&snapshots, &conflicts, &digest); err != nil || snapshots != 1 || conflicts != 1 || digest != processorDigest(raw) {
		t.Fatal(snapshots, conflicts, digest, err)
	}
}
func TestProcessorExpiryAndFinalBudgetsKeepEvidence(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	f.order(t)
	mutation, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, mutation.binding.id)
	recovery := processorFixture(t, f)
	recovery.runtime.options.lifecycle.Recovery = true
	if err = recovery.ExpireLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err = recovery.ClaimMutation(f.ctx); !errors.Is(err, ErrPaymentsRecovery) {
		t.Fatal(err)
	}
	if _, _, err = recovery.ClaimEvidence(f.ctx); !errors.Is(err, ErrPaymentsRecovery) {
		t.Fatal(err)
	}
	var state string
	if err = f.app.QueryRow(f.ctx, `SELECT state FROM payment_command_outbox WHERE id=$1`, mutation.binding.id).Scan(&state); err != nil || state != "leased" {
		t.Fatal(state, err)
	}
	if err = processor.ExpireLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	processorOwnerMutation(t, f, "payment_command_outbox", `UPDATE payment_command_outbox SET generation=19,attempt_count=19 WHERE kind='query'`)
	query, ok, err := processor.ClaimQuery(f.ctx)
	if err != nil || !ok || query.binding.generation != 20 {
		t.Fatal(ok, query, err)
	}
	w, err := processor.PrepareQuery(f.ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	h, raw := processorQuerySigned(t, f, w, "not_seen")
	if err = processor.RecordQuery(f.ctx, w, h, raw); err != nil {
		t.Fatal(err)
	}
	var original, supervisor string
	var snapshots int
	if err = f.app.QueryRow(f.ctx, `SELECT b.state,s.state,(SELECT count(*) FROM payment_verified_queries) FROM payment_command_outbox s JOIN payment_command_outbox b ON b.id=s.original_outbox_id WHERE s.id=$1`, query.binding.id).Scan(&original, &supervisor, &snapshots); err != nil || original != "submission_unknown" || supervisor != "review_required" || snapshots != 1 {
		t.Fatal(original, supervisor, snapshots, err)
	}
	if _, ok, err = processor.ClaimQuery(f.ctx); err != nil || ok {
		t.Fatal(ok, err)
	}
	processorOwnerMutation(t, f, "payment_evidence_processing", `UPDATE payment_evidence_processing SET generation=19,attempt_count=19`)
	e, ok, err := processor.ClaimEvidence(f.ctx)
	if err != nil || !ok || e.binding.generation != 20 {
		t.Fatal(ok, e, err)
	}
	processorOwnerMutation(t, f, "payment_evidence_processing", `UPDATE payment_evidence_processing SET lease_until=clock_timestamp()-interval '1 second' WHERE evidence_id=$1`, e.binding.id)
	if err = processor.ExpireLeases(f.ctx); err != nil {
		t.Fatal(err)
	}
	r, err := processor.ProcessEvidence(f.ctx, e)
	if err != nil || r.Outcome != "review" || *r.ReviewReason != "processing_attempts_exhausted" {
		t.Fatal(r, err)
	}
}
