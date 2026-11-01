package periodicbudget_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// This tracer only observes actual PostgreSQL wire results. It never supplies
// an error or changes the command/transaction under test.
type pbCommanderFaultTrace struct {
	begins, deadlocks atomic.Int32
}

func (v *pbCommanderFaultTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, in pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(in.SQL)), "begin") {
		v.begins.Add(1)
	}
	return ctx
}
func (v *pbCommanderFaultTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, out pgx.TraceQueryEndData) {
	var pg *pgconn.PgError
	if errors.As(out.Err, &pg) && pg != nil && pg.Code == "40P01" {
		v.deadlocks.Add(1)
	}
}

func pbCommanderFaultPool(t *testing.T, f *pbTxFixture) (*pgxpool.Pool, *pbCommanderFaultTrace, *atomic.Int32, int32) {
	t.Helper()
	trace, connects := &pbCommanderFaultTrace{}, &atomic.Int32{}
	cfg := f.app.Config().Copy()
	cfg.MaxConns, cfg.MinConns = 1, 0
	cfg.ConnConfig.Tracer = trace
	previous := cfg.AfterConnect
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		if previous != nil {
			if err := previous(ctx, c); err != nil {
				return err
			}
		}
		connects.Add(1)
		return nil
	}
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	pbTxMust(t, "open isolated single-connection APP fault pool", err)
	t.Cleanup(p.Close)
	var pid int32
	pbTxMust(t, "observe fault APP backend identity", p.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	return p, trace, connects, pid
}

type pbCommanderFaultResult struct {
	out ConfigureResult
	err error
}

func pbCommanderFaultStart(t *testing.T, f *pbTxFixture, p *pgxpool.Pool, in PutPeriodicBudget) <-chan pbCommanderFaultResult {
	t.Helper()
	c, err := NewCommander(p)
	pbTxMust(t, "construct actual APP fault Commander", err)
	done := make(chan pbCommanderFaultResult, 1)
	go func() {
		out, err := c.ConfigurePlatformTenant(f.ctx, f.user, f.tenant, in)
		done <- pbCommanderFaultResult{out, err}
	}()
	return done
}

func pbCommanderFaultAwait(t *testing.T, f *pbTxFixture, done <-chan pbCommanderFaultResult) pbCommanderFaultResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-f.ctx.Done():
		t.Fatal("fault command did not finish within its fixture budget")
		return pbCommanderFaultResult{}
	}
}

func pbCommanderFaultWait(t *testing.T, f *pbTxFixture, pid int32, commit bool, blocking int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var ready bool
		var err error
		if blocking != 0 {
			err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE pid=$1 AND state='active' AND wait_event_type='Lock' AND wait_event='advisory'
 AND $2::integer=ANY(pg_blocking_pids(pid)))`, pid, blocking).Scan(&ready)
		} else {
			err = f.owner.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
 WHERE pid=$1 AND state='active' AND wait_event_type='Timeout' AND wait_event='PgSleep'
 AND (NOT $2::boolean OR upper(btrim(query))='COMMIT'))`, pid, commit).Scan(&ready)
		}
		pbTxMust(t, "observe the exact backend fault boundary", err)
		if ready {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("backend did not reach the required observable fault boundary")
		case <-tick.C:
		}
	}
}

func pbCommanderFaultZero(t *testing.T, got pbCommanderFaultResult, code string) {
	t.Helper()
	if got.err == nil || ErrorCode(got.err) != code || got.err.Error() != code || !reflect.DeepEqual(got.out, ConfigureResult{}) {
		t.Fatalf("fault command returned code=%q instead of %q with a zero result", ErrorCode(got.err), code)
	}
}

func pbCommanderFaultDestroyed(t *testing.T, f *pbTxFixture, p *pgxpool.Pool, trace *pbCommanderFaultTrace, connects *atomic.Int32, oldPID int32) {
	t.Helper()
	if trace.begins.Load() != 1 || connects.Load() != 1 || p.Stat().TotalConns() != 0 {
		t.Fatal("disconnected transaction was retried or its connection remained pooled")
	}
	var replacementPID int32
	pbTxMust(t, "obtain a fresh connection after destruction", p.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&replacementPID))
	if connects.Load() != 2 || replacementPID == oldPID {
		t.Fatal("the destroyed backend was reused instead of opening a new APP connection")
	}
}

func TestCommanderFaultPGAuditFailureRollsBackAllFacts(t *testing.T) {
	f := newPBTxFixture(t)
	call := pbCommandCallFor(pbCommander(t, f), f, "tenant")
	for _, phase := range []string{"first_configuration", "existing_configuration"} {
		t.Run(phase, func(t *testing.T) {
			expected := "0"
			if phase == "existing_configuration" {
				_, err := call(f.ctx, pbCommandInput("0", "500"))
				pbTxMust(t, "establish control before atomic audit-failure change", err)
				expected = "1"
			}
			before := pbCommandSnapshot(t, f)
			_, err := f.owner.Exec(f.ctx, "REVOKE INSERT ON audit_events FROM "+pgx.Identifier{f.appRole}.Sanitize())
			pbTxMust(t, "install explicit audit permission failure", err)
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if _, restoreErr := f.owner.Exec(cleanup, "GRANT INSERT ON audit_events TO "+pgx.Identifier{f.appRole}.Sanitize()); restoreErr != nil {
					t.Error("audit permission fault cleanup failed")
				}
			}()
			in := pbCommandInput(expected, "600")
			out, err := call(f.ctx, in)
			pbCommanderFaultZero(t, pbCommanderFaultResult{out, err}, "periodic_budget_unavailable")
			if before != pbCommandSnapshot(t, f) {
				t.Fatal("audit failure retained a control change, receipt, version, source or audit")
			}
		})
	}
	f.verifyFinance(t)
}

func TestCommanderFaultPGBodyDisconnectDestroysWithoutRetry(t *testing.T) {
	f := newPBTxFixture(t)
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pb_commander_body_pause() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_catalog.pg_sleep(30);RETURN NEW;END $$;
 CREATE TRIGGER pb_commander_body_pause BEFORE INSERT ON periodic_budget_operations
 FOR EACH ROW EXECUTE FUNCTION pb_commander_body_pause()`)
	pbTxMust(t, "install explicit body-stage connection fault boundary", err)
	before := pbCommandSnapshot(t, f)
	p, trace, connects, pid := pbCommanderFaultPool(t, f)
	done := pbCommanderFaultStart(t, f, p, pbCommandInput("0", "500"))
	pbCommanderFaultWait(t, f, pid, false, 0)
	var terminated bool
	pbTxMust(t, "terminate actual APP connection during transaction body", f.owner.QueryRow(f.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated))
	if !terminated {
		t.Fatal("body-stage backend termination was not accepted")
	}
	pbCommanderFaultZero(t, pbCommanderFaultAwait(t, f, done), "periodic_budget_unavailable")
	pbCommanderFaultDestroyed(t, f, p, trace, connects, pid)
	if before != pbCommandSnapshot(t, f) {
		t.Fatal("body disconnect retained partially written configuration facts")
	}
	f.verifyFinance(t)
}

func TestCommanderFaultPGDisconnectDuringCommitReturnsUnknown(t *testing.T) {
	f := newPBTxFixture(t)
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pb_commander_commit_pause() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_catalog.pg_sleep(30);RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER pb_commander_commit_pause AFTER INSERT ON periodic_budget_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_commander_commit_pause()`)
	pbTxMust(t, "install explicit deferred COMMIT-stage fault boundary", err)
	before := pbCommandSnapshot(t, f)
	p, trace, connects, pid := pbCommanderFaultPool(t, f)
	done := pbCommanderFaultStart(t, f, p, pbCommandInput("0", "500"))
	pbCommanderFaultWait(t, f, pid, true, 0)
	var terminated bool
	pbTxMust(t, "terminate actual APP connection during COMMIT", f.owner.QueryRow(f.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated))
	if !terminated {
		t.Fatal("COMMIT-stage backend termination was not accepted")
	}
	pbCommanderFaultZero(t, pbCommanderFaultAwait(t, f, done), "periodic_budget_outcome_unknown")
	pbCommanderFaultDestroyed(t, f, p, trace, connects, pid)
	if before != pbCommandSnapshot(t, f) {
		t.Fatal("the interrupted deferred COMMIT unexpectedly retained command facts")
	}
	// The observed server sleep is before actual commit completion. This proves
	// a disconnect during COMMIT, not a successful commit with a lost reply.
	f.verifyFinance(t)
}

func TestCommanderFaultPGRealDeadlockRetryBound(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		deadlocks  int
		wantCommit bool
	}{{"one_deadlock_then_confirmed_commit", 1, true}, {"three_deadlocks_exhaust_attempts", 3, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newPBTxFixture(t)
			// Only this isolated LOGIN receives a shorter detector timeout. The
			// actual wait cycle, rather than RAISE or a mock, produces SQLSTATE 40P01.
			_, err := f.owner.Exec(f.ctx, "ALTER ROLE "+pgx.Identifier{f.appRole}.Sanitize()+" SET deadlock_timeout='50ms'")
			pbTxMust(t, "configure actual APP deadlock detection for the fault fixture", err)
			_, err = f.owner.Exec(f.ctx, `CREATE FUNCTION pb_commander_deadlock_pause() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_advisory_xact_lock(23023025,hashtext(current_schema()));RETURN NEW;END $$;
 CREATE TRIGGER pb_commander_deadlock_pause BEFORE INSERT ON periodic_budget_controls
 FOR EACH ROW EXECUTE FUNCTION pb_commander_deadlock_pause()`)
			pbTxMust(t, "install actual advisory/tenant deadlock boundary", err)
			blocker, err := f.owner.Acquire(f.ctx)
			pbTxMust(t, "acquire explicit owner fault blocker", err)
			advisoryHeld := false
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_, _ = blocker.Exec(cleanup, `ROLLBACK`)
				if advisoryHeld {
					_, _ = blocker.Exec(cleanup, `SELECT pg_advisory_unlock(23023025,hashtext(current_schema()))`)
				}
				blocker.Release()
			}()
			var blockerPID int32
			pbTxMust(t, "observe exact owner blocker identity", blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
			_, err = blocker.Exec(f.ctx, `SELECT pg_advisory_lock(23023025,hashtext(current_schema()))`)
			pbTxMust(t, "hold explicit session advisory half of deadlock", err)
			advisoryHeld = true
			before := pbCommandSnapshot(t, f)
			p, trace, connects, pid := pbCommanderFaultPool(t, f)
			in := pbCommandInput("0", "500")
			done := pbCommanderFaultStart(t, f, p, in)
			for i := 0; i < scenario.deadlocks; i++ {
				pbCommanderFaultWait(t, f, pid, false, blockerPID)
				_, err = blocker.Exec(f.ctx, `BEGIN;SET LOCAL deadlock_timeout='5s'`)
				pbTxMust(t, "begin the explicit opposite lock order", err)
				// Commander owns tenant and waits on our advisory lock. This
				// backend owns advisory and now waits on that same tenant.
				var tenant string
				pbTxMust(t, "complete the actual two-backend deadlock cycle", blocker.QueryRow(f.ctx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, f.tenant).Scan(&tenant))
				if tenant != f.tenant {
					t.Fatal("the blocker acquired an unexpected tenant identity")
				}
				_, err = blocker.Exec(f.ctx, `ROLLBACK`)
				pbTxMust(t, "release only the blocker tenant lock after the deadlock victim rolled back", err)
			}
			if scenario.wantCommit {
				var unlocked bool
				pbTxMust(t, "release advisory blocker for the successful retry", blocker.QueryRow(f.ctx, `SELECT pg_advisory_unlock(23023025,hashtext(current_schema()))`).Scan(&unlocked))
				if !unlocked {
					t.Fatal("the explicit retry blocker lost its advisory lock")
				}
				advisoryHeld = false
			}
			got := pbCommanderFaultAwait(t, f, done)
			if scenario.wantCommit {
				pbTxMust(t, "commit the original operation after one actual deadlock", got.err)
				pbCommandReceipt(t, f, got.out, in, "tenant", "1", true)
				if trace.begins.Load() != 2 {
					t.Fatal("one deadlock did not result in exactly two transaction attempts")
				}
			} else {
				pbCommanderFaultZero(t, got, "periodic_budget_unavailable")
				if trace.begins.Load() != 3 || before != pbCommandSnapshot(t, f) {
					t.Fatal("deadlock exhaustion exceeded three attempts or retained partial facts")
				}
			}
			if trace.deadlocks.Load() != int32(scenario.deadlocks) || connects.Load() != 1 || p.Stat().IdleConns() != 1 {
				t.Fatal("confirmed deadlock rollbacks did not safely reuse exactly one idle APP connection")
			}
			f.verifyFinance(t)
		})
	}
}
