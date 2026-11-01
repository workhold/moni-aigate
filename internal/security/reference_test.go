package security

import (
	"strings"
	"testing"
)

func TestEvidenceReferencesCannotCarrySignedURLsOrCredentials(t *testing.T) {
	for _, v := range []string{"receipt:2026-001", "invoice/123.4", "核对记录:2026_001"} {
		if !ValidReference(v, 256) {
			t.Fatalf("reference rejected: %s", v)
		}
	}
	for _, v := range []string{"", "https://files.example/a?token=secret", "https://user:secret@example.com", "invoice?token=secret", "invoice\nAuthorization:Bearer", "{\"prompt\":\"secret\"}", "sk-provider-secret", "mg_customer-secret", strings.Repeat("a", 257)} {
		if ValidReference(v, 256) {
			t.Fatal("unsafe evidence reference accepted")
		}
	}
}
