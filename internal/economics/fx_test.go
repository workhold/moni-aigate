package economics

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestFxSameCurrencyIdentityHasNoRateOrFee(t *testing.T) {
	for _, target := range []Currency{CurrencyCNY, CurrencyUSD} {
		policy := validFx(target)
		policy.Rates = nil
		for _, amount := range []Money{0, 1, math.MaxInt64} {
			income, err := policy.IncomeLower(amount, target, testNow)
			if err != nil || income != amount {
				t.Fatalf("%s income identity: %d %v", target, income, err)
			}
			cost, err := policy.CostUpper(amount, target, testNow)
			if err != nil || cost != amount {
				t.Fatalf("%s cost identity: %d %v", target, cost, err)
			}
		}
	}
}

func TestFxCNYUSDCrossesAndFees(t *testing.T) {
	policy := validFx(CurrencyCNY)
	income, err := policy.IncomeLower(1000000, CurrencyUSD, testNow)
	if err != nil || income != 7000000 {
		t.Fatalf("USD/CNY income: %d %v", income, err)
	}
	cost, err := policy.CostUpper(1000000, CurrencyUSD, testNow)
	if err != nil || cost != 7000000 {
		t.Fatalf("USD/CNY cost: %d %v", cost, err)
	}
	policy.ValuationCurrency = CurrencyUSD
	income, err = policy.IncomeLower(7000000, CurrencyCNY, testNow)
	if err != nil || income != 1000000 {
		t.Fatalf("CNY/USD income: %d %v", income, err)
	}
	cost, err = policy.CostUpper(7000000, CurrencyCNY, testNow)
	if err != nil || cost != 1000000 {
		t.Fatalf("CNY/USD cost: %d %v", cost, err)
	}

	policy = validFx(CurrencyCNY)
	policy.Rates[0].FeeBufferPPM = 10000
	income, err = policy.IncomeLower(1000000, CurrencyUSD, testNow)
	if err != nil || income != 6930000 {
		t.Fatalf("fee must reduce income: %d %v", income, err)
	}
	cost, err = policy.CostUpper(1000000, CurrencyUSD, testNow)
	if err != nil || cost != 7070000 {
		t.Fatalf("fee must increase cost: %d %v", cost, err)
	}
}

func TestFxFloorAndCeilCounterexamples(t *testing.T) {
	policy := validFx(CurrencyCNY)
	policy.Rates = []FxRate{{SourceCurrency: CurrencyUSD, TargetCurrency: CurrencyCNY, RateNum: 1, RateDen: 3}}
	income, err := policy.IncomeLower(1, CurrencyUSD, testNow)
	if err != nil || income != 0 {
		t.Fatalf("income must floor: %d %v", income, err)
	}
	cost, err := policy.CostUpper(1, CurrencyUSD, testNow)
	if err != nil || cost != 1 {
		t.Fatalf("cost must ceil: %d %v", cost, err)
	}
	policy.Rates[0].RateDen = 1
	policy.Rates[0].FeeBufferPPM = 100000
	income, err = policy.IncomeLower(1, CurrencyUSD, testNow)
	if err != nil {
		t.Fatal(err)
	}
	cost, err = policy.CostUpper(1, CurrencyUSD, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if income != 0 || cost != 2 {
		t.Fatalf("fee rounding directions: income=%d cost=%d", income, cost)
	}
}

func TestFxMissingReverseAndValidityFailClosed(t *testing.T) {
	policy := validFx(CurrencyUSD)
	policy.Rates = []FxRate{{SourceCurrency: CurrencyUSD, TargetCurrency: CurrencyCNY, RateNum: 7, RateDen: 1}}
	if _, err := policy.IncomeLower(7, CurrencyCNY, testNow); !errors.Is(err, ErrMissingFxRate) {
		t.Fatalf("reverse quote inferred: %v", err)
	}
	if _, err := policy.CostUpper(0, CurrencyCNY, testNow); !errors.Is(err, ErrMissingFxRate) {
		t.Fatalf("explicit zero bypassed missing coverage: %v", err)
	}
	if _, err := policy.IncomeLower(1, CurrencyCNY, policy.QuotedAt.Add(-time.Nanosecond)); !errors.Is(err, ErrFxNotYetValid) {
		t.Fatalf("future quote accepted: %v", err)
	}
	for _, at := range []time.Time{policy.ValidUntil, policy.ValidUntil.Add(time.Second)} {
		if _, err := policy.CostUpper(1, CurrencyCNY, at); !errors.Is(err, ErrStaleFx) {
			t.Fatalf("stale quote accepted: %v", err)
		}
	}
	if _, err := policy.IncomeLower(1, CurrencyUSD, policy.QuotedAt); err != nil {
		t.Fatal("quoted_at lower bound was not inclusive")
	}
}

func TestFxRateValidation(t *testing.T) {
	valid := FxRate{SourceCurrency: CurrencyUSD, TargetCurrency: CurrencyCNY, RateNum: 1, RateDen: 1}
	for name, mutate := range map[string]func(*FxRate){
		"same currency":      func(r *FxRate) { r.TargetCurrency = CurrencyUSD },
		"unknown source":     func(r *FxRate) { r.SourceCurrency = "EUR" },
		"zero numerator":     func(r *FxRate) { r.RateNum = 0 },
		"negative numerator": func(r *FxRate) { r.RateNum = -1 },
		"zero denominator":   func(r *FxRate) { r.RateDen = 0 },
		"negative fee":       func(r *FxRate) { r.FeeBufferPPM = -1 },
		"excess fee":         func(r *FxRate) { r.FeeBufferPPM = 100001 },
	} {
		t.Run(name, func(t *testing.T) {
			rate := valid
			mutate(&rate)
			if !errors.Is(rate.Validate(), ErrInvalidFxRate) {
				t.Fatal("invalid rate accepted")
			}
		})
	}
	valid.FeeBufferPPM = 100000
	if valid.Validate() != nil {
		t.Fatal("maximum fee rejected")
	}
	policy := validFx(CurrencyCNY)
	policy.Rates[1] = policy.Rates[0]
	if !errors.Is(policy.Validate(), ErrInvalidFxRate) {
		t.Fatal("duplicate directed rate accepted")
	}
}

func TestFxOverflowAndNegativeAmounts(t *testing.T) {
	policy := validFx(CurrencyCNY)
	policy.Rates[0].RateNum = 2
	if _, err := policy.IncomeLower(math.MaxInt64, CurrencyUSD, testNow); !errors.Is(err, ErrOverflow) {
		t.Fatalf("income overflow: %v", err)
	}
	if _, err := policy.CostUpper(math.MaxInt64, CurrencyUSD, testNow); !errors.Is(err, ErrOverflow) {
		t.Fatalf("cost overflow: %v", err)
	}
	if _, err := policy.IncomeLower(-1, CurrencyCNY, testNow); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative identity accepted: %v", err)
	}
	policy = validFx(CurrencyUSD)
	policy.Rates[1].RateNum = 1
	policy.Rates[1].RateDen = 1
	vector := MoneyVector{CurrencyUSD: math.MaxInt64, CurrencyCNY: 1}
	if _, err := policy.CostUpperVector(vector, testNow); !errors.Is(err, ErrOverflow) {
		t.Fatalf("vector sum overflow: %v", err)
	}
}
