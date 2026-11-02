package fundinglotscan

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const walletTableCount = 84

// WalletReport describes this fixed physical union and eight raw checks.
// CoverageComplete is domain coverage, never maintenance or funds authority.
type WalletReport struct {
	Scope                      string `json:"scope"`
	Mode                       Mode   `json:"mode"`
	Status                     string `json:"status"`
	PhysicalEOFComplete        bool   `json:"physical_eof_complete"`
	ScannedTables              uint64 `json:"scanned_tables"`
	SourceRows                 uint64 `json:"source_rows"`
	SourceBytes                uint64 `json:"source_bytes"`
	InputMismatchFields        uint64 `json:"input_mismatch_fields"`
	JournalMetadataMismatches  uint64 `json:"journal_metadata_mismatches"`
	OrdinaryBoundaryMismatches uint64 `json:"ordinary_boundary_mismatches"`
	// Fixed order: ledger, links, funding, customer, procurement, PB, sandbox, wallet.
	Algorithms       [8]string                                      `json:"algorithms"`
	CoverageComplete bool                                           `json:"coverage_complete"`
	Ledger           fundinglotverify.LedgerRowsCheck               `json:"ledger"`
	Links            fundinglotverify.FundingSourceLinksCheck       `json:"links"`
	Funding          fundinglotverify.FundingSourceGraphCheck       `json:"funding"`
	Customer         fundinglotverify.CustomerKeyGraphCheck         `json:"customer"`
	Procurement      fundinglotverify.ProcurementEvidenceGraphCheck `json:"procurement"`
	Periodic         fundinglotverify.PeriodicBudgetGraphCheck      `json:"periodic"`
	Sandbox          fundinglotverify.SandboxSourceGraphCheck       `json:"sandbox"`
	Wallet           fundinglotverify.WalletLotsRowsCheck           `json:"wallet"`
}

type walletFactsState struct {
	raw    *session
	report WalletReport
}
type WalletFacts struct{ state *walletFactsState }

func (WalletFacts) String() string   { return "source_wallet_union_facts_redacted" }
func (WalletFacts) GoString() string { return "source_wallet_union_facts_redacted" }
func (WalletFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"source_wallet_union_facts":"redacted"}`), nil
}
func (f WalletFacts) Summary() WalletReport {
	if f.state == nil {
		return WalletReport{}
	}
	return f.state.report
}

// ReadWallet owns one 84-table read. Caller Query/Mode carry no authority;
// neither old Facts nor a caller report can replace any physical scan/core.
func ReadWallet(ctx context.Context, q Query, schema string, mode Mode) (WalletFacts, error) {
	if ctx == nil || nilValue(q) || schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) || mode != ModeOrdinary && mode != ModeSandbox {
		return WalletFacts{}, ErrInput
	}
	if ctx.Err() != nil {
		return WalletFacts{}, contextFailure(ctx)
	}
	tables, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings())
	if err != nil || len(tables) != walletTableCount {
		return WalletFacts{}, ErrExecution
	}
	s := &session{ctx: ctx, query: q, schema: schema, mode: mode, tables: tables, index: index, rejected: map[cellRef]bool{}}
	if err = s.readAll(); err != nil {
		return WalletFacts{}, readFailure(ctx, err)
	}
	if err = s.check(); err != nil {
		return WalletFacts{}, readFailure(ctx, err)
	}
	in, qualified, err := projectWallet(s)
	if err != nil {
		return WalletFacts{}, readFailure(ctx, err)
	}
	var wallet fundinglotverify.WalletLotsRowsCheck
	walletState := "input_mismatch"
	if qualified {
		wallet, err = fundinglotverify.CheckWalletLotsRows(ctx, in)
		if err != nil {
			if err == fundinglotverify.ErrWalletLotsRowsResource {
				return WalletFacts{}, ErrResource
			}
			return WalletFacts{}, readFailure(ctx, err)
		}
		if wallet.Scope != "supplied_wallet_lots_rows" {
			return WalletFacts{}, ErrExecution
		}
		walletState = "executed"
	}
	if ctx.Err() != nil {
		return WalletFacts{}, contextFailure(ctx)
	}
	r := WalletReport{
		Scope: "complete_source_wallet_graphs_v1", Mode: mode, PhysicalEOFComplete: true,
		ScannedTables: uint64(len(s.tables)), SourceRows: uint64(s.rows), SourceBytes: uint64(s.bytes), InputMismatchFields: uint64(len(s.rejected)),
		JournalMetadataMismatches: s.report.JournalMetadataMismatches, OrdinaryBoundaryMismatches: s.report.OrdinaryBoundaryMismatches,
		Ledger: s.report.Ledger, Links: s.report.Links, Funding: s.report.Funding, Customer: s.report.Customer,
		Procurement: s.report.Procurement, Periodic: s.report.Periodic, Sandbox: s.report.Sandbox, Wallet: wallet,
	}
	copy(r.Algorithms[:], s.report.Algorithms[:])
	r.Algorithms[7] = walletState
	r.CoverageComplete = s.report.CoverageComplete && qualified && len(s.rejected) == 0 && wallet.Unproven == (fundinglotverify.WalletLotsRowUnproven{})
	r.Status = "consistent"
	if s.report.Status == "mismatch" || !qualified || wallet.Status == "mismatch" || len(s.rejected) != 0 {
		r.Status = "mismatch"
	} else if !r.CoverageComplete || wallet.Status == "indeterminate" {
		r.Status = "indeterminate"
	}
	if ctx.Err() != nil {
		return WalletFacts{}, contextFailure(ctx)
	}
	return WalletFacts{state: &walletFactsState{raw: s, report: r}}, nil
}
