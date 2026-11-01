package maintenancetrust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSnapshotDeadlineBudgets(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		parent  time.Duration
		want    time.Duration
		wantErr error
	}{
		{"default", 0, 30 * time.Second, nil},
		{"short parent", 2 * time.Second, 2 * time.Second, nil},
		{"explicit longer parent", 2 * time.Minute, 2 * time.Minute, nil},
		{"maximum", time.Hour, 10 * time.Minute, nil},
		{"minimum inclusive", time.Second, time.Second, nil},
		{"below minimum", time.Second - time.Nanosecond, 0, ErrTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.parent != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, now.Add(tc.parent))
				defer cancel()
			}
			d, err := snapshotDeadline(ctx, now)
			if err != tc.wantErr || err == nil && !d.Equal(now.Add(tc.want)) {
				t.Fatal("snapshot deadline did not preserve the frozen budget")
			}
		})
	}
	if _, err := snapshotDeadline(nil, now); err != ErrInput {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := snapshotDeadline(ctx, now); err != ErrTimeout {
		t.Fatal("cancelled context")
	}
}

// This fixture exercises only lifecycle bookkeeping/socket closure. It has no
// pgx transaction and cannot issue a catalog proof or maintenance authority.
func pureSnapshotLifecycle(t *testing.T) (*Snapshot, context.CancelFunc, net.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	d, _ := ctx.Deadline()
	gs := &gateState{nonce: &gateNonce{}}
	st := &snapshotState{
		gate: gs, gateNonce: gs.nonce, nonce: &snapshotNonce{}, mode: snapshotPlan,
		ctx: ctx, cancel: cancel, deadline: d, outcome: OutcomeOpen,
		stopWatch: make(chan struct{}), watchDone: make(chan struct{}),
	}
	gs.snapshot = st
	client, peer := net.Pipe()
	gs.registerSocket(client)
	go st.watch()
	t.Cleanup(func() {
		gs.mu.Lock()
		st.stopWatcherLocked()
		gs.mu.Unlock()
		st.waitWatcher()
		cancel()
		gs.closeSockets()
		_ = peer.Close()
	})
	return &Snapshot{state: st}, cancel, peer
}

func requireSnapshotSocketClosed(t *testing.T, peer net.Conn) {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := peer.Read(b[:]); err != io.EOF {
		t.Fatal("lifecycle cancellation did not physically close the captured socket")
	}
}

func TestSnapshotParentCancellationClosesIdleAndBusySockets(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprint(busy), func(t *testing.T) {
			s, cancel, peer := pureSnapshotLifecycle(t)
			st, gs := s.state, s.state.gate
			gs.mu.Lock()
			gs.busy = busy
			gs.mu.Unlock()
			cancel()
			requireSnapshotSocketClosed(t, peer)
			gs.mu.Lock()
			if st.invalidErr != ErrTimeout || st.outcome != OutcomeAbandoned || gs.busy != busy {
				t.Fatal("watcher did not revoke state independently of its operation owner")
			}
			gs.mu.Unlock()
		})
	}
}

func TestSnapshotStageCancellationClosesSocketDuringCPUGap(t *testing.T) {
	s, _, peer := pureSnapshotLifecycle(t)
	st, gs := s.state, s.state.gate
	gs.mu.Lock()
	gs.busy = true
	gs.mu.Unlock()
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	call, err := st.stageContext(caller)
	if err != nil {
		t.Fatal("stage context")
	}
	defer call.close()
	// No query or pgx driver exists here: only the caller cancellation can
	// invalidate state and close the socket during the operation's CPU gap.
	cancel()
	requireSnapshotSocketClosed(t, peer)
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if st.invalidErr != ErrTimeout || st.outcome != OutcomeAbandoned || !gs.busy {
		t.Fatal("caller watcher altered ownership or failed to invalidate")
	}
}

func TestSnapshotStageContextsOnlyTightenAndHealthyStop(t *testing.T) {
	s, _, _ := pureSnapshotLifecycle(t)
	st := s.state
	for _, short := range []bool{false, true} {
		limit := time.Hour
		if short {
			limit = time.Second
		}
		caller, cancel := context.WithTimeout(context.Background(), limit)
		call, err := st.stageContext(caller)
		if err != nil {
			t.Fatal("stage context")
		}
		got, ok := call.ctx.Deadline()
		want := st.deadline
		if short {
			want, _ = caller.Deadline()
		}
		if !ok || !got.Equal(want) {
			t.Fatal("caller extended snapshot deadline")
		}
		// The successful stage stops its caller watcher in its state transition.
		st.gate.mu.Lock()
		call.stopLocked()
		st.gate.mu.Unlock()
		call.close()
		cancel()
		st.gate.socketMu.Lock()
		socketsClosed := st.gate.socketsClosed
		st.gate.socketMu.Unlock()
		st.gate.mu.Lock()
		if st.invalidErr != nil || st.outcome != OutcomeOpen || socketsClosed {
			t.Fatal("healthy stage cleanup invalidated the snapshot")
		}
		st.gate.mu.Unlock()
	}
	if _, err := st.stageContext(nil); err != ErrInput {
		t.Fatal("nil caller")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.stageContext(cancelled); err != ErrTimeout {
		t.Fatal("cancelled caller")
	}
}

func TestSnapshotWatcherDoesNotGuessCommitOutcome(t *testing.T) {
	s, cancel, peer := pureSnapshotLifecycle(t)
	st := s.state
	st.gate.mu.Lock()
	st.commitInFlight = true
	st.gate.mu.Unlock()
	cancel()
	requireSnapshotSocketClosed(t, peer)
	st.gate.mu.Lock()
	defer st.gate.mu.Unlock()
	if st.invalidErr != ErrTimeout || st.outcome != OutcomeOpen {
		t.Fatal("watcher guessed an in-flight commit result")
	}
	st.commitInFlight = false
	st.outcome = OutcomeCommitted
	st.invalidateLocked(ErrCleanup)
	if st.outcome != OutcomeCommitted {
		t.Fatal("known outcome replaced by cleanup")
	}
}

func TestSnapshotCommitResultRequiresActualReplyEvidence(t *testing.T) {
	server := &pgconn.PgError{Code: "23503", Message: "redacted"}
	for _, tc := range []struct {
		name    string
		err     error
		idle    bool
		outcome Outcome
		safe    error
	}{
		{"success", nil, true, OutcomeCommitted, nil},
		{"rollback tag", pgx.ErrTxCommitRollback, false, OutcomeRolledBack, ErrTransaction},
		{"wrapped rollback tag", fmt.Errorf("redacted: %w", pgx.ErrTxCommitRollback), true, OutcomeRolledBack, ErrTransaction},
		{"server error and ready idle", server, true, OutcomeRolledBack, ErrTransaction},
		{"server error without ready idle", server, false, OutcomeUnknown, ErrCommitUnknown},
		{"wrapped server error", fmt.Errorf("redacted: %w", server), true, OutcomeUnknown, ErrCommitUnknown},
		{"closed handle and idle", pgx.ErrTxClosed, true, OutcomeUnknown, ErrCommitUnknown},
		{"cancelled and idle", context.Canceled, true, OutcomeUnknown, ErrCommitUnknown},
		{"transport error and idle", io.EOF, true, OutcomeUnknown, ErrCommitUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, safe := commitResult(tc.err, tc.idle)
			if outcome != tc.outcome || safe != tc.safe {
				t.Fatal("commit reply misclassified")
			}
		})
	}
}

func TestSnapshotTerminalCopiesRetainFirstResult(t *testing.T) {
	for _, tc := range []struct {
		method  terminalMethod
		outcome Outcome
		err     error
	}{
		{terminalCommit, OutcomeCommitted, nil},
		{terminalCommit, OutcomeRolledBack, ErrTransaction},
		{terminalCommit, OutcomeUnknown, ErrCommitUnknown},
		{terminalRollback, OutcomeRolledBack, nil},
		{terminalRollback, OutcomeAbandoned, ErrCleanup},
	} {
		s, _, _ := pureSnapshotLifecycle(t)
		st := s.state
		st.gate.mu.Lock()
		st.terminal, st.outcome, st.terminalErr, st.terminalDone = tc.method, tc.outcome, tc.err, true
		st.gate.mu.Unlock()
		copied := *s
		var same, opposite error
		if tc.method == terminalCommit {
			same, opposite = copied.Commit(nil), copied.Rollback(nil)
		} else {
			same, opposite = copied.Rollback(nil), copied.Commit(nil)
		}
		if same != tc.err || opposite != ErrTransaction || copied.Outcome() != tc.outcome {
			t.Fatal("copy repeated SQL/changed the retained terminal result")
		}
	}
}

func TestSnapshotInvalidBeginConsumesGateAndRetainsCleanupFailure(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	short, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	for _, tc := range []struct {
		ctx context.Context
		err error
	}{{nil, ErrInput}, {cancelled, ErrTimeout}, {short, ErrTimeout}} {
		g := &Gate{state: &gateState{nonce: &gateNonce{}}}
		if s, err := g.BeginPlan(tc.ctx); s != nil || err != tc.err {
			t.Fatal("bad Begin input")
		}
		if !g.state.beginAttempted {
			t.Fatal("Begin opportunity not consumed")
		}
		if s, err := g.BeginInit(context.Background()); s != nil || err != ErrTransaction {
			t.Fatal("Begin retry accepted")
		}
		if g.Close(nil) != ErrCleanup {
			t.Fatal("failed Begin silently reported graceful cleanup")
		}
	}
}

func TestSnapshotZeroRedactionAndSafeErrors(t *testing.T) {
	var zero Snapshot
	if zero.Outcome() != OutcomeInvalid || (*Snapshot)(nil).Outcome() != OutcomeInvalid {
		t.Fatal("zero outcome")
	}
	if zero.Commit(nil) != ErrInput || zero.Rollback(nil) != ErrInput || (*Snapshot)(nil).Commit(nil) != ErrInput {
		t.Fatal("zero termination")
	}
	if proof, err := zero.VerifyInstallation(nil); proof != nil || err != ErrInput {
		t.Fatal("zero proof")
	}
	for _, value := range []any{zero, &zero} {
		if fmt.Sprint(value) != "maintenance_snapshot_redacted" || fmt.Sprintf("%#v", value) != "maintenance_snapshot_redacted" {
			t.Fatal("snapshot redaction")
		}
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `{"snapshot":"redacted"}` {
			t.Fatal("snapshot json redaction")
		}
	}
	for _, err := range []error{ErrSchema, ErrTransaction, ErrCommitUnknown} {
		if errors.Unwrap(err) != nil {
			t.Fatal("unsafe driver unwrap")
		}
	}
	typ := reflect.TypeOf(zero)
	if typ.NumField() != 1 || typ.Field(0).IsExported() {
		t.Fatal("snapshot fields exposed")
	}
	for _, forbidden := range []string{"Tx", "Conn", "PgConn", "Context", "Begin", "Exec", "Query", "QueryRow"} {
		if _, ok := reflect.TypeOf(&zero).MethodByName(forbidden); ok {
			t.Fatal("snapshot exposes arbitrary transaction access")
		}
	}
}
