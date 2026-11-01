package paymentreceiver

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"golang.org/x/time/rate"
)

const notificationPath = "/api/payments/sandbox/notifications"

type receiverHTTP struct {
	repository *Repository
	limiter    *rate.Limiter
	inflight   chan struct{}
}

func newReceiverHTTP(repository *Repository) *receiverHTTP {
	return &receiverHTTP{repository: repository, limiter: rate.NewLimiter(20, 40), inflight: make(chan struct{}, 4)}
}
func values(h http.Header, name string) []string {
	var out []string
	for k, v := range h {
		if strings.EqualFold(k, name) {
			out = append(out, v...)
		}
	}
	return out
}
func requestMedia(h http.Header) bool {
	v := values(h, "Content-Type")
	if len(v) != 1 {
		return false
	}
	t, params, e := mime.ParseMediaType(v[0])
	if e != nil || t != "application/json" || len(params) > 1 {
		return false
	}
	for k, v := range params {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	v = values(h, "Content-Encoding")
	return len(v) == 0 || len(v) == 1 && strings.EqualFold(v[0], "identity")
}
func httpError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}{Error: struct {
		Code string `json:"code"`
	}{code}})
}
func errorStatus(code string) int {
	switch code {
	case "invalid_request", "payment_environment_invalid":
		return 400
	case "payment_signature_invalid":
		return 401
	case "not_found":
		return 404
	case "method_not_allowed":
		return 405
	case "payment_event_conflict":
		return 409
	case "body_too_large":
		return 413
	case "unsupported_media_type":
		return 415
	case "rate_limited":
		return 429
	default:
		return 503
	}
}
func (h *receiverHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != notificationPath || r.URL.RawPath != "" {
		httpError(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		httpError(w, 405, "method_not_allowed")
		return
	}
	if r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.RawQuery != "" || r.URL.ForceQuery || len(values(r.Header, "Authorization")) != 0 || len(values(r.Header, "Upgrade")) != 0 {
		httpError(w, 400, "invalid_request")
		return
	}
	if !requestMedia(r.Header) {
		httpError(w, 415, "unsupported_media_type")
		return
	}
	if !h.limiter.Allow() {
		w.Header().Set("Retry-After", "1")
		httpError(w, 429, "rate_limited")
		return
	}
	select {
	case h.inflight <- struct{}{}:
		defer func() { <-h.inflight }()
	default:
		w.Header().Set("Retry-After", "1")
		httpError(w, 429, "rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// The HTTP deadline includes reading the body, verification, pool and COMMIT.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	if r.ContentLength > 65536 {
		httpError(w, 413, "body_too_large")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if len(body) > 65536 {
		httpError(w, 413, "body_too_large")
		return
	}
	if err != nil {
		httpError(w, 400, "invalid_request")
		return
	}
	headers, err := paymentproto.ReadSignedHeaders(r.Header)
	if err != nil {
		code := ErrorCode(err)
		httpError(w, errorStatus(code), code)
		return
	}
	out, err := h.repository.Receive(ctx, headers, body)
	if err != nil {
		code := ErrorCode(err)
		httpError(w, errorStatus(code), code)
		return
	}
	if out.Status == ReceiveConflict {
		httpError(w, 409, "payment_event_conflict")
		return
	}
	if out.Status != ReceiveNew && out.Status != ReceiveDuplicate {
		httpError(w, 503, "receiver_unavailable")
		return
	}
	b, err := json.Marshal(ReceiptEnvelope{Data: out.Receipt, RequestID: uuid.NewString()})
	if err != nil || len(b) > 8192 {
		httpError(w, 503, "receiver_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusAccepted
	if out.Status == ReceiveDuplicate {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
