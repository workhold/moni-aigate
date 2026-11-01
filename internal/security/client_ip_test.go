package security

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeClientCIDRs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in, want []string
	}{
		{"empty", nil, []string{}},
		{"canonical", []string{" 192.0.2.19/24 ", "2001:DB8:1:2::5/48", "192.0.2.0/24", "198.51.100.7", "2001:db8::1", "::192.0.2.0"}, []string{"192.0.2.0/24", "198.51.100.7/32", "2001:db8:1::/48", "2001:db8::1/128", "::c000:200/128"}},
		{"minimum_prefix", []string{"128.0.0.0/1", "8000::/1"}, []string{"128.0.0.0/1", "8000::/1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeClientCIDRs(tc.in)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("canonical=%v want=%v err=%v", got, tc.want, err)
			}
		})
	}
	for _, bad := range []string{"", " ", "example.com", "https://192.0.2.1", "192.0.2.1:80", "[::1]:80", "fe80::1%en0", "fe80::1%en0/64", "::ffff:192.0.2.1", "::ffff:c000:200/120", "0.0.0.0/0", "::/0", "192.0.2.1/33", "2001:db8::/129", "192.0.2.1,198.51.100.1", "192.0.2.001"} {
		t.Run("invalid_"+bad, func(t *testing.T) {
			_, err := NormalizeClientCIDRs([]string{bad})
			if err == nil || err.Error() != "invalid client IP allowlist" {
				t.Fatalf("invalid input must return a generic error: %v", err)
			}
		})
	}
	for _, in := range [][]string{make([]string, 33), {strings.Repeat(" ", 4096) + "192.0.2.1"}} {
		if _, err := NormalizeClientCIDRs(in); err == nil {
			t.Fatal("unbounded original input accepted")
		}
	}
	maximum := make([]string, 32)
	for i := range maximum {
		maximum[i] = "192.0.2.1"
	}
	if got, err := NormalizeClientCIDRs(maximum); err != nil || !reflect.DeepEqual(got, []string{"192.0.2.1/32"}) {
		t.Fatalf("valid original count boundary rejected: %v %v", got, err)
	}
	if got, err := NormalizeClientCIDRs([]string{strings.Repeat(" ", 4096-len("192.0.2.1")) + "192.0.2.1"}); err != nil || !reflect.DeepEqual(got, []string{"192.0.2.1/32"}) {
		t.Fatalf("valid original byte boundary rejected: %v %v", got, err)
	}
}

func TestNormalizeClientIP(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"192.0.2.1", "192.0.2.1"}, {"2001:DB8::1", "2001:db8::1"}, {"::ffff:192.0.2.1", "192.0.2.1"},
		{"", ""}, {"fe80::1%en0", ""}, {" 192.0.2.1 ", ""}, {"192.0.2.1:80", ""}, {"192.0.2.1/32", ""}, {"example.com", ""},
	} {
		if got := NormalizeClientIP(tc.in); got != tc.want {
			t.Fatalf("NormalizeClientIP(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestClientIPAllowedFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		ip    string
		cidrs []string
		want  bool
	}{
		{"", nil, true}, {"invalid", []string{}, true},
		{"192.0.2.1", []string{"192.0.2.0/24"}, true}, {"::ffff:192.0.2.1", []string{"192.0.2.0/24"}, true},
		{"2001:DB8::1", []string{"2001:db8::/32"}, true}, {"::c000:200", []string{"::c000:200/128"}, true},
		{"198.51.100.1", []string{"192.0.2.0/24"}, false}, {"", []string{"192.0.2.0/24"}, false},
		{"fe80::1%en0", []string{"fe80::/64"}, false},
		{"192.0.2.1", []string{"192.0.2.0/24", "bad"}, false},
		{"192.0.2.1", []string{"192.0.2.0/24", "192.0.2.19/24"}, false},
		{"192.0.2.1", []string{"192.0.2.0/24", "2001:DB8::/32"}, false},
		{"192.0.2.1", []string{"192.0.2.0/24", "0.0.0.0/0"}, false},
		{"192.0.2.1", []string{"192.0.2.0/24", "::ffff:c000:200/120"}, false},
		{"192.0.2.1", []string{"192.0.2.1"}, false},
	} {
		if got := ClientIPAllowed(tc.ip, tc.cidrs); got != tc.want {
			t.Fatalf("ClientIPAllowed(%q,%v)=%v want %v", tc.ip, tc.cidrs, got, tc.want)
		}
	}
}
