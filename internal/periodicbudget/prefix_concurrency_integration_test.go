package periodicbudget_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
)

// A single INSERT is intentional: its statement snapshot begins before the
// competing transaction commits. The new BEFORE path must wait, then capture
// current control/window values and generate the receipt from that same cut.
const prefixDirectNoopSQL = `INSERT INTO periodic_budget_operations
 (operation_id,kind,actor,scope,target,resource_id,safe_input)
 VALUES($1,'tenant_periodic_budget.configure',$2,'platform',
 '/api/platform/tenants/'||$3||'/periodic-budget',$4,
 jsonb_build_object('expected_revision','1','period_kind','utc_month',
 'enabled',true,'limit_amount','1000')) RETURNING operation_id`

func prefixConfigureTenant(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	out, err := pbCommandCallFor(pbCommander(t, f), f, "tenant")(f.ctx, pbCommandInput("0", "1000"))
	pbTxMust(t, "prefix original tenant configuration", err)
	return out.Receipt.Operation.ResourceID
}

func prefixAssertComplete(t *testing.T, f *pbTxFixture) periodicbudgetprefix.Report {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	pbTxMust(t, "prefix actual APP RR read-only", err)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	}()
	report, err := periodicbudgetprefix.Check(f.ctx, pbPrefixRows(t, f, tx))
	pbTxMust(t, "prefix complete original graph and observation replay", err)
	if !report.Balanced || !report.CoverageComplete || report.Scope != "periodic_budget_snapshot_prefix_v1" {
		t.Fatalf("prefix expected complete report: %+v", report)
	}
	pbTxMust(t, "prefix close actual APP read-only snapshot", tx.Commit(f.ctx))
	return report
}

func prefixWaitForBlocker(t *testing.T, f *pbTxFixture, worker, blocker uint32, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		pbTxMust(t, "prefix observe exact APP SQL lock dependency", f.owner.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1::integer
 AND usename=$3 AND state='active' AND wait_event_type='Lock')
 AND $2::integer=ANY(pg_catalog.pg_blocking_pids($1::integer))`, worker, blocker, f.appRole).Scan(&waiting))
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("prefix INSERT ended before the independently observed wait (%T)", err)
		case <-ctx.Done():
			t.Fatal("prefix did not observe the original blocker")
		case <-ticker.C:
		}
	}
}

func TestPBPrefixPostgresReadCommittedFreshSnapshot(t *testing.T) {
	for _, financial := range []bool{false, true} {
		t.Run(fmt.Sprintf("financial_%t", financial), func(t *testing.T) {
			f := newPBPrefixFixture(t)
			control := prefixConfigureTenant(t, f)
			blocker := f.begin(t)
			wantHeld := "0"
			if financial {
				locks := pbTxCustomerLocks(t, f, blocker, "")
				_, _ = f.newCustomer(t, blocker, locks)
				wantHeld = "120"
			} else {
				var window string
				pbTxMust(t, "prefix uncommitted original zero window", blocker.QueryRow(f.ctx, `SELECT pb_observe($1,pb_now())`, control).Scan(&window))
			}
			conn, err := f.app.Acquire(f.ctx)
			pbTxMust(t, "prefix acquire separate actual APP", err)
			defer conn.Release()
			worker, err := conn.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			pbTxMust(t, "prefix worker READ COMMITTED", err)
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = worker.Rollback(ctx)
			}()
			op := uuid.NewString()
			ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
			defer cancel()
			done := make(chan error, 1)
			queryDone := make(chan struct{})
			go func() {
				defer close(queryDone)
				var id string
				err := worker.QueryRow(ctx, prefixDirectNoopSQL, op, f.user, f.tenant, control).Scan(&id)
				if err == nil && id != op {
					err = fmt.Errorf("prefix incorrect returned operation")
				}
				done <- err
			}()
			// Join the query before the earlier rollback/release defers, even
			// when the independent lock observer ends this test via Fatal.
			defer func() {
				cancel()
				<-queryDone
			}()
			prefixWaitForBlocker(t, f, worker.Conn().PgConn().PID(), blocker.Conn().PgConn().PID(), done)
			pbTxMust(t, "prefix publish original competing window/effect", blocker.Commit(f.ctx))
			select {
			case err := <-done:
				pbTxMust(t, "prefix direct INSERT after original lock wait", err)
			case <-ctx.Done():
				t.Fatal("prefix direct INSERT did not finish within its original deadline")
			}
			pbTxMust(t, "prefix commit original receipt and captured cut", worker.Commit(f.ctx))
			var held, window string
			pbTxMust(t, "prefix receipt observes post-wait window and amount", f.app.QueryRow(f.ctx, `SELECT
 result_snapshot->'current_window'->>'held_amount',result_snapshot->'current_window'->>'id'
 FROM periodic_budget_operations WHERE operation_id=$1`, op).Scan(&held, &window))
			if held != wantHeld || window == "" {
				t.Fatal("prefix receipt used a pre-wait window or amount")
			}
			report := prefixAssertComplete(t, f)
			if report.CertifiedCommands != 2 {
				t.Fatal("prefix did not authenticate both original commands")
			}
		})
	}
}

func TestPBPrefixPostgresReturningAndMultirow(t *testing.T) {
	t.Run("returning_creates_later_window_at_same_financial_cut", func(t *testing.T) {
		f := newPBPrefixFixture(t)
		control := prefixConfigureTenant(t, f)
		op := uuid.NewString()
		var window string
		query := `INSERT INTO periodic_budget_operations
 (operation_id,kind,actor,scope,target,resource_id,safe_input)
 VALUES($1,'tenant_periodic_budget.configure',$2,'platform','/api/platform/tenants/'||$3||'/periodic-budget',$4,
 jsonb_build_object('expected_revision','1','period_kind','utc_month','enabled',true,'limit_amount','1000'))
 RETURNING pb_observe(resource_id,pb_now())`
		pbTxMust(t, "prefix RETURNING side effect after source BEFORE capture", f.app.QueryRow(f.ctx, query, op, f.user, f.tenant, control).Scan(&window))
		var nullWindow bool
		pbTxMust(t, "prefix original receipt remains null before later birth", f.app.QueryRow(f.ctx, `SELECT result_snapshot->'current_window'='null'::jsonb
 FROM periodic_budget_operations WHERE operation_id=$1`, op).Scan(&nullWindow))
		if !nullWindow || window == "" {
			t.Fatal("prefix RETURNING side effect rewrote the earlier snapshot")
		}
		third := uuid.NewString()
		var returned string
		pbTxMust(t, "prefix later command sees original same-cut window", f.app.QueryRow(f.ctx, prefixDirectNoopSQL, third, f.user, f.tenant, control).Scan(&returned))
		prefixAssertComplete(t, f)
	})
	t.Run("multiple_commands_in_one_statement", func(t *testing.T) {
		f := newPBPrefixFixture(t)
		control := prefixConfigureTenant(t, f)
		rows, err := f.app.Query(f.ctx, `INSERT INTO periodic_budget_operations
 (operation_id,kind,actor,scope,target,resource_id,safe_input)
 SELECT gen_random_uuid()::text,'tenant_periodic_budget.configure',$1,'platform',
 '/api/platform/tenants/'||$2||'/periodic-budget',$3,jsonb_build_object('expected_revision','1','period_kind','utc_month',
 'enabled',true,'limit_amount','1000') FROM generate_series(1,2) RETURNING operation_id`, f.user, f.tenant, control)
		pbTxMust(t, "prefix multirow original command INSERT", err)
		count := 0
		for rows.Next() {
			var id string
			pbTxMust(t, "prefix multirow original result", rows.Scan(&id))
			count++
		}
		pbTxMust(t, "prefix multirow original EOF", rows.Err())
		rows.Close()
		if count != 2 {
			t.Fatal("prefix multirow returned an incomplete original result")
		}
		if prefixAssertComplete(t, f).CertifiedCommands != 3 {
			t.Fatal("prefix multirow lost a command observation")
		}
	})
}
