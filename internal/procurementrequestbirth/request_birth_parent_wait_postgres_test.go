package procurementrequestbirth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

type a1ParentReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Observe the expected committed management change while the owner still holds
// its lock. Taking this snapshot after APP finishes could hide partial writes.
func a1ParentSnapshot(t *testing.T, f *fixture, reader a1ParentReader) string {
	t.Helper()
	tables := []string{"request_records", "reservations", "request_idempotency", "api_keys", "ledger_accounts", "journals", "journal_lines", "periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "recovery_jobs", "audit_events", "projection_outbox", "pp_operation_roots", "pp_root_steps", "pp_request_origins", "pp_request_heads", "tenants", "memberships", "users", "models", "tenant_model_grants", "member_model_grants", "price_groups", "tenant_pricing_assignments"}
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		parts = append(parts, "('"+table+"',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb) FROM "+pgx.Identifier{f.b.Schema, table}.Sanitize()+" x))")
	}
	var v string
	must(t, "independent complete parent and business snapshot", reader.QueryRow(f.ctx, "SELECT jsonb_object_agg(k,v)::text FROM (VALUES "+strings.Join(parts, ",")+") actual(k,v)").Scan(&v))
	return v
}

func TestPostgresA1ParentLockWaitRechecksQualification(t *testing.T) {
	for _, kind := range []string{"tenant", "membership", "user", "key_revoked", "key_scopes", "member_scope", "model_enabled", "model_profile"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			var table, where, mutation, restoration, message string
			var args, restoreArgs []any
			wantCode := "forbidden"
			switch kind {
			case "tenant":
				table, where, mutation, restoration = "tenants", "id=$1", "status='suspended'", "status='active'"
				args, message = []any{f.tenant}, "tenant is not active"
			case "membership":
				table, where, mutation, restoration = "memberships", "tenant_id=$1 AND user_id=$2", "status='suspended'", "status='active'"
				args, message = []any{f.tenant, f.user}, "membership is not active"
			case "user":
				table, where, mutation, restoration = "users", "id=$1", "status='suspended'", "status='active'"
				args, message = []any{f.user}, "user is not active"
			case "key_revoked":
				table, where, mutation, restoration = "api_keys", "id=$1", "revoked_at=clock_timestamp()", "revoked_at=NULL"
				args, wantCode, message = []any{f.key}, "invalid_key", "key is not active"
			case "key_scopes":
				table, where, mutation, restoration = "api_keys", "id=$1", "scopes=ARRAY[]::text[]", "scopes=ARRAY[$2]::text[]"
				args, restoreArgs, message = []any{f.key}, []any{f.key, f.model}, "model is not authorized"
			case "member_scope":
				table, where, mutation, restoration = "memberships", "tenant_id=$1 AND user_id=$2", "allow_all_models=false", "allow_all_models=true"
				args, message = []any{f.tenant, f.user}, "model is not authorized"
			case "model_enabled":
				table, where, mutation, restoration = "models", "id=$1", "enabled=false", "enabled=true"
				args, message = []any{f.model}, "model is not authorized"
			case "model_profile":
				table, where, mutation, restoration = "models", "id=$1", "profile_version='v2'", "profile_version='v1'"
				args, wantCode, message = []any{f.model}, "profile_changed", "model profile changed before reservation"
			}
			if restoreArgs == nil {
				restoreArgs = args
			}
			blocker, err := f.owner.Begin(f.ctx)
			must(t, "begin single original parent blocker", err)
			defer blocker.Rollback(f.ctx)
			quoted := pgx.Identifier{table}.Sanitize()
			var present bool
			must(t, "lock exactly the original parent row", blocker.QueryRow(f.ctx, "SELECT true FROM "+quoted+" WHERE "+where+" FOR UPDATE", args...).Scan(&present))
			if !present {
				t.Fatal("parent blocker did not lock its expected row")
			}
			var pid int32
			must(t, "read parent locker backend", blocker.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid))
			in := f.input()
			// In particular, tenant blocking must occur at lockPrincipal rather
			// than request_idempotency's earlier FK KEY SHARE.
			in.IdempotencyKey = ""
			result, cancel := a1StartReserve(f, in)
			defer cancel()
			a1WaitBlocked(t, f, pid, result)
			tag, err := blocker.Exec(f.ctx, "UPDATE "+quoted+" SET "+mutation+" WHERE "+where, args...)
			must(t, "change legitimate management state while parent locked", err)
			if tag.RowsAffected() != 1 {
				t.Fatal("management mutation did not affect exactly its locked row")
			}
			expected := a1ParentSnapshot(t, f, blocker)
			must(t, "publish changed parent and release actual wait", blocker.Commit(f.ctx))
			got := a1TakeResult(t, result)
			var business *billing.Error
			if !errors.As(got.err, &business) || business.Code != wantCode || business.Message != message {
				t.Fatalf("wrong original post-wait qualification result: %v", got.err)
			}
			if a1ParentSnapshot(t, f, f.owner) != expected {
				t.Fatal("denied post-wait request left business writes or changed the committed management state")
			}
			_, err = f.owner.Exec(f.ctx, "UPDATE "+quoted+" SET "+restoration+" WHERE "+where, restoreArgs...)
			must(t, "restore exact legitimate parent configuration", err)
			positive := f.reserve(t)
			if positive.Amount != 120 {
				t.Fatal("restored original parent did not permit the original reserve amount")
			}
			f.checkBirth(t, positive.RequestID)
		})
	}
}

func TestPostgresA1EarlyImmediateConstraintsRejectPartialBirth(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	f.checkBirth(t, reference.RequestID)
	for _, tc := range []struct{ name, mode, state, message string }{
		{"all_original020_first", "ALL", "23514", "periodic source reservation missing"},
		{"A1_origin_before_graph_completion", "zz_pp_rb_origin_complete", "PP902", "procurement_history_invariant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			defer tx.Rollback(f.ctx)
			_, err := tx.Exec(f.ctx, "SET CONSTRAINTS "+tc.mode+" IMMEDIATE")
			must(t, "enable the exact early constraint mode", err)
			in := f.input()
			err = rawInsert(f, tx, in.RequestID, reference.RequestID, "", "")
			rawError(t, err, tc.state, tc.message)
			must(t, "rollback actual early-constraint failure", tx.Rollback(f.ctx))
			if f.snapshot(t) != before {
				t.Fatal("early immediate constraint mode retained a partial raw birth")
			}
			positive := f.reserve(t)
			f.checkBirth(t, positive.RequestID)
		})
	}
}
