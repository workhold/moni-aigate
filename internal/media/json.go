package media

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

func parseEnvelope(raw []byte, maximum int) (map[string]any, error) {
	value, err := strictjson.Parse(raw, maximum)
	if err != nil {
		if err == strictjson.ErrTooLarge {
			return nil, ErrTooLarge
		}
		return nil, ErrInvalidJSON
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalidJSON
	}
	return m, nil
}

func exactObject(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	for name := range m {
		if _, ok := want[name]; !ok {
			return false
		}
	}
	for _, name := range names {
		if _, ok := m[name]; !ok {
			return false
		}
	}
	return true
}

func stringValue(value any) (string, bool) {
	s, ok := value.(string)
	return s, ok
}

func decimalString(value any) (string, uint64, bool) {
	s, ok := stringValue(value)
	if !ok || s == "" || (len(s) > 1 && s[0] == '0') {
		return "", 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return s, n, true
}

func validUUID(value string) bool {
	if len(value) != 36 || strings.ToLower(value) != value {
		return false
	}
	for i, r := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	if value[14] < '1' || value[14] > '8' || !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	return strings.Trim(value, "-0") != ""
}

func validHex64(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeReference(value string) bool {
	if value == "" || len(value) > 200 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}

func safePrompt(value string) bool {
	if len(value) == 0 || len(value) > MaxPromptBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func digestHex(d [32]byte) string { return hex.EncodeToString(d[:]) }

func frameDigest(frames ...[]byte) [32]byte {
	h := sha256.New()
	for _, frame := range frames {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(frame)))
		_, _ = h.Write(length[:])
		_, _ = h.Write(frame)
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func marshalCanonical(value any) ([]byte, bool) {
	raw, err := json.Marshal(value)
	return raw, err == nil
}

func copyBytes(valid bool, raw []byte) ([]byte, error) {
	if !valid {
		return nil, ErrInvalidValue
	}
	return append([]byte(nil), raw...), nil
}
