package procurementprefix

import (
	"encoding/json"
	"io"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

func (r *finishReplay) strictUnicode(raw string) bool {
	if !utf8.ValidString(raw) {
		return false
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if !r.tick() {
			return false
		}
		if raw[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || raw[i] != '\\' {
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
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1:i+3] != "\\u" {
				return false
			}
			low, err := strconv.ParseUint(raw[i+3:i+7], 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return !quoted
}
func (r *finishReplay) strictJSON(raw string) (any, bool) {
	if !r.strictUnicode(raw) {
		return nil, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	nodes := 0
	v, ok := r.jsonValue(d, 0, &nodes)
	if !ok {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}
func (r *finishReplay) jsonValue(d *json.Decoder, depth int, nodes *int) (any, bool) {
	if !r.tick() || depth > 5 || *nodes >= 128 {
		return nil, false
	}
	*nodes++
	t, err := d.Token()
	if err != nil {
		return nil, false
	}
	switch v := t.(type) {
	case json.Delim:
		if v != '{' {
			return nil, false
		}
		m := map[string]any{}
		for d.More() {
			if !r.tick() || len(m) >= 16 {
				return nil, false
			}
			k, err := d.Token()
			name, ok := k.(string)
			if err != nil || !ok || !validText(name) {
				return nil, false
			}
			if _, dup := m[name]; dup {
				return nil, false
			}
			value, ok := r.jsonValue(d, depth+1, nodes)
			if !ok {
				return nil, false
			}
			m[name] = value
		}
		if last, err := d.Token(); err != nil || last != json.Delim('}') {
			return nil, false
		}
		return m, true
	case string:
		return v, validText(v)
	case json.Number:
		if len(v) > 128 {
			return nil, false
		}
		if i := strings.IndexAny(string(v), "eE"); i >= 0 {
			e, err := strconv.Atoi(string(v)[i+1:])
			if err != nil || e > 100 || e < -100 {
				return nil, false
			}
		}
		_, ok := new(big.Rat).SetString(string(v))
		return v, ok
	case bool, nil:
		return v, true
	default:
		return nil, false
	}
}
func (r *finishReplay) jsonEqual(a, b any) bool {
	if !r.tick() {
		return false
	}
	switch v := a.(type) {
	case map[string]any:
		other, ok := b.(map[string]any)
		if !ok || len(v) != len(other) {
			return false
		}
		for k, x := range v {
			if !r.tick() {
				return false
			}
			y, ok := other[k]
			if !ok || !r.jsonEqual(x, y) {
				return false
			}
		}
		return true
	case json.Number:
		other, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, ok := new(big.Rat).SetString(string(v))
		y, yok := new(big.Rat).SetString(string(other))
		return ok && yok && x.Cmp(y) == 0
	default:
		return a == b
	}
}
