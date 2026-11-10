package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
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

func TestProviderTimeoutConfigurationIsValidatedAndApplied(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	for _, name := range []string{"PROVIDER_CONNECT_TIMEOUT", "PROVIDER_HEADER_TIMEOUT", "PROVIDER_FIRST_EVENT_TIMEOUT", "PROVIDER_IDLE_TIMEOUT", "PROVIDER_PROGRESS_TIMEOUT"} {
		t.Setenv(name, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ProviderTimeouts != provider.DefaultTimeoutConfig() {
		t.Fatalf("unexpected provider defaults: %+v", c.ProviderTimeouts)
	}
	t.Setenv("PROVIDER_HEADER_TIMEOUT", "2s")
	c, err = Load()
	if err != nil || c.ProviderTimeouts.HeaderTimeout != 2*time.Second {
		t.Fatalf("valid provider timeout was not applied: %+v %v", c.ProviderTimeouts, err)
	}
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"PROVIDER_IDLE_TIMEOUT", "1ms"},
		{"PROVIDER_PROGRESS_TIMEOUT", "11m"},
		{"PROVIDER_FIRST_EVENT_TIMEOUT", "not-a-duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			if _, err := Load(); err == nil {
				t.Fatalf("unsafe %s accepted: %v", tc.name, err)
			}
		})
	}
}

func TestMediaProviderSafetyConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	t.Setenv("MEDIA_PROVIDER_PROFILE", "")
	t.Setenv("MEDIA_DETERMINISTIC_MOCK", "")
	c, err := Load()
	if err != nil || c.MediaProviderProfile != MediaProviderProfileInnerTest || c.MediaDeterministicMock || c.MockMediaProvidersEnabled() {
		t.Fatalf("default media profile is not closed: %+v err=%v", c, err)
	}

	for _, profile := range []string{MediaProviderProfileDevelopment, MediaProviderProfileBusinessQA} {
		t.Run(profile, func(t *testing.T) {
			t.Setenv("MEDIA_PROVIDER_PROFILE", profile)
			t.Setenv("MEDIA_DETERMINISTIC_MOCK", "false")
			c, err := Load()
			if err != nil || c.MockMediaProvidersEnabled() {
				t.Fatalf("%s enabled mock without explicit switch: %+v err=%v", profile, c, err)
			}
			t.Setenv("MEDIA_DETERMINISTIC_MOCK", "true")
			c, err = Load()
			if err != nil || !c.MockMediaProvidersEnabled() {
				t.Fatalf("%s did not enable explicit mock: %+v err=%v", profile, c, err)
			}
		})
	}

	t.Setenv("MEDIA_PROVIDER_PROFILE", MediaProviderProfileInnerTest)
	t.Setenv("MEDIA_DETERMINISTIC_MOCK", "true")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "MEDIA_DETERMINISTIC_MOCK") {
		t.Fatalf("inner-test accepted deterministic mock: %v", err)
	}
	t.Setenv("MEDIA_PROVIDER_PROFILE", "unsupported")
	t.Setenv("MEDIA_DETERMINISTIC_MOCK", "false")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "MEDIA_PROVIDER_PROFILE") {
		t.Fatalf("unsupported media profile accepted: %v", err)
	}
}
