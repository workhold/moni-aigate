package metering

import "math/big"

func exactCost(v vector, r Rates) (*big.Rat, error) {
	if err := checkBinding(v.binding, r.binding); err != nil {
		return nil, err
	}
	sum := new(big.Rat)
	for i, quantity := range v.values {
		numerator := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(r.numerators[i]))
		sum.Add(sum, new(big.Rat).SetFrac(numerator, big.NewInt(r.denominators[i])))
	}
	return sum, nil
}

func roundedCost(v vector, r Rates) (*big.Int, error) {
	sum, err := exactCost(v, r)
	if err != nil {
		return nil, err
	}
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(sum.Num(), sum.Denom(), remainder)
	if remainder.Sign() != 0 {
		integer.Add(integer, big.NewInt(1))
	}
	return integer, nil
}

// Amount preserves arbitrarily large actual costs rather than capping them to
// an int64 reservation or treating an observed excess as unknown/zero.
func Amount(quantities Quantities, rates Rates) (*big.Int, error) {
	return roundedCost(quantities.vector, rates)
}

func ReservationAmount(bounds Bounds, rates Rates) (int64, error) {
	amount, err := roundedCost(bounds.vector, rates)
	if err != nil {
		return 0, err
	}
	if !amount.IsInt64() {
		return 0, ErrAmountOverflow
	}
	return amount.Int64(), nil
}

func CompareExactCost(left Bounds, leftRates Rates, right Bounds, rightRates Rates) (int, error) {
	for _, b := range []binding{left.vector.binding, leftRates.binding, right.vector.binding, rightRates.binding} {
		if !b.valid {
			return 0, ErrInvalidValue
		}
	}
	for _, b := range []binding{left.vector.binding, leftRates.binding, right.vector.binding, rightRates.binding} {
		if b.side != Supplier {
			return 0, ErrSide
		}
	}
	for _, b := range []binding{leftRates.binding, right.vector.binding, rightRates.binding} {
		if err := checkBinding(left.vector.binding, b); err != nil {
			return 0, err
		}
	}
	l, err := exactCost(left.vector, leftRates)
	if err != nil {
		return 0, err
	}
	r, err := exactCost(right.vector, rightRates)
	if err != nil {
		return 0, err
	}
	return l.Cmp(r), nil
}

func WithinBounds(actual Quantities, upper Bounds) (bool, error) {
	if err := checkBinding(actual.vector.binding, upper.vector.binding); err != nil {
		return false, err
	}
	for i, quantity := range actual.vector.values {
		if quantity > upper.vector.values[i] {
			return false, nil
		}
	}
	return true, nil
}
