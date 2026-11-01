package identity

import (
	"encoding/json"
	"errors"
	"testing"
)

func keyLimitInt(value int) *int { return &value }

func TestKeyCreationRuntimeLimitsPresenceAndValues(t *testing.T) {
	roundtrip, err := json.Marshal(KeyInput{Name: "key", Scopes: []string{"model"}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded KeyInput
	if err = json.Unmarshal(roundtrip, &decoded); err != nil {
		t.Fatalf("omitted defaults were serialized as explicit null: %s %v", roundtrip, err)
	}
	for _, raw := range []string{`{}`, `{"name":"key","scopes":["model"]}`} {
		var in KeyInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil || in.RPMLimit != nil || in.BurstLimit != nil || in.ConcurrencyLimit != nil {
			t.Fatalf("omitted creation limits lost default distinction: %+v %v", in, err)
		}
		got, err := keyRuntimeLimitValues(in.RPMLimit, in.BurstLimit, in.ConcurrencyLimit, true)
		if err != nil || got.RPMLimit != 60 || got.BurstLimit != 20 || got.ConcurrencyLimit != 16 {
			t.Fatalf("wrong defaults: %+v %v", got, err)
		}
	}
	for _, field := range []string{"rpm_limit", "burst_limit", "concurrency_limit"} {
		var in KeyInput
		if err := json.Unmarshal([]byte(`{"`+field+`": null}`), &in); !errors.Is(err, ErrKeyLimitsInvalid) {
			t.Fatalf("explicit null %s accepted: %v", field, err)
		}
		for _, value := range []string{`1.0`, `1e0`, `"1"`, `true`, `[]`, `{}`, `9999999999999999999999999999`} {
			if err := json.Unmarshal([]byte(`{"`+field+`":`+value+`}`), &in); err == nil {
				t.Fatalf("non-integer field %s=%s accepted", field, value)
			}
		}
	}
	for _, values := range [][3]int{{1, 1, 1}, {6000, 200, 16}, {1, 200, 16}} {
		if _, err := keyRuntimeLimitValues(keyLimitInt(values[0]), keyLimitInt(values[1]), keyLimitInt(values[2]), false); err != nil {
			t.Fatalf("valid limits rejected: %v %v", values, err)
		}
	}
	for _, values := range [][3]int{{0, 20, 16}, {-1, 20, 16}, {6001, 20, 16}, {60, 0, 16}, {60, -1, 16}, {60, 201, 16}, {60, 20, 0}, {60, 20, -1}, {60, 20, 17}} {
		for _, defaults := range []bool{false, true} {
			if _, err := keyRuntimeLimitValues(keyLimitInt(values[0]), keyLimitInt(values[1]), keyLimitInt(values[2]), defaults); !errors.Is(err, ErrKeyLimitsInvalid) {
				t.Fatalf("invalid limits accepted with defaults=%v: %v %v", defaults, values, err)
			}
		}
	}
	if _, err := keyRuntimeLimitValues(nil, keyLimitInt(20), keyLimitInt(16), false); !errors.Is(err, ErrKeyLimitsInvalid) {
		t.Fatal("missing replacement integer accepted")
	}
	var explicit KeyInput
	if err := json.Unmarshal([]byte(`{"rpm_limit":7,"burst_limit":8,"concurrency_limit":2,"allowed_client_cidrs":null}`), &explicit); err != nil || explicit.RPMLimit == nil || *explicit.RPMLimit != 7 || explicit.BurstLimit == nil || *explicit.BurstLimit != 8 || explicit.ConcurrencyLimit == nil || *explicit.ConcurrencyLimit != 2 {
		t.Fatalf("explicit creation limits lost: %+v %v", explicit, err)
	}
}
