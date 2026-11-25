package economics

import (
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidFxPolicy = errors.New("invalid FX policy")
	ErrInvalidFxRate   = errors.New("invalid FX rate")
	ErrMissingFxRate   = errors.New("required FX rate is missing")
	ErrFxNotYetValid   = errors.New("FX policy is not yet valid")
	ErrStaleFx         = errors.New("FX policy is stale")
)

// FxRate is one explicit directed rational quote. No inverse is inferred.
type FxRate struct {
	SourceCurrency Currency `json:"source_currency"`
	TargetCurrency Currency `json:"target_currency"`
	RateNum        int64    `json:"rate_num"`
	RateDen        int64    `json:"rate_den"`
	FeeBufferPPM   int64    `json:"fee_buffer_ppm"`
}

func (r FxRate) Validate() error {
	if r.SourceCurrency.Validate() != nil || r.TargetCurrency.Validate() != nil {
		return ErrInvalidFxRate
	}
	if (r.SourceCurrency != CurrencyUSD || r.TargetCurrency != CurrencyCNY) &&
		(r.SourceCurrency != CurrencyCNY || r.TargetCurrency != CurrencyUSD) {
		return ErrInvalidFxRate
	}
	if r.RateNum <= 0 || r.RateDen <= 0 || r.FeeBufferPPM < 0 || r.FeeBufferPPM > 100000 {
		return ErrInvalidFxRate
	}
	return nil
}

// FxPolicy is an immutable-version DTO. Callers must persist versions without
// updating their business fields; conversion methods only read the value.
type FxPolicy struct {
	ID                string    `json:"id"`
	ValuationCurrency Currency  `json:"valuation_currency"`
	Source            string    `json:"source"`
	QuotedAt          time.Time `json:"quoted_at"`
	ValidUntil        time.Time `json:"valid_until"`
	CreatedBy         string    `json:"created_by"`
	CreatedAt         time.Time `json:"created_at"`
	Rates             []FxRate  `json:"rates"`
}

// FxPolicyVersion is the versioned-name counterpart used by persistence code.
type FxPolicyVersion = FxPolicy

func (p FxPolicy) Clone() FxPolicy {
	p.Rates = append([]FxRate(nil), p.Rates...)
	return p
}

func (p FxPolicy) Validate() error {
	if !canonicalUUID(p.ID) || p.ValuationCurrency.Validate() != nil ||
		strings.TrimSpace(p.Source) == "" || p.QuotedAt.IsZero() ||
		p.CreatedAt.IsZero() || strings.TrimSpace(p.CreatedBy) == "" ||
		!p.ValidUntil.After(p.QuotedAt) {
		return ErrInvalidFxPolicy
	}
	seen := make(map[[2]Currency]struct{}, len(p.Rates))
	for _, rate := range p.Rates {
		if rate.Validate() != nil {
			return ErrInvalidFxRate
		}
		key := [2]Currency{rate.SourceCurrency, rate.TargetCurrency}
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidFxRate
		}
		seen[key] = struct{}{}
	}
	return nil
}

// ValidateAt enforces quoted_at <= now < valid_until.
func (p FxPolicy) ValidateAt(now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if now.Before(p.QuotedAt) {
		return ErrFxNotYetValid
	}
	if !now.Before(p.ValidUntil) {
		return ErrStaleFx
	}
	return nil
}

// IncomeLower converts one non-negative amount to the valuation currency,
// rounding down after subtracting the fee buffer. Same-currency conversion is
// an identity and neither looks up a rate nor applies a fee.
func (p FxPolicy) IncomeLower(amount Money, source Currency, now time.Time) (Money, error) {
	return p.convert(amount, source, now, false)
}

// CostUpper converts one non-negative amount to the valuation currency,
// rounding up after adding the fee buffer.
func (p FxPolicy) CostUpper(amount Money, source Currency, now time.Time) (Money, error) {
	return p.convert(amount, source, now, true)
}

func (p FxPolicy) convert(amount Money, source Currency, now time.Time, cost bool) (Money, error) {
	if amount < 0 {
		return 0, ErrNegativeAmount
	}
	if err := p.ValidateAt(now); err != nil {
		return 0, err
	}
	if err := source.Validate(); err != nil {
		return 0, err
	}
	if source == p.ValuationCurrency {
		return amount, nil
	}
	var rate *FxRate
	for i := range p.Rates {
		if p.Rates[i].SourceCurrency == source && p.Rates[i].TargetCurrency == p.ValuationCurrency {
			rate = &p.Rates[i]
			break
		}
	}
	if rate == nil {
		return 0, ErrMissingFxRate
	}
	numerator := big.NewInt(int64(amount))
	numerator.Mul(numerator, big.NewInt(rate.RateNum))
	feeFactor := int64(1000000) - rate.FeeBufferPPM
	if cost {
		feeFactor = int64(1000000) + rate.FeeBufferPPM
	}
	numerator.Mul(numerator, big.NewInt(feeFactor))
	denominator := new(big.Int).Mul(big.NewInt(rate.RateDen), big.NewInt(1000000))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if cost && remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrOverflow
	}
	return Money(quotient.Int64()), nil
}

// IncomeLowerVector converts and sums a complete original-currency vector in
// the policy valuation currency. Every present currency must be covered.
func (p FxPolicy) IncomeLowerVector(vector MoneyVector, now time.Time) (Money, error) {
	return p.convertVector(vector, now, false)
}

// CostUpperVector converts and sums a complete original-currency vector in the
// policy valuation currency. Every present currency must be covered.
func (p FxPolicy) CostUpperVector(vector MoneyVector, now time.Time) (Money, error) {
	return p.convertVector(vector, now, true)
}

func (p FxPolicy) convertVector(vector MoneyVector, now time.Time, cost bool) (Money, error) {
	if err := vector.Validate(); err != nil {
		return 0, err
	}
	currencies := make([]Currency, 0, len(vector))
	for currency := range vector {
		currencies = append(currencies, currency)
	}
	sort.Slice(currencies, func(i, j int) bool { return currencies[i] < currencies[j] })
	total := new(big.Int)
	for _, currency := range currencies {
		var (
			converted Money
			err       error
		)
		if cost {
			converted, err = p.CostUpper(vector[currency], currency, now)
		} else {
			converted, err = p.IncomeLower(vector[currency], currency, now)
		}
		if err != nil {
			return 0, err
		}
		total.Add(total, big.NewInt(int64(converted)))
		if !total.IsInt64() {
			return 0, ErrOverflow
		}
	}
	return Money(total.Int64()), nil
}

func canonicalUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}
