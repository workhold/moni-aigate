package provider

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Do sends exactly one generation. Redirects and implicit replay are disabled.
func (c *Client) Do(ctx context.Context, endpoint Endpoint, request *Request) (*Response, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, failure("deadline_required", 503, ClassConfiguration, ConsumptionNotSent)
	}
	if request == nil {
		return nil, badRequest()
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("request_cancelled", 499, ClassCancelled, ConsumptionNotSent)
	}
	u, allow, err := validateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	model := endpoint.Model
	if model == "" {
		model = request.Model
		if request.profile.InputOnly() || request.Protocol() == ProtocolGemini {
			model = request.profile.Model
		}
	}
	payload, err := request.UpstreamJSON(model)
	if err != nil {
		return nil, err
	}
	// The only query allowed for Gemini is generated internally after the
	// configured endpoint has passed the common query-free SSRF validation.
	if request.Protocol() == ProtocolGemini && request.stream {
		u.RawQuery = "alt=sse"
	}
	ctx, cancel := context.WithCancel(ctx)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		cancel()
		return nil, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
	}
	httpRequest.ContentLength = int64(len(payload))
	httpRequest.GetBody = nil
	httpRequest.Header.Set("Content-Type", "application/json")
	if request.stream {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else {
		httpRequest.Header.Set("Accept", "application/json")
	}
	if request.Protocol() == ProtocolGemini {
		if endpoint.APIKey != "" {
			httpRequest.Header.Set("x-goog-api-key", endpoint.APIKey)
		}
	} else if request.Protocol() == ProtocolMessages {
		httpRequest.Header.Set("anthropic-version", "2023-06-01")
		if endpoint.APIKey != "" {
			httpRequest.Header.Set("x-api-key", endpoint.APIKey)
		}
	} else if endpoint.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
	}
	var written atomic.Bool
	transport := c.transport(endpoint, u, allow, &written)
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	upstream, err := client.Do(httpRequest)
	if err != nil {
		contextErr := ctx.Err()
		cancel()
		transport.CloseIdleConnections()
		consumption := ConsumptionUnknown
		if !written.Load() {
			consumption = ConsumptionNotSent
		}
		if contextErr != nil {
			class := ClassCancelled
			code := "request_cancelled"
			status := 499
			if errors.Is(contextErr, context.DeadlineExceeded) {
				class = ClassTimeout
				code = "request_timeout"
				status = 504
			}
			return nil, failure(code, status, class, consumption)
		}
		var known *Error
		if errors.As(err, &known) {
			return nil, known
		}
		return nil, failure("upstream_connection_failed", 502, ClassNetwork, consumption)
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		errorTimer := time.AfterFunc(c.options.IdleTimeout, func() { cancel(); _ = upstream.Body.Close() })
		errorBody, _ := io.ReadAll(io.LimitReader(upstream.Body, 64<<10))
		errorTimer.Stop()
		_ = upstream.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		class := classifyUpstream(upstream.StatusCode, errorBody)
		consumption := ConsumptionUnknown
		for _, s := range request.profile.NoConsumptionStatuses {
			if s == upstream.StatusCode {
				consumption = ConsumptionRejected
			}
		}
		e := failure("upstream_rejected", 502, class, consumption)
		e.UpstreamStatus = upstream.StatusCode
		if upstream.StatusCode == 429 {
			e.HTTPStatus = 429
		}
		e.RetryAfter = ParseRetryAfter(upstream.Header.Get("Retry-After"), time.Now())
		return nil, e
	}
	media, _, e := mime.ParseMediaType(upstream.Header.Get("Content-Type"))
	expected := "application/json"
	if request.stream {
		expected = "text/event-stream"
	}
	if e != nil || !strings.EqualFold(media, expected) {
		_ = upstream.Body.Close()
		cancel()
		return nil, failure("upstream_content_type", 502, ClassProtocol, ConsumptionUnknown)
	}
	r := &Response{body: upstream.Body, cancel: cancel, ctx: ctx, profile: request.Profile(), model: request.Model, stream: request.stream, retrieval: request.retrieval.clone(), Usage: Usage{Status: UsageMissing}, ProtocolOutcome: "pending", TransportEndReason: "pending", options: c.options}
	if request.profile.InputOnly() || request.Protocol() == ProtocolGemini {
		r.model = request.profile.Model
	}
	r.tracker.semantics = request.profile.UsageSemantics
	r.idle = time.AfterFunc(c.options.IdleTimeout, func() { r.timeout("upstream_idle_timeout") })
	if request.stream {
		r.first = time.AfterFunc(c.options.FirstEventTimeout, func() { r.timeout("upstream_first_event_timeout") })
		r.progress = time.AfterFunc(c.options.ProgressTimeout, func() { r.timeout("upstream_progress_timeout") })
		r.reader = bufio.NewReaderSize(&observedReader{response: r}, 32<<10)
	}
	return r, nil
}

func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if n, e := strconv.ParseInt(value, 10, 64); e == nil {
		if n <= 0 || n > int64((24*time.Hour)/time.Second) {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(value); e == nil {
		d := t.Sub(now)
		if d > 0 && d <= 24*time.Hour {
			return d
		}
	}
	return 0
}

type Response struct {
	Usage                 Usage
	ProtocolOutcome       string
	TransportEndReason    string
	FinishReason          string
	body                  io.ReadCloser
	cancel                context.CancelFunc
	ctx                   context.Context
	profile               Profile
	model                 string
	stream                bool
	options               ClientOptions
	reader                *bufio.Reader
	tracker               usageTracker
	closed                sync.Once
	timers                sync.Mutex
	idle, first, progress *time.Timer
	timeoutCode           atomic.Pointer[string]
	stopped               bool
	firstSeen             bool
	skipLF                bool
	done                  bool
	native                nativeState
	retrieval             retrievalExpectation
	gemini                geminiState
}

func (r *Response) Close() error {
	var err error
	r.closed.Do(func() {
		r.timers.Lock()
		r.stopped = true
		for _, t := range []*time.Timer{r.idle, r.first, r.progress} {
			if t != nil {
				t.Stop()
			}
		}
		r.timers.Unlock()
		r.cancel()
		err = r.body.Close()
	})
	return err
}
func (r *Response) timeout(code string) {
	r.timers.Lock()
	defer r.timers.Unlock()
	if !r.stopped {
		r.timeoutCode.CompareAndSwap(nil, &code)
		r.cancel()
		_ = r.body.Close()
	}
}
func (r *Response) bytesRead() {
	r.timers.Lock()
	defer r.timers.Unlock()
	if !r.stopped && r.idle != nil {
		r.idle.Reset(r.options.IdleTimeout)
	}
}
func (r *Response) protocolEvent(progress bool) {
	r.timers.Lock()
	defer r.timers.Unlock()
	if r.stopped {
		return
	}
	if !r.firstSeen {
		r.firstSeen = true
		if r.first != nil {
			r.first.Stop()
		}
	}
	if progress && r.progress != nil {
		r.progress.Reset(r.options.ProgressTimeout)
	}
}

type observedReader struct{ response *Response }

func (o *observedReader) Read(p []byte) (int, error) {
	n, e := o.response.body.Read(p)
	if n > 0 {
		o.response.bytesRead()
	}
	return n, e
}

func (r *Response) readFailure(err error) error {
	r.done = true
	r.ProtocolOutcome = "failed"
	r.TransportEndReason = "upstream_error"
	if code := r.timeoutCode.Load(); code != nil {
		r.TransportEndReason = *code
		return failure(*code, 504, ClassTimeout, ConsumptionUnknown)
	}
	if r.ctx.Err() != nil {
		r.TransportEndReason = "cancelled"
		if errors.Is(r.ctx.Err(), context.DeadlineExceeded) {
			r.TransportEndReason = "deadline"
			return failure("request_timeout", 504, ClassTimeout, ConsumptionUnknown)
		}
		return failure("request_cancelled", 499, ClassCancelled, ConsumptionUnknown)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		r.TransportEndReason = "unexpected_eof"
		return failure("upstream_incomplete", 502, ClassProtocol, ConsumptionUnknown)
	}
	var e *Error
	if errors.As(err, &e) {
		r.TransportEndReason = "protocol_error"
		return e
	}
	return failure("upstream_read_failed", 502, ClassNetwork, ConsumptionUnknown)
}
