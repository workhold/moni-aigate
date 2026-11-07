package mockpsp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/paymentrefundproto"
)

// c1MFixture is kept in this file so the candidate schema cannot accidentally
// become part of newRepositoryPG or the ordinary mock runtime installer.
func applyC1MOverlay(t *testing.T, env interface {
	OpenPool(context.Context, string) (*pgxpool.Pool, error)
}, ctx context.Context, schemaName, runtimeRole string) {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "specs", "022-self-service-payments", "c1-m-mock-schema.sql"))
	if err != nil {
		t.Fatal("read C1-M candidate SQL:", err)
	}
	owner, err := env.OpenPool(ctx, "mock_owner")
	if err != nil {
		t.Fatal("open mock owner:", err)
	}
	defer owner.Close()
	if _, err = owner.Exec(ctx, string(raw)); err != nil {
		t.Fatal("install isolated C1-M overlay:", err)
	}
	schema := pgx.Identifier{schemaName}.Sanitize()
	role := pgx.Identifier{runtimeRole}.Sanitize()
	for _, q := range []string{
		"REVOKE ALL ON " + schema + ".mock_refund_captures," + schema + ".mock_refund_commands," + schema + ".mock_refund_facts," + schema + ".mock_refund_queries," + schema + ".mock_refund_conflicts," + schema + ".mock_refund_controls FROM PUBLIC",
		"GRANT SELECT,INSERT,UPDATE ON " + schema + ".mock_refund_captures," + schema + ".mock_refund_commands," + schema + ".mock_refund_facts," + schema + ".mock_refund_queries," + schema + ".mock_refund_conflicts," + schema + ".mock_refund_controls TO " + role,
		"REVOKE ALL ON FUNCTION " + schema + ".mock_refund_digest(text,bytea)," + schema + ".mock_refund_parameter_digest(bytea)," + schema + ".mock_refund_fact_digest(bytea)," + schema + ".mock_refund_query_digest(bytea) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION " + schema + ".mock_refund_digest(text,bytea)," + schema + ".mock_refund_parameter_digest(bytea)," + schema + ".mock_refund_fact_digest(bytea)," + schema + ".mock_refund_query_digest(bytea) TO " + role,
	} {
		if _, err = owner.Exec(ctx, q); err != nil {
			t.Fatal("configure isolated C1-M overlay:", err)
		}
	}
}

func c1MCaptureBinding(t *testing.T, r *Repository, ctx context.Context, amount string) paymentrefundproto.CaptureBinding {
	t.Helper()
	in := repositoryPGCheckout(t, r, ctx, amount)
	if out, err := r.Checkout(ctx, in); err != nil || out.Body.request.Result == nil || out.Body.request.Result.Status != "ready" {
		t.Fatalf("checkout for C1-M capture failed: %s", ErrorCode(err))
	}
	cap := repositoryPGControl(t, r, ctx, ControlInput{Action: "capture_order", Scenario: "normal", MerchantOrderID: &in.request.MerchantOrderID})
	if cap.Reply.Status != "accepted" {
		t.Fatalf("capture for C1-M rejected: %s", cap.Reply.Reason)
	}
	var transaction, currency, revision string
	var capturedAmount int64
	var occurred time.Time
	if err := r.pool.QueryRow(ctx, `SELECT provider_transaction_id,amount,currency,occurred_at,provider_order_revision FROM mock_captures WHERE merchant_order_id=$1`, in.request.MerchantOrderID).Scan(&transaction, &capturedAmount, &currency, &occurred, &revision); err != nil {
		t.Fatal("read C1-M capture:", err)
	}
	_, orderID, err := paymentproto.ParseMerchantOrderID(in.request.MerchantOrderID)
	if err != nil {
		t.Fatal(err)
	}
	transactionCopy := transaction
	n := paymentproto.Notification{SchemaVersion: 1, EventID: uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, OrderID: orderID, ProviderTransactionID: &transactionCopy, Amount: strconv.FormatInt(capturedAmount, 10), Currency: currency, OccurredAt: timeText(occurred), ProviderOrderRevision: revision}
	raw, err := paymentproto.CanonicalNotification(n)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	h, err := r.keyring.SignNotification(r.env, paymentproto.SignedHeaders{DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, raw)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.keyring.VerifyNotification(r.env, now, h, raw)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := paymentrefundproto.CaptureFromV1Notification(v)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func c1MRefund(t *testing.T, r *Repository, binding paymentrefundproto.CaptureBinding, amount string) (CanonicalRefundCommand, paymentrefundproto.RefundRequest) {
	t.Helper()
	req, err := paymentrefundproto.NewRefundRequest(binding, uuid.NewString(), uuid.NewString(), amount)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := paymentrefundproto.CanonicalRefundRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ParseRefundCommandInput(r.env, raw)
	if err != nil {
		t.Fatal(err)
	}
	return in, req
}

func c1MQuery(t *testing.T, r *Repository, req paymentrefundproto.RefundRequest, queryID, generation string) CanonicalRefundQuery {
	t.Helper()
	q, err := paymentrefundproto.NewQueryRequest(req, queryID, generation)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := paymentrefundproto.CanonicalQueryRequest(q)
	if err != nil {
		t.Fatal(err)
	}
	in, err := ParseRefundQueryInput(r.env, raw)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func c1MControl(t *testing.T, r *Repository, ctx context.Context, in ControlInput) ControlOutcome {
	t.Helper()
	if in.OperationID == "" {
		in.OperationID = uuid.NewString()
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseControlInput(r.env, raw)
	if err != nil {
		t.Fatalf("C1-M control parse: %s", ErrorCode(err))
	}
	out, err := r.Control(ctx, parsed)
	if err != nil {
		t.Fatalf("C1-M control execution: %s", ErrorCode(err))
	}
	return out
}

func c1MRepo(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	r, env, _, ctx := newRepositoryPG(t)
	applyC1MOverlay(t, env, ctx, env.Schemas["mock"], env.Roles["mock_runtime"])
	return r, ctx
}

func TestC1MRefundControlLifecycleAndImmutableQuery(t *testing.T) {
	r, ctx := c1MRepo(t)
	binding := c1MCaptureBinding(t, r, ctx, "100")
	in, req := c1MRefund(t, r, binding, "25")
	started, err := r.Refund(ctx, in)
	if err != nil || started.Body.request.CommandState != "in_progress" {
		t.Fatalf("refund did not persist in_progress: %s", ErrorCode(err))
	}
	q := c1MQuery(t, r, req, uuid.NewString(), "1")
	old, err := r.RefundQuery(ctx, q)
	if err != nil || old.Body.request.CommandState != "in_progress" || old.Body.request.Result != nil {
		t.Fatalf("in_progress query changed state: %s", ErrorCode(err))
	}
	confirmed := c1MControl(t, r, ctx, ControlInput{Action: "refund_confirmed", Scenario: "normal", CommandID: &req.CommandID})
	if confirmed.Reply.Status != "accepted" || confirmed.Reply.ResourceID == nil {
		t.Fatalf("confirmed control rejected: %+v", confirmed.Reply)
	}
	oldReplay, err := r.RefundQuery(ctx, q)
	if err != nil || !oldReplay.Replayed || !bytes.Equal(old.Body.raw, oldReplay.Body.raw) || oldReplay.Body.request.CommandState != "in_progress" {
		t.Fatalf("query snapshot was not immutable: %s", ErrorCode(err))
	}
	newQuery := c1MQuery(t, r, req, uuid.NewString(), "1")
	completed, err := r.RefundQuery(ctx, newQuery)
	if err != nil || completed.Body.request.CommandState != "completed" || completed.Body.request.Result == nil {
		t.Fatalf("completed query missing fact: %s", ErrorCode(err))
	}
	replayed, err := r.Refund(ctx, in)
	if err != nil || !replayed.Replayed || replayed.Body.request.Result == nil || completed.Body.request.Result == nil || replayed.Body.request.Result.FactID != completed.Body.request.Result.FactID {
		t.Fatalf("command replay changed fact: %s", ErrorCode(err))
	}
	duplicate := c1MControl(t, r, ctx, ControlInput{Action: "refund_confirmed", Scenario: "normal", CommandID: &req.CommandID})
	if duplicate.Reply.Status != "accepted" || duplicate.Reply.ResourceID == nil {
		t.Fatalf("duplicate fact notification was not idempotent: %+v", duplicate.Reply)
	}
	var facts, confirmedAmount, noEffect int64
	if err = r.pool.QueryRow(ctx, `SELECT count(*),max(confirmed_amount),max(final_no_effect_amount) FROM mock_refund_facts f JOIN mock_refund_captures c USING(environment_id,merchant_id) WHERE f.command_id=$1`, req.CommandID).Scan(&facts, &confirmedAmount, &noEffect); err != nil || facts != 1 || confirmedAmount != 25 || noEffect != 0 {
		t.Fatalf("fact/cumulative uniqueness failed: facts=%d confirmed=%d no_effect=%d err=%v", facts, confirmedAmount, noEffect, err)
	}

	variant := req
	variant.Amount = "26"
	variantRaw, _ := paymentrefundproto.CanonicalRefundRequest(variant)
	variantIn, _ := ParseRefundCommandInput(r.env, variantRaw)
	if _, err = r.Refund(ctx, variantIn); ErrorCode(err) != "command_parameter_conflict" {
		t.Fatalf("same-command variant was accepted: %s", ErrorCode(err))
	}
	var conflicts int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM mock_refund_conflicts WHERE command_id=$1`, req.CommandID).Scan(&conflicts); err != nil || conflicts == 0 {
		t.Fatalf("command conflict was not recorded: %d %v", conflicts, err)
	}
}

func TestC1MRefundFinalNoEffectAndConcurrentCaptureLimit(t *testing.T) {
	r, ctx := c1MRepo(t)
	binding := c1MCaptureBinding(t, r, ctx, "100")
	first, req1 := c1MRefund(t, r, binding, "60")
	second, req2 := c1MRefund(t, r, binding, "40")
	if _, err := r.Refund(ctx, first); err != nil {
		t.Fatal(ErrorCode(err))
	}
	if _, err := r.Refund(ctx, second); err != nil {
		t.Fatal(ErrorCode(err))
	}
	var wg sync.WaitGroup
	outs := make([]ControlOutcome, 2)
	errs := make([]error, 2)
	for i, id := range []string{req1.CommandID, req2.CommandID} {
		wg.Add(1)
		go func(i int, commandID string) {
			defer wg.Done()
			raw, _ := json.Marshal(ControlInput{OperationID: uuid.NewString(), Action: "refund_confirmed", Scenario: "normal", CommandID: &commandID})
			in, e := ParseControlInput(r.env, raw)
			if e == nil {
				outs[i], errs[i] = r.Control(ctx, in)
			}
		}(i, id)
	}
	wg.Wait()
	accepted := 0
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("concurrent refund control %d: %s", i, ErrorCode(errs[i]))
		}
		if outs[i].Reply.Status == "accepted" {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("exact-fit partial refunds did not both complete: %d", accepted)
	}
	third, req3 := c1MRefund(t, r, binding, "1")
	if _, err := r.Refund(ctx, third); err != nil {
		t.Fatal(ErrorCode(err))
	}
	limited := c1MControl(t, r, ctx, ControlInput{Action: "refund_confirmed", Scenario: "normal", CommandID: &req3.CommandID})
	if limited.Reply.Status != "rejected" || limited.Reply.Reason != "state_conflict" {
		t.Fatalf("capture limit bypassed: %+v", limited.Reply)
	}
	q := c1MQuery(t, r, req3, uuid.NewString(), "1")
	observed, err := r.RefundQuery(ctx, q)
	if err != nil || observed.Body.request.CommandState != "in_progress" || observed.Body.request.Result != nil {
		t.Fatalf("limit rejection created a tombstone: %s", ErrorCode(err))
	}

	binding2 := c1MCaptureBinding(t, r, ctx, "50")
	finalIn, finalReq := c1MRefund(t, r, binding2, "20")
	if _, err := r.Refund(ctx, finalIn); err != nil {
		t.Fatal(ErrorCode(err))
	}
	final := c1MControl(t, r, ctx, ControlInput{Action: "refund_final_no_effect", Scenario: "refund_policy_rejected", CommandID: &finalReq.CommandID})
	if final.Reply.Status != "accepted" {
		t.Fatalf("final_no_effect control rejected: %+v", final.Reply)
	}
	finalReplay, err := r.Refund(ctx, finalIn)
	if err != nil || finalReplay.Body.request.CommandState != "completed" || finalReplay.Body.request.Result == nil || finalReplay.Body.request.Result.Outcome != "final_no_effect" {
		t.Fatalf("final_no_effect was not immutable: %s", ErrorCode(err))
	}
}

func TestC1MNotSeenAndQueryVariantConflict(t *testing.T) {
	r, ctx := c1MRepo(t)
	binding := c1MCaptureBinding(t, r, ctx, "100")
	in, req := c1MRefund(t, r, binding, "10")
	q := c1MQuery(t, r, req, uuid.NewString(), "1")
	first, err := r.RefundQuery(ctx, q)
	if err != nil || first.Body.request.CommandState != "not_seen" || first.Body.request.Result != nil {
		t.Fatalf("not_seen query not frozen: %s", ErrorCode(err))
	}
	if _, err = r.Refund(ctx, in); err != nil {
		t.Fatal(ErrorCode(err))
	}
	replay, err := r.RefundQuery(ctx, q)
	if err != nil || replay.Body.request.CommandState != "not_seen" || !bytes.Equal(first.Body.raw, replay.Body.raw) {
		t.Fatalf("not_seen snapshot changed: %s", ErrorCode(err))
	}
	changed := req
	changedQuery := c1MQuery(t, r, changed, q.request.QueryID, "2")
	if _, err = r.RefundQuery(ctx, changedQuery); ErrorCode(err) != "query_parameter_conflict" {
		t.Fatalf("query variant was accepted: %s", ErrorCode(err))
	}
	var facts int
	if err = r.pool.QueryRow(ctx, `SELECT count(*) FROM mock_refund_facts WHERE command_id=$1`, req.CommandID).Scan(&facts); err != nil || facts != 0 {
		t.Fatalf("not_seen/query conflict generated fact: %d %v", facts, err)
	}
}
