package maintenancetrust

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

// Snapshot owns one original top-level transaction. Its zero value is invalid;
// copies share both operation ownership and the retained terminal result.
// It exposes no connection, transaction, context, or arbitrary SQL callback.
type Snapshot struct{ state *snapshotState }

// Outcome describes only the observed transaction result, never maintenance
// authority, source completeness, or a business receipt.
type Outcome uint8

const (
	OutcomeInvalid Outcome = iota
	OutcomeOpen
	OutcomeCommitted
	OutcomeRolledBack
	OutcomeAbandoned
	OutcomeUnknown
)

const (
	snapshotDefaultBudget = 30 * time.Second
	snapshotMaximumBudget = 10 * time.Minute
	snapshotMinimumBudget = time.Second
	snapshotLockBudget    = 3 * time.Second
)

type snapshotMode uint8

const (
	snapshotPlan snapshotMode = iota + 1
	snapshotInit
)

type terminalMethod uint8

const (
	terminalNone terminalMethod = iota
	terminalCommit
	terminalRollback
	terminalClose
)

// Nonzero-size tokens are private instance identities, not caller input.
type gateNonce struct{ marker byte }
type snapshotNonce struct{ marker byte }

// All mutable lifecycle fields are protected by gate.mu. The connection and
// rawTx are used only by the holder of gate.busy, never by the watcher. rawTx,
// mode, ctx, deadline and identity pointers do not change after Begin returns.
type snapshotState struct {
	gate               *gateState
	gateNonce          *gateNonce
	nonce              *snapshotNonce
	rawTx              pgx.Tx
	conn               *pgx.Conn
	pgconn             *pgconn.PgConn
	mode               snapshotMode
	ctx                context.Context
	cancel             context.CancelFunc
	deadline           time.Time
	stopWatch          chan struct{}
	watchDone          chan struct{}
	stopOnce           sync.Once
	outcome            Outcome
	invalidErr         error
	catalogUsed        bool
	xid                string
	installation       *fundinglotcatalog.InstallationProof
	authorityAttempted bool
	activeAuthority    *authorityStage
	permit             *permitState
	permitCall         *snapshotCall
	activeLease        *leaseState
	terminal           terminalMethod
	terminalDone       bool
	terminalErr        error
	commitInFlight     bool
}

func (Snapshot) String() string               { return "maintenance_snapshot_redacted" }
func (Snapshot) GoString() string             { return "maintenance_snapshot_redacted" }
func (Snapshot) MarshalJSON() ([]byte, error) { return []byte(`{"snapshot":"redacted"}`), nil }

func (s *Snapshot) Outcome() Outcome {
	if s == nil || s.state == nil || s.state.gate == nil {
		return OutcomeInvalid
	}
	st := s.state
	st.gate.mu.Lock()
	defer st.gate.mu.Unlock()
	return st.outcome
}

// snapshotDeadline is evaluated at Begin entry, before any network operation.
func snapshotDeadline(ctx context.Context, now time.Time) (time.Time, error) {
	if ctx == nil {
		return time.Time{}, ErrInput
	}
	if ctx.Err() != nil {
		return time.Time{}, ErrTimeout
	}
	d := now.Add(snapshotDefaultBudget)
	if parent, ok := ctx.Deadline(); ok {
		d = now.Add(snapshotMaximumBudget)
		if parent.Before(d) {
			d = parent
		}
	}
	if d.Sub(now) < snapshotMinimumBudget {
		return time.Time{}, ErrTimeout
	}
	return d, nil
}

func (g *Gate) BeginPlan(ctx context.Context) (*Snapshot, error) {
	return g.beginSnapshot(ctx, snapshotPlan)
}

func (g *Gate) BeginInit(ctx context.Context) (*Snapshot, error) {
	return g.beginSnapshot(ctx, snapshotInit)
}

func (g *Gate) beginSnapshot(ctx context.Context, mode snapshotMode) (*Snapshot, error) {
	now := time.Now()
	if g == nil || g.state == nil {
		return nil, ErrInput
	}
	gs := g.state
	if !gs.mu.TryLock() {
		return nil, ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return nil, ErrGateBusy
	}
	if gs.closed || gs.beginAttempted {
		gs.mu.Unlock()
		return nil, ErrTransaction
	}
	// Even a bad/cancelled context consumes this Gate's only Begin attempt.
	gs.beginAttempted, gs.busy = true, true
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()

	deadline, err := snapshotDeadline(ctx, now)
	if err != nil {
		gs.destroyFailedBegin()
		return nil, err
	}
	if gs.nonce == nil || !gs.heldInstance || !gs.heldMigration || !gs.idle() {
		gs.destroyFailedBegin()
		return nil, ErrTransaction
	}
	lifetime, cancel := context.WithDeadline(ctx, deadline)
	st := &snapshotState{
		gate: gs, gateNonce: gs.nonce, nonce: &snapshotNonce{}, mode: mode,
		conn: gs.conn, pgconn: gs.conn.PgConn(), ctx: lifetime, cancel: cancel,
		deadline: deadline, stopWatch: make(chan struct{}), watchDone: make(chan struct{}), outcome: OutcomeOpen,
	}
	gs.mu.Lock()
	gs.snapshot = st
	gs.mu.Unlock()
	go st.watch()
	fail := func(err error) (*Snapshot, error) {
		gs.mu.Lock()
		st.invalidateLocked(err)
		st.terminal, st.terminalDone, st.terminalErr = terminalClose, true, err
		st.stopWatcherLocked()
		gs.mu.Unlock()
		st.waitWatcher()
		cancel()
		gs.destroyFailedBegin()
		return nil, err
	}
	access := pgx.ReadOnly
	if mode == snapshotInit {
		access = pgx.ReadWrite
	}
	tx, err := gs.conn.BeginTx(lifetime, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: access})
	if err != nil {
		return fail(executionError(lifetime))
	}
	st.rawTx = tx
	// These are utility statements: Init's single complete LOCK must precede
	// the first data-snapshot-producing SELECT, including identity queries.
	if _, err = tx.Exec(lifetime, "SET LOCAL search_path TO "+pgx.Identifier{gs.pins.AppSchema.Name}.Sanitize()+", pg_catalog, pg_temp"); err != nil {
		return fail(executionError(lifetime))
	}
	for _, setting := range []struct {
		name string
		cap  time.Duration
	}{{"lock_timeout", snapshotLockBudget}, {"statement_timeout", snapshotMaximumBudget}} {
		remaining := time.Until(deadline)
		if remaining > setting.cap {
			remaining = setting.cap
		}
		ms := remaining.Milliseconds()
		if lifetime.Err() != nil || ms < 1 {
			return fail(ErrTimeout)
		}
		q := "SET LOCAL " + setting.name + " = '" + strconv.FormatInt(ms, 10) + "ms'"
		if _, err = tx.Exec(lifetime, q); err != nil {
			return fail(executionError(lifetime))
		}
	}
	if mode == snapshotInit {
		if _, err = tx.Exec(lifetime, initLockSQL(gs.pins.AppSchema.Name)); err != nil {
			return fail(executionError(lifetime))
		}
	}
	gs.mu.Lock()
	if lifetime.Err() != nil || st.invalidErr != nil {
		gs.mu.Unlock()
		return fail(ErrTimeout)
	}
	gs.mu.Unlock()
	return &Snapshot{state: st}, nil
}

// watch never calls or examines pgx. Raw socket closure is independent of SQL
// ownership, so an idle snapshot and CPU gaps obey the same absolute deadline.
func (st *snapshotState) watch() {
	defer close(st.watchDone)
	select {
	case <-st.ctx.Done():
		st.gate.mu.Lock()
		closeSocket := !st.terminalDone
		if closeSocket {
			st.invalidateLocked(ErrTimeout)
		}
		st.gate.mu.Unlock()
		if closeSocket {
			st.gate.closeSockets()
		}
	case <-st.stopWatch:
	}
}

func (st *snapshotState) invalidateLocked(err error) {
	if st.invalidErr == nil {
		st.invalidErr = err
	}
	st.installation = nil
	st.revokeAuthorityLocked()
	if !st.commitInFlight && st.outcome == OutcomeOpen {
		st.outcome = OutcomeAbandoned
	}
}

func (st *snapshotState) stopWatcherLocked() { st.stopOnce.Do(func() { close(st.stopWatch) }) }
func (st *snapshotState) waitWatcher()       { <-st.watchDone }

func (gs *gateState) destroyFailedBegin() {
	gs.destroy()
	gs.mu.Lock()
	gs.closeErr = ErrCleanup
	gs.mu.Unlock()
}

// The caller's cancellation is a second invalidation source while this fixed
// stage is active, including SQL gaps. It never touches pgx. stopped and the
// AfterFunc acknowledgement are protected by the same short lifecycle mutex.
type snapshotCall struct {
	state      *snapshotState
	ctx        context.Context
	caller     context.Context
	cancel     context.CancelFunc
	stopCaller func() bool
	done       chan struct{}
	stopped    bool
	prevented  bool
}

// The per-call context can only tighten the private Snapshot lifetime.
func (st *snapshotState) stageContext(caller context.Context) (*snapshotCall, error) {
	if caller == nil {
		return nil, ErrInput
	}
	if st.ctx.Err() != nil || caller.Err() != nil {
		return nil, ErrTimeout
	}
	d := st.deadline
	if cd, ok := caller.Deadline(); ok && cd.Before(d) {
		d = cd
	}
	ctx, cancel := context.WithDeadline(st.ctx, d)
	call := &snapshotCall{state: st, ctx: ctx, caller: caller, cancel: cancel, done: make(chan struct{})}
	call.stopCaller = context.AfterFunc(caller, func() {
		defer close(call.done)
		cancel()
		st.gate.mu.Lock()
		closeSocket := !call.stopped && !st.terminalDone
		if closeSocket {
			st.invalidateLocked(ErrTimeout)
		}
		st.gate.mu.Unlock()
		if closeSocket {
			st.gate.closeSockets()
		}
	})
	return call, nil
}

// stopLocked is part of the successful stage/terminal state transition. No
// callback can invalidate an already finished call after that transition.
func (call *snapshotCall) stopLocked() {
	if !call.stopped {
		call.stopped = true
		call.prevented = call.stopCaller()
	}
}

func (call *snapshotCall) close() {
	call.state.gate.mu.Lock()
	call.stopLocked()
	prevented := call.prevented
	call.state.gate.mu.Unlock()
	if !prevented {
		<-call.done
	}
	call.cancel()
}

// VerifyInstallation is the only T1 evidence stage. It does not issue a Permit
// and never claims that history, sources, or maintenance authority were checked.
func (s *Snapshot) VerifyInstallation(ctx context.Context) (*fundinglotcatalog.InstallationProof, error) {
	if s == nil || s.state == nil || s.state.gate == nil {
		return nil, ErrInput
	}
	st, gs := s.state, s.state.gate
	if !gs.mu.TryLock() {
		return nil, ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return nil, ErrGateBusy
	}
	if gs.closed || st.terminal != terminalNone || st.invalidErr != nil || st.catalogUsed || st.outcome != OutcomeOpen {
		err := st.invalidErr
		gs.mu.Unlock()
		if err == nil {
			err = ErrTransaction
		}
		return nil, err
	}
	gs.busy, st.catalogUsed = true, true
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()
	fail := func(err error) (*fundinglotcatalog.InstallationProof, error) {
		gs.mu.Lock()
		st.invalidateLocked(err)
		st.stopWatcherLocked()
		gs.mu.Unlock()
		gs.closeSockets()
		st.waitWatcher()
		st.cancel()
		return nil, err
	}
	call, err := st.stageContext(ctx)
	if err != nil {
		return fail(err)
	}
	defer call.close()
	work := call.ctx
	catalogFailure := func() (*fundinglotcatalog.InstallationProof, error) {
		if work.Err() != nil || st.ctx.Err() != nil || ctx.Err() != nil {
			return fail(ErrTimeout)
		}
		return fail(ErrSchema)
	}
	if st.verifyBinding(work) != nil {
		return catalogFailure()
	}
	proof, err := fundinglotcatalog.VerifyInstallationTx(work, st.rawTx, gs.pins)
	if err != nil || proof == nil || proof.VerifyTx(work, st.rawTx) != nil || !proof.Catalog().MatchesPins(gs.pins) || st.verifyBinding(work) != nil {
		return catalogFailure()
	}
	gs.mu.Lock()
	if work.Err() != nil || ctx.Err() != nil || st.invalidErr != nil {
		gs.mu.Unlock()
		return fail(ErrTimeout)
	}
	owned, returned := *proof, *proof
	st.installation = &owned
	call.stopLocked()
	gs.mu.Unlock()
	return &returned, nil
}

const snapshotIdentitySQL = `SELECT pg_catalog.pg_backend_pid(),a.backend_start,
 pg_catalog.pg_current_xact_id()::pg_catalog.text,
 pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')
 FROM pg_catalog.pg_stat_activity a WHERE a.pid OPERATOR(pg_catalog.=) pg_catalog.pg_backend_pid()`

// Called only by the current stage owner, and only after Init's complete LOCK.
func (st *snapshotState) verifyBinding(ctx context.Context) error {
	gs := st.gate
	if st.rawTx == nil || st.rawTx.Conn() != st.conn || st.conn != gs.conn || st.pgconn != st.conn.PgConn() || gs.snapshot != st || st.gateNonce != gs.nonce || st.nonce == nil ||
		st.mode != snapshotPlan && st.mode != snapshotInit ||
		st.conn.IsClosed() || st.pgconn.IsBusy() || st.pgconn.TxStatus() != 'T' || !gs.heldInstance || !gs.heldMigration {
		return ErrTransaction
	}
	var pid uint32
	var start time.Time
	var xid, isolation, access string
	if st.rawTx.QueryRow(ctx, snapshotIdentitySQL, pgx.QueryExecModeSimpleProtocol).Scan(&pid, &start, &xid, &isolation, &access) != nil {
		return ErrTransaction
	}
	wantAccess := "on"
	if st.mode == snapshotInit {
		wantAccess = "off"
	}
	xidValue, err := strconv.ParseUint(xid, 10, 64)
	gs.mu.Lock()
	previousXID := st.xid
	gs.mu.Unlock()
	if pid != gs.pid || pid != st.pgconn.PID() || !start.Equal(gs.backendStart) || isolation != "repeatable read" || access != wantAccess ||
		err != nil || xidValue == 0 || strconv.FormatUint(xidValue, 10) != xid || previousXID != "" && previousXID != xid {
		return ErrTransaction
	}
	gs.mu.Lock()
	st.xid = xid
	gs.mu.Unlock()
	rows, err := st.rawTx.Query(ctx, locksSQL, pgx.QueryExecModeSimpleProtocol)
	if err != nil {
		return ErrTransaction
	}
	defer rows.Close()
	instance, migration, count := false, false, 0
	for rows.Next() {
		count++
		var db, class, key uint32
		var mode string
		var granted bool
		var sub int16
		if rows.Scan(&db, &mode, &granted, &class, &key, &sub) != nil || count > 2 || db != gs.pins.Database.OID || class != 0 || sub != 1 || mode != "ExclusiveLock" || !granted {
			return ErrTransaction
		}
		switch key {
		case instanceKey:
			if instance {
				return ErrTransaction
			}
			instance = true
		case migrationKey:
			if migration {
				return ErrTransaction
			}
			migration = true
		default:
			return ErrTransaction
		}
	}
	if rows.Err() != nil || !instance || !migration || ctx.Err() != nil {
		return ErrTransaction
	}
	return nil
}

// Commit calls the original raw Commit at most once. A lost reply is retained
// as unknown; this package never retries a generation, init, or COMMIT.
func (s *Snapshot) Commit(ctx context.Context) error {
	return s.terminate(ctx, terminalCommit)
}

// Rollback uses its own bounded cleanup context even when caller is cancelled.
// It cannot convert an invalid/unknown transaction into confirmed rollback.
func (s *Snapshot) Rollback(ctx context.Context) error {
	return s.terminate(ctx, terminalRollback)
}

func (s *Snapshot) terminate(ctx context.Context, method terminalMethod) error {
	if s == nil || s.state == nil || s.state.gate == nil {
		return ErrInput
	}
	st, gs := s.state, s.state.gate
	if !gs.mu.TryLock() {
		return ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return ErrGateBusy
	}
	if st.terminalDone {
		err := ErrTransaction
		if st.terminal == method {
			err = st.terminalErr
		}
		gs.mu.Unlock()
		return err
	}
	if gs.closed || st.terminal != terminalNone || st.rawTx == nil {
		gs.mu.Unlock()
		return ErrTransaction
	}
	gs.busy = true
	st.terminal, st.installation = method, nil
	st.revokeAuthorityLocked()
	invalid := st.invalidErr
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()
	var call *snapshotCall
	finish := func(outcome Outcome, err error, closeSocket bool) error {
		gs.mu.Lock()
		st.commitInFlight = false
		st.outcome, st.terminalErr, st.terminalDone = outcome, err, true
		st.stopWatcherLocked()
		if call != nil {
			call.stopLocked()
		}
		gs.mu.Unlock()
		if closeSocket {
			gs.closeSockets()
		}
		st.waitWatcher()
		st.cancel()
		return err
	}
	if invalid != nil || st.ctx.Err() != nil {
		err := ErrCleanup
		if method == terminalCommit {
			err = ErrTransaction
			if invalid == ErrTimeout || invalid == nil && st.ctx.Err() != nil {
				err = ErrTimeout
			}
		}
		return finish(OutcomeAbandoned, err, true)
	}
	if method == terminalRollback {
		work, cancel := context.WithTimeout(context.Background(), cleanupBudget)
		defer cancel()
		err := st.rawTx.Rollback(work)
		if err != nil || st.pgconn.TxStatus() != 'I' {
			return finish(OutcomeAbandoned, ErrCleanup, true)
		}
		return finish(OutcomeRolledBack, nil, false)
	}
	var err error
	call, err = st.stageContext(ctx)
	if err != nil {
		return finish(OutcomeAbandoned, err, true)
	}
	defer call.close()
	work := call.ctx
	// The last check and revocation are atomic with lifecycle observation. Once
	// the single raw Commit invocation starts, errors conservatively mean sent.
	gs.mu.Lock()
	if st.invalidErr != nil || work.Err() != nil || ctx.Err() != nil {
		gs.mu.Unlock()
		return finish(OutcomeAbandoned, ErrTimeout, true)
	}
	st.commitInFlight = true
	gs.mu.Unlock()
	err = st.rawTx.Commit(work)
	// pgx v5.7.6 returns an actual PgError only after draining ReadyForQuery;
	// a receive failure replaces it with the transport error. Require the idle
	// state on that original connection as well, never TxStatus alone.
	idle := !st.conn.IsClosed() && !st.pgconn.IsBusy() && st.pgconn.TxStatus() == 'I'
	outcome, safe := commitResult(err, idle)
	return finish(outcome, safe, outcome == OutcomeUnknown)
}

func commitResult(err error, readyIdle bool) (Outcome, error) {
	if err == nil {
		return OutcomeCommitted, nil
	}
	if errors.Is(err, pgx.ErrTxCommitRollback) {
		return OutcomeRolledBack, ErrTransaction
	}
	if _, ok := err.(*pgconn.PgError); ok && readyIdle {
		return OutcomeRolledBack, ErrTransaction
	}
	return OutcomeUnknown, ErrCommitUnknown
}
