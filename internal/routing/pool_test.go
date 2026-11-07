package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

func poolMember(channel, credential string) Candidate {
	c := candidate(channel, 10, 1)
	c.CredentialID = credential
	c.HealthKey = credential + ":" + c.Model
	return c
}

func TestPoolChannelWeightIsIndependentOfMemberCount(t *testing.T) {
	members := []Candidate{}
	for i := range 32 {
		members = append(members, poolMember("large", fmt.Sprintf("large-%02d", i)))
	}
	members = append(members, poolMember("small", "small-0"))
	for _, selectLarge := range []bool{false, true} {
		t.Run(fmt.Sprint(selectLarge), func(t *testing.T) {
			var draws []uint64
			engine := NewEngine(Config{RandN: func(n uint64) uint64 {
				draws = append(draws, n)
				if len(draws) == 1 && selectLarge {
					return 0
				}
				return n - 1
			}})
			a, err := plan(t, members...).Start(context.Background(), engine, admit)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Finish(Neutral)
			wantCredential, wantDraws := "small-0", []uint64{2}
			if selectLarge {
				wantCredential, wantDraws = "large-31", []uint64{2, 32}
			}
			if a.Candidate.CredentialID != wantCredential || !reflect.DeepEqual(draws, wantDraws) {
				t.Fatalf("selected=%s draws=%v", a.Candidate.CredentialID, draws)
			}
			if len(a.Candidate.Decisions) != 2 || a.Candidate.Decisions[0].Weight != 1 || a.Candidate.Decisions[1].Weight != 1 {
				t.Fatalf("pool inflated channel decisions: %+v", a.Candidate.Decisions)
			}
		})
	}
}

func TestPoolMemberWeights(t *testing.T) {
	members := []Candidate{poolMember("a", "first"), poolMember("a", "second"), poolMember("a", "third")}
	members[0].CredentialWeight = 2
	members[1].CredentialWeight = 5
	// A zero credential weight is the backward-compatible default of one.
	for roll, want := range []string{"first", "first", "second", "second", "second", "second", "second", "third"} {
		t.Run(fmt.Sprint(roll), func(t *testing.T) {
			calls := 0
			engine := NewEngine(Config{RandN: func(n uint64) uint64 {
				calls++
				if calls == 1 {
					if n != 1 {
						t.Fatalf("channel weight %d", n)
					}
					return 0
				}
				if n != 8 {
					t.Fatalf("credential weight %d", n)
				}
				return uint64(roll)
			}})
			a, err := plan(t, members...).Start(context.Background(), engine, admit)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Finish(Neutral)
			if a.Candidate.CredentialID != want || calls != 2 {
				t.Fatal(a.Candidate.CredentialID, calls)
			}
		})
	}
}

func TestPoolCredentialCapacitySpansChannelsModelsAndVersions(t *testing.T) {
	engine := NewEngine(Config{})
	first := poolMember("old-channel", "shared")
	first.CredentialMaxInflight = 1
	other := first
	other.ChannelID, other.Model, other.VersionID, other.HealthKey = "new-channel", "other-model", "v2", "shared:other-model"
	other.CostQuote = testCostQuote(other.ChannelID, other.Model, "", "unknown", 0, 0)
	a, err := plan(t, first).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	if engine.CredentialInflight("shared") != 1 {
		t.Fatal("credential capacity was not reserved")
	}
	p := plan(t, other)
	if _, err = p.Start(context.Background(), engine, admit); !errors.Is(err, ErrUnavailable) {
		t.Fatal("credential capacity did not span model/version/channel", err)
	}
	a.Candidate.CredentialID = "mutated"
	a.Finish(Neutral)
	a.Finish(Neutral)
	if engine.CredentialInflight("shared") != 0 {
		t.Fatal("cleanup followed public identity or released twice")
	}
	b, err := p.Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal("capacity not returned", err)
	}
	b.Finish(Neutral)
}

func TestPoolChannelCapacityAndCredentialCapacityAreBothEnforced(t *testing.T) {
	members := []Candidate{poolMember("a", "first"), poolMember("a", "second"), poolMember("a", "third")}
	for i := range members {
		members[i].MaxInflight, members[i].CredentialMaxInflight = 2, 1
	}
	engine := NewEngine(Config{RandN: func(uint64) uint64 { return 0 }})
	a, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	b, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	if a.Candidate.CredentialID != "first" || b.Candidate.CredentialID != "second" {
		t.Fatal("full credential was selected", a.Candidate.CredentialID, b.Candidate.CredentialID)
	}
	if _, err = plan(t, members...).Start(context.Background(), engine, admit); !errors.Is(err, ErrUnavailable) {
		t.Fatal("channel total capacity exceeded", err)
	}
	a.Finish(Neutral)
	c, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal("channel capacity not returned", err)
	}
	b.Finish(Neutral)
	c.Finish(Neutral)
	if engine.Snapshot("first:m", "a").Inflight != 0 || engine.CredentialInflight("first") != 0 || engine.CredentialInflight("second") != 0 {
		t.Fatal("pool leaked capacity")
	}
}

func TestPoolConcurrentSharedCredentialCapacity(t *testing.T) {
	engine := NewEngine(Config{})
	var admitted atomic.Int32
	var started, finished sync.WaitGroup
	started.Add(40)
	finished.Add(40)
	release := make(chan struct{})
	for i := range 40 {
		c := poolMember(fmt.Sprint(i), "shared")
		c.CredentialMaxInflight = 3
		p := plan(t, c)
		go func() {
			defer finished.Done()
			a, err := p.Start(context.Background(), engine, admit)
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrUnavailable) {
				t.Error(err)
			}
			started.Done()
			<-release
			if a != nil {
				a.Finish(Neutral)
				a.Finish(Neutral)
			}
		}()
	}
	started.Wait()
	if admitted.Load() != 3 || engine.CredentialInflight("shared") != 3 {
		t.Error("non-atomic shared credential admission", admitted.Load(), engine.CredentialInflight("shared"))
	}
	close(release)
	finished.Wait()
	if engine.CredentialInflight("shared") != 0 {
		t.Fatal("shared credential leaked capacity")
	}
}

func TestPoolCircuitAndHalfOpenAreIsolatedPerCredential(t *testing.T) {
	now := time.Unix(100, 0)
	engine := NewEngine(Config{FailureThreshold: 1, Now: func() time.Time { return now }, RandN: func(uint64) uint64 { return 0 }})
	members := []Candidate{poolMember("a", "first"), poolMember("a", "second")}
	first, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	first.Finish(Failure)
	second, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil || second.Candidate.CredentialID != "second" {
		t.Fatal("first credential circuit disabled the pool", second, err)
	}
	second.Finish(Success)
	now = now.Add(time.Minute)
	probe, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil || probe.Candidate.CredentialID != "first" {
		t.Fatal("did not acquire first credential probe", probe, err)
	}
	whileProbe, err := plan(t, members...).Start(context.Background(), engine, admit)
	if err != nil || whileProbe.Candidate.CredentialID != "second" {
		t.Fatal("half-open probe blocked unrelated credential", whileProbe, err)
	}
	if !engine.Snapshot("first:m", "a").ProbeInFlight || engine.Snapshot("second:m", "a").State != Closed {
		t.Fatal("probe state crossed credential boundaries")
	}
	engine.Cooldown("first:m", time.Minute)
	probe.Finish(Success)
	whileProbe.Finish(Success)
	if engine.Snapshot("first:m", "a").State != Open || engine.CredentialInflight("first") != 0 {
		t.Fatal("old generation completion changed health or leaked capacity")
	}
}

func TestPoolFiltersRevokedAndQuarantinedMembersAndAggregatesDecisions(t *testing.T) {
	revoked := poolMember("pool", "revoked")
	revoked.Enabled, revoked.ExclusionReason = false, "credential_revoked"
	quarantined := poolMember("pool", "quarantined")
	healthy := poolMember("pool", "healthy")
	fallback := poolMember("fallback", "backup")
	fallback.Priority = 0
	engine := NewEngine(Config{})
	engine.SetQuarantined(quarantined.HealthKey, true)
	a, err := plan(t, revoked, quarantined, healthy, fallback).Start(context.Background(), engine, admit)
	if err != nil || a.Candidate.CredentialID != "healthy" {
		t.Fatal("did not filter unavailable members", a, err)
	}
	if len(a.Candidate.Decisions) != 2 || a.Candidate.Decisions[0].Reason != "selected" {
		t.Fatal("member reasons replaced eligible channel decision", a.Candidate.Decisions)
	}
	a.Finish(Neutral)
	b, err := plan(t, revoked, quarantined, fallback).Start(context.Background(), engine, admit)
	if err != nil || b.Candidate.CredentialID != "backup" {
		t.Fatal("did not fall back after all members unavailable", b, err)
	}
	defer b.Finish(Neutral)
	if len(b.Candidate.Decisions) != 2 || b.Candidate.Decisions[0].Reason != "credential_revoked" {
		t.Fatal("unstable unavailable pool summary", b.Candidate.Decisions)
	}
}

func TestPoolNeverRetriesThroughAnotherMemberOfTheSameChannel(t *testing.T) {
	members := []Candidate{poolMember("a", "first"), poolMember("a", "second"), poolMember("b", "third")}
	engine := NewEngine(Config{RandN: func(uint64) uint64 { return 0 }})
	p := plan(t, members...)
	a, err := p.Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	a.Finish(Failure)
	if !p.CanRetry(context.Background(), &provider.Error{Consumption: provider.ConsumptionNotSent, Class: provider.ClassNetwork}) {
		t.Fatal("safe fallback was rejected")
	}
	b, err := p.Start(context.Background(), engine, admit)
	if err != nil || b.Candidate.ChannelID != "b" || b.Sequence != 2 {
		t.Fatal("retry reused a pool channel", b, err)
	}
	defer b.Finish(Neutral)
	if len(b.Candidate.Decisions) != 2 || b.Candidate.Decisions[0].Reason != "already_tried" {
		t.Fatal(b.Candidate.Decisions)
	}
}

func TestPoolAdmissionPanicAndCancellationReleaseBothLeases(t *testing.T) {
	for _, mode := range []string{"panic", "cancel", "deny"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Unix(100, 0)
			engine := NewEngine(Config{Now: func() time.Time { return now }})
			c := poolMember("a", "credential")
			c.CredentialMaxInflight = 1
			engine.Cooldown(c.HealthKey, time.Second)
			now = now.Add(time.Minute)
			p := plan(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			denied := errors.New("denied")
			func() {
				if mode == "panic" {
					defer func() {
						if recover() != "admit panic" {
							t.Error("admission panic was swallowed")
						}
					}()
				}
				_, err := p.Start(ctx, engine, func(context.Context, Candidate, int) error {
					if engine.CredentialInflight(c.CredentialID) != 1 || !engine.Snapshot(c.HealthKey, c.ChannelID).ProbeInFlight {
						t.Fatal("admission did not own both leases and probe")
					}
					switch mode {
					case "panic":
						panic("admit panic")
					case "cancel":
						cancel()
						return nil
					default:
						return denied
					}
				})
				if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "deny" && !errors.Is(err, denied) {
					t.Fatal(err)
				}
			}()
			snapshot := engine.Snapshot(c.HealthKey, c.ChannelID)
			if p.Attempts() != 0 || engine.CredentialInflight(c.CredentialID) != 0 || snapshot.Inflight != 0 || snapshot.ProbeInFlight || snapshot.State != HalfOpen {
				t.Fatal("untransferred lease leaked", snapshot, p.Attempts())
			}
			a, err := p.Start(context.Background(), engine, admit)
			if err != nil || a.Sequence != 1 {
				t.Fatal("failed admission consumed attempt or probe", a, err)
			}
			a.Finish(Neutral)
			a.Finish(Neutral)
		})
	}
}

func TestPoolLegacyCapacityKeyDoesNotCollideWithRealCredentialID(t *testing.T) {
	legacy := candidate("same-string", 10, 1)
	legacy.MaxInflight = 1
	real := poolMember("other", "same-string")
	real.CredentialMaxInflight = 1
	engine := NewEngine(Config{})
	a, err := plan(t, legacy).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Finish(Neutral)
	b, err := plan(t, real).Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal("legacy channel identity collided with real credential", err)
	}
	b.Finish(Neutral)
}

func TestPoolPolicyValidation(t *testing.T) {
	first, second := poolMember("a", "first"), poolMember("a", "second")
	mutations := map[string]func(*Candidate){
		"version":                      func(c *Candidate) { c.VersionID = "v2" },
		"model":                        func(c *Candidate) { c.Model = "other" },
		"region":                       func(c *Candidate) { c.Region = "other" },
		"priority":                     func(c *Candidate) { c.Priority++ },
		"weight":                       func(c *Candidate) { c.Weight++ },
		"capacity":                     func(c *Candidate) { c.MaxInflight++ },
		"empty credential":             func(c *Candidate) { c.CredentialID = "" },
		"duplicate credential":         func(c *Candidate) { c.CredentialID = first.CredentialID },
		"negative credential capacity": func(c *Candidate) { c.CredentialMaxInflight = -1 },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			bad := second
			change(&bad)
			if _, err := NewPlan([]Candidate{first, bad}, 2); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatal("invalid pool accepted", err)
			}
		})
	}
	legacy := first
	legacy.CredentialID = ""
	if _, err := NewPlan([]Candidate{legacy, second}, 2); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("legacy first member accepted a duplicate channel", err)
	}
	channelOverflow := []Candidate{first, poolMember("b", "other")}
	channelOverflow[0].Weight = math.MaxUint64
	credentialOverflow := []Candidate{first, second}
	credentialOverflow[0].CredentialWeight = math.MaxUint64
	for _, bad := range [][]Candidate{channelOverflow, credentialOverflow} {
		if _, err := NewPlan(bad, 2); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatal("weight overflow accepted", err)
		}
	}
	for _, boundary := range []struct{ channels, members int }{{1, 32}, {1000, 1}} {
		members := []Candidate{}
		for ch := range boundary.channels {
			for member := range boundary.members {
				members = append(members, poolMember(fmt.Sprint(ch), fmt.Sprintf("%d-%d", ch, member)))
			}
		}
		if _, err := NewPlan(members, 2); err != nil {
			t.Fatal("valid boundary rejected", boundary, err)
		}
		if boundary.channels == 1 {
			members = append(members, poolMember("0", "extra"))
		} else {
			members = append(members, poolMember("extra", "extra"))
		}
		if _, err := NewPlan(members, 2); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatal("pool bound exceeded", boundary, err)
		}
	}
	p, err := NewPlan([]Candidate{first, second}, 1)
	if err != nil || p.candidates[0].CredentialWeight != 1 || p.candidates[0].CredentialMaxInflight != first.MaxInflight {
		t.Fatal("credential defaults were not frozen", p, err)
	}
}
