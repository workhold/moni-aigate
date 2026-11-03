package procurementaccountwriter

import (
	"errors"
	"strings"
	"testing"
)

func TestMaterialsAndBindings(t *testing.T) {
	b := Bindings{"schema", "app", "writer", 17001, 17002}
	digests, err := Digests()
	if err != nil || len(digests.SQLSHA256) != 64 || len(digests.PermissionsSHA256) != 64 {
		t.Fatalf("frozen materials: %v", err)
	}
	for _, render := range []func(Bindings) (string, error){SQL, Permissions} {
		body, err := render(b)
		if err != nil || strings.Contains(body, "{{MONI_") {
			t.Fatal("unrendered or invalid fixed material")
		}
		for _, invalid := range []Bindings{
			{"", "app", "writer", 1, 2}, {"bad\x00schema", "app", "writer", 1, 2},
			{"bad\xffschema", "app", "writer", 1, 2}, {strings.Repeat("中", 22), "app", "writer", 1, 2},
			{"schema", "same", "same", 1, 2}, {"schema", "app", "writer", 0, 2},
			{"schema", "app", "writer", 1, 0}, {"schema", "app", "writer", 1, 1},
		} {
			if got, err := render(invalid); got != "" || !errors.Is(err, ErrBindings) {
				t.Fatal("invalid installation bindings accepted")
			}
		}
	}
}

func TestSingleReplacementDoesNotReinterpretQuotedNames(t *testing.T) {
	b := Bindings{`x"; SELECT 1; --`, "{{MONI_WRITER}}", "writer", 1, 2}
	got := render("{{MONI_SCHEMA}} {{MONI_APP}} {{MONI_APP_OID}}", b)
	want := `"x""; SELECT 1; --" "{{MONI_WRITER}}" 1`
	if got != want {
		t.Fatal("quoted names changed or were recursively replaced")
	}
	b.Schema = `safe$$ $moni_ac_0$ " name`
	got = render("AS $$ SELECT {{MONI_SCHEMA}}.id $$", b)
	if !strings.HasPrefix(got, "AS $moni_ac_1$ SELECT ") || !strings.HasSuffix(got, "$moni_ac_1$") || strings.Count(got, "$moni_ac_1$") != 2 {
		t.Fatal("a quoted identifier terminated the function body")
	}
}
