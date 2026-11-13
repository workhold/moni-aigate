package catalog

import (
	"math/big"
	"testing"
	"time"
)

func TestGroupMultiplierUsesExactSixPlaceRational(t *testing.T) {
	for _, tc := range []struct {
		raw, numerator string
	}{
		{"0.85", "850000"},
		{"1", "1000000"},
		{"100", "100000000"},
		{"0.000001", "1"},
	} {
		got, err := parseGroupMultiplier(tc.raw)
		if err != nil || got.numerator.String() != tc.numerator || got.raw != tc.raw {
			t.Fatalf("%s: %#v/%v", tc.raw, got, err)
		}
	}
	for _, raw := range []string{"0", "0.000000", "100.000001", "01", ".85", "1.0000001", "1e-1", "-1"} {
		if _, err := parseGroupMultiplier(raw); err == nil {
			t.Fatalf("accepted invalid multiplier %q", raw)
		}
	}
}

func TestCompileRateRoundsUpWithBigInt(t *testing.T) {
	large := int64(9_007_199_254_740_993)
	multiplier := big.NewInt(850_000)
	got, err := compileRate(large, multiplier)
	if err != nil || got != 7_656_119_366_529_845 {
		t.Fatalf("large ceil: got %d/%v", got, err)
	}
	for _, tc := range []struct {
		base, want int64
		mult       int64
	}{
		{0, 0, 1},
		{1, 1, 1},
		{1, 2, 1_000_001},
		{100, 85, 850_000},
	} {
		got, err := compileRate(tc.base, big.NewInt(tc.mult))
		if err != nil || got != tc.want {
			t.Fatalf("%d*%d: got %d/%v, want %d", tc.base, tc.mult, got, err, tc.want)
		}
	}
	if _, err := compileRate(9223372036854775807, big.NewInt(100_000_000)); err == nil {
		t.Fatal("overflow was accepted")
	}
}

func TestNormalizeGroupPriceCompileDeduplicatesAndSorts(t *testing.T) {
	first := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	second := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	group := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	op := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	c, m, err := normalizeGroupPriceCompile(GroupPriceCompileCommand{OperationID: op, GroupID: group, Multiplier: "0.85", ModelIDs: []string{second, first, second}, EffectiveAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), ExpectedBaseVersionIDs: map[string]string{first: second}})
	if err != nil || m.raw != "0.85" || len(c.ModelIDs) != 2 || c.ModelIDs[0] != first || c.ModelIDs[1] != second || c.ExpectedBaseVersionIDs[first] != second {
		t.Fatalf("normalized compile: %#v/%#v/%v", c, m, err)
	}
	if _, _, err := normalizeGroupPriceCompile(GroupPriceCompileCommand{OperationID: op, GroupID: group, Multiplier: "0.85", ModelIDs: []string{first}, EffectiveAt: time.Time{}, ExpectedBaseVersionIDs: map[string]string{second: first}}); err == nil {
		t.Fatal("accepted invalid expected model")
	}
}
