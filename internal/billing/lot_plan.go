package billing

import (
	"math/big"
	"sort"
	"time"
)

// These snapshots carry no authority. The eventual transaction helper must
// prove their complete source graph and lock every affected account/lot.
// The independent historical verifier must not reuse these runtime planners.
type fundingLotCandidate struct {
	lotID     string
	fifoAt    time.Time
	available Money
}
type fundingLotAllocation struct {
	index  int64
	lotID  string
	amount Money
}
type fundingLotFinish struct {
	allocation        fundingLotAllocation
	charged, released Money
}
type fundingLotCharged struct {
	allocation        fundingLotAllocation
	charged, refunded Money
}
type fundingLotRefund struct {
	index, allocationIndex int64
	lotID                  string
	amount                 Money
}

func lotPlanInvariant() error {
	return fail("funding_lot_invariant", "funding source allocation integrity failed")
}

func planReserveFundingLots(candidates []fundingLotCandidate, amount Money) ([]fundingLotAllocation, error) {
	if amount < 0 {
		return nil, lotPlanInvariant()
	}
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		utc := c.fifoAt.UTC()
		if !voucherID(c.lotID) || seen[c.lotID] || c.available < 0 || utc.Year() < 1 || utc.Year() > 9999 || utc.Nanosecond()%1000 != 0 {
			return nil, lotPlanInvariant()
		}
		seen[c.lotID] = true
	}
	ordered := append([]fundingLotCandidate(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].fifoAt.Equal(ordered[j].fifoAt) {
			return ordered[i].lotID < ordered[j].lotID
		}
		return ordered[i].fifoAt.Before(ordered[j].fifoAt)
	})
	remaining := amount
	plan := make([]fundingLotAllocation, 0)
	for _, c := range ordered {
		if remaining == 0 {
			break
		}
		take := c.available
		if take > remaining {
			take = remaining
		}
		if take == 0 {
			continue
		}
		plan = append(plan, fundingLotAllocation{index: int64(len(plan)) + 1, lotID: c.lotID, amount: take})
		remaining -= take
	}
	if remaining != 0 {
		return nil, fail("insufficient_balance", "insufficient wallet balance")
	}
	return plan, nil
}

func validateFundingLotAllocations(original []fundingLotAllocation, amount Money) error {
	if amount < 0 {
		return lotPlanInvariant()
	}
	sum := new(big.Int)
	seen := make(map[string]bool, len(original))
	for i, a := range original {
		if a.index != int64(i)+1 || !voucherID(a.lotID) || seen[a.lotID] || a.amount <= 0 {
			return lotPlanInvariant()
		}
		seen[a.lotID] = true
		sum.Add(sum, big.NewInt(int64(a.amount)))
	}
	if sum.Cmp(big.NewInt(int64(amount))) != 0 {
		return lotPlanInvariant()
	}
	return nil
}

func planFinishFundingLots(original []fundingLotAllocation, reserved, charged Money) ([]fundingLotFinish, error) {
	if charged < 0 || charged > reserved {
		return nil, lotPlanInvariant()
	}
	if err := validateFundingLotAllocations(original, reserved); err != nil {
		return nil, err
	}
	remaining := charged
	plan := make([]fundingLotFinish, 0, len(original))
	for _, a := range original {
		charge := a.amount
		if charge > remaining {
			charge = remaining
		}
		plan = append(plan, fundingLotFinish{allocation: a, charged: charge, released: a.amount - charge})
		remaining -= charge
	}
	return plan, nil
}

func planRefundFundingLots(original []fundingLotCharged, amount Money) ([]fundingLotRefund, error) {
	if amount <= 0 {
		return nil, lotPlanInvariant()
	}
	seen := make(map[string]bool, len(original))
	reserved := new(big.Int)
	chargePrefixEnded := false
	for i, edge := range original {
		a := edge.allocation
		if a.index != int64(i)+1 || !voucherID(a.lotID) || seen[a.lotID] || a.amount <= 0 || edge.charged < 0 || edge.charged > a.amount || edge.refunded < 0 || edge.refunded > edge.charged {
			return nil, lotPlanInvariant()
		}
		if chargePrefixEnded && edge.charged != 0 {
			return nil, lotPlanInvariant()
		}
		if edge.charged < a.amount {
			chargePrefixEnded = true
		}
		reserved.Add(reserved, big.NewInt(int64(a.amount)))
		if !reserved.IsInt64() {
			return nil, lotPlanInvariant()
		}
		seen[a.lotID] = true
	}
	refundPrefixEnded := false
	for i := len(original) - 1; i >= 0; i-- {
		edge := original[i]
		if refundPrefixEnded && edge.refunded != 0 {
			return nil, lotPlanInvariant()
		}
		if edge.refunded < edge.charged {
			refundPrefixEnded = true
		}
	}
	remaining := amount
	plan := make([]fundingLotRefund, 0)
	for i := len(original) - 1; i >= 0 && remaining > 0; i-- {
		edge := original[i]
		take := edge.charged - edge.refunded
		if take > remaining {
			take = remaining
		}
		if take == 0 {
			continue
		}
		plan = append(plan, fundingLotRefund{index: int64(len(plan)) + 1, allocationIndex: edge.allocation.index, lotID: edge.allocation.lotID, amount: take})
		remaining -= take
	}
	if remaining != 0 {
		return nil, lotPlanInvariant()
	}
	return plan, nil
}
