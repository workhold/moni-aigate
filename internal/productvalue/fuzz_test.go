package productvalue_test

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/productvalue"
)

func FuzzUnitAndOriginalEdgeOracle(f *testing.F) {
	f.Add([]byte{100, 150, 3, 10, 0, 1, 2})
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{63, 63, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) < 3 {
			return
		}
		p, g := int(raw[0]%64)+1, int(raw[1]%64)+1
		selected, cuts := make([]bool, g), make([]bool, p)
		for i := range selected {
			selected[i] = raw[2+i%(len(raw)-2)]&1 != 0
		}
		for i := range cuts {
			cuts[i] = raw[2+i%(len(raw)-2)]&2 != 0
		}
		checkSmallOracle(t, p, g, selected, cuts)
	})
}

func FuzzLargeAbsoluteSplitIsAdditive(f *testing.F) {
	f.Add(uint64(2), uint64(9), uint64(1), uint64(1))
	f.Add(uint64(math.MaxInt64-1), uint64(math.MaxInt64-2), uint64(math.MaxInt64-3), uint64(math.MaxInt64-4))
	f.Fuzz(func(t *testing.T, pRaw, gRaw, splitRaw, edgeRaw uint64) {
		p, g := int64(pRaw%math.MaxInt64)+1, int64(gRaw%math.MaxInt64)+1
		k := int64(splitRaw % (uint64(g) + 1))
		ranges := make([]productvalue.Range, 0, 2)
		if k > 0 {
			ranges = append(ranges, r(0, k))
		}
		if k < g {
			ranges = append(ranges, r(k, g))
		}
		x := int64(edgeRaw % (uint64(p) + 1))
		edges := make([]productvalue.Range, 0, 2)
		if x > 0 {
			edges = append(edges, r(0, x))
		}
		if x < p {
			edges = append(edges, r(x, p))
		}
		got, err := terms(t, strconv.FormatInt(p, 10), strconv.FormatInt(g, 10)).Attribute(ranges, edges)
		if err != nil || got.Value.Amount != strconv.FormatInt(p, 10) || got.Value.Units != strconv.FormatInt(g, 10) {
			t.Fatalf("full split did not preserve P/G: %#v %v", got, err)
		}
		for i, edge := range edges {
			a, _ := strconv.ParseInt(edge.Start, 10, 64)
			b, _ := strconv.ParseInt(edge.End, 10, 64)
			if got.ByEdge[i] != strconv.FormatInt(b-a, 10) {
				t.Fatal("full split did not restore exact original edge amount")
			}
		}
	})
}

func FuzzStrictInputsFailAtomically(f *testing.F) {
	f.Add("3", "10", "0", "10", "2026-01-31T00:00:00.000001Z", "1")
	f.Add("private-invalid", "0", "-1", "-1", "bad-time", "9223372036854775807")
	f.Fuzz(func(t *testing.T, p, g, start, end, anchor, offset string) {
		v, err := productvalue.New(p, g)
		checkSafeError(t, err)
		if err == nil {
			got, err := v.Attribute([]productvalue.Range{{Start: start, End: end}}, []productvalue.Range{{Start: "0", End: p}})
			checkSafeError(t, err)
			if err != nil && !reflect.DeepEqual(got, productvalue.Attribution{}) {
				t.Fatal("failed attribution returned partial data")
			}
		}
		date, err := productvalue.MonthAt(anchor, offset)
		checkSafeError(t, err)
		if err != nil && date != "" {
			t.Fatal("failed month returned partial data")
		}
		term, err := productvalue.TermAt(anchor, offset)
		checkSafeError(t, err)
		if err != nil && term != (productvalue.Term{}) {
			t.Fatal("failed term returned partial data")
		}
	})
}

func checkSafeError(t testing.TB, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, allowed := range []error{
		productvalue.ErrInvalidValue, productvalue.ErrInvalidRanges, productvalue.ErrInvalidPartition,
		productvalue.ErrResourceLimit, productvalue.ErrInvalidTime, productvalue.ErrMonthOverflow,
	} {
		if errors.Is(err, allowed) && err.Error() == allowed.Error() {
			return
		}
	}
	t.Fatal("error is not a fixed safe sentinel")
}
