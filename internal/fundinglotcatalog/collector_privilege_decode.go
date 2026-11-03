package fundinglotcatalog

import (
	"bytes"
	"encoding/json"
)

// PostgreSQL jsonb emits these fixed effective-privilege records with kind
// first. This narrow parser avoids reflective map decoding for their repeated
// five-field shape. A miss always delegates to the unchanged generic parser;
// this is an optimization of representation, never an authorization decision.
func decodeEffectivePrivilegeJSON(raw []byte) (object, bool) {
	if !bytes.HasPrefix(raw, []byte(`{"kind":`)) {
		return nil, false
	}
	p := privilegeJSONCursor{raw: raw}
	if !p.take('{') {
		return nil, false
	}
	out := make(object, 5)
	var seen uint8
	for {
		key, ok := p.word()
		if !ok || !p.take(':') {
			return nil, false
		}
		var bit uint8
		var value any
		switch key {
		case "kind":
			bit = 1
			kind, valid := p.word()
			if !valid || kind != "table" && kind != "column" && kind != "function" && kind != "type" && kind != "schema" {
				return nil, false
			}
			value = kind
		case "role":
			bit = 2
			value, ok = p.reference()
		case "object":
			bit = 4
			value, ok = p.reference()
		case "privileges":
			bit = 8
			value, ok = p.booleans()
		case "grant_options":
			bit = 16
			value, ok = p.booleans()
		default:
			return nil, false
		}
		if !ok || seen&bit != 0 {
			return nil, false
		}
		seen |= bit
		out[key] = value
		if p.take('}') {
			break
		}
		if !p.take(',') {
			return nil, false
		}
	}
	p.space()
	if seen != 31 || p.at != len(raw) {
		return nil, false
	}
	return out, true
}

type privilegeJSONCursor struct {
	raw []byte
	at  int
}

func (p *privilegeJSONCursor) space() {
	for p.at < len(p.raw) {
		switch p.raw[p.at] {
		case ' ', '\t', '\r', '\n':
			p.at++
		default:
			return
		}
	}
}

func (p *privilegeJSONCursor) take(c byte) bool {
	p.space()
	if p.at >= len(p.raw) || p.raw[p.at] != c {
		return false
	}
	p.at++
	return true
}

// Escapes, non-ASCII and control bytes use the general decoder. Copying the
// result prevents the returned ordinary DTO from retaining driver row bytes.
func (p *privilegeJSONCursor) word() (string, bool) {
	if !p.take('"') {
		return "", false
	}
	start := p.at
	for p.at < len(p.raw) {
		c := p.raw[p.at]
		if c == '"' {
			value := string(p.raw[start:p.at])
			p.at++
			return value, true
		}
		if c == '\\' || c < 0x20 || c >= 0x7f {
			return "", false
		}
		p.at++
	}
	return "", false
}

func (p *privilegeJSONCursor) number() (json.Number, bool) {
	p.space()
	start := p.at
	var value uint64
	for p.at < len(p.raw) && p.raw[p.at] >= '0' && p.raw[p.at] <= '9' {
		value = value*10 + uint64(p.raw[p.at]-'0')
		p.at++
		if p.at-start > 10 || value > 1<<32-1 {
			return "", false
		}
	}
	if p.at == start || p.at-start > 1 && p.raw[start] == '0' {
		return "", false
	}
	return json.Number(string(p.raw[start:p.at])), true
}

func (p *privilegeJSONCursor) reference() (object, bool) {
	if !p.take('{') {
		return nil, false
	}
	key, ok := p.word()
	if !ok || key != "$ref" || !p.take(':') || !p.take('[') {
		return nil, false
	}
	values := make([]any, 3)
	for i := range values {
		if i != 0 && !p.take(',') {
			return nil, false
		}
		value, ok := p.number()
		if !ok {
			return nil, false
		}
		values[i] = value
	}
	if !p.take(']') || !p.take('}') {
		return nil, false
	}
	return object{"$ref": values}, true
}

func (p *privilegeJSONCursor) booleans() ([]any, bool) {
	if !p.take('[') {
		return nil, false
	}
	values := make([]any, 0, 7)
	if p.take(']') {
		return values, true
	}
	for {
		p.space()
		if len(values) == 7 {
			return nil, false
		}
		switch {
		case bytes.HasPrefix(p.raw[p.at:], []byte("true")):
			values = append(values, true)
			p.at += 4
		case bytes.HasPrefix(p.raw[p.at:], []byte("false")):
			values = append(values, false)
			p.at += 5
		default:
			return nil, false
		}
		if p.take(']') {
			return values, true
		}
		if !p.take(',') {
			return nil, false
		}
	}
}
