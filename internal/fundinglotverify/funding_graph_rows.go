package fundinglotverify

import "time"

// FundingGraphJournalRow preserves the full safe journal projection for source
// and customer-funds checks. Supplying this row authenticates no transaction.
type FundingGraphJournalRow struct {
	ID, TenantID, OperationID, Kind, Reference, Actor, Currency, CreatedTXID string
	CreatedAt                                                                time.Time
}
