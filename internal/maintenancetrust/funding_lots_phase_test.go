package maintenancetrust

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotinit"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const fundingLotsUnitEpoch = "067cb568-7791-55de-b7ba-54fc2496f411"

// Only lifecycle state is constructed here. No raw transaction exists and no
// public constructor, authority check, writer or successful COMMIT can pass.
func pureFundingLotsPermit(t *testing.T) (*Snapshot, *Permit) {
	t.Helper()
	s, _, _ := pureSnapshotLifecycle(t)
	st := s.state
	a := &authorityStage{snapshot: st, nonce: &authorityNonce{}, installed: &fundinglotcatalog.InstallationProof{}}
	ps := &permitState{snapshot: st, authority: a, nonce: &permitNonce{}}
	st.permit, st.installation = ps, a.installed
	return s, &Permit{state: ps}
}

func pureFundingLotsPhase(t *testing.T) (*fundingLotsPhase, *Permit) {
	t.Helper()
	_, permit := pureFundingLotsPermit(t)
	ph, err := permit.beginFundingLots(context.Background(), snapshotPlan, fundingLotsUnitEpoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !ph.released {
			ph.fail(ErrTransaction)
			ph.release()
		}
	})
	return ph, permit
}

func TestFundingLotsPhaseUnitPermanentLatchAndOnce(t *testing.T) {
	if fundingLotsOnce != 128 || validScanKind(ScanKind(8)) {
		t.Fatal("phase changed public scan namespace")
	}
	for _, input := range []struct {
		name, epoch string
		ctx         context.Context
		mode        snapshotMode
		want        error
	}{
		{"nil-context", fundingLotsUnitEpoch, nil, snapshotPlan, ErrInput},
		{"uppercase", strings.ToUpper(fundingLotsUnitEpoch), context.Background(), snapshotPlan, ErrInput},
		{"nil-uuid", "00000000-0000-0000-0000-000000000000", context.Background(), snapshotPlan, ErrInput},
		{"oversized", strings.Repeat("x", 1<<20), context.Background(), snapshotPlan, ErrInput},
		{"wrong-mode", fundingLotsUnitEpoch, context.Background(), snapshotInit, ErrInput},
	} {
		t.Run(input.name, func(t *testing.T) {
			s, permit := pureFundingLotsPermit(t)
			ph, err := permit.beginFundingLots(input.ctx, input.mode, input.epoch)
			if ph != nil || err != input.want {
				t.Fatal("invalid attempt result")
			}
			st, gs := s.state, s.state.gate
			if permit.state.scanAttempted&128 == 0 || st.fundingLots == nil || !st.fundingLots.released || gs.busy || st.outcome != OutcomeAbandoned {
				t.Fatal("invalid attempt did not permanently consume phase")
			}
			if st.fundingLots.epochID != "" {
				t.Fatal("invalid input retained in phase")
			}
			if s.Commit(context.Background()) != ErrTransaction {
				t.Fatal("failed phase bypassed public commit latch")
			}
			if again, err := permit.beginFundingLots(context.Background(), snapshotPlan, fundingLotsUnitEpoch); again != nil || err != ErrTransaction {
				t.Fatal("phase repeated after failure")
			}
		})
	}
	for _, stage := range []fundingLotsStage{fundingLotsPre, fundingLotsReady, fundingLotsDone} {
		s, permit := pureFundingLotsPermit(t)
		s.state.fundingLots = &fundingLotsPhase{stage: stage, released: true}
		if s.Commit(context.Background()) != ErrTransaction || permit.state.revoked {
			t.Fatal("public commit crossed permanent nonterminal latch")
		}
	}
}

func TestFundingLotsPhaseUnitBusyAndExactBinding(t *testing.T) {
	ph, permit := pureFundingLotsPhase(t)
	st, gs := ph.snapshot, ph.snapshot.gate
	copyPermit := *permit
	if next, err := copyPermit.beginFundingLots(context.Background(), snapshotPlan, fundingLotsUnitEpoch); next != nil || err != ErrGateBusy {
		t.Fatal("busy phase entered twice")
	}
	if ph.revoked || ph.released || permit.state.revoked || !gs.busy || st.fundingLots != ph {
		t.Fatal("busy rejection revoked original owner")
	}
	if err := (&Snapshot{state: st}).Commit(context.Background()); err != ErrGateBusy {
		t.Fatal("commit competed with busy owner")
	}
	if ph.verify(ph.call.ctx) != nil {
		t.Fatal("healthy pure binding")
	}
	copyPhase := *ph
	if copyPhase.verify(ph.call.ctx) != ErrTransaction {
		t.Fatal("phase pointer copy rebound")
	}
	old := ph.nonce
	ph.nonce = nil
	if ph.verify(ph.call.ctx) != ErrTransaction {
		t.Fatal("missing phase nonce")
	}
	ph.nonce = old
	if err := ph.advance(ph.call.ctx, fundingLotsBasis, fundingLotsDraft); err != ErrTransaction || ph.stage != fundingLotsPre {
		t.Fatal("state skipped")
	}
	if err := ph.advance(ph.call.ctx, fundingLotsPre, fundingLotsReady); err != ErrTransaction || ph.stage != fundingLotsPre {
		t.Fatal("illegal target bypassed phase")
	}
	if err := ph.advance(ph.call.ctx, fundingLotsPre, fundingLotsBasis); err != nil {
		t.Fatal(err)
	}
	if err := ph.advance(ph.call.ctx, fundingLotsBasis, fundingLotsDraft); err != nil {
		t.Fatal(err)
	}
	if err := ph.advance(ph.call.ctx, fundingLotsDraft, fundingLotsWriting); err != ErrTransaction || ph.stage != fundingLotsDraft {
		t.Fatal("plan acquired writing stage")
	}
	deadline, ok := ph.call.ctx.Deadline()
	if !ok || deadline.After(time.Now().Add(30*time.Second)) || deadline.After(st.deadline) {
		t.Fatal("phase extended cumulative deadline")
	}
	if ph.call.caller == context.Background() {
		t.Fatal("phase lacks one cumulative budget")
	}
}

func TestFundingLotsPhaseUnitBudgetUsesActualTwelveGroups(t *testing.T) {
	ph, _ := pureFundingLotsPhase(t)
	ph.draft.Metadata.GeneratedRows = 2
	if err := ph.checkFundingLotsBudget(ph.call.ctx); err != nil {
		t.Fatal("empty generated set budget", err)
	}
	ph.draft.Metadata.GeneratedRows = 3
	if err := ph.checkFundingLotsBudget(ph.call.ctx); err != ErrTransaction {
		t.Fatal("descriptive generated count replaced actual rows")
	}
	ph.draft.Metadata.GeneratedRows = 2
	ph.draft.Heads = []fundinglotverify.WalletSequenceHeadRow{{}}
	if err := ph.checkFundingLotsBudget(ph.call.ctx); err != ErrTransaction {
		t.Fatal("actual group not recounted")
	}
	ph.draft.Metadata.GeneratedRows, ph.draft.Metadata.DataRows = 3, 1
	if err := ph.checkFundingLotsBudget(ph.call.ctx); err != nil {
		t.Fatal("actual generated set budget", err)
	}
}

func TestFundingLotsPhaseUnitRowsEOFAndOwnedDestination(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "err-first", true: "close-first"}[closeFirst], func(t *testing.T) {
			ph, _ := pureFundingLotsPhase(t)
			driver := &pureLeaseRows{count: 1}
			rows := &fundingLotsRows{phase: ph, driver: driver}
			ph.pending = rows
			if more, err := rows.Next(); !more || err != nil {
				t.Fatal("first row")
			}
			var got string
			if err := rows.Scan(&got); err != nil || got != "owned" {
				t.Fatal("owned scalar scan")
			}
			if more, err := rows.Next(); more || err != nil {
				t.Fatal("actual EOF")
			}
			if closeFirst {
				if rows.Close() != nil {
					t.Fatal("close")
				}
			} else if rows.Err() != nil {
				t.Fatal("err")
			}
			if ph.pending == nil || ph.noPending(ph.call.ctx) != ErrTransaction || !ph.snapshot.gate.busy {
				t.Fatal("early release of pending or owner")
			}
			if closeFirst {
				if rows.Err() != nil {
					t.Fatal("err")
				}
			} else if rows.Close() != nil {
				t.Fatal("close")
			}
			if ph.pending != nil || ph.noPending(ph.call.ctx) != nil || !ph.snapshot.gate.busy || driver.closes.Load() != 1 || rows.Close() != nil || rows.Err() != nil {
				t.Fatal("EOF cleanup lost phase ownership")
			}
		})
	}
}

func TestFundingLotsPhaseUnitRowsFailuresNeverDrainOrReleaseOwner(t *testing.T) {
	for _, mistake := range []string{"early-close", "skipped-row", "callback-destination", "driver-error"} {
		t.Run(mistake, func(t *testing.T) {
			ph, _ := pureFundingLotsPhase(t)
			driver := &pureLeaseRows{count: 2}
			rows := &fundingLotsRows{phase: ph, driver: driver}
			ph.pending = rows
			want := ErrTransaction
			var err error
			switch mistake {
			case "early-close":
				err = rows.Close()
			case "skipped-row":
				_, _ = rows.Next()
				_, err = rows.Next()
			case "callback-destination":
				_, _ = rows.Next()
				err = rows.Scan(&struct{}{})
				want = ErrInput
			case "driver-error":
				driver.count = 0
				driver.fail = errors.New("must not escape")
				_, err = rows.Next()
				want = ErrExecution
			}
			if err != want || !ph.revoked || ph.released || !ph.snapshot.gate.busy || driver.closes.Load() != 0 || rows.Close() != nil {
				t.Fatal("failed rows lost owner or touched driver during abandonment")
			}
			ph.release()
			if ph.snapshot.gate.busy || !ph.released || ph.snapshot.fundingLots != ph {
				t.Fatal("unwind lost permanent latch")
			}
		})
	}
}

func TestFundingLotsPhaseUnitCleanupWaitsForCallbackAck(t *testing.T) {
	ph, permit := pureFundingLotsPhase(t)
	ph.call.close()
	ctx, cancel := context.WithCancel(context.Background())
	ack := make(chan struct{})
	call := &snapshotCall{state: ph.snapshot, ctx: ctx, caller: context.Background(), cancel: cancel, stopCaller: func() bool { return false }, done: ack}
	ph.call, ph.snapshot.permitCall = call, call
	done := make(chan struct{})
	go func() { ph.release(); close(done) }()
	if next, err := permit.beginFundingLots(context.Background(), snapshotPlan, fundingLotsUnitEpoch); next != nil || err != ErrGateBusy {
		t.Fatal("owner released before callback acknowledgement")
	}
	close(ack)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("callback acknowledgement hung")
	}
	if !ph.released || ph.snapshot.gate.busy || ph.snapshot.permitCall != nil || ph.snapshot.fundingLots != ph {
		t.Fatal("cleanup/latch incorrect")
	}
}

func TestFundingLotsPhaseUnitSafeFailureAndExpectedSurface(t *testing.T) {
	for _, leaf := range []error{fundinglotscan.ErrResource, fundinglotbasis.ErrResource, fundinglotinit.ErrResource} {
		if fundingLotsReadFailure(leaf, ErrTransaction) != ErrResource || fundingLotsFailure(leaf) != ErrResource {
			t.Fatal("resource loss during early close")
		}
	}
	if fundingLotsFailure(errors.New("driver secret")) != ErrExecution || errors.Unwrap(ErrResource) != nil {
		t.Fatal("error escaped closed vocabulary")
	}
	typ := reflect.TypeOf(FundingLotsExpected{})
	if typ.NumField() != 5 {
		t.Fatal("expected expanded authority surface")
	}
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type.Kind() != reflect.String {
			t.Fatal("expected contains noncomparison field")
		}
	}
	var permit Permit
	if got, err := permit.PlanFundingLots(context.Background(), fundingLotsUnitEpoch); err != ErrInput || got != (FundingLotsPlanResult{}) {
		t.Fatal("zero permit plan leaked")
	}
	if got, err := permit.InitializeFundingLots(context.Background(), FundingLotsExpected{}); err != ErrInput || got != (FundingLotsInitResult{}) {
		t.Fatal("zero permit init leaked")
	}
	if fundingLotsDigest(strings.Repeat("a", 64)) != true || fundingLotsDigest(strings.Repeat("A", 64)) || fundingLotsUUID(strings.ToUpper(fundingLotsUnitEpoch)) {
		t.Fatal("noncanonical comparison values")
	}
}

func TestFundingLotsPhaseUnitCommitRequiresReadyBeforeSQL(t *testing.T) {
	ph, _ := pureFundingLotsPhase(t)
	if err := ph.commitOwned(ph.call.ctx); err != ErrTransaction {
		t.Fatal("nonready owner reached SQL")
	}
	if !ph.revoked || ph.readyConsumed || ph.snapshot.terminal != terminalNone || ph.snapshot.outcome != OutcomeAbandoned || !ph.snapshot.gate.busy {
		t.Fatal("nonready commit changed terminal or released owner")
	}
}

func TestFundingLotsPhaseUnitPostChecksEveryChildAndActualCount(t *testing.T) {
	r := fundinglotscan.WalletReport{Scope: "complete_source_wallet_graphs_v1", Mode: fundinglotscan.ModeOrdinary, Status: "consistent", PhysicalEOFComplete: true, ScannedTables: 84, CoverageComplete: true, SourceRows: 2,
		Algorithms:  [8]string{"executed", "executed", "executed", "executed", "executed", "executed", "not_applicable", "executed"},
		Ledger:      fundinglotverify.LedgerRowsCheck{Scope: "supplied_ledger_rows", RowsConsistent: true},
		Links:       fundinglotverify.FundingSourceLinksCheck{Scope: "manual_voucher_receipt_links", ScopedLinksConsistent: true},
		Funding:     fundinglotverify.FundingSourceGraphCheck{Scope: "manual_voucher_funding_graph_v1", ScopedGraphConsistent: true},
		Customer:    fundinglotverify.CustomerKeyGraphCheck{Scope: "customer_key_financial_graph_v1", Balanced: true},
		Procurement: fundinglotverify.ProcurementEvidenceGraphCheck{Scope: "procurement_domain_evidence_graph_v1", Status: "balanced", Balanced: true, CoverageComplete: true},
		Periodic:    fundinglotverify.PeriodicBudgetGraphCheck{Scope: "periodic_budget_graph_v1", Status: "balanced", Balanced: true, CoverageComplete: true},
		Wallet:      fundinglotverify.WalletLotsRowsCheck{Scope: "supplied_wallet_lots_rows", Status: "consistent", RowsConsistent: true, Scanned: fundinglotverify.WalletLotsRowCounts{Epochs: 1, Attestations: 1}}}
	if !fundingLotsPostQualified(r, fundinglotscan.ModeOrdinary) {
		t.Fatal("qualified empty post rejected")
	}
	for _, mutate := range []func(*fundinglotscan.WalletReport){
		func(v *fundinglotscan.WalletReport) { v.PhysicalEOFComplete = false },
		func(v *fundinglotscan.WalletReport) { v.Algorithms[7] = "not_applicable" },
		func(v *fundinglotscan.WalletReport) { v.Procurement.CoverageComplete = false },
		func(v *fundinglotscan.WalletReport) { v.Wallet.Unproven.ReplayPrefixUnproven = 1 },
		func(v *fundinglotscan.WalletReport) {
			v.Wallet.Sequence = fundinglotverify.WalletSequenceRowMismatches{InvalidTextRows: 1}
		},
		func(v *fundinglotscan.WalletReport) { v.Sandbox.Scope = "unexpected" },
	} {
		copyReport := r
		mutate(&copyReport)
		if fundingLotsPostQualified(copyReport, fundinglotscan.ModeOrdinary) {
			t.Fatal("aggregate status hid failed child")
		}
	}
	plan := fundinglotinit.Plan{Metadata: fundinglotinit.Metadata{GeneratedRows: 2}}
	if !fundingLotsPostCounts(r, fundinglotscan.Report{}, plan) {
		t.Fatal("empty actual count qualification")
	}
	r.SourceRows++
	if fundingLotsPostCounts(r, fundinglotscan.Report{}, plan) {
		t.Fatal("actual source count drift accepted")
	}
}
