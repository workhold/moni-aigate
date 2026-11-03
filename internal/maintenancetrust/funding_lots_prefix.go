package maintenancetrust

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

type fundingLotsSourceCounts struct{ SourceRows, SourceBytes uint64 }

func (p *fundingLotsPhase) sourceCounts() fundingLotsSourceCounts {
	if p.snapshot.sourceProfile() == fundinglotcatalog.PeriodicPrefixV3 {
		r := p.prefixFacts.Summary()
		return fundingLotsSourceCounts{r.SourceRows, r.SourceBytes}
	}
	r := p.facts.Summary()
	return fundingLotsSourceCounts{r.SourceRows, r.SourceBytes}
}

func (p *fundingLotsPhase) readPeriodicPrefixSources(ctx context.Context, query *fundingLotsSourceQuery, mode fundinglotscan.Mode) error {
	var err error
	p.prefixFacts, err = fundinglotscan.ReadPrefix(ctx, query, p.snapshot.gate.pins.AppSchema.Name, mode)
	if err != nil {
		return fundingLotsReadFailure(err, query.failure)
	}
	// This independently qualifies all source/core/raw relationships, including
	// the retained old Base gap and newly authenticated observation prefix.
	p.seed, err = fundinglotscan.ProjectLegacySeedPrefix(ctx, p.prefixFacts)
	return err
}

func (p *fundingLotsPhase) readPeriodicPrefixPost(ctx context.Context, query *fundingLotsSourceQuery, mode fundinglotscan.Mode) error {
	var err error
	p.prefixPost, err = fundinglotscan.ReadWalletPrefix(ctx, query, p.snapshot.gate.pins.AppSchema.Name, mode)
	if err != nil {
		return fundingLotsReadFailure(err, query.failure)
	}
	r := p.prefixPost.Summary()
	if r.Scope != "complete_source_wallet_prefix_graphs_v1" || r.Mode != mode || r.Status != "consistent" ||
		!r.PhysicalEOFComplete || !r.CoverageComplete || r.ScannedTables != 87 || r.ScannedColumns != 804 ||
		!fundingLotsWalletCounts(r.Wallet.Scanned, r.SourceRows, p.sourceCounts().SourceRows, p.draft) {
		return ErrTransaction
	}
	// The projection rechecks every child, fixed column, EOF and shared budget;
	// the phase then compares the actual immutable originals via its own reader.
	if _, err = fundinglotscan.ProjectEpochProofPrefix(ctx, p.prefixPost); err != nil {
		return err
	}
	return nil
}

func readOwnedBasis(ctx context.Context, q fundinglotbasis.Query, schema string, profile fundinglotcatalog.Profile) (fundinglotbasis.Facts, error) {
	if profile == fundinglotcatalog.PeriodicPrefixV3 {
		return fundinglotbasis.ReadPrefix(ctx, q, schema)
	}
	if profile != fundinglotcatalog.LegacyV1 {
		return fundinglotbasis.Facts{}, ErrSchema
	}
	return fundinglotbasis.Read(ctx, q, schema)
}

func fundingLotsProofBasisProfileValid(b fundinglotbasis.Report, profile fundinglotcatalog.Profile) bool {
	if profile == fundinglotcatalog.PeriodicPrefixV3 {
		return b.Scope == "fixed_112_table_prefix_basis_v1" && b.PhysicalEOFComplete && b.ScannedTables == 112 && b.ScannedColumns == 1004 &&
			b.SourceRows <= 100000 && b.SourceBytes <= 64<<20 && fundingLotsDigest(b.BasisSHA256) &&
			b.ManifestSHA256 == authorityManifestSHA && b.DescriptorSHA256 == fundinglotbasis.PeriodicPrefixDescriptorSHA256 && b.SendMetadataSHA256 == fundingLotsBasisSend
	}
	return profile == fundinglotcatalog.LegacyV1 && fundingLotsProofBasisValid(b)
}
