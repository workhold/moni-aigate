package catalog_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Create the damaged public price on the real pre-018 schema. No guard is
// disabled and no profile is temporarily switched. This fixture stops at 023;
// 024 must reject its history rather than silently convert it.
func publicLegacyInputPriceDB(t *testing.T) (*publicFixture, string) {
	t.Helper()
	f := &publicFixture{credentialPoolFixture: credentialPoolDB(t, false), admin: uuid.NewString(), customer: uuid.NewString(), finance: uuid.NewString()}
	if err := migrations.ApplyThrough(f.ctx, f.owner, "017_funding_vouchers.sql"); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, role string }{{f.admin, "admin"}, {f.customer, "none"}, {f.finance, "finance_operator"}} {
		if _, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused',$3)`, user.id, user.id+"@legacy-public.invalid", user.role); err != nil {
			t.Fatal(err)
		}
	}
	input := uuid.NewString()
	// Keep this old bad price private until its own assertion, so it cannot
	// make the independent malformed-profile list checks pass accidentally.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO models(id,public_name,profile,profile_version,public_visible) VALUES($1,'bad-input-price',$2,'fixture',false)`, input, publicProfileJSON(t, "bad-input-price", provider.ProtocolEmbeddings)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1,1,clock_timestamp()-interval '1 hour',$2)`, input, f.admin); err != nil {
		t.Fatal("pre-018 historical bad price:", err)
	}
	if err := migrations.ApplyThrough(f.ctx, f.owner, "023_wallet_sources.sql"); err != nil {
		t.Fatal("upgrade historical public price:", err)
	}
	if err := migrations.VerifyThrough(f.ctx, f.owner, "023_wallet_sources.sql"); err != nil {
		t.Fatal("verify historical migration checksums:", err)
	}
	return f, input
}

func TestPublicCatalogLegacyInputPriceUpgradeRefusesUnifiedMetering(t *testing.T) {
	f, input := publicLegacyInputPriceDB(t)
	_, err := f.owner.Exec(f.ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1,1,clock_timestamp(),$2)`, input, f.admin)
	var invalid *pgconn.PgError
	if !errors.As(err, &invalid) || invalid.Message != "input-only price requires zero output rate" {
		t.Fatal("018 did not reject a new malformed input-only price", err)
	}
	if err = migrations.Apply(f.ctx, f.owner); err == nil || !strings.Contains(err.Error(), "refuses existing models history") {
		t.Fatal("024 converted historical catalog data", err)
	}
	var historical, installed int
	if err = f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM price_versions WHERE model_id=$1 AND output_rate=1),(SELECT count(*) FROM schema_migrations WHERE name='024_unified_text_metering.sql')`, input).Scan(&historical, &installed); err != nil || historical != 1 || installed != 0 {
		t.Fatal("failed upgrade changed original history", historical, installed, err)
	}
}
