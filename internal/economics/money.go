// Package economics implements the pure, exact arithmetic used by P0 economic
// admission. It performs no I/O and never treats unknown evidence as zero.
package economics

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strconv"
)

var (
	ErrInvalidMoney    = errors.New("invalid money")
	ErrInvalidCurrency = errors.New("invalid currency")
	ErrInvalidVector   = errors.New("invalid money vector")
	ErrMissingCurrency = errors.New("required currency is missing")
	ErrNegativeAmount  = errors.New("negative amount is not allowed")
	ErrOverflow        = errors.New("amount overflows int64")
)

// Currency is the closed set of fiat currencies supported by P0.
type Currency string

const (
	CurrencyCNY Currency = "CNY"
	CurrencyUSD Currency = "USD"
)

func (c Currency) Validate() error {
	if c != CurrencyCNY && c != CurrencyUSD {
		return ErrInvalidCurrency
	}
	return nil
}

// Money is an integer number of micro-units of a fiat currency. Its JSON
// representation is always a canonical base-10 string, never a JSON number.
type Money int64

func (m Money) String() string { return strconv.FormatInt(int64(m), 10) }
func (m Money) Int64() int64   { return int64(m) }

func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(m.String())), nil
}

func (m *Money) UnmarshalJSON(b []byte) error {
	if m == nil {
		return ErrInvalidMoney
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil || !canonicalInt64(s) {
		return ErrInvalidMoney
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return ErrInvalidMoney
	}
	*m = Money(n)
	return nil
}

func canonicalInt64(s string) bool {
	if s == "0" {
		return true
	}
	if s == "" || s == "-0" {
		return false
	}
	digits := s
	if s[0] == '-' {
		digits = s[1:]
	}
	if digits == "" || digits[0] == '0' {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	return true
}

// MoneyVector is a closed CNY/USD mapping. A nil vector is not a known empty
// vector: use a non-nil empty map for a known absence of amounts, and use the
// relevant completeness flag or a nil optional vector for unknown evidence.
type MoneyVector map[Currency]Money

// NewMoneyVector constructs a known vector after validating its currencies.
func NewMoneyVector(values map[Currency]Money) (MoneyVector, error) {
	v := MoneyVector(values)
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return v.Clone(), nil
}

func (v MoneyVector) Clone() MoneyVector {
	if v == nil {
		return nil
	}
	out := make(MoneyVector, len(v))
	for currency, amount := range v {
		out[currency] = amount
	}
	return out
}

// Validate checks the closed key set and, when supplied, required currency
// presence. Amounts may be negative here because vectors also serve reporting;
// FX conversion and admission apply their own non-negative constraints.
func (v MoneyVector) Validate(required ...Currency) error {
	if v == nil {
		return ErrInvalidVector
	}
	for currency := range v {
		if err := currency.Validate(); err != nil {
			return err
		}
	}
	for _, currency := range required {
		if err := currency.Validate(); err != nil {
			return err
		}
		if _, ok := v[currency]; !ok {
			return ErrMissingCurrency
		}
	}
	return nil
}

func (v MoneyVector) Amount(currency Currency) (Money, bool) {
	amount, ok := v[currency]
	return amount, ok
}

// Add performs exact per-currency addition and rejects an int64 overflow.
func (v MoneyVector) Add(other MoneyVector) (MoneyVector, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if err := other.Validate(); err != nil {
		return nil, err
	}
	out := v.Clone()
	for currency, amount := range other {
		sum := new(big.Int).Add(big.NewInt(int64(out[currency])), big.NewInt(int64(amount)))
		if !sum.IsInt64() {
			return nil, ErrOverflow
		}
		out[currency] = Money(sum.Int64())
	}
	return out, nil
}

func (v MoneyVector) MarshalJSON() ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	type plainVector map[Currency]Money
	return json.Marshal(plainVector(v))
}

func (v *MoneyVector) UnmarshalJSON(b []byte) error {
	if v == nil {
		return ErrInvalidVector
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrInvalidVector
	}
	out := make(MoneyVector)
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return ErrInvalidVector
		}
		key, ok := token.(string)
		if !ok {
			return ErrInvalidVector
		}
		currency := Currency(key)
		if err = currency.Validate(); err != nil {
			return err
		}
		if _, duplicate := out[currency]; duplicate {
			return ErrInvalidVector
		}
		var amount Money
		if err = decoder.Decode(&amount); err != nil {
			return err
		}
		out[currency] = amount
	}
	if _, err = decoder.Token(); err != nil {
		return ErrInvalidVector
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return ErrInvalidVector
	}
	*v = out
	return nil
}
