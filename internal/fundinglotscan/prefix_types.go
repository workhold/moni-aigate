package fundinglotscan

import (
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
)

const (
	prefixTableCount        = 71
	prefixColumnCount       = 635
	prefixWalletTableCount  = 87
	prefixWalletColumnCount = 804
)

// PrefixReport describes one complete physical prefix-source union. Its
// descriptive fields never grant database, maintenance, or financial authority.
type PrefixReport struct {
	Scope                       string                                         `json:"scope"`
	Mode                        Mode                                           `json:"mode"`
	Status                      string                                         `json:"status"`
	PhysicalEOFComplete         bool                                           `json:"physical_eof_complete"`
	ScannedTables               uint64                                         `json:"scanned_tables"`
	ScannedColumns              uint64                                         `json:"scanned_columns"`
	SourceRows                  uint64                                         `json:"source_rows"`
	SourceBytes                 uint64                                         `json:"source_bytes"`
	InputMismatchFields         uint64                                         `json:"input_mismatch_fields"`
	JournalMetadataMismatches   uint64                                         `json:"journal_metadata_mismatches"`
	OrdinaryBoundaryMismatches  uint64                                         `json:"ordinary_boundary_mismatches"`
	ObservationSourceMismatches uint64                                         `json:"observation_source_mismatches"`
	CoverageComplete            bool                                           `json:"coverage_complete"`
	Ledger                      fundinglotverify.LedgerRowsCheck               `json:"ledger"`
	Links                       fundinglotverify.FundingSourceLinksCheck       `json:"links"`
	Funding                     fundinglotverify.FundingSourceGraphCheck       `json:"funding"`
	Customer                    fundinglotverify.CustomerKeyGraphCheck         `json:"customer"`
	Procurement                 fundinglotverify.ProcurementEvidenceGraphCheck `json:"procurement"`
	Periodic                    fundinglotverify.PeriodicBudgetGraphCheck      `json:"periodic"`
	Sandbox                     fundinglotverify.SandboxSourceGraphCheck       `json:"sandbox"`
	PeriodicPrefix              periodicbudgetprefix.Report                    `json:"periodic_prefix"`
	// Original seven checks followed by the prefix check.
	Algorithms [8]string `json:"algorithms"`
}

// PrefixWalletReport retains every original source and wallet result, then
// appends the independently evaluated periodic prefix result.
type PrefixWalletReport struct {
	Scope                       string                                         `json:"scope"`
	Mode                        Mode                                           `json:"mode"`
	Status                      string                                         `json:"status"`
	PhysicalEOFComplete         bool                                           `json:"physical_eof_complete"`
	ScannedTables               uint64                                         `json:"scanned_tables"`
	ScannedColumns              uint64                                         `json:"scanned_columns"`
	SourceRows                  uint64                                         `json:"source_rows"`
	SourceBytes                 uint64                                         `json:"source_bytes"`
	InputMismatchFields         uint64                                         `json:"input_mismatch_fields"`
	JournalMetadataMismatches   uint64                                         `json:"journal_metadata_mismatches"`
	OrdinaryBoundaryMismatches  uint64                                         `json:"ordinary_boundary_mismatches"`
	ObservationSourceMismatches uint64                                         `json:"observation_source_mismatches"`
	CoverageComplete            bool                                           `json:"coverage_complete"`
	Ledger                      fundinglotverify.LedgerRowsCheck               `json:"ledger"`
	Links                       fundinglotverify.FundingSourceLinksCheck       `json:"links"`
	Funding                     fundinglotverify.FundingSourceGraphCheck       `json:"funding"`
	Customer                    fundinglotverify.CustomerKeyGraphCheck         `json:"customer"`
	Procurement                 fundinglotverify.ProcurementEvidenceGraphCheck `json:"procurement"`
	Periodic                    fundinglotverify.PeriodicBudgetGraphCheck      `json:"periodic"`
	Sandbox                     fundinglotverify.SandboxSourceGraphCheck       `json:"sandbox"`
	PeriodicPrefix              periodicbudgetprefix.Report                    `json:"periodic_prefix"`
	Wallet                      fundinglotverify.WalletLotsRowsCheck           `json:"wallet"`
	// Original eight checks followed by the prefix check.
	Algorithms [9]string `json:"algorithms"`
}

// Different private named states prevent converting the source-only facts to
// wallet facts, even by an external explicit Go conversion.
type prefixFactsState struct {
	raw    *session
	report PrefixReport
}
type prefixWalletFactsState struct {
	raw    *session
	report PrefixWalletReport
}
type PrefixFacts struct{ state *prefixFactsState }
type PrefixWalletFacts struct{ state *prefixWalletFactsState }

func (PrefixFacts) String() string   { return "funding_prefix_source_facts_redacted" }
func (PrefixFacts) GoString() string { return "funding_prefix_source_facts_redacted" }
func (PrefixFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_prefix_source_facts":"redacted"}`), nil
}
func (f PrefixFacts) Summary() PrefixReport {
	if f.state == nil {
		return PrefixReport{}
	}
	return f.state.report
}
func (PrefixWalletFacts) String() string   { return "funding_prefix_wallet_facts_redacted" }
func (PrefixWalletFacts) GoString() string { return "funding_prefix_wallet_facts_redacted" }
func (PrefixWalletFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_prefix_wallet_facts":"redacted"}`), nil
}
func (f PrefixWalletFacts) Summary() PrefixWalletReport {
	if f.state == nil {
		return PrefixWalletReport{}
	}
	return f.state.report
}
