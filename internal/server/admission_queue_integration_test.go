package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
)

type admissionHTTPResult struct {
	response *http.Response
	err      error
}

// Construct requests on the test goroutine. Workers only perform Client.Do;
// assertions, body reads and cleanup registration stay on the test goroutine.
func admissionHTTPRequest(t *testing.T, f *gatewayFixture, ctx context.Context, streaming bool) *http.Request {
	t.Helper()
	body := `{"model":"retail-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":false}`
	if streaming {
		body = `{"model":"retail-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":true,"stream_options":{"include_usage":true}}`
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+f.token)
	return request
}

func admissionStartHTTP(client *http.Client, request *http.Request) <-chan admissionHTTPResult {
	result := make(chan admissionHTTPResult, 1)
	go func() {
		response, err := client.Do(request)
		result <- admissionHTTPResult{response: response, err: err}
	}()
	return result
}

func admissionAwaitHTTP(t *testing.T, result <-chan admissionHTTPResult) admissionHTTPResult {
	t.Helper()
	select {
	case got := <-result:
		if got.response != nil {
			t.Cleanup(func() { _ = got.response.Body.Close() })
		}
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP request did not reach its expected state")
		return admissionHTTPResult{}
	}
}

func admissionAwait(t *testing.T, state string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("did not observe %s", state)
		case <-tick.C:
		}
	}
}

type admissionRouteResource struct{ channel, health string }

func admissionRouteResources(t *testing.T, f *gatewayFixture) []admissionRouteResource {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := f.pool.Query(ctx, "SELECT c.id,v.credential_id FROM channels c JOIN channel_versions v ON v.id=c.current_version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var resources []admissionRouteResource
	for rows.Next() {
		var resource admissionRouteResource
		var credential string
		if err := rows.Scan(&resource.channel, &credential); err != nil {
			t.Fatal(err)
		}
		resource.health = credential + ":" + f.model
		resources = append(resources, resource)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(resources) == 0 {
		t.Fatal("fixture has no route resources")
	}
	return resources
}

type admissionResourceState struct {
	global, tenant, key, inflight int
	streams                       int64
	probe                         bool
}

func admissionResources(f *gatewayFixture, routes []admissionRouteResource) admissionResourceState {
	f.server.mu.Lock()
	state := admissionResourceState{global: len(f.server.global), tenant: f.server.tenantPermits[f.tenant], key: f.server.keyPermits[f.key], streams: f.server.Streams.Load()}
	f.server.mu.Unlock()
	for _, route := range routes {
		snapshot := f.server.Routing.Snapshot(route.health, route.channel)
		state.inflight += snapshot.Inflight
		state.probe = state.probe || snapshot.ProbeInFlight
	}
	return state
}

func admissionAssertNoFacts(t *testing.T, f *gatewayFixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var requests, reservations, attempts, settlements, held, spent int64
	err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM settlements),held,spent FROM api_keys WHERE id=$1`, f.key).Scan(&requests, &reservations, &attempts, &settlements, &held, &spent)
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := f.server.Billing.Wallet(ctx, billing.Query{TenantID: f.tenant})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 || reservations != 0 || attempts != 0 || settlements != 0 || held != 0 || spent != 0 || wallet.Available != 1000 || wallet.Held != 0 || f.posts.Load() != 0 {
		t.Fatalf("queued request created facts: requests=%d reservations=%d attempts=%d settlements=%d held=%d spent=%d wallet=%+v posts=%d", requests, reservations, attempts, settlements, held, spent, wallet, f.posts.Load())
	}
}

func TestGatewayPostgresAdmissionWaitingCancellationUsesNoDatabaseOrChannelLease(t *testing.T) {
	f := newGatewayFixtureWithPool(t, []http.HandlerFunc{gatewaySuccess(t)}, nil, 1)
	routes := admissionRouteResources(t, f)
	ctx, timeout := context.WithTimeout(context.Background(), 10*time.Second)
	defer timeout()
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	request := admissionHTTPRequest(t, f, requestCtx, false)
	result := admissionStartHTTP(f.http.Client(), request)
	admissionAwait(t, "one runtime permit and a waiting admission ticket", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2 && admissionResources(f, routes) == (admissionResourceState{global: 1, tenant: 1, key: 1}) && f.pool.Stat().AcquiredConns() == 0
	})
	admissionAssertNoFacts(t, f)
	// Owning the only database connection proves cancellation does not need
	// a connection or transaction. Its PostgreSQL status is idle before and
	// after cancellation; the waiting request has no other pool connection.
	connection, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if connection.Conn().PgConn().TxStatus() != 'I' || f.pool.Stat().TotalConns() != 1 {
		t.Fatal("waiting admission retained a database transaction or connection")
	}
	cancel()
	got := admissionAwaitHTTP(t, result)
	if got.response != nil || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("waiting cancellation returned response=%v error=%v", got.response, got.err)
	}
	admissionAwait(t, "cancelled waiter removed and runtime permits returned", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 1 && admissionResources(f, routes) == (admissionResourceState{})
	})
	if connection.Conn().PgConn().TxStatus() != 'I' || f.pool.Stat().AcquiredConns() != 1 {
		t.Fatal("cancelled waiter borrowed the occupied pool or left a transaction")
	}
	connection.Release()
	admissionAssertNoFacts(t, f)
	owner()
	owner()
	if admissionQueueDepth(&f.server.admission, f.key) != 0 {
		t.Fatal("released admission owner left a queue")
	}
}

func TestGatewayPostgresAdmissionRechecksPermissionsAfterWaiting(t *testing.T) {
	for _, boundary := range []struct {
		name, setup, revoke string
		status              int
	}{
		{name: "key_revoked", revoke: "UPDATE api_keys SET revoked_at=clock_timestamp() WHERE id=$1", status: http.StatusUnauthorized},
		{name: "tenant_model_grant", revoke: "DELETE FROM tenant_model_grants WHERE tenant_id=$1 AND model_id=$2", status: http.StatusForbidden},
		{name: "member_model_grant", setup: "member", revoke: "DELETE FROM member_model_grants WHERE tenant_id=$1 AND model_id=$2", status: http.StatusForbidden},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
			routes := admissionRouteResources(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if boundary.setup == "member" {
				if _, err := f.pool.Exec(ctx, "UPDATE memberships SET allow_all_models=false WHERE tenant_id=$1", f.tenant); err != nil {
					t.Fatal(err)
				}
				if _, err := f.pool.Exec(ctx, "INSERT INTO member_model_grants(tenant_id,user_id,model_id) SELECT tenant_id,owner_id,$2 FROM api_keys WHERE id=$1", f.key, f.model); err != nil {
					t.Fatal(err)
				}
			}
			owner, err := f.server.admission.acquire(ctx, f.key)
			if err != nil {
				t.Fatal(err)
			}
			defer owner()
			request := admissionHTTPRequest(t, f, ctx, false)
			result := admissionStartHTTP(f.http.Client(), request)
			admissionAwait(t, "authorized request waiting before reservation", func() bool {
				return admissionQueueDepth(&f.server.admission, f.key) == 2
			})
			admissionAssertNoFacts(t, f)
			args := []any{f.tenant, f.model}
			if boundary.name == "key_revoked" {
				args = []any{f.key}
			}
			// This committed change occurs after HTTP authentication and the
			// initial model lookup, while the request is explicitly parked.
			if _, err := f.pool.Exec(ctx, boundary.revoke, args...); err != nil {
				t.Fatal(err)
			}
			owner()
			got := admissionAwaitHTTP(t, result)
			if got.err != nil {
				t.Fatal(got.err)
			}
			body, err := io.ReadAll(got.response.Body)
			if err != nil || got.response.StatusCode != boundary.status {
				t.Fatalf("revoked authorization: HTTP %d %s %v", got.response.StatusCode, body, err)
			}
			admissionAwait(t, "rejected request returning all resources", func() bool {
				return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
			})
			admissionAssertNoFacts(t, f)
		})
	}
}

func TestGatewayPostgresAdmissionLongStreamDoesNotBlockSameKeySuccessor(t *testing.T) {
	finishStream := make(chan struct{})
	var finishOnce sync.Once
	var calls atomic.Int32
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		if calls.Add(1) != 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, gatewayJSON)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		select {
		case <-finishStream:
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", gatewayFinish, gatewayUsage)
		case <-r.Context().Done():
		}
	}}, nil)
	t.Cleanup(func() { finishOnce.Do(func() { close(finishStream) }) })
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstRequest := admissionHTTPRequest(t, f, ctx, true)
	first := admissionAwaitHTTP(t, admissionStartHTTP(f.http.Client(), firstRequest))
	if first.err != nil {
		t.Fatal(first.err)
	}
	reader := bufio.NewReader(first.response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || first.response.StatusCode != http.StatusOK || !strings.Contains(line, "hello") {
		t.Fatalf("first stream did not start: HTTP %d %s %v", first.response.StatusCode, line, err)
	}
	wantRunning := admissionResourceState{global: 1, tenant: 1, key: 1, inflight: 1, streams: 1}
	admissionAwait(t, "active stream with no admission ticket", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == wantRunning
	})
	secondRequest := admissionHTTPRequest(t, f, ctx, false)
	second := admissionAwaitHTTP(t, admissionStartHTTP(f.http.Client(), secondRequest))
	if second.err != nil {
		t.Fatal(second.err)
	}
	body, err := io.ReadAll(second.response.Body)
	if err != nil || second.response.StatusCode != http.StatusOK {
		t.Fatalf("same-key successor: HTTP %d %s %v", second.response.StatusCode, body, err)
	}
	admissionAwait(t, "successor finished while first stream still holds execution permits", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == wantRunning
	})
	if f.posts.Load() != 2 {
		t.Fatalf("expected both upstream requests before finishing stream, got %d", f.posts.Load())
	}
	finishOnce.Do(func() { close(finishStream) })
	rest, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("stream did not complete: %s %v", rest, err)
	}
	admissionAwait(t, "both requests returning all execution resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	admissionAssertTwoSettled(t, f, []string{first.response.Header.Get("X-Request-ID"), second.response.Header.Get("X-Request-ID")}, []int64{1, 1})
}

func admissionAssertTwoSettled(t *testing.T, f *gatewayFixture, requests []string, attempts []int64) {
	t.Helper()
	for i, request := range requests {
		state := f.state(t, request)
		if state.billing != "settled" || state.reservation != "settled" || state.amount != 140 || state.charge != 20 || state.settlements != 1 || state.attempts != attempts[i] || state.available != 960 || state.held != 0 || state.spent != 40 || state.keyHeld != 0 || state.usage != "confirmed_final" {
			t.Fatalf("incorrect two-request final charge/hold: %+v", state)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ledger, err := f.server.Billing.VerifyLedger(ctx, billing.Query{})
	if err != nil || !ledger.Balanced {
		t.Fatalf("unbalanced two-request ledger: %+v %v", ledger, err)
	}
}

func TestGatewayPostgresAdmissionPreservesSameKeyIdempotency(t *testing.T) {
	finish := make(chan struct{})
	var finishOnce sync.Once
	f := newGatewayFixture(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		select {
		case <-finish:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, gatewayJSON)
		case <-r.Context().Done():
		}
	}}, nil)
	t.Cleanup(func() { finishOnce.Do(func() { close(finish) }) })
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	firstRequest := admissionHTTPRequest(t, f, ctx, false)
	firstRequest.Header.Set("Idempotency-Key", "queued-same-key")
	firstResult := admissionStartHTTP(f.http.Client(), firstRequest)
	admissionAwait(t, "first idempotent request queued", func() bool { return admissionQueueDepth(&f.server.admission, f.key) == 2 })
	secondRequest := admissionHTTPRequest(t, f, ctx, false)
	secondRequest.Header.Set("Idempotency-Key", "queued-same-key")
	secondResult := admissionStartHTTP(f.http.Client(), secondRequest)
	admissionAwait(t, "both idempotent requests queued in order", func() bool { return admissionQueueDepth(&f.server.admission, f.key) == 3 })
	owner()
	second := admissionAwaitHTTP(t, secondResult)
	if second.err != nil {
		t.Fatal(second.err)
	}
	body, err := io.ReadAll(second.response.Body)
	if err != nil || second.response.StatusCode != http.StatusConflict || !strings.Contains(string(body), "request_exists") {
		t.Fatalf("duplicate request: HTTP %d %s %v", second.response.StatusCode, body, err)
	}
	admissionAwait(t, "one original request running without a duplicate admission ticket", func() bool {
		return f.posts.Load() == 1 && admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{global: 1, tenant: 1, key: 1, inflight: 1})
	})
	finishOnce.Do(func() { close(finish) })
	first := admissionAwaitHTTP(t, firstResult)
	if first.err != nil {
		t.Fatal(first.err)
	}
	body, err = io.ReadAll(first.response.Body)
	if err != nil || first.response.StatusCode != http.StatusOK {
		t.Fatalf("original request: HTTP %d %s %v", first.response.StatusCode, body, err)
	}
	if second.response.Header.Get("X-Original-Request-ID") != first.response.Header.Get("X-Request-ID") {
		t.Fatal("duplicate request did not retain its persistent original request ID")
	}
	admissionAwait(t, "idempotent requests returning all resources", func() bool { return admissionResources(f, routes) == (admissionResourceState{}) })
	f.assertSettled(t, first.response.Header.Get("X-Request-ID"), 1)
	var requests, reservations int64
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations)").Scan(&requests, &reservations); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || reservations != 1 || f.posts.Load() != 1 {
		t.Fatalf("duplicate created a second fact: requests=%d reservations=%d posts=%d", requests, reservations, f.posts.Load())
	}
}

func TestGatewayPostgresAdmissionSafeRetryRejoinsBehindWaitingRequest(t *testing.T) {
	firstStarted := make(chan struct{})
	rejectFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	finishSecond := make(chan struct{})
	var rejectOnce sync.Once
	var finishOnce sync.Once
	var calls atomic.Int32
	upstream := func(w http.ResponseWriter, r *http.Request) {
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			select {
			case <-rejectFirst:
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
			case <-r.Context().Done():
			}
			return
		case 2:
			close(secondStarted)
			select {
			case <-finishSecond:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = io.WriteString(w, gatewayJSON)
	}
	// Both channels share call numbering: the initial 429 cools down the
	// primary before its completion ticket, so the successor can also use
	// the secondary. The test does not depend on which channel it selects.
	f := newGatewayFixture(t, []http.HandlerFunc{upstream, upstream}, []int{429})
	t.Cleanup(func() { rejectOnce.Do(func() { close(rejectFirst) }) })
	t.Cleanup(func() { finishOnce.Do(func() { close(finishSecond) }) })
	routes := admissionRouteResources(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	firstRequest := admissionHTTPRequest(t, f, ctx, false)
	firstResult := admissionStartHTTP(f.http.Client(), firstRequest)
	select {
	case <-firstStarted:
	case <-ctx.Done():
		t.Fatal("first upstream request did not start")
	}
	// Completion now shares the lane. Initially park the successor's
	// admission and then the first attempt's finalization behind this owner.
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	secondRequest := admissionHTTPRequest(t, f, ctx, false)
	secondResult := admissionStartHTTP(f.http.Client(), secondRequest)
	admissionAwait(t, "successor waiting behind local owner", func() bool { return admissionQueueDepth(&f.server.admission, f.key) == 2 })
	rejectOnce.Do(func() { close(rejectFirst) })
	admissionAwait(t, "first completion waiting behind successor while retaining its old channel lease", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 3 && admissionResources(f, routes) == (admissionResourceState{global: 2, tenant: 2, key: 2, inflight: 1})
	})
	tailResult := make(chan admissionResult, 1)
	go func() {
		release, err := f.server.admission.acquire(ctx, f.key)
		tailResult <- admissionResult{release: release, err: err}
	}()
	admissionAwait(t, "tail owner registered after the first completion ticket", func() bool { return admissionQueueDepth(&f.server.admission, f.key) == 4 })
	owner()
	tail := receiveAdmission(t, tailResult)
	if tail.err != nil || tail.release == nil {
		t.Fatalf("tail owner did not acquire after completion: %v", tail.err)
	}
	defer tail.release()
	select {
	case <-secondStarted:
	case <-ctx.Done():
		t.Fatal("successor upstream request did not start")
	}
	// The successor has passed admission and is blocked reading its provider
	// response. The first attempt has durably finalized and returned its old
	// channel lease. Its retry must take a fresh ticket behind the tail owner.
	admissionAwait(t, "safe retry rejoining behind tail owner with only successor holding a channel lease", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2 && admissionResources(f, routes) == (admissionResourceState{global: 2, tenant: 2, key: 2, inflight: 1})
	})
	var requests, reservations, attempts int64
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM reservations),(SELECT count(*) FROM upstream_attempts)").Scan(&requests, &reservations, &attempts); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || reservations != 2 || attempts != 2 || f.posts.Load() != 2 {
		t.Fatalf("waiting retry advanced persistent work: requests=%d reservations=%d attempts=%d posts=%d", requests, reservations, attempts, f.posts.Load())
	}
	tail.release()
	first := admissionAwaitHTTP(t, firstResult)
	if first.err != nil {
		t.Fatal(first.err)
	}
	body, err := io.ReadAll(first.response.Body)
	if err != nil || first.response.StatusCode != http.StatusOK {
		t.Fatalf("retry completion: HTTP %d %s %v", first.response.StatusCode, body, err)
	}
	finishOnce.Do(func() { close(finishSecond) })
	second := admissionAwaitHTTP(t, secondResult)
	if second.err != nil {
		t.Fatal(second.err)
	}
	body, err = io.ReadAll(second.response.Body)
	if err != nil || second.response.StatusCode != http.StatusOK {
		t.Fatalf("successor completion: HTTP %d %s %v", second.response.StatusCode, body, err)
	}
	admissionAwait(t, "safe retry and successor returning all resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	if f.posts.Load() != 3 {
		t.Fatalf("expected one safe retry and one successor, got %d upstream POSTs", f.posts.Load())
	}
	admissionAssertTwoSettled(t, f, []string{first.response.Header.Get("X-Request-ID"), second.response.Header.Get("X-Request-ID")}, []int64{2, 1})
}
