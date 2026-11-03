// check-procurement-layout checks a design-only column manifest without a database.
// It does not install or attest DDL, keys, ACLs, readers, or a catalog profile.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	contractPath     = "specs/023-periodic-products/b-procurement-schema-contract.md"
	manifestPath     = "specs/023-periodic-products/b-procurement-column-layout-v1.json"
	layoutStatus     = "design-only-new-table-columns"
	maxContractBytes = 2 << 20
)

type column struct {
	Name                string  `json:"name"`
	Type                string  `json:"type"`
	Nullable            bool    `json:"nullable"`
	GeneratedExpression *string `json:"generated_expression"`
}

type table struct {
	Name                    string   `json:"name"`
	ColumnCount             int      `json:"column_count"`
	GeneratedColumnCount    int      `json:"generated_column_count"`
	NongeneratedColumnCount int      `json:"nongenerated_column_count"`
	Columns                 []column `json:"columns"`
}

type manifest struct {
	Schema                  int      `json:"schema"`
	Status                  string   `json:"status"`
	Source                  string   `json:"source"`
	NotCovered              []string `json:"not_covered"`
	TableCount              int      `json:"table_count"`
	ColumnCount             int      `json:"column_count"`
	GeneratedColumnCount    int      `json:"generated_column_count"`
	NongeneratedColumnCount int      `json:"nongenerated_column_count"`
	Tables                  []table  `json:"tables"`
}

type fieldSpec struct {
	column
	generated   string
	placeholder bool
}

type macro struct {
	name      string
	parameter string
	fields    []fieldSpec
}

type tableDraft struct {
	table
	appendCount int
}

type generatedSpec struct {
	name       string
	expression string
}

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldPattern      = regexp.MustCompile(`^([a-z][a-z0-9_]*):([A-Z]+)(\?)?(?: generated(?:\(([^()]*)\)|(\?)))?$`)
	callPattern       = regexp.MustCompile(`^([A-Z])\(([^()]*)\)(\?)?$`)
	tailPattern       = regexp.MustCompile(`^随后(?:§3的)?([1-9][0-9]*)列(generated discriminator|cause discriminator)$`)
	casePattern       = regexp.MustCompile(`^CASE WHEN (kind|cause_kind|source_kind|mode|result_kind) = '([a-z][a-z0-9_]*)' THEN ([a-z][a-z0-9_]*) ELSE NULL END$`)
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "procurement layout:", err)
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("check-procurement-layout", flag.ContinueOnError)
	flags.SetOutput(errOut)
	root := flags.String("root", ".", "repository root")
	write := flags.Bool("write", false, "explicitly write the reviewed design-only column manifest")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	source, err := os.ReadFile(filepath.Join(*root, contractPath))
	if err != nil {
		return fmt.Errorf("read schema contract: %w", err)
	}
	layout, err := parseContract(string(source))
	if err != nil {
		return fmt.Errorf("schema contract: %w", err)
	}
	want, err := encodeManifest(layout)
	if err != nil {
		return err
	}
	path := filepath.Join(*root, manifestPath)
	if *write {
		if err := writeManifest(path, want); err != nil {
			return fmt.Errorf("write design-only manifest: %w", err)
		}
	} else {
		got, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read static manifest: %w (review the contract, then use --write explicitly)", err)
		}
		if !bytes.Equal(got, want) {
			return errors.New("static manifest differs from the schema contract; review the column changes, then run with --write explicitly")
		}
	}
	verb := "PASS"
	if *write {
		verb = "WROTE"
	}
	_, err = fmt.Fprintf(out, "%s %s: %d tables, %d columns (%d generated, %d nongenerated); design only, no DDL/keys/ACL/reader/catalog attestation\n", verb, manifestPath, layout.TableCount, layout.ColumnCount, layout.GeneratedColumnCount, layout.NongeneratedColumnCount)
	return err
}

func encodeManifest(layout manifest) ([]byte, error) {
	b, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode column manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// Only --write reaches this function. A failed parse never changes the fixture.
func writeManifest(path string, data []byte) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".procurement-layout-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0644); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func parseContract(source string) (manifest, error) {
	if len(source) > maxContractBytes {
		return manifest{}, errors.New("contract exceeds the 2 MiB design-input limit")
	}
	macroSection, err := section(source, "## 1. 固定展开规则")
	if err != nil {
		return manifest{}, err
	}
	macroRows, err := markdownTable(macroSection, []string{"宏", "按顺序展开的物理列"})
	if err != nil {
		return manifest{}, fmt.Errorf("macro table: %w", err)
	}
	macros, err := parseMacros(macroRows)
	if err != nil {
		return manifest{}, err
	}
	tableSection, err := section(source, "## 2. 32张新表的固定字段")
	if err != nil {
		return manifest{}, err
	}
	tableRows, err := markdownTable(tableSection, []string{"表", "固定物理列（依序）"})
	if err != nil {
		return manifest{}, fmt.Errorf("new-table layout: %w", err)
	}
	if len(tableRows) != 32 {
		return manifest{}, fmt.Errorf("expected exactly 32 new tables, got %d", len(tableRows))
	}
	drafts := make([]tableDraft, 0, len(tableRows))
	indexes := make(map[string]int)
	for _, row := range tableRows {
		name := row[0]
		if err := validIdentifier(name); err != nil {
			return manifest{}, fmt.Errorf("table: %w", err)
		}
		if !strings.HasPrefix(name, "pp_") {
			return manifest{}, fmt.Errorf("unexpected new-table namespace %q", name)
		}
		if _, ok := indexes[name]; ok {
			return manifest{}, fmt.Errorf("duplicate table %s", name)
		}
		draft, err := parseTable(name, row[1], macros)
		if err != nil {
			return manifest{}, fmt.Errorf("table %s: %w", name, err)
		}
		indexes[name] = len(drafts)
		drafts = append(drafts, draft)
	}
	generatedSection, err := section(source, "### 3.1 generated表达式的固定展开表")
	if err != nil {
		return manifest{}, err
	}
	generatedRows, err := markdownTable(generatedSection, []string{"表", "generated列", "表达式"})
	if err != nil {
		return manifest{}, fmt.Errorf("generated expressions: %w", err)
	}
	generated := make(map[string][]generatedSpec)
	seen := make(map[string]bool)
	for _, row := range generatedRows {
		if _, ok := indexes[row[0]]; !ok {
			return manifest{}, fmt.Errorf("generated expression references unknown table %q", row[0])
		}
		if err := validIdentifier(row[1]); err != nil {
			return manifest{}, fmt.Errorf("generated column: %w", err)
		}
		key := row[0] + "." + row[1]
		if seen[key] {
			return manifest{}, fmt.Errorf("duplicate generated expression %s", key)
		}
		seen[key] = true
		generated[row[0]] = append(generated[row[0]], generatedSpec{row[1], row[2]})
	}
	layout := manifest{Schema: 1, Status: layoutStatus, Source: contractPath, NotCovered: []string{"executable_ddl", "primary_unique_foreign_keys", "checks_triggers_function_bodies", "acl_roles", "reader_basis_catalog_profiles", "original_table_extensions", "database_or_production_acceptance"}, Tables: make([]table, 0, len(drafts))}
	for _, draft := range drafts {
		if err := finishGenerated(&draft, generated[draft.Name]); err != nil {
			return manifest{}, fmt.Errorf("table %s: %w", draft.Name, err)
		}
		draft.ColumnCount = len(draft.Columns)
		for _, col := range draft.Columns {
			if col.GeneratedExpression != nil {
				draft.GeneratedColumnCount++
			} else {
				draft.NongeneratedColumnCount++
			}
		}
		layout.ColumnCount += draft.ColumnCount
		layout.GeneratedColumnCount += draft.GeneratedColumnCount
		layout.NongeneratedColumnCount += draft.NongeneratedColumnCount
		layout.Tables = append(layout.Tables, draft.table)
	}
	layout.TableCount = len(layout.Tables)
	return layout, nil
}

func section(source, heading string) (string, error) {
	lines := strings.Split(source, "\n")
	start := -1
	for i, line := range lines {
		if line == heading {
			if start != -1 {
				return "", fmt.Errorf("duplicate section %q", heading)
			}
			start = i + 1
		}
	}
	if start == -1 {
		return "", fmt.Errorf("missing section %q", heading)
	}
	level := strings.IndexByte(heading, ' ')
	end := len(lines)
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "#") {
			depth := strings.IndexByte(line, ' ')
			if depth > 0 && depth <= level && strings.Trim(line[:depth], "#") == "" {
				end = i
				break
			}
		}
	}
	return strings.Join(lines[start:end], "\n"), nil
}

func markdownTable(source string, header []string) ([][]string, error) {
	state := 0 // header, separator, rows, completed
	var rows [][]string
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "+|") || strings.HasPrefix(line, "-|") {
			return nil, fmt.Errorf("malformed table row %q", line)
		}
		if !strings.HasPrefix(line, "|") {
			if state == 1 {
				return nil, errors.New("missing markdown table separator")
			}
			if state == 2 {
				state = 3
			}
			continue
		}
		if state == 3 {
			return nil, errors.New("unexpected additional markdown table/row")
		}
		if !strings.HasSuffix(line, "|") {
			return nil, errors.New("table row must end in a pipe")
		}
		cells := strings.Split(line[1:len(line)-1], "|")
		if len(cells) != len(header) {
			return nil, fmt.Errorf("expected %d table cells, got %d", len(header), len(cells))
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
			if cells[i] == "" {
				return nil, errors.New("empty markdown table cell")
			}
		}
		switch state {
		case 0:
			for i := range header {
				if cells[i] != header[i] {
					return nil, fmt.Errorf("unexpected table header %q", cells[i])
				}
			}
			state = 1
		case 1:
			for _, cell := range cells {
				if cell != "---" {
					return nil, fmt.Errorf("unsupported table separator %q", cell)
				}
			}
			state = 2
		case 2:
			rows = append(rows, cells)
		}
	}
	if len(rows) == 0 {
		return nil, errors.New("missing or empty markdown table")
	}
	return rows, nil
}

func parseMacros(rows [][]string) (map[string]macro, error) {
	allowed := map[string]string{"S(k)": "k", "C(k)": "k", "R": "", "A": "", "A?": "", "U(p)": "p", "F(p)": "p", "K(p)": "p", "P(p)": "p", "Q": ""}
	result := make(map[string]macro)
	for _, row := range rows {
		parameter, ok := allowed[row[0]]
		if !ok {
			return nil, fmt.Errorf("unknown macro definition %q", row[0])
		}
		if _, ok := result[row[0]]; ok {
			return nil, fmt.Errorf("duplicate macro %s", row[0])
		}
		tokens, err := splitFields(row[1])
		if err != nil {
			return nil, fmt.Errorf("macro %s: %w", row[0], err)
		}
		def := macro{name: row[0], parameter: parameter}
		seen := make(map[string]bool)
		for _, token := range tokens {
			field, err := parseField(token)
			if err != nil {
				return nil, fmt.Errorf("macro %s: %w", row[0], err)
			}
			if seen[field.Name] {
				return nil, fmt.Errorf("duplicate macro column %s.%s", row[0], field.Name)
			}
			if parameter == "p" && !strings.HasPrefix(field.Name, "p_") {
				return nil, fmt.Errorf("prefix macro %s column %s lacks p_", row[0], field.Name)
			}
			if field.placeholder || (field.generated != "" && !(parameter == "k" && field.Name == "step_kind" && field.Type == "TEXT" && field.generated == "k")) {
				return nil, fmt.Errorf("unsupported generated field in macro %s", row[0])
			}
			seen[field.Name] = true
			def.fields = append(def.fields, field)
		}
		result[row[0]] = def
	}
	for name := range allowed {
		if _, ok := result[name]; !ok {
			return nil, fmt.Errorf("missing macro %s", name)
		}
	}
	return result, nil
}

func splitFields(source string) ([]string, error) {
	var tokens []string
	start, depth := 0, 0
	for i, r := range source {
		switch r {
		case '(':
			depth++
			if depth > 1 {
				return nil, errors.New("nested field/macro parentheses are unsupported")
			}
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced field parentheses")
			}
		case ',':
			if depth == 0 {
				tokens = append(tokens, strings.TrimSpace(source[start:i]))
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, errors.New("unbalanced field parentheses")
	}
	tokens = append(tokens, strings.TrimSpace(source[start:]))
	for _, token := range tokens {
		if token == "" {
			return nil, errors.New("empty field or macro")
		}
	}
	return tokens, nil
}

func parseField(token string) (fieldSpec, error) {
	m := fieldPattern.FindStringSubmatch(token)
	if m == nil {
		return fieldSpec{}, fmt.Errorf("unsupported field/type/suffix %q", token)
	}
	if err := validIdentifier(m[1]); err != nil {
		return fieldSpec{}, err
	}
	if strings.Contains(token, " generated(") && m[4] == "" {
		return fieldSpec{}, errors.New("empty generated expression")
	}
	types := map[string]string{"T": "TEXT", "I": "BIGINT", "N": "NUMERIC(100,0)", "B": "BOOLEAN", "D": "TIMESTAMPTZ", "X": "XID8", "O": "OID", "J": "JSONB", "H": "SMALLINT", "INTEGER": "INTEGER"}
	typeName, ok := types[m[2]]
	if !ok {
		return fieldSpec{}, fmt.Errorf("unknown type %q", m[2])
	}
	return fieldSpec{column: column{Name: m[1], Type: typeName, Nullable: m[3] != ""}, generated: m[4], placeholder: m[5] != ""}, nil
}

func parseTable(name, source string, macros map[string]macro) (tableDraft, error) {
	parts := strings.Split(source, "；")
	if len(parts) > 2 {
		return tableDraft{}, errors.New("multiple generated tail clauses")
	}
	draft := tableDraft{table: table{Name: name}}
	if len(parts) == 2 {
		tail := tailPattern.FindStringSubmatch(parts[1])
		if tail == nil {
			return tableDraft{}, fmt.Errorf("unsupported generated tail %q", parts[1])
		}
		n, err := strconv.Atoi(tail[1])
		if err != nil || n > 1000 {
			return tableDraft{}, errors.New("generated tail count exceeds the layout limit")
		}
		if (name == "pp_root_steps") != (tail[2] == "generated discriminator") {
			return tableDraft{}, errors.New("generated tail discriminator family mismatch")
		}
		draft.appendCount = n
	}
	tokens, err := splitFields(parts[0])
	if err != nil {
		return tableDraft{}, err
	}
	seen := make(map[string]bool)
	for _, token := range tokens {
		fields, err := expandToken(token, macros)
		if err != nil {
			return tableDraft{}, err
		}
		for _, field := range fields {
			if seen[field.Name] {
				return tableDraft{}, fmt.Errorf("duplicate column %s", field.Name)
			}
			seen[field.Name] = true
			if err := applyInlineGenerated(name, &field); err != nil {
				return tableDraft{}, err
			}
			draft.Columns = append(draft.Columns, field.column)
		}
	}
	return draft, nil
}

func expandToken(token string, macros map[string]macro) ([]fieldSpec, error) {
	if strings.Contains(token, ":") {
		field, err := parseField(token)
		return []fieldSpec{field}, err
	}
	if strings.HasSuffix(token, "??") {
		return nil, fmt.Errorf("unsupported macro nullable suffix %q", token)
	}
	def, exact := macros[token] // A? is a distinct macro, not nullable A.
	arg, groupNullable := "", false
	if !exact {
		if match := callPattern.FindStringSubmatch(token); match != nil {
			var ok bool
			for _, parameter := range []string{"k", "p"} {
				if def, ok = macros[match[1]+"("+parameter+")"]; ok {
					break
				}
			}
			if !ok {
				return nil, fmt.Errorf("unknown macro %q", token)
			}
			arg, groupNullable = match[2], match[3] != ""
			if arg != "空" || def.parameter != "p" {
				if err := validIdentifier(arg); err != nil {
					return nil, fmt.Errorf("macro argument: %w", err)
				}
			}
		} else if strings.HasSuffix(token, "?") {
			var ok bool
			def, ok = macros[strings.TrimSuffix(token, "?")]
			if !ok || def.parameter != "" {
				return nil, fmt.Errorf("unknown macro or suffix %q", token)
			}
			groupNullable = true
		} else {
			return nil, fmt.Errorf("unknown macro %q", token)
		}
	}
	if exact && def.parameter != "" {
		return nil, fmt.Errorf("unbound macro parameter %q", token)
	}
	fields := make([]fieldSpec, 0, len(def.fields))
	for _, template := range def.fields {
		field := template
		if def.parameter == "p" {
			prefix := arg + "_"
			if arg == "空" {
				prefix = ""
			}
			field.Name = prefix + strings.TrimPrefix(field.Name, "p_")
		}
		if def.parameter == "k" && field.generated == "k" {
			field.generated = arg
		}
		field.Nullable = field.Nullable || groupNullable
		if err := validIdentifier(field.Name); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func applyInlineGenerated(tableName string, field *fieldSpec) error {
	if field.placeholder {
		if tableName != "pp_account_journal" || field.Type != "TEXT" || (field.Name != "command_operation_id" && field.Name != "procurement_event_id" && field.Name != "dispatch_attempt_id") {
			return fmt.Errorf("unsupported generated? placeholder %s", field.Name)
		}
		field.Nullable = true
		placeholder := "" // Resolved exactly once from section 3.1.
		field.GeneratedExpression = &placeholder
		return nil
	}
	if field.generated == "" {
		return nil
	}
	var expression string
	switch {
	case field.Name == "step_kind" && field.Type == "TEXT":
		if err := validIdentifier(field.generated); err != nil {
			return fmt.Errorf("generated step kind: %w", err)
		}
		expression = "'" + field.generated + "'::text"
	case field.Name == "previous_ordinal" && field.Type == "BIGINT" && field.generated == "ordinal-1":
		expression = "ordinal - 1"
	case field.Name == "previous_member_ordinal" && field.Type == "BIGINT" && field.generated == "member_ordinal-1":
		expression = "member_ordinal - 1"
	default:
		return fmt.Errorf("unsupported inline generated expression %s(%s)", field.Name, field.generated)
	}
	field.GeneratedExpression = &expression
	return nil
}

func finishGenerated(draft *tableDraft, specs []generatedSpec) error {
	indexes := make(map[string]int)
	for i, col := range draft.Columns {
		indexes[col.Name] = i
	}
	appended := 0
	for _, spec := range specs {
		if err := validateCase(draft.Name, spec, draft.Columns); err != nil {
			return err
		}
		expression := spec.expression
		if i, ok := indexes[spec.name]; ok {
			col := &draft.Columns[i]
			if col.GeneratedExpression == nil || *col.GeneratedExpression != "" {
				return fmt.Errorf("generated column %s duplicates an inline or ordinary column", spec.name)
			}
			col.GeneratedExpression = &expression
		} else {
			if draft.appendCount == 0 || (!strings.HasPrefix(spec.name, "d_") && !(draft.Name == "pp_cost_reset_decisions" && spec.name == "mutation_cause_kind")) {
				return fmt.Errorf("unexpected appended generated column %s", spec.name)
			}
			indexes[spec.name] = len(draft.Columns)
			draft.Columns = append(draft.Columns, column{Name: spec.name, Type: "TEXT", Nullable: true, GeneratedExpression: &expression})
			appended++
		}
	}
	if appended != draft.appendCount {
		return fmt.Errorf("generated tail requires %d columns, got %d", draft.appendCount, appended)
	}
	for _, col := range draft.Columns {
		if col.GeneratedExpression == nil {
			continue
		}
		if *col.GeneratedExpression == "" {
			return fmt.Errorf("missing generated expression for %s", col.Name)
		}
		var required string
		switch *col.GeneratedExpression {
		case "ordinal - 1":
			required = "ordinal"
		case "member_ordinal - 1":
			required = "member_ordinal"
		}
		if required != "" {
			i, ok := indexes[required]
			if !ok || draft.Columns[i].Type != "BIGINT" || draft.Columns[i].GeneratedExpression != nil {
				return fmt.Errorf("generated %s requires ordinary BIGINT column %s", col.Name, required)
			}
		}
	}
	return nil
}

func validateCase(tableName string, spec generatedSpec, columns []column) error {
	if tableName == "pp_cost_reset_decisions" && spec.name == "mutation_cause_kind" {
		if spec.expression != "CASE WHEN policy = 'applied' AND cause_kind = 'procurement_dispute' THEN 'procurement_dispute_reset' WHEN policy = 'applied' AND cause_kind = 'incoming_conflict' THEN 'incoming_conflict_reset' ELSE NULL END" {
			return errors.New("reset mutation kind has mismatched generated mapping")
		}
		for _, name := range []string{"policy", "cause_kind"} {
			found := false
			for _, col := range columns {
				if col.Name == name && col.Type == "TEXT" && col.GeneratedExpression == nil {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("reset mutation kind references missing ordinary TEXT column %s", name)
			}
		}
		return nil
	}
	match := casePattern.FindStringSubmatch(spec.expression)
	if match == nil {
		return fmt.Errorf("unsupported generated CASE for %s", spec.name)
	}
	condition, value, result := match[1], match[2], match[3]
	wantCondition, wantResult := "cause_kind", "cause_source_id"
	if tableName == "pp_attempt_delivery_sources" {
		wantCondition = "mode"
	}
	if tableName == "pp_customer_finish_outcomes" {
		wantCondition, wantResult = "result_kind", "settlement_id"
	}
	if tableName == "pp_root_steps" {
		wantCondition, wantResult = "kind", "source_id"
	}
	if tableName == "pp_account_journal" {
		wantCondition, wantResult = "source_kind", "source_id"
		wantValue, ok := map[string]string{"command_operation_id": "command", "procurement_event_id": "budget_event", "dispatch_attempt_id": "dispatch"}[spec.name]
		if !ok || value != wantValue {
			return fmt.Errorf("account discriminator %s has mismatched branch", spec.name)
		}
	} else if spec.name != "d_"+value {
		return fmt.Errorf("generated discriminator %s does not match its CASE branch", spec.name)
	}
	if condition != wantCondition || result != wantResult {
		return fmt.Errorf("generated %s uses mismatched source columns", spec.name)
	}
	for _, required := range []string{condition, result} {
		found := false
		for _, col := range columns {
			if col.Name == required && col.Type == "TEXT" && col.GeneratedExpression == nil {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("generated %s references missing ordinary TEXT column %s", spec.name, required)
		}
	}
	return nil
}

func validIdentifier(name string) error {
	if !identifierPattern.MatchString(name) {
		return fmt.Errorf("unsupported PostgreSQL identifier %q", name)
	}
	if len(name) > 63 {
		return fmt.Errorf("PostgreSQL identifier exceeds 63 bytes and risks truncation: %s", name)
	}
	return nil
}
