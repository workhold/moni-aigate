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
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

func TestUnifiedTextMeteringGuardClosureDeferredAggregateRollback(t *testing.T) {
	f := newGuardFixture(t)
	base := f.fact(f.a)
	var amount int64
	if err := f.owner.QueryRow(f.ctx, `SELECT amount FROM reservations WHERE id=$1`, base.reservation).Scan(&amount); err != nil {
		t.Fatal(err)
	}

	requestID := uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = insertGuardRequestWithSnapshotTx(t, f, tx, base.requestID, requestID); err == nil {
		_, err = tx.Exec(f.ctx, `
INSERT INTO reservations(id,tenant_id,request_id,amount,deadline)
VALUES($1,$2,$3,$4,clock_timestamp()+interval '1 hour')`, uuid.NewString(), f.a.tenant, requestID, amount+1)
	}
	if err == nil {
		_, err = tx.Exec(f.ctx, `SET CONSTRAINTS meter_reservation_amount IMMEDIATE`)
	}
	if err == nil {
		_ = tx.Rollback(f.ctx)
		t.Fatal("invalid aggregate committed")
	}
	if rollbackErr := tx.Rollback(f.ctx); rollbackErr != nil && !strings.Contains(rollbackErr.Error(), "tx is closed") {
		t.Fatal(rollbackErr)
	}

	for _, table := range []string{"request_records", "request_meter_snapshots", "request_meter_bound_lines", "reservations"} {
		var count int
		column := "request_id"
		if table == "request_records" {
			column = "id"
		}
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s=$1", pgx.Identifier{f.schema, table}.Sanitize(), column)
		if err := f.owner.QueryRow(f.ctx, query, requestID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("deferred failure left %d rows in %s", count, table)
		}
	}
}

func TestUnifiedTextMeteringGuardClosureRuntimeCrossRequestAttemptRollsBack(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	b := f.fact(f.a)
	c := f.fact(f.b)

	for _, tc := range []struct {
		name, tenant, request, attempt string
	}{
		{name: "same tenant", tenant: f.a.tenant, request: b.requestID, attempt: a.attempt},
		{name: "cross tenant", tenant: f.b.tenant, request: c.requestID, attempt: a.attempt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := "guard-cross-request-attempt-" + uuid.NewString()
			beforeEvents, beforeFinalizations := countEvidence(t, f, tc.tenant, tc.request, event)
			_, err := f.store.RecordFinalization(f.ctx, billing.FinalizationInput{
				TenantID: tc.tenant, RequestID: tc.request, AttemptID: tc.attempt,
				Source: "gateway", SourceEventID: event,
				Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
				ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none",
			})
			if err == nil {
				t.Fatal("cross-request attempt was accepted")
			}
			afterEvents, afterFinalizations := countEvidence(t, f, tc.tenant, tc.request, event)
			if afterEvents != beforeEvents || afterFinalizations != beforeFinalizations {
				t.Fatalf("rejected finalization left evidence events=%d/%d finalizations=%d/%d", beforeEvents, afterEvents, beforeFinalizations, afterFinalizations)
			}
		})
	}
}

func countEvidence(t *testing.T, f *guardFixture, tenant, request, sourceEvent string) (int, int) {
	t.Helper()
	var events, finalizations int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM metering_events WHERE tenant_id=$1 AND request_id=$2 AND source_event_id=$3`, tenant, request, sourceEvent).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM finalization_records WHERE tenant_id=$1 AND request_id=$2 AND event_id IN (SELECT id FROM metering_events WHERE tenant_id=$1 AND request_id=$2 AND source_event_id=$3)`, tenant, request, sourceEvent).Scan(&finalizations); err != nil {
		t.Fatal(err)
	}
	return events, finalizations
}

func TestUnifiedTextMeteringGuardClosureSourceIdentityTenantScopeAndConflictRace(t *testing.T) {
	f := newGuardFixture(t)
	a := f.fact(f.a)
	b := f.fact(f.b)
	shared := "guard-shared-source-event-" + uuid.NewString()
	for _, fact := range []guardFact{a, b} {
		if _, err := f.store.RecordFinalization(f.ctx, billing.FinalizationInput{
			TenantID: fact.tenant.tenant, RequestID: fact.requestID, AttemptID: fact.attempt,
			Source: "gateway", SourceEventID: shared,
			Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none",
		}); err != nil {
			t.Fatal(err)
		}
	}
	var tenantRows int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM metering_events WHERE source='gateway' AND source_event_id=$1`, shared).Scan(&tenantRows); err != nil {
		t.Fatal(err)
	}
	if tenantRows != 2 {
		t.Fatalf("tenant-scoped source identity produced %d rows, want 2", tenantRows)
	}

	raceFact := f.fact(f.a)
	race := "guard-conflicting-source-event-" + uuid.NewString()
	base := billing.FinalizationInput{
		TenantID: f.a.tenant, RequestID: raceFact.requestID, AttemptID: raceFact.attempt,
		Source: "gateway", SourceEventID: race,
		Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none",
	}
	conflict := base
	conflict.ID = uuid.NewString()
	conflict.Usage.InputTokens = 11
	conflict.Usage.TotalTokens = 16
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, input := range []billing.FinalizationInput{base, conflict} {
		input := input
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.RecordFinalization(f.ctx, input)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, failures := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("conflicting source identity race results success=%d failure=%d", successes, failures)
	}
	var events, finalizations, conflicts int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM metering_events WHERE tenant_id=$1 AND source='gateway' AND source_event_id=$2`, f.a.tenant, race).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM finalization_records WHERE tenant_id=$1 AND request_id=$2 AND event_id IN (SELECT id FROM metering_events WHERE tenant_id=$1 AND request_id=$2 AND source='gateway' AND source_event_id=$3)`, f.a.tenant, raceFact.requestID, race).Scan(&finalizations); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM metering_event_conflicts c JOIN metering_events e ON e.id=c.event_id WHERE e.tenant_id=$1 AND e.source='gateway' AND e.source_event_id=$2`, f.a.tenant, race).Scan(&conflicts); err != nil {
		t.Fatal(err)
	}
	if events != 1 || finalizations != 1 || conflicts != 1 {
		t.Fatalf("conflicting source identity race left events=%d finalizations=%d conflicts=%d", events, finalizations, conflicts)
	}
}

func TestUnifiedTextMeteringGuardClosureActualLoginFunctionSurface(t *testing.T) {
	f := newGuardFixture(t)
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	type loginRole struct{ name, password string }
	roles := []loginRole{
		{name: "guard_surface_app_" + suffix, password: "app-" + suffix},
		{name: "guard_surface_projection_" + suffix, password: "projection-" + suffix},
		{name: "guard_surface_worker_" + suffix, password: "worker-" + suffix},
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
	for label, pool := range map[string]*pgxpool.Pool{"app": app, "projection": projection, "worker": worker} {
		var allApp, anyRestricted bool
		if err := pool.QueryRow(f.ctx, `
SELECT coalesce(bool_and(has_function_privilege(current_user,p.oid,'EXECUTE')),false),
       coalesce(bool_or(has_function_privilege(current_user,p.oid,'EXECUTE')),false)
FROM pg_catalog.pg_proc p
JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname=$1 AND p.proname LIKE 'meter\_%' ESCAPE '\'`, f.schema).Scan(&allApp, &anyRestricted); err != nil {
			t.Fatal(err)
		}
		if label == "app" {
			if !allApp || !anyRestricted {
				t.Fatalf("APP meter function surface mismatch all=%t any=%t", allApp, anyRestricted)
			}
		} else if anyRestricted {
			t.Fatalf("%s LOGIN unexpectedly executes a meter function", label)
		}
	}
}
