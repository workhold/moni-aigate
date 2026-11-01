package maintenancetrust

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestPermitZeroAndRedaction(t *testing.T) {
	for _, p := range []*Permit{nil, {}} {
		if p.Verify(context.Background()) != ErrInput {
			t.Fatal("zero permit accepted")
		}
	}
	if p, err := Attest(context.Background(), nil); p != nil || err != ErrInput {
		t.Fatal("nil snapshot accepted")
	}
	for _, v := range []any{Permit{}, &Permit{}} {
		b, err := json.Marshal(v)
		if err != nil || string(b) != `{"permit":"redacted"}` || fmt.Sprint(v) != "maintenance_permit_redacted" || fmt.Sprintf("%#v", v) != "maintenance_permit_redacted" {
			t.Fatal("permit output exposed internals")
		}
	}
}

func TestPermitLifecycleRevokesAllCopiesWithoutPGX(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			s, cancel, peer := pureSnapshotLifecycle(t)
			st, gs := s.state, s.state.gate
			a := &authorityStage{snapshot: st, nonce: &authorityNonce{}, active: true}
			ps := &permitState{snapshot: st, authority: a, nonce: &permitNonce{}}
			p := Permit{state: ps}
			copyP := p
			gs.mu.Lock()
			st.activeAuthority = a
			st.permit = ps
			gs.mu.Unlock()
			if cancelParent {
				cancel()
				requireSnapshotSocketClosed(t, peer)
			} else {
				gs.mu.Lock()
				st.invalidateLocked(ErrSchema)
				gs.mu.Unlock()
			}
			gs.mu.Lock()
			revoked := ps.revoked && !a.active && st.activeAuthority == nil && st.permit == nil && st.permitCall == nil
			gs.mu.Unlock()
			if !revoked || p.Verify(context.Background()) != ErrTransaction || copyP.Verify(context.Background()) != ErrTransaction {
				t.Fatal("revoked shared permit survived")
			}
		})
	}
}

func TestPermitAuthorityBusyAndMissingOwnedInstallation(t *testing.T) {
	s, _, peer := pureSnapshotLifecycle(t)
	st, gs := s.state, s.state.gate
	gs.mu.Lock()
	gs.busy = true
	gs.mu.Unlock()
	if p, err := Attest(context.Background(), s); p != nil || err != ErrGateBusy {
		t.Fatal("busy attest accepted")
	}
	gs.mu.Lock()
	attempted := st.authorityAttempted
	gs.busy = false
	gs.mu.Unlock()
	if attempted {
		t.Fatal("busy call consumed authority attempt")
	}
	if p, err := Attest(context.Background(), s); p != nil || err != ErrTransaction {
		t.Fatal("attest without actual installation accepted")
	}
	requireSnapshotSocketClosed(t, peer)
	gs.mu.Lock()
	invalid := st.authorityAttempted && st.invalidErr == ErrTransaction && st.outcome == OutcomeAbandoned && !gs.busy
	gs.mu.Unlock()
	if !invalid {
		t.Fatal("bad authority attempt did not fail closed")
	}
	if p, err := Attest(context.Background(), s); p != nil || err != ErrTransaction {
		t.Fatal("failed authority attempt repeated")
	}
}
