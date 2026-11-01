package paymentproto

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

// encoding/json replaces malformed escaped UTF-16 with U+FFFD. Reject it first
// so two different signed byte sequences cannot silently become one value.
func validUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
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
	return true
}
func scanValue(d *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	t, e := d.Token()
	if e != nil {
		return false
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return true
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			s, ok := k.(string)
			if e != nil || !ok || seen[s] {
				return false
			}
			seen[s] = true
			if !scanValue(d, depth+1) {
				return false
			}
		}
		t, e = d.Token()
		return e == nil && t == json.Delim('}')
	case '[':
		for d.More() {
			if !scanValue(d, depth+1) {
				return false
			}
		}
		t, e = d.Token()
		return e == nil && t == json.Delim(']')
	}
	return false
}
func strictObject(raw []byte, limit int, fields, nullable []string, dest any) (map[string]json.RawMessage, error) {
	bad := func() (map[string]json.RawMessage, error) { return nil, fail("invalid_request") }
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) || !validUnicodeEscapes(raw) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !scanValue(d, 0) {
		return bad()
	}
	if _, e := d.Token(); e != io.EOF {
		return bad()
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil || len(object) != len(fields) {
		return bad()
	}
	allowsNull := map[string]bool{}
	for _, s := range nullable {
		allowsNull[s] = true
	}
	for _, s := range fields {
		value, ok := object[s]
		if !ok || (!allowsNull[s] && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return bad()
		}
	}
	if value, ok := object["schema_version"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("1")) {
		return bad()
	}
	if dest != nil && json.Unmarshal(raw, dest) != nil {
		return bad()
	}
	return object, nil
}
func encode(value any, limit int) ([]byte, error) {
	b, e := json.Marshal(value)
	if e != nil || len(b) > limit {
		return nil, fail("invalid_request")
	}
	return b, nil
}
