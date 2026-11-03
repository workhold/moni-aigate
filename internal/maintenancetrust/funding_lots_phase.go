package maintenancetrust

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// FundingLotsExpected contains comparison values, never maintenance authority.
type FundingLotsExpected struct {
	EpochID, BasisDigest, ManifestDigest, DescriptorDigest, RuntimeProofDigest string
}
type FundingLotsPlanResult struct {
	Expected                                       FundingLotsExpected
	Metadata                                       fundinglotinit.Metadata
	SourceRows, SourceBytes, BasisRows, BasisBytes uint64
}
type FundingLotsInitResult struct {
	Metadata           fundinglotinit.Metadata
	RuntimeProofDigest string
}

const fundingLotsOnce uint8 = 128
const fundingLotsBudget = 30 * time.Second

type fundingLotsNonce struct{ marker byte }
type fundingLotsStage uint8

const (
	fundingLotsPre fundingLotsStage = iota + 1
	fundingLotsBasis
	fundingLotsDraft
	fundingLotsWriting
	fundingLotsPost
	fundingLotsReady
	fundingLotsCommitting
	fundingLotsDone
)

// The pointer remains installed in Snapshot after failure or completion. It is
// both the single original owner and a permanent public-Commit exclusion latch.
type fundingLotsPhase struct {
	snapshot                                    *snapshotState
	permit                                      *permitState
	authority                                   *authorityStage
	nonce                                       *fundingLotsNonce
	permitNonce                                 *permitNonce
	authorityNonce                              *authorityNonce
	snapshotNonce                               *snapshotNonce
	gateNonce                                   *gateNonce
	call                                        *snapshotCall
	budgetCancel                                context.CancelFunc
	purpose                                     snapshotMode
	stage                                       fundingLotsStage
	revoked, released, operation, readyConsumed bool
	pending                                     *fundingLotsRows
	epochID                                     string
	epochAt                                     fundinglotverify.WalletLotsTime
	facts                                       fundinglotscan.Facts
	prefixFacts                                 fundinglotscan.PrefixFacts
	seed                                        fundinglotverify.LegacySeedRows
	draft                                       fundinglotinit.Plan
	basis                                       fundinglotbasis.Report
	canonical                                   *fundinglotproof.CanonicalV2
	expected                                    FundingLotsExpected
	post                                        fundinglotscan.WalletFacts
	prefixPost                                  fundinglotscan.PrefixWalletFacts
}

func (p *fundingLotsPhase) verify(ctx context.Context) error {
	if p == nil || p.snapshot == nil || p.snapshot.gate == nil || ctx == nil {
		return ErrInput
	}
	gs := p.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	return p.validLocked(ctx)
}
func (p *fundingLotsPhase) validLocked(ctx context.Context) error {
	if ctx == nil {
		return ErrInput
	}
	st, gs := p.snapshot, p.snapshot.gate
	if st.invalidErr != nil {
		return st.invalidErr
	}
	if ctx.Err() != nil || st.ctx.Err() != nil || !time.Now().Before(st.deadline) || p.call != nil && (p.call.ctx.Err() != nil || p.call.caller.Err() != nil) {
		return ErrTimeout
	}
	if p.nonce == nil || p.revoked || p.released || p.call == nil || p.call.stopped || p.stage < fundingLotsPre || p.stage > fundingLotsReady || p.readyConsumed ||
		st.fundingLots != p || st.nonce != p.snapshotNonce || st.gateNonce != p.gateNonce || gs.nonce != p.gateNonce || gs.snapshot != st || gs.closed || !gs.busy ||
		p.permit == nil || p.permit.revoked || p.permit.nonce != p.permitNonce || st.permit != p.permit || st.permitCall != p.call || p.permit.authority != p.authority ||
		p.authority == nil || p.authority.nonce != p.authorityNonce || p.authority.snapshot != st || st.installation != p.authority.installed ||
		st.mode != p.purpose || st.terminal != terminalNone || st.outcome != OutcomeOpen || st.activeLease != nil {
		return ErrTransaction
	}
	return nil
}

func (p *Permit) beginFundingLots(ctx context.Context, mode snapshotMode, epochID string) (*fundingLotsPhase, error) {
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
	if ps.revoked || ps.nonce == nil || ps.authority == nil || ps.authority.nonce == nil || st.permit != ps || st.invalidErr != nil || st.terminal != terminalNone || st.outcome != OutcomeOpen || gs.closed || gs.snapshot != st || st.fundingLots != nil || ps.scanAttempted&fundingLotsOnce != 0 {
		gs.mu.Unlock()
		return nil, ErrTransaction
	}
	ph := &fundingLotsPhase{snapshot: st, permit: ps, authority: ps.authority, nonce: &fundingLotsNonce{}, permitNonce: ps.nonce, authorityNonce: ps.authority.nonce, snapshotNonce: st.nonce, gateNonce: gs.nonce, purpose: mode, stage: fundingLotsPre}
	ps.scanAttempted |= fundingLotsOnce
	gs.busy = true
	st.fundingLots = ph
	gs.mu.Unlock()
	if ctx == nil || mode != st.mode || !fundingLotsUUID(epochID) {
		ph.fail(ErrInput)
		ph.release()
		return nil, ErrInput
	}
	ph.epochID = strings.Clone(epochID)
	budget, cancel := context.WithTimeout(ctx, fundingLotsBudget)
	call, err := st.stageContext(budget)
	if err != nil {
		cancel()
		ph.fail(err)
		ph.release()
		return nil, err
	}
	gs.mu.Lock()
	ph.call, ph.budgetCancel = call, cancel
	st.permitCall = call
	gs.mu.Unlock()
	if err = ph.verify(call.ctx); err != nil {
		ph.fail(err)
		ph.release()
		return nil, err
	}
	return ph, nil
}

func fundingLotsUUID(s string) bool {
	if len(s) != 36 || s == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if s[i] != '-' {
				return false
			}
		} else if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}
func fundingLotsDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := range s {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

func (p *fundingLotsPhase) fail(err error) error {
	// Keep an already recognized resource failure through cancellation cleanup.
	st, gs := p.snapshot, p.snapshot.gate
	gs.mu.Lock()
	if err != ErrResource {
		if st.invalidErr != nil {
			err = st.invalidErr
		} else if st.ctx.Err() != nil || p.call != nil && (p.call.ctx.Err() != nil || p.call.caller.Err() != nil) {
			err = ErrTimeout
		}
	}
	st.invalidateLocked(err)
	st.stopWatcherLocked()
	gs.mu.Unlock()
	gs.closeSockets()
	st.waitWatcher()
	st.cancel()
	return err
}
func (p *fundingLotsPhase) release() {
	if p.call != nil {
		p.call.close()
	}
	if p.budgetCancel != nil {
		p.budgetCancel()
	}
	st, gs := p.snapshot, p.snapshot.gate
	gs.mu.Lock()
	p.released = true
	p.operation = false
	if st.permitCall == p.call {
		st.permitCall = nil
	}
	gs.busy = false
	gs.mu.Unlock()
}

func fundingLotsFailure(err error) error {
	if errors.Is(err, fundinglotscan.ErrResource) || errors.Is(err, fundinglotbasis.ErrResource) || errors.Is(err, fundinglotinit.ErrResource) {
		return ErrResource
	}
	if errors.Is(err, fundinglotscan.ErrCanceled) || errors.Is(err, fundinglotscan.ErrTimeout) || errors.Is(err, fundinglotbasis.ErrCanceled) || errors.Is(err, fundinglotbasis.ErrTimeout) || errors.Is(err, fundinglotinit.ErrCanceled) || errors.Is(err, fundinglotinit.ErrTimeout) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	switch err {
	case ErrInput, ErrIdentity, ErrSchema, ErrTransaction, ErrExecution, ErrTimeout, ErrResource, ErrCommitUnknown, ErrCleanup:
		return err
	}
	if errors.Is(err, fundinglotbasis.ErrMetadata) {
		return ErrSchema
	}
	if errors.Is(err, fundinglotinit.ErrInput) || errors.Is(err, fundinglotscan.ErrInput) || errors.Is(err, fundinglotbasis.ErrInput) {
		return ErrTransaction
	}
	return ErrExecution
}
