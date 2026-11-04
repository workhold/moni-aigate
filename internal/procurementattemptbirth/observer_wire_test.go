package procurementattemptbirth_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This tracer is a passive test oracle. It stores SQL templates and protocol
// stages only, never bind arguments, cancellation secrets or business payloads.
// It runs no SQL or callbacks that mutate the connection.
type abObserverQuery struct {
	sql           string
	pid           uint32
	before, after byte
	deadline      time.Time
	ended         bool
}

type abObserverTraceState struct {
	acquires, releases int
	pid                uint32
	acquireDeadline    time.Time
	releaseStatus      byte
	queries            []abObserverQuery
}

type abObserverTrace struct {
	mu     sync.Mutex
	active bool
	state  abObserverTraceState
}

type abObserverQueryKey struct{}

func (a *abObserverTrace) start() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active, a.state = true, abObserverTraceState{}
}

func (a *abObserverTrace) finish() abObserverTraceState {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active = false
	s := a.state
	s.queries = append([]abObserverQuery(nil), s.queries...)
	return s
}

func (a *abObserverTrace) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active {
		a.state.acquires++
		a.state.acquireDeadline, _ = ctx.Deadline()
	}
	return ctx
}

func (a *abObserverTrace) TraceAcquireEnd(_ context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active && data.Conn != nil {
		a.state.pid = data.Conn.PgConn().PID()
	}
}

func (a *abObserverTrace) TraceRelease(_ *pgxpool.Pool, data pgxpool.TraceReleaseData) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active {
		a.state.releases++
		a.state.releaseStatus = data.Conn.PgConn().TxStatus()
	}
}

func (a *abObserverTrace) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return ctx
	}
	deadline, _ := ctx.Deadline()
	index := len(a.state.queries)
	a.state.queries = append(a.state.queries, abObserverQuery{sql: data.SQL, pid: conn.PgConn().PID(), before: conn.PgConn().TxStatus(), deadline: deadline})
	return context.WithValue(ctx, abObserverQueryKey{}, index)
}

func (a *abObserverTrace) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, _ pgx.TraceQueryEndData) {
	a.mu.Lock()
	defer a.mu.Unlock()
	index, ok := ctx.Value(abObserverQueryKey{}).(int)
	if a.active && ok && index >= 0 && index < len(a.state.queries) {
		a.state.queries[index].after, a.state.queries[index].ended = conn.PgConn().TxStatus(), true
	}
}

// Clone the frozen actual APP connection configuration. Existing executor wire
// and fixture files stay unchanged; this only adds a passive observer tracer.
func abObserverUnknownPool(t *testing.T, f *fixture) (*pgxpool.Pool, *abExecutorWire, uint32, *abExecutorDialAudit, *abObserverTrace) {
	t.Helper()
	cfg := f.app.Config()
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != f.b.App || c.TLSConfig != nil || len(c.Fallbacks) != 0 || c.DialFunc == nil || c.Tracer != nil {
		t.Fatal("observer acceptance requires unchanged plain-loopback actual APP config without existing tracer")
	}
	trace := new(abObserverTrace)
	c.Tracer = trace
	cfg.MaxConns, cfg.MinConns = 1, 0
	dial := c.DialFunc
	audit := new(abExecutorDialAudit)
	var mu sync.Mutex
	var wires []*abExecutorWire
	c.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		audit.mu.Lock()
		audit.state.dials++
		audit.mu.Unlock()
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		peer, ok := conn.RemoteAddr().(*net.TCPAddr)
		if !ok || !peer.IP.Equal(net.ParseIP("127.0.0.1")) || peer.Port != 55439 {
			_ = conn.Close()
			return nil, errors.New("observer wire peer outside dedicated database")
		}
		w := &abExecutorWire{Conn: &abExecutorDialConn{Conn: conn, audit: audit}}
		mu.Lock()
		wires = append(wires, w)
		mu.Unlock()
		return w, nil
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	must(t, "create isolated actual APP observer pool", err)
	t.Cleanup(func() {
		mu.Lock()
		all := append([]*abExecutorWire(nil), wires...)
		mu.Unlock()
		for _, wire := range all {
			_ = wire.Close()
		}
		pool.Close()
	})
	conn, err := pool.Acquire(f.ctx)
	must(t, "acquire authenticated target observer socket", err)
	defer conn.Release()
	var session, current, isolation, lock, statement string
	var pid uint32
	must(t, "confirm actual APP identity and unchanged RC3s5s", conn.QueryRow(f.ctx, "SELECT session_user,current_user,pg_backend_pid(),current_setting('transaction_isolation'),current_setting('lock_timeout'),current_setting('statement_timeout')").Scan(&session, &current, &pid, &isolation, &lock, &statement))
	if session != f.b.App || current != f.b.App || pid == 0 || isolation != "read committed" || lock != "3s" || statement != "5s" {
		t.Fatal("observer test pool changed actual APP identity or mutation budgets")
	}
	wire, ok := conn.Conn().PgConn().Conn().(*abExecutorWire)
	if !ok {
		t.Fatal("observer target socket is not exact frozen wire wrapper")
	}
	audit.mu.Lock()
	audit.pid, audit.secret, audit.cleanup = pid, conn.Conn().PgConn().SecretKey(), conn.Conn().PgConn().CleanupDone()
	audit.mu.Unlock()
	return pool, wire, pid, audit, trace
}

func abObserverTraceCheck(t *testing.T, state abObserverTraceState, oldPID uint32, started, callerDeadline time.Time, success bool) {
	t.Helper()
	if state.acquires != 1 || state.releases != 1 || state.pid == 0 || state.pid == oldPID || state.releaseStatus != 'I' {
		t.Fatalf("Observe did not use one fresh healthy actual APP acquisition: acquires=%d releases=%d new_backend=%t release_idle=%t", state.acquires, state.releases, state.pid != 0 && state.pid != oldPID, state.releaseStatus == 'I')
	}
	if state.acquireDeadline.IsZero() || state.acquireDeadline.After(started.Add(5*time.Second)) || state.acquireDeadline.After(callerDeadline) || len(state.queries) < 3 {
		t.Fatal("Observe did not retain one bounded work context through fresh transaction")
	}
	for n, q := range state.queries {
		lower := strings.ToLower(strings.TrimSpace(q.sql))
		if q.pid != state.pid || !q.ended {
			t.Fatal("Observe spliced connections or did not finish/drain a query")
		}
		if n == 0 {
			if lower != "begin isolation level repeatable read read only" || q.before != 'I' || q.after != 'T' {
				t.Fatal("Observe did not begin a real fresh RR READ ONLY transaction")
			}
		} else if n == len(state.queries)-1 {
			if lower != "rollback" || q.after != 'I' || q.deadline.IsZero() || q.deadline.After(time.Now().Add(5*time.Second)) {
				t.Fatal("Observe did not end by bounded rollback to idle")
			}
			continue // cleanup owns a separate absolute context
		} else {
			if !strings.HasPrefix(lower, "select ") || q.before != 'T' || (success && q.after != 'T') {
				t.Fatal("Observe issued a mutation or left its RR read-only transaction")
			}
			for _, banned := range []string{" for update", " for share", "for no key update", "pg_advisory", "pp_create_account", "pp_configure_account", "pp_record_attempt", "set role", "pg_temp"} {
				if strings.Contains(lower, banned) {
					t.Fatal("Observe issued forbidden locking/root/role/temporary operation")
				}
			}
		}
		if !q.deadline.Equal(state.acquireDeadline) {
			t.Fatal("Observe reset the shared work deadline per query")
		}
	}
	if !strings.Contains(state.queries[1].sql, "set_config('lock_timeout','3s',true)") || !strings.Contains(state.queries[1].sql, "set_config('statement_timeout','5s',true)") {
		t.Fatal("Observe changed fixed statement/lock settings")
	}
}
