package paymentproto

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	requestLimit = 8 << 10
	bodyLimit    = 64 << 10
	timeLayout   = "2006-01-02T15:04:05.000000Z"
)

func uuidValid(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
func positive(s string) bool {
	if len(s) == 0 || len(s) > 19 || s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	_, e := strconv.ParseInt(s, 10, 64)
	return e == nil
}
func lowerHex(s string) bool {
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
func reference(s string) bool {
	if len(s) == 0 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
func label(s string) bool {
	if len(s) == 0 || len(s) > 100 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func version(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := range s {
		if !(s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z' || s[i] >= '0' && s[i] <= '9' || s[i] == '_' || s[i] == '-') {
			return false
		}
	}
	return true
}
func environmentValid(e Environment) bool {
	return uuidValid(e.ID) && reference(e.MerchantID) && label(e.PayeeLabel)
}
func commandKind(s string) bool { return s == "checkout" || s == "cancel" }
func failureCode(s string) bool {
	return s == "checkout_rejected" || s == "order_closed" || s == "order_expired"
}

// Timestamp syntax is deliberately narrower than time.Parse's RFC3339 parser:
// only Z, a dot, and at most six decimal places are allowed.
func instant(s string) (time.Time, bool) {
	if len(s) < 20 || len(s) > 27 || s[len(s)-1] != 'Z' {
		return time.Time{}, false
	}
	for i := 0; i < 19; i++ {
		switch i {
		case 4, 7:
			if s[i] != '-' {
				return time.Time{}, false
			}
		case 10:
			if s[i] != 'T' {
				return time.Time{}, false
			}
		case 13, 16:
			if s[i] != ':' {
				return time.Time{}, false
			}
		default:
			if s[i] < '0' || s[i] > '9' {
				return time.Time{}, false
			}
		}
	}
	if len(s) > 20 {
		if len(s) < 22 || s[19] != '.' {
			return time.Time{}, false
		}
		for i := 20; i < len(s)-1; i++ {
			if s[i] < '0' || s[i] > '9' {
				return time.Time{}, false
			}
		}
	}
	t, e := time.Parse(time.RFC3339Nano, s)
	return t, e == nil && t.Year() >= 1 && t.Year() <= 9999
}
func MerchantOrderID(environmentID, orderID string) (string, error) {
	if !uuidValid(environmentID) || !uuidValid(orderID) {
		return "", fail("invalid_request")
	}
	return "mps1." + environmentID + "." + orderID, nil
}
func ParseMerchantOrderID(s string) (string, string, error) {
	if len(s) != 78 || s[:5] != "mps1." || s[41] != '.' || !uuidValid(s[5:41]) || !uuidValid(s[42:]) {
		return "", "", fail("invalid_request")
	}
	return s[5:41], s[42:], nil
}
func identity(schema int, env, merchant, command, order string) bool {
	if schema != 1 || !uuidValid(env) || !reference(merchant) || !uuidValid(command) {
		return false
	}
	e, _, err := ParseMerchantOrderID(order)
	return err == nil && e == env
}
func RequestDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
