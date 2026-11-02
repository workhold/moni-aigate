package fundinglotscan

import (
	"context"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// EpochProofRows is ordinary supplied data. It carries no database, snapshot,
// initialization, runtime, or financial authority.
type EpochProofRows struct {
	Epoch       fundinglotverify.WalletLotsEpochRow
	Attestation fundinglotverify.WalletLotsAttestationRow
}

// ProjectEpochProof copies the two unique originals from one completed and
// qualified 84-table ReadWallet. It performs no query and uses only this call's
// context. The output never shares nullable pointers or byte storage with Facts.
func ProjectEpochProof(ctx context.Context, facts WalletFacts) (EpochProofRows, error) {
	if ctx == nil {
		return EpochProofRows{}, ErrInput
	}
	if ctx.Err() != nil {
		return EpochProofRows{}, contextFailure(ctx)
	}
	f := facts.state
	if f == nil || f.raw == nil || !epochProofReportQualified(f) {
		return EpochProofRows{}, ErrInput
	}
	if err := epochProofRaw(ctx, f); err != nil {
		return EpochProofRows{}, err
	}
	if err := legacySeedScanned(ctx, f.raw); err != nil {
		return EpochProofRows{}, err
	}
	if !legacySeedCounts(f.raw, f.report.Wallet.Scanned, legacySeedBindingTables(walletBindings())) {
		return EpochProofRows{}, ErrInput
	}
	// projectInto records any failed cell qualification only in this fresh view.
	// The old Query, context and mutable rejected map are deliberately absent.
	view := &session{ctx: ctx, tables: f.raw.tables, index: f.raw.index, rejected: map[cellRef]bool{}}
	b := walletBindings()
	var epochs []fundinglotverify.WalletLotsEpochRow
	var attestations []fundinglotverify.WalletLotsAttestationRow
	qualified := true
	if err := projectInto(view, b[9], &epochs, &qualified); err != nil {
		return EpochProofRows{}, err
	}
	if err := projectInto(view, b[10], &attestations, &qualified); err != nil {
		return EpochProofRows{}, err
	}
	if !qualified || len(view.rejected) != 0 || len(epochs) != 1 || len(attestations) != 1 {
		return EpochProofRows{}, ErrInput
	}
	out := EpochProofRows{Epoch: epochs[0], Attestation: attestations[0]}
	if len(out.Attestation.ProofCanonical) == 0 || !epochProofTime(out.Epoch.CreatedAt) || !epochProofTime(out.Attestation.CreatedAt) {
		return EpochProofRows{}, ErrInput
	}
	if err := cloneLegacySeedStrings(ctx, reflect.ValueOf(&out).Elem()); err != nil {
		return EpochProofRows{}, err
	}
	if ctx.Err() != nil {
		return EpochProofRows{}, contextFailure(ctx)
	}
	return out, nil
}

func epochProofTime(t fundinglotverify.WalletLotsTime) bool {
	return t.Valid && t.Infinity == 0 && t.Value.Year() >= -4712 && t.Value.Year() <= 294276 && t.Value.Nanosecond()%1000 == 0
}

func epochProofReportQualified(f *walletFactsState) bool {
	r, s := f.report, f.raw
	if r.Scope != "complete_source_wallet_graphs_v1" || r.Mode != s.mode || s.mode != ModeOrdinary && s.mode != ModeSandbox ||
		r.Status != "consistent" || !r.PhysicalEOFComplete || r.ScannedTables != walletTableCount || !r.CoverageComplete ||
		r.SourceRows > rowLimit || r.SourceBytes > byteLimit || r.InputMismatchFields != 0 || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 || len(s.rejected) != 0 {
		return false
	}
	// The aggregate must still describe the original seven source checks, not a
	// report copied onto different raw facts. These structs contain no authority.
	if r.Ledger != s.report.Ledger || r.Links != s.report.Links || r.Funding != s.report.Funding || r.Customer != s.report.Customer ||
		r.Procurement != s.report.Procurement || r.Periodic != s.report.Periodic || r.Sandbox != s.report.Sandbox ||
		s.report.Status != "consistent" || !s.report.CoverageComplete || s.report.JournalMetadataMismatches != 0 || s.report.OrdinaryBoundaryMismatches != 0 {
		return false
	}
	for i, state := range r.Algorithms {
		want := "executed"
		if i == 6 && s.mode == ModeOrdinary {
			want = "not_applicable"
		}
		if state != want || i < 7 && s.report.Algorithms[i] != want {
			return false
		}
	}
	if r.Ledger.Scope != "supplied_ledger_rows" || !r.Ledger.RowsConsistent || r.Ledger.Mismatches != (fundinglotverify.LedgerRowMismatches{}) ||
		r.Links.Scope != "manual_voucher_receipt_links" || !r.Links.ScopedLinksConsistent || r.Links.UnknownReceiptKinds != 0 || r.Links.Mismatches != (fundinglotverify.FundingSourceLinkMismatches{}) ||
		r.Funding.Scope != "manual_voucher_funding_graph_v1" || !r.Funding.ScopedGraphConsistent || r.Funding.Mismatches != (fundinglotverify.FundingSourceGraphMismatches{}) ||
		r.Customer.Scope != "customer_key_financial_graph_v1" || !r.Customer.Balanced || r.Customer.Mismatches != (fundinglotverify.CustomerKeyGraphMismatches{}) ||
		r.Procurement.Scope != "procurement_domain_evidence_graph_v1" || r.Procurement.Status != "balanced" || !r.Procurement.Balanced || !r.Procurement.CoverageComplete ||
		r.Procurement.Structural != (fundinglotverify.ProcurementEvidenceGraphStructural{}) || r.Procurement.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{}) || r.Procurement.Unproven != (fundinglotverify.ProcurementEvidenceGraphUnproven{}) ||
		r.Periodic.Scope != "periodic_budget_graph_v1" || r.Periodic.Status != "balanced" || !r.Periodic.Balanced || !r.Periodic.CoverageComplete ||
		r.Periodic.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) || r.Periodic.Unproven != (fundinglotverify.PeriodicBudgetGraphGaps{}) ||
		r.Wallet.Scope != "supplied_wallet_lots_rows" || r.Wallet.Status != "consistent" || !r.Wallet.RowsConsistent || r.Wallet.Sequence != (fundinglotverify.WalletSequenceRowMismatches{}) ||
		r.Wallet.Mismatches != (fundinglotverify.WalletLotsRowMismatches{}) || r.Wallet.Unproven != (fundinglotverify.WalletLotsRowUnproven{}) {
		return false
	}
	if s.mode == ModeOrdinary {
		return r.Sandbox == (fundinglotverify.SandboxSourceGraphCheck{}) && r.Links.UncoveredSandboxReceipts == 0 && r.Funding.UncoveredSandboxReceipts == 0
	}
	return r.Sandbox.Scope == "sandbox_stored_verified_source_graph_v1" && r.Sandbox.Status == "consistent" && r.Sandbox.ScopedGraphConsistent &&
		r.Sandbox.Mismatches == (fundinglotverify.SandboxSourceGraphMismatches{}) && r.Sandbox.Unproven == (fundinglotverify.SandboxSourceGraphUnproven{}) &&
		r.Links.UncoveredSandboxReceipts >= 0 && uint64(r.Links.UncoveredSandboxReceipts) == r.Funding.UncoveredSandboxReceipts
}

// Recheck the entire physical union and all logical transport budgets before
// allocating the DTO. No data is queried or reinterpreted as an initialization
// budget, and the larger fixed proof column remains the only field exception.
func epochProofRaw(ctx context.Context, f *walletFactsState) error {
	s := f.raw
	want, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings())
	if err != nil {
		return ErrExecution
	}
	if len(s.tables) != walletTableCount {
		return ErrInput
	}
	if !reflect.DeepEqual(s.index, index) {
		return ErrExecution
	}
	budget := &session{}
	for i, table := range s.tables {
		if ctx.Err() != nil {
			return contextFailure(ctx)
		}
		if !table.eof {
			return ErrInput
		}
		if !reflect.DeepEqual(table.spec, want[i].spec) {
			return ErrExecution
		}
		var previous []any
		for _, row := range table.rows {
			if ctx.Err() != nil {
				return contextFailure(ctx)
			}
			if len(row) != len(table.spec.columns) {
				return ErrExecution
			}
			for ci, c := range row {
				if c.kind != table.spec.columns[ci].kind || c.kind == timeKind && c.valid != c.instant.Valid {
					return ErrExecution
				}
			}
			if err := budget.addTableRow(table.spec, row); err != nil {
				return err
			}
			for _, c := range row {
				if c.kind == textKind && c.valid && (!utf8.ValidString(c.text) || strings.ContainsRune(c.text, 0)) {
					return ErrInput
				}
			}
			cursor, err := rowCursor(table.spec, row)
			if err != nil || previous != nil && !cursorAfter(cursor, previous, table.spec.keys) {
				return ErrExecution
			}
			previous = cursor
		}
	}
	if s.rows != budget.rows || s.bytes != budget.bytes || f.report.SourceRows != uint64(budget.rows) || f.report.SourceBytes != uint64(budget.bytes) {
		return ErrExecution
	}
	return nil
}
