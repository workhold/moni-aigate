package pricing_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/pricing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Current selection tests use an empty current database and the provisioned APP.
// The pre-018 history fixture above is not silently upgraded into a vector DB.
type vectorFixture struct{ *fixture }

func newVectorFixture(t *testing.T) *vectorFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for current vector pricing tests")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, appRole, projectionRole := "vector_pricing_"+suffix, "vp_app_"+suffix, "vp_projector_"+suffix
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		owner.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		for _, role := range []string{appRole, projectionRole} {
			_, _ = admin.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
			_, _ = admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
		}
		admin.Close()
	})
	if err = migrations.ApplyCurrent(ctx, owner); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, pool: owner, owner: owner, actor: uuid.NewString(), tenant: uuid.NewString(), model: uuid.NewString(), defaultPrice: uuid.NewString(), schema: schema}
	f.exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused','admin')`, f.actor, f.actor+"@example.invalid")
	f.exec(`INSERT INTO tenants(id,name) VALUES($1,'vector pricing')`, f.tenant)
	profile := uuid.NewString()
	f.exec(`SELECT meter_insert_text_model($1,'vector-model',jsonb_build_object('version',$2::text,'model','vector-model','protocol','openai_chat','input_token_upper_bound',1000,'context_limit',1000,'output_limit',1000,'default_output_limit',1000,'output_limit_field','max_tokens'),$2)`, f.model, profile)
	f.defaultPrice = meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{
		ID: f.defaultPrice, ModelID: f.model, Actor: f.actor, InputRate: 10, OutputRate: 20,
		EffectiveAt: time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond),
	})
	for _, role := range []string{appRole, projectionRole} {
		f.exec("CREATE ROLE " + pgx.Identifier{role}.Sanitize() + " NOLOGIN NOINHERIT NOSUPERUSER")
	}
	if err = operations.ProvisionFor(ctx, owner, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	appCfg := owner.Config()
	appCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
		return err
	}
	app, err = pgxpool.NewWithConfig(ctx, appCfg)
	if err != nil {
		t.Fatal(err)
	}
	var role string
	var super bool
	if err = app.QueryRow(ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&role, &super); err != nil || role != appRole || super {
		t.Fatal("not the restricted pricing APP", role, super, err)
	}
	f.pool = app
	return &vectorFixture{f}
}

func (f *vectorFixture) createGroup(label string) string {
	f.t.Helper()
	r, err := catalog.New(f.pool, nil).CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.actor}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: label})
	if err != nil {
		f.t.Fatal(err)
	}
	return r.Result.ID
}

func (f *vectorFixture) assign(group *string, revision int64) {
	f.t.Helper()
	_, err := catalog.New(f.pool, nil).AssignTenantPricing(f.ctx, catalog.PricingActor{UserID: f.actor}, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: revision, GroupID: group})
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *vectorFixture) publish(group, target string, at time.Time) (string, error) {
	return f.publishRates(group, target, at, 10, 20)
}

func (f *vectorFixture) publishRates(group, target string, at time.Time, input, output int64) (string, error) {
	if group != target {
		f.t.Fatal("current selection fixture cannot publish a cross-target price")
	}
	r, err := catalog.New(f.pool, nil).PublishGroupPrice(f.ctx, catalog.PricingActor{UserID: f.actor}, meterquote.BindGroup(f.t, f.ctx, f.pool, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group, ModelID: f.model, InputRate: input, OutputRate: output, EffectiveAt: at}))
	return r.Result.ID, err
}

func selectionRates(t *testing.T, selected *pricing.Selection, input, output int64) bool {
	t.Helper()
	if selected == nil {
		return false
	}
	expected, err := metering.ManagementTokenRates(selected.Schema, metering.Retail, input, output)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := expected.CanonicalJSON()
	got, _ := selected.Rates.CanonicalJSON()
	return bytes.Equal(got, want)
}

func vectorModel(t *testing.T, f *vectorFixture, input int64) string {
	t.Helper()
	model, profile := uuid.NewString(), uuid.NewString()
	f.exec(`SELECT meter_insert_text_model($1,$1,jsonb_build_object('version',$2::text,'model',$1::text,'protocol','openai_chat','input_token_upper_bound',$3::bigint,'context_limit',$3::bigint,'output_limit',20,'default_output_limit',20,'output_limit_field','max_tokens'),$2)`, model, profile, input)
	return model
}

// Raw inserts exercise the read gate before deferred completeness checks. No
// immutable row or constraint is modified, even for intentionally invalid data.
func vectorRawPrice(t *testing.T, f *vectorFixture, tx pgx.Tx, model, source string, mutate func([]byte, []byte) ([]byte, []byte)) string {
	t.Helper()
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	if _, err := tx.Exec(f.ctx, `SET LOCAL session_replication_role='replica'`); err != nil {
		t.Fatal(err)
	}
	var canonical, digest []byte
	if err := tx.QueryRow(f.ctx, `SELECT rates_canonical,rates_digest FROM price_versions WHERE id=$1`, source).Scan(&canonical, &digest); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		canonical, digest = mutate(canonical, digest)
	}
	id := uuid.NewString()
	mustExec(t, f.ctx, tx, `INSERT INTO price_versions(id,model_id,currency,effective_at,created_by,created_at,price_group_id,pricing_operation_id,schema_id,schema_digest,variant_key,rates_canonical,rates_digest)
 SELECT $1,$2,'COIN',clock_timestamp(),$3,clock_timestamp(),NULL,$4,schema_id,schema_digest,variant_key,$5,$6 FROM price_versions WHERE id=$7`, id, model, f.actor, uuid.NewString(), canonical, digest, source)
	mustExec(t, f.ctx, tx, `INSERT INTO price_rate_lines(price_version_id,dimension,unit,numerator,denominator)
 SELECT $1,dimension,unit,numerator,denominator FROM price_rate_lines WHERE price_version_id=$2`, id, source)
	return id
}

func TestVectorSelectionIgnoresForeignSchema(t *testing.T) {
	f := newVectorFixture(t)
	foreign := vectorModel(t, f, 80)
	foreignPrice := meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{
		ID: uuid.NewString(), ModelID: foreign, Actor: f.actor, InputRate: 9000000, OutputRate: 9000000,
		EffectiveAt: time.Now().UTC().Truncate(time.Microsecond),
	})
	empty := vectorModel(t, f, 200)
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	vectorRawPrice(t, f, tx, f.model, foreignPrice, nil)
	vectorRawPrice(t, f, tx, empty, foreignPrice, nil)
	mustCommit(t, f.ctx, tx)
	tx = f.tx()
	selected, _, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, pricing.Domain{TenantRevision: 1})
	if err != nil || selected == nil || selected.VersionID != f.defaultPrice || !selectionRates(t, selected, 10, 20) {
		t.Fatal("newer foreign schema replaced the original price", selected, err)
	}
	missing, at, err := pricing.SelectEffectiveTx(f.ctx, tx, empty, pricing.Domain{TenantRevision: 1})
	if err != nil || missing != nil || at.IsZero() {
		t.Fatal("foreign schema filled a domain with no price", missing, at, err)
	}
	meterquote.PublishDefaultPrice(t, f.ctx, f.pool, meterquote.DefaultPrice{
		ID: uuid.NewString(), ModelID: empty, Actor: f.actor, InputRate: 0, OutputRate: 0,
		EffectiveAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond),
	})
	missing, at, err = pricing.SelectEffectiveTx(f.ctx, tx, empty, pricing.Domain{TenantRevision: 1})
	if err != nil || missing != nil || at.IsZero() {
		t.Fatal("foreign price or future current price became effective", missing, at, err)
	}
}

func TestVectorSelectionRejectsInvalidCanonical(t *testing.T) {
	f := newVectorFixture(t)
	for _, tc := range []struct{ name, from, to string }{
		{"whitespace", `{`, `{ `},
		{"side", `"side":"retail"`, `"side":"supplier"`},
		{"currency", `"currency":"CNY"`, `"currency":"USD"`},
		{"version", `"version":"1"`, `"version":"2"`},
		{"dimension", `text_output_token`, `request_count`},
		{"unit", `"unit":"token"`, `"unit":"byte"`},
		{"negative", `"numerator":"1"`, `"numerator":"-1"`},
		{"binding", `"schema_id":"`, `"schema_id":"invalid-`},
		{"digest", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.owner.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			vectorRawPrice(t, f, tx, f.model, f.defaultPrice, func(canonical, digest []byte) ([]byte, []byte) {
				if tc.name == "digest" {
					digest[0] ^= 1
					return canonical, digest
				}
				changed := []byte(strings.Replace(string(canonical), tc.from, tc.to, 1))
				if bytes.Equal(changed, canonical) {
					t.Fatal("negative fixture did not change canonical bytes")
				}
				return changed, digest
			})
			selected, at, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, pricing.Domain{TenantRevision: 1})
			var pe *pricing.Error
			if selected != nil || !at.IsZero() || !errors.As(err, &pe) || pe.Code != "pricing_invariant" {
				t.Fatal("invalid current price fell back, became zero, or escaped", selected, at, err)
			}
		})
	}
	tx := f.tx()
	selected, _, err := pricing.SelectEffectiveTx(f.ctx, tx, f.model, pricing.Domain{TenantRevision: 1})
	if err != nil || selected == nil || selected.VersionID != f.defaultPrice {
		t.Fatal("negative rollback changed the real price", selected, err)
	}
	selected, at, err := pricing.SelectEffectiveTx(f.ctx, tx, uuid.NewString(), pricing.Domain{TenantRevision: 1})
	var pe *pricing.Error
	if selected != nil || !at.IsZero() || !errors.As(err, &pe) || pe.Code != "pricing_invariant" {
		t.Fatal("missing capability was confused with an unpriced domain", selected, at, err)
	}
}
