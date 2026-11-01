package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestVoucherConfigurationDefaultsAndIndependentStrictKeys(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	t.Setenv("VOUCHERS_ENABLED", "")
	t.Setenv("VOUCHER_HMAC_KEYS", "")
	t.Setenv("VOUCHER_HMAC_ACTIVE_KEY", "")
	c, err := Load()
	if err != nil || c.VouchersEnabled || c.VoucherKeys != nil {
		t.Fatalf("default voucher configuration: enabled=%v err=%v", c.VouchersEnabled, err)
	}
	t.Setenv("VOUCHERS_ENABLED", "true")
	if _, err = Load(); err == nil {
		t.Fatal("enabled vouchers accepted without dedicated keys")
	}
	t.Setenv("VOUCHER_HMAC_ACTIVE_KEY", "v1")
	key := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("v", 32)))
	t.Setenv("VOUCHER_HMAC_KEYS", `{"v1":"`+key+`"}`)
	c, err = Load()
	if err != nil || !c.VouchersEnabled || c.VoucherKeys == nil || c.VoucherKeys.ActiveVersion() != "v1" {
		t.Fatalf("valid independent voucher keys rejected: %v", err)
	}
	t.Setenv("VOUCHERS_ENABLED", "false")
	t.Setenv("VOUCHER_HMAC_KEYS", `{"v1":"SENSITIVE_SENTINEL"}`)
	if _, err = Load(); err == nil || strings.Contains(err.Error(), "SENSITIVE_SENTINEL") {
		t.Fatal("malformed explicit key accepted or exposed when off")
	}
}
