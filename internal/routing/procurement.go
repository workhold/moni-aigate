package routing

import "math/big"

// This snapshot is only an initial filter. Durable per-attempt admission owns
// the cross-tenant account lock and remains the authority for spending.
func ProcurementExclusion(c Candidate) string {
	if !c.ProcurementManaged {
		return ""
	}
	if c.ProcurementState != "active" {
		return "procurement_paused"
	}
	q := c.CostQuote
	if q.InputRate == nil || q.OutputRate == nil || q.VersionID == "" || (q.Basis != "contract" && q.Basis != "invoice") {
		return "cost_unknown"
	}
	if c.InputUpperBound <= 0 || c.OutputLimit < 0 || *q.InputRate < 0 || *q.OutputRate < 0 {
		return "procurement_bound_invalid"
	}
	available, ok := new(big.Int).SetString(c.ProcurementAvailable, 10)
	if !ok {
		return "procurement_budget_unavailable"
	}
	n := new(big.Int).Mul(big.NewInt(c.InputUpperBound), big.NewInt(*q.InputRate))
	n.Add(n, new(big.Int).Mul(big.NewInt(c.OutputLimit), big.NewInt(*q.OutputRate)))
	n.Add(n, big.NewInt(999999))
	n.Div(n, big.NewInt(1000000))
	if !n.IsInt64() {
		return "procurement_bound_invalid"
	}
	if n.Cmp(available) > 0 {
		return "procurement_budget_exceeded"
	}
	return ""
}
