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
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// TestUnifiedTextMeteringCurrentGuardDDLIndex is intentionally tied to the
// current-only installer. It makes the ten guard categories observable in a
// real PostgreSQL schema instead of relying on names in the 024 source file.
func TestUnifiedTextMeteringCurrentGuardDDLIndex(t *testing.T) {
	f := newGuardFixture(t)
	checks := []struct {
		category, relation, trigger string
		deferred                    bool
	}{
		{"schema", "meter_schemas", "meter_schema_complete", true},
		{"schema", "meter_schema_dimensions", "meter_schema_dimension_complete", true},
		{"capability", "model_capability_versions", "meter_capability_root_complete", true},
		{"capability", "model_capability_variants", "meter_capability_variant_complete", true},
		{"capability", "model_capability_bound_lines", "meter_capability_bound_complete", true},
		{"route", "route_policy_versions", "meter_route_root_complete", true},
		{"route", "model_bindings", "meter_route_binding_complete", true},
		{"route", "binding_cost_rate_limits", "meter_route_cap_complete", true},
		{"retail", "price_versions", "meter_price_root_complete", true},
		{"retail", "price_rate_lines", "meter_price_line_complete", true},
		{"supplier", "cost_versions", "meter_cost_root_complete", true},
		{"supplier", "cost_rate_lines", "meter_cost_line_complete", true},
		{"request", "request_records", "meter_request_snapshot_present", true},
		{"request", "request_meter_snapshots", "meter_request_bounds_complete", true},
		{"request", "request_meter_snapshots", "meter_request_plan_matches", true},
		{"request", "request_meter_snapshots", "meter_request_quote_matches", true},
		{"attempt", "upstream_attempts", "meter_attempt_snapshot_present", true},
		{"attempt", "attempt_meter_snapshots", "meter_attempt_bounds_complete", true},
		{"attempt", "attempt_meter_snapshots", "meter_attempt_plan_matches", true},
		{"attempt", "attempt_meter_snapshots", "meter_attempt_quote_matches", true},
		{"observation", "metering_events", "meter_event_attempt_bound", true},
		{"observation", "metering_events", "meter_event_observation_complete", true},
		{"observation", "metering_events", "meter_event_rows_complete", true},
		{"finalization", "finalization_records", "meter_finalization_complete", true},
		{"finalization", "settlements", "meter_settlement_amount", true},
		{"finalization", "settlements", "meter_settlement_finalization", true},
		{"procurement", "procurement_reservations", "meter_procurement_attempt_closed", true},
		{"procurement", "procurement_reservations", "meter_procurement_bound_matches", true},
		{"procurement", "procurement_cost_proposals", "meter_procurement_proposal_current", true},
	}
	for _, tc := range checks {
		t.Run(tc.category+"/"+tc.relation+"/"+tc.trigger, func(t *testing.T) {
			var present, deferrable, initiallyDeferred bool
			if err := f.owner.QueryRow(f.ctx, `
SELECT EXISTS(
  SELECT 1 FROM pg_catalog.pg_trigger tg
  JOIN pg_catalog.pg_class c ON c.oid=tg.tgrelid
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname=$2 AND tg.tgname=$3 AND NOT tg.tgisinternal
),
coalesce((SELECT tg.tgdeferrable FROM pg_catalog.pg_trigger tg
  JOIN pg_catalog.pg_class c ON c.oid=tg.tgrelid
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname=$2 AND tg.tgname=$3 AND NOT tg.tgisinternal),false),
coalesce((SELECT tg.tginitdeferred FROM pg_catalog.pg_trigger tg
  JOIN pg_catalog.pg_class c ON c.oid=tg.tgrelid
  JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname=$2 AND tg.tgname=$3 AND NOT tg.tgisinternal),false)`, f.schema, tc.relation, tc.trigger).Scan(&present, &deferrable, &initiallyDeferred); err != nil {
				t.Fatal(err)
			}
			if !present || tc.deferred && (!deferrable || !initiallyDeferred) {
				t.Fatalf("current baseline is missing the required deferred guard: present=%t deferrable=%t initially_deferred=%t", present, deferrable, initiallyDeferred)
			}
		})
	}

	var baseline, appendMigration int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE name='current_mvp.sql'),count(*) FILTER (WHERE name='current_001_append_path.sql') FROM schema_migrations`).Scan(&baseline, &appendMigration); err != nil {
		t.Fatal(err)
	}
	if baseline != 1 || appendMigration != 1 {
		t.Fatalf("guard fixture did not use the current-only migration plan: baseline=%d append=%d", baseline, appendMigration)
	}
}

func TestUnifiedTextMeteringCurrentPositiveGuardFacts(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	fact := f.fact(f.a)
	f.finalize(&fact)

	checks := []struct {
		name  string
		query string
		args  []any
		want  int
	}{
		{"schema dimensions", `SELECT count(*) FROM meter_schema_dimensions d JOIN model_capability_versions c ON c.schema_id=d.schema_id WHERE c.id=$1`, []any{f.profile}, 2},
		{"single default capability", `SELECT count(*) FROM model_capability_variants WHERE profile_version=$1 AND variant_key='default'`, []any{f.profile}, 1},
		{"capability bounds", `SELECT count(*) FROM model_capability_bound_lines WHERE profile_version=$1 AND variant_key='default'`, []any{f.profile}, 4},
		{"route binding", `SELECT count(*) FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id WHERE p.model_id=$1 AND p.revision=$2`, []any{f.model, guardRouteRevision}, 1},
		{"retail rate lines", `SELECT count(*) FROM price_rate_lines l JOIN request_meter_snapshots s ON s.price_version_id=l.price_version_id WHERE s.request_id=$1`, []any{fact.requestID}, 2},
		{"supplier rate lines", `SELECT count(*) FROM cost_rate_lines l JOIN attempt_meter_snapshots s ON s.cost_version_id=l.cost_version_id WHERE s.attempt_id=$1`, []any{fact.attempt}, 2},
		{"request bounds", `SELECT count(*) FROM request_meter_bound_lines WHERE request_id=$1`, []any{fact.requestID}, 4},
		{"attempt bounds", `SELECT count(*) FROM attempt_meter_bound_lines WHERE attempt_id=$1`, []any{fact.attempt}, 4},
		{"observation quantities", `SELECT count(*) FROM meter_event_quantities WHERE event_id=$1`, []any{fact.event}, 2},
		{"finalization", `SELECT count(*) FROM finalization_records WHERE id=$1`, []any{fact.final}, 1},
		{"settlement", `SELECT count(*) FROM settlements WHERE id=$1`, []any{fact.settlement}, 1},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			if err := f.owner.QueryRow(f.ctx, tc.query, tc.args...).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("count=%d want=%d", got, tc.want)
			}
		})
	}
	var quoteStatus string
	if err := f.owner.QueryRow(f.ctx, `SELECT quote_status FROM attempt_meter_snapshots WHERE attempt_id=$1`, fact.attempt).Scan(&quoteStatus); err != nil {
		t.Fatal(err)
	}
	if quoteStatus != "known" {
		t.Fatalf("valid supplier quote was not persisted as known: %s", quoteStatus)
	}
}

func TestUnifiedTextMeteringCurrentImmutablePriceCapabilityAndAttemptFacts(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	fact := f.fact(f.a)
	var priceID, costID string
	if err := f.owner.QueryRow(f.ctx, `SELECT price_version_id FROM request_meter_snapshots WHERE request_id=$1`, fact.requestID).Scan(&priceID); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.QueryRow(f.ctx, `SELECT cost_version_id FROM attempt_meter_snapshots WHERE attempt_id=$1`, fact.attempt).Scan(&costID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, query string
		arg         string
	}{
		{"schema", `UPDATE meter_schemas SET name=name WHERE id=(SELECT schema_id FROM model_capability_versions WHERE id=$1)`, f.profile},
		{"schema dimension", `UPDATE meter_schema_dimensions SET unit=unit WHERE schema_id=(SELECT schema_id FROM model_capability_versions WHERE id=$1)`, f.profile},
		{"capability", `UPDATE model_capability_versions SET profile=profile WHERE id=$1`, f.profile},
		{"capability variant", `UPDATE model_capability_variants SET parameters=parameters WHERE profile_version=$1`, f.profile},
		{"capability bound", `UPDATE model_capability_bound_lines SET quantity=quantity WHERE profile_version=$1`, f.profile},
		{"price", `UPDATE price_versions SET effective_at=effective_at WHERE id=$1`, priceID},
		{"price rate", `UPDATE price_rate_lines SET numerator=numerator WHERE price_version_id=$1`, priceID},
		{"cost", `UPDATE cost_versions SET effective_at=effective_at WHERE id=$1`, costID},
		{"cost rate", `UPDATE cost_rate_lines SET numerator=numerator WHERE cost_version_id=$1`, costID},
		{"attempt snapshot", `UPDATE attempt_meter_snapshots SET plan_digest=plan_digest WHERE attempt_id=$1`, fact.attempt},
		{"attempt bound", `UPDATE attempt_meter_bound_lines SET quantity=quantity WHERE attempt_id=$1`, fact.attempt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectPoolReject(t, f.owner, tc.name, tc.query, tc.arg)
		})
	}
}

func TestUnifiedTextMeteringCurrentConcurrentAttemptAndFinalizationIdentity(t *testing.T) {
	f := newGuardFixture(t)
	requestID := uuid.NewString()
	reserveInput := billingReserveInput(f, f.a)
	reserveInput.RequestID = requestID
	reservation, err := f.store.Reserve(f.ctx, reserveInput)
	if err != nil {
		t.Fatal(err)
	}
	attemptInput := billing.AttemptInput{TenantID: f.a.tenant, RequestID: requestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "guard-upstream", Sequence: 1}
	attemptErrors := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.store.RecordAttempt(f.ctx, attemptInput)
			attemptErrors <- e
		}()
	}
	wg.Wait()
	close(attemptErrors)
	for e := range attemptErrors {
		if e != nil {
			t.Fatalf("same attempt identity was not idempotent under concurrency: %v", e)
		}
	}
	var attemptID string
	if err = f.owner.QueryRow(f.ctx, `SELECT id FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2 AND sequence=1`, f.a.tenant, requestID).Scan(&attemptID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2`, f.a.tenant, requestID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent attempt identity left count=%d err=%v", count, err)
	}
	if reservation.ID == "" || attemptID == "" {
		t.Fatal("valid request/attempt facts were not created")
	}

	finalID, sourceEventID := uuid.NewString(), uuid.NewString()
	finalInput := billing.FinalizationInput{
		ID: finalID, TenantID: f.a.tenant, RequestID: requestID, AttemptID: attemptID,
		Source: "gateway", SourceEventID: sourceEventID,
		Usage:           billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none", ContentWritten: false,
	}
	finalErrors := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.store.RecordFinalization(f.ctx, finalInput)
			finalErrors <- e
		}()
	}
	wg.Wait()
	close(finalErrors)
	for e := range finalErrors {
		if e != nil {
			t.Fatalf("same finalization identity was not idempotent under concurrency: %v", e)
		}
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM finalization_records WHERE id=$1`, finalID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent finalization left count=%d err=%v", count, err)
	}
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM metering_events WHERE tenant_id=$1 AND source=$2 AND source_event_id=$3`, f.a.tenant, "gateway", sourceEventID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent observation identity left count=%d err=%v", count, err)
	}
}

func TestUnifiedTextMeteringCurrentManagedProcurementGuardFacts(t *testing.T) {
	f := newGuardFixture(t)
	inRate, outRate := int64(1000000), int64(1000000)
	meterquote.PublishCost(t, f.ctx, f.owner, meterquote.CostPrice{
		ChannelID: f.channel, UpstreamModel: "guard-upstream", ModelID: f.model,
		Actor: f.operator, Basis: "contract", InputRate: &inRate, OutputRate: &outRate,
	})
	accountID := uuid.NewString()
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'guard-account','fixture','active',1000000)`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO credential_procurement_bindings(id,credential_id,channel_id,account_id,operation_id) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), f.credential, f.channel, accountID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	requestID := uuid.NewString()
	reserveInput := billingReserveInput(f, f.a)
	reserveInput.RequestID = requestID
	reservation, err := f.store.Reserve(f.ctx, reserveInput)
	if err != nil {
		t.Fatal(err)
	}
	quote, err := meterquote.Select(f.ctx, f.owner, f.model, f.channel, "guard-upstream")
	if err != nil {
		t.Fatal(err)
	}
	attemptInput := billing.AttemptInput{TenantID: f.a.tenant, RequestID: requestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "guard-upstream", Sequence: 1, CostQuote: &quote}
	attempt, err := f.store.RecordAttempt(f.ctx, attemptInput)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	var upper string
	if err = f.owner.QueryRow(f.ctx, `SELECT state,upper_amount::text FROM procurement_reservations WHERE attempt_id=$1`, attempt.ID).Scan(&state, &upper); err != nil {
		t.Fatal(err)
	}
	if state != "prepared" || upper != "120" {
		t.Fatalf("valid supplier projection state=%s upper=%s", state, upper)
	}
	if reservation.ID == "" {
		t.Fatal("valid customer reservation missing")
	}

	expectPoolReject(t, f.owner, "procurement reservation identity", `UPDATE procurement_reservations SET binding_id=$2 WHERE attempt_id=$1`, attempt.ID, uuid.NewString())
	expectPoolReject(t, f.owner, "procurement reservation bound amount", `UPDATE procurement_reservations SET upper_amount=upper_amount+1 WHERE attempt_id=$1`, attempt.ID)
	expectPoolReject(t, f.owner, "procurement reservation history", `DELETE FROM procurement_reservations WHERE attempt_id=$1`, attempt.ID)
	expectPoolReject(t, f.owner, "cross-request procurement reservation", `UPDATE procurement_reservations SET attempt_id=$2 WHERE attempt_id=$1`, attempt.ID, uuid.NewString())
	var rows int
	if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM procurement_reservations WHERE attempt_id=$1`, attempt.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rejected procurement writes changed the valid fact count=%d err=%v", rows, err)
	}
}

func TestUnifiedTextMeteringCurrentActualLoginMeterPermissions(t *testing.T) {
	f := newGuardFixture(t)
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	type loginRole struct{ name, password string }
	roles := []loginRole{
		{name: "guard_login_app_" + suffix, password: "app-" + suffix},
		{name: "guard_login_projection_" + suffix, password: "projection-" + suffix},
		{name: "guard_login_worker_" + suffix, password: "worker-" + suffix},
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
	base, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	base.ConnConfig.RuntimeParams["search_path"] = f.schema
	connect := func(role loginRole) *pgxpool.Pool {
		cfg := base.Copy()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role.name, role.password
		cfg.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
		if err != nil {
			t.Fatalf("connect %s: %v", role.name, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	app, projection, worker := connect(roles[0]), connect(roles[1]), connect(roles[2])
	var current string
	if err = app.QueryRow(f.ctx, `SELECT current_user`).Scan(&current); err != nil || current != roles[0].name {
		t.Fatalf("APP login identity=%q err=%v", current, err)
	}
	if _, err = app.Exec(f.ctx, `SELECT count(*) FROM meter_schemas`); err != nil {
		t.Fatalf("APP cannot read meter history: %v", err)
	}
	if _, err = app.Exec(f.ctx, `UPDATE meter_schemas SET name=name`); err == nil {
		t.Fatal("APP updated immutable meter history")
	}
	for name, pool := range map[string]*pgxpool.Pool{"projection": projection, "worker": worker} {
		if _, err = pool.Exec(f.ctx, `SELECT count(*) FROM meter_schemas`); err == nil {
			t.Fatalf("%s LOGIN unexpectedly read meter history", name)
		}
	}
	var appExec, projectionExec, workerExec bool
	fn := pgx.Identifier{f.schema}.Sanitize() + `.meter_token_amount(bigint,bigint,bigint,bigint,bigint,bigint)`
	if err = f.owner.QueryRow(f.ctx, `SELECT has_function_privilege($1,$2,'EXECUTE'),has_function_privilege($3,$2,'EXECUTE'),has_function_privilege($4,$2,'EXECUTE')`, roles[0].name, fn, roles[1].name, roles[2].name).Scan(&appExec, &projectionExec, &workerExec); err != nil {
		t.Fatal(err)
	}
	if !appExec || projectionExec || workerExec {
		t.Fatalf("meter function LOGIN ACL mismatch app=%t projection=%t worker=%t", appExec, projectionExec, workerExec)
	}
}
