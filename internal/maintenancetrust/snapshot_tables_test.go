package maintenancetrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const initTablesSHA = "6fd978751fe24d37fa29da679be0976e12659e8a8f64d3983050280fffc00b19"

func TestInitLockTablesFrozenSources(t *testing.T) {
	if err := checkFrozenInitTables(initLockTables[:]); err != nil {
		t.Fatal(err)
	}

	// Authenticate both complete roots before extracting their six application
	// inventories. Peer heaps, indexes, TOAST and composite relations stay out.
	roots := []struct {
		mode, sha string
		variants  []string
	}{
		{"ordinary", "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2", []string{"named-isolated", "named-minimal-login", "named-image-login", "public-minimal-login", "public-image-login"}},
		{"sandbox", "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f", []string{"full-sandbox"}},
	}
	checked := 0
	for _, root := range roots {
		raw := readInitTableSource(t, "../fundinglotcatalog/expected/"+root.mode+"-v1.json")
		assertInitSourceSHA(t, root.mode, raw, root.sha)
		var document struct {
			Version  int    `json:"format_version"`
			Major    int    `json:"postgres_major"`
			Mode     string `json:"mode"`
			Variants []struct {
				Name   string                     `json:"name"`
				Schema map[string]json.RawMessage `json:"schema"`
			} `json:"variants"`
		}
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatal(err)
		}
		if document.Version != 1 || document.Major != 16 || document.Mode != root.mode || len(document.Variants) != len(root.variants) {
			t.Fatalf("unexpected %s root profile", root.mode)
		}
		for i, variant := range document.Variants {
			if variant.Name != root.variants[i] {
				t.Fatalf("unexpected %s variant %d: %q", root.mode, i, variant.Name)
			}
			var application struct {
				Relations []struct {
					Identity  struct{ Class, Scope, Name string } `json:"identity"`
					Kind      string                              `json:"kind"`
					Inherits  []json.RawMessage                   `json:"inherits"`
					Partition bool                                `json:"partition"`
				} `json:"relations"`
			}
			if err := json.Unmarshal(variant.Schema["application"], &application); err != nil {
				t.Fatal(err)
			}
			var tables []string
			for _, relation := range application.Relations {
				if relation.Kind != "r" {
					continue
				}
				if relation.Identity.Class != "relation" || relation.Identity.Scope != "application" || relation.Partition || len(relation.Inherits) != 0 {
					t.Fatalf("%s has an unexpected heap identity or hierarchy", variant.Name)
				}
				tables = append(tables, relation.Identity.Name)
			}
			slices.Sort(tables)
			if err := checkFrozenInitTables(tables); err != nil || !slices.Equal(tables, initLockTables[:]) {
				t.Fatalf("%s application lock set differs: %v", variant.Name, err)
			}
			checked++
		}
	}
	if checked != 6 {
		t.Fatal("not all six application profiles were checked")
	}

	// Independently recover the original 109-table descriptor from its frozen
	// canonical bytes, not from either new expected root or the lock array.
	basisDoc := string(readInitTableSource(t, "../../specs/022-self-service-payments/c0-maintenance-contract.md"))
	_, basisDoc, found := strings.Cut(basisDoc, "<!-- C0_BASIS_DESCRIPTOR_BEGIN -->")
	if !found {
		t.Fatal("missing basis descriptor start")
	}
	basisDoc, _, found = strings.Cut(basisDoc, "<!-- C0_BASIS_DESCRIPTOR_END -->")
	if !found {
		t.Fatal("missing basis descriptor end")
	}
	_, basisDoc, found = strings.Cut(basisDoc, "```json\n")
	if !found {
		t.Fatal("missing basis JSON fence")
	}
	basisJSON, _, found := strings.Cut(basisDoc, "\n```")
	if !found {
		t.Fatal("missing basis JSON end")
	}
	assertInitSourceSHA(t, "original basis", []byte(basisJSON), "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5")
	var basis []struct {
		Table   string            `json:"table"`
		Columns []json.RawMessage `json:"columns"`
	}
	if err := json.Unmarshal([]byte(basisJSON), &basis); err != nil {
		t.Fatal(err)
	}
	var original []string
	columns := 0
	for _, table := range basis {
		original = append(original, table.Table)
		columns += len(table.Columns)
	}
	if len(original) != 109 || columns != 976 || !strictInitTableOrder(original) {
		t.Fatal("original basis table/column count or order changed")
	}

	added := initTableLiteral(t, "../fundinglotcatalog/proof.go", "installationTables")
	if len(added) != 16 || !strictInitTableOrder(added) {
		t.Fatal("installation empty-set tables changed")
	}
	ddl := readInitTableSource(t, "../../specs/022-self-service-payments/c0-production-schema.sql")
	assertInitSourceSHA(t, "C0 DDL", ddl, "e61eb57ec2697fce94be6bac4dd306c86f8b05a339541339dd0fd554f3632047")
	var declared []string
	for _, match := range regexp.MustCompile(`(?m)^CREATE TABLE ([a-z][a-z0-9_]*)\s*\(`).FindAllSubmatch(ddl, -1) {
		declared = append(declared, string(match[1]))
	}
	slices.Sort(declared)
	if !slices.Equal(declared, added) {
		t.Fatal("C0 DDL and installation empty-set tables differ")
	}
	for _, table := range added {
		if slices.Contains(original, table) {
			t.Fatalf("original and C0 sets overlap at %q", table)
		}
	}
	union := append(slices.Clone(original), added...)
	slices.Sort(union)
	if err := checkFrozenInitTables(union); err != nil || !slices.Equal(union, initLockTables[:]) {
		t.Fatalf("109+16 union differs from lock set: %v", err)
	}
	if literal := initTableLiteral(t, "snapshot_tables.go", "initLockTables"); !slices.Equal(literal, union) {
		t.Fatal("production lock targets are not the reviewed static array literal")
	}
}

func TestInitLockTablesRejectDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]string) []string
	}{
		{"missing", func(v []string) []string { return v[:len(v)-1] }},
		{"extra", func(v []string) []string { return append(v, "zz_extra") }},
		{"same count replacement", func(v []string) []string { v[0] = "aa_replacement"; return v }},
		{"wrong order", func(v []string) []string { v[0], v[1] = v[1], v[0]; return v }},
		{"duplicate", func(v []string) []string { v[0] = v[1]; return v }},
		{"composite relation", func(v []string) []string { v[0] = "fl_source_proof"; slices.Sort(v); return v }},
		{"peer heap", func(v []string) []string { v[0] = "mock_environment"; slices.Sort(v); return v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkFrozenInitTables(tc.edit(slices.Clone(initLockTables[:]))); err == nil {
				t.Fatal("accepted changed lock inventory")
			}
		})
	}
}

func TestInitLockSQLOnlyQualifiedSingleStatement(t *testing.T) {
	for _, schema := range []string{"target", "public", `tenant, "quoted".测试; SELECT 1 --`} {
		if err := checkInitLockSQL(initLockSQL(schema), schema); err != nil {
			t.Fatalf("quoted schema %q: %v", schema, err)
		}
	}
	query := initLockSQL("target")
	for name, changed := range map[string]string{
		"missing first ONLY": strings.Replace(query, "ONLY ", "", 1),
		"missing later ONLY": strings.Replace(query, ", ONLY ", ", ", 1),
		"unqualified target": strings.Replace(query, `"target".`, "", 1),
		"different schema":   strings.Replace(query, `"target".`, `"other".`, 1),
		"weaker mode":        strings.Replace(query, "SHARE ROW EXCLUSIVE", "SHARE", 1),
		"extra command":      query + "; SELECT 1",
		"omitted target":     strings.Replace(query, `, ONLY "target"."vouchers"`, "", 1),
		"duplicated target":  strings.Replace(query, `"active_report_projection"`, `"alert_commands"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkInitLockSQL(changed, "target"); err == nil {
				t.Fatal("accepted changed lock grammar or targets")
			}
		})
	}
}

func readInitTableSource(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertInitSourceSHA(t *testing.T, name string, raw []byte, expected string) {
	t.Helper()
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		t.Fatalf("%s frozen source SHA changed; independently review its contract", name)
	}
}

func strictInitTableOrder(tables []string) bool {
	for i, table := range tables {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(table) || i > 0 && tables[i-1] >= table {
			return false
		}
	}
	return true
}

func checkFrozenInitTables(tables []string) error {
	if len(tables) != 125 || !strictInitTableOrder(tables) {
		return fmt.Errorf("lock inventory count, names or strict C-order differ")
	}
	raw, err := json.Marshal(tables)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if len(raw) != 2878 || hex.EncodeToString(digest[:]) != initTablesSHA {
		return fmt.Errorf("lock inventory canonical bytes differ")
	}
	return nil
}

func initTableLiteral(t *testing.T, path, name string) []string {
	t.Helper()
	source, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range source.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.VAR {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != name || len(value.Values) != 1 {
				continue
			}
			literal, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatal("table list is not a static composite literal")
			}
			array, ok := literal.Type.(*ast.ArrayType)
			if !ok {
				t.Fatal("table list is not an array")
			}
			_, inferred := array.Len.(*ast.Ellipsis)
			element, named := array.Elt.(*ast.Ident)
			if !inferred || !named || element.Name != "string" {
				t.Fatal("table list is not [...]string")
			}
			var tables []string
			for _, expression := range literal.Elts {
				text, ok := expression.(*ast.BasicLit)
				if !ok || text.Kind != token.STRING {
					t.Fatal("table target is not a string literal")
				}
				table, err := strconv.Unquote(text.Value)
				if err != nil {
					t.Fatal(err)
				}
				tables = append(tables, table)
			}
			return tables
		}
	}
	t.Fatalf("missing table array %s", name)
	return nil
}

// Parse the generated command independently of pgx.Identifier and the builder.
// This deliberately accepts only the frozen LOCK grammar, including ONLY on
// every target, while decoding doubled quotes inside schema identifiers.
func checkInitLockSQL(query, schema string) error {
	rest, ok := strings.CutPrefix(query, "LOCK TABLE ")
	if !ok {
		return fmt.Errorf("missing LOCK TABLE")
	}
	rest, ok = strings.CutSuffix(rest, " IN SHARE ROW EXCLUSIVE MODE")
	if !ok {
		return fmt.Errorf("wrong mode or extra statement")
	}
	var tables []string
	for {
		rest, ok = strings.CutPrefix(rest, "ONLY ")
		if !ok {
			return fmt.Errorf("missing per-target ONLY")
		}
		qualifiedSchema, tail, err := initQuotedIdentifier(rest)
		if err != nil || qualifiedSchema != schema {
			return fmt.Errorf("wrong or unquoted schema")
		}
		rest, ok = strings.CutPrefix(tail, ".")
		if !ok {
			return fmt.Errorf("unqualified target")
		}
		table, tail, err := initQuotedIdentifier(rest)
		if err != nil {
			return err
		}
		tables = append(tables, table)
		if tail == "" {
			break
		}
		rest, ok = strings.CutPrefix(tail, ", ")
		if !ok {
			return fmt.Errorf("unexpected target separator")
		}
	}
	return checkFrozenInitTables(tables)
}

func initQuotedIdentifier(raw string) (string, string, error) {
	if !strings.HasPrefix(raw, `"`) {
		return "", "", fmt.Errorf("unquoted identifier")
	}
	var decoded strings.Builder
	for i := 1; i < len(raw); i++ {
		if raw[i] != '"' {
			decoded.WriteByte(raw[i])
			continue
		}
		if i+1 < len(raw) && raw[i+1] == '"' {
			decoded.WriteByte('"')
			i++
			continue
		}
		return decoded.String(), raw[i+1:], nil
	}
	return "", "", fmt.Errorf("unterminated identifier")
}
