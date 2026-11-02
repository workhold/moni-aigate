package productvalue_test

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/productvalue"
)

func terms(t testing.TB, p, g string) productvalue.Terms {
	t.Helper()
	v, err := productvalue.New(p, g)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func r(a, b int64) productvalue.Range {
	return productvalue.Range{Start: strconv.FormatInt(a, 10), End: strconv.FormatInt(b, 10)}
}

func TestAbsoluteValueStripsAndZeroValueUnits(t *testing.T) {
	tests := []struct {
		p, g   string
		ranges []productvalue.Range
		want   productvalue.Value
	}{
		{"100", "150", []productvalue.Range{r(0, 60)}, productvalue.Value{Units: "60", Amount: "40", MoneyRanges: []productvalue.Range{r(0, 40)}}},
		{"100", "150", []productvalue.Range{r(60, 150)}, productvalue.Value{Units: "90", Amount: "60", MoneyRanges: []productvalue.Range{r(40, 100)}}},
		{"3", "10", []productvalue.Range{r(0, 1), r(1, 4), r(4, 10)}, productvalue.Value{Units: "10", Amount: "3", MoneyRanges: []productvalue.Range{r(0, 0), r(0, 1), r(1, 3)}}},
		{"10", "3", []productvalue.Range{r(1, 2)}, productvalue.Value{Units: "1", Amount: "3", MoneyRanges: []productvalue.Range{r(3, 6)}}},
		{"10", "3", []productvalue.Range{r(2, 3)}, productvalue.Value{Units: "1", Amount: "4", MoneyRanges: []productvalue.Range{r(6, 10)}}},
		{"9223372036854775807", "9223372036854775807", []productvalue.Range{r(0, 1), r(1, 9223372036854775807)}, productvalue.Value{Units: "9223372036854775807", Amount: "9223372036854775807", MoneyRanges: []productvalue.Range{r(0, 1), r(1, 9223372036854775807)}}},
		{"9223372036854775807", "9223372036854775806", []productvalue.Range{r(9223372036854775805, 9223372036854775806)}, productvalue.Value{Units: "1", Amount: "2", MoneyRanges: []productvalue.Range{r(9223372036854775805, 9223372036854775807)}}},
		{"9223372036854775806", "9223372036854775807", []productvalue.Range{r(0, 1), r(1, 9223372036854775807)}, productvalue.Value{Units: "9223372036854775807", Amount: "9223372036854775806", MoneyRanges: []productvalue.Range{r(0, 0), r(0, 9223372036854775806)}}},
	}
	for _, tc := range tests {
		v, err := terms(t, tc.p, tc.g).Value(tc.ranges)
		if err != nil || !reflect.DeepEqual(v, tc.want) {
			t.Fatalf("P=%s G=%s got %#v %v, want %#v", tc.p, tc.g, v, err, tc.want)
		}
	}
	for _, empty := range [][]productvalue.Range{nil, {}} {
		v, err := terms(t, "3", "10").Value(empty)
		if err != nil || v.Units != "0" || v.Amount != "0" || v.MoneyRanges == nil || len(v.MoneyRanges) != 0 {
			t.Fatalf("empty selection did not produce explicit empty result: %#v %v", v, err)
		}
	}
}

func TestAttributionPreservesOriginalEdgePositions(t *testing.T) {
	v := terms(t, "100", "150")
	edges := []productvalue.Range{r(0, 25), r(25, 70), r(70, 100)}
	got, err := v.Attribute([]productvalue.Range{r(60, 150)}, edges)
	if err != nil || !reflect.DeepEqual(got.ByEdge, []string{"0", "30", "30"}) || got.Value.Amount != "60" {
		t.Fatalf("wrong tail attribution: %#v %v", got, err)
	}
	got, err = v.Attribute([]productvalue.Range{r(0, 15), r(90, 135)}, edges)
	if err != nil || !reflect.DeepEqual(got.ByEdge, []string{"10", "10", "20"}) || got.Value.Amount != "40" {
		t.Fatalf("wrong disjoint attribution: %#v %v", got, err)
	}
	got, err = terms(t, "3", "10").Attribute([]productvalue.Range{r(0, 1)}, []productvalue.Range{r(0, 1), r(1, 3)})
	if err != nil || !reflect.DeepEqual(got.ByEdge, []string{"0", "0"}) || !reflect.DeepEqual(got.Value.MoneyRanges, []productvalue.Range{r(0, 0)}) {
		t.Fatalf("zero-value units created money or disappeared: %#v %v", got, err)
	}
	got, err = v.Attribute(nil, edges)
	if err != nil || !reflect.DeepEqual(got.ByEdge, []string{"0", "0", "0"}) || got.Value.MoneyRanges == nil {
		t.Fatalf("empty selection lost original edge positions: %#v %v", got, err)
	}
}

func TestStrictAmountsRangesPartitionsAndFailureAtomicity(t *testing.T) {
	bad := []string{"", "00", "01", "+1", "-1", " 1", "1 ", "1\n", "1.0", "1e1", "１２", "١", "1\x00", "9223372036854775808", "10000000000000000000"}
	for _, s := range append(append([]string{}, bad...), "0") {
		for _, args := range [][2]string{{s, "1"}, {"1", s}} {
			v, err := productvalue.New(args[0], args[1])
			if v != (productvalue.Terms{}) || !errors.Is(err, productvalue.ErrInvalidValue) || err.Error() != productvalue.ErrInvalidValue.Error() {
				t.Fatal("invalid amount returned data or an uncontrolled error")
			}
		}
	}
	v := terms(t, "100", "150")
	badRanges := [][]productvalue.Range{
		{r(0, 0)}, {r(2, 1)}, {r(0, 151)}, {r(2, 3), r(0, 1)},
		{r(0, 2), r(1, 3)}, {r(0, 1), r(0, 1)}, {r(149, 150), r(149, 150)},
	}
	for _, s := range bad {
		badRanges = append(badRanges, []productvalue.Range{{Start: s, End: "150"}}, []productvalue.Range{{Start: "0", End: s}})
	}
	for _, ranges := range badRanges {
		got, err := v.Value(ranges)
		if !reflect.DeepEqual(got, productvalue.Value{}) || !errors.Is(err, productvalue.ErrInvalidRanges) || err.Error() != productvalue.ErrInvalidRanges.Error() {
			t.Fatal("bad ranges returned partial data or an uncontrolled error")
		}
		at, err := v.Attribute(ranges, []productvalue.Range{r(0, 100)})
		if !reflect.DeepEqual(at, productvalue.Attribution{}) || !errors.Is(err, productvalue.ErrInvalidRanges) {
			t.Fatal("bad attribution ranges returned partial data")
		}
	}
	badPartitions := [][]productvalue.Range{
		nil, {}, {r(1, 100)}, {r(0, 99)}, {r(0, 101)}, {r(0, 0), r(0, 100)},
		{r(0, 50), r(49, 100)}, {r(0, 50), r(51, 100)}, {r(50, 100), r(0, 50)},
		{{Start: "00", End: "100"}}, {{Start: "0", End: "+100"}},
	}
	for _, edges := range badPartitions {
		for _, ranges := range [][]productvalue.Range{nil, {r(0, 150)}} {
			got, err := v.Attribute(ranges, edges)
			if !reflect.DeepEqual(got, productvalue.Attribution{}) || !errors.Is(err, productvalue.ErrInvalidPartition) || err.Error() != productvalue.ErrInvalidPartition.Error() {
				t.Fatal("invalid partition was accepted, skipped for zero selection or returned partial data")
			}
		}
	}
	var zero productvalue.Terms
	got, err := zero.Value([]productvalue.Range{r(0, 1)})
	if !reflect.DeepEqual(got, productvalue.Value{}) || !errors.Is(err, productvalue.ErrInvalidValue) {
		t.Fatal("zero Terms was accepted")
	}
	at, err := zero.Attribute(nil, nil)
	if !reflect.DeepEqual(at, productvalue.Attribution{}) || !errors.Is(err, productvalue.ErrInvalidValue) {
		t.Fatal("zero Terms did not take precedence")
	}
}

func TestResourceBoundsAndNoAliasing(t *testing.T) {
	v := terms(t, "4096", "4096")
	ranges := make([]productvalue.Range, productvalue.MaxRanges)
	edges := make([]productvalue.Range, productvalue.MaxFundingEdges)
	for i := range ranges {
		ranges[i], edges[i] = r(int64(i), int64(i+1)), r(int64(i), int64(i+1))
	}
	before := append([]productvalue.Range(nil), ranges...)
	got, err := v.Attribute(ranges, edges)
	if err != nil || got.Value.Amount != "4096" || len(got.ByEdge) != 4096 || !reflect.DeepEqual(ranges, before) || !reflect.DeepEqual(edges, before) {
		t.Fatalf("exact resource limit or input immutability failed: %v", err)
	}
	for _, amount := range got.ByEdge {
		if amount != "1" {
			t.Fatal("bounded full partition lost money")
		}
	}
	got.Value.MoneyRanges[0], got.ByEdge[0] = r(100, 200), "private-mutated-output"
	again, err := v.Attribute(ranges, edges)
	if err != nil || again.Value.MoneyRanges[0] != r(0, 1) || again.ByEdge[0] != "1" || !reflect.DeepEqual(ranges, before) {
		t.Fatal("output aliases input, Terms or a later result")
	}
	tooMany := make([]productvalue.Range, productvalue.MaxRanges+1)
	failed, err := v.Value(tooMany)
	if !errors.Is(err, productvalue.ErrResourceLimit) || !reflect.DeepEqual(failed, productvalue.Value{}) {
		t.Fatal("excess ranges were truncated or did not fail atomically")
	}
	for _, args := range [][2][]productvalue.Range{{tooMany, nil}, {nil, tooMany}, {{r(0, 0)}, tooMany}} {
		failed, err := v.Attribute(args[0], args[1])
		if !errors.Is(err, productvalue.ErrResourceLimit) || !reflect.DeepEqual(failed, productvalue.Attribution{}) || err.Error() != productvalue.ErrResourceLimit.Error() {
			t.Fatal("resource limit did not precede validation")
		}
	}
}

// oracle walks individual allowance units with a remainder counter and then
// individual money units. It does not multiply endpoints or copy the algorithm.
func checkSmallOracle(t testing.TB, p, g int, selected []bool, cuts []bool) {
	t.Helper()
	ranges := make([]productvalue.Range, 0)
	for i := 0; i < g; {
		if !selected[i] {
			i++
			continue
		}
		start := i
		for i < g && selected[i] {
			i++
		}
		ranges = append(ranges, r(int64(start), int64(i)))
	}
	edges := make([]productvalue.Range, 0)
	start := 0
	for i := 1; i <= p; i++ {
		if i == p || cuts[i-1] {
			edges = append(edges, r(int64(start), int64(i)))
			start = i
		}
	}
	wantEdges := make([]int, len(edges))
	edge, money, remainder, units, amount := 0, 0, 0, 0, 0
	for i := 0; i < g; i++ {
		if selected[i] {
			units++
		}
		remainder += p
		for remainder >= g {
			remainder -= g
			for end, _ := strconv.Atoi(edges[edge].End); money >= end; end, _ = strconv.Atoi(edges[edge].End) {
				edge++
			}
			if selected[i] {
				amount++
				wantEdges[edge]++
			}
			money++
		}
	}
	got, err := terms(t, strconv.Itoa(p), strconv.Itoa(g)).Attribute(ranges, edges)
	if err != nil || got.Value.Units != strconv.Itoa(units) || got.Value.Amount != strconv.Itoa(amount) || len(got.ByEdge) != len(wantEdges) {
		t.Fatalf("oracle totals mismatch P=%d G=%d: %#v %v", p, g, got, err)
	}
	for i, want := range wantEdges {
		if got.ByEdge[i] != strconv.Itoa(want) {
			t.Fatalf("oracle edge %d mismatch: got %s want %d", i, got.ByEdge[i], want)
		}
	}
}

func TestIndependentUnitOracleAndConcurrentCopies(t *testing.T) {
	rng := rand.New(rand.NewSource(23032026))
	for p := 1; p <= 35; p++ {
		for g := 1; g <= 35; g++ {
			for trial := 0; trial < 4; trial++ {
				selected, cuts := make([]bool, g), make([]bool, p)
				for i := range selected {
					selected[i] = trial == 0 || (trial > 1 && rng.Intn(2) == 1)
				}
				for i := range cuts {
					cuts[i] = rng.Intn(2) == 1
				}
				checkSmallOracle(t, p, g, selected, cuts)
			}
		}
	}
	v := terms(t, "100", "150")
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 30 {
				got, err := v.Attribute([]productvalue.Range{r(60, 150)}, []productvalue.Range{r(0, 100)})
				if err != nil || got.Value.Amount != "60" || !reflect.DeepEqual(got.ByEdge, []string{"60"}) {
					t.Error("concurrent immutable Terms did not preserve result")
				}
			}
		})
	}
	wg.Wait()
}
