package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"strconv"
	"strings"

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
}

func Load() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), Addr: env("LISTEN_ADDR", "127.0.0.1:8080"), Origin: strings.TrimRight(env("PUBLIC_ORIGIN", "http://localhost:8080"), "/"), CookieSecure: env("COOKIE_SECURE", "true") != "false", RecoveryMode: os.Getenv("RECOVERY_MODE") == "true"}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL required")
	}
	var err error
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
