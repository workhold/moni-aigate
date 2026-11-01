package periodicbudget

import (
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// MaintenanceResult proves completion of this package's periodic-budget scan,
// not coverage of the wallet, payment, funding, key or complete C0 source graph.
// Its report preserves all three existing historical coverage gaps.
type MaintenanceResult struct {
	report Verification
	proof  *maintenancetrust.ScanProof
}

// MaintenanceSummary is a descriptive copy, never maintenance authority.
type MaintenanceSummary struct {
	Scope        string       `json:"scope"`
	Verification Verification `json:"verification"`
}

func (r *MaintenanceResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil || r.report.SchemaVersion != 1 {
		return ErrVerifyInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanPeriodicBudget); err != nil {
		if errors.Is(err, maintenancetrust.ErrTimeout) {
			return ErrVerifyTimeout
		}
		return ErrVerifyAuthority
	}
	return nil
}

// Report returns only the original local periodic-budget report. A caller must
// not treat Balanced or CoverageComplete as proof about any other source.
func (r *MaintenanceResult) Report(permit *maintenancetrust.Permit) (Verification, error) {
	if err := r.Verify(permit); err != nil {
		return Verification{}, err
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

func (q maintenanceVerifierQuery) Check() error { return q.lease.Err() }
func (q maintenanceVerifierQuery) Query(sql string, args ...any) (verifierRows, error) {
	return q.lease.Query(sql, args...)
}
func (q maintenanceVerifierQuery) QueryRow(sql string, args ...any) verifierRow {
	return q.lease.QueryRow(sql, args...)
}

func maintenanceVerificationFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, maintenancetrust.ErrTimeout) || lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return ErrVerifyTimeout
	}
	if errors.Is(err, maintenancetrust.ErrInput) {
		return ErrVerifyInput
	}
	return ErrVerifyExecution
}

// VerifyMaintenance reads exactly the authorized lease's existing snapshot. It
// never starts or finishes a transaction, changes settings or borrows a pool.
// The public Verifier retains its separate actual LOGIN/owner/RRRO checks.
func VerifyMaintenance(lease *maintenancetrust.ReadLease) (*MaintenanceResult, error) {
	if lease == nil {
		return nil, ErrVerifyInput
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
	if schema == "" || schema == "pg_catalog" || strings.HasPrefix(schema, "pg_temp") {
		return nil, ErrVerifySchema
	}
	render := verifierRenderer(schema)
	if err := verifyMaintenanceResources(q, render); err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	report, err := readVerification(q, render)
	if err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, maintenanceVerificationFailure(lease, err)
	}
	if proof == nil {
		return nil, ErrVerifyExecution
	}
	return &MaintenanceResult{report: report, proof: proof}, nil
}

const (
	maintenanceVerifierMaxRows       = 100000
	maintenanceVerifierMaxFieldBytes = 65536
	maintenanceVerifierMaxBytes      = 16 << 20
)

// This fixed preflight covers every declared field and every row in all 19
// relations, including the complete audit and migration tables. Only a scalar
// qualification leaves PostgreSQL. It is a maintenance compatibility boundary,
// not a bound on PostgreSQL aggregate/sort memory or the application's RSS.
var maintenanceVerifierResourcesSQL = maintenanceVerifierResourceSQL()

func maintenanceVerifierResourceSQL() string {
	aliases := make([]string, 0, len(verifierTables))
	for alias := range verifierTables {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	parts := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		var lengths, eligible []string
		for _, column := range strings.Fields(verifierColumns[alias]) {
			field := "source." + pgx.Identifier{column}.Sanitize()
			length := "coalesce(pg_catalog.octet_length(pg_catalog.convert_to(" + field + "::pg_catalog.text,'UTF8')),0)"
			lengths = append(lengths, length+"::pg_catalog.numeric")
			eligible = append(eligible, length+"<=65536")
		}
		parts = append(parts, "SELECT pg_catalog.count(*)::pg_catalog.numeric AS rows,coalesce(pg_catalog.sum("+strings.Join(lengths, "+")+"),0::pg_catalog.numeric) AS bytes,coalesce(pg_catalog.bool_and("+strings.Join(eligible, " AND ")+"),true) AS eligible FROM @"+alias+"@ AS source")
	}
	return "SELECT coalesce(pg_catalog.sum(rows),0::pg_catalog.numeric)<=100000 AND coalesce(pg_catalog.sum(bytes),0::pg_catalog.numeric)<=16777216 AND coalesce(pg_catalog.bool_and(eligible),true) FROM (" + strings.Join(parts, " UNION ALL ") + ") AS guarded"
}

func verifyMaintenanceResources(q verifierQuery, render verifierRender) error {
	if err := q.Check(); err != nil {
		return err
	}
	var eligible bool
	if err := q.QueryRow(render(maintenanceVerifierResourcesSQL)).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return ErrVerifyExecution
	}
	return q.Check()
}
