package routing

import "math/big"

type CostQuote struct {
	VersionID  string
	InputRate  *int64
	OutputRate *int64
	Basis      string
}

type CostPolicy struct {
	Mode              string `json:"mode"`
	UnknownCostPolicy string `json:"unknown_cost_policy"`
}

func DefaultCostPolicy() CostPolicy { return CostPolicy{Mode: "manual", UnknownCostPolicy: "allow"} }

func ValidateCostPolicy(p CostPolicy) error {
	if (p.Mode != "manual" && p.Mode != "cost_first") || (p.UnknownCostPolicy != "allow" && p.UnknownCostPolicy != "deny") || (p.Mode == "cost_first" && p.UnknownCostPolicy != "deny") {
		return ErrInvalidPolicy
	}
	return nil
}

func ManagedCostPolicy(p CostPolicy, maxInput, maxOutput *int64) bool {
	return p.Mode == "cost_first" || p.UnknownCostPolicy == "deny" || maxInput != nil || maxOutput != nil
}

func (p CostPolicy) Managed(maxInput, maxOutput *int64) bool {
	return ManagedCostPolicy(p, maxInput, maxOutput)
}

func KnownCostQuote(q CostQuote) bool {
	return q.VersionID != "" && q.InputRate != nil && q.OutputRate != nil && *q.InputRate >= 0 && *q.OutputRate >= 0 && (q.Basis == "contract" || q.Basis == "invoice")
}

func (q CostQuote) Known() bool { return KnownCostQuote(q) }

func (q CostQuote) Equal(other CostQuote) bool { return EqualCostQuote(q, other) }

func sameOptionalCostRate(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func EqualCostQuote(a, b CostQuote) bool {
	return a.VersionID == b.VersionID && a.Basis == b.Basis && sameOptionalCostRate(a.InputRate, b.InputRate) && sameOptionalCostRate(a.OutputRate, b.OutputRate)
}

func cloneCostRate(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func CloneCostQuote(q CostQuote) CostQuote {
	q.InputRate = cloneCostRate(q.InputRate)
	q.OutputRate = cloneCostRate(q.OutputRate)
	return q
}

// CostExclusion evaluates purchase unit prices, independently of customer
// selling prices. An absent or incomplete quote is never treated as zero.
func CostExclusion(p CostPolicy, q CostQuote, maxInput, maxOutput *int64) string {
	if !KnownCostQuote(q) {
		if ManagedCostPolicy(p, maxInput, maxOutput) {
			return "cost_unknown"
		}
		return ""
	}
	if maxInput != nil && *q.InputRate > *maxInput || maxOutput != nil && *q.OutputRate > *maxOutput {
		return "cost_limit_exceeded"
	}
	return ""
}

// CostScore compares the conservative purchase amount before division or
// rounding: equal integer scores retain the existing weighted selection.
func CostScore(inputUpperBound, outputLimit int64, q CostQuote) (*big.Int, error) {
	if inputUpperBound < 0 || outputLimit < 0 || !KnownCostQuote(q) {
		return nil, ErrInvalidPolicy
	}
	score := new(big.Int).Mul(big.NewInt(inputUpperBound), big.NewInt(*q.InputRate))
	return score.Add(score, new(big.Int).Mul(big.NewInt(outputLimit), big.NewInt(*q.OutputRate))), nil
}
