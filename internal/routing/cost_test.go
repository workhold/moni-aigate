package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/routecost"
)

// Per-million fixture input only; production selectors never reconstruct it.
func testCostQuote(channel, model, version, basis string, input, output int64) routecost.Quote {
	s, err := metering.ParseSchema([]byte(`{"schema_id":"10000000-0000-4000-8000-000000000001","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"9223372036854775807"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"9223372036854775807"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	if err != nil {
		panic(err)
	}
	in := routecost.Input{ChannelID: channel, UpstreamModel: model, VersionID: version, Basis: basis, VariantKey: "default", Schema: s}
	if basis != "unknown" {
		in.Rates, err = metering.LegacyTokenRates(s, metering.Supplier, input, output)
		if err != nil {
			panic(err)
		}
	}
	q, err := routecost.NewQuote(in)
	if err != nil {
		panic(err)
	}
	return q
}
func testCostCaps(q routecost.Quote, input, output *int64) routecost.Limits {
	entries := []metering.Rate{}
	for i, ptr := range []*int64{input, output} {
		if ptr == nil {
			continue
		}
		rates, err := metering.LegacyTokenRates(q.Schema(), metering.Supplier, *ptr, *ptr)
		if err != nil {
			panic(err)
		}
		rows, _ := rates.Entries()
		entries = append(entries, rows[i])
	}
	caps, err := routecost.NewLimits(q.Schema(), entries)
	if err != nil {
		panic(err)
	}
	return caps
}
func costRate(n int64) *int64 { return &n }
func pricedCandidate(id string, input, output int64) Candidate {
	c := candidate(id, 10, 1)
	c.CostPolicy = CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}
	c.CostQuote = testCostQuote(id, "m", "quote-"+id, "contract", input, output)
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
	unknown := testCostQuote("unknown", "m", "", "unknown", 0, 0)
	knownZero := testCostQuote("zero", "m", "zero", "invoice", 0, 0)
	for _, tc := range []struct {
		q      routecost.Quote
		caps   routecost.Limits
		policy CostPolicy
		want   string
	}{
		{unknown, routecost.Limits{}, policy, ""},
		{unknown, testCostCaps(unknown, costRate(0), nil), policy, "cost_unknown"},
		{knownZero, testCostCaps(knownZero, costRate(0), costRate(0)), policy, ""},
		{unknown, routecost.Limits{}, CostPolicy{Mode: "manual", UnknownCostPolicy: "deny"}, "cost_unknown"},
	} {
		if reason, err := CostExclusion(tc.policy, tc.q, tc.caps); err != nil || reason != tc.want {
			t.Fatal("unknown was treated as zero or policy changed", reason, err)
		}
	}
	unknownCandidate := pricedCandidate("unknown", 1, 1)
	unknownCandidate.CostQuote = unknown
	overCap := pricedCandidate("over-cap", 1, 5)
	overCap.CostLimits = testCostCaps(overCap.CostQuote, nil, costRate(4))
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

func TestCostExclusionRejectsInvalidValuesWithoutUnknownDowngrade(t *testing.T) {
	q := testCostQuote("channel", "m", "v", "contract", 1, 1)
	raw, _ := q.Schema().CanonicalJSON()
	other, err := metering.ParseSchema([]byte(strings.Replace(string(raw), "9223372036854775807", "100", 1)))
	if err != nil {
		t.Fatal(err)
	}
	wrongCaps, err := routecost.NewLimits(other, []metering.Rate{{Dimension: "text_input_token", Unit: "token", Numerator: 0, Denominator: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		policy CostPolicy
		q      routecost.Quote
		caps   routecost.Limits
		want   error
	}{
		{CostPolicy{}, q, routecost.Limits{}, ErrInvalidPolicy},
		{DefaultCostPolicy(), routecost.Quote{}, routecost.Limits{}, routecost.ErrInvalidQuote},
		{DefaultCostPolicy(), q, wrongCaps, routecost.ErrInvalidLimits},
	} {
		if reason, err := CostExclusion(tc.policy, tc.q, tc.caps); reason != "" || !errors.Is(err, tc.want) {
			t.Fatal("invalid original became a business exclusion", reason, err)
		}
	}
}

func TestCostPlanRejectsWrongDomainSchemaCapsAndBounds(t *testing.T) {
	base := pricedCandidate("a", 1, 1)
	raw, _ := base.CostQuote.Schema().CanonicalJSON()
	small, err := metering.ParseSchema([]byte(strings.ReplaceAll(string(raw), "9223372036854775807", "10")))
	if err != nil {
		t.Fatal(err)
	}
	rates, err := metering.LegacyTokenRates(small, metering.Supplier, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	smallQuote, err := routecost.NewQuote(routecost.Input{ChannelID: "a", UpstreamModel: "m", VersionID: "v", Basis: "contract", VariantKey: "default", Schema: small, Rates: rates})
	if err != nil {
		t.Fatal(err)
	}
	smallCaps, err := routecost.NewLimits(small, []metering.Rate{{Dimension: "text_input_token", Unit: "token", Numerator: 1, Denominator: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Candidate){
		"channel":         func(c *Candidate) { c.ChannelID = "other" },
		"upstream":        func(c *Candidate) { c.Model = "other" },
		"cap_schema":      func(c *Candidate) { c.CostLimits = smallCaps },
		"input_overflow":  func(c *Candidate) { c.CostQuote = smallQuote; c.InputUpperBound = 11; c.OutputLimit = 1 },
		"output_overflow": func(c *Candidate) { c.CostQuote = smallQuote; c.InputUpperBound = 1; c.OutputLimit = 11 },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			if _, err := NewPlan([]Candidate{c}, 1); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatal("invalid domain accepted", err)
			}
		})
	}
	// Same schema UUID, different complete definition: neither quote identity
	// nor a cross-channel plan may collapse these into one schema.
	base.InputUpperBound, base.OutputLimit = 1, 1
	second := base
	second.ChannelID = "second"
	second.CostQuote, err = routecost.NewQuote(routecost.Input{ChannelID: "second", UpstreamModel: "m", VersionID: "v", Basis: "contract", VariantKey: "default", Schema: small, Rates: rates})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewPlan([]Candidate{base, second}, 1); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("same UUID hid schema mismatch", err)
	}
}

func TestCostExactRationalsDoNotRequirePerMillionMirrors(t *testing.T) {
	makeQuote := func(c Candidate, numerator, denominator int64) routecost.Quote {
		s := c.CostQuote.Schema()
		id, _ := s.ID()
		raw := fmt.Sprintf(`{"schema_id":%q,"version":"1","side":"supplier","currency":"CNY","rates":[{"dimension":"text_input_token","unit":"token","numerator":"%d","denominator":"%d"},{"dimension":"text_output_token","unit":"token","numerator":"0","denominator":"1"}]}`, id, numerator, denominator)
		r, err := metering.ParseRates(s, metering.Supplier, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		q, err := routecost.NewQuote(routecost.Input{ChannelID: c.ChannelID, UpstreamModel: c.Model, VersionID: "original", Basis: "contract", VariantKey: "default", Schema: s, Rates: r})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	a, b := pricedCandidate("one-third", 0, 0), pricedCandidate("two-sevenths", 0, 0)
	a.InputUpperBound, a.OutputLimit, b.InputUpperBound, b.OutputLimit = 1, 0, 1, 0
	a.CostQuote, b.CostQuote = makeQuote(a, 1, 3), makeQuote(b, 2, 7)
	for _, cap := range []struct {
		n, d int64
		want string
	}{{1, 3, ""}, {3, 10, "cost_limit_exceeded"}} {
		limits, err := routecost.NewLimits(a.CostQuote.Schema(), []metering.Rate{{Dimension: "text_input_token", Unit: "token", Numerator: cap.n, Denominator: cap.d}})
		if err != nil {
			t.Fatal(err)
		}
		if reason, err := CostExclusion(a.CostPolicy, a.CostQuote, limits); err != nil || reason != cap.want {
			t.Fatal("inexact cap comparison", reason, err)
		}
	}
	selected, err := plan(t, a, b).Start(context.Background(), NewEngine(Config{RandN: func(uint64) uint64 { return 0 }}), admit)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Finish(Neutral)
	if selected.Candidate.ChannelID != b.ChannelID {
		t.Fatal("equal ceil values hid different exact costs")
	}
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
	base.CostLimits = testCostCaps(base.CostQuote, costRate(2), nil)
	p := plan(t, base)
	rates, _ := base.CostQuote.Rates()
	entries, _ := rates.Entries()
	entries[0].Numerator = 999
	caps := base.CostLimits.Entries()
	caps[0].Numerator = 0
	a, err := p.Start(context.Background(), NewEngine(Config{}), admit)
	if err != nil || !a.Candidate.CostQuote.Equal(testCostQuote("a", "m", "quote-a", "contract", 2, 3)) || a.Candidate.CostLimits.Entries()[0].Numerator != 1 {
		t.Fatal("caller mutated frozen purchase policy", err)
	}
	a.Finish(Neutral)
	for _, mutate := range []func(*Candidate){func(c *Candidate) { c.CostPolicy.UnknownCostPolicy = "allow" }, func(c *Candidate) { c.InputUpperBound = 0 }, func(c *Candidate) { c.OutputLimit = -1 }, func(c *Candidate) {
		c.CostLimits = testCostCaps(testCostQuote("x", "m", "x", "contract", 1, 1), costRate(0), nil)
		c.CostQuote = routecost.Quote{}
	}, func(c *Candidate) { c.CostQuote = routecost.Quote{} }} {
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
	two.CostQuote = testCostQuote("pool", "m", "different", "contract", 1, 1)
	if _, err := NewPlan([]Candidate{one, two}, 2); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("pool mixed purchase quotes", err)
	}
}
