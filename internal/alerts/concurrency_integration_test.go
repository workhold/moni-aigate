package alerts

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type alertLockTrace struct{ retries atomic.Int64 }

func (*alertLockTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (tr *alertLockTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	var pg *pgconn.PgError
	if errors.As(d.Err, &pg) && (pg.Code == "40P01" || pg.Code == "40001") {
		tr.retries.Add(1)
	}
}

func alertConcurrentFixture(t *testing.T) *fixture {
	t.Helper()
	f := integration(t)
	cfg := f.app.Config()
	tr := &alertLockTrace{}
	cfg.ConnConfig.Tracer = tr
	p, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		if tr.retries.Load() != 0 {
			t.Error("repository retry concealed a lock cycle", tr.retries.Load())
		}
	})
	f.store, err = NewStore(p, RuntimeOptions{DeploymentEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func alertTx(t *testing.T, f *fixture) pgx.Tx {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func alertWaitPID(t *testing.T, f *fixture, blocker uint32, n int) []uint32 {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	timer := time.NewTicker(time.Millisecond)
	defer timer.Stop()
	for {
		rows, err := f.owner.Query(ctx, `WITH RECURSIVE blocked(pid) AS (
 SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid))
 UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid)) WHERE a.datname=current_database()
) SELECT pid FROM blocked ORDER BY pid`, int32(blocker))
		if err != nil {
			t.Fatal(err)
		}
		pids := []uint32{}
		for rows.Next() {
			var pid uint32
			if err = rows.Scan(&pid); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			pids = append(pids, pid)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(pids) >= n {
			return pids
		}
		select {
		case <-ctx.Done():
			t.Fatal("required real PostgreSQL lock edge absent", ctx.Err())
		case <-timer.C:
		}
	}
}

func alertGate(t *testing.T, f *fixture, table, event, condition string) pgx.Tx {
	t.Helper()
	u := uuid.New()
	key := int64(binary.BigEndian.Uint64(u[:8]))
	f.exec(fmt.Sprintf(`CREATE FUNCTION alert_test_gate() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END $$; CREATE TRIGGER zz_alert_test_gate BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION alert_test_gate()`, condition, key, event, table))
	tx := alertTx(t, f)
	if _, err := tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	return tx
}

type alertCommandResult struct {
	receipt CommandReceipt
	err     error
}

func alertRun(fn func() (CommandReceipt, error)) <-chan alertCommandResult {
	done := make(chan alertCommandResult, 1)
	go func() { r, e := fn(); done <- alertCommandResult{r, e} }()
	return done
}
func alertAwait(t *testing.T, ch <-chan alertCommandResult) alertCommandResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(4 * time.Second):
		t.Fatal("alert command did not finish")
		return alertCommandResult{}
	}
}

func alertOpen(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	f.exec(`UPDATE channels SET quarantined=true WHERE id=$1`, f.channel)
	r := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "enabled")
	f.evaluate()
	rule, err := f.store.Rule(f.ctx, f.reader(), *r.ResourceID)
	if err != nil || rule.Runtime.CurrentEpisodeID == nil {
		t.Fatal(rule, err)
	}
	return *r.ResourceID, *rule.Runtime.CurrentEpisodeID
}

func TestPostgresAlertAcknowledgmentAndRecoveryBothOrders(t *testing.T) {
	for _, first := range []string{"acknowledge", "recover"} {
		t.Run(first, func(t *testing.T) {
			f := alertConcurrentFixture(t)
			rule, episode := alertOpen(t, f)
			f.due(rule)
			f.exec(`UPDATE channels SET quarantined=false WHERE id=$1`, f.channel)
			condition := "NEW.acknowledged_by IS DISTINCT FROM OLD.acknowledged_by"
			if first == "recover" {
				condition = "NEW.resolve_reason='recovered'"
			}
			gate := alertGate(t, f, "alert_episodes", "UPDATE", condition)
			var ack <-chan alertCommandResult
			worker := make(chan struct {
				r EvaluationResult
				e error
			}, 1)
			runAck := func() <-chan alertCommandResult {
				return alertRun(func() (CommandReceipt, error) {
					return f.store.Acknowledge(f.ctx, Reader{f.finance}, episode, Acknowledge{uuid.NewString(), "1"})
				})
			}
			runWorker := func() {
				go func() {
					r, e := f.store.EvaluateDue(f.ctx)
					worker <- struct {
						r EvaluationResult
						e error
					}{r, e}
				}()
			}
			if first == "acknowledge" {
				ack = runAck()
			} else {
				runWorker()
			}
			writer := alertWaitPID(t, f, gate.Conn().PgConn().PID(), 1)[0]
			if first == "acknowledge" {
				// SKIP LOCKED is the intended competing-worker behavior while ACK
				// holds the rule, followed by a fresh recovery after ACK commits.
				r, err := f.store.EvaluateDue(f.ctx)
				if err != nil || r.Outcome != "idle" {
					t.Fatal(r, err)
				}
			} else {
				ack = runAck()
				alertWaitPID(t, f, writer, 1)
			}
			if err := gate.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			a := alertAwait(t, ack)
			if first == "acknowledge" {
				requireApplied(t, a.receipt, a.err)
				runWorker()
			} else {
				requireRejected(t, a.receipt, a.err, "alert_not_open")
			}
			select {
			case w := <-worker:
				if w.e != nil || w.r.Outcome != "evaluated" {
					t.Fatal(w)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("worker did not finish")
			}
			ep, err := f.store.Episode(f.ctx, f.reader(), episode)
			wantRevision := "2"
			if first == "acknowledge" {
				wantRevision = "3"
			}
			if err != nil || ep.Status != "resolved" || ep.ActionRevision != wantRevision || (ep.AcknowledgedBy != nil) != (first == "acknowledge") {
				t.Fatal(ep, err)
			}
		})
	}
}

func TestPostgresAlertAcknowledgmentAndConfigureBothOrders(t *testing.T) {
	for _, first := range []string{"acknowledge", "configure"} {
		t.Run(first, func(t *testing.T) {
			f := alertConcurrentFixture(t)
			rule, episode := alertOpen(t, f)
			table, condition := "alert_episodes", "NEW.acknowledged_by IS DISTINCT FROM OLD.acknowledged_by"
			if first == "configure" {
				table, condition = "alert_rules", "NEW.label='changed'"
			}
			gate := alertGate(t, f, table, "UPDATE", condition)
			runAck := func() <-chan alertCommandResult {
				return alertRun(func() (CommandReceipt, error) {
					return f.store.Acknowledge(f.ctx, Reader{f.finance}, episode, Acknowledge{uuid.NewString(), "1"})
				})
			}
			runConfigure := func() <-chan alertCommandResult {
				return alertRun(func() (CommandReceipt, error) {
					return f.store.Configure(f.ctx, f.reader(), rule, ConfigureRule{uuid.NewString(), "1", "changed", "enabled", EmptyParameters{}})
				})
			}
			var ack, config <-chan alertCommandResult
			if first == "acknowledge" {
				ack = runAck()
			} else {
				config = runConfigure()
			}
			writer := alertWaitPID(t, f, gate.Conn().PgConn().PID(), 1)[0]
			if first == "acknowledge" {
				config = runConfigure()
			} else {
				ack = runAck()
			}
			alertWaitPID(t, f, writer, 1)
			if err := gate.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			a, c := alertAwait(t, ack), alertAwait(t, config)
			requireApplied(t, c.receipt, c.err)
			if first == "acknowledge" {
				requireApplied(t, a.receipt, a.err)
			} else {
				requireRejected(t, a.receipt, a.err, "alert_not_open")
			}
			ep, err := f.store.Episode(f.ctx, f.reader(), episode)
			want := "2"
			if first == "acknowledge" {
				want = "3"
			}
			if err != nil || ep.ActionRevision != want || ep.Status != "resolved" || ep.ResolveReason == nil || *ep.ResolveReason != "rule_changed" {
				t.Fatal(ep, err)
			}
		})
	}
}

func TestPostgresAlertRoleRevocationWinsWaitAndCreateAfterRetire(t *testing.T) {
	t.Run("fresh role after wait", func(t *testing.T) {
		f := alertConcurrentFixture(t)
		tx := alertTx(t, f)
		if _, err := tx.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); err != nil {
			t.Fatal(err)
		}
		op := uuid.NewString()
		result := alertRun(func() (CommandReceipt, error) {
			return f.store.Create(f.ctx, f.reader(), CreateRule{op, ChannelQuarantined, "channel", f.channel, "blocked", "enabled", EmptyParameters{}})
		})
		alertWaitPID(t, f, tx.Conn().PgConn().PID(), 1)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		r := alertAwait(t, result)
		requireCode(t, r.err, "forbidden")
		var count int
		if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM alert_commands WHERE operation_id=$1`, op).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	})
	t.Run("retirement frees live unique scope", func(t *testing.T) {
		f := alertConcurrentFixture(t)
		r := f.create(ChannelQuarantined, "channel", f.channel, EmptyParameters{}, "enabled")
		gate := alertGate(t, f, "alert_rules", "UPDATE", "NEW.state='retired'")
		retire := alertRun(func() (CommandReceipt, error) {
			return f.store.Retire(f.ctx, f.reader(), *r.ResourceID, RetireRule{uuid.NewString(), "1"})
		})
		writer := alertWaitPID(t, f, gate.Conn().PgConn().PID(), 1)[0]
		create := alertRun(func() (CommandReceipt, error) {
			return f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), ChannelQuarantined, "channel", f.channel, "recreated", "enabled", EmptyParameters{}})
		})
		alertWaitPID(t, f, writer, 1)
		if err := gate.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		a, b := alertAwait(t, retire), alertAwait(t, create)
		requireApplied(t, a.receipt, a.err)
		requireApplied(t, b.receipt, b.err)
		if *a.receipt.ResourceID == *b.receipt.ResourceID {
			t.Fatal("retirement reused immutable identity")
		}
	})
}

func TestPostgresAlertConcurrentEnabledCapacityBoundary(t *testing.T) {
	f := alertConcurrentFixture(t)
	ids := make([]string, 65)
	for i := range ids {
		ids[i] = uuid.NewString()
		f.exec(`INSERT INTO tenants(id,name) VALUES($1,'capacity')`, ids[i])
		if i < 63 {
			f.create(CustomerReconciliationAged, "tenant", ids[i], AgingParameters{"600", "1", "0"}, "enabled")
		}
	}
	gate := alertTx(t, f)
	if _, err := gate.Exec(f.ctx, `SELECT singleton FROM alert_control WHERE singleton FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	run := func(id string) <-chan alertCommandResult {
		return alertRun(func() (CommandReceipt, error) {
			return f.store.Create(f.ctx, f.reader(), CreateRule{uuid.NewString(), CustomerReconciliationAged, "tenant", id, "boundary", "enabled", AgingParameters{"600", "1", "0"}})
		})
	}
	a, b := run(ids[63]), run(ids[64])
	alertWaitPID(t, f, gate.Conn().PgConn().PID(), 2)
	if err := gate.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	applied, rejected := 0, 0
	for _, ch := range []<-chan alertCommandResult{a, b} {
		r := alertAwait(t, ch)
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch r.receipt.Status {
		case "applied":
			applied++
		case "rejected":
			if r.receipt.Reason != "alert_rule_limit" {
				t.Fatal(r)
			}
			rejected++
		}
	}
	if applied != 1 || rejected != 1 {
		t.Fatal(applied, rejected)
	}
	var count int
	if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM alert_rules WHERE state='enabled'`).Scan(&count); err != nil || count != 64 {
		t.Fatal(count, err)
	}
}
