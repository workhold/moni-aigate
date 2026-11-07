package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

var parallelCommercialProtocols = []string{
	provider.ProtocolChat,
	provider.ProtocolResponses,
	provider.ProtocolMessages,
	provider.ProtocolEmbeddings,
	provider.ProtocolRerank,
	provider.ProtocolGemini,
}

func parallelCommercialWire(protocol, kind string) string {
	if kind == "over_bound" {
		switch protocol {
		case provider.ProtocolResponses:
			return strings.Replace(nativeResponsesJSON, `"input_tokens":10,"output_tokens":5,"total_tokens":15`, `"input_tokens":101,"output_tokens":21,"total_tokens":122`, 1)
		case provider.ProtocolMessages:
			return strings.Replace(nativeMessagesJSON, `"input_tokens":6,"cache_read_input_tokens":3,"cache_creation_input_tokens":1,"output_tokens":5`, `"input_tokens":101,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":21`, 1)
		case provider.ProtocolEmbeddings:
			return strings.Replace(retrievalEmbeddingJSON, `"prompt_tokens":10,"total_tokens":10`, `"prompt_tokens":101,"total_tokens":101`, 1)
		case provider.ProtocolRerank:
			return strings.Replace(retrievalRerankJSON, `"total_tokens":10`, `"total_tokens":101`, 1)
		case provider.ProtocolGemini:
			return strings.Replace(geminiResponseJSON, geminiFinalUsage, `{"promptTokenCount":101,"candidatesTokenCount":21,"thoughtsTokenCount":0,"totalTokenCount":122}`, 1)
		default:
			return strings.Replace(gatewayJSON, `"prompt_tokens":10,"completion_tokens":5,"total_tokens":15`, `"prompt_tokens":101,"completion_tokens":21,"total_tokens":122`, 1)
		}
	}

	switch protocol {
	case provider.ProtocolResponses, provider.ProtocolMessages:
		return strings.Replace(nativeJSON(protocol), `"usage":{`, `"usage":null, "unused":{`, 1)
	case provider.ProtocolEmbeddings:
		return strings.Replace(retrievalEmbeddingJSON, `,"usage":{"prompt_tokens":10,"total_tokens":10}`, ``, 1)
	case provider.ProtocolRerank:
		return strings.Replace(retrievalRerankJSON, `,"usage":{"total_tokens":10}`, ``, 1)
	case provider.ProtocolGemini:
		return strings.Replace(geminiResponseJSON, `,"usageMetadata":`+geminiFinalUsage, ``, 1)
	default:
		return strings.Replace(gatewayJSON, `,"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`, ``, 1)
	}
}

func parallelCommercialHTTPInput(t *testing.T, protocol string) (string, string) {
	t.Helper()
	return keyPolicyProtocolInput(t, protocol)
}

func TestParallelCommercialGapOverBoundCapsOnceAcrossProtocols(t *testing.T) {
	for _, protocol := range parallelCommercialProtocols {
		t.Run(protocol, func(t *testing.T) {
			wire := parallelCommercialWire(protocol, "over_bound")
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(wire))
			}}, nil, 8, protocol)
			probeAdmin(t, f)
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := parallelCommercialHTTPInput(t, protocol)
			got := keyPolicyHTTP(t, f.server.Engine(), http.MethodPost, path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			if got.Code != http.StatusOK || f.posts.Load() != 1 {
				t.Fatalf("over-bound request was not delivered once: HTTP=%d body=%s posts=%d", got.Code, got.Body.String(), f.posts.Load())
			}

			wantCharge := int64(140)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				wantCharge = 100
			}
			state := f.state(t, got.Header().Get("X-Request-ID"))
			if state.billing != "settled" || state.reservation != "settled" || state.charge != wantCharge || state.spent != wantCharge || state.held != 0 || state.settlements != 1 || state.attempts != 1 {
				t.Fatalf("over-bound settlement changed the wrong facts: %+v want_charge=%d", state, wantCharge)
			}
			var profileVersion string
			if err := f.pool.QueryRow(context.Background(), `SELECT profile_version FROM request_meter_snapshots WHERE request_id=$1`, got.Header().Get("X-Request-ID")).Scan(&profileVersion); err != nil {
				t.Fatal(err)
			}
			var blocks, audits int
			if err := f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM billing_profile_blocks WHERE model_id=$1 AND profile_version=$2 AND request_id=$3),
 (SELECT count(*) FROM audit_events WHERE action='billing.profile_blocked' AND target=$1 AND request_id=$3)`, f.model, profileVersion, got.Header().Get("X-Request-ID")).Scan(&blocks, &audits); err != nil {
				t.Fatal(err)
			}
			if blocks != 1 || audits != 1 {
				t.Fatalf("over-bound profile was not isolated exactly once: blocks=%d audits=%d", blocks, audits)
			}
			again, err := f.server.Billing.Settle(context.Background(), billing.SettleInput{TenantID: f.tenant, RequestID: got.Header().Get("X-Request-ID")})
			if err != nil || int64(again.ChargedAmount) != wantCharge || again.ID == "" || f.posts.Load() != 1 {
				t.Fatalf("settlement replay changed over-bound facts: %+v err=%v posts=%d", again, err, f.posts.Load())
			}
		})
	}
}

func TestParallelCommercialGapManualReconciliationKeepsSupplierUnknownWhenDeliveryIsUnconfirmed(t *testing.T) {
	for _, protocol := range parallelCommercialProtocols {
		t.Run(protocol, func(t *testing.T) {
			wire := parallelCommercialWire(protocol, "missing_usage")
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(wire))
			}}, nil, 8, protocol)
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := parallelCommercialHTTPInput(t, protocol)
			got := keyPolicyHTTP(t, f.server.Engine(), http.MethodPost, path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			if got.Code != http.StatusServiceUnavailable || f.posts.Load() != 1 {
				t.Fatalf("missing usage did not enter reconciliation: HTTP=%d body=%s posts=%d", got.Code, got.Body.String(), f.posts.Load())
			}
			requestID := got.Header().Get("X-Request-ID")
			state := f.state(t, requestID)
			if state.billing != "pending_reconciliation" || state.reservation != "pending_reconciliation" || state.settlements != 0 || state.attempts != 1 || state.spent != 0 || state.held == 0 {
				t.Fatalf("missing usage did not preserve the original hold: %+v", state)
			}

			var operator, approver string
			if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_operator' ORDER BY id LIMIT 1`).Scan(&operator); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_approver' ORDER BY id LIMIT 1`).Scan(&approver); err != nil {
				t.Fatal(err)
			}
			input, output := int64(10), int64(5)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				output = 0
			}
			usage := billing.Usage{Status: "confirmed_final", InputTokens: input, OutputTokens: output, TotalTokens: input + output}
			proposalInput := billing.ReconcileInput{TenantID: f.tenant, RequestID: requestID, ActorID: operator, Action: "propose", EvidenceRef: "invoice:parallel-customer-only", Reason: "confirm customer usage only", Usage: usage}
			proposal, err := f.server.Billing.Reconcile(context.Background(), proposalInput)
			if err != nil || proposal.State != "proposed" {
				t.Fatalf("customer-only reconciliation proposal failed: %+v err=%v", proposal, err)
			}
			if replay, replayErr := f.server.Billing.Reconcile(context.Background(), proposalInput); replayErr != nil || replay.State != "proposed" {
				t.Fatalf("identical proposal replay was not idempotent: %+v err=%v", replay, replayErr)
			}
			conflict := proposalInput
			conflict.Usage.InputTokens++
			conflict.Usage.TotalTokens++
			if _, err = f.server.Billing.Reconcile(context.Background(), conflict); err == nil {
				t.Fatal("changed proposal replaced the original customer evidence")
			}
			approved, err := f.server.Billing.Reconcile(context.Background(), billing.ReconcileInput{TenantID: f.tenant, RequestID: requestID, ActorID: approver, Action: "approve"})
			wantCharge := int64(0)
			if err != nil || approved.State != "approved" || approved.Settlement == nil {
				t.Fatalf("customer-only reconciliation approval failed: %+v err=%v", approved, err)
			}

			var supplierMissing bool
			if err = f.pool.QueryRow(context.Background(), `SELECT f.supplier_usage_state='missing' AND f.supplier_quantities_canonical IS NULL AND f.supplier_quantities_digest IS NULL
 AND a.cost_status='unknown' AND a.cost_amount IS NULL AND a.latest_cost_finalization_id IS NULL AND a.latest_cost_correction_id IS NULL
 FROM finalization_records f JOIN metering_events e ON e.id=f.event_id JOIN upstream_attempts a ON a.id=f.attempt_id WHERE f.request_id=$1 AND e.source='approved_reconciliation'`, requestID).Scan(&supplierMissing); err != nil {
				t.Fatal(err)
			}
			if !supplierMissing {
				t.Fatal("manual customer evidence confirmed or priced the supplier side")
			}
			final := f.state(t, requestID)
			if final.billing != "released" || final.reservation != "released" || final.charge != wantCharge || final.spent != wantCharge || final.held != 0 || final.settlements != 1 || final.attempts != 1 || f.posts.Load() != 1 {
				t.Fatalf("manual reconciliation changed original commercial facts: %+v want_charge=%d", final, wantCharge)
			}
		})
	}
}

func TestParallelCommercialGapUnknownCostDoesNotDispatchAcrossProtocols(t *testing.T) {
	for _, protocol := range parallelCommercialProtocols {
		t.Run(protocol, func(t *testing.T) {
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
				t.Error("unknown supplier quote reached provider")
			}}, nil, 8, protocol)
			channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
			publishCostFixture(t, f, channels[0], "provider-model", "unknown", nil, nil, time.Time{})
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := parallelCommercialHTTPInput(t, protocol)
			got := keyPolicyHTTP(t, f.server.Engine(), http.MethodPost, path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			if got.Code != http.StatusServiceUnavailable || f.posts.Load() != 0 {
				t.Fatalf("unknown supplier quote dispatched or returned success: HTTP=%d body=%s posts=%d", got.Code, got.Body.String(), f.posts.Load())
			}
			var requests, attempts, reservations, held, spent int64
			if err := f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM request_records),(SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM reservations),
 (SELECT held FROM api_keys WHERE id=$1),(SELECT spent FROM api_keys WHERE id=$1)`, f.key).Scan(&requests, &attempts, &reservations, &held, &spent); err != nil {
				t.Fatal(err)
			}
			var requestStatus, reservationStatus string
			if err := f.pool.QueryRow(context.Background(), `SELECT r.billing_status,v.status FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=(SELECT id FROM request_records ORDER BY id DESC LIMIT 1)`).Scan(&requestStatus, &reservationStatus); err != nil {
				t.Fatal(err)
			}
			if requests != 1 || attempts != 0 || reservations != 1 || requestStatus != "released" || reservationStatus != "released" || held != 0 || spent != 0 {
				t.Fatalf("unknown supplier quote left customer financial facts: requests=%d attempts=%d reservations=%d held=%d spent=%d", requests, attempts, reservations, held, spent)
			}
		})
	}
}
