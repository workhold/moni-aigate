package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func atRoot(t *testing.T) {
	t.Helper()
	old, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Clean(filepath.Join(old, "../.."))
	if os.Chdir(root) != nil {
		t.Fatal("root")
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func TestFixedInputs(t *testing.T) {
	atRoot(t)
	in, e := loadInputs()
	if e != nil || len(in.manifest) != 22 || len(in.tools) < 7 {
		t.Fatal("fixed source pins differ", e)
	}
}

func TestInputDiskSetAndBytesClosed(t *testing.T) {
	atRoot(t)
	root, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	in, e := loadInputs()
	if e != nil {
		t.Fatal(e)
	}
	paths := []string{"go.mod", ddlName, aclName}
	for _, p := range provisionPins {
		paths = append(paths, p.Name)
	}
	for _, p := range in.manifest {
		paths = append(paths, filepath.Join("migrations", p.Name))
	}
	for _, p := range in.tools {
		paths = append(paths, filepath.Join("scripts/funding-lot-descriptor", p.Name))
	}
	mirror := t.TempDir()
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil || os.MkdirAll(filepath.Dir(filepath.Join(mirror, p)), 0700) != nil || os.WriteFile(filepath.Join(mirror, p), b, 0600) != nil {
			t.Fatal("source mirror")
		}
	}
	if os.Chdir(mirror) != nil {
		t.Fatal("source mirror cwd")
	}
	t.Cleanup(func() { _ = os.Chdir(root) })
	if _, e := loadInputs(); e != nil {
		t.Fatal("unchanged source mirror rejected")
	}
	for _, p := range []string{"migrations/023_unreviewed.sql", "scripts/funding-lot-descriptor/unreviewed.go"} {
		if os.WriteFile(p, []byte("unreviewed"), 0600) != nil {
			t.Fatal("extra input")
		}
		if _, e := loadInputs(); e == nil {
			t.Fatal("new disk input ignored")
		}
		if os.Remove(p) != nil {
			t.Fatal("extra input cleanup")
		}
	}
	for _, p := range []string{ddlName, aclName, provisionPins[0].Name, filepath.Join("migrations", in.manifest[0].Name), filepath.Join("scripts/funding-lot-descriptor", in.tools[0].Name)} {
		b, e := os.ReadFile(p)
		if e != nil || os.WriteFile(p, append(append([]byte(nil), b...), '\n'), 0600) != nil {
			t.Fatal("modify source mirror")
		}
		if _, e := loadInputs(); e == nil {
			t.Fatal("changed input bytes accepted")
		}
		if os.Remove(p) != nil {
			t.Fatal("remove source mirror")
		}
		if _, e := loadInputs(); e == nil {
			t.Fatal("missing input accepted")
		}
		if os.WriteFile(p, b, 0600) != nil {
			t.Fatal("restore source mirror")
		}
	}
}

func TestDatabaseDestinationClosed(t *testing.T) {
	good := "postgres://moni_test@127.0.0.1:55439/moni_test?sslmode=disable"
	if _, e := databaseConfig(good); e != nil {
		t.Fatal("literal dedicated database rejected")
	}
	for _, bad := range []string{"", "postgres://moni_test@localhost:55439/moni_test?sslmode=disable", "postgres://moni_test@127.0.0.1:5432/moni_test?sslmode=disable", "postgres://moni_test@127.0.0.1:55439/other?sslmode=disable", good + "&service=production", good + "&host=elsewhere", good + "&sslmode=disable"} {
		if _, e := databaseConfig(bad); e == nil {
			t.Fatal("unsafe connection configuration accepted")
		}
	}
}

func TestCanonicalAndLiteral(t *testing.T) {
	a := document{"z": []any{2, 1}, "a": "a < b && 'flcdg_literal'"}
	b := document{"a": "a < b && 'flcdg_literal'", "z": []any{2, 1}}
	ca, e := canonical(a)
	if e != nil || !same(a, b) || string(ca) != `{"a":"a < b && 'flcdg_literal'","z":[2,1]}` {
		t.Fatal("canonical key order or literal mutated")
	}
	if same(a, document{"a": a["a"], "z": []any{1, 2}}) {
		t.Fatal("ordered array sorted")
	}
	for _, s := range []string{`"one", pg_catalog, pg_temp`, `one,pg_catalog,pg_temp`, `"a""b", pg_catalog, pg_temp`} {
		if _, e := parsePath(s); e != nil {
			t.Fatal("valid identifier list rejected")
		}
	}
	for _, s := range []string{"", `"unterminated`, `one,,pg_catalog`, `one; SET ROLE owner`, `one,`} {
		if _, e := parsePath(s); e == nil {
			t.Fatal("malformed path accepted")
		}
	}
}

func TestModifiedIdentityIsChanged(t *testing.T) {
	id := object{"class": "relation", "scope": "application", "name": "tenants"}
	b := document{"relations": []any{object{"identity": id, "attributes": []any{"id"}}}}
	a := document{"relations": []any{object{"identity": id, "attributes": []any{"id", "fl_created_xid"}}}}
	d, e := difference(b, a)
	if e != nil {
		t.Fatal(e)
	}
	if len(d["removed"].(document)["relations"].([]any)) != 0 || len(d["added"].(document)["relations"].([]any)) != 0 || len(d["changed"].(document)["relations"].([]any)) != 1 {
		t.Fatal("modified record disguised as remove/add")
	}
}

func TestRoleDeltaBindsExactDescriptors(t *testing.T) {
	before := document{"profile": "ordinary_app_acl", "relation_acl": []any{}}
	after := document{"profile": "ordinary_app_acl", "relation_acl": []any{object{"object": object{"class": "relation", "scope": "application", "name": "tenants"}, "acl_is_null": true, "entries": []any{}, "default_entries": []any{}}}}
	d, e := roleDifference(before, after)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := canonical(before)
	a, _ := canonical(after)
	if d["before_sha256"] != digest(b) || d["after_sha256"] != digest(a) || d["before_sha256"] == d["after_sha256"] {
		t.Fatal("role delta did not bind exact descriptor bytes")
	}
}

func TestDependencyMultisetRejectsConflictingContents(t *testing.T) {
	id := object{"class": "relation", "scope": "application", "name": "tenants"}
	a := object{"catalog": "pg_depend", "object": id, "referenced": id, "kind": "n"}
	grouped, e := dependencyMultiset([]any{a, a})
	if e != nil || len(grouped) != 1 || grouped[0].(object)["multiplicity"] != 2 {
		t.Fatal("dependency multiplicity lost")
	}
	bad := object{"catalog": "pg_depend", "object": id, "referenced": id, "kind": "n", "unexpected": "different"}
	if _, e := dependencyMultiset([]any{a, bad}); e == nil {
		t.Fatal("conflicting same-identity dependency accepted")
	}
	one, e := dependencyMultiset([]any{a})
	if e != nil {
		t.Fatal(e)
	}
	d, e := difference(document{"dependencies": one}, document{"dependencies": grouped})
	if e != nil || len(d["changed"].(document)["dependencies"].([]any)) != 1 {
		t.Fatal("multiplicity change misclassified")
	}
	if sortRecords([]any{a, a}) == nil {
		t.Fatal("non-dependency duplicate accepted")
	}
}

func TestReferenceSubobjectClosed(t *testing.T) {
	r := &resolver{refs: map[refKey]object{{classRelation, 123, 0}: {"class": "relation", "scope": "application", "name": "tenants"}}, attributes: map[uint32]map[uint32]string{123: {1: "id"}}}
	if _, e := r.reference(refKey{classRelation, 123, 1}); e != nil {
		t.Fatal(e)
	}
	for _, k := range []refKey{{classRelation, 123, 9}, {classFunction, 123, 1}, {classRole, 123, 1}} {
		if _, e := r.reference(k); e == nil {
			t.Fatal("unknown subobject accepted")
		}
	}
	if _, e := r.normal(object{"$ref": []any{json.Number("1259"), json.Number("123"), json.Number("-1")}}); e == nil {
		t.Fatal("negative subobject accepted")
	}
}

func TestNormalizedDependenciesRejectRawEdgeAliasing(t *testing.T) {
	id := object{"class": "relation", "scope": "application", "name": "tenants"}
	// Deliberately bypass registration to simulate a resolver regression. The
	// dependency guard must reject aliasing before identical symbols are grouped.
	r := &resolver{refs: map[refKey]object{
		{classRelation, 123, 0}: id,
		{classRelation, 124, 0}: id,
	}}
	edge := func(oid string) any {
		ref := object{"$ref": []any{"1259", oid, "0"}}
		return object{"catalog": "pg_depend", "object": ref, "referenced": ref, "kind": "n"}
	}
	if _, e := normalizeRecords(r, true, []any{edge("123"), edge("124")}); e == nil {
		t.Fatal("distinct raw edges merged into a symbolic multiplicity")
	}
	grouped, e := normalizeRecords(r, true, []any{edge("123"), edge("123")})
	if e != nil || len(grouped) != 1 || grouped[0].(object)["multiplicity"] != 2 {
		t.Fatal("identical raw dependency multiplicity lost")
	}
}

func TestSymbolRegistrationIsInjective(t *testing.T) {
	r := &resolver{refs: map[refKey]object{}}
	id := object{"class": "relation", "scope": "application", "name": "tenants"}
	if r.register(refKey{classRelation, 123, 0}, id) != nil {
		t.Fatal("first symbol")
	}
	if r.register(refKey{classRelation, 124, 0}, id) == nil {
		t.Fatal("different raw identity aliased")
	}
	if r.register(refKey{classRelation, 123, 0}, object{"class": "relation", "scope": "application", "name": "other"}) == nil {
		t.Fatal("raw identity overwritten")
	}
}

func TestOutputNoOverwrite(t *testing.T) {
	atRoot(t)
	for _, p := range []string{"", "docs/expected", ".local/c0-descriptor-candidates/../outside", ".local/c0-descriptor-candidates/.hidden"} {
		if _, e := outputPath(p); e == nil {
			t.Fatal("invalid output accepted")
		}
	}
	p, e := randomProfile(false)
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(".local/c0-descriptor-candidates", "qa-no-overwrite-"+p.schema[6:38])
	if os.MkdirAll(dir, 0700) != nil {
		t.Fatal("owned output directory")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if _, e := outputPath(dir); e == nil {
		t.Fatal("existing output accepted")
	}
	link := dir + "-link"
	if os.Symlink(dir, link) != nil {
		t.Fatal("owned output symlink")
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if _, e := outputPath(link); e == nil {
		t.Fatal("output symlink accepted")
	}
}

func TestRealDescriptorReproducibility(t *testing.T) {
	raw := os.Getenv("C0_DESCRIPTOR_DATABASE_URL")
	if raw == "" {
		t.Skip("requires explicit dedicated database; no descriptor acceptance")
	}
	atRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	var dirs []string
	for _, label := range []string{"one", "two"} {
		p, e := randomProfile(false)
		if e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(".local/c0-descriptor-candidates", "qa-"+label+"-"+p.schema[6:38])
		dirs = append(dirs, dir)
		if e = run(ctx, dir, raw); e != nil {
			t.Fatal(e)
		}
	}
	entries, e := os.ReadDir(dirs[0])
	if e != nil || len(entries) != 10 {
		t.Fatal("artifact file set")
	}
	for _, entry := range entries {
		a, e := os.ReadFile(filepath.Join(dirs[0], entry.Name()))
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(filepath.Join(dirs[1], entry.Name()))
		if e != nil || string(a) != string(b) {
			t.Fatalf("canonical two-run difference in %s", entry.Name())
		}
		if fi, e := os.Stat(filepath.Join(dirs[0], entry.Name())); e != nil || fi.Mode().Perm() != 0600 {
			t.Fatal("artifact permissions")
		}
	}
	t.Log("two isolated runs identical; artifacts", dirs[0], dirs[1])
}

func TestRealFailureCleanup(t *testing.T) {
	raw := os.Getenv("C0_DESCRIPTOR_DATABASE_URL")
	if raw == "" {
		t.Skip("requires explicit dedicated database")
	}
	atRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, e := databaseConfig(raw)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal("connect")
	}
	defer admin.Close()
	if _, e = verifyDatabase(ctx, admin); e != nil {
		t.Fatal(e)
	}
	count := func() int {
		var n int
		if admin.QueryRow(ctx, `SELECT (SELECT count(*) FROM pg_namespace WHERE left(nspname,6)='flcdg_')+(SELECT count(*) FROM pg_roles WHERE left(rolname,6)='flcdg_')`).Scan(&n) != nil {
			t.Fatal("audit")
		}
		return n
	}
	before := count()
	in, e := loadInputs()
	if e != nil {
		t.Fatal(e)
	}
	in.ddl = []byte("SELECT no_such_fixed_descriptor_fixture_function()")
	if _, e = installProfile(ctx, admin, cfg, in, false); e == nil {
		t.Fatal("expected injected installation failure")
	}
	if count() != before {
		t.Fatal("failed profile leaked owned objects")
	}
}
