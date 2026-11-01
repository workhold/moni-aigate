package maintenancetrust

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

// Permit is authority for this original maintenance Snapshot only. It grants
// no source-completeness, epoch, runtime or financial-write capability. Copies
// share revocation; the zero value is invalid. No raw SQL handle is exposed.
type Permit struct{ state *permitState }

type authorityNonce struct{ marker byte }
type permitNonce struct{ marker byte }

// The single busy owner fills these immutable facts before publication. Only
// active and permitState.revoked may change later, under the lifecycle mutex.
// Watchers revoke facts without clearing fields that the SQL owner is reading.
type authorityStage struct {
	snapshot    *snapshotState
	nonce       *authorityNonce
	call        *snapshotCall
	installed   *fundinglotcatalog.InstallationProof
	manifest    *manifestEvidence
	identity    *maintenanceIdentity
	predecessor *predecessorEvidence
	accounts    *accountsEvidence
	active      bool
}

type permitState struct {
	snapshot      *snapshotState
	authority     *authorityStage
	nonce         *permitNonce
	revoked       bool
	scanAttempted uint8
}

func (Permit) String() string               { return "maintenance_permit_redacted" }
func (Permit) GoString() string             { return "maintenance_permit_redacted" }
func (Permit) MarshalJSON() ([]byte, error) { return []byte(`{"permit":"redacted"}`), nil }

func (st *snapshotState) revokeAuthorityLocked() {
	st.revokeReadLeaseLocked()
	if st.activeAuthority != nil {
		st.activeAuthority.active = false
	}
	st.activeAuthority = nil
	if st.permit != nil {
		st.permit.revoked = true
	}
	st.permit = nil
	st.permitCall = nil
}

// abandonAuthority preserves safe errors and permanently closes the original
// socket, including failures observed in CPU gaps rather than SQL.
func (st *snapshotState) abandonAuthority(err error) error {
	gs := st.gate
	gs.mu.Lock()
	st.invalidateLocked(err)
	st.stopWatcherLocked()
	gs.mu.Unlock()
	gs.closeSockets()
	st.waitWatcher()
	st.cancel()
	return err
}

func authorityError(ctx context.Context, st *snapshotState, fallback error) error {
	if ctx != nil && ctx.Err() != nil || st.ctx.Err() != nil {
		return ErrTimeout
	}
	st.gate.mu.Lock()
	err := st.invalidErr
	st.gate.mu.Unlock()
	if err != nil {
		return err
	}
	return fallback
}

// verify checks actual ownership, not a caller digest. It runs only while the
// original busy owner holds an active Attest or this Permit's Verify call.
func (a *authorityStage) verify(ctx context.Context) error {
	if a == nil || a.snapshot == nil || a.snapshot.gate == nil || a.nonce == nil || a.call == nil || a.installed == nil || ctx == nil {
		return ErrTransaction
	}
	st, gs := a.snapshot, a.snapshot.gate
	gs.mu.Lock()
	call := a.call
	owned := a.active && st.activeAuthority == a
	if !owned && st.permit != nil && !st.permit.revoked && st.permit.authority == a && st.permit.snapshot == st && st.permit.nonce != nil {
		call = st.permitCall
		owned = call != nil
	}
	valid := owned && call != nil && call.state == st && !call.stopped && gs.busy && !gs.closed && gs.snapshot == st && st.gateNonce == gs.nonce && st.nonce != nil && st.authorityAttempted && st.installation == a.installed && st.invalidErr == nil && st.terminal == terminalNone && st.outcome == OutcomeOpen && time.Now().Before(st.deadline) && st.ctx.Err() == nil && ctx.Err() == nil
	if valid {
		valid = call.ctx.Err() == nil && call.caller.Err() == nil
	}
	gs.mu.Unlock()
	if !valid {
		return authorityError(ctx, st, ErrTransaction)
	}
	if st.verifyBinding(ctx) != nil || a.installed.VerifyTx(ctx, st.rawTx) != nil || !a.installed.Catalog().MatchesPins(gs.pins) {
		return authorityError(ctx, st, ErrTransaction)
	}
	return nil
}

// The private reader gets one cumulative sub-budget and the original raw-close
// cancellation watcher. It never releases the enclosing Attest's busy slot.
func (a *authorityStage) beginPredecessor() (*snapshotCall, context.CancelFunc, error) {
	if a == nil || a.snapshot == nil || a.call == nil {
		return nil, nil, ErrTransaction
	}
	if err := a.verify(a.call.ctx); err != nil {
		return nil, nil, err
	}
	st := a.snapshot
	st.gate.mu.Lock()
	active := a.active && st.activeAuthority == a
	st.gate.mu.Unlock()
	if !active {
		return nil, nil, ErrTransaction
	}
	ctx, cancel := context.WithTimeout(a.call.ctx, 5*time.Second)
	call, err := st.stageContext(ctx)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return call, cancel, nil
}

var authorityEmptyTables = [...]string{
	"funding_consumption_refund_lots", "funding_lot_balances", "funding_lot_epoch", "funding_lot_events",
	"funding_lot_legacy_approved_refunds", "funding_lot_legacy_openings", "funding_lot_legacy_receipts", "funding_lot_legacy_sandbox_receipts",
	"funding_lot_legacy_settlements", "funding_lot_runtime_attestations", "funding_lots", "funding_reservation_lots",
	"funding_settlement_lots", "funding_wallet_heads", "funding_wallet_legacy_sources", "funding_wallet_operations",
}

func (a *authorityStage) verifyEmpty(ctx context.Context) error {
	if err := a.verify(ctx); err != nil {
		return err
	}
	for _, table := range authorityEmptyTables {
		var exists bool
		q := "SELECT EXISTS(SELECT 1 FROM ONLY " + pgx.Identifier{a.snapshot.gate.pins.AppSchema.Name, table}.Sanitize() + ")"
		if a.snapshot.rawTx.QueryRow(ctx, q, pgx.QueryExecModeSimpleProtocol).Scan(&exists) != nil || exists {
			return authorityError(ctx, a.snapshot, ErrSchema)
		}
	}
	return nil
}

// Attest issues once, from the Snapshot's owned installation proof and the
// complete fixed authority pipeline. External proof wrappers are never input.
func Attest(ctx context.Context, snapshot *Snapshot) (*Permit, error) {
	if snapshot == nil || snapshot.state == nil || snapshot.state.gate == nil {
		return nil, ErrInput
	}
	st, gs := snapshot.state, snapshot.state.gate
	if !gs.mu.TryLock() {
		return nil, ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return nil, ErrGateBusy
	}
	if gs.closed || st.invalidErr != nil || st.terminal != terminalNone || st.outcome != OutcomeOpen || st.authorityAttempted {
		err := st.invalidErr
		gs.mu.Unlock()
		if err == nil {
			err = ErrTransaction
		}
		return nil, err
	}
	gs.busy, st.authorityAttempted = true, true
	installed := st.installation
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()
	fail := func(err error) (*Permit, error) { return nil, st.abandonAuthority(authorityError(ctx, st, err)) }
	if installed == nil {
		return fail(ErrTransaction)
	}
	call, err := st.stageContext(ctx)
	if err != nil {
		return fail(err)
	}
	defer call.close()
	a := &authorityStage{snapshot: st, nonce: &authorityNonce{}, call: call, installed: installed, active: true}
	gs.mu.Lock()
	st.activeAuthority = a
	gs.mu.Unlock()
	work := call.ctx
	if err = a.verify(work); err != nil {
		return fail(err)
	}
	if a.manifest, err = a.attestManifest(work); err != nil {
		return fail(err)
	}
	if a.identity, err = a.readMaintenanceIdentity(work); err != nil {
		return fail(err)
	}
	if err = a.verifyStopped(work); err != nil {
		return fail(err)
	}
	if err = a.verifyEmpty(work); err != nil {
		return fail(err)
	}
	if a.predecessor, err = readPredecessorAuthority(a); err != nil {
		return fail(err)
	}
	if st.mode == snapshotInit {
		if a.accounts, err = a.lockAccounts(work); err != nil {
			return fail(err)
		}
	}
	if err = a.verifyEvidence(work); err != nil {
		return fail(err)
	}
	gs.mu.Lock()
	if st.invalidErr != nil || work.Err() != nil || ctx.Err() != nil || !a.active || st.activeAuthority != a {
		gs.mu.Unlock()
		return fail(ErrTimeout)
	}
	p := &permitState{snapshot: st, authority: a, nonce: &permitNonce{}}
	st.permit = p
	a.active = false
	st.activeAuthority = nil
	call.stopLocked()
	gs.mu.Unlock()
	return &Permit{state: p}, nil
}

// Called under the single SQL owner both before issuance and during Verify.
// The final stopped sample explicitly clears PostgreSQL's activity cache.
func (a *authorityStage) verifyEvidence(ctx context.Context) error {
	if err := a.verify(ctx); err != nil {
		return err
	}
	if a.manifest == nil || a.manifest.verifyAuthority(a) != nil || a.predecessor == nil || a.predecessor.verifyAuthority(a) != nil || a.identity == nil {
		return ErrTransaction
	}
	if a.snapshot.mode == snapshotInit {
		if a.accounts == nil || a.accounts.verifyAuthority(a) != nil {
			return ErrTransaction
		}
	} else if a.snapshot.mode != snapshotPlan || a.accounts != nil {
		return ErrTransaction
	}
	identity, err := a.readMaintenanceIdentity(ctx)
	if err != nil || identity == nil || *identity != *a.identity {
		return authorityError(ctx, a.snapshot, ErrIdentity)
	}
	if err = a.verify(ctx); err != nil {
		return err
	}
	return a.verifyStopped(ctx)
}

func (p *Permit) Verify(ctx context.Context) error {
	if p == nil || p.state == nil || p.state.snapshot == nil || p.state.snapshot.gate == nil {
		return ErrInput
	}
	ps := p.state
	st, gs := ps.snapshot, ps.snapshot.gate
	if !gs.mu.TryLock() {
		return ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return ErrGateBusy
	}
	if ps.revoked || st.permit != ps || ps.nonce == nil || ps.authority == nil || st.invalidErr != nil || st.terminal != terminalNone || gs.closed || st.outcome != OutcomeOpen {
		gs.mu.Unlock()
		return ErrTransaction
	}
	gs.busy = true
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()
	fail := func(err error) error { return st.abandonAuthority(authorityError(ctx, st, err)) }
	call, err := st.stageContext(ctx)
	if err != nil {
		return fail(err)
	}
	defer call.close()
	gs.mu.Lock()
	st.permitCall = call
	gs.mu.Unlock()
	if err = ps.authority.verifyEvidence(call.ctx); err != nil {
		return fail(err)
	}
	gs.mu.Lock()
	if ps.revoked || st.permit != ps || st.permitCall != call || st.invalidErr != nil || call.ctx.Err() != nil || ctx.Err() != nil {
		gs.mu.Unlock()
		return fail(ErrTimeout)
	}
	call.stopLocked()
	st.permitCall = nil
	gs.mu.Unlock()
	return nil
}
