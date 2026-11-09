package migrations

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestUnifiedTextMeteringRejectsEveryListedLegacyRelation(t *testing.T) {
	newID := uuid.NewString
	newDigest := func() string { return strings.Repeat("a", 64) }
	newRow := func(table string) string {
		return "unified text metering refuses existing " + table + " history"
	}
	tests := []struct {
		name, want string
		setup      func(*testing.T, *prefixFixture)
	}{
		{
			name: "request_records", want: newRow("request_records"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO request_records(id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming) VALUES($1,$2,$3,$4,$5,$6,0,0,0,0,$7,'legacy',$8,clock_timestamp(),'legacy',false)`, newID(), newID(), newID(), newID(), newID(), newID(), newID(), newDigest())
			},
		},
		{
			name: "request_idempotency", want: newRow("request_idempotency"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO request_idempotency(tenant_id,operation,key,request_hmac,request_id) VALUES($1,'inference',$2,$3,$4)`, newID(), newID(), newDigest(), newID())
			},
		},
		{
			name: "reservations", want: newRow("reservations"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline) VALUES($1,$2,$3,0,clock_timestamp()+interval '1 hour')`, newID(), newID(), newID())
			},
		},
		{
			name: "upstream_attempts", want: newRow("upstream_attempts"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO upstream_attempts(id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model) VALUES($1,$2,$3,1,$4,$5,$6,'legacy')`, newID(), newID(), newID(), newID(), newID(), newID())
			},
		},
		{
			name: "metering_events", want: newRow("metering_events"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO metering_events(id,tenant_id,request_id,source,source_event_id,normalized_usage,happened_at) VALUES($1,$2,$3,'legacy',$4,'{}'::jsonb,clock_timestamp())`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "metering_event_conflicts", want: newRow("metering_event_conflicts"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO metering_event_conflicts(id,event_id,normalized_usage) VALUES($1,$2,'{}'::jsonb)`, newID(), newID())
			},
		},
		{
			name: "finalization_records", want: newRow("finalization_records"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO finalization_records(id,tenant_id,request_id,event_id,input_digest,protocol_outcome,transport_end_reason,delivery_stage,content_written,policy_version) VALUES($1,$2,$3,$4,$5,'completed','done','none',false,'legacy')`, newID(), newID(), newID(), newID(), newDigest())
			},
		},
		{
			name: "settlements", want: newRow("settlements"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO settlements(id,tenant_id,request_id,charged_amount,raw_retail_amount,waived_retail_amount,status,reason,journal_id) VALUES($1,$2,$3,0,0,0,'released','legacy',$4)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "reconciliation_cases", want: newRow("reconciliation_cases"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO reconciliation_cases(id,tenant_id,request_id,reason) VALUES($1,$2,$3,'legacy')`, newID(), newID(), newID())
			},
		},
		{
			name: "recovery_jobs", want: newRow("recovery_jobs"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO recovery_jobs(request_id,tenant_id) VALUES($1,$2)`, newID(), newID())
			},
		},
		{
			name: "delivery_observations", want: newRow("delivery_observations"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO delivery_observations(id,tenant_id,request_id,stage) VALUES($1,$2,$3,'complete_local_write')`, newID(), newID(), newID())
			},
		},
		{
			name: "platform_probe_attempts", want: newRow("platform_probe_attempts"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO platform_probe_attempts(id,actor,channel_id,credential_id,channel_version_id,model_id,profile_version,upstream_model) VALUES($1,$2,$3,$4,$5,$6,$7,'legacy')`, newID(), newID(), newID(), newID(), newID(), newID(), newID())
			},
		},
		{
			name: "provider_cost_corrections", want: newRow("provider_cost_corrections"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO provider_cost_corrections(id,tenant_id,attempt_id,operation_id,amount,basis,evidence_ref) VALUES($1,$2,$3,$4,0,'contract','legacy')`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "procurement_reservations", want: newRow("procurement_reservations"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_reservations(attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,state) VALUES($1,$2,$3,1,0,1,1,'prepared')`, newID(), newID(), newID())
			},
		},
		{
			name: "procurement_budget_events", want: newRow("procurement_budget_events"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_budget_events(id,event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES($1,$2,$3,$4,0,0,'legacy')`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "procurement_evidence_identities", want: newRow("procurement_evidence_identities"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_evidence_identities(attempt_id,evidence_digest,event_id) VALUES($1,$2,$3)`, newID(), newDigest(), newID())
			},
		},
		{
			name: "procurement_cost_proposals", want: newRow("procurement_cost_proposals"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_cost_proposals(id,tenant_id,attempt_id,amount,basis,evidence_ref,reason,requester,expected_reservation_revision,expected_evidence_count) VALUES($1,$2,$3,0,'contract','legacy','legacy',$4,1,0)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "journals", want: newRow("journals"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,'legacy','legacy',$4)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "journal_lines", want: newRow("journal_lines"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO journal_lines(id,journal_id,account_id,signed_delta) VALUES($1,$2,$3,0)`, newID(), newID(), newID())
			},
		},
		{
			name: "funding_receipts", want: newRow("funding_receipts"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,journal_id) VALUES($1,$2,'manual_credit',$3,1,$4)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "wallet_openings", want: newRow("wallet_openings"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO wallet_openings(id,tenant_id,external_ref,amount,actor) VALUES($1,$2,$3,1,$4)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "funding_lots", want: newRow("funding_lots"), setup: func(t *testing.T, f *prefixFixture) {
				id := newID()
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_lots(id,tenant_id,funding_receipt_id,source_kind,source_id,refund_class,issued_amount,fifo_at) VALUES($1,$2,$1,'manual_credit',$3,'nonrefundable',1,clock_timestamp())`, id, newID(), newID())
			},
		},
		{
			name: "funding_lot_events", want: newRow("funding_lot_events"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed) VALUES($1,$2,$3,$4,$5,'funding',1,1,0,0)`, newID(), newID(), newID(), newID(), newID())
			},
		},
		{
			name: "funding_lot_balances", want: newRow("funding_lot_balances"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,held_amount,consumed_amount,revision) VALUES($1,$2,0,0,0,1)`, newID(), newID())
			},
		},
		{
			name: "funding_reservation_lots", want: newRow("funding_reservation_lots"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_reservation_lots(tenant_id,reservation_id,request_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,1,1)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "funding_settlement_lots", want: newRow("funding_settlement_lots"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_settlement_lots(tenant_id,settlement_id,reservation_id,allocation_index,lot_id,charged_amount,released_amount) VALUES($1,$2,$3,1,$4,0,0)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "funding_consumption_refund_lots", want: newRow("funding_consumption_refund_lots"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO funding_consumption_refund_lots(tenant_id,refund_order_id,settlement_id,allocation_index,lot_id,refund_index,amount) VALUES($1,$2,$3,1,$4,1,1)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "models", want: newRow("models"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO models(id,public_name,profile) VALUES($1,'legacy-model','{}'::jsonb)`, newID())
			},
		},
		{
			name: "model_capability_versions", want: newRow("model_capability_versions"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO model_capability_versions(id,model_id,profile) VALUES($1,$2,'{}'::jsonb)`, newID(), newID())
			},
		},
		{
			name: "route_policy_versions", want: newRow("route_policy_versions"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,1)`, newID(), newID())
			},
		},
		{
			name: "model_bindings", want: newRow("model_bindings"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'legacy',1,1)`, newID(), newID(), newID())
			},
		},
		{
			name: "price_versions", want: newRow("price_versions"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO price_versions(id,model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,$2,0,0,clock_timestamp(),$3)`, newID(), newID(), newID())
			},
		},
		{
			name: "cost_versions", want: newRow("cost_versions"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis) VALUES($1,$2,'legacy',0,0,'unknown')`, newID(), newID())
			},
		},
		{
			name: "pricing_operations", want: newRow("pricing_operations"), setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,revision,safe_input,input_fingerprint,result_snapshot) VALUES($1,'price_group.create',$2,'platform','legacy',$3,1,'{}'::jsonb,$4,'{}'::jsonb)`, newID(), newID(), newID(), newDigest())
			},
		},
		{
			name: "key_held", want: "unified text metering refuses key held or spent history", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,held) VALUES($1,$2,$3,'legacy','legacy',$4,ARRAY['openai_chat'],1)`, newID(), newID(), newID(), newDigest())
			},
		},
		{
			name: "procurement_spent", want: "unified text metering refuses procurement held or spent history", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount,spent_amount) VALUES($1,'legacy account','legacy provider','active',100,1)`, newID())
			},
		},
		{
			name: "ledger_nonzero", want: "unified text metering refuses a non-zero ledger balance", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO ledger_accounts(tenant_id,kind,balance) VALUES($1,'AVAILABLE',1)`, newID())
			},
		},
		{
			name: "periodic_budget_events", want: "unified text metering refuses periodic budget allocation history", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO periodic_budget_events(id,control_id,allocation_id,source_kind,source_id,seq,allocation_before_revision,before_held,before_spent,after_held,after_spent,after_source_spent,after_state,held_delta,spent_delta) VALUES($1,$2,$3,'legacy_import',$4,1,0,0,0,0,0,0,'released',0,0)`, newID(), newID(), newID(), newID())
			},
		},
		{
			name: "payment_captures", want: "unified text metering refuses payment capture history", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO payment_captures(id,order_id,identity_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,wallet_credit_amount,captured_at,provider_order_revision,verified_evidence_id,processing_generation) VALUES($1,$2,$3,$4,$5,'legacy','legacy-tx',1,1,clock_timestamp(),1,$6,1)`, newID(), newID(), newID(), newID(), newID(), newID())
			},
		},
		{
			name: "payment_effects", want: "unified text metering refuses payment capture history", setup: func(t *testing.T, f *prefixFixture) {
				insertLegacyReplicaFact(t, f, `INSERT INTO payment_effects(capture_id,identity_id,environment_id,merchant_id,provider_transaction_id) VALUES($1,$2,$3,'legacy','legacy-tx')`, newID(), newID(), newID())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newPrefixFixture(t)
			prefixMust(t, "install exact023 baseline", ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
			tc.setup(t, f)
			before := f.snapshot(t)
			err := Apply(f.ctx, f.pool)
			if err == nil {
				t.Fatal("current migration accepted listed legacy history")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("legacy history produced an unexpected error: %v", err)
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

func TestUnifiedTextMeteringRejectsChanged023DependencyBeforeDDL(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install exact023 baseline", ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
	f.exec(t, "ALTER TABLE price_versions DROP CONSTRAINT price_versions_domain_effective_key")
	before := f.snapshot(t)
	if err := Apply(f.ctx, f.pool); err == nil {
		t.Fatal("current migration accepted a changed 023 dependency")
	}
	if f.snapshot(t) != before {
		t.Fatal("changed dependency rejection changed the prefix schema or facts")
	}
	var meterSchemas bool
	prefixMust(t, "confirm no 024 relation after dependency rejection", f.pool.QueryRow(f.ctx, "SELECT to_regclass('meter_schemas') IS NOT NULL").Scan(&meterSchemas))
	if meterSchemas {
		t.Fatal("changed dependency rejection left a 024 relation")
	}
}
