package procurementaccountwriter_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementaccountwriter"
	"github.com/moni-ai/moni-aigate/internal/procurementidentity"
	"github.com/moni-ai/moni-aigate/internal/procurementprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

type fixture struct {
	ctx              context.Context
	owner, app, peer *pgxpool.Pool
	bindings         procurementaccountwriter.Bindings
	admin            string
}

func must(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		var p *pgconn.PgError
		if errors.As(err, &p) {
			t.Fatalf("%s: %v; detail=%s; where=%s; position=%d; internal_position=%d; internal_query=%s", step, err, p.Detail, p.Where, p.Position, p.InternalPosition, p.InternalQuery)
		}
		t.Fatalf("%s: %v", step, err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated actual LOGIN account writer acceptance")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	must(t, "parse dedicated database", err)
	c := cfg.ConnConfig
	if c.Host != "127.0.0.1" || c.Port != 55439 || c.Database != "moni_test" || c.User != "moni_test" || len(c.Fallbacks) != 0 {
		t.Fatal("requires dedicated loopback moni_test LOGIN/database at port 55439 without fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	owner, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	must(t, "connect fixture owner", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	b := procurementaccountwriter.Bindings{Schema: "pp_ac_" + suffix, App: "pp_ac_app_" + suffix, Writer: "pp_ac_w_" + suffix}
	peerName := "pp_ac_peer_" + suffix
	f := &fixture{ctx: ctx, owner: owner, bindings: b, admin: uuid.NewString()}
	q := func(v string) string { return pgx.Identifier{v}.Sanitize() }
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.peer != nil {
			f.peer.Close()
		}
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		defer owner.Close()
		defer cancel()
		if _, err := owner.Exec(cleanup, "DROP SCHEMA IF EXISTS "+q(b.Schema)+" CASCADE"); err != nil {
			t.Errorf("exact fixture schema cleanup: %v", err)
			return
		}
		for _, role := range []string{peerName, b.App, b.Writer} {
			var exists bool
			if err := owner.QueryRow(cleanup, "SELECT EXISTS(SELECT FROM pg_catalog.pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
				t.Error("role cleanup observation failed")
				continue
			}
			if exists {
				if _, err := owner.Exec(cleanup, "DROP ROLE "+q(role)); err != nil {
					t.Errorf("exact fixture role cleanup: %v", err)
				}
			}
		}
		var remains bool
		if err := owner.QueryRow(cleanup, `SELECT EXISTS(SELECT FROM pg_catalog.pg_namespace WHERE nspname=$1)
 OR EXISTS(SELECT FROM pg_catalog.pg_roles WHERE rolname=ANY($2::text[]))`, b.Schema, []string{peerName, b.App, b.Writer}).Scan(&remains); err != nil || remains {
			t.Error("fixture schema or roles remain")
		}
	})
	var version int
	must(t, "read PG version", owner.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("requires PG16")
	}
	for _, role := range []string{b.App, peerName} {
		_, err = owner.Exec(ctx, "CREATE ROLE "+q(role)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
		must(t, "create exact fixture LOGIN", err)
	}
	_, err = owner.Exec(ctx, "CREATE ROLE "+q(b.Writer)+" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
	must(t, "create exact fixture NOLOGIN writer", err)
	must(t, "read APP OID", owner.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", b.App).Scan(&f.bindings.AppOID))
	must(t, "read writer OID", owner.QueryRow(ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1", b.Writer).Scan(&f.bindings.WriterOID))
	_, err = owner.Exec(ctx, "CREATE SCHEMA "+q(b.Schema))
	must(t, "create exact schema", err)
	ownerCfg := cfg.Copy()
	ownerCfg.MaxConns = 2
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = b.Schema
	owned, err := pgxpool.NewWithConfig(ctx, ownerCfg)
	must(t, "open owner schema session", err)
	defer owned.Close()
	must(t, "install original22", migrations.ApplyThrough(ctx, owned, "022_payment_runtime_attestation.sql"))
	bp1, err := periodicbudgetprefixschema.SQL()
	must(t, "authenticate B-P1", err)
	_, err = owned.Exec(ctx, bp1)
	must(t, "install B-P1", err)
	_, err = owned.Exec(ctx, strings.ReplaceAll(procurementidentity.SQLTemplate(), "{{MONI_SCHEMA}}", q(b.Schema)))
	must(t, "install identity codec", err)
	relations, err := procurementprefixschema.SQL(b.Schema)
	must(t, "authenticate relation SQL", err)
	_, err = owned.Exec(ctx, relations)
	must(t, "install relation SQL", err)
	_, err = owned.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-disabled','admin')`, f.admin, f.admin+"@example.invalid")
	must(t, "seed real business administrator", err)
	// A real legacy account is installed before guards; it must remain untracked.
	_, err = owned.Exec(ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES('10000000-0000-0000-0000-000000000099','legacy','fixture','active',1000)`)
	must(t, "seed untracked legacy account", err)
	writer, err := procurementaccountwriter.SQL(f.bindings)
	must(t, "authenticate account writer", err)
	_, err = owned.Exec(ctx, writer)
	must(t, "install account functions and guards", err)
	permissions, err := procurementaccountwriter.Permissions(f.bindings)
	must(t, "authenticate precise account permissions", err)
	_, err = owned.Exec(ctx, permissions)
	must(t, "apply precise account permissions", err)
	_, err = owned.Exec(ctx, "GRANT USAGE ON SCHEMA "+q(b.Schema)+" TO "+q(peerName))
	must(t, "allow peer namespace visibility for EXECUTE denial", err)
	login := func(name string) *pgxpool.Pool {
		cfg := ownerCfg.Copy()
		cfg.ConnConfig.User = name
		cfg.MaxConns = 4
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
		cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
		cfg.ConnConfig.RuntimeParams["TimeZone"] = "Asia/Shanghai"
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		must(t, "connect real restricted LOGIN", err)
		t.Cleanup(pool.Close)
		var session, current string
		must(t, "check actual LOGIN identity", pool.QueryRow(ctx, "SELECT session_user,current_user").Scan(&session, &current))
		if session != name || current != name {
			t.Fatal("fixture did not use actual restricted LOGIN")
		}
		return pool
	}
	f.app = login(b.App)
	f.peer = login(peerName)
	return f
}

type receipt struct {
	Outcome string
	Command procurement.Command
	Root    string
	Journal *string
}

func scanReceipt(row pgx.Row) (receipt, error) {
	var r receipt
	err := row.Scan(&r.Outcome, &r.Command.OperationID, &r.Command.Actor, &r.Command.Kind, &r.Command.TargetID, &r.Command.Status, &r.Command.Reason, &r.Command.Before, &r.Command.After, &r.Command.CreatedAt, &r.Root, &r.Journal)
	return r, err
}

func (f *fixture) create(t *testing.T, in procurement.CreateInput) (receipt, error) {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		return receipt{}, err
	}
	defer tx.Rollback(f.ctx)
	r, err := scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,$3,$4,$5,$6)`, f.admin, in.OperationID, in.Label, in.Provider, in.State, in.LimitAmount))
	if err != nil {
		return receipt{}, err
	}
	if err = tx.Commit(f.ctx); err != nil {
		return receipt{}, err
	}
	return r, nil
}

func (f *fixture) configure(t *testing.T, account string, in procurement.ConfigurationInput) (receipt, error) {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		return receipt{}, err
	}
	defer tx.Rollback(f.ctx)
	r, err := scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_configure_account($1,$2,$3,$4,$5,$6,$7,$8)`, f.admin, in.OperationID, account, in.ExpectedRevision, in.Label, in.Provider, in.State, in.LimitAmount))
	if err != nil {
		return receipt{}, err
	}
	if err = tx.Commit(f.ctx); err != nil {
		return receipt{}, err
	}
	return r, nil
}

func state(t *testing.T, want string, err error) {
	t.Helper()
	var p *pgconn.PgError
	if !errors.As(err, &p) || p.Code != want {
		t.Fatalf("want SQLSTATE %s, got %v", want, err)
	}
	messages := map[string]string{
		"PP001": "invalid_procurement_configuration",
		"PP002": "procurement_operation_conflict",
		"PP003": "procurement_forbidden",
		"PP901": "procurement_execution_forbidden",
		"PP902": "procurement_history_invariant",
		"PP903": "procurement_execution_protocol",
		"PP905": "procurement_history_exhausted",
		"PP906": "procurement_prefix_untracked_domain",
	}
	if message := messages[want]; message != "" && p.Message != message {
		t.Fatalf("SQLSTATE %s has a different fixed safety message: %s", want, p.Message)
	}
}

func TestPostgresActualAccountWriter(t *testing.T) {
	f := newFixture(t)
	in := procurement.CreateInput{OperationID: uuid.NewString(), Label: "团队<&>\u2028A", Provider: "DeepSeek", State: "active", LimitAmount: "1000"}
	created, err := f.create(t, in)
	must(t, "actual APP create and COMMIT", err)
	if created.Outcome != "applied" || created.Command.Status != "applied" || created.Command.Kind != "account_create" || created.Journal == nil || created.Root == "" {
		t.Fatal("wrong applied receipt")
	}
	var account procurement.Account
	must(t, "read real account", f.app.QueryRow(f.ctx, `SELECT id,label,provider,state,limit_amount::text,held_amount::text,spent_amount::text,revision::text,created_at FROM procurement_accounts WHERE id=$1`, created.Command.TargetID).Scan(&account.ID, &account.Label, &account.Provider, &account.State, &account.LimitAmount, &account.HeldAmount, &account.SpentAmount, &account.Revision, &account.CreatedAt))
	account.CreatedAt = account.CreatedAt.UTC()
	wantJSON, err := json.Marshal(account)
	must(t, "marshal independent original Account ABI", err)
	var want, got any
	must(t, "decode original snapshot oracle", json.Unmarshal(wantJSON, &want))
	must(t, "decode actual snapshot", json.Unmarshal(created.Command.After, &got))
	if !reflect.DeepEqual(want, got) || string(created.Command.Before) != "null" {
		t.Fatal("actual original account snapshot differs from independent Go ABI")
	}
	encoded, err := json.Marshal(struct {
		Kind, Target string
		Payload      any
	}{"account_create", "", in})
	must(t, "marshal original command digest ABI", err)
	sum := sha256.Sum256(encoded)
	var actualDigest string
	must(t, "read original digest", f.app.QueryRow(f.ctx, `SELECT input_digest FROM procurement_commands WHERE operation_id=$1`, in.OperationID).Scan(&actualDigest))
	if actualDigest != hex.EncodeToString(sum[:]) {
		t.Fatal("DB command digest differs from original Go identity")
	}
	count := func() int {
		var n int
		must(t, "count sealed roots", f.app.QueryRow(f.ctx, "SELECT count(*) FROM pp_operation_roots").Scan(&n))
		return n
	}
	beforeReplay := count()
	replayed, err := f.create(t, in)
	must(t, "exact replay", err)
	if replayed.Outcome != "replay" || replayed.Root != created.Root || !reflect.DeepEqual(replayed.Command, created.Command) || count() != beforeReplay {
		t.Fatal("replay created history or changed immutable result")
	}
	config := procurement.ConfigurationInput{OperationID: uuid.NewString(), ExpectedRevision: "1", Label: "reconfigured", Provider: "DeepSeek", State: "paused", LimitAmount: "700"}
	changed, err := f.configure(t, account.ID, config)
	must(t, "actual configure and COMMIT", err)
	if changed.Command.Status != "applied" {
		t.Fatal("configuration was not applied")
	}
	encoded, err = json.Marshal(struct {
		Kind, Target string
		Payload      any
	}{"account_configure", account.ID, config})
	must(t, "marshal original configure digest ABI", err)
	sum = sha256.Sum256(encoded)
	must(t, "read actual configure digest", f.app.QueryRow(f.ctx, `SELECT input_digest FROM procurement_commands WHERE operation_id=$1`, config.OperationID).Scan(&actualDigest))
	if actualDigest != hex.EncodeToString(sum[:]) {
		t.Fatal("configure digest differs from independent Go identity")
	}
	config.OperationID = uuid.NewString()
	config.ExpectedRevision = "2"
	_, err = f.configure(t, account.ID, config)
	must(t, "same configuration remains a real command", err)
	config.OperationID = uuid.NewString()
	config.ExpectedRevision = "1"
	rejected, err := f.configure(t, account.ID, config)
	must(t, "stale revision persists rejected command", err)
	if rejected.Outcome != "applied" || rejected.Command.Status != "rejected" || rejected.Command.Reason != "revision_conflict" || rejected.Journal == nil {
		t.Fatal("wrong persistent CAS rejection")
	}
	config.OperationID = uuid.NewString()
	missing, err := f.configure(t, uuid.NewString(), config)
	must(t, "not-found persists rejected command", err)
	if missing.Command.Reason != "not_found" || missing.Journal != nil {
		t.Fatal("not-found fabricated an account journal")
	}
	var ordinal, revision string
	must(t, "read full head and configuration sequence", f.app.QueryRow(f.ctx, `SELECT ordinal::text,configuration_revision::text FROM pp_account_heads WHERE account_id=$1`, account.ID).Scan(&ordinal, &revision))
	if ordinal != "4" || revision != "2" {
		t.Fatal("command ordinal was conflated with configuration revision")
	}
	_, err = f.owner.Exec(f.ctx, "UPDATE "+pgx.Identifier{f.bindings.Schema, "users"}.Sanitize()+" SET status='suspended' WHERE id=$1", f.admin)
	must(t, "suspend original business actor", err)
	_, err = f.create(t, in)
	state(t, "PP003", err)
	different := in
	different.Label = "different"
	_, err = f.create(t, different)
	state(t, "PP002", err)
	_, err = f.owner.Exec(f.ctx, "UPDATE "+pgx.Identifier{f.bindings.Schema, "users"}.Sanitize()+" SET status='active' WHERE id=$1", f.admin)
	must(t, "restore business actor", err)
	config.OperationID = uuid.NewString()
	_, err = f.configure(t, "10000000-0000-0000-0000-000000000099", config)
	state(t, "PP906", err)
	t.Run("permission_attacks", func(t *testing.T) { permissionAttacks(t, f, account.ID) })
	t.Run("same_transaction_roots_and_rollback", func(t *testing.T) {
		tx, err := f.app.Begin(f.ctx)
		must(t, "begin multiroot transaction", err)
		defer tx.Rollback(f.ctx)
		for i := 0; i < 2; i++ {
			_, err := scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,'parallel','fixture','active',1)`, f.admin, uuid.NewString()))
			must(t, "independent root in same top transaction", err)
		}
		must(t, "flush real APP deferred closure", func() error { _, err := tx.Exec(f.ctx, "SET CONSTRAINTS ALL IMMEDIATE"); return err }())
		must(t, "rollback all effects and evidence", tx.Rollback(f.ctx))
		if count() != 5 {
			t.Fatal("rolled-back roots were retained")
		}
	})
}

func permissionAttacks(t *testing.T, f *fixture, account string) {
	for _, tc := range []struct {
		name, sql, code string
		args            []any
	}{
		{"raw configuration", `UPDATE procurement_accounts SET state='active' WHERE id=$1`, "42501", []any{account}},
		{"lock column write", `UPDATE procurement_accounts SET created_at=created_at WHERE id=$1`, "PP901", []any{account}},
		{"private helper", `SELECT pp_ac_require_root_v1()`, "42501", nil},
		{"set writer", "SET ROLE " + pgx.Identifier{f.bindings.Writer}.Sanitize(), "42501", nil},
		{"schema create", "CREATE TABLE " + pgx.Identifier{f.bindings.Schema, "attack"}.Sanitize() + "(id int)", "42501", nil},
		{"source truncate", `TRUNCATE pp_account_command_sources`, "42501", nil},
	} {
		t.Run(tc.name, func(t *testing.T) { _, err := f.app.Exec(f.ctx, tc.sql, tc.args...); state(t, tc.code, err) })
	}
	_, err := f.peer.Exec(f.ctx, `SELECT * FROM pp_create_account($1,$2,'peer','fixture','active',1)`, f.admin, uuid.NewString())
	state(t, "42501", err)
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	must(t, "begin RR attack", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, `SELECT * FROM pp_create_account($1,$2,'rr','fixture','active',1)`, f.admin, uuid.NewString())
	state(t, "PP903", err)
	owner, err := f.owner.Acquire(f.ctx)
	must(t, "acquire owner spoof session", err)
	defer owner.Release()
	_, err = owner.Exec(f.ctx, "SET ROLE "+pgx.Identifier{f.bindings.App}.Sanitize())
	must(t, "owner SET ROLE APP for negative test", err)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := owner.Exec(cleanup, "RESET ROLE"); err != nil {
			_ = owner.Conn().Close(cleanup)
		}
	}()
	_, err = owner.Exec(f.ctx, "SELECT * FROM "+pgx.Identifier{f.bindings.Schema, "pp_create_account"}.Sanitize()+"($1,$2,'spoof','fixture','active',1)", f.admin, uuid.NewString())
	state(t, "PP901", err)
}

func waitForBlock(f *fixture, blocked, blocker uint32) error {
	ctx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	for {
		var observed bool
		if err := f.owner.QueryRow(ctx, `SELECT $2::integer = ANY(pg_catalog.pg_blocking_pids($1::integer))`, blocked, blocker).Scan(&observed); err != nil {
			return err
		}
		if observed {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// Both sessions are actual restricted APP LOGINs. The first root remains
// uncommitted until pg_blocking_pids proves that the second has reached its
// advisory/account lock. No sleeps stand in for evidence of contention.
func TestPostgresAccountWriterConcurrency(t *testing.T) {
	f := newFixture(t)
	in := procurement.CreateInput{OperationID: uuid.NewString(), Label: "race", Provider: "fixture", State: "active", LimitAmount: "1000"}
	t.Run("operation_lock_returns_original_receipt", func(t *testing.T) {
		first, err := f.app.Begin(f.ctx)
		must(t, "begin first APP", err)
		defer first.Rollback(f.ctx)
		second, err := f.app.Begin(f.ctx)
		must(t, "begin second APP", err)
		defer second.Rollback(f.ctx)
		created, err := scanReceipt(first.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,$3,$4,$5,$6)`, f.admin, in.OperationID, in.Label, in.Provider, in.State, in.LimitAmount))
		must(t, "create uncommitted original root", err)
		type result struct {
			receipt receipt
			err     error
		}
		done := make(chan result, 1)
		raceCtx, cancel := context.WithCancel(f.ctx)
		finished := make(chan struct{})
		defer func() { cancel(); <-finished }()
		firstPID, secondPID := first.Conn().PgConn().PID(), second.Conn().PgConn().PID()
		go func() {
			defer close(finished)
			r, e := scanReceipt(second.QueryRow(raceCtx, `SELECT * FROM pp_create_account($1,$2,$3,$4,$5,$6)`, f.admin, in.OperationID, in.Label, in.Provider, in.State, in.LimitAmount))
			if e == nil {
				e = second.Commit(raceCtx)
			}
			done <- result{r, e}
		}()
		blocked := waitForBlock(f, secondPID, firstPID)
		if blocked != nil {
			_ = first.Rollback(f.ctx)
			<-done
			must(t, "observe real operation lock", blocked)
		}
		must(t, "commit original root", first.Commit(f.ctx))
		got := <-done
		must(t, "commit concurrent replay", got.err)
		if got.receipt.Outcome != "replay" || got.receipt.Root != created.Root || !reflect.DeepEqual(got.receipt.Command, created.Command) {
			t.Fatal("concurrent operation invented a second receipt")
		}
	})
	t.Run("account_lock_rechecks_committed_revision", func(t *testing.T) {
		created, err := f.create(t, in)
		must(t, "read original receipt", err)
		first, err := f.app.Begin(f.ctx)
		must(t, "begin first configure", err)
		defer first.Rollback(f.ctx)
		second, err := f.app.Begin(f.ctx)
		must(t, "begin second configure", err)
		defer second.Rollback(f.ctx)
		a, err := scanReceipt(first.QueryRow(f.ctx, `SELECT * FROM pp_configure_account($1,$2,$3,1,'winner','fixture','active',900)`, f.admin, uuid.NewString(), created.Command.TargetID))
		must(t, "apply first revision", err)
		type result struct {
			receipt receipt
			err     error
		}
		done := make(chan result, 1)
		raceCtx, cancel := context.WithCancel(f.ctx)
		finished := make(chan struct{})
		defer func() { cancel(); <-finished }()
		firstPID, secondPID := first.Conn().PgConn().PID(), second.Conn().PgConn().PID()
		go func() {
			defer close(finished)
			r, e := scanReceipt(second.QueryRow(raceCtx, `SELECT * FROM pp_configure_account($1,$2,$3,1,'stale','fixture','active',800)`, f.admin, uuid.NewString(), created.Command.TargetID))
			if e == nil {
				e = second.Commit(raceCtx)
			}
			done <- result{r, e}
		}()
		blocked := waitForBlock(f, secondPID, firstPID)
		if blocked != nil {
			_ = first.Rollback(f.ctx)
			<-done
			must(t, "observe real account lock", blocked)
		}
		must(t, "commit winning configuration", first.Commit(f.ctx))
		got := <-done
		must(t, "commit stale rejection", got.err)
		if a.Command.Status != "applied" || got.receipt.Command.Status != "rejected" || got.receipt.Command.Reason != "revision_conflict" {
			t.Fatal("waiting configuration did not re-read the winning revision")
		}
		var label, ordinal, revision string
		must(t, "read final account prefix", f.app.QueryRow(f.ctx, `SELECT label,ordinal::text,configuration_revision::text FROM pp_account_heads WHERE account_id=$1`, created.Command.TargetID).Scan(&label, &ordinal, &revision))
		if label != "winner" || ordinal != "3" || revision != "2" {
			t.Fatal("concurrent configuration lost or duplicated a transition")
		}
	})
}

func TestPostgresAccountWriterFailureBoundaries(t *testing.T) {
	f := newFixture(t)
	t.Run("commit_requires_actual_app_readonly_validator", func(t *testing.T) {
		tx, err := f.app.Begin(f.ctx)
		must(t, "begin revocation transaction", err)
		defer tx.Rollback(f.ctx)
		op := uuid.NewString()
		_, err = scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,'revoke','fixture','active',1)`, f.admin, op))
		must(t, "complete root before deferred validation", err)
		fn := pgx.Identifier{f.bindings.Schema, "pp_ac_validate_root_v1"}.Sanitize() + "(text)"
		role := pgx.Identifier{f.bindings.App}.Sanitize()
		_, err = f.owner.Exec(f.ctx, "REVOKE EXECUTE ON FUNCTION "+fn+" FROM "+role)
		must(t, "revoke exact APP validator", err)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := f.owner.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+fn+" TO "+role)
			if err != nil {
				t.Error("restore exact APP validator grant:", err)
			}
		}()
		state(t, "42501", tx.Commit(f.ctx))
		var commands, roots, accounts int
		must(t, "observe rollback after COMMIT rejection", f.app.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM procurement_commands WHERE operation_id=$1),(SELECT count(*) FROM pp_operation_roots),(SELECT count(*) FROM procurement_accounts WHERE pp_version IS NOT NULL)`, op).Scan(&commands, &roots, &accounts))
		if commands != 0 || roots != 0 || accounts != 0 {
			t.Fatal("deferred permission failure retained effects or evidence")
		}
	})
	t.Run("timeouts_are_frontend_preconditions", func(t *testing.T) {
		for _, setting := range []string{"SET LOCAL statement_timeout='0'", "SET LOCAL statement_timeout='6s'", "SET LOCAL lock_timeout='0'", "SET LOCAL lock_timeout='4s'"} {
			tx, err := f.app.Begin(f.ctx)
			must(t, "begin protocol rejection", err)
			_, err = tx.Exec(f.ctx, setting)
			must(t, "set rejected frontend protocol", err)
			_, err = tx.Exec(f.ctx, `SELECT * FROM pp_create_account($1,$2,'protocol','fixture','active',1)`, f.admin, uuid.NewString())
			state(t, "PP903", err)
			must(t, "rollback rejected protocol", tx.Rollback(f.ctx))
		}
	})
	t.Run("ordinary_audit_retains_explicit_timestamp", func(t *testing.T) {
		when := time.Date(2026, 11, 3, 1, 2, 3, 4000, time.UTC)
		var actual time.Time
		must(t, "ordinary APP audit INSERT including PB timestamp surface", f.app.QueryRow(f.ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff,created_at) VALUES($1,'fixture.ordinary','fixture','{}',$2) RETURNING created_at`, f.admin, when).Scan(&actual))
		if !actual.Equal(when) {
			t.Fatal("ordinary audit lost its timestamp")
		}
	})
	t.Run("sql_null_input_rejected_without_history", func(t *testing.T) {
		_, err := f.app.Exec(f.ctx, `SELECT * FROM pp_create_account($1,$2,NULL,'fixture','active',1)`, f.admin, uuid.NewString())
		state(t, "PP001", err)
		var n int
		must(t, "read root count after input rejection", f.app.QueryRow(f.ctx, `SELECT count(*) FROM pp_operation_roots`).Scan(&n))
		if n != 0 {
			t.Fatal("input or protocol rejection retained history")
		}
	})
}
