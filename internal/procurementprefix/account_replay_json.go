package procurementprefix

import (
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// encoding/json intentionally substitutes malformed UTF16. Immutable snapshots
// must instead retain the writer's exact Unicode input domain, and reject two
// keys that decode to the same spelling before constructing a Go map.
func (e *acReplay) jsonUnicode(raw string) bool {
	if !utf8.ValidString(raw) {
		return false
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if !e.tick() {
			return false
		}
		c := raw[i]
		if !quoted {
			if c == '"' {
				quoted = true
			}
			continue
		}
		if c == '"' {
			quoted = false
			continue
		}
		if c != '\\' {
			if c < 32 {
				return false
			}
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(raw[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1:i+3] != "\\u" {
				return false
			}
			low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		} else if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
	}
	return !quoted
}
func (e *acReplay) flatJSON(raw string) (map[string]any, bool) {
	if !e.jsonUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	out := map[string]any{}
	for d.More() {
		// All sealed snapshots and audit shapes have at most nine keys. Reject
		// unrelated larger objects before spending work decoding their members.
		if !e.tick() || len(out) >= 9 {
			return nil, false
		}
		key, err := d.Token()
		if err != nil {
			return nil, false
		}
		name, ok := key.(string)
		if !ok {
			return nil, false
		}
		if _, duplicate := out[name]; duplicate {
			return nil, false
		}
		value, err := d.Token()
		if err != nil {
			return nil, false
		}
		switch value.(type) {
		case string, json.Number, bool, nil:
		default:
			return nil, false
		}
		out[name] = value
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}
func (e *acReplay) jsonMatches(raw string, want map[string]any) bool {
	got, ok := e.flatJSON(raw)
	if !ok || len(got) != len(want) {
		return false
	}
	for key, value := range want {
		if !e.tick() {
			return false
		}
		actual, exists := got[key]
		if !exists {
			return false
		}
		if number, yes := value.(json.Number); yes {
			other, valid := actual.(json.Number)
			if !valid {
				return false
			}
			if len(other) > 128 {
				return false
			}
			if i := strings.IndexAny(string(other), "eE"); i >= 0 {
				exponent, err := strconv.Atoi(string(other)[i+1:])
				if err != nil || exponent > 100 || exponent < -100 {
					return false
				}
			}
			a, x := new(big.Rat).SetString(string(number))
			b, y := new(big.Rat).SetString(string(other))
			if !x || !y || a.Cmp(b) != 0 {
				return false
			}
		} else if value != actual {
			return false
		}
	}
	return true
}
