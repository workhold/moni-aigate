package maintenancetrust

import "context"

// commitOwned is reachable only through the synchronous private owner. It
// consumes ready and terminal together and keeps the original call and busy
// slot until the raw COMMIT and cancellation acknowledgements are complete.
func (p *fundingLotsPhase) commitOwned(ctx context.Context) error {
	if err := p.noPending(ctx); err != nil {
		return p.fail(fundingLotsFailure(err))
	}
	gs := p.snapshot.gate
	gs.mu.Lock()
	ready := p.stage == fundingLotsReady && !p.readyConsumed
	gs.mu.Unlock()
	if !ready {
		return p.fail(ErrTransaction)
	}
	if err := p.authority.verify(ctx); err != nil {
		return p.fail(fundingLotsFailure(err))
	}
	st := p.snapshot
	gs.mu.Lock()
	err := p.validLocked(ctx)
	if err == nil && (p.stage != fundingLotsReady || p.pending != nil || p.operation || p.readyConsumed) {
		err = ErrTransaction
	}
	if err != nil {
		gs.mu.Unlock()
		return p.fail(fundingLotsFailure(err))
	}
	p.readyConsumed, p.stage = true, fundingLotsCommitting
	st.terminal, st.installation, st.commitInFlight = terminalCommit, nil, true
	st.revokeAuthorityLocked()
	gs.mu.Unlock()
	err = st.rawTx.Commit(p.call.ctx)
	idle := !st.conn.IsClosed() && !st.pgconn.IsBusy() && st.pgconn.TxStatus() == 'I'
	outcome, safe := commitResult(err, idle)
	gs.mu.Lock()
	st.commitInFlight = false
	st.outcome, st.terminalErr, st.terminalDone = outcome, safe, true
	p.stage = fundingLotsDone
	st.stopWatcherLocked()
	p.call.stopLocked()
	gs.mu.Unlock()
	if outcome == OutcomeUnknown {
		gs.closeSockets()
	}
	st.waitWatcher()
	st.cancel()
	return safe
}
