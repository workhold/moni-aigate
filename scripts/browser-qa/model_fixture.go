package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/moni-ai/moni-aigate/internal/security"
)

// This fixture is only installed by the private modelLifecycle test seam.
// Normal CLI/browser mock response timing and behavior stay unchanged.
type lifecycleModelFixture struct {
	ctx         context.Context
	cancel      context.CancelFunc
	mock        *mockProvider
	mu          sync.Mutex
	closed      bool
	active      sync.WaitGroup
	point       modelFixturePoint
	armed       bool
	hit         chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
	hitOnce     sync.Once
}

type modelFixturePoint int

const (
	modelBeforeResponse modelFixturePoint = iota + 1
	modelBeforeFinal
)

type privateModelLifecycle struct {
	*privateLifecycle
	fixture  *lifecycleModelFixture
	secrets  *security.Secrets
	provider providerManifest
}

func newLifecycleModelFixture(ctx context.Context, mock *mockProvider) *lifecycleModelFixture {
	bounded, cancel := context.WithTimeout(ctx, 320*time.Second)
	return &lifecycleModelFixture{ctx: bounded, cancel: cancel, mock: mock, hit: make(chan struct{}), release: make(chan struct{})}
}

func (f *lifecycleModelFixture) arm(point modelFixturePoint) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.armed || (point != modelBeforeResponse && point != modelBeforeFinal) {
		return stage("model_fixture_invalid_arm")
	}
	f.point, f.armed = point, true
	return nil
}

func (f *lifecycleModelFixture) resume() { f.releaseOnce.Do(func() { close(f.release) }) }
func (f *lifecycleModelFixture) close() {
	f.mu.Lock()
	f.closed = true
	f.cancel()
	f.mu.Unlock()
	f.active.Wait()
}
func (f *lifecycleModelFixture) pause(r *http.Request) bool {
	f.hitOnce.Do(func() { close(f.hit) })
	select {
	case <-f.ctx.Done():
		return false
	case <-r.Context().Done():
		return false
	case <-f.release:
		return true
	}
}

func (f *lifecycleModelFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	f.active.Add(1)
	point, armed := f.point, f.armed
	f.mu.Unlock()
	defer f.active.Done()
	if !armed || r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	authorized := false
	if len(r.Header.Values("Authorization")) == 1 {
		for _, key := range f.mock.keys {
			authorized = subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) == 1 || authorized
		}
	}
	if !authorized {
		f.mock.authRejected.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var in struct {
		Model     string          `json:"model"`
		Stream    bool            `json:"stream"`
		MaxTokens int64           `json:"max_tokens"`
		Messages  json.RawMessage `json:"messages"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || in.Model != "qa-chat" || in.MaxTokens != 16 || len(in.Messages) == 0 || string(in.Messages) == "null" || (point == modelBeforeFinal && !in.Stream) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mock.posts.Add(1)
	if point == modelBeforeResponse && !f.pause(r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	usage := map[string]int{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}
	if !in.Stream {
		f.mock.jsonPosts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl_model_lifecycle", "object": "chat.completion", "model": in.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "QA OK"}, "finish_reason": "stop"}}, "usage": usage})
		return
	}
	f.mock.ssePosts.Add(1)
	w.Header().Set("Content-Type", "text/event-stream")
	write := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
	}
	write(map[string]any{"id": "chatcmpl_model_lifecycle", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "QA OK"}, "finish_reason": nil}}})
	if point == modelBeforeFinal && !f.pause(r) {
		return
	}
	write(map[string]any{"id": "chatcmpl_model_lifecycle", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	write(map[string]any{"id": "chatcmpl_model_lifecycle", "object": "chat.completion.chunk", "model": in.Model, "choices": []any{}, "usage": usage})
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}
