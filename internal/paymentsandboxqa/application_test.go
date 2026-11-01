package paymentsandboxqa_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/payments"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestApplicationAttestedBuilderActualRolesAndCleanup(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	env, err := paymentsandboxqa.CreateApplication(ctx, dsn, root)
	if err != nil {
		t.Fatal("create application QA", err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if env.Close(cleanup) != nil {
			t.Error("environment cleanup")
		}
	})
	if env.TenantID == env.ApplicationTenantID || len(env.Roles) != 8 || len(env.ActorIDs) != 6 {
		t.Fatal("application environment shape")
	}
	for _, path := range []string{env.ApplicationManifestPath, env.WorkerManifestPath} {
		fi, err := os.Stat(filepath.Dir(path))
		if err != nil || fi.Mode().Perm() != 0700 {
			t.Fatal("private manifest directory")
		}
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatal("read private directory")
		}
		for _, entry := range entries {
			fi, err = entry.Info()
			if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
				t.Fatal("private manifest file")
			}
		}
	}
	raw, err := os.ReadFile(env.ApplicationManifestPath)
	if err != nil {
		t.Fatal("read application manifest")
	}
	for _, forbidden := range []string{"runtime_dsn_file", "api_token_file", "control_token"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("application manifest secret separation")
		}
	}
	raw, err = json.Marshal(env)
	if err != nil || string(raw) != `{"redacted":true}` {
		t.Fatal("redacted JSON")
	}
	if got := fmt.Sprintf("%v %#v", env, env); got != "paymentsandboxqa.ApplicationEnvironment(redacted) paymentsandboxqa.ApplicationEnvironment(redacted)" {
		t.Fatal("redacted formatting")
	}

	a, err := payments.LoadSandboxOptions(ctx, env.ApplicationManifestPath)
	if err != nil {
		t.Fatal("load application options", err)
	}
	w, err := payments.LoadWorkerOptions(ctx, env.WorkerManifestPath)
	if err != nil || payments.ValidateOptionsPair(a, w) != nil {
		t.Fatal("load worker option pair", err)
	}
	appDSN, err := env.RuntimeDSN("app")
	if err != nil {
		t.Fatal("application DSN capability")
	}
	cfg, err := payments.ApplicationPoolConfig(ctx, appDSN, a)
	if err != nil {
		t.Fatal("pinned application pool", err)
	}
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("application pool")
	}
	defer app.Close()
	if migrations.Verify(ctx, app) != nil {
		t.Fatal("complete current migration")
	}
	projectionDSN, err := env.RuntimeDSN("projector")
	if err != nil {
		t.Fatal("projection DSN capability")
	}
	cfg, err = payments.ProjectionPoolConfig(ctx, projectionDSN, a)
	if err != nil {
		t.Fatal("pinned projection pool", err)
	}
	projector, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("projection pool")
	}
	defer projector.Close()
	life := payments.Lifecycle{Mode: payments.ModeSandbox, Guard: func(context.Context) error { return nil }}
	a, err = a.BindLifecycle(life)
	if err != nil {
		t.Fatal("application lifecycle")
	}
	w, err = w.BindLifecycle(life)
	if err != nil {
		t.Fatal("worker lifecycle")
	}
	service, err := payments.NewService(ctx, app, a)
	if err != nil || service == nil || !service.SandboxNotificationsEnabled() {
		t.Fatal("actual application constructor", err)
	}
	processor, err := payments.OpenProcessor(ctx, w)
	if err != nil {
		t.Fatal("actual worker constructor", err)
	}
	if processor.Close(ctx) != nil {
		t.Fatal("processor close")
	}

	for _, role := range []struct {
		component, key string
		db             operations.PaymentDatabaseRole
	}{{"app", "app_runtime", operations.PaymentApplicationRole}, {"worker", "app_worker", operations.PaymentWorkerRole}, {"projector", "app_projector", operations.PaymentProjectionRole}} {
		p, err := env.OpenPool(ctx, role.component)
		if err != nil {
			t.Fatal("open actual role")
		}
		var current, session string
		err = p.QueryRow(ctx, `SELECT current_user,session_user`).Scan(&current, &session)
		if err != nil || current != session || current != env.Roles[role.key] || operations.VerifyPaymentRole(ctx, p, role.db) != nil {
			p.Close()
			t.Fatal("actual LOGIN and permission matrix")
		}
		if role.component == "worker" {
			var valid bool
			if p.QueryRow(ctx, `SELECT funding_uuid('00000000-0000-4000-8000-000000000001')`).Scan(&valid) != nil || !valid {
				p.Close()
				t.Fatal("worker exact funding leaf")
			}
			if _, err = p.Exec(ctx, `SELECT pb_now()`); err == nil {
				p.Close()
				t.Fatal("worker acquired periodic budget capability")
			}
			if _, err = p.Exec(ctx, `SELECT * FROM payment_skus`); err == nil {
				p.Close()
				t.Fatal("worker acquired application configuration")
			}
		}
		if role.component == "projector" {
			for _, table := range []string{"payment_sandbox_environment", "payment_runtime_attestations"} {
				if _, err = p.Exec(ctx, "SELECT * FROM "+pgx.Identifier{table}.Sanitize()); err == nil {
					p.Close()
					t.Fatal("projector acquired private proof")
				}
			}
		}
		p.Close()
	}
	for _, component := range []paymentruntime.Component{paymentruntime.Mock, paymentruntime.Receiver} {
		path := env.MockManifestPath
		if component == paymentruntime.Receiver {
			path = env.ReceiverManifestPath
		}
		options, err := paymentruntime.Load(ctx, path, component)
		if err != nil {
			t.Fatal("original peer manifest", err)
		}
		runtime, err := paymentruntime.Open(ctx, options)
		if err != nil {
			t.Fatal("original peer proof", err)
		}
		if runtime.Check(ctx) != nil || runtime.Close(ctx) != nil {
			t.Fatal("original peer lifecycle")
		}
	}
	passwords := map[string]bool{}
	for actor, id := range env.ActorIDs {
		email, password, err := env.Credentials(actor)
		if err != nil || email == "" || len(password) != 43 || passwords[password] {
			t.Fatal("random actor credentials")
		}
		passwords[password] = true
		var stored, emailDB string
		var mfa bool
		if app.QueryRow(ctx, `SELECT email,password_hash,mfa_enabled FROM users WHERE id=$1`, id).Scan(&emailDB, &stored, &mfa) != nil || emailDB != email || mfa || !security.CheckPassword(stored, password) {
			t.Fatal("hashed optional MFA actor")
		}
	}
	var orders, skus, captures int
	if app.QueryRow(ctx, `SELECT (SELECT count(*) FROM payment_orders),(SELECT count(*) FROM payment_skus),(SELECT count(*) FROM payment_captures)`).Scan(&orders, &skus, &captures) != nil || orders+skus+captures != 0 {
		t.Fatal("builder fabricated payment work")
	}
	if env.VerifyFundsUnchanged(ctx) != nil || env.VerifyLegacyFundingUnchanged(ctx) != nil {
		t.Fatal("retained A funding changed")
	}
	if _, err = app.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target) VALUES($1,$2,'qa.safe_observation',$2)`, env.ActorIDs["admin"], env.ApplicationTenantID); err != nil {
		t.Fatal("independent tenant safe audit")
	}
	if env.VerifyLegacyFundingUnchanged(ctx) != nil {
		t.Fatal("independent tenant contaminated legacy witness")
	}

	// Close observation connections before role/schema removal, as the CLI must.
	app.Close()
	projector.Close()
	if env.Close(ctx) != nil || env.Close(ctx) != nil {
		t.Fatal("idempotent complete environment cleanup")
	}
	if _, _, err = env.Credentials("admin"); err == nil {
		t.Fatal("closed credential capability")
	}
	if _, err = env.RuntimeDSN("worker"); err == nil {
		t.Fatal("closed DSN capability")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("private directory cleanup")
	}
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("cleanup observer")
	}
	defer admin.Close()
	for _, role := range env.Roles {
		var exists bool
		if admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1)`, role).Scan(&exists) != nil || exists {
			t.Fatal("role cleanup")
		}
	}
	for _, schema := range env.Schemas {
		var exists bool
		if admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, schema).Scan(&exists) != nil || exists {
			t.Fatal("schema cleanup")
		}
	}
}
