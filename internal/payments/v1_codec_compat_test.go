package payments

// Test-only bridge. Both historical production codecs remain private and
// unchanged; the format leaf imports neither legacy runtime nor maintenance.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

func v1CodecCompatGolden(t testing.TB) ([]byte, runtimeProof) {
	t.Helper()
	raw, err := os.ReadFile("../maintenancetrust/testdata/predecessor_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var p runtimeProof
	if runtimeStrictDecode(raw, &p) != nil || runtimeValidateProof(p) != nil {
		t.Fatal("independent historical golden rejected")
	}
	return raw, p
}
func v1CodecCompatDigest(raw []byte) string { return runtimeSHA(raw) }
func v1CodecCompatJSON(t testing.TB, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if e.Encode(v) != nil {
		t.Fatal("independent JSON encoder failed")
	}
	return bytes.Clone(bytes.TrimSuffix(b.Bytes(), []byte{'\n'}))
}
func v1CodecCompatCopy(p runtimeProof) runtimeProof {
	p.Keys = append([]runtimeKeyPin(nil), p.Keys...)
	return p
}
func v1CodecCompatCanonical(t testing.TB, p runtimeProof) []byte {
	t.Helper()
	raw, err := runtimeCanonicalProof(p)
	if err != nil {
		t.Fatal("historical encoder failed")
	}
	return raw
}
func v1CodecCompatInput(p runtimeProof) fundinglotproof.V1Input {
	role := func(r runtimeRolePin) fundinglotproof.V1Role { return fundinglotproof.V1Role(r) }
	schema := func(s runtimeSchemaPin) fundinglotproof.V1Schema {
		return fundinglotproof.V1Schema{Name: s.Name, OID: s.OID, Owner: role(s.Owner), Runtime: role(s.Runtime)}
	}
	keys := make([]fundinglotproof.V1KeyPin, len(p.Keys))
	for i, k := range p.Keys {
		keys[i] = fundinglotproof.V1KeyPin(k)
	}
	return fundinglotproof.V1Input{
		ProofVersion: p.ProofVersion, ProtocolVersion: p.ProtocolVersion,
		EnvironmentID: p.EnvironmentID, MerchantID: p.MerchantID, PayeeLabel: p.PayeeLabel,
		Database: fundinglotproof.V1Database(p.Database), App: schema(p.App), Mock: schema(p.Mock), Receiver: schema(p.Receiver),
		Worker: role(p.Worker), Projector: role(p.Projector), AppNonceDigest: p.AppNonceDigest,
		WorkerNonceDigest: p.WorkerNonceDigest, APITokenDigest: p.APITokenDigest,
		APIOrigin: p.APIOrigin, ControlOrigin: p.ControlOrigin, AppListenOrigin: p.AppListenOrigin, CallbackURL: p.CallbackURL, Keys: keys,
	}
}
func v1CodecCompatOld(p fundinglotproof.V1Bindings) runtimeProof {
	role := func(r fundinglotproof.V1Role) runtimeRolePin { return runtimeRolePin(r) }
	schema := func(s fundinglotproof.V1Schema) runtimeSchemaPin {
		return runtimeSchemaPin{Name: s.Name, OID: s.OID, Owner: role(s.Owner), Runtime: role(s.Runtime)}
	}
	keys := make([]runtimeKeyPin, len(p.Keys))
	for i, k := range p.Keys {
		keys[i] = runtimeKeyPin(k)
	}
	return runtimeProof{
		ProofVersion: p.ProofVersion, ProtocolVersion: p.ProtocolVersion,
		EnvironmentID: p.EnvironmentID, MerchantID: p.MerchantID, PayeeLabel: p.PayeeLabel,
		Database: runtimeDatabasePin(p.Database), App: schema(p.App), Mock: schema(p.Mock), Receiver: schema(p.Receiver),
		Worker: role(p.Worker), Projector: role(p.Projector), AppNonceDigest: p.AppNonceDigest,
		WorkerNonceDigest: p.WorkerNonceDigest, APITokenDigest: p.APITokenDigest,
		APIOrigin: p.APIOrigin, ControlOrigin: p.ControlOrigin, AppListenOrigin: p.AppListenOrigin, CallbackURL: p.CallbackURL, Keys: keys,
	}
}

// Decode acceptance includes strict parsing, semantics, and original canonical
// byte equality. Malformed raw is never repaired through a preliminary Unmarshal.
func v1CodecCompatOldAccept(raw []byte) bool {
	var p runtimeProof
	if runtimeStrictDecode(raw, &p) != nil || runtimeValidateProof(p) != nil {
		return false
	}
	canonical, err := runtimeCanonicalProof(p)
	return err == nil && bytes.Equal(canonical, raw)
}
func v1CodecCompatAssert(t testing.TB, raw []byte, want bool) {
	t.Helper()
	if v1CodecCompatOldAccept(raw) != want {
		t.Fatal("historical acceptance differs from expected boundary")
	}
	proof, err := fundinglotproof.DecodeV1(raw)
	if !want {
		if proof != nil || err != fundinglotproof.ErrCanonical {
			t.Fatal("DecodeV1 accepted invalid bytes or returned a partial result")
		}
	} else {
		if err != nil || proof == nil || !bytes.Equal(proof.Bytes(), raw) || proof.SHA256() != runtimeSHA(raw) {
			t.Fatal("DecodeV1 changed historical bytes or digest")
		}
		b, err := proof.Bindings()
		if err != nil {
			t.Fatal(err)
		}
		old := v1CodecCompatOld(b)
		if runtimeValidateProof(old) != nil || !bytes.Equal(v1CodecCompatCanonical(t, old), raw) {
			t.Fatal("new bindings do not round-trip through the original codec")
		}
	}
	// Strict parsing can accept a noncanonical but well-shaped JSON document.
	// Typed Encode must independently preserve its original semantic boundary.
	var old runtimeProof
	if runtimeStrictDecode(raw, &old) == nil {
		v1CodecCompatEncode(t, old)
	}
}
func v1CodecCompatEncode(t testing.TB, p runtimeProof) {
	t.Helper()
	valid := runtimeValidateProof(p) == nil
	input := v1CodecCompatInput(p)
	got, err := fundinglotproof.EncodeV1(input)
	if !valid {
		if got != nil || err != fundinglotproof.ErrCanonical {
			t.Fatal("EncodeV1 accepted invalid historical semantics or returned a partial result")
		}
		return
	}
	raw := v1CodecCompatCanonical(t, p)
	if err != nil || got == nil || !bytes.Equal(got.Bytes(), raw) || got.SHA256() != runtimeSHA(raw) {
		t.Fatal("EncodeV1 diverged from validated historical bytes or digest")
	}
	b, err := got.Bindings()
	if err != nil || !reflect.DeepEqual(v1CodecCompatOld(b), p) {
		t.Fatal("EncodeV1 lost a historical field")
	}
	if !v1CodecCompatOldAccept(got.Bytes()) {
		t.Fatal("original parser rejected new canonical bytes")
	}
}
func v1CodecCompatJSONPaths(raw []byte, prefix []string) [][]string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil
	}
	var out [][]string
	for key, value := range obj {
		path := append(append([]string(nil), prefix...), key)
		out = append(out, path)
		out = append(out, v1CodecCompatJSONPaths(value, path)...)
	}
	return out
}
func v1CodecCompatMutateJSON(raw []byte, path []string, operation string) []byte {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	key := path[0]
	if len(path) > 1 {
		obj[key] = v1CodecCompatMutateJSON(obj[key], path[1:], operation)
	} else {
		switch operation {
		case "missing":
			delete(obj, key)
		case "null":
			obj[key] = json.RawMessage("null")
		case "case":
			value := obj[key]
			delete(obj, key)
			obj[strings.ToUpper(key)] = value
		case "unknown":
			obj["unknown_"+key] = json.RawMessage("true")
		case "wrong_type":
			replacement := json.RawMessage("[]")
			if len(obj[key]) > 0 && obj[key][0] == '[' {
				replacement = json.RawMessage("{}")
			}
			obj[key] = replacement
		case "duplicate", "escaped_duplicate":
			encoded, _ := json.Marshal(obj)
			name, _ := json.Marshal(key)
			if operation == "escaped_duplicate" {
				name = []byte(`"\u` + fmt.Sprintf("%04x", key[0]) + key[1:] + `"`)
			}
			result := append(bytes.Clone(encoded[:len(encoded)-1]), ',')
			result = append(result, name...)
			result = append(result, ':')
			result = append(result, obj[key]...)
			return append(result, '}')
		}
	}
	result, _ := json.Marshal(obj)
	return result
}
func TestV1CodecCompatibilityAccepted(t *testing.T) {
	raw, original := v1CodecCompatGolden(t)
	if v1CodecCompatDigest(raw) != "1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c" {
		t.Fatal("independent historical golden changed")
	}
	t.Run("unchanged_historical_golden", func(t *testing.T) { v1CodecCompatAssert(t, raw, true) })
	for _, tc := range []struct {
		name   string
		mutate func(*runtimeProof)
	}{
		{"one_key_equal_times", func(p *runtimeProof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].NotAfter = p.Keys[0].NotBefore
		}},
		{"128_keys_equal_times_duplicate_digests", func(p *runtimeProof) {
			key := p.Keys[0]
			key.NotAfter = key.NotBefore
			p.Keys = make([]runtimeKeyPin, 128)
			for i := range p.Keys {
				p.Keys[i] = key
				p.Keys[i].ID = fmt.Sprintf("key_%03d", i)
			}
		}},
		{"duplicate_key_and_nonce_digests", func(p *runtimeProof) {
			p.Keys[1].Digest = p.Keys[0].Digest
			p.WorkerNonceDigest, p.APITokenDigest = p.AppNonceDigest, p.AppNonceDigest
		}},
		{"unicode_html_quotes_and_separators", func(p *runtimeProof) {
			p.MerchantID = "商户:É/abc.ǿ_1-2"
			p.PayeeLabel = "商户 <A&B> \"quote\" \\ slash / 😀 \u2028 \u2029 e\u0301"
		}},
		{"label_exactly_100_bytes", func(p *runtimeProof) { p.PayeeLabel = strings.Repeat("a", 100) }},
		{"merchant_exactly_100_bytes", func(p *runtimeProof) { p.MerchantID = strings.Repeat("a", 100) }},
		{"role_names_2_and_63_bytes", func(p *runtimeProof) {
			p.Worker.Name, p.Projector.Name = "ab", strings.Repeat("z", 63)
		}},
		{"key_id_32_bytes", func(p *runtimeProof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].ID = strings.Repeat("Z", 32)
		}},
		{"canonical_non_rfc_uuid_bits", func(p *runtimeProof) { p.EnvironmentID = "00000000-0000-0000-0000-000000000001" }},
		{"cross_catalog_oid_categories", func(p *runtimeProof) {
			p.Database.OID, p.App.OID = p.Worker.OID, p.App.Owner.OID
		}},
		{"maximum_uint32_oid", func(p *runtimeProof) { p.Worker.OID = ^uint32(0) }},
		{"loopback_origin_ports_1_and_65535", func(p *runtimeProof) {
			p.APIOrigin, p.ControlOrigin = "http://127.0.0.1:1", "http://127.0.0.1:65535"
		}},
		{"shared_lowercase_schema_suffix", func(p *runtimeProof) {
			p.App.Name = "payment_sandbox_" + strings.Repeat("f", 32)
			p.Mock.Name = "payment_mock_" + strings.Repeat("f", 32)
			p.Receiver.Name = "payment_receiver_" + strings.Repeat("f", 32)
		}},
		{"equal_maximum_instant", func(p *runtimeProof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].NotBefore = "9999-12-31T23:59:59.999999Z"
			p.Keys[0].NotAfter = p.Keys[0].NotBefore
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := v1CodecCompatCopy(original)
			tc.mutate(&p)
			v1CodecCompatEncode(t, p)
			v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), true)
		})
	}
}

func TestV1CodecCompatibilitySemanticRejections(t *testing.T) {
	_, original := v1CodecCompatGolden(t)
	for _, tc := range []struct {
		name   string
		mutate func(*runtimeProof)
	}{
		{"proof_version", func(p *runtimeProof) { p.ProofVersion = 2 }},
		{"protocol_version", func(p *runtimeProof) { p.ProtocolVersion = 0 }},
		{"uuid_uppercase", func(p *runtimeProof) { p.EnvironmentID = strings.ToUpper(p.EnvironmentID) }},
		{"uuid_nil", func(p *runtimeProof) { p.EnvironmentID = "00000000-0000-0000-0000-000000000000" }},
		{"uuid_braces", func(p *runtimeProof) { p.EnvironmentID = "{" + p.EnvironmentID + "}" }},
		{"uuid_unhyphenated", func(p *runtimeProof) { p.EnvironmentID = strings.ReplaceAll(p.EnvironmentID, "-", "") }},
		{"merchant_empty", func(p *runtimeProof) { p.MerchantID = "" }},
		{"merchant_101_bytes", func(p *runtimeProof) { p.MerchantID = strings.Repeat("a", 101) }},
		{"merchant_sk_credential", func(p *runtimeProof) { p.MerchantID = "SK-secret" }},
		{"merchant_mg_credential", func(p *runtimeProof) { p.MerchantID = "mG_secret" }},
		{"merchant_bearer_credential", func(p *runtimeProof) { p.MerchantID = "Bearer:secret" }},
		{"merchant_combining_mark", func(p *runtimeProof) { p.MerchantID = "e\u0301" }},
		{"merchant_symbol", func(p *runtimeProof) { p.MerchantID = "merchant@name" }},
		{"label_empty", func(p *runtimeProof) { p.PayeeLabel = "" }},
		{"label_101_bytes", func(p *runtimeProof) { p.PayeeLabel = strings.Repeat("a", 101) }},
		{"label_control", func(p *runtimeProof) { p.PayeeLabel = "bad\u0085label" }},
		{"label_url", func(p *runtimeProof) { p.PayeeLabel = "https://example.test" }},
		{"database_alias", func(p *runtimeProof) { p.Database.Host = "localhost" }},
		{"database_port", func(p *runtimeProof) { p.Database.Port++ }},
		{"database_name", func(p *runtimeProof) { p.Database.Name = "postgres" }},
		{"database_oid", func(p *runtimeProof) { p.Database.OID = 0 }},
		{"origin_https", func(p *runtimeProof) { p.APIOrigin = "https://127.0.0.1:19081" }},
		{"origin_alias", func(p *runtimeProof) { p.APIOrigin = "http://localhost:19081" }},
		{"origin_ipv6", func(p *runtimeProof) { p.APIOrigin = "http://[::1]:19081" }},
		{"origin_noncanonical_port", func(p *runtimeProof) { p.APIOrigin = "http://127.0.0.1:019081" }},
		{"origin_zero_port", func(p *runtimeProof) { p.APIOrigin = "http://127.0.0.1:0" }},
		{"origin_path", func(p *runtimeProof) { p.APIOrigin += "/" }},
		{"origin_query", func(p *runtimeProof) { p.APIOrigin += "?" }},
		{"origin_fragment", func(p *runtimeProof) { p.APIOrigin += "#" }},
		{"origin_userinfo", func(p *runtimeProof) { p.APIOrigin = "http://user@127.0.0.1:19081" }},
		{"duplicate_origin", func(p *runtimeProof) { p.ControlOrigin = p.APIOrigin }},
		{"callback_wrong_origin", func(p *runtimeProof) { p.CallbackURL = p.APIOrigin + "/api/payments/sandbox/notifications" }},
		{"callback_wrong_path", func(p *runtimeProof) { p.CallbackURL = p.AppListenOrigin + "/wrong" }},
		{"keys_empty", func(p *runtimeProof) { p.Keys = []runtimeKeyPin{} }},
		{"keys_129", func(p *runtimeProof) {
			key := p.Keys[0]
			p.Keys = make([]runtimeKeyPin, 129)
			for i := range p.Keys {
				p.Keys[i] = key
				p.Keys[i].ID = fmt.Sprintf("key_%03d", i)
			}
		}},
		{"key_order", func(p *runtimeProof) { p.Keys[0], p.Keys[1] = p.Keys[1], p.Keys[0] }},
		{"key_duplicate_id", func(p *runtimeProof) { p.Keys[1].ID = p.Keys[0].ID }},
		{"key_empty_id", func(p *runtimeProof) { p.Keys[0].ID = "" }},
		{"key_33_byte_id", func(p *runtimeProof) { p.Keys[0].ID = strings.Repeat("A", 33) }},
		{"key_unicode_id", func(p *runtimeProof) { p.Keys[0].ID = "密钥" }},
		{"key_bad_digest", func(p *runtimeProof) { p.Keys[0].Digest = strings.Repeat("A", 64) }},
		{"key_zero_instant", func(p *runtimeProof) { p.Keys[0].NotBefore = "0001-01-01T00:00:00.000000Z" }},
		{"key_wrong_precision", func(p *runtimeProof) { p.Keys[0].NotBefore = "2001-02-03T04:05:06.123Z" }},
		{"key_offset", func(p *runtimeProof) { p.Keys[0].NotBefore = "2001-02-03T04:05:06.123456+00:00" }},
		{"key_infinity", func(p *runtimeProof) { p.Keys[0].NotAfter = "infinity" }},
		{"key_reversed_times", func(p *runtimeProof) { p.Keys[0].NotBefore = p.Keys[1].NotAfter }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := v1CodecCompatCopy(original)
			tc.mutate(&p)
			v1CodecCompatEncode(t, p)
			v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), false)
		})
	}
	for i := range runtimeRoles(original) {
		for _, operation := range []string{"empty_name", "short_name", "long_name", "uppercase_name", "zero_oid", "duplicate_name", "duplicate_oid", "login"} {
			t.Run(fmt.Sprintf("role_%d/%s", i, operation), func(t *testing.T) {
				p := v1CodecCompatCopy(original)
				roles := []*runtimeRolePin{&p.App.Owner, &p.App.Runtime, &p.Mock.Owner, &p.Mock.Runtime, &p.Receiver.Owner, &p.Receiver.Runtime, &p.Worker, &p.Projector}
				r := roles[i]
				switch operation {
				case "empty_name":
					r.Name = ""
				case "short_name":
					r.Name = "a"
				case "long_name":
					r.Name = strings.Repeat("a", 64)
				case "uppercase_name":
					r.Name = "Uppercase_role"
				case "zero_oid":
					r.OID = 0
				case "duplicate_name":
					r.Name = roles[(i+1)%len(roles)].Name
				case "duplicate_oid":
					r.OID = roles[(i+1)%len(roles)].OID
				case "login":
					r.Login = !r.Login
				}
				v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), false)
			})
		}
	}
	for i := 0; i < 3; i++ {
		for _, operation := range []string{"prefix", "suffix", "uppercase", "zero_oid", "duplicate_oid"} {
			t.Run(fmt.Sprintf("schema_%d/%s", i, operation), func(t *testing.T) {
				p := v1CodecCompatCopy(original)
				schemas := []*runtimeSchemaPin{&p.App, &p.Mock, &p.Receiver}
				s := schemas[i]
				switch operation {
				case "prefix":
					s.Name = "wrong_" + strings.Repeat("0", 32)
				case "suffix":
					s.Name = s.Name[:len(s.Name)-1]
				case "uppercase":
					s.Name = strings.ToUpper(s.Name)
				case "zero_oid":
					s.OID = 0
				case "duplicate_oid":
					s.OID = schemas[(i+1)%len(schemas)].OID
				}
				v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), false)
			})
		}
	}
	for _, field := range []string{"app_nonce", "worker_nonce", "api_token"} {
		for _, digest := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
			t.Run(fmt.Sprintf("%s_digest/length_%d/%c", field, len(digest), byte('a'+len(digest)%20)), func(t *testing.T) {
				p := v1CodecCompatCopy(original)
				switch field {
				case "app_nonce":
					p.AppNonceDigest = digest
				case "worker_nonce":
					p.WorkerNonceDigest = digest
				case "api_token":
					p.APITokenDigest = digest
				}
				v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), false)
			})
		}
	}
}

func TestV1CodecCompatibilityRawAndCanonicalRejections(t *testing.T) {
	raw, _ := v1CodecCompatGolden(t)
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"empty", nil}, {"bom", append([]byte{0xef, 0xbb, 0xbf}, raw...)},
		{"invalid_utf8", bytes.Replace(raw, []byte("merchant:"), []byte{0xff}, 1)},
		{"bare_control", bytes.Replace(raw, []byte("merchant:"), []byte("merchant:\n"), 1)},
		{"bad_escape", bytes.Replace(raw, []byte("merchant:"), []byte(`merchant:\x20`), 1)},
		{"isolated_high_surrogate", bytes.Replace(raw, []byte("merchant:"), []byte(`merchant:\ud800`), 1)},
		{"isolated_low_surrogate", bytes.Replace(raw, []byte("merchant:"), []byte(`merchant:\udc00`), 1)},
		{"wrong_surrogate_pair", bytes.Replace(raw, []byte("merchant:"), []byte(`merchant:\ud800\u0061`), 1)},
		{"decoded_duplicate", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1,"proof_\u0076ersion":1`), 1)},
		{"negative_zero", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":-0`), 1)},
		{"negative", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":-1`), 1)},
		{"leading_zero", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":01`), 1)},
		{"fraction", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1.0`), 1)},
		{"exponent", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1e0`), 1)},
		{"quoted_number", bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":"1"`), 1)},
		{"port_overflow", bytes.Replace(raw, []byte(`"port":55439`), []byte(`"port":65536`), 1)},
		{"oid_overflow", bytes.Replace(raw, []byte(`"oid":100`), []byte(`"oid":4294967296`), 1)},
		{"tail_value", append(bytes.Clone(raw), []byte(`{}`)...)},
		{"tail_lf", append(bytes.Clone(raw), '\n')}, {"leading_space", append([]byte{' '}, raw...)},
		{"html_escape", bytes.Replace(raw, []byte("<"), []byte(`\u003c`), 1)},
		{"unicode_escape", bytes.Replace(raw, []byte("测试"), []byte(`\u6d4b\u8bd5`), 1)},
		{"surrogate_pair_noncanonical", bytes.Replace(raw, []byte("测试"), []byte(`\ud83d\ude00`), 1)},
		{"escaped_slash", bytes.Replace(raw, []byte("http://"), []byte(`http:\/\/`), 1)},
		{"field_order", bytes.Replace(raw, []byte(`"proof_version":1,"protocol_version":1`), []byte(`"protocol_version":1,"proof_version":1`), 1)},
		{"depth", []byte(strings.Repeat("[", 14) + "1" + strings.Repeat("]", 14))},
		{"size_65536_invalid", bytes.Repeat([]byte{' '}, 65536)}, {"size_65537", bytes.Repeat([]byte{' '}, 65537)},
	} {
		t.Run(tc.name, func(t *testing.T) { v1CodecCompatAssert(t, tc.raw, false) })
	}
}

func TestV1CodecCompatibilityExactShape(t *testing.T) {
	raw, _ := v1CodecCompatGolden(t)
	paths := v1CodecCompatJSONPaths(raw, nil)
	if len(paths) != 59 {
		t.Fatal("historical nested field matrix changed")
	}
	for _, path := range paths {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate", "escaped_duplicate", "wrong_type"} {
			t.Run(strings.Join(path, "/")+"/"+operation, func(t *testing.T) {
				mutated := v1CodecCompatMutateJSON(raw, path, operation)
				var shape runtimeProof
				if runtimeStrictDecode(mutated, &shape) == nil {
					t.Fatal("historical exact shape accepted mutation")
				}
				v1CodecCompatAssert(t, mutated, false)
			})
		}
	}
	for _, field := range []string{"id", "digest", "not_before", "not_after"} {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate", "escaped_duplicate", "wrong_type"} {
			t.Run("key/"+field+"/"+operation, func(t *testing.T) {
				var top map[string]json.RawMessage
				var keys []json.RawMessage
				if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["keys"], &keys) != nil {
					t.Fatal("fixture shape")
				}
				keys[0] = v1CodecCompatMutateJSON(keys[0], []string{field}, operation)
				top["keys"] = v1CodecCompatJSON(t, keys)
				mutated := v1CodecCompatJSON(t, top)
				var shape runtimeProof
				if runtimeStrictDecode(mutated, &shape) == nil {
					t.Fatal("historical key exact shape accepted mutation")
				}
				v1CodecCompatAssert(t, mutated, false)
			})
		}
	}
}

func TestV1CodecCompatibilityReservedRoleBoundary(t *testing.T) {
	_, original := v1CodecCompatGolden(t)
	for i := range runtimeRoles(original) {
		t.Run(fmt.Sprintf("role_%d", i), func(t *testing.T) {
			p := v1CodecCompatCopy(original)
			roles := []*runtimeRolePin{&p.App.Owner, &p.App.Runtime, &p.Mock.Owner, &p.Mock.Runtime, &p.Receiver.Owner, &p.Receiver.Runtime, &p.Worker, &p.Projector}
			roles[i].Name = fmt.Sprintf("pg_legacy_%d", i)
			// The V1 API must retain the historical role regex independently
			// of the stricter V2 outer envelope and actual catalog rules.
			v1CodecCompatAssert(t, v1CodecCompatCanonical(t, p), true)
		})
	}
}

func FuzzV1CodecCompatibility(f *testing.F) {
	raw, _ := v1CodecCompatGolden(f)
	f.Add(raw)
	f.Add(append(bytes.Clone(raw), '\n'))
	f.Add([]byte(`{"proof_version":1}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) { v1CodecCompatAssert(t, raw, v1CodecCompatOldAccept(raw)) })
}

func TestV1CodecCompatibilityTypedTextAndShape(t *testing.T) {
	_, original := v1CodecCompatGolden(t)
	var visit func(reflect.Value, []int)
	var paths [][]int
	visit = func(v reflect.Value, path []int) {
		switch v.Kind() {
		case reflect.String:
			paths = append(paths, append([]int(nil), path...))
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				visit(v.Field(i), append(append([]int(nil), path...), i))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i), append(append([]int(nil), path...), i))
			}
		}
	}
	visit(reflect.ValueOf(original), nil)
	for n, path := range paths {
		t.Run(fmt.Sprintf("invalid_utf8_%d", n), func(t *testing.T) {
			p := v1CodecCompatCopy(original)
			v := reflect.ValueOf(&p).Elem()
			for _, i := range path {
				if v.Kind() == reflect.Struct {
					v = v.Field(i)
				} else {
					v = v.Index(i)
				}
			}
			v.SetString(string([]byte{0xff}))
			v1CodecCompatEncode(t, p)
		})
	}
	// Compare every field name, order, tag, and nested scalar kind, rather than
	// relying on a JSON conversion which could silently discard a field.
	var shape func(reflect.Type, reflect.Type)
	shape = func(a, b reflect.Type) {
		if a.Kind() != b.Kind() {
			t.Fatalf("kind mismatch: %v %v", a, b)
		}
		switch a.Kind() {
		case reflect.Struct:
			if a.NumField() != b.NumField() {
				t.Fatal("field count mismatch")
			}
			for i := 0; i < a.NumField(); i++ {
				x, y := a.Field(i), b.Field(i)
				if x.Name != y.Name || x.Tag != y.Tag {
					t.Fatal("field order/name/tag mismatch")
				}
				shape(x.Type, y.Type)
			}
		case reflect.Slice:
			shape(a.Elem(), b.Elem())
		}
	}
	shape(reflect.TypeOf(original), reflect.TypeOf(fundinglotproof.V1Input{}))
}
