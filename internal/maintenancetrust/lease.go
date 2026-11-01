package maintenancetrust

import (
	"context"
	"time"
)

// ScanKind names an audited reader. It never asserts domain coverage.
type ScanKind uint8

const (
	ScanPeriodicBudget ScanKind = iota + 1
	ScanLedger
	ScanFunding
	ScanProcurement
	ScanSandboxSources
)

type leaseNonce struct{ marker byte }
type scanProofNonce struct{ marker byte }

// ReadLease keeps the original Gate busy across SQL, iteration and CPU gaps.
// Its copies share all lifecycle and query ownership. No driver handle escapes.
type ReadLease struct{ state *leaseState }
type ScanProof struct{ state *scanProofState }

type leaseState struct {
	snapshot      *snapshotState
	permit        *permitState
	permitNonce   *permitNonce
	snapshotNonce *snapshotNonce
	gateNonce     *gateNonce
	nonce         *leaseNonce
	kind          ScanKind
	call          *snapshotCall
	budgetCancel  context.CancelFunc
	query         *leaseQuery
	proof         *scanProofState
	opBusy        bool
	revoked       bool
	finished      bool
	released      bool
	cleaning      bool
}
type scanProofState struct {
	lease *leaseState
	nonce *scanProofNonce
}

func (ReadLease) String() string               { return "maintenance_read_lease_redacted" }
func (ReadLease) GoString() string             { return "maintenance_read_lease_redacted" }
func (ReadLease) MarshalJSON() ([]byte, error) { return []byte(`{"read_lease":"redacted"}`), nil }
func (ScanProof) String() string               { return "maintenance_scan_proof_redacted" }
func (ScanProof) GoString() string             { return "maintenance_scan_proof_redacted" }
func (ScanProof) MarshalJSON() ([]byte, error) { return []byte(`{"scan_proof":"redacted"}`), nil }

func validScanKind(kind ScanKind) bool {
	return kind >= ScanPeriodicBudget && kind <= ScanSandboxSources
}

// Called only with gate.mu held. The watcher revokes but never touches pgx,
// clears an in-flight operation, or releases its owner's Gate busy flag.
func (st *snapshotState) revokeReadLeaseLocked() {
	if st.activeLease != nil {
		st.activeLease.revoked = true
	}
}

func (p *Permit) BeginScan(ctx context.Context, kind ScanKind) (*ReadLease, error) {
	if p == nil || p.state == nil || p.state.snapshot == nil || p.state.snapshot.gate == nil {
		return nil, ErrInput
	}
	ps, st := p.state, p.state.snapshot
	gs := st.gate
	if !gs.mu.TryLock() {
		return nil, ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return nil, ErrGateBusy
	}
	if ps.revoked || ps.nonce == nil || ps.authority == nil || st.permit != ps || st.invalidErr != nil || st.terminal != terminalNone || st.outcome != OutcomeOpen || gs.closed || gs.snapshot != st {
		gs.mu.Unlock()
		return nil, ErrTransaction
	}
	if validScanKind(kind) && ps.scanAttempted&(1<<uint(kind-1)) != 0 {
		gs.mu.Unlock()
		return nil, ErrTransaction
	}
	ls := &leaseState{snapshot: st, permit: ps, permitNonce: ps.nonce, snapshotNonce: st.nonce, gateNonce: gs.nonce, nonce: &leaseNonce{}, kind: kind, opBusy: true}
	gs.busy, st.activeLease = true, ls
	if validScanKind(kind) {
		ps.scanAttempted |= 1 << uint(kind-1)
	}
	gs.mu.Unlock()
	fail := func(err error) (*ReadLease, error) {
		err = ls.fail(err)
		ls.endMethod()
		return nil, err
	}
	if !validScanKind(kind) || ctx == nil {
		return fail(ErrInput)
	}
	budget, cancel := context.WithTimeout(ctx, 30*time.Second)
	call, err := st.stageContext(budget)
	if err != nil {
		cancel()
		return fail(err)
	}
	gs.mu.Lock()
	ls.call, ls.budgetCancel = call, cancel
	st.permitCall = call
	gs.mu.Unlock()
	if err = ps.authority.verifyEvidence(call.ctx); err != nil {
		return fail(err)
	}
	gs.mu.Lock()
	err = ls.validLocked()
	if err == nil {
		ls.opBusy = false
	}
	gs.mu.Unlock()
	if err != nil {
		return fail(err)
	}
	return &ReadLease{state: ls}, nil
}

// This predicate performs no SQL and reads only immutable facts or fields
// protected by gate.mu. It is also checked after every driver operation.
func (ls *leaseState) validLocked() error {
	st, gs := ls.snapshot, ls.snapshot.gate
	if st.invalidErr != nil {
		return st.invalidErr
	}
	if ls.finished || ls.released {
		return ErrTransaction
	}
	if st.ctx.Err() != nil || !time.Now().Before(st.deadline) || ls.call != nil && (ls.call.ctx.Err() != nil || ls.call.caller.Err() != nil) {
		return ErrTimeout
	}
	if ls.revoked || ls.finished || ls.released || ls.nonce == nil || ls.call == nil || ls.call.stopped ||
		ls.permit == nil || ls.permit.revoked || ls.permit.nonce != ls.permitNonce || st.permit != ls.permit ||
		st.activeLease != ls || st.permitCall != ls.call || gs.snapshot != st || gs.closed || !gs.busy ||
		st.nonce != ls.snapshotNonce || gs.nonce != ls.gateNonce || st.gateNonce != ls.gateNonce ||
		st.terminal != terminalNone || st.outcome != OutcomeOpen || !validScanKind(ls.kind) {
		return ErrTransaction
	}
	return nil
}

func (ls *leaseState) beginMethod() error {
	gs := ls.snapshot.gate
	if !gs.mu.TryLock() {
		return ErrGateBusy
	}
	if ls.opBusy {
		gs.mu.Unlock()
		return ErrGateBusy
	}
	err := ls.validLocked()
	if err == nil {
		ls.opBusy = true
	}
	cleanupFailure := err != nil && !ls.finished && !ls.released && (ls.revoked || err == ErrTimeout || ls.snapshot.invalidErr != nil)
	gs.mu.Unlock()
	if cleanupFailure {
		err = ls.fail(err)
		gs.mu.Lock()
		clean := ls.claimCleanupLocked()
		gs.mu.Unlock()
		if clean {
			ls.closeCall()
		}
	}
	return err
}

// fail never touches a driver. The current method remains the owner until
// endMethod runs after its pgx call has returned. Concurrent Abort does the same.
func (ls *leaseState) fail(err error) error {
	st, gs := ls.snapshot, ls.snapshot.gate
	gs.mu.Lock()
	if st.invalidErr != nil {
		err = st.invalidErr
	} else if st.ctx.Err() != nil || ls.call != nil && (ls.call.ctx.Err() != nil || ls.call.caller.Err() != nil) {
		err = ErrTimeout
	}
	st.invalidateLocked(err)
	st.stopWatcherLocked()
	gs.mu.Unlock()
	gs.closeSockets()
	st.waitWatcher()
	st.cancel()
	return err
}

// claimCleanupLocked retains busy through cancellation callback acknowledgement.
// Historical driver handles remain inert; a second goroutine never drains them.
func (ls *leaseState) claimCleanupLocked() bool {
	if ls.released || ls.opBusy || ls.cleaning {
		return false
	}
	ls.cleaning, ls.opBusy = true, true
	if ls.call != nil {
		ls.call.stopLocked()
	}
	return true
}

func (ls *leaseState) closeCall() {
	if ls.call != nil {
		ls.call.close()
	}
	if ls.budgetCancel != nil {
		ls.budgetCancel()
	}
	gs, st := ls.snapshot.gate, ls.snapshot
	gs.mu.Lock()
	ls.released, ls.cleaning, ls.opBusy = true, false, false
	if st.activeLease == ls {
		st.activeLease = nil
		if st.permitCall == ls.call {
			st.permitCall = nil
		}
		gs.busy = false
	}
	gs.mu.Unlock()
}

func (ls *leaseState) endMethod() {
	gs := ls.snapshot.gate
	gs.mu.Lock()
	ls.opBusy = false
	clean := false
	if ls.revoked {
		clean = ls.claimCleanupLocked()
	}
	gs.mu.Unlock()
	if clean {
		ls.closeCall()
	}
}

func (l *ReadLease) Require(kind ScanKind) error {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return ErrInput
	}
	ls := l.state
	if err := ls.beginMethod(); err != nil {
		return err
	}
	defer ls.endMethod()
	if kind != ls.kind || !validScanKind(kind) {
		return ls.fail(ErrTransaction)
	}
	return nil
}

func (l *ReadLease) Finish() (*ScanProof, error) {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return nil, ErrInput
	}
	ls := l.state
	if err := ls.beginMethod(); err != nil {
		return nil, err
	}
	defer ls.endMethod()
	gs := ls.snapshot.gate
	gs.mu.Lock()
	pending := ls.query != nil
	gs.mu.Unlock()
	if pending {
		return nil, ls.fail(ErrTransaction)
	}
	if err := ls.permit.authority.verifyEvidence(ls.call.ctx); err != nil {
		return nil, ls.fail(err)
	}
	gs.mu.Lock()
	err := ls.validLocked()
	if err != nil {
		gs.mu.Unlock()
		return nil, ls.fail(err)
	}
	ls.finished = true
	ls.proof = &scanProofState{lease: ls, nonce: &scanProofNonce{}}
	ls.opBusy = false
	clean := ls.claimCleanupLocked()
	proof := &ScanProof{state: ls.proof}
	gs.mu.Unlock()
	if clean {
		ls.closeCall()
	}
	return proof, nil
}

func (l *ReadLease) Abort() error {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return ErrInput
	}
	ls := l.state
	gs := ls.snapshot.gate
	gs.mu.Lock()
	if ls.finished || ls.released || ls.cleaning {
		gs.mu.Unlock()
		return nil
	}
	err := ErrTransaction
	if ls.snapshot.invalidErr != nil {
		err = ls.snapshot.invalidErr
	} else if ls.snapshot.ctx.Err() != nil || ls.call != nil && (ls.call.ctx.Err() != nil || ls.call.caller.Err() != nil) {
		err = ErrTimeout
	}
	ls.snapshot.invalidateLocked(err)
	ls.snapshot.stopWatcherLocked()
	gs.mu.Unlock()
	gs.closeSockets()
	ls.snapshot.waitWatcher()
	ls.snapshot.cancel()
	gs.mu.Lock()
	clean := ls.claimCleanupLocked()
	gs.mu.Unlock()
	if clean {
		ls.closeCall()
	}
	return nil
}

var closedLeaseDone = func() <-chan struct{} { c := make(chan struct{}); close(c); return c }()

func (l *ReadLease) Deadline() (time.Time, bool) {
	if l == nil || l.state == nil || l.state.call == nil {
		return time.Time{}, false
	}
	return l.state.call.ctx.Deadline()
}
func (l *ReadLease) Done() <-chan struct{} {
	if l == nil || l.state == nil || l.state.call == nil {
		return closedLeaseDone
	}
	return l.state.call.ctx.Done()
}
func (l *ReadLease) Err() error {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return ErrInput
	}
	ls := l.state
	ls.snapshot.gate.mu.Lock()
	defer ls.snapshot.gate.mu.Unlock()
	if ls.finished || ls.released {
		return ErrTransaction
	}
	return ls.validLocked()
}

// Verify is deliberately pure: it may run while another lease owns SQL.
// A completed lease's cancelled child context is not the proof lifetime.
func (p *ScanProof) Verify(permit *Permit, kind ScanKind) error {
	if p == nil || p.state == nil || p.state.lease == nil || permit == nil || permit.state == nil {
		return ErrInput
	}
	ls := p.state.lease
	if ls.snapshot == nil || ls.snapshot.gate == nil {
		return ErrInput
	}
	st, gs := ls.snapshot, ls.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if st.ctx.Err() != nil || !time.Now().Before(st.deadline) {
		return ErrTimeout
	}
	// A successor's caller can already be cancelled before its AfterFunc
	// obtains this lock and records global revocation. Inspect only the
	// currently active call, never this normally completed lease's old call.
	if call := st.permitCall; call != nil && !call.stopped && (call.ctx.Err() != nil || call.caller.Err() != nil) {
		return ErrTimeout
	}
	if !validScanKind(kind) || kind != ls.kind || p.state.nonce == nil || p.state != ls.proof || !ls.finished || !ls.released || ls.revoked ||
		permit.state != ls.permit || ls.permit.revoked || ls.permit.nonce != ls.permitNonce || st.permit != ls.permit ||
		ls.nonce == nil || st.nonce != ls.snapshotNonce || gs.nonce != ls.gateNonce || st.gateNonce != ls.gateNonce ||
		gs.snapshot != st || gs.closed || st.invalidErr != nil || st.terminal != terminalNone || st.outcome != OutcomeOpen {
		return ErrTransaction
	}
	return nil
}
