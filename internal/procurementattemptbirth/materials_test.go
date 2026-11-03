package procurementattemptbirth

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func testBindings() Bindings {
	return Bindings{Schema: "ab_static", App: "ab_app", Writer: "ab_writer", AppOID: 1234, WriterOID: 1235}
}

func TestCompleteMaterialPinsAndAssembly(t *testing.T) {
	pins, err := Digests()
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 15 {
		t.Fatalf("material count=%d", len(pins))
	}
	if pins[0].Name != "preflight-v1.sql" || pins[11].Name != "triggers-v1.sql" || pins[12].Name != "permissions-v1.sql" {
		t.Fatal("changed canonical assembly order")
	}
	pins[0].SHA256 = "caller mutation"
	again, err := Digests()
	if err != nil || again[0].SHA256 == pins[0].SHA256 {
		t.Fatal("digest inventory is not owned")
	}
	sql, err := SQL(testBindings())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(sql, "CREATE FUNCTION ") != 38 || strings.Count(sql, "CREATE TRIGGER ")+strings.Count(sql, "CREATE CONSTRAINT TRIGGER ") != 68 {
		t.Fatal("incomplete fixed function/trigger candidate")
	}
	if strings.Contains(sql, "OWNER TO") || strings.Contains(sql, "GRANT ") {
		t.Fatal("SQL and permission phase mixed")
	}
	for _, descriptor := range []func() ([]byte, error){RelationDescriptor, CatalogDescriptor} {
		raw, e := descriptor()
		if e != nil || !json.Valid(raw) {
			t.Fatal("invalid pinned descriptor", e)
		}
		raw[0] = 0
		copy, e := descriptor()
		if e != nil || !json.Valid(copy) {
			t.Fatal("descriptor returned shared storage", e)
		}
	}
}

func TestRendererQuotesAdversarialBindingsOnce(t *testing.T) {
	for _, schema := range []string{"a\";DROP SCHEMA public;--", "$$", "$moni_ab_0$", "$moni_ab_0$$moni_ab_1$", "{{MONI_APP}}", "中文schema"} {
		b := testBindings()
		b.Schema = schema
		b.App = "app\"$$"
		b.Writer = "{{MONI_SCHEMA}}"
		sql, e := SQL(b)
		if e != nil {
			t.Fatal(e)
		}
		acl, e := Permissions(b)
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(sql, "CREATE TABLE "+pgx.Identifier{schema}.Sanitize()+".") || !strings.Contains(acl, pgx.Identifier{b.Writer}.Sanitize()) {
			t.Fatal("lost identifier quoting")
		}
		if schema == "{{MONI_APP}}" && !strings.Contains(sql, "\"{{MONI_APP}}\"") {
			t.Fatal("replacement recursively interpreted inserted name")
		}
		if strings.Contains(schema, "$moni_ab_0$") && !strings.Contains(sql, "AS $moni_ab_2$") && schema != "$moni_ab_0$" {
			t.Fatal("delimiter collision not avoided")
		}
	}
}

func TestRendererRejectsInvalidBindingsBeforeRendering(t *testing.T) {
	for _, change := range []func(*Bindings){func(b *Bindings) { b.Schema = "" }, func(b *Bindings) { b.Schema = strings.Repeat("a", 64) }, func(b *Bindings) { b.App = "a\x00b" }, func(b *Bindings) { b.Writer = string([]byte{255}) }, func(b *Bindings) { b.App = b.Writer }, func(b *Bindings) { b.AppOID = 0 }, func(b *Bindings) { b.WriterOID = b.AppOID }} {
		b := testBindings()
		change(&b)
		if sql, e := SQL(b); sql != "" || !errors.Is(e, ErrBindings) {
			t.Fatal("bad SQL bindings accepted")
		}
		if sql, e := Permissions(b); sql != "" || !errors.Is(e, ErrBindings) {
			t.Fatal("bad ACL bindings accepted")
		}
	}
}
