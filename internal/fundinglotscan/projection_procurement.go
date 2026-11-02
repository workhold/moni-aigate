package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/fundinglotverify"

const (
	procurementChannels = iota
	procurementCredentials
	procurementVersions
	procurementPoolMemberships
	procurementTenants
	procurementRequests
	procurementCostVersions
	procurementAccounts
	procurementBindingsIndex
	procurementAttempts
	procurementReservations
	procurementBudgetEvents
	procurementUsers
	procurementCorrections
	procurementProposals
	procurementCommands
	procurementObservations
	procurementAudits
	procurementOutbox
	procurementIdentities
	procurementMetering
	procurementConflicts
	procurementFinalizations
)

// Columns follow each DTO's Go declaration order. The root registry merges
// these fixed projections with other domains before reading each table once.
func procurementBindings() []binding {
	return []binding{
		bind[fundinglotverify.ProcurementAttemptChannelRow]("channels", "id", "id"),
		bind[fundinglotverify.ProcurementAttemptCredentialRow]("channel_credentials", "id", "id channel_id"),
		bind[fundinglotverify.ProcurementAttemptVersionRow]("channel_versions", "id", "id channel_id credential_id"),
		bind[fundinglotverify.ProcurementAttemptPoolMemberRow]("channel_version_credentials", "channel_version_id credential_id", "channel_version_id channel_id credential_id position weight max_inflight"),
		bind[fundinglotverify.ProcurementAttemptTenantRow]("tenants", "id", "id"),
		bind[fundinglotverify.ProcurementEvidenceRequestRow]("request_records", "id", "id tenant_id input_upper_bound output_limit policy_version latest_finalization_id request_deadline"),
		bind[fundinglotverify.ProcurementAttemptCostVersionRow]("cost_versions", "id", "id channel_id upstream_model input_rate output_rate basis effective_at created_at"),
		bind[fundinglotverify.ProcurementEvidenceAccountRow]("procurement_accounts", "id", "id label provider state limit_amount held_amount spent_amount revision created_at"),
		bind[fundinglotverify.ProcurementAttemptBindingRow]("credential_procurement_bindings", "id", "id credential_id channel_id account_id operation_id bound_at"),
		bind[fundinglotverify.ProcurementAttemptRow]("upstream_attempts", "id", "id tenant_id request_id sequence channel_id channel_version_id credential_id upstream_model delivery_state cost_version_id cost_input_rate cost_output_rate cost_basis cost_status cost_amount created_at"),
		bind[fundinglotverify.ProcurementEvidenceReservationRow]("procurement_reservations", "attempt_id", "attempt_id account_id binding_id input_bound output_bound upper_amount held_amount spent_amount state revision anomaly created_at updated_at"),
		bind[fundinglotverify.ProcurementEvidenceBudgetEventRow]("procurement_budget_events", "id", "id event_key account_id attempt_id held_delta spent_delta type evidence_ref created_at"),
		bind[fundinglotverify.ProcurementApprovalUserRow]("users", "id", "id"),
		bind[fundinglotverify.ProcurementCostCorrectionRow]("provider_cost_corrections", "id", "id tenant_id attempt_id operation_id amount basis evidence_ref created_at"),
		bind[fundinglotverify.ProcurementCostProposalRow]("procurement_cost_proposals", "id", "id tenant_id attempt_id amount basis evidence_ref reason requester expected_reservation_revision expected_evidence_count state approver approval_operation_id created_at approved_at"),
		bind[fundinglotverify.ProcurementCommandRow]("procurement_commands", "operation_id", "operation_id actor kind target_id input_digest status reason before_state after_state created_at"),
		bind[fundinglotverify.ProcurementObservationRow]("procurement_balance_observations", "id", "id account_id operation_id actor status amount currency source semantic observed_at provider_as_of evidence_ref created_at"),
		bind[fundinglotverify.ProcurementAuditRow]("audit_events", "id", "id actor action target sanitized_diff request_id tenant_scope created_at"),
		bind[fundinglotverify.ProcurementOutboxRow]("projection_outbox", "event_id", "event_id tenant_id operation_id kind sanitized_payload schema_version created_at"),
		bind[fundinglotverify.ProcurementEvidenceIdentityRow]("procurement_evidence_identities", "attempt_id evidence_digest", "attempt_id evidence_digest event_id created_at"),
		bind[fundinglotverify.ProcurementEvidenceMeterRow]("metering_events", "id", "id tenant_id request_id source source_event_id normalized_usage attempt_id schema_version happened_at received_at"),
		bind[fundinglotverify.ProcurementEvidenceConflictRow]("metering_event_conflicts", "id", "id event_id normalized_usage created_at"),
		bind[fundinglotverify.ProcurementEvidenceFinalizationRow]("finalization_records", "id", "id tenant_id request_id event_id input_digest protocol_outcome transport_end_reason delivery_stage finish_reason policy_version content_written created_at"),
	}
}

func projectProcurement(s *session) (fundinglotverify.ProcurementEvidenceGraphRows, bool, error) {
	var out fundinglotverify.ProcurementEvidenceGraphRows
	qualified := true
	b := procurementBindings()
	if err := projectInto(s, b[procurementChannels], &out.Channels, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementCredentials], &out.Credentials, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementVersions], &out.Versions, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementPoolMemberships], &out.PoolMemberships, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementTenants], &out.Tenants, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementRequests], &out.Requests, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementCostVersions], &out.CostVersions, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementAccounts], &out.Accounts, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementBindingsIndex], &out.Bindings, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementAttempts], &out.Attempts, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementReservations], &out.Reservations, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementBudgetEvents], &out.BudgetEvents, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementUsers], &out.Users, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementCorrections], &out.Corrections, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementProposals], &out.Proposals, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementCommands], &out.Commands, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementObservations], &out.Observations, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementAudits], &out.Audits, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementOutbox], &out.Outbox, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementIdentities], &out.Identities, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementMetering], &out.Metering, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementConflicts], &out.Conflicts, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	if err := projectInto(s, b[procurementFinalizations], &out.Finalizations, &qualified); err != nil {
		return fundinglotverify.ProcurementEvidenceGraphRows{}, false, err
	}
	return out, qualified, nil
}
