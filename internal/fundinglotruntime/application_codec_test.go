package fundinglotruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func applicationTestJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var result any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func applicationTestMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func applicationTestAt(value any, path []string) any {
	for _, part := range path {
		switch current := value.(type) {
		case map[string]any:
			value = current[part]
		case []any:
			index, _ := strconv.Atoi(part)
			value = current[index]
		}
	}
	return value
}

func applicationTestSet(value any, path []string, replacement any) {
	parent := applicationTestAt(value, path[:len(path)-1])
	key := path[len(path)-1]
	switch current := parent.(type) {
	case map[string]any:
		current[key] = replacement
	case []any:
		index, _ := strconv.Atoi(key)
		current[index] = replacement
	}
}

func applicationTestFieldPaths(value any, path []string) [][]string {
	var result [][]string
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := append(append([]string(nil), path...), key)
			result = append(result, child)
			result = append(result, applicationTestFieldPaths(current[key], child)...)
		}
	case []any:
		for i, child := range current {
			result = append(result, applicationTestFieldPaths(child, append(append([]string(nil), path...), strconv.Itoa(i)))...)
		}
	}
	return result
}

func applicationTestDecode(t *testing.T, raw []byte, valid bool) {
	t.Helper()
	b, err := newApplicationBudget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.cancel()
	var m applicationManifest
	err = applicationStrictDecode(b, raw, &m)
	if valid && err != nil {
		t.Fatalf("valid strict manifest rejected: %v", err)
	}
	if !valid && err != ErrRuntime {
		t.Fatalf("invalid strict manifest accepted or wrong error: %v", err)
	}
}

func TestApplicationStrictManifestEveryField(t *testing.T) {
	m, _ := applicationTestManifest(t, 1)
	raw := applicationTestRaw(t, m)
	root := applicationTestJSON(t, raw)
	paths := applicationTestFieldPaths(root, nil)
	for _, path := range paths {
		for _, mutation := range []string{"missing", "null", "wrong_type", "duplicate", "escaped_duplicate"} {
			t.Run(strings.Join(path, "/")+"/"+mutation, func(t *testing.T) {
				value := applicationTestJSON(t, raw)
				parentPath, key := path[:len(path)-1], path[len(path)-1]
				parent := applicationTestAt(value, parentPath).(map[string]any)
				var invalid []byte
				switch mutation {
				case "missing":
					delete(parent, key)
				case "null":
					parent[key] = nil
				case "wrong_type":
					if _, object := parent[key].(map[string]any); object {
						parent[key] = "wrong"
					} else {
						parent[key] = map[string]any{"wrong": true}
					}
				case "duplicate", "escaped_duplicate":
					quoted := string(applicationTestMarshal(t, key))
					if mutation == "escaped_duplicate" {
						quoted = fmt.Sprintf(`"\u%04x%s"`, key[0], key[1:])
					}
					body := applicationTestMarshal(t, parent)
					duplicate := []byte("{" + quoted + ":" + string(applicationTestMarshal(t, parent[key])) + "," + string(body[1:]))
					if len(parentPath) == 0 {
						invalid = duplicate
					} else {
						applicationTestSet(value, parentPath, json.RawMessage(duplicate))
					}
				}
				if invalid == nil {
					invalid = applicationTestMarshal(t, value)
				}
				applicationTestDecode(t, invalid, false)
			})
		}
	}
	// Reject an unknown member at every object, including array elements.
	objects := [][]string{nil}
	for _, path := range paths {
		if _, ok := applicationTestAt(root, path).(map[string]any); ok {
			objects = append(objects, path)
		}
		if list, ok := applicationTestAt(root, path).([]any); ok {
			for i, child := range list {
				if _, ok := child.(map[string]any); ok {
					objects = append(objects, append(append([]string(nil), path...), strconv.Itoa(i)))
				}
			}
		}
	}
	for _, path := range objects {
		t.Run("unknown/"+strings.Join(path, "/"), func(t *testing.T) {
			value := applicationTestJSON(t, raw)
			applicationTestAt(value, path).(map[string]any)["unknown_member"] = true
			applicationTestDecode(t, applicationTestMarshal(t, value), false)
		})
	}
}

func TestApplicationStrictManifestNumberTextAndDepth(t *testing.T) {
	m, _ := applicationTestManifest(t, 1)
	raw := applicationTestRaw(t, m)
	for _, field := range []struct{ find, replacement string }{
		{`"manifest_version":1`, `"manifest_version":1e0`},
		{`"manifest_version":1`, `"manifest_version":1.0`},
		{`"manifest_version":1`, `"manifest_version":-0`},
		{`"manifest_version":1`, `"manifest_version":-1`},
		{`"manifest_version":1`, `"manifest_version":01`},
		{`"manifest_version":1`, `"manifest_version":18446744073709551616`},
		{`"manifest_version":1`, `"manifest_version":9223372036854775808`},
		{`"proof_version":1`, `"proof_version":1e0`},
		{`"protocol_version":1`, `"protocol_version":-0`},
		{`"port":55439`, `"port":65536`},
		{`"port":55439`, `"port":55439.0`},
	} {
		t.Run(field.replacement, func(t *testing.T) {
			if !bytes.Contains(raw, []byte(field.find)) {
				t.Fatal("mutation did not match")
			}
			applicationTestDecode(t, bytes.Replace(raw, []byte(field.find), []byte(field.replacement), 1), false)
		})
	}
	for _, suffix := range []string{"{}", "null", "false", "0", "[", "garbage"} {
		t.Run("trailing/"+suffix, func(t *testing.T) { applicationTestDecode(t, append(bytes.Clone(raw), []byte(suffix)...), false) })
	}
	for _, escaped := range []string{`\ud800`, `\udfff`, `\ud800\u0041`, `\uZZZZ`, `\q`, "\x01", string([]byte{0xff})} {
		t.Run(fmt.Sprintf("bad_text/%x", escaped), func(t *testing.T) {
			bad := bytes.Replace(raw, []byte(`"component":"application"`), []byte(`"component":"`+escaped+`"`), 1)
			applicationTestDecode(t, bad, false)
		})
	}
	applicationTestDecode(t, bytes.Replace(raw, []byte(`"component":"application"`), []byte(`"component":"\ud83d\ude00"`), 1), true)
	applicationTestDecode(t, append([]byte(" \n\t"), append(bytes.Clone(raw), '\n')...), true)
	applicationTestDecode(t, []byte{}, false)
	applicationTestDecode(t, []byte("null"), false)
	for _, depth := range []int{12, 13} {
		b, err := newApplicationBudget(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer b.cancel()
		decoder := json.NewDecoder(strings.NewReader(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)))
		decoder.UseNumber()
		_, ok := applicationReadJSONValue(b, decoder, 0)
		if ok != (depth == 12) {
			t.Fatalf("historical root-zero depth %d changed", depth)
		}
	}
}

func FuzzApplicationManifestStrict(f *testing.F) {
	// Standalone bytes ensure fuzzing does not need files, keys or a database.
	manifest, _ := applicationTestManifest(f, 1)
	f.Add(applicationTestRaw(f, manifest))
	f.Add([]byte(`{"manifest_version":1}`))
	f.Add([]byte(`{"x":1,"\u0078":2}`))
	f.Add([]byte(`{"x":"\ud800"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		b, err := newApplicationBudget(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer b.cancel()
		var parsed applicationManifest
		err = applicationStrictDecode(b, raw, &parsed)
		if err != nil && err != ErrRuntime {
			t.Fatal("parser leaked nonconstant error")
		}
		if err == nil {
			encoded, err := json.Marshal(parsed)
			if err != nil {
				t.Fatal(err)
			}
			applicationTestDecode(t, encoded, true)
		}
	})
}
