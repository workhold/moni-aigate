package procurementprefix

import (
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func (w *requiredWork) jsonUnicode(raw string) bool {
	unit := func(at int) (uint16, bool) {
		if at+4 > len(raw) {
			return 0, false
		}
		var value uint16
		for i := at; i < at+4; i++ {
			value <<= 4
			switch c := raw[i]; {
			case c >= '0' && c <= '9':
				value |= uint16(c - '0')
			case c >= 'a' && c <= 'f':
				value |= uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				value |= uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return value, true
	}
	quoted := false
	for i := 0; i < len(raw); i++ {
		if w.tick() != nil {
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
			continue // Consume escaped quotes and slashes without reinterpreting them.
		}
		value, ok := unit(i + 1)
		if !ok {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, lowOK := unit(i + 3)
			if !lowOK || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

func (w *requiredWork) metadata(value string, max int, nonspace bool) bool {
	if len(value) == 0 || len(value) > max || !utf8.ValidString(value) || nonspace && strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if w.tick() != nil {
			return false
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// A snapshot is flat and closed. Token decoding detects duplicate decoded keys,
// including escaped aliases, rather than accepting map/struct last-key-wins.

func (w *requiredWork) stringObject(raw string, keys []string, nullable ...string) (map[string]*string, bool, bool) {
	if len(raw) > 16384 || !utf8.ValidString(raw) || !w.jsonUnicode(raw) {
		return nil, false, false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	token, err := d.Token()
	if err != nil {
		return nil, false, false
	}
	if token == nil {
		if _, err := d.Token(); err == io.EOF {
			return nil, true, true
		}
		return nil, false, false
	}
	if token != json.Delim('{') {
		return nil, false, false
	}
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = false
	}
	for _, key := range nullable {
		allowed[key] = true
	}
	values := make(map[string]*string, len(keys))
	for d.More() {
		if w.tick() != nil {
			return nil, false, false
		}
		keyToken, err := d.Token()
		key, keyOK := keyToken.(string)
		allowNull, allowedKey := allowed[key]
		if err != nil || !keyOK || !allowedKey {
			return nil, false, false
		}
		if _, duplicate := values[key]; duplicate {
			return nil, false, false
		}
		value, err := d.Token()
		if err != nil {
			return nil, false, false
		}
		if value == nil && allowNull {
			values[key] = nil
			continue
		}
		text, ok := value.(string)
		if !ok || !w.metadata(text, 1000, false) {
			return nil, false, false
		}
		values[key] = &text
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(values) != len(keys) {
		return nil, false, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false, false
	}
	return values, false, true
}

// encoding/json replaces malformed UTF-16 escapes with U+FFFD. PostgreSQL
// JSONB rejects those escapes, so the independent DTO parser must reject them
// too rather than silently making an invalid identity match a literal U+FFFD.

func requiredTime(value string) (time.Time, bool) {
	// time.Parse accepts a comma fraction, but Go's persisted JSON timestamp
	// grammar emits a period. Preserve the supported persisted grammar.
	if strings.ContainsRune(value, ',') {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return parsed, err == nil && requiredFiniteTime(parsed)
}

func requiredFiniteTime(value time.Time) bool {
	return value.Year() >= 1 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}

func requiredStringValue(values map[string]*string, key string) string {
	if values[key] == nil {
		return ""
	}
	return *values[key]
}

type requiredSnapshot struct {
	values      map[string]*string
	null, valid bool
}

func requiredText(s string) bool { return s != "" && validText(s) }
func requiredOneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func requiredUnsigned(s string) (*big.Int, bool) { return numeric(s, false) }
func requiredSigned(s string) (*big.Int, bool)   { return numeric(s, true) }
func requiredUnsignedInt64(s string) (*big.Int, bool) {
	n, ok := requiredUnsigned(s)
	return n, ok && n.IsInt64()
}
func requiredHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
func requiredUUID(s string) bool { v, err := uuid.Parse(s); return err == nil && v.String() == s }
func requiredSnapshotValue(s requiredSnapshot, key string) string {
	return requiredStringValue(s.values, key)
}
func (w *requiredWork) snapshotDecode(kind, raw string) requiredSnapshot {
	if kind != "account_create" && kind != "account_configure" {
		return requiredSnapshot{}
	}
	keys := []string{"id", "label", "provider", "state", "limit_amount", "held_amount", "spent_amount", "revision", "created_at"}
	values, null, valid := w.stringObject(raw, keys)
	s := requiredSnapshot{values, null, valid}
	if !valid || null {
		return s
	}
	v := func(k string) string { return requiredStringValue(values, k) }
	_, limitOK := requiredUnsignedInt64(v("limit_amount"))
	_, heldOK := requiredUnsigned(v("held_amount"))
	_, spentOK := requiredUnsigned(v("spent_amount"))
	rev, revisionOK := requiredUnsignedInt64(v("revision"))
	_, timeOK := requiredTime(v("created_at"))
	s.valid = limitOK && heldOK && spentOK && revisionOK && rev.Sign() > 0 && timeOK && requiredOneOf(v("state"), "active", "paused") && w.metadata(v("label"), 100, true) && w.metadata(v("provider"), 100, true)
	return s
}

// Sorting is structural work shared by the domains; it does not calculate any
// expected business state, input identity or proof.
func (w *requiredWork) sortRevisions(values []int64) error {
	if w.err == nil {
		w.err = replaySort(w.replay, values, func(a, b int64) bool { return a < b })
	}
	return w.err
}
