package procurementrequestbirth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func historicalPublishPricePB(ctx context.Context, store *catalog.Store, actor, model string, input, output int64, effective time.Time) (string, error) {
	var schema, digest string
	if err := store.Pool.QueryRow(ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schema, &digest); err != nil {
		return "", err
	}
	result, err := store.PublishDefaultPrice(ctx, catalog.PricingActor{UserID: actor}, catalog.DefaultPriceCommand{OperationID: uuid.NewString(), ModelID: model, SchemaID: schema, SchemaDigest: digest, VariantKey: "default", InputRate: input, OutputRate: output, EffectiveAt: effective.UTC().Truncate(time.Microsecond)})
	return result.Result.ID, err
}

func a1PBWaitSetup(t *testing.T, scope string) (*fixture, string, map[string]string) {
	t.Helper()
	f := newFixture(t)
	admin := birthBudgetBaseline(t, f)
	tenantState, keyState := "none", "none"
	if scope == "tenant" {
		tenantState = "enabled"
	} else if scope == "key" {
		keyState = "enabled"
	} else {
		t.Fatal("unsupported PB wait scope")
	}
	return f, admin, birthBudgetConfigure(t, f, admin, tenantState, keyState)
}

func a1PBWaitRollback(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("release this fixture's PB control blocker: %v", err)
	}
}

func a1PBWaitBlock(t *testing.T, f *fixture, control string) (pgx.Tx, int32) {
	t.Helper()
	if control == "" {
		t.Fatal("the real Commander did not return the blocked control")
	}
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin isolated PB control blocker", err)
	t.Cleanup(func() { a1PBWaitRollback(t, tx) })
	var id string
	must(t, "hold only the configured control row", tx.QueryRow(f.ctx, "SELECT id FROM periodic_budget_controls WHERE id=$1 FOR NO KEY UPDATE", control).Scan(&id))
	if id != control {
		t.Fatal("PB control blocker locked the wrong original")
	}
	var pid int32
	must(t, "read actual PB blocker backend", tx.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid))
	return tx, pid
}

type a1PBWaitOperation struct {
	result   <-chan a1WaitResult
	finished bool
}

func a1PBWaitStart(t *testing.T, f *fixture, blocker pgx.Tx, in billing.ReserveInput) *a1PBWaitOperation {
	t.Helper()
	result, cancel := a1StartReserve(f, in)
	op := &a1PBWaitOperation{result: result}
	t.Cleanup(func() {
		// On any assertion failure, cancel before releasing the row lock, then
		// join the original call. Neither cleanup step replays the reservation.
		cancel()
		a1PBWaitRollback(t, blocker)
		if !op.finished {
			select {
			case <-op.result:
			case <-time.After(5 * time.Second):
				t.Error("canceled PB-wait Reserve did not finish within cleanup budget")
			}
		}
	})
	return op
}

func (op *a1PBWaitOperation) take(t *testing.T) a1WaitResult {
	t.Helper()
	got := a1TakeResult(t, op.result)
	op.finished = true
	return got
}

func a1PBWaitBlocked(t *testing.T, f *fixture, locker int32, op *a1PBWaitOperation) {
	t.Helper()
	const originalControlQuery = `SELECT id,scope FROM periodic_budget_controls
 WHERE (scope='tenant' AND tenant_id=$1) OR (scope='key' AND tenant_id=$1 AND key_id=$2)
 ORDER BY id FOR NO KEY UPDATE`
	normalize := func(sql string) string { return strings.ToLower(strings.Join(strings.Fields(sql), " ")) }
	want := normalize(originalControlQuery)
	timer := time.NewTimer(0)
	defer timer.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-op.result:
			op.finished = true
			t.Fatalf("Reserve finished before the required real PB control wait: %v", got.err)
		case <-f.ctx.Done():
			t.Fatal("fixture canceled before the real PB control wait")
		case <-deadline.C:
			t.Fatal("actual APP did not reach the exact original control query")
		case <-timer.C:
			var count int
			var query string
			must(t, "locate the unique actual APP blocked by this control owner", f.owner.QueryRow(f.ctx, `SELECT count(*),coalesce(min(query),'') FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_catalog.pg_blocking_pids(pid))`, f.b.App, locker).Scan(&count, &query))
			if count > 1 {
				t.Fatal("more than one APP backend is blocked by the control owner")
			}
			if count == 1 {
				if normalize(query) != want {
					t.Fatal("APP is blocked outside the exact original PB control query")
				}
				return
			}
			timer.Reset(10 * time.Millisecond)
		}
	}
}

func TestPostgresA1PBControlWaitPreservesSelectedRetailQuote(t *testing.T) {
	for _, scope := range []string{"tenant", "key"} {
		t.Run(scope, func(t *testing.T) {
			f, admin, controls := a1PBWaitSetup(t, scope)
			var oldPrice string
			must(t, "read independent original retail version", f.owner.QueryRow(f.ctx, `SELECT id FROM price_versions WHERE model_id=$1 AND price_group_id IS NULL AND effective_at<=clock_timestamp() ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.model).Scan(&oldPrice))
			cut := birthBudgetClock(t, f).Add(1500 * time.Millisecond)
			newPrice, err := historicalPublishPricePB(f.ctx, catalog.New(f.app, nil), admin, f.model, 2000000, 3000000, cut)
			must(t, "publish future retail version with the original catalog command", err)
			var effective time.Time
			must(t, "read actual immutable future retail instant", f.owner.QueryRow(f.ctx, "SELECT effective_at FROM price_versions WHERE id=$1", newPrice).Scan(&effective))
			blocker, pid := a1PBWaitBlock(t, f, controls[scope])
			before := birthBudgetClock(t, f)
			op := a1PBWaitStart(t, f, blocker, f.input())
			a1PBWaitBlocked(t, f, pid, op)
			a1WaitDBCut(t, f, effective)
			must(t, "release the actual control only after scheduled price is effective", blocker.Commit(f.ctx))
			got := op.take(t)
			must(t, "actual APP Reserve after PB control wait", got.err)
			after := birthBudgetClock(t, f)
			r := got.reservation
			if r.Amount != 120 || !birthOriginalRates(t, f, r.RequestID, "1000000", "1000000") || r.PriceVersionID != oldPrice || r.PriceVersionID == newPrice {
				t.Fatal("PB control wait reselected or changed the frozen retail quote")
			}
			f.checkBirth(t, r.RequestID)
			var exactTiming bool
			must(t, "independent PB-wait stage clocks and source snapshots", f.owner.QueryRow(f.ctx, `SELECT r.retail_priced_at<$2 AND r.pb_admitted_at>=$2
 AND r.retail_priced_at>=$3 AND r.retail_priced_at<=$4 AND r.pb_admitted_at>=$3 AND r.pb_admitted_at<=$4
 AND o.retail_priced_at=r.retail_priced_at AND o.pb_admitted_at=r.pb_admitted_at AND o.price_version_id=r.price_version_id
 FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id WHERE r.id=$1`, r.RequestID, effective, before, after).Scan(&exactTiming))
			if !exactTiming {
				t.Fatal("real selected/admission/source instants did not straddle the price cut")
			}
			var current string
			must(t, "independent current price after the control wait", f.owner.QueryRow(f.ctx, `SELECT id FROM price_versions WHERE model_id=$1 AND price_group_id IS NULL AND effective_at<=clock_timestamp() ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.model).Scan(&current))
			if current != newPrice {
				t.Fatal("future price did not become the real current quote")
			}
			birthBudgetWallet(t, f, 9880, 120)
			birthBudgetEffects(t, f, r, controls, false)
		})
	}
}

func TestPostgresA1PBControlWaitExpiryRollsBackBirthAndFunds(t *testing.T) {
	for _, scope := range []string{"tenant", "key"} {
		for _, kind := range []string{"request", "key"} {
			t.Run(scope+"_"+kind, func(t *testing.T) {
				f, _, controls := a1PBWaitSetup(t, scope)
				in := f.input()
				cut := birthBudgetClock(t, f).Add(1500 * time.Millisecond)
				if kind == "request" {
					in.RequestDeadline = cut
				} else {
					_, err := f.owner.Exec(f.ctx, "UPDATE api_keys SET expires_at=$2 WHERE id=$1", f.key, cut)
					must(t, "prepare the real finite Key expiry before Reserve", err)
				}
				before := birthBudgetSnapshot(t, f)
				blocker, pid := a1PBWaitBlock(t, f, controls[scope])
				op := a1PBWaitStart(t, f, blocker, in)
				a1PBWaitBlocked(t, f, pid, op)
				a1WaitDBCut(t, f, cut)
				must(t, "release the actual PB control only after real expiry", blocker.Commit(f.ctx))
				got := op.take(t)
				var business *billing.Error
				wantCode, wantMessage := "request_deadline", "request deadline expired"
				if kind == "key" {
					wantCode, wantMessage = "invalid_key", "key is not active"
				}
				if !errors.As(got.err, &business) || business.Code != wantCode || business.Message != wantMessage {
					t.Fatalf("wrong original post-PB-wait expiry branch: %v", got.err)
				}
				if got.reservation.RequestID != "" || birthBudgetSnapshot(t, f) != before {
					t.Fatal("post-PB-control expiry exposed a receipt or left birth/financial/PB effects")
				}
			})
		}
	}
}
