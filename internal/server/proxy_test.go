package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/config"
)

func proxyLogin(t *testing.T, engine http.Handler, peer string, realIPs []string, forwarded string) int {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader("{"))
	r.RemoteAddr = peer
	r.Header.Set("Origin", "http://console.test")
	r.Header.Set("Content-Type", "application/json")
	for _, value := range realIPs {
		r.Header.Add("X-Real-IP", value)
	}
	r.Header.Set("X-Forwarded-For", forwarded)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w.Code
}

func TestTrustedProxyLoginLimitsClientsIndependently(t *testing.T) {
	s := New(config.Config{Origin: "http://console.test", TrustedProxyCIDRs: []string{"127.0.0.1/32"}}, nil, nil, nil)
	s.Ready(true)
	engine := s.Engine()
	for i := 0; i < 5; i++ {
		if got := proxyLogin(t, engine, "127.0.0.1:1234", []string{"198.51.100.1"}, "203.0.113.9"); got != 400 {
			t.Fatalf("first client request %d status=%d", i, got)
		}
	}
	if got := proxyLogin(t, engine, "127.0.0.1:1234", []string{"198.51.100.1"}, "203.0.113.10"); got != 429 {
		t.Fatalf("XFF bypassed exhausted client bucket: %d", got)
	}
	if got := proxyLogin(t, engine, "127.0.0.1:5678", []string{"198.51.100.2"}, "198.51.100.1"); got != 400 {
		t.Fatalf("another client inherited proxy's login limit: %d", got)
	}
}

func TestForwardedIdentityCannotBypassUntrustedOrAmbiguousLoginLimits(t *testing.T) {
	for _, tc := range []struct {
		name, peer string
		trusted    []string
		headers    func(int) []string
	}{
		{"default_untrusted", "127.0.0.1:1234", nil, func(i int) []string { return []string{"198.51.100." + string(rune('1'+i))} }},
		{"outside_allowlist", "203.0.113.1:1234", []string{"127.0.0.1/32"}, func(i int) []string { return []string{"198.51.100." + string(rune('1'+i))} }},
		{"ambiguous_chain", "127.0.0.1:1234", []string{"127.0.0.1/32"}, func(int) []string { return []string{"198.51.100.1, 198.51.100.2"} }},
		{"duplicate_header", "127.0.0.1:1234", []string{"127.0.0.1/32"}, func(int) []string { return []string{"198.51.100.1", "198.51.100.2"} }},
		{"invalid_header", "127.0.0.1:1234", []string{"127.0.0.1/32"}, func(int) []string { return []string{"invalid-address"} }},
		{"xff_only", "127.0.0.1:1234", []string{"127.0.0.1/32"}, func(int) []string { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(config.Config{Origin: "http://console.test", TrustedProxyCIDRs: tc.trusted}, nil, nil, nil)
			s.Ready(true)
			engine := s.Engine()
			for i := 0; i < 6; i++ {
				want := 400
				if i == 5 {
					want = 429
				}
				if got := proxyLogin(t, engine, tc.peer, tc.headers(i), "198.51.100."+string(rune('1'+i))); got != want {
					t.Fatalf("request %d status=%d want=%d", i, got, want)
				}
			}
		})
	}
}

func TestMetricsAuthorizationUsesTransportPeer(t *testing.T) {
	s := New(config.Config{TrustedProxyCIDRs: []string{"203.0.113.0/24", "127.0.0.1/32"}}, nil, nil, nil)
	engine := s.Engine()
	for _, tc := range []struct {
		peer, realIP string
		want         int
	}{
		{"203.0.113.1:1234", "127.0.0.1", 403},
		{"198.51.100.1:1234", "::1", 403},
		{"127.0.0.1:1234", "198.51.100.1", 200},
		{"[::1]:1234", "198.51.100.1", 200},
	} {
		r := httptest.NewRequest("GET", "/metrics", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Real-IP", tc.realIP)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("metrics peer=%s status=%d want=%d", tc.peer, w.Code, tc.want)
		}
	}
}
