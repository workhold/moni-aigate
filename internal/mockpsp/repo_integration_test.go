package mockpsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

// This fixture goes through the production manifest/catalog/ownership proof.
// Its offline installer also creates a nonempty application funding baseline.
func newRepositoryPG(t *testing.T) (*Repository, *paymentsandboxqa.Environment, *paymentruntime.Runtime, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	root, e := filepath.Abs(t.TempDir())
	if e != nil {
		t.Fatal("private fixture directory unavailable")
	}
	fixture, e := paymentsandboxqa.Create(ctx, dsn, root)
	if e != nil {
		t.Fatalf("offline fixture: %v", e)
	}
	var rt *paymentruntime.Runtime
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if rt != nil {
			if e := rt.Close(cleanup); e != nil {
				t.Error("runtime cleanup failed")
			}
		}
		if e := fixture.VerifyFundsUnchanged(cleanup); e != nil {
			t.Error("application funding changed")
		}
		if e := fixture.Close(cleanup); e != nil {
			t.Error("fixture cleanup failed")
		}
	})
	cfg, e := paymentruntime.Load(ctx, fixture.MockManifestPath, paymentruntime.Mock)
	if e != nil {
		t.Fatal("mock manifest rejected")
	}
	rt, e = paymentruntime.Open(ctx, cfg)
	if e != nil {
		t.Fatal("mock attestation rejected")
	}
	r, e := newRepository(rt)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	return r, fixture, rt, ctx
}

func repositoryPGCheckout(t *testing.T, r *Repository, ctx context.Context, amount string) CanonicalCheckout {
	t.Helper()
	var now time.Time
	if e := r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		t.Fatal("database clock unavailable")
	}
	return repositoryPGCheckoutAt(t, r, amount, now.Add(-time.Second).UTC().Truncate(time.Microsecond))
}
func repositoryPGCheckoutAt(t *testing.T, r *Repository, amount string, created time.Time) CanonicalCheckout {
	t.Helper()
	order, e := paymentproto.MerchantOrderID(r.env.ID, uuid.NewString())
	if e != nil {
		t.Fatal("order identity unavailable")
	}
	raw, e := paymentproto.CanonicalCheckout(paymentproto.CheckoutRequest{SchemaVersion: 1, EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, CommandID: uuid.NewString(), MerchantOrderID: order, Amount: amount, Currency: "CNY", CreatedAt: timeText(created), ExpiresAt: timeText(created.Add(15 * time.Minute))})
	if e != nil {
		t.Fatal("checkout fixture invalid")
	}
	in, e := ParseCheckoutInput(r.env, raw)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	return in
}
func repositoryPGControl(t *testing.T, r *Repository, ctx context.Context, in ControlInput) ControlOutcome {
	t.Helper()
	if in.OperationID == "" {
		in.OperationID = uuid.NewString()
	}
	raw, e := json.Marshal(in)
	if e != nil {
		t.Fatal("control fixture invalid")
	}
	parsed, e := ParseControlInput(r.env, raw)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	out, e := r.Control(ctx, parsed)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	return out
}
func repositoryPGCancel(t *testing.T, r *Repository, in CanonicalCheckout) CanonicalCancel {
	t.Helper()
	raw, e := paymentproto.CanonicalCancel(paymentproto.CancelRequest{SchemaVersion: 1, EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, CommandID: uuid.NewString(), MerchantOrderID: in.request.MerchantOrderID})
	if e != nil {
		t.Fatal("cancel fixture invalid")
	}
	p, e := ParseCancelInput(r.env, raw)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	return p
}
func repositoryPGQuery(t *testing.T, r *Repository, in CanonicalCheckout) CanonicalQuery {
	t.Helper()
	raw, e := paymentproto.CanonicalQuery(paymentproto.QueryRequest{SchemaVersion: 1, EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, QueryID: uuid.NewString(), CommandID: in.request.CommandID, MerchantOrderID: in.request.MerchantOrderID, CommandKind: "checkout", ParameterDigest: in.digest})
	if e != nil {
		t.Fatal("query fixture invalid")
	}
	p, e := ParseQueryInput(r.env, raw)
	if e != nil {
		t.Fatal(ErrorCode(e))
	}
	return p
}

func TestRepositoryPersistencePG(t *testing.T) {
	r, _, _, ctx := newRepositoryPG(t)
	t.Run("hundred_same_command_and_cross_order_conflicts", func(t *testing.T) {
		in := repositoryPGCheckout(t, r, ctx, "100")
		var wg sync.WaitGroup
		outcomes := make([]CommandOutcome, 100)
		errs := make([]error, 100)
		for i := range outcomes {
			wg.Add(1)
			go func(i int) { defer wg.Done(); outcomes[i], errs[i] = r.Checkout(ctx, in) }(i)
		}
		wg.Wait()
		for i, e := range errs {
			if e != nil {
				t.Fatalf("concurrent command %d: %s", i, ErrorCode(e))
			}
			if !bytes.Equal(outcomes[0].Body.raw, outcomes[i].Body.raw) {
				t.Fatal("same command changed receipt bytes")
			}
		}
		if outcomes[0].Body.request.Result.Status != "ready" {
			t.Fatal("checkout did not create ready order")
		}
		var commands, orders, transitions, finals int
		e := r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mock_commands WHERE command_id=$1),(SELECT count(*) FROM mock_orders WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_order_transitions WHERE merchant_order_id=$2),(SELECT count(*) FROM mock_command_results WHERE command_id=$1)`, in.request.CommandID, in.request.MerchantOrderID).Scan(&commands, &orders, &transitions, &finals)
		if e != nil || commands != 1 || orders != 1 || transitions != 1 || finals != 1 {
			t.Fatal("concurrent command facts are not unique")
		}
		changed := in.request
		changed.Amount = "101"
		raw, _ := paymentproto.CanonicalCheckout(changed)
		other, _ := ParseCheckoutInput(r.env, raw)
		if _, e = r.Checkout(ctx, other); ErrorCode(e) != "command_parameter_conflict" {
			t.Fatal("amount conflict missing")
		}
		changed = in.request
		changed.MerchantOrderID, _ = paymentproto.MerchantOrderID(r.env.ID, uuid.NewString())
		raw, _ = paymentproto.CanonicalCheckout(changed)
		other, _ = ParseCheckoutInput(r.env, raw)
		if _, e = r.Checkout(ctx, other); ErrorCode(e) != "command_parameter_conflict" {
			t.Fatal("cross-order conflict missing")
		}
		cancel := repositoryPGCancel(t, r, in)
		cancel.request.CommandID = in.request.CommandID
		raw, _ = paymentproto.CanonicalCancel(cancel.request)
		cancel, _ = ParseCancelInput(r.env, raw)
		if _, e = r.Cancel(ctx, cancel); ErrorCode(e) != "command_parameter_conflict" {
			t.Fatal("cross-kind conflict missing")
		}
	})
	t.Run("expired_and_early_checkout", func(t *testing.T) {
		var now time.Time
		if r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now) != nil {
			t.Fatal("database clock unavailable")
		}
		expired := repositoryPGCheckoutAt(t, r, "100", now.Add(-901*time.Second).UTC().Truncate(time.Microsecond))
		out, e := r.Checkout(ctx, expired)
		if e != nil || out.Body.request.Result == nil || out.Body.request.Result.Status != "rejected" || *out.Body.request.Result.FailureCode != "order_expired" {
			t.Fatal("expired checkout not persisted as rejection", ErrorCode(e))
		}
		var state string
		var revision int64
		var hasTerms bool
		if r.pool.QueryRow(ctx, `SELECT state,revision,amount IS NOT NULL AND checkout_ref IS NOT NULL FROM mock_orders WHERE merchant_order_id=$1`, expired.request.MerchantOrderID).Scan(&state, &revision, &hasTerms) != nil || state != "closed" || revision != 1 || !hasTerms {
			t.Fatal("expired order terms/closed transition missing")
		}
		replayed, e := r.Checkout(ctx, expired)
		if e != nil || !replayed.Replayed || !bytes.Equal(out.Body.raw, replayed.Body.raw) {
			t.Fatal("expired receipt not immutable")
		}
		early := repositoryPGCheckoutAt(t, r, "100", now.Add(time.Minute).UTC().Truncate(time.Microsecond))
		out, e = r.Checkout(ctx, early)
		if e != nil || out.Body.request.Result.Status != "ready" {
			t.Fatal("early checkout must be ready")
		}
		cap := repositoryPGControl(t, r, ctx, ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: strptr(early.request.MerchantOrderID)})
		if cap.Reply.Status != "rejected" || cap.Reply.Reason != "state_conflict" {
			t.Fatal("early capture accepted")
		}
	})
	t.Run("cancel_first_tombstone", func(t *testing.T) {
		in := repositoryPGCheckout(t, r, ctx, "100")
		cancel := repositoryPGCancel(t, r, in)
		out, e := r.Cancel(ctx, cancel)
		if e != nil || out.Body.request.Result.Status != "closed" {
			t.Fatal("cancel-first failed", ErrorCode(e))
		}
		late, e := r.Checkout(ctx, in)
		if e != nil || late.Body.request.Result.Status != "rejected" || *late.Body.request.Result.FailureCode != "order_closed" {
			t.Fatal("tombstone reopened")
		}
		var nullTerms bool
		if r.pool.QueryRow(ctx, `SELECT amount IS NULL AND created_at IS NULL AND expires_at IS NULL AND checkout_ref IS NULL AND revision=1 FROM mock_orders WHERE merchant_order_id=$1`, in.request.MerchantOrderID).Scan(&nullTerms) != nil || !nullTerms {
			t.Fatal("cancel tombstone terms changed")
		}
	})
	t.Run("capture_cancel_compete", func(t *testing.T) {
		for i := 0; i < 6; i++ {
			in := repositoryPGCheckout(t, r, ctx, "100")
			if _, e := r.Checkout(ctx, in); e != nil {
				t.Fatal(ErrorCode(e))
			}
			cancel := repositoryPGCancel(t, r, in)
			control := ControlInput{OperationID: uuid.NewString(), Action: "capture_order", Scenario: "normal", MerchantOrderID: strptr(in.request.MerchantOrderID)}
			raw, _ := json.Marshal(control)
			p, e := ParseControlInput(r.env, raw)
			if e != nil {
				t.Fatal(ErrorCode(e))
			}
			var cap ControlOutcome
			var closed CommandOutcome
			var capErr, cancelErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); cap, capErr = r.Control(ctx, p) }()
			go func() { defer wg.Done(); closed, cancelErr = r.Cancel(ctx, cancel) }()
			wg.Wait()
			if capErr != nil || cancelErr != nil {
				t.Fatal("capture/cancel transaction failed", ErrorCode(capErr), ErrorCode(cancelErr))
			}
			var state string
			var captures int
			if r.pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM mock_captures WHERE merchant_order_id=$1) FROM mock_orders WHERE merchant_order_id=$1`, in.request.MerchantOrderID).Scan(&state, &captures) != nil {
				t.Fatal("order read failed")
			}
			if state == "captured" {
				if cap.Reply.Status != "accepted" || captures != 1 || closed.Body.request.Result.Status != "captured" {
					t.Fatal("capture-first facts inconsistent")
				}
			} else if state == "closed" {
				if cap.Reply.Status != "rejected" || captures != 0 || closed.Body.request.Result.Status != "closed" {
					t.Fatal("cancel-first facts inconsistent")
				}
			} else {
				t.Fatal("neither canonical terminal won")
			}
		}
	})
	t.Run("held_release_and_immutable_queries", func(t *testing.T) {
		in := repositoryPGCheckout(t, r, ctx, "100")
		arm := ControlInput{Action: "arm_fault", Scenario: "hold_checkout_before_effect", CommandID: strptr(in.request.CommandID)}
		if repositoryPGControl(t, r, ctx, arm).Reply.Status != "accepted" {
			t.Fatal("hold arm rejected")
		}
		if repositoryPGControl(t, r, ctx, arm).Reply.Reason != "state_conflict" {
			t.Fatal("second active plan not a persisted refusal")
		}
		held, e := r.Checkout(ctx, in)
		if e != nil || held.Body.request.CommandState != "in_progress" {
			t.Fatal("hold did not persist intent", ErrorCode(e))
		}
		query := repositoryPGQuery(t, r, in)
		old, e := r.Query(ctx, query)
		if e != nil || old.Body.request.CommandState != "in_progress" {
			t.Fatal("held query failed", ErrorCode(e))
		}
		release := repositoryPGControl(t, r, ctx, ControlInput{Action: "release_command", Scenario: "normal", CommandID: strptr(in.request.CommandID)})
		if release.Reply.Status != "accepted" {
			t.Fatal("release failed")
		}
		replay, e := r.Checkout(ctx, in)
		if e != nil || !bytes.Equal(held.Body.raw, replay.Body.raw) {
			t.Fatal("release overwrote original in_progress receipt")
		}
		frozen, e := r.Query(ctx, query)
		if e != nil || !frozen.Replayed || !bytes.Equal(old.Body.raw, frozen.Body.raw) {
			t.Fatal("old held query changed")
		}
		latest := repositoryPGQuery(t, r, in)
		ready, e := r.Query(ctx, latest)
		if e != nil || ready.Body.request.Result == nil || ready.Body.request.Result.Status != "ready" {
			t.Fatal("new query did not observe release", ErrorCode(e))
		}
		signed, e := r.SignQueryOutcome(ctx, latest, ready)
		if e != nil {
			t.Fatal(ErrorCode(e))
		}
		var now time.Time
		_ = r.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
		verified, e := r.keyring.VerifyQuery(r.env, latest.request, now, signed.Headers, signed.Body.raw)
		if e != nil {
			t.Fatal("signed persisted query did not verify")
		}
		canonical, _ := verified.Canonical()
		if !bytes.Equal(canonical, ready.Body.raw) {
			t.Fatal("signing changed snapshot")
		}
		unknown := repositoryPGCheckout(t, r, ctx, "100")
		before := repositoryPGQuery(t, r, unknown)
		notSeen, e := r.Query(ctx, before)
		if e != nil || notSeen.Body.request.CommandState != "not_seen" {
			t.Fatal("not_seen snapshot missing")
		}
		if _, e = r.Checkout(ctx, unknown); e != nil {
			t.Fatal(ErrorCode(e))
		}
		again, e := r.Query(ctx, before)
		if e != nil || !bytes.Equal(notSeen.Body.raw, again.Body.raw) {
			t.Fatal("not_seen rewritten after command arrived")
		}
		changed := before.request
		changed.ParameterDigest = in.digest
		raw, _ := paymentproto.CanonicalQuery(changed)
		bad, _ := ParseQueryInput(r.env, raw)
		if _, e = r.Query(ctx, bad); ErrorCode(e) != "query_parameter_conflict" {
			t.Fatal("query ID parameter conflict missing")
		}
	})
	t.Run("before_after_faults_do_not_revive", func(t *testing.T) {
		for _, scenario := range []string{"before_command_commit_disconnect_once", "after_command_commit_drop_reply_once"} {
			in := repositoryPGCheckout(t, r, ctx, "100")
			arm := repositoryPGControl(t, r, ctx, ControlInput{Action: "arm_fault", Scenario: scenario, CommandID: strptr(in.request.CommandID)})
			if arm.Reply.Status != "accepted" {
				t.Fatal("fault arm failed")
			}
			if _, e := r.Checkout(ctx, in); !errors.Is(e, ErrDisconnect) {
				t.Fatal("fault did not request exact disconnect", ErrorCode(e))
			}
			var count, remaining int
			if r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mock_commands WHERE command_id=$1),(SELECT remaining FROM mock_fault_plans WHERE target_id=$1)`, in.request.CommandID).Scan(&count, &remaining) != nil || remaining != 0 {
				t.Fatal("fault consumption not durable")
			}
			if scenario == "before_command_commit_disconnect_once" && count != 0 || scenario == "after_command_commit_drop_reply_once" && count != 1 {
				t.Fatal("fault crossed wrong commit boundary")
			}
			out, e := r.Checkout(ctx, in)
			if e != nil || out.Body.request.Result.Status != "ready" {
				t.Fatal("original command recovery failed", ErrorCode(e))
			}
		}
		in := repositoryPGCheckout(t, r, ctx, "100")
		q := repositoryPGQuery(t, r, in)
		if repositoryPGControl(t, r, ctx, ControlInput{Action: "arm_fault", Scenario: "after_query_snapshot_commit_drop_reply_once", QueryID: strptr(q.request.QueryID)}).Reply.Status != "accepted" {
			t.Fatal("query fault arm failed")
		}
		if _, e := r.Query(ctx, q); !errors.Is(e, ErrDisconnect) {
			t.Fatal("query fault did not drop")
		}
		out, e := r.Query(ctx, q)
		if e != nil || !out.Replayed || out.Body.request.CommandState != "not_seen" {
			t.Fatal("query snapshot not recovered")
		}
	})
	t.Run("control_event_replay_is_one_fact", func(t *testing.T) {
		in := repositoryPGCheckout(t, r, ctx, "100")
		if _, e := r.Checkout(ctx, in); e != nil {
			t.Fatal(ErrorCode(e))
		}
		if repositoryPGControl(t, r, ctx, ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: strptr(in.request.MerchantOrderID)}).Reply.Status != "accepted" {
			t.Fatal("normal capture failed")
		}
		for _, scenario := range []string{"capture_at_created", "capture_before_created_1us", "capture_before_expiry_1us", "capture_at_expiry", "capture_future_6s", "amount_plus_one", "duplicate_capture_other_transaction", "same_revision_conflict", "refund_unsupported", "dispute_unsupported"} {
			control := ControlInput{OperationID: uuid.NewString(), Action: "emit_test_event", Scenario: scenario, MerchantOrderID: strptr(in.request.MerchantOrderID)}
			first := repositoryPGControl(t, r, ctx, control)
			second := repositoryPGControl(t, r, ctx, control)
			if first.Reply.Status != "accepted" || !second.Replayed || *first.Reply.ResourceID != *second.Reply.ResourceID {
				t.Fatal("event replay changed resource", scenario)
			}
			var count int
			var raw []byte
			if r.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM mock_notification_events WHERE control_operation_id=$1),canonical_body FROM mock_notification_events WHERE id=$2`, control.OperationID, *first.Reply.ResourceID).Scan(&count, &raw) != nil || count != 1 {
				t.Fatal("test event duplicated")
			}
			if _, e := paymentproto.ParseNotification(raw); e != nil {
				t.Fatal("persisted test event invalid")
			}
		}
		var captures int
		var state string
		if r.pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM mock_captures WHERE merchant_order_id=$1) FROM mock_orders WHERE merchant_order_id=$1`, in.request.MerchantOrderID).Scan(&state, &captures) != nil || state != "captured" || captures != 1 {
			t.Fatal("typed event changed canonical capture")
		}
	})
}
