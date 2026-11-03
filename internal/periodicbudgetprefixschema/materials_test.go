package periodicbudgetprefixschema

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestFrozenMaterialsMatchSpecOriginals(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source unavailable")
	}
	for _, test := range []struct{ name, embedded, sha string }{
		{"b-prefix-candidate.sql", candidateSQL, sqlSHA},
		{"b-prefix-permissions.sql", permissionSQL, permissionsSHA},
	} {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(here), "..", "..", "specs", "023-periodic-products", test.name))
		if err != nil || string(raw) != test.embedded || digest(string(raw)) != test.sha {
			t.Fatal("frozen candidate does not match its reviewed specification original")
		}
	}
	ddl, err := SQL()
	if err != nil || ddl != candidateSQL {
		t.Fatal("fixed SQL unavailable")
	}
	d, err := Digests()
	if err != nil || d.SQLSHA256 != sqlSHA || d.PermissionsSHA256 != permissionsSHA {
		t.Fatal("fixed digests unavailable")
	}
}

func TestPermissionIdentifiers(t *testing.T) {
	for _, test := range []struct{ app, projection string }{
		{"", "projection"}, {"app", ""}, {"same", "same"},
		{"a\x00b", "projection"}, {"app", "p\x00q"},
		{strings.Repeat("a", 64), "projection"}, {"app", strings.Repeat("p", 64)},
		{strings.Repeat("中", 22), "projection"},
	} {
		if out, err := Permissions(test.app, test.projection); err != ErrIdentifiers || out != "" {
			t.Fatal("invalid SQL identifier accepted")
		}
	}
	for _, test := range []struct{ app, projection string }{
		{"app", "projection"}, {strings.Repeat("a", 63), strings.Repeat("p", 63)},
		{`app"; SELECT 'quoted'--`, `projection"role`}, {"{{projection}}", "{{app}}"},
	} {
		out, err := Permissions(test.app, test.projection)
		want := strings.NewReplacer("{{app}}", pgx.Identifier{test.app}.Sanitize(), "{{projection}}", pgx.Identifier{test.projection}.Sanitize()).Replace(permissionSQL)
		if err != nil || out != want {
			t.Fatal("identifiers were not rendered exactly once as SQL identifiers")
		}
	}
}

func TestPermissionMatrixRemainsFrozen(t *testing.T) {
	if strings.Count(permissionSQL, "{{app}}") != 10 || strings.Count(permissionSQL, "{{projection}}") != 3 {
		t.Fatal("frozen placeholder shape changed")
	}
	if strings.Count(permissionSQL, "pb_prefix_") != 12 || strings.Contains(permissionSQL, "ALL FUNCTIONS") || strings.Contains(permissionSQL, "ALL TABLES") {
		t.Fatal("candidate permissions are not the explicit twelve-function delta")
	}
}
