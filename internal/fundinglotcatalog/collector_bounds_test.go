package fundinglotcatalog

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type limitCatalogRows struct {
	pgx.Rows
	remaining, scans int
	closed           bool
}

func (r *limitCatalogRows) Next() bool {
	if r.remaining == 0 {
		return false
	}
	r.remaining--
	return true
}
func (r *limitCatalogRows) Scan(dst ...any) error {
	r.scans++
	*dst[0].(*[]byte) = []byte(`{"value":1}`)
	return nil
}
func (r *limitCatalogRows) Close()     { r.closed = true }
func (r *limitCatalogRows) Err() error { return nil }

type limitCatalogTx struct {
	pgx.Tx
	rows *limitCatalogRows
	sql  string
}

func (tx *limitCatalogTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	tx.sql = sql
	return tx.rows, nil
}

func TestCatalogStaticCategoryBoundsRejectBeforeOverflowScan(t *testing.T) {
	for _, limit := range []int{0, 1, 3} {
		rows := &limitCatalogRows{remaining: limit + 1}
		tx := &limitCatalogTx{rows: rows}
		if values, e := collectRows(context.Background(), tx, &collectorBudget{}, `SELECT NULL`, limit); values != nil || e != ErrCatalog {
			t.Fatal("category overflow accepted")
		}
		if rows.scans != limit || !rows.closed {
			t.Fatal("overflow row was scanned or rows leaked")
		}
		if !strings.HasSuffix(tx.sql, []string{" LIMIT 1", " LIMIT 2", "", " LIMIT 4"}[limit]) {
			t.Fatal("query did not retain exact static LIMIT+1")
		}
	}
	d, _, e := loadExpected(Pins{Mode: Ordinary})
	if e != nil {
		t.Fatal("embedded bound source unavailable")
	}
	b, e := d.categoryBound("application", "schema", "dependencies")
	if e != nil || b.raw != 6860 || b.normalized != 6848 {
		t.Fatal("dependency multiplicity was replaced by distinct row count")
	}
	b, e = d.categoryBound("application", "schema", "operators")
	if e != nil || b.raw != 0 || b.normalized != 0 {
		t.Fatal("closed empty category gained a row allowance")
	}
	if _, e := d.categoryBound("unknown", "schema", "relations"); e != ErrCatalog {
		t.Fatal("unknown static category accepted")
	}
}

func TestCatalogPGJSONRuntimeBoundsRetainStrictScalarRules(t *testing.T) {
	for _, raw := range []string{`{"value":1e1}`, `{"value":-0}`, `{"value":"\u0000"}`, `{} {}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)} {
		if _, e := decodeCatalogJSON([]byte(raw)); e != ErrCatalog {
			t.Fatalf("runtime record boundary accepted %.80s", raw)
		}
	}
	if _, e := decodeCatalogJSON([]byte(`{"value":[true,null,1.25,"text"]}`)); e != nil {
		t.Fatal("valid PG jsonb representation rejected")
	}
}

// Every object key in the trusted SQL path must be a unique static literal.
// Values never supply JSON text directly: PG's jsonb constructor encodes them.
func TestCatalogStaticJSONBObjectKeysAreUniqueLiterals(t *testing.T) {
	calls := 0
	for _, queries := range [][]catalogQuery{schemaQueries(), roleQueries()} {
		for _, q := range queries {
			marker := "pg_catalog.jsonb_build_object("
			for search := 0; search < len(q.sql); {
				rel := strings.Index(q.sql[search:], marker)
				if rel < 0 {
					break
				}
				start := search + rel + len(marker)
				search = start
				depth, bracket := 1, 0
				quoted := byte(0)
				part := start
				var args []string
				end := -1
				for i := start; i < len(q.sql); i++ {
					c := q.sql[i]
					if quoted != 0 {
						if c == quoted {
							if i+1 < len(q.sql) && q.sql[i+1] == quoted {
								i++
								continue
							}
							quoted = 0
						}
						continue
					}
					switch c {
					case '\'', '"':
						quoted = c
					case '[':
						bracket++
					case ']':
						bracket--
					case '(':
						depth++
					case ')':
						depth--
						if depth == 0 {
							args = append(args, q.sql[part:i])
							end = i
						}
					case ',':
						if depth == 1 && bracket == 0 {
							args = append(args, q.sql[part:i])
							part = i + 1
						}
					}
					if end >= 0 {
						break
					}
				}
				if end < 0 || len(args)%2 != 0 {
					t.Fatalf("invalid static jsonb constructor in %s", q.name)
				}
				seen := map[string]bool{}
				for i := 0; i < len(args); i += 2 {
					k := strings.TrimSpace(args[i])
					if len(k) < 2 || k[0] != '\'' || k[len(k)-1] != '\'' || strings.Contains(k[1:len(k)-1], "'") || seen[k] {
						t.Fatalf("nonliteral/duplicate static jsonb key in %s", q.name)
					}
					seen[k] = true
				}
				calls++
			}
		}
	}
	if calls == 0 {
		t.Fatal("static JSONB registry was not audited")
	}
}
