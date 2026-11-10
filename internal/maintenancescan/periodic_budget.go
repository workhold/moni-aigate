//go:build historical

package maintenancescan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// MaintenanceResult proves completion of the fixed periodic-budget scan,
// not coverage of the wallet, payment, funding, key or complete C0 source graph.
// Its report preserves all three existing historical coverage gaps.
type MaintenanceResult struct {
	report periodicbudget.Verification
	proof  *maintenancetrust.ScanProof
}

// MaintenanceSummary is a descriptive copy, never maintenance authority.
type MaintenanceSummary struct {
	Scope        string                      `json:"scope"`
	Verification periodicbudget.Verification `json:"verification"`
}

func (r *MaintenanceResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil || r.report.SchemaVersion != 1 {
		return periodicbudget.ErrVerifyInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanPeriodicBudget); err != nil {
		if errors.Is(err, maintenancetrust.ErrTimeout) {
			return periodicbudget.ErrVerifyTimeout
		}
		return periodicbudget.ErrVerifyAuthority
	}
	return nil
}

// Report returns only the original local periodic-budget report. A caller must
// not treat Balanced or CoverageComplete as proof about any other source.
func (r *MaintenanceResult) Report(permit *maintenancetrust.Permit) (periodicbudget.Verification, error) {
	if err := r.Verify(permit); err != nil {
		return periodicbudget.Verification{}, err
	}
	return r.report, nil
}
func (r *MaintenanceResult) Summary(permit *maintenancetrust.Permit) (MaintenanceSummary, error) {
	report, err := r.Report(permit)
	if err != nil {
		return MaintenanceSummary{}, err
	}
	return MaintenanceSummary{Scope: "periodic_budget_rows", Verification: report}, nil
}
func (MaintenanceResult) String() string   { return "periodic_budget_maintenance_redacted" }
func (MaintenanceResult) GoString() string { return "periodic_budget_maintenance_redacted" }
func (MaintenanceResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"periodic_budget_maintenance":"redacted"}`), nil
}

type maintenanceVerifierQuery struct{ lease *maintenancetrust.ReadLease }

var _ periodicbudget.VerificationQuery = maintenanceVerifierQuery{}

func (q maintenanceVerifierQuery) Check() error { return q.lease.Err() }
func (q maintenanceVerifierQuery) Query(sql string, args ...any) (periodicbudget.VerificationRows, error) {
	return q.lease.Query(sql, args...)
}
func (q maintenanceVerifierQuery) QueryRow(sql string, args ...any) periodicbudget.VerificationRow {
	return q.lease.QueryRow(sql, args...)
}

func maintenanceVerificationFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, periodicbudget.ErrVerifySchema) {
		return periodicbudget.ErrVerifySchema
	}
	if errors.Is(err, maintenancetrust.ErrTimeout) || lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return periodicbudget.ErrVerifyTimeout
	}
	if errors.Is(err, maintenancetrust.ErrInput) {
		return periodicbudget.ErrVerifyInput
	}
	return periodicbudget.ErrVerifyExecution
}

// VerifyMaintenance reads exactly the authorized lease's existing snapshot. It
// never starts or finishes a transaction, changes settings or borrows a pool.
// The public Verifier retains its separate actual LOGIN/owner/RRRO checks.
func VerifyMaintenance(lease *maintenancetrust.ReadLease) (*MaintenanceResult, error) {
	if lease == nil {
		return nil, periodicbudget.ErrVerifyInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanPeriodicBudget); err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	q := maintenanceVerifierQuery{lease: lease}
	var schema string
	if err := q.QueryRow(`SELECT pg_catalog.current_schema()::pg_catalog.text`).Scan(&schema); err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	report, err := periodicbudget.ReadBoundedVerificationRows(q, schema)
	if err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	if proof == nil {
		return nil, periodicbudget.ErrVerifyExecution
	}
	return &MaintenanceResult{report: report, proof: proof}, nil
}
