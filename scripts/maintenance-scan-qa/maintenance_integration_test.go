//go:build historical

package maintenancescanqa_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/payments"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementexecution"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/testsupport/frozenattempt"
)

// This external consumer imports only public production entry points. It starts
// with genuine v1 funded QA data and a signed sandbox capture, appends exactly
// the frozen C0 DDL and ACL, then runs all five local readers and the complete 68-table reader on the same
// attested maintenance transaction.
func TestMaintenanceConsumersPostgresFundedSnapshot(t *testing.T) {
	for _, mode := range []string{"plan", "init"} {
		t.Run(mode, func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx := context.Background()
			captureID := f.sandboxCaptureID
			if mode == "init" {
				damageConsumerProcurementAttempt(t, f, ctx)
				breakConsumerFundingReceiptLinks(t, f, ctx, f.fundingSourceCreditID, captureID)
			}
			fundingBeforeScan, err := f.env.FundingSnapshot(ctx)
			if err != nil {
				t.Fatal("consumer funding baseline after voucher fixture failed")
			}
			legacyBeforeScan, err := f.env.LegacyFundingSnapshot(ctx)
			if err != nil {
				t.Fatal("consumer legacy funding baseline after all fixture setup failed")
			}
			procurementBeforeScan, err := f.procurementAttemptSnapshot(ctx)
			if err != nil {
				t.Fatal("consumer procurement baseline before scan failed", err)
			}
			g, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
			if err != nil || g == nil {
				t.Fatal("real public maintenance Acquire failed")
			}
			f.gates = append(f.gates, g)
			f.observeMaintenance(t)
			var snapshot *maintenancetrust.Snapshot
			if mode == "init" {
				snapshot, err = g.BeginInit(ctx)
			} else {
				snapshot, err = g.BeginPlan(ctx)
			}
			if err != nil || snapshot == nil {
				t.Fatal("real public maintenance snapshot failed")
			}
			installation, err := snapshot.VerifyInstallation(ctx)
			if err != nil || installation == nil {
				t.Fatal("genuine installation proof failed", err)
			}
			// The caller's descriptive copy does not own the installed authority.
			*installation = fundinglotcatalog.InstallationProof{}
			permit, err := maintenancetrust.Attest(ctx, snapshot)
			if err != nil || permit == nil {
				t.Fatal("genuine funded predecessor attestation failed")
			}
			var ledger *maintenancescan.MaintenanceLedgerResult
			var budget *maintenancescan.MaintenanceResult
			var fundingLinks *maintenancescan.FundingFinancialResult
			var sandboxLinks *maintenancescan.SandboxCaptureReceiptLinksResult
			var procurementResult *maintenancescan.ProcurementMaintenanceResult
			readLedger := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanLedger)
				if e != nil {
					t.Fatal("real ledger lease failed")
				}
				ledger, e = maintenancescan.VerifyLedgerMaintenance(lease)
				if e != nil || ledger == nil {
					t.Fatal("real maintenance ledger scan failed")
				}
			}
			readBudget := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanPeriodicBudget)
				if e != nil {
					t.Fatal("real periodic budget lease failed")
				}
				budget, e = maintenancescan.VerifyMaintenance(lease)
				if e != nil || budget == nil {
					t.Fatal("real maintenance periodic budget scan failed")
				}
			}
			readFundingLinks := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanFunding)
				if e != nil {
					t.Fatal("real funding source lease failed")
				}
				fundingLinks, e = maintenancescan.VerifyFundingFinancialMaintenance(lease)
				if e != nil || fundingLinks == nil {
					t.Fatal("real maintenance funding source link scan failed")
				}
			}
			readSandboxLinks := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanSandboxSources)
				if e != nil {
					t.Fatal("real sandbox source lease failed")
				}
				sandboxLinks, e = maintenancescan.VerifySandboxCaptureReceiptLinksMaintenance(lease)
				if e != nil || sandboxLinks == nil {
					t.Fatal("real maintenance sandbox capture/receipt scan failed")
				}
			}
			readProcurement := func() {
				lease, e := permit.BeginScan(ctx, maintenancetrust.ScanProcurement)
				if e != nil {
					t.Fatal("real procurement budget lease failed")
				}
				procurementResult, e = maintenancescan.VerifyProcurementMaintenance(lease)
				if e != nil || procurementResult == nil {
					t.Fatal("real maintenance procurement scan failed", e)
				}
			}
			if mode == "plan" {
				readLedger()
				readFundingLinks()
				readSandboxLinks()
				readProcurement()
				readBudget()
			} else {
				readBudget()
				readFundingLinks()
				readSandboxLinks()
				readProcurement()
				readLedger()
			}
			completeLease, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources)
			if err != nil {
				t.Fatal("real complete-source lease failed", err)
			}
			complete, err := maintenancescan.VerifyCompleteSourcesMaintenance(completeLease)
			if err != nil || complete == nil {
				t.Fatal("real complete-source maintenance scan failed", err)
			}
			completeReport, err := complete.Summary(permit)
			if err != nil || completeReport.Scope != "complete_business_source_graphs_v1" || !completeReport.PhysicalEOFComplete || completeReport.ScannedTables != 68 || completeReport.InputMismatchFields != 0 {
				t.Fatal("complete-source fixed union was not physically completed", err, completeReport)
			}
			for _, algorithm := range completeReport.Algorithms {
				if algorithm != "executed" {
					t.Fatal("all seven sandbox algorithms must actually execute", completeReport.Algorithms)
				}
			}
			if mode == "init" && (completeReport.Status != "mismatch" || completeReport.Customer.Balanced || completeReport.Funding.ScopedGraphConsistent) {
				t.Fatal("complete-source scan hid the genuine corrupted business rows", completeReport)
			}
			if mode == "plan" && (completeReport.Status != "indeterminate" || completeReport.CoverageComplete ||
				completeReport.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{ProposalBarrierUnproven: 1}) ||
				completeReport.Periodic.Status != "balanced" || completeReport.Sandbox.Status != "consistent") {
				t.Fatal("complete-source scan invented a predecessor for the historical approval barrier", completeReport)
			}
			t.Logf("complete-source %s: status=%s coverage=%t rows=%d bytes=%d procurement_unproven=%+v PB_unproven=%+v sandbox_unproven=%+v", mode, completeReport.Status, completeReport.CoverageComplete, completeReport.SourceRows, completeReport.SourceBytes, completeReport.Procurement.Unproven, completeReport.Periodic.Unproven, completeReport.Sandbox.Unproven)
			check, err := ledger.Summary(permit)
			if err != nil || check.Scope != "supplied_ledger_rows" || !check.RowsConsistent || check.Scanned.Accounts == 0 || check.Scanned.Journals == 0 || check.Scanned.Lines == 0 {
				t.Fatal("funded ledger local facts were not retained")
			}
			report, err := budget.Report(permit)
			if err != nil || report.SchemaVersion != 1 {
				t.Fatal("periodic budget local report was not retained")
			}
			fundingCheck, err := fundingLinks.SourceLinksSummary(permit)
			wantConsistent := mode == "plan"
			linksMatch := fundingCheck.ScopedLinksConsistent == wantConsistent
			if mode == "init" {
				linksMatch = linksMatch && fundingCheck.Mismatches.ApprovedCreditMissingReceipt == 1 && fundingCheck.Mismatches.OrphanManualReceipts == 1
			}
			if err != nil || fundingCheck.Scope != "manual_voucher_receipt_links" || !linksMatch || fundingCheck.Scanned.Receipts < 4 || fundingCheck.Scanned.Credits < 2 || fundingCheck.Scanned.Redemptions != 1 || fundingCheck.UncoveredSandboxReceipts != 1 {
				t.Fatal("manual funding local relation facts were not retained")
			}
			if completeReport.Ledger != check || completeReport.Links != fundingCheck {
				t.Fatal("complete union disagreed with the unchanged local raw algorithms")
			}
			financialLedger, err := fundingLinks.LedgerSummary(permit)
			if err != nil || financialLedger != check {
				t.Fatal("same-session financial ledger differed from the fixed ledger algorithm", err)
			}
			sourceGraph, err := fundingLinks.SourceGraphSummary(permit)
			if err != nil || sourceGraph.Scope != "manual_voucher_funding_graph_v1" || sourceGraph.ScopedGraphConsistent != (mode == "plan") || sourceGraph.UncoveredSandboxReceipts != 1 || sourceGraph.Scanned.Redemptions != 1 {
				t.Fatal("full manual/voucher source graph was not retained", err, sourceGraph)
			}
			if completeReport.Funding != sourceGraph {
				t.Fatal("complete union disagreed with the unchanged manual/voucher core")
			}
			customerGraph, err := fundingLinks.CustomerKeySummary(permit)
			if err != nil || customerGraph.Scope != "customer_key_financial_graph_v1" || customerGraph.Balanced != (mode == "plan") || customerGraph.Scanned.Requests != 1 || customerGraph.Scanned.Keys != 1 || (mode == "init" && customerGraph.Mismatches.KeyHeldRows != 1) {
				t.Fatal("customer Key graph was not retained", err, customerGraph)
			}
			if completeReport.Customer != customerGraph {
				t.Fatal("complete union disagreed with the unchanged customer core")
			}
			sandboxCheck, err := sandboxLinks.Summary(permit)
			wantSandboxConsistent := mode == "plan"
			if err != nil || sandboxCheck.Scope != "sandbox_capture_receipt_reference_v1" || sandboxCheck.DirectLinksConsistent != wantSandboxConsistent ||
				sandboxCheck.Scanned.Captures != 1 || sandboxCheck.UnvalidatedCaptureGraphs != 1 || sandboxCheck.UnvalidatedLedgerEffects != 1 ||
				(mode == "init" && (sandboxCheck.OrphanSandboxReceipts != 1 || sandboxCheck.CapturesMissingReceipts != 1)) {
				t.Fatal("sandbox direct-link local facts were not retained")
			}
			procurementBudgetCheck, err := procurementResult.BudgetSummary(permit)
			if err != nil || procurementBudgetCheck.Scope != "procurement_budget_projection_v1" || !procurementBudgetCheck.Balanced ||
				procurementBudgetCheck.Scanned.Accounts != 1 || procurementBudgetCheck.Mismatches != (fundinglotverify.ProcurementBudgetProjectionMismatches{}) {
				t.Fatal("procurement budget projection was not retained as balanced", err, procurementBudgetCheck)
			}
			procurementBindingCheck, err := procurementResult.BindingSummary(permit)
			wantBindingBalanced := mode == "plan"
			wantBoundMismatch := uint64(0)
			wantUpperMismatch := uint64(0)
			if mode == "init" {
				wantBoundMismatch = 1
				wantUpperMismatch = 1
			}
			if err != nil || procurementBindingCheck.Scope != "procurement_attempt_binding_graph_v1" || procurementBindingCheck.Balanced != wantBindingBalanced ||
				procurementBindingCheck.Scanned.Channels != 1 || procurementBindingCheck.Scanned.Credentials != 1 ||
				procurementBindingCheck.Scanned.Versions != 1 || procurementBindingCheck.Scanned.PoolMemberships != 1 ||
				procurementBindingCheck.Scanned.Tenants != 2 || procurementBindingCheck.Scanned.Requests != 1 || procurementBindingCheck.Scanned.CostVersions != 1 ||
				procurementBindingCheck.Scanned.Accounts != 1 || procurementBindingCheck.Scanned.Bindings != 1 ||
				procurementBindingCheck.Scanned.Attempts != 1 || procurementBindingCheck.Scanned.Reservations != 1 ||
				procurementBindingCheck.Scanned.BudgetEvents != 3 ||
				procurementBindingCheck.Mismatches.ReservationRequestBoundRows != wantBoundMismatch ||
				procurementBindingCheck.Mismatches.ReservationUpperBoundRows != wantUpperMismatch {
				t.Fatal("procurement attempt binding local facts were not retained", err, procurementBindingCheck)
			}
			approvalCheck, err := procurementResult.ApprovalSummary(permit)
			wantApprovalBalanced := mode == "plan"
			if err != nil || approvalCheck.Scope != "procurement_cost_approval_graph_v1" || approvalCheck.Balanced != wantApprovalBalanced ||
				approvalCheck.Scanned.Proposals != 1 || approvalCheck.Scanned.Corrections != 1 ||
				(mode == "init" && (approvalCheck.Mismatches.ApprovedCorrectionRows != 1 || approvalCheck.Mismatches.ApprovedEventRows != 1 || approvalCheck.Mismatches.ManagedCorrectionApprovalRows != 1 || approvalCheck.Mismatches.CorrectionEventApprovalRows != 1)) {
				t.Fatal("procurement approval local facts were not retained", err, approvalCheck)
			}

			commandCheck, err := procurementResult.CommandSummary(permit)
			if err != nil || commandCheck.Scope != "procurement_command_observation_graph_v1" || commandCheck.Balanced != (mode == "plan") || commandCheck.Scanned.Commands != 4 || commandCheck.Scanned.Observations != 1 ||
				(mode == "init" && (commandCheck.Mismatches.AppliedObservationRows != 1 || commandCheck.Mismatches.ObservationCommandRows != 1)) {
				t.Fatal("procurement command local facts were not retained", err, commandCheck)
			}
			journalCheck, err := procurementResult.JournalSummary(permit)
			if err != nil || journalCheck.Scope != "procurement_cost_journal_graph_v1" || journalCheck.Balanced != (mode == "plan") || journalCheck.Scanned.Corrections != 1 || journalCheck.Scanned.Proposals != 1 {
				t.Fatal("procurement cost journal local facts were not retained", err, journalCheck)
			}

			if err := ledger.Verify(permit); err != nil || budget.Verify(permit) != nil || fundingLinks.Verify(permit) != nil || sandboxLinks.Verify(permit) != nil || procurementResult.Verify(permit) != nil || complete.Verify(permit) != nil {
				t.Fatal("all real consumers must share the original permit")
			}
			if _, err := permit.BeginScan(ctx, maintenancetrust.ScanFunding); err == nil {
				t.Fatal("the same Permit accepted ScanFunding twice")
			}
			if _, err := permit.BeginScan(ctx, maintenancetrust.ScanCompleteSources); err == nil {
				t.Fatal("the same Permit accepted ScanCompleteSources twice")
			}
			wrong := &maintenancetrust.Permit{}
			if ledger.Verify(wrong) == nil || budget.Verify(wrong) == nil || fundingLinks.Verify(wrong) == nil || sandboxLinks.Verify(wrong) == nil || procurementResult.Verify(wrong) == nil || complete.Verify(wrong) == nil {
				t.Fatal("consumer accepted an unrelated permit")
			}
			ledgerCopy, budgetCopy, fundingLinksCopy, sandboxLinksCopy, procurementCopy := *ledger, *budget, *fundingLinks, *sandboxLinks, *procurementResult
			completeCopy := *complete
			if snapshot.Rollback(ctx) != nil {
				t.Fatal("real shared snapshot rollback failed")
			}
			if ledger.Verify(permit) == nil || budget.Verify(permit) == nil || fundingLinks.Verify(permit) == nil || sandboxLinks.Verify(permit) == nil || procurementResult.Verify(permit) == nil ||
				ledgerCopy.Verify(permit) == nil || budgetCopy.Verify(permit) == nil || fundingLinksCopy.Verify(permit) == nil || sandboxLinksCopy.Verify(permit) == nil || procurementCopy.Verify(permit) == nil || complete.Verify(permit) == nil || completeCopy.Verify(permit) == nil {
				t.Fatal("rollback did not revoke every original consumer result")
			}
			if g.Close(ctx) != nil {
				t.Fatal("original maintenance connection close failed")
			}
			f.awaitOwnedGone(t)
			fundingAfterScan, err := f.env.FundingSnapshot(ctx)
			if err != nil || fundingAfterScan != fundingBeforeScan {
				t.Fatal("maintenance scans changed genuine funded data")
			}
			legacyAfterScan, err := f.env.LegacyFundingSnapshot(ctx)
			if err != nil || legacyAfterScan != legacyBeforeScan {
				t.Fatal("maintenance scans changed post-fixture legacy funding facts")
			}
			procurementAfterScan, err := f.procurementAttemptSnapshot(ctx)
			if err != nil || string(procurementAfterScan) != string(procurementBeforeScan) {
				t.Fatal("maintenance scan changed procurement business facts", err)
			}
			var procurementHeld, procurementSpent string
			// The fixture confirms an invoice cost of 12 before maintenance;
			// approval releases the original upper-bound hold of 240.
			if err = f.admin.QueryRow(ctx, "SELECT held_amount::text,spent_amount::text FROM "+pgx.Identifier{f.env.Schemas["app"]}.Sanitize()+".procurement_accounts WHERE id=$1", f.procurementAccountID).Scan(&procurementHeld, &procurementSpent); err != nil || procurementHeld != "0" || procurementSpent != "12" {
				t.Fatal("maintenance scan changed procurement budget projection", err, procurementHeld, procurementSpent)
			}
		})
	}
}

func seedConsumerFundingSources(t *testing.T, f *consumerFixture, ctx context.Context) string {
	t.Helper()
	app, err := f.env.OpenPool(ctx, "app")
	if err != nil {
		t.Fatal("consumer voucher app pool failed")
	}
	f.pools = append(f.pools, app)
	store := billing.New(app)
	credit, err := store.CreateCredit(ctx, billing.CreditInput{
		TenantID: f.env.ApplicationTenantID, ActorID: f.env.ActorIDs["finance_operator"], Amount: 219,
		BusinessRef: "bank:maintenance-qa:" + uuid.NewString(), ProofRef: "proof:maintenance-qa:verified",
		IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString(),
	})
	if err != nil {
		t.Fatal("consumer manual credit creation failed", err)
	}
	creditApproved, err := store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.env.ApplicationTenantID, ActorID: f.env.ActorIDs["finance_approver"], OrderID: credit.ID})
	if err != nil || creditApproved.State != "approved" || creditApproved.JournalID == "" {
		t.Fatal("consumer manual credit approval failed", err)
	}
	keys, err := security.ParseVoucherKeyring(`{"v1":"`+base64.RawURLEncoding.EncodeToString(make([]byte, 32))+`"}`, "v1")
	if err != nil {
		t.Fatal("consumer fixed voucher keyring failed")
	}
	service, err := billing.NewVoucherService(ctx, billing.New(app), billing.VoucherOptions{RuntimeAllowed: true, Keys: keys})
	if err != nil {
		t.Fatal("consumer voucher service failed")
	}
	command := func(actor string) billing.VoucherCommandContext {
		return billing.VoucherCommandContext{OperationID: uuid.NewString(), ActorID: actor, RequestID: uuid.NewString()}
	}
	if _, err = service.UpdateVoucherPolicy(ctx, billing.UpdateVoucherPolicyInput{ActorID: f.env.ActorIDs["admin"], ExpectedRevision: 1, Enabled: true}); err != nil {
		t.Fatal("consumer voucher policy enable failed", err)
	}
	issued, err := service.CreateVoucherBatch(ctx, billing.CreateVoucherBatchInput{
		Command: command(f.env.ActorIDs["finance_operator"]), FaceAmount: 17, Count: 1,
		ExpiresAt: time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond), PurposeRef: "grant:maintenance-qa", Reason: "maintenance source-link fixture",
	})
	if err != nil || !issued.Created || len(issued.Codes) != 1 || issued.Batch == nil {
		t.Fatal("consumer voucher issue failed", err)
	}
	voucherApproved, err := service.ApproveVoucherBatch(ctx, billing.ApproveVoucherBatchInput{Command: command(f.env.ActorIDs["finance_approver"]), BatchID: issued.Batch.ID, ExpectedRevision: 1})
	if err != nil || voucherApproved.Status != "accepted" {
		t.Fatal("consumer voucher approval failed", err)
	}
	redeem := command(f.env.ActorIDs["owner"])
	redeem.TenantID = f.env.ApplicationTenantID
	redemption, err := service.RedeemVoucher(ctx, billing.RedeemVoucherInput{Command: redeem, Code: issued.Codes[0].Code})
	if err != nil || redemption.Status != "accepted" || redemption.FundingReceiptID == nil || redemption.JournalID == nil {
		t.Fatalf("consumer voucher redemption failed: status=%s reason=%s error=%v", redemption.Status, redemption.Reason, err)
	}
	return credit.ID
}

func breakConsumerFundingReceiptLinks(t *testing.T, f *consumerFixture, ctx context.Context, creditID, captureID string) {
	t.Helper()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal("consumer mismatch fixture transaction failed")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role='replica'"); err != nil {
		t.Fatal("consumer mismatch fixture isolation failed", err)
	}
	schema := pgx.Identifier{f.env.Schemas["app"]}.Sanitize()
	if _, err = tx.Exec(ctx, "UPDATE "+schema+".funding_receipts SET source_id=$1 WHERE source_kind='manual_credit' AND source_id=$2", "unlinked-"+uuid.NewString(), creditID); err != nil {
		t.Fatal("consumer mismatch fixture source adjustment failed", err)
	}
	if _, err = tx.Exec(ctx, "UPDATE "+schema+".funding_receipts SET source_id=$1 WHERE source_kind='sandbox_capture' AND source_id=$2", "unlinked-"+uuid.NewString(), captureID); err != nil {
		t.Fatal("consumer sandbox mismatch fixture source adjustment failed", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("consumer mismatch fixture commit failed", err)
	}
}

// seedConsumerSandboxCapture creates one real local-sandbox order, accepts a
// signed capture notification, and lets the restricted worker credit it. This
// exercises the exact receipt/capture relation scanned below without using
// direct SQL to fabricate either side of that relation.
func seedConsumerSandboxCapture(t *testing.T, f *consumerFixture, ctx context.Context) string {
	t.Helper()
	a, err := payments.LoadSandboxOptions(ctx, f.env.ApplicationManifestPath)
	if err != nil {
		t.Fatal("consumer app payment options failed")
	}
	w, err := payments.LoadWorkerOptions(ctx, f.env.WorkerManifestPath)
	if err != nil || payments.ValidateOptionsPair(a, w) != nil {
		t.Fatal("consumer worker payment options failed")
	}
	life := payments.Lifecycle{Mode: payments.ModeSandbox, Guard: func(context.Context) error { return nil }}
	a, err = a.BindLifecycle(life)
	if err != nil {
		t.Fatal("consumer app payment lifecycle failed")
	}
	w, err = w.BindLifecycle(life)
	if err != nil || payments.ValidateOptionsPair(a, w) != nil {
		t.Fatal("consumer worker payment lifecycle failed")
	}
	appDSN, err := f.env.RuntimeDSN("app")
	if err != nil {
		t.Fatal("consumer app payment DSN failed")
	}
	cfg, err := payments.ApplicationPoolConfig(ctx, appDSN, a)
	if err != nil {
		t.Fatal("consumer app payment pool config failed")
	}
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("consumer app payment pool failed")
	}
	defer app.Close()
	service, err := payments.NewService(ctx, app, a)
	if err != nil {
		t.Fatal("consumer app payment service failed")
	}
	worker, err := payments.OpenProcessor(ctx, w)
	if err != nil {
		t.Fatal("consumer payment worker failed")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if worker.Close(closeCtx) != nil {
			t.Error("consumer payment worker close failed")
		}
	}()
	adminID, ownerID := f.env.ActorIDs["admin"], f.env.ActorIDs["owner"]
	skuID := uuid.NewString()
	published, err := service.PublishSKU(ctx, payments.PublishSKUInput{
		CommandMeta: payments.CommandMeta{OperationID: uuid.NewString(), ActorID: adminID},
		SKUID:       skuID, ExpectedRevision: 0, DisplayName: "Maintenance capture fixture", Amount: 23,
	})
	if err != nil || published.Receipt.ResourceID == nil {
		t.Fatal("consumer sandbox SKU publish failed")
	}
	versionID := *published.Receipt.ResourceID
	if _, err = service.ActivateSKU(ctx, payments.ActivateSKUInput{
		CommandMeta: payments.CommandMeta{OperationID: uuid.NewString(), ActorID: adminID},
		SKUID:       skuID, ExpectedRevision: 1, VersionID: &versionID, Enabled: true,
	}); err != nil {
		t.Fatal("consumer sandbox SKU activation failed")
	}
	if _, err = service.UpdatePolicy(ctx, payments.UpdatePolicyInput{ActorID: adminID, ExpectedRevision: 1, Enabled: true}); err != nil {
		t.Fatal("consumer sandbox policy enable failed")
	}
	order, err := service.CreateOrder(ctx, payments.CreateOrderInput{
		CommandMeta:  payments.CommandMeta{OperationID: uuid.NewString(), ActorID: ownerID, TenantID: f.env.ApplicationTenantID},
		SKUVersionID: versionID,
	})
	if err != nil || order.Order == nil {
		t.Fatal("consumer sandbox order creation failed")
	}
	var now time.Time
	if app.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
		t.Fatal("consumer sandbox clock read failed")
	}
	now = now.UTC()
	transactionID := "transaction:" + uuid.NewString()
	notification := paymentproto.Notification{
		SchemaVersion: 1, EventID: "event:" + uuid.NewString(), EventType: "capture.confirmed",
		EnvironmentID: f.env.Protocol.ID, MerchantID: f.env.Protocol.MerchantID, OrderID: order.Order.ID,
		ProviderTransactionID: &transactionID, Amount: "23", Currency: "CNY",
		OccurredAt: now.Format("2006-01-02T15:04:05.000000Z"), ProviderOrderRevision: "2",
	}
	raw, err := paymentproto.CanonicalNotification(notification)
	if err != nil {
		t.Fatal("consumer sandbox notification encoding failed")
	}
	keyring, err := f.env.Keyring()
	if err != nil {
		t.Fatal("consumer sandbox keyring failed")
	}
	headers, err := keyring.SignNotification(f.env.Protocol, paymentproto.SignedHeaders{
		KeyID: "qa-key", DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10),
	}, raw)
	if err != nil {
		t.Fatal("consumer sandbox notification signing failed")
	}
	if _, _, err = service.ReceiveNotification(ctx, payments.NotificationInput{Headers: headers, Body: raw}); err != nil {
		t.Fatal("consumer sandbox notification receive failed")
	}
	lease, ok, err := worker.ClaimEvidence(ctx)
	if err != nil || !ok {
		t.Fatal("consumer sandbox evidence claim failed")
	}
	result, err := worker.ProcessEvidence(ctx, lease)
	if err != nil || result.Outcome != "credited" || result.CaptureID == nil || result.FundingReceiptID == nil {
		t.Fatal("consumer sandbox capture funding failed")
	}
	return *result.CaptureID
}

type consumerBackend struct {
	pid   int32
	start time.Time
}
type consumerFixture struct {
	env                       *paymentsandboxqa.ApplicationEnvironment
	admin                     *pgx.Conn
	cfg                       *pgx.ConnConfig
	pins                      fundinglotcatalog.Pins
	sharedBefore              string
	tag                       string
	fundingSourceCreditID     string
	sandboxCaptureID          string
	procurementAccountID      string
	procurementChannelID      string
	procurementCredentialID   string
	procurementVersionID      string
	procurementModelID        string
	procurementPriceVersionID string
	procurementKeyID          string
	procurementRequestID      string
	procurementAttemptID      string
	procurementBindingID      string
	procurementProposalID     string
	procurementObservationID  string
	gates                     []*maintenancetrust.Gate
	pools                     []*pgxpool.Pool
	backends                  []consumerBackend
}

func consumerConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	raw := os.Getenv("C0_MAINTENANCE_DATABASE_URL")
	if raw == "" {
		t.Skip("C0_MAINTENANCE_DATABASE_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host != "127.0.0.1:55439" || u.Path != "/moni_test" || u.User == nil || u.User.Username() != "moni_test" || u.Fragment != "" {
		t.Fatal("consumer dedicated test endpoint rejected")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		t.Fatal("consumer test query rejected")
	}
	for key, values := range q {
		if key != "sslmode" || len(values) != 1 || values[0] != "disable" {
			t.Fatal("consumer test query rejected")
		}
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55439 || cfg.Database != "moni_test" || cfg.User != "moni_test" || len(cfg.Fallbacks) != 0 || cfg.TLSConfig != nil {
		t.Fatal("consumer parsed test endpoint rejected")
	}
	cfg.ConnectTimeout = 3 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "c0_consumer_admin", "search_path": "pg_catalog"}
	return cfg
}

func consumerFixedSource(t *testing.T, name, want string) []byte {
	t.Helper()
	_, path, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("consumer repository location unavailable")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(path), "../..", name))
	sum := sha256.Sum256(body)
	if err != nil || hex.EncodeToString(sum[:]) != want {
		t.Fatal("consumer frozen fixture source changed: " + name)
	}
	return body
}

func newConsumerFixture(t *testing.T) *consumerFixture {
	t.Helper()
	cfg := consumerConfig(t)
	for _, pin := range []struct{ name, sha string }{
		{"internal/paymentsandboxqa/install.go", "dd0ac0d67bbe9b6e400f11ccff14479a9c156779b5fc2079ee24c7c830e18888"},
		{"internal/paymentsandboxqa/application.go", "b0d28048b9fa3d856b83e494d319158ad4c3937eb745dde88674536496254f33"},
		{"internal/paymentsandboxschema/mock_v1.sql", "3942a0af2577bdad4e841f2b1fa3f0502a3a0db898d6f050fbaf2da45e7e0127"},
		{"internal/paymentsandboxschema/receiver_v1.sql", "3c1a4956b55ef058ad6a927e678bf026017a4f3d23bc60d19cb02640e98f433e"},
		{"internal/paymentsandboxschema/mock_inventory.json", "3f5e926b8ed9152d6bf9ff757400ab3119241629cb3b00cdbd40974d9f818234"},
		{"internal/paymentsandboxschema/receiver_inventory.json", "73be5ea3231cd8eb3504d1e1b8b269fab6b92f10be6b8128e7feafa569b662d8"},
	} {
		consumerFixedSource(t, pin.name, pin.sha)
	}
	ddl := consumerFixedSource(t, "specs/022-self-service-payments/c0-production-schema.sql", "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	acl := consumerFixedSource(t, "specs/022-self-service-payments/c0-production-permissions.sql", "9ddeecca3c2e9ec9d26f77bea80c92e4505e07f0f2a4c4bdc75d9e09b02876ae")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f := &consumerFixture{}
	var err error
	f.admin, err = pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal("consumer actual installer connection failed")
	}
	t.Cleanup(func() { f.close(t) })
	var version int
	var actual bool
	if f.admin.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer,current_user=session_user AND current_user='moni_test' AND r.rolsuper AND r.rolcanlogin AND d.datdba=r.oid FROM pg_catalog.pg_roles r JOIN pg_catalog.pg_database d ON d.datname=current_database() WHERE r.rolname=current_user").Scan(&version, &actual) != nil || version/10000 != 16 || !actual {
		t.Fatal("consumer actual PG16 installer LOGIN rejected")
	}
	if f.admin.QueryRow(ctx, consumerSharedSQL).Scan(&f.sharedBefore) != nil {
		t.Fatal("consumer shared catalog baseline failed")
	}
	u := url.URL{Scheme: "postgres", Host: "127.0.0.1:55439", Path: "/moni_test", User: url.UserPassword(cfg.User, cfg.Password), RawQuery: "sslmode=disable"}
	f.env, err = paymentsandboxqa.CreateApplication(ctx, u.String(), t.TempDir())
	if err != nil || f.env == nil {
		t.Fatal("consumer genuine funded v1 install failed")
	}
	if f.env.VerifyFundsUnchanged(ctx) != nil {
		t.Fatal("consumer genuine payment funding changed before voucher source setup")
	}
	f.fundingSourceCreditID = seedConsumerFundingSources(t, f, ctx)
	f.procurementAccountID = seedConsumerProcurementAccount(t, f, ctx)
	// Build genuine pre-cutover history before installing the C0 candidate's
	// protected-writer triggers. This fixture intentionally has no epoch yet.
	f.sandboxCaptureID = seedConsumerSandboxCapture(t, f, ctx)
	seedConsumerProcurementAttempt(t, f, ctx)
	f.loadPins(t, ctx)
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("consumer offline owner pool failed")
	}
	f.pools = append(f.pools, owner)
	var current, session string
	var pid int32
	if owner.QueryRow(ctx, "SELECT current_user::text,session_user::text,pg_backend_pid()").Scan(&current, &session, &pid) != nil || current != f.env.Roles["app_owner"] || session != "moni_test" || owner.Config().ConnConfig.User != "moni_test" {
		t.Fatal("consumer original SET ROLE owner identity failed")
	}
	f.observePID(t, ctx, pid)
	if _, err = owner.Exec(ctx, string(ddl)); err != nil {
		t.Fatal("consumer frozen C0 DDL failed")
	}
	text := string(acl)
	for _, entry := range []struct{ key, role string }{{"fl_app", "app_runtime"}, {"fl_worker", "app_worker"}, {"fl_projector", "app_projector"}} {
		text = strings.ReplaceAll(text, ":\""+entry.key+"\"", pgx.Identifier{f.env.Roles[entry.role]}.Sanitize())
	}
	if strings.Contains(text, ":\"") {
		t.Fatal("consumer unresolved frozen ACL")
	}
	if _, err = owner.Exec(ctx, text); err != nil {
		t.Fatal("consumer frozen C0 ACL failed")
	}
	public, err := periodicbudget.NewVerifier(owner, periodicbudget.VerifyOptions{})
	if err != nil {
		t.Fatal("consumer ordinary periodic verifier construction failed")
	}
	if _, err = public.Verify(ctx); !errors.Is(err, periodicbudget.ErrVerifyAuthority) {
		t.Fatal("ordinary verifier accepted SET ROLE instead of actual LOGIN")
	}
	// The genuine business fixture uses several independently owned runtime
	// pools. Stop every producer before asserting the maintenance entrance has
	// no live runtime sessions; closing only this final owner pool leaves the
	// earlier funding/procurement pools legitimately idle in pg_stat_activity.
	for _, pool := range f.pools {
		pool.Close()
	}
	f.pools = nil
	f.awaitOwnedGone(t)
	f.cfg = cfg.Copy()
	f.tag = "c0_consumer_" + f.env.Schemas["app"]
	if len(f.tag) > 63 {
		f.tag = f.tag[:63]
	}
	f.cfg.RuntimeParams = map[string]string{"application_name": f.tag, "search_path": pgx.Identifier{f.env.Schemas["app"]}.Sanitize() + ",pg_catalog,pg_temp"}
	f.cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return f
}

func seedConsumerProcurementAccount(t *testing.T, f *consumerFixture, ctx context.Context) string {
	t.Helper()
	app, err := f.env.OpenPool(ctx, "app")
	if err != nil {
		t.Fatal("consumer procurement app pool failed", err)
	}
	f.pools = append(f.pools, app)
	command, err := procurement.New(app).CreateAccount(ctx, f.env.ActorIDs["admin"], procurement.CreateInput{
		OperationID: uuid.NewString(), Label: "Maintenance QA account", Provider: "local-test", State: "active", LimitAmount: "1000",
	})
	if err != nil || command.Status != "applied" {
		t.Fatal("consumer procurement account setup failed", err)
	}
	var after struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(command.After, &after) != nil || after.ID == "" {
		t.Fatal("consumer procurement account identity missing")
	}
	return after.ID
}

func seedConsumerProcurementAttempt(t *testing.T, f *consumerFixture, ctx context.Context) {
	t.Helper()
	f.procurementChannelID = uuid.NewString()
	f.procurementCredentialID = uuid.NewString()
	f.procurementVersionID = uuid.NewString()
	f.procurementModelID = uuid.NewString()
	f.procurementPriceVersionID = uuid.NewString()
	f.procurementKeyID = uuid.NewString()
	f.procurementRequestID = uuid.NewString()
	profileVersion := "maintenance-procurement-v1"
	profile := `{"version":"v1","model":"maintenance-procurement","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	requestDeadline := time.Now().UTC().Add(4 * time.Minute)
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("consumer procurement owner pool failed", err)
	}
	f.pools = append(f.pools, owner)
	tx, err := owner.Begin(ctx)
	if err != nil {
		t.Fatal("consumer procurement source fixture transaction failed", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$2,$3,$4)`, []any{f.procurementModelID, "maintenance-procurement-" + f.procurementModelID, profile, profileVersion}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{f.env.ApplicationTenantID, f.procurementModelID}},
		{`INSERT INTO member_model_grants(tenant_id,user_id,model_id) VALUES($1,$2,$3)`, []any{f.env.ApplicationTenantID, f.env.ActorIDs["owner"], f.procurementModelID}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'maintenance procurement key','qa',$4,$5,1000000)`, []any{f.procurementKeyID, f.env.ApplicationTenantID, f.env.ActorIDs["owner"], "maintenance-" + f.procurementKeyID, []string{f.procurementModelID}}},
		{`INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1000000,1000000,clock_timestamp()-interval '1 minute',$3)`, []any{f.procurementPriceVersionID, f.procurementModelID, f.env.ActorIDs["admin"]}},
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'maintenance procurement channel',$2)`, []any{f.procurementChannelID, f.procurementVersionID}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'qa-ciphertext')`, []any{f.procurementCredentialID, f.procurementChannelID}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{f.procurementVersionID, f.procurementChannelID, f.procurementCredentialID}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'maintenance-qa',1,1)`, []any{f.procurementVersionID, f.procurementChannelID, f.procurementCredentialID}},
		{`INSERT INTO cost_versions(channel_id,upstream_model,input_rate,output_rate,basis) VALUES($1,'maintenance-upstream',2000000,2000000,'contract')`, []any{f.procurementChannelID}},
	} {
		if _, err = tx.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal("consumer procurement source fact setup failed", err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("consumer procurement source fixture commit failed", err)
	}
	app, err := f.env.OpenPool(ctx, "app")
	if err != nil {
		t.Fatal("consumer procurement runtime pool failed", err)
	}
	f.pools = append(f.pools, app)
	var revision string
	if err = app.QueryRow(ctx, `SELECT management_revision::text FROM channels WHERE id=$1`, f.procurementChannelID).Scan(&revision); err != nil {
		t.Fatal("consumer procurement channel revision read failed", err)
	}
	bound, err := procurement.New(app).BindCredential(ctx, f.env.ActorIDs["admin"], procurement.BindingInput{
		OperationID: uuid.NewString(), ChannelID: f.procurementChannelID, CredentialID: f.procurementCredentialID,
		AccountID: f.procurementAccountID, ExpectedChannelRevision: revision,
	})
	if err != nil || bound.Status != "applied" {
		t.Fatal("consumer procurement credential binding failed", err)
	}
	if err = app.QueryRow(ctx, `SELECT id FROM credential_procurement_bindings WHERE credential_id=$1`, f.procurementCredentialID).Scan(&f.procurementBindingID); err != nil {
		t.Fatal("consumer procurement binding identity read failed", err)
	}
	store := billing.New(app)
	reserved, err := store.Reserve(ctx, billing.ReserveInput{
		Principal: billing.Principal{TenantID: f.env.ApplicationTenantID, UserID: f.env.ActorIDs["owner"], KeyID: f.procurementKeyID},
		RequestID: f.procurementRequestID, ModelID: f.procurementModelID, InputUpperBound: 100, OutputLimit: 20,
		RequestHMAC: "maintenance-procurement-" + uuid.NewString(), RequestDeadline: requestDeadline,
		ProfileVersion: profileVersion, PolicyVersion: "maintenance-qa",
	})
	if err != nil || reserved.Amount != 120 {
		t.Fatal("consumer genuine customer reservation failed", err)
	}
	defer store.EndRequest(f.procurementRequestID)
	var quote procurementexecution.CostQuote
	var inputRate, outputRate int64
	if err = app.QueryRow(ctx, `SELECT id,input_rate,output_rate,basis FROM cost_versions WHERE channel_id=$1 AND upstream_model=$2 ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.procurementChannelID, "maintenance-upstream").Scan(&quote.VersionID, &inputRate, &outputRate, &quote.Basis); err != nil {
		t.Fatal("consumer procurement cost quote read failed", err)
	}
	quote.InputRate, quote.OutputRate = &inputRate, &outputRate
	attempt, err := frozenattempt.Reject(ctx, store, frozenattempt.Input{
		TenantID: f.env.ApplicationTenantID, RequestID: f.procurementRequestID, Sequence: 1,
		ChannelID: f.procurementChannelID, ChannelVersionID: f.procurementVersionID,
		CredentialID: f.procurementCredentialID, UpstreamModel: "maintenance-upstream", CostQuote: &quote,
	})
	if err != nil || attempt.ID == "" {
		t.Fatal("consumer procurement upstream attempt setup failed", err)
	}
	f.procurementAttemptID = attempt.ID
	if err = store.PrepareDispatch(ctx, billing.DispatchInput{TenantID: f.env.ApplicationTenantID, AttemptID: attempt.ID}); err != nil {
		t.Fatal("consumer procurement sending setup failed", err)
	}
	if _, err = store.RecordFinalization(ctx, billing.FinalizationInput{
		ID: uuid.NewString(), TenantID: f.env.ApplicationTenantID, RequestID: f.procurementRequestID, AttemptID: attempt.ID,
		Source: "maintenance-procurement", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "missing"},
		ProtocolOutcome: "completed", TransportEndReason: "connection_error", DeliveryStage: "content", ContentWritten: true,
	}); err != nil {
		t.Fatal("consumer procurement unknown evidence setup failed", err)
	}
	proposal, err := store.ProposeProcurementCost(ctx, billing.ProcurementCostProposalInput{
		OperationID: uuid.NewString(), TenantID: f.env.ApplicationTenantID, AttemptID: attempt.ID,
		ActorID: f.env.ActorIDs["finance_operator"], Amount: 12, Basis: "invoice", EvidenceRef: "maintenance:invoice", Reason: "local procurement QA",
	})
	if err != nil {
		t.Fatal("consumer procurement proposal setup failed", err)
	}
	f.procurementProposalID = proposal.ID
	if _, err = store.ApproveProcurementCost(ctx, billing.ProcurementCostApprovalInput{
		ProposalID: proposal.ID, OperationID: uuid.NewString(), ActorID: f.env.ActorIDs["finance_approver"],
	}); err != nil {
		t.Fatal("consumer procurement approval setup failed", err)
	}
	amount := "900"
	observation, err := procurement.New(app).AddObservation(ctx, f.env.ActorIDs["admin"], f.procurementAccountID, procurement.ObservationInput{
		OperationID: uuid.NewString(), Status: "known", Amount: &amount, Currency: "CNY", Source: "manual", Semantic: "local QA observation", ObservedAt: time.Now().UTC().Truncate(time.Microsecond), EvidenceRef: "maintenance:balance",
	})
	if err != nil || observation.Status != "applied" {
		t.Fatal("consumer observation setup failed", err)
	}
	var observed struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(observation.After, &observed) != nil || observed.ID == "" {
		t.Fatal("consumer observation receipt missing")
	}
	f.procurementObservationID = observed.ID
	rejected, err := procurement.New(app).ConfigureAccount(ctx, f.env.ActorIDs["admin"], uuid.NewString(), procurement.ConfigurationInput{
		OperationID: uuid.NewString(), ExpectedRevision: "1", Label: "missing account", Provider: "local-test", State: "active", LimitAmount: "1000",
	})
	if err != nil || rejected.Status != "rejected" || rejected.Reason != "not_found" {
		t.Fatal("consumer command rejection setup failed", err)
	}

}

func damageConsumerProcurementAttempt(t *testing.T, f *consumerFixture, ctx context.Context) {
	t.Helper()
	tx, err := f.admin.Begin(ctx)
	if err != nil {
		t.Fatal("consumer attempt mismatch fixture transaction failed", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role='replica'"); err != nil {
		t.Fatal("consumer attempt mismatch fixture trigger bypass failed", err)
	}
	schema := pgx.Identifier{f.env.Schemas["app"]}.Sanitize()
	tag, err := tx.Exec(ctx, "UPDATE "+schema+".procurement_reservations SET input_bound=input_bound+1 WHERE attempt_id=$1", f.procurementAttemptID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("consumer procurement attempt bound mismatch fixture failed", err)
	}
	tag, err = tx.Exec(ctx, "UPDATE "+schema+".procurement_cost_proposals SET evidence_ref='maintenance:damaged' WHERE id=$1", f.procurementProposalID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("consumer procurement proposal mismatch fixture failed", err)
	}
	tag, err = tx.Exec(ctx, "UPDATE "+schema+".api_keys SET held=held+1 WHERE id=$1", f.procurementKeyID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("consumer customer Key mismatch fixture failed", err)
	}
	tag, err = tx.Exec(ctx, "UPDATE "+schema+".procurement_balance_observations SET evidence_ref='maintenance:damaged-observation' WHERE id=$1", f.procurementObservationID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("consumer observation mismatch fixture failed", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal("consumer attempt mismatch fixture commit failed", err)
	}
}

func (f *consumerFixture) procurementAttemptSnapshot(ctx context.Context) ([]byte, error) {
	schema := pgx.Identifier{f.env.Schemas["app"]}.Sanitize()
	var snapshot string
	err := f.admin.QueryRow(ctx, `SELECT jsonb_build_object(
 'channel',(SELECT to_jsonb(x) FROM `+schema+`.channels x WHERE x.id=$1),
 'credential',(SELECT jsonb_build_object('id',x.id,'channel_id',x.channel_id,'revoked_at',x.revoked_at) FROM `+schema+`.channel_credentials x WHERE x.id=$2),
 'version',(SELECT to_jsonb(x) FROM `+schema+`.channel_versions x WHERE x.id=$3),
 'pool',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.channel_version_id,x.credential_id),'[]'::jsonb) FROM `+schema+`.channel_version_credentials x WHERE x.channel_version_id=$3),
 'cost',(SELECT to_jsonb(x) FROM `+schema+`.cost_versions x WHERE x.channel_id=$1 AND x.upstream_model='maintenance-upstream' ORDER BY x.effective_at DESC,x.created_at DESC,x.id DESC LIMIT 1),
 'account',(SELECT to_jsonb(x) FROM `+schema+`.procurement_accounts x WHERE x.id=$6),
 'binding',(SELECT to_jsonb(x) FROM `+schema+`.credential_procurement_bindings x WHERE x.id=$7),
	 'request',(SELECT jsonb_build_object('id',x.id,'tenant_id',x.tenant_id,'key_id',x.key_id,'owner_id',x.owner_id,'model_id',x.model_id,'price_version_id',x.price_version_id,'input_upper_bound',x.input_upper_bound,'output_limit',x.output_limit,'profile_version',x.profile_version,'policy_version',x.policy_version,'status',x.status,'billing_status',x.billing_status,'request_deadline',x.request_deadline,'created_at',x.created_at) FROM `+schema+`.request_records x WHERE x.id=$4),
 'customer_key',(SELECT jsonb_build_object('id',x.id,'tenant_id',x.tenant_id,'owner_id',x.owner_id,'held',x.held,'spent',x.spent,'lifetime_limit',x.lifetime_limit) FROM `+schema+`.api_keys x WHERE x.id=$8),
 'customer_reservation',(SELECT to_jsonb(x) FROM `+schema+`.reservations x WHERE x.request_id=$4),
 'attempt',(SELECT to_jsonb(x) FROM `+schema+`.upstream_attempts x WHERE x.id=$5),
 'supplier_reservation',(SELECT to_jsonb(x) FROM `+schema+`.procurement_reservations x WHERE x.attempt_id=$5),
 'budget_events',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]'::jsonb) FROM `+schema+`.procurement_budget_events x WHERE x.attempt_id=$5),
 'proposals',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]'::jsonb) FROM `+schema+`.procurement_cost_proposals x WHERE x.attempt_id=$5),
 'corrections',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]'::jsonb) FROM `+schema+`.provider_cost_corrections x WHERE x.attempt_id=$5),
 'commands',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.operation_id),'[]'::jsonb) FROM `+schema+`.procurement_commands x),
 'observations',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]'::jsonb) FROM `+schema+`.procurement_balance_observations x),
 'audits',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]'::jsonb) FROM `+schema+`.audit_events x),
 'outbox',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.event_id),'[]'::jsonb) FROM `+schema+`.projection_outbox x)
)::text`, f.procurementChannelID, f.procurementCredentialID, f.procurementVersionID, f.procurementRequestID, f.procurementAttemptID, f.procurementAccountID, f.procurementBindingID, f.procurementKeyID).Scan(&snapshot)
	if err != nil {
		return nil, err
	}
	return []byte(snapshot), nil
}

func (f *consumerFixture) loadPins(t *testing.T, ctx context.Context) {
	t.Helper()
	var db uint32
	if f.admin.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_database WHERE datname=current_database()").Scan(&db) != nil {
		t.Fatal("consumer database pin failed")
	}
	role := func(key string, login bool) fundinglotcatalog.RolePin {
		r := fundinglotcatalog.RolePin{Name: f.env.Roles[key]}
		var actual bool
		if r.Name == "" || f.admin.QueryRow(ctx, "SELECT oid,rolcanlogin FROM pg_catalog.pg_roles WHERE rolname=$1", r.Name).Scan(&r.OID, &actual) != nil || r.OID == 0 || actual != login {
			t.Fatal("consumer actual role pin failed")
		}
		return r
	}
	schema := func(key string) fundinglotcatalog.SchemaPin {
		o := role(key+"_owner", false)
		s := fundinglotcatalog.SchemaPin{Name: f.env.Schemas[key], NamespaceOwner: o, ObjectOwner: o}
		var owner uint32
		if f.admin.QueryRow(ctx, "SELECT oid,nspowner FROM pg_catalog.pg_namespace WHERE nspname=$1", s.Name).Scan(&s.OID, &owner) != nil || s.OID == 0 || owner != o.OID {
			t.Fatal("consumer actual schema pin failed")
		}
		return s
	}
	w := role("app_worker", true)
	f.pins = fundinglotcatalog.Pins{Mode: fundinglotcatalog.Sandbox, Database: fundinglotcatalog.DatabasePin{Host: "127.0.0.1", Port: 55439, Name: "moni_test", OID: db}, AppSchema: schema("app"), App: role("app_runtime", true), Projector: role("app_projector", true), Worker: &w, Sandbox: &fundinglotcatalog.SandboxPins{Mock: schema("mock"), MockRuntime: role("mock_runtime", true), Receiver: schema("receiver"), ReceiverRuntime: role("receiver_runtime", true)}}
}

func (f *consumerFixture) observePID(t *testing.T, ctx context.Context, pid int32) {
	t.Helper()
	b := consumerBackend{pid: pid}
	if f.admin.QueryRow(ctx, "SELECT backend_start FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND datid=$2 AND usename='moni_test'", pid, f.pins.Database.OID).Scan(&b.start) != nil || b.start.IsZero() {
		t.Fatal("consumer exact backend observation failed")
	}
	f.backends = append(f.backends, b)
}
func (f *consumerFixture) observeMaintenance(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := f.admin.Query(ctx, "SELECT pid,backend_start FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename='moni_test' AND application_name=$2", f.pins.Database.OID, f.tag)
	if err != nil {
		t.Fatal("consumer maintenance backend observation failed")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var b consumerBackend
		if rows.Scan(&b.pid, &b.start) != nil || b.pid == 0 || b.start.IsZero() || b.pid == int32(f.admin.PgConn().PID()) {
			t.Fatal("consumer independent maintenance identity failed")
		}
		f.backends = append(f.backends, b)
		count++
	}
	if rows.Err() != nil || count != 1 {
		t.Fatal("consumer expected one original maintenance backend")
	}
}
func (f *consumerFixture) ownedGone(ctx context.Context) error {
	for ctx.Err() == nil {
		alive := false
		for _, b := range f.backends {
			var present bool
			if err := f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3) OR EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE pid=$1 AND locktype='advisory')", b.pid, b.start, f.pins.Database.OID).Scan(&present); err != nil {
				return err
			}
			alive = alive || present
		}
		if f.env != nil {
			roles := []string{f.env.Roles["app_runtime"], f.env.Roles["app_worker"], f.env.Roles["app_projector"], f.env.Roles["mock_runtime"], f.env.Roles["receiver_runtime"]}
			var present bool
			if err := f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=$1 AND usename=ANY($2::text[]))", f.pins.Database.OID, roles).Scan(&present); err != nil {
				return err
			}
			alive = alive || present
		}
		if !alive {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return ctx.Err()
}
func (f *consumerFixture) awaitOwnedGone(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if f.ownedGone(ctx) != nil {
		t.Fatal("consumer exact owned backend/lock cleanup failed")
	}
}

const consumerSharedSQL = "SELECT n.nspowner::text || '/' || COALESCE(n.nspacl::text,'NULL') || '/' || (SELECT count(*)::text FROM pg_catalog.pg_class c WHERE c.relnamespace=n.oid) || '/' || COALESCE(d.datacl::text,'NULL') FROM pg_catalog.pg_namespace n,pg_catalog.pg_database d WHERE n.nspname='public' AND d.datname=current_database()"

func (f *consumerFixture) close(t *testing.T) {
	t.Helper()
	if f.admin == nil {
		return
	}
	for _, g := range f.gates {
		_ = g.Close(nil)
	}
	for _, p := range f.pools {
		p.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if f.env != nil {
		if f.ownedGone(ctx) != nil {
			t.Error("consumer sessions survived; exact fixture retained for diagnosis")
		} else {
			if f.env.Close(ctx) != nil {
				t.Error("consumer exact fixture cleanup failed")
			}
			for _, name := range f.env.Schemas {
				var exists bool
				if f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)", name).Scan(&exists) != nil || exists {
					t.Error("consumer exact schema survived cleanup")
				}
			}
			for _, name := range f.env.Roles {
				var exists bool
				if f.admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1)", name).Scan(&exists) != nil || exists {
					t.Error("consumer exact role survived cleanup")
				}
			}
		}
	}
	var after string
	if f.sharedBefore != "" && (f.admin.QueryRow(ctx, consumerSharedSQL).Scan(&after) != nil || after != f.sharedBefore) {
		t.Error("consumer changed shared public/database catalog")
	}
	if f.admin.Close(ctx) != nil {
		t.Error("consumer administrator close failed")
	}
}
