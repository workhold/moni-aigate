package procurementrequestbirth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

type a1WaitResult struct {
	reservation billing.Reservation
	err         error
}

func a1WaitDBCut(t *testing.T, f *fixture, cut time.Time) {
	t.Helper()
	timer := time.NewTimer(0)
	defer timer.Stop()
	deadline := time.NewTimer(2500 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case <-f.ctx.Done():
			t.Fatal("fixture canceled before DB cut")
		case <-deadline.C:
			t.Fatal("DB cut was not reached within bounded wait")
		case <-timer.C:
			var reached bool
			must(t, "observe actual DB cut", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()>=$1::timestamptz", cut).Scan(&reached))
			if reached {
				return
			}
			timer.Reset(10 * time.Millisecond)
		}
	}
}

func a1WaitBlocked(t *testing.T, f *fixture, locker int32, result <-chan a1WaitResult) {
	t.Helper()
	timer := time.NewTimer(0)
	defer timer.Stop()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case got := <-result:
			t.Fatalf("Reserve finished before the required real wallet wait: %v", got.err)
		case <-deadline.C:
			t.Fatal("actual APP did not reach the wallet blocker")
		case <-timer.C:
			var blocked bool
			must(t, "confirm actual APP lock wait", f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_catalog.pg_blocking_pids(pid)))`, f.b.App, locker).Scan(&blocked))
			if blocked {
				return
			}
			timer.Reset(10 * time.Millisecond)
		}
	}
}

func a1WalletBlock(t *testing.T, f *fixture) (pgx.Tx, int32) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin real wallet blocker", err)
	var id string
	must(t, "lock actual AVAILABLE wallet row", tx.QueryRow(f.ctx, "SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE", f.tenant).Scan(&id))
	var pid int32
	must(t, "read actual locker backend", tx.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&pid))
	return tx, pid
}

func a1StartReserve(f *fixture, in billing.ReserveInput) (<-chan a1WaitResult, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	result := make(chan a1WaitResult, 1)
	go func() {
		r, err := billing.New(f.app).Reserve(ctx, in)
		result <- a1WaitResult{r, err}
	}()
	return result, cancel
}

func a1TakeResult(t *testing.T, result <-chan a1WaitResult) a1WaitResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(6 * time.Second):
		t.Fatal("Reserve did not finish after real blocker released")
		return a1WaitResult{}
	}
}

func TestPostgresA1WalletWaitPreservesSelectedRetailQuote(t *testing.T) {
	f := newFixture(t)
	blocker, pid := a1WalletBlock(t, f)
	defer blocker.Rollback(f.ctx)
	var effective time.Time
	newPrice := uuid.NewString()
	_, err := f.owner.Exec(f.ctx, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,2000000,3000000,clock_timestamp()+interval '1500 milliseconds',$3)`, newPrice, f.model, f.operator)
	must(t, "publish real scheduled price", err)
	must(t, "read immutable effective instant", f.owner.QueryRow(f.ctx, "SELECT effective_at FROM price_versions WHERE id=$1", newPrice).Scan(&effective))
	result, cancel := a1StartReserve(f, f.input())
	defer cancel()
	a1WaitBlocked(t, f, pid, result)
	a1WaitDBCut(t, f, effective)
	must(t, "release real wallet lock", blocker.Commit(f.ctx))
	got := a1TakeResult(t, result)
	must(t, "APP Reserve after scheduled publication", got.err)
	if got.reservation.Amount != 120 || got.reservation.InputRate != 1000000 || got.reservation.OutputRate != 1000000 || got.reservation.PriceVersionID == newPrice {
		t.Fatal("wallet wait reselected the already frozen retail quote")
	}
	f.checkBirth(t, got.reservation.RequestID)
	var timing bool
	must(t, "independent original and source timing", f.owner.QueryRow(f.ctx, `SELECT r.retail_priced_at<$2 AND r.pb_admitted_at>=$2 AND o.retail_priced_at=r.retail_priced_at AND o.pb_admitted_at=r.pb_admitted_at AND o.price_version_id=r.price_version_id FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id WHERE r.id=$1`, got.reservation.RequestID, effective).Scan(&timing))
	if !timing {
		t.Fatal("actual selected/admission/source clocks did not straddle the publication")
	}
	var current string
	must(t, "independent current retail quote", f.owner.QueryRow(f.ctx, `SELECT id FROM price_versions WHERE model_id=$1 AND price_group_id IS NULL AND effective_at<=clock_timestamp() ORDER BY effective_at DESC,created_at DESC,id DESC LIMIT 1`, f.model).Scan(&current))
	if current != newPrice {
		t.Fatal("scheduled price did not become the current independent quote")
	}
}

func TestPostgresA1WalletWaitExpiryRollsBackBirthAndFunds(t *testing.T) {
	for _, kind := range []string{"request", "key"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			in := f.input()
			var cut time.Time
			must(t, "sample finite real expiry", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()+interval '1500 milliseconds'").Scan(&cut))
			if kind == "request" {
				in.RequestDeadline = cut
			} else {
				_, err := f.owner.Exec(f.ctx, "UPDATE api_keys SET expires_at=$2 WHERE id=$1", f.key, cut)
				must(t, "configure real Key expiry before request", err)
			}
			before := f.snapshot(t)
			blocker, pid := a1WalletBlock(t, f)
			defer blocker.Rollback(f.ctx)
			result, cancel := a1StartReserve(f, in)
			defer cancel()
			a1WaitBlocked(t, f, pid, result)
			a1WaitDBCut(t, f, cut)
			must(t, "release actual wallet after expiry", blocker.Commit(f.ctx))
			got := a1TakeResult(t, result)
			var business *billing.Error
			wantCode, wantMessage := "request_deadline", "request deadline expired"
			if kind == "key" {
				wantCode, wantMessage = "invalid_key", "key is not active"
			}
			if !errors.As(got.err, &business) || business.Code != wantCode || business.Message != wantMessage {
				t.Fatalf("wrong post-wait original expiry branch: %v", got.err)
			}
			if f.snapshot(t) != before {
				t.Fatal("post-admission expiry left partial birth or customer finances")
			}
		})
	}
}

func TestPostgresA1WalletWaitDatabaseFailureRollsBack(t *testing.T) {
	for _, kind := range []string{"lock_timeout", "cancel_backend"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			before := f.snapshot(t)
			blocker, pid := a1WalletBlock(t, f)
			defer blocker.Rollback(f.ctx)
			result, cancel := a1StartReserve(f, f.input())
			defer cancel()
			a1WaitBlocked(t, f, pid, result)
			wantState, wantMessage := "55P03", "canceling statement due to lock timeout"
			if kind == "cancel_backend" {
				var canceled bool
				must(t, "cancel the unique actually blocked APP backend", f.owner.QueryRow(f.ctx, `SELECT pg_catalog.pg_cancel_backend(pid) FROM pg_catalog.pg_stat_activity WHERE usename=$1 AND $2::integer=ANY(pg_catalog.pg_blocking_pids(pid))`, f.b.App, pid).Scan(&canceled))
				if !canceled {
					t.Fatal("PostgreSQL did not accept cancellation of the blocked APP")
				}
				wantState, wantMessage = "57014", "canceling statement due to user request"
			}
			got := a1TakeResult(t, result)
			var databaseError *pgconn.PgError
			if !errors.As(got.err, &databaseError) || databaseError.Code != wantState || databaseError.Message != wantMessage {
				t.Fatalf("wrong original database wait failure: %v", got.err)
			}
			if f.snapshot(t) != before {
				t.Fatal("known wallet wait failure left birth or financial changes")
			}
			must(t, "release wallet only after the original failure", blocker.Commit(f.ctx))
			positive := f.reserve(t)
			if positive.Amount != 120 {
				t.Fatal("post-failure Reserve did not preserve the original reserve amount")
			}
			f.checkBirth(t, positive.RequestID)
		})
	}
}
