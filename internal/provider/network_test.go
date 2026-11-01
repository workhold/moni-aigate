package provider

import (
	"compress/gzip"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const completeJSON = `{"id":"c","object":"chat.completion","model":"provider-secret-name","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`

type fixtureResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
	calls   int
}

func (r *fixtureResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.calls
	r.calls++
	if i >= len(r.answers) {
		i = len(r.answers) - 1
	}
	return r.answers[i], nil
}
func resolveTo(ips ...string) []net.IPAddr {
	var out []net.IPAddr
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out
}

func TestHTTPSResolvesValidatedIPAndVerifiesOriginalHostname(t *testing.T) {
	var requests atomic.Int32
	var serverName atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		serverName.Store(r.TLS.ServerName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeJSON)
	}))
	defer srv.Close()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	resolver := &fixtureResolver{answers: [][]net.IPAddr{resolveTo("127.0.0.1")}}
	client := NewClient(ClientOptions{RootCAs: pool, Resolver: resolver})
	u, _ := url.Parse(srv.URL)
	ep := fixtureEndpoint(t, srv.URL)
	ep.URL = "https://example.com:" + u.Port() + "/v1/chat/completions"
	ctx, cancel := testContext(t)
	defer cancel()
	r, e := client.Do(ctx, ep, fixtureRequest(t, false))
	if e != nil {
		t.Fatal(e)
	}
	body, e := r.DecodeJSON()
	if e != nil || !strings.Contains(string(body), `"model":"public-model"`) || r.Usage.Status != UsageConfirmed || serverName.Load() != "example.com" {
		t.Fatalf("decode=%s err=%v SNI=%v", body, e, serverName.Load())
	}
	ep.URL = "https://wrong-host.example:" + u.Port() + "/v1/chat/completions"
	_, e = client.Do(ctx, ep, fixtureRequest(t, false))
	if e == nil || requests.Load() != 1 {
		t.Fatalf("hostname validation bypassed: err=%v requests=%d", e, requests.Load())
	}
}

func TestDNSRebindingAndMixedAddressAnswersCannotReachForbiddenIP(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeJSON)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	ep := fixtureEndpoint(t, srv.URL)
	ep.URL = "http://upstream.example:" + u.Port() + "/chat"
	resolver := &fixtureResolver{answers: [][]net.IPAddr{resolveTo("127.0.0.1"), resolveTo("169.254.169.254")}}
	client := NewClient(ClientOptions{Resolver: resolver})
	ctx, cancel := testContext(t)
	defer cancel()
	r, e := client.Do(ctx, ep, fixtureRequest(t, false))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.DecodeJSON(); e != nil {
		t.Fatal(e)
	}
	_, e = client.Do(ctx, ep, fixtureRequest(t, false))
	var p *Error
	if !errors.As(e, &p) || p.Code != "endpoint_ip_forbidden" || requests.Load() != 1 || resolver.calls != 2 {
		t.Fatalf("rebind err=%v calls=%d requests=%d", e, resolver.calls, requests.Load())
	}
	client = NewClient(ClientOptions{Resolver: &fixtureResolver{answers: [][]net.IPAddr{resolveTo("127.0.0.1", "169.254.169.254")}}})
	_, e = client.Do(ctx, ep, fixtureRequest(t, false))
	if e == nil || requests.Load() != 1 {
		t.Fatal("mixed DNS answers bypassed validation", e)
	}
}

func TestSSRFPolicyAtActualDial(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeJSON)
	}))
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	client := NewClient(ClientOptions{})
	ep := fixtureEndpoint(t, srv.URL)
	ep.Network.AllowedCIDRs = nil
	if _, e := client.Do(ctx, ep, fixtureRequest(t, false)); e == nil {
		t.Fatal("implicit loopback allowed")
	}
	for _, raw := range []string{"http://169.254.169.254/metadata", "http://100.100.100.200/metadata", "http://[fe80::1]/", "http://[::]/", "http://224.0.0.1/", "http://user:secret@127.0.0.1/", "http://127.0.0.1/?key=secret", "http://127.0.0.1/#secret"} {
		ep := Endpoint{URL: raw, Network: NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"0.0.0.0/0", "::/0"}}}
		if _, e := client.Do(ctx, ep, fixtureRequest(t, false)); e == nil {
			t.Fatalf("forbidden endpoint accepted: %s", raw)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("forbidden request reached server")
	}
	ep = fixtureEndpoint(t, srv.URL)
	r, e := client.Do(ctx, ep, fixtureRequest(t, false))
	if e != nil {
		t.Fatal(e)
	}
	r.Close()
	if requests.Load() != 1 {
		t.Fatal("explicit loopback CIDR could not dial")
	}
	// IPv4-mapped loopback must be evaluated as IPv4, not mistaken for public IPv6.
	u, _ := url.Parse(srv.URL)
	ep = fixtureEndpoint(t, srv.URL)
	ep.URL = "http://[::ffff:127.0.0.1]:" + u.Port() + "/"
	ep.Network.AllowedCIDRs = nil
	if _, e = client.Do(ctx, ep, fixtureRequest(t, false)); e == nil {
		t.Fatal("IPv4-mapped loopback bypassed policy")
	}
}

func TestRedirectAndProviderStatusDoNotCauseHiddenPostReplay(t *testing.T) {
	var destination, source atomic.Int32
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destination.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeJSON)
	}))
	defer dst.Close()
	for _, status := range []int{307, 308, 500, 429} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				source.Add(1)
				w.Header().Set("Location", dst.URL)
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"secret-canary"}}`)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			before := source.Load()
			p := fixtureProfile()
			p.NoConsumptionStatuses = []int{429}
			request, _ := ParseChat([]byte(`{"model":"public-model","messages":[{"role":"user","content":"x"}]}`), p)
			_, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), request)
			if e == nil || strings.Contains(e.Error(), "secret") || source.Load() != before+1 || destination.Load() != 0 {
				t.Fatalf("hidden replay/leak: err=%v source=%d target=%d", e, source.Load(), destination.Load())
			}
			if SafeToRetry(e) != (status == 429) {
				t.Fatal("incorrect safe retry", status, e)
			}
		})
	}
}

func TestNoStalePoolReplayAndNoEnvironmentProxy(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		if posts.Load() == 2 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, completeJSON)
	}))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client := NewClient(ClientOptions{})
	ctx, cancel := testContext(t)
	defer cancel()
	ep := fixtureEndpoint(t, srv.URL)
	r, e := client.Do(ctx, ep, fixtureRequest(t, false))
	if e != nil {
		t.Fatal(e)
	}
	r.DecodeJSON()
	_, e = client.Do(ctx, ep, fixtureRequest(t, false))
	if e == nil || SafeToRetry(e) || posts.Load() != 2 {
		t.Fatalf("hidden pool replay: %v posts=%d", e, posts.Load())
	}
}

func TestNonStreamUsageValidationAndDecompressedLimit(t *testing.T) {
	for name, body := range map[string]string{"good": completeJSON, "zero": strings.Replace(completeJSON, `"prompt_tokens":4,"completion_tokens":2,"total_tokens":6`, `"prompt_tokens":0,"completion_tokens":0,"total_tokens":0`, 1), "missing": strings.Replace(completeJSON, `,"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`, "", 1), "invalid": strings.Replace(completeJSON, `"total_tokens":6`, `"total_tokens":7`, 1)} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.DecodeJSON()
			if e != nil || r.ProtocolOutcome != "completed" {
				t.Fatal(e)
			}
			want := UsageConfirmed
			if name == "missing" {
				want = UsageMissing
			}
			if name == "invalid" {
				want = UsageConflicting
			}
			if r.Usage.Status != want {
				t.Fatal(r.Usage)
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		g := gzip.NewWriter(w)
		_, _ = io.WriteString(g, strings.Repeat("x", MaxResponseBytes+1))
		g.Close()
	}))
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.DecodeJSON()
	var p *Error
	if !errors.As(e, &p) || p.Code != "upstream_response_too_large" {
		t.Fatal(e)
	}
}

func TestRetryAfterFormats(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	if ParseRetryAfter("3", now) != 3*time.Second || ParseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now) != 5*time.Second || ParseRetryAfter("-1", now) != 0 || ParseRetryAfter("9223372036854775807", now) != 0 {
		t.Fatal("invalid Retry-After parsing")
	}
}
