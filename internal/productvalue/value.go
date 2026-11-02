// Package productvalue provides bounded, exact arithmetic for the proposed
// product value strips. Its results carry no source, payment or refund authority.
package productvalue

import (
	"errors"
	"math/big"
	"strconv"
)

const (
	MaxRanges       = 4096
	MaxFundingEdges = 4096
)

var (
	ErrInvalidValue     = errors.New("product_value_invalid_value")
	ErrInvalidRanges    = errors.New("product_value_invalid_ranges")
	ErrInvalidPartition = errors.New("product_value_invalid_partition")
	ErrResourceLimit    = errors.New("product_value_resource_limit")
	ErrInvalidTime      = errors.New("product_value_invalid_time")
	ErrMonthOverflow    = errors.New("product_value_month_overflow")
)

// Range represents integer coordinates in a half-open interval. Input ranges
// must be nonempty; a projected MoneyRange may be empty and have zero value.
type Range struct {
	Start string
	End   string
}

// Value preserves each input segment, including zero-value money segments.
type Value struct {
	Units       string
	Amount      string
	MoneyRanges []Range
}

// Attribution binds amounts to zero-based input edge positions, not identities.
// The caller remains responsible for proving the original edges and eligibility.
type Attribution struct {
	Value  Value
	ByEdge []string
}

// Terms is an immutable pair of validated purchase and allowance amounts.
// Its zero value is invalid. It is a mathematical parameter, not a proof.
type Terms struct {
	purchase  int64
	allowance int64
	valid     bool
}

// New accepts canonical positive decimal amounts no greater than MaxInt64.
func New(purchaseAmount, allowanceAmount string) (Terms, error) {
	p, ok := decimal(purchaseAmount)
	if !ok || p == 0 {
		return Terms{}, ErrInvalidValue
	}
	g, ok := decimal(allowanceAmount)
	if !ok || g == 0 {
		return Terms{}, ErrInvalidValue
	}
	return Terms{purchase: p, allowance: g, valid: true}, nil
}

func decimal(s string) (int64, bool) {
	if len(s) == 0 || len(s) > 19 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

type span struct{ start, end int64 }

func parseRange(r Range, upper int64) (span, bool) {
	a, aok := decimal(r.Start)
	b, bok := decimal(r.End)
	return span{a, b}, aok && bok && a < b && b <= upper
}

func (t Terms) isValid() bool { return t.valid && t.purchase > 0 && t.allowance > 0 }

// Value maps sorted, nonoverlapping allowance ranges to absolute money ranges.
// Adjacent input segments are accepted and preserved. No input is reordered.
func (t Terms) Value(ranges []Range) (Value, error) {
	if !t.isValid() {
		return Value{}, ErrInvalidValue
	}
	if len(ranges) > MaxRanges {
		return Value{}, ErrResourceLimit
	}
	v, _, err := t.evaluate(ranges)
	return v, err
}

// evaluate computes absolute endpoints before subtracting them, so splitting a
// range never loses or creates a remainder. Disjoint coordinates bound both sums.
func (t Terms) evaluate(ranges []Range) (Value, []span, error) {
	out := Value{MoneyRanges: make([]Range, 0, len(ranges))}
	mapped := make([]span, 0, len(ranges))
	p, g := big.NewInt(t.purchase), big.NewInt(t.allowance)
	var units, amount, previous int64
	for i, r := range ranges {
		s, ok := parseRange(r, t.allowance)
		if !ok || (i > 0 && s.start < previous) {
			return Value{}, nil, ErrInvalidRanges
		}
		previous = s.end
		a := prefixValue(p, g, s.start)
		b := prefixValue(p, g, s.end)
		units += s.end - s.start
		amount += b - a
		mapped = append(mapped, span{a, b})
		out.MoneyRanges = append(out.MoneyRanges, Range{strconv.FormatInt(a, 10), strconv.FormatInt(b, 10)})
	}
	out.Units, out.Amount = strconv.FormatInt(units, 10), strconv.FormatInt(amount, 10)
	return out, mapped, nil
}

func prefixValue(p, g *big.Int, k int64) int64 {
	n := new(big.Int).Mul(p, big.NewInt(k))
	return n.Quo(n, g).Int64() // 0 <= k <= G, so the exact result is <= P.
}

// Attribute intersects projected money ranges with a complete [0,P) partition.
// It returns one amount per original edge, including zeros, in O(ranges+edges).
func (t Terms) Attribute(ranges, fundingEdges []Range) (Attribution, error) {
	if !t.isValid() {
		return Attribution{}, ErrInvalidValue
	}
	if len(ranges) > MaxRanges || len(fundingEdges) > MaxFundingEdges {
		return Attribution{}, ErrResourceLimit
	}
	v, mapped, err := t.evaluate(ranges)
	if err != nil {
		return Attribution{}, err
	}
	if len(fundingEdges) == 0 {
		return Attribution{}, ErrInvalidPartition
	}
	edges := make([]span, len(fundingEdges))
	var previous int64
	for i, r := range fundingEdges {
		s, ok := parseRange(r, t.purchase)
		if !ok || s.start != previous {
			return Attribution{}, ErrInvalidPartition
		}
		edges[i], previous = s, s.end
	}
	if previous != t.purchase {
		return Attribution{}, ErrInvalidPartition
	}
	amounts := make([]int64, len(edges))
	edge := 0
	for _, r := range mapped {
		if r.start == r.end {
			continue
		}
		for edge < len(edges) && edges[edge].end <= r.start {
			edge++
		}
		for edge < len(edges) && edges[edge].start < r.end {
			a, b := max(r.start, edges[edge].start), min(r.end, edges[edge].end)
			amounts[edge] += b - a
			if edges[edge].end >= r.end {
				break
			}
			edge++
		}
	}
	out := Attribution{Value: v, ByEdge: make([]string, len(edges))}
	for i, amount := range amounts {
		out.ByEdge[i] = strconv.FormatInt(amount, 10)
	}
	return out, nil
}
