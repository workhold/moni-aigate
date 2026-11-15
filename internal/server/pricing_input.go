package server

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"
)

// encoding/json replaces unpaired UTF-16 escape sequences. New pricing
// commands reject them before decoding, so a fingerprint never silently binds
// a replacement string different from the supplied JSON string.
func pricingUnicode(raw []byte) bool {
	quoted := false
	hex := func(b byte) (uint16, bool) {
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
	unit := func(i int) (uint16, bool) {
		if i+4 > len(raw) {
			return 0, false
		}
		var v uint16
		for _, b := range raw[i : i+4] {
			n, ok := hex(b)
			if !ok {
				return 0, false
			}
			v = v*16 + n
		}
		return v, true
	}
	for i := 0; i < len(raw); i++ {
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
		v, ok := unit(i + 1)
		if !ok {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v < 0xd800 || v > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		next, ok := unit(i + 3)
		if !ok || next < 0xdc00 || next > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
func pricingBody(c *gin.Context, fields ...string) (map[string]json.RawMessage, bool) {
	raw, e := io.ReadAll(io.LimitReader(c.Request.Body, 8193))
	if e != nil || len(raw) > 8192 || !pricingUnicode(raw) {
		return nil, false
	}
	var obj map[string]json.RawMessage
	ok := decodeCanonicalJSON(bytes.NewReader(raw), &obj, 8192, fields...) && len(obj) == len(fields)
	return obj, ok
}

func pricingBodyOptional(c *gin.Context, required []string, optional ...string) (map[string]json.RawMessage, bool) {
	raw, e := io.ReadAll(io.LimitReader(c.Request.Body, 8193))
	if e != nil || len(raw) > 8192 || !pricingUnicode(raw) {
		return nil, false
	}
	fields := append(append([]string(nil), required...), optional...)
	obj, ok := strictPricingRawObject(raw, fields...)
	if !ok || len(obj) < len(required) || len(obj) > len(fields) {
		return nil, false
	}
	for _, field := range required {
		if value, ok := obj[field]; !ok || string(value) == "null" {
			return nil, false
		}
	}
	return obj, true
}

func strictPricingRawObject(raw []byte, fields ...string) (map[string]json.RawMessage, bool) {
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	obj := make(map[string]json.RawMessage, len(fields))
	for dec.More() {
		token, err = dec.Token()
		key, valid := token.(string)
		if err != nil || !valid || len(fields) > 0 && !allowed[key] || obj[key] != nil {
			return nil, false
		}
		var value json.RawMessage
		if err = dec.Decode(&value); err != nil {
			return nil, false
		}
		obj[key] = value
	}
	if token, err = dec.Token(); err != nil || token != json.Delim('}') || dec.Decode(new(any)) != io.EOF {
		return nil, false
	}
	return obj, true
}
