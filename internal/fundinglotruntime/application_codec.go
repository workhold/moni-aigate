package fundinglotruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

type applicationSecretFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type applicationMaterial struct {
	ID                string `json:"id"`
	MaterialBase64URL string `json:"material_base64_url"`
}
type applicationManifest struct {
	ManifestVersion int                     `json:"manifest_version"`
	Component       string                  `json:"component"`
	Proof           fundinglotproof.V1Input `json:"proof"`
	ProofSHA256     string                  `json:"proof_sha256"`
	NonceFile       applicationSecretFile   `json:"nonce_file"`
	Keys            []applicationMaterial   `json:"keys"`
}

// encoding/json replaces invalid escaped UTF-16 rather than rejecting it.
// Validate strings before decoding so neither malformed text nor duplicate
// decoded member names can disappear in a permissive intermediate value.
func applicationValidEscapes(b applicationBudget, raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if b.check() != nil {
			return false
		}
		if raw[i] != '"' {
			continue
		}
		i++
		for ; i < len(raw) && raw[i] != '"'; i++ {
			if b.check() != nil || raw[i] < 0x20 {
				return false
			}
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				if !strings.ContainsRune(`"\/bfnrt`, rune(raw[i])) {
					return false
				}
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 4
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if utf16.IsSurrogate(rune(n)) {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
		if i >= len(raw) {
			return false
		}
	}
	return b.check() == nil
}

func applicationReadJSONValue(b applicationBudget, d *json.Decoder, depth int) (any, bool) {
	if b.check() != nil || depth > 12 {
		return nil, false
	}
	value, err := d.Token()
	if err != nil || value == nil || b.check() != nil {
		return nil, false
	}
	switch token := value.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := map[string]any{}
			for d.More() {
				if b.check() != nil {
					return nil, false
				}
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || b.check() != nil {
					return nil, false
				}
				if _, exists := object[name]; exists {
					return nil, false
				}
				child, ok := applicationReadJSONValue(b, d, depth+1)
				if !ok {
					return nil, false
				}
				object[name] = child
			}
			end, err := d.Token()
			return object, err == nil && end == json.Delim('}') && b.check() == nil
		case '[':
			array := []any{}
			for d.More() {
				child, ok := applicationReadJSONValue(b, d, depth+1)
				if !ok {
					return nil, false
				}
				array = append(array, child)
			}
			end, err := d.Token()
			return array, err == nil && end == json.Delim(']') && b.check() == nil
		}
		return nil, false
	default:
		return token, true
	}
}

func applicationExactShape(b applicationBudget, value any, kind reflect.Type) bool {
	if b.check() != nil {
		return false
	}
	switch kind.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok || len(object) != kind.NumField() {
			return false
		}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			child, exists := object[name]
			if name == "" || name == "-" || !exists || !applicationExactShape(b, child, field.Type) {
				return false
			}
		}
	case reflect.Slice:
		array, ok := value.([]any)
		if !ok {
			return false
		}
		for _, child := range array {
			if !applicationExactShape(b, child, kind.Elem()) {
				return false
			}
		}
	case reflect.String:
		if _, ok := value.(string); !ok {
			return false
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return false
		}
	case reflect.Int, reflect.Uint16, reflect.Uint32:
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		text := string(number)
		if len(text) == 0 || (len(text) > 1 && text[0] == '0') {
			return false
		}
		for _, c := range text {
			if c < '0' || c > '9' || b.check() != nil {
				return false
			}
		}
		if _, err := strconv.ParseUint(text, 10, kind.Bits()); err != nil {
			return false
		}
	default:
		return false
	}
	return b.check() == nil
}

func applicationStrictDecode(b applicationBudget, raw []byte, out *applicationManifest) error {
	if b.check() != nil || out == nil || len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) || !applicationValidEscapes(b, raw) || b.check() != nil {
		return ErrRuntime
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, ok := applicationReadJSONValue(b, decoder, 0)
	if !ok || b.check() != nil {
		return ErrRuntime
	}
	if _, err := decoder.Token(); err != io.EOF || b.check() != nil {
		return ErrRuntime
	}
	if !applicationExactShape(b, value, reflect.TypeOf(*out)) || b.check() != nil {
		return ErrRuntime
	}
	if json.Unmarshal(raw, out) != nil || b.check() != nil {
		return ErrRuntime
	}
	return nil
}
