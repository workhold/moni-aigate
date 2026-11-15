package mockpsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

// This deliberately slow QA receiver exercises the real fixed-destination HTTP
// runner. Durable real-receiver validation is covered separately and by the CLI.
func TestPostgresCallbackRunnerTwoSlotsNoLocksDuringHTTP(t *testing.T) {
	r, env, rt, ctx := newRepositoryPG(t)
	// Isolate the network-slot oracle from legal multi-connection Prepare
	// deferral: a stale ClaimDue candidate can briefly hold the target gate
	// after another slot has committed its claim. The production runtime still
	// has its normal pool and dedicated actual ownership/Guard connection.
	// With this one-connection same-LOGIN repository pool, both blocked HTTP
	// requests must release all DB resources for the strict assertions below.
	poolConfig := r.pool.Config().Copy()
	poolConfig.MaxConns, poolConfig.MinConns = 1, 0
	networkTestPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal("network-slot test pool unavailable")
	}
	t.Cleanup(networkTestPool.Close)
	r.pool = networkTestPool
	for i := 0; i < 3; i++ {
		in := repositoryPGCheckout(t, r, ctx, "100")
		if _, err := r.Checkout(ctx, in); err != nil {
			t.Fatal(ErrorCode(err))
		}
		out := repositoryPGControl(t, r, ctx, ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: &in.request.MerchantOrderID})
		if out.Reply.Status != "accepted" {
			t.Fatal("capture rejected")
		}
	}
	u, err := url.Parse(env.ReceiverOrigin)
	if err != nil {
		t.Fatal("callback origin")
	}
	l, err := net.Listen("tcp4", u.Host)
	if err != nil {
		t.Fatal("callback listener")
	}
	keys, err := env.Keyring()
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var active, maxActive atomic.Int32
	var invalid atomic.Bool
	h := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maxActive.Load()
			if n <= old || maxActive.CompareAndSwap(old, n) {
				break
			}
		}
		raw, e := io.ReadAll(io.LimitReader(q.Body, 65537))
		headers, e2 := paymentproto.ReadSignedHeaders(q.Header)
		if e != nil || e2 != nil || q.Method != "POST" || q.URL.Path != "/api/payments/sandbox/notifications" || q.Header.Get("Authorization") != "" || q.Header.Get("Accept-Encoding") != "" {
			invalid.Store(true)
			w.WriteHeader(400)
			return
		}
		verified, e := keys.VerifyNotification(env.Protocol, time.Now().UTC(), headers, raw)
		if e != nil {
			invalid.Store(true)
			w.WriteHeader(401)
			return
		}
		canonical, e := verified.Canonical()
		if e != nil {
			invalid.Store(true)
			w.WriteHeader(400)
			return
		}
		notice, e := paymentproto.ParseNotification(canonical)
		if e != nil {
			invalid.Store(true)
			w.WriteHeader(400)
			return
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-q.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(ReceiptEnvelope{Data: ReceiptData{ReceiptID: uuid.NewString(), EventID: notice.EventID, Status: "received", Environment: "sandbox", ReceivedAt: time.Now().UTC().Format(repoTimeLayout)}, RequestID: uuid.NewString()})
	})}
	served := make(chan struct{})
	go func() { defer close(served); _ = h.Serve(l) }()
	runner, err := newCallbackRunner(&Server{runtime: rt, repository: r})
	if err != nil {
		t.Fatal(err)
	}
	life, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); runner.run(life) }()
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); cancel(); _ = h.Close(); <-served; <-done })
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("two sends did not start")
		}
	}
	var leased, pending, attempts int
	err = r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mock_notification_delivery WHERE state='leased'),(SELECT count(*) FROM mock_notification_delivery WHERE state='pending'),(SELECT count(*) FROM mock_delivery_attempts)`).Scan(&leased, &pending, &attempts)
	if err != nil || leased != 2 || pending != 1 || attempts != 2 {
		t.Fatalf("preclaimed too much: leased=%d pending=%d attempts=%d queryOK=%v", leased, pending, attempts, err == nil)
	}
	var locks int
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.usename=current_user AND l.locktype='advisory' AND l.classid IN (782193410,782193411,782193412,782193413,782193414)`).Scan(&locks)
	if err != nil || locks != 0 {
		t.Fatal("callback held a session/transaction business gate during HTTP")
	}
	// Independent API work remains available while both network slots wait.
	other := repositoryPGCheckout(t, r, ctx, "10")
	if _, err = r.Checkout(ctx, other); err != nil {
		t.Fatal("callback stalled independent command", ErrorCode(err))
	}
	releaseOnce.Do(func() { close(release) })
	until := time.NewTimer(5 * time.Second)
	defer until.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var count int
		if r.pool.QueryRow(ctx, `SELECT count(*) FROM mock_notification_delivery WHERE state='delivered'`).Scan(&count) != nil {
			t.Fatal("delivery query")
		}
		if count == 3 {
			break
		}
		select {
		case <-tick.C:
		case <-until.C:
			t.Fatal("ACKs not durably recorded")
		}
	}
	if invalid.Load() || maxActive.Load() != 2 {
		t.Fatal("fixed transport/signatures or two-slot bound failed")
	}
}

func TestPostgresCallbackRunnerClaimPrepareSerializesSlots(t *testing.T) {
	r, env, _, ctx := newRepositoryPG(t)
	eventID := callbackPGCapture(t, r, ctx)
	armed := repositoryPGControl(t, r, ctx, ControlInput{Action: "arm_fault", Scenario: "before_callback_send_once", EventID: &eventID})
	if armed.Reply.Status != "accepted" {
		t.Fatal("could not arm the fixture-only Prepare boundary")
	}
	owner, err := env.OpenPool(ctx, "mock_owner")
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	holder, err := r.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	holderActive := true
	t.Cleanup(func() {
		if !holderActive {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = holder.Exec(cleanup, `SELECT pg_advisory_unlock(782193999,1)`)
		holder.Release()
	})
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = owner.Exec(cleanup, `DROP TRIGGER IF EXISTS runner_prepare_barrier ON mock_fault_plans;DROP FUNCTION IF EXISTS runner_prepare_barrier()`)
	})
	if _, err = owner.Exec(ctx, `CREATE FUNCTION runner_prepare_barrier() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(782193999,1); RETURN NEW; END $$;CREATE TRIGGER runner_prepare_barrier BEFORE UPDATE ON mock_fault_plans FOR EACH ROW EXECUTE FUNCTION runner_prepare_barrier()`); err != nil {
		t.Fatal(err)
	}
	if _, err = holder.Exec(ctx, `SELECT pg_advisory_lock(782193999,1)`); err != nil {
		t.Fatal(err)
	}
	var holderPID int
	if err = holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	runner := &callbackRunner{server: &Server{repository: r}}
	type result struct {
		claim    DeliveryClaim
		prepared PreparedDelivery
		ok       bool
		err      error
	}
	firstDone := make(chan result, 1)
	go func() {
		claim, prepared, ok, err := runner.claimAndPrepare(ctx)
		firstDone <- result{claim, prepared, ok, err}
	}()
	blocked := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !blocked; {
		if err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory' AND $1::int=ANY(pg_blocking_pids(pid)) AND lower(query) LIKE 'update mock_fault_plans%')`, holderPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if !blocked {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if !blocked {
		t.Fatal("first claim did not reach the actual Prepare barrier")
	}
	if runner.claimPrepareMu.TryLock() {
		runner.claimPrepareMu.Unlock()
		t.Fatal("claim/prepare mutex was released during the actual Prepare barrier")
	}
	if _, err = holder.Exec(ctx, `SELECT pg_advisory_unlock(782193999,1)`); err != nil {
		t.Fatal(err)
	}
	holder.Release()
	holderActive = false
	var first result
	select {
	case first = <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first claim did not resume after the fixture barrier was released")
	}
	if first.claim.eventID != eventID || !errors.Is(first.err, ErrDeliveryDeferred) {
		t.Fatalf("fixture fault did not retain its deferred result: event=%s err=%v", first.claim.eventID, first.err)
	}
	secondDone := make(chan result, 1)
	go func() {
		claim, prepared, ok, err := runner.claimAndPrepare(ctx)
		secondDone <- result{claim, prepared, ok, err}
	}()
	select {
	case second := <-secondDone:
		if second.err != nil || second.ok {
			t.Fatalf("live deferred claim was immediately reused by the second slot: ok=%t err=%v", second.ok, second.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second slot did not finish after the first deferred Prepare")
	}
	var state string
	var generation, attempts, observations int
	if err = r.pool.QueryRow(ctx, `SELECT d.state,d.generation,d.attempt_count,(SELECT count(*) FROM mock_delivery_observations o WHERE o.event_id=d.event_id) FROM mock_notification_delivery d WHERE d.event_id=$1`, first.claim.eventRowID).Scan(&state, &generation, &attempts, &observations); err != nil || state != "leased" || generation != 1 || attempts != 1 || observations != 0 {
		t.Fatalf("serialized deferred facts differ: state=%s generation=%d attempts=%d observations=%d err=%v", state, generation, attempts, observations, err)
	}
}
