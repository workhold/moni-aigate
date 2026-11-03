package procurementrequestbirth_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

// The shared fixture deliberately retains a real original legacy hold. Close
// it through the unchanged APP API, never through owner financial/source DML.
func birthBudgetBaseline(t *testing.T, f *fixture) string {
	t.Helper()
	closed, err := billing.New(f.app).Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: f.legacy, Reason: "not_sent"})
	must(t, "release legitimate untracked baseline through APP", err)
	if closed.Status != "released" || closed.ChargedAmount != 0 {
		t.Fatal("legacy baseline was not actually released")
	}
	var unchanged bool
	must(t, "legacy release does not backfill a birth", f.owner.QueryRow(f.ctx, `SELECT pp_origin_root_id IS NULL AND pp_origin_root_step IS NULL AND pp_origin_xid IS NULL AND pp_last_decision_id IS NULL AND NOT EXISTS(SELECT 1 FROM pp_request_origins WHERE request_id=$1) FROM request_records WHERE id=$1`, f.legacy).Scan(&unchanged))
	if !unchanged {
		t.Fatal("legacy cleanup registered retrospective evidence")
	}
	birthBudgetWallet(t, f, 10000, 0)
	admin := uuid.NewString()
	_, err = f.owner.Exec(f.ctx, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-disabled','admin')", admin, admin+"@example.invalid")
	must(t, "prepare separate legitimate management actor", err)
	return admin
}

func birthBudgetWallet(t *testing.T, f *fixture, available, held int64) {
	t.Helper()
	store := billing.New(f.app)
	w, err := store.Wallet(f.ctx, billing.Query{TenantID: f.tenant})
	must(t, "read actual APP wallet", err)
	if int64(w.Available) != available || int64(w.Held) != held {
		t.Fatal("single wallet differs from independent reservation arithmetic")
	}
	var keyHeld, keySpent int64
	must(t, "read actual Key budget", f.owner.QueryRow(f.ctx, "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&keyHeld, &keySpent))
	if keyHeld != held || keySpent != 0 {
		t.Fatal("Key held/spent differs from single customer financial effect")
	}
	ledger, err := store.VerifyLedger(f.ctx, billing.Query{TenantID: f.tenant})
	must(t, "run original ledger and funding verification", err)
	if !ledger.Balanced || ledger.AccountMismatches != 0 || ledger.UnbalancedJournals != 0 {
		t.Fatal("original financial verification failed")
	}
}

func birthBudgetClock(t *testing.T, f *fixture) time.Time {
	t.Helper()
	var at time.Time
	must(t, "read real database clock", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&at))
	return at.UTC()
}

func birthBudgetConfigure(t *testing.T, f *fixture, admin, tenantState, keyState string) map[string]string {
	t.Helper()
	commander, err := periodicbudget.NewCommander(f.app)
	must(t, "construct real APP Commander", err)
	controls := map[string]string{}
	for _, item := range []struct{ scope, state string }{{"tenant", tenantState}, {"key", keyState}} {
		if item.state == "none" {
			continue
		}
		limit := "100000"
		if item.state == "disabled" {
			limit = "0"
		}
		in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_day", Enabled: item.state == "enabled", LimitAmount: limit}
		var result periodicbudget.ConfigureResult
		if item.scope == "tenant" {
			result, err = commander.ConfigurePlatformTenant(f.ctx, admin, f.tenant, in)
		} else {
			result, err = commander.ConfigureKey(f.ctx, f.user, f.tenant, f.key, in)
		}
		must(t, "configure real "+item.scope+" "+item.state, err)
		must(t, "validate original immutable Commander receipt", result.Receipt.Validate())
		if !result.Created || result.Receipt.Operation.Revision != "1" || result.Receipt.Result.Enabled != in.Enabled {
			t.Fatal("new control configuration differs from original API receipt")
		}
		control := result.Receipt.Operation.ResourceID
		controls[item.scope] = control
		var tracking, effective time.Time
		var enabled bool
		var actualLimit string
		must(t, "observe actual control calendar without replacing pb_now", f.owner.QueryRow(f.ctx, "SELECT tracking_started_at,effective_from,enabled,limit_amount::text FROM periodic_budget_controls WHERE id=$1", control).Scan(&tracking, &effective, &enabled, &actualLimit))
		tracking = tracking.UTC()
		next := time.Date(tracking.Year(), tracking.Month(), tracking.Day()+1, 0, 0, 0, 0, time.UTC)
		if !effective.Equal(next) || enabled != in.Enabled || actualLimit != limit {
			t.Fatal("control changed original next-UTC-boundary or enabled/limit contract")
		}
	}
	return controls
}

func birthBudgetReserve(t *testing.T, f *fixture, in billing.ReserveInput, amount int64) billing.Reservation {
	t.Helper()
	before := birthBudgetClock(t, f)
	r, err := billing.New(f.app).Reserve(f.ctx, in)
	must(t, "actual APP Reserve under unchanged A1 ACL", err)
	after := birthBudgetClock(t, f)
	if int64(r.Amount) != amount {
		t.Fatalf("actual reservation=%d, independent expected=%d", r.Amount, amount)
	}
	f.checkBirth(t, r.RequestID)
	var priced, admitted time.Time
	must(t, "observe original separate retail and PB clocks", f.owner.QueryRow(f.ctx, "SELECT retail_priced_at,pb_admitted_at FROM request_records WHERE id=$1", r.RequestID).Scan(&priced, &admitted))
	if priced.Before(before) || priced.After(after) || admitted.Before(before) || admitted.After(after) || !priced.Equal(r.RetailPricedAt) {
		t.Fatal("actual stage clocks are not inside the original invocation")
	}
	birthBudgetWallet(t, f, 10000-amount, amount)
	return r
}

func birthBudgetRows(t *testing.T, f *fixture, tables []string) string {
	t.Helper()
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		parts = append(parts, "('"+table+"',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb) FROM "+pgx.Identifier{f.b.Schema, table}.Sanitize()+" x))")
	}
	var value string
	must(t, "read independent full-row business snapshot", f.owner.QueryRow(f.ctx, "SELECT jsonb_object_agg(k,v)::text FROM (VALUES "+strings.Join(parts, ",")+") actual(k,v)").Scan(&value))
	return value
}

func birthBudgetSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	return f.snapshot(t) + birthBudgetRows(t, f, []string{"settlements", "reconciliation_cases", "billing_profile_blocks", "periodic_budget_operations", "periodic_budget_configuration_versions", "price_groups", "pricing_operations", "tenant_pricing_assignments", "price_versions"})
}

func birthBudgetPeriodic(t *testing.T, f *fixture) string {
	t.Helper()
	return birthBudgetRows(t, f, []string{"periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events"})
}

func birthBudgetFrozen(t *testing.T, f *fixture, request string) string {
	t.Helper()
	var result string
	must(t, "retain immutable complete birth graph", f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('origin',to_jsonb(o),'head',to_jsonb(h),'step',to_jsonb(s),'root',to_jsonb(rt))::text
 FROM pp_request_origins o JOIN pp_request_heads h ON h.request_id=o.request_id JOIN pp_root_steps s ON s.root_id=o.root_id AND s.step_no=o.step_no JOIN pp_operation_roots rt ON rt.id=o.root_id WHERE o.request_id=$1`, request).Scan(&result))
	return result
}

// One new request in a fresh scope: every count and amount has an independent
// expected value. Disabled controls still track, and none has no substitute ID.
func birthBudgetEffects(t *testing.T, f *fixture, r billing.Reservation, controls map[string]string, released bool) {
	t.Helper()
	var tenantMarker, keyMarker *string
	must(t, "read exact nullable request markers", f.owner.QueryRow(f.ctx, "SELECT pb_tenant_control,pb_key_control FROM request_records WHERE id=$1", r.RequestID).Scan(&tenantMarker, &keyMarker))
	for _, item := range []struct {
		scope  string
		marker *string
	}{{"tenant", tenantMarker}, {"key", keyMarker}} {
		id, configured := controls[item.scope]
		if !configured {
			var absent bool
			must(t, "unconfigured scope remains absent", f.owner.QueryRow(f.ctx, "SELECT NOT EXISTS(SELECT 1 FROM periodic_budget_controls WHERE scope=$1 AND tenant_id=$2)", item.scope, f.tenant).Scan(&absent))
			if item.marker != nil || !absent {
				t.Fatal("unconfigured scope acquired a marker or control")
			}
			continue
		}
		if item.marker == nil || *item.marker != id {
			t.Fatal("request marker differs from real configured control identity")
		}
		phase, held, state := int64(1), int64(r.Amount), "held"
		if released {
			phase, held, state = 2, 0, "released"
		}
		var exact bool
		must(t, "independent control/window/allocation/effect conservation", f.owner.QueryRow(f.ctx, `SELECT
 c.held_amount=$3::numeric AND c.spent_amount=0 AND c.event_seq=$4
 AND a.origin='new' AND a.origin_source_id=$5 AND a.upper_amount=$6::bigint AND a.baseline=0
 AND a.held_amount=$3::numeric AND a.spent_amount=0 AND a.source_held=$3::numeric AND a.source_spent=0 AND a.revision=$4 AND a.state=$7
 AND a.admitted_at=r.pb_admitted_at AND w.control_id=c.id AND w.start_at<=r.pb_admitted_at AND r.pb_admitted_at<w.end_at AND w.held_amount=$3::numeric AND w.spent_amount=0
 AND (SELECT count(*) FROM periodic_budget_allocations WHERE control_id=c.id AND subject_id=r.id)=1
 AND (SELECT count(*) FROM periodic_budget_events WHERE control_id=c.id AND allocation_id=a.id)=$4
 AND EXISTS(SELECT 1 FROM periodic_budget_events e WHERE e.control_id=c.id AND e.allocation_id=a.id AND e.source_kind='reservation' AND e.source_id=$5 AND e.allocation_before_revision=0 AND e.before_held=0 AND e.before_spent=0 AND e.after_held=$6::numeric AND e.after_spent=0 AND e.after_source_spent=0 AND e.after_state='held')
 AND (NOT $8::boolean OR EXISTS(SELECT 1 FROM periodic_budget_events e JOIN settlements z ON z.id=e.source_id WHERE e.control_id=c.id AND e.allocation_id=a.id AND e.source_kind='settlement' AND z.request_id=r.id AND z.status='released' AND z.charged_amount=0 AND e.allocation_before_revision=1 AND e.before_held=$6::numeric AND e.after_held=0 AND e.after_spent=0 AND e.after_source_spent=0 AND e.after_state='released'))
 FROM periodic_budget_controls c JOIN periodic_budget_allocations a ON a.control_id=c.id JOIN request_records r ON r.id=a.subject_id JOIN periodic_budget_windows w ON w.id=a.window_id
 WHERE c.id=$1 AND a.subject_type='customer_request' AND a.subject_id=$2`, id, r.RequestID, strconv.FormatInt(held, 10), phase, r.ID, strconv.FormatInt(int64(r.Amount), 10), state, released).Scan(&exact))
		if !exact {
			t.Fatal("periodic scope duplicated money, lost its original window, or changed exact source/effect fields")
		}
	}
	var allocations int
	must(t, "count scopes independently of wallet", f.owner.QueryRow(f.ctx, "SELECT count(*) FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=$1", r.RequestID).Scan(&allocations))
	if allocations != len(controls) {
		t.Fatal("scope count differs from independently configured tenant and Key")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pb_check_customer($1)", r.RequestID)
	must(t, "run unchanged original020 customer checker", err)
}

func birthBudgetPendingAndRelease(t *testing.T, f *fixture, r billing.Reservation, controls map[string]string) {
	t.Helper()
	store := billing.New(f.app)
	frozen, periodic := birthBudgetFrozen(t, f, r.RequestID), birthBudgetPeriodic(t, f)
	pending, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	must(t, "live Settle with no finalization follows actual pending path", err)
	if pending.Status != "pending_reconciliation" || pending.Reason != "delivery_unknown" {
		t.Fatal("no finalization was treated as known settled consumption")
	}
	must(t, "actual MarkPending existing-case path", store.MarkPending(f.ctx, billing.PendingInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "a1-budget-followup"}))
	var pendingExact bool
	must(t, "pending case exists only after original status updates", f.owner.QueryRow(f.ctx, `SELECT r.status='accepted' AND r.billing_status='pending_reconciliation' AND v.status='pending_reconciliation' AND (SELECT count(*) FROM reconciliation_cases WHERE tenant_id=r.tenant_id AND request_id=r.id AND reason='a1-budget-followup')=1 AND NOT EXISTS(SELECT 1 FROM settlements WHERE request_id=r.id) FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=$1`, r.RequestID).Scan(&pendingExact))
	if !pendingExact || birthBudgetFrozen(t, f, r.RequestID) != frozen || birthBudgetPeriodic(t, f) != periodic {
		t.Fatal("pending changed frozen initial facts or financial/PB effects")
	}
	birthBudgetWallet(t, f, 10000-int64(r.Amount), int64(r.Amount))
	f.checkBirth(t, r.RequestID)
	birthBudgetEffects(t, f, r, controls, false)
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	must(t, "actual APP Release from pending", err)
	if released.Status != "released" || released.Reason != "released_not_sent" || released.ChargedAmount != 0 || released.RawRetailAmount != "0" || released.WaivedRetailAmount != "0" {
		t.Fatal("original release did not produce the unique zero-charge settlement")
	}
	var terminal bool
	must(t, "original terminal companions are complete", f.owner.QueryRow(f.ctx, `SELECT r.status='failed' AND r.billing_status='released' AND v.status='released' AND (SELECT count(*) FROM settlements WHERE request_id=r.id)=1 AND EXISTS(SELECT 1 FROM reconciliation_cases WHERE request_id=r.id AND state='closed') AND NOT EXISTS(SELECT 1 FROM recovery_jobs WHERE request_id=r.id) FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=$1`, r.RequestID).Scan(&terminal))
	if !terminal || birthBudgetFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("release changed immutable birth or left incomplete terminal companions")
	}
	birthBudgetWallet(t, f, 10000, 0)
	f.checkBirth(t, r.RequestID)
	birthBudgetEffects(t, f, r, controls, true)
	before := birthBudgetSnapshot(t, f)
	for _, call := range []func() (billing.Settlement, error){
		func() (billing.Settlement, error) {
			return store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
		},
		func() (billing.Settlement, error) {
			return store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
		},
	} {
		again, e := call()
		must(t, "read unique existing settlement without replay effects", e)
		if again.ID != released.ID || again.Status != "released" || birthBudgetSnapshot(t, f) != before {
			t.Fatal("terminal replay generated another source or financial effect")
		}
	}
}

func TestPostgresA1BudgetScopeMatrixPendingAndRelease(t *testing.T) {
	for _, tenantState := range []string{"none", "enabled", "disabled"} {
		for _, keyState := range []string{"none", "enabled", "disabled"} {
			t.Run(tenantState+"_"+keyState, func(t *testing.T) {
				f := newFixture(t)
				admin := birthBudgetBaseline(t, f)
				controls := birthBudgetConfigure(t, f, admin, tenantState, keyState)
				r := birthBudgetReserve(t, f, f.input(), 120)
				birthBudgetEffects(t, f, r, controls, false)
				birthBudgetPendingAndRelease(t, f, r, controls)
			})
		}
	}
}

func TestPostgresA1BudgetZeroPriceAndInputOnly(t *testing.T) {
	for _, kind := range []string{"zero_price", "input_only"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			admin := birthBudgetBaseline(t, f)
			controls := birthBudgetConfigure(t, f, admin, "enabled", "disabled")
			store := catalog.New(f.app, nil)
			in, amount, inputRate := f.input(), int64(0), "0"
			if kind == "input_only" {
				model, err := store.CreateModel(f.ctx, admin, catalog.ModelInput{PublicName: "a1-embeddings", Protocol: provider.ProtocolEmbeddings, ContextLimit: 100, InputUpperBound: 100})
				must(t, "create original valid input-only profile", err)
				_, err = f.owner.Exec(f.ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, model)
				must(t, "prepare legitimate input-only tenant grant", err)
				_, err = f.owner.Exec(f.ctx, "UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", f.key, model)
				must(t, "prepare legitimate input-only Key scope", err)
				in.ModelID, in.OutputLimit = model, 0
				must(t, "read actually published profile version", f.app.QueryRow(f.ctx, "SELECT profile_version FROM models WHERE id=$1", model).Scan(&in.ProfileVersion))
				amount, inputRate = 200, "2000000"
			}
			price, err := store.PublishPrice(f.ctx, admin, in.ModelID, inputRate, "0", time.Time{})
			must(t, "publish actual effective zero/output-zero price", err)
			if kind == "input_only" {
				before := birthBudgetSnapshot(t, f)
				bad := in
				bad.OutputLimit = 1
				_, err = billing.New(f.app).Reserve(f.ctx, bad)
				var business *billing.Error
				if !errors.As(err, &business) || business.Code != "invalid_bounds" || birthBudgetSnapshot(t, f) != before {
					t.Fatal("input-only nonzero output was accepted or left partial facts")
				}
				_, err = store.PublishPrice(f.ctx, admin, in.ModelID, inputRate, "1", time.Time{})
				if !errors.Is(err, catalog.ErrInvalid) || birthBudgetSnapshot(t, f) != before {
					t.Fatal("original catalog accepted input-only output price or wrote partial configuration")
				}
			}
			r := birthBudgetReserve(t, f, in, amount)
			if r.PriceVersionID != price || r.OutputRate != 0 {
				t.Fatal("zero/input-only request did not freeze the real published price")
			}
			birthBudgetEffects(t, f, r, controls, false)
			birthBudgetPendingAndRelease(t, f, r, controls)
		})
	}
}

func TestPostgresA1BudgetPriceGroupSnapshotAndNoFallback(t *testing.T) {
	f := newFixture(t)
	admin := birthBudgetBaseline(t, f)
	store, actor := catalog.New(f.app, nil), catalog.PricingActor{UserID: admin}
	group, err := store.CreatePriceGroup(f.ctx, actor, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "A1 exact group"})
	must(t, "create actual price group", err)
	assigned, err := store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 1, GroupID: &group.Result.ID})
	must(t, "assign actual tenant price domain", err)
	before := birthBudgetSnapshot(t, f)
	_, err = billing.New(f.app).Reserve(f.ctx, f.input())
	var business *billing.Error
	if !errors.As(err, &business) || business.Code != "price_unavailable" || birthBudgetSnapshot(t, f) != before {
		t.Fatal("missing group price fell back to default or left partial birth/finances")
	}
	price, err := store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group.Result.ID, ModelID: f.model, InputRate: 2000000, OutputRate: 3000000, EffectiveAt: birthBudgetClock(t, f).Add(-time.Minute)})
	must(t, "publish actual immutable group price", err)
	r := birthBudgetReserve(t, f, f.input(), 260)
	if r.PriceVersionID != price.Result.ID || r.PricingGroupID == nil || *r.PricingGroupID != group.Result.ID || strconv.FormatInt(r.TenantPricingRevision, 10) != assigned.Result.PricingRevision || r.InputRate != 2000000 || r.OutputRate != 3000000 {
		t.Fatal("group price ID, exact dimensions or tenant revision differ from real command receipts")
	}
	frozen := birthBudgetFrozen(t, f, r.RequestID)
	_, err = store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group.Result.ID, ModelID: f.model, InputRate: 7000000, OutputRate: 9000000, EffectiveAt: birthBudgetClock(t, f)})
	must(t, "append later group price without rewriting request", err)
	_, err = store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 2})
	must(t, "return tenant to default domain through actual command", err)
	if birthBudgetFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("current group assignment or new price rewrote immutable old snapshot")
	}
	birthBudgetPendingAndRelease(t, f, r, map[string]string{})
}

func TestPostgresA1BudgetRecoveryWithoutAttemptReleases(t *testing.T) {
	f := newFixture(t)
	admin := birthBudgetBaseline(t, f)
	controls := birthBudgetConfigure(t, f, admin, "disabled", "enabled")
	r := birthBudgetReserve(t, f, f.input(), 120)
	frozen := birthBudgetFrozen(t, f, r.RequestID)
	settled, err := billing.New(f.app).Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "recovery"})
	must(t, "actual recovery API with no attempt", err)
	if settled.Status != "released" || settled.Reason != "released_no_delivery" || settled.ChargedAmount != 0 || birthBudgetFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("no-attempt recovery invented consumption or changed birth")
	}
	birthBudgetWallet(t, f, 10000, 0)
	birthBudgetEffects(t, f, r, controls, true)
	f.checkBirth(t, r.RequestID)
	var noGeneration bool
	must(t, "recovery created no attempt, finalization or pending case", f.owner.QueryRow(f.ctx, "SELECT NOT EXISTS(SELECT 1 FROM upstream_attempts WHERE request_id=$1) AND NOT EXISTS(SELECT 1 FROM finalization_records WHERE request_id=$1) AND NOT EXISTS(SELECT 1 FROM reconciliation_cases WHERE request_id=$1)", r.RequestID).Scan(&noGeneration))
	if !noGeneration {
		t.Fatal("no-evidence recovery synthesized a generation or case")
	}
}

func birthBudgetFault(t *testing.T, f *fixture, table, condition, state string) func() {
	t.Helper()
	body := fmt.Sprintf("CREATE FUNCTION a1_budget_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF %s THEN RAISE EXCEPTION 'a1_budget_injected_fault' USING ERRCODE='%s'; END IF; RETURN NEW; END$$", condition, state)
	_, err := f.owner.Exec(f.ctx, body)
	must(t, "install isolated business effect fault", err)
	quoted := pgx.Identifier{table}.Sanitize()
	_, err = f.owner.Exec(f.ctx, "CREATE TRIGGER zz_a1_budget_fault BEFORE INSERT ON "+quoted+" FOR EACH ROW EXECUTE FUNCTION a1_budget_fault()")
	must(t, "attach exact business effect fault", err)
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		_, e := f.owner.Exec(f.ctx, "DROP TRIGGER zz_a1_budget_fault ON "+quoted)
		must(t, "remove exact effect fault trigger", e)
		_, e = f.owner.Exec(f.ctx, "DROP FUNCTION a1_budget_fault()")
		must(t, "remove exact effect fault function", e)
	}
	t.Cleanup(remove)
	return remove
}

func TestPostgresA1BudgetBusinessEffectFailuresRollback(t *testing.T) {
	f := newFixture(t)
	admin := birthBudgetBaseline(t, f)
	birthBudgetConfigure(t, f, admin, "enabled", "disabled")
	store := billing.New(f.app)
	anchor := birthBudgetReserve(t, f, f.input(), 120)
	_, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: anchor.RequestID, Reason: "not_sent"})
	must(t, "retain an already committed tracked root before every fault", err)
	anchorFrozen := birthBudgetFrozen(t, f, anchor.RequestID)
	in := f.input()
	remove := birthBudgetFault(t, f, "periodic_budget_events", "NEW.source_kind='reservation'", "PZ981")
	before := birthBudgetSnapshot(t, f)
	_, err = store.Reserve(f.ctx, in)
	code(t, err, "PZ981")
	if birthBudgetSnapshot(t, f) != before {
		t.Fatal("failed real reservation effect retained birth, idempotency or financial/PB writes")
	}
	remove()
	r, err := store.Reserve(f.ctx, in)
	must(t, "same original Reserve succeeds after exact fault removal", err)
	f.checkBirth(t, r.RequestID)
	remove = birthBudgetFault(t, f, "reconciliation_cases", "true", "PZ982")
	before = birthBudgetSnapshot(t, f)
	_, err = store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	code(t, err, "PZ982")
	if birthBudgetSnapshot(t, f) != before {
		t.Fatal("failed pending case retained a half status/case or changed funds")
	}
	remove()
	pending, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	must(t, "same live Settle succeeds after pending fault removal", err)
	if pending.Status != "pending_reconciliation" {
		t.Fatal("fault-free no-finalization Settle was not pending")
	}
	remove = birthBudgetFault(t, f, "periodic_budget_events", "NEW.source_kind='settlement'", "PZ983")
	before = birthBudgetSnapshot(t, f)
	_, err = store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	code(t, err, "PZ983")
	if birthBudgetSnapshot(t, f) != before {
		t.Fatal("failed release PB effect retained settlement, journal, Key or case changes")
	}
	remove()
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	must(t, "same real Release succeeds after exact effect fault removal", err)
	if released.Status != "released" || released.ChargedAmount != 0 || birthBudgetFrozen(t, f, anchor.RequestID) != anchorFrozen {
		t.Fatal("failure/recovery changed prior committed root or unique release")
	}
	f.checkBirth(t, anchor.RequestID)
	f.checkBirth(t, r.RequestID)
	birthBudgetWallet(t, f, 10000, 0)
}
