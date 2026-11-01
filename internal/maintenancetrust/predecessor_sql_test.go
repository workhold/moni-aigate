package maintenancetrust

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestPredecessorSQLCompleteBoundedProjection(t *testing.T) {
	tables := predecessorTables()
	counts := []int{12, 14, 27, 13, 3, 3, 5, 4}
	for i, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			if len(table.columns) != counts[i] {
				t.Fatal("persistent column inventory changed")
			}
			q := predecessorProjectionSQL("owner_\"quoted", table)
			if !strings.Contains(q, `FROM ONLY "owner_""quoted".`) || strings.Contains(q, " WHERE ") {
				t.Fatal("projection hid unqualified rows or lost identifier qualification")
			}
			if strings.Count(q, "CASE WHEN q.qualified THEN") != len(table.columns) {
				t.Fatal("not every field is qualified before returning")
			}
			if !strings.Contains(q, "COALESCE(") || !strings.HasPrefix(q, "SELECT q.qualified,") {
				t.Fatal("qualification flag missing")
			}
			for _, c := range table.columns {
				if !strings.Contains(q, "s."+pgx.Identifier{c.name}.Sanitize()+" IS NOT NULL") && c.kind != predecessorText {
					t.Fatal("non-null guard missing")
				}
				if c.kind == predecessorText || c.kind == predecessorBytes {
					if !strings.Contains(q, "pg_catalog.octet_length(s."+pgx.Identifier{c.name}.Sanitize()) {
						t.Fatal("server scalar size guard missing")
					}
				}
			}
			if table.keys {
				if !strings.HasSuffix(q, `ORDER BY q."key_id" COLLATE pg_catalog."C" LIMIT 129`) {
					t.Fatal("key C order/sentinel missing")
				}
			} else if !strings.HasSuffix(q, " LIMIT 2") {
				t.Fatal("singleton sentinel missing")
			}
		})
	}
	if strings.Contains(predecessorProjectionSQL("s", tables[7]), "created_at") {
		t.Fatal("receiver key column fabricated")
	}
	if len(predecessorHistoryTables) != 21 || len(predecessorBypasses) != 6 {
		t.Fatal("ordinary predicate inventory changed")
	}
	for _, part := range []string{"child.attnotnull", "f.convalidated", "NOT f.condeferrable", "NOT f.condeferred", "f.conrelid", "f.confrelid", "ARRAY[child.attnum]", "ARRAY[parent.attnum]", "NOT child.attisdropped", "NOT parent.attisdropped"} {
		if !strings.Contains(predecessorQueryConflictsSQL, part) {
			t.Fatal("mandatory FK proof incomplete")
		}
	}
}

// Only a scanner fixture; it cannot enter a production authority or substitute
// a pgx transaction. It tests the exact bytes charged for a safe projection.
type predecessorScanFixture struct {
	pgx.Rows
	values []any
	fail   bool
}

func (f *predecessorScanFixture) Scan(dest ...any) error {
	if f.fail {
		return ErrExecution
	}
	if len(dest) != len(f.values) {
		return ErrExecution
	}
	for i, v := range f.values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(v))
	}
	return nil
}
func TestPredecessorProjectionScanExactResources(t *testing.T) {
	table := predecessorTable{name: "test", columns: []predecessorColumn{{name: "text", kind: predecessorText, min: 1, max: 100}, {name: "data", kind: predecessorBytes, min: 1, max: 100}, {name: "bool", kind: predecessorBool}, {name: "oid", kind: predecessorOID}, {name: "int4", kind: predecessorInt4}, {name: "int8", kind: predecessorInt8}, {name: "time", kind: predecessorTimestamp}}}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	data := []byte{1, 2, 3}
	values := []any{pgtype.Bool{Bool: true, Valid: true}, pgtype.Text{String: "测试", Valid: true}, data, pgtype.Bool{Bool: true, Valid: true}, pgtype.Uint32{Uint32: 4, Valid: true}, pgtype.Int4{Int32: 1, Valid: true}, pgtype.Int8{Int64: 1, Valid: true}, pgtype.Timestamptz{Time: now, Valid: true}}
	budget := predecessorResources{}
	row, err := predecessorScanRow(&predecessorScanFixture{values: values}, table, &budget)
	if err != nil || budget.bytes != 1+6+3+1+4+4+8+8 || budget.rows != 0 {
		t.Fatal("projected scalar byte accounting changed", err, budget)
	}
	data[0] = 9
	if row["data"].data[0] != 1 {
		t.Fatal("returned bytea aliases scanner buffer")
	}
	values[0] = pgtype.Bool{Bool: false, Valid: true}
	budget = predecessorResources{}
	if _, err = predecessorScanRow(&predecessorScanFixture{values: values}, table, &budget); err != ErrIdentity || budget.bytes != 1 {
		t.Fatal("unqualified projection accepted")
	}
	values[0] = pgtype.Bool{Bool: true, Valid: true}
	for i := 1; i < len(values); i++ {
		saved := values[i]
		values[i] = reflect.Zero(reflect.TypeOf(saved)).Interface()
		if _, err = predecessorScanRow(&predecessorScanFixture{values: values}, table, &predecessorResources{}); err == nil {
			t.Fatal("NULL projected scalar accepted")
		}
		values[i] = saved
	}
	if _, err = predecessorScanRow(&predecessorScanFixture{values: values, fail: true}, table, &predecessorResources{}); err != ErrExecution {
		t.Fatal("scanner error accepted")
	}
	if _, err = predecessorScanRow(&predecessorScanFixture{values: values}, table, &predecessorResources{bytes: predecessorByteBudget}); err != ErrExecution {
		t.Fatal("cumulative projected bytes exceeded")
	}
}
