package maintenancetrust

import (
	"context"
	"testing"
)

// This opt-in test authenticates the ordinary predecessor through the real
// Acquire/Snapshot/installation/Attest/BeginScan chain. ScanProof still proves
// lease completion only; the complete 68-table reader is verified separately.
func TestCompleteSourceReadLeasePostgresOrdinaryPlanInitModeAndProof(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, initialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "plan", true: "init"}[initialize], func(t *testing.T) {
			g, snapshot, permit, lease, wire, backend := realReadLease(t, f, initialize, context.Background(), ScanCompleteSources)
			if err := lease.Require(ScanCompleteSources); err != nil {
				t.Fatal("complete-source ordinary lease kind", err)
			}
			copyLease, copyPermit := *lease, *permit
			before, _ := wire.recorded()
			for _, current := range []*ReadLease{lease, &copyLease} {
				mode, err := current.SourceMode()
				if err != nil || mode != SourceModeOrdinary {
					t.Fatal("real ordinary predecessor did not supply its bound mode", err)
				}
			}
			after, _ := wire.recorded()
			if len(before) != len(after) {
				t.Fatal("owned ordinary mode getter sent SQL")
			}
			var pid uint32
			var xid, isolation, access string
			if err := lease.QueryRow(`SELECT pg_catalog.pg_backend_pid(),pg_catalog.pg_current_xact_id()::pg_catalog.text,
 pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')`).Scan(&pid, &xid, &isolation, &access); err != nil ||
				pid != backend.pid || xid != snapshot.state.xid || isolation != "repeatable read" || (access == "off") != initialize {
				t.Fatal("complete-source lease changed its original authenticated transaction", err)
			}
			proof, err := lease.Finish()
			if err != nil || proof == nil || proof.Verify(permit, ScanCompleteSources) != nil || proof.Verify(&copyPermit, ScanCompleteSources) != nil {
				t.Fatal("real complete-source lease did not finish with its original permit", err)
			}
			copyProof := *proof
			if next, err := copyLease.Finish(); next != nil || err != ErrTransaction {
				t.Fatal("copied complete-source lease finished a second time")
			}
			if mode, err := copyLease.SourceMode(); mode != 0 || err != ErrTransaction {
				t.Fatal("completed ordinary lease retained an active source-mode getter")
			}
			if err := lease.Abort(); err != nil || copyProof.Verify(permit, ScanCompleteSources) != nil {
				t.Fatal("idempotent post-Finish cleanup revoked its completed proof")
			}
			if next, err := permit.BeginScan(context.Background(), ScanCompleteSources); next != nil || err != ErrTransaction || proof.Verify(permit, ScanCompleteSources) != nil {
				t.Fatal("complete-source scan kind repeated or disturbed the original proof")
			}
			clonedState := *permit.state
			if proof.Verify(&Permit{state: &clonedState}, ScanCompleteSources) == nil || proof.Verify(&Permit{}, ScanCompleteSources) == nil || proof.Verify(permit, ScanProcurement) == nil {
				t.Fatal("complete-source proof rebound to another permit identity or legacy kind")
			}
			if err := snapshot.Rollback(context.Background()); err != nil {
				t.Fatal("ordinary complete-source snapshot rollback", err)
			}
			if proof.Verify(permit, ScanCompleteSources) == nil || copyProof.Verify(&copyPermit, ScanCompleteSources) == nil {
				t.Fatal("complete-source proof or its copy survived the original snapshot terminal state")
			}
			if err := g.Close(nil); err != nil {
				t.Fatal("ordinary complete-source gate cleanup", err)
			}
			f.gone(t, backend)
		})
	}
}
