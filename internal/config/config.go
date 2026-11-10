package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type Config struct {
	DatabaseURL, Addr, Origin string
	CookieSecure              bool
	Secrets                   *security.Secrets
	RecoveryMode              bool
	MFAEnforcePlatform        bool
	TrustedProxyCIDRs         []string
	AuthRegistrationEnabled   bool
	AuthPasswordResetEnabled  bool
	Mail                      MailConfig
	VouchersEnabled           bool
	VoucherKeys               *security.VoucherKeyring
	OperationsAlertsEnabled   bool
	PaymentsMode              string
	PaymentsAppManifest       string
	PaymentsWorkerManifest    string
	ProviderTimeouts          provider.TimeoutConfig
	MediaProviderProfile      string
	MediaDeterministicMock    bool
}

const (
	MediaProviderProfileDevelopment = "development"
	MediaProviderProfileBusinessQA  = "business-qa"
	MediaProviderProfileInnerTest   = "inner-test"
)

// MockMediaProvidersEnabled is deliberately false for the default inner-test
// profile. The main binary only receives a validated Config from Load, so a
// mock adapter can be installed only by an explicit development/business-qa
// configuration.
func (c Config) MockMediaProvidersEnabled() bool {
	return c.MediaDeterministicMock &&
		(c.MediaProviderProfile == MediaProviderProfileDevelopment || c.MediaProviderProfile == MediaProviderProfileBusinessQA)
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Origin: strings.TrimRight(env("PUBLIC_ORIGIN", "http://localhost:8080"), "/"), CookieSecure: env("COOKIE_SECURE", "true") != "false", RecoveryMode: os.Getenv("RECOVERY_MODE") == "true"}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL required")
	}
	var err error
	c.MediaProviderProfile = env("MEDIA_PROVIDER_PROFILE", MediaProviderProfileInnerTest)
	if c.MediaProviderProfile != MediaProviderProfileDevelopment && c.MediaProviderProfile != MediaProviderProfileBusinessQA && c.MediaProviderProfile != MediaProviderProfileInnerTest {
		return c, errors.New("MEDIA_PROVIDER_PROFILE must be development, business-qa, or inner-test")
	}
	c.MediaDeterministicMock, err = strconv.ParseBool(env("MEDIA_DETERMINISTIC_MOCK", "false"))
	if err != nil {
		return c, errors.New("MEDIA_DETERMINISTIC_MOCK must be a boolean")
	}
	if c.MediaProviderProfile == MediaProviderProfileInnerTest && c.MediaDeterministicMock {
		return c, errors.New("MEDIA_DETERMINISTIC_MOCK is not allowed for inner-test")
	}
	c.ProviderTimeouts, err = loadProviderTimeouts()
	if err != nil {
		return c, err
	}
	if err = loadPayments(&c); err != nil {
		return c, err
	}
	c.OperationsAlertsEnabled, err = strconv.ParseBool(env("OPERATIONS_ALERTS_ENABLED", "false"))
	if err != nil {
		return c, errors.New("OPERATIONS_ALERTS_ENABLED must be a boolean")
	}
	c.MFAEnforcePlatform, err = strconv.ParseBool(env("MFA_ENFORCE_PLATFORM", "false"))
	if err != nil {
		return c, errors.New("MFA_ENFORCE_PLATFORM must be a boolean")
	}
	c.TrustedProxyCIDRs, err = trustedProxyCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return c, err
	}
	c.AuthRegistrationEnabled, err = strconv.ParseBool(env("AUTH_REGISTRATION_ENABLED", "false"))
	if err != nil {
		return c, errors.New("AUTH_REGISTRATION_ENABLED must be a boolean")
	}
	c.AuthPasswordResetEnabled, err = strconv.ParseBool(env("AUTH_PASSWORD_RESET_ENABLED", "true"))
	if err != nil {
		return c, errors.New("AUTH_PASSWORD_RESET_ENABLED must be a boolean")
	}
	c.Mail, err = loadMail(c.Origin)
	if err != nil {
		return c, err
	}
	c.VouchersEnabled, err = strconv.ParseBool(env("VOUCHERS_ENABLED", "false"))
	if err != nil {
		return c, errors.New("VOUCHERS_ENABLED must be a boolean")
	}
	voucherRaw, voucherActive := os.Getenv("VOUCHER_HMAC_KEYS"), os.Getenv("VOUCHER_HMAC_ACTIVE_KEY")
	if c.VouchersEnabled || voucherRaw != "" || voucherActive != "" {
		c.VoucherKeys, err = security.ParseVoucherKeyring(voucherRaw, voucherActive)
		if err != nil {
			return c, errors.New("invalid voucher HMAC configuration")
		}
	}
	keys := map[string]string{}
	if raw := os.Getenv("MASTER_KEYS_JSON"); raw != "" {
		if e := json.Unmarshal([]byte(raw), &keys); e != nil {
			return c, errors.New("invalid MASTER_KEYS_JSON")
		}
	} else {
		keys[env("MASTER_KEY_ID", "primary")] = os.Getenv("MASTER_KEY")
	}
	s := &security.Secrets{Keys: map[string][]byte{}, Active: env("MASTER_KEY_ID", "primary")}
	for id, key := range keys {
		b, e := base64.StdEncoding.DecodeString(key)
		if e != nil {
			return c, errors.New("MASTER_KEY must be base64")
		}
		s.Keys[id] = b
	}
	var e error
	s.Pepper, e = base64.StdEncoding.DecodeString(os.Getenv("AUTH_SECRET"))
	if e != nil {
		return c, errors.New("AUTH_SECRET must be base64")
	}
	if e = s.Validate(); e != nil {
		return c, e
	}
	c.Secrets = s
	return c, nil
}

func loadProviderTimeouts() (provider.TimeoutConfig, error) {
	t := provider.DefaultTimeoutConfig()
	values := []struct {
		name string
		to   *time.Duration
	}{
		{"PROVIDER_CONNECT_TIMEOUT", &t.ConnectTimeout},
		{"PROVIDER_HEADER_TIMEOUT", &t.HeaderTimeout},
		{"PROVIDER_FIRST_EVENT_TIMEOUT", &t.FirstEventTimeout},
		{"PROVIDER_IDLE_TIMEOUT", &t.IdleTimeout},
		{"PROVIDER_PROGRESS_TIMEOUT", &t.ProgressTimeout},
	}
	for _, value := range values {
		raw := strings.TrimSpace(os.Getenv(value.name))
		if raw == "" {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return t, fmt.Errorf("%s must be a duration", value.name)
		}
		*value.to = parsed
	}
	if err := t.Validate(); err != nil {
		return t, err
	}
	return t, nil
}

func trustedProxyCIDRs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	invalid := errors.New("TRUSTED_PROXY_CIDRS must contain at most 32 explicit CIDRs; universal networks are not allowed")
	if len(raw) > 4096 {
		return nil, invalid
	}
	parts := strings.Split(raw, ",")
	if len(parts) > 32 {
		return nil, invalid
	}
	prefixes := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, invalid
		}
		canonical := prefix.Masked().String()
		if !seen[canonical] {
			prefixes = append(prefixes, canonical)
			seen[canonical] = true
		}
	}
	return prefixes, nil
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
