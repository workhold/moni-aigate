// This isolated experiment checks PostgreSQL row-lock privilege semantics, not
// the complete procurement writer, its business DDL, or a production profile.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func main() {
	report, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func code(err error, expected string) bool {
	var p *pgconn.PgError
	return errors.As(err, &p) && p.Code == expected
}
func run() (report map[string]any, resultErr error) {
	ctx, stop := context.WithTimeout(context.Background(), 45*time.Second)
	defer stop()
	raw := os.Getenv("PROCUREMENT_LOCK_ACL_DATABASE_URL")
	if raw == "" {
		return nil, errors.New("PROCUREMENT_LOCK_ACL_DATABASE_URL must identify the dedicated test cluster")
	}
	cfg, err := pgx.ParseConfig(raw)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55439 || cfg.Database != "moni_test" || cfg.User != "moni_test" || len(cfg.Fallbacks) != 0 {
		return nil, errors.New("requires dedicated 127.0.0.1:55439/moni_test as moni_test without fallbacks")
	}
	owner, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("fixture connection failed")
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		resultErr = errors.Join(resultErr, owner.Close(c))
	}()
	var version int
	if err = owner.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil || version < 160000 || version >= 170000 {
		return nil, errors.New("requires PostgreSQL16")
	}
	var entropy [16]byte
	if _, err = rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	suffix := hex.EncodeToString(entropy[:])
	schema, app := "pp_lock_"+suffix, "pp_lock_app_"+suffix
	var secret [24]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return nil, err
	}
	password := hex.EncodeToString(secret[:])
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	table := q(schema) + ".accounts"
	schemaCreated, roleCreated := false, false
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if schemaCreated {
			if _, e := owner.Exec(c, "DROP SCHEMA "+q(schema)+" CASCADE"); e != nil {
				resultErr = errors.Join(resultErr, errors.New("fixture schema cleanup failed"))
			}
		}
		if roleCreated {
			if _, e := owner.Exec(c, "DROP ROLE "+q(app)); e != nil {
				resultErr = errors.Join(resultErr, errors.New("fixture role cleanup failed"))
			}
		}
		var n int
		if e := owner.QueryRow(c, "SELECT (SELECT count(*) FROM pg_namespace WHERE nspname=$1)+(SELECT count(*) FROM pg_roles WHERE rolname=$2)", schema, app).Scan(&n); e != nil || n != 0 {
			resultErr = errors.Join(resultErr, errors.New("fixture cleanup not confirmed"))
		} else if report != nil {
			report["cleanup_confirmed"] = true
		}
	}()
	if _, err = owner.Exec(ctx, "CREATE ROLE "+q(app)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
		return nil, errors.New("fixture role creation failed")
	}
	roleCreated = true
	if _, err = owner.Exec(ctx, "CREATE SCHEMA "+q(schema)); err != nil {
		return nil, err
	}
	schemaCreated = true
	setup := []string{
		"REVOKE ALL ON SCHEMA " + q(schema) + " FROM PUBLIC",
		"GRANT USAGE ON SCHEMA " + q(schema) + " TO " + q(app),
		"CREATE TABLE " + table + "(id text PRIMARY KEY,created_at timestamptz NOT NULL,held bigint NOT NULL)",
		"INSERT INTO " + table + " VALUES('one','2026-01-01T00:00:00Z',7)",
		"CREATE FUNCTION " + q(schema) + ".guard_update() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog,pg_temp AS $guard$ BEGIN RAISE EXCEPTION 'raw update guard' USING ERRCODE='42501'; END $guard$",
		"REVOKE EXECUTE ON FUNCTION " + q(schema) + ".guard_update() FROM PUBLIC",
		"CREATE TRIGGER reject_raw_update BEFORE UPDATE ON " + table + " FOR EACH ROW EXECUTE FUNCTION " + q(schema) + ".guard_update()",
		"GRANT SELECT ON " + table + " TO " + q(app),
	}
	for _, sql := range setup {
		if _, err = owner.Exec(ctx, sql); err != nil {
			return nil, err
		}
	}
	callerCfg := cfg.Copy()
	callerCfg.User = app
	callerCfg.Password = password
	caller, err := pgx.ConnectConfig(ctx, callerCfg)
	if err != nil {
		return nil, errors.New("actual APP connection failed")
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		resultErr = errors.Join(resultErr, caller.Close(c))
	}()
	var login string
	if err = caller.QueryRow(ctx, "SELECT session_user").Scan(&login); err != nil || login != app {
		return nil, errors.New("not actual APP LOGIN")
	}
	locks := []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"}
	for _, lock := range locks {
		var id string
		err = caller.QueryRow(ctx, "SELECT id FROM "+table+" WHERE id='one' FOR "+lock).Scan(&id)
		if !code(err, "42501") {
			return nil, errors.New("SELECT-only APP unexpectedly obtained row-lock authority")
		}
	}
	if _, err = owner.Exec(ctx, "GRANT UPDATE(created_at) ON "+table+" TO "+q(app)); err != nil {
		return nil, err
	}
	var tableUpdate, columnUpdate, moneyUpdate, guardInvoker, restricted bool
	if err = owner.QueryRow(ctx, `SELECT has_table_privilege($1,$2,'UPDATE'),has_column_privilege($1,$2,'created_at','UPDATE'),has_column_privilege($1,$2,'held','UPDATE'),(SELECT NOT prosecdef FROM pg_proc WHERE oid=($3::regprocedure)),(SELECT NOT rolsuper AND NOT rolinherit AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls FROM pg_roles WHERE rolname=$1)`, app, table, q(schema)+".guard_update()").Scan(&tableUpdate, &columnUpdate, &moneyUpdate, &guardInvoker, &restricted); err != nil || tableUpdate || !columnUpdate || moneyUpdate || !guardInvoker || !restricted {
		return nil, errors.New("column-only ACL or guard/role attributes differ")
	}
	for _, lock := range locks {
		var id string
		if err = caller.QueryRow(ctx, "SELECT id FROM "+table+" WHERE id='one' FOR "+lock).Scan(&id); err != nil || id != "one" {
			return nil, errors.New("column grant did not allow expected row lock")
		}
	}
	for _, change := range []string{"created_at=created_at", "created_at=created_at+interval '1 second'"} {
		_, err = caller.Exec(ctx, "UPDATE "+table+" SET "+change+" WHERE id='one'")
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Code != "42501" || p.Message != "raw update guard" {
			return nil, errors.New("raw timestamp update bypassed invoker guard")
		}
	}
	for _, sql := range []string{"UPDATE " + table + " SET held=8", "DELETE FROM " + table, "INSERT INTO " + table + " VALUES('two',clock_timestamp(),0)", "TRUNCATE " + table} {
		if _, err = caller.Exec(ctx, sql); !code(err, "42501") {
			return nil, errors.New("APP unexpectedly acquired financial or structural DML")
		}
	}
	hold, err := caller.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := hold.Rollback(c); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, e)
		}
	}()
	if _, err = hold.Exec(ctx, "SELECT id FROM "+table+" WHERE id='one' FOR NO KEY UPDATE"); err != nil {
		return nil, err
	}
	wait, err := owner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := wait.Rollback(c); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, e)
		}
	}()
	if _, err = wait.Exec(ctx, "SET LOCAL lock_timeout='150ms'"); err != nil {
		return nil, err
	}
	_, err = wait.Exec(ctx, "SELECT id FROM "+table+" WHERE id='one' FOR NO KEY UPDATE")
	if !code(err, "55P03") {
		return nil, errors.New("APP row lock did not block competing account lock")
	}
	if err = wait.Rollback(ctx); err != nil {
		return nil, err
	}
	if err = hold.Rollback(ctx); err != nil {
		return nil, err
	}
	var unchanged bool
	if err = owner.QueryRow(ctx, "SELECT count(*)=1 AND bool_and(id='one' AND held=7 AND created_at='2026-01-01T00:00:00Z'::timestamptz) FROM "+table).Scan(&unchanged); err != nil || !unchanged {
		return nil, errors.New("experiment changed protected state")
	}
	report = map[string]any{"pass": true, "scope": "isolated PG16 row-lock ACL semantics; not complete writer/profile/business acceptance", "actual_app_login": true, "select_only_lock_modes_denied": len(locks), "column_grant_lock_modes_allowed": len(locks), "table_update": tableUpdate, "timestamp_column_update": columnUpdate, "held_column_update": moneyUpdate, "invoker_guard_rejected_timestamp_updates": 2, "other_dml_denied": 4, "competing_lock_sqlstate": "55P03", "lock_timeout_ms": 150, "state_unchanged": unchanged, "restricted_app": restricted, "guard_invoker": guardInvoker}
	return report, nil
}
