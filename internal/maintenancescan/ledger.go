//go:build historical

package maintenancescan

import (
	"context"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// MaintenanceLedgerResult retains local ledger facts from one completed lease.
// Its proof establishes lease completion, never full-source coverage or funds.
type MaintenanceLedgerResult struct {
	facts billing.LedgerFacts
	proof *maintenancetrust.ScanProof
}

func (MaintenanceLedgerResult) String() string   { return "maintenance_ledger_result_redacted" }
func (MaintenanceLedgerResult) GoString() string { return "maintenance_ledger_result_redacted" }
func (MaintenanceLedgerResult) MarshalJSON() ([]byte, error) {
	return []byte("{\"maintenance_ledger_result\":\"redacted\"}"), nil
}

func (r *MaintenanceLedgerResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return billing.ErrLedgerFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanLedger)
}

func (r *MaintenanceLedgerResult) Summary(permit *maintenancetrust.Permit) (fundinglotverify.LedgerRowsCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.LedgerRowsCheck{}, err
	}
	return r.facts.LocalSummary(), nil
}

// The pure arithmetic observes cancellation without exposing or replacing the
// lease's SQL context. No caller context or values enter maintenance queries.
type fundingLedgerLeaseContext struct{ lease *maintenancetrust.ReadLease }

func (c fundingLedgerLeaseContext) Deadline() (time.Time, bool) { return c.lease.Deadline() }
func (c fundingLedgerLeaseContext) Done() <-chan struct{}       { return c.lease.Done() }
func (c fundingLedgerLeaseContext) Err() error                  { return c.lease.Err() }
func (fundingLedgerLeaseContext) Value(any) any                 { return nil }

var _ context.Context = fundingLedgerLeaseContext{}

type fundingLedgerLeaseQuery struct{ lease *maintenancetrust.ReadLease }

func (q fundingLedgerLeaseQuery) Query(sql string, args ...any) (billing.LedgerFactRows, error) {
	return q.lease.Query(sql, args...)
}

// VerifyLedgerMaintenance reads only the original three ledger tables. Actual
// authority/catalog/history are established by the lease's original T2 Permit.
// A local RowsConsistent result must not be used as a full-source ready flag.
func VerifyLedgerMaintenance(lease *maintenancetrust.ReadLease) (*MaintenanceLedgerResult, error) {
	if lease == nil {
		return nil, billing.ErrLedgerFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanLedger); err != nil {
		return nil, err
	}
	var schema string
	if err := lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return nil, err
	}
	if schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") {
		return nil, billing.ErrLedgerFactsExecution
	}
	snapshot, err := billing.ReadLedgerFacts(fundingLedgerLeaseContext{lease: lease}, fundingLedgerLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, err
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, err
	}
	return &MaintenanceLedgerResult{facts: snapshot, proof: proof}, nil
}
