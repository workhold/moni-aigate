package provider

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func discoveryFixture(t *testing.T, handler http.HandlerFunc) (Endpoint, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	endpoint := fixtureEndpoint(t, srv.URL)
	endpoint.URL += "/v1"
	return endpoint, NewClient(ClientOptions{})
}

func discoveryErrorCode(t *testing.T, result DiscoveryResult, err error, code string) {
	t.Helper()
	var safe *Error
	if !errors.As(err, &safe) || safe.Code != code || result.Models != nil || result.Pages != 0 {
		t.Fatalf("wanted zero result and %s; got %+v / %v", code, result, err)
	}
	if strings.Contains(err.Error(), "secret-canary") || strings.Contains(err.Error(), "http") {
		t.Fatal("discovery error exposed provider data", err)
	}
}

func TestDiscoveryFormatsUseFixedGETAndOnlyTheirProviderCredential(t *testing.T) {
	for _, format := range []string{DiscoveryFormatOpenAI, DiscoveryFormatAnthropic, DiscoveryFormatGemini} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			ep, client := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Accept") != "application/json" {
					t.Error("unexpected discovery request", r.Method, r.URL.Path)
				}
				if r.Header.Get("Cookie") != "" {
					t.Error("cookie forwarded")
				}
				wantQuery := ""
				switch format {
				case DiscoveryFormatOpenAI:
					if r.Header.Get("Authorization") != "Bearer secret-canary" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-Goog-Api-Key") != "" {
						t.Error("wrong OpenAI credential")
					}
				case DiscoveryFormatAnthropic:
					wantQuery = "limit=100"
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "secret-canary" || r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
						t.Error("wrong Anthropic credential")
					}
				case DiscoveryFormatGemini:
					wantQuery = "pageSize=100"
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("X-Goog-Api-Key") != "secret-canary" {
						t.Error("wrong Gemini credential")
					}
				}
				if r.URL.RawQuery != wantQuery {
					t.Error("unexpected query", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				switch format {
				case DiscoveryFormatOpenAI:
					_, _ = io.WriteString(w, `{"data":[{"id":"z-model","debug":"secret-canary"},{"id":"a-model"},{"id":"z-model"}],"object":"list","debug":"secret-canary"}`)
				case DiscoveryFormatAnthropic:
					_, _ = io.WriteString(w, `{"data":[{"id":"z-model"},{"id":"a-model","display_name":"secret-canary"},{"id":"z-model"}],"has_more":false,"last_id":"z-model"}`)
				case DiscoveryFormatGemini:
					_, _ = io.WriteString(w, `{"models":[{"name":"models/z-model","supportedGenerationMethods":["generateContent"]},{"name":"models/a-model","description":"secret-canary"},{"name":"models/z-model"},{"name":"models/embedding","supportedGenerationMethods":["embedContent"]}]}`)
				}
			})
			result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: format})
			if err != nil || !reflect.DeepEqual(result.Models, []string{"a-model", "z-model"}) || result.Pages != 1 || requests.Load() != 1 {
				t.Fatalf("result=%+v err=%v requests=%d", result, err, requests.Load())
			}
		})
	}
}

func TestDiscoveryPaginationRemainsOnFixedOriginAndSortsCompleteSnapshot(t *testing.T) {
	for _, format := range []string{DiscoveryFormatAnthropic, DiscoveryFormatGemini} {
		t.Run(format, func(t *testing.T) {
			var requests atomic.Int32
			ep, client := discoveryFixture(t, func(w http.ResponseWriter, r *http.Request) {
				page := requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || len(r.URL.Query()) > 2 {
					t.Error("pagination changed resource", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if format == DiscoveryFormatAnthropic {
					if page == 1 {
						_, _ = io.WriteString(w, `{"data":[{"id":"z"}],"has_more":true,"last_id":"z"}`)
					} else {
						if r.URL.Query().Get("after_id") != "z" || r.URL.Query().Get("limit") != "100" {
							t.Error("bad Anthropic cursor", r.URL.RawQuery)
						}
						_, _ = io.WriteString(w, `{"data":[{"id":"a"},{"id":"z"}],"has_more":false}`)
					}
				} else if page == 1 {
					_, _ = io.WriteString(w, `{"models":[{"name":"models/z"}],"nextPageToken":"opaque +/=?&值"}`)
				} else {
					if r.URL.Query().Get("pageToken") != "opaque +/=?&值" || r.URL.Query().Get("pageSize") != "100" {
						t.Error("opaque cursor escaped incorrectly", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, `{"models":[{"name":"models/a"},{"name":"models/z"}]}`)
				}
			})
			result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: format})
			if err != nil || result.Pages != 2 || requests.Load() != 2 || !reflect.DeepEqual(result.Models, []string{"a", "z"}) {
				t.Fatalf("result=%+v err=%v requests=%d", result, err, requests.Load())
			}
		})
	}
}

func TestDiscoveryRejectsAmbiguousOrIncompleteListsAndUnsafeIDs(t *testing.T) {
	for _, tc := range []struct{ format, body string }{
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"data":[]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a","id":"b"}]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a","metadata":{"debug":1,"debug":2}}]}`},
		{DiscoveryFormatOpenAI, `{"data":null}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":1}]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"https://provider.example/path"}]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a..b"}]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a\nb"}]}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"has_more":true}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"hasMore":true}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"has_more":null}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"next_page":"https://secret-canary/"}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"next":"https://secret-canary/"}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"next_cursor":"opaque"}`},
		{DiscoveryFormatOpenAI, `{"data":[{"id":"a"}],"error":{"message":"secret-canary"}}`},
		{DiscoveryFormatOpenAI, `{"data":[]} {"data":[]}`},
		{DiscoveryFormatAnthropic, `{"data":[{"id":"a"}]}`},
		{DiscoveryFormatAnthropic, `{"data":[{"id":"a"}],"has_more":true}`},
		{DiscoveryFormatAnthropic, `{"data":[],"has_more":true,"last_id":"a"}`},
		{DiscoveryFormatAnthropic, `{"data":[{"id":"a"}],"has_more":true,"last_id":"https://secret-canary"}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a/b"}]}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a","supportedGenerationMethods":null}]}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a","supportedGenerationMethods":[1]}]}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a"}],"nextPageToken":null}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a"}],"nextPageToken":"bad\u0000cursor"}`},
		{DiscoveryFormatGemini, `{"models":[{"name":"models/a"}],"nextPageToken":"` + strings.Repeat("a", 513) + `"}`},
	} {
		t.Run(tc.format+fmt.Sprint(len(tc.body)), func(t *testing.T) {
			ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			})
			result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: tc.format})
			discoveryErrorCode(t, result, err, "discovery_invalid_response")
		})
	}
}

func TestDiscoveryIdentifierContract(t *testing.T) {
	for _, id := range []string{"gpt-4.1", "claude_3", "org/model", "version:1", strings.Repeat("a", 200)} {
		if !ValidDiscoveredModelID(id) {
			t.Fatal("safe model rejected", id)
		}
	}
	for _, id := range []string{"", ".hidden", "../a", "a..b", "a//b", "https://bad", "a?x", "a#x", "a%2Fb", "a b", "a\nb", "a\\b", "模型", strings.Repeat("a", 201)} {
		if ValidDiscoveredModelID(id) {
			t.Fatal("unsafe model accepted", id)
		}
	}
}

func TestDiscoveryLaterFailureDiscardsEarlierPage(t *testing.T) {
	var requests atomic.Int32
	ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`)
			return
		}
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-canary"}}`)
	})
	result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: DiscoveryFormatAnthropic})
	discoveryErrorCode(t, result, err, "discovery_upstream_rejected")
	if requests.Load() != 2 {
		t.Fatal("later failure retried", requests.Load())
	}
}

func TestDiscoveryBoundsPagesCursorEntriesAndDecompressedBytes(t *testing.T) {
	for _, scenario := range []string{"page", "gzip_page", "total", "entries", "pages", "cursor", "duplicate_entries"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				page := requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				body := map[string]any{"data": []map[string]string{{"id": "a"}}, "has_more": true, "last_id": fmt.Sprintf("cursor%d", page)}
				switch scenario {
				case "page", "gzip_page":
					body["padding"] = strings.Repeat("x", discoveryPageBytes)
				case "total":
					body["padding"] = strings.Repeat("x", discoveryPageBytes-200)
				case "entries":
					rows := make([]map[string]string, 1001)
					for i := range rows {
						rows[i] = map[string]string{"id": fmt.Sprintf("model%d", i)}
					}
					body["data"] = rows
				case "cursor":
					body["last_id"] = "cursor"
				case "duplicate_entries":
					rows := make([]map[string]string, 600)
					for i := range rows {
						rows[i] = map[string]string{"id": "a"}
					}
					body["data"] = rows
				}
				if scenario == "gzip_page" {
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					_ = json.NewEncoder(compressed).Encode(body)
					_ = compressed.Close()
				} else {
					_ = json.NewEncoder(w).Encode(body)
				}
			})
			result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: DiscoveryFormatAnthropic})
			discoveryErrorCode(t, result, err, "discovery_limit_exceeded")
			want := int32(1)
			switch scenario {
			case "total":
				want = 5
			case "pages":
				want = 10
			case "cursor", "duplicate_entries":
				want = 2
			}
			if requests.Load() != want {
				t.Fatal("unexpected bounded request count", requests.Load(), want)
			}
		})
	}
}

func TestDiscoveryRejectsStatusTypeAndRedirectWithoutFollowingOrRetry(t *testing.T) {
	var destinations atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		destinations.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer destination.Close()
	for _, scenario := range []string{"401", "429", "500", "307", "html", "empty_type"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				switch scenario {
				case "401":
					w.WriteHeader(401)
				case "429":
					w.WriteHeader(429)
				case "500":
					w.WriteHeader(500)
				case "307":
					w.Header().Set("Location", destination.URL)
					w.WriteHeader(307)
				case "html":
					w.Header().Set("Content-Type", "text/html")
				case "empty_type":
					w.Header()["Content-Type"] = nil
				}
				_, _ = io.WriteString(w, `{"data":[],"debug":"secret-canary"}`)
			})
			result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
			code := "discovery_upstream_rejected"
			if scenario == "html" || scenario == "empty_type" {
				code = "discovery_invalid_response"
			}
			discoveryErrorCode(t, result, err, code)
			if requests.Load() != 1 || destinations.Load() != 0 {
				t.Fatal("discovery followed or retried", requests.Load(), destinations.Load())
			}
		})
	}
}

func TestDiscoveryRejectsInvalidEndpointAndSSRFBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	for _, suffix := range []string{"?key=secret-canary", "#fragment", "/../v1", "/%2fhidden", "/a//b"} {
		bad := ep
		bad.URL += suffix
		result, err := client.DiscoverModels(context.Background(), bad, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
		discoveryErrorCode(t, result, err, "invalid_endpoint")
	}
	bad := ep
	bad.Network.AllowedCIDRs = nil
	result, err := client.DiscoverModels(context.Background(), bad, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
	discoveryErrorCode(t, result, err, "endpoint_ip_forbidden")
	for _, raw := range []string{"http://169.254.169.254/", "http://100.100.100.200/"} {
		bad := Endpoint{URL: raw, APIKey: "secret-canary", Network: NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"0.0.0.0/0"}}}
		result, err := client.DiscoverModels(context.Background(), bad, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
		discoveryErrorCode(t, result, err, "endpoint_ip_forbidden")
	}
	result, err = client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: "unknown"})
	discoveryErrorCode(t, result, err, "invalid_discovery_format")
	if requests.Load() != 0 {
		t.Fatal("invalid discovery reached upstream", requests.Load())
	}
}

func TestDiscoveryRevalidatesDNSOnEveryPageAndIgnoresEnvironmentProxy(t *testing.T) {
	var requests atomic.Int32
	ep, _ := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"a"}],"has_more":true,"last_id":"a"}`)
	})
	u, _ := url.Parse(ep.URL)
	ep.URL = "http://upstream.example:" + u.Port() + "/v1"
	resolver := &fixtureResolver{answers: [][]net.IPAddr{resolveTo("127.0.0.1"), resolveTo("169.254.169.254")}}
	client := NewClient(ClientOptions{Resolver: resolver})
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: DiscoveryFormatAnthropic})
	discoveryErrorCode(t, result, err, "endpoint_ip_forbidden")
	if requests.Load() != 1 || resolver.calls != 2 {
		t.Fatal("pagination DNS was not revalidated", requests.Load(), resolver.calls)
	}
}

func TestDiscoveryCancellationAndDeadlineCloseRealSocket(t *testing.T) {
	for _, scenario := range []string{"cancel", "deadline"} {
		t.Run(scenario, func(t *testing.T) {
			started, disconnected, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[`)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-r.Context().Done():
					close(disconnected)
				case <-release:
				}
			}))
			defer srv.Close()
			defer close(release)
			ep := fixtureEndpoint(t, srv.URL)
			var ctx context.Context
			var cancel context.CancelFunc
			if scenario == "deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				result, err := NewClient(ClientOptions{}).DiscoverModels(ctx, ep, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
				if result.Models != nil || result.Pages != 0 {
					done <- fmt.Errorf("partial cancellation result")
					return
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("discovery did not start")
			}
			if scenario == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				code := "discovery_cancelled"
				if scenario == "deadline" {
					code = "discovery_timeout"
				}
				discoveryErrorCode(t, DiscoveryResult{}, err, code)
			case <-time.After(2 * time.Second):
				t.Fatal("discovery did not stop")
			}
			select {
			case <-disconnected:
			case <-time.After(2 * time.Second):
				t.Fatal("discovery socket remained open")
			}
		})
	}
}

func TestDiscoveryEmptyListsAreCompleteAndNonNil(t *testing.T) {
	for _, format := range []string{DiscoveryFormatOpenAI, DiscoveryFormatAnthropic, DiscoveryFormatGemini} {
		ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			body := `{"data":[],"has_more":false}`
			if format == DiscoveryFormatGemini {
				body = `{"models":[]}`
			}
			_, _ = io.WriteString(w, body)
		})
		result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: format})
		if err != nil || result.Models == nil || len(result.Models) != 0 || result.Pages != 1 {
			t.Fatalf("empty %s result=%+v err=%v", format, result, err)
		}
	}
}

func TestDiscoveryAcceptsCompleteThousandEntrySnapshot(t *testing.T) {
	rows := make([]map[string]string, 1000)
	for i := range rows {
		rows[i] = map[string]string{"id": fmt.Sprintf("model%04d", 999-i)}
	}
	ep, client := discoveryFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	})
	result, err := client.DiscoverModels(context.Background(), ep, DiscoveryOptions{Format: DiscoveryFormatOpenAI})
	if err != nil || result.Pages != 1 || len(result.Models) != 1000 || result.Models[0] != "model0000" || result.Models[999] != "model0999" {
		t.Fatalf("boundary result=%+v err=%v", result, err)
	}
}

type discoveryCountingBody struct {
	reader io.Reader
	read   int
}

func (body *discoveryCountingBody) Read(p []byte) (int, error) {
	n, err := body.reader.Read(p)
	body.read += n
	return n, err
}

func (body *discoveryCountingBody) Close() error { return nil }

func TestDiscoveryPageReadRespectsRemainingTotalBudget(t *testing.T) {
	for _, remaining := range []int{0, 123, discoveryPageBytes, discoveryTotalBytes} {
		body := &discoveryCountingBody{reader: strings.NewReader(strings.Repeat("x", discoveryPageBytes+200))}
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: body}
		_, err := readDiscoveryPage(response, remaining)
		discoveryErrorCode(t, DiscoveryResult{}, err, "discovery_limit_exceeded")
		limit := remaining
		if limit > discoveryPageBytes {
			limit = discoveryPageBytes
		}
		want := limit + 1
		if remaining == 0 {
			want = 0
		}
		if body.read != want {
			t.Fatalf("remaining %d read %d bytes, want %d including overflow probe", remaining, body.read, want)
		}
	}
}
