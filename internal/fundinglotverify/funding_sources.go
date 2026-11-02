package fundinglotverify

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	fundingSourceRowLimit  = 100000
	fundingSourceByteLimit = 16 << 20
)

var (
	ErrFundingSourceRowsInput    = errors.New("funding_lot_funding_source_rows_input")
	ErrFundingSourceRowsResource = errors.New("funding_lot_funding_source_rows_resource")
	ErrFundingSourceRowsCanceled = errors.New("funding_lot_funding_source_rows_canceled")
	ErrFundingSourceRowsTimeout  = errors.New("funding_lot_funding_source_rows_timeout")
)

// FundingSourceReceiptRow is a raw funding_receipts projection. Amount remains
// decimal text so this core validates the same exact representation returned
// by PostgreSQL without trusting a query-side business predicate.
type FundingSourceReceiptRow struct {
	ID, TenantID, SourceKind, SourceID, Amount, Currency, JournalID string
}

// FundingSourceCreditRow is a raw credit_orders projection. Nullable approval
// and journal fields preserve SQL NULL separately from an empty text value.
type FundingSourceCreditRow struct {
	ID, TenantID, Amount, State, Requester string
	Approver, JournalID                    *string
}

// FundingSourceRedemptionRow is a raw voucher_redemptions projection.
type FundingSourceRedemptionRow struct {
	ID, TenantID, VoucherID, FundingReceiptID, Amount, JournalID string
}

// FundingSourceRows is a complete supplied row set for the fixed local scope.
// It does not authenticate completeness; the fixed reader owns that property.
type FundingSourceRows struct {
	Receipts    []FundingSourceReceiptRow
	Credits     []FundingSourceCreditRow
	Redemptions []FundingSourceRedemptionRow
}

type FundingSourceRowCounts struct {
	Receipts    int64 `json:"receipts,string"`
	Credits     int64 `json:"credits,string"`
	Redemptions int64 `json:"redemptions,string"`
}

type FundingSourceLinkMismatches struct {
	ReceiptShapeMismatches       int64 `json:"receipt_shape_mismatches,string"`
	CreditShapeMismatches        int64 `json:"credit_shape_mismatches,string"`
	PendingCreditEffects         int64 `json:"pending_credit_effects,string"`
	ApprovedCreditMissingReceipt int64 `json:"approved_credit_missing_receipt,string"`
	CreditReceiptMismatches      int64 `json:"credit_receipt_mismatches,string"`
	OrphanManualReceipts         int64 `json:"orphan_manual_receipts,string"`
	RedemptionShapeMismatches    int64 `json:"redemption_shape_mismatches,string"`
	RedemptionReceiptMismatches  int64 `json:"redemption_receipt_mismatches,string"`
	OrphanVoucherReceipts        int64 `json:"orphan_voucher_receipts,string"`
	DuplicateSourceReceipts      int64 `json:"duplicate_source_receipts,string"`
}

// FundingSourceLinksCheck describes only the manual-credit and voucher
// receipt crosswalk. In particular, sandbox receipts are counted as uncovered
// and this value never represents whole-wallet coverage or permission.
type FundingSourceLinksCheck struct {
	Scope                    string                      `json:"scope"`
	ScopedLinksConsistent    bool                        `json:"scoped_links_consistent"`
	Scanned                  FundingSourceRowCounts      `json:"scanned"`
	UncoveredSandboxReceipts int64                       `json:"uncovered_sandbox_receipts,string"`
	UnknownReceiptKinds      int64                       `json:"unknown_receipt_kinds,string"`
	Mismatches               FundingSourceLinkMismatches `json:"mismatches"`
}

type fundingSourceCoreWork struct {
	ctx   context.Context
	steps uint16
}

func (w *fundingSourceCoreWork) tick() error {
	w.steps++
	if w.steps == 256 {
		w.steps = 0
		if w.ctx.Err() != nil {
			return fundingSourceContextError(w.ctx)
		}
	}
	return nil
}

func fundingSourceContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrFundingSourceRowsTimeout
	}
	return ErrFundingSourceRowsCanceled
}

func fundingSourceCanonicalInt64(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 20 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && strconv.FormatInt(n, 10) == s
}

func fundingSourceText(fields ...string) bool {
	for _, field := range fields {
		if !utf8.ValidString(field) || strings.ContainsRune(field, 0) {
			return false
		}
	}
	return true
}

func fundingSourceRowsBudget(w *fundingSourceCoreWork, rows FundingSourceRows) error {
	leftRows := fundingSourceRowLimit
	for _, n := range [...]int{len(rows.Receipts), len(rows.Credits), len(rows.Redemptions)} {
		if n > leftRows {
			return ErrFundingSourceRowsResource
		}
		leftRows -= n
	}
	leftBytes := fundingSourceByteLimit
	consume := func(s string) bool {
		if len(s) > leftBytes {
			return false
		}
		leftBytes -= len(s)
		return true
	}
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return err
		}
		for _, field := range [...]string{row.ID, row.TenantID, row.SourceKind, row.SourceID, row.Amount, row.Currency, row.JournalID} {
			if !fundingSourceText(field) {
				return ErrFundingSourceRowsInput
			}
			if !consume(field) {
				return ErrFundingSourceRowsResource
			}
		}
	}
	for _, row := range rows.Credits {
		if err := w.tick(); err != nil {
			return err
		}
		for _, field := range [...]string{row.ID, row.TenantID, row.Amount, row.State, row.Requester} {
			if !fundingSourceText(field) {
				return ErrFundingSourceRowsInput
			}
			if !consume(field) {
				return ErrFundingSourceRowsResource
			}
		}
		for _, optional := range []*string{row.Approver, row.JournalID} {
			if optional != nil {
				if !fundingSourceText(*optional) {
					return ErrFundingSourceRowsInput
				}
				if !consume(*optional) {
					return ErrFundingSourceRowsResource
				}
			}
		}
	}
	for _, row := range rows.Redemptions {
		if err := w.tick(); err != nil {
			return err
		}
		for _, field := range [...]string{row.ID, row.TenantID, row.VoucherID, row.FundingReceiptID, row.Amount, row.JournalID} {
			if !fundingSourceText(field) {
				return ErrFundingSourceRowsInput
			}
			if !consume(field) {
				return ErrFundingSourceRowsResource
			}
		}
	}
	return nil
}

type fundingReceiptKey struct{ kind, sourceID string }

// CheckFundingSourceLinks independently compares the supplied complete rows
// for manual-credit and voucher receipt links. Other receipt types remain
// explicitly uncovered. A successful local check cannot establish that the
// caller supplied complete database rows or any wider financial coverage.
func CheckFundingSourceLinks(ctx context.Context, rows FundingSourceRows) (FundingSourceLinksCheck, error) {
	if ctx == nil {
		return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
	}
	if ctx.Err() != nil {
		return FundingSourceLinksCheck{}, fundingSourceContextError(ctx)
	}
	w := fundingSourceCoreWork{ctx: ctx}
	if err := fundingSourceRowsBudget(&w, rows); err != nil {
		return FundingSourceLinksCheck{}, err
	}
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		if _, ok := fundingSourceCanonicalInt64(row.Amount); !ok {
			return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
		}
	}
	for _, row := range rows.Credits {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		if _, ok := fundingSourceCanonicalInt64(row.Amount); !ok {
			return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
		}
	}
	for _, row := range rows.Redemptions {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		if _, ok := fundingSourceCanonicalInt64(row.Amount); !ok {
			return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
		}
	}
	for _, group := range [][]string{
		func() []string {
			v := make([]string, len(rows.Receipts))
			for i := range rows.Receipts {
				v[i] = rows.Receipts[i].ID
			}
			return v
		}(),
		func() []string {
			v := make([]string, len(rows.Credits))
			for i := range rows.Credits {
				v[i] = rows.Credits[i].ID
			}
			return v
		}(),
		func() []string {
			v := make([]string, len(rows.Redemptions))
			for i := range rows.Redemptions {
				v[i] = rows.Redemptions[i].ID
			}
			return v
		}(),
	} {
		ids := make(map[string]struct{}, len(group))
		for _, id := range group {
			if err := w.tick(); err != nil {
				return FundingSourceLinksCheck{}, err
			}
			if !fundingSourceText(id) {
				return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
			}
			if _, ok := ids[id]; ok {
				return FundingSourceLinksCheck{}, ErrFundingSourceRowsInput
			}
			ids[id] = struct{}{}
		}
	}

	creditByID := make(map[string]int, len(rows.Credits))
	redemptionsByReceipt := make(map[string]int, len(rows.Redemptions))
	redemptionsByVoucher := make(map[string]int, len(rows.Redemptions))
	redemptionIndexByVoucher := make(map[string]int, len(rows.Redemptions))
	receiptsByID := make(map[string]int, len(rows.Receipts))
	receiptsBySource := make(map[fundingReceiptKey]int, len(rows.Receipts))
	receiptIndexBySource := make(map[fundingReceiptKey]int, len(rows.Receipts))
	for i, row := range rows.Credits {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		creditByID[row.ID] = i
	}
	for i, row := range rows.Redemptions {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		redemptionsByReceipt[row.FundingReceiptID]++
		redemptionsByVoucher[row.VoucherID]++
		redemptionIndexByVoucher[row.VoucherID] = i
	}
	for i, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		receiptsByID[row.ID] = i
		key := fundingReceiptKey{row.SourceKind, row.SourceID}
		receiptsBySource[key]++
		receiptIndexBySource[key] = i
	}

	out := FundingSourceLinksCheck{
		Scope: "manual_voucher_receipt_links",
		Scanned: FundingSourceRowCounts{
			Receipts: int64(len(rows.Receipts)), Credits: int64(len(rows.Credits)), Redemptions: int64(len(rows.Redemptions)),
		},
	}
	m := &out.Mismatches
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		amount, _ := fundingSourceCanonicalInt64(row.Amount)
		if row.ID == "" || row.SourceID == "" || row.JournalID == "" || row.Currency != "CNY" || amount <= 0 {
			m.ReceiptShapeMismatches++
		}
		switch row.SourceKind {
		case "manual_credit":
			creditIndex, ok := creditByID[row.SourceID]
			if !ok {
				m.OrphanManualReceipts++
				continue
			}
			credit := rows.Credits[creditIndex]
			if credit.State != "approved" || credit.JournalID == nil || *credit.JournalID != row.JournalID || credit.TenantID != row.TenantID || credit.Amount != row.Amount {
				m.OrphanManualReceipts++
			}
		case "voucher_grant":
			if redemptionsByVoucher[row.SourceID] != 1 {
				m.OrphanVoucherReceipts++
				continue
			}
			redemption := rows.Redemptions[redemptionIndexByVoucher[row.SourceID]]
			matched := redemption.FundingReceiptID == row.ID && redemption.TenantID == row.TenantID && redemption.Amount == row.Amount && redemption.JournalID == row.JournalID
			if !matched {
				m.OrphanVoucherReceipts++
			}
		case "sandbox_capture":
			out.UncoveredSandboxReceipts++
		default:
			out.UnknownReceiptKinds++
		}
	}
	for key, count := range receiptsBySource {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		if count > 1 && (key.kind == "manual_credit" || key.kind == "voucher_grant") {
			m.DuplicateSourceReceipts += int64(count - 1)
		}
	}
	for _, row := range rows.Credits {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		amount, _ := fundingSourceCanonicalInt64(row.Amount)
		shape := row.ID == "" || row.Requester == "" || amount <= 0 || row.State != "pending" && row.State != "approved"
		if row.Approver != nil && (*row.Approver == "" || *row.Approver == row.Requester) {
			shape = true
		}
		if row.JournalID != nil && *row.JournalID == "" {
			shape = true
		}
		if row.State == "approved" && (row.Approver == nil || row.JournalID == nil) {
			shape = true
		}
		count := receiptsBySource[fundingReceiptKey{"manual_credit", row.ID}]
		switch row.State {
		case "pending":
			if row.Approver != nil || row.JournalID != nil || count != 0 {
				m.PendingCreditEffects++
			}
		case "approved":
			if count == 0 {
				m.ApprovedCreditMissingReceipt++
			} else if count != 1 {
				m.CreditReceiptMismatches++
			} else {
				receipt := rows.Receipts[receiptIndexBySource[fundingReceiptKey{"manual_credit", row.ID}]]
				if receipt.SourceID != row.ID || receipt.TenantID != row.TenantID || receipt.Amount != row.Amount || row.JournalID == nil || receipt.JournalID != *row.JournalID || receipt.Currency != "CNY" {
					m.CreditReceiptMismatches++
				}
			}
		default:
		}
		if shape {
			m.CreditShapeMismatches++
		}
	}
	for _, row := range rows.Redemptions {
		if err := w.tick(); err != nil {
			return FundingSourceLinksCheck{}, err
		}
		amount, _ := fundingSourceCanonicalInt64(row.Amount)
		if row.ID == "" || row.VoucherID == "" || row.FundingReceiptID == "" || row.JournalID == "" || amount <= 0 {
			m.RedemptionShapeMismatches++
		}
		if redemptionsByReceipt[row.FundingReceiptID] > 1 || redemptionsByVoucher[row.VoucherID] > 1 {
			m.RedemptionShapeMismatches++
		}
		receiptIndex, ok := receiptsByID[row.FundingReceiptID]
		if !ok {
			m.RedemptionReceiptMismatches++
			continue
		}
		receipt := rows.Receipts[receiptIndex]
		if receipt.SourceKind != "voucher_grant" || receipt.SourceID != row.VoucherID || receipt.TenantID != row.TenantID || receipt.Amount != row.Amount || receipt.JournalID != row.JournalID || receipt.Currency != "CNY" {
			m.RedemptionReceiptMismatches++
		}
	}
	out.ScopedLinksConsistent = m.ReceiptShapeMismatches == 0 && m.CreditShapeMismatches == 0 && m.PendingCreditEffects == 0 &&
		m.ApprovedCreditMissingReceipt == 0 && m.CreditReceiptMismatches == 0 && m.OrphanManualReceipts == 0 &&
		m.RedemptionShapeMismatches == 0 && m.RedemptionReceiptMismatches == 0 && m.OrphanVoucherReceipts == 0 && m.DuplicateSourceReceipts == 0 && out.UnknownReceiptKinds == 0
	if ctx.Err() != nil {
		return FundingSourceLinksCheck{}, fundingSourceContextError(ctx)
	}
	return out, nil
}
