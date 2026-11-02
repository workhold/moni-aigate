package fundinglotverify

import (
	"context"
)

// FundingSandboxCaptureRow is the minimal raw capture projection needed to
// check only the direct capture-to-receipt reference. It proves no evidence or
// journal relation.
type FundingSandboxCaptureRow struct {
	ID, TenantID, Amount, WalletCreditAmount, Currency string
}

// FundingSandboxCaptureRows contains both complete raw table projections for
// the narrow direct-link scope.
type FundingSandboxCaptureRows struct {
	Receipts []FundingSourceReceiptRow
	Captures []FundingSandboxCaptureRow
}

type FundingSandboxCaptureLinkCounts struct {
	Receipts int64 `json:"receipts,string"`
	Captures int64 `json:"captures,string"`
}

// FundingSandboxCaptureLinkCheck is descriptive only. DirectLinksConsistent
// does not cover capture evidence, processing, effects, journals or ledger.
type FundingSandboxCaptureLinkCheck struct {
	Scope                           string                          `json:"scope"`
	DirectLinksConsistent           bool                            `json:"direct_links_consistent"`
	Scanned                         FundingSandboxCaptureLinkCounts `json:"scanned"`
	FundingReceiptShapeMismatches   int64                           `json:"funding_receipt_shape_mismatches,string"`
	CaptureShapeMismatches          int64                           `json:"capture_shape_mismatches,string"`
	OrphanSandboxReceipts           int64                           `json:"orphan_sandbox_receipts,string"`
	SandboxReceiptCaptureMismatches int64                           `json:"sandbox_receipt_capture_mismatches,string"`
	CapturesMissingReceipts         int64                           `json:"captures_missing_receipts,string"`
	CaptureReceiptCountMismatches   int64                           `json:"capture_receipt_count_mismatches,string"`
	DuplicateSandboxReceiptSources  int64                           `json:"duplicate_sandbox_receipt_sources,string"`
	OtherFundingReceipts            int64                           `json:"other_funding_receipts,string"`
	UnvalidatedCaptureGraphs        int64                           `json:"unvalidated_capture_graphs,string"`
	UnvalidatedLedgerEffects        int64                           `json:"unvalidated_ledger_effects,string"`
}

// CheckSandboxCaptureReceiptLinks checks only the direct relationship between
// every sandbox_capture receipt and payment_captures row. It never invokes a
// payment SQL validator and does not establish source provenance or funding.
func CheckSandboxCaptureReceiptLinks(ctx context.Context, rows FundingSandboxCaptureRows) (FundingSandboxCaptureLinkCheck, error) {
	if ctx == nil {
		return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
	}
	if ctx.Err() != nil {
		return FundingSandboxCaptureLinkCheck{}, fundingSourceContextError(ctx)
	}
	if len(rows.Receipts) > fundingSourceRowLimit || len(rows.Captures) > fundingSourceRowLimit-len(rows.Receipts) {
		return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsResource
	}
	w := fundingSourceCoreWork{ctx: ctx}
	leftBytes := fundingSourceByteLimit
	consume := func(value string) bool {
		if len(value) > leftBytes {
			return false
		}
		leftBytes -= len(value)
		return true
	}
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		for _, value := range [...]string{row.ID, row.TenantID, row.SourceKind, row.SourceID, row.Amount, row.Currency, row.JournalID} {
			if !fundingSourceText(value) {
				return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
			}
			if !consume(value) {
				return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsResource
			}
		}
		if _, ok := fundingSourceCanonicalInt64(row.Amount); !ok {
			return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
		}
	}
	for _, row := range rows.Captures {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		for _, value := range [...]string{row.ID, row.TenantID, row.Amount, row.WalletCreditAmount, row.Currency} {
			if !fundingSourceText(value) {
				return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
			}
			if !consume(value) {
				return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsResource
			}
		}
		if _, ok := fundingSourceCanonicalInt64(row.Amount); !ok {
			return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
		}
		if _, ok := fundingSourceCanonicalInt64(row.WalletCreditAmount); !ok {
			return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
		}
	}

	receiptIDs := make(map[string]struct{}, len(rows.Receipts))
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		if _, exists := receiptIDs[row.ID]; exists {
			return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
		}
		receiptIDs[row.ID] = struct{}{}
	}
	capturesByID := make(map[string]FundingSandboxCaptureRow, len(rows.Captures))
	for _, row := range rows.Captures {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		if _, exists := capturesByID[row.ID]; exists {
			return FundingSandboxCaptureLinkCheck{}, ErrFundingSourceRowsInput
		}
		capturesByID[row.ID] = row
	}
	receiptsBySource := make(map[string]int, len(rows.Receipts))
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		if row.SourceKind == "sandbox_capture" {
			receiptsBySource[row.SourceID]++
		}
	}

	out := FundingSandboxCaptureLinkCheck{
		Scope: "sandbox_capture_receipt_reference_v1",
		Scanned: FundingSandboxCaptureLinkCounts{
			Receipts: int64(len(rows.Receipts)), Captures: int64(len(rows.Captures)),
		},
		UnvalidatedCaptureGraphs: int64(len(rows.Captures)),
		UnvalidatedLedgerEffects: int64(len(rows.Captures)),
	}
	for _, row := range rows.Receipts {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		if row.SourceKind != "sandbox_capture" {
			out.OtherFundingReceipts++
			continue
		}
		amount, _ := fundingSourceCanonicalInt64(row.Amount)
		if row.ID == "" || row.TenantID == "" || row.SourceID == "" || row.JournalID == "" || amount <= 0 || row.Currency != "CNY" {
			out.FundingReceiptShapeMismatches++
		}
		capture, ok := capturesByID[row.SourceID]
		if !ok {
			out.OrphanSandboxReceipts++
			continue
		}
		if row.TenantID != capture.TenantID || row.Amount != capture.WalletCreditAmount || row.Currency != capture.Currency {
			out.SandboxReceiptCaptureMismatches++
		}
	}
	for _, count := range receiptsBySource {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		if count > 1 {
			out.DuplicateSandboxReceiptSources += int64(count - 1)
		}
	}
	for _, row := range rows.Captures {
		if err := w.tick(); err != nil {
			return FundingSandboxCaptureLinkCheck{}, err
		}
		amount, _ := fundingSourceCanonicalInt64(row.Amount)
		walletAmount, _ := fundingSourceCanonicalInt64(row.WalletCreditAmount)
		if row.ID == "" || row.TenantID == "" || amount <= 0 || walletAmount <= 0 || amount != walletAmount || row.Currency != "CNY" {
			out.CaptureShapeMismatches++
		}
		count := receiptsBySource[row.ID]
		if count == 0 {
			out.CapturesMissingReceipts++
		} else if count > 1 {
			out.CaptureReceiptCountMismatches++
		}
	}
	out.DirectLinksConsistent = out.FundingReceiptShapeMismatches == 0 && out.CaptureShapeMismatches == 0 &&
		out.OrphanSandboxReceipts == 0 && out.SandboxReceiptCaptureMismatches == 0 && out.CapturesMissingReceipts == 0 &&
		out.CaptureReceiptCountMismatches == 0 && out.DuplicateSandboxReceiptSources == 0
	if ctx.Err() != nil {
		return FundingSandboxCaptureLinkCheck{}, fundingSourceContextError(ctx)
	}
	return out, nil
}
