package procurementbirthschema

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

func readCanonical(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../procurementprefixschema/materials/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func tableStatement(t *testing.T, body, name string) string {
	t.Helper()
	pattern := `(?s)CREATE TABLE \{\{MONI_SCHEMA\}\}\."` + regexp.QuoteMeta(name) + `" \(.*?\n\);`
	result := regexp.MustCompile(pattern).FindAllString(body, -1)
	if len(result) != 1 {
		t.Fatalf("table %s has %d definitions", name, len(result))
	}
	return result[0]
}

func constraintStatements(t *testing.T, body string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, stmt := range regexp.MustCompile(`(?s)ALTER TABLE \{\{MONI_SCHEMA\}\}\.[^;]+;`).FindAllString(body, -1) {
		name := regexp.MustCompile(`ADD CONSTRAINT (\w+)`).FindStringSubmatch(stmt)
		if len(name) != 2 {
			t.Fatalf("not a constraint statement: %s", stmt)
		}
		if _, exists := result[name[1]]; exists {
			t.Fatalf("duplicate constraint %s", name[1])
		}
		result[name[1]] = stmt
	}
	return result
}

func TestA1LayoutMatchesCanonicalSlice(t *testing.T) {
	items, err := originals()
	if err != nil {
		t.Fatal(err)
	}
	canonical := readCanonical(t, "tables-v1.sql")
	tables := []string{"pp_operation_roots", "pp_root_steps", "pp_request_origins", "pp_request_heads"}
	if strings.Count(items[0].body, "CREATE TABLE ") != len(tables) {
		t.Fatal("A1 contains unrelated tables")
	}
	for _, table := range tables {
		if tableStatement(t, items[0].body, table) != tableStatement(t, canonical, table) {
			t.Fatalf("A1 changed canonical columns/types/nullability/generated expression: %s", table)
		}
	}
	if len(regexp.MustCompile(`(?m)^ "`).FindAllString(items[0].body, -1)) != 71 {
		t.Fatal("A1 four-table column count changed")
	}
	pattern := regexp.MustCompile(`(?s)ALTER TABLE \{\{MONI_SCHEMA\}\}\."request_records".*?;`)
	expected := pattern.FindString(readCanonical(t, "original-columns-v1.sql"))
	if expected == "" || pattern.FindString(items[1].body) != expected || strings.Count(items[1].body, "ALTER TABLE ") != 1 || strings.Count(items[1].body, "ADD COLUMN ") != 4 {
		t.Fatal("A1 changed nullable original request additions or extends unrelated tables")
	}
}

func TestA1ConstraintWhitelistAndCanonicalContracts(t *testing.T) {
	items, err := originals()
	if err != nil {
		t.Fatal(err)
	}
	actual := constraintStatements(t, items[2].body)
	canonical := constraintStatements(t, readCanonical(t, "source-constraints-v1.sql")+"\n"+readCanonical(t, "original-constraints-v1.sql"))
	retained := []string{
		"pp_s_root_pk", "pp_s_root_xid_uq", "pp_s_step_pk", "pp_s_request_birth_pk", "pp_s_request_head_pk",
		"pp_s_root_scalar_ck", "pp_s_root_template_ck", "pp_s_step_shape_ck", "pp_s_request_birth_step_ck",
		"pp_s_request_initial_ck", "pp_s_request_snapshot_ck", "pp_s_request_head_shape_ck", "pp_o_request_birth_ck",
		"pp_s_request_birth_root_fk", "pp_s_step_root_fk", "pp_s_root_request_fk", "pp_s_request_original_fk",
		"pp_s_request_origin_head_fk", "pp_s_request_head_origin_fk",
	}
	for _, name := range retained {
		if actual[name] == "" || actual[name] != canonical[name] {
			t.Fatalf("retained canonical contract changed: %s", name)
		}
	}
	locators := map[string]string{
		"pp_bx_request_step_locator_fk": `pp_request_origins ADD CONSTRAINT pp_bx_request_step_locator_fk
 FOREIGN KEY (root_id,step_no)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)`,
		"pp_bx_step_request_origin_fk": `pp_root_steps ADD CONSTRAINT pp_bx_step_request_origin_fk
 FOREIGN KEY (d_request_birth)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)`,
		"pp_bx_root_request_origin_fk": `pp_operation_roots ADD CONSTRAINT pp_bx_root_request_origin_fk
 FOREIGN KEY (request_id)
 REFERENCES {{MONI_SCHEMA}}.pp_request_origins (request_id)`,
		"pp_bx_original_request_step_locator_fk": `request_records ADD CONSTRAINT pp_bx_original_request_step_locator_fk
 FOREIGN KEY (pp_origin_root_id,pp_origin_root_step)
 REFERENCES {{MONI_SCHEMA}}.pp_root_steps (root_id,step_no)`,
	}
	for name, body := range locators {
		expected := "ALTER TABLE {{MONI_SCHEMA}}." + body + "\n DEFERRABLE INITIALLY DEFERRED;"
		if actual[name] != expected {
			t.Fatalf("locator mapping or true deferred timing changed: %s", name)
		}
	}
	if len(actual) != len(retained)+len(locators) {
		t.Fatal("unreviewed constraint installed by A1")
	}
	keys, checks, fks := 0, 0, 0
	for _, stmt := range actual {
		switch {
		case strings.Contains(stmt, "FOREIGN KEY"):
			fks++
			if !strings.HasSuffix(stmt, "DEFERRABLE INITIALLY DEFERRED;") {
				t.Fatal("birth native FK must allow real bounded construction order")
			}
		case strings.Contains(stmt, " CHECK "):
			checks++
		default:
			keys++
		}
	}
	if keys != 5 || checks != 8 || fks != 10 || strings.Count(items[2].body, " UNIQUE ") != 1 {
		t.Fatalf("A1 constraint shape keys=%d checks=%d fks=%d", keys, checks, fks)
	}
	for _, token := range []string{"NOT VALID", "ON DELETE", "ON UPDATE", "MATCH FULL"} {
		if strings.Contains(items[2].body, token) {
			t.Fatalf("unexpected native relationship behavior: %s", token)
		}
	}
}

func TestA1PinsAndRelationOnlyAssembly(t *testing.T) {
	inventory, err := A1Digests()
	if err != nil || len(inventory) != 3 {
		t.Fatalf("A1 inventory: %v", err)
	}
	for i, expected := range []string{"a1-tables-v1.sql", "a1-original-columns-v1.sql", "a1-constraints-v1.sql"} {
		if inventory[i].Name != expected {
			t.Fatal("A1 assembly order changed")
		}
	}
	inventory[0].SHA256 = "caller mutation"
	if next, err := A1Digests(); err != nil || next[0].SHA256 == "caller mutation" {
		t.Fatal("caller changed pinned inventory")
	}
	sql, err := A1SQL("isolated schema")
	if err != nil || !strings.Contains(sql, `"isolated schema"."pp_operation_roots"`) || strings.Contains(sql, "{{MONI_SCHEMA}}") {
		t.Fatal("A1 schema rendering failed")
	}
	if strings.LastIndex(sql, "PRIMARY KEY") > strings.Index(sql, "FOREIGN KEY") || strings.LastIndex(sql, " UNIQUE ") > strings.Index(sql, "FOREIGN KEY") || strings.LastIndex(sql, " CHECK ") > strings.Index(sql, "FOREIGN KEY") {
		t.Fatal("A1 dependent constraints preceded keys/checks")
	}
	for _, token := range []string{"CREATE FUNCTION", "CREATE TRIGGER", "GRANT ", "SECURITY DEFINER", "DROP ", "UPDATE ", "INSERT INTO", "DELETE FROM", "REFERENCES \"isolated schema\".pp_account", "REFERENCES \"isolated schema\".upstream_attempts"} {
		if strings.Contains(sql, token) {
			t.Fatalf("A1 relational slice includes unrelated capability: %s", token)
		}
	}
	items, err := originals()
	if err != nil {
		t.Fatal(err)
	}
	for i := range items {
		changed := append([]material(nil), items...)
		changed[i].body += "\n-- drift"
		if result, err := assemble(changed, `"test"`); !errors.Is(err, ErrMaterials) || result != "" {
			t.Fatalf("material %d drift accepted", i)
		}
	}
	items[0], items[1] = items[1], items[0]
	if result, err := assemble(items, `"test"`); !errors.Is(err, ErrMaterials) || result != "" {
		t.Fatal("reordered materials accepted")
	}
	if result, err := assemble(items[:2], `"test"`); !errors.Is(err, ErrMaterials) || result != "" {
		t.Fatal("partial candidate accepted")
	}
}

func TestA1SafeSingleSchemaRendering(t *testing.T) {
	for _, schema := range []string{"", strings.Repeat("x", 64), strings.Repeat("中", 22), "bad\x00name", "bad\xffname"} {
		if sql, err := A1SQL(schema); !errors.Is(err, ErrIdentifier) || sql != "" {
			t.Fatal("invalid schema identifier accepted")
		}
	}
	for _, schema := range []string{strings.Repeat("x", 63), strings.Repeat("中", 21), `x"; DROP SCHEMA public; --`, "{{MONI_SCHEMA}}"} {
		sql, err := A1SQL(schema)
		quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
		if err != nil || !strings.Contains(sql, "CREATE TABLE "+quoted+`."pp_operation_roots"`) || !strings.Contains(sql, "ALTER TABLE "+quoted+`.request_records ADD CONSTRAINT pp_o_request_birth_ck`) {
			t.Fatal("explicit schema changed or was recursively replaced")
		}
	}
}
