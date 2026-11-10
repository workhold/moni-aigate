//go:build historical

package billing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// The old pagination tests continue to exercise the one shared paging loop.
func fundingLedgerPages(ctx context.Context, tx pgx.Tx, sql string, scan func(pgx.Rows) (string, error)) error {
	return fundingLedgerReadPages(fundingLedgerRawQuery{ctx: ctx, tx: tx}, sql, func(rows fundingLedgerRows) (string, error) {
		return scan(rows.(*fundingLedgerRawRows).rows)
	})
}

type fundingLedgerScriptRows struct {
	values                          [][]any
	index                           int
	scanErr, nextErr, err, closeErr error
	sawErr, closed                  bool
}

func (r *fundingLedgerScriptRows) Next() (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	r.index++
	return r.index < len(r.values), nil
}
func (r *fundingLedgerScriptRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) {
		return fundingLedgerReadExecution
	}
	for i, value := range r.values[r.index] {
		target := reflect.ValueOf(dest[i]).Elem()
		if value == nil {
			target.SetZero()
			continue
		}
		v := reflect.ValueOf(value)
		if !v.Type().AssignableTo(target.Type()) {
			return fundingLedgerReadExecution
		}
		target.Set(v)
	}
	return nil
}
func (r *fundingLedgerScriptRows) Err() error   { r.sawErr = true; return r.err }
func (r *fundingLedgerScriptRows) Close() error { r.closed = true; return r.closeErr }

type fundingLedgerScriptPage struct {
	table string
	rows  *fundingLedgerScriptRows
}
type fundingLedgerScriptQuery struct {
	pages []fundingLedgerScriptPage
	calls int
	args  [][]any
}

func (q *fundingLedgerScriptQuery) Query(sql string, args ...any) (fundingLedgerRows, error) {
	if q.calls >= len(q.pages) {
		return nil, fundingLedgerReadExecution
	}
	if q.calls > 0 {
		prior := q.pages[q.calls-1].rows
		if !prior.closed || !prior.sawErr {
			return nil, fundingLedgerReadExecution
		}
	}
	p := q.pages[q.calls]
	if !strings.Contains(sql, "\"schema\".\""+p.table+"\"") {
		return nil, fundingLedgerReadExecution
	}
	q.calls++
	q.args = append(q.args, append([]any(nil), args...))
	return p.rows, nil
}
func fundingLedgerScript(table string, values ...[]any) fundingLedgerScriptPage {
	return fundingLedgerScriptPage{table: table, rows: &fundingLedgerScriptRows{values: values, index: -1}}
}
func fundingLedgerCoreFixture() *fundingLedgerScriptQuery {
	tenant := "tenant"
	return &fundingLedgerScriptQuery{pages: []fundingLedgerScriptPage{
		fundingLedgerScript("ledger_accounts", []any{"a", &tenant, "AVAILABLE", "100"}, []any{"b", nil, "FUNDING_CLEARING", "-100"}),
		fundingLedgerScript("journals", []any{"j", "tenant", "op", "credit", "ref", "actor", "CNY", "1", time.Date(2020, 1, 2, 3, 4, 5, 6000, time.FixedZone("offset", 3600))}),
		fundingLedgerScript("journal_lines", []any{"l1", "j", "a", "100"}, []any{"l2", "j", "b", "-100"}),
	}}
}
func TestFundingLedgerMaintenanceSharedCorePreservesFactsAndArithmetic(t *testing.T) {
	q := fundingLedgerCoreFixture()
	got, err := readFundingLedgerCore(context.Background(), q, "schema")
	if err != nil || !got.check.RowsConsistent || got.check.Scope != "supplied_ledger_rows" || got.check.Scanned != (fundinglotverify.LedgerRowCounts{Accounts: 2, Journals: 1, Lines: 2}) || q.calls != 3 {
		t.Fatal("shared core lost full local ledger input")
	}
	j := got.journals[0]
	if j.Kind != "credit" || j.Reference != "ref" || j.Actor != "actor" || j.CreatedTXID != "1" || j.CreatedAt.Location() != time.UTC || j.CreatedAt.Hour() != 2 || j.ID != got.rows.Journals[0].ID {
		t.Fatal("shared core lost same-row journal business or lifecycle columns")
	}
	for _, p := range q.pages {
		if !p.rows.closed || !p.rows.sawErr {
			t.Fatal("shared core omitted rows Err/Close")
		}
	}
	for _, a := range q.args {
		if len(a) != 2 || a[0] != true || a[1] != "" {
			t.Fatal("first page changed original empty-byte cursor")
		}
	}
}
func TestFundingLedgerMaintenanceSharedCoreKeepsMismatchAsLocalResult(t *testing.T) {
	q := fundingLedgerCoreFixture()
	q.pages[2].rows.values[1][3] = "-99"
	got, err := readFundingLedgerCore(context.Background(), q, "schema")
	if err != nil || got.check.RowsConsistent || got.check.Mismatches.UnbalancedJournals != 1 || q.calls != 3 {
		t.Fatal("known data mismatch changed into partial/error or through result")
	}
}
func TestFundingLedgerMaintenanceSharedCoreErrorsReturnEntireZero(t *testing.T) {
	for _, mode := range []string{"scan", "next", "err", "close", "order", "xid", "time", "bad-amount"} {
		t.Run(mode, func(t *testing.T) {
			q := fundingLedgerCoreFixture()
			switch mode {
			case "scan":
				q.pages[0].rows.scanErr = fundingLedgerReadExecution
			case "next":
				q.pages[0].rows.nextErr = errors.New("test-next-opaque")
			case "err":
				q.pages[0].rows.err = fundingLedgerReadCanceled
			case "close":
				q.pages[0].rows.closeErr = errors.New("test-close-opaque")
			case "order":
				q.pages[0].rows.values[1][0] = "a"
			case "xid":
				q.pages[1].rows.values[0][7] = "01"
			case "time":
				q.pages[1].rows.values[0][8] = time.Time{}
			case "bad-amount":
				q.pages[2].rows.values[0][3] = "1.0"
			}
			got, err := readFundingLedgerCore(context.Background(), q, "schema")
			if err == nil || !reflect.DeepEqual(got, fundingLedgerSnapshot{}) {
				t.Fatal("shared core returned partial facts after failure")
			}
			if mode == "err" || mode == "close" {
				if !q.pages[0].rows.sawErr || !q.pages[0].rows.closed {
					t.Fatal("failed page did not observe both Err and Close")
				}
			}
		})
	}
}
func TestFundingLedgerMaintenanceReadPagesObservesBothTerminalMethods(t *testing.T) {
	for _, mode := range []string{"healthy", "err", "close"} {
		rows := &fundingLedgerScriptRows{index: -1}
		if mode == "err" {
			rows.err = errors.New("test-rows-opaque")
		}
		if mode == "close" {
			rows.closeErr = errors.New("test-close-opaque")
		}
		q := &fundingLedgerScriptQuery{pages: []fundingLedgerScriptPage{{table: "ledger_accounts", rows: rows}}}
		err := fundingLedgerReadPages(q, fundingLedgerPageSQL("schema", "ledger_accounts"), func(fundingLedgerRows) (string, error) { t.Fatal("unexpected row"); return "", nil })
		if (err == nil) != (mode == "healthy") || !rows.sawErr || !rows.closed {
			t.Fatal("lease Err/Close requirement lost")
		}
	}
}

func TestLedgerFactsRemainDescriptiveAndRedacted(t *testing.T) {
	q := fundingLedgerCoreFixture()
	facts, err := ReadLedgerFacts(context.Background(), q, "schema")
	if err != nil || facts.LocalSummary().Scope != "supplied_ledger_rows" || !facts.LocalSummary().RowsConsistent {
		t.Fatal("fixed local facts algorithm changed")
	}
	if len(facts.snapshot.journals) != 1 || facts.snapshot.journals[0].Actor != "actor" || facts.snapshot.journals[0].Reference != "ref" {
		t.Fatal("complete private journal facts discarded")
	}
	raw, err := facts.MarshalJSON()
	if err != nil || string(raw) != `{"ledger_facts":"redacted"}` || facts.String() != "ledger_facts_redacted" || facts.GoString() != "ledger_facts_redacted" {
		t.Fatal("raw facts formatting leaked")
	}
}

func TestLedgerFactsFailureReturnsZeroAndPreservesOpaqueError(t *testing.T) {
	fault := errors.New("opaque-query-failure")
	for _, stage := range []string{"next", "err", "close"} {
		t.Run(stage, func(t *testing.T) {
			q := fundingLedgerCoreFixture()
			switch stage {
			case "next":
				q.pages[0].rows.nextErr = fault
			case "err":
				q.pages[0].rows.err = fault
			case "close":
				q.pages[0].rows.closeErr = fault
			}
			facts, err := ReadLedgerFacts(context.Background(), q, "schema")
			if !errors.Is(err, fault) || !reflect.DeepEqual(facts, LedgerFacts{}) {
				t.Fatal("partial facts or replaced error")
			}
		})
	}
	for _, schema := range []string{"", "pg_catalog", "information_schema", "pg_temp_1", string([]byte{'s', 0})} {
		q := fundingLedgerCoreFixture()
		facts, err := ReadLedgerFacts(context.Background(), q, schema)
		if err != ErrLedgerFactsInput || !reflect.DeepEqual(facts, LedgerFacts{}) || q.calls != 0 {
			t.Fatal("invalid schema queried")
		}
	}
	if facts, err := ReadLedgerFacts(context.Background(), nil, "schema"); err != ErrLedgerFactsInput || !reflect.DeepEqual(facts, LedgerFacts{}) {
		t.Fatal("nil query accepted")
	}
}
