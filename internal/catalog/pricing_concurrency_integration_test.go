package catalog_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/metering"
	"github.com/moni-ai/moni-aigate/internal/pricing"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// Successful Store retries must not hide a new pricing lock cycle.
type pricingConcurrencyTrace struct{ rollbacks atomic.Int64 }

func (tr *pricingConcurrencyTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (tr *pricingConcurrencyTrace) TraceQueryEnd(_ context.Context, _ *pgx.Conn, d pgx.TraceQueryEndData) {
	var pg *pgconn.PgError
	if errors.As(d.Err, &pg) && (pg.Code == "40P01" || pg.Code == "40001") {
		tr.rollbacks.Add(1)
	}
}

func pricingConcurrencyDB(t *testing.T) *publicFixture {
	t.Helper()
	f := publicDB(t)
	cfg := f.app.Config()
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("requires dedicated local moni_test on port 55439")
	}
	trace := &pricingConcurrencyTrace{}
	cfg.ConnConfig.Tracer = trace
	app, e := pgxpool.NewWithConfig(f.ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		app.Close()
		if trace.rollbacks.Load() != 0 {
			t.Error("automatic retry masked a pricing deadlock/serialization rollback", trace.rollbacks.Load())
		}
	})
	var current string
	var super bool
	if e = app.QueryRow(f.ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&current, &super); e != nil || current != f.appRole || super {
		t.Fatal("not restricted APP", current, super, e)
	}
	f.store = catalog.New(app, nil)
	return f
}
func pricingConcurrencyExec(t *testing.T, f *publicFixture, sql string, args ...any) {
	t.Helper()
	if _, e := f.owner.Exec(f.ctx, sql, args...); e != nil {
		t.Fatal(e)
	}
}
func pricingConcurrencyTx(t *testing.T, f *publicFixture) pgx.Tx {
	t.Helper()
	tx, e := f.owner.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}
func pricingConcurrencyWaiters(t *testing.T, ctx context.Context, f *publicFixture, blocker uint32, n int) []string {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, e := f.owner.Query(ctx, `SELECT query FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) ORDER BY pid`, int32(blocker))
		if e != nil {
			t.Fatal(e)
		}
		queries := []string{}
		for rows.Next() {
			var q string
			if e = rows.Scan(&q); e != nil {
				rows.Close()
				t.Fatal(e)
			}
			queries = append(queries, q)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			t.Fatal(e)
		}
		if len(queries) >= n {
			return queries
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected PostgreSQL lock edges absent", ctx.Err())
		case <-ticker.C:
		}
	}
}
func pricingConcurrencyBlockedPID(t *testing.T, ctx context.Context, f *publicFixture, blocker uint32) uint32 {
	t.Helper()
	pricingConcurrencyWaiters(t, ctx, f, blocker, 1)
	var pid uint32
	if e := f.owner.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND $1::int=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1`, int32(blocker)).Scan(&pid); e != nil {
		t.Fatal(e)
	}
	return pid
}
func pricingConcurrencyGroup(t *testing.T, f *publicFixture) string {
	t.Helper()
	g, e := f.store.CreatePriceGroup(f.ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "concurrency"})
	if e != nil {
		t.Fatal(e)
	}
	return g.Result.ID
}
func pricingConcurrencyTenant(t *testing.T, f *publicFixture) string {
	t.Helper()
	id := uuid.NewString()
	pricingConcurrencyExec(t, f, `INSERT INTO tenants(id,name) VALUES($1,'concurrency')`, id)
	return id
}
func pricingConcurrencyNow(t *testing.T, f *publicFixture) time.Time {
	t.Helper()
	var at time.Time
	if e := f.owner.QueryRow(f.ctx, `SELECT clock_timestamp()::timestamptz(6)`).Scan(&at); e != nil {
		t.Fatal(e)
	}
	return at
}

// A trigger pauses the actual repository command after its parent locks and
// active-state validation. The random advisory key is isolated to this case.
func pricingConcurrencyGate(t *testing.T, f *publicFixture, table, event, condition string) pgx.Tx {
	t.Helper()
	u := uuid.New()
	key := int64(binary.BigEndian.Uint64(u[:8]))
	sql := fmt.Sprintf(`CREATE FUNCTION pricing_concurrency_pause() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF %s THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END $$; CREATE TRIGGER zz_pricing_concurrency_pause BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION pricing_concurrency_pause()`, condition, key, event, table)
	pricingConcurrencyExec(t, f, sql)
	tx := pricingConcurrencyTx(t, f)
	if _, e := tx.Exec(f.ctx, `SELECT pg_advisory_xact_lock($1)`, key); e != nil {
		t.Fatal(e)
	}
	return tx
}
func pricingConcurrencyRetire(ctx context.Context, f *publicFixture, group string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, e := f.store.UpdatePriceGroup(ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupUpdateCommand{OperationID: uuid.NewString(), GroupID: group, ExpectedRevision: 1, Label: "retired", State: "retired"})
		done <- e
	}()
	return done
}
func pricingConcurrencyAwait(t *testing.T, ctx context.Context, ch <-chan error) error {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-ctx.Done():
		t.Fatal("pricing command did not finish", ctx.Err())
		return ctx.Err()
	}
}

func TestTenantPricingActiveGroupShareWinsBeforeRetirement(t *testing.T) {
	for _, kind := range []string{"publish", "assign"} {
		t.Run(kind, func(t *testing.T) {
			f := pricingConcurrencyDB(t)
			group := pricingConcurrencyGroup(t, f)
			tenant := pricingConcurrencyTenant(t, f)
			table, event, condition := "price_versions", "INSERT", "NEW.price_group_id IS NOT NULL"
			if kind == "assign" {
				table, event, condition = "tenants", "UPDATE", "NEW.price_group_id IS DISTINCT FROM OLD.price_group_id"
			}
			gate := pricingConcurrencyGate(t, f, table, event, condition)
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			operation := uuid.NewString()
			command := make(chan error, 1)
			go func() {
				var e error
				if kind == "publish" {
					_, e = f.store.PublishGroupPrice(ctx, catalog.PricingActor{UserID: f.admin}, meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: operation, GroupID: group, ModelID: f.model, InputRate: 11, OutputRate: 22, EffectiveAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}))
				} else {
					_, e = f.store.AssignTenantPricing(ctx, catalog.PricingActor{UserID: f.admin}, catalog.TenantPricingCommand{OperationID: operation, TenantID: tenant, ExpectedRevision: 1, GroupID: &group})
				}
				command <- e
			}()
			writerPID := pricingConcurrencyBlockedPID(t, ctx, f, gate.Conn().PgConn().PID())
			retired := pricingConcurrencyRetire(ctx, f, group)
			queries := pricingConcurrencyWaiters(t, ctx, f, writerPID, 1)
			if !strings.Contains(queries[0], "FROM price_groups") || !strings.Contains(queries[0], "FOR NO KEY UPDATE") {
				t.Fatal("retirement did not wait for the active command's group SHARE", queries)
			}
			if e := gate.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e := pricingConcurrencyAwait(t, ctx, command); e != nil {
				t.Fatal("earlier validated command must commit", e)
			}
			if e := pricingConcurrencyAwait(t, ctx, retired); e != nil {
				t.Fatal(e)
			}
			g, e := f.store.PriceGroup(ctx, f.admin, group)
			if e != nil || g.State != "retired" || g.Revision != "2" {
				t.Fatal(g, e)
			}
			receipt, e := f.store.GetPricingOperation(ctx, f.admin, operation)
			if e != nil {
				t.Fatal("successful command lost receipt", e)
			}
			if kind == "publish" {
				v := receipt.Result.(*catalog.GroupPriceView)
				if v.PriceGroupID != group || !strings.Contains(string(v.Rates), `"numerator":"11","denominator":"1000000"`) || !strings.Contains(string(v.Rates), `"numerator":"11","denominator":"500000"`) {
					t.Fatal(v)
				}
			} else {
				v := receipt.Result.(*catalog.AssignmentView)
				actual, e := f.store.TenantPricing(ctx, f.admin, tenant)
				if e != nil || v.PriceGroupID == nil || *v.PriceGroupID != group || v.PricingRevision != "2" || actual.PriceGroupID == nil || *actual.PriceGroupID != group || actual.PricingRevision != "2" {
					t.Fatal(v, actual, e)
				}
			}
		})
	}
}

func TestTenantPricingRetirementWinsBeforePublishAndAssignment(t *testing.T) {
	f := pricingConcurrencyDB(t)
	group := pricingConcurrencyGroup(t, f)
	tenant := pricingConcurrencyTenant(t, f)
	gate := pricingConcurrencyGate(t, f, "price_groups", "UPDATE", "OLD.state='active' AND NEW.state='retired'")
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	retired := pricingConcurrencyRetire(ctx, f, group)
	retirementPID := pricingConcurrencyBlockedPID(t, ctx, f, gate.Conn().PgConn().PID())
	publishOp, assignOp := uuid.NewString(), uuid.NewString()
	publish, assign := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := f.store.PublishGroupPrice(ctx, catalog.PricingActor{UserID: f.admin}, meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: publishOp, GroupID: group, ModelID: f.model, InputRate: 11, OutputRate: 22, EffectiveAt: time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)}))
		publish <- e
	}()
	go func() {
		_, e := f.store.AssignTenantPricing(ctx, catalog.PricingActor{UserID: f.admin}, catalog.TenantPricingCommand{OperationID: assignOp, TenantID: tenant, ExpectedRevision: 1, GroupID: &group})
		assign <- e
	}()
	queries := pricingConcurrencyWaiters(t, ctx, f, retirementPID, 2)
	for _, q := range queries {
		if !strings.Contains(q, "FROM price_groups") || !strings.Contains(q, "FOR SHARE") {
			t.Fatal("later command did not synchronize group active state", queries)
		}
	}
	if e := gate.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	if e := pricingConcurrencyAwait(t, ctx, retired); e != nil {
		t.Fatal(e)
	}
	pricingCode(t, pricingConcurrencyAwait(t, ctx, publish), "price_group_retired")
	pricingCode(t, pricingConcurrencyAwait(t, ctx, assign), "price_group_retired")
	var prices, receipts, assignments, audits int
	if e := f.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM price_versions WHERE price_group_id=$1),(SELECT count(*) FROM pricing_operations WHERE operation_id=ANY($2)),(SELECT count(*) FROM tenant_pricing_assignments WHERE tenant_id=$3),(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=ANY($2))`, group, []string{publishOp, assignOp}, tenant).Scan(&prices, &receipts, &assignments, &audits); e != nil || prices != 0 || receipts != 0 || assignments != 0 || audits != 0 {
		t.Fatal("retired rejection left partial facts", prices, receipts, assignments, audits, e)
	}
	actual, e := f.store.TenantPricing(ctx, f.admin, tenant)
	if e != nil || actual.PriceGroupID != nil || actual.PricingRevision != "1" {
		t.Fatal(actual, e)
	}
}

func pricingConcurrencySelection(t *testing.T, f *publicFixture, tenant string) (*pricing.Selection, time.Time) {
	t.Helper()
	tx, e := f.app.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	var domain pricing.Domain
	if e = tx.QueryRow(f.ctx, `SELECT price_group_id,pricing_revision FROM tenants WHERE id=$1 FOR SHARE`, tenant).Scan(&domain.GroupID, &domain.TenantRevision); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `SELECT id FROM models WHERE id=$1 FOR SHARE`, f.model); e != nil {
		t.Fatal(e)
	}
	if e = pricing.LockAssignedGroupTx(f.ctx, tx, domain); e != nil {
		t.Fatal(e)
	}
	selected, at, e := pricing.SelectEffectiveTx(f.ctx, tx, f.model, domain)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
	return selected, at
}

func pricingConcurrencyRates(t *testing.T, s *pricing.Selection, input, output int64) bool {
	t.Helper()
	if s == nil {
		return false
	}
	expected, err := metering.LegacyTokenRates(s.Schema, metering.Retail, input, output)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := expected.CanonicalJSON()
	got, _ := s.Rates.CanonicalJSON()
	return bytes.Equal(got, want)
}

func TestTenantPricingRetiredAssignedGroupActivatesPrepublishedFuturePrice(t *testing.T) {
	f := pricingConcurrencyDB(t)
	group := pricingConcurrencyGroup(t, f)
	tenant := pricingConcurrencyTenant(t, f)
	actor := catalog.PricingActor{UserID: f.admin}
	pricingConcurrencyExec(t, f, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant, f.customer)
	pricingConcurrencyExec(t, f, `INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, tenant, f.model)
	if _, e := f.store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: tenant, ExpectedRevision: 1, GroupID: &group}); e != nil {
		t.Fatal(e)
	}
	old, e := f.store.PublishGroupPrice(f.ctx, actor, meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group, ModelID: f.model, InputRate: 10, OutputRate: 20, EffectiveAt: pricingConcurrencyNow(t, f).Add(-time.Minute)}))
	if e != nil {
		t.Fatal(e)
	}
	effective := pricingConcurrencyNow(t, f).Add(time.Second)
	future, e := f.store.PublishGroupPrice(f.ctx, actor, meterquote.BindGroup(t, f.ctx, f.owner, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group, ModelID: f.model, InputRate: 30, OutputRate: 40, EffectiveAt: effective}))
	if e != nil {
		t.Fatal(e)
	}
	if e = pricingConcurrencyAwait(t, f.ctx, pricingConcurrencyRetire(f.ctx, f, group)); e != nil {
		t.Fatal(e)
	}
	before, observed := pricingConcurrencySelection(t, f, tenant)
	if !observed.Before(effective) || before == nil || before.VersionID != old.Result.ID || !pricingConcurrencyRates(t, before, 10, 20) {
		t.Fatal("retirement changed the pre-activation selection", before, observed, effective)
	}
	quote, e := f.store.RetailPrice(f.ctx, f.customer, tenant, f.model)
	if e != nil || quote.PriceVersionID == nil || *quote.PriceVersionID != old.Result.ID {
		t.Fatal(quote, e)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 4*time.Second)
	defer cancel()
	if _, e = f.owner.Exec(ctx, `SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.001)`, effective); e != nil {
		t.Fatal(e)
	}
	after, observed := pricingConcurrencySelection(t, f, tenant)
	if observed.Before(effective) || after == nil || after.VersionID != future.Result.ID || !pricingConcurrencyRates(t, after, 30, 40) || !after.EffectiveAt.Equal(effective) {
		t.Fatal("retired existing domain failed future activation", after, observed, effective)
	}
	quote, e = f.store.RetailPrice(ctx, f.customer, tenant, f.model)
	if e != nil || quote.PriceVersionID == nil || *quote.PriceVersionID != future.Result.ID || quote.PriceGroupID == nil || *quote.PriceGroupID != group {
		t.Fatal(quote, e)
	}
}

func TestTenantPricingActorRevocationDuringLockWaitRejectsCommand(t *testing.T) {
	f := pricingConcurrencyDB(t)
	role := pricingConcurrencyTx(t, f)
	if _, e := role.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.admin); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	op := uuid.NewString()
	done := make(chan error, 1)
	go func() {
		_, e := f.store.CreatePriceGroup(ctx, catalog.PricingActor{UserID: f.admin}, catalog.GroupCreateCommand{OperationID: op, Label: "must not be created"})
		done <- e
	}()
	queries := pricingConcurrencyWaiters(t, ctx, f, role.Conn().PgConn().PID(), 1)
	if !strings.Contains(queries[0], "FROM users") || !strings.Contains(queries[0], "FOR SHARE") {
		t.Fatal("command did not synchronize current actor", queries)
	}
	if e := role.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	pricingCode(t, pricingConcurrencyAwait(t, ctx, done), "forbidden")
	var facts int
	if e := f.owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM pricing_operations WHERE operation_id=$1)+(SELECT count(*) FROM price_groups WHERE label='must not be created')+(SELECT count(*) FROM audit_events WHERE sanitized_diff->>'operation_id'=$1)`, op).Scan(&facts); e != nil || facts != 0 {
		t.Fatal("revoked actor wrote pricing effects", facts, e)
	}
}
