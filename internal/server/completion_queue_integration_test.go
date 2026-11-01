package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
)

// Final usage is withheld until the test owns the same-Key admission lane.
// The real provider socket then completes before the server queues cleanup.
func completionStreamFixture(t *testing.T) (*gatewayFixture, func(), <-chan struct{}) {
	t.Helper()
	finish := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	f := newGatewayFixtureWithPool(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		gatewayValidateUpstream(t, r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", gatewayContent)
		w.(http.Flusher).Flush()
		select {
		case <-finish:
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", gatewayFinish, gatewayUsage)
		case <-r.Context().Done():
		}
	}}, nil, 1)
	release := func() { once.Do(func() { close(finish) }) }
	t.Cleanup(release)
	return f, release, finished
}

// Capture the actual incoming server context to observe cancellation. An
// optional execution deadline also lets recovery use real elapsed PostgreSQL
// time without editing any persisted request or financial deadline.
func completionObservedServer(t *testing.T, f *gatewayFixture, executionLimit time.Duration) (*httptest.Server, <-chan context.Context) {
	t.Helper()
	observed := make(chan context.Context, 1)
	handler := f.http.Config.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if executionLimit > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), executionLimit)
			defer cancel()
			r = r.WithContext(ctx)
		}
		observed <- r.Context()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server, observed
}

func completionStartStream(t *testing.T, f *gatewayFixture, target *httptest.Server, ctx context.Context) (*http.Response, *bufio.Reader) {
	t.Helper()
	request := admissionHTTPRequest(t, f, ctx, true)
	request.URL.Host = target.Listener.Addr().String()
	request.Host = request.URL.Host
	got := admissionAwaitHTTP(t, admissionStartHTTP(target.Client(), request))
	if got.err != nil {
		t.Fatal(got.err)
	}
	reader := bufio.NewReader(got.response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || got.response.StatusCode != http.StatusOK || !strings.Contains(line, "hello") {
		t.Fatalf("initial stream content: HTTP %d %s %v", got.response.StatusCode, line, err)
	}
	return got.response, reader
}

type completionEvidence struct {
	finals, metering, settlements, attempts, completeWrites int64
}

func completionEvidenceFor(t *testing.T, f *gatewayFixture, request string) completionEvidence {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var evidence completionEvidence
	err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM finalization_records WHERE request_id=$1),(SELECT count(*) FROM metering_events WHERE request_id=$1),(SELECT count(*) FROM settlements WHERE request_id=$1),(SELECT count(*) FROM upstream_attempts WHERE request_id=$1),(SELECT count(*) FROM delivery_observations WHERE request_id=$1 AND stage='complete_local_write')`, request).Scan(&evidence.finals, &evidence.metering, &evidence.settlements, &evidence.attempts, &evidence.completeWrites)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func completionAssertUnrecorded(t *testing.T, f *gatewayFixture, request string) {
	t.Helper()
	evidence := completionEvidenceFor(t, f, request)
	state := f.state(t, request)
	if evidence != (completionEvidence{attempts: 1}) || state.billing != "reserved" || state.reservation != "reserved" || state.amount != 140 || state.available != 860 || state.held != 140 || state.keyHeld != 140 || state.spent != 0 || state.usage != "" || f.posts.Load() != 1 {
		t.Fatalf("completion fabricated durable evidence or money: evidence=%+v state=%+v posts=%d", evidence, state, f.posts.Load())
	}
}

func TestGatewayPostgresCompletionQueueCancellationKeepsIndependentCleanup(t *testing.T) {
	f, finishUpstream, upstreamFinished := completionStreamFixture(t)
	routes := admissionRouteResources(t, f)
	server, observed := completionObservedServer(t, f, 0)
	ctx, timeout := context.WithTimeout(context.Background(), 15*time.Second)
	defer timeout()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	response, reader := completionStartStream(t, f, server, requestCtx)
	requestID := response.Header.Get("X-Request-ID")
	var serverCtx context.Context
	select {
	case serverCtx = <-observed:
	case <-ctx.Done():
		t.Fatal("server request context was not observed")
	}
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	finishUpstream()
	select {
	case <-upstreamFinished:
	case <-ctx.Done():
		t.Fatal("upstream did not complete its final usage")
	}
	wantRunning := admissionResourceState{global: 1, tenant: 1, key: 1, inflight: 1, streams: 1}
	admissionAwait(t, "completion waiting without a database connection while retaining execution permits", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2 && f.pool.Stat().AcquiredConns() == 0 && admissionResources(f, routes) == wantRunning
	})
	completionAssertUnrecorded(t, f, requestID)
	// Occupy the only connection, then cancel the actual customer socket.
	// The cleanup ticket must survive that server-side cancellation without
	// borrowing the occupied connection or consuming a new upstream POST.
	connection, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if connection.Conn().PgConn().TxStatus() != 'I' || f.pool.Stat().TotalConns() != 1 {
		t.Fatal("completion waiter retained a database transaction or connection")
	}
	cancelRequest()
	select {
	case <-serverCtx.Done():
	case <-ctx.Done():
		t.Fatal("client cancellation did not reach the live server context")
	}
	rest, readErr := io.ReadAll(reader)
	if !errors.Is(readErr, context.Canceled) || strings.Contains(string(rest), "[DONE]") {
		t.Fatalf("cancelled customer received a successful terminal: %s %v", rest, readErr)
	}
	if admissionQueueDepth(&f.server.admission, f.key) != 2 || admissionResources(f, routes) != wantRunning || f.pool.Stat().AcquiredConns() != 1 || connection.Conn().PgConn().TxStatus() != 'I' {
		t.Fatal("customer cancellation cancelled independent cleanup or leaked execution resources")
	}
	connection.Release()
	completionAssertUnrecorded(t, f, requestID)
	owner()
	admissionAwait(t, "independent cleanup settling and returning all execution resources", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 0 && admissionResources(f, routes) == (admissionResourceState{})
	})
	f.assertSettled(t, requestID, 1)
	evidence := completionEvidenceFor(t, f, requestID)
	if evidence != (completionEvidence{finals: 1, metering: 1, settlements: 1, attempts: 1}) || f.posts.Load() != 1 {
		t.Fatalf("cleanup duplicated evidence/charge or wrote a successful terminal: evidence=%+v posts=%d", evidence, f.posts.Load())
	}
}

func TestGatewayPostgresCompletionQueueRealCleanupTimeoutLeavesUnknownForRecovery(t *testing.T) {
	f, finishUpstream, upstreamFinished := completionStreamFixture(t)
	routes := admissionRouteResources(t, f)
	// Eight real seconds leaves room for the independent five-second cleanup
	// to expire before the execution deadline, so billing_pending is visible.
	// Recovery below waits for this original persisted deadline to elapse.
	server, _ := completionObservedServer(t, f, 8*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response, reader := completionStartStream(t, f, server, ctx)
	requestID := response.Header.Get("X-Request-ID")
	owner, err := f.server.admission.acquire(ctx, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer owner()
	beforeFinal := time.Now()
	finishUpstream()
	select {
	case <-upstreamFinished:
	case <-ctx.Done():
		t.Fatal("upstream did not complete its final usage")
	}
	wantRunning := admissionResourceState{global: 1, tenant: 1, key: 1, inflight: 1, streams: 1}
	admissionAwait(t, "completion waiting before finalization and settlement", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 2 && f.pool.Stat().AcquiredConns() == 0 && admissionResources(f, routes) == wantRunning
	})
	completionAssertUnrecorded(t, f, requestID)
	// This body read waits for the production cleanupContext's real timeout;
	// no injected timeout, sleep, or changed financial clock is involved.
	rest, err := io.ReadAll(reader)
	if err != nil || strings.Contains(string(rest), "[DONE]") || !strings.Contains(string(rest), "billing_pending") {
		t.Fatalf("cleanup timeout emitted a successful or incorrect terminal: %s %v", rest, err)
	}
	if elapsed := time.Since(beforeFinal); elapsed < 5*time.Second {
		t.Fatalf("cleanup did not use the real five-second budget: %s", elapsed)
	}
	admissionAwait(t, "expired cleanup returning its ticket and execution permits", func() bool {
		return admissionQueueDepth(&f.server.admission, f.key) == 1 && admissionResources(f, routes) == (admissionResourceState{}) && f.pool.Stat().AcquiredConns() == 0
	})
	completionAssertUnrecorded(t, f, requestID)
	owner()
	if admissionQueueDepth(&f.server.admission, f.key) != 0 {
		t.Fatal("manual owner left a completion queue after cleanup timed out")
	}
	// RAM-only trusted usage was lost when finalization never obtained its
	// ticket. A fresh worker must use only durable sending evidence, retain
	// the hold for reconciliation, and never manufacture usage or replay.
	admissionAwait(t, "the original request deadline expiring in PostgreSQL", func() bool {
		var expired bool
		if err := f.pool.QueryRow(ctx, "SELECT request_deadline<clock_timestamp() FROM request_records WHERE id=$1", requestID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		return expired
	})
	worker := billing.New(f.pool)
	result, err := worker.Recover(ctx, billing.RecoverInput{Limit: 1})
	if err != nil || result.Processed != 1 || result.Pending != 1 || result.Settled != 0 || result.Released != 0 {
		t.Fatalf("unknown completion recovery: %+v %v", result, err)
	}
	state := f.state(t, requestID)
	evidence := completionEvidenceFor(t, f, requestID)
	if state.billing != "pending_reconciliation" || state.reservation != "pending_reconciliation" || state.available != 860 || state.held != 140 || state.keyHeld != 140 || state.spent != 0 || state.charge != 0 || state.usage != "" || evidence != (completionEvidence{attempts: 1}) || f.posts.Load() != 1 {
		t.Fatalf("recovery fabricated final usage, charged, or replayed: state=%+v evidence=%+v posts=%d", state, evidence, f.posts.Load())
	}
	var reason string
	if err := f.pool.QueryRow(ctx, "SELECT reason FROM reconciliation_cases WHERE request_id=$1", requestID).Scan(&reason); err != nil || reason != "delivery_unknown" {
		t.Fatalf("unknown recovery reason=%s error=%v", reason, err)
	}
	second, err := worker.Recover(ctx, billing.RecoverInput{Limit: 1})
	if err != nil || second.Processed != 0 || f.posts.Load() != 1 {
		t.Fatalf("repeated recovery replayed or ignored its retry schedule: %+v %v posts=%d", second, err, f.posts.Load())
	}
	ledger, err := f.server.Billing.VerifyLedger(ctx, billing.Query{})
	if err != nil || !ledger.Balanced {
		t.Fatalf("unknown completion left an unbalanced ledger: %+v %v", ledger, err)
	}
}
