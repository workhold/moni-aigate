package economics

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestMoneyCanonicalDecimalStringJSON(t *testing.T) {
	for _, value := range []Money{0, 1, -1, math.MaxInt64, math.MinInt64} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != `"`+value.String()+`"` {
			t.Fatalf("non-canonical money JSON: %s", raw)
		}
		var decoded Money
		if err = json.Unmarshal(raw, &decoded); err != nil || decoded != value {
			t.Fatalf("round trip %d: %v", value, err)
		}
	}
	for _, raw := range []string{
		`1`, `1.0`, `null`, `true`, `""`, `"1.0"`, `"01"`, `"-0"`,
		`"+1"`, `" 1"`, `"9223372036854775808"`, `"-9223372036854775809"`,
	} {
		var value Money
		if err := json.Unmarshal([]byte(raw), &value); !errors.Is(err, ErrInvalidMoney) {
			t.Fatalf("accepted invalid money %s: %v", raw, err)
		}
	}
}

func TestMoneyVectorStrictJSON(t *testing.T) {
	original := MoneyVector{CurrencyUSD: 2, CurrencyCNY: -3}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"CNY":"-3","USD":"2"}` {
		t.Fatalf("non-canonical vector JSON: %s", raw)
	}
	var decoded MoneyVector
	if err = json.Unmarshal(raw, &decoded); err != nil || !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip: %#v %v", decoded, err)
	}
	empty := MoneyVector{}
	if raw, err = json.Marshal(empty); err != nil || string(raw) != `{}` {
		t.Fatalf("known empty vector: %s %v", raw, err)
	}
	var nilVector MoneyVector
	if _, err = json.Marshal(nilVector); !errors.Is(err, ErrInvalidVector) {
		t.Fatalf("nil vector treated as known empty: %v", err)
	}
	for _, raw := range []string{
		`null`, `[]`, `{"CNY":1}`, `{"CNY":1.5}`, `{"CNY":"01"}`,
		`{"EUR":"1"}`, `{"CNY":"1","CNY":"2"}`, `{"CNY":"1"} {}`,
	} {
		var value MoneyVector
		if err = json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("accepted invalid vector: %s", raw)
		}
	}
}

func TestMoneyVectorRequiredCurrenciesAndClone(t *testing.T) {
	vector, err := NewMoneyVector(map[Currency]Money{CurrencyCNY: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = vector.Validate(CurrencyCNY, CurrencyUSD); !errors.Is(err, ErrMissingCurrency) {
		t.Fatalf("missing required currency: %v", err)
	}
	clone := vector.Clone()
	clone[CurrencyCNY] = 99
	if vector[CurrencyCNY] != 1 {
		t.Fatal("clone aliases original")
	}
	if _, ok := vector.Amount(CurrencyUSD); ok {
		t.Fatal("missing USD reported present")
	}
}

func TestMoneyVectorAddAndOverflow(t *testing.T) {
	left := MoneyVector{CurrencyCNY: 2}
	right := MoneyVector{CurrencyCNY: 3, CurrencyUSD: 4}
	got, err := left.Add(right)
	if err != nil || !reflect.DeepEqual(got, MoneyVector{CurrencyCNY: 5, CurrencyUSD: 4}) {
		t.Fatalf("sum: %#v %v", got, err)
	}
	if _, err = left.Add(MoneyVector{CurrencyCNY: math.MaxInt64}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("addition overflow: %v", err)
	}
	if _, err = (MoneyVector{CurrencyCNY: math.MinInt64}).Add(MoneyVector{CurrencyCNY: -1}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("negative addition overflow: %v", err)
	}
}
