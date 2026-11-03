package procurementattemptbirth_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/routing"
)

// These cases exercise the complete actual-LOGIN root, rather than only the
// isolated A0 helpers. The expected errors and payloads are fixed by the
// original Go input/check/idempotency contract; none comes from the new writer.
type abRuleInput struct {
	tenant, request, sequence, channel, version, credential, model, route      any
	hasExpected, expectedVersion, expectedInput, expectedOutput, expectedBasis any
}

func abRuleBase(f *fixture, request string) abRuleInput {
	return abRuleInput{f.tenant, request, 1, f.channel, f.version, f.credential, "upstream", "[]", true, []byte(f.cost), int64(2000000), int64(2000000), []byte("contract")}
}

func abRuleCall(t *testing.T, f *fixture, in abRuleInput) (attemptReceipt, error) {
	t.Helper()
	var raw []byte
	var got attemptReceipt
	err := f.app.QueryRow(f.ctx, `SELECT to_jsonb(x) FROM pp_record_attempt($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) x`,
		in.tenant, in.request, in.sequence, in.channel, in.version, in.credential, in.model, in.route,
		in.hasExpected, in.expectedVersion, in.expectedInput, in.expectedOutput, in.expectedBasis).Scan(&raw)
	if err != nil {
		return got, err
	}
	return got, json.Unmarshal(raw, &got)
}

func TestPostgresA2AttemptInputAndIgnoredExpectedUnion(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	invalid := []struct {
		name, code string
		change     func(*abRuleInput)
	}{
		{"sequence_zero", "PP101", func(in *abRuleInput) { in.sequence = 0 }},
		{"sequence_three", "PP101", func(in *abRuleInput) { in.sequence = 3 }},
		{"sequence_null", "PP101", func(in *abRuleInput) { in.sequence = nil }},
		{"empty_model", "PP101", func(in *abRuleInput) { in.model = "" }},
		{"null_model", "PP101", func(in *abRuleInput) { in.model = nil }},
		{"model_201_bytes", "PP101", func(in *abRuleInput) { in.model = strings.Repeat("x", 201) }},
		{"model_multibyte_201_bytes", "PP101", func(in *abRuleInput) { in.model = strings.Repeat("界", 67) }},
		{"null_route", "PP101", func(in *abRuleInput) { in.route = nil }},
		{"object_route", "PP101", func(in *abRuleInput) { in.route = "{}" }},
		{"route_before_union", "PP101", func(in *abRuleInput) { in.route = "{}"; in.hasExpected = nil }},
		{"null_tenant", "PP903", func(in *abRuleInput) { in.tenant = nil }},
		{"null_request", "PP903", func(in *abRuleInput) { in.request = nil }},
		{"null_channel", "PP903", func(in *abRuleInput) { in.channel = nil }},
		{"null_version", "PP903", func(in *abRuleInput) { in.version = nil }},
		{"null_credential", "PP903", func(in *abRuleInput) { in.credential = nil }},
		{"null_union_tag", "PP903", func(in *abRuleInput) { in.hasExpected = nil }},
		{"false_tag_with_payload", "PP903", func(in *abRuleInput) { in.hasExpected = false }},
		{"true_tag_null_version", "PP903", func(in *abRuleInput) { in.expectedVersion = nil }},
		{"true_tag_null_basis", "PP903", func(in *abRuleInput) { in.expectedBasis = nil }},
		{"missing_request", "PP190", func(in *abRuleInput) { in.request = uuid.NewString() }},
		{"sequence_two_without_terminal", "PP410", func(in *abRuleInput) { in.sequence = 2 }},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			in := abRuleBase(f, r.RequestID)
			tc.change(&in)
			before := f.snapshot(t)
			_, err := abRuleCall(t, f, in)
			sqlstate(t, err, tc.code)
			if f.snapshot(t) != before {
				t.Fatal("rejected input changed the complete original/source/funds graph")
			}
		})
	}
	accepted := []struct {
		name   string
		change func(*abRuleInput)
	}{
		{"absent_expected", func(in *abRuleInput) {
			in.hasExpected, in.expectedVersion, in.expectedInput, in.expectedOutput, in.expectedBasis = false, nil, nil, nil, nil
		}},
		{"ignored_empty_bytes", func(in *abRuleInput) { in.expectedVersion, in.expectedBasis = []byte{}, []byte{} }},
		{"ignored_non_utf8_and_zero_bytes", func(in *abRuleInput) {
			in.expectedVersion, in.expectedBasis, in.expectedInput, in.expectedOutput = []byte{0xff, 0}, []byte{0, 0xfe}, int64(-1), nil
		}},
		{"ignored_wide_bytes", func(in *abRuleInput) {
			in.expectedVersion, in.expectedBasis = []byte(strings.Repeat("v", 300000)), []byte(strings.Repeat("b", 300000))
		}},
		{"model_exact_200_bytes", func(in *abRuleInput) { in.model = strings.Repeat("x", 200) }},
		{"model_multibyte_200_bytes", func(in *abRuleInput) { in.model = strings.Repeat("界", 66) + "ab" }},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			request := f.reserve(t)
			in := abRuleBase(f, request.RequestID)
			tc.change(&in)
			got, err := abRuleCall(t, f, in)
			must(t, "actual unmanaged birth accepts original unused expected bytes", err)
			if got.Outcome != "applied" || got.Root == nil || got.Source == nil || got.Attempt != *got.Source {
				t.Fatal("new birth did not return its exact source/root")
			}
			_, err = f.app.Exec(f.ctx, "SELECT pp_validate_attempt($1)", got.Attempt)
			must(t, "certify original accepted payload", err)
			_, err = f.app.Exec(f.ctx, "SELECT pp_validate_request($1)", request.RequestID)
			must(t, "certify request after original accepted payload", err)
			before := f.snapshot(t)
			replayed, err := abRuleCall(t, f, in)
			must(t, "unmanaged replay", err)
			if replayed.Outcome != "replay" || replayed.Root != nil || replayed.Source != nil || replayed.Attempt != got.Attempt || f.snapshot(t) != before {
				t.Fatal("replay fabricated provenance or changed the graph")
			}
		})
	}
}

func TestPostgresA2AttemptWideRouteAndReplayFields(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	decisions := make([]routing.Decision, 1000)
	for i := range decisions {
		decisions[i] = routing.Decision{ChannelID: uuid.NewString(), VersionID: uuid.NewString(), Priority: -9223372036854775808, Weight: 18446744073709551615, Reason: "procurement_budget_unavailable"}
	}
	decisions[0].ChannelID, decisions[0].VersionID, decisions[0].Reason = f.channel, f.version, "selected"
	encoded, err := json.Marshal(decisions)
	must(t, "encode exact typed Go route", err)
	in := abRuleBase(f, r.RequestID)
	in.route = string(encoded)
	got, err := abRuleCall(t, f, in)
	must(t, "actual 1000-row wide route birth", err)
	var same bool
	must(t, "compare full frozen route without hashes or truncation", f.owner.QueryRow(f.ctx, `SELECT a.route_decisions=$2::jsonb AND o.route_decisions=a.route_decisions
 AND o.upstream_model='upstream' AND o.management_mode='unmanaged'
 FROM upstream_attempts a JOIN pp_attempt_origins o ON o.attempt_id=a.id WHERE a.id=$1`, got.Attempt, encoded).Scan(&same))
	if !same {
		t.Fatal("wide route was normalized outside JSON semantics, truncated or lost")
	}
	t.Run("route_is_not_a_fifth_replay_key", func(t *testing.T) {
		in.route = "[]"
		before := f.snapshot(t)
		replayed, err := abRuleCall(t, f, in)
		must(t, "original four-field replay with different valid route", err)
		if replayed.Outcome != "replay" || replayed.Attempt != got.Attempt || replayed.Root != nil || replayed.Source != nil || f.snapshot(t) != before {
			t.Fatal("route-only replay changed the original snapshot or added a source")
		}
	})
	t.Run("invalid_route_is_checked_before_replay", func(t *testing.T) {
		in.route = "{}"
		before := f.snapshot(t)
		_, err := abRuleCall(t, f, in)
		sqlstate(t, err, "PP101")
		if f.snapshot(t) != before {
			t.Fatal("invalid replay changed graph")
		}
	})
	t.Run("1001_route_rows_rejected_before_replay", func(t *testing.T) {
		more := append(append([]routing.Decision{}, decisions...), routing.Decision{ChannelID: uuid.NewString(), VersionID: uuid.NewString(), Weight: 1, Reason: "disabled"})
		raw, err := json.Marshal(more)
		must(t, "encode oversized route", err)
		in.route = string(raw)
		before := f.snapshot(t)
		_, err = abRuleCall(t, f, in)
		sqlstate(t, err, "PP101")
		if f.snapshot(t) != before {
			t.Fatal("oversized replay changed graph")
		}
	})
	t.Run("upstream_model_conflicts_after_checks", func(t *testing.T) {
		in.route, in.model = "[]", "different-upstream"
		before := f.snapshot(t)
		_, err := abRuleCall(t, f, in)
		sqlstate(t, err, "PP116")
		if f.snapshot(t) != before {
			t.Fatal("idempotency conflict changed graph")
		}
	})
	_, err = f.app.Exec(f.ctx, "SELECT pp_validate_attempt($1)", got.Attempt)
	must(t, "wide-route committed history remains valid", err)
}

func TestPostgresA2AttemptReplayChecksMatchOriginalErrors(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	in := abRuleBase(f, r.RequestID)
	committed, err := abRuleCall(t, f, in)
	must(t, "commit genuine unmanaged attempt before replay eligibility changes", err)
	// Only rejected original calls are used as an independent error oracle.
	// No owner call is accepted as a positive A2 business identity.
	cases := []struct {
		name, code, mutation string
		invalidCredential    bool
	}{
		{"channel_before_source_and_key", "PP117", "UPDATE channels SET enabled=false WHERE id=$1", false},
		{"source_before_revocation", "PP102", "UPDATE api_keys SET allowed_client_cidrs=ARRAY['10.0.0.0/8'],revoked_at=clock_timestamp() WHERE id=$1", false},
		{"revocation_before_scope", "PP103", "UPDATE api_keys SET revoked_at=clock_timestamp(),scopes=ARRAY['different-model'] WHERE id=$1", false},
		{"expiry_before_scope", "PP103", "UPDATE api_keys SET expires_at=clock_timestamp()-interval '1 second',scopes=ARRAY['different-model'] WHERE id=$1", false},
		{"scope_before_membership", "PP406", "UPDATE api_keys SET scopes=ARRAY['different-model'] WHERE id=$1", true},
		{"credential_before_region", "PP101", "UPDATE api_keys SET allowed_regions=ARRAY['restricted-region'] WHERE id=$1", true},
		{"region_before_replay", "PP407", "UPDATE api_keys SET allowed_regions=ARRAY['restricted-region'] WHERE id=$1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.owner.Exec(f.ctx, "UPDATE channels SET enabled=true,quarantined=false WHERE id=$1", f.channel)
			must(t, "restore original channel policy", err)
			_, err = f.owner.Exec(f.ctx, "UPDATE api_keys SET allowed_client_cidrs='{}',revoked_at=NULL,expires_at=NULL,scopes=$2,allowed_regions=ARRAY['*'] WHERE id=$1", f.key, []string{f.model})
			must(t, "restore original key policy", err)
			target := f.key
			if tc.code == "PP117" {
				target = f.channel
			}
			_, err = f.owner.Exec(f.ctx, tc.mutation, target)
			must(t, "commit legitimate changed eligibility policy", err)
			call := abRuleBase(f, r.RequestID)
			original := billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
			if tc.invalidCredential {
				original.CredentialID = uuid.NewString()
				call.credential = original.CredentialID
			}
			before := f.snapshot(t)
			_, oldErr := billing.New(f.owner).RecordAttempt(f.ctx, original)
			var business *billing.Error
			if !errors.As(oldErr, &business) {
				t.Fatalf("original error oracle reached an unexpected path: %v", oldErr)
			}
			_, err = abRuleCall(t, f, call)
			sqlstate(t, err, tc.code)
			var sqlError *pgconn.PgError
			if !errors.As(err, &sqlError) || sqlError.Message != business.Message {
				t.Fatalf("A2 error order/message differs from original: old=%v new=%v", oldErr, err)
			}
			if f.snapshot(t) != before {
				t.Fatal("rejected original or A2 replay changed the full graph")
			}
		})
	}
	_, err = f.owner.Exec(f.ctx, "UPDATE channels SET enabled=true,quarantined=false WHERE id=$1", f.channel)
	must(t, "restore final channel policy", err)
	_, err = f.owner.Exec(f.ctx, "UPDATE api_keys SET allowed_client_cidrs='{}',revoked_at=NULL,expires_at=NULL,scopes=$2,allowed_regions=ARRAY['*'] WHERE id=$1", f.key, []string{f.model})
	must(t, "restore final key policy", err)
	before := f.snapshot(t)
	replayed, err := abRuleCall(t, f, in)
	must(t, "original replay works after legitimate eligibility restoration", err)
	if replayed.Outcome != "replay" || replayed.Attempt != committed.Attempt || replayed.Root != nil || replayed.Source != nil || f.snapshot(t) != before {
		t.Fatal("restored replay produced a second attempt or procurement source")
	}
}

func TestPostgresA2AttemptManagedQuoteRejectionsAndZeroUpper(t *testing.T) {
	for _, tc := range []struct {
		name, model, basis, code string
		input, output            any
		noExpected, badBytes     bool
	}{
		{"no_quote_before_expected", "no-quote", "", "PP110", nil, nil, true, false},
		{"missing_input_dimension", "upstream", "contract", "PP110", nil, int64(1), false, false},
		{"missing_output_dimension", "upstream", "invoice", "PP110", int64(1), nil, false, false},
		{"unknown_basis", "upstream", "unknown", "PP110", int64(1), int64(1), false, false},
		{"complete_quote_requires_expected", "upstream", "contract", "PP411", int64(2), int64(2), true, false},
		{"complete_quote_rejects_stale", "upstream", "contract", "PP411", int64(2), int64(2), false, false},
		{"consumed_non_utf8_is_compared_as_bytes", "upstream", "contract", "PP411", int64(2), int64(2), false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			a := f.create(t, 1000)
			f.bind(t, a.TargetID)
			if tc.basis != "" {
				_, err := f.owner.Exec(f.ctx, "INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',$3,$4,$5,clock_timestamp()-interval '1 second')", uuid.NewString(), f.channel, tc.input, tc.output, tc.basis)
				must(t, "publish real newer procurement quote", err)
			}
			r := f.reserve(t)
			in := abRuleBase(f, r.RequestID)
			in.model = tc.model
			quote := &routing.CostQuote{VersionID: f.cost, Basis: "contract", InputRate: abRuleRate(2000000), OutputRate: abRuleRate(2000000)}
			if tc.noExpected {
				in.hasExpected, in.expectedVersion, in.expectedInput, in.expectedOutput, in.expectedBasis = false, nil, nil, nil, nil
				quote = nil
			}
			if tc.badBytes {
				in.expectedVersion, in.expectedBasis = []byte{0xff, 0}, []byte{0xfe, 0}
				quote.VersionID, quote.Basis = string([]byte{0xff, 0}), string([]byte{0xfe, 0})
			}
			before := f.snapshot(t)
			_, oldErr := billing.New(f.owner).RecordAttempt(f.ctx, billing.AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: tc.model, Sequence: 1, CostQuote: quote})
			var business *billing.Error
			if !errors.As(oldErr, &business) {
				t.Fatalf("original quote rejection oracle reached unexpected path: %v", oldErr)
			}
			_, err := abRuleCall(t, f, in)
			sqlstate(t, err, tc.code)
			var p *pgconn.PgError
			if !errors.As(err, &p) || p.Message != business.Message || f.snapshot(t) != before {
				t.Fatalf("quote rejection changed original error/graph: original=%v candidate=%v", oldErr, err)
			}
		})
	}
	for _, withPB := range []bool{false, true} {
		name := "zero_upper_without_pb"
		if withPB {
			name = "zero_upper_with_scheduled_pb"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			a := f.create(t, 0)
			f.bind(t, a.TargetID)
			zeroCost := uuid.NewString()
			_, err := f.owner.Exec(f.ctx, "INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',0,0,'contract',clock_timestamp()-interval '1 second')", zeroCost, f.channel)
			must(t, "publish zero procurement dimensions", err)
			if withPB {
				commander, err := periodicbudget.NewCommander(f.app)
				must(t, "construct original supplier PB commander", err)
				_, err = commander.ConfigureProcurement(f.ctx, f.admin, a.TargetID, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "0"})
				// Original configuration starts enforcement at the next UTC month.
				// This proves scheduled-control allocation, not an active cap gate.
				must(t, "configure genuine scheduled zero-cap supplier PB", err)
			}
			r := f.reserve(t)
			in := abRuleBase(f, r.RequestID)
			in.expectedVersion, in.expectedInput, in.expectedOutput = []byte(zeroCost), int64(0), int64(0)
			got, err := abRuleCall(t, f, in)
			must(t, "actual APP zero upper still creates complete managed origin", err)
			var exact bool
			must(t, "independent zero-effect provenance and account oracle", f.owner.QueryRow(f.ctx, `SELECT
 p.upper_amount=0 AND p.held_amount=0 AND p.spent_amount=0 AND p.state='prepared'
 AND e.type='reserved' AND e.held_delta=0 AND e.spent_delta=0
 AND a.limit_amount=0 AND a.held_amount=0 AND a.spent_amount=0
 AND h.ordinal=2 AND h.held_amount=0 AND h.spent_amount=0
 AND j.kind='reserve' AND j.upper_amount=0 AND t.upper_amount=0
 FROM procurement_reservations p JOIN procurement_budget_events e ON e.attempt_id=p.attempt_id
 JOIN procurement_accounts a ON a.id=p.account_id JOIN pp_account_heads h ON h.account_id=a.id
 JOIN pp_account_journal j ON j.id=h.journal_id JOIN pp_procurement_transitions t ON t.id=j.transition_id
 WHERE p.attempt_id=$1`, got.Attempt).Scan(&exact))
			if !exact || got.Outcome != "applied" || got.Root == nil || got.Source == nil {
				t.Fatal("zero amount skipped the mandatory reserved event/history")
			}
			if withPB {
				must(t, "zero PB allocation/event remain real", f.owner.QueryRow(f.ctx, `SELECT count(*)=1 AND bool_and(x.upper_amount=0 AND x.held_amount=0 AND x.spent_amount=0 AND x.origin='new'
 AND EXISTS(SELECT 1 FROM periodic_budget_events e WHERE e.allocation_id=x.id AND e.held_delta=0 AND e.spent_delta=0))
 FROM periodic_budget_allocations x WHERE x.subject_type='supplier_attempt' AND x.subject_id=$1`, got.Attempt).Scan(&exact))
				if !exact {
					t.Fatal("zero PB amount lost allocation or provenance")
				}
			}
			_, err = f.app.Exec(f.ctx, "SELECT pp_validate_attempt($1),pp_validate_account($2),pp_validate_root($3)", got.Attempt, a.TargetID, *got.Root)
			must(t, "zero upper complete committed prefix", err)
		})
	}
}

func abRuleRate(v int64) *int64 { return &v }
