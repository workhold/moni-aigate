package operations

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestMeterRoleBoundary(t *testing.T) {
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
	schema, appRole, projectionRole, paymentWorkerRole := "meter_roles_"+suffix, "meter_app_"+suffix, "meter_projection_"+suffix, "meter_payment_"+suffix
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, name := range []string{appRole, projectionRole, paymentWorkerRole} {
			q := pgx.Identifier{name}.Sanitize()
			_, _ = admin.Exec(cleanup, "DROP OWNED BY "+q)
			_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+q)
		}
	})
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{appRole, projectionRole, paymentWorkerRole} {
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE"); err != nil {
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
	if err = migrations.Apply(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = ProvisionFor(ctx, owner, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	if err = VerifyMeterRolesFor(ctx, owner, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	for _, table := range meterHistoryTables {
		qualified := pgx.Identifier{schema, table}.Sanitize()
		var workerAny bool
		if err = owner.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT') OR has_table_privilege($1::name,$2::text,'INSERT') OR has_table_privilege($1::name,$2::text,'UPDATE') OR has_table_privilege($1::name,$2::text,'DELETE')`, paymentWorkerRole, qualified).Scan(&workerAny); err != nil {
			t.Fatal(err)
		}
		if workerAny {
			t.Fatalf("payment worker unexpectedly has meter table privilege: %s", table)
		}
	}
	for _, table := range mediaRuntimeTables {
		qualified := pgx.Identifier{schema, table}.Sanitize()
		var appSelect, appInsert, appUpdate, appDelete, projectionAny, workerAny, publicAny bool
		if err = owner.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege($4::name,$2::text,'SELECT') OR has_table_privilege($4::name,$2::text,'INSERT') OR has_table_privilege($4::name,$2::text,'UPDATE') OR has_table_privilege($4::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, qualified, projectionRole, paymentWorkerRole).Scan(&appSelect, &appInsert, &appUpdate, &appDelete, &projectionAny, &workerAny, &publicAny); err != nil {
			t.Fatal(err)
		}
		if !appSelect || !appInsert || !appUpdate || appDelete || projectionAny || workerAny || publicAny {
			t.Fatalf("media runtime role boundary mismatch for %s", table)
		}
	}
	var artifactSelect, artifactInsert, artifactUpdate, artifactDelete, artifactProjection, artifactWorker, artifactPublic bool
	artifactTable := pgx.Identifier{schema, "media_result_artifacts"}.Sanitize()
	if err = owner.QueryRow(ctx, `SELECT has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),has_table_privilege($1::name,$2::text,'DELETE'),has_table_privilege($3::name,$2::text,'SELECT') OR has_table_privilege($3::name,$2::text,'INSERT') OR has_table_privilege($3::name,$2::text,'UPDATE') OR has_table_privilege($3::name,$2::text,'DELETE'),has_table_privilege($4::name,$2::text,'SELECT') OR has_table_privilege($4::name,$2::text,'INSERT') OR has_table_privilege($4::name,$2::text,'UPDATE') OR has_table_privilege($4::name,$2::text,'DELETE'),has_table_privilege('public',$2::text,'SELECT') OR has_table_privilege('public',$2::text,'INSERT') OR has_table_privilege('public',$2::text,'UPDATE') OR has_table_privilege('public',$2::text,'DELETE')`, appRole, artifactTable, projectionRole, paymentWorkerRole).Scan(&artifactSelect, &artifactInsert, &artifactUpdate, &artifactDelete, &artifactProjection, &artifactWorker, &artifactPublic); err != nil {
		t.Fatal(err)
	}
	if !artifactSelect || !artifactInsert || artifactUpdate || artifactDelete || artifactProjection || artifactWorker || artifactPublic {
		t.Fatal("media result artifact role boundary mismatch")
	}
	var workerExecute bool
	if err = owner.QueryRow(ctx, `SELECT COALESCE(bool_or(has_function_privilege($1::name,p.oid,'EXECUTE')),false)
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname=$2 AND p.proname LIKE 'meter\_%' ESCAPE '\'`, paymentWorkerRole, schema).Scan(&workerExecute); err != nil {
		t.Fatal(err)
	}
	if workerExecute {
		t.Fatal("payment worker unexpectedly has meter function execute privilege")
	}
	var invoker, stable, pinned, appExecute, projectorExecute, publicExecute bool
	err = owner.QueryRow(ctx, `SELECT NOT p.prosecdef,p.provolatile='s',
 p.proconfig=ARRAY[format('search_path=%I, pg_catalog, pg_temp',current_schema())],
 has_function_privilege($1::name,p.oid,'EXECUTE'),has_function_privilege($2::name,p.oid,'EXECUTE'),has_function_privilege('public',p.oid,'EXECUTE')
 FROM pg_proc p WHERE p.oid='meter_attempt_supplier_bounds(text,text,text)'::regprocedure`, appRole, projectionRole).Scan(&invoker, &stable, &pinned, &appExecute, &projectorExecute, &publicExecute)
	if err != nil || !invoker || !stable || !pinned || !appExecute || projectorExecute || publicExecute {
		t.Fatal("supplier bounds projection escaped meter role boundary", invoker, stable, pinned, appExecute, projectorExecute, publicExecute, err)
	}
}
