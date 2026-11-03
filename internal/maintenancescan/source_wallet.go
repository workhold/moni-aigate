package maintenancescan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// SourceWalletResult binds this fixed 84-table read to its original Permit.
// It grants no initialization, basis, epoch, runtime or financial-write right.
type SourceWalletResult struct {
	facts fundinglotscan.WalletFacts
	mode  fundinglotscan.Mode
	proof *maintenancetrust.ScanProof
}

func (SourceWalletResult) String() string   { return "source_wallet_result_redacted" }
func (SourceWalletResult) GoString() string { return "source_wallet_result_redacted" }
func (SourceWalletResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"source_wallet_result":"redacted"}`), nil
}

func (r *SourceWalletResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return fundinglotscan.ErrInput
	}
	report := r.facts.Summary()
	if report.Scope != "complete_source_wallet_graphs_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 84 ||
		report.Mode != r.mode || r.mode != fundinglotscan.ModeOrdinary && r.mode != fundinglotscan.ModeSandbox {
		return fundinglotscan.ErrInput
	}
	if err := r.proof.Verify(permit, maintenancetrust.ScanSourceWallet); err != nil {
		return completeSourcesMaintenanceFailure(nil, err)
	}
	return nil
}

func (r *SourceWalletResult) Summary(permit *maintenancetrust.Permit) (fundinglotscan.WalletReport, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotscan.WalletReport{}, completeSourcesMaintenanceFailure(nil, err)
	}
	return r.facts.Summary(), nil
}

// VerifySourceWalletMaintenance is the sole trusted constructor. Original
// schema/mode, all 84 physical EOFs and eight algorithms belong to one lease;
// only its single successful Finish permits publication. An empty pre-epoch
// installation remains a data mismatch, never an initialization authorization.
func VerifySourceWalletMaintenance(lease *maintenancetrust.ReadLease) (*SourceWalletResult, error) {
	if lease == nil {
		return nil, fundinglotscan.ErrInput
	}
	if err := lease.Require(maintenancetrust.ScanSourceWallet); err != nil {
		return nil, completeSourcesMaintenanceFailure(lease, err)
	}
	defer lease.Abort()
	profile, profileErr := lease.SourceProfile()
	if profileErr != nil {
		return nil, completeSourcesMaintenanceFailure(lease, profileErr)
	}
	if profile != fundinglotcatalog.LegacyV1 {
		return nil, fundinglotscan.ErrExecution
	}
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
	facts, err := fundinglotscan.ReadWallet(completeSourcesLeaseContext{lease: lease}, query, schema, mode)
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
	return &SourceWalletResult{facts: facts, mode: mode, proof: proof}, nil
}
