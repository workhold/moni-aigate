package economics

import (
	"errors"
	"math"
	"testing"
)

func known(vector MoneyVector) CostBasis { return CostBasis{Vector: vector, Complete: true} }

func TestCumulativeAdmissionExactMarginBoundary(t *testing.T) {
	request := AdmissionRequest{
		Snapshot: validSnapshot(), PriorCost: known(MoneyVector{CurrencyCNY: 1000}),
		CandidateCost: known(MoneyVector{CurrencyCNY: 8000}),
	}
	decision, err := EvaluateAdmission(request, testNow)
	if err != nil || decision.IncomeLower != 10000 || decision.CumulativeCostUpper != 9000 {
		t.Fatalf("exact margin boundary rejected: %#v %v", decision, err)
	}
	request.CandidateCost = known(MoneyVector{CurrencyCNY: 8001})
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrMarginAdmission) {
		t.Fatalf("margin counterexample accepted: %v", err)
	}
}

func TestAdmissionIncludesAllPriorCosts(t *testing.T) {
	request := AdmissionRequest{
		Snapshot: validSnapshot(), PriorCost: known(MoneyVector{CurrencyCNY: 9000}),
		CandidateCost: known(MoneyVector{}),
	}
	if _, err := EvaluateAdmission(request, testNow); err != nil {
		t.Fatalf("known empty candidate should pass exact boundary: %v", err)
	}
	request.CandidateCost = known(MoneyVector{CurrencyCNY: 1})
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrMarginAdmission) {
		t.Fatalf("prior cost omitted from cumulative admission: %v", err)
	}
}

func TestAdmissionCNYUSDIdentitiesAndCrosses(t *testing.T) {
	for _, test := range []struct {
		name      string
		target    Currency
		basis     MoneyVector
		prior     MoneyVector
		candidate MoneyVector
		income    Money
		cost      Money
	}{
		{name: "CNY identity", target: CurrencyCNY, basis: MoneyVector{CurrencyCNY: 10000}, prior: MoneyVector{}, candidate: MoneyVector{CurrencyCNY: 8000}, income: 10000, cost: 8000},
		{name: "USD identity", target: CurrencyUSD, basis: MoneyVector{CurrencyUSD: 10000}, prior: MoneyVector{}, candidate: MoneyVector{CurrencyUSD: 8000}, income: 10000, cost: 8000},
		{name: "USD income CNY cost", target: CurrencyCNY, basis: MoneyVector{CurrencyUSD: 1000}, prior: MoneyVector{CurrencyCNY: 1000}, candidate: MoneyVector{CurrencyUSD: 500}, income: 7000, cost: 4500},
		{name: "CNY income USD cost", target: CurrencyUSD, basis: MoneyVector{CurrencyCNY: 7000}, prior: MoneyVector{CurrencyUSD: 100}, candidate: MoneyVector{CurrencyCNY: 3500}, income: 1000, cost: 600},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := validSnapshotInput()
			input.ValuationCurrency = test.target
			input.FxSnapshot = validFx(test.target)
			input.PaymentBasisMicro = test.basis
			input.MinMarginRate = 0
			snapshot, err := NewEconomicsSnapshot(input)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := EvaluateAdmission(AdmissionRequest{Snapshot: snapshot, PriorCost: known(test.prior), CandidateCost: known(test.candidate)}, testNow)
			if err != nil || decision.IncomeLower != test.income || decision.CumulativeCostUpper != test.cost {
				t.Fatalf("cross admission: %#v %v", decision, err)
			}
		})
	}
}

func TestAdmissionFeesAndConservativeRounding(t *testing.T) {
	input := validSnapshotInput()
	input.PaymentBasisMicro = MoneyVector{CurrencyUSD: 1}
	input.FxSnapshot.Rates = []FxRate{{SourceCurrency: CurrencyUSD, TargetCurrency: CurrencyCNY, RateNum: 1, RateDen: 3}}
	input.MinMarginRate = 0
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := EvaluateAdmission(AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{}), CandidateCost: known(MoneyVector{})}, testNow)
	if err != nil || decision.IncomeLower != 0 {
		t.Fatalf("income floor: %#v %v", decision, err)
	}
	request := AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{}), CandidateCost: known(MoneyVector{CurrencyUSD: 1})}
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrZeroPrice) {
		t.Fatalf("cost did not round up against zero income: %v", err)
	}
}

func TestAdmissionOptionalCumulativeLimit(t *testing.T) {
	input := validSnapshotInput()
	cap := MoneyVector{CurrencyCNY: 8000}
	input.MaxCumulativeCostMicro = &cap
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{CurrencyCNY: 1000}), CandidateCost: known(MoneyVector{CurrencyCNY: 7000})}
	if _, err = EvaluateAdmission(request, testNow); err != nil {
		t.Fatalf("cumulative limit equality rejected: %v", err)
	}
	request.CandidateCost = known(MoneyVector{CurrencyCNY: 7001})
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrCumulativeCostLimit) {
		t.Fatalf("cumulative cap exceeded: %v", err)
	}
}

func TestAdmissionZeroPrice(t *testing.T) {
	input := validSnapshotInput()
	input.PaymentBasisMicro = MoneyVector{}
	input.MinMarginRate = 0
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{}), CandidateCost: known(MoneyVector{})}
	if _, err = EvaluateAdmission(request, testNow); err != nil {
		t.Fatalf("known zero price and cost rejected: %v", err)
	}
	request.CandidateCost = known(MoneyVector{CurrencyCNY: 1})
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrZeroPrice) {
		t.Fatalf("zero price with cost accepted: %v", err)
	}
	input.MinMarginRate = 1
	snapshot, _ = NewEconomicsSnapshot(input)
	request.Snapshot = snapshot
	request.CandidateCost = known(MoneyVector{})
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrZeroPrice) {
		t.Fatalf("zero price with margin accepted: %v", err)
	}
}

func TestAdmissionUnknownAndInvalidEvidenceFailClosed(t *testing.T) {
	snapshot := validSnapshot()
	request := AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{}), CandidateCost: known(MoneyVector{})}
	request.PriorCost.Complete = false
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrUnknownCost) {
		t.Fatalf("unknown prior cost treated as zero: %v", err)
	}
	request = AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{}), CandidateCost: CostBasis{Vector: MoneyVector{CurrencyCNY: 1}}}
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrUnknownCost) {
		t.Fatalf("unknown candidate treated as zero: %v", err)
	}
	input := validSnapshotInput()
	input.PaymentBasisComplete = false
	snapshot, _ = NewEconomicsSnapshot(input)
	request.Snapshot = snapshot
	request.CandidateCost = known(MoneyVector{})
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrIncompletePaymentBasis) {
		t.Fatalf("unknown payment treated as zero: %v", err)
	}
	input = validSnapshotInput()
	input.FxSnapshot.ValidUntil = testNow
	snapshot, _ = NewEconomicsSnapshot(input)
	request.Snapshot = snapshot
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrStaleFx) {
		t.Fatalf("stale snapshot FX accepted: %v", err)
	}
	input = validSnapshotInput()
	input.PaymentBasisMicro = MoneyVector{CurrencyUSD: 10000}
	input.FxSnapshot.Rates = nil
	snapshot, _ = NewEconomicsSnapshot(input)
	request.Snapshot = snapshot
	if _, err := EvaluateAdmission(request, testNow); !errors.Is(err, ErrMissingFxRate) {
		t.Fatalf("missing FX accepted: %v", err)
	}
}

func TestAdmissionOverflowAndNegativeValues(t *testing.T) {
	input := validSnapshotInput()
	input.PaymentBasisMicro = MoneyVector{CurrencyCNY: math.MaxInt64}
	snapshot, err := NewEconomicsSnapshot(input)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{Snapshot: snapshot, PriorCost: known(MoneyVector{CurrencyCNY: math.MaxInt64}), CandidateCost: known(MoneyVector{CurrencyCNY: 1})}
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrOverflow) {
		t.Fatalf("cumulative overflow accepted: %v", err)
	}
	request.PriorCost = known(MoneyVector{CurrencyCNY: -1})
	request.CandidateCost = known(MoneyVector{})
	if _, err = EvaluateAdmission(request, testNow); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative prior cost accepted: %v", err)
	}
}

func TestAdmissionRejectsFixedExecutionMutation(t *testing.T) {
	input := validSnapshotInput()
	input.ExecutionPolicy.MaxOuterAttempts = 2
	input.MaxOuterAttempts = 2
	_, err := NewEconomicsSnapshot(input)
	if !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("second-attempt policy accepted during snapshot construction: %v", err)
	}
}
