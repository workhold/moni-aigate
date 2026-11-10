package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func forbidScalarQuoteReads(t *testing.T, f *gatewayFixture) {
	t.Helper()
	for _, signature := range []string{"meter_cost_million(cost_id text, dimension text)", "meter_price_million(price_id text, dimension text)"} {
		authExec(t, f.pool, `CREATE OR REPLACE FUNCTION `+signature+` RETURNS bigint LANGUAGE plpgsql STABLE AS $$ BEGIN RAISE EXCEPTION 'scalar quote read forbidden'; END $$`)
	}
}

// This exercises current candidate -> attempt -> procurement -> dispatch ->
// settlement through restricted SET ROLE APP, not production LOGIN qualification
// or the complete six-protocol commercial exit matrix.
func TestCostVectorApplicationQuoteChain(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			result := gatewayJSON
			switch protocol {
			case provider.ProtocolResponses, provider.ProtocolMessages:
				result = nativeJSON(protocol)
			case provider.ProtocolEmbeddings, provider.ProtocolRerank:
				result = retrievalJSON(protocol)
			case provider.ProtocolGemini:
				result = geminiResponseJSON
			}
			for _, zero := range []bool{false, true} {
				name := "paid"
				if zero {
					name = "known_zero"
				}
				t.Run(name, func(t *testing.T) {
					f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, result)
					}}, nil, 8, protocol)
					probeAdmin(t, f)
					var admin string
					if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='admin'`).Scan(&admin); err != nil {
						t.Fatal(err)
					}
					channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
					account := procurementMeterAccount(t, f, admin, channels[0])
					input, output := int64(1000000), int64(1000000)
					retail, cost := int64(20), int64(15)
					if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
						output, retail, cost = 0, 10, 10
					}
					if zero {
						input, output, cost = 0, 0, 0
					}
					publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(input), costRate(output), time.Time{})
					procurementMeterApplication(t, f)
					forbidScalarQuoteReads(t, f)
					path, raw := keyPolicyProtocolInput(t, protocol)
					got := keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
					if got.Code != 200 || f.posts.Load() != 1 {
						t.Fatal("restricted vector request failed", got.Code, got.Body.String(), f.posts.Load())
					}
					request := got.Header().Get("X-Request-ID")
					state := f.state(t, request)
					if state.charge != retail || state.spent != retail || state.held != 0 || state.keyHeld != 0 || state.settlements != 1 || state.attempts != 1 {
						t.Fatal("unique original retail settlement changed", state)
					}
					var reserveAudits, settlementAudits, observations, recoveryJobs int
					if err := f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='billing.reserve'),
 (SELECT count(*) FROM audit_events WHERE request_id=$1 AND action='billing.settled'),
 (SELECT count(*) FROM metering_events WHERE request_id=$1 AND source='adapter'),
 (SELECT count(*) FROM recovery_jobs WHERE request_id=$1)`, request).Scan(&reserveAudits, &settlementAudits, &observations, &recoveryJobs); err != nil {
						t.Fatal(err)
					}
					if reserveAudits != 1 || settlementAudits != 1 || observations != 1 || recoveryJobs != 0 {
						t.Fatalf("commercial facts were not closed exactly once: reserve_audits=%d settlement_audits=%d observations=%d recovery_jobs=%d", reserveAudits, settlementAudits, observations, recoveryJobs)
					}
					var storedCost, held, spent, reserved int64
					var original bool
					if err := f.pool.QueryRow(context.Background(), `SELECT a.cost_amount::bigint,p.held_amount::bigint,p.spent_amount::bigint,p.upper_amount::bigint,
 s.supplier_rates_canonical IS NOT NULL AND s.supplier_rates_canonical=c.rates_canonical AND s.supplier_rates_digest=c.rates_digest
 AND s.parent_plan_digest=r.plan_digest AND a.latest_cost_finalization_id IS NOT NULL AND a.latest_cost_correction_id IS NULL
 AND e.retail_usage_state='confirmed_final' AND e.supplier_usage_state='confirmed_final'
 FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id
 JOIN request_meter_snapshots r ON r.request_id=a.request_id JOIN cost_versions c ON c.id=s.cost_version_id
 JOIN procurement_reservations p ON p.attempt_id=a.id JOIN finalization_records z ON z.id=a.latest_cost_finalization_id
 JOIN metering_events e ON e.id=z.event_id WHERE a.request_id=$1 AND p.account_id=$2`, request, account).Scan(&storedCost, &held, &spent, &reserved, &original); err != nil || storedCost != cost || held != 0 || spent != cost || !original || zero && reserved != 0 || !zero && reserved <= cost {
						t.Fatal("attempt originals/procurement/evidence diverged", storedCost, held, spent, reserved, original, err)
					}
					// Later publication cannot rewrite an original cost or settlement.
					publishCostFixture(t, f, channels[0], "provider-model", "invoice", costRate(9000000), costRate(0), time.Time{})
					if _, err := f.server.Billing.Settle(context.Background(), billing.SettleInput{TenantID: f.tenant, RequestID: request}); err != nil {
						t.Fatal(err)
					}
					if after := f.state(t, request); after.charge != retail || after.settlements != 1 || after.attempts != 1 || f.posts.Load() != 1 {
						t.Fatal("settlement replay repriced or resent", after)
					}
					var accountHeld, accountSpent int64
					if err := f.pool.QueryRow(context.Background(), `SELECT held_amount::bigint,spent_amount::bigint FROM procurement_accounts WHERE id=$1`, account).Scan(&accountHeld, &accountSpent); err != nil || accountHeld != 0 || accountSpent != cost {
						t.Fatal("original procurement total changed", accountHeld, accountSpent, err)
					}
				})
			}
		})
	}
}

func TestCostVectorApplicationUnknownIsNotFree(t *testing.T) {
	for _, kind := range []string{"manual_allow", "manual_deny", "managed_procurement"} {
		t.Run(kind, func(t *testing.T) {
			f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
			probeAdmin(t, f)
			var admin string
			if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='admin'`).Scan(&admin); err != nil {
				t.Fatal(err)
			}
			policy := routing.DefaultCostPolicy()
			if kind == "manual_deny" {
				policy.UnknownCostPolicy = "deny"
			}
			channels := publishCostTestRoute(t, f, policy, nil)
			if kind == "managed_procurement" {
				procurementMeterAccount(t, f, admin, channels[0])
			}
			publishCostFixture(t, f, channels[0], "provider-model", "unknown", nil, nil, time.Time{})
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := keyPolicyProtocolInput(t, provider.ProtocolChat)
			got := keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			var attempts, reservations, held int64
			if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM upstream_attempts),(SELECT count(*) FROM procurement_reservations),held FROM api_keys WHERE id=$1`, f.key).Scan(&attempts, &reservations, &held); err != nil {
				t.Fatal(err)
			}
			if kind != "manual_allow" {
				if got.Code != 503 || f.posts.Load() != 0 || attempts != 0 || reservations != 0 || held != 0 {
					t.Fatal("unknown reached supplier or stranded funds", got.Code, got.Body.String(), attempts, reservations, held, f.posts.Load())
				}
				return
			}
			if got.Code != 200 || f.posts.Load() != 1 || attempts != 1 || reservations != 0 || held != 0 {
				t.Fatal("manual unknown policy changed", got.Code, got.Body.String(), attempts, reservations, held)
			}
			var unknown bool
			if err := f.pool.QueryRow(context.Background(), `SELECT a.cost_status='unknown' AND a.cost_amount IS NULL AND a.latest_cost_finalization_id IS NULL AND a.latest_cost_correction_id IS NULL
 AND s.quote_status='unknown' AND s.unknown_reason='basis_unknown' AND s.supplier_rates_canonical IS NULL AND s.supplier_rates_digest IS NULL
 AND s.supplier_quote_canonical IS NOT NULL FROM upstream_attempts a JOIN attempt_meter_snapshots s ON s.attempt_id=a.id WHERE a.request_id=$1`, got.Header().Get("X-Request-ID")).Scan(&unknown); err != nil || !unknown {
				t.Fatal("unknown fabricated free cost or lost outer quote", unknown, err)
			}
			if strings.Contains(got.Body.String(), "cost_version") {
				t.Fatal("private quote leaked")
			}
		})
	}
}

func TestCostVectorApplicationRecoveryDoesNotRegenerate(t *testing.T) {
	for _, protocol := range parallelCommercialProtocols {
		t.Run(protocol, func(t *testing.T) {
			missingUsage := parallelCommercialWire(protocol, "missing_usage")
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, missingUsage)
			}}, nil, 8, protocol)
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := keyPolicyProtocolInput(t, protocol)
			response := keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
			if response.Code < 400 || f.posts.Load() != 1 {
				t.Fatal("missing usage was accepted or retried", response.Code, response.Body.String(), f.posts.Load())
			}
			requestID := response.Header().Get("X-Request-ID")
			wantHold := int64(140)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				wantHold = 100
			}
			state := f.state(t, requestID)
			if state.billing != "pending_reconciliation" || state.reservation != "pending_reconciliation" || state.attempts != 1 || state.settlements != 0 || state.held != wantHold || state.spent != 0 {
				t.Fatalf("missing usage did not preserve the recovery hold: %+v want_hold=%d", state, wantHold)
			}
			tx, err := f.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = tx.Exec(context.Background(), "UPDATE request_records SET request_deadline=clock_timestamp()-interval '1 second' WHERE id=$1", requestID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(context.Background(), "ALTER TABLE reservations DISABLE TRIGGER pb_customer_reservation_identity"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(context.Background(), "UPDATE reservations SET deadline=clock_timestamp()-interval '1 second' WHERE request_id=$1", requestID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(context.Background(), "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(context.Background(), "ALTER TABLE reservations ENABLE TRIGGER pb_customer_reservation_identity"); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			// Rebuild the service and billing store before recovery. The durable
			// reservation/recovery job, rather than process memory, must remain
			// authoritative after a process restart.
			f.server = New(f.server.Config, f.pool, f.server.assets, nil)
			f.server.Ready(true)
			recovered, err := f.server.Billing.Recover(context.Background(), billing.RecoverInput{Limit: 1})
			if err != nil || recovered.Processed != 1 || recovered.Released != 1 {
				t.Fatal("recovery did not release the expired unknown request", recovered, err)
			}
			state = f.state(t, requestID)
			if state.billing != "released" || state.reservation != "released" || state.attempts != 1 || state.settlements != 1 || state.held != 0 || state.spent != 0 || state.available != 1000 || f.posts.Load() != 1 {
				t.Fatal("recovery regenerated or changed the original financial facts", state, f.posts.Load())
			}
			again, err := f.server.Billing.Recover(context.Background(), billing.RecoverInput{Limit: 1})
			if err != nil || again.Processed != 0 || f.posts.Load() != 1 {
				t.Fatal("recovery replay changed the request", again, err, f.posts.Load())
			}
		})
	}
}

func TestCostVectorApplicationIdempotencyChargesOnceAcrossProtocols(t *testing.T) {
	for _, protocol := range parallelCommercialProtocols {
		t.Run(protocol, func(t *testing.T) {
			result := gatewayJSON
			switch protocol {
			case provider.ProtocolResponses, provider.ProtocolMessages:
				result = nativeJSON(protocol)
			case provider.ProtocolEmbeddings, provider.ProtocolRerank:
				result = retrievalJSON(protocol)
			case provider.ProtocolGemini:
				result = geminiResponseJSON
			}
			f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, result)
			}}, nil, 8, protocol)
			procurementMeterApplication(t, f)
			forbidScalarQuoteReads(t, f)
			path, raw := keyPolicyProtocolInput(t, protocol)
			headers := http.Header{"Authorization": []string{"Bearer " + f.token}, "Idempotency-Key": []string{"six-protocol-single-charge"}}
			first := keyPolicyHTTP(t, f.server.Engine(), http.MethodPost, path, raw, "", "", "", "127.0.0.1:1234", headers)
			if first.Code != http.StatusOK || f.posts.Load() != 1 {
				t.Fatal("first request did not settle once", first.Code, first.Body.String(), f.posts.Load())
			}
			second := keyPolicyHTTP(t, f.server.Engine(), http.MethodPost, path, raw, "", "", "", "127.0.0.1:1234", headers)
			if second.Code != http.StatusConflict || second.Header().Get("X-Original-Request-ID") != first.Header().Get("X-Request-ID") || f.posts.Load() != 1 || protocol != provider.ProtocolMessages && !strings.Contains(second.Body.String(), "request_exists") {
				t.Fatalf("duplicate request was not rejected without replay: status=%d original=%s body=%s posts=%d", second.Code, second.Header().Get("X-Original-Request-ID"), second.Body.String(), f.posts.Load())
			}
			wantCharge := int64(20)
			if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
				wantCharge = 10
			}
			state := f.state(t, first.Header().Get("X-Request-ID"))
			if state.billing != "settled" || state.reservation != "settled" || state.charge != wantCharge || state.spent != wantCharge || state.held != 0 || state.settlements != 1 || state.attempts != 1 {
				t.Fatalf("duplicate request changed the unique charge: %+v want_charge=%d", state, wantCharge)
			}
			var requests, settlements int
			if err := f.pool.QueryRow(context.Background(), "SELECT (SELECT count(*) FROM request_records),(SELECT count(*) FROM settlements)").Scan(&requests, &settlements); err != nil || requests != 1 || settlements != 1 {
				t.Fatalf("duplicate request created extra business facts: requests=%d settlements=%d err=%v", requests, settlements, err)
			}
		})
	}
}

func TestCostVectorApplicationDefaultAndGroupPricesAcrossProtocols(t *testing.T) {
	for _, protocol := range []string{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolMessages, provider.ProtocolEmbeddings, provider.ProtocolRerank, provider.ProtocolGemini} {
		t.Run(protocol, func(t *testing.T) {
			for _, grouped := range []bool{false, true} {
				name := "default"
				if grouped {
					name = "group"
				}
				t.Run(name, func(t *testing.T) {
					result := gatewayJSON
					switch protocol {
					case provider.ProtocolResponses, provider.ProtocolMessages:
						result = nativeJSON(protocol)
					case provider.ProtocolEmbeddings, provider.ProtocolRerank:
						result = retrievalJSON(protocol)
					case provider.ProtocolGemini:
						result = geminiResponseJSON
					}
					f := newGatewayFixtureConfigured(t, []http.HandlerFunc{func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, result)
					}}, nil, 8, protocol)
					probeAdmin(t, f)
					var admin string
					if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='admin' ORDER BY id DESC LIMIT 1`).Scan(&admin); err != nil {
						t.Fatal(err)
					}
					var groupID string
					if grouped {
						group, err := f.server.Catalog.CreatePriceGroup(context.Background(), catalog.PricingActor{UserID: admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "six-protocol-vector"})
						if err != nil {
							t.Fatal(err)
						}
						groupID = group.Result.ID
						outputRate := int64(4000000)
						if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
							outputRate = 0
						}
						command := meterquote.BindGroup(t, context.Background(), f.pool, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: groupID, ModelID: f.model, InputRate: 3000000, OutputRate: outputRate, EffectiveAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)})
						if _, err = f.server.Catalog.PublishGroupPrice(context.Background(), catalog.PricingActor{UserID: admin}, command); err != nil {
							t.Fatal(err)
						}
						if _, err = f.server.Catalog.AssignTenantPricing(context.Background(), catalog.PricingActor{UserID: admin}, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 1, GroupID: &groupID}); err != nil {
							t.Fatal(err)
						}
					}
					channels := publishCostTestRoute(t, f, routing.CostPolicy{Mode: "cost_first", UnknownCostPolicy: "deny"}, nil)
					account := procurementMeterAccount(t, f, admin, channels[0])
					input, output := int64(1000000), int64(1000000)
					if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
						output = 0
					}
					publishCostFixture(t, f, channels[0], "provider-model", "contract", costRate(input), costRate(output), time.Time{})
					procurementMeterApplication(t, f)
					forbidScalarQuoteReads(t, f)
					path, raw := keyPolicyProtocolInput(t, protocol)
					got := keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
					if got.Code != 200 || f.posts.Load() != 1 {
						t.Fatal("default/group vector request failed", got.Code, got.Body.String(), f.posts.Load())
					}
					wantCharge := int64(20)
					if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
						wantCharge = 10
					}
					if grouped {
						if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
							wantCharge = 30
						} else {
							wantCharge = 50
						}
					}
					state := f.state(t, got.Header().Get("X-Request-ID"))
					if state.charge != wantCharge || state.spent != wantCharge || state.held != 0 || state.settlements != 1 || state.attempts != 1 {
						t.Fatal("default/group price changed the wrong financial facts", state, wantCharge)
					}
					var snapshotGroup *string
					if err := f.pool.QueryRow(context.Background(), `SELECT pricing_group_id FROM request_meter_snapshots WHERE request_id=$1`, got.Header().Get("X-Request-ID")).Scan(&snapshotGroup); err != nil {
						t.Fatal(err)
					}
					if grouped && (snapshotGroup == nil || *snapshotGroup != groupID) || !grouped && snapshotGroup != nil {
						t.Fatal("retail price group was not frozen in the request snapshot", snapshotGroup, groupID, grouped)
					}
					var accountHeld, accountSpent int64
					wantSupplierCost := int64(15)
					if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
						wantSupplierCost = 10
					}
					if err := f.pool.QueryRow(context.Background(), `SELECT held_amount::bigint,spent_amount::bigint FROM procurement_accounts WHERE id=$1`, account).Scan(&accountHeld, &accountSpent); err != nil || accountHeld != 0 || accountSpent != wantSupplierCost {
						t.Fatal("supplier cost was not settled once", accountHeld, accountSpent, wantSupplierCost, err)
					}
				})
			}
		})
	}
}

func TestCostVectorApplicationRefundKeepsOriginalSourceGraph(t *testing.T) {
	f := newGatewayFixture(t, []http.HandlerFunc{gatewaySuccess(t)}, nil)
	probeAdmin(t, f)
	var operator, approver string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_operator' ORDER BY id LIMIT 1`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE platform_role='finance_approver' ORDER BY id LIMIT 1`).Scan(&approver); err != nil {
		t.Fatal(err)
	}
	procurementMeterApplication(t, f)
	forbidScalarQuoteReads(t, f)
	path, raw := keyPolicyProtocolInput(t, provider.ProtocolChat)
	response := keyPolicyHTTP(t, f.server.Engine(), "POST", path, raw, "", "", "", "127.0.0.1:1234", http.Header{"Authorization": []string{"Bearer " + f.token}})
	if response.Code != 200 || f.posts.Load() != 1 {
		t.Fatal("restricted APP request failed", response.Code, response.Body.String(), f.posts.Load())
	}
	requestID := response.Header().Get("X-Request-ID")
	var settlementID string
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM settlements WHERE request_id=$1`, requestID).Scan(&settlementID); err != nil {
		t.Fatal(err)
	}
	ref := uuid.NewString()
	order, err := f.server.Billing.CreateRefund(context.Background(), billing.RefundInput{TenantID: f.tenant, SettlementID: settlementID, ActorID: operator, Amount: 5, BusinessRef: ref, Reason: "vector source refund", IdempotencyKey: ref, RequestHMAC: ref})
	if err != nil {
		t.Fatal("restricted APP refund create failed", err)
	}
	if _, err = f.server.Billing.ApproveRefund(context.Background(), billing.ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: approver}); err != nil {
		t.Fatal("restricted APP refund approve failed", err)
	}
	if _, err = f.server.Billing.ApproveRefund(context.Background(), billing.ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: approver}); err != nil {
		t.Fatal("refund replay failed", err)
	}
	wallet, err := f.server.Billing.Wallet(context.Background(), billing.Query{TenantID: f.tenant})
	if err != nil || wallet.Available != 985 || wallet.Held != 0 {
		t.Fatal("refund changed the wrong wallet facts", wallet, err)
	}
	var refunded, refundEdges bool
	if err = f.pool.QueryRow(context.Background(), `SELECT s.refunded_amount=5,
 (SELECT count(*) FROM funding_consumption_refund_lots WHERE settlement_id=$1 AND refund_order_id=$2)=1
 FROM settlements s JOIN refund_orders r ON r.settlement_id=s.id WHERE s.id=$1 AND r.id=$2`, settlementID, order.ID).Scan(&refunded, &refundEdges); err != nil || !refunded || !refundEdges {
		t.Fatal("refund did not preserve the original source graph", refunded, refundEdges, err)
	}
	if check, checkErr := f.server.Billing.CheckWalletSources(context.Background(), f.tenant); checkErr != nil || !check.Balanced || check.SourceMismatches != 0 || check.EventMismatches != 0 || check.AllocationMismatches != 0 || check.WalletMismatches != 0 {
		t.Fatal("refund left wallet source facts unbalanced", check, checkErr)
	}
	var refundCount int
	if err = f.pool.QueryRow(context.Background(), `SELECT count(*) FROM refund_orders WHERE settlement_id=$1 AND state='approved'`, settlementID).Scan(&refundCount); err != nil || refundCount != 1 {
		t.Fatal("refund replay created another financial order", refundCount, err)
	}
}
