//go:build historical

package billing

import (
	"context"
	"testing"
	"time"
)

func TestProcurementApprovalReaderMapsExactNullableFactsAndMismatch(t *testing.T) {
	q := newProcurementAttemptQueryFixture()
	created := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	proposalID := "00000000-0000-4000-8000-000000000011"
	approvalID := "00000000-0000-4000-8000-000000000012"
	q.tables["provider_cost_corrections"] = [][]any{{"correction-1", "tenant-1", "attempt-1", "procurement:" + approvalID, "12", "invoice", "invoice:one", created}}
	q.tables["procurement_cost_proposals"] = [][]any{{proposalID, "tenant-1", "attempt-1", "12", "invoice", "invoice:one", "supplier statement", "maker", "1", "0", "approved", procurementAttemptString("checker"), procurementAttemptString(approvalID), created, &created}}
	q.tables["procurement_budget_events"] = append(q.tables["procurement_budget_events"], []any{"event-2", "correction:" + approvalID, "account-1", "attempt-1", "0", "0", "cost_corrected", "invoice:one"})
	facts, err := ReadProcurementAttemptBindingFacts(context.Background(), q, "private_schema")
	if err != nil || !facts.ApprovalSummary().Balanced || facts.ApprovalSummary().Scanned.Corrections != 1 || facts.ApprovalSummary().Scanned.Proposals != 1 {
		t.Fatal("exact approval fields not retained", err, facts.ApprovalSummary())
	}
	q.tables["procurement_cost_proposals"][0][5] = "invoice:other"
	facts, err = ReadProcurementAttemptBindingFacts(context.Background(), q, "private_schema")
	check := facts.ApprovalSummary()
	if err != nil || check.Balanced || check.Mismatches.ApprovedCorrectionRows != 1 || check.Mismatches.ApprovedEventRows != 1 || check.Mismatches.ManagedCorrectionApprovalRows != 1 || check.Mismatches.CorrectionEventApprovalRows != 1 {
		t.Fatal("approval data mismatch must return a complete local summary", err, check)
	}
}
