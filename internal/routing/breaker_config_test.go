package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreakerConfigBoundsAndPlanSnapshot(t *testing.T) {
	valid := BreakerConfig{FailureThreshold: 2, OpenDuration: 5 * time.Second, HalfOpenTokens: 2}
	if ValidateBreakerConfig(valid) != nil || ValidateBreakerConfig(DefaultBreakerConfig()) != nil {
		t.Fatal("valid breaker configuration rejected")
	}
	for _, bad := range []BreakerConfig{
		{FailureThreshold: 1, OpenDuration: time.Second, HalfOpenTokens: 1},
		{FailureThreshold: 21, OpenDuration: time.Second, HalfOpenTokens: 1},
		{FailureThreshold: 2, OpenDuration: 999 * time.Millisecond, HalfOpenTokens: 1},
		{FailureThreshold: 2, OpenDuration: 10*time.Minute + time.Millisecond, HalfOpenTokens: 1},
		{FailureThreshold: 2, OpenDuration: time.Second, HalfOpenTokens: 0},
		{FailureThreshold: 2, OpenDuration: time.Second, HalfOpenTokens: 5},
	} {
		if !errors.Is(ValidateBreakerConfig(bad), ErrInvalidPolicy) {
			t.Fatalf("invalid breaker accepted: %+v", bad)
		}
	}
	c := candidate("a", 0, 1)
	c.Breaker = valid
	other := candidate("b", 0, 1)
	other.Breaker = valid
	other.Breaker.HalfOpenTokens = 1
	if _, err := NewPlan([]Candidate{c, other}, 2); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("mixed route revisions entered one plan: %v", err)
	}
}

func TestBreakerAttemptSnapshotAndHalfOpenTokens(t *testing.T) {
	now := time.Unix(100, 0)
	engine := NewEngine(Config{Now: func() time.Time { return now }, RandN: func(n uint64) uint64 { return 0 }})
	c := candidate("a", 0, 1)
	c.HealthKey = "channel:a:model:m"
	c.Breaker = BreakerConfig{FailureThreshold: 2, OpenDuration: 5 * time.Second, HalfOpenTokens: 2}
	start := func() (*Attempt, error) {
		plan, err := NewPlan([]Candidate{c}, 2)
		if err != nil {
			t.Fatal(err)
		}
		return plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	}
	first, err := start()
	if err != nil {
		t.Fatal(err)
	}
	first.Finish(Failure)
	second, err := start()
	if err != nil {
		t.Fatal(err)
	}
	second.Finish(Failure)
	snapshot := engine.Snapshot(c.HealthKey, c.ChannelID)
	if snapshot.State != Open || !snapshot.OpenUntil.Equal(now.Add(5*time.Second)) {
		t.Fatalf("versioned threshold/open duration not applied: %+v", snapshot)
	}
	if _, err = start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("open circuit admitted request: %v", err)
	}
	now = now.Add(5 * time.Second)
	probe1, err := start()
	if err != nil {
		t.Fatal(err)
	}
	probe2, err := start()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = start(); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("half-open token bound not applied: %v", err)
	}
	probe1.Finish(Neutral)
	probe3, err := start()
	if err != nil {
		t.Fatal(err)
	}
	probe2.Finish(Success)
	probe3.Finish(Failure) // The successful probe closes a newer generation.
	if got := engine.Snapshot(c.HealthKey, c.ChannelID); got.State != Closed || got.ConsecutiveFailures != 0 || got.ProbeInFlight {
		t.Fatalf("stale half-open completion changed closed state: %+v", got)
	}
}
