package fundinglotcatalog

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func privilegeDecoderRecord(kind string, width, bits int) []byte {
	privileges, grants := make([]string, width), make([]string, width)
	for i := range privileges {
		privileges[i], grants[i] = "false", "true"
		if bits&(1<<i) != 0 {
			privileges[i], grants[i] = "true", "false"
		}
	}
	return []byte(fmt.Sprintf(`{"kind": %q, "role": {"$ref": [1260, 4294967295, 0]}, "object": {"$ref": [1259, 2147483648, 1]}, "privileges": [%s], "grant_options": [%s]}`, kind, strings.Join(privileges, ", "), strings.Join(grants, ", ")))
}

func privilegeDecoderEqual(t testing.TB, raw []byte) {
	t.Helper()
	got, gotErr := decodeCatalogJSON(raw)
	want, wantErr := decodeCatalogJSONGeneral(raw)
	if (gotErr != nil) != (wantErr != nil) || !reflect.DeepEqual(got, want) {
		t.Fatalf("optimized and original catalog decoders differ (bytes=%d optimized_error=%t original_error=%t)", len(raw), gotErr != nil, wantErr != nil)
	}
}

func TestCatalogPrivilegeDecoderExactVectorsReferencesAndOwnership(t *testing.T) {
	for _, item := range []struct {
		kind  string
		width int
	}{{"table", 7}, {"column", 4}, {"function", 1}, {"type", 1}, {"schema", 2}} {
		for bits := 0; bits < 1<<item.width; bits++ {
			raw := privilegeDecoderRecord(item.kind, item.width, bits)
			fast, ok := decodeEffectivePrivilegeJSON(raw)
			if !ok {
				t.Fatal("fixed PostgreSQL privilege shape missed fast decoder")
			}
			privilegeDecoderEqual(t, raw)
			before, _ := canonical(fast)
			for i := range raw {
				raw[i] = 'x'
			}
			after, _ := canonical(fast)
			if !bytes.Equal(before, after) {
				t.Fatal("privilege DTO retained driver row bytes")
			}
		}
	}
}

func TestCatalogPrivilegeDecoderFallbackAndFailureDifferential(t *testing.T) {
	raw := privilegeDecoderRecord("column", 4, 5)
	cases := [][]byte{
		[]byte(`{}`), []byte(`null`), []byte(`[true,null,1.25,"text"]`),
		append(append([]byte(nil), raw...), []byte(` {}`)...),
		[]byte(strings.Replace(string(raw), `"role":`, `"Role":`, 1)),
		[]byte(strings.Replace(string(raw), `"kind":`, `"k\u0069nd":`, 1)),
		[]byte(strings.Replace(string(raw), `"column"`, `"col\u0075mn"`, 1)),
		[]byte(strings.Replace(string(raw), `"column"`, `"列"`, 1)),
		[]byte(strings.Replace(string(raw), `"column"`, `"\u0000"`, 1)),
		[]byte(strings.Replace(string(raw), `"column"`, `"unknown"`, 1)),
		[]byte(strings.Replace(string(raw), `"role":`, `"extra": true, "role":`, 1)),
		[]byte(strings.Replace(string(raw), `"role":`, `"kind": "table", "role":`, 1)),
		[]byte(strings.Replace(string(raw), `4294967295`, `4294967296`, 1)),
		[]byte(strings.Replace(string(raw), `4294967295`, `00001`, 1)),
		[]byte(strings.Replace(string(raw), `4294967295`, `1e2`, 1)),
		[]byte(strings.Replace(string(raw), `4294967295`, `-0`, 1)),
		[]byte(strings.Replace(string(raw), `4294967295`, `1.25`, 1)),
		[]byte(strings.Replace(string(raw), `[1260, 4294967295, 0]`, `[1260, 1]`, 1)),
		[]byte(strings.Replace(string(raw), `[1260, 4294967295, 0]`, `[1260, 1, 0, 9]`, 1)),
		[]byte(strings.Replace(string(raw), `[1260, 4294967295, 0]`, `null`, 1)),
		[]byte(strings.Replace(string(raw), `true`, `null`, 1)),
		[]byte(strings.Replace(string(raw), `true`, `1`, 1)),
		[]byte(strings.Replace(string(raw), `true`, `true false`, 1)),
		privilegeDecoderRecord("table", 8, 3),
		bytes.Repeat([]byte(" "), collectorRecordLimit+1),
		[]byte{0xff}, []byte(strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65)),
	}
	for _, value := range cases {
		if _, ok := decodeEffectivePrivilegeJSON(value); ok {
			t.Fatal("non-fixed shape did not fall back to the original decoder")
		}
		privilegeDecoderEqual(t, value)
	}
	for i := 0; i < len(raw); i++ {
		privilegeDecoderEqual(t, raw[:i])
		for _, replace := range []byte{'"', '\\', '{', '}', '[', ']', ',', ':', 0, 0xff} {
			mutated := append([]byte(nil), raw...)
			mutated[i] = replace
			privilegeDecoderEqual(t, mutated)
		}
	}
}

func FuzzCatalogPrivilegeDecoderDifferential(f *testing.F) {
	for _, raw := range [][]byte{privilegeDecoderRecord("column", 4, 5), privilegeDecoderRecord("table", 7, 127), []byte(`{"kind":`), []byte(`null`), []byte(`{"value":[1.25,true,null]}`)} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > collectorRecordLimit+1 {
			t.Skip()
		}
		privilegeDecoderEqual(t, raw)
	})
}

func BenchmarkCatalogPrivilegeJSON(b *testing.B) {
	raw := privilegeDecoderRecord("column", 4, 5)
	for _, name := range []string{"original", "optimized"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				if name == "original" {
					_, err = decodeCatalogJSONGeneral(raw)
				} else {
					_, err = decodeCatalogJSON(raw)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
