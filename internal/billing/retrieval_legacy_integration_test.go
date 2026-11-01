package billing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This is an upgrade fixture, not a way to weaken the current price guard.
// The bad price is a committed fact on 001–017, before 018 exists; current
// migrations and role provisioning run before any admission is exercised.
func retrievalLegacyPriceFixture(t *testing.T, protocol string) (*fixture, ReserveInput) {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated legacy-price upgrade test")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("legacy-price fixture requires dedicated local moni_test on port 55439")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	schema, appRole, projectorRole := "retrieval_legacy_"+suffix, "retrieval_app_"+suffix, "retrieval_proj_"+suffix
	var owner, app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if owner != nil {
			owner.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("clean legacy schema:", e)
		}
		for _, role := range []string{appRole, projectorRole} {
			_, _ = admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			if _, e := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); e != nil {
				t.Error("clean legacy role:", e)
			}
		}
		admin.Close()
	})
	for _, q := range []string{
		"CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize(),
		"CREATE ROLE " + pgx.Identifier{appRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER",
		"CREATE ROLE " + pgx.Identifier{projectorRole}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER",
	} {
		if _, err = admin.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	ownerCfg := cfg.Copy()
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = schema
	ownerCfg.MaxConns = 4
	if owner, err = pgxpool.NewWithConfig(ctx, ownerCfg); err != nil {
		t.Fatal(err)
	}
	applyRetrievalLegacyBaseline(t, ctx, owner)
	f := &fixture{ctx: ctx, pool: owner, store: New(owner), tenant: uuid.NewString(), user: uuid.NewString(), key: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), admin: uuid.NewString()}
	if _, err = owner.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,'legacy retrieval')`, f.tenant); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, role string }{{f.user, "none"}, {f.operator, "finance_operator"}, {f.approver, "finance_approver"}, {f.admin, "admin"}} {
		if _, err = owner.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, user.id, user.id+"@legacy.invalid", user.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = owner.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, f.tenant, f.user); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'legacy','test',$1,ARRAY[]::text[],1000)`, f.key, f.tenant, f.user); err != nil {
		t.Fatal(err)
	}
	in := retrievalBillingInput(t, f, protocol, 1, nil)
	f.model = in.ModelID
	if err = migrations.Apply(ctx, owner); err != nil {
		t.Fatal("upgrade legacy price to current migrations:", err)
	}
	if err = migrations.Verify(ctx, owner); err != nil {
		t.Fatal("verify actual upgraded migration checksums:", err)
	}
	if err = operations.ProvisionFor(ctx, owner, schema, appRole, projectorRole); err != nil {
		t.Fatal("provision only after upgrade:", err)
	}
	appCfg := ownerCfg.Copy()
	appCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, e := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
		return e
	}
	if app, err = pgxpool.NewWithConfig(ctx, appCfg); err != nil {
		t.Fatal(err)
	}
	f.pool, f.store = app, New(app)
	f.fund(t, 1000)
	return f, in
}

func applyRetrievalLegacyBaseline(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	count := 0
	for _, name := range names {
		if name >= "018" {
			break
		}
		body, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(ctx, string(body)); e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body)))
		}
		if e != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(name, e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		count++
	}
	if count != 17 {
		t.Fatal("legacy baseline must apply exactly 001–017", count)
	}
}
