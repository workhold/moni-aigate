package periodicbudget_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

func TestPBPrefixPGFaultRealDeadlockRetryBound(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		deadlocks  int
		wantCommit bool
	}{{"one_deadlock_then_confirmed_commit", 1, true}, {"three_deadlocks_exhaust_attempts", 3, false}} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newPBPrefixFixture(t)
			_, err := f.owner.Exec(f.ctx, "ALTER ROLE "+pgx.Identifier{f.appRole}.Sanitize()+" SET deadlock_timeout='50ms'")
			pbTxMust(t, "configure actual prefix APP deadlock detection", err)
			// PostgreSQL orders BEFORE triggers by name. This boundary is after
			// pb_operation_zz_prefix_capture, so both the new journal and head
			// have already been written when the real lock cycle is formed.
			_, err = f.owner.Exec(f.ctx, `CREATE FUNCTION pb_prefix_fault_deadlock() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_observation_journal j
 JOIN periodic_budget_observation_heads h ON h.control_id=j.control_id
 WHERE j.id=NEW.observation_id AND j.kind='command' AND j.source_id=NEW.operation_id
 AND j.control_id=NEW.resource_id AND h.ordinal=j.ordinal AND h.last_observation_id=j.id) THEN
 RAISE EXCEPTION 'prefix fault boundary requires the captured source and head';END IF;
 PERFORM pg_advisory_xact_lock(23023026,hashtext(current_schema()));RETURN NEW;END $$;
 CREATE TRIGGER zz_prefix_deadlock BEFORE INSERT ON periodic_budget_operations
 FOR EACH ROW EXECUTE FUNCTION pb_prefix_fault_deadlock()`)
			pbTxMust(t, "install actual deadlock after candidate source capture", err)
			blocker, err := f.owner.Acquire(f.ctx)
			pbTxMust(t, "acquire explicit prefix fault blocker", err)
			advisoryHeld := false
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				_, _ = blocker.Exec(cleanup, `ROLLBACK`)
				if advisoryHeld {
					_, _ = blocker.Exec(cleanup, `SELECT pg_advisory_unlock(23023026,hashtext(current_schema()))`)
				}
				blocker.Release()
			}()
			var blockerPID int32
			pbTxMust(t, "observe exact prefix blocker identity", blocker.QueryRow(f.ctx, `SELECT pg_backend_pid()`).Scan(&blockerPID))
			_, err = blocker.Exec(f.ctx, `SELECT pg_advisory_lock(23023026,hashtext(current_schema()))`)
			pbTxMust(t, "hold explicit prefix advisory half of deadlock", err)
			advisoryHeld = true
			before := pbPrefixSnapshot(t, f)
			p, trace, connects, pid := pbCommanderFaultPool(t, f)
			in := pbCommandInput("0", "500")
			done := pbCommanderFaultStart(t, f, p, in)
			for i := 0; i < scenario.deadlocks; i++ {
				pbCommanderFaultWait(t, f, pid, false, blockerPID)
				_, err = blocker.Exec(f.ctx, `BEGIN;SET LOCAL deadlock_timeout='5s'`)
				pbTxMust(t, "begin opposite prefix fixture lock order", err)
				// APP owns tenant/control/head and waits on advisory; this
				// backend owns advisory and now waits on that same tenant.
				var tenant string
				pbTxMust(t, "form actual prefix two-backend deadlock", blocker.QueryRow(f.ctx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, f.tenant).Scan(&tenant))
				if tenant != f.tenant {
					t.Fatal("the prefix blocker acquired an unexpected tenant")
				}
				_, err = blocker.Exec(f.ctx, `ROLLBACK`)
				pbTxMust(t, "release blocker tenant after actual APP rollback", err)
			}
			if scenario.wantCommit {
				var unlocked bool
				pbTxMust(t, "release advisory for confirmed prefix retry", blocker.QueryRow(f.ctx, `SELECT pg_advisory_unlock(23023026,hashtext(current_schema()))`).Scan(&unlocked))
				if !unlocked {
					t.Fatal("the prefix retry blocker lost its advisory lock")
				}
				advisoryHeld = false
			}
			got := pbCommanderFaultAwait(t, f, done)
			if scenario.wantCommit {
				pbTxMust(t, "confirm original operation after one actual prefix deadlock", got.err)
				pbCommandReceipt(t, f, got.out, in, "tenant", "1", true)
				var exact bool
				pbTxMust(t, "verify retry retained exactly one candidate source", f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*)=1 FROM periodic_budget_observation_origins)
 AND (SELECT count(*)=1 AND bool_and(ordinal=1 AND last_observation_id IS NOT NULL) FROM periodic_budget_observation_heads)
 AND (SELECT count(*)=1 AND bool_and(kind='command' AND source_id=$1 AND ordinal=1 AND previous_id IS NULL) FROM periodic_budget_observation_journal)
 AND (SELECT count(*)=1 AND bool_and(operation_id=$1 AND observation_id IS NOT NULL) FROM periodic_budget_operations)`, in.OperationID).Scan(&exact))
				if !exact || trace.begins.Load() != 2 {
					t.Fatal("one deadlock did not yield one source in exactly two attempts")
				}
				pbPrefixBalanced(t, f, 1)
			} else {
				pbCommanderFaultZero(t, got, "periodic_budget_unavailable")
				if trace.begins.Load() != 3 || before != pbPrefixSnapshot(t, f) {
					t.Fatal("prefix deadlock exhaustion exceeded its retry bound or retained facts")
				}
				pbPrefixBalanced(t, f, 0)
			}
			if trace.deadlocks.Load() != int32(scenario.deadlocks) || connects.Load() != 1 || p.Stat().IdleConns() != 1 {
				t.Fatal("confirmed prefix deadlock rollbacks did not reuse one idle APP connection")
			}
			t.Logf("actual wire deadlocks=%d transaction attempts=%d APP connections=%d committed=%t", trace.deadlocks.Load(), trace.begins.Load(), connects.Load(), scenario.wantCommit)
			f.verifyFinance(t)
		})
	}
}

func TestPBPrefixPGFaultDisconnectDuringCommitReturnsUnknown(t *testing.T) {
	f := newPBPrefixFixture(t)
	_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pb_prefix_fault_commit_pause() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
 IF NOT EXISTS(SELECT 1 FROM periodic_budget_observation_journal j
 JOIN periodic_budget_observation_heads h ON h.control_id=j.control_id
 WHERE j.id=NEW.observation_id AND j.kind='command' AND j.source_id=NEW.operation_id
 AND h.ordinal=j.ordinal AND h.last_observation_id=j.id) THEN
 RAISE EXCEPTION 'prefix COMMIT fault requires the captured source and head';END IF;
 PERFORM pg_catalog.pg_sleep(30);RETURN NULL;END $$;
 CREATE CONSTRAINT TRIGGER zz_prefix_commit_pause AFTER INSERT ON periodic_budget_operations
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION pb_prefix_fault_commit_pause()`)
	pbTxMust(t, "install deferred COMMIT fault after candidate source capture", err)
	before := pbPrefixSnapshot(t, f)
	p, trace, connects, pid := pbCommanderFaultPool(t, f)
	in := pbCommandInput("0", "500")
	done := pbCommanderFaultStart(t, f, p, in)
	pbCommanderFaultWait(t, f, pid, true, 0)
	var terminated bool
	pbTxMust(t, "interrupt actual prefix APP during deferred COMMIT", f.owner.QueryRow(f.ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated))
	if !terminated {
		t.Fatal("prefix COMMIT-stage termination was not accepted")
	}
	pbCommanderFaultZero(t, pbCommanderFaultAwait(t, f, done), "periodic_budget_outcome_unknown")
	pbCommanderFaultDestroyed(t, f, p, trace, connects, pid)
	if before != pbPrefixSnapshot(t, f) {
		t.Fatal("interrupted prefix COMMIT retained an origin, head, source, receipt or audit")
	}
	// Resolve by the read-only original-operation API, without submitting the
	// command again. This fixture terminated the deferred trigger before the
	// COMMIT completed: it does not model a successful commit with a lost reply.
	reader, err := NewReader(f.app, make([]byte, 32))
	pbTxMust(t, "construct prefix original-operation reader", err)
	original, err := reader.PlatformOperation(f.ctx, f.user, in.OperationID)
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(original, CommandReceipt{}) {
		t.Fatal("interrupted original operation was not observed as absent by the reader")
	}
	if trace.begins.Load() != 1 || trace.deadlocks.Load() != 0 || before != pbPrefixSnapshot(t, f) {
		t.Fatal("unknown prefix outcome was automatically resubmitted or read-only recovery changed facts")
	}
	t.Logf("deferred COMMIT interrupted before completion: attempts=%d deadlocks=%d replacement connections=%d original operation absent; no resubmission", trace.begins.Load(), trace.deadlocks.Load(), connects.Load()-1)
	pbPrefixBalanced(t, f, 0)
	f.verifyFinance(t)
}
