package maintenancetrust

import "context"

// Snapshot fact readers are private, read-only projections. Their caller owns
// the snapshot lifecycle, busy slot and actual SQL binding; these checks do not
// issue or replace an InstallationProof, authorityStage or Permit.
func maintenanceFactsSnapshot(ctx context.Context, st *snapshotState) error {
	if ctx == nil {
		return ErrInput
	}
	if ctx.Err() != nil {
		return ErrTimeout
	}
	if st == nil || st.gate == nil || st.rawTx == nil {
		return ErrTransaction
	}
	if !validPins(st.gate.pins) {
		return ErrIdentity
	}
	return nil
}
