package procurementprefixschema_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This models an exact-link mechanism in isolated test relations. The fixture
// owner sets an immutable tracked marker; it does not implement, enable or
// authorize any business source/root, and does not replace the 685 constraints.
const exactLinkExperimentSQL = `
CREATE TABLE {{schema}}.el_parent (
 id pg_catalog.text PRIMARY KEY,
 payload pg_catalog.text,
 tracked pg_catalog.bool NOT NULL DEFAULT false
);
CREATE TABLE {{schema}}.el_child (
 id pg_catalog.text PRIMARY KEY,
 parent_id pg_catalog.text NOT NULL,
 expected_payload pg_catalog.text,
 FOREIGN KEY(parent_id) REFERENCES {{schema}}.el_parent(id)
 DEFERRABLE INITIALLY DEFERRED
);

CREATE FUNCTION {{schema}}.el_parent_guard() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,{{schema}},pg_temp AS $fn$
BEGIN
 IF TG_OP='UPDATE' AND (NEW.id IS DISTINCT FROM OLD.id OR NEW.tracked IS DISTINCT FROM OLD.tracked) THEN
  RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='exact_link_identity_immutable';
 END IF;
 IF OLD.tracked AND pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='exact_link_requires_read_committed';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $fn$;

CREATE FUNCTION {{schema}}.el_child_guard() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,{{schema}},pg_temp AS $fn$
BEGIN
 IF pg_catalog.current_setting('transaction_isolation') <> 'read committed' THEN
  RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='exact_link_requires_read_committed';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $fn$;

CREATE FUNCTION {{schema}}.el_child_exact() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,{{schema}},pg_temp AS $fn$
DECLARE c record; actual_payload pg_catalog.text; actual_tracked pg_catalog.bool;
BEGIN
 SELECT parent_id,expected_payload INTO c FROM {{schema}}.el_child WHERE id=NEW.id;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM 1 FROM {{schema}}.el_parent WHERE id=c.parent_id FOR SHARE;
 IF NOT FOUND THEN
  RAISE EXCEPTION USING ERRCODE='23503', MESSAGE='exact_link_parent_missing';
 END IF;
 -- A separate statement after the locking statement obtains a fresh RC view.
 SELECT payload,tracked INTO actual_payload,actual_tracked FROM {{schema}}.el_parent WHERE id=c.parent_id;
 IF NOT actual_tracked OR actual_payload IS DISTINCT FROM c.expected_payload THEN
  RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='exact_link_payload_mismatch';
 END IF;
 RETURN NULL;
END $fn$;

CREATE FUNCTION {{schema}}.el_parent_exact() RETURNS trigger
 LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,{{schema}},pg_temp AS $fn$
DECLARE actual_payload pg_catalog.text; actual_tracked pg_catalog.bool;
BEGIN
 IF NOT OLD.tracked THEN RETURN NULL; END IF;
 -- UPDATE/DELETE already holds the parent row lock. Read final current state,
 -- rather than the NEW of an earlier event in a multi-update transaction.
 SELECT payload,tracked INTO actual_payload,actual_tracked FROM {{schema}}.el_parent WHERE id=OLD.id;
 IF NOT FOUND THEN
  IF EXISTS(SELECT 1 FROM {{schema}}.el_child WHERE parent_id=OLD.id) THEN
   RAISE EXCEPTION USING ERRCODE='23503', MESSAGE='exact_link_parent_missing';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM {{schema}}.el_child WHERE parent_id=OLD.id AND (NOT actual_tracked OR expected_payload IS DISTINCT FROM actual_payload)) THEN
  RAISE EXCEPTION USING ERRCODE='23514', MESSAGE='exact_link_payload_mismatch';
 END IF;
 RETURN NULL;
END $fn$;

CREATE TRIGGER el_parent_guard BEFORE UPDATE OR DELETE ON {{schema}}.el_parent
 FOR EACH ROW EXECUTE FUNCTION {{schema}}.el_parent_guard();
CREATE TRIGGER el_child_guard BEFORE INSERT OR UPDATE OR DELETE ON {{schema}}.el_child
 FOR EACH ROW EXECUTE FUNCTION {{schema}}.el_child_guard();
CREATE CONSTRAINT TRIGGER el_child_exact AFTER INSERT OR UPDATE ON {{schema}}.el_child
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{schema}}.el_child_exact();
CREATE CONSTRAINT TRIGGER el_parent_exact AFTER UPDATE OR DELETE ON {{schema}}.el_parent
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION {{schema}}.el_parent_exact();
REVOKE ALL ON FUNCTION {{schema}}.el_parent_guard(),{{schema}}.el_child_guard(),{{schema}}.el_parent_exact(),{{schema}}.el_child_exact() FROM PUBLIC;
`

func exactLinkError(t *testing.T, err error, code, message string) {
	t.Helper()
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != code || pgError.Message != message {
		t.Fatalf("want %s/%s; got %v", code, message, err)
	}
}

func exactLinkBlocked(ctx context.Context, owner *pgxpool.Pool, blocked, blocker uint32) error {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		var observed bool
		if err := owner.QueryRow(deadline, `SELECT $2::pg_catalog.int4 = ANY(pg_catalog.pg_blocking_pids($1::pg_catalog.int4))`, blocked, blocker).Scan(&observed); err != nil {
			return err
		}
		if observed {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-deadline.Done():
			timer.Stop()
			return errors.New("exact_link_expected_blocking_not_observed")
		}
	}
}

func TestPostgresProcurementExactLinkRCExperiment(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated exact-link mechanism experiment")
	}
	config, err := pgxpool.ParseConfig(raw)
	must(t, "parse exact-link dedicated configuration", err)
	cc := config.ConnConfig
	if cc.Host != "127.0.0.1" || cc.Port != 55439 || cc.Database != "moni_test" || cc.User != "moni_test" || len(cc.Fallbacks) != 0 {
		t.Fatal("exact-link experiment requires dedicated 127.0.0.1:55439/moni_test and moni_test LOGIN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner, err := pgxpool.NewWithConfig(ctx, config.Copy())
	must(t, "connect exact-link owner", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, role := "pp_exact_link_"+suffix, "pp_exact_app_"+suffix
	qs, qr := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{role}.Sanitize()
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		defer owner.Close()
		if _, err := owner.Exec(cleanup, "DROP SCHEMA IF EXISTS "+qs+" CASCADE"); err != nil {
			t.Error("exact-link schema cleanup failed")
		}
		if _, err := owner.Exec(cleanup, "DROP ROLE IF EXISTS "+qr); err != nil {
			t.Error("exact-link role cleanup failed")
		}
		var remains bool
		if err := owner.QueryRow(cleanup, `SELECT EXISTS(SELECT FROM pg_catalog.pg_namespace WHERE nspname=$1) OR EXISTS(SELECT FROM pg_catalog.pg_roles WHERE rolname=$2)`, schema, role).Scan(&remains); err != nil || remains {
			t.Error("exact-link fixture schema or role remains")
		}
	})
	var version int
	must(t, "read exact-link PG version", owner.QueryRow(ctx, `SELECT pg_catalog.current_setting('server_version_num')::pg_catalog.int4`).Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("exact-link experiment requires PostgreSQL16")
	}
	_, err = owner.Exec(ctx, "CREATE SCHEMA "+qs)
	must(t, "create exact-link schema", err)
	_, err = owner.Exec(ctx, strings.ReplaceAll(exactLinkExperimentSQL, "{{schema}}", qs))
	must(t, "create exact-link mechanism", err)
	// The random fixture password is not a claim about the local HBA policy.
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = owner.Exec(ctx, "CREATE ROLE "+qr+" LOGIN PASSWORD '"+password+"'")
	must(t, "create exact-link actual LOGIN", err)
	_, err = owner.Exec(ctx, "GRANT USAGE ON SCHEMA "+qs+" TO "+qr+"; GRANT SELECT ON "+qs+".el_parent,"+qs+".el_child TO "+qr+"; GRANT INSERT(id,payload),UPDATE(payload),DELETE ON "+qs+".el_parent TO "+qr+"; GRANT INSERT(id,parent_id,expected_payload),UPDATE(expected_payload),DELETE ON "+qs+".el_child TO "+qr)
	must(t, "grant isolated exact-link model only", err)
	appConfig := config.Copy()
	appConfig.MaxConns = 4
	appConfig.ConnConfig.User = role
	appConfig.ConnConfig.Password = password
	appConfig.ConnConfig.RuntimeParams["search_path"] = schema
	appConfig.ConnConfig.RuntimeParams["statement_timeout"] = "8s"
	appConfig.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	app, err = pgxpool.NewWithConfig(ctx, appConfig)
	must(t, "open actual exact-link LOGIN pool", err)
	var actualUser, actualSession string
	must(t, "verify actual exact-link LOGIN", app.QueryRow(ctx, `SELECT current_user::pg_catalog.text,session_user::pg_catalog.text`).Scan(&actualUser, &actualSession))
	if actualUser != role || actualSession != role {
		t.Fatal("exact-link fixture did not connect as the restricted LOGIN")
	}
	seed := func(t *testing.T, id string, payload *string) {
		t.Helper()
		_, err := owner.Exec(ctx, "INSERT INTO "+qs+".el_parent(id,payload,tracked) VALUES($1,$2,true)", id, payload)
		must(t, "seed owner-only tracked experiment parent", err)
	}
	textPointer := func(v string) *string { return &v }
	t.Run("long_text_exact_and_null_semantics", func(t *testing.T) {
		var longText string
		must(t, "construct deterministic unbounded TEXT", owner.QueryRow(ctx, `SELECT pg_catalog.string_agg(pg_catalog.md5(i::pg_catalog.text),'' ORDER BY i) FROM pg_catalog.generate_series(1,256) i`).Scan(&longText))
		if len(longText) != 8192 {
			t.Fatal("long TEXT fixture length differs")
		}
		seed(t, "long-tracked", &longText)
		_, err := app.Exec(ctx, `INSERT INTO el_parent(id,payload) VALUES('long-legacy',$1)`, longText)
		must(t, "write untracked legacy long TEXT without compound index", err)
		_, err = app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('long-match','long-tracked',$1)`, longText)
		must(t, "commit exact long TEXT link", err)
		changed := longText[:len(longText)-1] + "!"
		_, err = app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('long-wrong','long-tracked',$1)`, changed)
		exactLinkError(t, err, "23514", "exact_link_payload_mismatch")
		seed(t, "null-parent", nil)
		_, err = app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('null-match','null-parent',NULL)`)
		must(t, "commit exact NULL link", err)
		_, err = app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('null-empty','null-parent','')`)
		exactLinkError(t, err, "23514", "exact_link_payload_mismatch")
	})
	t.Run("repeatable_read_tracked_rejected_legacy_allowed", func(t *testing.T) {
		seed(t, "rr-tracked", textPointer("A"))
		_, err := app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('rr-existing-child','rr-tracked','A')`)
		must(t, "seed checked child for RR write guards", err)
		for _, query := range []string{
			`UPDATE el_parent SET payload='B' WHERE id='rr-tracked'`,
			`DELETE FROM el_parent WHERE id='rr-tracked'`,
			`INSERT INTO el_child(id,parent_id,expected_payload) VALUES('rr-child','rr-tracked','A')`,
			`UPDATE el_child SET expected_payload='B' WHERE id='rr-existing-child'`,
			`DELETE FROM el_child WHERE id='rr-existing-child'`,
		} {
			tx, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			must(t, "begin actual RR experiment", err)
			_, err = tx.Exec(ctx, query)
			exactLinkError(t, err, "23514", "exact_link_requires_read_committed")
			must(t, "rollback RR rejection", tx.Rollback(ctx))
		}
		tx, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		must(t, "begin legacy RR", err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `INSERT INTO el_parent(id,payload) VALUES('rr-legacy','A')`)
		must(t, "legacy INSERT under RR", err)
		_, err = tx.Exec(ctx, `UPDATE el_parent SET payload='B' WHERE id='rr-legacy'`)
		must(t, "legacy UPDATE under RR", err)
		must(t, "commit legacy RR", tx.Commit(ctx))
	})
	t.Run("deferred_checks_use_final_state_and_guard_both_ends", func(t *testing.T) {
		seed(t, "final-state", textPointer("A"))
		_, err := app.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('final-state-child','final-state','A')`)
		must(t, "seed final-state checked child", err)
		// Both parent events must check the final A, rather than the earlier B.
		tx, err := app.Begin(ctx)
		must(t, "begin multi-update final state", err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `UPDATE el_parent SET payload='B' WHERE id='final-state'`)
		must(t, "write parent intermediate payload", err)
		_, err = tx.Exec(ctx, `UPDATE el_parent SET payload='A' WHERE id='final-state'`)
		must(t, "restore parent final payload", err)
		must(t, "commit final parent state", tx.Commit(ctx))
		_, err = app.Exec(ctx, `UPDATE el_child SET expected_payload='wrong' WHERE id='final-state-child'`)
		exactLinkError(t, err, "23514", "exact_link_payload_mismatch")
		// Changing both ends in one transaction is valid at its final state.
		tx, err = app.Begin(ctx)
		must(t, "begin coherent two-ended final state", err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `UPDATE el_parent SET payload='C' WHERE id='final-state'`)
		must(t, "write coherent parent final payload", err)
		_, err = tx.Exec(ctx, `UPDATE el_child SET expected_payload='C' WHERE id='final-state-child'`)
		must(t, "write coherent child final payload", err)
		must(t, "commit coherent two-ended final state", tx.Commit(ctx))
		// A parent locator cannot be reused as an untracked legacy row while
		// retaining children, even when its TEXT payload is unchanged.
		tx, err = app.Begin(ctx)
		must(t, "begin tracked identity reinsert attack", err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `DELETE FROM el_parent WHERE id='final-state'`)
		must(t, "delete tracked parent pending final checks", err)
		_, err = tx.Exec(ctx, `INSERT INTO el_parent(id,payload) VALUES('final-state','C')`)
		must(t, "attempt untracked parent locator reuse", err)
		exactLinkError(t, tx.Commit(ctx), "23514", "exact_link_payload_mismatch")
		// A complete child/parent removal remains valid under RC.
		tx, err = app.Begin(ctx)
		must(t, "begin coherent two-ended removal", err)
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `DELETE FROM el_child WHERE id='final-state-child'`)
		must(t, "delete child before parent", err)
		_, err = tx.Exec(ctx, `DELETE FROM el_parent WHERE id='final-state'`)
		must(t, "delete parent without final children", err)
		must(t, "commit coherent two-ended removal", tx.Commit(ctx))
	})
	t.Run("child_share_blocks_parent_then_reverse_check_rejects", func(t *testing.T) {
		seed(t, "child-first", textPointer("A"))
		child, err := app.Begin(ctx)
		must(t, "begin child-first child", err)
		defer child.Rollback(ctx)
		parent, err := app.Begin(ctx)
		must(t, "begin child-first parent", err)
		defer parent.Rollback(ctx)
		_, err = child.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES('child-first-row','child-first','A')`)
		must(t, "insert child-first source", err)
		_, err = child.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
		must(t, "obtain child-first SHARE and exact check", err)
		race, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		done := make(chan error, 1)
		go func() {
			_, e := parent.Exec(race, `UPDATE el_parent SET payload='B' WHERE id='child-first'`)
			if e == nil {
				_, e = parent.Exec(race, `SET CONSTRAINTS ALL IMMEDIATE`)
			}
			done <- e
		}()
		if err := exactLinkBlocked(ctx, owner, parent.Conn().PgConn().PID(), child.Conn().PgConn().PID()); err != nil {
			stop()
			<-done
			must(t, "observe child-first actual blocking", err)
		}
		if err := child.Commit(ctx); err != nil {
			stop()
			<-done
			must(t, "commit checked child-first source", err)
		}
		exactLinkError(t, <-done, "23514", "exact_link_payload_mismatch")
	})
	for _, matches := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_first_fresh_statement_match_%t", matches), func(t *testing.T) {
			id := fmt.Sprintf("parent-first-%t", matches)
			seed(t, id, textPointer("A"))
			parent, err := app.Begin(ctx)
			must(t, "begin parent-first parent", err)
			defer parent.Rollback(ctx)
			child, err := app.Begin(ctx)
			must(t, "begin parent-first child", err)
			defer child.Rollback(ctx)
			_, err = parent.Exec(ctx, `UPDATE el_parent SET payload='B' WHERE id=$1`, id)
			must(t, "change parent under actual write lock", err)
			expected := "A"
			if matches {
				expected = "B"
			}
			_, err = child.Exec(ctx, `INSERT INTO el_child(id,parent_id,expected_payload) VALUES($1,$1,$2)`, id, expected)
			must(t, "insert deferred parent-first child", err)
			race, stop := context.WithTimeout(ctx, 5*time.Second)
			defer stop()
			done := make(chan error, 1)
			go func() {
				_, err := child.Exec(race, `SET CONSTRAINTS ALL IMMEDIATE`)
				done <- err
			}()
			if err := exactLinkBlocked(ctx, owner, child.Conn().PgConn().PID(), parent.Conn().PgConn().PID()); err != nil {
				stop()
				<-done
				must(t, "observe parent-first actual blocking", err)
			}
			if err := parent.Commit(ctx); err != nil {
				stop()
				<-done
				must(t, "commit parent before child re-read", err)
			}
			err = <-done
			if !matches {
				exactLinkError(t, err, "23514", "exact_link_payload_mismatch")
			} else {
				must(t, "fresh statement observes matching committed parent", err)
				must(t, "commit matching parent-first child", child.Commit(ctx))
			}
		})
	}
	var orphan int
	must(t, "verify final exact-link model", owner.QueryRow(ctx, "SELECT count(*) FROM "+qs+".el_child c LEFT JOIN "+qs+".el_parent p ON p.id=c.parent_id WHERE p.id IS NULL OR NOT p.tracked OR c.expected_payload IS DISTINCT FROM p.payload").Scan(&orphan))
	if orphan != 0 {
		t.Fatal("committed experiment graph contains a wrong exact link")
	}
}
