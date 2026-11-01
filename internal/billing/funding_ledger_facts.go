package billing

import (
	"context"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// LedgerFactQuery and LedgerFactRows supply the fixed SELECT algorithm. Their
// implementations are untrusted and cannot issue maintenance proof.
type LedgerFactQuery = fundingLedgerQuery
type LedgerFactRows = fundingLedgerRows

// LedgerFacts owns local raw facts. It authenticates no source or transaction
// and confers no maintenance or financial authority, even when consistent.
type LedgerFacts struct{ snapshot fundingLedgerSnapshot }

func (LedgerFacts) String() string   { return "ledger_facts_redacted" }
func (LedgerFacts) GoString() string { return "ledger_facts_redacted" }
func (LedgerFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"ledger_facts":"redacted"}`), nil
}
func (f LedgerFacts) LocalSummary() fundinglotverify.LedgerRowsCheck {
	return f.snapshot.check
}

// These aliases preserve the existing safe error identities across the
// independent maintenance integration package. They contain no driver detail.
var (
	ErrLedgerFactsInput     = fundingLedgerReadInput
	ErrLedgerFactsExecution = fundingLedgerReadExecution
)

// ReadLedgerFacts executes only the existing three-table SELECT algorithm and
// resource checks. Its result is descriptive, never an authenticated receipt.
// Maintenance integration must supply its own private real-lease adapter.
func ReadLedgerFacts(ctx context.Context, query LedgerFactQuery, schema string) (LedgerFacts, error) {
	if query == nil || schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") || strings.ContainsRune(schema, 0) {
		return LedgerFacts{}, ErrLedgerFactsInput
	}
	snapshot, err := readFundingLedgerCore(ctx, query, schema)
	if err != nil {
		return LedgerFacts{}, err
	}
	return LedgerFacts{snapshot: snapshot}, nil
}
