package paymentrefundproto

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func unicodeHex(b byte) (uint16, bool) {
	switch {
	case b >= '0' && b <= '9':
		return uint16(b - '0'), true
	case b >= 'a' && b <= 'f':
		return uint16(b - 'a' + 10), true
	case b >= 'A' && b <= 'F':
		return uint16(b - 'A' + 10), true
	}
	return 0, false
}
func escapedRune(raw []byte, at int) (uint16, bool) {
	if at+4 > len(raw) {
		return 0, false
	}
	var n uint16
	for _, b := range raw[at : at+4] {
		v, ok := unicodeHex(b)
		if !ok {
			return 0, false
		}
		n = n*16 + v
	}
	return n, true
}

// Check byte/depth/array bounds and escaped UTF-16 before allocating decoded
// JSON values. encoding/json otherwise replaces invalid surrogates silently.
func boundedJSON(raw []byte, limit int) bool {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return false
	}
	depth := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '[':
			return false
		case '{':
			depth++
			if depth > 8 {
				return false
			}
		case '}':
			depth--
			if depth < 0 {
				return false
			}
		case '"':
			i++
			for ; i < len(raw) && raw[i] != '"'; i++ {
				if raw[i] < 0x20 {
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
					if !bytes.ContainsRune([]byte(`"\/bfnrt`), rune(raw[i])) {
						return false
					}
					continue
				}
				n, ok := escapedRune(raw, i+1)
				if !ok {
					return false
				}
				i += 4
				if n >= 0xdc00 && n <= 0xdfff {
					return false
				}
				if n >= 0xd800 && n <= 0xdbff {
					if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return false
					}
					low, ok := escapedRune(raw, i+3)
					if !ok || low < 0xdc00 || low > 0xdfff {
						return false
					}
					i += 6
				}
			}
			if i >= len(raw) {
				return false
			}
		}
	}
	return depth == 0
}
func scanValue(d *json.Decoder) bool {
	t, err := d.Token()
	if err != nil {
		return false
	}
	delim, object := t.(json.Delim)
	if !object {
		return true
	}
	if delim != '{' {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		k, err := d.Token()
		s, ok := k.(string)
		if err != nil || !ok || seen[s] {
			return false
		}
		seen[s] = true
		if !scanValue(d) {
			return false
		}
	}
	t, err = d.Token()
	return err == nil && t == json.Delim('}')
}
func strictObject(raw []byte, limit int, fields, nullable []string, dest any) (map[string]json.RawMessage, error) {
	if !boundedJSON(raw, limit) {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !scanValue(d) {
		return nil, ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil || len(object) != len(fields) {
		return nil, ErrInvalid
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists {
			return nil, ErrInvalid
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			allowed := false
			for _, n := range nullable {
				if field == n {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, ErrInvalid
			}
		}
	}
	if v, exists := object["schema_version"]; exists && !bytes.Equal(bytes.TrimSpace(v), []byte("2")) {
		return nil, ErrInvalid
	}
	if dest != nil && json.Unmarshal(raw, dest) != nil {
		return nil, ErrInvalid
	}
	return object, nil
}
func encode(v any, limit int) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(b) > limit {
		return nil, ErrInvalid
	}
	return b, nil
}
func null(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
