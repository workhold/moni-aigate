package fundinglotscan

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func seedReadFacts(t *testing.T) (Facts, *readerQuery) {
	t.Helper()
	q := newReaderQuery(t, true)
	f, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	return f, q
}

// These white-box projection vectors deliberately do not claim valid source
// history. The original reader supplies all raw rows and child scan counts;
// only its private qualification flags are changed to isolate the copy codec.
// Public callers cannot mutate those flags or obtain maintenance authority.
func seedRichFacts(t *testing.T) (Facts, *readerQuery) {
	t.Helper()
	q := newReaderQuery(t, true)
	q.record("tenants", readerRecord{"id": readerText("租户/旧")})
	q.record("ledger_accounts", readerRecord{"id": readerText("账户"), "tenant_id": readerText("租户/旧"), "kind": readerText("AVAILABLE"), "balance": readerText("41")})
	q.record("journals", readerRecord{"id": readerText("账本"), "tenant_id": readerText("租户/旧"), "operation_id": readerText("操作/非UUID"), "kind": readerText("credit"), "currency": readerText("CNY"), "created_txid": readerText("123"), "reference": readerText(""), "actor": readerText("操作人")})
	q.record("journal_lines", readerRecord{"id": readerText("分录"), "journal_id": readerText("账本"), "account_id": readerText("账户"), "signed_delta": readerText("41")})
	q.record("funding_receipts", readerRecord{"id": readerText("收据"), "tenant_id": readerText("租户/旧"), "source_kind": readerText("manual_credit"), "source_id": readerText("充值单"), "amount": readerText("41"), "currency": readerText("CNY"), "journal_id": readerText("账本")})
	q.record("request_records", readerRecord{"id": readerText("请求"), "tenant_id": readerText("租户/旧"), "owner_id": readerText("客户"), "billing_status": readerText("released")})
	q.record("reservations", readerRecord{"id": readerText("预占"), "tenant_id": readerText("租户/旧"), "request_id": readerText("请求"), "amount": readerText("0"), "status": readerText("released")})
	q.record("settlements", readerRecord{"id": readerText("结算"), "tenant_id": readerText("租户/旧"), "request_id": readerText("请求"), "charged_amount": readerText("0"), "refunded_amount": readerText("0"), "status": readerText("released"), "reason": readerText(""), "journal_id": readerText("账本")})
	q.record("refund_orders", readerRecord{"id": readerText("退款"), "tenant_id": readerText("租户/旧"), "settlement_id": readerText("结算"), "amount": readerText("1"), "state": readerText("approved"), "requester": readerText("申请人"), "approver": readerText("审批人"), "journal_id": readerText("退款账本")})
	f, err := Read(context.Background(), q, "reader_fixture", ModeOrdinary)
	if err != nil {
		t.Fatal(err)
	}
	seedQualify(f.state)
	return f, q
}

func seedQualify(s *session) {
	r := &s.report
	r.Status, r.CoverageComplete = "consistent", true
	r.InputMismatchFields, r.JournalMetadataMismatches, r.OrdinaryBoundaryMismatches = 0, 0, 0
	r.Ledger.RowsConsistent, r.Ledger.Mismatches = true, fundinglotverify.LedgerRowMismatches{}
	r.Links.ScopedLinksConsistent, r.Links.Mismatches, r.Links.UnknownReceiptKinds = true, fundinglotverify.FundingSourceLinkMismatches{}, 0
	r.Funding.ScopedGraphConsistent, r.Funding.Mismatches = true, fundinglotverify.FundingSourceGraphMismatches{}
	r.Customer.Balanced, r.Customer.Mismatches = true, fundinglotverify.CustomerKeyGraphMismatches{}
	r.Procurement.Status, r.Procurement.Balanced, r.Procurement.CoverageComplete = "balanced", true, true
	r.Procurement.Structural, r.Procurement.Mismatches, r.Procurement.Unproven = fundinglotverify.ProcurementEvidenceGraphStructural{}, fundinglotverify.ProcurementEvidenceGraphMismatches{}, fundinglotverify.ProcurementEvidenceGraphUnproven{}
	r.Periodic.Status, r.Periodic.Balanced, r.Periodic.CoverageComplete = "balanced", true, true
	r.Periodic.Mismatches, r.Periodic.Unproven = fundinglotverify.PeriodicBudgetGraphMismatches{}, fundinglotverify.PeriodicBudgetGraphGaps{}
}

func seedCell(t *testing.T, f Facts, table, column string) *cell {
	t.Helper()
	s := f.state
	i := s.index[table]
	if len(s.tables[i].rows) == 0 {
		t.Fatal("missing white-box projection vector")
	}
	return &s.tables[i].rows[0][s.tables[i].spec.index[column]]
}

func assertSeedZero(t *testing.T, out LegacySeedRows, err, want error) {
	t.Helper()
	if err != want || !reflect.DeepEqual(out, LegacySeedRows{}) {
		t.Fatalf("expected fixed error and zero rows: err=%v want=%v", err, want)
	}
}

func TestProjectLegacySeedOriginalReadAndNoQuery(t *testing.T) {
	f, q := seedReadFacts(t)
	before := f.Summary()
	calls := len(q.calls)
	out, err := ProjectLegacySeed(context.Background(), f)
	if err != nil || reflect.ValueOf(out).NumField() != 9 {
		t.Fatalf("original 68-table ordinary projection failed: %v", err)
	}
	if len(q.calls) != calls || f.Summary() != before || len(f.state.rejected) != 0 {
		t.Fatal("projection queried or changed original facts")
	}
	if reflect.TypeFor[fundinglotverify.LegacySeedTenantRow]().NumField() != 1 {
		t.Fatal("legacy tenant projection fabricated a birth column")
	}
	for _, b := range legacySeedBindings() {
		if !b.valid {
			t.Fatal("invalid fixed seed binding")
		}
		for _, col := range b.columns {
			if col.name == "fl_created_xid" {
				t.Fatal("birth escaped into the legacy seed")
			}
		}
	}
}

func TestProjectLegacySeedNineGroupsAndOwnedValues(t *testing.T) {
	f, q := seedRichFacts(t)
	before, calls := f.Summary(), len(q.calls)
	out, err := ProjectLegacySeed(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	at := fundinglotverify.WalletLotsTime{Valid: true, Value: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC).In(time.Local)}
	tenant, approver, journal := "租户/旧", "审批人", "退款账本"
	want := LegacySeedRows{
		Tenants:      []fundinglotverify.LegacySeedTenantRow{{ID: tenant}},
		Accounts:     []fundinglotverify.LedgerAccountRow{{ID: "账户", TenantID: &tenant, Kind: "AVAILABLE", Balance: "41"}},
		Journals:     []fundinglotverify.WalletLotsJournalRow{{WalletSequenceJournalRow: fundinglotverify.WalletSequenceJournalRow{ID: "账本", TenantID: tenant, OperationID: "操作/非UUID", Kind: "credit", Currency: "CNY", CreatedTXID: "123"}, Reference: "", Actor: "操作人", CreatedAt: at}},
		Lines:        []fundinglotverify.LedgerLineRow{{ID: "分录", JournalID: "账本", AccountID: "账户", SignedDelta: "41"}},
		Receipts:     []fundinglotverify.WalletLotsReceiptRow{{ID: "收据", TenantID: tenant, SourceKind: "manual_credit", SourceID: "充值单", Amount: "41", Currency: "CNY", JournalID: "账本", CreatedAt: at}},
		Requests:     []fundinglotverify.WalletLotsRequestRow{{ID: "请求", TenantID: tenant, OwnerID: "客户", BillingStatus: "released"}},
		Reservations: []fundinglotverify.WalletLotsReservationRow{{ID: "预占", TenantID: tenant, RequestID: "请求", Amount: "0", Status: "released"}},
		Settlements:  []fundinglotverify.WalletLotsSettlementRow{{ID: "结算", TenantID: tenant, RequestID: "请求", ChargedAmount: "0", RefundedAmount: "0", Status: "released", Reason: "", JournalID: "账本"}},
		Refunds:      []fundinglotverify.WalletLotsRefundRow{{ID: "退款", TenantID: tenant, SettlementID: "结算", Amount: "1", State: "approved", Requester: "申请人", Approver: &approver, JournalID: &journal}},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatal("projection differs from independently listed nine groups")
	}
	if unsafe.StringData(out.Tenants[0].ID) == unsafe.StringData(seedCell(t, f, "tenants", "id").text) || unsafe.StringData(*out.Accounts[0].TenantID) == unsafe.StringData(seedCell(t, f, "ledger_accounts", "tenant_id").text) {
		t.Fatal("projected strings retain raw backing storage")
	}
	second, err := ProjectLegacySeed(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if &out.Tenants[0] == &second.Tenants[0] || out.Accounts[0].TenantID == second.Accounts[0].TenantID || out.Refunds[0].Approver == second.Refunds[0].Approver || out.Refunds[0].JournalID == second.Refunds[0].JournalID {
		t.Fatal("separate calls share mutable slices or pointers")
	}
	out.Tenants[0].ID, *out.Accounts[0].TenantID, *out.Refunds[0].Approver, *out.Refunds[0].JournalID = "changed", "changed", "changed", "changed"
	third, err := ProjectLegacySeed(context.Background(), f)
	if err != nil || !reflect.DeepEqual(third, want) || !reflect.DeepEqual(second, want) || f.Summary() != before || len(f.state.rejected) != 0 || len(q.calls) != calls {
		t.Fatal("caller mutation or repeated projection changed private facts or another result")
	}
}

func TestProjectLegacySeedOriginalContextIsNotReused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := newReaderQuery(t, true)
	f, err := Read(ctx, q, "reader_fixture", ModeOrdinary)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if _, err = ProjectLegacySeed(context.Background(), f); err != nil {
		t.Fatalf("pure projection inherited an expired Read context: %v", err)
	}
}

func TestProjectLegacySeedConcurrentCopies(t *testing.T) {
	f, q := seedRichFacts(t)
	before, calls := f.Summary(), len(q.calls)
	var out [2]LegacySeedRows
	var failures [2]error
	var group sync.WaitGroup
	for i := range out {
		group.Add(1)
		go func() {
			defer group.Done()
			out[i], failures[i] = ProjectLegacySeed(context.Background(), f)
		}()
	}
	group.Wait()
	if failures[0] != nil || failures[1] != nil || !reflect.DeepEqual(out[0], out[1]) || &out[0].Tenants[0] == &out[1].Tenants[0] || out[0].Accounts[0].TenantID == out[1].Accounts[0].TenantID || out[0].Refunds[0].Approver == out[1].Refunds[0].Approver {
		t.Fatal("concurrent copies failed or share mutable output")
	}
	*out[0].Accounts[0].TenantID, *out[0].Refunds[0].Approver = "changed", "changed"
	if *out[1].Accounts[0].TenantID != "租户/旧" || *out[1].Refunds[0].Approver != "审批人" || before != f.Summary() || len(q.calls) != calls || len(f.state.rejected) != 0 {
		t.Fatal("concurrent projection changed facts, queries, or another copy")
	}
}

func TestProjectLegacySeedEveryChildCountFromNonemptyRead(t *testing.T) {
	q := newReaderQuery(t, false)
	tables, _, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	if err != nil {
		t.Fatal(err)
	}
	for ti, table := range tables {
		count := ti + 1
		if table.spec.keys[0].name == "singleton" {
			count = 1
		}
		for ri := 0; ri < count; ri++ {
			values := readerRecord{}
			for _, col := range table.spec.columns {
				if col.kind == textKind {
					values[col.name] = readerText("1")
				}
			}
			for _, key := range table.spec.keys {
				if table.spec.columns[table.spec.index[key.name]].kind == boolKind {
					continue
				}
				value := fmt.Sprintf("key/%04d", ri)
				if key.numeric {
					value = fmt.Sprint(ri + 1)
				}
				values[key.name] = readerText(value)
			}
			q.record(table.spec.name, values)
		}
	}
	f, err := Read(context.Background(), q, "reader_fixture", ModeSandbox)
	if err != nil {
		t.Fatal(err)
	}
	// Every original core really executed over the physical nonempty union.
	for _, algorithm := range f.Summary().Algorithms {
		if algorithm != "executed" {
			t.Fatal("nonempty vector failed native qualification")
		}
	}
	seedQualify(f.state)
	f.state.report.Sandbox.Status, f.state.report.Sandbox.ScopedGraphConsistent = "consistent", true
	f.state.report.Sandbox.Mismatches, f.state.report.Sandbox.Unproven = fundinglotverify.SandboxSourceGraphMismatches{}, fundinglotverify.SandboxSourceGraphUnproven{}
	if _, err := ProjectLegacySeed(context.Background(), f); err != nil {
		t.Fatalf("child count mapping rejected actual nonempty Read reports: %v", err)
	}
	for _, field := range []string{"Ledger", "Links", "Funding", "Customer", "Procurement", "Periodic", "Sandbox"} {
		t.Run(field, func(t *testing.T) {
			counts := reflect.ValueOf(&f.state.report).Elem().FieldByName(field).FieldByName("Scanned")
			for i := 0; i < counts.NumField(); i++ {
				value := counts.Field(i)
				if value.Kind() == reflect.Int64 {
					n := value.Int()
					value.SetInt(n + 1)
					out, err := ProjectLegacySeed(context.Background(), f)
					value.SetInt(n)
					assertSeedZero(t, out, err, ErrInput)
				} else {
					n := value.Uint()
					value.SetUint(n + 1)
					out, err := ProjectLegacySeed(context.Background(), f)
					value.SetUint(n)
					assertSeedZero(t, out, err, ErrInput)
				}
			}
		})
	}
}

func TestProjectLegacySeedPreservesNativeEnvelopesAndNullPointers(t *testing.T) {
	for _, native := range []pgtype.Timestamptz{
		{},
		{Valid: true, InfinityModifier: pgtype.Infinity},
		{Valid: true, InfinityModifier: pgtype.NegativeInfinity},
		{Valid: true, Time: time.Date(10000, 1, 2, 3, 4, 5, 6000, time.UTC)},
	} {
		f, _ := seedRichFacts(t)
		for _, table := range []string{"journals", "funding_receipts"} {
			c := seedCell(t, f, table, "created_at")
			c.valid, c.instant = native.Valid, native
		}
		for _, pair := range [][2]string{{"ledger_accounts", "tenant_id"}, {"refund_orders", "approver"}, {"refund_orders", "journal_id"}} {
			c := seedCell(t, f, pair[0], pair[1])
			f.state.bytes -= len(c.text)
			c.valid, c.text = false, ""
		}
		f.state.report.SourceBytes = uint64(f.state.bytes)
		out, err := ProjectLegacySeed(context.Background(), f)
		want := fundinglotverify.WalletLotsTime{Value: native.Time, Valid: native.Valid, Infinity: int8(native.InfinityModifier)}
		if err != nil || out.Journals[0].CreatedAt != want || out.Receipts[0].CreatedAt != want || out.Accounts[0].TenantID != nil || out.Refunds[0].Approver != nil || out.Refunds[0].JournalID != nil {
			t.Fatalf("native envelope or SQL NULL changed: %v", err)
		}
	}
}

func TestProjectLegacySeedRejectsQualifications(t *testing.T) {
	tests := []struct {
		name   string
		change func(*session)
	}{
		{"scope", func(s *session) { s.report.Scope = "complete_source_wallet_graphs_v1" }},
		{"report_mode", func(s *session) { s.report.Mode = ModeSandbox }},
		{"mode", func(s *session) { s.mode, s.report.Mode = 0, 0 }},
		{"status", func(s *session) { s.report.Status = "mismatch" }},
		{"coverage", func(s *session) { s.report.CoverageComplete = false }},
		{"physical_eof", func(s *session) { s.report.PhysicalEOFComplete = false }},
		{"table_count", func(s *session) { s.report.ScannedTables = 84 }},
		{"input_mismatch", func(s *session) { s.report.InputMismatchFields = 1 }},
		{"journal_mismatch", func(s *session) { s.report.JournalMetadataMismatches = 1 }},
		{"ordinary_mismatch", func(s *session) { s.report.OrdinaryBoundaryMismatches = 1 }},
		{"rejected", func(s *session) { s.rejected[cellRef{"tenants", 0, 0}] = true }},
		{"algorithm", func(s *session) { s.report.Algorithms[3] = "input_mismatch" }},
		{"ordinary_sandbox_algorithm", func(s *session) { s.report.Algorithms[6] = "executed" }},
		{"ordinary_sandbox_report", func(s *session) { s.report.Sandbox.Status = "consistent" }},
		{"ledger_scope", func(s *session) { s.report.Ledger.Scope = "other" }},
		{"ledger_flag", func(s *session) { s.report.Ledger.RowsConsistent = false }},
		{"ledger_mismatch", func(s *session) { s.report.Ledger.Mismatches.MissingAccounts = 1 }},
		{"links_flag", func(s *session) { s.report.Links.ScopedLinksConsistent = false }},
		{"links_mismatch", func(s *session) { s.report.Links.Mismatches.OrphanManualReceipts = 1 }},
		{"links_unknown", func(s *session) { s.report.Links.UnknownReceiptKinds = 1 }},
		{"links_uncovered", func(s *session) { s.report.Links.UncoveredSandboxReceipts = 1 }},
		{"funding_mismatch", func(s *session) { s.report.Funding.Mismatches.ReceiptSourceRows = 1 }},
		{"funding_uncovered", func(s *session) { s.report.Funding.UncoveredSandboxReceipts = 1 }},
		{"customer_mismatch", func(s *session) { s.report.Customer.Mismatches.RefundShapeRows = 1 }},
		{"procurement_structural", func(s *session) { s.report.Procurement.Structural.Budget.AccountHeldEventMismatches = 1 }},
		{"procurement_status", func(s *session) { s.report.Procurement.Status = "indeterminate" }},
		{"procurement_gap", func(s *session) { s.report.Procurement.Unproven.ConflictIncomingUnproven = 1 }},
		{"periodic_gap", func(s *session) { s.report.Periodic.Unproven.LegacyMembershipUnproven = 1 }},
		{"child_count", func(s *session) { s.report.Ledger.Scanned.Accounts = 1 }},
		{"child_negative_count", func(s *session) { s.report.Links.Scanned.Receipts = -1 }},
		{"one_incomplete_table", func(s *session) { s.tables[67].eof = false }},
		{"private_84_raw", func(s *session) {
			tables, index, _ := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings(), walletBindings())
			s.tables, s.index = tables, index
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := seedReadFacts(t)
			tt.change(f.state)
			out, err := ProjectLegacySeed(context.Background(), f)
			assertSeedZero(t, out, err, ErrInput)
		})
	}
	out, err := ProjectLegacySeed(context.Background(), Facts{})
	assertSeedZero(t, out, err, ErrInput)
}

func TestProjectLegacySeedPrivateStructureFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(Facts)
	}{
		{"index", func(f Facts) { f.state.index["tenants"] = 0 }},
		{"column", func(f Facts) { f.state.tables[0].spec.columns[0].name = "replacement" }},
		{"key", func(f Facts) { f.state.tables[0].spec.keys[0].name = "replacement" }},
		{"raw_count", func(f Facts) { f.state.rows++ }},
		{"raw_bytes", func(f Facts) { f.state.bytes++ }},
		{"report_count", func(f Facts) { f.state.report.SourceRows++ }},
		{"report_bytes", func(f Facts) { f.state.report.SourceBytes++ }},
		{"row_width", func(f Facts) { i := f.state.index["payment_policy"]; f.state.tables[i].rows[0] = nil }},
		{"cell_kind", func(f Facts) { seedCell(t, f, "payment_policy", "revision").kind = bytesKind }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := seedReadFacts(t)
			tt.change(f)
			out, err := ProjectLegacySeed(context.Background(), f)
			assertSeedZero(t, out, err, ErrExecution)
		})
	}
}

func TestProjectLegacySeedInvalidRawTextAndIndependentRejectedMap(t *testing.T) {
	for _, value := range []string{"bad\x00text", string([]byte{0xff, 0xfe})} {
		f, _ := seedRichFacts(t)
		c := seedCell(t, f, "request_records", "owner_id")
		f.state.bytes += len(value) - len(c.text)
		c.text = value
		f.state.report.SourceBytes = uint64(f.state.bytes)
		out, err := ProjectLegacySeed(context.Background(), f)
		assertSeedZero(t, out, err, ErrInput)
	}
	f, _ := seedRichFacts(t)
	c := seedCell(t, f, "request_records", "owner_id")
	f.state.bytes -= len(c.text)
	c.valid, c.text = false, ""
	f.state.report.SourceBytes = uint64(f.state.bytes)
	before := f.Summary()
	out, err := ProjectLegacySeed(context.Background(), f)
	assertSeedZero(t, out, err, ErrInput)
	if f.Summary() != before || len(f.state.rejected) != 0 {
		t.Fatal("failed projection stored a rejection in original facts")
	}
}

type seedCountingContext struct {
	context.Context
	calls, failAt int
	failure       error
}

func (c *seedCountingContext) Err() error {
	c.calls++
	if c.failAt > 0 && c.calls >= c.failAt {
		return c.failure
	}
	return nil
}

func TestProjectLegacySeedCancellationAlwaysZero(t *testing.T) {
	f, _ := seedRichFacts(t)
	out, err := ProjectLegacySeed(nil, f)
	assertSeedZero(t, out, err, ErrInput)
	for _, failure := range []struct{ cause, want error }{{context.Canceled, ErrCanceled}, {context.DeadlineExceeded, ErrTimeout}} {
		probe := &seedCountingContext{Context: context.Background()}
		if _, err := ProjectLegacySeed(probe, f); err != nil {
			t.Fatal(err)
		}
		for _, at := range []int{1, 5, 45, probe.calls - 3, probe.calls} {
			ctx := &seedCountingContext{Context: context.Background(), failAt: at, failure: failure.cause}
			out, err := ProjectLegacySeed(ctx, f)
			assertSeedZero(t, out, err, failure.want)
		}
	}
}

func TestProjectLegacySeedRawResourceBudgets(t *testing.T) {
	for _, size := range []int{65536, 65537} {
		f, _ := seedReadFacts(t)
		i := f.state.index["users"]
		f.state.tables[i].rows = []rawRow{{readerText(strings.Repeat("x", size))}}
		f.state.rows++
		f.state.bytes += size
		f.state.report.SourceRows, f.state.report.SourceBytes = uint64(f.state.rows), uint64(f.state.bytes)
		f.state.report.Funding.Scanned.Users++
		f.state.report.Customer.Scanned.Users++
		f.state.report.Procurement.Scanned.Users++
		f.state.report.Periodic.Scanned.Users++
		out, err := ProjectLegacySeed(context.Background(), f)
		if size == 65536 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertSeedZero(t, out, err, ErrResource)
		}
	}
	for _, count := range []int{99999, 100000} {
		f, _ := seedReadFacts(t)
		i := f.state.index["users"]
		f.state.tables[i].rows = make([]rawRow, count)
		for n := range f.state.tables[i].rows {
			// All rows have increasing fixed-width keys, independent of reader cursors.
			f.state.tables[i].rows[n] = rawRow{readerText(time.Unix(int64(n), 0).UTC().Format(time.RFC3339))}
		}
		seedUserCounts(f.state, count, count*20)
		// payment_policy consumes one of the shared rows.
		out, err := ProjectLegacySeed(context.Background(), f)
		if count == 99999 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertSeedZero(t, out, err, ErrResource)
		}
	}
	for _, extra := range []int{0, 1} {
		f, _ := seedReadFacts(t)
		i := f.state.index["users"]
		f.state.tables[i].rows = make([]rawRow, 256)
		for n := range f.state.tables[i].rows {
			size := 65536
			if n == 255 {
				size -= 3 - extra
			}
			value := fmt.Sprintf("%04d", n) + strings.Repeat("x", size-4)
			f.state.tables[i].rows[n] = rawRow{readerText(value)}
		}
		seedUserCounts(f.state, 256, byteLimit-3+extra)
		out, err := ProjectLegacySeed(context.Background(), f)
		if extra == 0 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertSeedZero(t, out, err, ErrResource)
		}
	}
}

func seedUserCounts(s *session, rows, bytes int) {
	s.rows, s.bytes = 1+rows, 3+bytes
	s.report.SourceRows, s.report.SourceBytes = uint64(s.rows), uint64(s.bytes)
	s.report.Funding.Scanned.Users = uint64(rows)
	s.report.Customer.Scanned.Users = uint64(rows)
	s.report.Procurement.Scanned.Users = uint64(rows)
	s.report.Periodic.Scanned.Users = uint64(rows)
}
