package maintenancetrust

import (
	"context"
	"testing"
	"time"
)

// This private-stage test starts from a real Gate/raw RR/owned installation. It
// exercises the subcall's CPU-gap watcher only; it cannot issue a Permit.
func TestAuthorityPostgresPredecessorCPUBudgetClosesOriginalSocket(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, mode := range []string{"sub-budget", "caller-cancel"} {
		t.Run(mode, func(t *testing.T) {
			g, s, _, b := authorityInstalled(t, f, false)
			st, gs := s.state, s.state.gate
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			call, err := st.stageContext(caller)
			if err != nil {
				t.Fatal("actual authority call")
			}
			defer call.close()
			a := &authorityStage{snapshot: st, nonce: &authorityNonce{}, call: call, installed: st.installation, active: true}
			gs.mu.Lock()
			gs.busy = true
			st.authorityAttempted = true
			st.activeAuthority = a
			gs.mu.Unlock()
			sub, subcancel, err := a.beginPredecessor()
			if err != nil {
				t.Fatal("actual predecessor subcall")
			}
			defer subcancel()
			defer sub.close()
			start := time.Now()
			if mode == "caller-cancel" {
				cancel()
			}
			// No pgx operation runs during this wait. Only cancellation/deadline can
			// close the physical backend while its owner is between fixed queries.
			select {
			case <-sub.ctx.Done():
			case <-time.After(7 * time.Second):
				t.Fatal("CPU sub-budget did not expire")
			}
			if mode == "sub-budget" && (time.Since(start) < 4500*time.Millisecond || time.Since(start) > 6500*time.Millisecond) {
				t.Fatal("private five second cumulative CPU budget changed")
			}
			f.gone(t, b)
			gs.mu.Lock()
			invalid := st.invalidErr == ErrTimeout && !a.active && st.activeAuthority == nil && gs.busy
			gs.busy = false
			gs.mu.Unlock()
			if !invalid {
				t.Fatal("CPU expiration did not revoke without taking SQL ownership")
			}
			_ = g.Close(nil)
		})
	}
}
