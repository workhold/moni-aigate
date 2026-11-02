package fundinglotproof

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func inputFixture(t testing.TB, name string) V2Input {
	t.Helper()
	var v V2Input
	if err := json.Unmarshal(fixture(t, name), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func testSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func pointer(s string) *string  { return &s }
func setV1(p *V2Input, raw []byte) {
	p.PredecessorPaymentDigest = pointer(testSHA(raw))
	p.PredecessorPaymentCanonicalBase64URL = pointer(base64.RawURLEncoding.EncodeToString(raw))
}
func setManifest(t testing.TB, p *V2Input, entries []Migration) {
	t.Helper()
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	p.MigrationManifest = entries
	p.ManifestDigest = testSHA(raw)
}
func rejected(t testing.TB, raw []byte) {
	t.Helper()
	p, err := DecodeV2(raw)
	if p != nil || err != ErrCanonical {
		t.Fatalf("wanted nil/fixed error; proof=%t error=%v", p != nil, err)
	}
}

// These hashes were calculated by the independent Python testdata generator.
// Tests consume the committed raw bytes, never regenerate their oracle via Go.
func TestIndependentCanonicalGoldens(t *testing.T) {
	for _, tc := range []struct{ name, sha string }{
		{"ordinary.json", "b2aaed4dd053602715b993c78be33d9da04e5e5407b2886349c8cfdcfb85a3a5"},
		{"sandbox.json", "da8c961c2a0f0ef83d087f93eacfc540fa043994c460e1843923b9aaf854a424"},
		{"sandbox_1keys.json", "4c364d4363e41a42c73ca641d552b02cc6555cdb082d898f4e6e01fc7346f920"},
		{"sandbox_128keys.json", "d741c571ac203406b30745a725e415b2adb530a8562764219f71048d44bd8acf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixture(t, tc.name)
			if testSHA(raw) != tc.sha {
				t.Fatal("independent fixture digest changed")
			}
			p, err := DecodeV2(raw)
			if err != nil {
				t.Fatal(err)
			}
			if p.SHA256() != tc.sha || !bytes.Equal(p.Bytes(), raw) {
				t.Fatal("golden mismatch")
			}
			bindings, err := p.Bindings()
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeV2(bindings)
			if err != nil || !bytes.Equal(encoded.Bytes(), raw) {
				t.Fatalf("typed encoding differs: %v", err)
			}
		})
	}
}

func TestOrdinaryCatalogCompatibleNamesAndClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*V2Input)
	}{
		{"public_builtin_namespace", func(p *V2Input) {
			p.AppSchema.Name = "public"
			p.AppSchema.NamespaceOwner = Role{Name: "pg_database_owner", OID: 6171}
		}},
		{"public_owned_namespace", func(p *V2Input) { p.AppSchema.Name = "public" }},
		{"maximum_oid_and_version", func(p *V2Input) { p.Database.OID = ^uint32(0); p.CatalogDescriptorVersion = ^uint32(0) }},
		{"unicode_name_63_bytes", func(p *V2Input) { p.AppRole.Name = strings.Repeat("界", 21) }},
		{"host_253_bytes", func(p *V2Input) { p.Database.Host = strings.Repeat("a", 253) }},
		{"all_zero_digest_claim", func(p *V2Input) { p.BasisDigest = strings.Repeat("0", 64) }},
		{"swapped_legal_digest_claims", func(p *V2Input) {
			p.BasisDescriptorDigest, p.CatalogExpectedDigest = p.CatalogExpectedDigest, p.BasisDescriptorDigest
		}},
		{"foreign_mode_root_is_only_a_claim", func(p *V2Input) {
			p.CatalogExpectedDigest = "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inputFixture(t, "ordinary.json")
			tc.mutate(&p)
			if _, err := EncodeV2(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRejectInvalidTypedValues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*V2Input)
	}{
		{"domain", func(p *V2Input) { p.Domain += "x" }}, {"version", func(p *V2Input) { p.ProofVersion = 1 }},
		{"basis_version", func(p *V2Input) { p.BasisVersion = 2 }}, {"catalog_version_zero", func(p *V2Input) { p.CatalogDescriptorVersion = 0 }},
		{"postgres_major", func(p *V2Input) { p.CatalogPostgresMajor = 17 }},
		{"nil_uuid", func(p *V2Input) { p.EpochID = "00000000-0000-0000-0000-000000000000" }},
		{"uppercase_uuid", func(p *V2Input) { p.EpochID = strings.ToUpper(p.EpochID) }},
		{"uuid_without_hyphens", func(p *V2Input) { p.EpochID = strings.ReplaceAll(p.EpochID, "-", "") }},
		{"mode", func(p *V2Input) { p.Mode = "off" }},
		{"empty_host", func(p *V2Input) { p.Database.Host = "" }}, {"socket", func(p *V2Input) { p.Database.Host = "/tmp/pg" }},
		{"host_overflow", func(p *V2Input) { p.Database.Host = strings.Repeat("a", 254) }},
		{"host_nul", func(p *V2Input) { p.Database.Host = "db\x00" }},
		{"host_invalid_utf8", func(p *V2Input) { p.Database.Host = string([]byte{0xff}) }},
		{"db_name_invalid_utf8", func(p *V2Input) { p.Database.Name = string([]byte{0xff}) }},
		{"port_zero", func(p *V2Input) { p.Database.Port = 0 }}, {"db_oid_zero", func(p *V2Input) { p.Database.OID = 0 }},
		{"empty_db_name", func(p *V2Input) { p.Database.Name = "" }}, {"db_name_overflow", func(p *V2Input) { p.Database.Name = strings.Repeat("d", 64) }},
		{"schema_zero_oid", func(p *V2Input) { p.AppSchema.OID = 0 }}, {"schema_system", func(p *V2Input) { p.AppSchema.Name = "information_schema" }},
		{"schema_prefix", func(p *V2Input) { p.AppSchema.Name = "pg_custom" }}, {"schema_nul", func(p *V2Input) { p.AppSchema.Name = "a\x00" }},
		{"namespace_owner_different", func(p *V2Input) { p.AppSchema.NamespaceOwner.OID++ }},
		{"public_wrong_builtin_oid", func(p *V2Input) {
			p.AppSchema.Name = "public"
			p.AppSchema.NamespaceOwner = Role{Name: "pg_database_owner", OID: 6172}
		}},
		{"role_system", func(p *V2Input) { p.AppRole.Name = "pg_anything" }}, {"owner_system", func(p *V2Input) { p.AppSchema.ObjectOwner.Name = "pg_anything" }},
		{"role_zero_oid", func(p *V2Input) { p.AppRole.OID = 0 }}, {"role_name_64_bytes", func(p *V2Input) { p.AppRole.Name = strings.Repeat("界", 21) + "x" }},
		{"duplicate_role_name", func(p *V2Input) { p.ProjectorRole.Name = p.AppRole.Name }}, {"duplicate_role_oid", func(p *V2Input) { p.ProjectorRole.OID = p.AppRole.OID }},
		{"uppercase_digest", func(p *V2Input) { p.CatalogExpectedDigest = strings.Repeat("A", 64) }},
		{"short_digest", func(p *V2Input) { p.CatalogRolesDigest = strings.Repeat("a", 63) }},
		{"nonhex_digest", func(p *V2Input) { p.BasisDescriptorDigest = strings.Repeat("g", 64) }},
		{"manifest_digest_wrong", func(p *V2Input) { p.ManifestDigest = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inputFixture(t, "ordinary.json")
			tc.mutate(&p)
			got, err := EncodeV2(p)
			if got != nil || err != ErrCanonical {
				t.Fatalf("proof=%t error=%v", got != nil, err)
			}
		})
	}
}

func TestNullableUnionEveryCombination(t *testing.T) {
	for _, mode := range []string{"ordinary", "sandbox"} {
		for mask := 0; mask < 8; mask++ {
			t.Run(fmt.Sprintf("%s_%d", mode, mask), func(t *testing.T) {
				p := inputFixture(t, "sandbox.json")
				p.Mode = mode
				if mask&1 == 0 {
					p.Sandbox = nil
				}
				if mask&2 == 0 {
					p.PredecessorPaymentDigest = nil
				}
				if mask&4 == 0 {
					p.PredecessorPaymentCanonicalBase64URL = nil
				}
				got, err := EncodeV2(p)
				valid := mode == "ordinary" && mask == 0 || mode == "sandbox" && mask == 7
				if valid {
					if err != nil {
						t.Fatal(err)
					}
				} else if got != nil || err != ErrCanonical {
					t.Fatal("partial union accepted")
				}
			})
		}
	}
}

func TestManifestClosedArrayAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []Migration
		valid   bool
	}{
		{"nil", nil, false}, {"empty", []Migration{}, false},
		{"sorted", []Migration{{"001_a.sql", strings.Repeat("1", 64)}, {"099_b.sql", strings.Repeat("2", 64)}}, true},
		{"unsorted", []Migration{{"099_b.sql", strings.Repeat("1", 64)}, {"001_a.sql", strings.Repeat("2", 64)}}, false},
		{"duplicate", []Migration{{"001_a.sql", strings.Repeat("1", 64)}, {"001_a.sql", strings.Repeat("2", 64)}}, false},
		{"path", []Migration{{"../001_a.sql", strings.Repeat("1", 64)}}, false},
		{"uppercase", []Migration{{"001_A.sql", strings.Repeat("1", 64)}}, false},
		{"unicode", []Migration{{"001_测试.sql", strings.Repeat("1", 64)}}, false},
		{"bad_hash", []Migration{{"001_a.sql", strings.Repeat("A", 64)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := inputFixture(t, "ordinary.json")
			setManifest(t, &p, tc.entries)
			got, err := EncodeV2(p)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if got != nil || err != ErrCanonical {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	p := inputFixture(t, "ordinary.json")
	entries := make([]Migration, 256)
	for i := range entries {
		entries[i] = Migration{fmt.Sprintf("001_%03d_%s.sql", i, strings.Repeat("a", 243)), strings.Repeat("1", 64)}
	}
	if len(entries[0].Name) != 255 {
		t.Fatal("test boundary")
	}
	setManifest(t, &p, entries)
	if _, err := EncodeV2(p); err != nil {
		t.Fatal("256-entry/255-byte boundary", err)
	}
	entries = append(entries, Migration{"999_a.sql", strings.Repeat("1", 64)})
	setManifest(t, &p, entries)
	if got, err := EncodeV2(p); got != nil || err != ErrCanonical {
		t.Fatal("257 entries accepted")
	}
	entries = entries[:1]
	entries[0].Name += "a"
	setManifest(t, &p, entries)
	if got, err := EncodeV2(p); got != nil || err != ErrCanonical {
		t.Fatal("256-byte filename accepted")
	}
}

func TestOwnedImmutableAndRedacted(t *testing.T) {
	for _, name := range []string{"ordinary.json", "sandbox.json"} {
		t.Run(name, func(t *testing.T) {
			input := inputFixture(t, name)
			p, err := EncodeV2(input)
			if err != nil {
				t.Fatal(err)
			}
			original := p.Bytes()
			wantSHA := p.SHA256()
			input.MigrationManifest[0].Name = "changed"
			if input.Sandbox != nil {
				input.Sandbox.MockSchema.Name = "changed"
				*input.PredecessorPaymentDigest = "changed"
				*input.PredecessorPaymentCanonicalBase64URL = "changed"
			}
			b, _ := p.Bindings()
			b.MigrationManifest[0].SHA256 = "changed"
			if b.Sandbox != nil {
				b.Sandbox.WorkerRole.Name = "changed"
				*b.PredecessorPaymentDigest = "changed"
				*b.PredecessorPaymentCanonicalBase64URL = "changed"
			}
			out := p.Bytes()
			out[0] = 'x'
			copyValue := *p
			if !bytes.Equal(p.Bytes(), original) || p.SHA256() != wantSHA || !bytes.Equal(copyValue.Bytes(), original) {
				t.Fatal("mutable alias")
			}
			raw := p.Bytes()
			decoded, err := DecodeV2(raw)
			if err != nil {
				t.Fatal(err)
			}
			raw[0] = 'x'
			if !bytes.Equal(decoded.Bytes(), original) {
				t.Fatal("decode retained input alias")
			}
			for _, v := range []any{p, *p} {
				if fmt.Sprintf("%v", v) != "fundinglotproof.CanonicalV2(redacted)" || fmt.Sprintf("%#v", v) != "fundinglotproof.CanonicalV2(redacted)" {
					t.Fatal("format not redacted")
				}
				encoded, err := json.Marshal(v)
				if err != nil || string(encoded) != `{"redacted":true}` {
					t.Fatal("JSON not redacted")
				}
			}
		})
	}
}

func TestZeroAndJSONCannotConstruct(t *testing.T) {
	for _, p := range []*CanonicalV2{nil, {}} {
		if p.Bytes() != nil || p.SHA256() != "" {
			t.Fatal("zero valid")
		}
		b, err := p.Bindings()
		if !reflect.DeepEqual(b, V2Bindings{}) || err != ErrCanonical {
			t.Fatal("zero bindings")
		}
	}
	for _, v := range []any{CanonicalV2{}, &CanonicalV2{}} {
		if fmt.Sprintf("%v", v) != "fundinglotproof.CanonicalV2(redacted)" || fmt.Sprintf("%#v", v) != "fundinglotproof.CanonicalV2(redacted)" {
			t.Fatal("zero formatting not redacted")
		}
		raw, err := json.Marshal(v)
		if err != nil || string(raw) != `{"redacted":true}` {
			t.Fatal("zero JSON not redacted")
		}
	}
	var nilProof *CanonicalV2
	// fmt/json special-case nil pointers before invoking a value receiver.
	// Their standard nil spellings disclose no bytes or bindings.
	if fmt.Sprintf("%v", nilProof) != "<nil>" || fmt.Sprintf("%#v", nilProof) != "<nil>" {
		t.Fatalf("unexpected nil formatting: %q / %q", fmt.Sprintf("%v", nilProof), fmt.Sprintf("%#v", nilProof))
	}
	nilJSON, err := json.Marshal(nilProof)
	if err != nil || string(nilJSON) != "null" {
		t.Fatal("unexpected nil JSON")
	}
	var p CanonicalV2
	for _, raw := range [][]byte{fixture(t, "ordinary.json"), []byte(`{"state":{"sha":"claimed"}}`), []byte(`{"redacted":true}`)} {
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if p.SHA256() != "" || p.Bytes() != nil {
			t.Fatal("JSON constructed a proof")
		}
	}
}

func TestConcurrentImmutableReaders(t *testing.T) {
	p, err := DecodeV2(fixture(t, "sandbox_128keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	expected := p.SHA256()
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				raw := p.Bytes()
				b, err := p.Bindings()
				if err != nil || testSHA(raw) != expected {
					t.Error("changed immutable state")
					return
				}
				raw[0] = 'x'
				b.MigrationManifest[0].Name = "mutated"
				b.Sandbox.WorkerRole.Name = "mutated"
				*b.PredecessorPaymentDigest = "mutated"
				*b.PredecessorPaymentCanonicalBase64URL = "mutated"
			}
		}()
	}
	wg.Wait()
}
