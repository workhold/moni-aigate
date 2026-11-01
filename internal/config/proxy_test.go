package config

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
)

func TestTrustedProxyConfigurationLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://fixture/unused")
	t.Setenv("MASTER_KEYS_JSON", "")
	t.Setenv("MASTER_KEY_ID", "fixture")
	t.Setenv("MASTER_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	t.Setenv("AUTH_SECRET", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("p", 32))))
	t.Setenv("MFA_ENFORCE_PLATFORM", "false")
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128")
	c, err := Load()
	if err != nil || !reflect.DeepEqual(c.TrustedProxyCIDRs, []string{"127.0.0.1/32", "::1/128"}) {
		t.Fatalf("explicit proxy peers were not loaded: %v", err)
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "0.0.0.0/0")
	if _, err = Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
		t.Fatalf("unsafe deployment configuration accepted: %v", err)
	}
}

func TestTrustedProxyCIDRsAreExplicitAndBounded(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        []string
		invalid     bool
	}{
		{name: "default"},
		{name: "empty", input: "  "},
		{name: "exact_peers", input: "127.0.0.1/32, ::1/128", want: []string{"127.0.0.1/32", "::1/128"}},
		{name: "canonical_unique", input: "10.20.30.9/24,10.20.30.0/24", want: []string{"10.20.30.0/24"}},
		{name: "host_rejected", input: "proxy.example", invalid: true},
		{name: "address_requires_mask", input: "127.0.0.1", invalid: true},
		{name: "ipv4_universal", input: "0.0.0.0/0", invalid: true},
		{name: "ipv6_universal", input: "::/0", invalid: true},
		{name: "mapped_ipv4", input: "::ffff:127.0.0.1/128", invalid: true},
		{name: "empty_entry", input: "127.0.0.1/32,", invalid: true},
		{name: "wildcard", input: "*", invalid: true},
		{name: "too_many", input: strings.Repeat("127.0.0.1/32,", 32) + "::1/128", invalid: true},
		{name: "too_long", input: strings.Repeat("x", 4097), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trustedProxyCIDRs(tc.input)
			if (err != nil) != tc.invalid || !tc.invalid && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%v err=%v want=%v invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
}
