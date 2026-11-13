package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureEndpoint(t *testing.T, rawURL string) Endpoint {
	t.Helper()
	u, e := url.Parse(rawURL)
	if e != nil {
		t.Fatal(e)
	}
	n, _ := strconv.Atoi(u.Port())
	return Endpoint{URL: rawURL, Model: "provider-model", APIKey: "secret-canary", Network: NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8", "::1/128"}, AllowedPorts: []uint16{uint16(n)}}}
}
func testContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 3*time.Second)
}

const contentJSON = `{"id":"c","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"},"finish_reason":null}]}`
const finishJSON = `{"id":"c","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
const usageJSON = `{"id":"c","choices":[],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`
const deepSeekUsage = `{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"prompt_tokens_details":{"cached_tokens":3},"prompt_cache_hit_tokens":3,"prompt_cache_miss_tokens":1}`

func sse(data, ending string) string { return "data: " + data + ending + ending }
func serveStream(t *testing.T, wire string, fragment bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if fragment {
			for _, b := range []byte(wire) {
				_, _ = w.Write([]byte{b})
				w.(http.Flusher).Flush()
			}
		} else {
			_, _ = io.WriteString(w, wire)
		}
	}))
}

func TestSSEEventFramingAndFinalUsage(t *testing.T) {
	for name, ending := range map[string]string{"LF": "\n", "CRLF": "\r\n", "CR": "\r"} {
		t.Run(name, func(t *testing.T) {
			// Multiple data lines form one JSON value, and UTF-8 is split over writes.
			wire := "data: {\"id\":\"c\"," + ending + "data: \"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"},\"finish_reason\":null}]}" + ending + ending + sse(finishJSON, ending) + sse(usageJSON, ending) + sse("[DONE]", ending)
			srv := serveStream(t, wire, true)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			content := false
			usages := 0
			for {
				event, e := r.ReadEvent()
				if errors.Is(e, io.EOF) {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				if event.Content {
					content = true
				}
				if event.Kind == "usage" {
					usages++
				}
				if strings.Contains(string(event.Data), `"usage"`) {
					t.Fatal("interim usage leaked into downstream chunk")
				}
			}
			if !content || usages != 1 || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConfirmed || r.Usage.TotalTokens != 6 {
				t.Fatalf("content=%v usageEvents=%d response=%+v", content, usages, r)
			}
		})
	}
}

func TestOpenAICompatibleDeepSeekUsageAcrossHTTPModes(t *testing.T) {
	t.Run("non_stream", func(t *testing.T) {
		body := `{"id":"c","object":"chat.completion","model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],"usage":` + deepSeekUsage + `}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		defer srv.Close()
		ctx, cancel := testContext(t)
		defer cancel()
		r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		clean, err := r.DecodeJSON()
		if err != nil {
			t.Fatal(err)
		}
		if r.Usage.Status != UsageConfirmed || r.Usage.InputTokens != 4 || r.Usage.OutputTokens != 2 || r.Usage.TotalTokens != 6 || r.Usage.CachedTokens != 3 {
			t.Fatalf("non-stream usage %+v", r.Usage)
		}
		if strings.Contains(string(clean), "prompt_cache_") || !strings.Contains(string(clean), `"cached_tokens":3`) {
			t.Fatalf("non-stream usage was not normalized: %s", clean)
		}
	})

	t.Run("stream", func(t *testing.T) {
		wire := sse(contentJSON, "\n") + sse(finishJSON, "\n") + sse(`{"id":"c","choices":[],"usage":`+deepSeekUsage+`}`, "\n") + sse("[DONE]", "\n")
		srv := serveStream(t, wire, true)
		defer srv.Close()
		ctx, cancel := testContext(t)
		defer cancel()
		r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		usageEvents := 0
		for {
			event, readErr := r.ReadEvent()
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				t.Fatal(readErr)
			}
			if event.Kind == "usage" {
				usageEvents++
			}
		}
		if usageEvents != 1 || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConfirmed || r.Usage.InputTokens != 4 || r.Usage.OutputTokens != 2 || r.Usage.CachedTokens != 3 {
			t.Fatalf("stream usage events=%d response=%+v", usageEvents, r)
		}
	})

	t.Run("unknown_nonzero_dimension_stays_unconfirmed", func(t *testing.T) {
		body := strings.Replace(deepSeekUsage, `"prompt_cache_miss_tokens":1`, `"prompt_cache_miss_tokens":1,"prompt_cache_write_tokens":1`, 1)
		body = `{"id":"c","object":"chat.completion","model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"你好"},"finish_reason":"stop"}],"usage":` + body + `}`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		defer srv.Close()
		ctx, cancel := testContext(t)
		defer cancel()
		r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if _, err := r.DecodeJSON(); err != nil {
			t.Fatal(err)
		}
		if r.Usage.Status != UsageConflicting {
			t.Fatalf("unknown charge dimension was accepted: %+v", r.Usage)
		}
		if _, err := UsageJSON(r.Usage); err == nil {
			t.Fatal("conflicting usage crossed the settlement boundary")
		}
	})
}

func TestStreamOutcomesDoNotInventUsageOrReplayAfterError(t *testing.T) {
	for name, wire := range map[string]string{
		"missing":             sse(contentJSON, "\n") + sse(finishJSON, "\n") + sse("[DONE]", "\n"),
		"zero":                sse(finishJSON, "\n") + sse(`{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, "\n") + sse("[DONE]", "\n"),
		"conflict":            sse(usageJSON, "\n") + sse(`{"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`, "\n") + sse(finishJSON, "\n") + sse("[DONE]", "\n"),
		"bare_eof":            sse(contentJSON, "\n"),
		"truncated":           sse(contentJSON, "\n") + "data: {\"choices\":",
		"error_then_done":     sse(contentJSON, "\n") + sse(`{"error":{"message":"secret-canary","debug_url":"https://secret"}}`, "\n") + sse("[DONE]", "\n"),
		"done_without_finish": sse("[DONE]", "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			srv := serveStream(t, wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			var end error
			for {
				_, end = r.ReadEvent()
				if end != nil {
					break
				}
			}
			if name == "missing" || name == "zero" || name == "conflict" {
				if end != io.EOF || r.ProtocolOutcome != "completed" {
					t.Fatal(r.ProtocolOutcome, end)
				}
				want := UsageMissing
				if name == "zero" {
					want = UsageConfirmed
				}
				if name == "conflict" {
					want = UsageConflicting
				}
				if r.Usage.Status != want {
					t.Fatal(r.Usage)
				}
			} else {
				if end == io.EOF || r.ProtocolOutcome != "failed" || SafeToRetry(end) {
					t.Fatalf("wrong terminal: %v %+v", end, r)
				}
				if strings.Contains(end.Error(), "secret") {
					t.Fatal("upstream error leaked")
				}
				if _, again := r.ReadEvent(); again != io.EOF {
					t.Fatal("failed stream resumed")
				}
			}
		})
	}
}

func TestCompleteAndIncompleteSSEEventByteLimit(t *testing.T) {
	for name, wire := range map[string]string{"complete": "data: " + strings.Repeat("x", MaxEventBytes) + "\n\n", "incomplete": "data: " + strings.Repeat("x", MaxEventBytes+1)} {
		t.Run(name, func(t *testing.T) {
			srv := serveStream(t, wire, false)
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
			if e != nil {
				t.Fatal(e)
			}
			_, e = r.ReadEvent()
			var p *Error
			if !errors.As(e, &p) || p.Code != "upstream_event_too_large" {
				t.Fatalf("got %v", e)
			}
			r.Close()
		})
	}
}

func TestToolOnlyOutputCountsAsContent(t *testing.T) {
	tool := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call1","type":"function","function":{"name":"weather","arguments":"{"}}]},"finish_reason":null}]}`
	srv := serveStream(t, sse(tool, "\n")+sse(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, "\n")+sse(usageJSON, "\n")+sse("[DONE]", "\n"), false)
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	first, e := r.ReadEvent()
	if e != nil || !first.Content {
		t.Fatal(first, e)
	}
	for {
		_, e = r.ReadEvent()
		if e != nil {
			break
		}
	}
	if e != io.EOF || r.FinishReason != "tool_calls" || r.Usage.OutputTokens != 2 {
		t.Fatal(r.FinishReason, r.Usage, e)
	}
}

func TestCancelClosesRealUpstreamBody(t *testing.T) {
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer srv.Close()
	ctx, cancel := testContext(t)
	r, e := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	_, e = r.ReadEvent()
	var p *Error
	if !errors.As(e, &p) || p.Class != ClassCancelled || SafeToRetry(e) {
		t.Fatal(e)
	}
	r.Close()
	r.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("upstream socket still open after cancellation")
	}
}

func TestRoleHeartbeatCannotExtendProgressOrTotalDeadline(t *testing.T) {
	for _, total := range []bool{false, true} {
		t.Run(strconv.FormatBool(total), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						_, _ = io.WriteString(w, ": heartbeat\n\n"+sse(`{"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, "\n"))
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer srv.Close()
			o := ClientOptions{ProgressTimeout: 80 * time.Millisecond, FirstEventTimeout: time.Second}
			budget := 2 * time.Second
			if total {
				o.ProgressTimeout = time.Second
				budget = 80 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			r, e := NewClient(o).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			for {
				_, e = r.ReadEvent()
				if e != nil {
					break
				}
			}
			var p *Error
			if !errors.As(e, &p) || p.Class != ClassTimeout {
				t.Fatal(e)
			}
			if !total && p.Code != "upstream_progress_timeout" {
				t.Fatal(e)
			}
		})
	}
}

func TestHeaderAndFirstEventTimeout(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(strconv.FormatBool(headers), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if headers {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, e := NewClient(ClientOptions{HeaderTimeout: 60 * time.Millisecond, FirstEventTimeout: 60 * time.Millisecond, IdleTimeout: time.Second, ProgressTimeout: time.Second}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
			if e == nil {
				defer r.Close()
				_, e = r.ReadEvent()
			}
			var pe *Error
			if e == nil || !errors.As(e, &pe) || pe.Class != ClassTimeout || SafeToRetry(e) {
				t.Fatal("timeout after HTTP write was retryable", e)
			}
			if headers {
				if pe.Code != "upstream_first_event_timeout" {
					t.Fatalf("first event timeout was misclassified: %v", pe)
				}
			} else if pe.Code != "upstream_header_timeout" || pe.Consumption != ConsumptionUnknown {
				t.Fatalf("header timeout was misclassified: %+v", pe)
			}
		})
	}
}

func TestIdleTimeoutIsSeparateFromFirstEventAndProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, sse(`{"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`, "\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	r, err := NewClient(ClientOptions{
		HeaderTimeout:     time.Second,
		FirstEventTimeout: time.Second,
		IdleTimeout:       60 * time.Millisecond,
		ProgressTimeout:   time.Second,
	}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if event, err := r.ReadEvent(); err != nil || event == nil {
		t.Fatalf("first event: %+v %v", event, err)
	}
	_, err = r.ReadEvent()
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "upstream_idle_timeout" || pe.Class != ClassTimeout || SafeToRetry(err) {
		t.Fatalf("idle timeout was misclassified: %v", err)
	}
}

func TestRefusedConnectionIsSafeAndWrittenDisconnectIsNot(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := ln.Addr().String()
	ln.Close()
	ctx, cancel := testContext(t)
	defer cancel()
	_, e = NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, "http://"+address), fixtureRequest(t, false))
	var pe *Error
	if !errors.As(e, &pe) || pe.Class != ClassNetwork || !SafeToRetry(e) {
		t.Fatalf("refused connection=%v", e)
	}
	var accepted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer srv.Close()
	_, e = NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
	if e == nil || SafeToRetry(e) || accepted.Load() != 1 {
		t.Fatalf("written disconnect err=%v requests=%d", e, accepted.Load())
	}
}

func TestReasoningContentKeepsStreamAliveUntilTotalDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = io.WriteString(w, sse(`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":null}]}`, "\n"))
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer srv.Close()
	// The progress timer (80ms) would fire long before the 500ms total budget if
	// reasoning deltas were not progress; they are bounded only by the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r, e := NewClient(ClientOptions{ProgressTimeout: 80 * time.Millisecond, FirstEventTimeout: time.Second}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, true))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	started := time.Now()
	for {
		event, err := r.ReadEvent()
		if err != nil {
			var p *Error
			if !errors.As(err, &p) || p.Class != ClassTimeout || p.Code == "upstream_progress_timeout" || time.Since(started) < 300*time.Millisecond {
				t.Fatalf("reasoning stream ended early or with wrong cause after %s: %v", time.Since(started), err)
			}
			return
		}
		if event != nil && strings.Contains(string(event.Data), "thinking") {
			t.Fatalf("reasoning_content forwarded to client: %s", event.Data)
		}
	}
}
