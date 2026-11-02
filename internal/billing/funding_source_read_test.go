package billing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type fundingSourceReaderTestRows struct {
	values   [][]any
	index    int
	scanned  bool
	closed   bool
	queryErr error
	rowsErr  error
	closeErr error
	nextErr  error
	scanErr  error
	onClose  func()
}

func (r *fundingSourceReaderTestRows) Next() (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.index >= 0 && r.index < len(r.values) && !r.scanned {
		return false, errors.New("test row was not scanned")
	}
	r.index++
	r.scanned = false
	return r.index < len(r.values), nil
}

func (r *fundingSourceReaderTestRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) || r.scanned {
		return errors.New("test scan arity or repeated scan")
	}
	for i, value := range r.values[r.index] {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("test scan target")
		}
		valueTarget := target.Elem()
		if value == nil {
			valueTarget.SetZero()
			continue
		}
		actual := reflect.ValueOf(value)
		if valueTarget.Kind() == reflect.Pointer {
			if actual.Type().AssignableTo(valueTarget.Type()) {
				valueTarget.Set(actual)
			} else if actual.Type().AssignableTo(valueTarget.Type().Elem()) {
				copy := reflect.New(valueTarget.Type().Elem())
				copy.Elem().Set(actual)
				valueTarget.Set(copy)
			} else {
				return fmt.Errorf("test scan type")
			}
			continue
		}
		if !actual.Type().AssignableTo(valueTarget.Type()) {
			return fmt.Errorf("test scan type")
		}
		valueTarget.Set(actual)
	}
	r.scanned = true
	return nil
}

func (r *fundingSourceReaderTestRows) Err() error { return r.rowsErr }
func (r *fundingSourceReaderTestRows) Close() error {
	r.closed = true
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}

type fundingSourceReaderTestQuery struct {
	tables       map[string][][]any
	calls        map[string]int
	queries      map[string][]string
	queryFailure string
	rowFailure   string
	nextFailure  string
	scanFailure  string
	closeFailure string
	onRowsClose  func(string)
}

func (q *fundingSourceReaderTestQuery) Query(sql string, args ...any) (fundingSourceRows, error) {
	var table string
	for _, candidate := range []string{"funding_receipts", "credit_orders", "voucher_redemptions", "payment_captures"} {
		if strings.Contains(sql, `"`+candidate+`"`) {
			table = candidate
			break
		}
	}
	if table == "" || len(args) != 2 {
		return nil, errors.New("private query shape")
	}
	if !strings.Contains(sql, `ORDER BY id COLLATE "C" LIMIT 128`) {
		return nil, errors.New("private query order")
	}
	if table == q.queryFailure {
		return nil, errors.New("secret query detail")
	}
	hasCursor, ok := args[0].(bool)
	if !ok {
		return nil, errors.New("private cursor mode")
	}
	cursor, ok := args[1].(string)
	if !ok {
		return nil, errors.New("private cursor")
	}
	var page [][]any
	for _, row := range q.tables[table] {
		id, ok := row[0].(string)
		if !ok || hasCursor && id <= cursor {
			continue
		}
		page = append(page, row)
		if len(page) == fundingSourcePageSize {
			break
		}
	}
	q.calls[table]++
	if q.queries == nil {
		q.queries = map[string][]string{}
	}
	q.queries[table] = append(q.queries[table], sql)
	rows := &fundingSourceReaderTestRows{values: page, index: -1}
	if q.onRowsClose != nil {
		rows.onClose = func() { q.onRowsClose(table) }
	}
	if table == q.rowFailure {
		rows.rowsErr = errors.New("secret rows detail")
	}
	if table == q.nextFailure {
		rows.nextErr = errors.New("secret next detail")
	}
	if table == q.scanFailure {
		rows.scanErr = errors.New("secret scan detail")
	}
	if table == q.closeFailure {
		rows.closeErr = errors.New("secret close detail")
	}
	return rows, nil
}

func fundingSourceQueryFixture() *fundingSourceReaderTestQuery {
	approver, journal := "checker", "journal-1"
	return &fundingSourceReaderTestQuery{
		tables: map[string][][]any{
			"funding_receipts":    {{"receipt-1", "tenant-1", "manual_credit", "credit-1", "5", "CNY", journal}},
			"credit_orders":       {{"credit-1", "tenant-1", "5", "approved", "maker", &approver, &journal}},
			"voucher_redemptions": {},
			"payment_captures":    {},
		},
		calls: map[string]int{},
	}
}

func TestReadFundingSourceFactsPagesThreeTablesAndChecksLinks(t *testing.T) {
	query := fundingSourceQueryFixture()
	facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if !got.ScopedLinksConsistent || got.Scanned != (fundinglotverifyFundingRows(1, 1, 0)) || got.Scope != "manual_voucher_receipt_links" {
		t.Fatalf("unexpected reader summary: %+v", got)
	}
	if query.calls["funding_receipts"] != 1 || query.calls["credit_orders"] != 1 || query.calls["voucher_redemptions"] != 1 {
		t.Fatalf("reader did not exhaust each fixed relation: %+v", query.calls)
	}
}

func fundinglotverifyFundingRows(receipts, credits, redemptions int64) fundinglotverify.FundingSourceRowCounts {
	return fundinglotverify.FundingSourceRowCounts{Receipts: receipts, Credits: credits, Redemptions: redemptions}
}

func TestReadFundingSourceFactsUsesCOrderAndExplicitCursorForEmptyText(t *testing.T) {
	query := &fundingSourceReaderTestQuery{
		tables: map[string][][]any{
			"funding_receipts": {{"", "t", "sandbox_capture", "", "1", "CNY", ""}, {"r", "t", "sandbox_capture", "c", "1", "CNY", "j"}},
			"credit_orders":    {}, "voucher_redemptions": {},
		},
		calls: map[string]int{},
	}
	facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if got.Scanned.Receipts != 2 || got.UncoveredSandboxReceipts != 2 || query.calls["funding_receipts"] != 1 {
		t.Fatalf("empty text ID was not included in the complete first page: %+v calls=%+v", got, query.calls)
	}
}

func TestReadFundingSourceFactsContinuesFullPagesUntilShortPage(t *testing.T) {
	query := fundingSourceQueryFixture()
	query.tables["funding_receipts"] = make([][]any, 129)
	for i := range query.tables["funding_receipts"] {
		id := fmt.Sprintf("receipt-%03d", i)
		query.tables["funding_receipts"][i] = []any{id, "tenant-1", "sandbox_capture", "capture", "1", "CNY", "journal"}
	}
	query.tables["credit_orders"] = nil
	query.tables["voucher_redemptions"] = nil
	facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if got.Scanned.Receipts != 129 || got.UncoveredSandboxReceipts != 129 || query.calls["funding_receipts"] != 2 {
		t.Fatalf("128-row continuation did not reach the final short page: %+v calls=%+v", got, query.calls)
	}
}

func fundingSourceReaderFixtureRow(table, id string) []any {
	switch table {
	case "funding_receipts":
		return []any{id, "tenant", "sandbox_capture", "capture-" + id, "1", "CNY", "journal-" + id}
	case "credit_orders":
		return []any{id, "tenant", "1", "pending", "requester", nil, nil}
	case "voucher_redemptions":
		return []any{id, "tenant", "voucher-" + id, "receipt-" + id, "1", "journal-" + id}
	case "payment_captures":
		return []any{id, "tenant", "1", "1", "CNY"}
	default:
		panic("unknown funding source fixture relation")
	}
}

func fundingSourceReaderQueryWithRows(table string, count int) *fundingSourceReaderTestQuery {
	query := fundingSourceQueryFixture()
	for _, relation := range []string{"funding_receipts", "credit_orders", "voucher_redemptions", "payment_captures"} {
		query.tables[relation] = nil
	}
	for i := 0; i < count; i++ {
		query.tables[table] = append(query.tables[table], fundingSourceReaderFixtureRow(table, fmt.Sprintf("row-%06d", i)))
	}
	return query
}

func TestReadFundingSourceFactsPaginationBoundariesForEveryRelation(t *testing.T) {
	for _, relation := range []string{"funding_receipts", "credit_orders", "voucher_redemptions"} {
		for _, count := range []int{0, 1, 127, 128, 129} {
			t.Run(fmt.Sprintf("%s/%d", relation, count), func(t *testing.T) {
				query := fundingSourceReaderQueryWithRows(relation, count)
				facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
				if err != nil {
					t.Fatal(err)
				}
				got := facts.LocalSummary().Scanned
				var expected fundinglotverify.FundingSourceRowCounts
				switch relation {
				case "funding_receipts":
					expected.Receipts = int64(count)
				case "credit_orders":
					expected.Credits = int64(count)
				case "voucher_redemptions":
					expected.Redemptions = int64(count)
				}
				calls := 1
				if count >= fundingSourcePageSize {
					calls = 2 // a full page must be followed by an explicit EOF query
				}
				if got != expected || query.calls[relation] != calls {
					t.Fatalf("incorrect complete read: got=%+v want=%+v calls=%d want_calls=%d", got, expected, query.calls[relation], calls)
				}
			})
		}
	}
}

func TestReadFundingSourceFactsClassifiesEachRowsLifecycleFailure(t *testing.T) {
	for _, relation := range []string{"funding_receipts", "credit_orders", "voucher_redemptions"} {
		for _, failure := range []struct {
			name string
			set  func(*fundingSourceReaderTestQuery, string)
		}{
			{"next", func(q *fundingSourceReaderTestQuery, table string) { q.nextFailure = table }},
			{"scan", func(q *fundingSourceReaderTestQuery, table string) { q.scanFailure = table }},
			{"err", func(q *fundingSourceReaderTestQuery, table string) { q.rowFailure = table }},
			{"close", func(q *fundingSourceReaderTestQuery, table string) { q.closeFailure = table }},
		} {
			t.Run(relation+"/"+failure.name, func(t *testing.T) {
				query := fundingSourceReaderQueryWithRows(relation, 1)
				failure.set(query, relation)
				facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
				if err != ErrFundingSourceFactsExecution || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("lifecycle failure returned partial or private result: %+v %v", facts.LocalSummary(), err)
				}
			})
		}
	}
}

func TestReadFundingSourceFactsEnforcesWholeReadBudgetsWithoutPartialFacts(t *testing.T) {
	for _, count := range []int{fundingSourceReadRowLimit, fundingSourceReadRowLimit + 1} {
		t.Run(fmt.Sprintf("rows-%d", count), func(t *testing.T) {
			query := fundingSourceReaderQueryWithRows("funding_receipts", count)
			facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
			if count == fundingSourceReadRowLimit {
				if err != nil || facts.LocalSummary().Scanned.Receipts != int64(count) {
					t.Fatalf("exact row limit should be accepted: scanned=%+v err=%v", facts.LocalSummary().Scanned, err)
				}
				return
			}
			if err != ErrFundingSourceFactsResource || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
				t.Fatalf("row overflow should fail with zero facts: %+v %v", facts.LocalSummary(), err)
			}
		})
	}
	for _, extra := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("bytes-%d", fundingSourceReadByteLimit+extra), func(t *testing.T) {
			row := fundingSourceReaderFixtureRow("funding_receipts", "r")
			base := 0
			for i, value := range row {
				if i == 3 {
					continue
				}
				base += len(value.(string))
			}
			row[3] = strings.Repeat("x", fundingSourceReadByteLimit+extra-base)
			query := fundingSourceReaderQueryWithRows("funding_receipts", 0)
			query.tables["funding_receipts"] = [][]any{row}
			facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
			if extra <= 0 {
				if err != nil || facts.LocalSummary().Scanned.Receipts != 1 {
					t.Fatalf("at-or-under byte limit should be accepted: %+v %v", facts.LocalSummary(), err)
				}
				return
			}
			if err != ErrFundingSourceFactsResource || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
				t.Fatalf("byte overflow should fail with zero facts: %+v %v", facts.LocalSummary(), err)
			}
		})
	}
}

func TestReadFundingSourceFactsFailsClosedOnExecutionAndInput(t *testing.T) {
	for _, table := range []string{"funding_receipts", "credit_orders", "voucher_redemptions"} {
		query := fundingSourceQueryFixture()
		query.queryFailure = table
		facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
		if err != ErrFundingSourceFactsExecution || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("query failure leaked partial or private results for %s: %+v %v", table, facts.LocalSummary(), err)
		}
	}
	query := fundingSourceQueryFixture()
	query.tables["funding_receipts"] = [][]any{{"z", "t", "sandbox_capture", "s", "1", "CNY", "j"}, {"a", "t", "sandbox_capture", "s", "1", "CNY", "j"}}
	facts, err := ReadFundingSourceFacts(context.Background(), query, "private_schema")
	if err != ErrFundingSourceFactsExecution || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
		t.Fatalf("out-of-order reader rows were accepted: %+v %v", facts.LocalSummary(), err)
	}
	if facts, err = ReadFundingSourceFacts(context.Background(), fundingSourceQueryFixture(), "bad\x00schema"); err != ErrFundingSourceFactsInput || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
		t.Fatalf("invalid schema accepted: %+v %v", facts.LocalSummary(), err)
	}
	query = fundingSourceQueryFixture()
	query.rowFailure = "funding_receipts"
	facts, err = ReadFundingSourceFacts(context.Background(), query, "private_schema")
	if err != ErrFundingSourceFactsExecution || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("rows failure leaked partial or private result: %+v %v", facts.LocalSummary(), err)
	}
	deadline, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	facts, err = ReadFundingSourceFacts(deadline, fundingSourceQueryFixture(), "private_schema")
	if err != ErrFundingSourceFactsTimeout || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
		t.Fatalf("lease deadline was not classified as timeout: %+v %v", facts.LocalSummary(), err)
	}
}

func TestReadFundingSourceFactsObservesCancellationAfterEOFAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	query := fundingSourceQueryFixture()
	query.tables["funding_receipts"] = nil
	query.onRowsClose = func(table string) {
		if table == "funding_receipts" {
			cancel()
		}
	}
	facts, err := ReadFundingSourceFacts(ctx, query, "private_schema")
	if err != ErrFundingSourceFactsCanceled || facts.LocalSummary() != (fundinglotverify.FundingSourceLinksCheck{}) {
		t.Fatalf("cancellation after EOF/Close was not observed atomically: %+v %v", facts.LocalSummary(), err)
	}
}

func TestFundingSourceReadBudgetAcceptsExactAndRejectsExceededLimits(t *testing.T) {
	b := fundingSourceReadBudget{}
	if err := b.add(strings.Repeat("x", fundingSourceReadByteLimit)); err != nil || b.rows != 1 || b.bytes != fundingSourceReadByteLimit {
		t.Fatalf("exact read byte limit should be accepted: %+v %v", b, err)
	}
	b.rows = fundingSourceReadRowLimit
	if err := b.add(""); err != fundingSourceReadResource {
		t.Fatalf("row limit should reject the next fact: %+v %v", b, err)
	}
	b = fundingSourceReadBudget{}
	if err := b.add(strings.Repeat("x", fundingSourceReadByteLimit+1)); err != fundingSourceReadResource {
		t.Fatalf("read byte overflow should fail closed: %+v %v", b, err)
	}
}

func TestReadSandboxCaptureReceiptFactsReadsBothFixedTables(t *testing.T) {
	query := fundingSourceReaderQueryWithRows("funding_receipts", 0)
	query.tables["funding_receipts"] = [][]any{{"r", "tenant", "sandbox_capture", "c", "7", "CNY", "j"}}
	query.tables["payment_captures"] = [][]any{{"c", "tenant", "7", "7", "CNY"}}
	facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if !got.DirectLinksConsistent || got.Scope != "sandbox_capture_receipt_reference_v1" ||
		got.Scanned != (fundinglotverify.FundingSandboxCaptureLinkCounts{Receipts: 1, Captures: 1}) ||
		got.UnvalidatedCaptureGraphs != 1 || got.UnvalidatedLedgerEffects != 1 ||
		query.calls["funding_receipts"] != 1 || query.calls["payment_captures"] != 1 {
		t.Fatalf("fixed direct-link reader did not scan both tables: %+v calls=%+v", got, query.calls)
	}
	wantReceiptSQL := `SELECT id,tenant_id,source_kind,source_id,amount::pg_catalog.text,currency,journal_id FROM "private_schema"."funding_receipts" WHERE NOT $1::pg_catalog.bool OR id COLLATE "C" > $2::pg_catalog.text COLLATE "C" ORDER BY id COLLATE "C" LIMIT 128`
	wantCaptureSQL := `SELECT id,tenant_id,amount::pg_catalog.text,wallet_credit_amount::pg_catalog.text,currency FROM "private_schema"."payment_captures" WHERE NOT $1::pg_catalog.bool OR id COLLATE "C" > $2::pg_catalog.text COLLATE "C" ORDER BY id COLLATE "C" LIMIT 128`
	if query.queries["funding_receipts"][0] != wantReceiptSQL || query.queries["payment_captures"][0] != wantCaptureSQL {
		t.Fatalf("fixed direct-link projection changed: receipts=%q captures=%q", query.queries["funding_receipts"][0], query.queries["payment_captures"][0])
	}
}

func TestReadSandboxCaptureReceiptFactsIncludesEmptyTextIDsOnFirstPage(t *testing.T) {
	query := fundingSourceReaderQueryWithRows("funding_receipts", 0)
	query.tables["funding_receipts"] = [][]any{{"", "tenant", "sandbox_capture", "", "1", "CNY", "journal"}}
	query.tables["payment_captures"] = [][]any{{"", "tenant", "1", "1", "CNY"}}
	facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if got.Scanned != (fundinglotverify.FundingSandboxCaptureLinkCounts{Receipts: 1, Captures: 1}) ||
		got.FundingReceiptShapeMismatches != 1 || got.CaptureShapeMismatches != 1 ||
		got.OrphanSandboxReceipts != 0 || got.CapturesMissingReceipts != 0 {
		t.Fatalf("empty TEXT primary keys were skipped or treated as pagination cursors: %+v", got)
	}
}

func TestReadSandboxCaptureReceiptFactsPaginationBoundariesForBothTables(t *testing.T) {
	for _, relation := range []string{"funding_receipts", "payment_captures"} {
		for _, count := range []int{0, 1, 127, 128, 129} {
			t.Run(fmt.Sprintf("%s/%d", relation, count), func(t *testing.T) {
				query := fundingSourceReaderQueryWithRows(relation, count)
				facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
				if err != nil {
					t.Fatal(err)
				}
				got := facts.LocalSummary().Scanned
				want := fundinglotverify.FundingSandboxCaptureLinkCounts{}
				if relation == "funding_receipts" {
					want.Receipts = int64(count)
				} else {
					want.Captures = int64(count)
				}
				calls := 1
				if count >= fundingSourcePageSize {
					calls = 2
				}
				if got != want || query.calls[relation] != calls {
					t.Fatalf("incomplete fixed read: got=%+v want=%+v calls=%d want_calls=%d", got, want, query.calls[relation], calls)
				}
			})
		}
	}
}

func TestReadSandboxCaptureReceiptFactsClassifiesBothRowsLifecycles(t *testing.T) {
	for _, relation := range []string{"funding_receipts", "payment_captures"} {
		for _, failure := range []struct {
			name string
			set  func(*fundingSourceReaderTestQuery, string)
		}{
			{"query", func(q *fundingSourceReaderTestQuery, table string) { q.queryFailure = table }},
			{"next", func(q *fundingSourceReaderTestQuery, table string) { q.nextFailure = table }},
			{"scan", func(q *fundingSourceReaderTestQuery, table string) { q.scanFailure = table }},
			{"err", func(q *fundingSourceReaderTestQuery, table string) { q.rowFailure = table }},
			{"close", func(q *fundingSourceReaderTestQuery, table string) { q.closeFailure = table }},
		} {
			t.Run(relation+"/"+failure.name, func(t *testing.T) {
				query := fundingSourceReaderQueryWithRows(relation, 1)
				failure.set(query, relation)
				facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
				if err != ErrFundingSourceFactsExecution || facts.LocalSummary() != (fundinglotverify.FundingSandboxCaptureLinkCheck{}) || strings.Contains(err.Error(), "secret") {
					t.Fatalf("lifecycle error returned partial or private facts: %+v %v", facts.LocalSummary(), err)
				}
			})
		}
	}
}

func TestReadSandboxCaptureReceiptFactsEnforcesCombinedRowAndByteBudgets(t *testing.T) {
	for _, count := range []int{fundingSourceReadRowLimit - 1, fundingSourceReadRowLimit} {
		t.Run(fmt.Sprintf("rows-%d-plus-capture", count), func(t *testing.T) {
			query := fundingSourceReaderQueryWithRows("funding_receipts", count)
			query.tables["payment_captures"] = [][]any{fundingSourceReaderFixtureRow("payment_captures", "capture-extra")}
			facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
			if count == fundingSourceReadRowLimit-1 {
				if err != nil || facts.LocalSummary().Scanned != (fundinglotverify.FundingSandboxCaptureLinkCounts{Receipts: int64(count), Captures: 1}) {
					t.Fatalf("combined exact row budget should be accepted: %+v %v", facts.LocalSummary(), err)
				}
				return
			}
			if err != ErrFundingSourceFactsResource || facts.LocalSummary() != (fundinglotverify.FundingSandboxCaptureLinkCheck{}) {
				t.Fatalf("combined row overflow should fail atomically: %+v %v", facts.LocalSummary(), err)
			}
		})
	}
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("bytes-extra-%d", extra), func(t *testing.T) {
			query := fundingSourceReaderQueryWithRows("funding_receipts", 0)
			receipt := []any{"receipt", "tenant", "manual_credit", "source", "1", "CNY", "journal"}
			capture := fundingSourceReaderFixtureRow("payment_captures", "id")
			usedWithoutCaptureID := 0
			for _, value := range receipt {
				usedWithoutCaptureID += len(value.(string))
			}
			for i, value := range capture {
				if i != 0 {
					usedWithoutCaptureID += len(value.(string))
				}
			}
			capture[0] = strings.Repeat("c", fundingSourceReadByteLimit+extra-usedWithoutCaptureID)
			query.tables["funding_receipts"] = [][]any{receipt}
			query.tables["payment_captures"] = [][]any{capture}
			facts, err := ReadSandboxCaptureReceiptFacts(context.Background(), query, "private_schema")
			if extra == 0 {
				if err != nil || facts.LocalSummary().Scanned != (fundinglotverify.FundingSandboxCaptureLinkCounts{Receipts: 1, Captures: 1}) {
					t.Fatalf("combined exact byte budget should be accepted: %+v %v", facts.LocalSummary(), err)
				}
				return
			}
			if err != ErrFundingSourceFactsResource || facts.LocalSummary() != (fundinglotverify.FundingSandboxCaptureLinkCheck{}) {
				t.Fatalf("combined byte overflow should fail atomically: %+v %v", facts.LocalSummary(), err)
			}
		})
	}
}

func TestReadSandboxCaptureReceiptFactsObservesCancellationAfterEOFAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	query := fundingSourceReaderQueryWithRows("funding_receipts", 0)
	query.onRowsClose = func(table string) {
		if table == "funding_receipts" {
			cancel()
		}
	}
	facts, err := ReadSandboxCaptureReceiptFacts(ctx, query, "private_schema")
	if err != ErrFundingSourceFactsCanceled || facts.LocalSummary() != (fundinglotverify.FundingSandboxCaptureLinkCheck{}) {
		t.Fatalf("EOF/Close cancellation returned partial facts: %+v %v", facts.LocalSummary(), err)
	}
}
