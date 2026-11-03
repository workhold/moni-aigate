package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
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
	report["cleanup_confirmed"] = true
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (report map[string]any, resultErr error) {
	resultErr = execute(&report)
	return report, resultErr
}
func execute(report *map[string]any) (resultErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := os.Getenv("PROCUREMENT_WRITER_PROBE_DATABASE_URL")
	if base == "" {
		return errors.New("PROCUREMENT_WRITER_PROBE_DATABASE_URL must identify the dedicated test cluster")
	}
	conf, err := pgx.ParseConfig(base)
	if err != nil {
		return errors.New("invalid dedicated test database configuration")
	}
	if conf.Host != "127.0.0.1" || conf.Port != 55439 || conf.Database != "moni_test" || conf.User != "moni_test" || len(conf.Fallbacks) != 0 {
		return errors.New("requires 127.0.0.1:55439/moni_test and moni_test LOGIN, without fallbacks")
	}
	owner, err := pgx.ConnectConfig(ctx, conf)
	if err != nil {
		return errors.New("dedicated database connection failed")
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		resultErr = errors.Join(resultErr, owner.Close(c))
	}()
	var versionNumber int
	if err = owner.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&versionNumber); err != nil {
		return err
	}
	if versionNumber < 160000 || versionNumber >= 170000 {
		return errors.New("requires PostgreSQL 16")
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		return err
	}
	suffix := hex.EncodeToString(b[:])
	schema, writer, app := "bp4_ns_"+suffix, "bp4_wr_"+suffix, "bp4_app_"+suffix
	var secret [24]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return err
	}
	password := hex.EncodeToString(secret[:])
	q := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	exec := func(sql string) error { _, e := owner.Exec(ctx, sql); return e }
	createdWriter, createdApp, createdSchema := false, false, false
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		cleanup := []struct {
			created bool
			sql     string
		}{{createdSchema, "DROP SCHEMA " + q(schema) + " CASCADE"}, {createdApp, "DROP ROLE " + q(app)}, {createdWriter, "DROP ROLE " + q(writer)}}
		for _, step := range cleanup {
			if step.created {
				if _, e := owner.Exec(c, step.sql); e != nil {
					resultErr = errors.Join(resultErr, errors.New("isolated object cleanup failed"))
				}
			}
		}
		var schemas, roles int
		if e := owner.QueryRow(c, "SELECT (SELECT count(*) FROM pg_namespace WHERE nspname=$1),(SELECT count(*) FROM pg_roles WHERE rolname=$2 OR rolname=$3)", schema, app, writer).Scan(&schemas, &roles); e != nil || schemas != 0 || roles != 0 {
			resultErr = errors.Join(resultErr, errors.New("isolated object cleanup not confirmed"))
		}
	}()
	setup := []string{
		"CREATE ROLE " + q(writer) + " NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS",
		"CREATE ROLE " + q(app) + " LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '" + password + "'",
		"CREATE SCHEMA " + q(schema),
		"REVOKE ALL ON SCHEMA " + q(schema) + " FROM PUBLIC",
		"GRANT USAGE ON SCHEMA " + q(schema) + " TO " + q(writer) + "," + q(app),
		"GRANT CREATE ON SCHEMA " + q(schema) + " TO " + q(writer),
		"SET search_path=" + q(schema) + ",pg_catalog,pg_temp",
	}
	for i, sql := range setup {
		if err = exec(sql); err != nil {
			return err
		}
		switch i {
		case 0:
			createdWriter = true
		case 1:
			createdApp = true
		case 2:
			createdSchema = true
		}
	}
	source, err := os.ReadFile("migrations/020_periodic_budgets.sql")
	if err != nil {
		return err
	}
	sources := map[string]string{}
	expected := map[string]string{
		"pb_uuid":                   "8f6909baf84322ec9abc8793625c240dfdcb53b112742f40d0cb95cd131f83ce",
		"pb_require_read_committed": "859c21bc09b4fea5e85f6c07f6c57065ac799ea2ee4033191286b3c77f53529d",
		"pb_scope_sync":             "0d16654e290330ad8aeff44a40a784130f0fffb2540f97bfb65c47f8012bf07d",
	}
	for _, name := range []string{"pb_uuid", "pb_require_read_committed", "pb_scope_sync"} {
		re := regexp.MustCompile(`(?s)CREATE FUNCTION ` + name + `\(.*?END \$\$;`)
		var def string
		if name == "pb_uuid" {
			re = regexp.MustCompile(`(?s)CREATE FUNCTION pb_uuid\(.*?\$\$;`)
		}
		def = re.FindString(string(source))
		if def == "" {
			return fmt.Errorf("missing fixed function %s", name)
		}
		sum := sha256.Sum256([]byte(def))
		sources[name] = hex.EncodeToString(sum[:])
		if sources[name] != expected[name] {
			return fmt.Errorf("fixed original020 function changed: %s", name)
		}
		if err = exec(def); err != nil {
			return err
		}
	}
	for _, variant := range []string{"good", "bad"} {
		path := q(schema) + ",pg_catalog,pg_temp"
		if variant == "bad" {
			path = "pg_catalog," + q(schema) + ",pg_temp"
		}
		sql := "CREATE FUNCTION " + q(schema) + "." + q(variant) + "(target text) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=" + path + " AS $probe$ BEGIN PERFORM " + q(schema) + ".pb_scope_sync('procurement',target,false); RETURN current_schema(); END $probe$"
		if err = exec(sql); err != nil {
			return err
		}
		if err = exec("ALTER FUNCTION " + q(schema) + "." + q(variant) + "(text) OWNER TO " + q(writer)); err != nil {
			return err
		}
		if err = exec("REVOKE ALL ON FUNCTION " + q(schema) + "." + q(variant) + "(text) FROM PUBLIC"); err != nil {
			return err
		}
		if err = exec("GRANT EXECUTE ON FUNCTION " + q(schema) + "." + q(variant) + "(text) TO " + q(app)); err != nil {
			return err
		}
	}
	if err = exec("REVOKE CREATE ON SCHEMA " + q(schema) + " FROM " + q(writer)); err != nil {
		return err
	}
	var restrictedWriter, ownedDefiners bool
	if err = owner.QueryRow(ctx, `SELECT NOT rolcanlogin AND NOT rolinherit AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls AND NOT has_schema_privilege(oid,$2,'CREATE') FROM pg_roles WHERE rolname=$1`, writer, schema).Scan(&restrictedWriter); err != nil || !restrictedWriter {
		return errors.New("isolated writer role does not have the required attributes")
	}
	if err = owner.QueryRow(ctx, `SELECT count(*)=2 AND bool_and(p.prosecdef AND p.proowner=r.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN pg_roles r WHERE n.nspname=$1 AND p.proname IN ('good','bad') AND r.rolname=$2`, schema, writer).Scan(&ownedDefiners); err != nil || !ownedDefiners {
		return errors.New("isolated functions do not have the required owner or definer mode")
	}
	callerConf := conf.Copy()
	callerConf.User = app
	callerConf.Password = password
	caller, err := pgx.ConnectConfig(ctx, callerConf)
	if err != nil {
		return errors.New("isolated APP connection failed")
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		resultErr = errors.Join(resultErr, caller.Close(c))
	}()
	target := "11111111-1111-4111-8111-111111111111"
	var goodSchema, badSchema, login string
	if err = caller.QueryRow(ctx, "SELECT session_user").Scan(&login); err != nil {
		return err
	}
	if login != app {
		return fmt.Errorf("not actual APP login")
	}
	if err = caller.QueryRow(ctx, "SELECT "+q(schema)+".good($1)", target).Scan(&goodSchema); err != nil {
		return err
	}
	hold, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := hold.Rollback(c); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, errors.New("isolated lock transaction cleanup failed"))
		}
	}()
	if _, err = hold.Exec(ctx, "SET LOCAL search_path="+q(schema)+",pg_catalog,pg_temp"); err != nil {
		return err
	}
	if _, err = hold.Exec(ctx, "SELECT "+q(schema)+".pb_scope_sync('procurement',$1,true)", target); err != nil {
		return err
	}
	probe, err := caller.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := probe.Rollback(c); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, errors.New("isolated APP transaction cleanup failed"))
		}
	}()
	if _, err = probe.Exec(ctx, "SET LOCAL lock_timeout='150ms'"); err != nil {
		return err
	}
	start := time.Now()
	err = probe.QueryRow(ctx, "SELECT "+q(schema)+".good($1)", target).Scan(&goodSchema)
	elapsed := time.Since(start)
	var pgerr *pgconn.PgError
	blocked := errors.As(err, &pgerr) && pgerr.Code == "55P03"
	if r := probe.Rollback(ctx); r != nil {
		return r
	}
	if !blocked {
		return fmt.Errorf("good path did not hit expected namespace lock: %v", err)
	}
	if err = caller.QueryRow(ctx, "SELECT "+q(schema)+".bad($1)", target).Scan(&badSchema); err != nil {
		return err
	}
	if goodSchema != schema || badSchema != "pg_catalog" {
		return fmt.Errorf("unexpected current_schema results")
	}
	if err = hold.Rollback(ctx); err != nil {
		return err
	}
	var version string
	if err = owner.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return err
	}
	*report = map[string]any{"scope": "isolated prospective authority design experiment, not B-P4 implementation acceptance", "actual_app_login": true, "writer_nologin": restrictedWriter, "owned_security_definers": ownedDefiners, "good_trusted_schema_namespace": goodSchema == schema, "good_blocked_by_existing_account_namespace": blocked, "good_lock_wait_ms": elapsed.Milliseconds(), "bad_pg_catalog_first_namespace": badSchema, "bad_bypassed_existing_namespace": true, "original020_function_sha256": sources, "postgres_version": version, "pass": true}
	return nil
}
