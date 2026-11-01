package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// callbackRunner owns exactly two network slots. It never holds a transaction
// or session gate during HTTP and never preclaims work for a busy slot.
type callbackRunner struct {
	server    *Server
	client    *http.Client
	transport *http.Transport
	callback  string
}

func newCallbackRunner(s *Server) (*callbackRunner, error) {
	u, e := url.Parse(s.runtime.CallbackURL())
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/api/payments/sandbox/notifications" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, serverFailure("mock_unavailable")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	t := &http.Transport{Proxy: nil, DisableCompression: true, ForceAttemptHTTP2: false, MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != u.Host {
			return nil, serverFailure("mock_unavailable")
		}
		return dialer.DialContext(ctx, "tcp4", u.Host)
	}
	return &callbackRunner{server: s, transport: t, callback: u.String(), client: &http.Client{Transport: t, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (r *callbackRunner) run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.slot(ctx) }()
	}
	wg.Wait()
	r.transport.CloseIdleConnections()
}
func pause(ctx context.Context) bool {
	t := time.NewTimer(250 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (r *callbackRunner) slot(ctx context.Context) {
	for ctx.Err() == nil {
		// Expiration is a bounded state transition, not another delivery slot.
		if _, e := r.server.repository.ExpireFinalClaims(ctx, 100); e != nil {
			if !pause(ctx) {
				return
			}
			continue
		}
		claim, ok, e := r.server.repository.ClaimDue(ctx)
		if e != nil || !ok {
			if !pause(ctx) {
				return
			}
			continue
		}
		prepared, e := r.server.repository.Prepare(ctx, claim)
		if e != nil {
			if !pause(ctx) {
				return
			}
			continue
		}
		if e = r.server.runtime.Check(ctx); e != nil {
			return
		}
		status, body, e := r.sendOnce(ctx, prepared)
		if ctx.Err() != nil {
			return
		}
		var observation Observation
		if e == nil && (status == 200 || status == 202) {
			now, clockErr := r.server.repository.ObserveReceivedAt(ctx)
			if clockErr != nil {
				continue
			}
			eventID, idErr := prepared.EventID()
			if idErr != nil {
				continue
			}
			ack, ackErr := ParseACK(status, body, eventID, now)
			if ackErr == nil {
				observation, e = ACKObservation(ack)
			} else {
				code := FailureProtocol
				if ErrorCode(ackErr) == "payment_environment_invalid" {
					code = FailureEnvironment
				}
				observation, e = FailedObservation(code, &status)
			}
		} else {
			code := FailureNetwork
			var netErr net.Error
			if errors.Is(e, serverFailure("mock_protocol_invalid")) {
				code = FailureProtocol
			} else if e == nil {
				switch {
				case status == 401 || status == 403:
					code = FailureAuth
				case status == 429 || status >= 500:
					code = FailureTemporary
				case status >= 300 && status < 400:
					code = FailureProtocol
				default:
					code = FailurePermanent
				}
			} else if errors.As(e, &netErr) && netErr.Timeout() {
				code = FailureTimeout
			}
			var safeStatus *int
			if status >= 100 && status <= 599 {
				safeStatus = &status
			}
			observation, e = FailedObservation(code, safeStatus)
		}
		if e != nil {
			continue
		}
		// Unknown persistence leaves the claim to its durable lease. No IO retry.
		_, _ = r.server.repository.RecordObservation(ctx, claim, observation)
	}
}
func (r *callbackRunner) sendOnce(ctx context.Context, p PreparedDelivery) (int, []byte, error) {
	body, e := p.BodyBytes()
	if e != nil {
		return 0, nil, e
	}
	h, e := p.Headers()
	if e != nil {
		return 0, nil, e
	}
	ioCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ioCtx, http.MethodPost, r.callback, bytes.NewReader(body))
	if e != nil {
		return 0, nil, serverFailure("mock_unavailable")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Moni-Key-Id", h.KeyID)
	req.Header.Set("X-Moni-Delivery-Id", h.DeliveryID)
	req.Header.Set("X-Moni-Delivery-Time", h.DeliveryTime)
	req.Header.Set("X-Moni-Signature", h.Signature)
	res, e := r.client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer res.Body.Close()
	if res.ProtoMajor != 1 || res.ProtoMinor != 1 || !validMedia(res.Header) {
		return res.StatusCode, nil, serverFailure("mock_protocol_invalid")
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 8193))
	if e != nil || len(raw) > 8192 {
		return res.StatusCode, nil, serverFailure("mock_protocol_invalid")
	}
	return res.StatusCode, raw, nil
}
