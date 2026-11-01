package maintenancetrust

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func predecessorGolden(t *testing.T) ([]byte, predecessorV1Proof) {
	t.Helper()
	raw, err := os.ReadFile("testdata/predecessor_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var p predecessorV1Proof
	if predecessorV1StrictDecode(raw, &p) != nil || predecessorV1ValidateProof(p) != nil {
		t.Fatal("fixed independent golden rejected")
	}
	return raw, p
}
func TestPredecessorV1GoldenCanonical(t *testing.T) {
	raw, p := predecessorGolden(t)
	want, err := os.ReadFile("testdata/predecessor_v1.sha256")
	if err != nil {
		t.Fatal(err)
	}
	if predecessorV1SHA(raw) != strings.TrimSpace(string(want)) || predecessorV1SHA(raw) != "1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c" {
		t.Fatal("golden digest changed")
	}
	recoded, err := predecessorV1CanonicalProof(p)
	if err != nil || !bytes.Equal(recoded, raw) {
		t.Fatal("canonical is not the original bytes")
	}
	if !bytes.Contains(raw, []byte("<A&B>")) || !bytes.Contains(raw, []byte(`\u2028`)) {
		t.Fatal("HTML and separator vector missing")
	}
	p.Keys[1].Digest = p.Keys[0].Digest
	if predecessorV1ValidateProof(p) != nil {
		t.Fatal("pure v1 validator gained a peer-only uniqueness rule")
	}
}

func predecessorJSONPaths(raw []byte, prefix []string) [][]string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil
	}
	var out [][]string
	for key, value := range obj {
		path := append(append([]string(nil), prefix...), key)
		out = append(out, path)
		out = append(out, predecessorJSONPaths(value, path)...)
	}
	return out
}
func predecessorMutateJSON(raw []byte, path []string, operation string) []byte {
	var obj map[string]json.RawMessage
	_ = json.Unmarshal(raw, &obj)
	key := path[0]
	if len(path) > 1 {
		obj[key] = predecessorMutateJSON(obj[key], path[1:], operation)
	} else {
		switch operation {
		case "missing":
			delete(obj, key)
		case "null":
			obj[key] = json.RawMessage("null")
		case "case":
			v := obj[key]
			delete(obj, key)
			obj[strings.ToUpper(key)] = v
		case "unknown":
			obj["unknown_"+key] = json.RawMessage("true")
		case "duplicate":
			encoded, _ := json.Marshal(obj)
			name, _ := json.Marshal(key)
			return append(append(append(append([]byte{}, encoded[:len(encoded)-1]...), ','), append(name, ':')...), append(obj[key], '}')...)
		}
	}
	result, _ := json.Marshal(obj)
	return result
}
func TestPredecessorV1EveryObjectFieldExactShape(t *testing.T) {
	raw, _ := predecessorGolden(t)
	paths := predecessorJSONPaths(raw, nil)
	if len(paths) != 59 {
		t.Fatal("incomplete nested field matrix")
	}
	for _, path := range paths {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate"} {
			t.Run(strings.Join(path, "/")+"/"+operation, func(t *testing.T) {
				var p predecessorV1Proof
				if predecessorV1StrictDecode(predecessorMutateJSON(raw, path, operation), &p) == nil {
					t.Fatal("invalid exact object shape accepted")
				}
			})
		}
	}
	for _, field := range []string{"id", "digest", "not_before", "not_after"} {
		for _, operation := range []string{"missing", "null", "case", "unknown", "duplicate"} {
			t.Run("key-object/"+field+"/"+operation, func(t *testing.T) {
				var top map[string]json.RawMessage
				_ = json.Unmarshal(raw, &top)
				var keys []json.RawMessage
				_ = json.Unmarshal(top["keys"], &keys)
				keys[0] = predecessorMutateJSON(keys[0], []string{field}, operation)
				top["keys"], _ = json.Marshal(keys)
				mutated, _ := json.Marshal(top)
				var p predecessorV1Proof
				if predecessorV1StrictDecode(mutated, &p) == nil {
					t.Fatal("invalid key object shape accepted")
				}
			})
		}
	}
}
func TestPredecessorV1MalformedAndNonCanonical(t *testing.T) {
	raw, _ := predecessorGolden(t)
	for name, mutated := range map[string][]byte{
		"invalid_utf8": append([]byte{0xff}, raw...), "second_json": append(bytes.Clone(raw), []byte(`{}`)...), "bom": append([]byte{0xef, 0xbb, 0xbf}, raw...),
		"surrogate_high": bytes.Replace(raw, []byte(`"payee_label":"`), []byte(`"payee_label":"\ud800`), 1),
		"surrogate_low":  bytes.Replace(raw, []byte(`"payee_label":"`), []byte(`"payee_label":"\udc00`), 1),
		"negative_zero":  bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":-0`), 1),
		"exponent":       bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1e0`), 1),
		"port_overflow":  bytes.Replace(raw, []byte(`"port":55439`), []byte(`"port":65536`), 1),
		"number_string":  bytes.Replace(raw, []byte(`"oid":100`), []byte(`"oid":"100"`), 1),
		"deep":           []byte(strings.Repeat("[", 14) + "1" + strings.Repeat("]", 14)), "oversize": bytes.Repeat([]byte(" "), 65537),
	} {
		t.Run(name, func(t *testing.T) {
			var p predecessorV1Proof
			if predecessorV1StrictDecode(mutated, &p) == nil {
				t.Fatal("malformed v1 accepted")
			}
		})
	}
	for _, mutated := range [][]byte{append(bytes.Clone(raw), '\n'), bytes.Replace(raw, []byte("<"), []byte(`\u003c`), 1), append([]byte(" "), raw...)} {
		var p predecessorV1Proof
		if predecessorV1StrictDecode(mutated, &p) != nil {
			t.Fatal("valid JSON vector unexpectedly malformed")
		}
		recoded, _ := predecessorV1CanonicalProof(p)
		if bytes.Equal(mutated, recoded) {
			t.Fatal("noncanonical bytes accepted")
		}
	}
}
func TestPredecessorV1SemanticBoundaries(t *testing.T) {
	_, original := predecessorGolden(t)
	cases := map[string]func(*predecessorV1Proof){
		"version": func(p *predecessorV1Proof) { p.ProofVersion = 2 }, "protocol": func(p *predecessorV1Proof) { p.ProtocolVersion = 2 },
		"uuid": func(p *predecessorV1Proof) { p.EnvironmentID = strings.ToUpper(p.EnvironmentID) }, "merchant": func(p *predecessorV1Proof) { p.MerchantID = "sk-secret" }, "label": func(p *predecessorV1Proof) { p.PayeeLabel = "bad\nlabel" },
		"role_oid": func(p *predecessorV1Proof) { p.Worker.OID = p.Projector.OID }, "role_name": func(p *predecessorV1Proof) { p.Worker.Name = p.Projector.Name }, "owner_login": func(p *predecessorV1Proof) { p.Mock.Owner.Login = true },
		"runtime_login": func(p *predecessorV1Proof) { p.Mock.Runtime.Login = false }, "worker_login": func(p *predecessorV1Proof) { p.Worker.Login = false }, "schema_suffix": func(p *predecessorV1Proof) { p.Mock.Name = "payment_mock_" + strings.Repeat("a", 32) },
		"host": func(p *predecessorV1Proof) { p.Database.Host = "localhost" }, "port": func(p *predecessorV1Proof) { p.Database.Port = 55440 }, "db": func(p *predecessorV1Proof) { p.Database.Name = "postgres" }, "oid_zero": func(p *predecessorV1Proof) { p.Database.OID = 0 },
		"origin_slash": func(p *predecessorV1Proof) { p.APIOrigin += "/" }, "origin_leading_port": func(p *predecessorV1Proof) { p.APIOrigin = "http://127.0.0.1:019081" }, "origin_query": func(p *predecessorV1Proof) { p.APIOrigin += "?x" },
		"origin_alias": func(p *predecessorV1Proof) { p.APIOrigin = "http://localhost:19081" }, "origin_fragment": func(p *predecessorV1Proof) { p.APIOrigin += "#" }, "origins_same": func(p *predecessorV1Proof) { p.APIOrigin = p.ControlOrigin },
		"callback": func(p *predecessorV1Proof) {
			p.CallbackURL = "http://127.0.0.1:19082/api/payments/sandbox/notifications"
		}, "digest": func(p *predecessorV1Proof) { p.AppNonceDigest = strings.Repeat("A", 64) },
		"keys_empty": func(p *predecessorV1Proof) { p.Keys = nil }, "keys_order": func(p *predecessorV1Proof) { p.Keys[0], p.Keys[1] = p.Keys[1], p.Keys[0] }, "key_duplicate": func(p *predecessorV1Proof) { p.Keys[1].ID = p.Keys[0].ID },
		"time_zero": func(p *predecessorV1Proof) { p.Keys[0].NotBefore = "0001-01-01T00:00:00.000000Z" }, "time_micro": func(p *predecessorV1Proof) { p.Keys[0].NotBefore = "2020-01-01T00:00:00Z" }, "window": func(p *predecessorV1Proof) { p.Keys[0].NotBefore = p.Keys[1].NotAfter },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := original
			p.Keys = append([]predecessorV1KeyPin(nil), original.Keys...)
			mutate(&p)
			if predecessorV1ValidateProof(p) == nil {
				t.Fatal("invalid v1 semantics accepted")
			}
		})
	}
}
