package routing

import (
	"context"
	"errors"
	"testing"
	"time"
)

func dynamicCandidate(id, tier string) Candidate {
	c := candidate(id, 1, 1)
	c.BindingID = "binding-" + id
	c.PhysicalModelID = "physical-" + id
	c.PolicyVersionID = "policy-v1"
	c.Model = "model-" + id
	c.CostQuote = testCostQuote(id, c.Model, "quote-"+id, "contract", 10, 10)
	c.CostPolicy = DefaultCostPolicy()
	c.Physical = PhysicalCapability{ContextLimit: 1000, InputUpperBound: 100, OutputLimit: 20, UsageSemantics: "final", Modalities: []string{"text"}, QualityTier: tier, MaxConcurrency: 10, SupportedParameters: []string{"temperature"}}
	c.InputUpperBound, c.OutputLimit, c.RetailAmount = 100, 20, 10000
	return c
}

func dynamicRequest() RequestShape {
	return RequestShape{InputUpperBound: 100, OutputLimit: 20, RetailAmount: 10000, Preference: RoutePreference{AllowFallbacks: true}, StatsWindowS: 900, StatsMinSamples: 50}
}

func startDynamic(t *testing.T, candidates []Candidate, policy DynamicPolicy, request RequestShape, draw uint64) (*Plan, *Attempt) {
	t.Helper()
	plan, err := NewDynamicPlan(candidates, 2, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(Config{RandN: func(weight uint64) uint64 { return draw % weight }, Now: func() time.Time { return time.Unix(100, 0) }})
	attempt, err := plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return plan, attempt
}

func decisionReason(t *testing.T, plan *Plan, binding string) string {
	t.Helper()
	for _, candidate := range plan.LastDecision().Candidates {
		if candidate.BindingID == binding {
			return candidate.Reason
		}
	}
	t.Fatalf("candidate %s is absent", binding)
	return ""
}

func TestDynamicHardFiltersPrecedePriorityAndScores(t *testing.T) {
	input := "0"
	tests := []struct {
		name    string
		mutate  func(*Candidate)
		policy  DynamicPolicy
		request RequestShape
		want    string
	}{
		{"only", func(c *Candidate) {}, DefaultDynamicPolicy(), func() RequestShape { r := dynamicRequest(); r.Preference.Only = []string{"physical-other"}; return r }(), "not_authorized"},
		{"context", func(c *Candidate) { c.Physical.ContextLimit = 99; c.Physical.InputUpperBound = 99 }, DefaultDynamicPolicy(), dynamicRequest(), "context_too_small"},
		{"output", func(c *Candidate) { c.Physical.OutputLimit = 19 }, DefaultDynamicPolicy(), dynamicRequest(), "output_limit_too_small"},
		{"tools", func(c *Candidate) {}, DefaultDynamicPolicy(), func() RequestShape { r := dynamicRequest(); r.Tools = true; return r }(), "tools_unsupported"},
		{"parameters", func(c *Candidate) {}, DefaultDynamicPolicy(), func() RequestShape {
			r := dynamicRequest()
			r.Parameters = []string{"seed"}
			r.Preference.RequireParameters = true
			return r
		}(), "capability_missing"},
		{"quality", func(c *Candidate) { c.Physical.QualityTier = "economy" }, DynamicPolicy{CostPolicy: DefaultCostPolicy(), QualityFloor: "standard"}, dynamicRequest(), "quality_below_floor"},
		{"max_price", func(c *Candidate) {}, DefaultDynamicPolicy(), func() RequestShape { r := dynamicRequest(); r.Preference.MaxPrice = &MaxPrice{Input: &input}; return r }(), "cost_limit_exceeded"},
		{"margin", func(c *Candidate) {}, DefaultDynamicPolicy(), func() RequestShape {
			r := dynamicRequest()
			r.RetailAmount = 100
			r.RetailCurrency = "CNY"
			r.PaymentAmount = 100
			r.PaymentCurrency = "CNY"
			return r
		}(), "margin_below_floor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocked := dynamicCandidate("blocked", "standard")
			blocked.Priority = 100
			tc.mutate(&blocked)
			fallback := dynamicCandidate("other", "standard")
			switch tc.name {
			case "tools":
				fallback.Physical.SupportsTools = true
			case "parameters":
				fallback.Physical.SupportedParameters = []string{"seed"}
			case "max_price":
				fallback.CostQuote = testCostQuote("other", fallback.Model, "quote-other", "contract", 0, 0)
			case "margin":
				blocked.CostQuote = testCostQuote("blocked", blocked.Model, "quote-blocked", "contract", 1000000, 1000000)
			}
			if tc.name == "margin" {
				floor := int64(5000)
				tc.policy.MinMarginRate = &floor
				for i, candidate := range []Candidate{blocked, fallback} {
					candidate.RetailAmount = 100
					if i == 0 {
						blocked = candidate
					} else {
						fallback = candidate
					}
				}
				tc.request.RetailAmount = 100
				tc.request.RetailCurrency = "CNY"
				tc.request.PaymentAmount = 100
				tc.request.PaymentCurrency = "CNY"
			}
			plan, attempt := startDynamic(t, []Candidate{blocked, fallback}, tc.policy, tc.request, 0)
			defer attempt.Finish(Neutral)
			if attempt.Candidate.BindingID != "binding-other" {
				t.Fatal("hard-filtered high-priority candidate was revived")
			}
			if got := decisionReason(t, plan, "binding-blocked"); got != tc.want {
				t.Fatalf("reason=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestDynamicOrderPriceLatencyThroughputAndReplay(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		request    RequestShape
		statsA     StatsSnapshot
	}{
		{name: "order", want: "binding-b", request: func() RequestShape { r := dynamicRequest(); r.Preference.Order = []string{"physical-b"}; return r }()},
		{name: "price", want: "binding-b", request: func() RequestShape { r := dynamicRequest(); r.Preference.Sort = "price"; return r }()},
		{name: "latency", want: "binding-b", request: func() RequestShape { r := dynamicRequest(); r.Preference.Sort = "latency"; return r }(), statsA: StatsSnapshot{SampleCount: 50}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := dynamicCandidate("a", "standard"), dynamicCandidate("b", "standard")
			if tc.name == "price" {
				a.CostQuote = testCostQuote("a", a.Model, "quote-a", "contract", 100, 100)
			}
			if tc.name == "latency" {
				low, high := int64(100), int64(200)
				b.Stats = StatsSnapshot{SampleCount: 50, P50FirstContentUS: &low, P95FirstContentUS: &low}
				a.Stats = StatsSnapshot{SampleCount: 50, P50FirstContentUS: &high, P95FirstContentUS: &high}
			}
			plan, attempt := startDynamic(t, []Candidate{a, b}, DefaultDynamicPolicy(), tc.request, 0)
			defer attempt.Finish(Neutral)
			if attempt.Candidate.BindingID != tc.want {
				t.Fatalf("selected=%s want=%s", attempt.Candidate.BindingID, tc.want)
			}
			replayed, err := ReplayRoute(plan.LastDecision())
			if err != nil || replayed.SelectedBindingID != tc.want {
				t.Fatalf("replay=%+v err=%v", replayed, err)
			}
		})
	}

	a, b := dynamicCandidate("a", "standard"), dynamicCandidate("b", "standard")
	high, low := int64(2000), int64(1000)
	a.Stats = StatsSnapshot{SampleCount: 50, ThroughputMicroS: &high}
	b.Stats = StatsSnapshot{SampleCount: 50, ThroughputMicroS: &low}
	request := dynamicRequest()
	request.Preference.Sort = "throughput"
	plan, attempt := startDynamic(t, []Candidate{a, b}, DefaultDynamicPolicy(), request, 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-a" {
		t.Fatalf("throughput did not use the real statistics snapshot: %s", attempt.Candidate.BindingID)
	}
	if _, err := ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicStickyEstablishInheritAndHealthBreak(t *testing.T) {
	policy := DefaultDynamicPolicy()
	policy.Stickiness = &StickinessPolicy{Enabled: true, MaxAgeS: 60, BreakOn: []string{"health"}}
	a, b := dynamicCandidate("a", "standard"), dynamicCandidate("b", "standard")
	b.Priority = 100
	request := dynamicRequest()
	request.SessionID = "customer-conversation"
	request.Sticky = &StickyTarget{PhysicalModelID: a.PhysicalModelID, ChannelID: a.ChannelID}
	plan, attempt := startDynamic(t, []Candidate{a, b}, policy, request, 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-a" || plan.LastDecision().Sticky != "inherit" || plan.LastDecision().Candidates[0].Reason != "sticky_override" {
		t.Fatalf("sticky decision=%+v", plan.LastDecision())
	}
	if _, err := ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}

	request.Sticky = nil
	plan, attempt = startDynamic(t, []Candidate{a, b}, policy, request, 0)
	attempt.Finish(Neutral)
	if plan.LastDecision().Sticky != "establish" || attempt.Candidate.BindingID != "binding-b" {
		t.Fatal("session was not established from the selected physical candidate")
	}

	engine := NewEngine(Config{Now: func() time.Time { return time.Unix(100, 0) }, RandN: func(uint64) uint64 { return 0 }})
	engine.health[key(a)] = &health{state: Open, until: time.Unix(200, 0)}
	request.Sticky = &StickyTarget{PhysicalModelID: a.PhysicalModelID, ChannelID: a.ChannelID}
	plan, err := NewDynamicPlan([]Candidate{a, b}, 2, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err = plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	attempt.Finish(Neutral)
	if plan.LastDecision().Sticky != "break_health" || attempt.Candidate.BindingID != "binding-b" {
		t.Fatalf("sticky health break=%+v", plan.LastDecision())
	}
}

func TestDynamicAutoSinglePriceAndFallbackRestriction(t *testing.T) {
	policy := DefaultDynamicPolicy()
	policy.Scoring = ScoringPolicy{Auto: true, AllowDegradation: true}
	premium, economy := dynamicCandidate("premium", "premium"), dynamicCandidate("economy", "economy")
	economy.CostQuote = testCostQuote("economy", economy.Model, "quote-economy", "contract", 1, 1)
	request := dynamicRequest()
	request.Preference.Sort = "price"
	plan, attempt := startDynamic(t, []Candidate{premium, economy}, policy, request, 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-economy" || plan.LastDecision().Degradation != "tier_changed" || plan.LastDecision().Request.RetailAmount != 10000 {
		t.Fatalf("auto decision=%+v", plan.LastDecision())
	}
	if _, err := ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}

	policy.Scoring.AllowDegradation = false
	plan, attempt = startDynamic(t, []Candidate{premium, economy}, policy, request, 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-premium" || plan.LastDecision().Degradation != "none" {
		t.Fatal("auto silently degraded without an explicit policy")
	}
}

func TestDynamicMaxPriceRejectsUnknownRatherThanFree(t *testing.T) {
	unknown := dynamicCandidate("unknown", "standard")
	unknown.Priority = 100
	unknown.CostQuote = testCostQuote("unknown", unknown.Model, "", "unknown", 0, 0)
	known := dynamicCandidate("known", "standard")
	request := dynamicRequest()
	limit := "1000000"
	request.Preference.MaxPrice = &MaxPrice{Input: &limit}
	plan, attempt := startDynamic(t, []Candidate{unknown, known}, DefaultDynamicPolicy(), request, 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-known" || decisionReason(t, plan, "binding-unknown") != "cost_unknown" {
		t.Fatal("unknown price bypassed max_price")
	}
	if _, err := ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicStickyQualityAndCostBreaks(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{{"quality", "break_quality"}, {"cost", "break_cost"}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := dynamicCandidate("a", "standard"), dynamicCandidate("b", "premium")
			policy := DefaultDynamicPolicy()
			policy.Scoring = ScoringPolicy{Auto: true, AllowDegradation: true}
			policy.Stickiness = &StickinessPolicy{Enabled: true, MaxAgeS: 60, BreakOn: []string{tc.name}}
			if tc.name == "quality" {
				policy.QualityFloor = "premium"
			} else {
				a.CostQuote = testCostQuote("a", a.Model, "", "unknown", 0, 0)
				policy.CostPolicy = CostPolicy{Mode: "manual", UnknownCostPolicy: "deny"}
				a.CostPolicy = policy.CostPolicy
				b.CostPolicy = policy.CostPolicy
			}
			request := dynamicRequest()
			request.SessionID = "sticky-break"
			request.Sticky = &StickyTarget{PhysicalModelID: a.PhysicalModelID, ChannelID: a.ChannelID}
			plan, attempt := startDynamic(t, []Candidate{a, b}, policy, request, 0)
			attempt.Finish(Neutral)
			if plan.LastDecision().Sticky != tc.kind || attempt.Candidate.BindingID != "binding-b" {
				t.Fatalf("sticky break=%s selected=%s", plan.LastDecision().Sticky, attempt.Candidate.BindingID)
			}
			if _, err := ReplayRoute(plan.LastDecision()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDynamicAutoCannotSilentlyDegradeOnRetry(t *testing.T) {
	premium, economy := dynamicCandidate("premium", "premium"), dynamicCandidate("economy", "economy")
	premium.Priority, economy.Priority = 100, 0
	policy := DefaultDynamicPolicy()
	policy.Scoring = ScoringPolicy{Auto: true, AllowDegradation: false}
	plan, first := startDynamic(t, []Candidate{premium, economy}, policy, dynamicRequest(), 0)
	first.Finish(Neutral)
	_, err := plan.Start(context.Background(), NewEngine(Config{RandN: func(uint64) uint64 { return 0 }}), func(context.Context, Candidate, int) error { return nil })
	if !errors.Is(err, ErrUnavailable) || plan.LastDecision().SelectedBindingID != "" || plan.LastDecision().Degradation != "none" {
		t.Fatalf("silent retry degradation=%+v err=%v", plan.LastDecision(), err)
	}
	if _, err = ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicMixedCredentialBindingReplays(t *testing.T) {
	revoked := dynamicCandidate("shared", "standard")
	active := revoked
	revoked.CredentialID = "revoked"
	revoked.Enabled = false
	revoked.ExclusionReason = "credential_revoked"
	active.CredentialID = "active"
	plan, attempt := startDynamic(t, []Candidate{revoked, active}, DefaultDynamicPolicy(), dynamicRequest(), 0)
	attempt.Finish(Neutral)
	if attempt.Candidate.CredentialID != "active" || plan.LastDecision().Candidates[0].FilterReason != "" {
		t.Fatalf("mixed credential decision=%+v", plan.LastDecision())
	}
	if _, err := ReplayRoute(plan.LastDecision()); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicProcurementPrefilterUsesPhysicalSupplierBound(t *testing.T) {
	candidate := dynamicCandidate("managed", "standard")
	candidate.Physical.ContextLimit = 1100
	candidate.Physical.InputUpperBound = 1000
	candidate.ProcurementManaged = true
	candidate.ProcurementState = "active"
	candidate.ProcurementAvailable = "1"
	candidate.CostQuote = testCostQuote("managed", candidate.Model, "quote-managed", "contract", 1000000, 1000000)
	if got := ProcurementExclusion(candidate); got != "procurement_budget_exceeded" {
		t.Fatalf("procurement reason=%s", got)
	}
}

func TestDynamicZeroRetailPositiveMarginFloorFailsClosed(t *testing.T) {
	candidate := dynamicCandidate("free", "standard")
	candidate.CostQuote = testCostQuote("free", candidate.Model, "quote-free", "contract", 0, 0)
	candidate.RetailAmount = 0
	policy := DefaultDynamicPolicy()
	floor := int64(1)
	policy.MinMarginRate = &floor
	request := dynamicRequest()
	request.RetailAmount = 0
	request.RetailCurrency = "CNY"
	request.PaymentAmount = 0
	request.PaymentCurrency = "CNY"
	plan, err := NewDynamicPlan([]Candidate{candidate}, 1, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Start(context.Background(), NewEngine(Config{}), func(context.Context, Candidate, int) error { return nil })
	if !errors.Is(err, ErrUnavailable) || decisionReason(t, plan, "binding-free") != "margin_below_floor" {
		t.Fatalf("zero retail bypassed margin floor: %+v %v", plan.LastDecision(), err)
	}
}

func TestDynamicMarginRefusesCoinAgainstSupplierCNY(t *testing.T) {
	candidate := dynamicCandidate("coin", "standard")
	candidate.CostQuote = testCostQuote("coin", candidate.Model, "quote-coin", "contract", 0, 0)
	policy := DefaultDynamicPolicy()
	floor := int64(0)
	policy.MinMarginRate = &floor
	request := dynamicRequest()
	request.RetailAmount = 100
	request.RetailCurrency = "COIN"
	plan, err := NewDynamicPlan([]Candidate{candidate}, 1, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Start(context.Background(), NewEngine(Config{}), func(context.Context, Candidate, int) error { return nil })
	if !errors.Is(err, ErrUnavailable) || decisionReason(t, plan, "binding-coin") != "margin_basis_unavailable" || plan.LastDecision().AlgorithmVersion != DynamicAlgorithmVersion {
		t.Fatalf("cross-currency margin was treated as cash profit: %+v %v", plan.LastDecision(), err)
	}
}

func TestDynamicV1ReplayKeepsNumericMargin(t *testing.T) {
	candidate := dynamicCandidate("legacy", "standard")
	candidate.CostQuote = testCostQuote("legacy", candidate.Model, "quote-legacy", "contract", 0, 0)
	policy := DefaultDynamicPolicy()
	floor := int64(0)
	policy.MinMarginRate = &floor
	request := dynamicRequest()
	request.RetailAmount = 100
	request.RetailCurrency = "COIN"
	plan, err := NewDynamicPlan([]Candidate{candidate}, 1, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	plan.algorithm = "dynamic-v1"
	_, err = plan.Start(context.Background(), NewEngine(Config{}), func(context.Context, Candidate, int) error { return nil })
	if err != nil || plan.LastDecision().AlgorithmVersion != "dynamic-v1" || plan.LastDecision().SelectedBindingID != "binding-legacy" {
		t.Fatalf("historical numeric margin changed: %+v %v", plan.LastDecision(), err)
	}
	replayed, err := ReplayRoute(plan.LastDecision())
	if err != nil || replayed.SelectedBindingID != "binding-legacy" {
		t.Fatalf("v1 replay=%+v %v", replayed, err)
	}
}

func TestConservativeCNYPaymentUsesPaidLotsOnly(t *testing.T) {
	amount, covered := ConservativeCNYPayment(0, nil)
	if amount != 0 || !covered {
		t.Fatalf("zero reservation %+v %v", amount, covered)
	}
	paid := PaidLot{Coin: 10, ReceiptCoin: 10, FiatAmount: 5, Basis: "paid", FiatCurrency: "CNY", RateNum: 1, RateDen: 1}
	amount, covered = ConservativeCNYPayment(10, []PaidLot{paid})
	if amount != 5 || !covered {
		t.Fatalf("discounted fiat basis %+v %v", amount, covered)
	}
	paid.Coin, paid.ReceiptCoin, paid.FiatAmount = 40, 100, 50
	grant := PaidLot{Coin: 60, Basis: "grant"}
	amount, covered = ConservativeCNYPayment(100, []PaidLot{paid, grant})
	if amount != 20 || !covered {
		t.Fatalf("mixed paid and grant basis %+v %v", amount, covered)
	}
	if _, covered = ConservativeCNYPayment(10, []PaidLot{{Coin: 10, Basis: "grant"}}); covered {
		t.Fatal("grant coins were treated as paid CNY")
	}
	if _, covered = ConservativeCNYPayment(10, []PaidLot{{Coin: 10, ReceiptCoin: 10, FiatAmount: 10, Basis: "paid", FiatCurrency: "USD", RateNum: 1, RateDen: 1}}); covered {
		t.Fatal("USD payment was converted without an FX policy")
	}
	if _, covered = ConservativeCNYPayment(10, []PaidLot{{Coin: 4, ReceiptCoin: 4, FiatAmount: 4, Basis: "paid", FiatCurrency: "CNY", RateNum: 1, RateDen: 1}}); covered {
		t.Fatal("partial allocation covered the reservation")
	}
}

func TestDynamicSecondAttemptAddsPriorSupplierCost(t *testing.T) {
	first := dynamicCandidate("first", "standard")
	second := dynamicCandidate("second", "standard")
	first.Priority = 10
	first.CostQuote = testCostQuote("first", first.Model, "quote-first", "contract", 1000000, 1000000)
	second.CostQuote = testCostQuote("second", second.Model, "quote-second", "contract", 1000000, 1000000)
	policy := DefaultDynamicPolicy()
	floor := int64(0)
	policy.MinMarginRate = &floor
	request := dynamicRequest()
	request.PaymentAmount = 200
	request.PaymentCurrency = "CNY"
	plan, err := NewDynamicPlan([]Candidate{first, second}, 2, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(Config{})
	attempt, err := plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if err != nil || attempt.Candidate.BindingID != "binding-first" {
		t.Fatalf("first attempt %+v %v", attempt, err)
	}
	attempt.Finish(Failure)
	_, err = plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if !errors.Is(err, ErrUnavailable) || decisionReason(t, plan, "binding-second") != "margin_below_floor" {
		t.Fatalf("second attempt ignored prior cost: %+v %v", plan.LastDecision(), err)
	}
}

func TestDynamicNoConsumptionDoesNotCarrySupplierUpper(t *testing.T) {
	first := dynamicCandidate("cleared", "standard")
	second := dynamicCandidate("next", "standard")
	first.Priority = 10
	first.CostQuote = testCostQuote("cleared", first.Model, "quote-cleared", "contract", 1000000, 1000000)
	second.CostQuote = testCostQuote("next", second.Model, "quote-next", "contract", 1000000, 1000000)
	policy := DefaultDynamicPolicy()
	floor := int64(0)
	policy.MinMarginRate = &floor
	request := dynamicRequest()
	request.PaymentAmount = 200
	request.PaymentCurrency = "CNY"
	plan, err := NewDynamicPlan([]Candidate{first, second}, 2, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(Config{})
	attempt, err := plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	attempt.Finish(Failure)
	attempt.NoteNoSupplierConsumption()
	next, err := plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if err != nil || next.Candidate.BindingID != "binding-next" {
		t.Fatalf("proven no-consumption still blocked the next attempt: %+v %v", plan.LastDecision(), err)
	}
	next.Finish(Neutral)
}

func TestDynamicQualityScoreCannotRaiseTier(t *testing.T) {
	score := 1.0
	blocked := dynamicCandidate("scored", "economy")
	blocked.Physical.QualityScore = &score
	blocked.Priority = 100
	fallback := dynamicCandidate("plain", "standard")
	policy := DefaultDynamicPolicy()
	policy.QualityFloor = "standard"
	plan, attempt := startDynamic(t, []Candidate{blocked, fallback}, policy, dynamicRequest(), 0)
	defer attempt.Finish(Neutral)
	if attempt.Candidate.BindingID != "binding-plain" || decisionReason(t, plan, "binding-scored") != "quality_below_floor" {
		t.Fatalf("quality score raised the tier: %+v", plan.LastDecision())
	}
}

func TestDynamicCircuitReasonPrecedesCost(t *testing.T) {
	candidate := dynamicCandidate("open", "standard")
	zero := int64(0)
	candidate.CostLimits = testCostCaps(candidate.CostQuote, &zero, &zero)
	plan, err := NewDynamicPlan([]Candidate{candidate}, 1, DefaultDynamicPolicy(), dynamicRequest())
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(Config{Now: func() time.Time { return time.Unix(100, 0) }})
	engine.health[key(candidate)] = &health{state: Open, until: time.Unix(200, 0)}
	_, err = plan.Start(context.Background(), engine, func(context.Context, Candidate, int) error { return nil })
	if !errors.Is(err, ErrUnavailable) || decisionReason(t, plan, "binding-open") != "circuit_open" {
		t.Fatalf("cost masked circuit state: %+v %v", plan.LastDecision(), err)
	}
}

func TestDynamicUnknownConsumptionDoesNotGainRetry(t *testing.T) {
	candidate := dynamicCandidate("a", "standard")
	plan, attempt := startDynamic(t, []Candidate{candidate}, DefaultDynamicPolicy(), dynamicRequest(), 0)
	attempt.Finish(Failure)
	if plan.CanRetry(context.Background(), errors.New("unknown consumption")) {
		t.Fatal("an arbitrary failure became retryable")
	}
}
