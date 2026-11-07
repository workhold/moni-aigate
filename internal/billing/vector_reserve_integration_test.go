package billing

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// This current-schema test only covers the vector retail admission slice. It
// does not replace tenantPricingApplication's dedicated 55439 barrier suite.
func vectorReserveApplication(t *testing.T, f *fixture) {
	t.Helper()
	var schema string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	appRole, projectorRole := "vector_bill_app_"+suffix, "vector_bill_proj_"+suffix
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		for _, role := range []string{appRole, projectorRole} {
			for _, sql := range []string{`DROP OWNED BY ` + pgx.Identifier{role}.Sanitize(), `DROP ROLE IF EXISTS ` + pgx.Identifier{role}.Sanitize()} {
				if _, err := f.pool.Exec(context.Background(), sql); err != nil {
					t.Error(err)
				}
			}
		}
	})
	for _, role := range []string{appRole, projectorRole} {
		if _, err := f.pool.Exec(f.ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+` NOLOGIN NOINHERIT NOSUPERUSER`); err != nil {
			t.Fatal(err)
		}
	}
	if err := operations.ProvisionFor(f.ctx, f.pool, schema, appRole, projectorRole); err != nil {
		t.Fatal(err)
	}
	cfg := f.pool.Config()
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE `+pgx.Identifier{appRole}.Sanitize())
		return err
	}
	var err error
	app, err = pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var current string
	var superuser bool
	if err = app.QueryRow(f.ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &superuser); err != nil || current != appRole || superuser {
		t.Fatal("not the restricted vector admission APP", current, superuser, err)
	}
	f.store = New(app)
}

func vectorReserveModel(t *testing.T, f *fixture, input int64) ReserveInput {
	t.Helper()
	f.model = uuid.NewString()
	version := uuid.NewString()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT meter_insert_text_model($1,$2,jsonb_build_object('version',$3::text,'model',$2::text,'protocol','openai_chat','input_token_upper_bound',$4::bigint,'context_limit',$4::bigint,'output_limit',20,'default_output_limit',20,'output_limit_field','max_tokens'),$3)`, []any{f.model, "vector-" + f.model, version, input}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{f.tenant, f.model}},
		{`UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1`, []any{f.key, f.model}},
	} {
		if _, err := f.pool.Exec(f.ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	billingPoolPublish(t, f, f.version, 1)
	in := f.input()
	in.ModelID, in.ProfileVersion, in.RouteRevision, in.InputUpperBound = f.model, version, 1, input
	return in
}

func TestPostgresVectorReserveWithoutScalarReads(t *testing.T) {
	for _, domain := range []string{"default", "group"} {
		for _, price := range []struct {
			name          string
			input, output int64
			want          Money
		}{{"paid", 2000000, 3000000, 260}, {"zero", 0, 0, 0}, {"single_ceil", 1, 1, 1}} {
			t.Run(domain+"/"+price.name, func(t *testing.T) {
				f := integration(t)
				var group *string
				var version string
				if domain == "group" {
					c := catalog.New(f.pool, nil)
					id, quote := tenantPricingGroup(t, f, c, "vector group", price.input, price.output)
					tenantPricingAssign(t, f, c, &id, 1)
					group, version = &id, quote
				} else {
					version = retailPrice(t, f, price.input, price.output, retailNow(t, f))
				}
				vectorReserveApplication(t, f)
				forbidScalarActualReads(t, f)
				r := f.reserve(t)
				defer f.store.EndRequest(r.RequestID)
				if r.Amount != price.want || (r.PricingGroupID == nil) != (group == nil) || group != nil && *r.PricingGroupID != *group {
					t.Fatal("vector admission changed price/domain", r)
				}
				retailAssertSnapshot(t, f, r, version, Money(price.input), Money(price.output))
				wire, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				var receipt map[string]json.RawMessage
				if err = json.Unmarshal(wire, &receipt); err != nil || receipt["input_rate"] != nil || receipt["output_rate"] != nil {
					t.Fatal("scalar rate mirror survived in the reservation receipt", string(wire), err)
				}
				f.wallet(t, 1000-price.want, price.want)
				retrievalBillingKey(t, f, 0, int64(price.want))
				if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
					t.Fatal(err)
				}
				f.wallet(t, 1000, 0)
				retrievalBillingKey(t, f, 0, 0)
				f.check(t)
			})
		}
	}
	for _, protocol := range []string{"openai_embeddings", "jina_rerank"} {
		t.Run(protocol, func(t *testing.T) {
			f := integration(t)
			in := retrievalBillingInput(t, f, protocol, 0, nil)
			vectorReserveApplication(t, f)
			forbidScalarActualReads(t, f)
			r, err := f.store.Reserve(f.ctx, in)
			if err != nil || r.Amount != 100 {
				t.Fatal("input-only vector admission failed", r, err)
			}
			defer f.store.EndRequest(r.RequestID)
			retailAssertSnapshot(t, f, r, r.PriceVersionID, 1000000, 0)
			if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
				t.Fatal(err)
			}
			f.wallet(t, 1000, 0)
			f.check(t)
		})
	}
}

// Compare complete rows, not just the final wallet total: a rejected admission
// must not leave idempotency, budget, source, journal or projection fragments.
func vectorReserveFacts(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	facts := make(map[string]string)
	for _, table := range []string{
		"request_idempotency", "request_records", "request_meter_snapshots", "request_meter_bound_lines", "reservations", "api_keys",
		"periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events",
		"funding_lots", "funding_lot_balances", "funding_lot_events", "funding_reservation_lots",
		"ledger_accounts", "journals", "journal_lines", "audit_events", "projection_outbox", "recovery_jobs",
	} {
		var rows string
		if err := f.pool.QueryRow(f.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM `+pgx.Identifier{table}.Sanitize()+` t`).Scan(&rows); err != nil {
			t.Fatal(table, err)
		}
		facts[table] = rows
	}
	return facts
}

func vectorReserveRejected(t *testing.T, f *fixture, in ReserveInput, code string) {
	t.Helper()
	before := vectorReserveFacts(t, f)
	r, err := f.store.Reserve(f.ctx, in)
	assertCode(t, err, code)
	if r != (Reservation{}) {
		t.Fatal("rejected admission leaked a receipt", r)
	}
	for table, rows := range vectorReserveFacts(t, f) {
		if rows != before[table] {
			t.Errorf("rejected admission changed %s", table)
		}
	}
	retrievalBillingNoRequest(t, f, in)
}

func TestPostgresVectorReservePriceFailuresRollback(t *testing.T) {
	for _, domain := range []string{"default", "group"} {
		for _, future := range []bool{false, true} {
			name := domain + "/missing"
			if future {
				name = domain + "/future_only"
			}
			t.Run(name, func(t *testing.T) {
				f := integration(t)
				in := vectorReserveModel(t, f, 100)
				c := catalog.New(f.pool, nil)
				if domain == "group" {
					g, err := c.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "missing vector group"})
					if err != nil {
						t.Fatal(err)
					}
					tenantPricingAssign(t, f, c, &g.Result.ID, 1)
					// A real default price must not fill an assigned group's gap.
					retailPrice(t, f, 0, 0, retailNow(t, f))
					if future {
						_, err = c.PublishGroupPrice(f.ctx, catalog.PricingActor{UserID: f.admin}, meterquote.BindGroup(t, f.ctx, f.pool, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: g.Result.ID, ModelID: f.model, EffectiveAt: retailNow(t, f).AddDate(0, 0, 1)}))
						if err != nil {
							t.Fatal(err)
						}
					}
				} else if future {
					retailPrice(t, f, 0, 0, retailNow(t, f).AddDate(0, 0, 1))
				}
				vectorReserveApplication(t, f)
				forbidScalarActualReads(t, f)
				vectorReserveRejected(t, f, in, "price_unavailable")
			})
		}
	}
}

func TestPostgresVectorReserveOverflowRollbackAndRetry(t *testing.T) {
	f := integration(t)
	in := vectorReserveModel(t, f, 1000001)
	if _, err := meterquote.TryPublishDefaultPrice(f.ctx, f.pool, meterquote.DefaultPrice{ID: uuid.NewString(), ModelID: f.model, Actor: f.operator, InputRate: math.MaxInt64, OutputRate: math.MaxInt64, EffectiveAt: retailNow(t, f)}); err == nil || !strings.Contains(err.Error(), "group price maximum amount overflow") {
		t.Fatalf("overflowing retail price was accepted or failed at the wrong boundary: %v", err)
	}
	vectorReserveApplication(t, f)
	forbidScalarActualReads(t, f)
	// The same request and idempotency key remain usable after definite rollback.
	version := retailPrice(t, f, 1, 1, retailNow(t, f))
	r, err := f.store.Reserve(f.ctx, in)
	if err != nil || r.Amount != 2 {
		t.Fatal("overflow left a reservation/idempotency/active marker", r, err)
	}
	defer f.store.EndRequest(r.RequestID)
	retailAssertSnapshot(t, f, r, version, 1, 1)
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 1000, 0)
	f.check(t)
}
