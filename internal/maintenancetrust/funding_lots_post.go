package maintenancetrust

import (
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// Check every child qualification, not only the aggregate descriptive status.
func fundingLotsPostQualified(r fundinglotscan.WalletReport, mode fundinglotscan.Mode) bool {
	if r.Scope != "complete_source_wallet_graphs_v1" || r.Mode != mode || mode != fundinglotscan.ModeOrdinary && mode != fundinglotscan.ModeSandbox ||
		r.Status != "consistent" || !r.PhysicalEOFComplete || r.ScannedTables != 84 || !r.CoverageComplete || r.SourceRows > 100000 || r.SourceBytes > 16<<20 ||
		r.InputMismatchFields != 0 || r.JournalMetadataMismatches != 0 || r.OrdinaryBoundaryMismatches != 0 {
		return false
	}
	for i, algorithm := range r.Algorithms {
		if i == 6 && mode == fundinglotscan.ModeOrdinary {
			if algorithm != "not_applicable" {
				return false
			}
		} else if algorithm != "executed" {
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
	if mode == fundinglotscan.ModeOrdinary {
		return r.Sandbox == (fundinglotverify.SandboxSourceGraphCheck{}) && r.Links.UncoveredSandboxReceipts == 0 && r.Funding.UncoveredSandboxReceipts == 0
	}
	return r.Sandbox.Scope == "sandbox_stored_verified_source_graph_v1" && r.Sandbox.Status == "consistent" && r.Sandbox.ScopedGraphConsistent &&
		r.Sandbox.Mismatches == (fundinglotverify.SandboxSourceGraphMismatches{}) && r.Sandbox.Unproven == (fundinglotverify.SandboxSourceGraphUnproven{}) &&
		r.Links.UncoveredSandboxReceipts >= 0 && uint64(r.Links.UncoveredSandboxReceipts) == r.Funding.UncoveredSandboxReceipts
}

func fundingLotsPostCounts(r fundinglotscan.WalletReport, source fundinglotscan.Report, p fundinglotinit.Plan) bool {
	return fundingLotsWalletCounts(r.Wallet.Scanned, r.SourceRows, source.SourceRows, p)
}

func fundingLotsWalletCounts(w fundinglotverify.WalletLotsRowCounts, postRows, sourceRows uint64, p fundinglotinit.Plan) bool {
	m := p.Metadata
	if sourceRows > 100000 || m.GeneratedRows > 100000-sourceRows || postRows != sourceRows+m.GeneratedRows ||
		m.GeneratedRows < 2 || m.DataRows != m.GeneratedRows-2 || w.Epochs != 1 || w.Attestations != 1 {
		return false
	}
	return w.LegacyReceipts == uint64(len(p.LegacyReceipts)) && w.LegacySandboxReceipts == uint64(len(p.LegacySandboxReceipts)) &&
		w.LegacySettlements == uint64(len(p.LegacySettlements)) && w.LegacyApprovedRefunds == uint64(len(p.LegacyApprovedRefunds)) &&
		w.LegacyOpenings == uint64(len(p.LegacyOpenings)) && w.LegacySources == uint64(len(p.LegacySources)) &&
		w.Operations == uint64(len(p.Operations)) && w.Heads == uint64(len(p.Heads)) && w.Lots == uint64(len(p.Lots)) &&
		w.Events == uint64(len(p.Events)) && w.Balances == uint64(len(p.Balances)) && w.ReservationLots == uint64(len(p.ReservationLots)) &&
		w.SettlementLots == 0 && w.RefundLots == 0
}
