package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestRoutePublishInputJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want *routing.CostPolicy
	}{
		{"omitted-policy-preserves-default-selection", `{"expected_revision":0,"bindings":[]}`, nil},
		{"manual-allow", `{"cost_policy":{"mode":"manual","unknown_cost_policy":"allow"}}`, &routing.CostPolicy{Mode: "manual", UnknownCostPolicy: "allow"}},
		{"manual-deny", `{"cost_policy":{"mode":"manual","unknown_cost_policy":"deny"}}`, &routing.CostPolicy{Mode: "manual", UnknownCostPolicy: "deny"}},
		{"cost-first", `{"cost_policy":{"mode":"cost_first","unknown_cost_policy":"deny"}}`, &routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got RoutePublishInput
			if err := json.Unmarshal([]byte(tc.body), &got); err != nil {
				t.Fatal(err)
			}
			if (got.CostPolicy == nil) != (tc.want == nil) || got.CostPolicy != nil && *got.CostPolicy != *tc.want {
				t.Fatalf("policy=%+v want=%+v", got.CostPolicy, tc.want)
			}
		})
	}
	for name, body := range map[string]string{
		"explicit-null-policy":      `{"cost_policy":null}`,
		"empty-policy":              `{"cost_policy":{}}`,
		"missing-mode":              `{"cost_policy":{"unknown_cost_policy":"deny"}}`,
		"missing-unknown-rule":      `{"cost_policy":{"mode":"manual"}}`,
		"unknown-mode":              `{"cost_policy":{"mode":"cheapest","unknown_cost_policy":"deny"}}`,
		"unknown-rule":              `{"cost_policy":{"mode":"manual","unknown_cost_policy":"zero"}}`,
		"cost-first-allows-unknown": `{"cost_policy":{"mode":"cost_first","unknown_cost_policy":"allow"}}`,
		"policy-number":             `{"cost_policy":1}`,
		"binding-carries-policy":    `{"bindings":[{"cost_policy":{"mode":"manual","unknown_cost_policy":"allow"}}]}`,
		"binding-carries-mode":      `{"bindings":[{"mode":"manual"}]}`,
		"policy-carries-channel":    `{"cost_policy":{"mode":"manual","unknown_cost_policy":"allow","channel_id":"wrong-position"}}`,
		"top-level-carries-cap":     `{"max_input_cost_rate":"0"}`,
		"top-level-unknown":         `{"extra":true}`,
		"null-top-level":            `null`,
		"array-top-level":           `[]`,
		"scalar-top-level":          `1`,
		"trailing-json":             `{"bindings":[]} {}`,
		"numeric-input-cap":         `{"bindings":[{"max_input_cost_rate":1}]}`,
		"boolean-output-cap":        `{"bindings":[{"max_output_cost_rate":false}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var got RoutePublishInput
			if err := json.Unmarshal([]byte(body), &got); err == nil {
				t.Fatal("invalid route JSON accepted")
			}
		})
	}
	var caps RoutePublishInput
	if err := json.Unmarshal([]byte(`{"bindings":[{"channel_id":"channel","upstream_model":"model","priority":7,"weight":9,"max_inflight":11,"max_input_cost_rate":"9223372036854775807","max_output_cost_rate":null}]}`), &caps); err != nil {
		t.Fatal(err)
	}
	if len(caps.Bindings) != 1 || caps.Bindings[0].MaxInputCostRate == nil || *caps.Bindings[0].MaxInputCostRate != "9223372036854775807" || caps.Bindings[0].MaxOutputCostRate != nil {
		t.Fatal("cap JSON lost its exact decimal string or null")
	}
}

func TestCostVersionInputJSON(t *testing.T) {
	for _, instant := range []string{
		"2026-11-01T09:10:11Z",
		"2024-02-29T23:59:59.123456789Z",
		"2026-11-01T09:10:11.1+08:00",
		"2026-11-01T09:10:11-23:59",
		"2026-11-01T09:10:11+23:59",
		"0000-01-01T00:00:00Z",
		"9999-12-31T23:59:59.999999999Z",
	} {
		t.Run(instant, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"effective_at": instant, "input_rate": "9223372036854775807", "output_rate": "0", "expected_channel_revision": "1"})
			var got CostVersionInput
			if err := json.Unmarshal(body, &got); err != nil || got.EffectiveAt == nil {
				t.Fatalf("valid instant failed: %v", err)
			}
			want, err := time.Parse(time.RFC3339Nano, instant)
			if err != nil || !got.EffectiveAt.Equal(want) || got.InputRate == nil || *got.InputRate != "9223372036854775807" || got.OutputRate == nil || *got.OutputRate != "0" {
				t.Fatal("JSON changed precise timestamp or cost strings")
			}
		})
	}
	for _, body := range []string{`{}`, `{"effective_at":null,"input_rate":null,"output_rate":null}`} {
		var got CostVersionInput
		if err := json.Unmarshal([]byte(body), &got); err != nil || got.EffectiveAt != nil || got.InputRate != nil || got.OutputRate != nil {
			t.Fatalf("omitted/null optional values failed: %v", err)
		}
	}
	for _, instant := range []string{
		"2026-11-01", "2026-11-01 09:10:11Z", "2026-11-01t09:10:11z",
		"2026-2-01T09:10:11Z", "2026-11-1T09:10:11Z", "2026-11-01T9:10:11Z",
		"2026-11-01T09:1:11Z", "2026-11-01T09:10:1Z", "2026-11-01T24:00:00Z",
		"2026-11-01T09:60:00Z", "2026-11-01T09:10:60Z", "2026-02-29T09:10:11Z",
		"2026-13-01T09:10:11Z", "2026-11-31T09:10:11Z", "2026-11-01T09:10:11",
		"2026-11-01T09:10:11+24:00", "2026-11-01T09:10:11+08:60", "2026-11-01T09:10:11+8:00",
		"2026-11-01T09:10:11+0800", "2026-11-01T09:10:11.1234567890Z", "2026-11-01T09:10:11.Z",
		"2026-11-01T09:10:11,1Z", "-001-01-01T00:00:00Z", "10000-01-01T00:00:00Z",
		" 2026-11-01T09:10:11Z", "2026-11-01T09:10:11Z ",
	} {
		t.Run("invalid-"+instant, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"effective_at": instant})
			var got CostVersionInput
			if err := json.Unmarshal(body, &got); err == nil {
				t.Fatal("noncanonical RFC3339 accepted")
			}
		})
	}
	for name, body := range map[string]string{
		"unknown-field":            `{"extra":1}`,
		"route-policy-field":       `{"cost_policy":{"mode":"manual","unknown_cost_policy":"allow"}}`,
		"null-top-level":           `null`,
		"array-top-level":          `[]`,
		"scalar-top-level":         `"2026-11-01T00:00:00Z"`,
		"trailing-json":            `{} {}`,
		"numeric-effective-at":     `{"effective_at":1}`,
		"numeric-input-rate":       `{"input_rate":1}`,
		"boolean-output-rate":      `{"output_rate":false}`,
		"numeric-channel-revision": `{"expected_channel_revision":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			var got CostVersionInput
			if err := json.Unmarshal([]byte(body), &got); err == nil {
				t.Fatal("invalid cost version JSON accepted")
			}
		})
	}
}

func TestCostRateCanonicalInput(t *testing.T) {
	if got, err := parseCostRate(nil); err != nil || got != nil {
		t.Fatalf("null rate=%v err=%v", got, err)
	}
	for _, want := range []int64{0, 1, 1000000, math.MaxInt64} {
		value := strconv.FormatInt(want, 10)
		got, err := parseCostRate(&value)
		if err != nil || got == nil || *got != want {
			t.Fatalf("canonical rate %q=%v err=%v", value, got, err)
		}
	}
	for _, value := range []string{"", "-1", "-0", "+0", "+1", "00", "01", " 1", "1 ", "1\n", "1.0", "1e6", "0x10", "9223372036854775808", "18446744073709551615", "１２", "null"} {
		got, err := parseCostRate(&value)
		if !errors.Is(err, ErrInvalid) || got != nil {
			t.Fatalf("noncanonical rate %q=%v err=%v", value, got, err)
		}
	}
}

func TestCostVersionUTCYearBoundsBeforeTransaction(t *testing.T) {
	// The local timestamp can use four digits while conversion to the DB's UTC
	// publication instant crosses that boundary. These must fail before DB use.
	for _, instant := range []string{"0000-01-01T00:00:00+23:59", "9999-12-31T23:59:59-23:59"} {
		var in CostVersionInput
		body, _ := json.Marshal(map[string]any{"channel_id": "c514894a-bfca-4e6b-8f70-27373d38027c", "upstream_model": "model", "basis": "unknown", "expected_channel_revision": "1", "effective_at": instant})
		if err := json.Unmarshal(body, &in); err != nil {
			t.Fatal(err)
		}
		if _, err := (&Store{}).PublishCostVersion(context.Background(), "test", in); !errors.Is(err, ErrInvalid) {
			t.Fatalf("UTC year outside 0..9999 was not rejected: %v", err)
		}
	}
}
