// Package maintenancetrust owns isolated maintenance connections. A Gate proves
// only that its dedicated session acquired the two fixed advisory locks; it
// grants no installation, maintenance, source, or financial authority.
package maintenancetrust

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

var (
	ErrInput         = errors.New("maintenance_input")
	ErrGateBusy      = errors.New("maintenance_gate_busy")
	ErrIdentity      = errors.New("maintenance_identity")
	ErrTimeout       = errors.New("maintenance_timeout")
	ErrExecution     = errors.New("maintenance_execution")
	ErrCleanup       = errors.New("maintenance_cleanup")
	ErrSchema        = errors.New("maintenance_schema")
	ErrTransaction   = errors.New("maintenance_transaction")
	ErrCommitUnknown = errors.New("maintenance_commit_unknown")
)

const (
	acquireBudget             = 30 * time.Second
	pollInterval              = 50 * time.Millisecond
	cleanupBudget             = 5 * time.Second
	instanceKey        uint32 = 782193402
	migrationKey       uint32 = 782193401
	tryInstanceSQL            = `SELECT pg_catalog.pg_try_advisory_lock(782193402::pg_catalog.int8)`
	tryMigrationSQL           = `SELECT pg_catalog.pg_try_advisory_lock(782193401::pg_catalog.int8)`
	unlockInstanceSQL         = `SELECT pg_catalog.pg_advisory_unlock(782193402::pg_catalog.int8)`
	unlockMigrationSQL        = `SELECT pg_catalog.pg_advisory_unlock(782193401::pg_catalog.int8)`
)

// Gate values share a single private lifecycle. The zero value is invalid.
// Acquire is the only constructor. Copies cannot duplicate lock ownership.
type Gate struct{ state *gateState }

type gateState struct {
	mu                          sync.Mutex
	busy                        bool
	beginAttempted              bool
	nonce                       *gateNonce
	snapshot                    *snapshotState
	conn                        *pgx.Conn
	socketMu                    sync.Mutex
	sockets                     []net.Conn
	socketsClosed               bool
	pins                        fundinglotcatalog.Pins
	pid                         uint32
	backendStart                time.Time
	heldInstance, heldMigration bool
	closed                      bool
	closeErr                    error
}

func (Gate) String() string               { return "maintenance_gate_redacted" }
func (Gate) GoString() string             { return "maintenance_gate_redacted" }
func (Gate) MarshalJSON() ([]byte, error) { return []byte(`{"gate":"redacted"}`), nil }

// Acquire creates one dedicated physical connection and acquires instance then
// migration session locks, once each. All connection and query work shares the
// earlier of the caller deadline and the thirty-second entrance budget.
func Acquire(ctx context.Context, cfg *pgx.ConnConfig, pins fundinglotcatalog.Pins) (*Gate, error) {
	if ctx == nil || cfg == nil || !validPins(pins) || !validConfig(cfg, pins) {
		return nil, ErrInput
	}
	if ctx.Err() != nil {
		return nil, ErrTimeout
	}
	work, cancel := context.WithTimeout(ctx, acquireBudget)
	defer cancel()
	copied := cfg.Copy()
	state := &gateState{pins: copyPins(pins), nonce: &gateNonce{}}
	// Capture the dialed socket before pgx can mark an errored connection closed
	// and hand physical closure to its asynchronous (up to 15s) cleanup path.
	dial := copied.DialFunc
	copied.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if conn != nil && !state.registerSocket(conn) {
			return nil, ErrExecution
		}
		return conn, err
	}
	conn, err := pgx.ConnectConfig(work, copied)
	if err != nil {
		state.destroy()
		return nil, executionError(work)
	}
	state.conn = conn
	fail := func(err error) (*Gate, error) { state.destroy(); return nil, err }
	if err = state.preflight(work); err != nil {
		return fail(err)
	}
	if err = state.acquireKey(work, tryInstanceSQL, &state.heldInstance); err != nil {
		return fail(err)
	}
	if err = state.acquireKey(work, tryMigrationSQL, &state.heldMigration); err != nil {
		return fail(err)
	}
	if work.Err() != nil {
		return fail(ErrTimeout)
	}
	return &Gate{state: state}, nil
}

func executionError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ErrTimeout
	}
	return ErrExecution
}

func (s *gateState) idle() bool {
	return s.conn != nil && !s.conn.IsClosed() && !s.conn.PgConn().IsBusy() && s.conn.PgConn().TxStatus() == 'I'
}

func (s *gateState) acquireKey(ctx context.Context, sql string, held *bool) error {
	for {
		if ctx.Err() != nil {
			return ErrTimeout
		}
		if !s.idle() || *held {
			return ErrIdentity
		}
		var acquired bool
		if err := s.conn.QueryRow(ctx, sql, pgx.QueryExecModeSimpleProtocol).Scan(&acquired); err != nil {
			// A missing/error reply never authorizes another try for this key.
			return executionError(ctx)
		}
		if acquired {
			*held = true
			return s.checkLocks(ctx)
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ErrTimeout
		case <-timer.C:
		}
	}
}

// Close always uses its own five-second total cleanup budget, even when ctx is
// nil or cancelled. A concurrent/reentrant operation is rejected immediately.
// Repeated calls and copied Gates return the first cleanup result.
func (g *Gate) Close(_ context.Context) error {
	if g == nil || g.state == nil {
		return ErrInput
	}
	s := g.state
	if !s.mu.TryLock() {
		return ErrGateBusy
	}
	if s.busy {
		s.mu.Unlock()
		return ErrGateBusy
	}
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.busy = true
	s.closed = true
	// An unfinished/invalid snapshot is destroyed, never implicitly committed
	// or rolled back. A confirmed outcome survives a later cleanup failure.
	unfinished := false
	ss := s.snapshot
	if ss != nil {
		unfinished = !ss.terminalDone || ss.outcome != OutcomeCommitted && ss.outcome != OutcomeRolledBack
		if !ss.terminalDone {
			ss.terminal = terminalClose
			ss.terminalDone = true
			ss.invalidErr = ErrTransaction
			ss.installation = nil
			if ss.outcome != OutcomeUnknown {
				ss.outcome = OutcomeAbandoned
			}
		}
		ss.stopWatcherLocked()
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
	if ss != nil {
		ss.waitWatcher()
		ss.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	bad := func() error {
		s.destroyWithContext(ctx)
		s.mu.Lock()
		s.closeErr = ErrCleanup
		s.mu.Unlock()
		return ErrCleanup
	}
	if unfinished || !s.idle() {
		return bad()
	}
	// Validate the complete set before unlocks. Otherwise an extra/shared key or
	// an externally lost key could be hidden by an apparently successful unlock.
	if s.checkLocks(ctx) != nil {
		return bad()
	}
	for _, item := range []struct {
		sql  string
		held *bool
	}{
		{unlockMigrationSQL, &s.heldMigration}, {unlockInstanceSQL, &s.heldInstance},
	} {
		if !*item.held {
			continue
		}
		var unlocked bool
		if err := s.conn.QueryRow(ctx, item.sql, pgx.QueryExecModeSimpleProtocol).Scan(&unlocked); err != nil || !unlocked {
			return bad()
		}
		*item.held = false
	}
	if s.checkLocks(ctx) != nil || !s.idle() {
		return bad()
	}
	if err := s.conn.Close(ctx); err != nil {
		return bad()
	}
	// Close the captured sockets too, including any unsuccessful fallback dials.
	s.closeSockets()
	return nil
}

// pgx's asynchronous CancelRequest may dial through this same copied config
// after a query failure. Seal the registry before cleanup, and immediately
// destroy late arrivals rather than racing or leaving an untracked socket.
func (s *gateState) registerSocket(conn net.Conn) bool {
	s.socketMu.Lock()
	if s.socketsClosed {
		s.socketMu.Unlock()
		_ = conn.Close()
		return false
	}
	s.sockets = append(s.sockets, conn)
	s.socketMu.Unlock()
	return true
}

func (s *gateState) closeSockets() {
	s.socketMu.Lock()
	s.socketsClosed = true
	sockets := s.sockets
	s.sockets = nil
	s.socketMu.Unlock()
	for _, conn := range sockets {
		_ = conn.Close()
	}
}

func (s *gateState) destroy() {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	s.destroyWithContext(ctx)
}

func (s *gateState) destroyWithContext(ctx context.Context) {
	// Do this before pgx.Close: pgx may already consider its handle closed while
	// the server session and its locks still exist on a live physical socket.
	s.closeSockets()
	if s.conn != nil {
		_ = s.conn.Close(ctx)
	}
	s.mu.Lock()
	s.heldInstance, s.heldMigration = false, false
	s.closed = true
	s.mu.Unlock()
}
