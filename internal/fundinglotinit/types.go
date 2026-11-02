// Package fundinglotinit plans descriptive legacy wallet opening rows. It
// establishes no database coverage, maintenance authority or permission to write.
package fundinglotinit

import (
	"errors"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

var (
	ErrInput    = errors.New("funding_lot_init_input")
	ErrResource = errors.New("funding_lot_init_resource")
	ErrCanceled = errors.New("funding_lot_init_canceled")
	ErrTimeout  = errors.New("funding_lot_init_timeout")
)

type Input struct {
	EpochID string
	EpochAt fundinglotverify.WalletLotsTime
	Mode    string
	Rows    fundinglotverify.LegacySeedRows
}

// Metadata is a descriptive draft, not an epoch or runtime attestation.
type Metadata struct {
	EpochID                                                         string
	EpochAt                                                         fundinglotverify.WalletLotsTime
	TenantCount, ReceiptCount, SettlementCount, ApprovedRefundCount uint64
	OpeningCount, LegacySourceCount, DataRows, GeneratedRows        uint64
	AvailableTotal, HeldTotal                                       string
}

// Insert DTOs deliberately omit database transaction and creation stamps.
type Operation struct {
	ID, TenantID, EpochID, Seq                                        string
	PreviousSeq, PreviousID                                           *string
	Kind, SourceID                                                    string
	JournalID, JournalOperationID                                     *string
	EventCount, AvailableDelta, HeldDelta, ConsumedDelta, IssuedDelta string
}
type Lot struct {
	ID, TenantID, OriginKind                               string
	FundingReceiptID, LegacyOpeningID, LegacyRefundOrderID *string
	IssuedAmount, Currency, RefundClass                    string
	FIFOAt                                                 fundinglotverify.WalletLotsTime
}
type Event struct {
	ID, TenantID, LotID, WalletOperationID, WalletSeq, Kind                    string
	OperationID, JournalID                                                     *string
	AvailableDelta, HeldDelta, ConsumedDelta, IssuedDelta                      string
	PreviousEventID                                                            *string
	BeforeRevision, AfterRevision, BeforeAvailable, BeforeHeld, BeforeConsumed string
	AfterAvailable, AfterHeld, AfterConsumed                                   string
}
type ReservationLot struct {
	ID, TenantID, ReservationID, LotID, AllocationIndex, Amount string
}

// Plan is mutable ordinary data. Its rows cannot authorize a future writer.
type Plan struct {
	Metadata              Metadata
	LegacyReceipts        []fundinglotverify.WalletLotsLegacyReceiptRow
	LegacySandboxReceipts []fundinglotverify.WalletLotsLegacySandboxReceiptRow
	LegacySettlements     []fundinglotverify.WalletLotsLegacySettlementRow
	LegacyApprovedRefunds []fundinglotverify.WalletLotsLegacyApprovedRefundRow
	LegacyOpenings        []fundinglotverify.WalletLotsLegacyOpeningRow
	LegacySources         []fundinglotverify.WalletLotsLegacySourceRow
	Operations            []Operation
	Heads                 []fundinglotverify.WalletSequenceHeadRow
	Lots                  []Lot
	Events                []Event
	Balances              []fundinglotverify.WalletLotsBalanceRow
	ReservationLots       []ReservationLot
	SettlementLots        []fundinglotverify.WalletLotsSettlementLotRow
	RefundLots            []fundinglotverify.WalletLotsRefundLotRow
}
