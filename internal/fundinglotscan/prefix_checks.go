package fundinglotscan

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
)

func checkPrefix(s *session) (periodicbudgetprefix.Report, string, uint64, error) {
	in, qualified, mismatches, err := projectPrefix(s)
	if err != nil {
		return periodicbudgetprefix.Report{}, "", 0, err
	}
	if !qualified {
		return periodicbudgetprefix.Report{Base: s.report.Periodic}, "input_mismatch", mismatches, nil
	}
	r, err := periodicbudgetprefix.Check(s.ctx, in)
	if errors.Is(err, periodicbudgetprefix.ErrResource) {
		return periodicbudgetprefix.Report{}, "", 0, ErrResource
	}
	if errors.Is(err, periodicbudgetprefix.ErrTimeout) {
		return periodicbudgetprefix.Report{}, "", 0, ErrTimeout
	}
	if errors.Is(err, periodicbudgetprefix.ErrCanceled) {
		return periodicbudgetprefix.Report{}, "", 0, ErrCanceled
	}
	if err != nil {
		return periodicbudgetprefix.Report{}, "", 0, contextFailure(s.ctx)
	}
	if r.Scope != "periodic_budget_snapshot_prefix_v1" || r.Base != s.report.Periodic {
		return periodicbudgetprefix.Report{}, "", 0, ErrExecution
	}
	return r, "executed", mismatches, nil
}

func makePrefixReport(s *session, p periodicbudgetprefix.Report, state string, sourceMismatches uint64) PrefixReport {
	base := s.report
	r := PrefixReport{
		Scope: "complete_business_source_prefix_graphs_v1", Mode: s.mode, PhysicalEOFComplete: true,
		ScannedTables: uint64(len(s.tables)), ScannedColumns: prefixColumnCount, SourceRows: uint64(s.rows), SourceBytes: uint64(s.bytes),
		InputMismatchFields: uint64(len(s.rejected)), JournalMetadataMismatches: base.JournalMetadataMismatches, OrdinaryBoundaryMismatches: base.OrdinaryBoundaryMismatches,
		ObservationSourceMismatches: sourceMismatches,
		Ledger:                      base.Ledger, Links: base.Links, Funding: base.Funding, Customer: base.Customer, Procurement: base.Procurement, Periodic: base.Periodic, Sandbox: base.Sandbox, PeriodicPrefix: p,
	}
	copy(r.Algorithms[:], base.Algorithms[:])
	r.Algorithms[7] = state
	complete, mismatch := prefixComplete(s, p, state, sourceMismatches)
	r.CoverageComplete = complete
	r.Status = prefixStatus(complete, mismatch)
	return r
}

func makePrefixWalletReport(s *session, p periodicbudgetprefix.Report, state string, sourceMismatches uint64, wallet fundinglotverify.WalletLotsRowsCheck, walletState string) PrefixWalletReport {
	base := makePrefixReport(s, p, state, sourceMismatches)
	r := PrefixWalletReport{
		Scope: "complete_source_wallet_prefix_graphs_v1", Mode: base.Mode, PhysicalEOFComplete: base.PhysicalEOFComplete,
		ScannedTables: base.ScannedTables, ScannedColumns: prefixWalletColumnCount, SourceRows: base.SourceRows, SourceBytes: base.SourceBytes,
		InputMismatchFields: base.InputMismatchFields, JournalMetadataMismatches: base.JournalMetadataMismatches, OrdinaryBoundaryMismatches: base.OrdinaryBoundaryMismatches,
		ObservationSourceMismatches: base.ObservationSourceMismatches,
		Ledger:                      base.Ledger, Links: base.Links, Funding: base.Funding, Customer: base.Customer, Procurement: base.Procurement, Periodic: base.Periodic, Sandbox: base.Sandbox, PeriodicPrefix: p, Wallet: wallet,
	}
	copy(r.Algorithms[:7], base.Algorithms[:7])
	r.Algorithms[7], r.Algorithms[8] = walletState, state
	mismatch := base.Status == "mismatch" || walletState != "executed" || wallet.Status == "mismatch" || wallet.Mismatches != (fundinglotverify.WalletLotsRowMismatches{}) || wallet.Sequence != (fundinglotverify.WalletSequenceRowMismatches{})
	r.CoverageComplete = base.CoverageComplete && walletState == "executed" && wallet.Scope == "supplied_wallet_lots_rows" && wallet.Status == "consistent" && wallet.RowsConsistent && wallet.Unproven == (fundinglotverify.WalletLotsRowUnproven{}) && !mismatch
	r.Status = prefixStatus(r.CoverageComplete, mismatch)
	return r
}

func prefixStatus(complete, mismatch bool) string {
	if mismatch {
		return "mismatch"
	}
	if !complete {
		return "indeterminate"
	}
	return "consistent"
}

// Rebuild domain coverage from the actual children. The original aggregate's
// PB snapshot gap remains unchanged; its conservative indeterminate status is
// never reused as the completeness decision for this stronger scope.
func prefixComplete(s *session, p periodicbudgetprefix.Report, state string, sourceMismatches uint64) (complete, mismatch bool) {
	r := s.report
	complete = len(s.rejected) == 0
	mismatch = !complete || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 || sourceMismatches != 0
	for _, t := range s.tables {
		complete = complete && t.eof
	}
	for i, v := range r.Algorithms {
		if v != "executed" && !(i == 6 && s.mode == ModeOrdinary && v == "not_applicable") {
			complete = false
			mismatch = true
		}
	}
	if r.Algorithms[0] == "executed" {
		mismatch = mismatch || !r.Ledger.RowsConsistent || r.Ledger.Mismatches != (fundinglotverify.LedgerRowMismatches{})
		complete = complete && r.Ledger.Scope == "supplied_ledger_rows"
	}
	if r.Algorithms[1] == "executed" {
		mismatch = mismatch || !r.Links.ScopedLinksConsistent || r.Links.UnknownReceiptKinds != 0 || r.Links.Mismatches != (fundinglotverify.FundingSourceLinkMismatches{})
		complete = complete && r.Links.Scope == "manual_voucher_receipt_links"
	}
	if r.Algorithms[2] == "executed" {
		mismatch = mismatch || !r.Funding.ScopedGraphConsistent || r.Funding.Mismatches != (fundinglotverify.FundingSourceGraphMismatches{})
		complete = complete && r.Funding.Scope == "manual_voucher_funding_graph_v1"
	}
	if r.Algorithms[3] == "executed" {
		mismatch = mismatch || !r.Customer.Balanced || r.Customer.Mismatches != (fundinglotverify.CustomerKeyGraphMismatches{})
		complete = complete && r.Customer.Scope == "customer_key_financial_graph_v1"
	}
	if r.Algorithms[4] == "executed" {
		mismatch = mismatch || r.Procurement.Status == "mismatch" || r.Procurement.Structural != (fundinglotverify.ProcurementEvidenceGraphStructural{}) || r.Procurement.Mismatches != (fundinglotverify.ProcurementEvidenceGraphMismatches{})
		complete = complete && r.Procurement.Scope == "procurement_domain_evidence_graph_v1" && r.Procurement.Balanced && r.Procurement.CoverageComplete && r.Procurement.Unproven == (fundinglotverify.ProcurementEvidenceGraphUnproven{})
	}
	if r.Algorithms[5] == "executed" {
		mismatch = mismatch || r.Periodic.Status == "mismatch" || r.Periodic.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{})
		complete = complete && r.Periodic.Scope == "periodic_budget_graph_v1" && r.Periodic.Unproven.LegacyMembershipUnproven == 0 && r.Periodic.Unproven.LegacyEffectBoundaryUnproven == 0
	}
	if r.Algorithms[6] == "executed" {
		mismatch = mismatch || r.Sandbox.Status == "mismatch" || r.Sandbox.Mismatches != (fundinglotverify.SandboxSourceGraphMismatches{})
		complete = complete && r.Sandbox.Scope == "sandbox_stored_verified_source_graph_v1" && r.Sandbox.ScopedGraphConsistent && r.Sandbox.Unproven == (fundinglotverify.SandboxSourceGraphUnproven{})
		if r.Links.UncoveredSandboxReceipts < 0 || uint64(r.Links.UncoveredSandboxReceipts) != r.Funding.UncoveredSandboxReceipts {
			mismatch = true
		}
	} else if r.Links.UncoveredSandboxReceipts != 0 || r.Funding.UncoveredSandboxReceipts != 0 {
		complete = false
		mismatch = true
	}
	if state != "executed" {
		complete = false
		mismatch = true
	} else {
		mismatch = mismatch || p.Status == "mismatch" || p.Mismatches != (periodicbudgetprefix.Mismatches{}) || p.Base != r.Periodic
		complete = complete && p.Scope == "periodic_budget_snapshot_prefix_v1" && p.Base == r.Periodic && p.Balanced && p.CoverageComplete && p.Unproven == (periodicbudgetprefix.Gaps{})
	}
	return complete && !mismatch, mismatch
}
