package maintenancescan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// ProcurementBudgetMaintenanceResult proves only that the bounded account,
// reservation, and event projection scan completed on its bound lease.
type ProcurementBudgetMaintenanceResult struct {
	check fundinglotverify.ProcurementBudgetProjectionCheck
	proof *maintenancetrust.ScanProof
}

func (ProcurementBudgetMaintenanceResult) String() string {
	return "procurement_budget_maintenance_result_redacted"
}
func (ProcurementBudgetMaintenanceResult) GoString() string {
	return "procurement_budget_maintenance_result_redacted"
}
func (ProcurementBudgetMaintenanceResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"procurement_budget_maintenance_result":"redacted"}`), nil
}

func (r *ProcurementBudgetMaintenanceResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil || r.check.Scope != "procurement_budget_projection_v1" {
		return billing.ErrProcurementBudgetFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanProcurement)
}

// Summary returns a descriptive local projection check only after the exact
// ScanProcurement proof has been verified against its originating Permit.
func (r *ProcurementBudgetMaintenanceResult) Summary(permit *maintenancetrust.Permit) (fundinglotverify.ProcurementBudgetProjectionCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.ProcurementBudgetProjectionCheck{}, err
	}
	return r.check, nil
}

// VerifyProcurementBudgetMaintenance reads exactly the fixed local budget
// projection through one ScanProcurement ReadLease and finishes it once.
func VerifyProcurementBudgetMaintenance(lease *maintenancetrust.ReadLease) (*ProcurementBudgetMaintenanceResult, error) {
	if lease == nil {
		return nil, billing.ErrProcurementBudgetFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanProcurement); err != nil {
		return nil, procurementBudgetMaintenanceFailure(lease, err)
	}
	var schema string
	if err := lease.QueryRow(`SELECT pg_catalog.current_schema()::pg_catalog.text`).Scan(&schema); err != nil {
		return nil, procurementBudgetMaintenanceFailure(lease, err)
	}
	facts, err := billing.ReadProcurementBudgetFacts(fundingSourceLeaseContext{lease: lease}, fundingSourceLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, procurementBudgetMaintenanceFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, procurementBudgetMaintenanceFailure(lease, err)
	}
	if proof == nil {
		return nil, billing.ErrProcurementBudgetFactsExecution
	}
	return &ProcurementBudgetMaintenanceResult{check: facts.LocalSummary(), proof: proof}, nil
}

func procurementBudgetMaintenanceFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, billing.ErrProcurementBudgetFactsInput) {
		return billing.ErrProcurementBudgetFactsInput
	}
	if errors.Is(err, billing.ErrProcurementBudgetFactsResource) {
		return billing.ErrProcurementBudgetFactsResource
	}
	if errors.Is(err, billing.ErrProcurementBudgetFactsCanceled) {
		return billing.ErrProcurementBudgetFactsCanceled
	}
	if errors.Is(err, billing.ErrProcurementBudgetFactsTimeout) ||
		errors.Is(err, maintenancetrust.ErrTimeout) ||
		lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return billing.ErrProcurementBudgetFactsTimeout
	}
	return billing.ErrProcurementBudgetFactsExecution
}
