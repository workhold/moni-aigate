package paymentruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Independently reject duplicate keys and invalid UTF-16 rather than allowing
// encoding/json to merge names or replace malformed escaped values.
func validEscapes(raw []byte) bool {
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
				if !strings.ContainsRune(`"\/bfnrt`, rune(raw[i])) {
					return false
				}
				continue
			}
			if i+4 >= len(raw) {
				return false
			}
			n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if e != nil {
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
				low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if e != nil || low < 0xdc00 || low > 0xdfff {
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
func readValue(d *json.Decoder, depth int) (any, bool) {
	if depth > 12 {
		return nil, false
	}
	v, e := d.Token()
	if e != nil || v == nil {
		return nil, false
	}
	switch t := v.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for d.More() {
				key, e := d.Token()
				s, ok := key.(string)
				if e != nil || !ok {
					return nil, false
				}
				if _, exists := m[s]; exists {
					return nil, false
				}
				val, ok := readValue(d, depth+1)
				if !ok {
					return nil, false
				}
				m[s] = val
			}
			end, e := d.Token()
			return m, e == nil && end == json.Delim('}')
		case '[':
			a := []any{}
			for d.More() {
				v, ok := readValue(d, depth+1)
				if !ok {
					return nil, false
				}
				a = append(a, v)
			}
			end, e := d.Token()
			return a, e == nil && end == json.Delim(']')
		}
		return nil, false
	default:
		return t, true
	}
}
func structFields(t reflect.Type) map[string]reflect.Type {
	result := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			for k, v := range structFields(f.Type) {
				result[k] = v
			}
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			result[name] = f.Type
		}
	}
	return result
}
func exactShape(v any, t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		fields := structFields(t)
		if len(m) != len(fields) {
			return false
		}
		for k, kind := range fields {
			value, ok := m[k]
			if !ok || !exactShape(value, kind) {
				return false
			}
		}
		return true
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, v := range a {
			if !exactShape(v, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.String:
		_, ok := v.(string)
		return ok
	case reflect.Bool:
		_, ok := v.(bool)
		return ok
	case reflect.Int, reflect.Uint16, reflect.Uint32:
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		s := string(n)
		if len(s) == 0 || (len(s) > 1 && s[0] == '0') {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		_, e := strconv.ParseUint(s, 10, t.Bits())
		return e == nil
	}
	return false
}
func strictDecode(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !utf8.Valid(raw) || !validEscapes(raw) {
		return ErrConfiguration
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, ok := readValue(d, 0)
	if !ok {
		return ErrConfiguration
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrConfiguration
	}
	if !exactShape(v, reflect.TypeOf(out).Elem()) || json.Unmarshal(raw, out) != nil {
		return ErrConfiguration
	}
	return nil
}
