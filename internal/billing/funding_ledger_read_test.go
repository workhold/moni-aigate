//go:build historical

package billing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fundingLedgerNilTx struct{ pgx.Tx }

func (*fundingLedgerNilTx) Conn() *pgx.Conn { return nil }

func fundingLedgerRequireZero(t *testing.T, got fundingLedgerSnapshot, err, want error) {
	t.Helper()
	if err != want || !reflect.DeepEqual(got, fundingLedgerSnapshot{}) || errors.Unwrap(err) != nil {
		t.Fatal("reader did not return its fixed sentinel and entire zero snapshot")
	}
}

func TestFundingLedgerReadRejectsInputBeforeSQL(t *testing.T) {
	var typedNil *fundingLedgerNilTx
	for _, tx := range []pgx.Tx{nil, typedNil, &fundingLedgerNilTx{}} {
		got, err := readFundingLedgerSnapshotTx(context.Background(), tx)
		fundingLedgerRequireZero(t, got, err, fundingLedgerReadInput)
	}
	got, err := readFundingLedgerSnapshotTx(nil, typedNil)
	fundingLedgerRequireZero(t, got, err, fundingLedgerReadInput)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = readFundingLedgerSnapshotTx(ctx, typedNil)
	fundingLedgerRequireZero(t, got, err, fundingLedgerReadCanceled)
	for i, sentinel := range []error{fundingLedgerReadInput, fundingLedgerReadResource, fundingLedgerReadCanceled, fundingLedgerReadExecution} {
		if sentinel.Error() != "funding_ledger_read_"+[]string{"input", "resource", "canceled", "execution"}[i] {
			t.Fatal("unsafe error shape")
		}
	}
}

func TestFundingLedgerReadBudgetBoundaries(t *testing.T) {
	b := fundingLedgerBudget{rows: 99999, bytes: (16 << 20) - 3}
	if b.add("a", "bc") != nil || b.rows != 100000 || b.bytes != 16<<20 {
		t.Fatal("exact row/byte budget rejected")
	}
	if b.add() != fundingLedgerReadResource {
		t.Fatal("row overflow accepted")
	}
	b = fundingLedgerBudget{bytes: 16 << 20}
	if b.add("x") != fundingLedgerReadResource {
		t.Fatal("byte overflow accepted")
	}
	for _, text := range []string{"bad\x00text", string([]byte{0xff})} {
		if (&fundingLedgerBudget{}).add(text) != fundingLedgerReadExecution {
			t.Fatal("invalid raw text accepted")
		}
	}
}

type fundingLedgerPageRows struct {
	pgx.Rows
	keys   []string
	index  int
	closed bool
	err    error
}

func (r *fundingLedgerPageRows) Next() bool { r.index++; return r.index < len(r.keys) }
func (r *fundingLedgerPageRows) Close()     { r.closed = true }
func (r *fundingLedgerPageRows) Err() error { return r.err }

type fundingLedgerPageTx struct {
	pgx.Tx
	pages    []*fundingLedgerPageRows
	calls    int
	previous *fundingLedgerPageRows
	args     [][]any
}

func (tx *fundingLedgerPageTx) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	if tx.previous != nil && !tx.previous.closed {
		return nil, errors.New("previous rows not closed")
	}
	if tx.calls >= len(tx.pages) {
		return nil, errors.New("unexpected page")
	}
	r := tx.pages[tx.calls]
	tx.args = append(tx.args, append([]any(nil), args...))
	tx.calls++
	tx.previous = r
	return r, nil
}

func TestFundingLedgerReadPaginationRequiresActualEOF(t *testing.T) {
	for _, n := range []int{0, 1, 128, 129, 256, 300} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var keys []string
			for i := range n {
				keys = append(keys, fmt.Sprintf("%04d", i))
			}
			if n > 0 {
				keys[0] = ""
			}
			if n > 1 {
				keys[n-1] = "模型-🦋"
			}
			sort.Strings(keys)
			tx := &fundingLedgerPageTx{}
			for start := 0; ; start += 128 {
				end := min(start+128, n)
				tx.pages = append(tx.pages, &fundingLedgerPageRows{keys: keys[start:end], index: -1})
				if end-start < 128 {
					break
				}
			}
			var got []string
			err := fundingLedgerPages(context.Background(), tx, "static fixture", func(r pgx.Rows) (string, error) {
				p := r.(*fundingLedgerPageRows)
				key := p.keys[p.index]
				got = append(got, key)
				return key, nil
			})
			if err != nil || !reflect.DeepEqual(keys, got) || tx.calls != n/128+1 || !tx.previous.closed {
				t.Fatal("complete pages/empty tail/row closure not preserved")
			}
			for i, args := range tx.args {
				wantCursor := ""
				if i > 0 {
					wantCursor = keys[i*128-1]
				}
				if args[0] != (i == 0) || args[1] != wantCursor {
					t.Fatal("first-page flag or exact byte cursor lost")
				}
			}
		})
	}
}

func TestFundingLedgerReadPaginationRejectsBadOrIncompleteRows(t *testing.T) {
	for _, tc := range []struct {
		keys []string
		err  error
	}{{[]string{"a", "a"}, nil}, {[]string{"b", "a"}, nil}, {[]string{"a"}, errors.New("private driver diagnostic")}} {
		r := &fundingLedgerPageRows{keys: tc.keys, index: -1, err: tc.err}
		tx := &fundingLedgerPageTx{pages: []*fundingLedgerPageRows{r}}
		err := fundingLedgerPages(context.Background(), tx, "static fixture", func(r pgx.Rows) (string, error) {
			p := r.(*fundingLedgerPageRows)
			return p.keys[p.index], nil
		})
		if err != fundingLedgerReadExecution || !r.closed {
			t.Fatal("bad order or Rows.Err accepted/left open")
		}
	}
}

func TestFundingLedgerReadSQLUsesClosedBuiltinRegistry(t *testing.T) {
	for _, table := range []string{"ledger_accounts", "journals", "journal_lines"} {
		q := fundingLedgerPageSQL(`quoted"schema`, table)
		for _, required := range []string{`"quoted""schema".`, `COLLATE pg_catalog."C"`, `OPERATOR(pg_catalog.>)`, `USING OPERATOR(pg_catalog.<)`, `$2::pg_catalog.text`, `LIMIT 128`} {
			if !strings.Contains(q, required) {
				t.Fatal("page SQL lost identifier/collation/operator/cast/limit closure")
			}
		}
		for _, forbidden := range []string{"SUM(", "COUNT(", " JOIN ", "FOR UPDATE", "tenant_id=", "coalesce"} {
			if strings.Contains(q, forbidden) {
				t.Fatal("page SQL filters/aggregates/locks raw facts")
			}
		}
	}
	if fundingLedgerPageSQL("safe", "arbitrary") != "" || !strings.Contains(fundingLedgerHistoryFunctionsSQL, "pg_catalog.current_schemas(true)") || !strings.Contains(fundingLedgerPrimarySQL, "i.indrelid OPERATOR(pg_catalog.=) k.conrelid") {
		t.Fatal("closed table/function/PK registry lost")
	}
}
