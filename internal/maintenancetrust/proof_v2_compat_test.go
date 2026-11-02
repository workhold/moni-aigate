package maintenancetrust

// The maintenance package owns the historical v1 validator. These tests depend
// one way on the new format leaf; neither production package gains authority or
// a dependency on the other from the comparison.
import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

func proofV2CompatDigest(raw []byte) string {
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}

func proofV2CompatJSON(t testing.TB, v any) []byte {
	t.Helper()
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		t.Fatal("independent JSON encoding failed")
	}
	return bytes.Clone(bytes.TrimSuffix(b.Bytes(), []byte{'\n'}))
}

func proofV2CompatCanonical(t testing.TB, p predecessorV1Proof) []byte {
	t.Helper()
	raw, err := predecessorV1CanonicalProof(p)
	if err != nil {
		t.Fatal("historical canonical encoder failed")
	}
	return raw
}

func proofV2CompatOldAccept(raw []byte) bool {
	var p predecessorV1Proof
	if predecessorV1StrictDecode(raw, &p) != nil || predecessorV1ValidateProof(p) != nil {
		return false
	}
	canonical, err := predecessorV1CanonicalProof(p)
	return err == nil && bytes.Equal(canonical, raw)
}

func proofV2CompatCopy(p predecessorV1Proof) predecessorV1Proof {
	p.Keys = append([]predecessorV1KeyPin(nil), p.Keys...)
	return p
}

func proofV2CompatInput(t testing.TB, p predecessorV1Proof, raw []byte) fundinglotproof.V2Input {
	t.Helper()
	role := func(r predecessorV1RolePin) fundinglotproof.Role {
		return fundinglotproof.Role{Name: r.Name, OID: r.OID}
	}
	schema := func(s predecessorV1SchemaPin) fundinglotproof.Schema {
		owner := role(s.Owner)
		return fundinglotproof.Schema{Name: s.Name, OID: s.OID, NamespaceOwner: owner, ObjectOwner: owner}
	}
	// These are descriptive format claims. This test does not authenticate a
	// database, catalog root, migration installation, epoch, or source wallet.
	manifest := []fundinglotproof.Migration{{Name: "001_core.sql", SHA256: strings.Repeat("1", 64)}}
	digest, original := proofV2CompatDigest(raw), base64.RawURLEncoding.EncodeToString(raw)
	return fundinglotproof.V2Input{
		Domain: fundinglotproof.DomainV2, ProofVersion: 2, Mode: "sandbox",
		EpochID:   "23456789-2345-4345-8345-23456789abcd",
		Database:  fundinglotproof.Database{Host: p.Database.Host, Port: p.Database.Port, Name: p.Database.Name, OID: p.Database.OID},
		AppSchema: schema(p.App), AppRole: role(p.App.Runtime), ProjectorRole: role(p.Projector),
		Sandbox: &fundinglotproof.Sandbox{
			MockSchema: schema(p.Mock), MockRole: role(p.Mock.Runtime),
			ReceiverSchema: schema(p.Receiver), ReceiverRole: role(p.Receiver.Runtime), WorkerRole: role(p.Worker),
		},
		BasisVersion: 1, BasisDigest: strings.Repeat("6", 64), BasisDescriptorDigest: strings.Repeat("7", 64),
		MigrationManifest: manifest, ManifestDigest: proofV2CompatDigest(proofV2CompatJSON(t, manifest)),
		CatalogDescriptorVersion: 2, CatalogPostgresMajor: 16,
		CatalogExpectedDigest: strings.Repeat("8", 64), CatalogSchemaDigest: strings.Repeat("9", 64), CatalogRolesDigest: strings.Repeat("a", 64),
		PredecessorPaymentDigest: &digest, PredecessorPaymentCanonicalBase64URL: &original,
	}
}

func proofV2CompatAssert(t testing.TB, raw []byte, pins predecessorV1Proof, wantOld, wantV2 bool) {
	t.Helper()
	if got := proofV2CompatOldAccept(raw); got != wantOld {
		t.Fatal("historical strict, semantic, canonical acceptance differs from expected boundary")
	}
	input := proofV2CompatInput(t, pins, raw)
	wire := proofV2CompatJSON(t, input)
	for _, candidate := range []struct {
		name string
		call func() (*fundinglotproof.CanonicalV2, error)
	}{
		{"encode", func() (*fundinglotproof.CanonicalV2, error) { return fundinglotproof.EncodeV2(input) }},
		{"decode_independently_encoded_wire", func() (*fundinglotproof.CanonicalV2, error) { return fundinglotproof.DecodeV2(wire) }},
	} {
		proof, err := candidate.call()
		if !wantV2 {
			if proof != nil || err != fundinglotproof.ErrCanonical {
				t.Fatalf("%s accepted invalid predecessor or returned a partial result", candidate.name)
			}
			continue
		}
		if err != nil || proof == nil || !bytes.Equal(proof.Bytes(), wire) || proof.SHA256() != proofV2CompatDigest(wire) {
			t.Fatalf("%s rejected or changed a compatible predecessor", candidate.name)
		}
		bindings, err := proof.Bindings()
		if err != nil || bindings.PredecessorPaymentDigest == nil || *bindings.PredecessorPaymentDigest != proofV2CompatDigest(raw) || bindings.PredecessorPaymentCanonicalBase64URL == nil {
			t.Fatalf("%s lost predecessor bindings", candidate.name)
		}
		original, err := base64.RawURLEncoding.DecodeString(*bindings.PredecessorPaymentCanonicalBase64URL)
		if err != nil || !bytes.Equal(original, raw) {
			t.Fatalf("%s did not retain exact historical bytes", candidate.name)
		}
	}
}

func TestProofV2V1CompatibilityAccepted(t *testing.T) {
	raw, original := predecessorGolden(t)
	if proofV2CompatDigest(raw) != "1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c" {
		t.Fatal("independent historical golden changed")
	}
	t.Run("unchanged_historical_golden", func(t *testing.T) { proofV2CompatAssert(t, raw, original, true, true) })
	for _, tc := range []struct {
		name   string
		mutate func(*predecessorV1Proof)
	}{
		{"one_key_equal_times", func(p *predecessorV1Proof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].NotAfter = p.Keys[0].NotBefore
		}},
		{"128_keys_equal_times_duplicate_digests", func(p *predecessorV1Proof) {
			key := p.Keys[0]
			key.NotAfter = key.NotBefore
			p.Keys = make([]predecessorV1KeyPin, 128)
			for i := range p.Keys {
				p.Keys[i] = key
				p.Keys[i].ID = fmt.Sprintf("key_%03d", i)
			}
		}},
		{"duplicate_key_and_nonce_digests", func(p *predecessorV1Proof) {
			p.Keys[1].Digest = p.Keys[0].Digest
			p.WorkerNonceDigest, p.APITokenDigest = p.AppNonceDigest, p.AppNonceDigest
		}},
		{"unicode_html_quotes_and_separators", func(p *predecessorV1Proof) {
			p.MerchantID = "商户:É/abc.ǿ_1-2"
			p.PayeeLabel = "商户 <A&B> \"quote\" \\ slash / 😀 \u2028 \u2029 e\u0301"
		}},
		{"label_exactly_100_bytes", func(p *predecessorV1Proof) { p.PayeeLabel = strings.Repeat("a", 100) }},
		{"merchant_exactly_100_bytes", func(p *predecessorV1Proof) { p.MerchantID = strings.Repeat("a", 100) }},
		{"role_names_2_and_63_bytes", func(p *predecessorV1Proof) {
			p.Worker.Name, p.Projector.Name = "ab", strings.Repeat("z", 63)
		}},
		{"key_id_32_bytes", func(p *predecessorV1Proof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].ID = strings.Repeat("Z", 32)
		}},
		{"canonical_non_rfc_uuid_bits", func(p *predecessorV1Proof) { p.EnvironmentID = "00000000-0000-0000-0000-000000000001" }},
		{"cross_catalog_oid_categories", func(p *predecessorV1Proof) {
			p.Database.OID, p.App.OID = p.Worker.OID, p.App.Owner.OID
		}},
		{"maximum_uint32_oid", func(p *predecessorV1Proof) { p.Worker.OID = ^uint32(0) }},
		{"loopback_origin_ports_1_and_65535", func(p *predecessorV1Proof) {
			p.APIOrigin, p.ControlOrigin = "http://127.0.0.1:1", "http://127.0.0.1:65535"
		}},
		{"shared_lowercase_schema_suffix", func(p *predecessorV1Proof) {
			p.App.Name = "payment_sandbox_" + strings.Repeat("f", 32)
			p.Mock.Name = "payment_mock_" + strings.Repeat("f", 32)
			p.Receiver.Name = "payment_receiver_" + strings.Repeat("f", 32)
		}},
		{"equal_maximum_instant", func(p *predecessorV1Proof) {
			p.Keys = p.Keys[:1]
			p.Keys[0].NotBefore = "9999-12-31T23:59:59.999999Z"
			p.Keys[0].NotAfter = p.Keys[0].NotBefore
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := proofV2CompatCopy(original)
			tc.mutate(&p)
			proofV2CompatAssert(t, proofV2CompatCanonical(t, p), p, true, true)
		})
	}
}

func TestProofV2V1CompatibilitySemanticRejections(t *testing.T) {
	_, original := predecessorGolden(t)
	for _, tc := range []struct {
		name   string
		mutate func(*predecessorV1Proof)
	}{
		{"proof_version", func(p *predecessorV1Proof) { p.ProofVersion = 2 }},
		{"protocol_version", func(p *predecessorV1Proof) { p.ProtocolVersion = 0 }},
		{"uuid_uppercase", func(p *predecessorV1Proof) { p.EnvironmentID = strings.ToUpper(p.EnvironmentID) }},
		{"uuid_nil", func(p *predecessorV1Proof) { p.EnvironmentID = "00000000-0000-0000-0000-000000000000" }},
		{"uuid_braces", func(p *predecessorV1Proof) { p.EnvironmentID = "{" + p.EnvironmentID + "}" }},
		{"uuid_unhyphenated", func(p *predecessorV1Proof) { p.EnvironmentID = strings.ReplaceAll(p.EnvironmentID, "-", "") }},
		{"merchant_empty", func(p *predecessorV1Proof) { p.MerchantID = "" }},
		{"merchant_101_bytes", func(p *predecessorV1Proof) { p.MerchantID = strings.Repeat("a", 101) }},
		{"merchant_sk_credential", func(p *predecessorV1Proof) { p.MerchantID = "SK-secret" }},
		{"merchant_mg_credential", func(p *predecessorV1Proof) { p.MerchantID = "mG_secret" }},
		{"merchant_bearer_credential", func(p *predecessorV1Proof) { p.MerchantID = "Bearer:secret" }},
		{"merchant_combining_mark", func(p *predecessorV1Proof) { p.MerchantID = "e\u0301" }},
		{"merchant_symbol", func(p *predecessorV1Proof) { p.MerchantID = "merchant@name" }},
		{"label_empty", func(p *predecessorV1Proof) { p.PayeeLabel = "" }},
		{"label_101_bytes", func(p *predecessorV1Proof) { p.PayeeLabel = strings.Repeat("a", 101) }},
		{"label_control", func(p *predecessorV1Proof) { p.PayeeLabel = "bad\u0085label" }},
		{"label_url", func(p *predecessorV1Proof) { p.PayeeLabel = "https://example.test" }},
		{"database_alias", func(p *predecessorV1Proof) { p.Database.Host = "localhost" }},
		{"database_port", func(p *predecessorV1Proof) { p.Database.Port++ }},
		{"database_name", func(p *predecessorV1Proof) { p.Database.Name = "postgres" }},
		{"database_oid", func(p *predecessorV1Proof) { p.Database.OID = 0 }},
		{"origin_https", func(p *predecessorV1Proof) { p.APIOrigin = "https://127.0.0.1:19081" }},
		{"origin_alias", func(p *predecessorV1Proof) { p.APIOrigin = "http://localhost:19081" }},
		{"origin_ipv6", func(p *predecessorV1Proof) { p.APIOrigin = "http://[::1]:19081" }},
		{"origin_noncanonical_port", func(p *predecessorV1Proof) { p.APIOrigin = "http://127.0.0.1:019081" }},
		{"origin_zero_port", func(p *predecessorV1Proof) { p.APIOrigin = "http://127.0.0.1:0" }},
		{"origin_path", func(p *predecessorV1Proof) { p.APIOrigin += "/" }},
		{"origin_query", func(p *predecessorV1Proof) { p.APIOrigin += "?" }},
		{"origin_fragment", func(p *predecessorV1Proof) { p.APIOrigin += "#" }},
		{"origin_userinfo", func(p *predecessorV1Proof) { p.APIOrigin = "http://user@127.0.0.1:19081" }},
		{"duplicate_origin", func(p *predecessorV1Proof) { p.ControlOrigin = p.APIOrigin }},
		{"callback_wrong_origin", func(p *predecessorV1Proof) { p.CallbackURL = p.APIOrigin + "/api/payments/sandbox/notifications" }},
		{"callback_wrong_path", func(p *predecessorV1Proof) { p.CallbackURL = p.AppListenOrigin + "/wrong" }},
		{"keys_empty", func(p *predecessorV1Proof) { p.Keys = []predecessorV1KeyPin{} }},
		{"keys_129", func(p *predecessorV1Proof) {
			key := p.Keys[0]
			p.Keys = make([]predecessorV1KeyPin, 129)
			for i := range p.Keys {
				p.Keys[i] = key
				p.Keys[i].ID = fmt.Sprintf("key_%03d", i)
			}
		}},
		{"key_order", func(p *predecessorV1Proof) { p.Keys[0], p.Keys[1] = p.Keys[1], p.Keys[0] }},
		{"key_duplicate_id", func(p *predecessorV1Proof) { p.Keys[1].ID = p.Keys[0].ID }},
		{"key_empty_id", func(p *predecessorV1Proof) { p.Keys[0].ID = "" }},
		{"key_33_byte_id", func(p *predecessorV1Proof) { p.Keys[0].ID = strings.Repeat("A", 33) }},
		{"key_unicode_id", func(p *predecessorV1Proof) { p.Keys[0].ID = "密钥" }},
		{"key_bad_digest", func(p *predecessorV1Proof) { p.Keys[0].Digest = strings.Repeat("A", 64) }},
		{"key_zero_instant", func(p *predecessorV1Proof) { p.Keys[0].NotBefore = "0001-01-01T00:00:00.000000Z" }},
		{"key_wrong_precision", func(p *predecessorV1Proof) { p.Keys[0].NotBefore = "2001-02-03T04:05:06.123Z" }},
		{"key_offset", func(p *predecessorV1Proof) { p.Keys[0].NotBefore = "2001-02-03T04:05:06.123456+00:00" }},
		{"key_infinity", func(p *predecessorV1Proof) { p.Keys[0].NotAfter = "infinity" }},
		{"key_reversed_times", func(p *predecessorV1Proof) { p.Keys[0].NotBefore = p.Keys[1].NotAfter }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := proofV2CompatCopy(original)
			tc.mutate(&p)
			proofV2CompatAssert(t, proofV2CompatCanonical(t, p), original, false, false)
		})
	}
	for i := range predecessorV1Roles(original) {
		for _, operation := range []string{"empty_name", "short_name", "long_name", "uppercase_name", "zero_oid", "duplicate_name", "duplicate_oid", "login"} {
			t.Run(fmt.Sprintf("role_%d/%s", i, operation), func(t *testing.T) {
				p := proofV2CompatCopy(original)
				roles := []*predecessorV1RolePin{&p.App.Owner, &p.App.Runtime, &p.Mock.Owner, &p.Mock.Runtime, &p.Receiver.Owner, &p.Receiver.Runtime, &p.Worker, &p.Projector}
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
				proofV2CompatAssert(t, proofV2CompatCanonical(t, p), original, false, false)
			})
		}
	}
	for i := 0; i < 3; i++ {
		for _, operation := range []string{"prefix", "suffix", "uppercase", "zero_oid", "duplicate_oid"} {
			t.Run(fmt.Sprintf("schema_%d/%s", i, operation), func(t *testing.T) {
				p := proofV2CompatCopy(original)
				schemas := []*predecessorV1SchemaPin{&p.App, &p.Mock, &p.Receiver}
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
				proofV2CompatAssert(t, proofV2CompatCanonical(t, p), original, false, false)
			})
		}
	}
	for _, field := range []string{"app_nonce", "worker_nonce", "api_token"} {
		for _, digest := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
			t.Run(fmt.Sprintf("%s_digest/length_%d/%c", field, len(digest), byte('a'+len(digest)%20)), func(t *testing.T) {
				p := proofV2CompatCopy(original)
				switch field {
				case "app_nonce":
					p.AppNonceDigest = digest
				case "worker_nonce":
					p.WorkerNonceDigest = digest
				case "api_token":
					p.APITokenDigest = digest
				}
				proofV2CompatAssert(t, proofV2CompatCanonical(t, p), original, false, false)
			})
		}
	}
}

func TestProofV2V1CompatibilityRawAndCanonicalRejections(t *testing.T) {
	raw, pins := predecessorGolden(t)
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
		t.Run(tc.name, func(t *testing.T) { proofV2CompatAssert(t, tc.raw, pins, false, false) })
	}
}

func TestProofV2V1CompatibilityExactShape(t *testing.T) {
	raw, pins := predecessorGolden(t)
	paths := predecessorJSONPaths(raw, nil)
	if len(paths) != 59 {
		t.Fatal("historical nested field matrix changed")
	}
	for _, path := range paths {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate"} {
			t.Run(strings.Join(path, "/")+"/"+operation, func(t *testing.T) {
				mutated := predecessorMutateJSON(raw, path, operation)
				var shape predecessorV1Proof
				if predecessorV1StrictDecode(mutated, &shape) == nil {
					t.Fatal("historical exact shape accepted mutation")
				}
				proofV2CompatAssert(t, mutated, pins, false, false)
			})
		}
	}
	for _, field := range []string{"id", "digest", "not_before", "not_after"} {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate"} {
			t.Run("key/"+field+"/"+operation, func(t *testing.T) {
				var top map[string]json.RawMessage
				var keys []json.RawMessage
				if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["keys"], &keys) != nil {
					t.Fatal("fixture shape")
				}
				keys[0] = predecessorMutateJSON(keys[0], []string{field}, operation)
				top["keys"] = proofV2CompatJSON(t, keys)
				mutated := proofV2CompatJSON(t, top)
				var shape predecessorV1Proof
				if predecessorV1StrictDecode(mutated, &shape) == nil {
					t.Fatal("historical key exact shape accepted mutation")
				}
				proofV2CompatAssert(t, mutated, pins, false, false)
			})
		}
	}
}

func TestProofV2V1CompatibilityReservedRoleEnvelopeBoundary(t *testing.T) {
	_, original := predecessorGolden(t)
	for i := range predecessorV1Roles(original) {
		t.Run(fmt.Sprintf("role_%d", i), func(t *testing.T) {
			p := proofV2CompatCopy(original)
			roles := []*predecessorV1RolePin{&p.App.Owner, &p.App.Runtime, &p.Mock.Owner, &p.Mock.Runtime, &p.Receiver.Owner, &p.Receiver.Runtime, &p.Worker, &p.Projector}
			roles[i].Name = fmt.Sprintf("pg_legacy_%d", i)
			// Old v1 intentionally accepts its historical regular expression.
			// The distinct v2 outer Role format follows catalog Pins and rejects
			// reserved names; this must not silently tighten the v1 helper.
			proofV2CompatAssert(t, proofV2CompatCanonical(t, p), p, true, false)
		})
	}
}

func FuzzProofV2V1Compatibility(f *testing.F) {
	raw, pins := predecessorGoldenForProofV2Fuzz(f)
	f.Add(raw)
	f.Add(append(bytes.Clone(raw), '\n'))
	f.Add([]byte(`{"proof_version":1}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, original []byte) {
		// Fixed outer pins isolate arbitrary malformed v1 bytes. If a fuzzer
		// produces a new valid predecessor with different pins, derive its pins
		// and apply the separately specified reserved-role outer restriction.
		wantOld := proofV2CompatOldAccept(original)
		p, wantV2 := pins, wantOld
		if wantOld {
			if predecessorV1StrictDecode(original, &p) != nil {
				t.Fatal("historical decode unstable")
			}
			for _, r := range predecessorV1Roles(p) {
				if strings.HasPrefix(r.Name, "pg_") {
					wantV2 = false
				}
			}
		}
		proofV2CompatAssert(t, original, p, wantOld, wantV2)
	})
}

func predecessorGoldenForProofV2Fuzz(t testing.TB) ([]byte, predecessorV1Proof) {
	t.Helper()
	// Keep the corpus independent of the new leaf's own generated fixtures.
	var p predecessorV1Proof
	raw := []byte(`{"proof_version":1,"protocol_version":1,"environment_id":"12345678-1234-4234-8234-123456789abc","merchant_id":"merchant","payee_label":"compatibility","database":{"host":"127.0.0.1","port":55439,"name":"moni_test","oid":100},"app":{"name":"payment_sandbox_0123456789abcdef0123456789abcdef","oid":101,"owner":{"name":"compat_app_owner","oid":201,"login":false},"runtime":{"name":"compat_app_runtime","oid":202,"login":true}},"mock":{"name":"payment_mock_0123456789abcdef0123456789abcdef","oid":102,"owner":{"name":"compat_mock_owner","oid":203,"login":false},"runtime":{"name":"compat_mock_runtime","oid":204,"login":true}},"receiver":{"name":"payment_receiver_0123456789abcdef0123456789abcdef","oid":103,"owner":{"name":"compat_receiver_owner","oid":205,"login":false},"runtime":{"name":"compat_receiver_runtime","oid":206,"login":true}},"worker":{"name":"compat_worker","oid":207,"login":true},"projector":{"name":"compat_projector","oid":208,"login":true},"app_nonce_digest":"1111111111111111111111111111111111111111111111111111111111111111","worker_nonce_digest":"2222222222222222222222222222222222222222222222222222222222222222","api_token_digest":"3333333333333333333333333333333333333333333333333333333333333333","api_origin":"http://127.0.0.1:19081","control_origin":"http://127.0.0.1:19082","app_listen_origin":"http://127.0.0.1:19083","callback_url":"http://127.0.0.1:19083/api/payments/sandbox/notifications","keys":[{"id":"A-key","digest":"4444444444444444444444444444444444444444444444444444444444444444","not_before":"2001-01-01T00:00:00.000000Z","not_after":"2001-01-01T00:00:00.000000Z"}]}`)
	if predecessorV1StrictDecode(raw, &p) != nil || !proofV2CompatOldAccept(raw) {
		t.Fatal("independent fuzz corpus invalid")
	}
	return raw, p
}
