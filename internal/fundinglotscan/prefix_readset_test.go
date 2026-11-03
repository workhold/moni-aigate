package fundinglotscan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

type prefixReadsetProfile struct {
	Tables []walletReadsetTable `json:"tables"`
	Counts struct {
		Tables             int `json:"tables"`
		Columns            int `json:"columns"`
		ColumnsWithHistory int `json:"columns_with_history"`
	} `json:"counts"`
}
type prefixReadsetContract struct {
	FormatVersion int                  `json:"format_version"`
	Source        prefixReadsetProfile `json:"source"`
	Wallet        prefixReadsetProfile `json:"wallet"`
	History       walletReadsetTable   `json:"history"`
	SourceSHA256  map[string]string    `json:"source_sha256"`
}

func loadPrefixReadsetContract(t *testing.T) prefixReadsetContract {
	t.Helper()
	raw, err := os.ReadFile("../../specs/023-periodic-products/b-prefix-readset.json")
	if err != nil {
		t.Fatal(err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var c prefixReadsetContract
	if err = d.Decode(&c); err != nil {
		t.Fatal(err)
	}
	if d.Decode(new(any)) != io.EOF || c.FormatVersion != 1 {
		t.Fatal("prefix readset format/trailing data")
	}
	if c.History.Name != "schema_migrations" || !slices.Equal(c.History.Columns, []string{"name", "checksum"}) {
		t.Fatal("history must remain separate two-column read")
	}
	return c
}

func TestPrefixReadsetMatchesFrozenBothDirections(t *testing.T) {
	c := loadPrefixReadsetContract(t)
	for _, v := range []struct {
		name            string
		profile         prefixReadsetProfile
		wallet          bool
		tables, columns int
	}{
		{"source", c.Source, false, 71, 635}, {"wallet", c.Wallet, true, 87, 804},
	} {
		t.Run(v.name, func(t *testing.T) {
			if v.profile.Counts.Tables != v.tables || v.profile.Counts.Columns != v.columns || v.profile.Counts.ColumnsWithHistory != v.columns+2 || len(v.profile.Tables) != v.tables {
				t.Fatal("frozen profile counts changed")
			}
			actual, _, err := prefixRegistry(v.wallet)
			if err != nil {
				t.Fatal(err)
			}
			reviewed := map[string][]string{}
			columns, prev := 0, ""
			for _, table := range v.profile.Tables {
				if table.Name <= prev || len(table.Columns) == 0 || table.Name == c.History.Name {
					t.Fatal("tables must be sorted, unique, and exclude history")
				}
				prev = table.Name
				pc := ""
				for _, column := range table.Columns {
					if column <= pc {
						t.Fatal("columns must be sorted unique")
					}
					pc = column
					columns++
				}
				reviewed[table.Name] = table.Columns
			}
			if columns != v.columns || len(actual) != v.tables {
				t.Fatal("actual fixed dimensions changed")
			}
			for _, table := range actual {
				var names []string
				for _, col := range table.spec.columns {
					names = append(names, col.name)
				}
				slices.Sort(names)
				if !reflect.DeepEqual(names, reviewed[table.spec.name]) {
					t.Fatalf("unreviewed/omitted columns in %s", table.spec.name)
				}
				delete(reviewed, table.spec.name)
			}
			if len(reviewed) != 0 {
				t.Fatal("reviewed tables no longer scanned")
			}
		})
	}
}

func TestPrefixReadsetPinsExactProductionSet(t *testing.T) {
	c := loadPrefixReadsetContract(t)
	sources := []string{
		"internal/fundinglotscan/prefix_checks.go",
		"internal/fundinglotscan/prefix_projection.go",
		"internal/fundinglotscan/prefix_qualification.go",
		"internal/fundinglotscan/prefix_reader.go",
		"internal/fundinglotscan/prefix_types.go",
		"internal/fundinglotscan/projection_prefix.go",
		"internal/periodicbudgetprefix/check.go",
		"internal/periodicbudgetprefix/snapshot.go",
		"internal/periodicbudgetprefix/types.go",
		"internal/periodicbudgetprefix/values.go",
	}
	if len(c.SourceSHA256) != len(sources) {
		t.Fatal("prefix fixed source set differs")
	}
	for _, source := range sources {
		want, ok := c.SourceSHA256[source]
		decoded, err := hex.DecodeString(want)
		if !ok || err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != want {
			t.Fatalf("missing/noncanonical pin: %s", source)
		}
		raw, err := os.ReadFile(filepath.Join("../..", source))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(raw)
		if hex.EncodeToString(got[:]) != want {
			t.Errorf("prefix source pin changed: %s", source)
		}
	}
}
