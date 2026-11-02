package maintenancescan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// ProcurementMaintenanceResult binds the three scoped procurement checks from
// one complete ScanProcurement projection to the exact originating Permit.
// The summaries do not represent complete procurement coverage.
type ProcurementMaintenanceResult struct {
	budget   fundinglotverify.ProcurementBudgetProjectionCheck
	binding  fundinglotverify.ProcurementAttemptBindingCheck
	approval fundinglotverify.ProcurementCostApprovalCheck
	proof    *maintenancetrust.ScanProof
}

func (ProcurementMaintenanceResult) String() string {
	return "procurement_maintenance_result_redacted"
}
func (ProcurementMaintenanceResult) GoString() string {
	return "procurement_maintenance_result_redacted"
}
func (ProcurementMaintenanceResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"procurement_maintenance_result":"redacted"}`), nil
}

func (r *ProcurementMaintenanceResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil || r.budget.Scope != "procurement_budget_projection_v1" ||
		r.binding.Scope != "procurement_attempt_binding_graph_v1" ||
		r.approval.Scope != "procurement_cost_approval_graph_v1" {
		return billing.ErrProcurementAttemptBindingFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanProcurement)
}

// BudgetSummary returns the scoped account/reservation/event consistency check
// only after the exact ScanProcurement proof has been verified.
func (r *ProcurementMaintenanceResult) BudgetSummary(permit *maintenancetrust.Permit) (fundinglotverify.ProcurementBudgetProjectionCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.ProcurementBudgetProjectionCheck{}, err
	}
	return r.budget, nil
}

// BindingSummary returns the scoped credential/attempt/quote relation check
// only after the exact ScanProcurement proof has been verified.
func (r *ProcurementMaintenanceResult) BindingSummary(permit *maintenancetrust.Permit) (fundinglotverify.ProcurementAttemptBindingCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.ProcurementAttemptBindingCheck{}, err
	}
	return r.binding, nil
}

// ApprovalSummary returns only the proposal/correction/event relation check
// after the exact originating ScanProcurement proof has been verified.
func (r *ProcurementMaintenanceResult) ApprovalSummary(permit *maintenancetrust.Permit) (fundinglotverify.ProcurementCostApprovalCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.ProcurementCostApprovalCheck{}, err
	}
	return r.approval, nil
}

type procurementAttemptLeaseQuery struct{ lease *maintenancetrust.ReadLease }

func (q procurementAttemptLeaseQuery) Query(sql string, args ...any) (billing.ProcurementAttemptBindingFactRows, error) {
	return q.lease.Query(sql, args...)
}

// VerifyProcurementMaintenance reads the complete fixed procurement budget
// and attempt/approval projections through one ScanProcurement lease. A
// complete but unbalanced scan still finishes normally and returns all three local
// mismatch summaries; execution errors abort the lease and return no result.
func VerifyProcurementMaintenance(lease *maintenancetrust.ReadLease) (*ProcurementMaintenanceResult, error) {
	if lease == nil {
		return nil, billing.ErrProcurementAttemptBindingFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanProcurement); err != nil {
		return nil, procurementMaintenanceFailure(lease, err)
	}
	var schema string
	if err := lease.QueryRow(`SELECT pg_catalog.current_schema()::pg_catalog.text`).Scan(&schema); err != nil {
		return nil, procurementMaintenanceFailure(lease, err)
	}
	facts, err := billing.ReadProcurementAttemptBindingFacts(fundingSourceLeaseContext{lease: lease}, procurementAttemptLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, procurementMaintenanceFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil || proof == nil {
		return nil, procurementMaintenanceFailure(lease, err)
	}
	return &ProcurementMaintenanceResult{
		budget: facts.BudgetSummary(), binding: facts.BindingSummary(), approval: facts.ApprovalSummary(), proof: proof,
	}, nil
}

func procurementMaintenanceFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, billing.ErrProcurementAttemptBindingFactsInput) {
		return billing.ErrProcurementAttemptBindingFactsInput
	}
	if errors.Is(err, billing.ErrProcurementAttemptBindingFactsResource) {
		return billing.ErrProcurementAttemptBindingFactsResource
	}
	if errors.Is(err, billing.ErrProcurementAttemptBindingFactsCanceled) {
		return billing.ErrProcurementAttemptBindingFactsCanceled
	}
	if errors.Is(err, billing.ErrProcurementAttemptBindingFactsTimeout) ||
		errors.Is(err, maintenancetrust.ErrTimeout) ||
		lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return billing.ErrProcurementAttemptBindingFactsTimeout
	}
	return billing.ErrProcurementAttemptBindingFactsExecution
}
