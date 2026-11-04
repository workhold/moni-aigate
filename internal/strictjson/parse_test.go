package strictjson_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

func TestParseRejectsAmbiguousAndLossyInputs(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty":              nil,
		"BOM":                []byte("\xef\xbb\xbf{}"),
		"invalid UTF8":       {'"', 0xff, '"'},
		"duplicate":          []byte(`{"x":1,"x":2}`),
		"escaped duplicate":  []byte(`{"x":1,"\u0078":2}`),
		"nested duplicate":   []byte(`{"data":[{"x":1,"x":1}]}`),
		"high surrogate":     []byte(`"\ud800"`),
		"low surrogate":      []byte(`"\udc00"`),
		"unpaired surrogate": []byte(`"\ud800\u0041"`),
		"truncated unicode":  []byte(`"\u123"`),
		"invalid escape":     []byte(`"\q"`),
		"raw control":        []byte{'"', '\n', '"'},
		"trailing object":    []byte(`{} {}`),
		"trailing scalar":    []byte(`true false`),
		"truncated":          []byte(`{"x":`),
		"depth":              []byte(strings.Repeat("[", 9) + "0" + strings.Repeat("]", 9)),
		"empty array depth":  []byte(strings.Repeat("[", 9) + strings.Repeat("]", 9)),
		"empty object depth": []byte(strings.Repeat(`{"x":`, 8) + `{}` + strings.Repeat("}", 8)),
	} {
		t.Run(name, func(t *testing.T) {
			if value, err := strictjson.Parse(raw, 1024); value != nil || !errors.Is(err, strictjson.ErrInvalidJSON) {
				t.Fatal("ambiguous input returned data or wrong error", err)
			}
		})
	}
}

func TestParsePreservesValuesAndOwnsOutput(t *testing.T) {
	raw := []byte(`{"x":[9223372036854775807,1e100,null,true,"\ud834\udd1e","\\uD800"]}`)
	value, err := strictjson.Parse(raw, len(raw))
	want := map[string]any{"x": []any{json.Number("9223372036854775807"), json.Number("1e100"), nil, true, "𝄞", `\uD800`}}
	if err != nil || !reflect.DeepEqual(value, want) {
		t.Fatal("valid JSON lost a number, escape or value", value, err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if !reflect.DeepEqual(value, want) {
		t.Fatal("decoded output retained caller's mutable bytes")
	}
	if _, err := strictjson.Parse([]byte(strings.Repeat("[", 8)+"0"+strings.Repeat("]", 8)), 100); err != nil {
		t.Fatal("allowed depth boundary rejected", err)
	}
	for _, raw := range []string{strings.Repeat("[", 8) + strings.Repeat("]", 8), strings.Repeat(`{"x":`, 7) + `{}` + strings.Repeat("}", 7)} {
		if _, err := strictjson.Parse([]byte(raw), 100); err != nil {
			t.Fatal("allowed empty container depth boundary rejected", err)
		}
	}
}

func TestParseEnforcesByteLimitBeforeSyntax(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{}`), []byte(`{x`)} {
		if value, err := strictjson.Parse(raw, 1); value != nil || !errors.Is(err, strictjson.ErrTooLarge) {
			t.Fatal("oversized input was parsed or returned a partial result", err)
		}
	}
	if value, err := strictjson.Parse([]byte(`{}`), 2); err != nil || !reflect.DeepEqual(value, map[string]any{}) {
		t.Fatal("exact byte boundary rejected", value, err)
	}
}
