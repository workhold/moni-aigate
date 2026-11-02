package maintenancetrust

import (
	"context"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
)

func TestSourceWalletReadLeaseOwnedModeAndIndependentOnceBit(t *testing.T) {
	for _, mode := range []fundinglotcatalog.Mode{fundinglotcatalog.Ordinary, fundinglotcatalog.Sandbox} {
		t.Run(map[fundinglotcatalog.Mode]string{fundinglotcatalog.Ordinary: "ordinary", fundinglotcatalog.Sandbox: "sandbox"}[mode], func(t *testing.T) {
			lease, permit, driver := pureCompleteSourceLease(t, mode)
			ls, gs := lease.state, lease.state.snapshot.gate
			ls.kind = ScanSourceWallet
			// Previous six attempts must not alias the new seventh bit.
			permit.state.scanAttempted = 0x3f
			copyLease := *lease
			want := SourceModeOrdinary
			if mode == fundinglotcatalog.Sandbox {
				want = SourceModeSandbox
			}
			for _, current := range []*ReadLease{lease, &copyLease} {
				if current.Require(ScanSourceWallet) != nil {
					t.Fatal("source-wallet lease lost its exact kind")
				}
				if got, err := current.SourceMode(); err != nil || got != want {
					t.Fatal("source-wallet mode did not use the owned predecessor", got, err)
				}
			}
			gs.mu.Lock()
			ls.query = nil
			ls.finished = true
			ls.proof = &scanProofState{lease: ls, nonce: &scanProofNonce{}}
			permit.state.scanAttempted |= 1 << uint(ScanSourceWallet-1)
			clean := ls.claimCleanupLocked()
			gs.mu.Unlock()
			if !clean {
				t.Fatal("pure source-wallet completion lost cleanup ownership")
			}
			ls.closeCall()
			proof := &ScanProof{state: ls.proof}
			if permit.state.scanAttempted != 0x7f || proof.Verify(permit, ScanSourceWallet) != nil || proof.Verify(permit, ScanCompleteSources) != ErrTransaction {
				t.Fatal("source-wallet once bit or proof aliased an old kind")
			}
			if next, err := permit.BeginScan(context.Background(), ScanSourceWallet); next != nil || err != ErrTransaction || permit.state.revoked {
				t.Fatal("seventh kind repeated or changed the completed proof")
			}
			if next, err := copyLease.Finish(); next != nil || err != ErrTransaction || proof.Verify(permit, ScanSourceWallet) != nil {
				t.Fatal("copied source-wallet lease finished twice or revoked the original proof")
			}
			if got, err := copyLease.SourceMode(); got != 0 || err != ErrTransaction || driver.nexts.Load() != 0 || driver.scans.Load() != 0 || driver.closes.Load() != 0 {
				t.Fatal("completed source-wallet mode escaped or the mode getter touched SQL")
			}
		})
	}
}

func TestSourceModeRejectsEveryLegacyKind(t *testing.T) {
	for kind := ScanPeriodicBudget; kind < ScanCompleteSources; kind++ {
		lease, _, driver := pureCompleteSourceLease(t, fundinglotcatalog.Ordinary)
		lease.state.kind = kind
		if got, err := lease.SourceMode(); got != 0 || err != ErrTransaction || !lease.state.revoked || !lease.state.released {
			t.Fatal("legacy kind obtained the complete-source mode", kind)
		}
		if driver.nexts.Load() != 0 || driver.scans.Load() != 0 {
			t.Fatal("wrong-kind source mode reached a driver")
		}
	}
}
