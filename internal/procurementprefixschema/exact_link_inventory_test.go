package procurementprefixschema_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type exactLinkInventorySource struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type exactLinkInventoryKey struct {
	Name            string   `json:"name"`
	Table           string   `json:"table"`
	Kind            string   `json:"kind"`
	Columns         []string `json:"columns"`
	Classification  string   `json:"classification"`
	UncertifiedText []string `json:"text_columns_without_local_width_certificate"`
	ReferencingFKs  []string `json:"referencing_foreign_keys"`
}

type exactLinkInventoryFK struct {
	Name           string   `json:"name"`
	Table          string   `json:"table"`
	Columns        []string `json:"columns"`
	TargetTable    string   `json:"target_table"`
	TargetColumns  []string `json:"target_columns"`
	TargetNewKey   *string  `json:"target_new_constraint"`
	Classification string   `json:"classification"`
}

type exactLinkInventory struct {
	FormatVersion        int                        `json:"format_version"`
	Status               string                     `json:"status"`
	BaselineCommit       string                     `json:"baseline_commit"`
	Sources              []exactLinkInventorySource `json:"sources"`
	NotCovered           []string                   `json:"not_covered"`
	ClassificationNotes  map[string]string          `json:"classification_notes"`
	KeyCount             int                        `json:"key_count"`
	FKCount              int                        `json:"foreign_key_count"`
	ClassificationCounts map[string]int             `json:"key_classification_counts"`
	Keys                 []exactLinkInventoryKey    `json:"keys"`
	ForeignKeys          []exactLinkInventoryFK     `json:"foreign_keys"`
}

// This parses only the fixed relation-material declaration syntax. It neither
// installs SQL nor certifies the width or business authority of any TEXT key.
var exactLinkDeclaration = regexp.MustCompile(`ALTER TABLE \{\{MONI_SCHEMA\}\}\.([a-z_][a-z0-9_]*)\s+ADD CONSTRAINT ([a-z_][a-z0-9_]*)\s+(PRIMARY KEY|UNIQUE|FOREIGN KEY)\s*\(([^)]+)\)(?:\s+REFERENCES \{\{MONI_SCHEMA\}\}\.([a-z_][a-z0-9_]*)\s*\(([^)]+)\))?`)

func exactLinkColumns(t *testing.T, value string) []string {
	t.Helper()
	columns := strings.Split(value, ",")
	for i := range columns {
		columns[i] = strings.TrimSpace(columns[i])
		if columns[i] == "" {
			t.Fatal("empty column in fixed exact-link declaration")
		}
	}
	return columns
}

func TestProcurementExactLinkInventoryMatchesFixedRelations(t *testing.T) {
	raw, err := os.ReadFile("../../specs/023-periodic-products/b-procurement-exact-link-inventory-v1.json")
	must(t, "read exact-link design inventory", err)
	var inventory exactLinkInventory
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	must(t, "decode closed exact-link inventory", decoder.Decode(&inventory))
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("exact-link inventory has trailing data: %v", err)
	}
	if inventory.FormatVersion != 1 || inventory.Status != "design-only-conservative-width-review-inventory" || inventory.BaselineCommit != "cff5bef966c990cfea5d85f0b4ea4ba164ae146e" {
		t.Fatal("frozen exact-link inventory identity changed")
	}
	wantSources := []exactLinkInventorySource{
		{"internal/procurementprefixschema/materials/source-constraints-v1.sql", "4099f2303a39745d3112ca1a4e143bc884c356387a99e32da499396cfe9dda3a"},
		{"internal/procurementprefixschema/materials/account-constraints-v1.sql", "fc990854a869952f54d212bc1e042c8b56991d8feff19827376b04c81b7862bf"},
		{"internal/procurementprefixschema/materials/cost-constraints-v1.sql", "460c7d02c1e6dd791118cb2d5e1327a6efb39f74a969a46995fb2e1932f7e2ae"},
		{"internal/procurementprefixschema/materials/original-constraints-v1.sql", "66c5d54de859c70873f426220933691c31740e5e63e9c8833ea21964f410f01b"},
	}
	if !reflect.DeepEqual(inventory.Sources, wantSources) {
		t.Fatal("exact-link inventory sources or frozen SHA-256 changed")
	}
	var parsedKeys []exactLinkInventoryKey
	var parsedFKs []exactLinkInventoryFK
	seenNames := make(map[string]bool)
	for _, source := range wantSources {
		body, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(source.Path)))
		must(t, "read fixed exact-link SQL source", err)
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != source.SHA256 {
			t.Fatalf("exact-link SQL source SHA-256 differs: %s", source.Path)
		}
		for _, declaration := range exactLinkDeclaration.FindAllStringSubmatch(string(body), -1) {
			table, name, kind := declaration[1], declaration[2], declaration[3]
			if seenNames[name] {
				t.Fatalf("duplicate constraint declaration: %s", name)
			}
			seenNames[name] = true
			columns := exactLinkColumns(t, declaration[4])
			if kind == "FOREIGN KEY" {
				if declaration[5] == "" || declaration[6] == "" {
					t.Fatalf("FK declaration missing exact target: %s", name)
				}
				parsedFKs = append(parsedFKs, exactLinkInventoryFK{
					Name: name, Table: table, Columns: columns,
					TargetTable: declaration[5], TargetColumns: exactLinkColumns(t, declaration[6]),
				})
				continue
			}
			keyKind := "unique"
			if kind == "PRIMARY KEY" {
				keyKind = "primary_key"
			}
			parsedKeys = append(parsedKeys, exactLinkInventoryKey{Name: name, Table: table, Kind: keyKind, Columns: columns})
		}
	}
	if inventory.KeyCount != 268 || len(inventory.Keys) != 268 || len(parsedKeys) != 268 || inventory.FKCount != 291 || len(inventory.ForeignKeys) != 291 || len(parsedFKs) != 291 {
		t.Fatalf("fixed relation inventory count differs: JSON %d/%d, SQL %d/%d", len(inventory.Keys), len(inventory.ForeignKeys), len(parsedKeys), len(parsedFKs))
	}
	keysByName := make(map[string]exactLinkInventoryKey)
	keysByTarget := make(map[string]string)
	targetIdentity := func(table string, columns []string) string { return table + "/" + strings.Join(columns, ",") }
	counts := make(map[string]int)
	for i, key := range inventory.Keys {
		actual := parsedKeys[i]
		if key.Name != actual.Name || key.Table != actual.Table || key.Kind != actual.Kind || !slices.Equal(key.Columns, actual.Columns) {
			t.Fatalf("ordered key declaration differs at %d: %s", i, key.Name)
		}
		if key.Classification != "single_column_no_new_compound_width" && key.Classification != "fixed_envelope_reviewable" && key.Classification != "width_review_required" {
			t.Fatalf("undeclared key review classification: %s", key.Name)
		}
		for _, column := range key.UncertifiedText {
			if !slices.Contains(key.Columns, column) {
				t.Fatalf("review column outside key declaration: %s.%s", key.Name, column)
			}
		}
		counts[key.Classification]++
		keysByName[key.Name] = key
		target := targetIdentity(key.Table, key.Columns)
		if previous := keysByTarget[target]; previous != "" {
			t.Fatalf("ambiguous ordered new target key: %s/%s", previous, key.Name)
		}
		keysByTarget[target] = key.Name
	}
	wantCounts := map[string]int{"single_column_no_new_compound_width": 65, "fixed_envelope_reviewable": 37, "width_review_required": 166}
	if !reflect.DeepEqual(inventory.ClassificationCounts, wantCounts) || !reflect.DeepEqual(counts, wantCounts) {
		t.Fatal("frozen conservative review counts changed; these counts are not width certificates")
	}
	wantNotes := []string{"single_column_no_new_compound_width", "fixed_envelope_reviewable", "width_review_required", "existing_original_or_bp1_key_preserved"}
	if len(inventory.ClassificationNotes) != len(wantNotes) {
		t.Fatal("declared review classification notes changed")
	}
	for _, name := range wantNotes {
		if strings.TrimSpace(inventory.ClassificationNotes[name]) == "" {
			t.Fatalf("missing review classification note: %s", name)
		}
	}
	wantNotCovered := []string{"byte_width_certificate", "replacement_ddl", "trigger_or_guard_authority", "business_acceptance", "final_reader_basis_catalog_profile"}
	if !slices.Equal(inventory.NotCovered, wantNotCovered) {
		t.Fatal("design inventory must preserve its explicit unverified scope")
	}
	reverse := make(map[string][]string)
	for i, fk := range inventory.ForeignKeys {
		actual := parsedFKs[i]
		if fk.Name != actual.Name || fk.Table != actual.Table || !slices.Equal(fk.Columns, actual.Columns) || fk.TargetTable != actual.TargetTable || !slices.Equal(fk.TargetColumns, actual.TargetColumns) {
			t.Fatalf("ordered FK declaration differs at %d: %s", i, fk.Name)
		}
		if len(fk.Columns) != len(fk.TargetColumns) {
			t.Fatalf("FK source/target arity differs: %s", fk.Name)
		}
		matchedKey := keysByTarget[targetIdentity(fk.TargetTable, fk.TargetColumns)]
		if fk.TargetNewKey == nil {
			if matchedKey != "" || fk.Classification != "existing_original_or_bp1_key_preserved" {
				t.Fatalf("FK incorrectly omits its new target key: %s", fk.Name)
			}
			continue
		}
		if matchedKey == "" || *fk.TargetNewKey != matchedKey {
			t.Fatalf("FK names the wrong exact target key: %s", fk.Name)
		}
		if fk.Classification != keysByName[matchedKey].Classification {
			t.Fatalf("FK review class differs from its target key: %s", fk.Name)
		}
		reverse[matchedKey] = append(reverse[matchedKey], fk.Name)
	}
	for _, key := range inventory.Keys {
		if !slices.Equal(key.ReferencingFKs, reverse[key.Name]) {
			t.Fatalf("ordered key reverse-FK inventory differs: %s", key.Name)
		}
	}
}
