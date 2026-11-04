package inference_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/inference"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

const (
	schemaID   = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	profileID  = "11111111-1111-1111-1111-111111111111"
	tenantID   = "22222222-2222-2222-2222-222222222222"
	requestID  = "33333333-3333-3333-3333-333333333333"
	attemptID  = "44444444-4444-4444-4444-444444444444"
	priceID    = "55555555-5555-5555-5555-555555555555"
	otherID    = "66666666-6666-6666-6666-666666666666"
	textSchema = `{"schema_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"1000"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"1000"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`
)

func schema(t testing.TB, raw string) metering.Schema {
	t.Helper()
	s, err := metering.ParseSchema([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func profileInput(t testing.TB) inference.TextProfileInput {
	return inference.TextProfileInput{Version: profileID, Model: "model", VariantKey: "default", Protocol: inference.ProtocolChat, Schema: schema(t, textSchema), ContractDigest: [32]byte{1}, InputTokenUpperBound: 100, OutputLimit: 20}
}
func profile(t testing.TB, in inference.TextProfileInput) inference.Profile {
	t.Helper()
	p, err := inference.NewTextProfile(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func plan(t testing.TB, p inference.Profile, output int64, stream bool) inference.BoundPlan {
	t.Helper()
	v, err := inference.NewTextPlan(inference.TextPlanInput{Candidates: []inference.Profile{p}, InputHMAC: [32]byte{2}, OutputLimit: output, Stream: stream})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func attempt(t testing.TB, p inference.BoundPlan, profile inference.Profile) inference.BoundPlan {
	t.Helper()
	a, err := p.ForAttempt(profile)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func marshal(t testing.TB, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func unmarshal(t testing.TB, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func hexDigest(v interface{ Digest() ([32]byte, error) }) string {
	d, _ := v.Digest()
	return hex.EncodeToString(d[:])
}

func quoteBody(t testing.TB, p inference.BoundPlan, side metering.Side) map[string]any {
	t.Helper()
	s, _ := p.Schema()
	id, _ := s.ID()
	b, _ := p.Bounds(side)
	raw, _ := b.CanonicalJSON()
	return map[string]any{"version": "1", "status": "known", "side": string(side), "plan_digest": hexDigest(p), "schema_id": id, "schema_digest": hexDigest(s), "variant_key": "default", "version_id": priceID, "unknown_reason": nil, "bounds": unmarshal(t, raw), "rates": map[string]any{"schema_id": id, "version": "1", "side": string(side), "currency": "CNY", "rates": []any{map[string]any{"dimension": "text_input_token", "unit": "token", "numerator": "1", "denominator": "3"}, map[string]any{"dimension": "text_output_token", "unit": "token", "numerator": "2", "denominator": "3"}}}}
}
func rates(body map[string]any) []any { return body["rates"].(map[string]any)["rates"].([]any) }
func boundEntries(body map[string]any) []any {
	return body["bounds"].(map[string]any)["bounds"].([]any)
}
func binding(t testing.TB, p inference.BoundPlan, s metering.Schema) inference.ObservationBinding {
	t.Helper()
	d, _ := p.Digest()
	return inference.ObservationBinding{TenantID: tenantID, RequestID: requestID, AttemptID: attemptID, ProfileVersion: profileID, VariantKey: "default", Schema: s, PlanDigest: d}
}
func observationBody(t testing.TB, b inference.ObservationBinding) map[string]any {
	t.Helper()
	sID, _ := b.Schema.ID()
	ds, _ := b.Schema.Dimensions()
	entries := make([]any, len(ds))
	for i, d := range ds {
		q := "3"
		if d.Dimension == "text_output_token" {
			q = "4"
		}
		entries[i] = map[string]any{"dimension": d.Dimension, "unit": d.Unit, "status": "confirmed_final", "quantity": q}
	}
	return map[string]any{"version": "1", "tenant_id": b.TenantID, "request_id": b.RequestID, "attempt_id": b.AttemptID, "source": "adapter", "source_event_id": "event_1", "profile_version": b.ProfileVersion, "schema_id": sID, "schema_digest": hexDigest(b.Schema), "variant_key": "default", "plan_digest": hex.EncodeToString(b.PlanDigest[:]), "provider_reference": "resp_1", "observed_at": "2026-11-04T01:02:03Z", "protocol_outcome": "completed", "transport_end_reason": "done", "delivery_stage": "content", "content_written": true, "finish_reason": "stop", "quantities": entries, "unexpected_charge_reason": nil}
}
func quantity(body map[string]any, i int) map[string]any {
	return body["quantities"].([]any)[i].(map[string]any)
}
func parseObservation(t testing.TB, body map[string]any, b inference.ObservationBinding) inference.Observation {
	t.Helper()
	o, err := inference.ParseObservation(marshal(t, body), b)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestDomainGoldenAndExactAmount(t *testing.T) {
	// These values were independently calculated from the literal schema and
	// contract's ordered JSON/framing with Python hashlib, not from Go output.
	p := profile(t, profileInput(t))
	request := plan(t, p, 20, false)
	a := attempt(t, request, p)
	q, err := inference.ParseQuoteSnapshot(marshal(t, quoteBody(t, request, metering.Retail)), request, metering.Retail)
	if err != nil {
		t.Fatal(err)
	}
	b := binding(t, a, schema(t, textSchema))
	o := parseObservation(t, observationBody(t, b), b)
	f, err := o.Confirmed(metering.Retail)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name, want string
		value      interface{ Digest() ([32]byte, error) }
	}{
		{"schema", "8edbcf79f3b660e80791fda9931896d6e46a74f52731a1d628e5f8209d9ffa44", b.Schema},
		{"profile", "deda8b3f92aaf11cee7df346ebe0f3caf674fdcf62bd06b64c88cff93ed43431", p},
		{"request", "db46427f61c831a1e202dee18ea74b432ddb40bc4e409f4483f509e72c51bf8d", request},
		{"attempt", "bb5e592594506e2fe2ade95cfe321518e6a4d6008b949b88e9051605af5b5e08", a},
		{"quote", "88777fbf011b80ea2a55f93b53b8e186a4fc04a9757bed5e4377901f46077407", q},
		{"observation", "ebe023c1e3e52ab5d3e3589d2e8ddedfa3756cf8f66e1eab8ed5c38399490550", o},
		{"final", "468716cd0b6775d0b2c45acfc6160bea803265e0b7801ca7570a0ac30b77f6d6", f},
	} {
		t.Run(v.name, func(t *testing.T) {
			if got := hexDigest(v.value); got != v.want {
				t.Fatalf("got %s, want %s", got, v.want)
			}
		})
	}
	if amount, err := q.Amount(); err != nil || amount != 47 {
		t.Fatalf("reserve = %d, %v; want ceil(140/3)=47", amount, err)
	}
	quantities, _ := f.Quantities()
	price, _ := q.Rates()
	actual, err := metering.Amount(quantities, price)
	if err != nil || actual.String() != "4" {
		t.Fatalf("actual=%v %v; want ceil(11/3)=4", actual, err)
	}
	meta, _ := o.Metadata()
	if meta.FinishReason != "stop" || meta.Source != "adapter" {
		t.Fatal("lost original metadata")
	}
}

func TestSixProtocolsAndClosedProfiles(t *testing.T) {
	for _, protocol := range []inference.Protocol{inference.ProtocolChat, inference.ProtocolResponses, inference.ProtocolMessages, inference.ProtocolGemini, inference.ProtocolEmbeddings, inference.ProtocolRerank} {
		t.Run(string(protocol), func(t *testing.T) {
			in := profileInput(t)
			in.Protocol = protocol
			inputOnly := protocol == inference.ProtocolEmbeddings || protocol == inference.ProtocolRerank
			if inputOnly {
				in.OutputLimit = 0
			}
			p := profile(t, in)
			v := plan(t, p, in.OutputLimit, false)
			a := attempt(t, v, p)
			info, _ := p.Info()
			if len(info.ResultKinds) == 2 && inputOnly || len(info.ResultKinds) != 2 && !inputOnly {
				t.Fatal("wrong result kinds")
			}
			for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
				bounds, _ := a.Bounds(side)
				raw, _ := bounds.CanonicalJSON()
				if inputOnly && !bytes.Contains(raw, []byte(`"text_output_token","unit":"token","quantity":"0"`)) {
					t.Fatal("output 0 omitted")
				}
				body := quoteBody(t, v, side)
				target := v
				if side == metering.Supplier {
					target = a
					body = quoteBody(t, a, side)
				}
				if inputOnly {
					line := rates(body)[1].(map[string]any)
					line["numerator"] = "0"
					line["denominator"] = "1"
				}
				if _, err := inference.ParseQuoteSnapshot(marshal(t, body), target, side); err != nil {
					t.Fatal(err)
				}
			}
			if inputOnly {
				if _, err := inference.NewTextPlan(inference.TextPlanInput{Candidates: []inference.Profile{p}, InputHMAC: [32]byte{2}, Stream: true}); !errors.Is(err, inference.ErrInvalidPlan) {
					t.Fatal("input-only stream accepted", err)
				}
			} else {
				stream := plan(t, p, 20, true)
				raw, _ := stream.CanonicalJSON()
				if !bytes.Contains(raw, []byte(`"result_kind":"events"`)) || hexDigest(stream) == hexDigest(v) {
					t.Fatal("stream not bound")
				}
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*inference.TextProfileInput)
	}{
		{"empty_protocol", func(p *inference.TextProfileInput) { p.Protocol = "" }},
		{"unknown_protocol", func(p *inference.TextProfileInput) { p.Protocol = "openai_images" }},
		{"old_version", func(p *inference.TextProfileInput) { p.Version = "fixture-v1" }},
		{"nil_version", func(p *inference.TextProfileInput) { p.Version = "00000000-0000-0000-0000-000000000000" }},
		{"empty_variant", func(p *inference.TextProfileInput) { p.VariantKey = "" }},
		{"foreign_variant", func(p *inference.TextProfileInput) { p.VariantKey = "high" }},
		{"zero_input", func(p *inference.TextProfileInput) { p.InputTokenUpperBound = 0 }},
		{"above_schema", func(p *inference.TextProfileInput) { p.InputTokenUpperBound = 1001 }},
		{"no_generation_output", func(p *inference.TextProfileInput) { p.OutputLimit = 0 }},
		{"input_only_generation", func(p *inference.TextProfileInput) { p.Protocol = inference.ProtocolEmbeddings }},
		{"control_model", func(p *inference.TextProfileInput) { p.Model = "bad\nmodel" }},
		{"invalid_unicode", func(p *inference.TextProfileInput) { p.Model = string([]byte{0xff}) }},
		{"missing_contract", func(p *inference.TextProfileInput) { p.ContractDigest = [32]byte{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := profileInput(t)
			tc.mutate(&in)
			p, err := inference.NewTextProfile(in)
			if !errors.Is(err, inference.ErrInvalidProfile) {
				t.Fatal(err)
			}
			if _, err = p.CanonicalJSON(); !errors.Is(err, inference.ErrInvalidValue) {
				t.Fatal("returned partial profile")
			}
		})
	}
}

func TestCandidateUnionTighteningAndOwnership(t *testing.T) {
	in := profileInput(t)
	p := profile(t, in)
	in.Version = otherID
	in.InputTokenUpperBound = 120
	p2 := profile(t, in)
	candidates := []inference.Profile{p2, p}
	input := inference.TextPlanInput{Candidates: candidates, InputHMAC: [32]byte{2}, OutputLimit: 20}
	v, err := inference.NewTextPlan(input)
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := inference.NewTextPlan(inference.TextPlanInput{Candidates: []inference.Profile{p, p2}, InputHMAC: input.InputHMAC, OutputLimit: 20})
	if err != nil || hexDigest(v) != hexDigest(reverse) {
		t.Fatal("order-dependent plan")
	}
	candidates[0] = inference.Profile{}
	candidates[1] = inference.Profile{}
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		bound, _ := v.Bounds(side)
		raw, _ := bound.CanonicalJSON()
		if !bytes.Contains(raw, []byte(`"quantity":"120"`)) {
			t.Fatal("common bound not max")
		}
		a := attempt(t, v, p)
		bound, _ = a.Bounds(side)
		raw, _ = bound.CanonicalJSON()
		if !bytes.Contains(raw, []byte(`"quantity":"100"`)) {
			t.Fatal("candidate did not tighten")
		}
		if _, err := a.ForAttempt(p2); !errors.Is(err, inference.ErrInvalidPlan) {
			t.Fatal("attempt reparent accepted")
		}
	}
	changed := profileInput(t)
	changed.InputTokenUpperBound = 1
	foreign := profile(t, changed)
	if _, err := v.ForAttempt(foreign); !errors.Is(err, inference.ErrBindingMismatch) {
		t.Fatal("caller lowered input through unbound profile")
	}
	if _, err := v.Profile(); !errors.Is(err, inference.ErrInvalidPlan) {
		t.Fatal("public plan pretends one selected profile")
	}
	info, _ := p.Info()
	info.ResultKinds[0] = "changed"
	again, _ := p.Info()
	if again.ResultKinds[0] != inference.ResultJSON {
		t.Fatal("profile metadata alias")
	}
	for _, tc := range []struct {
		name  string
		input inference.TextPlanInput
		want  error
	}{
		{"empty", inference.TextPlanInput{}, inference.ErrInvalidPlan},
		{"zero_hmac", inference.TextPlanInput{Candidates: []inference.Profile{p}, OutputLimit: 20}, inference.ErrInvalidPlan},
		{"zero_profile", inference.TextPlanInput{Candidates: []inference.Profile{{}}, InputHMAC: input.InputHMAC, OutputLimit: 20}, inference.ErrInvalidPlan},
		{"duplicate", inference.TextPlanInput{Candidates: []inference.Profile{p, p}, InputHMAC: input.InputHMAC, OutputLimit: 20}, inference.ErrInvalidPlan},
		{"same_version_changed_contract", inference.TextPlanInput{Candidates: []inference.Profile{p, foreign}, InputHMAC: input.InputHMAC, OutputLimit: 20}, inference.ErrInvalidPlan},
		{"oversize", inference.TextPlanInput{Candidates: make([]inference.Profile, 257), InputHMAC: input.InputHMAC, OutputLimit: 20}, inference.ErrInvalidPlan},
		{"output_zero", inference.TextPlanInput{Candidates: []inference.Profile{p}, InputHMAC: input.InputHMAC}, inference.ErrInvalidPlan},
		{"output_too_large", inference.TextPlanInput{Candidates: []inference.Profile{p}, InputHMAC: input.InputHMAC, OutputLimit: 21}, inference.ErrInvalidPlan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := inference.NewTextPlan(tc.input)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if _, err = got.Digest(); !errors.Is(err, inference.ErrInvalidValue) {
				t.Fatal("partial plan")
			}
		})
	}
	for _, kind := range []string{"schema", "model", "protocol"} {
		t.Run(kind, func(t *testing.T) {
			other := profileInput(t)
			other.Version = otherID
			switch kind {
			case "schema":
				other.Schema = schema(t, strings.ReplaceAll(textSchema, "1000", "2000"))
			case "model":
				other.Model = "other"
			case "protocol":
				other.Protocol = inference.ProtocolResponses
			}
			bad := profile(t, other)
			if _, err := inference.NewTextPlan(inference.TextPlanInput{Candidates: []inference.Profile{p, bad}, InputHMAC: input.InputHMAC, OutputLimit: 20}); !errors.Is(err, inference.ErrBindingMismatch) {
				t.Fatal(err)
			}
		})
	}
}

func TestKnownUnknownQuotesAndZero(t *testing.T) {
	p := profile(t, profileInput(t))
	request := plan(t, p, 20, false)
	a := attempt(t, request, p)
	for _, reason := range []string{"version_missing", "basis_unknown"} {
		t.Run(reason, func(t *testing.T) {
			body := quoteBody(t, a, metering.Supplier)
			body["status"] = "unknown"
			body["unknown_reason"] = reason
			body["rates"] = nil
			if reason == "version_missing" {
				body["version_id"] = nil
			}
			raw := marshal(t, body)
			q, err := inference.ParseQuoteSnapshot(raw, a, metering.Supplier)
			if err != nil {
				t.Fatal(err)
			}
			raw[0] = 'x'
			if status, _ := q.Status(); status != inference.QuoteUnknown {
				t.Fatal(status)
			}
			if _, err = q.Amount(); !errors.Is(err, inference.ErrUnknownQuote) {
				t.Fatal("unknown became zero", err)
			}
			if _, err = q.Rates(); !errors.Is(err, inference.ErrUnknownQuote) {
				t.Fatal(err)
			}
			version, present, err := q.VersionID()
			if err != nil || present != (reason == "basis_unknown") || present && version != priceID {
				t.Fatal("lost selected version")
			}
		})
	}
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		target := request
		if side == metering.Supplier {
			target = a
		}
		body := quoteBody(t, target, side)
		for _, line := range rates(body) {
			line.(map[string]any)["numerator"] = "0"
			line.(map[string]any)["denominator"] = "1"
		}
		q, err := inference.ParseQuoteSnapshot(marshal(t, body), target, side)
		if err != nil {
			t.Fatal(err)
		}
		if amount, err := q.Amount(); err != nil || amount != 0 {
			t.Fatal("explicit zero unavailable")
		}
		if version, present, _ := q.VersionID(); !present || version != priceID {
			t.Fatal("zero quote lost version")
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{"unknown_retail", func(m map[string]any) {
			m["status"] = "unknown"
			m["unknown_reason"] = "version_missing"
			m["version_id"] = nil
			m["rates"] = nil
		}, inference.ErrInvalidQuote},
		{"known_no_version", func(m map[string]any) { m["version_id"] = nil }, inference.ErrInvalidQuote},
		{"known_no_rates", func(m map[string]any) { m["rates"] = nil }, inference.ErrInvalidQuote},
		{"missing_dimension", func(m map[string]any) { m["rates"].(map[string]any)["rates"] = rates(m)[:1] }, metering.ErrInvalidVector},
		{"foreign_plan", func(m map[string]any) { m["plan_digest"] = strings.Repeat("1", 64) }, inference.ErrBindingMismatch},
		{"malformed_plan", func(m map[string]any) { m["plan_digest"] = "UPPER" }, inference.ErrInvalidQuote},
		{"foreign_schema", func(m map[string]any) { m["schema_id"] = otherID }, inference.ErrBindingMismatch},
		{"malformed_schema", func(m map[string]any) { m["schema_id"] = "bad" }, inference.ErrInvalidQuote},
		{"variant_type", func(m map[string]any) { m["variant_key"] = 4 }, inference.ErrInvalidQuote},
		{"changed_bound", func(m map[string]any) { boundEntries(m)[0].(map[string]any)["quantity"] = "99" }, inference.ErrBindingMismatch},
		{"binding_plus_rate_lexical", func(m map[string]any) {
			m["plan_digest"] = strings.Repeat("1", 64)
			rates(m)[0].(map[string]any)["numerator"] = "01"
		}, metering.ErrInvalidVector},
		{"binding_plus_bad_version", func(m map[string]any) { m["plan_digest"] = strings.Repeat("1", 64); m["version_id"] = "bad" }, inference.ErrInvalidQuote},
		{"caller_amount", func(m map[string]any) { m["amount"] = "1" }, inference.ErrInvalidQuote},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := quoteBody(t, request, metering.Retail)
			tc.mutate(body)
			q, err := inference.ParseQuoteSnapshot(marshal(t, body), request, metering.Retail)
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if _, err = q.CanonicalJSON(); !errors.Is(err, inference.ErrInvalidValue) {
				t.Fatal("partial quote")
			}
		})
	}
	body := quoteBody(t, request, metering.Supplier)
	if _, err := inference.ParseQuoteSnapshot(marshal(t, body), request, metering.Supplier); !errors.Is(err, inference.ErrBindingMismatch) {
		t.Fatal("supplier accepted request scope")
	}
	body = quoteBody(t, a, metering.Retail)
	if _, err := inference.ParseQuoteSnapshot(marshal(t, body), a, metering.Retail); !errors.Is(err, inference.ErrBindingMismatch) {
		t.Fatal("retail accepted attempt scope")
	}
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		in := profileInput(t)
		in.Protocol = inference.ProtocolEmbeddings
		in.OutputLimit = 0
		p := profile(t, in)
		v := plan(t, p, 0, false)
		if side == metering.Supplier {
			v = attempt(t, v, p)
		}
		body := quoteBody(t, v, side)
		rates(body)[1].(map[string]any)["numerator"] = "1"
		rates(body)[1].(map[string]any)["denominator"] = "1"
		if _, err := inference.ParseQuoteSnapshot(marshal(t, body), v, side); !errors.Is(err, inference.ErrInvalidQuote) {
			t.Fatal("input-only nonzero output rate accepted", err)
		}
	}
}

func TestUnknownQuoteUnionAndOverflow(t *testing.T) {
	p := profile(t, profileInput(t))
	a := attempt(t, plan(t, p, 20, false), p)
	for _, tc := range []struct {
		name    string
		reason  any
		version any
		rates   bool
	}{
		{"missing_with_version", "version_missing", priceID, false},
		{"basis_without_version", "basis_unknown", nil, false},
		{"no_reason", nil, nil, false},
		{"unknown_reason", "default", nil, false},
		{"false_zero_rates", "version_missing", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := quoteBody(t, a, metering.Supplier)
			m["status"] = "unknown"
			m["unknown_reason"] = tc.reason
			m["version_id"] = tc.version
			if !tc.rates {
				m["rates"] = nil
			}
			if _, err := inference.ParseQuoteSnapshot(marshal(t, m), a, metering.Supplier); !errors.Is(err, inference.ErrInvalidQuote) {
				t.Fatal(err)
			}
		})
	}
	in := profileInput(t)
	in.Schema = schema(t, strings.ReplaceAll(textSchema, "1000", fmt.Sprint(math.MaxInt64)))
	in.InputTokenUpperBound = math.MaxInt64
	p = profile(t, in)
	v := plan(t, p, 20, false)
	m := quoteBody(t, v, metering.Retail)
	rates(m)[0].(map[string]any)["numerator"] = "2"
	rates(m)[0].(map[string]any)["denominator"] = "1"
	if _, err := inference.ParseQuoteSnapshot(marshal(t, m), v, metering.Retail); !errors.Is(err, metering.ErrAmountOverflow) || !errors.Is(err, inference.ErrInvalidQuote) {
		t.Fatal("overflow not rejected", err)
	}
}

func TestIndependentFinalQuantitiesAndManualSupplier(t *testing.T) {
	// Supplier-only request_count must not hold up independently known retail.
	s := schema(t, `{"schema_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","version":"1","dimensions":[{"dimension":"request_count","unit":"request","maximum_quantity":"1"},{"dimension":"text_input_token","unit":"token","maximum_quantity":"100"}],"retail_dimensions":["text_input_token"],"supplier_dimensions":["request_count","text_input_token"]}`)
	p := profile(t, profileInput(t))
	a := attempt(t, plan(t, p, 20, false), p)
	b := binding(t, a, s)
	for _, tc := range []struct {
		name             string
		status           string
		reason           any
		source           string
		retail, supplier error
	}{
		{"supplier_missing", "missing", nil, "adapter", nil, inference.ErrIncomplete},
		{"supplier_conflict", "conflicting", nil, "adapter", nil, inference.ErrConflicting},
		{"supplier_provisional", "provisional", nil, "adapter", nil, inference.ErrIncomplete},
		{"all_final", "confirmed_final", nil, "adapter", nil, nil},
		{"unmapped_supplier", "confirmed_final", "supplier_unmapped_charge", "adapter", nil, inference.ErrIncomplete},
		{"unattributable", "confirmed_final", "unattributable_charge", "adapter", inference.ErrIncomplete, inference.ErrIncomplete},
		{"manual_retail_only", "confirmed_final", nil, "approved_reconciliation", nil, inference.ErrIncomplete},
		{"manual_conflict_precedence", "conflicting", nil, "approved_reconciliation", nil, inference.ErrConflicting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := observationBody(t, b)
			m["source"] = tc.source
			m["unexpected_charge_reason"] = tc.reason
			quantity(m, 0)["status"] = tc.status
			quantity(m, 0)["quantity"] = nil
			if tc.status == "confirmed_final" || tc.status == "provisional" {
				quantity(m, 0)["quantity"] = "1"
			}
			o := parseObservation(t, m, b)
			for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
				want := tc.retail
				if side == metering.Supplier {
					want = tc.supplier
				}
				f, err := o.Confirmed(side)
				if !errors.Is(err, want) {
					t.Fatalf("side %s: got %v want %v", side, err, want)
				}
				if want == nil {
					q, _ := f.Quantities()
					raw, _ := q.CanonicalJSON()
					if side == metering.Retail && bytes.Contains(raw, []byte("request_count")) {
						t.Fatal("supplier quantity leaked into retail")
					}
				} else if _, err = f.Quantities(); !errors.Is(err, inference.ErrInvalidValue) {
					t.Fatal("returned partial final")
				}
			}
		})
	}
	m := observationBody(t, b)
	quantity(m, 1)["status"] = "missing"
	quantity(m, 1)["quantity"] = nil
	o := parseObservation(t, m, b)
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		if _, err := o.Confirmed(side); !errors.Is(err, inference.ErrIncomplete) {
			t.Fatal("shared missing quantity ignored")
		}
	}
	m = observationBody(t, b)
	quantity(m, 0)["quantity"] = "2"
	quantity(m, 1)["quantity"] = "9223372036854775807"
	o = parseObservation(t, m, b)
	f, err := o.Confirmed(metering.Supplier)
	if err != nil {
		t.Fatal("actual above maximum was discarded", err)
	}
	actual, _ := f.Quantities()
	bound, err := metering.ParseBounds(s, metering.Supplier, []byte(`{"schema_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","version":"1","side":"supplier","bounds":[{"dimension":"request_count","unit":"request","quantity":"1"},{"dimension":"text_input_token","unit":"token","quantity":"100"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if within, err := metering.WithinBounds(actual, bound); err != nil || within {
		t.Fatal("overbound actual truncated")
	}
}

func TestObservationStrictValuesBindingAndIdentity(t *testing.T) {
	p := profile(t, profileInput(t))
	a := attempt(t, plan(t, p, 20, false), p)
	b := binding(t, a, schema(t, textSchema))
	body := observationBody(t, b)
	original := parseObservation(t, body, b)
	body["observed_at"] = "2026-11-04T01:02:04Z"
	later := parseObservation(t, body, b)
	if hexDigest(original) != hexDigest(later) {
		t.Fatal("observation time changed evidence identity")
	}
	left, _ := original.CanonicalJSON()
	right, _ := later.CanonicalJSON()
	if bytes.Equal(left, right) {
		t.Fatal("diagnostic observation time lost")
	}
	body["source_event_id"] = "event_2"
	other := parseObservation(t, body, b)
	if hexDigest(other) == hexDigest(original) {
		t.Fatal("event identity not bound")
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{"foreign_request", func(m map[string]any) { m["request_id"] = otherID }, inference.ErrBindingMismatch},
		{"foreign_profile", func(m map[string]any) { m["profile_version"] = otherID }, inference.ErrBindingMismatch},
		{"foreign_schema_digest", func(m map[string]any) { m["schema_digest"] = strings.Repeat("1", 64) }, inference.ErrBindingMismatch},
		{"malformed_hex", func(m map[string]any) { m["schema_digest"] = "A" }, inference.ErrInvalidObservation},
		{"malformed_variant", func(m map[string]any) { m["variant_key"] = nil }, inference.ErrInvalidObservation},
		{"null_attempt", func(m map[string]any) { m["attempt_id"] = nil }, inference.ErrInvalidObservation},
		{"missing_has_zero", func(m map[string]any) { quantity(m, 0)["status"] = "missing"; quantity(m, 0)["quantity"] = "0" }, inference.ErrInvalidObservation},
		{"confirmed_null", func(m map[string]any) { quantity(m, 0)["quantity"] = nil }, inference.ErrInvalidObservation},
		{"negative", func(m map[string]any) { quantity(m, 0)["quantity"] = "-1" }, inference.ErrInvalidObservation},
		{"number", func(m map[string]any) { quantity(m, 0)["quantity"] = 3 }, inference.ErrInvalidObservation},
		{"overflow", func(m map[string]any) { quantity(m, 0)["quantity"] = "9223372036854775808" }, inference.ErrInvalidObservation},
		{"leading_zero", func(m map[string]any) { quantity(m, 0)["quantity"] = "03" }, inference.ErrInvalidObservation},
		{"extra_dimension", func(m map[string]any) { m["quantities"] = append(m["quantities"].([]any), quantity(m, 0)) }, inference.ErrInvalidObservation},
		{"wrong_order", func(m map[string]any) { q := m["quantities"].([]any); q[0], q[1] = q[1], q[0] }, inference.ErrInvalidObservation},
		{"wrong_unit", func(m map[string]any) { quantity(m, 0)["unit"] = "image" }, inference.ErrInvalidObservation},
		{"source", func(m map[string]any) { m["source"] = "customer" }, inference.ErrInvalidObservation},
		{"url_reference", func(m map[string]any) { m["provider_reference"] = "https://secret.invalid/body" }, inference.ErrInvalidObservation},
		{"unknown_charge", func(m map[string]any) { m["unexpected_charge_reason"] = "ignore" }, inference.ErrInvalidObservation},
		{"noncanonical_time", func(m map[string]any) { m["observed_at"] = "2026-11-04T09:02:03+08:00" }, inference.ErrInvalidObservation},
		{"inconsistent_delivery", func(m map[string]any) { m["content_written"] = false }, inference.ErrInvalidObservation},
		{"finish_reason_type", func(m map[string]any) { m["finish_reason"] = nil }, inference.ErrInvalidObservation},
		{"finish_reason_control", func(m map[string]any) { m["finish_reason"] = "stop\n" }, inference.ErrInvalidObservation},
		{"finish_reason_size", func(m map[string]any) { m["finish_reason"] = strings.Repeat("x", 81) }, inference.ErrInvalidObservation},
		{"foreign_plus_quantity", func(m map[string]any) { m["request_id"] = otherID; quantity(m, 0)["quantity"] = "01" }, inference.ErrInvalidObservation},
		{"foreign_plus_source", func(m map[string]any) { m["request_id"] = otherID; m["source"] = "evil" }, inference.ErrInvalidObservation},
		{"foreign_plus_delivery", func(m map[string]any) { m["request_id"] = otherID; m["content_written"] = false }, inference.ErrInvalidObservation},
		{"foreign_plus_time", func(m map[string]any) { m["request_id"] = otherID; m["observed_at"] = "now" }, inference.ErrInvalidObservation},
		{"foreign_plus_other_set", func(m map[string]any) {
			m["request_id"] = otherID
			quantity(m, 0)["dimension"] = "request_count"
			quantity(m, 0)["unit"] = "request"
		}, inference.ErrBindingMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := observationBody(t, b)
			tc.mutate(m)
			o, err := inference.ParseObservation(marshal(t, m), b)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if _, err = o.Digest(); !errors.Is(err, inference.ErrInvalidValue) {
				t.Fatal("partial observation")
			}
		})
	}
	zero := observationBody(t, b)
	quantity(zero, 0)["quantity"] = "0"
	quantity(zero, 1)["quantity"] = "0"
	o := parseObservation(t, zero, b)
	for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
		if _, err := o.Confirmed(side); err != nil {
			t.Fatal("explicit zero treated as missing")
		}
	}
}

func TestStrictGrammarSizeAndErrorOrder(t *testing.T) {
	p := profile(t, profileInput(t))
	v := plan(t, p, 20, false)
	a := attempt(t, v, p)
	b := binding(t, a, schema(t, textSchema))
	quote := marshal(t, quoteBody(t, v, metering.Retail))
	observation := marshal(t, observationBody(t, b))
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"duplicate", []byte(`{"version":"1","version":"1"}`)},
		{"escaped_duplicate", []byte(`{"version":"1","\u0076ersion":"1"}`)},
		{"isolated_high_surrogate", []byte(`{"value":"\ud800"}`)},
		{"isolated_low_surrogate", []byte(`{"value":"\udc00"}`)},
		{"invalid_utf8", []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}},
		{"bom", append([]byte{0xef, 0xbb, 0xbf}, quote...)},
		{"tail", append(append([]byte(nil), quote...), []byte(` {}`)...)},
		{"depth", []byte(`{"x":[[[[[[[[[0]]]]]]]]]}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := inference.ParseQuoteSnapshot(tc.raw, v, metering.Retail); !errors.Is(err, inference.ErrInvalidJSON) {
				t.Fatal(err)
			}
			if _, err := inference.ParseObservation(tc.raw, b); !errors.Is(err, inference.ErrInvalidJSON) {
				t.Fatal(err)
			}
		})
	}
	if _, err := inference.ParseQuoteSnapshot(bytes.Repeat([]byte{'x'}, 16_385), inference.BoundPlan{}, "bad"); !errors.Is(err, inference.ErrTooLarge) {
		t.Fatal("size must precede invalid expected", err)
	}
	if _, err := inference.ParseObservation(bytes.Repeat([]byte{'x'}, 32_769), inference.ObservationBinding{}); !errors.Is(err, inference.ErrTooLarge) {
		t.Fatal(err)
	}
	if _, err := inference.ParseQuoteSnapshot(quote, inference.BoundPlan{}, metering.Retail); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := inference.ParseObservation(observation, inference.ObservationBinding{}); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := inference.ParseQuoteSnapshot(quote, v, "bad"); !errors.Is(err, metering.ErrSide) {
		t.Fatal(err)
	}
	goodUnicode := bytes.Replace(observation, []byte(`"finish_reason":"stop"`), []byte(`"finish_reason":"\ud83d\ude00"`), 1)
	if _, err := inference.ParseObservation(goodUnicode, b); err != nil {
		t.Fatal("valid surrogate pair rejected", err)
	}
}

func TestImmutableReadValuesRace(t *testing.T) {
	p := profile(t, profileInput(t))
	v := plan(t, p, 20, false)
	a := attempt(t, v, p)
	b := binding(t, a, schema(t, textSchema))
	raw := marshal(t, observationBody(t, b))
	o, err := inference.ParseObservation(raw, b)
	if err != nil {
		t.Fatal(err)
	}
	q, err := inference.ParseQuoteSnapshot(marshal(t, quoteBody(t, v, metering.Retail)), v, metering.Retail)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = 'x'
	}
	original := hexDigest(o)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 100 {
				entries, _ := o.Entries()
				*entries[0].Quantity = 999
				entries[0].Dimension = "changed"
				raw, _ := o.CanonicalJSON()
				raw[0] = 'x'
				info, _ := p.Info()
				info.ResultKinds[0] = "changed"
				raw, _ = v.CanonicalJSON()
				raw[0] = 'x'
				raw, _ = q.CanonicalJSON()
				raw[0] = 'x'
				f, err := o.Confirmed(metering.Retail)
				if err != nil {
					t.Error(err)
					return
				}
				raw, _ = f.CanonicalJSON()
				raw[0] = 'x'
				if hexDigest(o) != original {
					t.Error("shared mutable observation")
					return
				}
			}
		})
	}
	wg.Wait()
	entries, _ := o.Entries()
	if *entries[0].Quantity != 3 || entries[0].Dimension != "text_input_token" {
		t.Fatal("caller mutation escaped copy")
	}
	bound, _ := o.Binding()
	bound.TenantID = otherID
	meta, _ := o.Metadata()
	meta.Source = "changed"
	again, _ := o.Binding()
	if again.TenantID != tenantID {
		t.Fatal("binding alias")
	}
	if state, err := o.State("wrong"); !errors.Is(err, metering.ErrSide) || state != "" {
		t.Fatal(err)
	}
}

func TestOpaqueZeroValues(t *testing.T) {
	values := []interface {
		CanonicalJSON() ([]byte, error)
		Digest() ([32]byte, error)
	}{inference.Profile{}, inference.BoundPlan{}, inference.QuoteSnapshot{}, inference.Observation{}, inference.FinalQuantities{}}
	for _, v := range values {
		if _, err := v.CanonicalJSON(); !errors.Is(err, inference.ErrInvalidValue) {
			t.Fatal(err)
		}
		if _, err := v.Digest(); !errors.Is(err, inference.ErrInvalidValue) {
			t.Fatal(err)
		}
	}
	if _, err := (inference.Profile{}).Info(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Profile{}).Schema(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).Schema(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).Scope(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).Profile(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).Bounds(metering.Retail); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).InputHMAC(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.BoundPlan{}).VariantKey(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.QuoteSnapshot{}).Status(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.QuoteSnapshot{}).Bounds(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.QuoteSnapshot{}).Rates(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.QuoteSnapshot{}).Amount(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, _, err := (inference.QuoteSnapshot{}).VersionID(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.QuoteSnapshot{}).UnknownReason(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Observation{}).State(metering.Retail); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Observation{}).Entries(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Observation{}).Binding(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Observation{}).Metadata(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.Observation{}).Confirmed(metering.Retail); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
	if _, err := (inference.FinalQuantities{}).Quantities(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatal(err)
	}
}

func FuzzInferenceEnvelopes(f *testing.F) {
	p := profile(f, profileInput(f))
	v := plan(f, p, 20, false)
	a := attempt(f, v, p)
	b := binding(f, a, schema(f, textSchema))
	f.Add(marshal(f, quoteBody(f, v, metering.Retail)))
	f.Add(marshal(f, observationBody(f, b)))
	f.Add([]byte(`{"x":"\ud800"}`))
	f.Add([]byte(`{"x":1,"x":2}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) {
		q, err := inference.ParseQuoteSnapshot(raw, v, metering.Retail)
		if err == nil {
			if _, err = q.Amount(); err != nil {
				t.Fatal(err)
			}
		} else if _, err = q.CanonicalJSON(); !errors.Is(err, inference.ErrInvalidValue) {
			t.Fatal("partial quote")
		}
		o, err := inference.ParseObservation(raw, b)
		if err == nil {
			for _, side := range []metering.Side{metering.Retail, metering.Supplier} {
				state, err := o.State(side)
				if err != nil {
					t.Fatal(err)
				}
				final, err := o.Confirmed(side)
				if state == inference.UsageConfirmed && err != nil {
					t.Fatal(err)
				}
				if err == nil {
					if _, err := final.Quantities(); err != nil {
						t.Fatal(err)
					}
				}
			}
		} else if _, err = o.CanonicalJSON(); !errors.Is(err, inference.ErrInvalidValue) {
			t.Fatal("partial observation")
		}
	})
}
