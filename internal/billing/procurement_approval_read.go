//go:build historical

package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// readProcurementApprovalPages extends the original fixed scan using its
// single resource budget. It neither opens a transaction nor finishes a lease.
func readProcurementApprovalPages(ctx context.Context, query fundingSourceQuery, schema string, rows *fundinglotverify.ProcurementCostApprovalRows, budget *fundingSourceReadBudget) error {
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "users", "id", "id"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementApprovalUserRow
		if err := source.Scan(&row.ID); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID}, func() { rows.Users = append(rows.Users, row) }, nil
	}, budget); err != nil {
		return err
	}
	if err := readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "provider_cost_corrections", "id", "id", "tenant_id", "attempt_id", "operation_id", "amount::pg_catalog.text", "basis", "evidence_ref", "created_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementCostCorrectionRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.AttemptID, &row.OperationID, &row.Amount, &row.Basis, &row.EvidenceRef, &row.CreatedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		return row.ID, []string{row.ID, row.TenantID, row.AttemptID, row.OperationID, row.Amount, row.Basis, row.EvidenceRef}, func() { rows.Corrections = append(rows.Corrections, row) }, nil
	}, budget); err != nil {
		return err
	}
	return readFundingSourcePages(ctx, query, procurementAttemptPageSQL(schema, "procurement_cost_proposals", "id", "id", "tenant_id", "attempt_id", "amount::pg_catalog.text", "basis", "evidence_ref", "reason", "requester", "expected_reservation_revision::pg_catalog.text", "expected_evidence_count::pg_catalog.text", "state", "approver", "approval_operation_id", "created_at", "approved_at"), func(source fundingSourceRows) (string, []string, func(), error) {
		var row fundinglotverify.ProcurementCostProposalRow
		if err := source.Scan(&row.ID, &row.TenantID, &row.AttemptID, &row.Amount, &row.Basis, &row.EvidenceRef, &row.Reason, &row.Requester, &row.ExpectedRevision, &row.ExpectedEvidenceCount, &row.State, &row.Approver, &row.ApprovalOperationID, &row.CreatedAt, &row.ApprovedAt); err != nil {
			return "", nil, nil, fundingSourceReadExecution
		}
		fields := procurementAttemptTextFields([]string{row.ID, row.TenantID, row.AttemptID, row.Amount, row.Basis, row.EvidenceRef, row.Reason, row.Requester, row.ExpectedRevision, row.ExpectedEvidenceCount, row.State}, row.Approver, row.ApprovalOperationID)
		return row.ID, fields, func() { rows.Proposals = append(rows.Proposals, row) }, nil
	}, budget)
}
