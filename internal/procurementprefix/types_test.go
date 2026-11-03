package procurementprefix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type layoutColumn struct {
	Name      string  `json:"name"`
	Type      string  `json:"type"`
	Nullable  bool    `json:"nullable"`
	Generated *string `json:"generated_expression"`
}
type layoutTable struct {
	Name    string         `json:"name"`
	Array   string         `json:"array"`
	RowType string         `json:"row_type"`
	Columns []layoutColumn `json:"columns"`
}
type layoutManifest struct {
	Tables      []layoutTable `json:"tables"`
	TableCount  int           `json:"table_count"`
	ColumnCount int           `json:"column_count"`
}

func readLayout(t *testing.T, name string) (layoutManifest, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../specs/023-periodic-products/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var result layoutManifest
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result, raw
}

func TestCompleteDTOColumnMapping(t *testing.T) {
	original, raw := readLayout(t, "b-procurement-original-core-layout-v1.json")
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != "132333daaeb265e1645530816a41fc9ad5205977a8ed8b66f956b6010a2d8e06" {
		t.Fatal("fixed original DTO layout changed without reviewed contract")
	}
	prefix, _ := readLayout(t, "b-procurement-column-layout-v1.json")
	if original.TableCount != 40 || original.ColumnCount != 482 || prefix.TableCount != 32 || prefix.ColumnCount != 715 {
		t.Fatal("wrong independent DTO inventory")
	}
	verify := func(layout layoutManifest, arrays, counts reflect.Type) (int, int) {
		t.Helper()
		if len(layout.Tables) != arrays.NumField() || arrays.NumField() != counts.NumField() {
			t.Fatal("missing physical array/count")
		}
		columns, generated := 0, 0
		for i, table := range layout.Tables {
			array := arrays.Field(i)
			if array.Tag.Get("json") != table.Name || array.Type.Kind() != reflect.Slice || counts.Field(i).Name != array.Name || counts.Field(i).Tag.Get("json") != table.Name || counts.Field(i).Type.Kind() != reflect.Uint64 {
				t.Fatalf("%s array/count mapping differs", table.Name)
			}
			row := array.Type.Elem()
			if table.Array != "" && (array.Name != table.Array || row.Name() != table.RowType) {
				t.Fatalf("%s fixed original array/row type differs", table.Name)
			}
			if row.NumField() != len(table.Columns) {
				t.Fatalf("%s missing columns", table.Name)
			}
			for j, col := range table.Columns {
				f := row.Field(j)
				want := reflect.TypeOf("")
				if col.Type == "TIMESTAMPTZ" {
					want = timeType
				} else if col.Type == "BOOLEAN" {
					want = reflect.TypeOf(false)
				} else if col.Type == "BYTEA" {
					want = reflect.TypeOf([]byte(nil))
				}
				if col.Nullable && want != timeType && want.Kind() != reflect.Slice {
					want = reflect.PointerTo(want)
				}
				if f.Tag.Get("json") != col.Name || f.Type != want {
					t.Fatalf("%s.%s has %s/%s want %s", table.Name, col.Name, f.Tag.Get("json"), f.Type, want)
				}
				columns++
				if col.Generated != nil {
					generated++
				}
			}
		}
		return columns, generated
	}
	if columns, _ := verify(original, reflect.TypeOf(OriginalRows{}), reflect.TypeOf(OriginalScan{})); columns != 482 {
		t.Fatal("original column count differs")
	}
	if columns, generated := verify(prefix, reflect.TypeOf(PrefixRows{}), reflect.TypeOf(PrefixScan{})); columns != 715 || generated != 68 {
		t.Fatal("all generated columns must be present")
	}
}

func TestOriginalLayoutRetainsLegacyReadsetAndAllExtensions(t *testing.T) {
	original, _ := readLayout(t, "b-procurement-original-core-layout-v1.json")
	extensions, _ := readLayout(t, "b-procurement-original-column-layout-v1.json")
	byTable := make(map[string]map[string]layoutColumn, len(original.Tables))
	for _, table := range original.Tables {
		columns := make(map[string]layoutColumn, len(table.Columns))
		for _, column := range table.Columns {
			if _, exists := columns[column.Name]; exists {
				t.Fatalf("duplicate original column %s.%s", table.Name, column.Name)
			}
			columns[column.Name] = column
		}
		byTable[table.Name] = columns
	}
	// The pre-existing source readset is an independent inventory: every column
	// of its forty relevant physical relations must survive the new DTO union.
	raw, err := os.ReadFile("../../specs/023-periodic-products/b-prefix-readset.json")
	if err != nil {
		t.Fatal(err)
	}
	var readset struct {
		Source struct {
			Tables []struct {
				Name    string   `json:"name"`
				Columns []string `json:"columns"`
			} `json:"tables"`
		} `json:"source"`
	}
	if err := json.Unmarshal(raw, &readset); err != nil {
		t.Fatal(err)
	}
	for _, table := range readset.Source.Tables {
		if columns, relevant := byTable[table.Name]; relevant {
			for _, column := range table.Columns {
				if _, retained := columns[column]; !retained {
					t.Fatalf("legacy source column dropped: %s.%s", table.Name, column)
				}
			}
		}
	}
	types := map[string]string{"text": "TEXT", "int2": "SMALLINT", "int8": "BIGINT", "xid8": "XID8", "timestamptz": "TIMESTAMPTZ"}
	count := 0
	for _, table := range extensions.Tables {
		for _, column := range table.Columns {
			got, exists := byTable[table.Name][column.Name]
			if !exists || got.Type != types[column.Type] || got.Nullable != column.Nullable {
				t.Fatalf("original extension lost or altered: %s.%s", table.Name, column.Name)
			}
			count++
		}
	}
	if count != 110 {
		t.Fatal("original extension inventory changed")
	}
}

func TestReportPreservesFrozenShape(t *testing.T) {
	r := reflect.TypeOf(Report{})
	want := []string{"Scope", "Status", "Balanced", "CoverageComplete", "Scanned", "Base", "SourceClosure", "RequestIncoming", "AttemptCost", "AccountPrefix", "Propositions", "Unproven"}
	if r.NumField() != len(want) {
		t.Fatal("report field inventory changed")
	}
	for i, name := range want {
		if r.Field(i).Name != name {
			t.Fatal("report shape changed")
		}
	}
	p := reflect.TypeOf(Propositions{})
	if p.NumField() != 11 || reflect.TypeOf(Gaps{}).NumField() != 14 {
		t.Fatal("missing fixed propositions/gaps")
	}
	for i := 0; i < p.NumField(); i++ {
		if p.Field(i).Type != reflect.TypeOf(Proposition{}) {
			t.Fatal("proposition shape changed")
		}
	}
}
