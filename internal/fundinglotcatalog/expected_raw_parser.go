package fundinglotcatalog

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// strictExpectedRawJSONNodes is private to immutable expected-root decoding.
// It preserves strictJSONNodes language, limits and owned tree types; actual
// metadata continues through the original Token parser.
func strictExpectedRawJSONNodes(raw []byte, nodeLimit int) (any, error) {
	if len(raw) > collectorByteLimit || !utf8.Valid(raw) {
		return nil, ErrCatalog
	}
	p := expectedRawParser{raw: raw, limit: nodeLimit}
	v, e := p.value(0)
	if e != nil {
		return nil, ErrCatalog
	}
	p.space()
	if p.pos != len(raw) {
		return nil, ErrCatalog
	}
	return v, nil
}

type expectedRawParser struct {
	raw               []byte
	pos, nodes, limit int
}

func (p *expectedRawParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *expectedRawParser) value(depth int) (any, error) {
	p.nodes++
	if depth > 64 || p.nodes > p.limit {
		return nil, ErrCatalog
	}
	p.space()
	if p.pos >= len(p.raw) {
		return nil, ErrCatalog
	}
	switch p.raw[p.pos] {
	case '{':
		p.pos++
		p.space()
		m := object{}
		if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
			p.pos++
			return m, nil
		}
		for {
			key, e := p.text()
			if e != nil {
				return nil, ErrCatalog
			}
			if _, duplicate := m[key]; duplicate {
				return nil, ErrCatalog
			}
			p.space()
			if p.pos >= len(p.raw) || p.raw[p.pos] != ':' {
				return nil, ErrCatalog
			}
			p.pos++
			value, e := p.value(depth + 1)
			if e != nil {
				return nil, ErrCatalog
			}
			m[key] = value
			p.space()
			if p.pos >= len(p.raw) {
				return nil, ErrCatalog
			}
			switch p.raw[p.pos] {
			case '}':
				p.pos++
				return m, nil
			case ',':
				p.pos++
				p.space()
			default:
				return nil, ErrCatalog
			}
		}
	case '[':
		p.pos++
		p.space()
		a := []any{}
		if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
			p.pos++
			return a, nil
		}
		for {
			value, e := p.value(depth + 1)
			if e != nil {
				return nil, ErrCatalog
			}
			a = append(a, value)
			if len(a) > collectorRowLimit {
				return nil, ErrCatalog
			}
			p.space()
			if p.pos >= len(p.raw) {
				return nil, ErrCatalog
			}
			switch p.raw[p.pos] {
			case ']':
				p.pos++
				return a, nil
			case ',':
				p.pos++
			default:
				return nil, ErrCatalog
			}
		}
	case '"':
		s, e := p.text()
		if e != nil || len(s) > collectorRecordLimit || strings.ContainsRune(s, 0) {
			return nil, ErrCatalog
		}
		return s, nil
	case 't':
		if !p.literal("true") {
			return nil, ErrCatalog
		}
		return true, nil
	case 'f':
		if !p.literal("false") {
			return nil, ErrCatalog
		}
		return false, nil
	case 'n':
		if !p.literal("null") {
			return nil, ErrCatalog
		}
		return nil, nil
	default:
		start := p.pos
		for p.pos < len(p.raw) {
			switch p.raw[p.pos] {
			case ' ', '\t', '\r', '\n', ',', '}', ']':
				goto numberDone
			}
			p.pos++
			if p.pos-start > 64 {
				return nil, ErrCatalog
			}
		}
	numberDone:
		s := string(p.raw[start:p.pos])
		if !validCanonicalNumber(s) {
			return nil, ErrCatalog
		}
		return json.Number(s), nil
	}
}
func (p *expectedRawParser) literal(s string) bool {
	if len(p.raw)-p.pos < len(s) || string(p.raw[p.pos:p.pos+len(s)]) != s {
		return false
	}
	p.pos += len(s)
	return true
}
func (p *expectedRawParser) text() (string, error) {
	if p.pos >= len(p.raw) || p.raw[p.pos] != '"' {
		return "", ErrCatalog
	}
	start := p.pos
	p.pos++
	escaped := false
	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		switch {
		case c == '"':
			p.pos++
			if !escaped {
				return string(p.raw[start+1 : p.pos-1]), nil
			}
			var value string
			if json.Unmarshal(p.raw[start:p.pos], &value) != nil {
				return "", ErrCatalog
			}
			return value, nil
		case c == '\\':
			escaped = true
			p.pos++
			if p.pos >= len(p.raw) {
				return "", ErrCatalog
			}
			p.pos++
		case c < 0x20:
			return "", ErrCatalog
		default:
			p.pos++
		}
	}
	return "", ErrCatalog
}
