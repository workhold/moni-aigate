package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// Disable the scalar read ABI only in this test's isolated schema. Any hidden
// actual-pricing dependency must fail, not silently pass by using the old path.
func forbidScalarActualReads(t *testing.T, f *fixture) {
	t.Helper()
	for _, sql := range []string{
		`CREATE OR REPLACE FUNCTION meter_price_million(price_id text, dimension text) RETURNS bigint LANGUAGE plpgsql STABLE AS $$ BEGIN RAISE EXCEPTION 'scalar actual pricing forbidden'; END $$`,
		`CREATE OR REPLACE FUNCTION meter_cost_million(cost_id text, dimension text) RETURNS bigint LANGUAGE plpgsql STABLE AS $$ BEGIN RAISE EXCEPTION 'scalar actual pricing forbidden'; END $$`,
	} {
		if _, err := f.pool.Exec(f.ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresVectorActualKeepsOriginalCanonical(t *testing.T) {
	for _, ready := range []bool{true, false} {
		for _, kind := range []string{"known", "zero", "basis_unknown", "version_missing", "over_bound", "not_sent", "rejected_no_consumption"} {
			t.Run(map[bool]string{true: "plan_ready", false: "plan_unready"}[ready]+"/"+kind, func(t *testing.T) {
				f := integration(t)
				rate := int64(500000)
				if kind == "zero" {
					rate = 0
				}
				publishBillingDefault(t, f, f.model, rate, rate, time.Now().UTC().Add(-time.Hour))
				basis := "contract"
				var costRate *int64 = billingRate(rate)
				if kind == "basis_unknown" {
					basis, costRate = "unknown", nil
				}
				publishBillingCost(t, f, f.channel, "upstream", basis, costRate, costRate, time.Time{})
				input := f.input()
				if !ready {
					input.RequestHMAC = "unready"
				}
				r, err := f.store.Reserve(f.ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				managed := kind != "basis_unknown" && kind != "version_missing"
				var account string
				if managed {
					account = procurementFixture(t, f, 1000)
				}
				attemptInput := dispatchBatchAttemptInput(f, r)
				if managed {
					attemptInput = procurementAttempt(t, f, r)
				} else if kind == "version_missing" {
					attemptInput.UpstreamModel = "unpriced"
				}
				a, err := f.store.RecordAttempt(f.ctx, attemptInput)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
					t.Fatal(err)
				}
				var requestReady, attemptReady bool
				if err = f.pool.QueryRow(f.ctx, `SELECT q.retail_rates_canonical IS NOT NULL,s.supplier_rates_canonical IS NOT NULL FROM request_meter_snapshots q JOIN attempt_meter_snapshots s ON s.request_id=q.request_id WHERE s.attempt_id=$1`, a.ID).Scan(&requestReady, &attemptReady); err != nil || requestReady != ready || attemptReady != (ready && managed) {
					t.Fatal("readiness gate changed", requestReady, attemptReady, err)
				}
				// Later publication (including filling a previously missing price)
				// must not change either side's original financial contract.
				publishBillingDefault(t, f, f.model, 9000000, 9000000, time.Now().UTC())
				publishBillingCost(t, f, f.channel, attemptInput.UpstreamModel, "contract", billingRate(9000000), billingRate(9000000), time.Now().UTC())
				forbidScalarActualReads(t, f)
				usage := Usage{Status: "confirmed_final", InputTokens: 1, OutputTokens: 1, TotalTokens: 2}
				wantCharge, wantRaw, wantWaived, wantCost := Money(1), "1", "0", "1"
				if kind == "zero" {
					wantCharge, wantRaw, wantCost = 0, "0", "0"
				} else if kind == "over_bound" {
					usage = Usage{Status: "confirmed_final", InputTokens: 110, OutputTokens: 30, TotalTokens: 140}
					wantCharge, wantRaw, wantWaived, wantCost = 60, "70", "10", "70"
				}
				finalInput := procurementFinal(t, f, r, a, usage, "done")
				if oneOf(kind, "not_sent", "rejected_no_consumption") {
					finalInput.Usage = Usage{Status: "missing"}
					finalInput.ProtocolOutcome, finalInput.TransportEndReason = "failed", kind
					finalInput.DeliveryStage, finalInput.ContentWritten = "none", false
					wantCharge, wantRaw, wantCost = 0, "0", "0"
				}
				final, err := f.store.RecordFinalization(f.ctx, finalInput)
				if err != nil {
					t.Fatal(err)
				}
				var first string
				for range 2 {
					settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
					if err != nil || settled.ChargedAmount != wantCharge || settled.RawRetailAmount != wantRaw || settled.WaivedRetailAmount != wantWaived || first != "" && settled.ID != first {
						t.Fatal("original vector/unique settlement changed", settled, err)
					}
					first = settled.ID
				}
				var cost, costFinal *string
				var status string
				if err = f.pool.QueryRow(f.ctx, `SELECT cost_amount::text,cost_status,latest_cost_finalization_id FROM upstream_attempts WHERE id=$1`, a.ID).Scan(&cost, &status, &costFinal); err != nil {
					t.Fatal(err)
				}
				if managed {
					if cost == nil || *cost != wantCost || status != "contract" || costFinal == nil || *costFinal != final.ID {
						t.Fatal("original supplier vector changed", cost, status, costFinal)
					}
					procurementBalance(t, f, account, "0", wantCost)
				} else if cost != nil || status != "unknown" || costFinal != nil {
					t.Fatal("unknown quote turned into a price", cost, status, costFinal)
				}
				// Raw readers must not borrow another tenant/request/attempt's final.
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx)
				for _, wrong := range []struct{ tenant, request, attempt string }{
					{uuid.NewString(), r.RequestID, a.ID}, {f.tenant, uuid.NewString(), a.ID}, {f.tenant, r.RequestID, uuid.NewString()},
				} {
					_, err = storedActualAmount(f.ctx, tx, wrong.tenant, wrong.request, final.ID, wrong.attempt, metering.Supplier)
					assertCode(t, err, "invalid_meter_snapshot")
				}
				if err = tx.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				f.wallet(t, 1000-wantCharge, 0)
				f.check(t)
			})
		}
	}
}

func TestPostgresVectorActualManualCustomerDoesNotConfirmSupplier(t *testing.T) {
	for _, ready := range []bool{true, false} {
		t.Run(map[bool]string{true: "plan_ready", false: "plan_unready"}[ready], func(t *testing.T) {
			f := integration(t)
			input := f.input()
			if !ready {
				input.RequestHMAC = "unready"
			}
			r, err := f.store.Reserve(f.ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			account := procurementFixture(t, f, 1000)
			a, err := f.store.RecordAttempt(f.ctx, procurementAttempt(t, f, r))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: a.ID}); err != nil {
				t.Fatal(err)
			}
			forbidScalarActualReads(t, f)
			missing := procurementFinal(t, f, r, a, Usage{Status: "missing"}, "done")
			if _, err = f.store.RecordFinalization(f.ctx, missing); err != nil {
				t.Fatal(err)
			}
			pending, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || pending.Status != "pending_reconciliation" {
				t.Fatal(pending, err)
			}
			if _, err = f.store.Reconcile(f.ctx, ReconcileInput{TenantID: f.tenant, RequestID: r.RequestID, ActorID: f.operator, Action: "propose", EvidenceRef: "invoice:customer-only", Reason: "confirm customer usage only", RetailQuantitiesCanonical: retailQuantitiesForRequest(t, f, r.RequestID, 10, 5)}); err != nil {
				t.Fatal(err)
			}
			approved, err := f.store.Reconcile(f.ctx, ReconcileInput{TenantID: f.tenant, RequestID: r.RequestID, ActorID: f.approver, Action: "approve"})
			if err != nil || approved.Settlement == nil || approved.Settlement.ChargedAmount != 15 {
				t.Fatal(approved, err)
			}
			var state, final string
			var absent bool
			if err = f.pool.QueryRow(f.ctx, `SELECT f.id,f.supplier_usage_state,f.supplier_quantities_canonical IS NULL AND f.supplier_quantities_digest IS NULL AND a.cost_status='unknown' AND a.cost_amount IS NULL AND a.latest_cost_finalization_id IS NULL AND a.latest_cost_correction_id IS NULL FROM finalization_records f JOIN metering_events e ON e.id=f.event_id JOIN upstream_attempts a ON a.id=f.attempt_id WHERE f.request_id=$1 AND e.source='approved_reconciliation'`, r.RequestID).Scan(&final, &state, &absent); err != nil || state != "missing" || !absent {
				t.Fatal("manual customer usage confirmed supplier", state, absent, err)
			}
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			_, err = storedActualAmount(f.ctx, tx, f.tenant, r.RequestID, final, a.ID, metering.Supplier)
			assertCode(t, err, "invalid_meter_snapshot")
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			procurementBalance(t, f, account, "240", "0")
			f.wallet(t, 985, 0)
			f.check(t)
		})
	}
}
