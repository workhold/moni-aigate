package billing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// Gemini cases publish their own immutable model/profile/price; the original
// fixture model and historical versions are never rewritten for a new protocol.
func geminiBillingInput(t *testing.T, f *fixture, profileOutput int64) ReserveInput {
	t.Helper()
	model, version := uuid.NewString(), uuid.NewString()
	profile, err := json.Marshal(map[string]any{
		"protocol": "gemini_generate_content", "version": version, "model": "gemini-" + model,
		"input_token_upper_bound": int64(100), "context_limit": int64(120),
		"output_limit": profileOutput, "default_output_limit": profileOutput,
		"output_limit_field": "maxOutputTokens", "usage_semantics": "cumulative",
		"supports_tools": false, "supports_parallel_tool_calls": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{"SELECT meter_insert_text_model($1,$2,$3::jsonb,$4)", []any{model, "gemini-" + model, profile, version}},
		{"INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", []any{f.tenant, model}},
		{"UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", []any{f.key, model}},
	} {
		if _, err = tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	policy := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision,cost_mode,unknown_cost_policy) VALUES($1,$2,1,'manual','allow')`, policy, model); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,priority,weight,max_inflight,variant_key) VALUES($1,$2,$3,'upstream',0,1,10,'default')`, policy, f.channel, f.version); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE models SET active_revision=1 WHERE id=$1`, model); err != nil {
		t.Fatal(err)
	}
	outputRate := int64(2000000)
	if profileOutput == 0 {
		outputRate = 0
	}
	meterquote.PublishDefaultPrice(t, f.ctx, tx, meterquote.DefaultPrice{ModelID: model, Actor: f.operator, InputRate: 1000000, OutputRate: outputRate, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.ModelID, in.ProfileVersion, in.RouteRevision = model, version, 1
	return in
}

func geminiBillingKey(t *testing.T, f *fixture, spent, held int64) {
	t.Helper()
	var actualSpent, actualHeld int64
	if err := f.pool.QueryRow(f.ctx, "SELECT spent,held FROM api_keys WHERE id=$1", f.key).Scan(&actualSpent, &actualHeld); err != nil || actualSpent != spent || actualHeld != held {
		t.Fatal("Gemini Key counters differ from financial facts", actualSpent, actualHeld, spent, held, err)
	}
}

func TestPostgresGeminiThinkingAndCacheSettleOnce(t *testing.T) {
	f := integration(t)
	in := geminiBillingInput(t, f, 20)
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil || r.Amount != 140 {
		t.Fatal("Gemini hard reservation did not cover input and aggregate output", r, err)
	}
	retailAssertSnapshot(t, f, r, r.PriceVersionID, 1000000, 2000000)
	defer f.store.EndRequest(r.RequestID)
	f.wallet(t, 860, 140)
	geminiBillingKey(t, f, 0, 140)
	var inputBound, outputBound, inputRate, outputRate int64
	if err = f.pool.QueryRow(f.ctx, `SELECT s.input_bound,s.output_bound,
 (li.numerator*1000000/li.denominator)::bigint,(lo.numerator*1000000/lo.denominator)::bigint
 FROM request_meter_snapshots s
 JOIN price_rate_lines li ON li.price_version_id=s.price_version_id AND li.dimension='text_input_token' AND li.unit='token'
 JOIN price_rate_lines lo ON lo.price_version_id=s.price_version_id AND lo.dimension='text_output_token' AND lo.unit='token'
 WHERE s.request_id=$1`, r.RequestID).Scan(&inputBound, &outputBound, &inputRate, &outputRate); err != nil || inputBound != 100 || outputBound != 20 || inputRate != 1000000 || outputRate != 2000000 {
		t.Fatal("Gemini immutable reservation snapshot differs", inputBound, outputBound, inputRate, outputRate, err)
	}
	// The adapter has normalized P10/C3/H2/T15/K3. Cache is a subset of P;
	// thinking is part of output, so the sole retail charge is 10 + (3+2)*2.
	f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 3 + 2, TotalTokens: 15, ReasoningTokens: 2, CachedTokens: 3}, "completed", "done", "none", false)
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.Status != "settled" || settled.ChargedAmount != 20 || settled.RawRetailAmount != "20" || settled.WaivedRetailAmount != "0" {
		t.Fatal("Gemini thinking/cache charge is not 20", settled, err)
	}
	var released int64
	if err = f.pool.QueryRow(f.ctx, "SELECT l.signed_delta FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=$1 AND a.kind='AVAILABLE'", settled.JournalID).Scan(&released); err != nil || released != 120 {
		t.Fatal("Gemini settlement did not release unused 120", released, err)
	}
	var storedInput, storedOutput int64
	if err = f.pool.QueryRow(f.ctx, `SELECT meter_event_quantity(e.id,'text_input_token'),meter_event_quantity(e.id,'text_output_token')
 FROM request_records r JOIN finalization_records z ON z.id=r.latest_finalization_id
 JOIN metering_events e ON e.id=z.event_id WHERE r.id=$1`, r.RequestID).Scan(&storedInput, &storedOutput); err != nil || storedInput != 10 || storedOutput != 5 {
		t.Fatal("Gemini durable normalized quantities differ", storedInput, storedOutput, err)
	}
	again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || again.ID != settled.ID || again.JournalID != settled.JournalID || again.ChargedAmount != 20 {
		t.Fatal("Gemini repeated settlement created new financial facts", again, err)
	}
	var count int
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM settlements WHERE request_id=$1", r.RequestID).Scan(&count); err != nil || count != 1 {
		t.Fatal("Gemini settlement count", count, err)
	}
	f.wallet(t, 980, 0)
	geminiBillingKey(t, f, 20, 0)
	f.check(t)
}

func TestPostgresGeminiZeroOutputRejectsWithoutFinancialFacts(t *testing.T) {
	for _, profileOutput := range []int64{0, 20} {
		name := "zero_profile"
		if profileOutput > 0 {
			name = "zero_request"
		}
		t.Run(name, func(t *testing.T) {
			f := integration(t)
			in := geminiBillingInput(t, f, profileOutput)
			if profileOutput > 0 {
				in.OutputLimit = 0
			}
			for range 2 {
				_, err := f.store.Reserve(f.ctx, in)
				assertCode(t, err, "invalid_bounds")
				var requests, reservations, idempotency, journals, recovery, events, outbox int
				err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM request_records WHERE id=$1),
 (SELECT count(*) FROM reservations WHERE request_id=$1),
 (SELECT count(*) FROM request_idempotency WHERE tenant_id=$2 AND key=$3),
 (SELECT count(*) FROM journals WHERE reference=$1),
 (SELECT count(*) FROM recovery_jobs WHERE request_id=$1),
 (SELECT count(*) FROM audit_events WHERE request_id=$1),
 (SELECT count(*) FROM projection_outbox WHERE sanitized_payload->>'request_id'=$1)`, in.RequestID, f.tenant, in.IdempotencyKey).Scan(&requests, &reservations, &idempotency, &journals, &recovery, &events, &outbox)
				if err != nil || requests != 0 || reservations != 0 || idempotency != 0 || journals != 0 || recovery != 0 || events != 0 || outbox != 0 {
					t.Fatal("Gemini zero output refusal left persistent facts", requests, reservations, idempotency, journals, recovery, events, outbox, err)
				}
				if _, live := f.store.active.Load(in.RequestID); live {
					t.Fatal("Gemini zero output refusal left an active request")
				}
				f.wallet(t, 1000, 0)
				geminiBillingKey(t, f, 0, 0)
				f.check(t)
			}
		})
	}
}

func TestPostgresGeminiAggregateOutputOverReservationCapsAndBlocks(t *testing.T) {
	f := integration(t)
	in := geminiBillingInput(t, f, 20)
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil || r.Amount != 140 {
		t.Fatal(r, err)
	}
	defer f.store.EndRequest(r.RequestID)
	// Visible candidates alone are only 3 tokens. The aggregate output is 80
	// after thinking is included, so raw retail 10 + 80*2 exceeds R140.
	f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 3 + 77, TotalTokens: 90, ReasoningTokens: 77, CachedTokens: 3}, "completed", "done", "none", false)
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.Status != "settled" || settled.ChargedAmount != 140 || settled.RawRetailAmount != "170" || settled.WaivedRetailAmount != "30" {
		t.Fatal("Gemini aggregate output was not capped at the hard reservation", settled, err)
	}
	var blocks, audits int
	if err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM billing_profile_blocks WHERE model_id=$1 AND profile_version=$2 AND request_id=$3),
 (SELECT count(*) FROM audit_events WHERE action='billing.profile_blocked' AND target=$1 AND request_id=$3)`, in.ModelID, in.ProfileVersion, r.RequestID).Scan(&blocks, &audits); err != nil || blocks != 1 || audits != 1 {
		t.Fatal("Gemini aggregate output breach was not blocked and audited", blocks, audits, err)
	}
	again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || again.ID != settled.ID || again.JournalID != settled.JournalID || again.ChargedAmount != 140 {
		t.Fatal("Gemini capped settlement was not idempotent", again, err)
	}
	next := in
	next.RequestID, next.IdempotencyKey = uuid.NewString(), uuid.NewString()
	_, err = f.store.Reserve(f.ctx, next)
	assertCode(t, err, "profile_blocked")
	var nextRequests, nextIdempotency int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM request_records WHERE id=$1),
 (SELECT count(*) FROM request_idempotency WHERE tenant_id=$2 AND key=$3)`, next.RequestID, f.tenant, next.IdempotencyKey).Scan(&nextRequests, &nextIdempotency); err != nil || nextRequests != 0 || nextIdempotency != 0 {
		t.Fatal("Gemini blocked profile admitted another request", nextRequests, nextIdempotency, err)
	}
	f.wallet(t, 860, 0)
	geminiBillingKey(t, f, 140, 0)
	f.check(t)
}
