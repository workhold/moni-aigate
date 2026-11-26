package routing

import (
	"context"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

// attributionFixture drives one channel+model health key ("ch:m") through
// deterministic attempts with per-plan credentials, mirroring how the gateway
// reports upstream failures to the engine.
type attributionFixture struct {
	t       *testing.T
	engine  *Engine
	now     time.Time
	breaker BreakerConfig
}

func newAttributionFixture(t *testing.T, breaker BreakerConfig) *attributionFixture {
	f := &attributionFixture{t: t, now: time.Unix(100, 0), breaker: breaker}
	f.engine = NewEngine(Config{Now: func() time.Time { return f.now }, RandN: func(n uint64) uint64 { return 0 }})
	return f
}

func (f *attributionFixture) candidate(credential string) Candidate {
	c := candidate("ch", 0, 1)
	c.CredentialID = credential
	c.HealthKey = "ch:m"
	c.Breaker = f.breaker
	return c
}

func (f *attributionFixture) start(credential string) *Attempt {
	f.t.Helper()
	a, err := f.tryStart(credential)
	if err != nil {
		f.t.Fatalf("start attempt %s: %v", credential, err)
	}
	return a
}

func (f *attributionFixture) tryStart(credential string) (*Attempt, error) {
	f.t.Helper()
	p := plan(f.t, f.candidate(credential))
	return p.Start(context.Background(), f.engine, admit)
}

func (f *attributionFixture) fail(credential string, status int) {
	f.start(credential).FinishWithFailure(Failure, FailureDetail{Class: provider.ClassUpstream, UpstreamStatus: status})
}

func (f *attributionFixture) succeed(credential string) {
	f.start(credential).Finish(Success)
}

func (f *attributionFixture) snap() HealthSnapshot {
	return f.engine.Snapshot("ch:m", "ch")
}

func TestAttributionWithdrawsPairedUpstreamFailure(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Second, HalfOpenTokens: 1})
	f.succeed("k0")
	f.fail("k1", 500)
	if got := f.snap(); got.State != Closed || got.ConsecutiveFailures != 1 {
		t.Fatalf("first unpaired failure must count: %+v", got)
	}
	f.fail("k2", 500)
	if got := f.snap(); got.State != Closed || got.ConsecutiveFailures != 0 {
		t.Fatalf("paired rejection must withdraw the count: %+v", got)
	}
	f.start("k3").Finish(Neutral) // route stays usable after the withdrawal
}

func TestAttributionRequiresRecentSuccess(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute, HalfOpenTokens: 1})
	f.succeed("k0")
	f.fail("k1", 500)
	f.now = f.now.Add(6 * time.Minute) // success older than the recency window
	f.fail("k2", 500)
	if got := f.snap(); got.State != Open || got.ConsecutiveFailures != 2 {
		t.Fatalf("stale success must not attribute: %+v", got)
	}
}

func TestAttributionDoesNotPairAcrossStatusesOrSameCredential(t *testing.T) {
	test := func(name string, first, second int, firstCred, secondCred string) {
		t.Run(name, func(t *testing.T) {
			f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute, HalfOpenTokens: 1})
			f.succeed("k0")
			f.fail(firstCred, first)
			f.fail(secondCred, second)
			if got := f.snap(); got.State != Open || got.ConsecutiveFailures != 2 {
				t.Fatalf("mismatched signatures must both count: %+v", got)
			}
		})
	}
	test("different_status", 500, 502, "k1", "k2")
	test("same_credential", 500, 500, "k1", "k1")
}

func TestAttributionWindowExpires(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute, HalfOpenTokens: 1})
	f.succeed("k0")
	f.fail("k1", 500)
	f.now = f.now.Add(attributionWindow + time.Second)
	f.fail("k2", 500)
	if got := f.snap(); got.State != Open || got.ConsecutiveFailures != 2 {
		t.Fatalf("expired ledger entries must not pair: %+v", got)
	}
}

func TestAttributionLedgerBounded(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 20, OpenDuration: time.Second, HalfOpenTokens: 1})
	f.succeed("k0")
	for range attributionLedgerMax + 1 {
		f.fail("k1", 500)
		f.now = f.now.Add(time.Second) // let the open expire so the next attempt can start
	}
	f.fail("k2", 500)
	if got := f.snap(); got.ConsecutiveFailures != attributionLedgerMax {
		t.Fatalf("ledger bound must keep counting consistent: %+v", got)
	}
}

func TestAttributedHalfOpenProbeKeepsHalfOpen(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Second, HalfOpenTokens: 1})
	f.succeed("k0")
	f.fail("k1", 500)
	f.fail("k2", 502) // unpaired: opens the breaker
	f.now = f.now.Add(time.Second)
	probe := f.start("k3")
	if got := f.snap(); got.State != HalfOpen || !got.ProbeInFlight {
		t.Fatalf("probe must hold the half-open token: %+v", got)
	}
	probe.FinishWithFailure(Failure, FailureDetail{Class: provider.ClassUpstream, UpstreamStatus: 500})
	if got := f.snap(); got.State != HalfOpen || got.ProbeInFlight || got.ConsecutiveFailures != 1 {
		t.Fatalf("attributed probe must return the token and stay half-open: %+v", got)
	}
	f.succeed("k4")
	if got := f.snap(); got.State != Closed || got.ConsecutiveFailures != 0 {
		t.Fatalf("trusted probe must close: %+v", got)
	}
}

func TestStaleGenerationCompletionCannotWithdraw(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute, HalfOpenTokens: 1})
	f.succeed("k0")
	stale := f.start("k1") // in flight while the breaker opens underneath it
	f.fail("k2", 500)
	f.fail("k3", 502) // unpaired: opens the breaker, bumps the generation
	stale.FinishWithFailure(Failure, FailureDetail{Class: provider.ClassUpstream, UpstreamStatus: 500})
	if got := f.snap(); got.State != Open || got.ConsecutiveFailures != 2 {
		t.Fatalf("stale generation must be discarded entirely: %+v", got)
	}
}

func TestOverloadedFixedRestOutsideLadder(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: 5 * time.Second, HalfOpenTokens: 1})
	for range 3 {
		f.start("k1").Finish(Overloaded)
		got := f.snap()
		if got.State != Open || got.ConsecutiveFailures != 0 || !got.OpenUntil.Equal(f.now.Add(overloadedRest)) {
			t.Fatalf("overloaded rest must be fixed and ladder-free: %+v", got)
		}
		f.now = f.now.Add(overloadedRest)
	}
	probe := f.start("k2")
	probe.Finish(Overloaded) // half-open probe re-applies the fixed rest
	got := f.snap()
	if got.State != Open || got.ConsecutiveFailures != 0 || !got.OpenUntil.Equal(f.now.Add(overloadedRest)) {
		t.Fatalf("overloaded rest must re-apply outside the ladder: %+v", got)
	}
	if _, err := f.tryStart("k3"); err == nil {
		t.Fatal("overloaded rest must block selection")
	}
	f.now = f.now.Add(overloadedRest)
	a := f.start("k4")
	if got := f.snap(); got.State != HalfOpen {
		t.Fatalf("rest expiry must reach half-open: %+v", got)
	}
	a.Finish(Success)
	if got := f.snap(); got.State != Closed {
		t.Fatalf("trusted success must close: %+v", got)
	}
}

func TestOverloadedDoesNotRescueQuarantine(t *testing.T) {
	f := newAttributionFixture(t, BreakerConfig{FailureThreshold: 2, OpenDuration: time.Minute, HalfOpenTokens: 1})
	inflight := f.start("k1")
	f.engine.SetQuarantined("ch:m", true)
	if _, err := f.tryStart("k2"); err == nil {
		t.Fatal("quarantine must block selection")
	}
	inflight.Finish(Overloaded)
	if got := f.snap(); got.State != Quarantined {
		t.Fatalf("quarantine outranks overloaded rest: %+v", got)
	}
}

func poolPair() []Candidate {
	a := candidate("ch", 0, 1)
	b := a
	a.CredentialID, b.CredentialID = "cred-a", "cred-b"
	a.BindingID, b.BindingID = "b1", "b1"
	a.HealthKey, b.HealthKey = "ch:m", "ch:m"
	return []Candidate{a, b}
}

func TestCredentialCooldownIsMemberScoped(t *testing.T) {
	now := time.Unix(100, 0)
	engine := NewEngine(Config{Now: func() time.Time { return now }, RandN: func(n uint64) uint64 { return 0 }})
	poolPlan := func() *Plan {
		p, err := NewPlan(poolPair(), 2)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	engine.CredentialCooldown("ch", "m", "cred-a", time.Minute)
	first, err := poolPlan().Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	if first.Candidate.CredentialID != "cred-b" {
		t.Fatalf("cooled member selected: %s", first.Candidate.CredentialID)
	}
	first.Finish(Neutral)
	if got := engine.Snapshot("ch:m", "ch"); got.State != Closed {
		t.Fatalf("credential cooldown must not touch channel health: %+v", got)
	}
	now = now.Add(time.Minute)
	second, err := poolPlan().Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	if second.Candidate.CredentialID != "cred-a" {
		t.Fatalf("expired cooldown still excludes member: %s", second.Candidate.CredentialID)
	}
	second.Finish(Neutral)
}

func TestCredentialCooldownExcludesWholeBinding(t *testing.T) {
	now := time.Unix(100, 0)
	engine := NewEngine(Config{Now: func() time.Time { return now }, RandN: func(n uint64) uint64 { return 0 }})
	p, err := NewPlan(poolPair(), 2)
	if err != nil {
		t.Fatal(err)
	}
	engine.CredentialCooldown("ch", "m", "cred-a", time.Minute)
	engine.CredentialCooldown("ch", "m", "cred-b", time.Minute)
	request := normalizeRequestShape(RequestShape{Preference: RoutePreference{AllowFallbacks: true}})
	_, _, decision, err := engine.acquirePlan(&Plan{candidates: p.candidates, tried: map[string]bool{}, policy: DefaultDynamicPolicy(), request: request}, 1)
	if err == nil || decision.Candidates[0].FilterReason != "credential_cooldown" {
		t.Fatalf("all-cooled binding must report credential_cooldown: %+v err=%v", decision, err)
	}
}
