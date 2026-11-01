package billing

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Each case publishes a separate immutable profile and price. Existing fixture
// models and historical capability/price rows are never edited to enable zero
// output; only the mutable Key scope is extended to authorize the new model.
func retrievalBillingInput(t *testing.T, f *fixture, protocol string, outputRate int64, change func(map[string]any)) ReserveInput {
	t.Helper()
	model, version := uuid.NewString(), uuid.NewString()
	profile := map[string]any{
		"protocol": protocol, "version": version, "model": "retrieval-" + model,
		"input_token_upper_bound": int64(100), "context_limit": int64(100),
		"output_limit": int64(0), "default_output_limit": int64(0), "output_limit_field": "",
		"supports_tools": false, "supports_parallel_tool_calls": false,
	}
	if change != nil {
		change(profile)
	}
	body, err := json.Marshal(profile)
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
		{"INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$2,$3,$4)", []any{model, "retrieval-" + model, body, version}},
		{"INSERT INTO model_capability_versions(id,model_id,profile) VALUES($1,$2,$3)", []any{version, model, body}},
		{"INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", []any{f.tenant, model}},
		{"UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", []any{f.key, model}},
		{"INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1000000,$2,clock_timestamp()-interval '1 day',$3)", []any{model, outputRate, f.operator}},
	} {
		if _, err = tx.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.ModelID, in.ProfileVersion = model, version
	in.OutputLimit = 0
	return in
}

func retrievalBillingFinal(t *testing.T, f *fixture, r Reservation, input int64) {
	t.Helper()
	f.final(t, r, Usage{Status: "confirmed_final", InputTokens: input, OutputTokens: 0, TotalTokens: input}, "completed", "done", "none", false)
}

func retrievalBillingKey(t *testing.T, f *fixture, spent, held int64) {
	t.Helper()
	var actualSpent, actualHeld int64
	if err := f.pool.QueryRow(f.ctx, "SELECT spent,held FROM api_keys WHERE id=$1", f.key).Scan(&actualSpent, &actualHeld); err != nil || actualSpent != spent || actualHeld != held {
		t.Fatal("retrieval Key counters differ from financial facts", actualSpent, actualHeld, spent, held, err)
	}
}

func retrievalBillingNoRequest(t *testing.T, f *fixture, in ReserveInput) {
	t.Helper()
	var requests, reservations, idempotency, journals, recovery, events, outbox int
	err := f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM request_records WHERE id=$1),
 (SELECT count(*) FROM reservations WHERE request_id=$1),
 (SELECT count(*) FROM request_idempotency WHERE tenant_id=$2 AND key=$3),
 (SELECT count(*) FROM journals WHERE reference=$1),
 (SELECT count(*) FROM recovery_jobs WHERE request_id=$1),
 (SELECT count(*) FROM audit_events WHERE request_id=$1),
 (SELECT count(*) FROM projection_outbox WHERE sanitized_payload->>'request_id'=$1)`, in.RequestID, f.tenant, in.IdempotencyKey).Scan(&requests, &reservations, &idempotency, &journals, &recovery, &events, &outbox)
	if err != nil || requests != 0 || reservations != 0 || idempotency != 0 || journals != 0 || recovery != 0 || events != 0 || outbox != 0 {
		t.Fatal("rejected retrieval admission left persistent facts", requests, reservations, idempotency, journals, recovery, events, outbox, err)
	}
	if _, live := f.store.active.Load(in.RequestID); live {
		t.Fatal("rejected retrieval admission left an active request")
	}
	f.wallet(t, 1000, 0)
	retrievalBillingKey(t, f, 0, 0)
	f.check(t)
}

func TestPostgresRetrievalInputOnlyReserveAndSettle(t *testing.T) {
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f := integration(t)
			in := retrievalBillingInput(t, f, protocol, 0, nil)
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil || r.Amount != 100 || r.InputRate != 1000000 || r.OutputRate != 0 {
				t.Fatal("input-only hard reservation was not 100", r, err)
			}
			defer f.store.EndRequest(r.RequestID)
			f.wallet(t, 900, 100)
			retrievalBillingKey(t, f, 0, 100)
			var inputBound, outputBound, outputPrice int64
			if err = f.pool.QueryRow(f.ctx, "SELECT input_upper_bound,output_limit,output_rate FROM request_records WHERE id=$1", r.RequestID).Scan(&inputBound, &outputBound, &outputPrice); err != nil || inputBound != 100 || outputBound != 0 || outputPrice != 0 {
				t.Fatal("input-only request snapshot was not preserved", inputBound, outputBound, outputPrice, err)
			}
			retrievalBillingFinal(t, f, r, 10)
			settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || settled.Status != "settled" || settled.ChargedAmount != 10 || settled.RawRetailAmount != "10" || settled.WaivedRetailAmount != "0" {
				t.Fatal("input-only settlement did not charge trustworthy input", settled, err)
			}
			var released int64
			if err = f.pool.QueryRow(f.ctx, "SELECT l.signed_delta FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=$1 AND a.kind='AVAILABLE'", settled.JournalID).Scan(&released); err != nil || released != 90 {
				t.Fatal("input-only settlement did not return 90", released, err)
			}
			again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || again.ID != settled.ID || again.JournalID != settled.JournalID || again.ChargedAmount != 10 {
				t.Fatal("input-only settlement replay duplicated financial facts", again, err)
			}
			var storedInput, storedOutput, storedTotal int64
			if err = f.pool.QueryRow(f.ctx, `SELECT (e.normalized_usage->>'input_tokens')::bigint,
 (e.normalized_usage->>'output_tokens')::bigint,(e.normalized_usage->>'total_tokens')::bigint
 FROM request_records r JOIN finalization_records z ON z.id=r.latest_finalization_id
 JOIN metering_events e ON e.id=z.event_id WHERE r.id=$1`, r.RequestID).Scan(&storedInput, &storedOutput, &storedTotal); err != nil || storedInput != 10 || storedOutput != 0 || storedTotal != 10 {
				t.Fatal("normalized retrieval usage changed", storedInput, storedOutput, storedTotal, err)
			}
			f.wallet(t, 990, 0)
			retrievalBillingKey(t, f, 10, 0)
			f.check(t)
		})
	}
}

func TestPostgresRetrievalReservationsRespectConcurrentKeyBudget(t *testing.T) {
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f := integration(t)
			base := retrievalBillingInput(t, f, protocol, 0, nil)
			if _, err := f.pool.Exec(f.ctx, "UPDATE api_keys SET lifetime_limit=250 WHERE id=$1", f.key); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				reservation Reservation
				err         error
			}
			outcomes := make([]outcome, 8)
			inputs := make([]ReserveInput, len(outcomes))
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range outcomes {
				inputs[i] = base
				inputs[i].RequestID, inputs[i].IdempotencyKey = uuid.NewString(), uuid.NewString()
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					outcomes[i].reservation, outcomes[i].err = f.store.Reserve(f.ctx, inputs[i])
				}(i)
			}
			close(start)
			wg.Wait()
			var winners []Reservation
			for i, got := range outcomes {
				if got.err == nil {
					if got.reservation.Amount != 100 {
						t.Fatal("concurrent input-only reservation underheld", got.reservation)
					}
					winners = append(winners, got.reservation)
					defer f.store.EndRequest(got.reservation.RequestID)
				} else {
					assertCode(t, got.err, "key_budget_exceeded")
					var count int
					if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM request_idempotency WHERE tenant_id=$1 AND key=$2", f.tenant, inputs[i].IdempotencyKey).Scan(&count); err != nil || count != 0 {
						t.Fatal("budget refusal consumed an idempotency key", count, err)
					}
				}
			}
			if len(winners) != 2 {
				t.Fatal("input-only reservations exceeded or lost the hard budget", len(winners), outcomes)
			}
			f.wallet(t, 800, 200)
			retrievalBillingKey(t, f, 0, 200)
			f.check(t)
			for _, r := range winners {
				retrievalBillingFinal(t, f, r, 10)
				v, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
				if err != nil || v.ChargedAmount != 10 {
					t.Fatal(v, err)
				}
			}
			f.wallet(t, 980, 0)
			retrievalBillingKey(t, f, 20, 0)
			f.check(t)
		})
	}
}

func TestPostgresRetrievalZeroOutputCannotRelaxOtherContracts(t *testing.T) {
	for _, protocol := range []string{"openai_chat", "openai_responses", "anthropic_messages", "unknown_protocol"} {
		for _, profileOutput := range []int64{0, 20} {
			name := "zero_profile"
			if profileOutput > 0 {
				name = "zero_request"
			}
			t.Run(protocol+"/"+name, func(t *testing.T) {
				f := integration(t)
				in := retrievalBillingInput(t, f, protocol, 0, func(profile map[string]any) {
					profile["output_limit"], profile["default_output_limit"] = profileOutput, profileOutput
					if profileOutput > 0 {
						profile["output_limit_field"] = "max_tokens"
						if protocol == "openai_responses" {
							profile["output_limit_field"] = "max_output_tokens"
						}
					}
				})
				_, err := f.store.Reserve(f.ctx, in)
				assertCode(t, err, "invalid_bounds")
				retrievalBillingNoRequest(t, f, in)
			})
		}
	}
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		for _, boundary := range []string{"profile_output", "default_output", "output_field", "tools", "parallel_tools", "requested_output", "input_bound"} {
			t.Run(protocol+"/"+boundary, func(t *testing.T) {
				f := integration(t)
				in := retrievalBillingInput(t, f, protocol, 0, func(profile map[string]any) {
					switch boundary {
					case "profile_output":
						profile["output_limit"] = int64(1)
					case "default_output":
						profile["default_output_limit"] = int64(1)
					case "output_field":
						profile["output_limit_field"] = "max_tokens"
					case "tools":
						profile["supports_tools"] = true
					case "parallel_tools":
						profile["supports_parallel_tool_calls"] = true
					}
				})
				if boundary == "requested_output" {
					in.OutputLimit = 1
				} else if boundary == "input_bound" {
					// A caller cannot substitute a per-item/smaller bound for the
					// published aggregate reservation contract.
					in.InputUpperBound = 1
				}
				_, err := f.store.Reserve(f.ctx, in)
				assertCode(t, err, "invalid_bounds")
				retrievalBillingNoRequest(t, f, in)
			})
		}
	}
}

func TestPostgresRetrievalNonzeroOutputPriceLeavesNoFinancialFacts(t *testing.T) {
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f, in := retrievalLegacyPriceFixture(t, protocol)
			// The legacy row survives a real 001–017 -> current upgrade, while
			// new malformed prices are rejected by 018 before they can exist.
			id := uuid.NewString()
			_, err := f.pool.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,1,1,clock_timestamp(),$3)`, id, in.ModelID, f.operator)
			var pgerr *pgconn.PgError
			if !errors.As(err, &pgerr) || pgerr.Code != "P0001" || !strings.Contains(pgerr.Where, "guard_group_price_insert") || pgerr.Message != "input-only price requires zero output rate" {
				t.Fatal("new nonzero input-only price was not rejected by 018 guard", err)
			}
			var legacyRows, attemptedRows int
			if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE model_id=$1 AND output_rate=1),count(*) FILTER (WHERE id=$2) FROM price_versions`, in.ModelID, id).Scan(&legacyRows, &attemptedRows); err != nil || legacyRows != 1 || attemptedRows != 0 {
				t.Fatal("upgrade changed the legacy price or retained the rejected insert", legacyRows, attemptedRows, err)
			}
			for i := 0; i < 2; i++ {
				_, err := f.store.Reserve(f.ctx, in)
				assertCode(t, err, "invalid_price")
				retrievalBillingNoRequest(t, f, in)
			}
		})
	}
}

func TestPostgresRetrievalUsageBeyondAggregateBoundCapsAndBlocksProfile(t *testing.T) {
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f := integration(t)
			in := retrievalBillingInput(t, f, protocol, 0, nil)
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil || r.Amount != 100 {
				t.Fatal(r, err)
			}
			defer f.store.EndRequest(r.RequestID)
			retrievalBillingFinal(t, f, r, 150)
			v, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || v.Status != "settled" || v.ChargedAmount != 100 || v.RawRetailAmount != "150" || v.WaivedRetailAmount != "50" {
				t.Fatal("retrieval over-bound usage was not capped", v, err)
			}
			var blocks, audits int
			if err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM billing_profile_blocks WHERE model_id=$1 AND profile_version=$2 AND request_id=$3),
 (SELECT count(*) FROM audit_events WHERE action='billing.profile_blocked' AND target=$1 AND request_id=$3)`, in.ModelID, in.ProfileVersion, r.RequestID).Scan(&blocks, &audits); err != nil || blocks != 1 || audits != 1 {
				t.Fatal("over-bound retrieval profile was not isolated and audited", blocks, audits, err)
			}
			f.wallet(t, 900, 0)
			retrievalBillingKey(t, f, 100, 0)
			f.check(t)
			next := in
			next.RequestID, next.IdempotencyKey = uuid.NewString(), uuid.NewString()
			_, err = f.store.Reserve(f.ctx, next)
			assertCode(t, err, "profile_blocked")
			var count int
			if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM request_records WHERE id=$1", next.RequestID).Scan(&count); err != nil || count != 0 {
				t.Fatal("blocked retrieval profile accepted another reservation", count, err)
			}
			f.wallet(t, 900, 0)
			retrievalBillingKey(t, f, 100, 0)
			f.check(t)
		})
	}
}
