package billing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestFiatMoneyJSONRetainsExactDecimalStringContract(t *testing.T) {
	for _, value := range []FiatMoney{0, 1, -1, math.MaxInt64, math.MinInt64} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded FiatMoney
		if err = json.Unmarshal(raw, &decoded); err != nil || decoded != value {
			t.Fatalf("fiat round trip changed %d: %s %v", value, raw, err)
		}
		var text string
		if err = json.Unmarshal(raw, &text); err != nil {
			t.Fatal("fiat amount is no longer a JSON string", err)
		}
	}
	for _, raw := range []string{`1`, `null`, `"1.0"`, `"9223372036854775808"`, `""`} {
		var value FiatMoney
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("invalid fiat amount accepted: %s", raw)
		}
	}
}

func TestFiatMoneyAndCoinAreNotAssignable(t *testing.T) {
	coin, fiat := reflect.TypeOf(Money(0)), reflect.TypeOf(FiatMoney(0))
	if coin.AssignableTo(fiat) || fiat.AssignableTo(coin) {
		t.Fatal("fiat and COIN amounts are implicitly assignable")
	}
}
