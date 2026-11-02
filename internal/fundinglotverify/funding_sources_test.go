package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sourceText(v string) *string { return &v }

func validFundingSourceRows() FundingSourceRows {
	return FundingSourceRows{
		Receipts: []FundingSourceReceiptRow{
			{ID: "credit", TenantID: "tenant-a", SourceKind: "manual_credit", SourceID: "credit", Amount: "10", Currency: "CNY", JournalID: "journal-a"},
			{ID: "receipt-voucher", TenantID: "tenant-a", SourceKind: "voucher_grant", SourceID: "voucher-a", Amount: "20", Currency: "CNY", JournalID: "journal-b"},
			{ID: "receipt-sandbox", TenantID: "tenant-a", SourceKind: "sandbox_capture", SourceID: "capture-a", Amount: "30", Currency: "CNY", JournalID: "journal-c"},
		},
		Credits: []FundingSourceCreditRow{
			{ID: "credit", TenantID: "tenant-a", Amount: "10", State: "approved", Requester: "maker", Approver: sourceText("checker"), JournalID: sourceText("journal-a")},
		},
		Redemptions: []FundingSourceRedemptionRow{
			{ID: "redemption-a", TenantID: "tenant-a", VoucherID: "voucher-a", FundingReceiptID: "receipt-voucher", Amount: "20", JournalID: "journal-b"},
		},
	}
}

func TestCheckFundingSourceLinksManualVoucherAndExplicitSandboxGap(t *testing.T) {
	rows := validFundingSourceRows()
	original := FundingSourceRows{
		Receipts:    append([]FundingSourceReceiptRow(nil), rows.Receipts...),
		Credits:     append([]FundingSourceCreditRow(nil), rows.Credits...),
		Redemptions: append([]FundingSourceRedemptionRow(nil), rows.Redemptions...),
	}
	for i := range original.Credits {
		if original.Credits[i].Approver != nil {
			original.Credits[i].Approver = sourceText(*original.Credits[i].Approver)
		}
		if original.Credits[i].JournalID != nil {
			original.Credits[i].JournalID = sourceText(*original.Credits[i].JournalID)
		}
	}
	got, err := CheckFundingSourceLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ScopedLinksConsistent || got.Scope != "manual_voucher_receipt_links" || got.UncoveredSandboxReceipts != 1 ||
		got.Scanned != (FundingSourceRowCounts{Receipts: 3, Credits: 1, Redemptions: 1}) || got.Mismatches != (FundingSourceLinkMismatches{}) {
		t.Fatalf("unexpected scoped result: %+v", got)
	}
	if !reflect.DeepEqual(rows, original) {
		t.Fatal("checker mutated supplied source rows")
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), `"uncovered_sandbox_receipts":"1"`) ||
		!strings.Contains(string(encoded), `"receipts":"3"`) ||
		!strings.Contains(string(encoded), `"orphan_manual_receipts":"0"`) || strings.Contains(string(encoded), "capture-a") {
		t.Fatalf("counter encoding or output scope is unsafe: %s %v", encoded, err)
	}
}

func TestCheckFundingSourceLinksCountsBothSidesWithoutSQLOracles(t *testing.T) {
	rows := FundingSourceRows{
		Receipts: []FundingSourceReceiptRow{
			{ID: "pending-receipt", TenantID: "t", SourceKind: "manual_credit", SourceID: "pending", Amount: "5", Currency: "CNY", JournalID: "j1"},
			{ID: "mismatch-receipt", TenantID: "t", SourceKind: "manual_credit", SourceID: "approved", Amount: "7", Currency: "CNY", JournalID: "wrong-journal"},
			{ID: "orphan-voucher", TenantID: "t", SourceKind: "voucher_grant", SourceID: "no-redemption", Amount: "9", Currency: "CNY", JournalID: "j3"},
			{ID: "unknown-receipt", TenantID: "t", SourceKind: "unexpected", SourceID: "x", Amount: "1", Currency: "CNY", JournalID: "j4"},
		},
		Credits: []FundingSourceCreditRow{
			{ID: "pending", TenantID: "t", Amount: "5", State: "pending", Requester: "maker"},
			{ID: "approved", TenantID: "t", Amount: "7", State: "approved", Requester: "maker", Approver: sourceText("checker"), JournalID: sourceText("j2")},
			{ID: "missing", TenantID: "t", Amount: "8", State: "approved", Requester: "maker", Approver: sourceText("checker"), JournalID: sourceText("j5")},
		},
		Redemptions: []FundingSourceRedemptionRow{
			{ID: "broken-redemption", TenantID: "t", VoucherID: "missing-voucher", FundingReceiptID: "absent", Amount: "3", JournalID: "j6"},
		},
	}
	got, err := CheckFundingSourceLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := FundingSourceLinkMismatches{
		PendingCreditEffects: 1, ApprovedCreditMissingReceipt: 1, CreditReceiptMismatches: 1,
		OrphanManualReceipts: 2, RedemptionReceiptMismatches: 1, OrphanVoucherReceipts: 1,
	}
	if got.ScopedLinksConsistent || got.Mismatches != want || got.UnknownReceiptKinds != 1 {
		t.Fatalf("got counters %+v unknown=%d, want %+v and one unknown", got.Mismatches, got.UnknownReceiptKinds, want)
	}
}

func TestCheckFundingSourceLinksDetectsDuplicateSourceReceipts(t *testing.T) {
	rows := validFundingSourceRows()
	rows.Receipts = append(rows.Receipts, FundingSourceReceiptRow{
		ID: "second-credit-receipt", TenantID: "tenant-a", SourceKind: "manual_credit", SourceID: "credit", Amount: "10", Currency: "CNY", JournalID: "journal-a",
	})
	got, err := CheckFundingSourceLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.ScopedLinksConsistent || got.Mismatches.DuplicateSourceReceipts != 1 || got.Mismatches.CreditReceiptMismatches != 1 {
		t.Fatalf("duplicate receipt was not rejected by independent source grouping: %+v", got)
	}
}

func TestCheckFundingSourceLinksPreservesNullAndEmptyTextAndTableLocalIDs(t *testing.T) {
	rows := FundingSourceRows{
		Receipts: []FundingSourceReceiptRow{{ID: "receipt-id", TenantID: "t", SourceKind: "manual_credit", SourceID: "", Amount: "1", Currency: "CNY", JournalID: ""}},
		Credits:  []FundingSourceCreditRow{{ID: "", TenantID: "t", Amount: "1", State: "pending", Requester: "", Approver: nil, JournalID: nil}},
	}
	got, err := CheckFundingSourceLinks(context.Background(), rows)
	if err != nil {
		t.Fatalf("empty text IDs are retained as exact keys and cross-table IDs may overlap: %v", err)
	}
	if got.Mismatches.ReceiptShapeMismatches != 1 || got.Mismatches.OrphanManualReceipts != 1 || got.Mismatches.PendingCreditEffects != 1 {
		t.Fatalf("NULL/empty values or table-local uniqueness were conflated: %+v", got.Mismatches)
	}
	rows.Credits[0].Approver = sourceText("")
	rows.Credits[0].JournalID = sourceText("")
	if _, err := CheckFundingSourceLinks(context.Background(), rows); err != nil {
		t.Fatalf("non-nil empty text is not a NULL decoding error: %v", err)
	}
}

func TestCheckFundingSourceLinksRejectsAmbiguousOrInvalidInputAtomically(t *testing.T) {
	rows := validFundingSourceRows()
	rows.Credits = append(rows.Credits, rows.Credits[0])
	if got, err := CheckFundingSourceLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("duplicate primary key was not an atomic input failure: %+v %v", got, err)
	}
	rows = validFundingSourceRows()
	rows.Receipts[0].Amount = "01"
	if got, err := CheckFundingSourceLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("noncanonical amount was not an atomic input failure: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := CheckFundingSourceLinks(ctx, validFundingSourceRows()); !errors.Is(err, ErrFundingSourceRowsCanceled) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("canceled input was not zeroed: %+v %v", got, err)
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 0)
	defer deadlineCancel()
	if got, err := CheckFundingSourceLinks(deadlineCtx, validFundingSourceRows()); !errors.Is(err, ErrFundingSourceRowsTimeout) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("deadline input was not classified as timeout: %+v %v", got, err)
	}
}

func TestCheckFundingSourceLinksEnforcesOwnedRowAndByteBudgets(t *testing.T) {
	rows := FundingSourceRows{Receipts: make([]FundingSourceReceiptRow, fundingSourceRowLimit+1)}
	if got, err := CheckFundingSourceLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsResource) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("row limit was not enforced atomically: %+v %v", got, err)
	}
	base := FundingSourceReceiptRow{ID: "r", TenantID: "t", SourceKind: "sandbox_capture", Amount: "1", Currency: "CNY", JournalID: "j"}
	baseBytes := len(base.ID) + len(base.TenantID) + len(base.SourceKind) + len(base.SourceID) + len(base.Amount) + len(base.Currency) + len(base.JournalID)
	base.SourceID = strings.Repeat("x", fundingSourceByteLimit-baseBytes)
	if _, err := CheckFundingSourceLinks(context.Background(), FundingSourceRows{Receipts: []FundingSourceReceiptRow{base}}); err != nil {
		t.Fatalf("exact byte budget should be accepted: %v", err)
	}
	base.SourceID += "x"
	if got, err := CheckFundingSourceLinks(context.Background(), FundingSourceRows{Receipts: []FundingSourceReceiptRow{base}}); !errors.Is(err, ErrFundingSourceRowsResource) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("byte budget overflow was not atomic: %+v %v", got, err)
	}
}

func TestCheckFundingSourceLinksRejectsInvalidUTF8AndNULAtomically(t *testing.T) {
	invalidUTF8 := string([]byte{0xff})
	for name, mutate := range map[string]func(*FundingSourceRows){
		"receipt invalid utf8": func(rows *FundingSourceRows) { rows.Receipts[0].SourceID = invalidUTF8 },
		"credit nul":           func(rows *FundingSourceRows) { rows.Credits[0].Requester = "maker\x00suffix" },
		"redemption invalid utf8": func(rows *FundingSourceRows) {
			rows.Redemptions[0].VoucherID = invalidUTF8
		},
		"optional approver nul": func(rows *FundingSourceRows) { rows.Credits[0].Approver = sourceText("checker\x00") },
	} {
		t.Run(name, func(t *testing.T) {
			rows := validFundingSourceRows()
			mutate(&rows)
			if got, err := CheckFundingSourceLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSourceLinksCheck{}) {
				t.Fatalf("invalid text did not fail atomically: %+v %v", got, err)
			}
		})
	}
}

func TestCheckFundingSourceLinksCountsNonpositiveAmountsAndNonCNYReceiptsAsShape(t *testing.T) {
	t.Run("negative amount is a shape mismatch", func(t *testing.T) {
		rows := validFundingSourceRows()
		rows.Receipts[0].Amount = "-10"
		rows.Credits[0].Amount = "-10"
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.ReceiptShapeMismatches != 1 || got.Mismatches.CreditShapeMismatches != 1 ||
			got.Mismatches.CreditReceiptMismatches != 0 || got.Mismatches.OrphanManualReceipts != 0 {
			t.Fatalf("canonical negative amount must be counted as shape, not input or link failure: %+v", got.Mismatches)
		}
	})

	t.Run("zero redemption amount is a shape mismatch", func(t *testing.T) {
		rows := validFundingSourceRows()
		rows.Redemptions[0].Amount = "0"
		rows.Receipts[1].Amount = "0"
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.ReceiptShapeMismatches != 1 || got.Mismatches.RedemptionShapeMismatches != 1 ||
			got.Mismatches.RedemptionReceiptMismatches != 0 || got.Mismatches.OrphanVoucherReceipts != 0 {
			t.Fatalf("zero amount shape counters differ: %+v", got.Mismatches)
		}
	})

	t.Run("receipt currency mismatch is local shape and relation mismatch", func(t *testing.T) {
		rows := validFundingSourceRows()
		rows.Receipts[0].Currency = "USD"
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.ReceiptShapeMismatches != 1 || got.Mismatches.CreditReceiptMismatches != 1 ||
			got.Mismatches.OrphanManualReceipts != 0 {
			t.Fatalf("non-CNY receipt should fail shape and approved-credit currency relation: %+v", got.Mismatches)
		}
	})
}

func TestCheckFundingSourceLinksCountsVoucherDuplicatesAndBothOrphanDirections(t *testing.T) {
	t.Run("duplicate voucher and missing receipt", func(t *testing.T) {
		rows := FundingSourceRows{
			Receipts: []FundingSourceReceiptRow{{ID: "receipt-1", TenantID: "tenant", SourceKind: "voucher_grant", SourceID: "voucher", Amount: "7", Currency: "CNY", JournalID: "journal"}},
			Redemptions: []FundingSourceRedemptionRow{
				{ID: "redemption-1", TenantID: "tenant", VoucherID: "voucher", FundingReceiptID: "receipt-1", Amount: "7", JournalID: "journal"},
				{ID: "redemption-2", TenantID: "tenant", VoucherID: "voucher", FundingReceiptID: "missing", Amount: "7", JournalID: "journal"},
			},
		}
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.RedemptionShapeMismatches != 2 || got.Mismatches.RedemptionReceiptMismatches != 1 ||
			got.Mismatches.OrphanVoucherReceipts != 1 {
			t.Fatalf("duplicate voucher / orphan counts differ: %+v", got.Mismatches)
		}
	})

	t.Run("duplicate receipt reference", func(t *testing.T) {
		rows := FundingSourceRows{
			Receipts: []FundingSourceReceiptRow{{ID: "receipt-1", TenantID: "tenant", SourceKind: "voucher_grant", SourceID: "voucher-1", Amount: "7", Currency: "CNY", JournalID: "journal"}},
			Redemptions: []FundingSourceRedemptionRow{
				{ID: "redemption-1", TenantID: "tenant", VoucherID: "voucher-1", FundingReceiptID: "receipt-1", Amount: "7", JournalID: "journal"},
				{ID: "redemption-2", TenantID: "tenant", VoucherID: "voucher-2", FundingReceiptID: "receipt-1", Amount: "7", JournalID: "journal"},
			},
		}
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.RedemptionShapeMismatches != 2 || got.Mismatches.RedemptionReceiptMismatches != 1 ||
			got.Mismatches.OrphanVoucherReceipts != 0 {
			t.Fatalf("duplicate receipt reference counts differ: %+v", got.Mismatches)
		}
	})

	t.Run("duplicate source receipts and reverse orphan", func(t *testing.T) {
		rows := FundingSourceRows{
			Receipts: []FundingSourceReceiptRow{
				{ID: "receipt-1", TenantID: "tenant", SourceKind: "voucher_grant", SourceID: "voucher", Amount: "7", Currency: "CNY", JournalID: "journal"},
				{ID: "receipt-2", TenantID: "tenant", SourceKind: "voucher_grant", SourceID: "voucher", Amount: "7", Currency: "CNY", JournalID: "journal"},
			},
			Redemptions: []FundingSourceRedemptionRow{{ID: "redemption-1", TenantID: "tenant", VoucherID: "voucher", FundingReceiptID: "receipt-1", Amount: "7", JournalID: "journal"}},
		}
		got, err := CheckFundingSourceLinks(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.Mismatches.DuplicateSourceReceipts != 1 || got.Mismatches.OrphanVoucherReceipts != 1 ||
			got.Mismatches.RedemptionReceiptMismatches != 0 {
			t.Fatalf("duplicate receipt/reverse orphan counts differ: %+v", got.Mismatches)
		}
	})
}

func TestCheckFundingSourceLinksContextValidationAndMidScanCancellation(t *testing.T) {
	if got, err := CheckFundingSourceLinks(nil, validFundingSourceRows()); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSourceLinksCheck{}) {
		t.Fatalf("nil context did not fail atomically: %+v %v", got, err)
	}

	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{name: "mid-scan canceled", err: context.Canceled, want: ErrFundingSourceRowsCanceled},
		{name: "mid-scan deadline", err: context.DeadlineExceeded, want: ErrFundingSourceRowsTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &fundingSourceSwitchingContext{switchAfter: 2, err: tc.err}
			rows := FundingSourceRows{Receipts: make([]FundingSourceReceiptRow, 256)}
			for i := range rows.Receipts {
				rows.Receipts[i] = FundingSourceReceiptRow{
					ID:       strings.Repeat("r", 1) + string(rune('a'+i%26)) + string(rune('A'+i/26)),
					TenantID: "tenant", SourceKind: "sandbox_capture", SourceID: "capture", Amount: "1", Currency: "CNY", JournalID: "journal",
				}
			}
			if got, err := CheckFundingSourceLinks(ctx, rows); !errors.Is(err, tc.want) || got != (FundingSourceLinksCheck{}) {
				t.Fatalf("mid-scan context failure did not return the fixed zero result: %+v %v", got, err)
			}
		})
	}
}

func TestCheckFundingSourceLinksObservesCancellationDuringIndexConstruction(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{name: "canceled", err: context.Canceled, want: ErrFundingSourceRowsCanceled},
		{name: "deadline", err: context.DeadlineExceeded, want: ErrFundingSourceRowsTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := FundingSourceRows{Receipts: make([]FundingSourceReceiptRow, 1000)}
			for i := range rows.Receipts {
				rows.Receipts[i] = FundingSourceReceiptRow{
					ID: fmt.Sprintf("receipt-%04d", i), TenantID: "tenant", SourceKind: "sandbox_capture",
					SourceID: "capture", Amount: "1", Currency: "CNY", JournalID: "journal",
				}
			}
			// Entry, row/shape validation, amount validation, and duplicate-key
			// indexing each run in bounded 256-row batches. The 11th check falls
			// inside the receipt-map construction loop.
			ctx := &fundingSourceSwitchingContext{switchAfter: 11, err: tc.err}
			if got, err := CheckFundingSourceLinks(ctx, rows); !errors.Is(err, tc.want) || got != (FundingSourceLinksCheck{}) {
				t.Fatalf("index construction ignored context failure: %+v %v (checks=%d)", got, err, ctx.checks)
			}
		})
	}
}

type fundingSourceSwitchingContext struct {
	checks      int
	switchAfter int
	err         error
}

func (c *fundingSourceSwitchingContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *fundingSourceSwitchingContext) Done() <-chan struct{}       { return nil }
func (c *fundingSourceSwitchingContext) Value(any) any               { return nil }
func (c *fundingSourceSwitchingContext) Err() error {
	c.checks++
	if c.checks >= c.switchAfter {
		return c.err
	}
	return nil
}
