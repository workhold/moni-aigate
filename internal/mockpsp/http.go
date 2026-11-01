package mockpsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"golang.org/x/time/rate"
)

type serverError string

func (e serverError) Error() string   { return string(e) }
func serverFailure(code string) error { return serverError(code) }
func headerValues(h http.Header, name string) []string {
	var out []string
	for k, v := range h {
		if strings.EqualFold(k, name) {
			out = append(out, v...)
		}
	}
	return out
}
func validMedia(h http.Header) bool {
	v := headerValues(h, "Content-Type")
	if len(v) != 1 {
		return false
	}
	t, p, e := mime.ParseMediaType(v[0])
	if e != nil || t != "application/json" || len(p) > 1 {
		return false
	}
	for k, v := range p {
		if k != "charset" || !strings.EqualFold(v, "utf-8") {
			return false
		}
	}
	v = headerValues(h, "Content-Encoding")
	return len(v) == 0 || len(v) == 1 && strings.EqualFold(v[0], "identity")
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}{Error: struct {
		Code string `json:"code"`
	}{code}})
}
func statusFor(code string) int {
	switch code {
	case "invalid_request", "payment_environment_invalid":
		return 400
	case "mock_auth_invalid", "payment_signature_invalid":
		return 401
	case "not_found":
		return 404
	case "method_not_allowed":
		return 405
	case "command_parameter_conflict", "query_parameter_conflict", "mock_control_conflict":
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

type mockHTTP struct {
	runtime    *paymentruntime.Runtime
	repository *Repository
	control    bool
	limiter    *rate.Limiter
	slots      chan struct{}
}

func newMockHTTP(rt *paymentruntime.Runtime, repo *Repository, control bool) *mockHTTP {
	q, b, n := 20, 40, 4
	if control {
		q, b, n = 5, 10, 2
	}
	return &mockHTTP{runtime: rt, repository: repo, control: control, limiter: rate.NewLimiter(rate.Limit(q), b), slots: make(chan struct{}, n)}
}
func disconnect(w http.ResponseWriter) {
	h, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, 503, "mock_unavailable")
		return
	}
	c, _, e := h.Hijack()
	if e != nil {
		writeError(w, 503, "mock_unavailable")
		return
	}
	_ = c.Close()
}
func writeReply(w http.ResponseWriter, body []byte) {
	if len(body) == 0 || len(body) > 65536 {
		writeError(w, 503, "mock_unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
func signedHeaders(w http.ResponseWriter, h paymentproto.SignedHeaders) {
	w.Header().Set("X-Moni-Key-Id", h.KeyID)
	w.Header().Set("X-Moni-Delivery-Id", h.DeliveryID)
	w.Header().Set("X-Moni-Delivery-Time", h.DeliveryTime)
	w.Header().Set("X-Moni-Signature", h.Signature)
}
func (h *mockHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	allowed := path == "/v1/checkout" || path == "/v1/cancel" || path == "/v1/query"
	if h.control {
		allowed = path == "/_qa/v1/commands"
	}
	if !allowed || r.URL.RawPath != "" {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed")
		return
	}
	if r.ProtoMajor != 1 || r.ProtoMinor != 1 || r.URL.RawQuery != "" || r.URL.ForceQuery || len(headerValues(r.Header, "Upgrade")) != 0 {
		writeError(w, 400, "invalid_request")
		return
	}
	if !validMedia(r.Header) {
		writeError(w, 415, "unsupported_media_type")
		return
	}
	tokenKind := paymentruntime.APIToken
	if h.control {
		tokenKind = paymentruntime.ControlToken
	}
	auth := headerValues(r.Header, "Authorization")
	if len(auth) != 1 || !strings.HasPrefix(auth[0], "Bearer ") || !h.runtime.TokenMatches(tokenKind, strings.TrimPrefix(auth[0], "Bearer ")) {
		writeError(w, 401, "mock_auth_invalid")
		return
	}
	if !h.limiter.Allow() {
		w.Header().Set("Retry-After", "1")
		writeError(w, 429, "rate_limited")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		w.Header().Set("Retry-After", "1")
		writeError(w, 429, "rate_limited")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Second))
	if r.ContentLength > 8192 {
		writeError(w, 413, "body_too_large")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8193))
	if len(raw) > 8192 {
		writeError(w, 413, "body_too_large")
		return
	}
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	var body []byte
	env := h.runtime.Environment()
	switch path {
	case "/v1/checkout":
		var in CanonicalCheckout
		in, err = ParseCheckoutInput(env, raw)
		if err == nil {
			var out CommandOutcome
			out, err = h.repository.Checkout(ctx, in)
			if err == nil {
				body, err = out.Body.Bytes()
			}
		}
	case "/v1/cancel":
		var in CanonicalCancel
		in, err = ParseCancelInput(env, raw)
		if err == nil {
			var out CommandOutcome
			out, err = h.repository.Cancel(ctx, in)
			if err == nil {
				body, err = out.Body.Bytes()
			}
		}
	case "/v1/query":
		var in CanonicalQuery
		in, err = ParseQueryInput(env, raw)
		if err == nil {
			var out QueryOutcome
			out, err = h.repository.Query(ctx, in)
			if err == nil {
				var signed SignedQueryOutcome
				signed, err = h.repository.SignQueryOutcome(ctx, in, out)
				if err == nil {
					body, err = signed.Body.Bytes()
					if err == nil {
						signedHeaders(w, signed.Headers)
					}
				}
			}
		}
	case "/_qa/v1/commands":
		var in CanonicalControl
		in, err = ParseControlInput(env, raw)
		if err == nil {
			var out ControlOutcome
			out, err = h.repository.Control(ctx, in)
			if err == nil {
				body, err = json.Marshal(out.Reply)
			}
		}
	}
	if errors.Is(err, ErrDisconnect) {
		disconnect(w)
		return
	}
	if err != nil {
		code := ErrorCode(err)
		writeError(w, statusFor(code), code)
		return
	}
	writeReply(w, body)
}
