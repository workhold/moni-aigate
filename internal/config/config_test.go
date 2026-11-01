package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestMFAEnrollmentPolicyConfiguration(t *testing.T) {
	// Synthetic inputs only; config tests must never depend on a local .env.
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	for _, tc := range []struct {
		name, value   string
		want, invalid bool
	}{
		{"default_optional", "", false, false},
		{"explicit_optional", "false", false, false},
		{"platform_enforced", "true", true, false},
		{"invalid_rejected", "enabled", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MFA_ENFORCE_PLATFORM", tc.value)
			c, err := Load()
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "MFA_ENFORCE_PLATFORM") {
					t.Fatalf("invalid policy accepted: %v", err)
				}
				return
			}
			if err != nil || c.MFAEnforcePlatform != tc.want {
				t.Fatalf("enforced=%v want=%v err=%v", c.MFAEnforcePlatform, tc.want, err)
			}
		})
	}
}
