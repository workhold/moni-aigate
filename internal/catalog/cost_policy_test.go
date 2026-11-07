package catalog

import (
	"encoding/json"
	"errors"
	"strconv"
	"testing"
)

func TestRoutePublishInputJSON(t *testing.T) {
	for _, body := range []string{
		`{"expected_revision":0,"bindings":[]}`,
		`{"cost_policy":{"mode":"manual","unknown_cost_policy":"allow"}}`,
		`{"cost_policy":{"mode":"cost_first","unknown_cost_policy":"deny"}}`,
	} {
		var got RoutePublishInput
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
	}
	for _, body := range []string{`{"cost_policy":null}`, `{"cost_policy":{}}`, `{"extra":true}`, `null`, `[]`, `1`} {
		var got RoutePublishInput
		if err := json.Unmarshal([]byte(body), &got); err == nil {
			t.Fatalf("invalid route JSON accepted: %s", body)
		}
	}
	var caps RoutePublishInput
	if err := json.Unmarshal([]byte(`{"bindings":[{"channel_id":"channel","upstream_model":"model","priority":7,"weight":9,"max_inflight":11,"max_input_cost_rate":"9223372036854775807","max_output_cost_rate":null}]}`), &caps); err != nil {
		t.Fatal(err)
	}
	if len(caps.Bindings) != 1 || caps.Bindings[0].MaxInputCostRate == nil || *caps.Bindings[0].MaxInputCostRate != "9223372036854775807" || caps.Bindings[0].MaxOutputCostRate != nil {
		t.Fatal("cap JSON lost exact decimal values")
	}
}

func TestCostRateCanonicalInput(t *testing.T) {
	for _, want := range []int64{0, 1, 1000000, int64(^uint64(0) >> 1)} {
		value := strconv.FormatInt(want, 10)
		got, err := parseCostRate(&value)
		if err != nil || got == nil || *got != want {
			t.Fatalf("rate %q=%v err=%v", value, got, err)
		}
	}
	for _, value := range []string{"", "-1", "-0", "+0", "01", " 1", "1 ", "1.0", "1e6", "9223372036854775808"} {
		got, err := parseCostRate(&value)
		if !errors.Is(err, ErrInvalid) || got != nil {
			t.Fatalf("noncanonical rate %q=%v err=%v", value, got, err)
		}
	}
}
