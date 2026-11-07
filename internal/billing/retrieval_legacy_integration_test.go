package billing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This fixture contains pre-018 bad prices. It verifies their historical
// preservation and 024 refusal, never a current reader on an old schema.
func TestLegacyRetrievalPriceMigration(t *testing.T) {
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for legacy-price migration test")
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
	defer admin.Close()
	schema := "retrieval_legacy_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error(e)
		}
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.ApplyThrough(ctx, pool, "017_funding_vouchers.sql"); err != nil {
		t.Fatal(err)
	}
	actor := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'unused')`, actor, actor+"@legacy.invalid"); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		model, version := uuid.NewString(), uuid.NewString()
		body, err := json.Marshal(map[string]any{"version": version, "model": "legacy-" + model, "protocol": protocol, "input_token_upper_bound": 100, "context_limit": 100, "output_limit": 0, "default_output_limit": 0, "output_limit_field": ""})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO models(id,public_name,profile,profile_version) VALUES($1,$2,$3,$4)`, model, "legacy-"+model, body, version); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1,1,clock_timestamp(),$2)`, model, actor); err != nil {
			t.Fatal(err)
		}
	}
	if err = migrations.ApplyThrough(ctx, pool, "023_wallet_sources.sql"); err != nil {
		t.Fatal(err)
	}
	var model string
	if err = pool.QueryRow(ctx, `SELECT id FROM models ORDER BY id LIMIT 1`).Scan(&model); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,1,1,clock_timestamp(),$2)`, model, actor)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Message != "input-only price requires zero output rate" {
		t.Fatal("018 accepted a new bad price", err)
	}
	if err = migrations.Apply(ctx, pool); err == nil || !strings.Contains(err.Error(), "refuses existing models history") {
		t.Fatal("024 converted legacy retrieval data", err)
	}
	var prices, installed int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM price_versions WHERE output_rate=1),(SELECT count(*) FROM schema_migrations WHERE name='024_unified_text_metering.sql')`).Scan(&prices, &installed); err != nil || prices != 2 || installed != 0 {
		t.Fatal("failed migration changed historical data", prices, installed, err)
	}
}
