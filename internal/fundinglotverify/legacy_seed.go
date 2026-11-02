package fundinglotverify

// LegacySeedTenantRow carries only the ID read by the original 68-table union.
// It makes no statement about the tenant's funding-lot birth transaction.
type LegacySeedTenantRow struct{ ID string }

// LegacySeedRows contains supplied business rows, never initialization authority.
type LegacySeedRows struct {
	Tenants      []LegacySeedTenantRow
	Accounts     []LedgerAccountRow
	Journals     []WalletLotsJournalRow
	Lines        []LedgerLineRow
	Receipts     []WalletLotsReceiptRow
	Requests     []WalletLotsRequestRow
	Reservations []WalletLotsReservationRow
	Settlements  []WalletLotsSettlementRow
	Refunds      []WalletLotsRefundRow
}
