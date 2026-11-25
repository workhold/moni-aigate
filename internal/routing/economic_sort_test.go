package routing

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/economics"
)

func TestOfferCostFirstUsesFrozenFXValuation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	cnyID, usdID := uuid.NewString(), uuid.NewString()
	taskCandidates := []string{cnyID, usdID}
	sort.Strings(taskCandidates)
	execution := economics.P0ExecutionPolicy(uuid.NewString())
	execution.CreatedBy, execution.CreatedAt = "tester", now
	fx := economics.FxPolicy{ID: uuid.NewString(), ValuationCurrency: economics.CurrencyUSD, Source: "routing-fixture", QuotedAt: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour), CreatedBy: "tester", CreatedAt: now, Rates: []economics.FxRate{{SourceCurrency: economics.CurrencyCNY, TargetCurrency: economics.CurrencyUSD, RateNum: 1, RateDen: 7}}}
	snapshot, err := economics.NewEconomicsSnapshot(economics.EconomicsSnapshotInput{
		TenantID: uuid.NewString(), RequestID: uuid.NewString(), ReservationID: uuid.NewString(), OfferVersionID: uuid.NewString(),
		EconomicsVersionID: uuid.NewString(), ExecutionPolicyVersionID: execution.ID, TaskPolicyVersionID: uuid.NewString(), QualityPolicyVersionID: uuid.NewString(),
		TaskClass: economics.TaskCodingNonReasoning, TaskSource: economics.TaskSourceSKU, TaskCandidates: taskCandidates,
		SettlementMode: execution.SettlementMode, MaxOuterAttempts: 1, ReservationCoinAmount: 10000,
		PaymentBasisMicro: economics.MoneyVector{economics.CurrencyUSD: 1000}, PaymentBasisComplete: true,
		FxPolicyVersionID: fx.ID, ValuationCurrency: fx.ValuationCurrency, FxSnapshot: fx,
		ExecutionPolicy: execution, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	usd := dynamicCandidate("usd", "standard")
	usd.PhysicalModelID = usdID
	usd.CostQuote = testCostQuoteCurrency(usd.ChannelID, usd.Model, uuid.NewString(), "contract", 100000, 1000000, "USD")
	usd.CostPolicy.Mode = "cost_first"
	usd.CostPolicy.UnknownCostPolicy = "deny"
	cny := dynamicCandidate("cny", "standard")
	cny.PhysicalModelID = cnyID
	cny.CostQuote = testCostQuoteCurrency(cny.ChannelID, cny.Model, uuid.NewString(), "contract", 500000, 1000000, "CNY")
	cny.CostPolicy.Mode = "cost_first"
	cny.CostPolicy.UnknownCostPolicy = "deny"
	request := dynamicRequest()
	request.Economics = &snapshot
	request.TaskClass = string(economics.TaskCodingNonReasoning)
	request.TaskCandidates = taskCandidates
	request.PriorCost = economics.CostBasis{Vector: economics.MoneyVector{}, Complete: true}
	policy := DefaultDynamicPolicy()
	policy.Mode, policy.UnknownCostPolicy = "cost_first", "deny"
	if err := request.Validate(); err != nil {
		t.Fatal("request", err)
	}
	if err := policy.Validate(); err != nil {
		t.Fatal("policy", err)
	}
	if !usd.CostQuote.Matches(usd.ChannelID, usd.Model) || !usd.CostLimits.ValidFor(usd.CostQuote) {
		t.Fatal("USD quote identity mismatch")
	}
	if err := usd.Physical.Validate(); err != nil {
		t.Fatal("USD physical capability", err)
	}
	if err := ValidateCostPolicy(usd.CostPolicy); err != nil {
		t.Fatal("USD cost policy", err)
	}
	for _, candidate := range []Candidate{usd, cny} {
		if _, candidateErr := NewDynamicPlan([]Candidate{candidate}, 1, policy, request); candidateErr != nil {
			t.Fatalf("candidate %s invalid: %v", candidate.PhysicalModelID, candidateErr)
		}
	}
	plan, err := NewDynamicPlan([]Candidate{usd, cny}, 1, policy, request)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := plan.Start(context.Background(), NewEngine(Config{RandN: func(weight uint64) uint64 { return 0 }}), func(context.Context, Candidate, int) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Finish(Success)
	for _, candidate := range plan.LastDecision().Candidates {
		t.Logf("candidate=%s reason=%s upper=%v valuation=%v", candidate.PhysicalModelID, candidate.Reason, *candidate.CostSnapshot.UpperAmount, *candidate.ValuationCostUpper)
	}
	if attempt.Candidate.PhysicalModelID != cnyID {
		t.Fatalf("selected %s (CNY=%s USD=%s); CNY cost 70 converts to USD 10 and must beat USD 30", attempt.Candidate.PhysicalModelID, cnyID, usdID)
	}
	for _, candidate := range plan.LastDecision().Candidates {
		t.Logf("candidate=%s reason=%s upper=%v valuation=%v", candidate.PhysicalModelID, candidate.Reason, *candidate.CostSnapshot.UpperAmount, *candidate.ValuationCostUpper)
		if candidate.ValuationCostUpper == nil {
			t.Fatalf("missing valuation cost for %s", candidate.PhysicalModelID)
		}
		if candidate.PhysicalModelID == cnyID && *candidate.ValuationCostUpper != "10" || candidate.PhysicalModelID == usdID && *candidate.ValuationCostUpper != "30" {
			t.Fatalf("wrong converted cost for %s: %s", candidate.PhysicalModelID, *candidate.ValuationCostUpper)
		}
	}
}
