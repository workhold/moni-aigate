package catalog_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Create the damaged public price on the real pre-018 schema. No guard is
// disabled, no profile is temporarily switched, and only then are current
// migrations and restricted-role grants installed.
func publicLegacyInputPriceDB(t *testing.T) (*publicFixture, string) {
	t.Helper()
	f := &publicFixture{credentialPoolFixture: credentialPoolDB(t, false), admin: uuid.NewString(), customer: uuid.NewString(), finance: uuid.NewString()}
	if _, err := f.owner.Exec(f.ctx, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
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
		tx, e := f.owner.Begin(f.ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(f.ctx, string(body)); e == nil {
			_, e = tx.Exec(f.ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body)))
		}
		if e != nil {
			_ = tx.Rollback(f.ctx)
			t.Fatal(name, e)
		}
		if e = tx.Commit(f.ctx); e != nil {
			t.Fatal(e)
		}
		count++
	}
	if count != 17 {
		t.Fatal("public legacy baseline must apply exactly 001–017", count)
	}
	for _, user := range []struct{ id, role string }{{f.admin, "admin"}, {f.customer, "none"}, {f.finance, "finance_operator"}} {
		if _, err = f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, user.id, user.id+"@legacy-public.invalid", user.role); err != nil {
			t.Fatal(err)
		}
	}
	input := uuid.NewString()
	// Keep this old bad price private until its own assertion, so it cannot
	// make the independent malformed-profile list checks pass accidentally.
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO models(id,public_name,profile,profile_version,public_visible) VALUES($1,'bad-input-price',$2,'fixture',false)`, input, publicProfileJSON(t, "bad-input-price", provider.ProtocolEmbeddings)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1,1,clock_timestamp()-interval '1 hour',$2)`, input, f.admin); err != nil {
		t.Fatal("pre-018 historical bad price:", err)
	}
	if err = migrations.Apply(f.ctx, f.owner); err != nil {
		t.Fatal("upgrade historical public price:", err)
	}
	if err = migrations.Verify(f.ctx, f.owner); err != nil {
		t.Fatal("verify current migration checksums:", err)
	}
	if err = operations.ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projector); err != nil {
		t.Fatal("provision only after upgrade:", err)
	}
	cfg := f.owner.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, e := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{f.appRole}.Sanitize())
		return e
	}
	if f.app, err = pgxpool.NewWithConfig(f.ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Public projection/publication uses no cryptography or credential writer.
	f.store = catalog.New(f.app, nil)
	return f, input
}
