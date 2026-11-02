package fundinglotruntime

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// VerifyOrdinaryApplicationTx authenticates the complete ordinary graph in the
// caller's existing actual-APP RR READ ONLY transaction. expected is only an
// exact-original comparison value, never authenticated file provenance.
func VerifyOrdinaryApplicationTx(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins,
	expected *fundinglotproof.CanonicalV2) (*RuntimeProof, error) {
	return verifyApplicationTx(ctx, tx, pins, expected, nil, fundinglotcatalog.Ordinary)
}

// VerifySandboxApplicationTx additionally binds the independently loaded APP
// nonce/signing-material snapshot to its two original DB rows and the full V2
// predecessor. It does not authenticate any peer runtime's current material.
func VerifySandboxApplicationTx(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins,
	expected *fundinglotproof.CanonicalV2, local *ApplicationState) (*RuntimeProof, error) {
	return verifyApplicationTx(ctx, tx, pins, expected, local, fundinglotcatalog.Sandbox)
}

func verifyApplicationTx(ctx context.Context, tx pgx.Tx, pins fundinglotcatalog.Pins,
	expected *fundinglotproof.CanonicalV2, local *ApplicationState, mode fundinglotcatalog.Mode) (*RuntimeProof, error) {
	w, err := newVerificationWindow(ctx, runtimeBudget)
	if err != nil {
		return nil, ErrRuntime
	}
	defer w.cancel()
	if pins.Mode != mode || expected == nil || tx == nil || w.check() != nil {
		return nil, ErrRuntime
	}
	pins = cloneRuntimePins(pins)
	// Never retain a caller-mutable wrapper or use an absent expected as a
	// request to adopt a value discovered in the database.
	original, err := fundinglotproof.DecodeV2(expected.Bytes())
	if err != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	b, err := original.Bindings()
	if err != nil || b.Mode != "ordinary" && mode == fundinglotcatalog.Ordinary || b.Mode != "sandbox" && mode == fundinglotcatalog.Sandbox || w.check() != nil {
		return nil, ErrRuntime
	}
	var app *applicationSnapshot
	scanMode := fundinglotscan.ModeOrdinary
	if mode == fundinglotcatalog.Ordinary {
		if local != nil || pins.Worker != nil || pins.Sandbox != nil {
			return nil, ErrRuntime
		}
	} else {
		if pins.Worker == nil || pins.Sandbox == nil {
			return nil, ErrRuntime
		}
		app, err = snapshotApplication(w, local)
		if err != nil || w.check() != nil {
			return nil, ErrRuntime
		}
		scanMode = fundinglotscan.ModeSandbox
	}
	entry, err := verifyApplicationCatalog(w, tx, pins)
	if err != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	if verifyRuntimeHistory(w, tx, pins.AppSchema.Name) != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	q := &walletQuery{tx: tx, window: w}
	facts, err := fundinglotscan.ReadWallet(w.ctx, q, pins.AppSchema.Name, scanMode)
	if err != nil || q.check() != nil || q.pending != nil || w.check() != nil {
		q.fail()
		return nil, ErrRuntime
	}
	report := facts.Summary()
	if !runtimeReportComplete(report, scanMode) || w.check() != nil {
		return nil, ErrRuntime
	}
	// Both projections recheck the complete raw/report relationship, all eight
	// algorithms, zero mismatch/unproven, EOF, and the original shared budgets.
	epoch, err := fundinglotscan.ProjectEpochProof(w.ctx, facts)
	if err != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	payment, err := fundinglotscan.ProjectApplicationPaymentProof(w.ctx, facts)
	if err != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	bound, err := bindRuntimeOriginals(w, pins, catalogDescription(entry), original, epoch, payment, app)
	if err != nil || w.check() != nil || q.check() != nil || q.pending != nil {
		return nil, ErrRuntime
	}
	exit, err := verifyApplicationCatalog(w, tx, pins)
	if err != nil || catalogDescription(exit) != catalogDescription(entry) || w.check() != nil {
		return nil, ErrRuntime
	}
	if verifyRuntimeHistory(w, tx, pins.AppSchema.Name) != nil || w.check() != nil {
		return nil, ErrRuntime
	}
	if verifyCatalogIdentity(w, entry, tx) != nil || verifyCatalogIdentity(w, exit, tx) != nil ||
		w.check() != nil || q.check() != nil || q.pending != nil {
		return nil, ErrRuntime
	}
	proof := &RuntimeProof{state: &runtimeProofState{
		catalog: entry, finalCatalog: exit, pins: cloneRuntimePins(pins),
		epoch: bound.epoch, proofSHA: bound.proofSHA, basisSHA: bound.basisSHA, manifestSHA: bound.manifestSHA,
		basisDescriptorSHA: bound.basisDescriptorSHA, predecessorSHA: bound.predecessorSHA,
		historicalRoot: bound.compatibility.Historical.Expected, currentRoot: bound.compatibility.Current.Expected,
		compatibility: bound.compatibility, report: report,
	}}
	if w.check() != nil {
		return nil, ErrRuntime
	}
	return proof, nil
}

func verifyApplicationCatalog(w *verificationWindow, tx pgx.Tx, pins fundinglotcatalog.Pins) (*fundinglotcatalog.CatalogProof, error) {
	if w.check() != nil {
		return nil, ErrRuntime
	}
	identity, err := newVerificationWindow(w.ctx, identityBudget)
	if err != nil {
		return nil, ErrRuntime
	}
	defer identity.cancel()
	p, err := fundinglotcatalog.VerifyRuntimeProfileTx(identity.ctx, tx, pins, fundinglotcatalog.Application, fundinglotcatalog.WalletReadV2)
	if err != nil || identity.check() != nil || w.check() != nil || p == nil ||
		p.Profile() != fundinglotcatalog.WalletReadV2 || !p.MatchesPins(pins) {
		return nil, ErrRuntime
	}
	return p, nil
}

func verifyCatalogIdentity(w *verificationWindow, p *fundinglotcatalog.CatalogProof, tx pgx.Tx) error {
	if w.check() != nil || p == nil {
		return ErrRuntime
	}
	identity, err := newVerificationWindow(w.ctx, identityBudget)
	if err != nil {
		return ErrRuntime
	}
	defer identity.cancel()
	if p.VerifyTx(identity.ctx, tx) != nil || identity.check() != nil || w.check() != nil {
		return ErrRuntime
	}
	return nil
}

// VerifyTx checks only the exact original still-active RR snapshot identity.
// It does not renew the proof, rerun a catalog/history/financial scan, or retain
// the issuance context (which the composer cancels when it returns).
func (p *RuntimeProof) VerifyTx(ctx context.Context, tx pgx.Tx) error {
	w, err := newVerificationWindow(ctx, identityBudget)
	if err != nil {
		return ErrRuntime
	}
	defer w.cancel()
	if p == nil || p.state == nil || tx == nil || w.check() != nil {
		return ErrRuntime
	}
	s := p.state
	if s.catalog == nil || s.finalCatalog == nil || !s.catalog.MatchesPins(s.pins) || !s.finalCatalog.MatchesPins(s.pins) ||
		catalogDescription(s.catalog) != catalogDescription(s.finalCatalog) ||
		s.catalog.Profile() != fundinglotcatalog.WalletReadV2 ||
		s.compatibility.Current != catalogDescription(s.catalog).tuple ||
		s.historicalRoot != s.compatibility.Historical.Expected || s.currentRoot != s.compatibility.Current.Expected ||
		s.manifestSHA != currentManifestSHA || s.basisDescriptorSHA != basisDescriptorSHA {
		return ErrRuntime
	}
	if verifyCatalogIdentity(w, s.catalog, tx) != nil || verifyCatalogIdentity(w, s.finalCatalog, tx) != nil || w.check() != nil {
		return ErrRuntime
	}
	return nil
}

func runtimeReportComplete(r fundinglotscan.WalletReport, mode fundinglotscan.Mode) bool {
	if mode != fundinglotscan.ModeOrdinary && mode != fundinglotscan.ModeSandbox ||
		r.Scope != "complete_source_wallet_graphs_v1" || r.Mode != mode || r.Status != "consistent" ||
		!r.PhysicalEOFComplete || !r.CoverageComplete || r.ScannedTables != 84 || r.SourceRows > 100000 || r.SourceBytes > 16<<20 ||
		r.InputMismatchFields != 0 || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 {
		return false
	}
	for i, status := range r.Algorithms {
		want := "executed"
		if i == 6 && mode == fundinglotscan.ModeOrdinary {
			want = "not_applicable"
		}
		if status != want {
			return false
		}
	}
	return true
}
