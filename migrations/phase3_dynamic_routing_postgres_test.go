package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

type phase3MigrationFixture struct {
	ctx  context.Context
	pool *pgxpool.Pool
}

func prefixMust(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

func newPhase3PrefixFixture(t *testing.T) *phase3MigrationFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated phase3 migration test")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	prefixMust(t, "parse dedicated PostgreSQL", err)
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("phase3 migration test requires the dedicated 55439 database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	prefixMust(t, "open migration administrator", err)
	schema := "phase3_backfill_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	prefixMust(t, "create migration schema", err)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	prefixMust(t, "open migration schema", err)
	f := &phase3MigrationFixture{ctx: ctx, pool: pool}
	t.Cleanup(func() {
		pool.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
		cancel()
	})
	return f
}

func (f *phase3MigrationFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, query, args...)
	prefixMust(t, "fixture SQL", err)
}

func TestPhase3MigrationBackfillsLegacyAttemptWithoutScalingFacts(t *testing.T) {
	f := newPhase3PrefixFixture(t)
	names, err := fs.Glob(migrations.Files, "current_*.sql")
	prefixMust(t, "resolve current plan", err)
	sort.Strings(names)
	ordered := []string{"current_mvp.sql"}
	for _, name := range names {
		// 015 backfills route_decisions from this prefix fixture, so it and every
		// later change that assumes installed route tables (016 profit economics,
		// 017 margin basis) apply after the fixture inserts its legacy attempt.
		if name != "current_mvp.sql" && name != "current_015_phase3_dynamic_routing.sql" && name != "current_016_profit_economics.sql" && name != "current_017_margin_basis.sql" {
			ordered = append(ordered, name)
		}
	}
	for _, name := range ordered {
		body, err := migrations.Files.ReadFile(name)
		prefixMust(t, "read current migration", err)
		f.exec(t, string(body))
		f.exec(t, `CREATE TABLE IF NOT EXISTS schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, checksum)
	}
	tenant, user, operator, approver, key, model, channel, version, credential := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	profile := uuid.NewString()
	f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'phase3-backfill')`, tenant)
	for _, item := range []struct{ id, role string }{{user, "none"}, {operator, "finance_operator"}, {approver, "finance_approver"}} {
		f.exec(t, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)`, item.id, item.id+"@fixture.invalid", item.role)
	}
	f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant, user)
	body := `{"version":"` + profile + `","model":"backfill-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	f.exec(t, `SELECT meter_insert_text_model($1,'backfill-model',$2::jsonb,$3)`, model, body, profile)
	f.exec(t, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, model)
	f.exec(t, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'backfill','fixture','digest',$4,1000000)`, key, tenant, user, []string{model})
	meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{ModelID: model, Actor: operator, InputRate: 1000000, OutputRate: 1000000, EffectiveAt: time.Now().UTC().Add(-time.Hour)})
	tx, err := f.pool.Begin(f.ctx)
	prefixMust(t, "open channel fixture transaction", err)
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'backfill',$2)`, []any{channel, version}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'fixture')`, []any{credential, channel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{version, channel, credential}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,1000)`, []any{version, channel, credential}},
	} {
		_, err = tx.Exec(f.ctx, query.sql, query.args...)
		prefixMust(t, "insert channel fixture", err)
	}
	prefixMust(t, "commit channel fixture", tx.Commit(f.ctx))
	for _, item := range []struct{ revision, capacity int }{{1, 1}, {2, 100}} {
		policy := uuid.NewString()
		routeTx, err := f.pool.Begin(f.ctx)
		prefixMust(t, "open route fixture", err)
		_, err = routeTx.Exec(f.ctx, `INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,$3)`, policy, model, item.revision)
		prefixMust(t, "insert route fixture", err)
		_, err = routeTx.Exec(f.ctx, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,$4)`, policy, channel, version, item.capacity)
		prefixMust(t, "insert route binding fixture", err)
		prefixMust(t, "commit route fixture", routeTx.Commit(f.ctx))
	}
	f.exec(t, `UPDATE models SET active_revision=2 WHERE id=$1`, model)
	operation := uuid.NewString()
	store := billing.New(f.pool)
	credit, err := store.CreateCredit(f.ctx, billing.CreditInput{TenantID: tenant, ActorID: operator, Amount: 10000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 10000, BusinessRef: operation, ProofRef: "backfill-fixture", IdempotencyKey: operation, RequestHMAC: operation})
	prefixMust(t, "create backfill funding", err)
	_, err = store.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: tenant, OrderID: credit.ID, ActorID: approver})
	prefixMust(t, "approve backfill funding", err)
	reservation, err := store.Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: tenant, UserID: user, KeyID: key}, RequestID: uuid.NewString(), ModelID: model, RouteRevision: 2, RequestHMAC: strings.Repeat("a", 64), ProfileVersion: profile, PolicyVersion: "retail-r3", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute)})
	prefixMust(t, "reserve pre-033 request", err)
	attempt := uuid.NewString()
	decisions := `[{"channel_id":"` + channel + `","version_id":"` + version + `","priority":0,"weight":1,"reason":"selected"}]`
	attemptTx, err := f.pool.Begin(f.ctx)
	prefixMust(t, "open legacy attempt", err)
	_, err = attemptTx.Exec(f.ctx, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions) VALUES($1,$2,$3,1,$4,$5,$6,'upstream',$7::jsonb)`, attempt, tenant, reservation.RequestID, channel, version, credential, decisions)
	prefixMust(t, "insert legacy attempt", err)
	_, err = attemptTx.Exec(f.ctx, `SELECT meter_insert_attempt_snapshot($1,$2,$3,NULL,'unknown')`, tenant, attempt, reservation.RequestID)
	prefixMust(t, "insert legacy attempt snapshot", err)
	prefixMust(t, "commit legacy attempt", attemptTx.Commit(f.ctx))
	var beforePlan, beforeBounds []byte
	prefixMust(t, "read original attempt vectors", f.pool.QueryRow(f.ctx, `SELECT plan_digest,supplier_bounds_digest FROM attempt_meter_snapshots WHERE attempt_id=$1`, attempt).Scan(&beforePlan, &beforeBounds))
	prefixMust(t, "append phase3 migration", migrations.ApplyCurrent(f.ctx, f.pool))
	prefixMust(t, "verify phase3 current plan", migrations.VerifyCurrent(f.ctx, f.pool))
	var maxConcurrency int
	var algorithm, selectedBinding, selectedPhysical string
	var candidates []byte
	prefixMust(t, "read backfilled decision", f.pool.QueryRow(f.ctx, `SELECT (m.capability->>'max_concurrency')::int,d.algorithm_version,d.selected_binding_id,d.selected_physical_model_id,d.candidates FROM route_decisions d JOIN physical_models m ON m.id=d.selected_physical_model_id WHERE d.attempt_id=$1`, attempt).Scan(&maxConcurrency, &algorithm, &selectedBinding, &selectedPhysical, &candidates))
	var decoded []struct {
		Reason string `json:"reason"`
	}
	if err = json.Unmarshal(candidates, &decoded); err != nil || len(decoded) != 1 || decoded[0].Reason != "selected" {
		t.Fatal("legacy candidate reason changed", err)
	}
	if maxConcurrency != 100 || algorithm != "legacy-v1" || selectedBinding == "" || selectedPhysical == "" {
		t.Fatalf("capacity=%d algorithm=%s binding=%s physical=%s candidates=%s", maxConcurrency, algorithm, selectedBinding, selectedPhysical, candidates)
	}
	var afterPlan, afterBounds []byte
	prefixMust(t, "compare immutable attempt vectors", f.pool.QueryRow(f.ctx, `SELECT plan_digest,supplier_bounds_digest FROM attempt_meter_snapshots WHERE attempt_id=$1`, attempt).Scan(&afterPlan, &afterBounds))
	if !bytes.Equal(beforePlan, afterPlan) || !bytes.Equal(beforeBounds, afterBounds) {
		t.Fatal("migration changed original attempt metering facts")
	}
	var oldColumn bool
	prefixMust(t, "check old decision column removal", f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='upstream_attempts' AND column_name='route_decisions')`).Scan(&oldColumn))
	if oldColumn {
		t.Fatal("legacy attempt decision column remains")
	}
}
