package procurementrequestbirth

import (
	"regexp"
	"strings"
	"testing"
)

func TestA1FixedMaterialsAndSurface(t *testing.T) {
	d, err := Digests()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.SQLSHA256) != 64 || len(d.PermissionsSHA256) != 64 {
		t.Fatal("unfixed material digests")
	}
	b := Bindings{Schema: "isolated", App: "actual_app", Writer: "no_login_writer", AppOID: 42001, WriterOID: 42002}
	sql, err := SQL(b)
	if err != nil {
		t.Fatal(err)
	}
	acl, err := Permissions(b)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(sql, "CREATE FUNCTION "); n != 7 {
		t.Fatalf("functions=%d", n)
	}
	if n := strings.Count(sql, "SECURITY DEFINER"); n != 1 {
		t.Fatalf("definers=%d", n)
	}
	if n := strings.Count(sql, "CREATE TRIGGER ") + strings.Count(sql, "CREATE CONSTRAINT TRIGGER "); n != 16 {
		t.Fatalf("triggers=%d", n)
	}
	if n := strings.Count(sql, "DEFERRABLE INITIALLY DEFERRED"); n != 5 {
		t.Fatalf("deferred=%d", n)
	}
	for _, v := range []string{"pg_trigger_depth", "set_config(", "CREATE TABLE", "pp_account_", "pp_attempt_", "pp_customer_", "EXCEPTION WHEN", "ON CONFLICT", "DISABLE TRIGGER", "SET CONSTRAINTS"} {
		if strings.Contains(sql, v) {
			t.Fatalf("unexpected writer capability %q", v)
		}
	}
	if strings.Contains(acl, "ALL TABLES") || strings.Contains(acl, "GRANT SELECT ON TABLE") || strings.Contains(acl, "GRANT INSERT ON TABLE") {
		t.Fatal("blanket ACL")
	}
	// Only capture defines business DML; its exact effect order is contractual.
	capture := strings.Split(strings.Split(sql, ".pp_rb_capture_request_v1()")[1], "END $$;")[0]
	prev := -1
	for _, v := range []string{"INSERT INTO \"isolated\".pp_request_origins(", "INSERT INTO \"isolated\".pp_request_heads(", "UPDATE \"isolated\".request_records SET pp_origin_root_id=", "INSERT INTO \"isolated\".pp_root_steps(", "INSERT INTO \"isolated\".pp_operation_roots("} {
		at := strings.Index(capture, v)
		if at <= prev {
			t.Fatalf("capture order missing/reversed: %s", v)
		}
		prev = at
	}
	for _, fn := range []string{"pp_rb_validate_request_v1(text)", "pp_rb_validate_root_v1(text)"} {
		if !strings.Contains(acl, "GRANT EXECUTE ON FUNCTION \"isolated\"."+fn+" TO \"actual_app\"") {
			t.Fatal("missing public readonly leaf")
		}
	}
	if n := len(regexp.MustCompile(`GRANT EXECUTE ON FUNCTION .* TO "actual_app";`).FindAllString(acl, -1)); n != 2 {
		t.Fatalf("APP executable functions=%d", n)
	}
}

func TestA1BindingRenderingDoesNotReparseIdentifiers(t *testing.T) {
	for _, v := range []string{"a'b", `a"b`, "a\\b", "中文", "$$", "$moni_rb_0$", "{{MONI_APP}}"} {
		b := Bindings{Schema: v, App: "app_" + v, Writer: "writer_" + v, AppOID: 7, WriterOID: 8}
		for _, render := range []func(Bindings) (string, error){SQL, Permissions} {
			s, err := render(b)
			if err != nil {
				t.Fatal(v, err)
			}
			if strings.Contains(s, "AS $$") {
				t.Fatal("unreplaced unsafe outer dollar delimiter")
			}
			if !strings.Contains(s, "pg_catalog.convert_from(pg_catalog.decode('") {
				t.Fatal("missing safely encoded literal")
			}
		}
	}
	for _, b := range []Bindings{{}, {Schema: "x", App: "a", Writer: "w", AppOID: 1}, {Schema: "x", App: "a", Writer: "a", AppOID: 1, WriterOID: 2}, {Schema: "x", App: "a", Writer: "w", AppOID: 1, WriterOID: 1}, {Schema: "bad\x00name", App: "a", Writer: "w", AppOID: 1, WriterOID: 2}, {Schema: strings.Repeat("x", 64), App: "a", Writer: "w", AppOID: 1, WriterOID: 2}, {Schema: string([]byte{0xff}), App: "a", Writer: "w", AppOID: 1, WriterOID: 2}} {
		if _, err := SQL(b); err != ErrBindings {
			t.Fatalf("accepted invalid binding: %+v %v", b, err)
		}
		if _, err := Permissions(b); err != ErrBindings {
			t.Fatalf("accepted invalid ACL binding: %+v %v", b, err)
		}
	}
}
