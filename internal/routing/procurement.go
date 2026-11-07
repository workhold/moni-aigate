package routing

import (
	"math/big"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

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
	if !q.Known() {
		return "cost_unknown"
	}
	if c.InputUpperBound <= 0 || c.OutputLimit < 0 {
		return "procurement_bound_invalid"
	}
	available, ok := new(big.Int).SetString(c.ProcurementAvailable, 10)
	if !ok {
		return "procurement_budget_unavailable"
	}
	bounds, err := metering.LegacyTokenBounds(q.Schema(), metering.Supplier, c.InputUpperBound, c.OutputLimit)
	if err != nil {
		return "procurement_bound_invalid"
	}
	rates, err := q.Rates()
	if err != nil {
		return "cost_unknown"
	}
	upper, err := metering.ReservationAmount(bounds, rates)
	if err != nil {
		return "procurement_bound_invalid"
	}
	if big.NewInt(upper).Cmp(available) > 0 {
		return "procurement_budget_exceeded"
	}
	return ""
}
