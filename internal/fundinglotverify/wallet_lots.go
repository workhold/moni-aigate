package fundinglotverify

import (
	"context"
	"errors"
)

var (
	ErrWalletLotsRowsInput    = errors.New("funding_lot_wallet_lots_rows_input")
	ErrWalletLotsRowsResource = errors.New("funding_lot_wallet_lots_rows_resource")
	ErrWalletLotsRowsCanceled = errors.New("funding_lot_wallet_lots_rows_canceled")
	ErrWalletLotsRowsTimeout  = errors.New("funding_lot_wallet_lots_rows_timeout")
)

type WalletLotsTime = PeriodicBudgetGraphTime
type WalletLotsJournalRow struct {
	WalletSequenceJournalRow
	Reference, Actor string
	CreatedAt        WalletLotsTime
}
type WalletLotsReceiptRow struct {
	ID, TenantID, SourceKind, SourceID, Amount, Currency, JournalID string
	CreatedAt                                                       WalletLotsTime
}
type WalletLotsRequestRow struct{ ID, TenantID, OwnerID, BillingStatus string }
type WalletLotsReservationRow struct{ ID, TenantID, RequestID, Amount, Status string }
type WalletLotsSettlementRow struct{ ID, TenantID, RequestID, ChargedAmount, RefundedAmount, Status, Reason, JournalID string }
type WalletLotsRefundRow struct {
	ID, TenantID, SettlementID, Amount, State, Requester string
	Approver, JournalID                                  *string
}
type WalletLotsEpochRow struct {
	Singleton                                                                                        bool
	ID, SchemaVersion, DatabaseOID, SchemaOID                                                        string
	BasisDigest, ManifestDigest, DescriptorDigest, RuntimeProofDigest                                string
	TenantCount, ReceiptCount, SettlementCount, ApprovedRefundCount, OpeningCount, LegacySourceCount string
	AvailableTotal, HeldTotal, Actor                                                                 string
	CreatedAt                                                                                        WalletLotsTime
}
type WalletLotsAttestationRow struct {
	Singleton                                                                                         bool
	EpochID, ProofVersion, Mode, DatabaseOID, SchemaOID, SchemaOwnerOID, AppRoleOID, ProjectorRoleOID string
	WorkerRoleOID, PredecessorPaymentDigest                                                           *string
	ProofCanonical                                                                                    []byte
	ProofSHA256, ManifestDigest, DescriptorDigest                                                     string
	CreatedAt                                                                                         WalletLotsTime
}
type WalletLotsLegacyReceiptRow struct{ ReceiptID, EpochID, TenantID, SourceKind, SourceID, Amount, JournalID string }
type WalletLotsLegacySandboxReceiptRow struct{ ReceiptID, EpochID, TenantID, CaptureID, Amount, JournalID string }
type WalletLotsLegacySettlementRow struct{ SettlementID, EpochID, TenantID, ReservationID, RequestID, ChargedAmount, RefundedAmountAtCutover, JournalID string }
type WalletLotsLegacyApprovedRefundRow struct{ RefundOrderID, EpochID, TenantID, SettlementID, Amount, JournalID string }
type WalletLotsLegacyOpeningRow struct {
	ID, EpochID, TenantID, Kind string
	AccountID, ReservationID    *string
	Amount                      string
	ObservedAt                  WalletLotsTime
}
type WalletLotsLegacySourceRow struct {
	WalletSequenceLegacySourceRow
	RequestID, ReservationID, SettlementID *string
}
type WalletLotsOperationRow struct {
	WalletSequenceOperationRow
	CreatedAt WalletLotsTime
}
type WalletLotsLotRow struct {
	ID, TenantID, OriginKind                               string
	FundingReceiptID, LegacyOpeningID, LegacyRefundOrderID *string
	IssuedAmount, Currency, RefundClass                    string
	FIFOAt, CreatedAt                                      WalletLotsTime
}
type WalletLotsEventRow struct {
	WalletSequenceEventRow
	PreviousEventID                                                                                                      *string
	BeforeRevision, AfterRevision, BeforeAvailable, BeforeHeld, BeforeConsumed, AfterAvailable, AfterHeld, AfterConsumed string
	CreatedAt                                                                                                            WalletLotsTime
}
type WalletLotsBalanceRow struct{ LotID, TenantID, AvailableAmount, InferenceHeldAmount, ConsumedAmount, Revision, LastEventID string }
type WalletLotsReservationLotRow struct {
	ID, TenantID, ReservationID, LotID, AllocationIndex, Amount, CreatedXID string
	CreatedAt                                                               WalletLotsTime
}
type WalletLotsSettlementLotRow struct {
	ID, TenantID, SettlementID, ReservationLotID, ChargedAmount, ReleasedAmount, CreatedXID string
	CreatedAt                                                                               WalletLotsTime
}
type WalletLotsRefundLotRow struct {
	ID, TenantID, RefundOrderID, LotID          string
	SettlementLotID                             *string
	AllocationIndex, Amount, Origin, CreatedXID string
	CreatedAt                                   WalletLotsTime
}

// Every input is a raw statement. No report, trusted flag or allocated plan is
// accepted in place of the underlying financial facts.
type WalletLotsRows struct {
	Tenants               []WalletSequenceTenantRow
	Accounts              []LedgerAccountRow
	Journals              []WalletLotsJournalRow
	Lines                 []LedgerLineRow
	Receipts              []WalletLotsReceiptRow
	Requests              []WalletLotsRequestRow
	Reservations          []WalletLotsReservationRow
	Settlements           []WalletLotsSettlementRow
	Refunds               []WalletLotsRefundRow
	Epochs                []WalletLotsEpochRow
	Attestations          []WalletLotsAttestationRow
	LegacyReceipts        []WalletLotsLegacyReceiptRow
	LegacySandboxReceipts []WalletLotsLegacySandboxReceiptRow
	LegacySettlements     []WalletLotsLegacySettlementRow
	LegacyApprovedRefunds []WalletLotsLegacyApprovedRefundRow
	LegacyOpenings        []WalletLotsLegacyOpeningRow
	LegacySources         []WalletLotsLegacySourceRow
	Operations            []WalletLotsOperationRow
	Heads                 []WalletSequenceHeadRow
	Lots                  []WalletLotsLotRow
	Events                []WalletLotsEventRow
	Balances              []WalletLotsBalanceRow
	ReservationLots       []WalletLotsReservationLotRow
	SettlementLots        []WalletLotsSettlementLotRow
	RefundLots            []WalletLotsRefundLotRow
}
type WalletLotsRowCounts struct {
	Tenants               uint64 `json:"tenants"`
	Accounts              uint64 `json:"accounts"`
	Journals              uint64 `json:"journals"`
	Lines                 uint64 `json:"lines"`
	Receipts              uint64 `json:"receipts"`
	Requests              uint64 `json:"requests"`
	Reservations          uint64 `json:"reservations"`
	Settlements           uint64 `json:"settlements"`
	Refunds               uint64 `json:"refunds"`
	Epochs                uint64 `json:"epochs"`
	Attestations          uint64 `json:"attestations"`
	LegacyReceipts        uint64 `json:"legacy_receipts"`
	LegacySandboxReceipts uint64 `json:"legacy_sandbox_receipts"`
	LegacySettlements     uint64 `json:"legacy_settlements"`
	LegacyApprovedRefunds uint64 `json:"legacy_approved_refunds"`
	LegacyOpenings        uint64 `json:"legacy_openings"`
	LegacySources         uint64 `json:"legacy_sources"`
	Operations            uint64 `json:"operations"`
	Heads                 uint64 `json:"heads"`
	Lots                  uint64 `json:"lots"`
	Events                uint64 `json:"events"`
	Balances              uint64 `json:"balances"`
	ReservationLots       uint64 `json:"reservation_lots"`
	SettlementLots        uint64 `json:"settlement_lots"`
	RefundLots            uint64 `json:"refund_lots"`
}
type WalletLotsRowMismatches struct {
	InvalidTextRows    uint64 `json:"invalid_text_rows"`
	InvalidValueRows   uint64 `json:"invalid_value_rows"`
	InvalidTimeRows    uint64 `json:"invalid_time_rows"`
	DuplicateFacts     uint64 `json:"duplicate_facts"`
	EpochRows          uint64 `json:"epoch_rows"`
	GenesisRows        uint64 `json:"genesis_rows"`
	WalletSourceRows   uint64 `json:"wallet_source_rows"`
	OriginRows         uint64 `json:"origin_rows"`
	FIFOAllocationRows uint64 `json:"fifo_allocation_rows"`
	ReservationRows    uint64 `json:"reservation_rows"`
	SettlementRows     uint64 `json:"settlement_rows"`
	RefundOrderRows    uint64 `json:"refund_order_rows"`
	RefundRows         uint64 `json:"refund_rows"`
	EventChainRows     uint64 `json:"event_chain_rows"`
	BalanceRows        uint64 `json:"balance_rows"`
	LegacyBoundaryRows uint64 `json:"legacy_boundary_rows"`
	MissingFacts       uint64 `json:"missing_facts"`
	OrphanFacts        uint64 `json:"orphan_facts"`
}
type WalletLotsRowUnproven struct {
	ReplayPrefixUnproven uint64 `json:"replay_prefix_unproven"`
}
type WalletLotsRowsCheck struct {
	Scope          string                      `json:"scope"`
	Status         string                      `json:"status"`
	RowsConsistent bool                        `json:"rows_consistent"`
	Scanned        WalletLotsRowCounts         `json:"scanned"`
	Sequence       WalletSequenceRowMismatches `json:"sequence"`
	Mismatches     WalletLotsRowMismatches     `json:"mismatches"`
	Unproven       WalletLotsRowUnproven       `json:"unproven"`
}

// CheckWalletLotsRows independently replays the supplied wallet facts. It
// authenticates no database snapshot, maintenance epoch or runtime authority.
func CheckWalletLotsRows(ctx context.Context, in WalletLotsRows) (WalletLotsRowsCheck, error) {
	return checkWalletLotsRows(ctx, in)
}
