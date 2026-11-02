package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/fundinglotverify"

// The order within each binding follows its Go DTO, including the shared
// audit/outbox DTOs whose field order differs from the physical table order.
func sandboxBindings() []binding {
	return []binding{
		bind[fundinglotverify.SandboxGraphEnvironmentRow]("payment_sandbox_environment", "singleton", "singleton id merchant_id payee_label database_oid schema_oid nonce_digest app_role worker_role projector_role protocol_version created_at"),
		bind[fundinglotverify.SandboxGraphAttestationRow]("payment_runtime_attestations", "singleton", "singleton environment_id database_oid schema_oid schema_owner_oid app_role_oid worker_role_oid projector_role_oid app_nonce_digest worker_nonce_digest proof_version @proof_canonical proof_sha256 created_at"),
		bind[fundinglotverify.SandboxGraphPolicyRow]("payment_policy", "singleton", "singleton enabled revision"),
		bind[fundinglotverify.SandboxGraphSKURow]("payment_skus", "id", "id revision last_version enabled current_version_id created_by created_at last_change_operation_id"),
		bind[fundinglotverify.SandboxGraphSKUVersionRow]("payment_sku_versions", "id", "id sku_id version display_name amount wallet_credit_amount currency environment_id merchant_id payee_label checkout_ttl_seconds publish_operation_id created_by created_at"),
		bind[fundinglotverify.SandboxGraphSKUChangeRow]("payment_sku_changes", "operation_id", "operation_id sku_id kind before_revision after_revision before_version_id after_version_id published_version_id before_enabled after_enabled created_at before_operation_id created_xid"),
		bind[fundinglotverify.SandboxGraphOrderRow]("payment_orders", "id", "id tenant_id creator sku_version_id display_name amount wallet_credit_amount currency environment_id merchant_id payee_label merchant_order_id created_at expires_at"),
		bind[fundinglotverify.SandboxGraphCancelIntentRow]("payment_cancel_intents", "order_id", "order_id operation_id actor created_at"),
		bind[fundinglotverify.SandboxGraphCommandOutboxRow]("payment_command_outbox", "id", "id order_id kind external_command_id original_outbox_id @canonical_parameters parameter_digest state generation attempt_count current_query_id next_attempt_at lease_until last_error_code created_at last_submission_unknown_at"),
		bind[fundinglotverify.SandboxGraphObservationRow]("payment_command_observations", "id", "id outbox_id generation @canonical_reply reply_digest observed_at"),
		bind[fundinglotverify.SandboxGraphQueryAttemptRow]("payment_query_attempts", "query_id", "query_id supervisor_id created_generation @canonical_request request_digest created_at mutation_generation"),
		bind[fundinglotverify.SandboxGraphInboxRow]("payment_inbox", "id", "id environment_id merchant_id event_id @canonical_body business_digest verified_key_id received_at"),
		bind[fundinglotverify.SandboxGraphEventConflictRow]("payment_event_conflicts", "id", "id inbox_id conflicting_digest received_at"),
		bind[fundinglotverify.SandboxGraphVerifiedQueryRow]("payment_verified_queries", "id", "id query_id @canonical_body snapshot_digest verified_key_id received_at"),
		bind[fundinglotverify.SandboxGraphQueryConflictRow]("payment_query_conflicts", "id", "id verified_query_id conflicting_digest received_at"),
		bind[fundinglotverify.SandboxGraphEvidenceRow]("payment_verified_evidence", "id", "id inbox_id verified_query_id environment_id merchant_id order_id kind provider_transaction_id amount currency happened_at provider_order_revision fact_digest verified_at"),
		bind[fundinglotverify.SandboxGraphProcessingRow]("payment_evidence_processing", "evidence_id", "evidence_id state generation attempt_count next_attempt_at lease_until last_error_code"),
		bind[fundinglotverify.SandboxGraphProviderRevisionRow]("payment_provider_revisions", "environment_id merchant_id order_id provider_order_revision#", "environment_id merchant_id order_id provider_order_revision kind fact_digest evidence_id"),
		bind[fundinglotverify.SandboxGraphIdentityRow]("payment_transaction_identities", "id", "id environment_id merchant_id provider_transaction_id order_id tenant_id amount currency captured_at provider_order_revision evidence_id created_at processing_generation"),
		bind[fundinglotverify.SandboxGraphSelectionRow]("payment_order_capture_selections", "order_id", "order_id identity_id first_evidence_id selected_at"),
		bind[fundinglotverify.SandboxGraphCaptureRow]("payment_captures", "id", "id order_id identity_id tenant_id environment_id merchant_id provider_transaction_id amount wallet_credit_amount currency captured_at provider_order_revision verified_evidence_id created_at processing_generation"),
		bind[fundinglotverify.SandboxGraphEffectRow]("payment_effects", "capture_id", "capture_id identity_id environment_id merchant_id provider_transaction_id effect_kind effect_id created_at"),
		bind[fundinglotverify.SandboxGraphReviewRow]("payment_review_facts", "id", "id order_id evidence_id identity_id selection_order_id reason created_at"),
		bind[fundinglotverify.ProcurementApprovalUserRow]("users", "id", "id"),
		bind[fundinglotverify.ProcurementAttemptTenantRow]("tenants", "id", "id"),
		bind[fundinglotverify.FundingGraphCommerceCommandRow]("commerce_commands", "operation_id", "operation_id actor scope kind request_digest fingerprint_key_version created_at"),
		bind[fundinglotverify.FundingGraphCommerceReceiptRow]("commerce_operation_receipts", "operation_id", "operation_id kind scope actor status reason resource_id amount funding_receipt_id journal_id created_at"),
		bind[fundinglotverify.FundingGraphReceiptRow]("funding_receipts", "id", "id tenant_id source_kind source_id amount currency journal_id created_at"),
		bind[fundinglotverify.LedgerAccountRow]("ledger_accounts", "id", "id tenant_id kind balance"),
		bind[fundinglotverify.FundingGraphJournalRow]("journals", "id", "id tenant_id operation_id kind reference actor currency created_txid created_at"),
		bind[fundinglotverify.LedgerLineRow]("journal_lines", "id", "id journal_id account_id signed_delta"),
		bind[fundinglotverify.ProcurementAuditRow]("audit_events", "id", "id actor action target sanitized_diff request_id tenant_scope created_at"),
		bind[fundinglotverify.ProcurementOutboxRow]("projection_outbox", "event_id", "event_id tenant_id operation_id kind sanitized_payload schema_version created_at"),
	}
}

func projectSandbox(s *session) (fundinglotverify.SandboxSourceGraphRows, bool, error) {
	var out fundinglotverify.SandboxSourceGraphRows
	if s == nil || s.ctx == nil {
		return out, false, ErrExecution
	}
	if s.ctx.Err() != nil {
		return out, false, contextFailure(s.ctx)
	}
	qualified := true
	b := sandboxBindings()
	if err := projectInto(s, b[0], &out.Environments, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[1], &out.Attestations, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[2], &out.Policies, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[3], &out.SKUs, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[4], &out.SKUVersions, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[5], &out.SKUChanges, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[6], &out.Orders, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[7], &out.CancelIntents, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[8], &out.CommandOutbox, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[9], &out.Observations, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[10], &out.QueryAttempts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[11], &out.Inbox, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[12], &out.EventConflicts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[13], &out.VerifiedQueries, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[14], &out.QueryConflicts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[15], &out.Evidence, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[16], &out.Processing, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[17], &out.ProviderRevisions, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[18], &out.Identities, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[19], &out.Selections, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[20], &out.Captures, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[21], &out.Effects, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[22], &out.Reviews, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[23], &out.Users, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[24], &out.Tenants, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[25], &out.Commands, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[26], &out.CommerceReceipts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[27], &out.Receipts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[28], &out.Accounts, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[29], &out.Journals, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[30], &out.Lines, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[31], &out.Audits, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if err := projectInto(s, b[32], &out.Outbox, &qualified); err != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, err
	}
	if s.ctx.Err() != nil {
		return fundinglotverify.SandboxSourceGraphRows{}, false, contextFailure(s.ctx)
	}
	return out, qualified, nil
}
