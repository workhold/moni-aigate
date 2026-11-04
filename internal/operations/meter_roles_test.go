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
	schema, appRole, projectionRole := "meter_roles_"+suffix, "meter_app_"+suffix, "meter_projection_"+suffix
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, name := range []string{appRole, projectionRole} {
			q := pgx.Identifier{name}.Sanitize()
			_, _ = admin.Exec(cleanup, "DROP OWNED BY "+q)
			_, _ = admin.Exec(cleanup, "DROP ROLE IF EXISTS "+q)
		}
	})
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{appRole, projectionRole} {
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
}
