// Package strictjson provides bounded JSON decoding without duplicate members
// or lossy Unicode replacement. It does not validate domain schemas.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	ErrTooLarge    = errors.New("strictjson_too_large")
	ErrInvalidJSON = errors.New("strictjson_invalid_json")
)

// encoding/json deliberately replaces invalid surrogate escapes. Reject them
// before decoding so two different wire inputs cannot collapse to replacement.
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
			n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil || n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			i += 4
			if n >= 0xd800 && n <= 0xdbff {
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
	return true
}

func readValue(d *json.Decoder, depth int) (any, bool) {
	if depth > 8 {
		return nil, false
	}
	token, err := d.Token()
	if err != nil {
		return nil, false
	}
	switch token {
	case json.Delim('{'):
		if depth >= 8 {
			return nil, false
		}
		object := make(map[string]any)
		for d.More() {
			name, err := d.Token()
			key, ok := name.(string)
			if err != nil || !ok {
				return nil, false
			}
			if _, exists := object[key]; exists {
				return nil, false
			}
			value, ok := readValue(d, depth+1)
			if !ok {
				return nil, false
			}
			object[key] = value
		}
		end, err := d.Token()
		return object, err == nil && end == json.Delim('}')
	case json.Delim('['):
		if depth >= 8 {
			return nil, false
		}
		array := make([]any, 0)
		for d.More() {
			value, ok := readValue(d, depth+1)
			if !ok {
				return nil, false
			}
			array = append(array, value)
		}
		end, err := d.Token()
		return array, err == nil && end == json.Delim(']')
	default:
		_, delimiter := token.(json.Delim)
		return token, !delimiter
	}
}

// Parse rejects oversized, ambiguous or invalid JSON before returning any value.
// Its callers own exact object shapes, domain values and canonical encoding.
func Parse(raw []byte, maximum int) (any, error) {
	if len(raw) > maximum {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(raw) || !validEscapes(raw) || !json.Valid(raw) {
		return nil, ErrInvalidJSON
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, ok := readValue(d, 0)
	if !ok {
		return nil, ErrInvalidJSON
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, ErrInvalidJSON
	}
	return value, nil
}
