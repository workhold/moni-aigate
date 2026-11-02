package maintenancetrust

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

type FundingLotsObservationStatus uint8

const (
	FundingLotsObservationInvalid FundingLotsObservationStatus = iota
	FundingLotsObservationMatched
	FundingLotsObservationUninitialized
	FundingLotsObservationDifferentEpoch
)

// FundingLotsObservation is descriptive data, never authority to initialize or
// write funds. It does not identify the transaction that created the epoch.
type FundingLotsObservation struct {
	Status                  FundingLotsObservationStatus
	Observed                FundingLotsExpected
	SourceRows, SourceBytes uint64
}

// Only this synchronous owner uses pgx. The ordinary snapshot watcher may
// invalidate its lifetime and close the captured socket, never issue SQL.
type fundingLotsObservationRun struct {
	snapshot    *snapshotState
	catalog     *fundinglotcatalog.CatalogProof
	manifest    []byte
	identity    *maintenanceIdentity
	predecessor predecessorSnapshotFacts
	pending     *fundingLotsObservationRows
	failure     error
}

// ObserveFundingLots consumes a fresh Gate's only Begin opportunity. It holds
// busy through independent read-only verification, rollback and cleanup; no
// Snapshot, SQL handle, Permit or proof capability is returned to the caller.
func (g *Gate) ObserveFundingLots(ctx context.Context, expected FundingLotsExpected) (FundingLotsObservation, error) {
	now := time.Now()
	if g == nil || g.state == nil {
		return FundingLotsObservation{}, ErrInput
	}
	gs := g.state
	if !gs.mu.TryLock() {
		return FundingLotsObservation{}, ErrGateBusy
	}
	if gs.busy {
		gs.mu.Unlock()
		return FundingLotsObservation{}, ErrGateBusy
	}
	if gs.closed || gs.beginAttempted {
		gs.mu.Unlock()
		return FundingLotsObservation{}, ErrTransaction
	}
	gs.beginAttempted, gs.busy = true, true
	gs.mu.Unlock()
	defer func() { gs.mu.Lock(); gs.busy = false; gs.mu.Unlock() }()
	inputFailure := func(err error) (FundingLotsObservation, error) {
		gs.destroyFailedBegin()
		return FundingLotsObservation{}, err
	}
	if ctx == nil || !fundingLotsExpectedValid(expected) {
		return inputFailure(ErrInput)
	}
	if ctx.Err() != nil {
		return inputFailure(ErrTimeout)
	}
	deadline := now.Add(fundingLotsBudget)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	// Length/format checks precede every retained copy.
	expected = FundingLotsExpected{EpochID: strings.Clone(expected.EpochID), BasisDigest: strings.Clone(expected.BasisDigest),
		ManifestDigest: strings.Clone(expected.ManifestDigest), DescriptorDigest: strings.Clone(expected.DescriptorDigest), RuntimeProofDigest: strings.Clone(expected.RuntimeProofDigest)}
	snapshot, err := gs.beginSnapshotOwned(ctx, snapshotObserve, deadline)
	if err != nil {
		return FundingLotsObservation{}, err
	}
	run := &fundingLotsObservationRun{snapshot: snapshot.state}
	fail := func(err error) (FundingLotsObservation, error) {
		return FundingLotsObservation{}, run.abort(fundingLotsFailure(err))
	}
	work := run.snapshot.ctx
	if err = run.authenticate(work); err != nil {
		return fail(err)
	}
	empty, err := run.empty(work)
	if err != nil {
		return fail(err)
	}
	result := FundingLotsObservation{Status: FundingLotsObservationUninitialized}
	if !empty {
		mode := fundinglotscan.ModeOrdinary
		if gs.pins.Mode == fundinglotcatalog.Sandbox {
			mode = fundinglotscan.ModeSandbox
		}
		facts, readErr := fundinglotscan.ReadWallet(work, run, gs.pins.AppSchema.Name, mode)
		if readErr != nil {
			return fail(fundingLotsReadFailure(readErr, run.failure))
		}
		report := facts.Summary()
		if !fundingLotsPostQualified(report, mode) || run.pending != nil {
			return fail(ErrSchema)
		}
		rows, projectionErr := fundinglotscan.ProjectEpochProof(work, facts)
		if projectionErr != nil {
			return fail(projectionErr)
		}
		actual, proofErr := fundingLotsObservedProof(rows, gs.pins, run.catalog, run.manifest,
			run.predecessor.mode, run.predecessor.canonical, run.predecessor.v1SHA256)
		if proofErr != nil {
			return fail(proofErr)
		}
		result = FundingLotsObservation{Status: FundingLotsObservationMatched, Observed: actual, SourceRows: report.SourceRows, SourceBytes: report.SourceBytes}
		if actual.EpochID != expected.EpochID {
			result.Status = FundingLotsObservationDifferentEpoch
		} else if actual != expected {
			return fail(ErrSchema)
		}
	}
	if err = run.verifyExit(work); err != nil {
		return fail(err)
	}
	if err = run.finish(work); err != nil {
		return fail(err)
	}
	// Watcher is fully acknowledged before the independent cleanup starts.
	cleanup, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	gs.mu.Lock()
	gs.closed = true
	gs.mu.Unlock()
	if err = gs.closeIdleOwned(cleanup); err != nil {
		return FundingLotsObservation{}, err
	}
	if ctx.Err() != nil {
		return FundingLotsObservation{}, ErrTimeout
	}
	return result, nil
}

func fundingLotsExpectedValid(e FundingLotsExpected) bool {
	return fundingLotsUUID(e.EpochID) && fundingLotsDigest(e.BasisDigest) && fundingLotsDigest(e.ManifestDigest) &&
		fundingLotsDigest(e.DescriptorDigest) && fundingLotsDigest(e.RuntimeProofDigest)
}

func (r *fundingLotsObservationRun) check(ctx context.Context) error {
	st, gs := r.snapshot, r.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if st.invalidErr != nil {
		return st.invalidErr
	}
	if ctx == nil {
		return ErrInput
	}
	if ctx.Err() != nil || st.ctx.Err() != nil || !time.Now().Before(st.deadline) {
		return ErrTimeout
	}
	if !gs.busy || gs.closed || !gs.beginAttempted || gs.snapshot != st || st.mode != snapshotObserve || st.terminal != terminalNone ||
		st.outcome != OutcomeOpen || st.gateNonce != gs.nonce || st.nonce == nil || st.installation != nil || st.permit != nil || st.fundingLots != nil || st.activeLease != nil {
		return ErrTransaction
	}
	return nil
}

func (r *fundingLotsObservationRun) authenticate(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	st := r.snapshot
	if st.verifyBinding(ctx) != nil {
		return authorityLeafFailure(ctx, ErrTransaction)
	}
	var err error
	r.catalog, err = fundinglotcatalog.VerifySnapshotTx(ctx, st.rawTx, st.gate.pins)
	if err != nil || r.catalog == nil || !r.catalog.MatchesPins(st.gate.pins) || r.catalog.VerifyTx(ctx, st.rawTx) != nil {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	if r.manifest, err = readMaintenanceManifestSnapshot(ctx, st); err != nil {
		return err
	}
	if r.identity, err = readMaintenanceIdentitySnapshot(ctx, st); err != nil {
		return err
	}
	if err = verifyMaintenanceStoppedSnapshot(ctx, st); err != nil {
		return err
	}
	predecessorCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	call, err := st.stageContext(predecessorCtx)
	if err != nil {
		return err
	}
	defer call.close()
	if r.predecessor, err = readPredecessorSnapshot(call.ctx, st); err != nil {
		return err
	}
	st.gate.mu.Lock()
	valid := st.invalidErr == nil && st.ctx.Err() == nil && call.ctx.Err() == nil && call.caller.Err() == nil
	if valid {
		call.stopLocked()
	}
	st.gate.mu.Unlock()
	if !valid {
		return ErrTimeout
	}
	return r.check(ctx)
}

func (r *fundingLotsObservationRun) empty(ctx context.Context) (bool, error) {
	empty := true
	for _, table := range authorityEmptyTables {
		if err := r.check(ctx); err != nil {
			return false, err
		}
		var exists bool
		q := "SELECT EXISTS(SELECT 1 FROM ONLY " + pgx.Identifier{r.snapshot.gate.pins.AppSchema.Name, table}.Sanitize() + ")"
		if r.snapshot.rawTx.QueryRow(ctx, q, pgx.QueryExecModeSimpleProtocol).Scan(&exists) != nil {
			return false, executionError(ctx)
		}
		empty = empty && !exists
	}
	return empty, nil
}

func (r *fundingLotsObservationRun) verifyExit(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	st := r.snapshot
	if r.pending != nil || st.verifyBinding(ctx) != nil {
		return authorityLeafFailure(ctx, ErrTransaction)
	}
	fresh, err := fundinglotcatalog.VerifySnapshotTx(ctx, st.rawTx, st.gate.pins)
	if err != nil || fresh == nil || fresh.VerifyTx(ctx, st.rawTx) != nil || !fresh.MatchesPins(st.gate.pins) ||
		fresh.DescriptorVersion() != r.catalog.DescriptorVersion() || fresh.ExpectedSHA256() != r.catalog.ExpectedSHA256() ||
		fresh.SchemaSHA256() != r.catalog.SchemaSHA256() || fresh.RolesSHA256() != r.catalog.RolesSHA256() {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	manifest, err := readMaintenanceManifestSnapshot(ctx, st)
	if err != nil || !bytes.Equal(manifest, r.manifest) {
		return authorityLeafFailure(ctx, ErrSchema)
	}
	identity, err := readMaintenanceIdentitySnapshot(ctx, st)
	if err != nil || identity == nil || r.identity == nil || *identity != *r.identity {
		return authorityLeafFailure(ctx, ErrIdentity)
	}
	if err = verifyMaintenanceStoppedSnapshot(ctx, st); err != nil {
		return err
	}
	return r.check(ctx)
}

// The only successful terminal command is the original read-only ROLLBACK.
func (r *fundingLotsObservationRun) finish(ctx context.Context) error {
	if err := r.check(ctx); err != nil {
		return err
	}
	st, gs := r.snapshot, r.snapshot.gate
	gs.mu.Lock()
	if st.invalidErr != nil || ctx.Err() != nil || st.ctx.Err() != nil || !time.Now().Before(st.deadline) || r.pending != nil {
		gs.mu.Unlock()
		return ErrTimeout
	}
	st.terminal = terminalRollback
	gs.mu.Unlock()
	if st.rawTx.Rollback(ctx) != nil || !gs.idle() {
		return executionError(ctx)
	}
	gs.mu.Lock()
	if st.invalidErr != nil || ctx.Err() != nil || st.ctx.Err() != nil || !time.Now().Before(st.deadline) {
		gs.mu.Unlock()
		return ErrTimeout
	}
	st.outcome, st.terminalDone = OutcomeRolledBack, true
	st.stopWatcherLocked()
	gs.mu.Unlock()
	st.waitWatcher()
	st.cancel()
	return nil
}

func (r *fundingLotsObservationRun) abort(err error) error {
	st, gs := r.snapshot, r.snapshot.gate
	gs.mu.Lock()
	if err != ErrResource {
		if st.invalidErr != nil {
			err = st.invalidErr
		} else if st.ctx.Err() != nil {
			err = ErrTimeout
		}
	}
	st.invalidateLocked(err)
	st.terminal, st.terminalDone, st.terminalErr = terminalClose, true, err
	st.stopWatcherLocked()
	gs.mu.Unlock()
	gs.closeSockets()
	st.waitWatcher()
	st.cancel()
	gs.destroyFailedBegin()
	return err
}
