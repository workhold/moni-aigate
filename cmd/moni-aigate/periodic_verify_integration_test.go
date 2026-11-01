package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Invoke the actual main/exit path without serving configuration or secrets.
func TestPeriodicVerificationCLIChild(t *testing.T) {
	if os.Getenv("MONI_PERIODIC_CLI_TEST_CHILD") != "1" {
		return
	}
	os.Args = []string{"moni-aigate", "verify-periodic-budgets"}
	main()
	os.Exit(0)
}

func TestPeriodicVerificationCLIPostgres(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated CLI PostgreSQL verification")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("CLI tests require the dedicated local moni_test database on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	if err != nil {
		t.Fatal("open isolated CLI administrator failed")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, appRole, projectorRole := "pb_cli_"+suffix, "pb_cli_a_"+suffix, "pb_cli_p_"+suffix
	var owner, app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if owner != nil {
			owner.Close()
		}
		clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, e := admin.Exec(clean, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("exact CLI schema cleanup failed")
		}
		for _, role := range []string{appRole, projectorRole} {
			if _, e := admin.Exec(clean, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("exact CLI role cleanup failed")
			}
		}
		admin.Close()
	})
	must := func(stage string, e error) {
		t.Helper()
		if e != nil {
			t.Fatal(stage + " failed")
		}
	}
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	must("create isolated CLI schema", err)
	cfg = cfg.Copy()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	owner, err = pgxpool.NewWithConfig(ctx, cfg.Copy())
	must("open isolated CLI owner", err)
	must("apply complete latest CLI migrations", migrations.Apply(ctx, owner))
	for _, role := range []string{appRole, projectorRole} {
		_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS")
		must("create actual CLI runtime LOGIN", err)
	}
	must("provision actual CLI runtime roles", operations.ProvisionFor(ctx, owner, schema, appRole, projectorRole))
	appCfg := cfg.Copy()
	appCfg.ConnConfig.User, appCfg.ConnConfig.Password = appRole, ""
	app, err = pgxpool.NewWithConfig(ctx, appCfg)
	must("open actual CLI APP", err)
	// ConnString preserves the original parse input; it does not serialize
	// changes made to RuntimeParams/User on a pgx configuration copy.
	u, err := url.Parse(raw)
	must("parse dedicated CLI URL", err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	ownerDSN := u.String()
	u.User = url.User(appRole)
	appDSN := u.String()

	runCLI := func(dsn, status string, code int, safe string) {
		t.Helper()
		binary, e := os.Executable()
		must("locate CLI test executable", e)
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestPeriodicVerificationCLIChild$")
		// No serving keys, settings, workers or test-owner connection reach main.
		cmd.Env = []string{"MONI_PERIODIC_CLI_TEST_CHILD=1", "DATABASE_URL=" + dsn}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		e = cmd.Run()
		exit := 0
		if e != nil {
			failure, ok := e.(*exec.ExitError)
			if !ok {
				t.Fatal("CLI child could not execute")
			}
			exit = failure.ExitCode()
		}
		if exit != code {
			t.Fatalf("CLI expected exit %d, received %d", code, exit)
		}
		if code == 1 {
			if stdout.Len() != 0 || stderr.String() != "periodic_budget_verification_failed:"+safe+"\n" {
				t.Fatal("failed CLI emitted a partial report or unsafe error")
			}
			return
		}
		var report periodicbudget.Verification
		decoder := json.NewDecoder(&stdout)
		if decoder.Decode(&report) != nil || report.Status != periodicbudget.VerificationStatus(status) || stderr.Len() != 0 {
			t.Fatal("CLI finding envelope invalid")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			t.Fatal("CLI emitted additional stdout")
		}
	}
	runCLI(ownerDSN, "balanced", 0, "")
	runCLI(appDSN, "", 1, "authority")

	tenant, actor := uuid.NewString(), uuid.NewString()
	_, err = owner.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'isolated CLI fixture')`, tenant)
	must("seed isolated CLI tenant", err)
	_, err = owner.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only','admin')`, actor, actor+"@cli.invalid")
	must("seed isolated CLI actor", err)
	commander, err := periodicbudget.NewCommander(app)
	must("construct actual APP commander", err)
	result, err := commander.ConfigurePlatformTenant(ctx, actor, tenant, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_day", Enabled: true, LimitAmount: "0"})
	must("configure actual APP periodic control", err)
	runCLI(ownerDSN, "balanced", 0, "")
	_, err = app.Exec(ctx, `SELECT pb_gate($1,pb_now(),0)`, result.Receipt.Operation.ResourceID)
	must("create legitimate empty current window", err)
	runCLI(ownerDSN, "indeterminate", 3, "")
	_, err = owner.Exec(ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'periodic_budget.configured',$2,'{}')`, actor, uuid.NewString())
	must("seed exact isolated orphan audit", err)
	runCLI(ownerDSN, "mismatch", 2, "")
	_, err = owner.Exec(ctx, `UPDATE schema_migrations SET checksum='isolated-bad-checksum' WHERE name='020_periodic_budgets.sql'`)
	must("damage isolated migration checksum", err)
	runCLI(ownerDSN, "", 1, "schema")
}
