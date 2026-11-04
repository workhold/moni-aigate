package procurementattemptbirth_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

type abDeadlineState struct {
	f          *fixture
	account    receipt
	old        billing.Reservation
	oldAttempt attemptReceipt
}

func abDeadlineBaseline(t *testing.T) abDeadlineState {
	t.Helper()
	f := newFixture(t)
	s := abDeadlineState{f: f, account: f.create(t, 5000)}
	f.bind(t, s.account.TargetID)
	commander, err := periodicbudget.NewCommander(f.app)
	must(t, "construct genuine supplier Commander", err)
	_, err = commander.ConfigureProcurement(f.ctx, f.admin, s.account.TargetID, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "5000"})
	must(t, "commit genuine supplier budget before deadline experiment", err)
	s.old = f.reserve(t)
	s.oldAttempt, err = f.attempt(s.old.RequestID)
	must(t, "commit genuine previous managed attempt", err)
	abDeadlineReceipt(t, s.oldAttempt, s.old.RequestID)
	s.validateOld(t, "240", 2)
	return s
}

func abDeadlineReceipt(t *testing.T, a attemptReceipt, request string) {
	t.Helper()
	if a.Outcome != "applied" || a.Root == nil || a.Source == nil || *a.Source != a.Attempt || a.Request != request || a.Sequence != 1 || a.Delivery != "prepared" || a.Cost != "unknown" {
		t.Fatalf("not a genuine committed managed birth: %+v", a)
	}
}

func (s abDeadlineState) validateOld(t *testing.T, held string, ordinal int64) {
	t.Helper()
	f := s.f
	_, err := f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4),pp_validate_root($5)", s.old.RequestID, s.oldAttempt.Attempt, s.account.TargetID, *s.oldAttempt.Root, s.account.RootID)
	must(t, "actual APP validates preserved command/request/attempt/account prefix", err)
	var actualHeld, spent string
	var actualOrdinal int64
	must(t, "independent committed account prefix amounts and ordinal", f.owner.QueryRow(f.ctx, "SELECT a.held_amount::text,a.spent_amount::text,h.ordinal FROM procurement_accounts a JOIN pp_account_heads h ON h.account_id=a.id WHERE a.id=$1", s.account.TargetID).Scan(&actualHeld, &spent, &actualOrdinal))
	if actualHeld != held || spent != "0" || actualOrdinal != ordinal {
		t.Fatalf("account prefix held=%s spent=%s ordinal=%d; want %s/0/%d", actualHeld, spent, actualOrdinal, held, ordinal)
	}
}

func (s abDeadlineState) restored(t *testing.T) {
	t.Helper()
	r := s.f.reserve(t)
	if r.Amount != 120 {
		t.Fatalf("restored genuine Reserve amount=%d", r.Amount)
	}
	a, err := s.f.attempt(r.RequestID)
	must(t, "restored genuine full root COMMIT with new request", err)
	abDeadlineReceipt(t, a, r.RequestID)
	_, err = s.f.app.Exec(s.f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_root($3)", r.RequestID, a.Attempt, *a.Root)
	must(t, "APP validates restored newly committed prefix", err)
	s.validateOld(t, "480", 3)
}

type abDeadlineBlocker struct {
	f        *fixture
	tx       pgx.Tx
	pid      int32
	released bool
}

func abDeadlineBlock(t *testing.T, f *fixture, query string, args ...any) *abDeadlineBlocker {
	t.Helper()
	tx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	must(t, "begin independent actual lock-only blocker", err)
	b := &abDeadlineBlocker{f: f, tx: tx}
	t.Cleanup(func() {
		if !b.released {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := b.tx.Rollback(ctx); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
				t.Errorf("rollback lock-only blocker cleanup: %v", e)
			}
			b.released = true
		}
	})
	var id string
	must(t, "hold exact genuine parent row without mutation", tx.QueryRow(f.ctx, query, args...).Scan(&id))
	must(t, "identify exact blocker PID", tx.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&b.pid))
	return b
}

func (b *abDeadlineBlocker) release(t *testing.T) {
	t.Helper()
	must(t, "release actual lock-only blocker", b.tx.Rollback(b.f.ctx))
	b.released = true
}

type abDeadlineResult struct {
	receipt attemptReceipt
	err     error
}

type abDeadlineRun struct {
	f        *fixture
	conn     *pgxpool.Conn
	tx       pgx.Tx
	pid      int32
	cancel   context.CancelFunc
	done     chan struct{}
	result   chan abDeadlineResult
	released bool
}

func abDeadlineStart(t *testing.T, f *fixture, request string) *abDeadlineRun {
	t.Helper()
	conn, err := f.app.Acquire(f.ctx)
	must(t, "acquire independent actual APP connection", err)
	r := &abDeadlineRun{f: f, conn: conn}
	t.Cleanup(func() {
		if r.released {
			return
		}
		if r.cancel != nil {
			r.cancel()
		}
		if r.done != nil {
			select {
			case <-r.done:
			case <-time.After(3 * time.Second):
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = r.conn.Conn().PgConn().Close(ctx)
				cancel()
				select {
				case <-r.done:
				case <-time.After(2 * time.Second):
					t.Error("APP root goroutine did not join after connection close")
				}
			}
		}
		if r.tx != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = r.tx.Rollback(ctx)
			cancel()
		}
		r.conn.Release()
		r.released = true
	})
	r.tx, err = conn.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	must(t, "begin original READ COMMITTED APP outer transaction", err)
	var session, current, isolation, lockLimit, statementLimit string
	must(t, "prove actual APP identity and unchanged server budgets", r.tx.QueryRow(f.ctx, `SELECT pg_backend_pid(),session_user,current_user,current_setting('transaction_isolation'),current_setting('lock_timeout'),current_setting('statement_timeout')`).Scan(&r.pid, &session, &current, &isolation, &lockLimit, &statementLimit))
	if session != f.b.App || current != f.b.App || isolation != "read committed" || lockLimit != "3s" || statementLimit != "5s" {
		t.Fatalf("wrong real APP root session: %s/%s %s lock=%s statement=%s", session, current, isolation, lockLimit, statementLimit)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	r.cancel, r.done, r.result = cancel, make(chan struct{}), make(chan abDeadlineResult, 1)
	go func() {
		defer close(r.done)
		var raw []byte
		got := abDeadlineResult{}
		got.err = r.tx.QueryRow(ctx, `SELECT to_jsonb(x) FROM pp_record_attempt($1,$2,1,$3,$4,$5,'upstream','[]',true,$6,2000000,2000000,$7) x`, f.tenant, request, f.channel, f.version, f.credential, []byte(f.cost), []byte("contract")).Scan(&raw)
		if got.err == nil {
			got.err = json.Unmarshal(raw, &got.receipt)
		}
		r.result <- got
	}()
	return r
}

func (r *abDeadlineRun) take(t *testing.T) abDeadlineResult {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(7 * time.Second):
		t.Fatal("actual APP root did not end within its unchanged server budget")
	}
	got := <-r.result
	must(t, "explicitly rollback failed APP root", r.tx.Rollback(r.f.ctx))
	r.cancel()
	r.conn.Release()
	r.released = true
	return got
}

type abDeadlineObservation struct {
	start time.Time
	now   time.Time
}

func abDeadlineWaitBlocked(t *testing.T, f *fixture, r *abDeadlineRun, blocker int32) abDeadlineObservation {
	t.Helper()
	limit := time.NewTimer(time.Second)
	defer limit.Stop()
	tick := time.NewTimer(0)
	defer tick.Stop()
	for {
		select {
		case <-r.done:
			got := r.take(t)
			t.Fatalf("root ended before exact APP/blocker wait was observed: %v", got.err)
		case <-limit.C:
			t.Fatalf("APP PID %d never waited for exact blocker PID %d", r.pid, blocker)
		case <-tick.C:
			var observation abDeadlineObservation
			err := f.owner.QueryRow(f.ctx, `SELECT query_start,clock_timestamp() FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND usename=$2 AND wait_event_type='Lock' AND $3::integer=ANY(pg_catalog.pg_blocking_pids(pid))`, r.pid, f.b.App, blocker).Scan(&observation.start, &observation.now)
			if err == nil {
				return observation
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				must(t, "observe exact actual APP/blocker PID pair", err)
			}
			tick.Reset(10 * time.Millisecond)
		}
	}
}

func abDeadlineWaitCut(t *testing.T, f *fixture, r *abDeadlineRun, cut time.Time) time.Time {
	t.Helper()
	limit := time.NewTimer(2800 * time.Millisecond)
	defer limit.Stop()
	tick := time.NewTimer(0)
	defer tick.Stop()
	for {
		select {
		case <-r.done:
			got := r.take(t)
			t.Fatalf("root ended before the required database clock cut: %v", got.err)
		case <-limit.C:
			t.Fatal("database clock cut not observed within the fixed real lock window")
		case <-tick.C:
			var now time.Time
			must(t, "observe real database clock without altering it", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now))
			if !now.Before(cut) {
				return now
			}
			tick.Reset(10 * time.Millisecond)
		}
	}
}

func abDeadlineError(t *testing.T, err error, code, message string) {
	t.Helper()
	var actual *pgconn.PgError
	if !errors.As(err, &actual) || actual.Code != code || actual.Message != message {
		t.Fatalf("want native PostgreSQL %s / %s; got %v", code, message, err)
	}
}

func TestPostgresA4AccountWaitNativeFailureRollsBackAndRestores(t *testing.T) {
	for _, kind := range []string{"lock_timeout", "cancel_backend"} {
		t.Run(kind, func(t *testing.T) {
			s := abDeadlineBaseline(t)
			f := s.f
			target := f.reserve(t)
			before := f.snapshot(t)
			blocker := abDeadlineBlock(t, f, "SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE", s.account.TargetID)
			run := abDeadlineStart(t, f, target.RequestID)
			abDeadlineWaitBlocked(t, f, run, blocker.pid)
			code, message := "55P03", "canceling statement due to lock timeout"
			if kind == "cancel_backend" {
				var canceled bool
				must(t, "cancel only the known actually blocked APP backend", f.owner.QueryRow(f.ctx, `SELECT pg_catalog.pg_cancel_backend(pid) FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND usename=$2 AND wait_event_type='Lock' AND $3::integer=ANY(pg_catalog.pg_blocking_pids(pid))`, run.pid, f.b.App, blocker.pid).Scan(&canceled))
				if !canceled {
					t.Fatal("PostgreSQL did not accept cancellation of exact APP backend")
				}
				code, message = "57014", "canceling statement due to user request"
			}
			got := run.take(t)
			abDeadlineError(t, got.err, code, message)
			blocker.release(t)
			if f.snapshot(t) != before {
				t.Fatal("native account wait failure changed the complete 34-table baseline")
			}
			s.validateOld(t, "240", 2)
			s.restored(t)
		})
	}
}

func TestPostgresA4AccountWaitExpiryRechecksAndRollsBack(t *testing.T) {
	for _, kind := range []string{"key", "request"} {
		t.Run(kind, func(t *testing.T) {
			s := abDeadlineBaseline(t)
			f := s.f
			blocker := abDeadlineBlock(t, f, "SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE", s.account.TargetID)
			var cut time.Time
			must(t, "sample authentic future expiry from DB clock", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()+interval '1800 milliseconds'").Scan(&cut))
			if kind == "key" {
				_, err := f.app.Exec(f.ctx, "UPDATE api_keys SET expires_at=$2 WHERE id=$1", f.key, cut)
				must(t, "APP configures original mutable Key expiry before Reserve", err)
			}
			var requestCut time.Time
			must(t, "sample original Reserve default request horizon", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()+interval '5 minutes'").Scan(&requestCut))
			if kind == "request" {
				requestCut = cut
			}
			target, err := billing.New(f.app).Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "a4-real-expiry", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: requestCut})
			must(t, "genuine original APP Reserve with immutable original deadline", err)
			var authentic bool
			must(t, "independent immutable request and real financial horizon", f.owner.QueryRow(f.ctx, `SELECT r.request_deadline=$2::timestamptz AND v.deadline=r.retail_priced_at+interval '24 hours' AND r.request_deadline<v.deadline FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=$1`, target.RequestID, requestCut).Scan(&authentic))
			if !authentic {
				t.Fatal("target did not retain genuine original request/24h financial deadlines")
			}
			before := f.snapshot(t)
			run := abDeadlineStart(t, f, target.RequestID)
			observed := abDeadlineWaitBlocked(t, f, run, blocker.pid)
			if !observed.now.Before(cut) {
				t.Fatal("target did not reach genuine account wait before expiry")
			}
			abDeadlineWaitCut(t, f, run, cut)
			blocker.release(t)
			got := run.take(t)
			code, message := "PP409", "request deadline expired while waiting for procurement"
			if kind == "key" {
				code, message = "PP103", "key is not active"
			}
			abDeadlineError(t, got.err, code, message)
			if f.snapshot(t) != before {
				t.Fatal("post-account-wait expiry left partial source, procurement, PB or customer finances")
			}
			s.validateOld(t, "240", 2)
			if kind == "key" {
				_, err = f.app.Exec(f.ctx, "UPDATE api_keys SET expires_at=NULL WHERE id=$1", f.key)
				must(t, "APP restores mutable Key configuration only after rollback comparison", err)
			}
			s.restored(t)
		})
	}
}

func TestPostgresA4SingleRootStatementTimeoutAccumulatesTwoActualWaits(t *testing.T) {
	s := abDeadlineBaseline(t)
	f := s.f
	target := f.reserve(t)
	before := f.snapshot(t)
	tenant := abDeadlineBlock(t, f, "SELECT id FROM tenants WHERE id=$1 FOR UPDATE", f.tenant)
	account := abDeadlineBlock(t, f, "SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE", s.account.TargetID)
	run := abDeadlineStart(t, f, target.RequestID)
	first := abDeadlineWaitBlocked(t, f, run, tenant.pid)
	abDeadlineWaitCut(t, f, run, first.start.Add(2500*time.Millisecond))
	var releaseLower time.Time
	must(t, "sample DB lower bound before first blocker release", tenant.tx.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&releaseLower))
	if releaseLower.Before(first.start.Add(2500*time.Millisecond)) || !releaseLower.Before(first.start.Add(2800*time.Millisecond)) {
		t.Fatal("first real wait missed its 2.5-2.8s release window below the 3s lock limit")
	}
	tenant.release(t)
	second := abDeadlineWaitBlocked(t, f, run, account.pid)
	if !second.start.Equal(first.start) || !second.now.After(releaseLower) {
		t.Fatal("two exact actual waits did not belong to the same frontend root statement")
	}
	got := run.take(t)
	abDeadlineError(t, got.err, "57014", "canceling statement due to statement timeout")
	var ended time.Time
	must(t, "observe server cumulative timeout completion from DB clock", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&ended))
	if ended.Before(first.start.Add(5*time.Second)) || !ended.Before(releaseLower.Add(3*time.Second)) {
		t.Fatalf("missing cumulative 5s / second-lock-under-3s proof: start=%s first-release-lower=%s end=%s", first.start, releaseLower, ended)
	}
	account.release(t)
	if f.snapshot(t) != before {
		t.Fatal("single root cumulative timeout changed the complete 34-table baseline")
	}
	s.validateOld(t, "240", 2)
	s.restored(t)
}
