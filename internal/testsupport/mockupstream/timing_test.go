package mockupstream

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDirectiveTimingUsesConfiguredWaitsAndRecordsEmission(t *testing.T) {
	var mu sync.Mutex
	var waits []time.Duration
	now := time.Unix(1700000000, 0)
	service, err := New(Config{Candidates: map[string]Candidate{
		"a": {Secret: "a-secret", Models: []string{"qa-text"}, TTFBMS: 100, EventIntervalMS: 2, OutputTokens: 8},
	}}, Options{
		AllowDirectives: true,
		Now: func() time.Time {
			now = now.Add(time.Millisecond)
			return now
		},
		Wait: func(_ context.Context, duration time.Duration) error {
			mu.Lock()
			waits = append(waits, duration)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(service.Handler())
	defer srv.Close()
	body := `{"model":"qa-text","stream":true,"messages":[{"role":"user","content":"hello [[mock:nonce=timing-1]] [[mock:fault=slow_ttfb:25]]"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/c/a/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer a-secret")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status=%d err=%v", resp.StatusCode, err)
	}
	wantWaits := []time.Duration{25 * time.Millisecond, 2 * time.Millisecond, 2 * time.Millisecond, 2 * time.Millisecond, 2 * time.Millisecond}
	if !reflect.DeepEqual(waits, wantWaits) {
		t.Fatalf("waits=%v want=%v", waits, wantWaits)
	}
	receipts := service.Receipts(0)
	if len(receipts) != 1 {
		t.Fatalf("receipts=%v", receipts)
	}
	receipt := receipts[0]
	if receipt.FirstEventAt == nil || receipt.FirstContentAt == nil || receipt.FirstEventAfterUS == nil || receipt.FirstContentAfterUS == nil || receipt.CompletedAt == nil || !receipt.Completed {
		t.Fatalf("missing emission timing: %+v", receipt)
	}
	if got := *receipt.FirstEventAfterUS; got != 1000 {
		t.Fatalf("first event monotonic duration=%dus", got)
	}
	if got := *receipt.FirstContentAfterUS; got != 1000 {
		t.Fatalf("first content monotonic duration=%dus", got)
	}
}

type emissionObservingWriter struct {
	header   http.Header
	service  *Server
	t        *testing.T
	observed bool
}

func (w *emissionObservingWriter) Header() http.Header { return w.header }
func (w *emissionObservingWriter) WriteHeader(int)     {}
func (w *emissionObservingWriter) Flush()              {}
func (w *emissionObservingWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("data:")) {
		w.observed = true
		receipts := w.service.Receipts(0)
		if len(receipts) != 1 || receipts[0].FirstEventAt == nil {
			w.t.Error("observable event write preceded its emission timestamp")
		}
	}
	return len(p), nil
}

func TestEmissionIsMarkedBeforeObservableWrite(t *testing.T) {
	service, err := New(Config{Candidates: map[string]Candidate{
		"a": {Secret: "a-secret", Models: []string{"qa-text"}, OutputTokens: 2},
	}}, Options{AllowDirectives: true})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"qa-text","stream":true,"messages":[{"role":"user","content":"hello [[mock:nonce=boundary]]"}]}`
	request := httptest.NewRequest(http.MethodPost, "/c/a/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer a-secret")
	writer := &emissionObservingWriter{header: http.Header{}, service: service, t: t}
	service.Handler().ServeHTTP(writer, request)
	if !writer.observed {
		t.Fatal("no stream event was observed")
	}
}

func TestMockEventContentBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, event string
		value                 any
		want                  bool
	}{
		{"chat role", "chat", "", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}}, false},
		{"chat text", "chat", "", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "hello"}}}}, true},
		{"chat tool id", "chat", "", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"id": "call"}}}}}}, false},
		{"chat tool args", "chat", "", map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"arguments": "{}"}}}}}}}, true},
		{"responses created", "responses", "response.created", map[string]any{}, false},
		{"responses text", "responses", "response.output_text.delta", map[string]any{"delta": "hello"}, true},
		{"messages start", "messages", "message_start", map[string]any{}, false},
		{"messages text", "messages", "content_block_delta", map[string]any{"delta": map[string]any{"text": "hello"}}, true},
		{"gemini text", "gemini", "", map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "hello"}}}}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mockEventContent(tc.protocol, tc.event, tc.value); got != tc.want {
				t.Fatalf("content=%v want=%v", got, tc.want)
			}
		})
	}
}
