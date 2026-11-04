package inference

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

// Envelope decoding shares the same strict grammar as metering. Vector and
// rate structure and arithmetic remain exclusively in the metering package.
func envelope(raw []byte, maximum int) (map[string]any, error) {
	value, err := strictjson.Parse(raw, maximum)
	if errors.Is(err, strictjson.ErrTooLarge) {
		return nil, ErrTooLarge
	}
	if err != nil {
		return nil, ErrInvalidJSON
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalidJSON
	}
	return object, nil
}

func fields(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, n := range names {
		if _, ok := m[n]; !ok {
			return false
		}
	}
	return true
}
func str(v any) string { s, _ := v.(string); return s }
func member(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func validUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func safeText(s string, max int, allowEmpty bool) bool {
	if len(s) > max || (!allowEmpty && s == "") || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func safeReference(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}
func decimal(v any) (int64, bool) {
	s, ok := v.(string)
	if !ok || s == "" || len(s) > 19 {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n >= 0 && strconv.FormatInt(n, 10) == s
}
func digestHex(d [32]byte) string { return hex.EncodeToString(d[:]) }
func validHex(value any) bool {
	s, ok := value.(string)
	if !ok || len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func safeIdentifier(value any) bool {
	s, ok := value.(string)
	if !ok || s == "" || len(s) > 80 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
func validVariant(value any) bool {
	s, ok := value.(string)
	if !ok || s == "" || len(s) > 100 {
		return false
	}
	for i, c := range s {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '_' || c == '.' || c == '-') {
			continue
		}
		return false
	}
	return true
}
func matchesDigest(v any, d [32]byte) bool {
	s, ok := v.(string)
	return ok && s == digestHex(d)
}
func frameDigest(domain string, frames ...[]byte) [32]byte {
	h := sha256.New()
	all := make([][]byte, 0, len(frames)+1)
	all = append(all, []byte(domain))
	all = append(all, frames...)
	for _, f := range all {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(f)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(f)
	}
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}
func canonicalCopy(valid bool, raw []byte) ([]byte, error) {
	if !valid {
		return nil, ErrInvalidValue
	}
	return append([]byte(nil), raw...), nil
}
