package billing

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"
)

func lotPlanID(n int) string { return fmt.Sprintf("10000000-0000-0000-0000-%012x", n) }
func lotPlanAllocation(index int64, n int, amount Money) fundingLotAllocation {
	return fundingLotAllocation{index, lotPlanID(n), amount}
}
func lotPlanAssertError(t *testing.T, err error, code string) {
	t.Helper()
	if e, ok := err.(*Error); !ok || e.Code != code {
		t.Fatalf("expected %s, got %v", code, err)
	}
}

func TestFundingLotPlanFIFOAndBounds(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 1000, time.UTC)
	original := []fundingLotCandidate{{lotPlanID(3), at.Add(time.Second), 50}, {lotPlanID(2), at, 7}, {lotPlanID(1), at, 5}, {lotPlanID(4), at.Add(-time.Second), 0}}
	saved := append([]fundingLotCandidate(nil), original...)
	got, e := planReserveFundingLots(original, 15)
	if e != nil {
		t.Fatal(e)
	}
	want := []fundingLotAllocation{lotPlanAllocation(1, 1, 5), lotPlanAllocation(2, 2, 7), lotPlanAllocation(3, 3, 3)}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(original, saved) {
		t.Fatalf("FIFO or input mutated: %+v", got)
	}
	max := Money(math.MaxInt64)
	got, e = planReserveFundingLots([]fundingLotCandidate{{lotPlanID(2), at, max}, {lotPlanID(1), at, max}}, max)
	if e != nil || !reflect.DeepEqual(got, []fundingLotAllocation{lotPlanAllocation(1, 1, max)}) {
		t.Fatalf("overflowed available candidate total: %v %+v", e, got)
	}
	if got, e = planReserveFundingLots(nil, 0); e != nil || len(got) != 0 {
		t.Fatal("zero reservation allocated a lot")
	}
	_, e = planReserveFundingLots(original, 63)
	lotPlanAssertError(t, e, "insufficient_balance")
	for _, input := range [][]fundingLotCandidate{
		{{lotPlanID(1), at, -1}}, {{lotPlanID(1), at, 1}, {lotPlanID(1), at, 2}}, {{"not-a-lot", at, 1}}, {{lotPlanID(1), at.Add(time.Nanosecond), 1}}, {{lotPlanID(1), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), 1}},
		{{lotPlanID(1), time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 14*3600)), 1}},
		{{lotPlanID(1), time.Date(9999, 12, 31, 23, 59, 59, 0, time.FixedZone("west", -14*3600)), 1}},
	} {
		_, e = planReserveFundingLots(input, 1)
		lotPlanAssertError(t, e, "funding_lot_invariant")
	}
	_, e = planReserveFundingLots(nil, -1)
	lotPlanAssertError(t, e, "funding_lot_invariant")
}

func TestFundingLotPlanOriginalSettlementPrefix(t *testing.T) {
	allocations := []fundingLotAllocation{lotPlanAllocation(1, 1, 5), lotPlanAllocation(2, 2, 7), lotPlanAllocation(3, 3, 3)}
	saved := append([]fundingLotAllocation(nil), allocations...)
	got, e := planFinishFundingLots(allocations, 15, 8)
	if e != nil {
		t.Fatal(e)
	}
	want := []fundingLotFinish{{allocations[0], 5, 0}, {allocations[1], 3, 4}, {allocations[2], 0, 3}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(allocations, saved) {
		t.Fatalf("wrong original prefix: %+v", got)
	}
	for _, charged := range []Money{0, 15} {
		got, e = planFinishFundingLots(allocations, 15, charged)
		if e != nil {
			t.Fatal(e)
		}
		for _, edge := range got {
			if edge.charged+edge.released != edge.allocation.amount {
				t.Fatal("lost reserved value")
			}
			if charged == 0 && edge.charged != 0 || charged == 15 && edge.released != 0 {
				t.Fatal("wrong full release/charge")
			}
		}
	}
	if got, e = planFinishFundingLots(nil, 0, 0); e != nil || len(got) != 0 {
		t.Fatal("zero finish created a positive allocation")
	}
	max := Money(math.MaxInt64)
	got, e = planFinishFundingLots([]fundingLotAllocation{lotPlanAllocation(1, 1, max)}, max, max-1)
	if e != nil || got[0].released != 1 {
		t.Fatal("MAX partial finish failed")
	}
	for _, input := range []struct {
		a    []fundingLotAllocation
		r, c Money
	}{
		{allocations, 15, 16}, {allocations, 15, -1}, {allocations, 14, 8}, {[]fundingLotAllocation{lotPlanAllocation(2, 1, 1)}, 1, 1}, {[]fundingLotAllocation{lotPlanAllocation(1, 1, 1), lotPlanAllocation(2, 1, 1)}, 2, 2}, {[]fundingLotAllocation{lotPlanAllocation(1, 1, 0)}, 0, 0}, {[]fundingLotAllocation{lotPlanAllocation(1, 1, max), lotPlanAllocation(2, 2, 1)}, max, 0},
	} {
		_, e = planFinishFundingLots(input.a, input.r, input.c)
		lotPlanAssertError(t, e, "funding_lot_invariant")
	}
}

func TestFundingLotPlanReverseRefundAndSplitEquivalence(t *testing.T) {
	original := []fundingLotCharged{{lotPlanAllocation(1, 1, 5), 5, 0}, {lotPlanAllocation(2, 2, 7), 3, 0}, {lotPlanAllocation(3, 3, 3), 0, 0}}
	saved := append([]fundingLotCharged(nil), original...)
	got, e := planRefundFundingLots(original, 4)
	if e != nil {
		t.Fatal(e)
	}
	want := []fundingLotRefund{{1, 2, lotPlanID(2), 3}, {2, 1, lotPlanID(1), 1}}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(original, saved) {
		t.Fatalf("wrong reverse original charged edges: %+v", got)
	}
	// Every partition of this original charge yields the same cumulative source
	// restoration as one refund, including a partial last charged allocation.
	for total := Money(1); total <= 8; total++ {
		for first := Money(1); first <= total; first++ {
			once, e := planRefundFundingLots(original, total)
			if e != nil {
				t.Fatal(e)
			}
			a, e := planRefundFundingLots(original, first)
			if e != nil {
				t.Fatal(e)
			}
			changed := append([]fundingLotCharged(nil), original...)
			cumulative := map[string]Money{}
			for _, edge := range a {
				cumulative[edge.lotID] += edge.amount
				changed[edge.allocationIndex-1].refunded += edge.amount
			}
			if first < total {
				b, e := planRefundFundingLots(changed, total-first)
				if e != nil {
					t.Fatal(e)
				}
				for _, edge := range b {
					cumulative[edge.lotID] += edge.amount
				}
			}
			expected := map[string]Money{}
			for _, edge := range once {
				expected[edge.lotID] += edge.amount
			}
			if !reflect.DeepEqual(cumulative, expected) {
				t.Fatalf("partition %d + %d changed source restoration", first, total-first)
			}
		}
	}
	max := Money(math.MaxInt64)
	got, e = planRefundFundingLots([]fundingLotCharged{{lotPlanAllocation(1, 1, max), max, max - 1}}, 1)
	if e != nil || got[0].amount != 1 {
		t.Fatal("MAX prior refund lost precision")
	}
	bad := [][]fundingLotCharged{
		{{lotPlanAllocation(1, 1, 2), 3, 0}}, {{lotPlanAllocation(1, 1, 2), 2, 3}}, {{lotPlanAllocation(1, 1, 2), 1, 0}, {lotPlanAllocation(2, 2, 2), 1, 0}},
		{{lotPlanAllocation(1, 1, 2), 2, 1}, {lotPlanAllocation(2, 2, 2), 2, 0}}, {{lotPlanAllocation(2, 1, 2), 2, 0}}, {{lotPlanAllocation(1, 1, 2), 2, 0}, {lotPlanAllocation(2, 1, 2), 2, 0}},
		{{lotPlanAllocation(1, 1, max), max, 0}, {lotPlanAllocation(2, 2, 1), 1, 0}},
	}
	for _, input := range bad {
		_, e = planRefundFundingLots(input, 1)
		lotPlanAssertError(t, e, "funding_lot_invariant")
	}
	for _, amount := range []Money{-1, 0, 9} {
		_, e = planRefundFundingLots(original, amount)
		lotPlanAssertError(t, e, "funding_lot_invariant")
	}
}

func TestFundingLotPlanValueConservationAcrossManyLots(t *testing.T) {
	// Vary both fragmentation and the charged/refunded boundary. The expected
	// totals use big integers independently of the runtime subtraction strategy.
	for count := 1; count <= 129; count++ {
		candidates := make([]fundingLotCandidate, 0, count)
		total := Money(0)
		for i := count; i > 0; i-- {
			amount := Money(i*7919 + 1)
			total += amount
			candidates = append(candidates, fundingLotCandidate{lotPlanID(i), time.Unix(int64(i/3), 0).UTC(), amount})
		}
		allocations, e := planReserveFundingLots(candidates, total-1)
		if e != nil {
			t.Fatal(e)
		}
		for _, charged := range []Money{0, 1, (total - 1) / 2, total - 1} {
			plan, e := planFinishFundingLots(allocations, total-1, charged)
			if e != nil {
				t.Fatal(e)
			}
			c, r := new(big.Int), new(big.Int)
			edges := make([]fundingLotCharged, 0, len(plan))
			for _, edge := range plan {
				c.Add(c, big.NewInt(int64(edge.charged)))
				r.Add(r, big.NewInt(int64(edge.released)))
				edges = append(edges, fundingLotCharged{edge.allocation, edge.charged, 0})
			}
			if c.Cmp(big.NewInt(int64(charged))) != 0 || new(big.Int).Add(c, r).Cmp(big.NewInt(int64(total-1))) != 0 {
				t.Fatal("fragmented settlement violated conservation")
			}
			if charged > 0 {
				refunds, e := planRefundFundingLots(edges, charged)
				if e != nil {
					t.Fatal(e)
				}
				f := new(big.Int)
				for _, edge := range refunds {
					f.Add(f, big.NewInt(int64(edge.amount)))
				}
				if f.Cmp(c) != 0 {
					t.Fatal("fragmented refund violated conservation")
				}
			}
		}
	}
}
