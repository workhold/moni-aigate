package maintenancetrust

import (
	"bytes"
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// PlanFundingLots reads and plans in this Permit's original read-only snapshot.
// Results are published only after that original transaction has committed.
func (p *Permit) PlanFundingLots(ctx context.Context, epochID string) (FundingLotsPlanResult, error) {
	ph, err := p.beginFundingLots(ctx, snapshotPlan, epochID)
	if err != nil {
		return FundingLotsPlanResult{}, err
	}
	defer ph.release()
	fail := func(err error) (FundingLotsPlanResult, error) {
		return FundingLotsPlanResult{}, ph.fail(fundingLotsFailure(err))
	}
	work := ph.call.ctx
	if err = ph.prepareFundingLots(work); err != nil {
		return fail(err)
	}
	if err = ph.verifyFundingLotsEvidence(work); err != nil {
		return fail(err)
	}
	if err = ph.advance(work, fundingLotsDraft, fundingLotsReady); err != nil {
		return fail(err)
	}
	if err = ph.commitOwned(work); err != nil {
		return FundingLotsPlanResult{}, err
	}
	source := ph.sourceCounts()
	return FundingLotsPlanResult{Expected: ph.expected, Metadata: ph.draft.Metadata,
		SourceRows: source.SourceRows, SourceBytes: source.SourceBytes,
		BasisRows: ph.basis.SourceRows, BasisBytes: ph.basis.SourceBytes}, nil
}

// InitializeFundingLots rereads every source under a new original Init owner.
// Expected values are comparisons, never a transported plan or write permit.
func (p *Permit) InitializeFundingLots(ctx context.Context, expected FundingLotsExpected) (FundingLotsInitResult, error) {
	want := expected
	ph, err := p.beginFundingLots(ctx, snapshotInit, want.EpochID)
	if err != nil {
		return FundingLotsInitResult{}, err
	}
	defer ph.release()
	fail := func(err error) (FundingLotsInitResult, error) {
		return FundingLotsInitResult{}, ph.fail(fundingLotsFailure(err))
	}
	work := ph.call.ctx
	if !fundingLotsDigest(want.BasisDigest) || !fundingLotsDigest(want.ManifestDigest) || !fundingLotsDigest(want.DescriptorDigest) || !fundingLotsDigest(want.RuntimeProofDigest) {
		return fail(ErrInput)
	}
	if err = ph.prepareFundingLots(work); err != nil {
		return fail(err)
	}
	if ph.expected != want {
		return fail(ErrTransaction)
	}
	if err = ph.advance(work, fundingLotsDraft, fundingLotsWriting); err != nil {
		return fail(err)
	}
	if err = ph.writeFundingLots(work); err != nil {
		return fail(err)
	}
	if err = ph.advance(work, fundingLotsWriting, fundingLotsPost); err != nil {
		return fail(err)
	}
	if err = ph.verifyFundingLotsPost(work); err != nil {
		return fail(err)
	}
	if err = ph.advance(work, fundingLotsPost, fundingLotsReady); err != nil {
		return fail(err)
	}
	if err = ph.commitOwned(work); err != nil {
		return FundingLotsInitResult{}, err
	}
	return FundingLotsInitResult{Metadata: ph.draft.Metadata, RuntimeProofDigest: ph.expected.RuntimeProofDigest}, nil
}

func (p *fundingLotsPhase) verifyFundingLotsPost(ctx context.Context) error {
	if err := p.noPending(ctx); err != nil {
		return err
	}
	gs := p.snapshot.gate
	gs.mu.Lock()
	post := p.stage == fundingLotsPost && p.purpose == snapshotInit
	gs.mu.Unlock()
	if !post {
		return ErrTransaction
	}
	mode, _, err := p.fundingLotsMode()
	if err != nil {
		return err
	}
	query := &fundingLotsSourceQuery{phase: p}
	if p.snapshot.sourceProfile() == fundinglotcatalog.PeriodicPrefixV3 {
		if err = p.readPeriodicPrefixPost(ctx, query, mode); err != nil {
			return err
		}
	} else {
		p.post, err = fundinglotscan.ReadWallet(ctx, query, p.snapshot.gate.pins.AppSchema.Name, mode)
		if err != nil {
			return fundingLotsReadFailure(err, query.failure)
		}
		if !fundingLotsPostQualified(p.post.Summary(), mode) || !fundingLotsPostCounts(p.post.Summary(), p.facts.Summary(), p.draft) {
			return ErrTransaction
		}
	}
	if err = p.checkFundingLotsPostProof(ctx); err != nil {
		return err
	}
	// Re-read the selected full basis, including tables outside the wallet union.
	after, err := p.readFundingLotsBasis(ctx)
	if err != nil {
		return err
	}
	if after != p.basis {
		return ErrTransaction
	}
	return p.authority.verifyEvidence(ctx)
}

func (p *fundingLotsPhase) advance(ctx context.Context, from, to fundingLotsStage) error {
	gs := p.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := p.validLocked(ctx); err != nil {
		return err
	}
	validEdge := from == fundingLotsPre && to == fundingLotsBasis || from == fundingLotsBasis && to == fundingLotsDraft
	if p.purpose == snapshotPlan {
		validEdge = validEdge || from == fundingLotsDraft && to == fundingLotsReady
	} else if p.purpose == snapshotInit {
		validEdge = validEdge || from == fundingLotsDraft && to == fundingLotsWriting || from == fundingLotsWriting && to == fundingLotsPost || from == fundingLotsPost && to == fundingLotsReady
	} else {
		validEdge = false
	}
	if p.stage != from || !validEdge || p.pending != nil || p.operation {
		return ErrTransaction
	}
	p.stage = to
	return nil
}

func (p *fundingLotsPhase) fundingLotsMode() (fundinglotscan.Mode, string, error) {
	if p.authority == nil || p.authority.predecessor == nil || p.authority.predecessor.verifyAuthority(p.authority) != nil {
		return 0, "", ErrTransaction
	}
	switch p.authority.predecessor.mode {
	case fundinglotcatalog.Ordinary:
		return fundinglotscan.ModeOrdinary, "ordinary", nil
	case fundinglotcatalog.Sandbox:
		return fundinglotscan.ModeSandbox, "sandbox", nil
	default:
		return 0, "", ErrTransaction
	}
}

func (p *fundingLotsPhase) prepareFundingLots(ctx context.Context) error {
	if err := p.verifyFundingLotsEvidence(ctx); err != nil {
		return err
	}
	if err := p.authority.verifyEmpty(ctx); err != nil {
		return err
	}
	mode, label, err := p.fundingLotsMode()
	if err != nil {
		return err
	}
	// The database clock is sampled once, in this original transaction.
	rows, err := p.query(ctx, "SELECT pg_catalog.timestamptz_send(pg_catalog.clock_timestamp())")
	if err != nil {
		return err
	}
	var instant []byte
	if err = fundingLotsSingleton(rows, &instant); err != nil {
		return err
	}
	if p.epochAt, err = fundingLotsProofTime(instant); err != nil {
		return err
	}
	query := &fundingLotsSourceQuery{phase: p}
	if p.snapshot.sourceProfile() == fundinglotcatalog.PeriodicPrefixV3 {
		if err = p.readPeriodicPrefixSources(ctx, query, mode); err != nil {
			return err
		}
	} else {
		p.facts, err = fundinglotscan.Read(ctx, query, p.snapshot.gate.pins.AppSchema.Name, mode)
		if err != nil {
			return fundingLotsReadFailure(err, query.failure)
		}
		if p.seed, err = fundinglotscan.ProjectLegacySeed(ctx, p.facts); err != nil {
			return err
		}
	}
	if err = p.readFundingLotsBirth(ctx); err != nil {
		return err
	}
	if err = p.advance(ctx, fundingLotsPre, fundingLotsBasis); err != nil {
		return err
	}
	if p.basis, err = p.readFundingLotsBasis(ctx); err != nil {
		return err
	}
	if err = p.advance(ctx, fundingLotsBasis, fundingLotsDraft); err != nil {
		return err
	}
	p.draft, err = fundinglotinit.Build(ctx, fundinglotinit.Input{EpochID: p.epochID, EpochAt: p.epochAt, Mode: label, Rows: p.seed})
	if err != nil {
		return err
	}
	if err = p.checkFundingLotsBudget(ctx); err != nil {
		return err
	}
	return p.issueFundingLotsProof(ctx)
}

// Installation and manifest objects authenticate their owner; these reads also
// recollect their actual catalog/history instead of trusting an old digest.
func (p *fundingLotsPhase) verifyFundingLotsEvidence(ctx context.Context) error {
	if err := p.noPending(ctx); err != nil {
		return err
	}
	if err := p.authority.verifyEvidence(ctx); err != nil {
		return err
	}
	manifest, err := p.authority.attestManifest(ctx)
	if err != nil {
		return err
	}
	if manifest == nil || manifest.digest != p.authority.manifest.digest || !bytes.Equal(manifest.canonical, p.authority.manifest.canonical) {
		return ErrSchema
	}
	fresh, err := verifyOwnedSnapshotCatalog(ctx, p.snapshot.rawTx, p.snapshot.gate.pins, p.snapshot.sourceProfile())
	original := p.authority.installed.Catalog()
	if err != nil || fresh == nil || original == nil || fresh.VerifyTx(ctx, p.snapshot.rawTx) != nil || !fresh.MatchesPins(p.snapshot.gate.pins) ||
		fresh.DescriptorVersion() != original.DescriptorVersion() || fresh.ExpectedSHA256() != original.ExpectedSHA256() ||
		fresh.SchemaSHA256() != original.SchemaSHA256() || fresh.RolesSHA256() != original.RolesSHA256() {
		return ErrSchema
	}
	return p.noPending(ctx)
}

func (p *fundingLotsPhase) readFundingLotsBasis(ctx context.Context) (fundinglotbasis.Report, error) {
	query := &fundingLotsSourceQuery{phase: p}
	facts, err := readOwnedBasis(ctx, fundingLotsBasisQuery{source: query}, p.snapshot.gate.pins.AppSchema.Name, p.snapshot.sourceProfile())
	if err != nil {
		return fundinglotbasis.Report{}, fundingLotsReadFailure(err, query.failure)
	}
	report := facts.Summary()
	if !fundingLotsProofBasisProfileValid(report, p.snapshot.sourceProfile()) {
		return fundinglotbasis.Report{}, ErrSchema
	}
	if err = p.noPending(ctx); err != nil {
		return fundinglotbasis.Report{}, err
	}
	return report, nil
}

func fundingLotsReadFailure(leaf, driver error) error {
	// A bounded leaf may intentionally stop before EOF; its resource result
	// takes priority over the adapter's permanent abandonment during cleanup.
	if fundingLotsFailure(leaf) == ErrResource {
		return ErrResource
	}
	if driver != nil {
		return driver
	}
	return leaf
}

func (p *fundingLotsPhase) readFundingLotsBirth(ctx context.Context) error {
	want := make(map[string]struct{}, len(p.seed.Tenants))
	for _, tenant := range p.seed.Tenants {
		if ctx.Err() != nil {
			return ErrTimeout
		}
		if _, exists := want[tenant.ID]; exists {
			return ErrTransaction
		}
		want[tenant.ID] = struct{}{}
	}
	// LIMIT permits exactly one over-budget sentinel without an unbounded read.
	rows, err := p.query(ctx, "SELECT id,fl_created_xid IS NULL FROM ONLY "+pgx.Identifier{p.snapshot.gate.pins.AppSchema.Name, "tenants"}.Sanitize()+" ORDER BY id COLLATE pg_catalog.\"C\" LIMIT 100001")
	if err != nil {
		return err
	}
	for count := 0; ; count++ {
		more, err := rows.Next()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		if count >= 100000 {
			return ErrResource
		}
		var id string
		var bornNull bool
		if err = rows.Scan(&id, &bornNull); err != nil {
			return err
		}
		if !bornNull {
			return ErrTransaction
		}
		if _, exists := want[id]; !exists {
			return ErrTransaction
		}
		delete(want, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if len(want) != 0 {
		return ErrTransaction
	}
	return p.noPending(ctx)
}

func fundingLotsSingleton(rows *fundingLotsRows, dest ...any) error {
	more, err := rows.Next()
	if err != nil {
		return err
	}
	if !more {
		return ErrSchema
	}
	if err = rows.Scan(dest...); err != nil {
		return err
	}
	if more, err = rows.Next(); err != nil {
		return err
	}
	if more {
		return ErrSchema
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

func (p *fundingLotsPhase) checkFundingLotsBudget(ctx context.Context) error {
	if err := p.noPending(ctx); err != nil {
		return err
	}
	r, m := p.sourceCounts(), p.draft.Metadata
	if len(p.draft.SettlementLots) != 0 || len(p.draft.RefundLots) != 0 {
		return ErrTransaction
	}
	var dataRows uint64
	for _, count := range []int{len(p.draft.LegacyReceipts), len(p.draft.LegacySandboxReceipts), len(p.draft.LegacySettlements), len(p.draft.LegacyApprovedRefunds),
		len(p.draft.LegacyOpenings), len(p.draft.LegacySources), len(p.draft.Operations), len(p.draft.Heads), len(p.draft.Lots), len(p.draft.Events), len(p.draft.Balances), len(p.draft.ReservationLots)} {
		if ctx.Err() != nil {
			return ErrTimeout
		}
		if uint64(count) > 100000-dataRows {
			return ErrResource
		}
		dataRows += uint64(count)
	}
	if dataRows > 99998 {
		return ErrResource
	}
	generatedRows := dataRows + 2
	if m.DataRows != dataRows || m.GeneratedRows != generatedRows {
		return ErrTransaction
	}
	if r.SourceRows > 100000 || generatedRows > 100000-r.SourceRows {
		return ErrResource
	}
	generated, err := fundingLotsGeneratedBytes(ctx, &p.draft)
	if err != nil {
		return err
	}
	const available = (16 << 20) - (1 << 20)
	if r.SourceBytes > available || generated > available-r.SourceBytes {
		return ErrResource
	}
	return p.noPending(ctx)
}
