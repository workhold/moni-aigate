package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The upstream and PostgreSQL are real. Only the customer's response writer is
// controlled, so the failure happens at a precise post-settlement write/flush
// boundary instead of relying on socket-close timing and kernel buffering.
type mvpFailingWriter struct {
	*httptest.ResponseRecorder
	failBody, failTerminalWrite, failTerminalFlush bool
	lastTerminal                                   bool
	failures                                       int
	beforeFailure                                  func()
}

func (w *mvpFailingWriter) Write(p []byte) (int, error) {
	w.lastTerminal = bytes.Contains(p, []byte("data: [DONE]"))
	if w.failBody || (w.failTerminalWrite && w.lastTerminal) {
		w.beforeFailure()
		w.failures++
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func (w *mvpFailingWriter) FlushError() error {
	if w.failTerminalFlush && w.lastTerminal {
		w.beforeFailure()
		w.failures++
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func (w *mvpFailingWriter) Flush() { _ = w.FlushError() }

func (w *mvpFailingWriter) SetWriteDeadline(time.Time) error { return nil }

func TestMVPPostgresPostSettlementDeliveryFailureKeepsCharge(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stream   bool
		body     bool
		terminal bool
		flush    bool
	}{
		{name: "nonstream_body_write", body: true},
		{name: "stream_terminal_write", stream: true, terminal: true},
		{name: "stream_terminal_flush", stream: true, flush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := gatewaySuccess(t)
			if tc.stream {
				handler = gatewayStream(t, true)
			}
			f := newGatewayFixture(t, []http.HandlerFunc{handler}, nil)
			body := `{"model":"retail-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":false}`
			if tc.stream {
				body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
			}
			req := httptest.NewRequest(http.MethodPost, "http://console.test/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.token)
			writer := &mvpFailingWriter{ResponseRecorder: httptest.NewRecorder(), failBody: tc.body, failTerminalWrite: tc.terminal, failTerminalFlush: tc.flush}
			writer.beforeFailure = func() {
				f.assertSettled(t, writer.Header().Get("X-Request-ID"), 1)
			}
			f.server.Engine().ServeHTTP(writer, req)
			if writer.failures != 1 {
				t.Fatalf("expected exactly one controlled delivery failure, got %d", writer.failures)
			}
			requestID := writer.Header().Get("X-Request-ID")
			f.assertSettled(t, requestID, 1)
			var stage string
			if err := f.pool.QueryRow(req.Context(), "SELECT stage FROM delivery_observations WHERE request_id=$1", requestID).Scan(&stage); err != nil || stage != "write_failed" {
				t.Fatalf("post-settlement failure observation: stage=%s err=%v", stage, err)
			}
			if f.posts.Load() != 1 || len(f.server.global) != 0 {
				t.Fatalf("delivery failure replayed upstream or leaked permit: posts=%d permits=%d", f.posts.Load(), len(f.server.global))
			}
		})
	}
}
