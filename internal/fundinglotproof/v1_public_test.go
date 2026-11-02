package fundinglotproof

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func v1InputFixture(t testing.TB, name string) V1Input {
	t.Helper()
	var p V1Input
	if err := json.Unmarshal(fixture(t, name), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestV1PublicIndependentGolden(t *testing.T) {
	for _, tc := range []struct{ name, sha string }{
		{"predecessor_v1.json", "1a6c1815c400ddf7b051c0c12f0fe203ff43484251aabd34d8f9361ab704b67c"},
		{"predecessor_1keys.json", "d78a12de58fef9e438793abc895dc8f136fa1456bfdf8870222970597ac29596"},
		{"predecessor_128keys.json", "3d93d9cb91071b759eafbfb88f22c58c15fe9dec83d774cfbd89e0a521015060"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixture(t, tc.name)
			if testSHA(raw) != tc.sha {
				t.Fatal("independent historical bytes changed")
			}
			for _, call := range []func() (*CanonicalV1, error){
				func() (*CanonicalV1, error) { return DecodeV1(raw) },
				func() (*CanonicalV1, error) { return EncodeV1(v1InputFixture(t, tc.name)) },
			} {
				p, err := call()
				if err != nil || p == nil || p.SHA256() != tc.sha || !bytes.Equal(p.Bytes(), raw) {
					t.Fatal("historical canonical bytes or SHA changed")
				}
				bindings, err := p.Bindings()
				if err != nil || !reflect.DeepEqual(bindings, v1InputFixture(t, tc.name)) {
					t.Fatal("historical binding lost")
				}
			}
		})
	}
	// This golden independently fixes SetEscapeHTML(false), raw UTF-8, and
	// encoding/json's mandatory escaping of the two JSONP separators.
	raw := fixture(t, "predecessor_v1.json")
	if !bytes.Contains(raw, []byte("<A&B>")) || !bytes.Contains(raw, []byte("测试")) || !bytes.Contains(raw, []byte(`\u2028`)) {
		t.Fatal("historical text vector incomplete")
	}
}

func TestV1PublicPreservesHistoricalSuccessBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*V1Input)
	}{
		{"reserved_role_names", func(p *V1Input) {
			roles := []*V1Role{&p.App.Owner, &p.App.Runtime, &p.Mock.Owner, &p.Mock.Runtime,
				&p.Receiver.Owner, &p.Receiver.Runtime, &p.Worker, &p.Projector}
			for i, r := range roles {
				r.Name = fmt.Sprintf("pg_legacy_%d", i)
			}
		}},
		{"cross_catalog_oid_categories", func(p *V1Input) { p.Database.OID, p.App.OID = p.Worker.OID, p.App.Owner.OID }},
		{"equal_times_shared_digests", func(p *V1Input) {
			for i := range p.Keys {
				p.Keys[i].Digest, p.Keys[i].NotAfter = p.Keys[0].Digest, p.Keys[i].NotBefore
			}
			p.WorkerNonceDigest, p.APITokenDigest = p.AppNonceDigest, p.AppNonceDigest
		}},
		{"maximum_oid_and_origin_ports", func(p *V1Input) {
			p.Worker.OID = ^uint32(0)
			p.APIOrigin, p.ControlOrigin = "http://127.0.0.1:1", "http://127.0.0.1:65535"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := v1InputFixture(t, "predecessor_v1.json")
			tc.mutate(&p)
			proof, err := EncodeV1(p)
			if err != nil || proof == nil {
				t.Fatal("historical pure-format success boundary tightened")
			}
			if got, err := DecodeV1(proof.Bytes()); err != nil || got.SHA256() != proof.SHA256() {
				t.Fatal("successful historical format does not round-trip")
			}
		})
	}
}

func TestV1PublicRejectsInvalidTypedTextBeforeRepair(t *testing.T) {
	original := v1InputFixture(t, "predecessor_v1.json")
	var paths [][]int
	var visit func(reflect.Value, []int)
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
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := copyV1Bindings(original)
			v := reflect.ValueOf(&p).Elem()
			for _, i := range path {
				if v.Kind() == reflect.Struct {
					v = v.Field(i)
				} else {
					v = v.Index(i)
				}
			}
			v.SetString(string([]byte{0xff}))
			if proof, err := EncodeV1(p); proof != nil || err != ErrCanonical {
				t.Fatal("invalid UTF-8 was repaired or returned partial proof")
			}
		})
	}
}

func TestV1PublicRejectsRawAndNoncanonicalBytes(t *testing.T) {
	raw := fixture(t, "predecessor_v1.json")
	for _, bad := range [][]byte{
		nil, {}, []byte("null"), []byte("{}"), append([]byte{0xef, 0xbb, 0xbf}, raw...),
		append([]byte{' '}, raw...), append(bytes.Clone(raw), '\n'), append(bytes.Clone(raw), []byte("{}")...),
		bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1e0`), 1),
		bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":-0`), 1),
		bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":1,"proof_\u0076ersion":1`), 1),
		bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":null`), 1),
		bytes.Replace(raw, []byte(`"proof_version":1`), []byte(`"proof_version":2`), 1),
		bytes.Replace(raw, []byte(`"proof_version":1,"protocol_version":1`), []byte(`"protocol_version":1,"proof_version":1`), 1),
		bytes.Replace(raw, []byte("<A&B>"), []byte(`\u003cA\u0026B\u003e`), 1),
		bytes.Replace(raw, []byte("测试"), []byte(`\ud800`), 1),
		bytes.Replace(raw, []byte("测试"), []byte(`\udc00`), 1),
		bytes.Replace(raw, []byte("测试"), []byte{0xff}, 1),
		bytes.Repeat([]byte{' '}, 65536), bytes.Repeat([]byte{' '}, 65537),
		[]byte(strings.Repeat("[", 14) + strings.Repeat("]", 14)),
	} {
		if proof, err := DecodeV1(bad); proof != nil || err != ErrCanonical {
			t.Fatal("invalid raw accepted or returned partial proof")
		}
	}
	if proof, err := EncodeV1(V1Input{}); proof != nil || err != ErrCanonical {
		t.Fatal("zero input accepted")
	}
}

func TestV1PublicOwnedImmutableAndRedacted(t *testing.T) {
	input := v1InputFixture(t, "predecessor_v1.json")
	p, err := EncodeV1(input)
	if err != nil {
		t.Fatal(err)
	}
	original, sha := p.Bytes(), p.SHA256()
	input.Keys[0].ID = "changed"
	raw := p.Bytes()
	raw[0] = '!'
	b, err := p.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	b.Keys[0].ID = "changed"
	copy := *p
	if !bytes.Equal(copy.Bytes(), original) || p.SHA256() != sha {
		t.Fatal("mutable result or input aliases canonical state")
	}
	owned, err := copy.Bindings()
	if err != nil || !reflect.DeepEqual(owned, v1InputFixture(t, "predecessor_v1.json")) {
		t.Fatal("input or returned key slice aliases stored bindings")
	}
	decodedInput := bytes.Clone(original)
	decoded, err := DecodeV1(decodedInput)
	if err != nil {
		t.Fatal(err)
	}
	decodedInput[0] = '!'
	if !bytes.Equal(decoded.Bytes(), original) {
		t.Fatal("decoder retained caller byte buffer")
	}
	for _, value := range []any{p, *p, CanonicalV1{}} {
		if got := fmt.Sprintf("%s", value); got != "fundinglotproof.CanonicalV1(redacted)" {
			t.Fatal("String leaked format values")
		}
		if got := fmt.Sprintf("%#v", value); got != "fundinglotproof.CanonicalV1(redacted)" {
			t.Fatal("GoString leaked format values")
		}
		if got, err := json.Marshal(value); err != nil || string(got) != `{"redacted":true}` {
			t.Fatal("JSON leaked format values")
		}
	}
	for _, invalid := range []*CanonicalV1{nil, {}} {
		if invalid.Bytes() != nil || invalid.SHA256() != "" {
			t.Fatal("invalid canonical exposed a result")
		}
		if b, err := invalid.Bindings(); err != ErrCanonical || !reflect.DeepEqual(b, V1Bindings{}) {
			t.Fatal("invalid canonical returned bindings")
		}
	}
	for _, forged := range [][]byte{original, []byte(`{"state":{"raw":"e30=","sha":"forged"}}`), []byte(`{"redacted":true}`)} {
		var invalid CanonicalV1
		if err := json.Unmarshal(forged, &invalid); err != nil {
			t.Fatal(err)
		}
		if _, err := invalid.Bindings(); err != ErrCanonical || invalid.Bytes() != nil {
			t.Fatal("JSON constructed valid private state")
		}
	}
	stateType := reflect.TypeOf(CanonicalV1{})
	for i := 0; i < stateType.NumField(); i++ {
		if stateType.Field(i).PkgPath == "" {
			t.Fatal("canonical state is public")
		}
	}
}

func TestV1PublicConcurrentOwnedReads(t *testing.T) {
	p, err := DecodeV1(fixture(t, "predecessor_128keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	original, sha := p.Bytes(), p.SHA256()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				copy := *p
				raw := copy.Bytes()
				bindings, err := copy.Bindings()
				if err != nil || !bytes.Equal(raw, original) || copy.SHA256() != sha {
					t.Error("concurrent immutable read changed")
					return
				}
				raw[0], bindings.Keys[0].ID = '!', "mutated_owned_result"
			}
		}()
	}
	wg.Wait()
	if !bytes.Equal(p.Bytes(), original) {
		t.Fatal("concurrent result mutation escaped")
	}
}

func TestV1PublicRetainsRootZeroDepth(t *testing.T) {
	for _, containers := range []int{13, 14} {
		raw := strings.Repeat("[", containers) + strings.Repeat("]", containers)
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if _, ok := v1ReadJSONValue(d, 0); ok != (containers == 13) {
			t.Fatal("historical root-zero depth limit changed")
		}
		if p, err := DecodeV1([]byte(raw)); p != nil || err != ErrCanonical {
			t.Fatal("non-proof tree accepted")
		}
	}
}

func FuzzDecodeV1(f *testing.F) {
	for _, name := range []string{"predecessor_v1.json", "predecessor_1keys.json", "predecessor_128keys.json"} {
		f.Add(fixture(f, name))
	}
	for _, raw := range []string{"", "null", "{}", `{"proof_version":1}`, strings.Repeat("[", 14)} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var old v1Proof
		want := false
		if v1StrictDecode(raw, &old) == nil && v1ValidateProof(old) == nil {
			canonical, err := v1CanonicalProof(old)
			want = err == nil && bytes.Equal(raw, canonical)
		}
		p, err := DecodeV1(raw)
		if !want {
			if p != nil || err != ErrCanonical {
				t.Fatal("invalid raw accepted or partial result")
			}
			return
		}
		if err != nil || p == nil || !bytes.Equal(p.Bytes(), raw) || p.SHA256() != testSHA(raw) {
			t.Fatal("historical acceptance diverged")
		}
		bindings, err := p.Bindings()
		if err != nil {
			t.Fatal(err)
		}
		again, err := EncodeV1(bindings)
		if err != nil || !bytes.Equal(again.Bytes(), raw) || again.SHA256() != p.SHA256() {
			t.Fatal("accepted format did not round-trip")
		}
	})
}
