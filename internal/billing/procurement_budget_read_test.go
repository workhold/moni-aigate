//go:build historical

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

type procurementBudgetTestRows struct {
	values   [][]any
	index    int
	scanned  bool
	rowsErr  error
	closeErr error
	nextErr  error
	scanErr  error
	closed   bool
	onClose  func()
}

func (r *procurementBudgetTestRows) Next() (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.index >= 0 && r.index < len(r.values) && !r.scanned {
		return false, errors.New("fixture row not scanned")
	}
	r.index++
	r.scanned = false
	return r.index < len(r.values), nil
}

func (r *procurementBudgetTestRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) || r.scanned {
		return errors.New("fixture scan shape")
	}
	for i, value := range r.values[r.index] {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("fixture scan target")
		}
		actual := reflect.ValueOf(value)
		if !actual.IsValid() || !actual.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fixture scan type")
		}
		target.Elem().Set(actual)
	}
	r.scanned = true
	return nil
}

func (r *procurementBudgetTestRows) Err() error { return r.rowsErr }
func (r *procurementBudgetTestRows) Close() error {
	r.closed = true
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}

type procurementBudgetTestQuery struct {
	tables       map[string][][]any
	calls        map[string]int
	queries      map[string][]string
	queryFailure string
	rowsFailure  string
	nextFailure  string
	scanFailure  string
	closeFailure string
	onRowsClose  func(string)
}

var procurementBudgetTestTables = []string{"procurement_accounts", "procurement_reservations", "procurement_budget_events"}

func (q *procurementBudgetTestQuery) Query(sql string, args ...any) (fundingSourceRows, error) {
	var table, cursor string
	for _, candidate := range procurementBudgetTestTables {
		if strings.Contains(sql, `"`+candidate+`"`) {
			table = candidate
			break
		}
	}
	switch table {
	case "procurement_accounts", "procurement_budget_events":
		cursor = "id"
	case "procurement_reservations":
		cursor = "attempt_id"
	}
	if table == "" || len(args) != 2 {
		return nil, errors.New("fixture query shape")
	}
	if !strings.Contains(sql, `ORDER BY "`+cursor+`" COLLATE "C" LIMIT 128`) ||
		!strings.Contains(sql, `"`+cursor+`" COLLATE "C" > $2::pg_catalog.text COLLATE "C"`) {
		return nil, errors.New("fixture query order")
	}
	if table == q.queryFailure {
		return nil, errors.New("private query detail")
	}
	hasCursor, ok := args[0].(bool)
	if !ok {
		return nil, errors.New("fixture cursor mode")
	}
	last, ok := args[1].(string)
	if !ok {
		return nil, errors.New("fixture cursor")
	}
	var page [][]any
	for _, row := range q.tables[table] {
		key, ok := row[0].(string)
		if !ok || hasCursor && key <= last {
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
	rows := &procurementBudgetTestRows{values: page, index: -1}
	if q.onRowsClose != nil {
		rows.onClose = func() { q.onRowsClose(table) }
	}
	if table == q.rowsFailure {
		rows.rowsErr = errors.New("private Rows detail")
	}
	if table == q.nextFailure {
		rows.nextErr = errors.New("private Next detail")
	}
	if table == q.scanFailure {
		rows.scanErr = errors.New("private Scan detail")
	}
	if table == q.closeFailure {
		rows.closeErr = errors.New("private Close detail")
	}
	return rows, nil
}

func newProcurementBudgetTestQuery() *procurementBudgetTestQuery {
	return &procurementBudgetTestQuery{
		tables: map[string][][]any{
			"procurement_accounts":     {{"account-1", "2", "3"}},
			"procurement_reservations": {{"attempt-1", "account-1", "5", "2", "3", "disputed", true}},
			"procurement_budget_events": {
				{"event-1", "reserve:attempt-1", "account-1", "attempt-1", "5", "-2"},
				{"event-2", "settle:attempt-1", "account-1", "attempt-1", "-3", "5"},
			},
		},
		calls: map[string]int{},
	}
}

func TestReadProcurementBudgetFactsUsesFixedProjectionAndCompletePages(t *testing.T) {
	query := newProcurementBudgetTestQuery()
	facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	got := facts.LocalSummary()
	if got.Scope != "procurement_budget_projection_v1" || !got.Balanced ||
		got.Scanned.Accounts != 1 || got.Scanned.Reservations != 1 || got.Scanned.Events != 2 {
		t.Fatalf("unexpected summary: %+v", got)
	}
	for _, table := range procurementBudgetTestTables {
		if query.calls[table] != 1 || len(query.queries[table]) != 1 {
			t.Fatalf("table %s not completely read once: calls=%d", table, query.calls[table])
		}
	}
	if !strings.Contains(query.queries["procurement_accounts"][0], `SELECT id,held_amount::pg_catalog.text,spent_amount::pg_catalog.text FROM "private_schema"."procurement_accounts"`) ||
		!strings.Contains(query.queries["procurement_reservations"][0], `ORDER BY "attempt_id" COLLATE "C" LIMIT 128`) ||
		!strings.Contains(query.queries["procurement_budget_events"][0], `ORDER BY "id" COLLATE "C" LIMIT 128`) {
		t.Fatal("fixed table projection/order changed", query.queries)
	}
	for _, result := range []ProcurementBudgetFacts{facts, {}} {
		encoded, err := result.MarshalJSON()
		if err != nil || strings.Contains(string(encoded), "account-1") || !strings.Contains(string(encoded), "redacted") {
			t.Fatal("facts exposed source rows", err, string(encoded))
		}
	}
}

func TestReadProcurementBudgetFactsUsesCOrderedTextCursorsIncludingEmptyFirstCursor(t *testing.T) {
	query := newProcurementBudgetTestQuery()
	query.tables["procurement_accounts"] = [][]any{{"", "0", "0"}}
	query.tables["procurement_reservations"] = nil
	query.tables["procurement_budget_events"] = nil
	facts, err := ReadProcurementBudgetFacts(context.Background(), query, `odd schema`)
	if err != nil || facts.LocalSummary().Scanned.Accounts != 1 {
		t.Fatal("empty-text first cursor failed", err)
	}
	if len(query.queries["procurement_accounts"]) != 1 || !strings.Contains(query.queries["procurement_accounts"][0], `WHERE NOT $1::pg_catalog.bool OR "id" COLLATE "C" > $2::pg_catalog.text COLLATE "C"`) {
		t.Fatal("missing explicit initial cursor predicate", query.queries)
	}
	if !strings.Contains(query.queries["procurement_accounts"][0], `"odd schema"."procurement_accounts"`) {
		t.Fatal("schema identifier was not safely quoted")
	}
}

func TestReadProcurementBudgetFactsContinuesFullPagesAt128(t *testing.T) {
	query := newProcurementBudgetTestQuery()
	query.tables["procurement_accounts"] = make([][]any, fundingSourcePageSize+1)
	for i := range query.tables["procurement_accounts"] {
		query.tables["procurement_accounts"][i] = []any{fmt.Sprintf("account-%03d", i), "0", "0"}
	}
	query.tables["procurement_reservations"] = nil
	query.tables["procurement_budget_events"] = nil
	facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
	if err != nil || facts.LocalSummary().Scanned.Accounts != fundingSourcePageSize+1 {
		t.Fatal("129-row scan failed", err, facts.LocalSummary())
	}
	if len(query.queries["procurement_accounts"]) != 2 || !strings.Contains(query.queries["procurement_accounts"][1], `"id" COLLATE "C" > $2::pg_catalog.text COLLATE "C"`) {
		t.Fatal("full page did not continue with keyset", query.queries)
	}
}

func TestReadProcurementBudgetFactsPaginationBoundariesForEveryRelation(t *testing.T) {
	for _, table := range procurementBudgetTestTables {
		for _, count := range []int{0, 1, 127, 128, 129} {
			t.Run(fmt.Sprintf("%s/%d", table, count), func(t *testing.T) {
				query := newProcurementBudgetTestQuery()
				for _, empty := range procurementBudgetTestTables {
					query.tables[empty] = nil
				}
				for i := 0; i < count; i++ {
					key := fmt.Sprintf("key-%06d", i)
					var row []any
					switch table {
					case "procurement_accounts":
						row = []any{key, "0", "0"}
					case "procurement_reservations":
						row = []any{key, "account", "0", "0", "0", "released", false}
					case "procurement_budget_events":
						row = []any{key, key, "account", "attempt", "0", "0"}
					}
					query.tables[table] = append(query.tables[table], row)
				}
				facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
				if err != nil {
					t.Fatal(err)
				}
				scanned := facts.LocalSummary().Scanned
				var got uint64
				switch table {
				case "procurement_accounts":
					got = scanned.Accounts
				case "procurement_reservations":
					got = scanned.Reservations
				case "procurement_budget_events":
					got = scanned.Events
				}
				calls := 1
				if count >= 128 {
					calls = 2
				}
				if got != uint64(count) || query.calls[table] != calls {
					t.Fatalf("incomplete EOF scan: scanned=%d calls=%d want=%d/%d", got, query.calls[table], count, calls)
				}
			})
		}
	}
}

func TestReadProcurementBudgetFactsReturnsNoPartialFactsOnRowsFailures(t *testing.T) {
	failures := []struct {
		name string
		set  func(*procurementBudgetTestQuery, string)
		want error
	}{
		{"query", func(q *procurementBudgetTestQuery, table string) { q.queryFailure = table }, ErrProcurementBudgetFactsExecution},
		{"next", func(q *procurementBudgetTestQuery, table string) { q.nextFailure = table }, ErrProcurementBudgetFactsExecution},
		{"scan", func(q *procurementBudgetTestQuery, table string) { q.scanFailure = table }, ErrProcurementBudgetFactsExecution},
		{"rows", func(q *procurementBudgetTestQuery, table string) { q.rowsFailure = table }, ErrProcurementBudgetFactsExecution},
		{"close", func(q *procurementBudgetTestQuery, table string) { q.closeFailure = table }, ErrProcurementBudgetFactsExecution},
	}
	for _, failure := range failures {
		for _, table := range procurementBudgetTestTables {
			t.Run(failure.name+"/"+table, func(t *testing.T) {
				query := newProcurementBudgetTestQuery()
				failure.set(query, table)
				facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
				if err != failure.want || facts != (ProcurementBudgetFacts{}) {
					t.Fatalf("failure leaked facts: err=%v facts=%+v", err, facts)
				}
			})
		}
	}
}

func TestReadProcurementBudgetFactsEnforcesSharedResourceBudget(t *testing.T) {
	query := newProcurementBudgetTestQuery()
	query.tables["procurement_accounts"] = make([][]any, fundingSourceReadRowLimit+1)
	for i := range query.tables["procurement_accounts"] {
		query.tables["procurement_accounts"][i] = []any{fmt.Sprintf("a-%06d", i), "0", "0"}
	}
	query.tables["procurement_reservations"] = nil
	query.tables["procurement_budget_events"] = nil
	facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementBudgetFactsResource || facts != (ProcurementBudgetFacts{}) {
		t.Fatalf("row limit did not fail atomically: err=%v facts=%+v", err, facts)
	}

	query = newProcurementBudgetTestQuery()
	query.tables["procurement_accounts"] = [][]any{{"account-1", strings.Repeat("1", fundingSourceReadByteLimit), "0"}}
	query.tables["procurement_reservations"] = nil
	query.tables["procurement_budget_events"] = nil
	facts, err = ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementBudgetFactsResource || facts != (ProcurementBudgetFacts{}) {
		t.Fatalf("byte limit did not fail atomically: err=%v facts=%+v", err, facts)
	}
}

func TestReadProcurementBudgetFactsBudgetSpansTablesAndIncludesNumericText(t *testing.T) {
	query := newProcurementBudgetTestQuery()
	query.tables["procurement_accounts"] = make([][]any, fundingSourceReadRowLimit-2)
	for i := range query.tables["procurement_accounts"] {
		query.tables["procurement_accounts"][i] = []any{fmt.Sprintf("a-%06d", i), "0", "0"}
	}
	// Accounts alone are below the bound; reservation + two events cross it.
	facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementBudgetFactsResource || facts != (ProcurementBudgetFacts{}) {
		t.Fatal("per-table budget reset accepted combined overflow", err, facts.LocalSummary())
	}
	for _, extra := range []int{-1, 0, 1} {
		query = newProcurementBudgetTestQuery()
		query.tables["procurement_reservations"] = nil
		query.tables["procurement_budget_events"] = nil
		query.tables["procurement_accounts"] = [][]any{{"account", strings.Repeat("1", fundingSourceReadByteLimit+extra-len("account")-1), "0"}}
		facts, err = ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
		if extra <= 0 {
			if err != nil || facts.LocalSummary().Scanned.Accounts != 1 || facts.LocalSummary().Balanced {
				t.Fatal("at/under text bound should complete as malformed amount", extra, err)
			}
		} else if err != ErrProcurementBudgetFactsResource || facts != (ProcurementBudgetFacts{}) {
			t.Fatal("numeric cast text exceeded byte bound without atomic failure", err)
		}
	}
}

func TestReadProcurementBudgetFactsValidatesInputsAndCancellation(t *testing.T) {
	if facts, err := ReadProcurementBudgetFacts(context.Background(), nil, "private_schema"); err != ErrProcurementBudgetFactsInput || facts != (ProcurementBudgetFacts{}) {
		t.Fatal("nil query accepted")
	}
	if facts, err := ReadProcurementBudgetFacts(context.Background(), newProcurementBudgetTestQuery(), "pg_catalog"); err != ErrProcurementBudgetFactsInput || facts != (ProcurementBudgetFacts{}) {
		t.Fatal("system schema accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	query := newProcurementBudgetTestQuery()
	query.onRowsClose = func(table string) {
		if table == "procurement_accounts" {
			cancel()
		}
	}
	facts, err := ReadProcurementBudgetFacts(ctx, query, "private_schema")
	if err != ErrProcurementBudgetFactsCanceled || facts != (ProcurementBudgetFacts{}) {
		t.Fatalf("cancellation did not fail atomically: err=%v facts=%+v", err, facts)
	}
	cancel()
	deadline, cancelDeadline := context.WithTimeout(context.Background(), 0)
	defer cancelDeadline()
	facts, err = ReadProcurementBudgetFacts(deadline, newProcurementBudgetTestQuery(), "private_schema")
	if err != ErrProcurementBudgetFactsTimeout || facts != (ProcurementBudgetFacts{}) {
		t.Fatal("deadline returned partial result or wrong category", err)
	}
	if facts, err = ReadProcurementBudgetFacts(nil, newProcurementBudgetTestQuery(), "private_schema"); err != ErrProcurementBudgetFactsInput || facts != (ProcurementBudgetFacts{}) {
		t.Fatal("nil context accepted")
	}
	for _, schema := range []string{"", "information_schema", "pg_private", "bad\x00schema"} {
		if facts, err = ReadProcurementBudgetFacts(context.Background(), newProcurementBudgetTestQuery(), schema); err != ErrProcurementBudgetFactsInput || facts != (ProcurementBudgetFacts{}) {
			t.Fatal("invalid schema accepted", schema, err)
		}
	}
}

func TestReadProcurementBudgetFactsRejectsNonIncreasingRowsWithoutPartialFacts(t *testing.T) {
	for _, key := range []string{"a", "z"} {
		query := newProcurementBudgetTestQuery()
		query.tables["procurement_accounts"] = [][]any{{"z", "0", "0"}, {key, "0", "0"}}
		facts, err := ReadProcurementBudgetFacts(context.Background(), query, "private_schema")
		if err != ErrProcurementBudgetFactsExecution || facts != (ProcurementBudgetFacts{}) {
			t.Fatal("nonincreasing page returned partial result", err, facts.LocalSummary())
		}
	}
}

func TestProcurementBudgetRowsResultDoesNotExposeLocalIdentifiers(t *testing.T) {
	facts := ProcurementBudgetFacts{check: fundinglotverify.ProcurementBudgetProjectionCheck{Scope: "procurement_budget_projection_v1"}}
	encoded, err := facts.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), "procurement_budget_projection_v1") || strings.Contains(string(encoded), "account-") {
		t.Fatal("summary wrapper exposed fields", err, string(encoded))
	}
}
