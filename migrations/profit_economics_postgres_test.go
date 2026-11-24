package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestProfitEconomicsFxVersionSealed(t *testing.T) {
	f := newPhase3PrefixFixture(t)
	prefixMust(t, "apply FX seal fixture", migrations.ApplyCurrent(f.ctx, f.pool))
	actor, policy := uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'fixture')`, actor, actor+"@fixture.invalid")
	f.exec(t, `INSERT INTO fx_policy_versions(id,valuation_currency,source,quoted_at,valid_until,created_by) VALUES($1,'CNY','sealed-fixture',clock_timestamp()-interval '1 minute',clock_timestamp()+interval '1 hour',$2)`, policy, actor)
	_, err := f.pool.Exec(f.ctx, `INSERT INTO fx_policy_rates(policy_version_id,source_currency,target_currency,rate_num,rate_den) VALUES($1,'USD','CNY',7,1)`, policy)
	if err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("accepted a late FX rate into an immutable version: %v", err)
	}
	_, err = f.pool.Exec(f.ctx, `UPDATE fx_policy_versions SET source='rewrite' WHERE id=$1`, policy)
	if err == nil {
		t.Fatal("FX history was mutable")
	}
}

func TestProfitEconomicsCurrentMigration(t *testing.T) {
	f := newPhase3PrefixFixture(t)
	prefixMust(t, "apply P0 economics migration", migrations.ApplyCurrent(f.ctx, f.pool))
	prefixMust(t, "verify P0 economics migration", migrations.VerifyCurrent(f.ctx, f.pool))
	prefixMust(t, "reapply P0 economics migration", migrations.ApplyCurrent(f.ctx, f.pool))
	var tables, functions int
	prefixMust(t, "read P0 migration objects", f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('fx_policy_versions','fx_policy_rates','task_policy_versions','quality_policy_versions','quality_evidence','quality_evidence_withdrawals','execution_policy_versions','economics_policy_versions','service_offer_documents','service_offer_versions','service_offer_states','service_offer_lifecycle_events','request_economics_snapshots','attempt_cost_components')),
 (SELECT count(*) FROM information_schema.routines WHERE routine_schema=current_schema() AND routine_name IN ('request_economics_guard','profit_attempt_budget_guard','profit_fixed_full_settlement_guard','profit_offer_dispatch_guard'))`).Scan(&tables, &functions))
	if tables != 14 || functions != 4 {
		t.Fatalf("P0 migration objects tables=%d functions=%d", tables, functions)
	}
	var currencies bool
	prefixMust(t, "read supplier currency constraint", f.pool.QueryRow(f.ctx, `SELECT pg_get_constraintdef(oid) LIKE '%USD%' FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND conrelid='cost_versions'::regclass AND conname='cost_versions_check'`).Scan(&currencies))
	if !currencies {
		t.Fatal("USD supplier costs remain disabled")
	}
}
