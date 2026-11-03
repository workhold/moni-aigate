package maintenancetrust

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// ObservePeriodicPrefixFundingLots consumes the same Gate begin opportunity
// and lifecycle as ObserveFundingLots, with the fixed V3 evidence selection.
func (g *Gate) ObservePeriodicPrefixFundingLots(ctx context.Context, expected FundingLotsExpected) (FundingLotsObservation, error) {
	return g.observeFundingLots(ctx, expected, fundinglotcatalog.PeriodicPrefixV3)
}

func (r *fundingLotsObservationRun) readManifest(ctx context.Context) ([]byte, error) {
	if r.profile == fundinglotcatalog.PeriodicPrefixV3 {
		if err := maintenanceFactsSnapshot(ctx, r.snapshot); err != nil {
			return nil, err
		}
		return readPeriodicPrefixManifestSnapshot(ctx, r.snapshot)
	}
	if r.profile != fundinglotcatalog.LegacyV1 {
		return nil, ErrSchema
	}
	return readMaintenanceManifestSnapshot(ctx, r.snapshot)
}

func (r *fundingLotsObservationRun) readWallet(ctx context.Context, mode fundinglotscan.Mode) (fundinglotscan.EpochProofRows, fundingLotsSourceCounts, error) {
	if r.profile == fundinglotcatalog.PeriodicPrefixV3 {
		facts, err := fundinglotscan.ReadWalletPrefix(ctx, r, r.snapshot.gate.pins.AppSchema.Name, mode)
		if err != nil {
			return fundinglotscan.EpochProofRows{}, fundingLotsSourceCounts{}, fundingLotsReadFailure(err, r.failure)
		}
		rows, err := fundinglotscan.ProjectEpochProofPrefix(ctx, facts)
		if err != nil || r.pending != nil {
			return fundinglotscan.EpochProofRows{}, fundingLotsSourceCounts{}, ErrSchema
		}
		summary := facts.Summary()
		return rows, fundingLotsSourceCounts{summary.SourceRows, summary.SourceBytes}, nil
	}
	facts, err := fundinglotscan.ReadWallet(ctx, r, r.snapshot.gate.pins.AppSchema.Name, mode)
	if err != nil {
		return fundinglotscan.EpochProofRows{}, fundingLotsSourceCounts{}, fundingLotsReadFailure(err, r.failure)
	}
	summary := facts.Summary()
	if !fundingLotsPostQualified(summary, mode) || r.pending != nil {
		return fundinglotscan.EpochProofRows{}, fundingLotsSourceCounts{}, ErrSchema
	}
	rows, err := fundinglotscan.ProjectEpochProof(ctx, facts)
	return rows, fundingLotsSourceCounts{summary.SourceRows, summary.SourceBytes}, err
}
