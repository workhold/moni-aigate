package procurementprefixschema

import (
	"errors"
	"strings"
	"testing"
)

func TestMaterialsPinnedAndPhased(t *testing.T) {
	inventory, err := Digests()
	if err != nil || len(inventory) != 6 {
		t.Fatalf("material inventory: %v", err)
	}
	inventory[0].SHA256 = "caller changed owned inventory"
	if _, err := Digests(); err != nil {
		t.Fatal(err)
	}
	sql, err := SQL("isolated schema")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sql, "CREATE TABLE ") != 32 || strings.Contains(sql, "{{MONI_SCHEMA}}") || !strings.Contains(sql, `"isolated schema"."pp_operation_roots"`) {
		t.Fatal("wrong rendered table inventory")
	}
	firstFK, lastKey := strings.Index(sql, "FOREIGN KEY"), -1
	for _, kind := range []string{"PRIMARY KEY", "UNIQUE"} {
		if at := strings.LastIndex(sql, kind); at > lastKey {
			lastKey = at
		}
	}
	if firstFK < lastKey || firstFK < 0 {
		t.Fatal("foreign keys preceded a target key")
	}
	for _, forbidden := range []string{"CREATE FUNCTION", "CREATE TRIGGER", "GRANT ", "NOT VALID", "SECURITY DEFINER"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("relation materials contain %s", forbidden)
		}
	}
	originals, err := originals()
	if err != nil {
		t.Fatal(err)
	}
	for i := range originals {
		changed := append([]material(nil), originals...)
		changed[i].body += "\n-- drift"
		if got, err := assemble(changed, `"test"`); !errors.Is(err, ErrMaterials) || got != "" {
			t.Fatalf("material %d drift accepted", i)
		}
	}
}

func TestSchemaIdentifiersAndSingleReplacement(t *testing.T) {
	for _, schema := range []string{"", strings.Repeat("x", 64), strings.Repeat("中", 22), "bad\x00name", "bad\xffname"} {
		if sql, err := SQL(schema); !errors.Is(err, ErrIdentifier) || sql != "" {
			t.Fatal("invalid identifier accepted")
		}
	}
	for _, schema := range []string{strings.Repeat("x", 63), strings.Repeat("中", 21), `x"; DROP SCHEMA public; --`, "{{MONI_SCHEMA}}"} {
		sql, err := SQL(schema)
		quoted := `"` + strings.ReplaceAll(schema, `"`, `""`) + `"`
		if err != nil || !strings.Contains(sql, "CREATE TABLE "+quoted+`."pp_operation_roots"`) {
			t.Fatal("valid quoted identifier changed or recursively replaced")
		}
	}
}

func TestRejectMalformedPhases(t *testing.T) {
	valid := "-- preamble\n-- @phase keys\nK\n-- @phase checks\nC\n-- @phase foreign-keys\nF\n"
	parts, err := splitPhases(valid)
	if err != nil || parts != [3]string{"K\n", "C\n", "F\n"} {
		t.Fatal("phase parsing changed fixed sections")
	}
	for _, body := range []string{
		strings.Replace(valid, "-- @phase checks\n", "", 1),
		valid + "-- @phase keys\n",
		strings.Replace(valid, "-- @phase keys\nK\n-- @phase checks\n", "-- @phase checks\nK\n-- @phase keys\n", 1),
		strings.Replace(valid, "-- @phase keys\n", "-- @phase keys \n", 1),
	} {
		if _, err := splitPhases(body); !errors.Is(err, ErrMaterials) {
			t.Fatal("malformed phase contract accepted")
		}
	}
}
