package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/fundinglotverify"

// periodicBindings is the PB18 contract's complete, unfiltered projection.
// JSONB values remain text bytes, while every PB timestamp retains its native
// NULL/finite/infinity envelope for the pure verifier to interpret.
func periodicBindings() []binding {
	return []binding{
		bind[fundinglotverify.PeriodicBudgetControlRow]("periodic_budget_controls", "id", "id scope tenant_id key_id account_id period_kind enabled limit_amount revision tracking_started_at effective_from max_observed_window_start held_amount spent_amount event_seq last_event_id created_xid configuration_operation_id"),
		bind[fundinglotverify.PeriodicBudgetWindowRow]("periodic_budget_windows", "id", "id control_id start_at end_at held_amount spent_amount revision last_event_id"),
		bind[fundinglotverify.PeriodicBudgetAllocationRow]("periodic_budget_allocations", "id", "id control_id subject_type subject_id origin origin_source_id window_id admitted_at upper_amount baseline source_held source_spent held_amount spent_amount state revision last_event_id created_xid created_at"),
		bind[fundinglotverify.PeriodicBudgetEventRow]("periodic_budget_events", "id", "id control_id allocation_id source_kind source_id seq allocation_before_revision window_before_revision before_held before_spent after_held after_spent after_source_spent after_state held_delta spent_delta created_xid created_at"),
		bind[fundinglotverify.PeriodicBudgetOperationRow]("periodic_budget_operations", "operation_id", "operation_id kind actor scope target resource_id revision safe_input result_snapshot input_fingerprint created_xid created_at"),
		bind[fundinglotverify.PeriodicBudgetVersionRow]("periodic_budget_configuration_versions", "control_id revision#", "control_id revision operation_id period_kind enabled limit_amount actor created_xid created_at"),
		bind[fundinglotverify.ProcurementAttemptTenantRow]("tenants", "id", "id"),
		bind[fundinglotverify.PeriodicBudgetKeyRow]("api_keys", "id", "id tenant_id"),
		bind[fundinglotverify.ProcurementApprovalUserRow]("users", "id", "id"),
		bind[fundinglotverify.PeriodicBudgetRequestRow]("request_records", "id", "id tenant_id key_id pb_created_xid pb_tenant_control pb_key_control pb_admitted_at"),
		bind[fundinglotverify.CustomerKeyReservationRow]("reservations", "id", "id tenant_id request_id amount status"),
		bind[fundinglotverify.PeriodicBudgetSettlementRow]("settlements", "id", "id tenant_id request_id charged_amount status"),
		bind[fundinglotverify.PeriodicBudgetAttemptRow]("upstream_attempts", "id", "id tenant_id request_id credential_id channel_id delivery_state pb_created_xid pb_dispatch_checked_at"),
		bind[fundinglotverify.ProcurementBudgetAccountRow]("procurement_accounts", "id", "id held_amount spent_amount"),
		bind[fundinglotverify.PeriodicBudgetBindingRow]("credential_procurement_bindings", "id", "id account_id credential_id channel_id"),
		bind[fundinglotverify.PeriodicBudgetSupplierRow]("procurement_reservations", "attempt_id", "attempt_id account_id binding_id upper_amount held_amount spent_amount state anomaly pb_created_xid pb_control pb_admitted_at"),
		bind[fundinglotverify.PeriodicBudgetProcurementEventRow]("procurement_budget_events", "id", "id event_key account_id attempt_id held_delta spent_delta type pb_created_xid"),
		bind[fundinglotverify.PeriodicBudgetAuditRow]("audit_events", "id", "id actor tenant_scope action target sanitized_diff request_id created_at"),
	}
}

func projectPeriodic(s *session) (fundinglotverify.PeriodicBudgetGraphRows, bool, error) {
	var out fundinglotverify.PeriodicBudgetGraphRows
	qualified := true
	b := periodicBindings()
	if err := projectInto(s, b[0], &out.Controls, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[1], &out.Windows, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[2], &out.Allocations, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[3], &out.Events, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[4], &out.Operations, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[5], &out.Versions, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[6], &out.Tenants, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[7], &out.Keys, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[8], &out.Users, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[9], &out.Requests, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[10], &out.Reservations, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[11], &out.Settlements, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[12], &out.Attempts, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[13], &out.Accounts, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[14], &out.Bindings, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[15], &out.Procurement, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[16], &out.ProcurementEvents, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	if err := projectInto(s, b[17], &out.Audits, &qualified); err != nil {
		return fundinglotverify.PeriodicBudgetGraphRows{}, false, err
	}
	return out, qualified, nil
}
