package migrations_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// These five roots are platform facts by contract. They deliberately have no
// tenant_id: every tenant may use the same published schema, capability,
// route, retail price and supplier quote, while request/attempt facts retain
// their own tenant binding.
func TestUnifiedTextMeteringGuardAuditGlobalFactsAreTenantNeutral(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	a, b := f.fact(f.a), f.fact(f.b)

	type snapshotRefs struct {
		model, profile, schema, schemaDigest, variant, price, cost string
	}
	read := func(requestID string) snapshotRefs {
		t.Helper()
		var refs snapshotRefs
		if err := f.owner.QueryRow(f.ctx, `
SELECT s.model_id,s.profile_version,s.schema_id,encode(s.schema_digest,'hex'),s.variant_key,
       s.price_version_id,a.cost_version_id
FROM request_meter_snapshots s
JOIN attempt_meter_snapshots a ON a.request_id=s.request_id
WHERE s.request_id=$1`, requestID).Scan(
			&refs.model, &refs.profile, &refs.schema, &refs.schemaDigest,
			&refs.variant, &refs.price, &refs.cost,
		); err != nil {
			t.Fatal(err)
		}
		return refs
	}
	aRefs, bRefs := read(a.requestID), read(b.requestID)
	if aRefs != bRefs {
		t.Fatalf("tenants did not share the same immutable platform facts: a=%+v b=%+v", aRefs, bRefs)
	}
	if a.tenant.tenant == b.tenant.tenant {
		t.Fatal("fixture tenants unexpectedly equal")
	}
	for _, fact := range []guardFact{a, b} {
		var tenant string
		if err := f.owner.QueryRow(f.ctx, `SELECT tenant_id FROM request_records WHERE id=$1`, fact.requestID).Scan(&tenant); err != nil {
			t.Fatal(err)
		}
		if tenant != fact.tenant.tenant {
			t.Fatalf("request tenant leaked: got %q want %q", tenant, fact.tenant.tenant)
		}
	}

	globalTables := []string{
		"meter_schemas", "meter_schema_dimensions", "model_capability_versions",
		"model_capability_variants", "model_capability_bound_lines", "route_policy_versions",
		"model_bindings", "binding_cost_rate_limits", "price_versions", "price_rate_lines",
		"cost_versions", "cost_rate_lines",
	}
	for _, table := range globalTables {
		var tenantColumns int
		if err := f.owner.QueryRow(f.ctx, `
SELECT count(*) FROM information_schema.columns
WHERE table_schema=$1 AND table_name=$2 AND column_name='tenant_id'`, f.schema, table).Scan(&tenantColumns); err != nil {
			t.Fatal(err)
		}
		if tenantColumns != 0 {
			t.Fatalf("platform fact %s unexpectedly became tenant-scoped", table)
		}
	}
}

// A single immutable-write test is insufficient evidence for the current
// READ COMMITTED publication boundary. Two concurrent mutation attempts for
// every platform-root category must both fail and leave the original facts.
func TestUnifiedTextMeteringGuardAuditConcurrentImmutableGlobalFacts(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	fact := f.fact(f.a)
	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "schema",
			query: `UPDATE meter_schemas SET name=name WHERE id=(SELECT schema_id FROM request_meter_snapshots WHERE request_id=$1)`,
			args:  []any{fact.requestID},
		},
		{
			name:  "capability",
			query: `UPDATE model_capability_versions SET protocol=protocol WHERE id=$1`,
			args:  []any{f.profile},
		},
		{
			name:  "route",
			query: `UPDATE route_policy_versions SET cost_mode=cost_mode WHERE model_id=$1 AND revision=$2`,
			args:  []any{f.model, guardRouteRevision},
		},
		{
			name:  "retail price",
			query: `UPDATE price_versions SET effective_at=effective_at WHERE id=(SELECT price_version_id FROM request_meter_snapshots WHERE request_id=$1)`,
			args:  []any{fact.requestID},
		},
		{
			name:  "supplier quote",
			query: `UPDATE cost_versions SET basis=basis WHERE id=(SELECT cost_version_id FROM attempt_meter_snapshots WHERE attempt_id=$1)`,
			args:  []any{fact.attempt},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := make(chan error, 2)
			var wg sync.WaitGroup
			for range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, err := f.owner.Exec(f.ctx, tc.query, tc.args...)
					errs <- err
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err == nil {
					t.Fatal("concurrent immutable mutation was accepted")
				}
			}
		})
	}
}

func TestUnifiedTextMeteringGuardAuditActualLoginTableSurface(t *testing.T) {
	f := newGuardFixture(t)
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	type loginRole struct{ name, password string }
	roles := []loginRole{
		{name: "guard_audit_app_" + suffix, password: "app-" + suffix},
		{name: "guard_audit_projection_" + suffix, password: "projection-" + suffix},
		{name: "guard_audit_worker_" + suffix, password: "worker-" + suffix},
	}
	for _, role := range roles {
		if _, err := f.admin.Exec(f.ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE", pgx.Identifier{role.name}.Sanitize(), role.password)); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, role := range roles {
			_, _ = f.admin.Exec(context.Background(), "DROP OWNED BY "+pgx.Identifier{role.name}.Sanitize())
			_, _ = f.admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{role.name}.Sanitize())
		}
	})
	if err := operations.ProvisionFor(f.ctx, f.owner, f.schema, roles[0].name, roles[1].name); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	connect := func(role loginRole) *pgxpool.Pool {
		login := cfg.Copy()
		login.ConnConfig.User, login.ConnConfig.Password = role.name, role.password
		login.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(f.ctx, login)
		if err != nil {
			t.Fatalf("connect %s: %v", role.name, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	app, projection, worker := connect(roles[0]), connect(roles[1]), connect(roles[2])
	roleNames := map[string]string{"app": roles[0].name, "projection": roles[1].name, "worker": roles[2].name}

	meterTables := []string{
		"meter_schemas", "meter_schema_dimensions", "model_capability_variants", "model_capability_bound_lines",
		"price_rate_lines", "cost_rate_lines", "binding_cost_rate_limits", "request_meter_snapshots",
		"attempt_meter_snapshots", "request_meter_bound_lines", "attempt_meter_bound_lines", "meter_event_quantities",
	}
	for label, pool := range map[string]*pgxpool.Pool{"app": app, "projection": projection, "worker": worker} {
		var current string
		if err := pool.QueryRow(f.ctx, `SELECT current_user`).Scan(&current); err != nil {
			t.Fatal(err)
		}
		for _, table := range meterTables {
			qualified := pgx.Identifier{f.schema, table}.Sanitize()
			if label == "app" {
				var selectOK, insertOK, updateOK, deleteOK, truncateOK, referencesOK, triggerOK bool
				if err := pool.QueryRow(f.ctx, `
SELECT has_table_privilege(current_user,$1::text,'SELECT'),
       has_table_privilege(current_user,$1::text,'INSERT'),
       has_table_privilege(current_user,$1::text,'UPDATE'),
       has_table_privilege(current_user,$1::text,'DELETE'),
       has_table_privilege(current_user,$1::text,'TRUNCATE'),
       has_table_privilege(current_user,$1::text,'REFERENCES'),
       has_table_privilege(current_user,$1::text,'TRIGGER')`, qualified).Scan(
					&selectOK, &insertOK, &updateOK, &deleteOK, &truncateOK, &referencesOK, &triggerOK,
				); err != nil {
					t.Fatalf("%s %s privilege query: %v", label, table, err)
				}
				if !selectOK || !insertOK || updateOK || deleteOK || truncateOK || referencesOK || triggerOK {
					t.Fatalf("APP ACL mismatch for %s select=%t insert=%t update=%t delete=%t truncate=%t references=%t trigger=%t", table, selectOK, insertOK, updateOK, deleteOK, truncateOK, referencesOK, triggerOK)
				}
				if _, err := pool.Exec(f.ctx, "SELECT count(*) FROM "+qualified); err != nil {
					t.Fatalf("APP cannot read %s through actual LOGIN: %v", table, err)
				}
				continue
			}
			if _, err := pool.Exec(f.ctx, "SELECT count(*) FROM "+qualified); err == nil {
				t.Fatalf("%s actual LOGIN read %s", label, table)
			}
			var any bool
			if err := f.owner.QueryRow(f.ctx, `
SELECT has_table_privilege($1::name,$2::text,'SELECT')
    OR has_table_privilege($1::name,$2::text,'INSERT')
    OR has_table_privilege($1::name,$2::text,'UPDATE')
    OR has_table_privilege($1::name,$2::text,'DELETE')
    OR has_table_privilege($1::name,$2::text,'TRUNCATE')
    OR has_table_privilege($1::name,$2::text,'REFERENCES')
    OR has_table_privilege($1::name,$2::text,'TRIGGER')`, roleNames[label], qualified).Scan(&any); err != nil {
				t.Fatalf("%s %s owner ACL query: %v", label, table, err)
			}
			if any {
				t.Fatalf("%s has a meter privilege on %s", label, table)
			}
		}
	}
}
