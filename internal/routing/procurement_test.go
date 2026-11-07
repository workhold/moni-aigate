package routing

import (
	"context"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"math"
	"testing"
)

func TestProcurementFiltersManualRoutesUsingExactSharedBudget(t *testing.T) {
	for _, tc := range []struct {
		name, available, state, want string
		rate                         int64
	}{
		{"round_up", "0", "active", "procurement_budget_exceeded", 1},
		{"exact_zero", "0", "active", "", 0},
		{"paused", "100000", "paused", "procurement_paused", 1},
		{"overcommitted", "-1", "active", "procurement_budget_exceeded", 0},
		{"unknown_budget", "", "active", "procurement_budget_unavailable", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := pricedCandidate("managed", tc.rate, tc.rate)
			c.CostPolicy = DefaultCostPolicy()
			c.ProcurementManaged = true
			c.ProcurementAvailable = tc.available
			c.ProcurementState = tc.state
			if got := ProcurementExclusion(c); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
	a := pricedCandidate("exhausted", 1, 1)
	a.ProcurementManaged = true
	a.ProcurementState = "active"
	a.ProcurementAvailable = "0"
	b := pricedCandidate("available", 2, 2)
	b.ProcurementManaged = true
	b.ProcurementState = "active"
	b.ProcurementAvailable = "100"
	attempt, err := plan(t, a, b).Start(context.Background(), NewEngine(Config{}), admit)
	if err != nil || attempt.Candidate.ChannelID != "available" || attempt.Candidate.Decisions[0].Reason != "procurement_budget_exceeded" {
		t.Fatal(attempt, err)
	}
	attempt.Finish(Neutral)
	a.InputUpperBound = math.MaxInt64
	a.OutputLimit = math.MaxInt64
	a.CostQuote = testCostQuote("exhausted", "m", "cost", "contract", math.MaxInt64, math.MaxInt64)
	if got := ProcurementExclusion(a); got != "procurement_bound_invalid" {
		t.Fatal("large hard bound wrapped", got)
	}
	a.CostQuote = routecost.Quote{}
	if got := ProcurementExclusion(a); got != "cost_unknown" {
		t.Fatal(got)
	}
}
