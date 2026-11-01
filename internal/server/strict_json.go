package server

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// decodeCanonicalJSON rejects duplicate and noncanonical field names at every
// depth. encoding/json alone accepts case-insensitive aliases of struct tags.
// Decoder errors and input bodies must never be included in public responses.
func decodeCanonicalJSON(r io.Reader, out any, maxBytes int64, fields ...string) bool {
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil || int64(len(raw)) > maxBytes || !utf8.Valid(raw) {
		return false
	}
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var value func(int) bool
	value = func(depth int) bool {
		if depth > 8 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				if err != nil || !ok || !allowed[key] || seen[key] {
					return false
				}
				seen[key] = true
				if !value(depth + 1) {
					return false
				}
			}
			token, err = d.Token()
			return err == nil && token == json.Delim('}')
		case json.Delim('['):
			for d.More() {
				if !value(depth + 1) {
					return false
				}
			}
			token, err = d.Token()
			return err == nil && token == json.Delim(']')
		default:
			_, delim := token.(json.Delim)
			return !delim
		}
	}
	if !value(0) {
		return false
	}
	if _, err = d.Token(); err != io.EOF {
		return false
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out) == nil && d.Decode(new(any)) == io.EOF
}
