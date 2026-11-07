package routing

import "github.com/moni-ai/moni-aigate/internal/routecost"

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
func (p CostPolicy) Managed(limits routecost.Limits) bool {
	return p.Mode == "cost_first" || p.UnknownCostPolicy == "deny" || !limits.Empty()
}
func CostExclusion(p CostPolicy, q routecost.Quote, limits routecost.Limits) (string, error) {
	if err := ValidateCostPolicy(p); err != nil {
		return "", err
	}
	if !q.Valid() {
		return "", routecost.ErrInvalidQuote
	}
	if !limits.ValidFor(q) {
		return "", routecost.ErrInvalidLimits
	}
	if !q.Known() {
		if p.Managed(limits) {
			return "cost_unknown", nil
		}
		return "", nil
	}
	exceeded, err := limits.Exceeded(q)
	if err != nil {
		return "", err
	}
	if exceeded {
		return "cost_limit_exceeded", nil
	}
	return "", nil
}
