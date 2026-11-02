package fundinglotproof

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestStrictRawRejections(t *testing.T) {
	raw := fixture(t, "ordinary.json")
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"nil", nil}, {"empty", []byte{}}, {"bom", append([]byte{0xef, 0xbb, 0xbf}, raw...)},
		{"leading_space", append([]byte{' '}, raw...)}, {"tail_lf", append(bytes.Clone(raw), '\n')},
		{"tail_value", append(bytes.Clone(raw), []byte(`{}`)...)},
		{"invalid_utf8", bytes.Replace(raw, []byte("db.example"), []byte{0xff}, 1)},
		{"duplicate_key", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":2,"proof_version":2`), 1)},
		{"decoded_duplicate_key", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":2,"proof_\u0076ersion":2`), 1)},
		{"unknown_key", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":2,"other":1`), 1)},
		{"missing_key", bytes.Replace(raw, []byte(`"proof_version":2,`), nil, 1)},
		{"missing_nullable", bytes.Replace(raw, []byte(`"sandbox":null,`), nil, 1)},
		{"wrong_field_case", bytes.Replace(raw, []byte(`"proof_version"`), []byte(`"Proof_Version"`), 1)},
		{"wrong_order", bytes.Replace(raw, []byte(`"proof_version":2,"mode":"ordinary"`), []byte(`"mode":"ordinary","proof_version":2`), 1)},
		{"nested_unknown", bytes.Replace(raw, []byte(`"host":"db.example"`), []byte(`"host":"db.example","extra":1`), 1)},
		{"nested_missing", bytes.Replace(raw, []byte(`"host":"db.example",`), nil, 1)},
		{"manifest_unknown", bytes.Replace(raw, []byte(`"name":"001_core.sql"`), []byte(`"name":"001_core.sql","extra":1`), 1)},
		{"bad_escape", bytes.Replace(raw, []byte(`db.example`), []byte(`db\x20example`), 1)},
		{"bare_control", bytes.Replace(raw, []byte(`db.example`), []byte("db\nexample"), 1)},
		{"isolated_low_surrogate", bytes.Replace(raw, []byte(`db.example`), []byte(`\udc00`), 1)},
		{"isolated_high_surrogate", bytes.Replace(raw, []byte(`db.example`), []byte(`\ud800`), 1)},
		{"high_wrong_partner", bytes.Replace(raw, []byte(`db.example`), []byte(`\ud800\u0061`), 1)},
		{"short_unicode_escape", bytes.Replace(raw, []byte(`db.example`), []byte(`\u123`), 1)},
		{"noncanonical_unicode_escape", bytes.Replace(raw, []byte(`db.example`), []byte(`\u0064b.example`), 1)},
		{"escaped_slash", bytes.Replace(raw, []byte(`funding-lots-runtime/v2`), []byte(`funding-lots-runtime\/v2`), 1)},
		{"nonnullable_null", bytes.Replace(raw, []byte(`"domain":"moni-aigate/funding-lots-runtime/v2"`), []byte(`"domain":null`), 1)},
		{"null_manifest", bytes.Replace(raw, []byte(`"migration_manifest":[{"name":"001_core.sql","sha256":"`+strings.Repeat("1", 64)+`"}]`), []byte(`"migration_manifest":null`), 1)},
		{"object_manifest", bytes.Replace(raw, []byte(`"migration_manifest":[`), []byte(`"migration_manifest":{`), 1)},
		{"array_database", bytes.Replace(raw, []byte(`"database":{`), []byte(`"database":[`), 1)},
		{"string_integer", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":"2"`), 1)},
		{"bool_integer", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":true`), 1)},
		{"negative_integer", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":-2`), 1)},
		{"negative_zero", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":-0`), 1)},
		{"fraction", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":2.0`), 1)},
		{"exponent", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":2e0`), 1)},
		{"leading_zero", bytes.Replace(raw, []byte(`"proof_version":2`), []byte(`"proof_version":02`), 1)},
		{"uint32_overflow", bytes.Replace(raw, []byte(`"catalog_descriptor_version":2`), []byte(`"catalog_descriptor_version":4294967296`), 1)},
		{"uint16_overflow", bytes.Replace(raw, []byte(`"port":5432`), []byte(`"port":65536`), 1)},
		{"long_integer", bytes.Replace(raw, []byte(`"oid":100`), []byte(`"oid":`+strings.Repeat("9", 1000)), 1)},
		{"over_total_bytes", bytes.Repeat([]byte{' '}, maxV2Bytes+1)},
		{"at_total_bytes_invalid", bytes.Repeat([]byte{' '}, maxV2Bytes)},
	} {
		t.Run(tc.name, func(t *testing.T) { rejected(t, tc.raw) })
	}

	// Each of the 21 fields is mandatory; only the three declared union fields
	// can be null. Mutate a tree to cover fields without using codec internals.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 21 {
		t.Fatal("golden field count")
	}
	for key := range fields {
		t.Run("missing_"+key, func(t *testing.T) {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(raw, &m)
			delete(m, key)
			b, _ := json.Marshal(m)
			rejected(t, b)
		})
	}
}

func TestSandboxEveryPinMismatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*V2Input)
	}{
		{"database_host", func(p *V2Input) { p.Database.Host = "localhost" }},
		{"database_port", func(p *V2Input) { p.Database.Port++ }},
		{"database_name", func(p *V2Input) { p.Database.Name += "x" }},
		{"database_oid", func(p *V2Input) { p.Database.OID++ }},
		{"app_schema_name", func(p *V2Input) { p.AppSchema.Name += "x" }},
		{"app_schema_oid", func(p *V2Input) { p.AppSchema.OID += 1000 }},
		{"app_namespace_owner", func(p *V2Input) { p.AppSchema.NamespaceOwner.OID += 1000 }},
		{"app_object_owner", func(p *V2Input) {
			p.AppSchema.ObjectOwner.OID += 1000
			p.AppSchema.NamespaceOwner = p.AppSchema.ObjectOwner
		}},
		{"app_owner_name", func(p *V2Input) {
			p.AppSchema.ObjectOwner.Name += "x"
			p.AppSchema.NamespaceOwner = p.AppSchema.ObjectOwner
		}},
		{"app_runtime_name", func(p *V2Input) { p.AppRole.Name += "x" }},
		{"app_runtime_oid", func(p *V2Input) { p.AppRole.OID += 1000 }},
		{"mock_schema_name", func(p *V2Input) { p.Sandbox.MockSchema.Name += "x" }},
		{"mock_schema_oid", func(p *V2Input) { p.Sandbox.MockSchema.OID += 1000 }},
		{"mock_namespace_owner", func(p *V2Input) { p.Sandbox.MockSchema.NamespaceOwner.OID += 1000 }},
		{"mock_owner_name", func(p *V2Input) {
			p.Sandbox.MockSchema.ObjectOwner.Name += "x"
			p.Sandbox.MockSchema.NamespaceOwner = p.Sandbox.MockSchema.ObjectOwner
		}},
		{"mock_owner_oid", func(p *V2Input) {
			p.Sandbox.MockSchema.ObjectOwner.OID += 1000
			p.Sandbox.MockSchema.NamespaceOwner = p.Sandbox.MockSchema.ObjectOwner
		}},
		{"mock_runtime_name", func(p *V2Input) { p.Sandbox.MockRole.Name += "x" }},
		{"mock_runtime_oid", func(p *V2Input) { p.Sandbox.MockRole.OID += 1000 }},
		{"receiver_schema_name", func(p *V2Input) { p.Sandbox.ReceiverSchema.Name += "x" }},
		{"receiver_schema_oid", func(p *V2Input) { p.Sandbox.ReceiverSchema.OID += 1000 }},
		{"receiver_namespace_owner", func(p *V2Input) { p.Sandbox.ReceiverSchema.NamespaceOwner.OID += 1000 }},
		{"receiver_owner_name", func(p *V2Input) {
			p.Sandbox.ReceiverSchema.ObjectOwner.Name += "x"
			p.Sandbox.ReceiverSchema.NamespaceOwner = p.Sandbox.ReceiverSchema.ObjectOwner
		}},
		{"receiver_owner_oid", func(p *V2Input) {
			p.Sandbox.ReceiverSchema.ObjectOwner.OID += 1000
			p.Sandbox.ReceiverSchema.NamespaceOwner = p.Sandbox.ReceiverSchema.ObjectOwner
		}},
		{"receiver_runtime_name", func(p *V2Input) { p.Sandbox.ReceiverRole.Name += "x" }},
		{"receiver_runtime_oid", func(p *V2Input) { p.Sandbox.ReceiverRole.OID += 1000 }},
		{"worker_name", func(p *V2Input) { p.Sandbox.WorkerRole.Name += "x" }},
		{"worker_oid", func(p *V2Input) { p.Sandbox.WorkerRole.OID += 1000 }},
		{"projector_name", func(p *V2Input) { p.ProjectorRole.Name += "x" }},
		{"projector_oid", func(p *V2Input) { p.ProjectorRole.OID += 1000 }},
		{"duplicate_role", func(p *V2Input) { p.Sandbox.MockRole = p.AppRole }},
		{"duplicate_schema", func(p *V2Input) { p.Sandbox.ReceiverSchema = p.Sandbox.MockSchema }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inputFixture(t, "sandbox.json")
			tc.mutate(&p)
			got, err := EncodeV2(p)
			if got != nil || err != ErrCanonical {
				t.Fatal("pin mismatch accepted")
			}
		})
	}
}

func TestV1OriginalCanonicalAndSemanticRejections(t *testing.T) {
	raw := fixture(t, "predecessor_v1.json")
	for _, tc := range []struct{ name, old, replacement string }{
		{"proof_version", `"proof_version":1`, `"proof_version":2`},
		{"protocol_version", `"protocol_version":1`, `"protocol_version":2`},
		{"v1_null", `"proof_version":1`, `"proof_version":null`},
		{"v1_unknown", `"proof_version":1`, `"proof_version":1,"extra":1`},
		{"v1_duplicate", `"proof_version":1`, `"proof_version":1,"proof_version":1`},
		{"v1_integer_exponent", `"proof_version":1`, `"proof_version":1e0`},
		{"v1_isolated_surrogate", `merchant:测试`, `merchant:\ud800`},
		{"v1_uuid_uppercase", `12345678-1234-4234-8234-123456789abc`, `12345678-1234-4234-8234-123456789ABC`},
		{"v1_nil_uuid", `12345678-1234-4234-8234-123456789abc`, `00000000-0000-0000-0000-000000000000`},
		{"credential_merchant", `merchant:测试`, `sk-not-a-merchant`},
		{"label_control", `测试 <A&B>`, `测试\n<A&B>`},
		{"invalid_label_url", `测试 <A&B>`, `https://example`},
		{"owner_login", `"oid":201,"login":false`, `"oid":201,"login":true`},
		{"runtime_nologin", `"oid":202,"login":true`, `"oid":202,"login":false`},
		{"worker_nologin", `"oid":207,"login":true`, `"oid":207,"login":false`},
		{"projector_nologin", `"oid":208,"login":true`, `"oid":208,"login":false`},
		{"schema_suffix", `payment_mock_0123456789abcdef0123456789abcdef`, `payment_mock_0123456789abcdef0123456789abcdee`},
		{"role_format", `golden_worker`, `Golden_worker`},
		{"nonce_digest", strings.Repeat("1", 64), strings.Repeat("A", 64)},
		{"nonloopback_origin", `http://127.0.0.1:19081`, `http://localhost:19081`},
		{"origin_path", `http://127.0.0.1:19081`, `http://127.0.0.1:19081/`},
		{"origin_query", `http://127.0.0.1:19081`, `http://127.0.0.1:19081?`},
		{"origin_fragment", `http://127.0.0.1:19081`, `http://127.0.0.1:19081#`},
		{"origin_userinfo", `http://127.0.0.1:19081`, `http://user@127.0.0.1:19081`},
		{"origin_port_spelling", `http://127.0.0.1:19081`, `http://127.0.0.1:019081`},
		{"duplicate_origin", `http://127.0.0.1:19082`, `http://127.0.0.1:19081`},
		{"callback_path", `/api/payments/sandbox/notifications`, `/wrong`},
		{"key_id", `A-key`, `bad id`},
		{"key_order", `z_key`, `0_key`},
		{"key_digest", strings.Repeat("4", 64), strings.Repeat("X", 64)},
		{"zero_instant", `0001-01-01T00:00:00.000001Z`, `0001-01-01T00:00:00.000000Z`},
		{"bad_instant_precision", `2001-02-03T04:05:06.123456Z`, `2001-02-03T04:05:06.123Z`},
		{"reversed_key_times", `9999-12-31T23:59:59.999999Z`, `2009-12-31T23:59:59.999999Z`},
		{"noncanonical_html_escape", `<A&B>`, `\u003cA\u0026B\u003e`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := bytes.Replace(raw, []byte(tc.old), []byte(tc.replacement), 1)
			if bytes.Equal(changed, raw) {
				t.Fatal("mutation did not apply")
			}
			p := inputFixture(t, "sandbox.json")
			setV1(&p, changed)
			got, err := EncodeV2(p)
			if got != nil || err != ErrCanonical {
				t.Fatal("invalid v1 accepted")
			}
		})
	}
	for _, extra := range [][]byte{append(bytes.Clone(raw), '\n'), append([]byte{' '}, raw...), bytes.Repeat([]byte{' '}, 65537)} {
		p := inputFixture(t, "sandbox.json")
		setV1(&p, extra)
		if got, err := EncodeV2(p); got != nil || err != ErrCanonical {
			t.Fatal("noncanonical/oversized v1 accepted")
		}
	}
}

func TestPredecessorDigestAndBase64Rejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*V2Input)
	}{
		{"wrong_sha", func(p *V2Input) { *p.PredecessorPaymentDigest = strings.Repeat("0", 64) }},
		{"upper_sha", func(p *V2Input) { *p.PredecessorPaymentDigest = strings.Repeat("A", 64) }},
		{"padding", func(p *V2Input) { *p.PredecessorPaymentCanonicalBase64URL += "=" }},
		{"newline", func(p *V2Input) { *p.PredecessorPaymentCanonicalBase64URL += "\n" }},
		{"invalid_alphabet", func(p *V2Input) { *p.PredecessorPaymentCanonicalBase64URL += "+" }},
		{"empty", func(p *V2Input) { *p.PredecessorPaymentCanonicalBase64URL = "" }},
		{"over_base64_limit", func(p *V2Input) { *p.PredecessorPaymentCanonicalBase64URL = strings.Repeat("A", 87383) }},
		{"decoded_65537", func(p *V2Input) { setV1(p, bytes.Repeat([]byte{'a'}, 65537)) }},
		{"changed_original_only", func(p *V2Input) {
			*p.PredecessorPaymentCanonicalBase64URL = base64.RawURLEncoding.EncodeToString([]byte(`{}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inputFixture(t, "sandbox.json")
			tc.mutate(&p)
			got, err := EncodeV2(p)
			if got != nil || err != ErrCanonical {
				t.Fatal("base64/original tamper accepted")
			}
		})
	}
}

func TestJSONNodeAndDepthBudget(t *testing.T) {
	parse := func(raw string, budget int) bool {
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		_, ok := readV2Value(d, 1, &budget)
		return ok
	}
	if !parse(`{"first":null,"second":null}`, 3) || parse(`{"first":null,"second":null}`, 2) {
		t.Fatal("keys incorrectly counted as nodes")
	}
	for _, count := range []int{8191, 8192} {
		raw := "[" + strings.TrimSuffix(strings.Repeat("null,", count), ",") + "]"
		if parse(raw, 8192) != (count == 8191) {
			t.Fatal("node boundary", count)
		}
		rejected(t, []byte(raw))
	}
	for _, depth := range []int{12, 13} {
		raw := strings.Repeat("[", depth) + strings.Repeat("]", depth)
		if parse(raw, 8192) != (depth == 12) {
			t.Fatal("root depth boundary", depth)
		}
		rejected(t, []byte(raw))
	}
}

func TestProductionOnlyStandardLibraryAndPrivateCanonical(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range tree.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := build.Default.Import(path, "", build.FindOnly)
			if err != nil || !pkg.Goroot {
				t.Fatalf("non-stdlib production import %q: %v", path, err)
			}
		}
	}
	typeOf := reflect.TypeOf(CanonicalV2{})
	for i := 0; i < typeOf.NumField(); i++ {
		if typeOf.Field(i).PkgPath == "" {
			t.Fatal("exported canonical field")
		}
	}
	// No public type declaration is permitted to disguise a writable state field.
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			decl, ok := n.(*ast.FuncDecl)
			if ok && decl.Name.Name == "init" {
				t.Error("codec has an init side effect")
			}
			return true
		})
	}
}

func FuzzDecodeV2(f *testing.F) {
	for _, name := range []string{"ordinary.json", "sandbox.json", "sandbox_128keys.json"} {
		f.Add(fixture(f, name))
	}
	for _, raw := range []string{"", `null`, `{}`, `{"domain":"x"}`, strings.Repeat("[", 13)} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := DecodeV2(raw)
		if err != nil {
			if err != ErrCanonical || p != nil {
				t.Fatal("failure leaked partial result")
			}
			return
		}
		if p == nil || !bytes.Equal(p.Bytes(), raw) || testSHA(raw) != p.SHA256() {
			t.Fatal("invalid canonical result")
		}
		bindings, err := p.Bindings()
		if err != nil {
			t.Fatal(err)
		}
		again, err := EncodeV2(bindings)
		if err != nil || again.SHA256() != p.SHA256() {
			t.Fatal("unstable accepted format")
		}
	})
}

func TestV1KeyCountBoundaries(t *testing.T) {
	for _, count := range []int{0, 129} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			raw := fixture(t, "predecessor_1keys.json")
			var p v1Proof
			if json.Unmarshal(raw, &p) != nil {
				t.Fatal("fixture")
			}
			key := p.Keys[0]
			p.Keys = make([]v1KeyPin, count)
			for i := range p.Keys {
				p.Keys[i] = key
				p.Keys[i].ID = fmt.Sprintf("key_%03d", i)
			}
			bad, err := v1CanonicalProof(p)
			if err != nil {
				t.Fatal(err)
			}
			v2 := inputFixture(t, "sandbox.json")
			setV1(&v2, bad)
			if proof, err := EncodeV2(v2); proof != nil || err != ErrCanonical {
				t.Fatal("key count accepted")
			}
		})
	}
}

func TestNoFixtureSecrets(t *testing.T) {
	// Fixed files are inputs, not private manifests or an installed environment.
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw := fixture(t, entry.Name())
		for _, forbidden := range []string{`"password"`, `"material_base64_url"`, `"runtime_dsn_file"`, `"api_token_file"`} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatal("unexpected credential fixture field")
			}
		}
	}
}
