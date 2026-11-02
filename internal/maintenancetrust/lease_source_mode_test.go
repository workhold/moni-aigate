package maintenancetrust

import (
	"context"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

// This fixture constructs private binding state only. It cannot satisfy a
// public BeginScan/Finish or authenticate an actual database installation.
func pureCompleteSourceLease(t *testing.T, mode fundinglotcatalog.Mode) (*ReadLease, *Permit, *pureLeaseRows) {
	t.Helper()
	driver := &pureLeaseRows{}
	l, _, p := pureReadLease(t, driver, false)
	ls := l.state
	st, gs := ls.snapshot, ls.snapshot.gate
	a := &authorityStage{
		snapshot: st, nonce: &authorityNonce{}, call: ls.call,
		installed: &fundinglotcatalog.InstallationProof{}, manifest: &manifestEvidence{}, identity: &maintenanceIdentity{},
	}
	e := &predecessorEvidence{
		authority: a, nonce: a.nonce, snapshot: st, snapshotNonce: st.nonce, gateNonce: st.gateNonce,
		installed: a.installed, manifest: a.manifest, identity: a.identity, purpose: st.mode, mode: mode,
		resources: predecessorResources{rows: 1},
	}
	gs.pins.Mode = mode
	if mode == fundinglotcatalog.Sandbox {
		pins, rows := predecessorRowsFixture(t)
		gs.pins = pins
		proof, raw, digest, err := predecessorValidateSandbox(pins, rows)
		if err != nil {
			t.Fatal(err)
		}
		e.rows, e.proof, e.canonical, e.v1SHA256 = rows, proof, raw, digest
	} else {
		e.ordinary = predecessorOrdinaryRows{foreignKey: true, policy: predecessorRow{
			"singleton": {kind: predecessorBool, boolean: true},
			"enabled":   {kind: predecessorBool},
			"revision":  {kind: predecessorInt8, int8: 1},
		}}
	}
	a.predecessor = e
	ls.kind = ScanCompleteSources
	ls.permit.authority = a
	return l, p, driver
}

func TestCompleteSourceScanKindAppendsWithoutChangingLegacyValues(t *testing.T) {
	kinds := []ScanKind{ScanPeriodicBudget, ScanLedger, ScanFunding, ScanProcurement, ScanSandboxSources, ScanCompleteSources}
	for i, kind := range kinds {
		if kind != ScanKind(i+1) || !validScanKind(kind) {
			t.Fatal("legacy scan kind changed or complete-source kind is unavailable")
		}
	}
	for _, kind := range []ScanKind{0, 7, 255} {
		if validScanKind(kind) {
			t.Fatal("unknown scan kind accepted")
		}
	}
}

func TestReadLeaseSourceModeUsesOwnedPredecessorWithoutSQL(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode fundinglotcatalog.Mode
		want SourceMode
	}{
		{"ordinary", fundinglotcatalog.Ordinary, SourceModeOrdinary},
		{"sandbox", fundinglotcatalog.Sandbox, SourceModeSandbox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _, driver := pureCompleteSourceLease(t, tc.mode)
			copyL := *l
			for _, lease := range []*ReadLease{l, &copyL} {
				got, err := lease.SourceMode()
				if err != nil || got != tc.want {
					t.Fatalf("owned source mode=%v want %v err=%v", got, tc.want, err)
				}
			}
			if !l.state.snapshot.gate.busy || l.state.opBusy || driver.nexts.Load() != 0 || driver.scans.Load() != 0 || driver.closes.Load() != 0 {
				t.Fatal("mode read touched a driver or released the enclosing lease busy slot")
			}
		})
	}
	for _, l := range []*ReadLease{nil, {}} {
		if mode, err := l.SourceMode(); mode != 0 || err != ErrInput {
			t.Fatal("zero lease produced a source mode")
		}
	}
}

func TestCompleteSourceRequireRejectsWrongKindAndPreservesBusyOwner(t *testing.T) {
	t.Run("wrong_kind", func(t *testing.T) {
		driver := &pureLeaseRows{}
		l, _, _ := pureReadLease(t, driver, false)
		if err := l.Require(ScanCompleteSources); err != ErrTransaction || !l.state.revoked || !l.state.released || l.state.snapshot.gate.busy {
			t.Fatal("wrong complete-source kind retained its invalid lease")
		}
		if driver.nexts.Load() != 0 || driver.scans.Load() != 0 || driver.closes.Load() != 0 {
			t.Fatal("wrong-kind Require touched a driver")
		}
	})
	t.Run("busy_owner", func(t *testing.T) {
		l, p, driver := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		ls, gs := l.state, l.state.snapshot.gate
		gs.mu.Lock()
		ls.opBusy = true
		gs.mu.Unlock()
		if err := l.Require(ScanCompleteSources); err != ErrGateBusy {
			t.Fatal("competing complete-source Require accepted")
		}
		gs.mu.Lock()
		unchanged := !ls.revoked && !ls.released && !p.state.revoked && ls.opBusy && gs.busy && ls.snapshot.activeLease == ls && ls.snapshot.permit == p.state
		ls.opBusy = false
		gs.mu.Unlock()
		if !unchanged || l.Require(ScanCompleteSources) != nil || driver.nexts.Load() != 0 || driver.scans.Load() != 0 || driver.closes.Load() != 0 {
			t.Fatal("rejected Require disturbed or touched the original owner")
		}
	})
}

func TestReadLeaseSourceModeRejectsRebindingAndWrongKind(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*leaseState)
	}{
		{"legacy_kind", func(ls *leaseState) { ls.kind = ScanProcurement }},
		{"missing_authority", func(ls *leaseState) { ls.permit.authority = nil }},
		{"missing_predecessor", func(ls *leaseState) { ls.permit.authority.predecessor = nil }},
		{"authority_copy", func(ls *leaseState) { copyA := *ls.permit.authority; ls.permit.authority = &copyA }},
		{"snapshot_nonce", func(ls *leaseState) { ls.permit.authority.predecessor.snapshotNonce = &snapshotNonce{} }},
		{"gate_nonce", func(ls *leaseState) { ls.permit.authority.predecessor.gateNonce = &gateNonce{} }},
		{"authority_nonce", func(ls *leaseState) { ls.permit.authority.predecessor.nonce = &authorityNonce{} }},
		{"snapshot_purpose", func(ls *leaseState) { ls.permit.authority.predecessor.purpose = snapshotInit }},
		{"mode_rebinding", func(ls *leaseState) { ls.permit.authority.predecessor.mode = fundinglotcatalog.Sandbox }},
		{"unknown_owned_mode", func(ls *leaseState) { ls.snapshot.gate.pins.Mode = 99; ls.permit.authority.predecessor.mode = 99 }},
		{"policy_damage", func(ls *leaseState) {
			ls.permit.authority.predecessor.ordinary.policy["enabled"] = predecessorScalar{kind: predecessorBool, boolean: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _, driver := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
			tc.mutate(l.state)
			if mode, err := l.SourceMode(); mode != 0 || err == nil {
				t.Fatal("invalid or rebound predecessor disclosed a mode")
			}
			if !l.state.revoked || !l.state.released || l.state.snapshot.gate.busy || driver.nexts.Load() != 0 || driver.scans.Load() != 0 {
				t.Fatal("bad predecessor did not fail closed without SQL")
			}
		})
	}
}

func TestReadLeaseSourceModeRespectsBusyTimeoutAndCompletion(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		l, _, _ := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		l.state.snapshot.gate.mu.Lock()
		l.state.opBusy = true
		l.state.snapshot.gate.mu.Unlock()
		if mode, err := l.SourceMode(); mode != 0 || err != ErrGateBusy || l.state.revoked {
			t.Fatal("busy mode read changed its owner")
		}
		l.state.snapshot.gate.mu.Lock()
		l.state.opBusy = false
		l.state.snapshot.gate.mu.Unlock()
		if mode, err := l.SourceMode(); mode != SourceModeOrdinary || err != nil {
			t.Fatal("mode unavailable after original operation completed")
		}
	})
	t.Run("expired", func(t *testing.T) {
		l, _, _ := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		l.state.snapshot.gate.mu.Lock()
		l.state.snapshot.deadline = time.Now().Add(-time.Second)
		l.state.snapshot.gate.mu.Unlock()
		if mode, err := l.SourceMode(); mode != 0 || err != ErrTimeout || !l.state.revoked {
			t.Fatal("expired source mode escaped or failed to revoke")
		}
	})
	t.Run("parent_canceled", func(t *testing.T) {
		l, _, _ := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		copyL := *l
		l.state.snapshot.cancel()
		for _, lease := range []*ReadLease{l, &copyL} {
			if mode, err := lease.SourceMode(); mode != 0 || err == nil {
				t.Fatal("canceled original snapshot or its copy still disclosed a mode")
			}
		}
	})
	t.Run("finished_and_once", func(t *testing.T) {
		l, p, driver := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		ls, gs := l.state, l.state.snapshot.gate
		gs.mu.Lock()
		ls.query = nil
		ls.finished = true
		ls.proof = &scanProofState{lease: ls, nonce: &scanProofNonce{}}
		ls.permit.scanAttempted |= 1 << uint(ScanCompleteSources-1)
		clean := ls.claimCleanupLocked()
		gs.mu.Unlock()
		if !clean {
			t.Fatal("pure completion did not retain cleanup ownership")
		}
		ls.closeCall()
		proof := &ScanProof{state: ls.proof}
		if mode, err := l.SourceMode(); mode != 0 || err != ErrTransaction {
			t.Fatal("completed lease still disclosed its mode")
		}
		if proof.Verify(p, ScanCompleteSources) != nil || proof.Verify(p, ScanProcurement) != ErrTransaction {
			t.Fatal("complete-source proof lost its exact kind binding")
		}
		if next, err := p.BeginScan(context.Background(), ScanCompleteSources); next != nil || err != ErrTransaction || ls.permit.revoked {
			t.Fatal("complete-source kind repeated or revoked the original completed proof")
		}
		if proof.Verify(p, ScanCompleteSources) != nil || driver.nexts.Load() != 0 {
			t.Fatal("duplicate attempt touched SQL or changed the completed proof")
		}
	})
}
