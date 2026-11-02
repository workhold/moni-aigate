package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func validSandboxCaptureRows() FundingSandboxCaptureRows {
	return FundingSandboxCaptureRows{
		Receipts: []FundingSourceReceiptRow{
			{ID: "receipt-capture", TenantID: "tenant-a", SourceKind: "sandbox_capture", SourceID: "capture-a", Amount: "20", Currency: "CNY", JournalID: "journal-a"},
			{ID: "receipt-manual", TenantID: "tenant-a", SourceKind: "manual_credit", SourceID: "credit-a", Amount: "30", Currency: "CNY", JournalID: "journal-b"},
		},
		Captures: []FundingSandboxCaptureRow{
			{ID: "capture-a", TenantID: "tenant-a", Amount: "20", WalletCreditAmount: "20", Currency: "CNY"},
		},
	}
}

func TestCheckSandboxCaptureReceiptLinksOnlyClaimsDirectReferences(t *testing.T) {
	rows := validSandboxCaptureRows()
	// The two tables own independent primary-key spaces; identical TEXT values
	// across them do not make the raw projection ambiguous.
	rows.Receipts[0].ID = rows.Captures[0].ID
	original := FundingSandboxCaptureRows{
		Receipts: append([]FundingSourceReceiptRow(nil), rows.Receipts...),
		Captures: append([]FundingSandboxCaptureRow(nil), rows.Captures...),
	}
	got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "sandbox_capture_receipt_reference_v1" || !got.DirectLinksConsistent ||
		got.Scanned != (FundingSandboxCaptureLinkCounts{Receipts: 2, Captures: 1}) || got.OtherFundingReceipts != 1 ||
		got.UnvalidatedCaptureGraphs != 1 || got.UnvalidatedLedgerEffects != 1 {
		t.Fatalf("unexpected direct-link scope: %+v", got)
	}
	if !reflect.DeepEqual(rows, original) {
		t.Fatal("direct-link checker mutated supplied rows")
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), `"receipts":"2"`) ||
		!strings.Contains(string(encoded), `"unvalidated_capture_graphs":"1"`) || strings.Contains(string(encoded), "capture-a") {
		t.Fatalf("unsafe source link summary encoding: %s %v", encoded, err)
	}
}

func TestCheckSandboxCaptureReceiptLinksCountsBothDirectionsAndDuplicates(t *testing.T) {
	rows := FundingSandboxCaptureRows{
		Receipts: []FundingSourceReceiptRow{
			{ID: "orphan", TenantID: "tenant", SourceKind: "sandbox_capture", SourceID: "absent", Amount: "3", Currency: "CNY", JournalID: "j1"},
			{ID: "wrong", TenantID: "tenant-x", SourceKind: "sandbox_capture", SourceID: "capture-a", Amount: "7", Currency: "CNY", JournalID: "j2"},
			{ID: "dup-one", TenantID: "tenant", SourceKind: "sandbox_capture", SourceID: "capture-b", Amount: "5", Currency: "CNY", JournalID: "j3"},
			{ID: "dup-two", TenantID: "tenant", SourceKind: "sandbox_capture", SourceID: "capture-b", Amount: "5", Currency: "CNY", JournalID: "j4"},
		},
		Captures: []FundingSandboxCaptureRow{
			{ID: "capture-a", TenantID: "tenant", Amount: "7", WalletCreditAmount: "7", Currency: "CNY"},
			{ID: "capture-b", TenantID: "tenant", Amount: "5", WalletCreditAmount: "5", Currency: "CNY"},
			{ID: "capture-c", TenantID: "tenant", Amount: "11", WalletCreditAmount: "11", Currency: "CNY"},
		},
	}
	got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := FundingSandboxCaptureLinkCheck{
		Scope: "sandbox_capture_receipt_reference_v1", Scanned: FundingSandboxCaptureLinkCounts{Receipts: 4, Captures: 3},
		OrphanSandboxReceipts: 1, SandboxReceiptCaptureMismatches: 1, CapturesMissingReceipts: 1,
		CaptureReceiptCountMismatches: 1, DuplicateSandboxReceiptSources: 1,
		UnvalidatedCaptureGraphs: 3, UnvalidatedLedgerEffects: 3,
	}
	if got != want {
		t.Fatalf("unexpected independent relation counts:\n got %+v\nwant %+v", got, want)
	}
}

func TestCheckSandboxCaptureReceiptLinksShapeAndOtherSourceScope(t *testing.T) {
	rows := validSandboxCaptureRows()
	rows.Receipts[0].Currency = "USD"
	rows.Captures[0].WalletCreditAmount = "19"
	rows.Captures[0].Currency = "USD"
	rows.Receipts = append(rows.Receipts, FundingSourceReceiptRow{ID: "unknown", TenantID: "tenant", SourceKind: "unknown", SourceID: "source", Amount: "0", Currency: "USD", JournalID: "j"})
	got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectLinksConsistent || got.FundingReceiptShapeMismatches != 1 || got.CaptureShapeMismatches != 1 ||
		got.SandboxReceiptCaptureMismatches != 1 || got.OtherFundingReceipts != 2 {
		t.Fatalf("shape and out-of-scope rows were not classified locally: %+v", got)
	}
}

func TestCheckSandboxCaptureReceiptLinksCountsNonpositiveAmountsAsShape(t *testing.T) {
	rows := validSandboxCaptureRows()
	rows.Receipts[0].Amount = "0"
	rows.Captures[0].Amount = "-1"
	rows.Captures[0].WalletCreditAmount = "-1"
	got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.DirectLinksConsistent || got.FundingReceiptShapeMismatches != 1 || got.CaptureShapeMismatches != 1 || got.SandboxReceiptCaptureMismatches != 1 {
		t.Fatalf("nonpositive canonical amounts were not reported as local shape/link mismatches: %+v", got)
	}
}

func TestCheckSandboxCaptureReceiptLinksRejectsAmbiguousRowsAndBudgetsAtomically(t *testing.T) {
	rows := validSandboxCaptureRows()
	rows.Receipts = append(rows.Receipts, rows.Receipts[0])
	if got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("duplicate receipt PK did not fail atomically: %+v %v", got, err)
	}
	rows = validSandboxCaptureRows()
	rows.Captures = make([]FundingSandboxCaptureRow, fundingSourceRowLimit)
	if got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsResource) || got != (FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("combined row budget did not fail atomically: %+v %v", got, err)
	}
	rows = validSandboxCaptureRows()
	rows.Captures = append(rows.Captures, rows.Captures[0])
	if got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("duplicate capture primary key did not fail atomically: %+v %v", got, err)
	}
	base := FundingSandboxCaptureRow{ID: "capture", TenantID: "tenant", Amount: "1", WalletCreditAmount: "1", Currency: "CNY"}
	bytes := len(base.TenantID) + len(base.Amount) + len(base.WalletCreditAmount) + len(base.Currency)
	base.ID = strings.Repeat("x", fundingSourceByteLimit-bytes)
	if _, err := CheckSandboxCaptureReceiptLinks(context.Background(), FundingSandboxCaptureRows{Captures: []FundingSandboxCaptureRow{base}}); err != nil {
		t.Fatalf("exact byte budget rejected: %v", err)
	}
	base.ID += "x"
	if got, err := CheckSandboxCaptureReceiptLinks(context.Background(), FundingSandboxCaptureRows{Captures: []FundingSandboxCaptureRow{base}}); !errors.Is(err, ErrFundingSourceRowsResource) || got != (FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("byte overflow did not fail atomically: %+v %v", got, err)
	}
}

func TestCheckSandboxCaptureReceiptLinksRejectsInvalidTextAndAmounts(t *testing.T) {
	for name, mutate := range map[string]func(*FundingSandboxCaptureRows){
		"invalid utf8": func(rows *FundingSandboxCaptureRows) { rows.Captures[0].TenantID = string([]byte{0xff}) },
		"nul":          func(rows *FundingSandboxCaptureRows) { rows.Receipts[0].SourceID = "capture\x00a" },
		"noncanonical amount": func(rows *FundingSandboxCaptureRows) {
			rows.Captures[0].WalletCreditAmount = "020"
		},
	} {
		t.Run(name, func(t *testing.T) {
			rows := validSandboxCaptureRows()
			mutate(&rows)
			if got, err := CheckSandboxCaptureReceiptLinks(context.Background(), rows); !errors.Is(err, ErrFundingSourceRowsInput) || got != (FundingSandboxCaptureLinkCheck{}) {
				t.Fatalf("invalid input did not fail atomically: %+v %v", got, err)
			}
		})
	}
}

func TestCheckSandboxCaptureReceiptLinksObservesCancellationDuringIndexing(t *testing.T) {
	rows := FundingSandboxCaptureRows{
		Receipts: make([]FundingSourceReceiptRow, 1000),
		Captures: make([]FundingSandboxCaptureRow, 1000),
	}
	for i := range rows.Receipts {
		id := fmt.Sprintf("capture-%04d", i)
		rows.Receipts[i] = FundingSourceReceiptRow{ID: "receipt-" + id, TenantID: "tenant", SourceKind: "sandbox_capture", SourceID: id, Amount: "1", Currency: "CNY", JournalID: "journal-" + id}
		rows.Captures[i] = FundingSandboxCaptureRow{ID: id, TenantID: "tenant", Amount: "1", WalletCreditAmount: "1", Currency: "CNY"}
	}
	ctx := &fundingSourceSwitchingContext{switchAfter: 11, err: context.DeadlineExceeded}
	if got, err := CheckSandboxCaptureReceiptLinks(ctx, rows); !errors.Is(err, ErrFundingSourceRowsTimeout) || got != (FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("indexing did not observe timeout atomically: %+v %v checks=%d", got, err, ctx.checks)
	}
}
