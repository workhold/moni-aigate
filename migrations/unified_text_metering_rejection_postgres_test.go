package migrations

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func insertLegacyReplicaFact(t *testing.T, f *prefixFixture, query string, args ...any) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, "SET LOCAL session_replication_role='replica'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUnifiedTextMeteringRejectsZeroBalanceBusinessHistory(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *prefixFixture)
	}{
		{
			name: "key_spent",
			setup: func(t *testing.T, f *prefixFixture) {
				tenant, user, key := uuid.NewString(), uuid.NewString(), uuid.NewString()
				f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'legacy key tenant')`, tenant)
				f.exec(t, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'unused')`, user, user+"@legacy.invalid")
				f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, tenant, user)
				f.exec(t, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,spent,held) VALUES($1,$2,$3,'legacy key','mk_legacy','legacy-key-digest',ARRAY['openai_chat'],1,0)`, key, tenant, user)
			},
		},
		{
			name: "procurement_held",
			setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount,held_amount,spent_amount) VALUES($1,'legacy account','legacy provider','active',100,7,0)`, uuid.NewString())
			},
		},
		{
			name: "procurement_evidence_identity",
			setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_evidence_identities(attempt_id,evidence_digest,event_id) VALUES($1,$2,$3)`, uuid.NewString(), strings.Repeat("a", 64), uuid.NewString())
			},
		},
		{
			name: "periodic_allocation",
			setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO periodic_budget_allocations(id,control_id,subject_type,subject_id,origin,origin_source_id,upper_amount,baseline,source_held,source_spent,held_amount,spent_amount,state,revision) VALUES($1,$2,'supplier_attempt',$3,'legacy_import',$4,0,0,0,0,0,0,'held',0)`, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString())
			},
		},
		{
			name: "zero_balance_lot_child",
			setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,held_amount,consumed_amount,revision) VALUES($1,$2,0,0,0,1)`, uuid.NewString(), uuid.NewString())
			},
		},
		{
			name: "payment_identity",
			setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO payment_transaction_identities(id,environment_id,merchant_id,provider_transaction_id,order_id,tenant_id,amount,currency,captured_at,provider_order_revision,evidence_id,processing_generation) VALUES($1,$2,'legacy merchant','legacy transaction','legacy-order',$3,1,'CNY',clock_timestamp(),1,$4,1)`, uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPrefixFixture(t)
			prefixMust(t, "install exact023 baseline", ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
			tc.setup(t, f)
			before := f.snapshot(t)
			if err := Apply(f.ctx, f.pool); err == nil {
				t.Fatal("current migration accepted legacy business history")
			} else if !strings.Contains(err.Error(), "unified text metering refuses") {
				t.Fatalf("legacy history produced an unrelated error: %v", err)
			}
			if f.snapshot(t) != before {
				t.Fatal("rejected migration changed legacy facts or schema")
			}
			var count int
			prefixMust(t, "confirm exact023 history remains", f.pool.QueryRow(f.ctx, "SELECT count(*) FROM schema_migrations").Scan(&count))
			if count != 23 {
				t.Fatalf("rejected migration changed migration history: %d", count)
			}
			var meterSchemas bool
			prefixMust(t, "confirm no 024 relation", f.pool.QueryRow(f.ctx, "SELECT to_regclass('meter_schemas') IS NOT NULL").Scan(&meterSchemas))
			if meterSchemas {
				t.Fatal("rejected migration left a 024 relation")
			}
		})
	}
}
