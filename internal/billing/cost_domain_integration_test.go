package billing

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

func TestPostgresCostQuoteDomainIsolation(t *testing.T) {
	for _, ready := range []bool{true, false} {
		name := map[bool]string{true: "plan_ready", false: "plan_unready"}[ready]
		t.Run(name, func(t *testing.T) {
			f := integration(t)
			r, in := managedCostRequest(t, f)
			if !ready {
				if _, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
					t.Fatal(err)
				}
				input := f.input()
				input.RouteRevision, input.RequestHMAC = 1, "unready"
				var err error
				r, err = f.store.Reserve(f.ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				in.RequestID = r.RequestID
			}
			// Force the procurement-lock re-read as well as ordinary attempt and
			// dispatch reads. Foreign quotes must never make the original stale.
			account := procurementFixture(t, f, 1000)
			foreignModel, profile := uuid.NewString(), uuid.NewString()
			if _, err := f.pool.Exec(f.ctx, `SELECT meter_insert_text_model($1,'foreign-model',jsonb_build_object('version',$2::text,'model','foreign-model','protocol','openai_chat','input_token_upper_bound',80,'context_limit',120,'output_limit',40,'default_output_limit',40,'output_limit_field','max_tokens'),$2)`, foreignModel, profile); err != nil {
				t.Fatal(err)
			}
			other := procurementSecondChannel(t, f)
			for _, tc := range []struct {
				name, channel, upstream, model, basis string
				input, output                         *int64
			}{
				{"schema_known", f.channel, "upstream", foreignModel, "contract", costDomainRate(9000000), costDomainRate(9000000)},
				{"schema_zero", f.channel, "upstream", foreignModel, "contract", costDomainRate(0), costDomainRate(0)},
				{"schema_unknown", f.channel, "upstream", foreignModel, "unknown", nil, nil},
				{"channel_known", other.channel, "upstream", f.model, "invoice", costDomainRate(0), costDomainRate(0)},
				{"channel_unknown", other.channel, "upstream", f.model, "unknown", nil, nil},
				{"upstream_unknown", f.channel, "other-upstream", f.model, "unknown", nil, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					cost := meterquote.PublishCost(t, f.ctx, f.pool, meterquote.CostPrice{ChannelID: tc.channel, UpstreamModel: tc.upstream, ModelID: tc.model, Actor: f.admin, Basis: tc.basis, InputRate: tc.input, OutputRate: tc.output})
					tx, err := f.pool.Begin(f.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(f.ctx)
					attempt := uuid.NewString()
					if _, err = tx.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'upstream')`, attempt, f.tenant, r.RequestID, f.channel, f.version, f.credential); err != nil {
						t.Fatal(err)
					}
					if _, err = tx.Exec(f.ctx, `SELECT meter_insert_attempt_snapshot($1,$2,$3,$4,$5)`, f.tenant, attempt, r.RequestID, cost, tc.basis); err != nil {
						t.Fatal(err)
					}
					// Select this guard explicitly: even a well-formed known or
					// unknown outer quote must fail the independent domain check.
					_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_attempt_matches_request IMMEDIATE`)
					if err == nil || !strings.Contains(err.Error(), "attempt quote domain does not match original attempt") {
						t.Fatal("foreign quote accepted", err)
					}
				})
			}
			attempt, err := f.store.RecordAttempt(f.ctx, in)
			if err != nil {
				t.Fatal("foreign domains changed the original attempt quote", err)
			}
			procurementBalance(t, f, account, "240", "0")
			if err = f.store.PrepareDispatch(f.ctx, DispatchInput{TenantID: f.tenant, AttemptID: attempt.ID}); err != nil {
				t.Fatal("foreign domains invalidated dispatch", err)
			}
			f.final(t, r, confirmed(), "completed", "done", "content", true)
			for range 2 {
				if _, err = f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err != nil {
					t.Fatal(err)
				}
			}
			var version, cost string
			var settlements int
			if err = f.pool.QueryRow(f.ctx, `SELECT s.cost_version_id,a.cost_amount::text,(SELECT count(*) FROM settlements WHERE request_id=$2) FROM attempt_meter_snapshots s JOIN upstream_attempts a ON a.id=s.attempt_id WHERE a.id=$1`, attempt.ID, r.RequestID).Scan(&version, &cost, &settlements); err != nil || version != in.CostQuote.VersionID() || cost != "30" || settlements != 1 {
				t.Fatal("original quote/unique settlement changed", version, cost, settlements, err)
			}
			procurementBalance(t, f, account, "0", "30")
			f.wallet(t, 985, 0)
			f.check(t)
		})
	}
}

func costDomainRate(n int64) *int64 { return &n }
