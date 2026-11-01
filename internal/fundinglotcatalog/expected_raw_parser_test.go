package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

func compareExpectedRawParsers(t testing.TB, raw []byte, limit int) {
	t.Helper()
	before, wantErr := strictJSONNodes(raw, limit)
	after, gotErr := strictExpectedRawJSONNodes(raw, limit)
	if (wantErr == nil) != (gotErr == nil) || wantErr != nil && wantErr != ErrCatalog || gotErr != nil && gotErr != ErrCatalog {
		t.Fatalf("acceptance mismatch limit=%d raw=%.200q want=%v got=%v", limit, raw, wantErr, gotErr)
	}
	if wantErr == nil && !reflect.DeepEqual(before, after) {
		t.Fatalf("tree mismatch limit=%d raw=%.200q", limit, raw)
	}
}

func TestExpectedRawJSONCorpus(t *testing.T) {
	cases := []string{"", " ", "{}", "[]", "null", "true", "false", "0", "-1", "1.25", "0.0001", "-0.1", "-0", "1e2", "1E+2", "1.0", "0.10", "01", "+1", ".1", "1.", "--1", "1+2", "1true", "-", "truex", "null0", "[1,]", "{\"x\":1,}", "[1 2]", "[1:2]", "{\"x\" 1}", "{1:2}", "{} []", "0 0", "NaN", "Infinity", "\ufeff{}", "[\v]", "[\f]",
		"\"plain\"", "\"界🙂\"", "\"\\u754c\\ud83d\\ude42\"", "\"\\ud800\"", "\"\\udc00\"", "\"\\ud800x\"", "\"\\ud800\\ud800\"", "\"\\u0000\"", "\"\\n\\r\\t\\b\\f\\\\\\\"\\/\"", "\"\\u12\"", "\"\\uGGGG\"", "\"\\x\"", "\"unfinished", "\"unfinished\\",
		"{\"x\":1,\"x\":2}", "{\"x\":1,\"\\u0078\":2}", "{\"\\ud800\":1,\"�\":2}", "{\"x\":{\"a\":1,\"a\":2}}", "{\"\\u0000\":1}", "{\"a\":[true,null,\"value\"]}",
	}
	cases = append(cases, "\"a"+string([]byte{0})+"b\"", string([]byte{0xff}), "\""+string([]byte{0xff})+"\"", string([]byte{0xc3, 0x28}))
	for _, raw := range cases {
		compareExpectedRawParsers(t, []byte(raw), 4000000)
	}
	for _, n := range []int{63, 64, 65} {
		compareExpectedRawParsers(t, []byte(strings.Repeat("[", n)+"0"+strings.Repeat("]", n)), 4000000)
	}
	for _, n := range []int{0, 1, 2, 3, 4, 5} {
		for _, raw := range []string{"0", "[]", "[1,2,3]", "{\"a\":[true,null]}"} {
			compareExpectedRawParsers(t, []byte(raw), n)
		}
	}
	for _, n := range []int{63, 64, 65} {
		compareExpectedRawParsers(t, []byte(strings.Repeat("1", n)), 4000000)
	}
}

func TestExpectedRawJSONTextAndArrayBounds(t *testing.T) {
	for _, n := range []int{65535, 65536, 65537} {
		compareExpectedRawParsers(t, []byte("\""+strings.Repeat("x", n)+"\""), 4000000)
		compareExpectedRawParsers(t, []byte("{\""+strings.Repeat("x", n)+"\":true}"), 4000000)
		suffix := strings.Repeat("a", n-65535)
		compareExpectedRawParsers(t, []byte("\""+strings.Repeat("界", 21845)+suffix+"\""), 4000000)
		compareExpectedRawParsers(t, []byte("\""+strings.Repeat("\\u754c", 21845)+suffix+"\""), 4000000)
	}
	for _, n := range []int{99999, 100000, 100001} {
		compareExpectedRawParsers(t, []byte("["+strings.Repeat("0,", n-1)+"0]"), 4000000)
	}
}

func TestExpectedRawJSONOwnedStrings(t *testing.T) {
	raw := []byte("{\"key\":\"value\",\"n\":12.5,\"e\":\"\\u754c\"}")
	tree, e := strictExpectedRawJSONNodes(raw, 4000000)
	if e != nil {
		t.Fatal(e)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	want := object{"key": "value", "n": json.Number("12.5"), "e": "界"}
	if !reflect.DeepEqual(tree, want) {
		t.Fatal("returned strings alias caller bytes")
	}
}

func generatedExpectedRawJSON(r *rand.Rand, depth int) string {
	scalar := []string{"true", "false", "null", "0", "-1", "123.45", "\"plain\"", "\"界🙂\"", "\"\\ud800\"", "\"\\n\\\\\\\"\\t\"", "\"\\u0078\""}
	if depth > 5 || r.Intn(3) == 0 {
		return scalar[r.Intn(len(scalar))]
	}
	n := r.Intn(5)
	parts := make([]string, n)
	if r.Intn(2) == 0 {
		for i := range parts {
			parts[i] = generatedExpectedRawJSON(r, depth+1)
		}
		return "[ " + strings.Join(parts, " , ") + " ]"
	}
	for i := range parts {
		parts[i] = fmt.Sprintf("%q : %s", fmt.Sprintf("key_%d_%d", i, r.Intn(100)), generatedExpectedRawJSON(r, depth+1))
	}
	return "{ " + strings.Join(parts, " , ") + " }"
}

func TestExpectedRawJSONRandomDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(20261102))
	alphabet := []byte("{}[],:\"\\truefalsnul0123456789.eE+- abcxyz\n\t\r\x00")
	for i := 0; i < 20000; i++ {
		raw := []byte(generatedExpectedRawJSON(r, 0))
		compareExpectedRawParsers(t, raw, 4000000)
		switch r.Intn(4) {
		case 0:
			if len(raw) > 0 {
				raw = raw[:r.Intn(len(raw))]
			}
		case 1:
			raw = append(raw, alphabet[r.Intn(len(alphabet))])
		case 2:
			if len(raw) > 0 {
				raw[r.Intn(len(raw))] = alphabet[r.Intn(len(alphabet))]
			}
		case 3:
			n := r.Intn(128)
			raw = make([]byte, n)
			for j := range raw {
				raw[j] = byte(r.Intn(256))
			}
		}
		compareExpectedRawParsers(t, raw, 1+r.Intn(400))
	}
}

func TestExpectedRawJSONFullRoots(t *testing.T) {
	for _, mode := range []Mode{Ordinary, Sandbox} {
		r := reviewedExpectedRoots[mode]
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			compareExpectedRawParsers(t, r.canonical, 4000000)
			d, e := decodeExpected(r.canonical, r.sha, mode)
			if e != nil || d == nil {
				t.Fatalf("full decode failed %v", e)
			}
		})
	}
}

func TestExpectedRawJSONCompleteGates(t *testing.T) {
	for _, mode := range []Mode{Ordinary, Sandbox} {
		r := reviewedExpectedRoots[mode]
		if _, e := decodeExpected(r.canonical, strings.Repeat("0", 64), mode); e != ErrCatalog {
			t.Fatal("SHA mismatch accepted")
		}
		whitespace := append([]byte(" "), r.canonical...)
		if _, e := decodeExpected(whitespace, digest(whitespace), mode); e != ErrCatalog {
			t.Fatal("noncanonical bytes accepted")
		}
		if _, e := decodeExpected(r.canonical, r.sha, Mode(0)); e != ErrCatalog {
			t.Fatal("wrong mode accepted")
		}
		trailing := append(append([]byte{}, r.canonical...), []byte(" true")...)
		if _, e := decodeExpected(trailing, digest(trailing), mode); e != ErrCatalog {
			t.Fatal("trailing value accepted")
		}
	}
}

func TestExpectedRawJSONLargeBounds(t *testing.T) {
	if os.Getenv("C0_EXPECTED_RAW_LARGE_TEST") != "1" {
		t.Skip("explicit memory-bound differential run")
	}
	raw := append([]byte("true"), bytes.Repeat([]byte{' '}, collectorByteLimit-4)...)
	compareExpectedRawParsers(t, raw, 4000000)
	raw = append(raw, ' ')
	compareExpectedRawParsers(t, raw, 4000000)
	for _, nodes := range []int{3999999, 4000000, 4000001} {
		parts := make([]string, 40)
		remaining := nodes - 41
		for i := range parts {
			n := remaining
			if n > 100000 {
				n = 100000
			}
			parts[i] = "[" + strings.Repeat("0,", n-1) + "0]"
			remaining -= n
		}
		raw = []byte("[" + strings.Join(parts, ",") + "]")
		compareExpectedRawParsers(t, raw, 4000000)
	}
}

func FuzzExpectedRawJSONDifferential(f *testing.F) {
	for _, raw := range []string{"{\"x\":[true,null,1.25,\"界\"]}", "{\"x\":1,\"\\u0078\":2}", "\"\\ud800\"", "[1,]", "0 0", "\"\\u0000\""} {
		f.Add([]byte(raw), uint16(100))
	}
	f.Fuzz(func(t *testing.T, raw []byte, limit uint16) {
		if len(raw) > 1<<20 {
			t.Skip()
		}
		compareExpectedRawParsers(t, raw, int(limit))
	})
}
