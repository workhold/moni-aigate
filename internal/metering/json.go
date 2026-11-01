package metering

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
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

func parseJSON(raw []byte, maximum int) (any, error) {
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

func exactObject(value any, fields ...string) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok || len(object) != len(fields) {
		return nil, false
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return nil, false
		}
	}
	return object, true
}

func stringValue(value any) (string, bool) { s, ok := value.(string); return s, ok }

func decimal(value any) (int64, bool) {
	s, ok := stringValue(value)
	if !ok || len(s) == 0 || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func validUUID(id string) bool {
	if len(id) != 36 || id == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if id[i] != '-' {
				return false
			}
		} else if !(id[i] >= '0' && id[i] <= '9' || id[i] >= 'a' && id[i] <= 'f') {
			return false
		}
	}
	return true
}

func sortedStrings(value any) ([]string, bool) {
	array, ok := value.([]any)
	if !ok || len(array) == 0 || len(array) > 8 {
		return nil, false
	}
	result := make([]string, len(array))
	for i, value := range array {
		s, ok := stringValue(value)
		if !ok || registryUnit(s) == "" || i > 0 && s <= result[i-1] {
			return nil, false
		}
		result[i] = s
	}
	return result, true
}

func canonical(value any) []byte {
	// Only fixed structs of validated ASCII strings reach this function.
	raw, _ := json.Marshal(value)
	return raw
}
