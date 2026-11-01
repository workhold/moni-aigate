package operations

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The probe connects to real PostgreSQL. It forwards bytes unchanged and can
// delay a Ping reply on the test side of a net.Pipe. Driver context deadlines
// still interrupt the real client Read, so cancellation/Close are exercised
// without a fake Ping or a production test hook.
type instanceWireProbe struct {
	mu      sync.Mutex
	enabled bool
	count   int
	current chan struct{}
	gates   map[int]chan struct{}
	starts  chan int
	blocked chan int
	conns   []*instanceProbeConn
}

type instanceProbeConn struct {
	net.Conn
	server, upstream net.Conn
	probe            *instanceWireProbe
	quit, done       chan struct{}
	once             sync.Once
	workers          atomic.Int32
}

func (c *instanceProbeConn) Close() error {
	c.once.Do(func() {
		close(c.quit)
		_ = c.Conn.Close()
		_ = c.server.Close()
		_ = c.upstream.Close()
	})
	return nil
}

func (c *instanceProbeConn) Write(p []byte) (int, error) {
	if len(p) > 5 && p[0] == 'Q' && bytes.Contains(p, []byte("-- ping")) {
		c.probe.mu.Lock()
		if c.probe.enabled {
			c.probe.count++
			c.probe.current = c.probe.gates[c.probe.count]
			c.probe.starts <- c.probe.count
		}
		c.probe.mu.Unlock()
	}
	return c.Conn.Write(p)
}

func (c *instanceProbeConn) finished() {
	_ = c.Close()
	if c.workers.Add(-1) == 0 {
		close(c.done)
	}
}

func newInstanceWirePool(t *testing.T) (*pgxpool.Pool, *pgxpool.Pool, *instanceWireProbe) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for real PostgreSQL instance batching")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	peer, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	probe := &instanceWireProbe{gates: map[int]chan struct{}{}, starts: make(chan int, 256), blocked: make(chan int, 256)}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		upstream, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		client, server := net.Pipe()
		conn := &instanceProbeConn{Conn: client, server: server, upstream: upstream, probe: probe, quit: make(chan struct{}), done: make(chan struct{})}
		conn.workers.Store(2)
		probe.mu.Lock()
		probe.conns = append(probe.conns, conn)
		probe.mu.Unlock()
		go func() {
			defer conn.finished()
			_, _ = io.Copy(upstream, server)
		}()
		go func() {
			defer conn.finished()
			buf := make([]byte, 32<<10)
			for {
				n, readErr := upstream.Read(buf)
				if n > 0 {
					probe.mu.Lock()
					gate, sequence := probe.current, probe.count
					probe.mu.Unlock()
					if gate != nil {
						select {
						case probe.blocked <- sequence:
						default:
						}
						select {
						case <-gate:
						case <-conn.quit:
							return
						}
					}
					if _, err := server.Write(buf[:n]); err != nil {
						return
					}
				}
				if readErr != nil {
					return
				}
			}
		}()
		return conn, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		probe.mu.Lock()
		connections := append([]*instanceProbeConn(nil), probe.conns...)
		probe.mu.Unlock()
		for _, conn := range connections {
			_ = conn.Close()
		}
		pool.Close()
		peer.Close()
		for _, conn := range connections {
			select {
			case <-conn.done:
			case <-time.After(2 * time.Second):
				t.Error("test wire forwarding goroutine leaked")
			}
		}
	})
	return pool, peer, probe
}

func (p *instanceWireProbe) enable(gates int) []chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]chan struct{}, gates)
	for n := range gates {
		result[n] = make(chan struct{})
		p.gates[n+1] = result[n]
	}
	p.enabled = true
	return result
}

func waitInstancePing(t *testing.T, probe *instanceWireProbe, want int) {
	t.Helper()
	select {
	case sequence := <-probe.starts:
		if sequence != want {
			t.Fatalf("Ping sequence=%d want=%d", sequence, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Ping %d never started", want)
	}
}

func waitInstancePending(t *testing.T, instance *Instance, want int) {
	t.Helper()
	limit := time.Now().Add(2 * time.Second)
	for {
		instance.mu.Lock()
		pending := len(instance.pending)
		instance.mu.Unlock()
		if pending == want {
			return
		}
		if time.Now().After(limit) {
			t.Fatalf("pending=%d want=%d", pending, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func checkInstanceAsync(instance *Instance, ctx context.Context, count int) <-chan error {
	done := make(chan error, count)
	for range count {
		go func() { done <- instance.Check(ctx) }()
	}
	return done
}

func requireInstanceResults(t *testing.T, done <-chan error, count int, lost bool) {
	t.Helper()
	for range count {
		select {
		case err := <-done:
			if (lost && !errors.Is(err, ErrInstanceLost)) || (!lost && err != nil) {
				t.Fatalf("Check error=%v want lost=%v", err, lost)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Check did not wake")
		}
	}
}

func requireInstanceReleased(t *testing.T, instance *Instance, pool *pgxpool.Pool) {
	t.Helper()
	instance.mu.Lock()
	stopped := instance.conn == nil && !instance.running && instance.workerDone == nil
	instance.mu.Unlock()
	if !stopped {
		t.Fatal("Close returned before the real Ping worker exited")
	}
	// pgxpool destroys closed resources asynchronously after Release. Its
	// statistics may briefly count the destruction task as still acquired.
	deadline := time.Now().Add(2 * time.Second)
	for pool.Stat().AcquiredConns() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("dedicated connection remained acquired after Close")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPostgresInstanceBatchesOnlyChecksRegisteredBeforePing(t *testing.T) {
	pool, _, probe := newInstanceWirePool(t)
	instance, err := AcquireInstance(context.Background(), pool, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	gates := probe.enable(3)
	first := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	queued := checkInstanceAsync(instance, context.Background(), 24)
	waitInstancePending(t, instance, 24)
	close(gates[0])
	requireInstanceResults(t, first, 1, false)
	waitInstancePing(t, probe, 2)
	select {
	case err := <-queued:
		t.Fatalf("late caller reused prior Ping: %v", err)
	default:
	}
	late := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePending(t, instance, 1)
	close(gates[1])
	requireInstanceResults(t, queued, 24, false)
	waitInstancePing(t, probe, 3)
	select {
	case err := <-late:
		t.Fatalf("caller registered during Ping 2 reused it: %v", err)
	default:
	}
	close(gates[2])
	requireInstanceResults(t, late, 1, false)
	if err = instance.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitInstancePing(t, probe, 4)
	probe.mu.Lock()
	count := probe.count
	probe.mu.Unlock()
	if count != 4 {
		t.Fatalf("26 concurrent checks plus later check used %d Pings, want 4", count)
	}
}

func TestPostgresInstanceBatchTimeoutFencesAllWaiters(t *testing.T) {
	pool, _, probe := newInstanceWirePool(t)
	instance, err := AcquireInstance(context.Background(), pool, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	gates := probe.enable(2)
	first := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	timed := checkInstanceAsync(instance, short, 1)
	peers := checkInstanceAsync(instance, context.Background(), 10)
	waitInstancePending(t, instance, 11)
	close(gates[0])
	requireInstanceResults(t, first, 1, false)
	waitInstancePing(t, probe, 2)
	next := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePending(t, instance, 1)
	requireInstanceResults(t, timed, 1, true)
	requireInstanceResults(t, peers, 10, true)
	requireInstanceResults(t, next, 1, true)
	if !errors.Is(instance.Check(context.Background()), ErrInstanceLost) {
		t.Fatal("failed batch reconnected")
	}
	instance.Close()
	requireInstanceReleased(t, instance, pool)
}

func TestPostgresInstanceQueuedCancellationFencesInFlightBatch(t *testing.T) {
	pool, _, probe := newInstanceWirePool(t)
	instance, err := AcquireInstance(context.Background(), pool, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	probe.enable(1)
	first := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := checkInstanceAsync(instance, ctx, 1)
	peers := checkInstanceAsync(instance, context.Background(), 8)
	waitInstancePending(t, instance, 9)
	cancel()
	requireInstanceResults(t, cancelled, 1, true)
	requireInstanceResults(t, peers, 8, true)
	requireInstanceResults(t, first, 1, true)
	instance.Close()
	requireInstanceReleased(t, instance, pool)
	probe.mu.Lock()
	count := probe.count
	probe.mu.Unlock()
	if count != 1 {
		t.Fatalf("cancelled pending batch started another Ping: %d", count)
	}
}

func TestPostgresInstanceBackgroundCheckHasBoundedPing(t *testing.T) {
	pool, _, probe := newInstanceWirePool(t)
	instance, err := AcquireInstance(context.Background(), pool, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	probe.enable(1)
	started := time.Now()
	done := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	requireInstanceResults(t, done, 1, true)
	// This is a bounded-failure check, not a latency benchmark. Give CI
	// scheduling slack while asserting a Background caller cannot hang.
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("background Ping failure elapsed=%s", elapsed)
	}
	instance.Close()
	requireInstanceReleased(t, instance, pool)
}

func TestPostgresInstanceCloseCancelsWorkerAndWaitsForConnectionExit(t *testing.T) {
	pool, peer, probe := newInstanceWirePool(t)
	key := int64(rand.Uint64() >> 1)
	instance, err := AcquireInstance(context.Background(), pool, key)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	probe.enable(1)
	first := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	queued := checkInstanceAsync(instance, context.Background(), 8)
	waitInstancePending(t, instance, 8)
	closed := make(chan struct{}, 4)
	for range 4 {
		go func() {
			instance.Close()
			closed <- struct{}{}
		}()
	}
	requireInstanceResults(t, first, 1, true)
	requireInstanceResults(t, queued, 8, true)
	for range 4 {
		select {
		case <-closed:
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent Close deadlocked")
		}
	}
	requireInstanceReleased(t, instance, pool)
	next, err := AcquireInstance(context.Background(), peer, key)
	if err != nil {
		t.Fatal("Close retained advisory ownership", err)
	}
	next.Close()
	if !errors.Is(instance.Check(context.Background()), ErrInstanceLost) {
		t.Fatal("closed instance admitted check")
	}
}

func TestPostgresInstanceCancelledBeforePingDoesNotReturnLockedSessionToPool(t *testing.T) {
	pool, peer, probe := newInstanceWirePool(t)
	key := int64(rand.Uint64() >> 1)
	instance, err := AcquireInstance(context.Background(), pool, key)
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	probe.enable(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(instance.Check(ctx), ErrInstanceLost) {
		t.Fatal("cancelled check admitted")
	}
	instance.Close()
	requireInstanceReleased(t, instance, pool)
	probe.mu.Lock()
	pings := probe.count
	probe.mu.Unlock()
	if pings != 0 {
		t.Fatalf("pre-I/O cancellation sent %d Pings", pings)
	}
	next, err := AcquireInstance(context.Background(), peer, key)
	if err != nil {
		t.Fatal("lost but live session retained advisory lock", err)
	}
	next.Close()
}

func TestPostgresInstanceLateBatchCannotUseReplyFromTerminatedSession(t *testing.T) {
	pool, peer, probe := newInstanceWirePool(t)
	instance, err := AcquireInstance(context.Background(), pool, int64(rand.Uint64()>>1))
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	pid := instance.conn.Conn().PgConn().PID()
	gates := probe.enable(1)
	first := checkInstanceAsync(instance, context.Background(), 1)
	waitInstancePing(t, probe, 1)
	// Wait until an actual first Ping reply is buffered in the proxy. Its
	// success evidence predates the late callers and backend termination.
	select {
	case sequence := <-probe.blocked:
		if sequence != 1 {
			t.Fatal(sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first real Ping reply was not buffered")
	}
	queued := checkInstanceAsync(instance, context.Background(), 12)
	waitInstancePending(t, instance, 12)
	if _, err = peer.Exec(context.Background(), "SELECT pg_terminate_backend($1)", pid); err != nil {
		t.Fatal(err)
	}
	close(gates[0])
	// The first call may have observed success before the session failed;
	// callers registered later must all wait for fresh evidence and fail.
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("first check did not finish")
	}
	requireInstanceResults(t, queued, 12, true)
	if !errors.Is(instance.Check(context.Background()), ErrInstanceLost) {
		t.Fatal("terminated session reconnected")
	}
}
