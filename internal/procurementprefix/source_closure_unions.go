package procurementprefix

import (
	"reflect"
	"strings"
	"unicode"
)

func sourceOneOf(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func sourcePresent(row reflect.Value, field string) bool {
	v := sourceField(row, field)
	if !v.IsValid() {
		return false
	}
	if v.Type() == timeType {
		return finite(v.Interface().(Time))
	}
	return v.Kind() != reflect.Pointer || !v.IsNil()
}
func sourceAll(row reflect.Value, present bool, fields ...string) bool {
	for _, field := range fields {
		if sourcePresent(row, field) != present {
			return false
		}
	}
	return true
}
func sourceCost(row reflect.Value, statusField, amountField string) bool {
	status := sourceText(row, statusField)
	return status == "unknown" && !sourcePresent(row, amountField) || sourceOneOf(status, "contract", "invoice") && sourcePresent(row, amountField)
}
func sourceTail(row reflect.Value, countField, tailField string) bool {
	count := sourceOptionalPointer(row, countField)
	return count != nil && signedInteger(*count, 64) && ((*count == "0" && !sourcePresent(row, tailField)) || *count != "0" && sourcePresent(row, tailField))
}
func sourceAccountText(v string) bool {
	if !validText(v) || len(v) < 1 || len(v) > 100 || strings.TrimSpace(v) != v {
		return false
	}
	for _, c := range v {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func sourceState(v string) bool {
	return sourceOneOf(v, "prepared", "sending", "unknown", "settled", "released", "disputed")
}
func sourceDelivery(v string) bool {
	return sourceOneOf(v, "prepared", "sending", "completed", "failed", "unknown")
}

func (s *sourceClosureState) semanticRows() error {
	for table, collection := range s.tables {
		if !strings.HasPrefix(table, "pp_") {
			continue
		}
		layout := sourcePhysicalLayouts[table]
		for _, raw := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			valid := sourceUnion(table, raw.value)
			for i := 0; i < raw.value.NumField(); i++ {
				if err := s.w.tick(); err != nil {
					return err
				}
				v := raw.value.Field(i)
				name := raw.value.Type().Field(i).Tag.Get("json")
				kind := layout.kinds[i]
				if v.Kind() == reflect.Pointer {
					if v.IsNil() {
						continue
					}
					v = v.Elem()
				}
				if v.Kind() != reflect.String {
					continue
				}
				text := v.String()
				switch kind {
				case 'N', 'n':
					if !sourceOneOf(name, "held_delta", "spent_delta") {
						if _, ok := numeric(text, false); !ok {
							valid = false
						}
					}
				case 'H', 'h', 'I', 'i', 'L', 'l':
					// All new integer fields are nonnegative; deltas only occur in the
					// ledger companion. Previous ordinals retain the generated zero.
					if name != "signed_delta" && strings.HasPrefix(text, "-") {
						valid = false
					}
					positive := name == "step_no" || name == "step_count" || name == "sequence" || name == "member_ordinal" || name == "account_ordinal_cut" || name == "version" || strings.Contains(name, "revision") && name != "route_revision" || name == "input_bound"
					if name == "ordinal" && table != "pp_request_heads" && table != "pp_attempt_heads" {
						positive = true
					}
					if positive && text == "0" {
						valid = false
					}
				case 'X', 'x':
					if !unsignedInteger(text, 64, true) {
						valid = false
					}
				case 'O', 'o':
					if !unsignedInteger(text, 32, true) {
						valid = false
					}
				}
				if name == "root_id" && !sourceNonzeroUUID(text) {
					valid = false
				}
				if name == "id" && table != "pp_operation_roots" && !sourceNonzeroUUID(text) {
					valid = false
				}
			}
			if !valid {
				s.mark("scalar", table, raw.key)
			}
		}
	}
	return nil
}

// Each switch arm states this row's closed nullable union; cross-row identity
// and the business calculation of the chosen branch are separate obligations.
func sourceUnion(table string, r reflect.Value) bool {
	txt := func(n string) string { return sourceText(r, n) }
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	switch table {
	case "pp_operation_roots":
		return sourceValidRootEnvelope(r.Interface().(OperationRootRow))
	case "pp_root_steps":
		return sourceOneOf(txt("kind"), sourceCanonicalKinds...)
	case "pp_request_origins":
		return !sourcePresent(r, "initial_latest_id") && txt("initial_status") == "accepted" && txt("initial_billing_status") == "reserved"
	case "pp_request_heads":
		return sourceTail(r, "ordinal", "last_decision_id")
	case "pp_attempt_origins":
		quote := all(false, "cost_version_id", "cost_input_rate", "cost_output_rate") && txt("cost_basis") == "unknown" || all(true, "cost_version_id", "cost_input_rate", "cost_output_rate") && sourceOneOf(txt("cost_basis"), "contract", "invoice")
		initial := txt("initial_delivery_state") == "prepared" && txt("initial_cost_status") == "unknown" && all(false, "initial_cost_amount")
		managed := txt("management_mode") == "managed" && all(true, "account_id", "binding_id", "procurement_transition_id", "reserved_event_id", "account_journal_id", "admitted_at")
		unmanaged := txt("management_mode") == "unmanaged" && all(false, "account_id", "binding_id", "procurement_transition_id", "reserved_event_id", "account_journal_id", "admitted_at")
		return quote && initial && (managed || unmanaged)
	case "pp_attempt_heads":
		return sourceTail(r, "ordinal", "last_node_id") && (!sourcePresent(r, "member_count") && !sourcePresent(r, "member_tail_id") || sourceTail(r, "member_count", "member_tail_id"))
	case "pp_attempt_nodes":
		return sourceOneOf(txt("step_kind"), sourceCanonicalKinds...) && txt("step_kind") != "attempt_birth"
	case "pp_account_origins":
		return txt("version") == "1"
	case "pp_account_heads":
		return sourceAccountText(txt("label")) && sourceAccountText(txt("provider")) && sourceOneOf(txt("state"), "active", "paused")
	case "pp_account_journal":
		return sourceAccountJournalUnion(r)
	case "pp_account_command_sources":
		if !sourceAccountText(txt("input_label")) || !sourceAccountText(txt("input_provider")) || !sourceOneOf(txt("input_state"), "active", "paused") || txt("actor_role") != "admin" || txt("actor_status") != "active" {
			return false
		}
		if txt("kind") == "account_create" {
			return txt("input_target_id") == "" && all(false, "input_expected_revision") && all(true, "journal_id") && txt("command_status") == "applied" && txt("command_reason") == ""
		}
		if txt("kind") != "account_configure" || txt("input_target_id") != txt("target_id") || !all(true, "input_expected_revision") {
			return false
		}
		switch txt("command_reason") {
		case "":
			return txt("command_status") == "applied" && all(true, "journal_id")
		case "revision_conflict":
			return txt("command_status") == "rejected" && all(true, "journal_id")
		case "not_found":
			return txt("command_status") == "rejected" && all(false, "journal_id")
		}
		return false
	case "pp_attempt_delivery_sources":
		if !sourceDelivery(txt("before_delivery_state")) || !sourceDelivery(txt("after_delivery_state")) {
			return false
		}
		if txt("mode") == "dispatch_permit" {
			pair := all(true, "dispatch_account_id", "dispatch_journal_id") || all(false, "dispatch_account_id", "dispatch_journal_id")
			return pair && all(false, "cause_source_id") && all(true, "dispatch_xid", "pb_dispatch_checked_at") && txt("before_delivery_state") == "prepared" && txt("after_delivery_state") == "sending"
		}
		return sourceOneOf(txt("mode"), "finalization", "recovery") && all(true, "cause_source_id") && all(false, "dispatch_account_id", "dispatch_journal_id", "dispatch_xid", "pb_dispatch_checked_at")
	case "pp_incoming_sources":
		return sourceOneOf(txt("usage_status"), "confirmed_final", "provisional", "estimated", "missing", "conflicting") && sourceOneOf(txt("protocol_outcome"), "completed", "failed", "incomplete", "cancelled", "unknown") && sourceOneOf(txt("transport_end_reason"), "done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption") && sourceOneOf(txt("raw_delivery_stage"), "none", "committed", "content", "unknown") && len(txt("source")) >= 1 && len(txt("source")) <= 80 && len(txt("source_event_id")) >= 1 && len(txt("source_event_id")) <= 200 && len(txt("finish_reason")) <= 80
	case "pp_meter_choices":
		if !sourceOneOf(txt("disposition"), "created", "reused") || !sourceOneOf(txt("collision_kind"), "none", "source_payload_mismatch", "other_confirmed_usage") {
			return false
		}
		if (txt("collision_kind") == "other_confirmed_usage") != sourcePresent(r, "competing_meter_id") {
			return false
		}
		if !sourcePresent(r, "attempt_id") {
			return all(false, "attempt_cut_before_id", "attempt_cut_after_meter_id")
		}
		return sourcePresent(r, "attempt_cut_after_meter_id")
	case "pp_meter_origins", "pp_conflict_sources", "pp_evidence_members":
		return true
	case "pp_request_decisions":
		if !sourceOneOf(txt("normalized_delivery_stage"), "none", "committed", "content", "unknown") {
			return false
		}
		if txt("branch") == "normal" {
			return all(true, "finalization_id") && all(false, "conflict_source_id") && sourceSameOptional(sourceOptional(r, "after_latest_id"), sourceOptional(r, "finalization_id"))
		}
		return txt("branch") == "conflict" && all(false, "finalization_id") && all(true, "conflict_source_id") && sourceSameOptional(sourceOptional(r, "before_latest_id"), sourceOptional(r, "after_latest_id"))
	case "pp_normal_cost_decisions":
		if !sourceCost(r, "before_cost_status", "before_cost_amount") {
			return false
		}
		before := []string{"before_procurement_revision", "before_procurement_state", "before_procurement_held", "before_procurement_spent", "before_procurement_anomaly", "member_count_before", "account_id", "account_ordinal_cut", "account_tail_cut"}
		switch txt("management_mode") {
		case "managed":
			if !all(true, before...) || !sourceTail(r, "member_count_before", "member_tail_before") {
				return false
			}
		case "unmanaged":
			if !all(false, before...) || !all(false, "member_tail_before") {
				return false
			}
		default:
			return false
		}
		if sourceOneOf(txt("decision"), "write_no_consumption", "write_final_usage") {
			return all(true, "selected_amount", "selected_basis", "cost_mutation_id", "cost_recorded_outbox_id")
		}
		return sourceOneOf(txt("decision"), "skip_already_known", "skip_disputed", "skip_no_final_usage_or_rate") && all(false, "selected_amount", "selected_basis", "cost_mutation_id", "cost_recorded_outbox_id")
	case "pp_cost_mutations":
		return sourceOneOf(txt("cause_kind"), "normal_cost_decision", "procurement_dispute_reset", "incoming_conflict_reset", "approval_consumed", "unmanaged_correction") && sourceCost(r, "before_status", "before_amount") && sourceCost(r, "after_status", "after_amount")
	case "pp_cost_reset_decisions":
		return sourceOneOf(txt("cause_kind"), "procurement_dispute", "incoming_conflict") && (txt("policy") == "applied" && all(true, "cost_mutation_id") || txt("policy") == "suppressed_adjudicated" && all(false, "cost_mutation_id")) && (txt("cause_kind") != "procurement_dispute" || all(true, "procurement_state_before") && txt("policy") == "applied")
	case "pp_evidence_choices":
		return sourceEvidenceUnion(r)
	case "pp_procurement_transitions":
		before := []string{"before_revision", "before_state", "before_held", "before_spent", "before_anomaly"}
		cause, event := txt("cause_kind"), txt("event_type")
		allowed := cause == "attempt_birth" && event == "reserved" || cause == "evidence_choice" && sourceOneOf(event, "released_no_consumption", "consumption_unknown", "cost_confirmed", "evidence_disputed") || cause == "approval_consumed" && event == "cost_corrected" || cause == "recovery" && sourceOneOf(event, "expired_without_permit", "recovered_unknown")
		return allowed && all(event != "reserved", before...) && sourceState(txt("after_state")) && len(txt("event_key")) >= 1 && len(txt("event_key")) <= 400 && len(txt("evidence_ref")) <= 256
	case "pp_proposal_births":
		return sourceTail(r, "member_count", "member_tail") && txt("requester_role") == "finance_operator" && txt("requester_status") == "active" && sourceOneOf(txt("reservation_state"), "unknown", "settled", "released", "disputed")
	case "pp_approval_consumptions":
		return sourceTail(r, "consumed_member_count", "consumed_member_tail") && sourceCost(r, "before_cost_status", "before_cost_amount") && txt("requester") != txt("approver") && txt("requester_role") == "finance_operator" && txt("requester_status") == "active" && txt("approver_role") == "finance_approver" && txt("approver_status") == "active"
	case "pp_unmanaged_correction_sources":
		return sourceCost(r, "before_cost_status", "before_cost_amount") && sourceOneOf(txt("basis"), "contract", "invoice") && txt("after_cost_status") == txt("basis") && txt("after_cost_amount") == txt("amount")
	case "pp_recovery_sources":
		return sourceOneOf(txt("decision"), "expired_without_permit", "recovered_unknown") && sourceOneOf(txt("procurement_before_state"), "prepared", "sending") && sourceDelivery(txt("delivery_before_state"))
	case "pp_cost_outbox_links":
		return sourceOneOf(txt("cause_kind"), "normal_cost_decision", "approval_consumed", "unmanaged_correction")
	case "pp_reconciliation_sources":
		return sourceReconciliationUnion(r)
	case "pp_customer_finish_outcomes":
		return sourceFinishUnion(r)
	case "pp_customer_finish_ledger_effects":
		return txt("account_kind") == "REVENUE" && !sourcePresent(r, "account_tenant_id") || sourceOneOf(txt("account_kind"), "AVAILABLE", "HELD") && sourcePresent(r, "account_tenant_id") && *sourceOptional(r, "account_tenant_id") == txt("tenant_id")
	case "pp_customer_finish_pb_effects":
		return sourceOneOf(txt("scope"), "tenant", "key") && txt("original_source_kind") == "settlement"
	}
	return false
}

func sourceAccountJournalUnion(r reflect.Value) bool {
	txt := func(n string) string { return sourceText(r, n) }
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	kind, source := txt("kind"), txt("source_kind")
	config := sourceOneOf(kind, "create", "configure", "configure_rejected")
	if config && source != "command" || kind == "dispatch" && source != "dispatch" || !config && kind != "dispatch" && source != "budget_event" {
		return false
	}
	if !sourceOneOf(kind, "create", "configure", "configure_rejected", "reserve", "dispatch", "released_no_consumption", "consumption_unknown", "cost_confirmed", "evidence_disputed", "expired_without_permit", "recovered_unknown", "cost_corrected") {
		return false
	}
	for _, field := range []string{"label", "provider", "state", "limit_amount", "configuration_revision", "held_amount", "spent_amount", "blocking_count"} {
		if sourcePresent(r, "before_"+field) != (kind != "create") {
			return false
		}
	}
	for _, field := range []string{"label", "provider"} {
		if !sourceAccountText(txt("after_" + field)) {
			return false
		}
		if kind != "create" {
			v := sourceOptional(r, "before_"+field)
			if v == nil || !sourceAccountText(*v) {
				return false
			}
		}
	}
	if !sourceOneOf(txt("after_state"), "active", "paused") {
		return false
	}
	if !all(source == "command", "command_source_id") || !all(source == "budget_event", "transition_id") || !all(source == "dispatch", "delivery_source_id") {
		return false
	}
	pFields := []string{"attempt_id", "binding_id", "after_reservation_revision", "after_reservation_state", "after_reservation_held_amount", "after_reservation_spent_amount", "after_reservation_anomaly", "input_bound", "output_bound", "upper_amount", "cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis"}
	if !all(!config, pFields...) {
		return false
	}
	before := []string{"before_reservation_revision", "before_reservation_state", "before_reservation_held_amount", "before_reservation_spent_amount", "before_reservation_anomaly"}
	if !all(!config && kind != "reserve", before...) {
		return false
	}
	if !all(kind == "reserve" || kind == "dispatch", "gate_extra", "gate_checked_at", "pb_decision_at") {
		return false
	}
	if config && sourcePresent(r, "pause_event_id") {
		return false
	}
	return true
}

func sourceEvidenceUnion(r reflect.Value) bool {
	txt := func(n string) string { return sourceText(r, n) }
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	if !sourceTail(r, "member_count_before", "member_tail_before") || !sourceTail(r, "member_count_after", "member_tail_after") {
		return false
	}
	if txt("disposition") == "existing_member" {
		if !all(false, "effective_conflict", "conflict_cause") || txt("decision") != "skip_existing_identity" {
			return false
		}
	} else if txt("disposition") == "new_member" {
		if !all(true, "effective_conflict", "conflict_cause") {
			return false
		}
		cause := sourceOptional(r, "conflict_cause")
		if cause == nil || !sourceOneOf(*cause, "none", "incoming_meter_conflict", "released_positive_usage", "settled_positive_no_consumption") {
			return false
		}
		v := sourceField(r, "effective_conflict")
		if v.IsNil() || v.Elem().Bool() != (*cause != "none") {
			return false
		}
	} else {
		return false
	}
	skips := map[string]string{"skip_existing_identity": "existing_identity", "skip_terminal_normal": "terminal_normal", "skip_already_disputed": "already_disputed", "skip_duplicate_dispute_event": "duplicate_dispute_event", "skip_already_unknown": "already_unknown"}
	if reason, skip := skips[txt("decision")]; skip {
		v := sourceOptional(r, "skip_reason")
		if v == nil || *v != reason || !all(false, "procurement_transition_id", "procurement_event_id", "account_journal_id") {
			return false
		}
	} else if !sourceOneOf(txt("decision"), "dispute", "release_no_consumption", "mark_unknown", "confirm_cost") || !all(false, "skip_reason") || !all(true, "procurement_transition_id", "procurement_event_id", "account_journal_id") {
		return false
	}
	return sourcePresent(r, "existing_dispute_event_id") == (txt("decision") == "skip_duplicate_dispute_event")
}

func sourceReconciliationUnion(r reflect.Value) bool {
	txt := func(n string) string { return sourceText(r, n) }
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	usage := []string{"input_usage_status", "input_input_tokens", "input_output_tokens", "input_total_tokens", "input_cached_tokens", "input_reasoning_tokens", "input_evidence_ref"}
	action := txt("action")
	if !sourceOneOf(action, "propose", "approve", "release") || !all(action == "propose", usage...) || !all(action != "approve", "input_reason") {
		return false
	}
	if txt("actor_status") != "active" || action == "propose" && txt("actor_role") != "finance_operator" || action != "propose" && txt("actor_role") != "finance_approver" {
		return false
	}
	if !all(action == "approve", "approval_checked_at") {
		return false
	}
	if action == "propose" {
		return all(false, "derived_incoming_source_id", "finalization_id", "customer_finish_outcome_id")
	}
	if !all(true, "customer_finish_outcome_id") {
		return false
	}
	if action == "release" {
		return all(false, "derived_incoming_source_id", "finalization_id")
	}
	return sourcePresent(r, "derived_incoming_source_id") == sourcePresent(r, "finalization_id")
}

func sourceFinishUnion(r reflect.Value) bool {
	txt := func(n string) string { return sourceText(r, n) }
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	kind := txt("result_kind")
	if !sourceOneOf(kind, "new_settlement", "existing_settlement", "pending") {
		return false
	}
	if kind == "new_settlement" {
		if !all(true, "financial_checked_at", "settlement_id", "customer_journal_id", "billing_audit_id", "billing_outbox_id") {
			return false
		}
	} else {
		if !all(false, "financial_checked_at", "customer_journal_id", "customer_tenant_control_id", "customer_key_control_id", "billing_audit_id", "billing_outbox_id") {
			return false
		}
		if !all(kind == "existing_settlement", "settlement_id") {
			return false
		}
		if kind == "existing_settlement" && !all(false, "settlement_decision_checked_at") {
			return false
		}
		if kind == "pending" && !all(true, "settlement_decision_checked_at") {
			return false
		}
	}
	block := sourcePresent(r, "profile_block_model_id")
	if !all(block, "profile_block_profile_version", "profile_block_disposition", "profile_block_audit_id") {
		return false
	}
	if block {
		disposition := sourceOptional(r, "profile_block_disposition")
		if kind != "new_settlement" || disposition == nil || !sourceOneOf(*disposition, "created", "reused") {
			return false
		}
	}
	return true
}
