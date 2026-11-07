package billing

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func TestPostgresLifecycleMeterSnapshotReadiness(t *testing.T) {
	f := integration(t)
	meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ChannelID: f.channel, UpstreamModel: "unknown", ModelID: f.model, Actor: f.admin, Basis: "unknown"})
	for _, hmac := range []string{fixtureRequestHMAC, "hmac", strings.Repeat("AB", 32), strings.Repeat("a", 63)} {
		for _, upstream := range []string{"upstream", "unknown", "missing"} {
			t.Run(hmac[:4]+"/"+upstream, func(t *testing.T) {
				in := f.input()
				in.RequestHMAC = hmac
				r, err := f.store.Reserve(f.ctx, in)
				if err != nil {
					t.Fatal(err)
				}
				a, err := f.store.RecordAttempt(f.ctx, AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: upstream, Sequence: 1})
				if err != nil {
					t.Fatal(err)
				}
				var requestFields, attemptFields, rateFields, boundRows int
				if err = f.pool.QueryRow(f.ctx, `SELECT
 num_nonnulls(s.plan_canonical,s.plan_digest,s.retail_quote_canonical,s.retail_quote_digest,s.retail_bounds_canonical,s.retail_bounds_digest,s.supplier_bounds_canonical,s.supplier_bounds_digest,s.retail_rates_canonical,s.retail_rates_digest,s.retail_meter_quote_digest),
 num_nonnulls(a.plan_canonical,a.plan_digest,a.parent_plan_digest,a.supplier_quote_canonical,a.supplier_quote_digest,a.retail_bounds_canonical,a.retail_bounds_digest,a.supplier_bounds_canonical,a.supplier_bounds_digest),
 num_nonnulls(a.supplier_rates_canonical,a.supplier_rates_digest,a.supplier_meter_quote_digest),
 (SELECT count(*) FROM request_meter_bound_lines WHERE request_id=s.request_id)+(SELECT count(*) FROM attempt_meter_bound_lines WHERE attempt_id=a.attempt_id)
 FROM request_meter_snapshots s JOIN attempt_meter_snapshots a ON a.request_id=s.request_id WHERE a.attempt_id=$1`, a.ID).Scan(&requestFields, &attemptFields, &rateFields, &boundRows); err != nil {
					t.Fatal(err)
				}
				wantRequest, wantAttempt, wantRates := 0, 0, 0
				if hmac == fixtureRequestHMAC {
					wantRequest, wantAttempt = 11, 9
					if upstream == "upstream" {
						wantRates = 3
					}
				}
				if requestFields != wantRequest || attemptFields != wantAttempt || rateFields != wantRates || boundRows != 8 {
					t.Fatal("snapshot readiness or complete bounds changed", requestFields, attemptFields, rateFields, boundRows)
				}
				// Exercise the deferred database guard independently of the writer:
				// a different snapshot cannot smuggle a pure quote into an unready
				// plan, or omit a required digest from a ready plan.
				tx, err := f.pool.Begin(f.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(f.ctx)
				_, err = tx.Exec(f.ctx, `INSERT INTO attempt_meter_snapshots(tenant_id,attempt_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,retail_bounds_canonical,retail_bounds_digest,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest)
 SELECT tenant_id,$2,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,cost_version_id,quote_status,unknown_reason,cost_basis,supplier_rates_canonical,supplier_rates_digest,
 CASE WHEN plan_digest IS NULL THEN meter_bounds_canonical(schema_id,'retail',input_bound,output_bound) ELSE retail_bounds_canonical END,
 NULL,supplier_bounds_canonical,supplier_bounds_digest,supplier_meter_quote_digest,input_bound,output_bound,plan_canonical,plan_digest,parent_plan_digest,supplier_quote_canonical,supplier_quote_digest
 FROM attempt_meter_snapshots WHERE attempt_id=$1`, a.ID, uuid.NewString())
				if err == nil {
					_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_attempt_vectors_match IMMEDIATE`)
				}
				if err == nil || (!strings.Contains(err.Error(), "attempt meter vectors") && !strings.Contains(err.Error(), "check constraint")) {
					t.Fatal("invalid pure vectors accepted", err)
				}
				if err = tx.Rollback(f.ctx); err != nil {
					t.Fatal(err)
				}
				if hmac != fixtureRequestHMAC && upstream == "upstream" {
					_, err = f.pool.Exec(f.ctx, `INSERT INTO request_meter_snapshots(tenant_id,request_id,model_id,profile_version,schema_id,schema_digest,variant_key,price_version_id,pricing_group_id,tenant_pricing_revision,retail_priced_at,input_bound,output_bound,retail_rates_canonical)
 SELECT s.tenant_id,$2,s.model_id,s.profile_version,s.schema_id,s.schema_digest,s.variant_key,s.price_version_id,s.pricing_group_id,s.tenant_pricing_revision,s.retail_priced_at,s.input_bound,s.output_bound,p.rates_canonical
 FROM request_meter_snapshots s JOIN price_versions p ON p.id=s.price_version_id WHERE s.request_id=$1`, r.RequestID, uuid.NewString())
					if err == nil || !strings.Contains(err.Error(), "request meter vectors require plan") {
						t.Fatal("unready request accepted a standalone pure rate snapshot", err)
					}
				}
				if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}
