package procurementattemptbirth_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// Frozen from the reviewed A2 contract and ordinary-column layouts, never from
// live grants/catalog. The SQL below contains literal expected columns. This is
// the managed first-window slice, not all A1, command, route/retry or HA paths.
// Explicit NULL INSERTs still require INSERT; generated columns only join reads.
type abGrantColumns struct{ table, columns string }

var abGrantCoreReads = []abGrantColumns{
	{"pp_operation_roots", "id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result"},
	{"pp_root_steps", "root_id,step_no,kind,source_id,created_xid,d_request_birth,d_attempt_birth,d_account_command,d_attempt_delivery,d_incoming,d_meter_choice,d_request_decision,d_normal_cost_decision,d_cost_mutation,d_cost_reset_decision,d_evidence_choice,d_procurement_transition,d_proposal_birth,d_approval_consumed,d_unmanaged_correction,d_recovery,d_cost_outbox,d_reconciliation"},
	{"pp_request_origins", "request_id,root_id,step_no,step_kind,created_xid,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,request_created_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,initial_latest_id,initial_status,initial_billing_status"},
	{"pp_request_heads", "request_id,ordinal,last_decision_id,updated_xid"},
	{"pp_attempt_origins", "attempt_id,root_id,step_no,step_kind,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at"},
	{"pp_attempt_heads", "attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid"},
	{"pp_attempt_nodes", "id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid,previous_ordinal"},
	{"pp_account_command_sources", "id,root_id,step_no,step_kind,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id"},
	{"pp_account_origins", "account_id,version,create_operation_id,command_source_id,root_id,step_no,step_kind,journal_id,created_xid,created_at"},
	{"pp_account_heads", "account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count"},
	{"pp_account_journal", "id,account_id,ordinal,previous_id,previous_ordinal,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,command_source_id,transition_id,delivery_source_id,command_operation_id,procurement_event_id,dispatch_attempt_id"},
	{"pp_procurement_transitions", "id,root_id,step_no,step_kind,created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,before_revision,before_state,before_held,before_spent,before_anomaly,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,d_attempt_birth,d_evidence_choice,d_approval_consumed,d_recovery"},
	{"request_records", "id,tenant_id,key_id,owner_id,model_id,status,billing_status,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,route_revision,request_deadline,instance_id,streaming,latest_finalization_id,created_at,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at,pb_created_xid,pb_tenant_control,pb_key_control,pb_admitted_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_last_decision_id"},
	{"upstream_attempts", "id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,delivery_state,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,cost_status,cost_amount,created_at,route_decisions,pb_created_xid,pb_dispatch_checked_at,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at,pp_last_cost_mutation_id,pp_last_delivery_source_id,pp_dispatch_source_id,pp_dispatch_root_id,pp_dispatch_root_step,pp_dispatch_xid,pp_dispatch_account_id,pp_dispatch_journal_id"},
	{"procurement_accounts", "id,label,provider,state,limit_amount,held_amount,spent_amount,revision,created_at,pp_version,pp_created_xid,pp_origin_journal_id"},
	{"procurement_reservations", "attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state,anomaly,revision,created_at,updated_at,pb_created_xid,pb_control,pb_admitted_at,pp_created_xid,pp_origin_journal_id,pp_last_journal_id"},
	{"procurement_budget_events", "id,event_key,account_id,attempt_id,held_delta,spent_delta,type,evidence_ref,created_at,pb_created_xid,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid"},
	{"procurement_commands", "operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,created_at,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id"},
	{"audit_events", "id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid"},
}

var abGrantPBReads = []abGrantColumns{
	{"periodic_budget_controls", "id,scope,tenant_id,key_id,account_id,period_kind,enabled,limit_amount,revision,tracking_started_at,effective_from,max_observed_window_start,held_amount,spent_amount,event_seq,last_event_id,created_xid,configuration_operation_id"},
	{"periodic_budget_windows", "id,control_id,start_at,end_at,held_amount,spent_amount,revision,last_event_id,observation_id,observation_created_xid"},
	{"periodic_budget_allocations", "id,control_id,subject_type,subject_id,origin,origin_source_id,window_id,admitted_at,upper_amount,baseline,source_held,source_spent,held_amount,spent_amount,state,revision,last_event_id,created_xid,created_at"},
	{"periodic_budget_events", "id,control_id,allocation_id,source_kind,source_id,seq,allocation_before_revision,window_before_revision,before_held,before_spent,after_held,after_spent,after_source_spent,after_state,held_delta,spent_delta,created_xid,created_at"},
	{"periodic_budget_observation_heads", "control_id,ordinal,last_observation_id,updated_xid"},
	{"periodic_budget_observation_journal", "id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,created_xid,observed_at,command_source_id,window_source_id"},
}

var abGrantAPPExtraReads = []abGrantColumns{
	{"periodic_budget_configuration_versions", "control_id,revision,operation_id,period_kind,enabled,limit_amount,actor,created_xid,created_at"},
	{"periodic_budget_observation_origins", "control_id,version,created_xid,tracking_started_at"},
}

var abGrantManagedInserts = []abGrantColumns{
	{"upstream_attempts", "id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,route_decisions,pp_origin_root_id,pp_origin_root_step,pp_origin_xid,pp_quote_selected_at"},
	{"procurement_reservations", "attempt_id,account_id,binding_id,input_bound,output_bound,upper_amount,held_amount,spent_amount,state"},
	{"pp_attempt_origins", "attempt_id,root_id,step_no,created_xid,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,route_decisions,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,quote_selected_at,attempt_created_at,pb_created_xid,initial_delivery_state,initial_cost_status,initial_cost_amount,management_mode,account_id,binding_id,procurement_transition_id,reserved_event_id,account_journal_id,admitted_at"},
	{"pp_attempt_heads", "attempt_id,ordinal,last_node_id,member_count,member_tail_id,updated_xid"},
	{"pp_procurement_transitions", "id,root_id,step_no,created_xid,created_at,tenant_id,request_id,attempt_id,account_id,binding_id,cause_kind,cause_source_id,event_id,journal_id,event_type,event_key,evidence_ref,held_delta,spent_delta,after_revision,after_state,after_held,after_spent,after_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis"},
	{"procurement_budget_events", "id,event_key,type,account_id,attempt_id,held_delta,spent_delta,evidence_ref,pp_root_id,pp_root_step,pp_transition_id,pp_journal_id,pp_created_xid"},
	{"periodic_budget_allocations", "id,control_id,subject_type,subject_id,origin,origin_source_id,window_id"},
	{"periodic_budget_events", "id,control_id,allocation_id,source_kind,source_id"},
	{"pp_account_journal", "id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,attempt_id,binding_id,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,transition_id"},
	{"pp_root_steps", "root_id,step_no,kind,source_id,created_xid"},
	{"pp_attempt_nodes", "id,attempt_id,ordinal,previous_node_id,root_id,step_no,step_kind,source_id,created_xid"},
	{"pp_operation_roots", "id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result"},
	{"periodic_budget_windows", "id,control_id,start_at,end_at"},
	{"periodic_budget_observation_journal", "id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at"},
}

var abGrantManagedUpdates = []abGrantColumns{
	{"pp_attempt_heads", "ordinal,last_node_id,member_count,member_tail_id,updated_xid"},
	{"pp_account_heads", "ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count"},
	{"procurement_accounts", "held_amount"},
	{"periodic_budget_controls", "held_amount,spent_amount,event_seq,last_event_id,max_observed_window_start"},
	{"periodic_budget_windows", "held_amount,spent_amount,revision,last_event_id"},
	{"periodic_budget_allocations", "held_amount,spent_amount,source_held,source_spent,state,revision,last_event_id"},
	{"periodic_budget_observation_heads", "ordinal,last_observation_id"},
	// Each of these relations has exactly one lock-only UPDATE grant. Revoking
	// one of several UPDATE columns on a business head would not remove locks.
	{"tenants", "created_at"}, {"memberships", "tenant_id"},
	{"api_keys", "created_at"}, {"models", "created_at"},
	{"channels", "created_at"}, {"reservations", "created_at"},
	{"upstream_attempts", "delivery_state"},
	{"procurement_reservations", "pp_last_journal_id"},
	{"procurement_budget_events", "id"},
	{"pp_root_steps", "root_id"}, {"pp_attempt_origins", "attempt_id"},
	{"pp_attempt_nodes", "id"}, {"pp_account_origins", "account_id"},
	{"pp_account_journal", "id"}, {"pp_procurement_transitions", "id"},
}

var abGrantWNarrowReads = []abGrantColumns{
	{"tenants", "id"}, {"memberships", "tenant_id,user_id"},
	{"api_keys", "id,tenant_id,allowed_client_cidrs,scopes,allowed_regions,revoked_at,expires_at"},
	{"models", "id"}, {"channels", "id,enabled,quarantined"},
	{"channel_versions", "id,region"},
	{"channel_version_credentials", "channel_version_id,channel_id,credential_id"},
	{"route_policy_versions", "id,model_id,revision,cost_mode,unknown_cost_policy"},
	{"model_bindings", "route_policy_version_id,channel_id,channel_version_id,upstream_model,max_input_cost_rate,max_output_cost_rate"},
	{"route_binding_isolations", "channel_version_id,upstream_model"},
	{"cost_versions", "id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at,created_at"},
	{"credential_procurement_bindings", "id,credential_id,channel_id,account_id,operation_id,bound_at"},
	{"reservations", "id,tenant_id,request_id,amount,status,deadline,created_at"},
	{"periodic_budget_observation_origins", "control_id"},
}

var abGrantWFunctions = []string{
	"pp_at_route_json_v1(jsonb,text,text)", "pp_at_uuid_parse_v1(text)", "pp_json_string_v1(text)",
	"pp_at_expected_shape_v1(boolean,bytea,bigint,bigint,bytea)",
	"pp_at_quote_equal_v1(boolean,bytea,bigint,bigint,bytea,text,bigint,bigint,text)",
	"pp_at_current_quote_v1(text,text)", "canonical_key_client_cidr(text)",
	"pb_now()", "pb_uuid(text)", "pb_start(text,timestamptz)", "pb_end(text,timestamptz)",
	"pb_current_tuple(xid8,xid)", "pb_require_read_committed()",
	"pb_sync_unconfigured(text,text)", "pb_observe(text,timestamptz)", "pb_gate(text,timestamptz,bigint)",
	"pb_prefix_append(text,text,text,bigint,text,bigint,text,text,bigint,text,timestamptz)",
}
var abGrantAPPDeferredFunctions = []string{
	"pp_ab_validate_request_leaf_v1(text)", "pp_ab_validate_command_leaf_v1(text)",
	"pp_ab_validate_attempt_leaf_v1(text)", "pp_ab_validate_account_leaf_v1(text)",
	"pp_validate_root(text)", "pp_validate_attempt(text)", "pp_validate_account(text)",
	"pp_validate_original(text,text,text)",
	"pp_ac_uuid_v1(text,boolean)", "pp_ac_text_v1(text)",
	"pp_ac_command_digest_v1(text,text,text,bigint,text,text,text,bigint)",
	"pp_ac_time_v1(timestamptz)",
	"pp_ac_snapshot_v1(text,text,text,text,bigint,numeric,numeric,bigint,timestamptz)",
	"pp_json_string_v1(text)", "pb_start(text,timestamptz)", "pb_end(text,timestamptz)", "pb_check_supplier(text)",
}

type abGrantItem struct {
	role, table, column, privilege, function, call string
	unused                                         bool
}

// These grants remain part of the fixed installation contract, but effective
// loss does not block this managed-attempt root or its outer APP COMMIT. Command
// validation first runs under W here; this does not prove that APP skips these
// helpers (PG16 can reuse simple-expression state in the same transaction).
// Other APP command/read paths have their own acceptance slice. Never add
// artificial reads to make conservative grants needed.
var abGrantAPPManagedUnusedFunctions = map[string]bool{
	"pp_ac_text_v1(text)": true,
	"pp_ac_command_digest_v1(text,text,text,bigint,text,text,text,bigint)": true,
	"pp_ac_time_v1(timestamptz)": true,
	"pp_ac_snapshot_v1(text,text,text,text,bigint,numeric,numeric,bigint,timestamptz)": true,
	"pp_json_string_v1(text)": true,
}

func (g abGrantItem) name() string {
	if g.function != "" {
		return g.role + "/EXECUTE/" + g.function
	}
	return g.role + "/" + g.privilege + "/" + g.table + "/" + g.column
}
func abGrantMatrices() map[string][]abGrantItem {
	out := map[string][]abGrantItem{
		"W_unconfigured_execute": {{role: "W", function: "pb_scope_sync(text,text,boolean)", privilege: "EXECUTE", call: "attempt"}},
	}
	add := func(group, role, priv string, columns []abGrantColumns) {
		for _, r := range columns {
			for _, c := range strings.Split(r.columns, ",") {
				out[group] = append(out[group], abGrantItem{role: role, table: r.table, column: c, privilege: priv, call: "attempt"})
			}
		}
	}
	add("managed_writes", "W", "INSERT", abGrantManagedInserts)
	add("managed_writes", "W", "UPDATE", abGrantManagedUpdates)
	add("W_core_reads", "W", "SELECT", abGrantCoreReads)
	add("W_PB_parent_reads", "W", "SELECT", abGrantPBReads)
	add("W_PB_parent_reads", "W", "SELECT", abGrantWNarrowReads)
	for _, table := range []string{"upstream_attempts", "procurement_reservations", "procurement_budget_events", "periodic_budget_observation_journal"} {
		add("W_PB_parent_reads", "W", "SELECT", []abGrantColumns{{table, "xmin"}})
	}
	add("APP_deferred_core_reads", "APP", "SELECT", abGrantCoreReads)
	add("APP_deferred_PB_reads", "APP", "SELECT", abGrantPBReads)
	add("APP_deferred_PB_reads", "APP", "SELECT", abGrantAPPExtraReads)
	for _, fn := range abGrantWFunctions {
		out["W_execute"] = append(out["W_execute"], abGrantItem{role: "W", function: fn, privilege: "EXECUTE", call: "attempt"})
	}
	for _, fn := range abGrantAPPDeferredFunctions {
		out["APP_deferred_execute"] = append(out["APP_deferred_execute"], abGrantItem{role: "APP", function: fn, privilege: "EXECUTE", call: "attempt", unused: abGrantAPPManagedUnusedFunctions[fn]})
	}
	for _, r := range []struct{ fn, call string }{
		{"pp_create_account(text,text,text,text,text,bigint)", "create"},
		{"pp_configure_account(text,text,text,bigint,text,text,text,bigint)", "configure"},
		{"pp_record_attempt(text,text,integer,text,text,text,text,jsonb,boolean,bytea,bigint,bigint,bytea)", "attempt"},
	} {
		out["APP_public_roots"] = append(out["APP_public_roots"], abGrantItem{role: "APP", function: r.fn, privilege: "EXECUTE", call: r.call})
	}
	return out
}
func TestA2GrantFrozenNecessaryMatrix(t *testing.T) {
	matrix := abGrantMatrices()
	want := map[string]int{"managed_writes": 256, "W_core_reads": 401, "W_PB_parent_reads": 144, "W_execute": 17, "W_unconfigured_execute": 1, "APP_deferred_core_reads": 401, "APP_deferred_PB_reads": 99, "APP_deferred_execute": 17, "APP_public_roots": 3}
	got := map[string]int{}
	seen := map[string]bool{}
	counts := map[string]int{}
	unused := 0
	for group, items := range matrix {
		got[group] = len(items)
		for _, g := range items {
			if seen[g.name()] {
				t.Fatal("duplicate frozen permission: " + g.name())
			}
			seen[g.name()] = true
			counts[g.role+"/"+g.privilege]++
			if g.unused {
				unused++
				if group != "APP_deferred_execute" || !abGrantAPPManagedUnusedFunctions[g.function] || g.call != "attempt" {
					t.Fatal("unreviewed unused grant control")
				}
			}
			if g.privilege == "EXECUTE" && (strings.Contains(g.function, "_write_") || strings.Contains(g.function, "_deferred_") || strings.Contains(g.function, "_capture_") || strings.Contains(g.function, "attempt_birth_p")) {
				t.Fatal("automatic trigger EXECUTE misclassified")
			}
		}
	}
	if !reflect.DeepEqual(got, want) || len(seen) != 1339 || unused != 5 || len(abGrantAPPManagedUnusedFunctions) != 5 {
		t.Fatalf("frozen necessary matrix drift: groups=%v total=%d counts=%v", got, len(seen), counts)
	}
	if counts["W/INSERT"] != 206 || counts["W/UPDATE"] != 50 || counts["W/SELECT"] != 545 || counts["APP/SELECT"] != 500 || counts["APP/EXECUTE"] != 20 {
		t.Fatalf("fixed role/privilege surface drift: %v", counts)
	}
	// This slice deliberately leaves original Reserve/A1 INSERT/pointer faults,
	// command-only DML, future dispatch, zero-U/absent PB and other route/retry
	// branches open. Omitting those paths does not call their grants unnecessary.
	t.Log("1334 necessary-grant expectations and five effective-loss managed success controls; 21 W lock-only no-ops and one APP account lock no-op are separate guard controls")
}

func abGrantRole(f *fixture, g abGrantItem) string {
	if g.role == "W" {
		return f.b.Writer
	}
	if g.role == "APP" {
		return f.b.App
	}
	panic("unknown frozen role")
}
func abGrantFunction(f *fixture, fn string) string {
	at := strings.IndexByte(fn, '(')
	return pgx.Identifier{f.b.Schema, fn[:at]}.Sanitize() + fn[at:]
}
func abGrantClause(f *fixture, g abGrantItem) string {
	if g.function != "" {
		return "EXECUTE ON FUNCTION " + abGrantFunction(f, g.function)
	}
	return g.privilege + "(" + pgx.Identifier{g.column}.Sanitize() + ") ON TABLE " + pgx.Identifier{f.b.Schema, g.table}.Sanitize()
}

// Canonical restoration compares every grantee/grantor/option, including xmin,
// not just the faulted role. Expected fault items never come from this catalog.
func abGrantACL(ctx context.Context, f *fixture, g abGrantItem) (string, error) {
	var value string
	var err error
	if g.function != "" {
		err = f.owner.QueryRow(ctx, `SELECT jsonb_build_object('owner',p.proowner::text,'entries',coalesce((SELECT jsonb_agg(jsonb_build_array(x.grantor::text,x.grantee::text,x.privilege_type,x.is_grantable) ORDER BY x.grantor,x.grantee,x.privilege_type,x.is_grantable) FROM pg_catalog.aclexplode(coalesce(p.proacl,pg_catalog.acldefault('f',p.proowner))) x),'[]'::jsonb))::text FROM pg_catalog.pg_proc p WHERE p.oid=pg_catalog.to_regprocedure($1)`, abGrantFunction(f, g.function)).Scan(&value)
	} else {
		err = f.owner.QueryRow(ctx, `WITH object AS (
 SELECT c.oid,c.relowner,c.relacl FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2
 ), entries AS (
 SELECT 0::smallint attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable FROM object o CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(o.relacl,pg_catalog.acldefault('r',o.relowner))) x
 UNION ALL
 SELECT a.attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable FROM object o JOIN pg_catalog.pg_attribute a ON a.attrelid=o.oid AND a.attnum<>0 AND NOT a.attisdropped CROSS JOIN LATERAL pg_catalog.aclexplode(a.attacl) x
 ) SELECT jsonb_build_object('owner',(SELECT relowner::text FROM object),'entries',coalesce((SELECT jsonb_agg(jsonb_build_array(attnum,grantor::text,grantee::text,privilege_type,is_grantable) ORDER BY attnum,grantor,grantee,privilege_type,is_grantable) FROM entries),'[]'::jsonb))::text`, f.b.Schema, g.table).Scan(&value)
	}
	return value, err
}
func abGrantEffective(ctx context.Context, f *fixture, g abGrantItem, want bool) error {
	var actual bool
	var err error
	if g.function != "" {
		err = f.owner.QueryRow(ctx, "SELECT pg_catalog.has_function_privilege($1,$2,'EXECUTE')", abGrantRole(f, g), abGrantFunction(f, g.function)).Scan(&actual)
	} else {
		err = f.owner.QueryRow(ctx, "SELECT pg_catalog.has_column_privilege($1,$2,$3,$4)", abGrantRole(f, g), pgx.Identifier{f.b.Schema, g.table}.Sanitize(), g.column, g.privilege).Scan(&actual)
	}
	if err != nil {
		return err
	}
	if actual != want {
		return fmt.Errorf("%s effective permission want=%t actual=%t", g.name(), want, actual)
	}
	return nil
}
func abGrantRevoke(t *testing.T, f *fixture, g abGrantItem) func() error {
	t.Helper()
	var owner bool
	if g.function != "" {
		must(t, "observe non-owner function fault target", f.owner.QueryRow(f.ctx, `SELECT p.proowner=r.oid FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_roles r ON r.rolname=$2 WHERE p.oid=pg_catalog.to_regprocedure($1)`, abGrantFunction(f, g.function), abGrantRole(f, g)).Scan(&owner))
	} else {
		var generated string
		must(t, "observe exact ordinary/system fault column", f.owner.QueryRow(f.ctx, `SELECT c.relowner=r.oid,a.attgenerated::text FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid JOIN pg_catalog.pg_roles r ON r.rolname=$3 WHERE n.nspname=$1 AND c.relname=$2 AND a.attname=$4 AND a.attnum<>0 AND NOT a.attisdropped`, f.b.Schema, g.table, abGrantRole(f, g), g.column).Scan(&owner, &generated))
		if g.privilege != "SELECT" && generated != "" {
			t.Fatal("generated DML fault target")
		}
	}
	if owner {
		t.Fatal("owner authorization is not an isolated ordinary grant fault: " + g.name())
	}
	must(t, "effective permission before REVOKE", abGrantEffective(f.ctx, f, g, true))
	baseline, err := abGrantACL(f.ctx, f, g)
	must(t, "snapshot complete object ACL", err)
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := f.owner.Exec(ctx, "GRANT "+abGrantClause(f, g)+" TO "+pgx.Identifier{abGrantRole(f, g)}.Sanitize()); e != nil {
			return e
		}
		if e := abGrantEffective(ctx, f, g, true); e != nil {
			return e
		}
		after, e := abGrantACL(ctx, f, g)
		if e != nil {
			return e
		}
		if after != baseline {
			return fmt.Errorf("full canonical ACL not restored: %s", g.name())
		}
		restored = true
		return nil
	}
	t.Cleanup(func() {
		if e := restore(); e != nil {
			t.Errorf("independent grant cleanup: %v", e)
		}
	})
	_, err = f.owner.Exec(f.ctx, "REVOKE "+abGrantClause(f, g)+" FROM "+pgx.Identifier{abGrantRole(f, g)}.Sanitize())
	must(t, "revoke only frozen grant", err)
	must(t, "effective permission really false, without PUBLIC/inheritance/owner bypass", abGrantEffective(f.ctx, f, g, false))
	return restore
}

type abGrantState struct {
	account      receipt
	old, request string
	oldAttempt   attemptReceipt
}

const abGrantLimit int64 = 1000000

func abGrantReserve(t *testing.T, f *fixture) string {
	t.Helper()
	r, e := billing.New(f.app).Reserve(f.ctx, billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key, ClientIP: "127.0.0.1"}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "a2-grant-request", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute)})
	must(t, "actual APP original request birth", e)
	if r.Amount != 120 {
		t.Fatalf("retail oracle amount=%d", r.Amount)
	}
	return r.RequestID
}
func abGrantBaseline(t *testing.T, f *fixture, withControl bool) abGrantState {
	t.Helper()
	must(t, "A2 unchanged APP/W role isolation", abCatalogCheckRoles(f.ctx, f.owner, f.b))
	_, e := f.owner.Exec(f.ctx, "UPDATE api_keys SET allowed_client_cidrs=ARRAY['127.0.0.1/32']::text[] WHERE id=$1", f.key)
	must(t, "prepare authentic source policy before business calls", e)
	a := f.create(t, abGrantLimit)
	f.bind(t, a.TargetID)
	old := abGrantReserve(t, f)
	attempt, e := f.attempt(old)
	must(t, "commit genuine managed prefix before supplier control", e)
	if attempt.Outcome != "applied" || attempt.Root == nil || attempt.Source == nil {
		t.Fatal("baseline did not commit an authentic managed attempt root")
	}
	if withControl {
		commander, e := periodicbudget.NewCommander(f.app)
		must(t, "construct original Commander", e)
		_, e = commander.ConfigureProcurement(f.ctx, f.admin, a.TargetID, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000000"})
		must(t, "actual APP first control imports committed prefix", e)
		var exact bool
		must(t, "original import has no manufactured current window", f.owner.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM periodic_budget_allocations WHERE subject_type='supplier_attempt' AND subject_id=$1 AND origin='legacy_import' AND window_id IS NULL AND held_amount=240 AND spent_amount=0) AND NOT EXISTS(SELECT 1 FROM periodic_budget_windows)`, attempt.Attempt).Scan(&exact))
		if !exact {
			t.Fatal("first-window grant scenario was not established by original Commander")
		}
	}
	s := abGrantState{account: a, old: old, request: abGrantReserve(t, f), oldAttempt: attempt}
	abGrantOldPrefix(t, f, s)
	return s
}
func abGrantOldPrefix(t *testing.T, f *fixture, s abGrantState) {
	t.Helper()
	_, e := f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4),pp_validate_root($5)", s.old, s.oldAttempt.Attempt, s.account.TargetID, *s.oldAttempt.Root, s.account.RootID)
	must(t, "old committed command/request/managed/import prefix still validates", e)
}
func abGrantInvoke(f *fixture, tx pgx.Tx, s abGrantState, call string) ([]byte, error) {
	var raw []byte
	var e error
	switch call {
	case "create":
		e = tx.QueryRow(f.ctx, "SELECT to_jsonb(x) FROM pp_create_account($1,$2,'grant','fixture','active',$3) x", f.admin, uuid.NewString(), abGrantLimit).Scan(&raw)
	case "configure":
		e = tx.QueryRow(f.ctx, "SELECT to_jsonb(x) FROM pp_configure_account($1,$2,$3,1,'supplier','fixture','active',$4) x", f.admin, uuid.NewString(), s.account.TargetID, abGrantLimit).Scan(&raw)
	case "attempt":
		route := fmt.Sprintf(`[{"channel_id":%q,"version_id":%q,"priority":0,"weight":1,"reason":"selected"}]`, f.channel, f.version)
		e = tx.QueryRow(f.ctx, `SELECT to_jsonb(x) FROM pp_record_attempt($1,$2,1,$3,$4,$5,'upstream',$6::jsonb,true,$7,2000000,2000000,$8) x`, f.tenant, s.request, f.channel, f.version, f.credential, route, []byte(f.cost), []byte("contract")).Scan(&raw)
	default:
		panic("unknown frozen root call")
	}
	return raw, e
}
func abGrantRollback(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := tx.Rollback(ctx)
	if e != nil && e != pgx.ErrTxClosed {
		t.Errorf("actual APP rollback: %v", e)
	}
}
func abGrantApplied(t *testing.T, raw []byte) {
	t.Helper()
	var r struct {
		Outcome string  `json:"outcome"`
		Root    *string `json:"root_id"`
	}
	must(t, "decode authentic root result", json.Unmarshal(raw, &r))
	if r.Outcome != "applied" || r.Root == nil || *r.Root == "" {
		t.Fatalf("missing applied authentic root: %s", raw)
	}
}
func abGrantPositive(t *testing.T, f *fixture, s abGrantState, call string, commit bool) {
	t.Helper()
	tx, e := f.app.Begin(f.ctx)
	must(t, "begin actual APP restoration control", e)
	defer abGrantRollback(t, tx)
	raw, e := abGrantInvoke(f, tx, s, call)
	must(t, "restored complete public root executes", e)
	abGrantApplied(t, raw)
	if commit {
		must(t, "actual APP normal COMMIT after matrix", tx.Commit(f.ctx))
	} else {
		_, e = tx.Exec(f.ctx, "SET CONSTRAINTS ALL IMMEDIATE")
		must(t, "all original and A2 deferred checks after restoration", e)
		must(t, "rollback positive control to retain honest first-window scenario", tx.Rollback(f.ctx))
	}
}
func abGrantRunCase(t *testing.T, f *fixture, s abGrantState, g abGrantItem, deferred bool) abGrantState {
	t.Helper()
	before := f.snapshot(t)
	restore := abGrantRevoke(t, f, g)
	tx, e := f.app.Begin(f.ctx)
	must(t, "begin actual APP missing-grant operation", e)
	var raw []byte
	func() {
		defer abGrantRollback(t, tx)
		var callErr error
		raw, callErr = abGrantInvoke(f, tx, s, g.call)
		if deferred {
			must(t, "internal W root succeeds before outer APP deferred check", callErr)
			abGrantApplied(t, raw)
			if g.unused {
				must(t, "effective unused managed grant loss permits actual APP COMMIT", tx.Commit(f.ctx))
			} else {
				sqlstate(t, tx.Commit(f.ctx), "42501")
			}
		} else {
			sqlstate(t, callErr, "42501")
		}
	}()
	if !g.unused && f.snapshot(t) != before {
		t.Fatal("effective loss changed one of 34 full original/customer/supplier/PB/source tables")
	}
	if g.unused && f.snapshot(t) == before {
		t.Fatal("unused-grant control did not commit the authentic managed effects")
	}
	must(t, "permission remains absent after real operation", abGrantEffective(f.ctx, f, g, false))
	must(t, "restore exact original effective permission and all-grantee ACL", restore())
	abGrantOldPrefix(t, f, s)
	if g.unused {
		var r attemptReceipt
		must(t, "decode committed managed success control", json.Unmarshal(raw, &r))
		if r.Root == nil || r.Source == nil || *r.Source != r.Attempt || r.Request != s.request || r.Delivery != "prepared" || r.Cost != "unknown" {
			t.Fatalf("unused-grant control did not return a full managed receipt: %s", raw)
		}
		_, e = f.app.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_attempt($2),pp_validate_account($3),pp_validate_root($4)", s.request, r.Attempt, s.account.TargetID, *r.Root)
		must(t, "restored APP validates the genuinely committed unused-grant prefix", e)
		var held, spent string
		var revision, ordinal int64
		must(t, "independent committed managed bucket oracle", f.owner.QueryRow(f.ctx, "SELECT a.held_amount::text,a.spent_amount::text,a.revision,h.ordinal FROM procurement_accounts a JOIN pp_account_heads h ON h.account_id=a.id WHERE a.id=$1", s.account.TargetID).Scan(&held, &spent, &revision, &ordinal))
		if held != "480" || spent != "0" || revision != 1 || ordinal != 3 {
			t.Fatalf("unused-grant committed bucket=%s/%s revision=%d ordinal=%d", held, spent, revision, ordinal)
		}
		// This control intentionally committed its first window. A new real
		// request supplies the restoration root; no existing request is replayed.
		s.request = abGrantReserve(t, f)
		before = f.snapshot(t)
	}
	abGrantPositive(t, f, s, g.call, false)
	if f.snapshot(t) != before {
		t.Fatal("restoration rollback changed committed state")
	}
	return s
}
func abGrantRunMatrix(t *testing.T, group string, deferred bool) {
	f := newFixture(t)
	s := abGrantBaseline(t, f, group != "W_unconfigured_execute")
	items := abGrantMatrices()[group]
	if len(items) == 0 {
		t.Fatal("empty frozen matrix")
	}
	for _, g := range items {
		if !t.Run(g.name(), func(t *testing.T) {
			s = abGrantRunCase(t, f, s, g, deferred)
		}) {
			// An unexpected COMMIT can consume the request/window. Stop this
			// fixture rather than report replay cascades as independent faults.
			t.Fatal("stop shared grant fixture after failed isolated case: " + g.name())
		}
	}
	// Every subcase restores full canonical ACL before the next begins. A fresh
	// actual APP COMMIT proves restored, naturally deferred execution once more.
	abGrantPositive(t, f, s, "attempt", true)
	abGrantOldPrefix(t, f, s)
	abCatalogCheck(t, f.owner, f.b)
}
func TestPostgresA2GrantManagedWrites(t *testing.T) { abGrantRunMatrix(t, "managed_writes", false) }
func TestPostgresA2GrantWCoreReads(t *testing.T)    { abGrantRunMatrix(t, "W_core_reads", false) }
func TestPostgresA2GrantWPBAndParentReads(t *testing.T) {
	abGrantRunMatrix(t, "W_PB_parent_reads", false)
}
func TestPostgresA2GrantOrdinaryWExecute(t *testing.T) { abGrantRunMatrix(t, "W_execute", false) }

// The namespace helper is conditional on a missing control. This distinct
// authentic case proves the grant without adding a manufactured read or call.
func TestPostgresA2GrantUnconfiguredScopeExecute(t *testing.T) {
	abGrantRunMatrix(t, "W_unconfigured_execute", false)
}
func TestPostgresA2GrantAPPDeferredCoreReads(t *testing.T) {
	abGrantRunMatrix(t, "APP_deferred_core_reads", true)
}
func TestPostgresA2GrantAPPDeferredPBReads(t *testing.T) {
	abGrantRunMatrix(t, "APP_deferred_PB_reads", true)
}
func TestPostgresA2GrantAPPDeferredExecute(t *testing.T) {
	// Each function has an independent actual-LOGIN fixture and request. The
	// observed unused text-helper case commits normally; an unexpected COMMIT
	// in another case must never manufacture replay failures in later cases.
	for _, g := range abGrantMatrices()["APP_deferred_execute"] {
		t.Run(g.name(), func(t *testing.T) {
			f := newFixture(t)
			s := abGrantBaseline(t, f, true)
			s = abGrantRunCase(t, f, s, g, true)
			abGrantPositive(t, f, s, "attempt", true)
			abGrantOldPrefix(t, f, s)
			abCatalogCheck(t, f.owner, f.b)
		})
	}
}
func TestPostgresA2GrantPublicRoots(t *testing.T) { abGrantRunMatrix(t, "APP_public_roots", false) }

type abGrantNoop struct{ table, column, keyColumn, key, role string }

func abGrantNoops(t *testing.T, f *fixture, s abGrantState) []abGrantNoop {
	t.Helper()
	var node, transition, event, source, audit string
	must(t, "locate real committed source targets", f.owner.QueryRow(f.ctx, `SELECT h.last_node_id,o.procurement_transition_id,o.reserved_event_id,a.command_source_id,c.audit_id
 FROM pp_attempt_origins o JOIN pp_attempt_heads h ON h.attempt_id=o.attempt_id
 JOIN pp_account_origins a ON a.account_id=o.account_id
 JOIN pp_account_command_sources c ON c.id=a.command_source_id WHERE o.attempt_id=$1`, s.oldAttempt.Attempt).Scan(&node, &transition, &event, &source, &audit))
	out := []abGrantNoop{
		{"users", "created_at", "id", f.admin, "W"},
		{"tenants", "created_at", "id", f.tenant, "W"},
		{"memberships", "tenant_id", "user_id", f.user, "W"},
		{"api_keys", "created_at", "id", f.key, "W"},
		{"models", "created_at", "id", f.model, "W"},
		{"channels", "created_at", "id", f.channel, "W"},
		{"reservations", "created_at", "request_id", s.old, "W"},
		{"upstream_attempts", "delivery_state", "id", s.oldAttempt.Attempt, "W"},
		{"procurement_reservations", "pp_last_journal_id", "attempt_id", s.oldAttempt.Attempt, "W"},
		{"procurement_budget_events", "id", "id", event, "W"},
		{"procurement_commands", "operation_id", "operation_id", s.account.OperationID, "W"},
		{"audit_events", "id", "id", audit, "W"},
		{"pp_root_steps", "root_id", "root_id", *s.oldAttempt.Root, "W"},
		{"pp_request_origins", "request_id", "request_id", s.old, "W"},
		{"pp_request_heads", "request_id", "request_id", s.old, "W"},
		{"pp_attempt_origins", "attempt_id", "attempt_id", s.oldAttempt.Attempt, "W"},
		{"pp_attempt_nodes", "id", "id", node, "W"},
		{"pp_account_command_sources", "id", "id", source, "W"},
		{"pp_account_origins", "account_id", "account_id", s.account.TargetID, "W"},
		{"pp_account_journal", "id", "id", *s.account.AccountJournalID, "W"},
		{"pp_procurement_transitions", "id", "id", transition, "W"},
		{"procurement_accounts", "created_at", "id", s.account.TargetID, "APP"},
	}
	if len(out) != 22 {
		t.Fatal("fixed lock-only no-op set changed")
	}
	return out
}
func abGrantNoopState(t *testing.T, f *fixture, n abGrantNoop) (string, string) {
	t.Helper()
	var value, rows string
	table := pgx.Identifier{f.b.Schema, n.table}.Sanitize()
	column := pgx.Identifier{n.column}.Sanitize()
	key := pgx.Identifier{n.keyColumn}.Sanitize()
	must(t, "read exact original value and entire target rows", f.owner.QueryRow(f.ctx, "SELECT min(x."+column+"::text),jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text)::text FROM "+table+" x WHERE x."+key+"=$1", n.key).Scan(&value, &rows))
	return value, rows
}
func abGrantSchemaACL(t *testing.T, f *fixture) string {
	t.Helper()
	var value string
	must(t, "snapshot full schema ACL", f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('owner',n.nspowner::text,'entries',coalesce((SELECT jsonb_agg(jsonb_build_array(x.grantor::text,x.grantee::text,x.privilege_type,x.is_grantable) ORDER BY x.grantor,x.grantee,x.privilege_type,x.is_grantable) FROM pg_catalog.aclexplode(coalesce(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) x),'[]'::jsonb))::text FROM pg_catalog.pg_namespace n WHERE n.nspname=$1`, f.b.Schema).Scan(&value))
	return value
}
func TestPostgresA2GrantLockOnlyNoopGuards(t *testing.T) {
	f := newFixture(t)
	s := abGrantBaseline(t, f, true)
	// These 15 automatic trigger functions have no APP direct EXECUTE. A real
	// complete root plus every deferred check still succeeds. No trigger REVOKE
	// or owner-implicit EXECUTE is counted as a necessary ordinary grant fault.
	for _, fn := range []string{"pp_ab_request_write_v1", "pp_ab_capture_request_v1", "pp_ab_shared_write_v1", "pp_ab_source_write_v1", "pp_ab_attempt_write_v1", "pp_ab_attempt_result_v1", "pp_ab_account_write_v1", "pp_ab_account_result_v1", "pp_ab_procurement_write_v1", "pp_ab_attempt_birth_p_v1", "pp_ab_event_write_v1", "pp_ab_command_write_v1", "pp_ab_audit_write_v1", "pp_ab_parent_write_v1", "pp_ab_deferred_v1"} {
		must(t, "APP direct automatic-trigger EXECUTE remains false", abGrantEffective(f.ctx, f, abGrantItem{role: "APP", function: fn + "()"}, false))
	}
	abGrantPositive(t, f, s, "attempt", false)
	cases := abGrantNoops(t, f, s)
	// A fixed, temporary test-only definer reaches a NOLOGIN W from the actual
	// APP session without SET ROLE or membership. Every CASE performs only one
	// frozen column assignment. Values are copied from owner-observed originals:
	// SET created_at=created_at would additionally need SELECT(created_at), which
	// minimal parent lock grants intentionally do not provide.
	probe := pgx.Identifier{f.b.Schema, "ab_grant_lock_probe"}.Sanitize()
	var body strings.Builder
	body.WriteString("CREATE FUNCTION " + probe + "(p_case text,p_key text,p_value text) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path=" + pgx.Identifier{f.b.Schema}.Sanitize() + ",pg_catalog,pg_temp AS $ab_grant_probe$ BEGIN PERFORM " + pgx.Identifier{f.b.Schema, "pp_ab_require_root_v1"}.Sanitize() + "(); CASE p_case ")
	for i, n := range cases {
		if n.role != "W" {
			continue
		}
		cast := ""
		if n.column == "created_at" {
			cast = "::timestamptz"
		}
		body.WriteString(fmt.Sprintf("WHEN 'case_%d' THEN UPDATE %s SET %s=p_value%s WHERE %s=p_key; ", i, pgx.Identifier{f.b.Schema, n.table}.Sanitize(), pgx.Identifier{n.column}.Sanitize(), cast, pgx.Identifier{n.keyColumn}.Sanitize()))
	}
	body.WriteString("ELSE RAISE EXCEPTION 'unknown test-only lock probe' USING ERRCODE='PZ994'; END CASE; RAISE EXCEPTION 'lock-only guard failed to reject' USING ERRCODE='PZ995'; END $ab_grant_probe$;")
	_, e := f.owner.Exec(f.ctx, body.String())
	must(t, "create fixed test-only lock probe", e)
	dropped := false
	drop := func() error {
		if dropped {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, e := f.owner.Exec(ctx, "DROP FUNCTION "+probe+"(text,text,text)")
		if e == nil {
			dropped = true
		}
		return e
	}
	t.Cleanup(func() {
		if e := drop(); e != nil {
			t.Errorf("remove test-only probe: %v", e)
		}
	})
	schemaACL := abGrantSchemaACL(t, f)
	// ALTER OWNER requires the target's CREATE on this schema on non-superuser
	// installers. The capability exists only in this installation transaction;
	// it is revoked before COMMIT, before APP can execute the probe.
	install, e := f.owner.Begin(f.ctx)
	must(t, "begin isolated probe ownership", e)
	defer abGrantRollback(t, install)
	for _, q := range []string{
		"REVOKE ALL ON FUNCTION " + probe + "(text,text,text) FROM PUBLIC",
		"GRANT CREATE ON SCHEMA " + pgx.Identifier{f.b.Schema}.Sanitize() + " TO " + pgx.Identifier{f.b.Writer}.Sanitize(),
		"ALTER FUNCTION " + probe + "(text,text,text) OWNER TO " + pgx.Identifier{f.b.Writer}.Sanitize(),
		"REVOKE CREATE ON SCHEMA " + pgx.Identifier{f.b.Schema}.Sanitize() + " FROM " + pgx.Identifier{f.b.Writer}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION " + probe + "(text,text,text) TO " + pgx.Identifier{f.b.App}.Sanitize(),
	} {
		_, e = install.Exec(f.ctx, q)
		must(t, "install probe without lasting role/schema escalation", e)
	}
	must(t, "commit test-only probe ownership", install.Commit(f.ctx))
	if abGrantSchemaACL(t, f) != schemaACL {
		t.Fatal("test-only probe changed canonical schema ACL")
	}
	must(t, "W remains isolated NOLOGIN without schema CREATE/membership", abCatalogCheckRoles(f.ctx, f.owner, f.b))
	for i, n := range cases {
		t.Run(n.role+"/"+n.table+"/"+n.column, func(t *testing.T) {
			g := abGrantItem{role: n.role, table: n.table, column: n.column, privilege: "UPDATE"}
			must(t, "existing lock-only grant is truly available", abGrantEffective(f.ctx, f, g, true))
			value, rowBefore := abGrantNoopState(t, f, n)
			before := f.snapshot(t)
			if n.role == "W" {
				_, e = f.app.Exec(f.ctx, "SELECT "+probe+"($1,$2,$3)", fmt.Sprintf("case_%d", i), n.key, value)
				sqlstate(t, e, "PP901")
			} else {
				// APP has this lock-only UPDATE, but the invoker account guard
				// first calls a private ordinary helper. Its absent EXECUTE is
				// the real refusal boundary; do not grant it to force PP901.
				must(t, "APP private writer-helper EXECUTE remains false", abGrantEffective(f.ctx, f, abGrantItem{role: "APP", function: "pp_ab_require_writer_v1()"}, false))
				_, e = f.app.Exec(f.ctx, "UPDATE "+pgx.Identifier{f.b.Schema, n.table}.Sanitize()+" SET "+pgx.Identifier{n.column}.Sanitize()+"=$1::timestamptz WHERE "+pgx.Identifier{n.keyColumn}.Sanitize()+"=$2", value, n.key)
				sqlstate(t, e, "42501")
				var denied *pgconn.PgError
				if !errors.As(e, &denied) || denied.Message != "permission denied for function pp_ab_require_writer_v1" {
					t.Fatalf("APP account no-op must fail at private writer-helper EXECUTE: %v", e)
				}
			}
			_, rowAfter := abGrantNoopState(t, f, n)
			if rowAfter != rowBefore || f.snapshot(t) != before {
				t.Fatal("lock-only original-value assignment changed target or 34-table state")
			}
			abGrantOldPrefix(t, f, s)
		})
	}
	must(t, "drop fixed probe before final catalog proof", drop())
	abGrantPositive(t, f, s, "attempt", true)
	abGrantOldPrefix(t, f, s)
	abCatalogCheck(t, f.owner, f.b)
}
