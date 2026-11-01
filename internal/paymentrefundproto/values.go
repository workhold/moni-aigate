package paymentrefundproto

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	requestLimit  = 8 << 10
	bodyLimit     = 64 << 10
	timeLayout    = "2006-01-02T15:04:05.000000Z"
	captureDomain = "moni-aigate/payment-refund-capture/v2"
	commandDomain = "moni-aigate/payment-refund-command/v2"
	queryDomain   = "moni-aigate/payment-refund-query/v2"
	factDomain    = "moni-aigate/payment-refund-fact/v2"
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
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}
func amountValid(amount, original string) bool {
	if !positive(amount) || !positive(original) {
		return false
	}
	a, _ := strconv.ParseInt(amount, 10, 64)
	b, _ := strconv.ParseInt(original, 10, 64)
	return a <= b
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

// This intentionally follows v1's Unicode reference semantics, including its
// absence of a first-character restriction. New refund references are separate.
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
func refundReference(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for i := range s {
		c := s[i]
		alphaNum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alphaNum && (i == 0 || !strings.ContainsRune("._:/-", rune(c))) {
			return false
		}
	}
	if strings.Contains(s, "://") {
		return false
	}
	lower := strings.ToLower(s) // Input is now known ASCII.
	return !strings.HasPrefix(lower, "sk-") && !strings.HasPrefix(lower, "mg_") && !strings.HasPrefix(lower, "bearer")
}
func version(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := range s {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func instant(s string) (time.Time, bool) {
	if len(s) != 27 {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, s)
	return t, err == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format(timeLayout) == s
}
func merchantOrderValid(order, env string) bool {
	return len(order) == 78 && order[:5] == "mps1." && order[41] == '.' && order[5:41] == env && uuidValid(order[42:])
}
func environmentValid(e Environment) bool { return uuidValid(e.ID) && reference(e.MerchantID) }
func writeFrame(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}
func digest(domain string, canonical []byte) string {
	h := sha256.New()
	writeFrame(h, []byte(domain))
	writeFrame(h, canonical)
	return hex.EncodeToString(h.Sum(nil))
}
