package security

import (
	"strings"
	"unicode"
)

// ValidReference accepts a controlled record identifier, never a download URL,
// query string, JSON document or recognizable API credential. Evidence content
// remains in its access-controlled source, outside financial audit metadata.
func ValidReference(value string, max int) bool {
	if len(value) == 0 || len(value) > max || strings.Contains(value, "://") {
		return false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}
