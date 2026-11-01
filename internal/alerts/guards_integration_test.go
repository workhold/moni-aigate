package alerts

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func alertRejectSQL(t *testing.T, f *fixture, name string, fn func(pgx.Tx) error) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		tx, err := f.app.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		err = fn(tx)
		if err == nil {
			err = tx.Commit(f.ctx)
		}
		if err == nil {
			t.Fatal("invalid actual APP history committed")
		}
	})
}
func alertClaimSQL(f *fixture, tx pgx.Tx, operation, kind string, target *string) (time.Time, error) {
	var at time.Time
	err := tx.QueryRow(f.ctx, `INSERT INTO alert_commands(operation_id,actor,kind,target_id,canonical_digest,status) VALUES($1,$2,$3,$4,repeat('a',64),'claimed') RETURNING created_at`, operation, f.admin, kind, target).Scan(&at)
	return at, err
}

func TestPostgresAlertHistoryRejectsInvalidDirectAPPChains(t *testing.T) {
	f := integration(t)
	rule, episode := alertOpen(t, f)
	alertRejectSQL(t, f, "two commands in same transaction", func(tx pgx.Tx) error {
		if _, err := alertClaimSQL(f, tx, uuid.NewString(), "rule_create", nil); err != nil {
			return err
		}
		_, err := alertClaimSQL(f, tx, uuid.NewString(), "rule_create", nil)
		return err
	})
	for _, infinity := range []string{"infinity", "-infinity"} {
		alertRejectSQL(t, f, "ack "+infinity, func(tx pgx.Tx) error {
			op := uuid.NewString()
			if _, err := alertClaimSQL(f, tx, op, "acknowledge", &episode); err != nil {
				return err
			}
			_, err := tx.Exec(f.ctx, `UPDATE alert_episodes SET acknowledged_by=$2,acknowledged_at=$4::timestamptz,acknowledged_command_id=$3 WHERE id=$1`, episode, f.admin, op, infinity)
			return err
		})
	}
	alertRejectSQL(t, f, "infinite event time", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `INSERT INTO alert_episode_events(id,episode_id,action_revision,type,actor,observed_at,created_at,command_id) VALUES($1,$2,2,'acknowledged',$3,'infinity','infinity',$4)`, uuid.NewString(), episode, f.admin, uuid.NewString())
		return err
	})
	alertRejectSQL(t, f, "infinite runtime failure", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_rule_runtime SET last_attempt_at='infinity',next_evaluation_at='infinity',last_error_code='source_unavailable' WHERE rule_id=$1`, rule)
		return err
	})
	alertRejectSQL(t, f, "ack without event", func(tx pgx.Tx) error {
		op := uuid.NewString()
		at, err := alertClaimSQL(f, tx, op, "acknowledge", &episode)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(f.ctx, `UPDATE alert_episodes SET acknowledged_by=$2,acknowledged_at=$4,acknowledged_command_id=$3 WHERE id=$1`, episode, f.admin, op, at); err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `UPDATE alert_commands SET status='applied',resource_id=$2,result_revision=2 WHERE operation_id=$1`, op, episode)
		return err
	})
	alertRejectSQL(t, f, "remove open pointer", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_rule_runtime SET current_episode_id=NULL WHERE rule_id=$1`, rule)
		return err
	})
	alertRejectSQL(t, f, "change initial snapshot", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_episodes SET rule_snapshot=jsonb_set(rule_snapshot,'{label}','"changed"') WHERE id=$1`, episode)
		return err
	})
	alertRejectSQL(t, f, "failed evaluation changes success", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_rule_runtime SET last_attempt_at=clock_timestamp()::timestamptz(6),next_evaluation_at=clock_timestamp()::timestamptz(6)+interval '30 seconds',last_error_code='source_unavailable',last_sample=NULL WHERE rule_id=$1`, rule)
		return err
	})
	alertRejectSQL(t, f, "rejected command has acknowledgment effect", func(tx pgx.Tx) error {
		op := uuid.NewString()
		at, err := alertClaimSQL(f, tx, op, "acknowledge", &episode)
		if err != nil {
			return err
		}
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE alert_episodes SET acknowledged_by=$2,acknowledged_at=$4,acknowledged_command_id=$3 WHERE id=$1`, []any{episode, f.admin, op, at}},
			{`INSERT INTO alert_episode_events(id,episode_id,action_revision,type,actor,observed_at,created_at,command_id) VALUES($1,$2,2,'acknowledged',$3,$4,$4,$5)`, []any{uuid.NewString(), episode, f.admin, at, op}},
			{`UPDATE alert_commands SET status='rejected',reason='alert_not_open',resource_id=$2 WHERE operation_id=$1`, []any{op, episode}},
		} {
			if _, err = tx.Exec(f.ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	ack, err := f.store.Acknowledge(f.ctx, f.reader(), episode, Acknowledge{uuid.NewString(), "1"})
	requireApplied(t, ack, err)
	alertRejectSQL(t, f, "second acknowledgment", func(tx pgx.Tx) error {
		op := uuid.NewString()
		at, err := alertClaimSQL(f, tx, op, "acknowledge", &episode)
		if err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `UPDATE alert_episodes SET acknowledged_by=$2,acknowledged_at=$4,acknowledged_command_id=$3 WHERE id=$1`, episode, f.admin, op, at)
		return err
	})
	alertRejectSQL(t, f, "terminal receipt rewrite", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_commands SET status='rejected',reason='alert_not_open',result_revision=NULL WHERE operation_id=$1`, ack.OperationID)
		return err
	})
	retired, err := f.store.Retire(f.ctx, f.reader(), rule, RetireRule{uuid.NewString(), "1"})
	requireApplied(t, retired, err)
	alertRejectSQL(t, f, "resolved episode rewrite", func(tx pgx.Tx) error {
		_, err := tx.Exec(f.ctx, `UPDATE alert_episodes SET last_seen_at=last_seen_at WHERE id=$1`, episode)
		return err
	})
}
