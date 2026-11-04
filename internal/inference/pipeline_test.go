package inference_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/inference"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

const (
	pipelineSchemaID   = "11111111-1111-1111-1111-111111111111"
	pipelineProfileID  = "22222222-2222-2222-2222-222222222222"
	pipelineRetailID   = "33333333-3333-3333-3333-333333333333"
	pipelineSupplierID = "44444444-4444-4444-4444-444444444444"
)

type pipelineFixture struct {
	request inference.BoundPlan
	attempt inference.BoundPlan
}

// These tests use only the exported domain API. Expected monetary values are
// independently written constants, rather than a second call to the calculator.
func TestPipelineKnownQuotesToIndependentSideAmounts(t *testing.T) {
	f := pipelineSetup(t)
	retail := pipelineQuote(t, f.request, metering.Retail, "known", pipelineRetailID, nil,
		pipelineRateLines("1", "2", "1", "3"))
	supplier := pipelineQuote(t, f.attempt, metering.Supplier, "known", pipelineSupplierID, nil,
		pipelineRateLines("1", "4", "1", "5"))

	// Public input is max(100,120), while the selected attempt tightens to100.
	// R=ceil(120/2+20/3)=67; U=ceil(100/4+20/5)=29 micro-CNY.
	for _, quote := range []struct {
		name string
		q    inference.QuoteSnapshot
		want int64
	}{{"retail", retail, 67}, {"supplier", supplier, 29}} {
		got, err := quote.q.Amount()
		if err != nil || got != quote.want {
			t.Fatalf("%s reservation = %d, %v; want %d", quote.name, got, err, quote.want)
		}
	}

	observation := pipelineObservation(t, f.attempt, "adapter")
	retailQuantity := pipelineFinal(t, observation, metering.Retail)
	supplierQuantity := pipelineFinal(t, observation, metering.Supplier)
	retailRates, err := retail.Rates()
	pipelineRequire(t, err)
	supplierRates, err := supplier.Rates()
	pipelineRequire(t, err)

	// The actual quantity is input=3, output=2. Retail is ceil(3/2+2/3)=3;
	// supplier is ceil(3/4+2/5)=2, with one rounding after the whole sum.
	retailAmount, err := metering.Amount(retailQuantity, retailRates)
	if err != nil || retailAmount.String() != "3" {
		t.Fatalf("retail actual = %v, %v; want 3", retailAmount, err)
	}
	supplierAmount, err := metering.Amount(supplierQuantity, supplierRates)
	if err != nil || supplierAmount.String() != "2" {
		t.Fatalf("supplier actual = %v, %v; want 2", supplierAmount, err)
	}
	if _, err := metering.Amount(supplierQuantity, retailRates); !errors.Is(err, metering.ErrBindingMismatch) {
		t.Fatalf("supplier quantity accepted retail pricing: %v", err)
	}
}

func TestPipelineUnknownSupplierPriceAndManualRetailEvidence(t *testing.T) {
	f := pipelineSetup(t)
	retail := pipelineQuote(t, f.request, metering.Retail, "known", pipelineRetailID, nil,
		pipelineRateLines("1", "2", "1", "3"))
	unknownSupplier := pipelineQuote(t, f.attempt, metering.Supplier, "unknown", pipelineSupplierID, "basis_unknown", nil)

	version, present, err := unknownSupplier.VersionID()
	if err != nil || !present || version != pipelineSupplierID {
		t.Fatalf("unknown original supplier version = %q, %v, %v", version, present, err)
	}
	if _, err := unknownSupplier.Amount(); !errors.Is(err, inference.ErrUnknownQuote) {
		t.Fatalf("unknown procurement price produced an amount: %v", err)
	}
	if _, err := unknownSupplier.Rates(); !errors.Is(err, inference.ErrUnknownQuote) {
		t.Fatalf("unknown procurement price exposed usable rates: %v", err)
	}

	// Price availability does not decide whether adapter quantities are final.
	// Both quantities can be confirmed while the original supplier price remains
	// unknown; the known retail quote still gives an independently expected 3.
	observation := pipelineObservation(t, f.attempt, "adapter")
	retailQuantity := pipelineFinal(t, observation, metering.Retail)
	_ = pipelineFinal(t, observation, metering.Supplier)
	retailRates, err := retail.Rates()
	pipelineRequire(t, err)
	amount, err := metering.Amount(retailQuantity, retailRates)
	if err != nil || amount.String() != "3" {
		t.Fatalf("retail with unknown procurement price = %v, %v; want 3", amount, err)
	}
	if _, err := unknownSupplier.Amount(); !errors.Is(err, inference.ErrUnknownQuote) {
		t.Fatalf("confirmed quantities changed the original unknown supplier price: %v", err)
	}

	// A different source event on this same real attempt can approve customer
	// quantities, but is never supplier evidence, even with every quantity final.
	manual := pipelineObservation(t, f.attempt, "approved_reconciliation")
	manualRetail := pipelineFinal(t, manual, metering.Retail)
	manualAmount, err := metering.Amount(manualRetail, retailRates)
	if err != nil || manualAmount.String() != "3" {
		t.Fatalf("manual retail amount = %v, %v; want 3", manualAmount, err)
	}
	state, err := manual.State(metering.Supplier)
	if err != nil || state != inference.UsageMissing {
		t.Fatalf("manual supplier state = %q, %v; want missing", state, err)
	}
	final, err := manual.Confirmed(metering.Supplier)
	if !errors.Is(err, inference.ErrIncomplete) {
		t.Fatalf("manual retail evidence confirmed supplier quantities: %v", err)
	}
	if _, err := final.Quantities(); !errors.Is(err, inference.ErrInvalidValue) {
		t.Fatalf("rejected manual supplier evidence exposed partial final quantities: %v", err)
	}
}

func pipelineSetup(t *testing.T) pipelineFixture {
	t.Helper()
	schema, err := metering.ParseSchema([]byte(`{"schema_id":"11111111-1111-1111-1111-111111111111","version":"1","dimensions":[{"dimension":"text_input_token","unit":"token","maximum_quantity":"120"},{"dimension":"text_output_token","unit":"token","maximum_quantity":"20"}],"retail_dimensions":["text_input_token","text_output_token"],"supplier_dimensions":["text_input_token","text_output_token"]}`))
	pipelineRequire(t, err)
	selected, err := inference.NewTextProfile(inference.TextProfileInput{
		Version: pipelineProfileID, Model: "pipeline-model", VariantKey: "default", Protocol: inference.ProtocolChat,
		Schema: schema, ContractDigest: sha256.Sum256([]byte("pipeline-provider-a")), InputTokenUpperBound: 100, OutputLimit: 20,
	})
	pipelineRequire(t, err)
	other, err := inference.NewTextProfile(inference.TextProfileInput{
		Version: "55555555-5555-5555-5555-555555555555", Model: "pipeline-model", VariantKey: "default", Protocol: inference.ProtocolChat,
		Schema: schema, ContractDigest: sha256.Sum256([]byte("pipeline-provider-b")), InputTokenUpperBound: 120, OutputLimit: 20,
	})
	pipelineRequire(t, err)
	request, err := inference.NewTextPlan(inference.TextPlanInput{
		Candidates: []inference.Profile{selected, other}, InputHMAC: sha256.Sum256([]byte("pipeline-effective-input")), OutputLimit: 20, Stream: true,
	})
	pipelineRequire(t, err)
	attempt, err := request.ForAttempt(selected)
	pipelineRequire(t, err)
	return pipelineFixture{request: request, attempt: attempt}
}

func pipelineRateLines(inputNumerator, inputDenominator, outputNumerator, outputDenominator string) []map[string]string {
	return []map[string]string{
		{"dimension": "text_input_token", "unit": "token", "numerator": inputNumerator, "denominator": inputDenominator},
		{"dimension": "text_output_token", "unit": "token", "numerator": outputNumerator, "denominator": outputDenominator},
	}
}

func pipelineQuote(t *testing.T, plan inference.BoundPlan, side metering.Side, status string, version, reason any, lines []map[string]string) inference.QuoteSnapshot {
	t.Helper()
	schema, err := plan.Schema()
	pipelineRequire(t, err)
	schemaDigest, err := schema.Digest()
	pipelineRequire(t, err)
	planDigest, err := plan.Digest()
	pipelineRequire(t, err)
	bounds, err := plan.Bounds(side)
	pipelineRequire(t, err)
	boundsJSON, err := bounds.CanonicalJSON()
	pipelineRequire(t, err)
	var rates any
	if lines != nil {
		rates = map[string]any{"schema_id": pipelineSchemaID, "version": "1", "side": side, "currency": "CNY", "rates": lines}
	}
	raw := pipelineJSON(t, map[string]any{
		"version": "1", "status": status, "side": side, "plan_digest": hex.EncodeToString(planDigest[:]),
		"schema_id": pipelineSchemaID, "schema_digest": hex.EncodeToString(schemaDigest[:]), "variant_key": "default",
		"version_id": version, "unknown_reason": reason, "bounds": json.RawMessage(boundsJSON), "rates": rates,
	})
	quote, err := inference.ParseQuoteSnapshot(raw, plan, side)
	pipelineRequire(t, err)
	return quote
}

func pipelineObservation(t *testing.T, attempt inference.BoundPlan, source string) inference.Observation {
	t.Helper()
	schema, err := attempt.Schema()
	pipelineRequire(t, err)
	schemaDigest, err := schema.Digest()
	pipelineRequire(t, err)
	planDigest, err := attempt.Digest()
	pipelineRequire(t, err)
	expected := inference.ObservationBinding{
		TenantID: "66666666-6666-6666-6666-666666666666", RequestID: "77777777-7777-7777-7777-777777777777",
		AttemptID: "88888888-8888-8888-8888-888888888888", ProfileVersion: pipelineProfileID,
		VariantKey: "default", Schema: schema, PlanDigest: planDigest,
	}
	raw := pipelineJSON(t, map[string]any{
		"version": "1", "tenant_id": expected.TenantID, "request_id": expected.RequestID, "attempt_id": expected.AttemptID,
		"source": source, "source_event_id": "pipeline:" + source, "profile_version": expected.ProfileVersion,
		"schema_id": pipelineSchemaID, "schema_digest": hex.EncodeToString(schemaDigest[:]), "variant_key": expected.VariantKey,
		"plan_digest": hex.EncodeToString(planDigest[:]), "provider_reference": "provider-result-1", "observed_at": "2026-11-04T12:00:00Z",
		"protocol_outcome": "completed", "transport_end_reason": "done", "delivery_stage": "content", "content_written": true, "finish_reason": "stop",
		"quantities": []map[string]any{
			{"dimension": "text_input_token", "unit": "token", "status": "confirmed_final", "quantity": "3"},
			{"dimension": "text_output_token", "unit": "token", "status": "confirmed_final", "quantity": "2"},
		},
		"unexpected_charge_reason": nil,
	})
	observation, err := inference.ParseObservation(raw, expected)
	pipelineRequire(t, err)
	return observation
}

func pipelineFinal(t *testing.T, observation inference.Observation, side metering.Side) metering.Quantities {
	t.Helper()
	final, err := observation.Confirmed(side)
	pipelineRequire(t, err)
	quantities, err := final.Quantities()
	pipelineRequire(t, err)
	return quantities
}

func pipelineJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	pipelineRequire(t, err)
	return raw
}

func pipelineRequire(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
