package maintenancescan

import (
	"context"
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// CompleteSourcesResult binds this fixed source-union read to its exact
// originating Permit. It grants no lot, basis, epoch or financial-write right.
type CompleteSourcesResult struct {
	facts fundinglotscan.Facts
	mode  fundinglotscan.Mode
	proof *maintenancetrust.ScanProof
}

func (CompleteSourcesResult) String() string   { return "complete_sources_result_redacted" }
func (CompleteSourcesResult) GoString() string { return "complete_sources_result_redacted" }
func (CompleteSourcesResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"complete_sources_result":"redacted"}`), nil
}

func (r *CompleteSourcesResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return fundinglotscan.ErrInput
	}
	report := r.facts.Summary()
	if report.Scope != "complete_business_source_graphs_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 68 ||
		report.Mode != r.mode || r.mode != fundinglotscan.ModeOrdinary && r.mode != fundinglotscan.ModeSandbox {
		return fundinglotscan.ErrInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanCompleteSources); err != nil {
		return completeSourcesMaintenanceFailure(nil, err)
	}
	return nil
}

func (r *CompleteSourcesResult) Summary(permit *maintenancetrust.Permit) (fundinglotscan.Report, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotscan.Report{}, completeSourcesMaintenanceFailure(nil, err)
	}
	return r.facts.Summary(), nil
}

type completeSourcesLeaseContext struct{ lease *maintenancetrust.ReadLease }

func (c completeSourcesLeaseContext) Deadline() (time.Time, bool) { return c.lease.Deadline() }
func (c completeSourcesLeaseContext) Done() <-chan struct{}       { return c.lease.Done() }
func (c completeSourcesLeaseContext) Err() error {
	if c.lease == nil {
		return context.Canceled
	}
	switch c.lease.Err() {
	case nil:
		return nil
	case maintenancetrust.ErrTimeout:
		return context.DeadlineExceeded
	default:
		return context.Canceled
	}
}
func (completeSourcesLeaseContext) Value(any) any { return nil }

var _ context.Context = completeSourcesLeaseContext{}

// Remember the first fixed lease failure so a driver error which revokes its
// context cannot be mistaken for caller cancellation by the untrusted reader.
type completeSourcesLeaseQuery struct {
	lease   *maintenancetrust.ReadLease
	failure error
}
type completeSourcesLeaseRows struct {
	rows  *maintenancetrust.Rows
	query *completeSourcesLeaseQuery
}

func (q *completeSourcesLeaseQuery) observe(err error) error {
	if err != nil && q.failure == nil {
		q.failure = err
	}
	return err
}
func (q *completeSourcesLeaseQuery) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	rows, err := q.lease.Query(sql, args...)
	if err != nil {
		return nil, q.observe(err)
	}
	return &completeSourcesLeaseRows{rows: rows, query: q}, nil
}
func (r *completeSourcesLeaseRows) Next() (bool, error) {
	more, err := r.rows.Next()
	return more, r.query.observe(err)
}
func (r *completeSourcesLeaseRows) Scan(dest ...any) error {
	return r.query.observe(r.rows.Scan(dest...))
}
func (r *completeSourcesLeaseRows) Err() error   { return r.query.observe(r.rows.Err()) }
func (r *completeSourcesLeaseRows) Close() error { return r.query.observe(r.rows.Close()) }

// VerifyCompleteSourcesMaintenance is the sole trusted constructor. Mode,
// schema, all 68 physical EOFs and seven algorithms belong to this one lease;
// the completed facts are published only after the single successful Finish.
// The caller exclusively owns the lease after Require succeeds. A rejected
// Require must leave any original operation owner responsible for cleanup.
func VerifyCompleteSourcesMaintenance(lease *maintenancetrust.ReadLease) (*CompleteSourcesResult, error) {
	if lease == nil {
		return nil, fundinglotscan.ErrInput
	}
	if err := lease.Require(maintenancetrust.ScanCompleteSources); err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	defer lease.Abort()
	original, err := lease.SourceMode()
	if err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	var mode fundinglotscan.Mode
	switch original {
	case maintenancetrust.SourceModeOrdinary:
		mode = fundinglotscan.ModeOrdinary
	case maintenancetrust.SourceModeSandbox:
		mode = fundinglotscan.ModeSandbox
	default:
		return nil, fundinglotscan.ErrExecution
	}
	var schema string
	if err = lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	query := &completeSourcesLeaseQuery{lease: lease}
	facts, err := fundinglotscan.Read(completeSourcesLeaseContext{lease: lease}, query, schema, mode)
	if err != nil {
		if errors.Is(err, fundinglotscan.ErrCanceled) && query.failure != nil {
			err = query.failure
		}
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	if proof == nil {
		return nil, fundinglotscan.ErrExecution
	}
	return &CompleteSourcesResult{facts: facts, mode: mode, proof: proof}, nil
}

func completeSourcesMaintenanceFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, fundinglotscan.ErrInput) || errors.Is(err, maintenancetrust.ErrInput) {
		return fundinglotscan.ErrInput
	}
	if errors.Is(err, fundinglotscan.ErrResource) {
		return fundinglotscan.ErrResource
	}
	if errors.Is(err, fundinglotscan.ErrTimeout) || errors.Is(err, maintenancetrust.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) || lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return fundinglotscan.ErrTimeout
	}
	if errors.Is(err, fundinglotscan.ErrCanceled) || errors.Is(err, context.Canceled) {
		return fundinglotscan.ErrCanceled
	}
	return fundinglotscan.ErrExecution
}
