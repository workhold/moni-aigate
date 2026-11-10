//go:build historical

package maintenancescan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// PrefixSourcesResult and PrefixWalletResult belong to their original lease.
// They grant no initialization, epoch, runtime or financial-write capability.
type PrefixSourcesResult struct {
	facts fundinglotscan.PrefixFacts
	mode  fundinglotscan.Mode
	proof *maintenancetrust.ScanProof
}
type PrefixWalletResult struct {
	facts fundinglotscan.PrefixWalletFacts
	mode  fundinglotscan.Mode
	proof *maintenancetrust.ScanProof
}

func (PrefixSourcesResult) String() string   { return "prefix_sources_result_redacted" }
func (PrefixSourcesResult) GoString() string { return "prefix_sources_result_redacted" }
func (PrefixSourcesResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"prefix_sources_result":"redacted"}`), nil
}
func (PrefixWalletResult) String() string   { return "prefix_wallet_result_redacted" }
func (PrefixWalletResult) GoString() string { return "prefix_wallet_result_redacted" }
func (PrefixWalletResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"prefix_wallet_result":"redacted"}`), nil
}

func (r *PrefixSourcesResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return fundinglotscan.ErrInput
	}
	s := r.facts.Summary()
	if s.Scope != "complete_business_source_prefix_graphs_v1" || !s.PhysicalEOFComplete || s.ScannedTables != 71 || s.ScannedColumns != 635 ||
		s.Mode != r.mode || r.mode != fundinglotscan.ModeOrdinary && r.mode != fundinglotscan.ModeSandbox {
		return fundinglotscan.ErrInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanCompleteSources); err != nil {
		return completeSourcesMaintenanceFailure(nil, err)
	}
	return nil
}
func (r *PrefixSourcesResult) Summary(permit *maintenancetrust.Permit) (fundinglotscan.PrefixReport, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotscan.PrefixReport{}, err
	}
	return r.facts.Summary(), nil
}
func (r *PrefixWalletResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return fundinglotscan.ErrInput
	}
	s := r.facts.Summary()
	if s.Scope != "complete_source_wallet_prefix_graphs_v1" || !s.PhysicalEOFComplete || s.ScannedTables != 87 || s.ScannedColumns != 804 ||
		s.Mode != r.mode || r.mode != fundinglotscan.ModeOrdinary && r.mode != fundinglotscan.ModeSandbox {
		return fundinglotscan.ErrInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanSourceWallet); err != nil {
		return completeSourcesMaintenanceFailure(nil, err)
	}
	return nil
}
func (r *PrefixWalletResult) Summary(permit *maintenancetrust.Permit) (fundinglotscan.PrefixWalletReport, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotscan.PrefixWalletReport{}, err
	}
	return r.facts.Summary(), nil
}

func prefixLeaseSource(lease *maintenancetrust.ReadLease) (fundinglotscan.Mode, string, error) {
	profile, err := lease.SourceProfile()
	if err != nil {
		return 0, "", completeSourcesMaintenanceFailure(lease, err)
	}
	if profile != fundinglotcatalog.PeriodicPrefixV3 {
		return 0, "", fundinglotscan.ErrExecution
	}
	original, err := lease.SourceMode()
	if err != nil {
		return 0, "", completeSourcesMaintenanceFailure(lease, err)
	}
	var mode fundinglotscan.Mode
	switch original {
	case maintenancetrust.SourceModeOrdinary:
		mode = fundinglotscan.ModeOrdinary
	case maintenancetrust.SourceModeSandbox:
		mode = fundinglotscan.ModeSandbox
	default:
		return 0, "", fundinglotscan.ErrExecution
	}
	var schema string
	if err = lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return 0, "", completeSourcesMaintenanceFailure(lease, err)
	}
	return mode, schema, nil
}

// VerifyPrefixSourcesMaintenance shares kind6's original once bit. Selecting a
// different wrapper after a failure cannot grant another attempt.
func VerifyPrefixSourcesMaintenance(lease *maintenancetrust.ReadLease) (*PrefixSourcesResult, error) {
	if lease == nil {
		return nil, fundinglotscan.ErrInput
	}
	if err := lease.Require(maintenancetrust.ScanCompleteSources); err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	defer lease.Abort()
	mode, schema, err := prefixLeaseSource(lease)
	if err != nil {
		return nil, err
	}
	q := &completeSourcesLeaseQuery{lease: lease}
	facts, err := fundinglotscan.ReadPrefix(completeSourcesLeaseContext{lease: lease}, q, schema, mode)
	if err != nil {
		return nil, prefixLeaseReadFailure(lease, q, err)
	}
	proof, err := lease.Finish()
	if err != nil || proof == nil {
		if err == nil {
			err = fundinglotscan.ErrExecution
		}
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	return &PrefixSourcesResult{facts: facts, mode: mode, proof: proof}, nil
}

// VerifyPrefixWalletMaintenance shares kind7's original once bit.
func VerifyPrefixWalletMaintenance(lease *maintenancetrust.ReadLease) (*PrefixWalletResult, error) {
	if lease == nil {
		return nil, fundinglotscan.ErrInput
	}
	if err := lease.Require(maintenancetrust.ScanSourceWallet); err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	defer lease.Abort()
	mode, schema, err := prefixLeaseSource(lease)
	if err != nil {
		return nil, err
	}
	q := &completeSourcesLeaseQuery{lease: lease}
	facts, err := fundinglotscan.ReadWalletPrefix(completeSourcesLeaseContext{lease: lease}, q, schema, mode)
	if err != nil {
		return nil, prefixLeaseReadFailure(lease, q, err)
	}
	proof, err := lease.Finish()
	if err != nil || proof == nil {
		if err == nil {
			err = fundinglotscan.ErrExecution
		}
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	return &PrefixWalletResult{facts: facts, mode: mode, proof: proof}, nil
}

func prefixLeaseReadFailure(lease *maintenancetrust.ReadLease, q *completeSourcesLeaseQuery, err error) error {
	if errors.Is(err, fundinglotscan.ErrCanceled) && q.failure != nil {
		err = q.failure
	}
	return completeSourcesMaintenanceFailure(lease, err)
}
