package procurementprefix

import "reflect"

func sourceTrackingValue(r reflect.Value, prefix string) bool {
	step, xid := sourceOptional(r, prefix+"root_step"), sourceOptional(r, prefix+"created_xid")
	return step != nil && signedInteger(*step, 64) && unsignedInteger(*step, 63, true) && xid != nil && unsignedInteger(*xid, 64, true)
}
func sourceOriginalG(r reflect.Value, prefix string) (present, valid bool) {
	fields := []string{prefix + "root_id", prefix + "root_step", prefix + "source_kind", prefix + "source_id", prefix + "source_role", prefix + "created_xid"}
	if sourceAll(r, false, fields...) {
		return false, true
	}
	return true, sourceAll(r, true, fields...) && sourceTrackingValue(r, prefix)
}
func sourceOriginalRole(table, kind, role string) bool {
	switch table {
	case "metering_events":
		return kind == "meter_choice" && role == "meter_origin"
	case "metering_event_conflicts":
		return kind == "meter_choice" && role == "conflict"
	case "finalization_records":
		return kind == "request_decision" && role == "finalization"
	case "procurement_evidence_identities":
		return kind == "evidence_choice" && role == "evidence_identity"
	case "procurement_cost_proposals":
		return kind == "proposal_birth" && role == "proposal_birth"
	case "provider_cost_corrections":
		return sourceOneOf(kind, "approval_consumed", "unmanaged_correction") && role == "correction"
	case "reconciliation_cases":
		return kind == "reconciliation" && role == "reconciliation"
	case "settlements":
		return kind == "reconciliation" && role == "settlement"
	case "journals":
		return kind == "reconciliation" && role == "customer_journal"
	case "journal_lines":
		return kind == "reconciliation" && role == "ledger_line"
	case "audit_events":
		return kind == "account_command" && role == "account_command" || kind == "proposal_birth" && role == "proposal" || kind == "approval_consumed" && role == "approval" || kind == "unmanaged_correction" && role == "unmanaged_correction" || kind == "reconciliation" && sourceOneOf(role, "reconciliation", "settlement", "profile_block")
	case "projection_outbox":
		return kind == "cost_outbox" && role == "cost" || kind == "reconciliation" && role == "settlement"
	}
	return false
}
func sourceTrackedAction(r reflect.Value, table string) bool {
	if table == "projection_outbox" {
		kind := sourceOptional(r, "pp_source_kind")
		if kind == nil {
			return false
		}
		if *kind == "cost_outbox" {
			return sourceOneOf(sourceText(r, "kind"), "cost_recorded", "cost_corrected")
		}
		return sourceOneOf(sourceText(r, "kind"), "settled", "released")
	}
	if table != "audit_events" {
		return true
	}
	kind, role := sourceOptional(r, "pp_source_kind"), sourceOptional(r, "pp_source_role")
	if kind == nil || role == nil {
		return false
	}
	action := sourceText(r, "action")
	switch *kind {
	case "account_command":
		return sourceOneOf(action, "procurement.command.applied", "procurement.command.rejected")
	case "proposal_birth":
		return action == "procurement.cost_proposed"
	case "approval_consumed":
		return action == "procurement.cost_approved"
	case "unmanaged_correction":
		return action == "cost.corrected"
	case "reconciliation":
		switch *role {
		case "reconciliation":
			return sourceOneOf(action, "reconciliation.proposed", "reconciliation.approved", "reconciliation.released")
		case "settlement":
			return sourceOneOf(action, "billing.settled", "billing.released")
		case "profile_block":
			return action == "billing.profile_blocked"
		}
	}
	return false
}

func (s *sourceClosureState) originalUnions() error {
	for table, collection := range s.tables {
		if sourceFieldType(table) == 0 {
			continue
		}
		for _, raw := range collection {
			if err := s.w.tick(); err != nil {
				return err
			}
			if !sourceOriginalUnion(table, raw.value) {
				s.mark("scalar", table, raw.key)
			}
		}
	}
	return nil
}
func sourceFieldType(table string) int {
	switch table {
	case "request_records", "upstream_attempts", "procurement_accounts", "procurement_reservations", "procurement_budget_events", "procurement_commands", "metering_events", "metering_event_conflicts", "finalization_records", "procurement_evidence_identities", "procurement_cost_proposals", "provider_cost_corrections", "reconciliation_cases", "settlements", "journals", "journal_lines", "audit_events", "projection_outbox":
		return 1
	}
	return 0
}
func sourceOriginalUnion(table string, r reflect.Value) bool {
	all := func(p bool, n ...string) bool { return sourceAll(r, p, n...) }
	if sourceField(r, "pp_source_role").IsValid() || table == "reconciliation_cases" {
		prefix := "pp_"
		if table == "reconciliation_cases" {
			prefix = "pp_last_"
		}
		present, valid := sourceOriginalG(r, prefix)
		if !valid {
			return false
		}
		if present {
			kind, role := sourceOptional(r, prefix+"source_kind"), sourceOptional(r, prefix+"source_role")
			if kind == nil || role == nil || !sourceOriginalRole(table, *kind, *role) || !sourceTrackedAction(r, table) {
				return false
			}
		}
		if table != "procurement_cost_proposals" {
			return true
		}
		approved, approvalValid := sourceOriginalG(r, "pp_approval_")
		if !approvalValid || approved && !present {
			return false
		}
		if !present {
			return !approved
		}
		state := sourceText(r, "state")
		if !sourceOneOf(state, "proposed", "approved") || approved != (state == "approved") {
			return false
		}
		return !approved || sourceSameOptional(sourceOptional(r, "pp_approval_source_kind"), sourcePtrValue("approval_consumed")) && sourceSameOptional(sourceOptional(r, "pp_approval_source_role"), sourcePtrValue("approval"))
	}
	switch table {
	case "request_records", "upstream_attempts":
		origins := []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid"}
		if all(false, origins...) {
			if table == "request_records" {
				return all(false, "pp_last_decision_id")
			}
			return all(false, "pp_quote_selected_at", "pp_last_cost_mutation_id", "pp_last_delivery_source_id", "pp_dispatch_source_id", "pp_dispatch_root_id", "pp_dispatch_root_step", "pp_dispatch_xid", "pp_dispatch_account_id", "pp_dispatch_journal_id")
		}
		step, xid := sourceOptional(r, "pp_origin_root_step"), sourceOptional(r, "pp_origin_xid")
		if !all(true, origins...) || step == nil || !unsignedInteger(*step, 63, true) || xid == nil || !unsignedInteger(*xid, 64, true) {
			return false
		}
		if table == "request_records" {
			return true
		}
		if !all(true, "pp_quote_selected_at") {
			return false
		}
		dispatch := []string{"pp_dispatch_source_id", "pp_dispatch_root_id", "pp_dispatch_root_step", "pp_dispatch_xid"}
		if all(false, dispatch...) {
			return sourceText(r, "delivery_state") != "sending" && all(false, "pp_dispatch_account_id", "pp_dispatch_journal_id")
		}
		ds, dx := sourceOptional(r, "pp_dispatch_root_step"), sourceOptional(r, "pp_dispatch_xid")
		return all(true, dispatch...) && ds != nil && unsignedInteger(*ds, 63, true) && dx != nil && unsignedInteger(*dx, 64, true) && all(true, "pb_dispatch_checked_at") && (all(true, "pp_dispatch_account_id", "pp_dispatch_journal_id") || all(false, "pp_dispatch_account_id", "pp_dispatch_journal_id"))
	case "procurement_accounts":
		group := []string{"pp_version", "pp_created_xid", "pp_origin_journal_id"}
		if all(false, group...) {
			return true
		}
		version, xid := sourceOptional(r, "pp_version"), sourceOptional(r, "pp_created_xid")
		return all(true, group...) && version != nil && *version == "1" && xid != nil && unsignedInteger(*xid, 64, true)
	case "procurement_reservations":
		group := []string{"pp_created_xid", "pp_origin_journal_id", "pp_last_journal_id"}
		if all(false, group...) {
			return true
		}
		xid := sourceOptional(r, "pp_created_xid")
		return all(true, group...) && xid != nil && unsignedInteger(*xid, 64, true)
	case "procurement_budget_events":
		group := []string{"pp_root_id", "pp_root_step", "pp_transition_id", "pp_journal_id", "pp_created_xid"}
		return all(false, group...) || all(true, group...) && sourceTrackingValue(r, "pp_")
	case "procurement_commands":
		group := []string{"pp_source_id", "pp_root_id", "pp_root_step", "pp_created_xid"}
		if all(false, group...) && all(false, "pp_account_journal_id") {
			return true
		}
		kind, status, reason := sourceText(r, "kind"), sourceText(r, "status"), sourceText(r, "reason")
		notFound := kind == "account_configure" && status == "rejected" && reason == "not_found"
		return all(true, group...) && sourceTrackingValue(r, "pp_") && sourceOneOf(kind, "account_create", "account_configure") && sourcePresent(r, "pp_account_journal_id") != notFound
	}
	return true
}
func sourcePtrValue(s string) *string { return &s }
