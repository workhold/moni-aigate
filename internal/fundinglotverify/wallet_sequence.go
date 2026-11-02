package fundinglotverify

import (
	"context"
	"errors"
)

var (
	ErrWalletSequenceRowsInput    = errors.New("funding_lot_wallet_sequence_rows_input")
	ErrWalletSequenceRowsResource = errors.New("funding_lot_wallet_sequence_rows_resource")
	ErrWalletSequenceRowsCanceled = errors.New("funding_lot_wallet_sequence_rows_canceled")
	ErrWalletSequenceRowsTimeout  = errors.New("funding_lot_wallet_sequence_rows_timeout")
)

// These projections carry supplied statements, never database authority.
// Existing TEXT identities retain their exact bytes, including empty strings.
type WalletSequenceEpochRow struct{ ID string }
type WalletSequenceTenantRow struct {
	ID         string
	CreatedXID *string
}
type WalletSequenceOperationRow struct {
	ID, TenantID, EpochID, Seq                                                    string
	PreviousSeq, PreviousID                                                       *string
	Kind, SourceID                                                                string
	JournalID, JournalOperationID                                                 *string
	EventCount, AvailableDelta, HeldDelta, ConsumedDelta, IssuedDelta, CreatedXID string
}
type WalletSequenceHeadRow struct{ TenantID, EpochID, TailSeq, TailOperationID string }
type WalletSequenceLegacySourceRow struct {
	EpochID, TenantID, Kind, SourceID, JournalID, JournalOperationID, Amount string
}
type WalletSequenceJournalRow struct{ ID, TenantID, OperationID, Kind, Currency, CreatedTXID string }
type WalletSequenceEventRow struct {
	ID, TenantID, LotID, WalletOperationID, WalletSeq, Kind           string
	OperationID, JournalID                                            *string
	CreatedXID, AvailableDelta, HeldDelta, ConsumedDelta, IssuedDelta string
}
type WalletSequenceRows struct {
	Epochs        []WalletSequenceEpochRow
	Tenants       []WalletSequenceTenantRow
	Operations    []WalletSequenceOperationRow
	Heads         []WalletSequenceHeadRow
	LegacySources []WalletSequenceLegacySourceRow
	Journals      []WalletSequenceJournalRow
	Events        []WalletSequenceEventRow
}
type WalletSequenceRowCounts struct {
	Epochs        uint64 `json:"epochs"`
	Tenants       uint64 `json:"tenants"`
	Operations    uint64 `json:"operations"`
	Heads         uint64 `json:"heads"`
	LegacySources uint64 `json:"legacy_sources"`
	Journals      uint64 `json:"journals"`
	Events        uint64 `json:"events"`
}
type WalletSequenceRowMismatches struct {
	EpochRows                    uint64 `json:"epoch_rows"`
	InvalidTextRows              uint64 `json:"invalid_text_rows"`
	InvalidValueRows             uint64 `json:"invalid_value_rows"`
	DuplicateTenantIDs           uint64 `json:"duplicate_tenant_ids"`
	DuplicateEpochIDs            uint64 `json:"duplicate_epoch_ids"`
	DuplicateOperationIDs        uint64 `json:"duplicate_operation_ids"`
	DuplicateHeadTenants         uint64 `json:"duplicate_head_tenants"`
	DuplicateJournalIDs          uint64 `json:"duplicate_journal_ids"`
	DuplicateJournalOperationIDs uint64 `json:"duplicate_journal_operation_ids"`
	DuplicateLegacySourceKeys    uint64 `json:"duplicate_legacy_source_keys"`
	DuplicateEventIDs            uint64 `json:"duplicate_event_ids"`
	DuplicateTenantSequences     uint64 `json:"duplicate_tenant_sequences"`
	DuplicatePredecessorIDs      uint64 `json:"duplicate_predecessor_ids"`
	DuplicateOperationSourceKeys uint64 `json:"duplicate_operation_source_keys"`
	DuplicateOperationJournalIDs uint64 `json:"duplicate_operation_journal_ids"`
	DuplicateLegacyJournalIDs    uint64 `json:"duplicate_legacy_journal_ids"`
	DuplicateEventOperationLots  uint64 `json:"duplicate_event_operation_lots"`
	TenantIdentityRows           uint64 `json:"tenant_identity_rows"`
	GenesisRows                  uint64 `json:"genesis_rows"`
	SequenceRows                 uint64 `json:"sequence_rows"`
	HeadRows                     uint64 `json:"head_rows"`
	SourceBoundaryRows           uint64 `json:"source_boundary_rows"`
	JournalBindingRows           uint64 `json:"journal_binding_rows"`
	EventBindingRows             uint64 `json:"event_binding_rows"`
	DeltaRows                    uint64 `json:"delta_rows"`
	OperationTotalsRows          uint64 `json:"operation_totals_rows"`
}
type WalletSequenceRowsCheck struct {
	Scope          string                      `json:"scope"`
	RowsConsistent bool                        `json:"rows_consistent"`
	Scanned        WalletSequenceRowCounts     `json:"scanned"`
	Mismatches     WalletSequenceRowMismatches `json:"mismatches"`
}

// CheckWalletSequenceRows checks the supplied sequence and declarations only.
// It does not prove source eligibility, FIFO, lot balances, database coverage,
// an authenticated epoch, or permission to initialize or mutate any funds.
func CheckWalletSequenceRows(ctx context.Context, in WalletSequenceRows) (WalletSequenceRowsCheck, error) {
	return checkWalletSequenceRows(ctx, in)
}
