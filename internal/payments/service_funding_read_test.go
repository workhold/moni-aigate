package payments

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgresPaymentServiceCreditedViewRejectsBrokenJournal(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	order := f.order(t)
	input := processorNotificationFixture(t, f, order, "txn:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", int64(order.Amount))
	processorReceive(t, f, input)
	lease, ok, e := processor.ClaimEvidence(f.ctx)
	if e != nil || !ok {
		t.Fatal("claim verified capture", e)
	}
	result, e := processor.ProcessEvidence(f.ctx, lease)
	if e != nil || result.Outcome != "credited" || result.FundingReceiptID == nil || result.JournalID == nil {
		t.Fatal("capture funding", e, result)
	}
	view, e := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID)
	if e != nil || view.FundingState != "credited" || view.FundingReceiptID == nil || *view.FundingReceiptID != *result.FundingReceiptID || view.JournalID == nil || *view.JournalID != *result.JournalID || view.CreditedAt == nil || view.WalletCreditAmount != order.Amount {
		t.Fatal("complete safe credit view", e, view)
	}
	// Isolated owner-only corruption fixture, never a serving-role mutation.
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	serviceExec(t, tx, f.ctx, `ALTER TABLE journals DISABLE TRIGGER ALL`)
	serviceExec(t, tx, f.ctx, `UPDATE journals SET actor='corrupted-fixture' WHERE id=$1`, *result.JournalID)
	serviceExec(t, tx, f.ctx, `ALTER TABLE journals ENABLE TRIGGER ALL`)
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	broken, e := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID)
	if !errors.Is(e, ErrPaymentsIntegrity) || broken.ID != "" || broken.FundingReceiptID != nil || broken.JournalID != nil || broken.CreditedAt != nil {
		t.Fatal("fragmentary credit escaped failed integrity", e, broken)
	}
	listed, e := f.service.ListWalletOrders(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, Page{})
	if !errors.Is(e, ErrPaymentsIntegrity) || len(listed.Data) != 0 {
		t.Fatal("broken funding list presented partial success", e, listed)
	}
}

func TestPostgresPaymentServiceHistoricalCaptureSurvivesOffAndRevocation(t *testing.T) {
	f := paymentServiceFixture(t)
	processor := processorFixture(t, f)
	order := f.order(t)
	input := processorNotificationFixture(t, f, order, "txn:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", int64(order.Amount))
	processorReceive(t, f, input)
	// Revoking present human authority stops reads/new commerce; it does not
	// cancel the immutable order's already authorized provider capture window.
	serviceExec(t, f.app, f.ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user)
	serviceExec(t, f.app, f.ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant)
	processor.runtime.options.lifecycle.Mode = ModeOff
	lease, ok, e := processor.ClaimEvidence(f.ctx)
	if e != nil || !ok {
		t.Fatal("history off capture claim", e)
	}
	result, e := processor.ProcessEvidence(f.ctx, lease)
	if e != nil || result.Outcome != "credited" {
		t.Fatal("authorized historical capture was discarded", e, result)
	}
	_, e = f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID)
	serviceCode(t, e, "forbidden")
	view, e := f.service.GetPlatformOrder(f.ctx, f.admin, order.ID)
	if e != nil || view.FundingState != "credited" || view.FundingReceiptID == nil || view.JournalID == nil {
		t.Fatal("platform could not observe retained funding", e, view)
	}
	var journals int
	if e = f.app.QueryRow(f.ctx, `SELECT count(*) FROM journals WHERE kind='sandbox_capture'`).Scan(&journals); e != nil || journals != 1 {
		t.Fatal("historical authority duplicated money", e, journals)
	}
}

func TestPostgresPaymentServiceAndWorkerFundingWithoutPublicFunctions(t *testing.T) {
	f := paymentServiceFixture(t)
	schema := pgx.Identifier{f.schema}.Sanitize()
	worker := pgx.Identifier{f.worker.Config().ConnConfig.User}.Sanitize()
	app := pgx.Identifier{f.app.Config().ConnConfig.User}.Sanitize()
	// The QA installer removes ambient PUBLIC execution. This one immutable
	// legacy predicate is an explicit prerequisite of payment UUID CHECKs.
	serviceExec(t, f.owner, f.ctx, `REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA `+schema+` FROM PUBLIC`)
	serviceExec(t, f.owner, f.ctx, `GRANT EXECUTE ON FUNCTION `+schema+`.funding_uuid(text) TO `+worker+","+app)
	published, e := f.service.PublishSKU(f.ctx, PublishSKUInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.admin}, SKUID: uuid.NewString(), ExpectedRevision: 0, DisplayName: "no ambient function authority", Amount: 100})
	if e != nil || !published.Created {
		t.Fatal("publication depends on PUBLIC functions", e)
	}
	order := f.order(t)
	cancelOrder := f.order(t)
	cancel, e := f.service.CancelOrder(f.ctx, CancelOrderInput{CommandMeta: CommandMeta{OperationID: uuid.NewString(), ActorID: f.user, TenantID: f.tenant}, OrderID: cancelOrder.ID})
	if e != nil || cancel.Status != "accepted" {
		t.Fatal("cancellation depends on PUBLIC functions", e)
	}
	processor := processorFixture(t, f)
	input := processorNotificationFixture(t, f, order, "txn:"+uuid.NewString(), "event:"+uuid.NewString(), "capture.confirmed", int64(order.Amount))
	processorReceive(t, f, input)
	lease, ok, e := processor.ClaimEvidence(f.ctx)
	if e != nil || !ok {
		t.Fatal("zero PUBLIC evidence claim", e)
	}
	result, e := processor.ProcessEvidence(f.ctx, lease)
	if e != nil || result.Outcome != "credited" {
		t.Fatal("funding has undeclared invoker dependency", e, result)
	}
	// Re-reading the original receipt additionally enters funding_journal_valid.
	again, e := processor.ProcessEvidence(f.ctx, lease)
	if e != nil || again.Outcome != "existing" {
		t.Fatal("funding replay has undeclared invoker dependency", e, again)
	}
	view, e := f.service.GetWalletOrder(f.ctx, Reader{ActorID: f.user, TenantID: f.tenant}, order.ID)
	if e != nil || view.FundingState != "credited" {
		t.Fatal("safe funding query depends on PUBLIC", e, view)
	}
	var forbidden int
	e = f.worker.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND p.proname LIKE 'pb\_%' ESCAPE '\' AND has_function_privilege(current_user,p.oid,'EXECUTE')`, f.schema).Scan(&forbidden)
	if e != nil || forbidden != 0 {
		t.Fatal("worker gained periodic-budget function execution", e, forbidden)
	}
}
