package payments

import (
	"context"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// Owner-only deterministic clock/budget/corruption injection in an isolated
// test schema. No application/worker business success uses this path.
// processorOwnerMutation commits deliberately corrupted history. Replica mode
// bypasses user, deferred and foreign-key triggers alike, so the processor's
// own integrity checks are what must detect the damage.
func processorOwnerMutation(t *testing.T, f *serviceFixture, table, query string, args ...any) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal("corrupt", table, err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}
func processorRunEvidence(t *testing.T, f *serviceFixture, processor *Processor) (EvidenceLease, ProcessResult) {
	t.Helper()
	lease, ok, err := processor.ClaimEvidence(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	r, err := processor.ProcessEvidence(f.ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	return lease, r
}
func processorResign(t *testing.T, f *serviceFixture, n p.Notification) NotificationInput {
	t.Helper()
	raw, err := p.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	now := processorNow(t, f)
	headers, err := f.keyring.SignNotification(f.env, p.SignedHeaders{KeyID: f.keyring.ActiveID(), DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if err != nil {
		t.Fatal(err)
	}
	return NotificationInput{Headers: headers, Body: raw}
}
func TestProcessorTerminalRecoveryRejectsDamagedOriginal(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	lease, _ := processorRunEvidence(t, f, processor)
	processorOwnerMutation(t, f, "payment_inbox", `UPDATE payment_inbox SET canonical_body=convert_to('{}','UTF8')`)
	if r, err := processor.ProcessEvidence(f.ctx, lease); !errors.Is(err, ErrPaymentsIntegrity) || r.Outcome != "" {
		t.Fatal(r, err)
	}
}
func TestProcessorTerminalClosedRequiresPersistedRevision(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	in := processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100)
	n, err := p.ParseNotification(in.Body)
	if err != nil {
		t.Fatal(err)
	}
	failure := "order_closed"
	n.EventType = "checkout.failed"
	n.ProviderTransactionID = nil
	n.FailureCode = &failure
	processorReceive(t, f, processorResign(t, f, n))
	lease, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "processed" {
		t.Fatal(r)
	}
	processorOwnerMutation(t, f, "payment_provider_revisions", `DELETE FROM payment_provider_revisions WHERE order_id=$1`, o.ID)
	if _, err = processor.ProcessEvidence(f.ctx, lease); !errors.Is(err, ErrPaymentsIntegrity) {
		t.Fatal(err)
	}
}
func TestProcessorTerminalCaptureMissingReceiptIsIntegrity(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	lease, r := processorRunEvidence(t, f, processor)
	processorOwnerMutation(t, f, "funding_receipts", `DELETE FROM funding_receipts WHERE id=$1`, *r.FundingReceiptID)
	if _, err := processor.ProcessEvidence(f.ctx, lease); !errors.Is(err, ErrPaymentsIntegrity) {
		t.Fatal(err)
	}
}
func TestProcessorFinalGenerationCASRollsBackWholeFundingGraph(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	serviceExec(t, f.owner, f.ctx, `CREATE FUNCTION test_skip_payment_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state='leased' AND NEW.state='processed' THEN RETURN NULL;END IF;RETURN NEW;END $$;CREATE TRIGGER z_test_skip_ack BEFORE UPDATE ON payment_evidence_processing FOR EACH ROW EXECUTE FUNCTION test_skip_payment_ack()`)
	l, ok, err := processor.ClaimEvidence(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err = processor.ProcessEvidence(f.ctx, l); !errors.Is(err, ErrPaymentsLeaseLost) {
		t.Fatal(err)
	}
	var total int
	err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_transaction_identities)+(SELECT count(*) FROM payment_order_capture_selections)+(SELECT count(*) FROM payment_captures)+(SELECT count(*) FROM payment_effects)+(SELECT count(*) FROM journals)+(SELECT count(*) FROM funding_receipts)+(SELECT count(*) FROM projection_outbox WHERE kind='credited')+(SELECT count(*) FROM audit_events WHERE action='payment.capture.credited')`).Scan(&total)
	if err != nil || total != 0 {
		t.Fatal(total, err)
	}
}
func TestProcessorLockBudgetCancellationDoesNotFence(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	l, ok, err := processor.ClaimMutation(f.ctx)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	blocker, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	serviceExec(t, blocker, f.ctx, `SELECT id FROM payment_orders WHERE id=$1 FOR NO KEY UPDATE`, o.ID)
	done := make(chan error, 1)
	go func() { _, err := processor.PrepareMutation(f.ctx, l); done <- err }()
	time.Sleep(2100 * time.Millisecond)
	if err = blocker.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if processor.runtime.fenced.Load() {
		t.Fatal("local time budget fenced global ownership")
	}
	if _, err = processor.PrepareMutation(f.ctx, l); err != nil {
		t.Fatal("healthy next transaction", err)
	}
}
func TestProcessorCrossOrderTransactionConflictAndDuplicateCapture(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o1, o2 := f.order(t), f.order(t)
	transaction := "tx:" + uuid.NewString()
	first := processorNotificationFixture(t, f, o1, transaction, "event:"+uuid.NewString(), "capture.confirmed", 100)
	processorReceive(t, f, first)
	_, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "credited" {
		t.Fatal(r)
	}
	processorReceive(t, f, processorNotificationFixture(t, f, o2, transaction, "event:"+uuid.NewString(), "capture.confirmed", 100))
	l2, r2 := processorRunEvidence(t, f, processor)
	if r2.Outcome != "review" || *r2.ReviewReason != "transaction_conflict" {
		t.Fatal(r2)
	}
	if _, err := processor.ProcessEvidence(f.ctx, l2); err != nil {
		t.Fatal("review restore", err)
	}
	n, err := p.ParseNotification(first.Body)
	if err != nil {
		t.Fatal(err)
	}
	second := "tx:" + uuid.NewString()
	n.ProviderTransactionID = &second
	n.EventID = "event:" + uuid.NewString()
	n.ProviderOrderRevision = "3"
	processorReceive(t, f, processorResign(t, f, n))
	_, r3 := processorRunEvidence(t, f, processor)
	if r3.Outcome != "review" || *r3.ReviewReason != "duplicate_order_capture" {
		t.Fatal(r3)
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM payment_captures`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
func TestProcessorRealFundingOverflowPreservesSelection(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	// Deliberately corrupt only this isolated AVAILABLE projection as a checked
	// overflow negative control; the source guard is restored before processing.
	fault, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fault.Rollback(context.Background())
	serviceExec(t, fault, f.ctx, `ALTER TABLE ledger_accounts DISABLE TRIGGER wallet_source_fact`)
	serviceExec(t, fault, f.ctx, `UPDATE ledger_accounts SET balance=$2 WHERE tenant_id=$1 AND kind='AVAILABLE'`, f.tenant, int64(math.MaxInt64))
	serviceExec(t, fault, f.ctx, `INSERT INTO ledger_accounts(tenant_id,kind,balance) SELECT $1,'AVAILABLE',$2 WHERE NOT EXISTS(SELECT 1 FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE')`, f.tenant, int64(math.MaxInt64))
	serviceExec(t, fault, f.ctx, `SET CONSTRAINTS ALL IMMEDIATE; ALTER TABLE ledger_accounts ENABLE TRIGGER wallet_source_fact`)
	if err = fault.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	lease, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "review" || *r.ReviewReason != "funding_amount_overflow" {
		t.Fatal(r)
	}
	var identities, selections, captures int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM payment_transaction_identities),(SELECT count(*) FROM payment_order_capture_selections),(SELECT count(*) FROM payment_captures)`).Scan(&identities, &selections, &captures); err != nil || identities != 1 || selections != 1 || captures != 0 {
		t.Fatal(identities, selections, captures, err)
	}
	if _, err = processor.ProcessEvidence(f.ctx, lease); err != nil {
		t.Fatal("overflow restore", err)
	}
}

func TestProcessorHistoricalUnknownOrderDoesNotReopenAfterLaterCreate(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	unknown := uuid.NewString()
	transaction := "tx:" + uuid.NewString()
	n := p.Notification{SchemaVersion: 1, EventID: "event:" + uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: f.env.ID, MerchantID: f.env.MerchantID, OrderID: unknown, ProviderTransactionID: &transaction, Amount: "100", Currency: "CNY", OccurredAt: processorTime(processorNow(t, f)), ProviderOrderRevision: "2"}
	processorReceive(t, f, processorResign(t, f, n))
	lease, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "review" || *r.ReviewReason != "unknown_order" {
		t.Fatal(r)
	}
	created, err := f.service.CreateOrder(f.ctx, CreateOrderInput{CommandMeta: CommandMeta{OperationID: unknown, ActorID: f.user, TenantID: f.tenant}, SKUVersionID: f.version})
	if err != nil || created.Order == nil {
		t.Fatal(created, err)
	}
	restored, err := processor.ProcessEvidence(f.ctx, lease)
	if err != nil || restored.Outcome != "review" || *restored.ReviewReason != "unknown_order" {
		t.Fatal(restored, err)
	}
	var captures int
	if err = f.worker.QueryRow(f.ctx, `SELECT count(*) FROM payment_captures`).Scan(&captures); err != nil || captures != 0 {
		t.Fatal(captures, err)
	}
}

func TestProcessorCaptureAfterRevokingLegacyPublicFunctionsNeedsOnlyFundingUUID(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	o := f.order(t)
	processorReceive(t, f, processorNotificationFixture(t, f, o, "tx:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", 100))
	var worker string
	if err := f.worker.QueryRow(f.ctx, `SELECT current_user`).Scan(&worker); err != nil {
		t.Fatal(err)
	}
	serviceExec(t, f.owner, f.ctx, `REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA `+pgx.Identifier{f.schema}.Sanitize()+` FROM PUBLIC`)
	serviceExec(t, f.owner, f.ctx, `GRANT EXECUTE ON FUNCTION `+pgx.Identifier{f.schema, "funding_uuid"}.Sanitize()+`(text) TO `+pgx.Identifier{worker}.Sanitize())
	if _, err := f.worker.Exec(f.ctx, `SELECT pb_now()`); err == nil {
		t.Fatal("payment worker acquired periodic function execute")
	}
	_, r := processorRunEvidence(t, f, processor)
	if r.Outcome != "credited" {
		t.Fatal(r)
	}
}
