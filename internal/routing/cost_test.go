package routing

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func costRate(n int64) *int64 { return &n }
func pricedCandidate(id string, input, output int64) Candidate {
	c := candidate(id, 10, 1)
	c.CostPolicy = CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}
	c.CostQuote = CostQuote{VersionID: "quote-" + id, InputRate: costRate(input), OutputRate: costRate(output), Basis: "contract"}
	c.InputUpperBound, c.OutputLimit = 100, 20
	return c
}

func TestCostFirstUsesBothDimensionsWithoutInt64OverflowOrRounding(t *testing.T) {
	cases := []struct {
		name          string
		input, output int64
		a, b          Candidate
		want          string
	}{
		{"both_dimensions", 100, 20, pricedCandidate("low-input", 1, 100), pricedCandidate("low-total", 10, 1), "low-total"},
		{"large_exact", math.MaxInt64, math.MaxInt64, pricedCandidate("higher", math.MaxInt64, math.MaxInt64), pricedCandidate("lower", math.MaxInt64, math.MaxInt64-1), "lower"},
		{"do_not_round_score", 1, 0, pricedCandidate("two", 2, 0), pricedCandidate("one", 1, 0), "one"},
		{"input_only", 100, 0, pricedCandidate("expensive", 2, 0), pricedCandidate("cheap", 1, 0), "cheap"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tt.a.InputUpperBound, tt.a.OutputLimit = tt.input, tt.output
			tt.b.InputUpperBound, tt.b.OutputLimit = tt.input, tt.output
			engine := NewEngine(Config{RandN: func(uint64) uint64 { return 0 }})
			a, err := plan(t, tt.a, tt.b).Start(context.Background(), engine, admit)
			if err != nil || a.Candidate.ChannelID != tt.want {
				t.Fatalf("wrong procurement comparison: %v %v", a, err)
			}
			a.Finish(Neutral)
		})
	}
}

func TestCostFirstKeepsPriorityThenWeightedTiesAndPoolWeights(t *testing.T) {
	low := pricedCandidate("lower-priority-cheap", 0, 0)
	low.Priority = 0
	main := pricedCandidate("main", 20, 20)
	a, err := plan(t, low, main).Start(context.Background(), NewEngine(Config{}), admit)
	if err != nil || a.Candidate.ChannelID != "main" {
		t.Fatalf("cost preference bypassed explicit priority: %v %v", a, err)
	}
	a.Finish(Neutral)
	first := pricedCandidate("large", 5, 10)
	first.CredentialID = "a"
	first.CredentialWeight = 2
	second := first
	second.CredentialID = "b"
	second.CredentialWeight = 5
	small := pricedCandidate("small", 6, 5)
	small.CredentialID = "only"
	small.Weight = 3 // Both scores are 700.
	for roll, want := range []string{"large", "small", "small", "small"} {
		var draws []uint64
		engine := NewEngine(Config{RandN: func(n uint64) uint64 {
			draws = append(draws, n)
			if len(draws) == 1 {
				return uint64(roll)
			}
			return n - 1
		}})
		a, err := plan(t, first, second, small).Start(context.Background(), engine, admit)
		if err != nil || a.Candidate.ChannelID != want || draws[0] != 4 {
			t.Fatalf("cost tie changed channel weights: %v %v %v", a, draws, err)
		}
		if want == "large" && (a.Candidate.CredentialID != "b" || !reflect.DeepEqual(draws, []uint64{4, 7})) {
			t.Fatalf("cost tie changed pool weights: %v", draws)
		}
		a.Finish(Neutral)
	}
}

func TestCostUnknownAndUnitCapsAreHardFilters(t *testing.T) {
	policy := DefaultCostPolicy()
	unknown := CostQuote{Basis: "unknown"}
	knownZero := CostQuote{VersionID: "zero", InputRate: costRate(0), OutputRate: costRate(0), Basis: "invoice"}
	if CostExclusion(policy, unknown, nil, nil) != "" || CostExclusion(policy, unknown, costRate(0), nil) != "cost_unknown" || CostExclusion(policy, knownZero, costRate(0), costRate(0)) != "" {
		t.Fatal("unknown was treated as zero or legacy default changed")
	}
	for _, quote := range []CostQuote{unknown, {VersionID: "partial", InputRate: costRate(1), Basis: "contract"}, {VersionID: "wrong-basis", InputRate: costRate(1), OutputRate: costRate(1), Basis: "unknown"}} {
		if CostExclusion(CostPolicy{Mode: "manual", UnknownCostPolicy: "deny"}, quote, nil, nil) != "cost_unknown" {
			t.Fatal("managed route accepted unknown cost")
		}
	}
	unknownCandidate := pricedCandidate("unknown", 1, 1)
	unknownCandidate.CostQuote = unknown
	overCap := pricedCandidate("over-cap", 1, 5)
	overCap.MaxOutputCostRate = costRate(4)
	known := pricedCandidate("known", 10, 10)
	a, err := plan(t, unknownCandidate, overCap, known).Start(context.Background(), NewEngine(Config{}), admit)
	if err != nil || a.Candidate.ChannelID != "known" || a.Candidate.Decisions[0].Reason != "cost_unknown" || a.Candidate.Decisions[1].Reason != "cost_limit_exceeded" {
		t.Fatalf("cost hard filters not explained: %v %v", a, err)
	}
	a.Finish(Neutral)
	if _, err := plan(t, unknownCandidate, overCap).Start(context.Background(), NewEngine(Config{}), admit); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("all cost-rejected routes dispatched: %v", err)
	}
	// Manual/allow still uses priority and weight regardless of the price score.
	manualA, manualB := pricedCandidate("manual-a", 100, 100), pricedCandidate("manual-b", 0, 0)
	manualA.CostPolicy, manualB.CostPolicy = DefaultCostPolicy(), DefaultCostPolicy()
	a, err = plan(t, manualA, manualB).Start(context.Background(), NewEngine(Config{RandN: func(uint64) uint64 { return 0 }}), admit)
	if err != nil || a.Candidate.ChannelID != "manual-a" {
		t.Fatal("manual default began minimizing cost")
	}
	a.Finish(Neutral)
}

func TestCostFirstHealthCapacityAndSafeAttemptBudgetRemain(t *testing.T) {
	cheap, expensive := pricedCandidate("cheap", 1, 1), pricedCandidate("expensive", 2, 2)
	for _, reason := range []string{"capacity_full", "circuit_open", "quarantined"} {
		engine := NewEngine(Config{})
		switch reason {
		case "capacity_full":
			engine.inflight[cheap.ChannelID] = cheap.MaxInflight
		case "circuit_open":
			engine.health[cheap.ChannelID] = &health{state: Open, until: time.Now().Add(time.Hour)}
		case "quarantined":
			engine.health[cheap.ChannelID] = &health{state: Quarantined}
		}
		a, err := plan(t, cheap, expensive).Start(context.Background(), engine, admit)
		if err != nil || a.Candidate.ChannelID != "expensive" || a.Candidate.Decisions[0].Reason != reason {
			t.Fatalf("cheap route bypassed %s: %v %v", reason, a, err)
		}
		a.Finish(Neutral)
	}
	engine := NewEngine(Config{})
	p := plan(t, cheap, expensive)
	a, err := p.Start(context.Background(), engine, admit)
	if err != nil {
		t.Fatal(err)
	}
	a.Finish(Neutral)
	b, err := p.Start(context.Background(), engine, admit)
	if err != nil || b.Candidate.ChannelID != "expensive" {
		t.Fatal("retry reused same channel", err)
	}
	b.Finish(Neutral)
	if _, err = p.Start(context.Background(), engine, admit); !errors.Is(err, ErrAttemptBudget) {
		t.Fatal("cost route expanded attempt budget", err)
	}
	p = plan(t, cheap)
	p.CommitDownstream()
	if _, err = p.Start(context.Background(), engine, admit); !errors.Is(err, ErrCommitted) {
		t.Fatal("cost route replayed committed SSE", err)
	}
}

func TestCostPlanValidatesAndFreezesQuotesAndCaps(t *testing.T) {
	base := pricedCandidate("a", 2, 3)
	base.CredentialID = "first"
	base.MaxInputCostRate = costRate(2)
	p := plan(t, base)
	*base.CostQuote.InputRate = 999
	*base.MaxInputCostRate = 0
	a, err := p.Start(context.Background(), NewEngine(Config{}), admit)
	if err != nil || *a.Candidate.CostQuote.InputRate != 2 || *a.Candidate.MaxInputCostRate != 2 {
		t.Fatal("caller mutated frozen purchase policy", err)
	}
	a.Finish(Neutral)
	for _, mutate := range []func(*Candidate){func(c *Candidate) { c.CostPolicy.UnknownCostPolicy = "allow" }, func(c *Candidate) { c.InputUpperBound = 0 }, func(c *Candidate) { c.OutputLimit = -1 }, func(c *Candidate) { c.MaxInputCostRate = costRate(-1) }, func(c *Candidate) { c.CostQuote.InputRate = costRate(-1) }} {
		bad := pricedCandidate("bad", 1, 1)
		mutate(&bad)
		if _, err := NewPlan([]Candidate{bad}, 2); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatal("invalid purchase policy accepted", err)
		}
	}
	one := pricedCandidate("pool", 1, 1)
	one.CredentialID = "first"
	two := one
	two.CredentialID = "second"
	two.CostQuote.VersionID = "different"
	if _, err := NewPlan([]Candidate{one, two}, 2); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("pool mixed purchase quotes", err)
	}
}
