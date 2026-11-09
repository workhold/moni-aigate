package operations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestMeterRoleBoundaryWithActualLoginConnections(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema := "meter_login_" + suffix
	roles := []struct {
		name     string
		password string
	}{
		{name: "meter_login_app_" + suffix, password: "app-" + strings.ReplaceAll(uuid.NewString(), "-", "")},
		{name: "meter_login_projection_" + suffix, password: "projection-" + strings.ReplaceAll(uuid.NewString(), "-", "")},
		{name: "meter_login_payment_" + suffix, password: "payment-" + strings.ReplaceAll(uuid.NewString(), "-", "")},
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, role := range roles {
			q := pgx.Identifier{role.name}.Sanitize()
			_, _ = admin.Exec(cleanup, "DROP OWNED BY "+q)
			_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+q)
		}
	})
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		if _, err = admin.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE", pgx.Identifier{role.name}.Sanitize(), role.password)); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = migrations.ApplyCurrent(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = ProvisionFor(ctx, owner, schema, roles[0].name, roles[1].name); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role)
SELECT $1,'meter-login-merchant','meter-login-payee',d.oid,n.oid,$2,$3,$4,$5
FROM pg_catalog.pg_database d CROSS JOIN pg_catalog.pg_namespace n
WHERE d.datname=current_database() AND n.nspname=$6`, uuid.NewString(), strings.Repeat("1", 64), roles[0].name, roles[2].name, roles[1].name, schema); err != nil {
		t.Fatal(err)
	}
	if err = ProvisionPaymentWorkerFor(ctx, owner, schema, roles[0].name, roles[2].name, roles[1].name); err != nil {
		t.Fatal(err)
	}

	connect := func(role struct {
		name     string
		password string
	}) *pgxpool.Pool {
		login := cfg.Copy()
		login.ConnConfig.User = role.name
		login.ConnConfig.Password = role.password
		login.MaxConns = 1
		pool, e := pgxpool.NewWithConfig(ctx, login)
		if e != nil {
			t.Fatalf("connect %s: %v", role.name, e)
		}
		return pool
	}
	app := connect(roles[0])
	projection := connect(roles[1])
	worker := connect(roles[2])
	defer app.Close()
	defer projection.Close()
	defer worker.Close()

	var current string
	if err = app.QueryRow(ctx, "SELECT current_user").Scan(&current); err != nil || current != roles[0].name {
		t.Fatalf("APP connection did not use its LOGIN identity: %q %v", current, err)
	}
	var selectAllowed, insertAllowed, updateAllowed, deleteAllowed bool
	qualified := pgx.Identifier{schema, "meter_schemas"}.Sanitize()
	if err = app.QueryRow(ctx, `SELECT has_table_privilege(current_user,$1,'SELECT'),has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'UPDATE'),has_table_privilege(current_user,$1,'DELETE')`, qualified).Scan(&selectAllowed, &insertAllowed, &updateAllowed, &deleteAllowed); err != nil {
		t.Fatal(err)
	}
	if !selectAllowed || !insertAllowed || updateAllowed || deleteAllowed {
		t.Fatalf("APP meter_schemas privilege mismatch select=%t insert=%t update=%t delete=%t", selectAllowed, insertAllowed, updateAllowed, deleteAllowed)
	}
	if err = app.QueryRow(ctx, "SELECT count(*) FROM "+qualified).Scan(new(int64)); err != nil {
		t.Fatalf("APP could not read its permitted meter table: %v", err)
	}
	if _, err = app.Exec(ctx, "UPDATE "+qualified+" SET name=name"); err == nil {
		t.Fatal("APP unexpectedly updated an immutable meter table")
	}

	for _, restricted := range []*pgxpool.Pool{projection, worker} {
		if _, err = restricted.Exec(ctx, "SELECT count(*) FROM "+qualified); err == nil {
			t.Fatal("restricted role unexpectedly read a meter table")
		}
	}
	var appFunction, projectionFunction, workerFunction bool
	if err = app.QueryRow(ctx, `SELECT has_function_privilege(current_user,$1,'EXECUTE')`, pgx.Identifier{schema}.Sanitize()+`.meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint)`).Scan(&appFunction); err != nil {
		t.Fatal(err)
	}
	if err = projection.QueryRow(ctx, `SELECT has_function_privilege(current_user,$1,'EXECUTE')`, pgx.Identifier{schema}.Sanitize()+`.meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint)`).Scan(&projectionFunction); err != nil {
		t.Fatal(err)
	}
	if err = worker.QueryRow(ctx, `SELECT has_function_privilege(current_user,$1,'EXECUTE')`, pgx.Identifier{schema}.Sanitize()+`.meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint)`).Scan(&workerFunction); err != nil {
		t.Fatal(err)
	}
	if !appFunction || projectionFunction || workerFunction {
		t.Fatalf("meter function privilege mismatch app=%t projection=%t worker=%t", appFunction, projectionFunction, workerFunction)
	}
}
