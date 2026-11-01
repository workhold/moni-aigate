package payments

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

func (r *paymentRuntime) doSandboxRequest(ctx context.Context, kind string, ownedCanonical []byte) (int, http.Header, []byte, error) {
	if r == nil || !r.hasEnvironment() || r.options.component != workerComponent {
		return 0, nil, nil, ErrPaymentsConfiguration
	}
	if kind != "checkout" && kind != "cancel" && kind != "query" || len(ownedCanonical) == 0 || len(ownedCanonical) > 65536 {
		return 0, nil, nil, ErrPaymentsInvalidWork
	}
	if r.recovery() && kind != "query" {
		return 0, nil, nil, ErrPaymentsRecovery
	}
	if e := r.check(ctx); e != nil {
		return 0, nil, nil, e
	}
	origin, e := runtimeOrigin(r.options.proof.APIOrigin, false)
	if e != nil {
		return 0, nil, nil, ErrPaymentsEnvironment
	}
	u, _ := url.Parse(origin)
	address := u.Host
	// Each bounded request owns and closes its transport. No extra runtime Close
	// lifecycle or reusable connection can outlive the processor operation.
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, ForceAttemptHTTP2: false,
		DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
			if network != "tcp" || target != address {
				return nil, errSandboxNetwork
			}
			return dialer.DialContext(ctx, "tcp", address)
		},
		MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 10 * time.Second, TLSHandshakeTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errSandboxNetwork }}
	ioCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ioCtx, http.MethodPost, origin+"/v1/"+kind, bytes.NewReader(bytes.Clone(ownedCanonical)))
	if e != nil {
		return 0, nil, nil, ErrPaymentsInvalidWork
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(r.options.apiToken[:]))
	response, e := client.Do(req)
	if e != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return 0, nil, nil, runtimeTransportError(ctx, ioCtx, e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil {
		return 0, nil, nil, runtimeTransportError(ctx, ioCtx, e)
	}
	if len(raw) > 65536 {
		return 0, nil, nil, errSandboxNetwork
	}
	if e := ctx.Err(); e != nil {
		return 0, nil, nil, runtimeTransportError(ctx, ioCtx, e)
	}
	if r.fenced.Load() {
		return 0, nil, nil, ErrPaymentsGuard
	}
	return response.StatusCode, response.Header.Clone(), raw, nil
}
func runtimeTransportError(caller, ioCtx context.Context, e error) error {
	if caller.Err() == context.Canceled {
		return context.Canceled
	}
	if ioCtx.Err() == context.DeadlineExceeded || caller.Err() == context.DeadlineExceeded {
		return errSandboxTimeout
	}
	var timeout net.Error
	if errors.As(e, &timeout) && timeout.Timeout() {
		return errSandboxTimeout
	}
	return errSandboxNetwork
}
