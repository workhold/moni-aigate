package mockpsp

import (
	"context"
	"encoding/json"
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
