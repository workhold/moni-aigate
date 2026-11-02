package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/fundinglotverify"

var (
	fgUsers           = bind[fundinglotverify.ProcurementApprovalUserRow]("users", "id", "id")
	fgTenants         = bind[fundinglotverify.ProcurementAttemptTenantRow]("tenants", "id", "id")
	fgAccounts        = bind[fundinglotverify.LedgerAccountRow]("ledger_accounts", "id", "id tenant_id kind balance")
	fgJournals        = bind[fundinglotverify.FundingGraphJournalRow]("journals", "id", "id tenant_id operation_id kind reference actor currency created_txid created_at")
	fgLines           = bind[fundinglotverify.LedgerLineRow]("journal_lines", "id", "id journal_id account_id signed_delta")
	fgCredits         = bind[fundinglotverify.FundingGraphCreditRow]("credit_orders", "id", "id tenant_id business_ref amount state requester proof_ref approver journal_id created_at")
	fgReceipts        = bind[fundinglotverify.FundingGraphReceiptRow]("funding_receipts", "id", "id tenant_id source_kind source_id amount currency journal_id created_at")
	fgBatches         = bind[fundinglotverify.FundingGraphBatchRow]("voucher_batches", "id", "id state face_amount count purpose_ref reason maker revision redeemed_count approver terminal_actor expires_at created_at activated_at terminal_at")
	fgVouchers        = bind[fundinglotverify.FundingGraphVoucherRow]("vouchers", "id", "id batch_id created_at")
	fgRevocations     = bind[fundinglotverify.FundingGraphRevocationRow]("voucher_revocations", "id", "id batch_id operation_id maker expected_revision expected_redeemed_count reason created_at")
	fgDecisions       = bind[fundinglotverify.FundingGraphRevocationDecisionRow]("voucher_revocation_decisions", "id", "id proposal_id batch_id operation_id approver status reason decided_at")
	fgRedemptions     = bind[fundinglotverify.FundingGraphRedemptionRow]("voucher_redemptions", "id", "id tenant_id voucher_id batch_id operation_id actor amount journal_id funding_receipt_id created_at")
	fgCommands        = bind[fundinglotverify.FundingGraphCommerceCommandRow]("commerce_commands", "operation_id", "operation_id actor scope kind request_digest fingerprint_key_version created_at")
	fgTerminals       = bind[fundinglotverify.FundingGraphCommerceReceiptRow]("commerce_operation_receipts", "operation_id", "operation_id kind scope actor status reason resource_id amount funding_receipt_id journal_id created_at")
	fgAudits          = bind[fundinglotverify.ProcurementAuditRow]("audit_events", "id", "id actor action target sanitized_diff request_id tenant_scope created_at")
	fgOutbox          = bind[fundinglotverify.ProcurementOutboxRow]("projection_outbox", "event_id", "event_id tenant_id operation_id kind sanitized_payload schema_version created_at")
	fgKeys            = bind[fundinglotverify.CustomerKeyRow]("api_keys", "id", "id tenant_id owner_id held spent lifetime_limit")
	fgRequests        = bind[fundinglotverify.CustomerKeyRequestRow]("request_records", "id", "id tenant_id key_id owner_id billing_status input_rate output_rate input_upper_bound output_limit")
	fgReservations    = bind[fundinglotverify.CustomerKeyReservationRow]("reservations", "id", "id tenant_id request_id amount status")
	fgSettlements     = bind[fundinglotverify.CustomerKeySettlementRow]("settlements", "id", "id tenant_id request_id charged_amount raw_retail_amount waived_retail_amount refunded_amount status reason journal_id")
	fgRefunds         = bind[fundinglotverify.CustomerKeyRefundRow]("refund_orders", "id", "id tenant_id settlement_id business_ref amount state requester approver journal_id")
	fgLedgerJournals  = bind[fundinglotverify.LedgerJournalRow]("journals", "id", "id tenant_id operation_id currency")
	fgLinkReceipts    = bind[fundinglotverify.FundingSourceReceiptRow]("funding_receipts", "id", "id tenant_id source_kind source_id amount currency journal_id")
	fgLinkCredits     = bind[fundinglotverify.FundingSourceCreditRow]("credit_orders", "id", "id tenant_id amount state requester approver journal_id")
	fgLinkRedemptions = bind[fundinglotverify.FundingSourceRedemptionRow]("voucher_redemptions", "id", "id tenant_id voucher_id funding_receipt_id amount journal_id")
)

func fundingBindings() []binding {
	return []binding{fgUsers, fgTenants, fgAccounts, fgJournals, fgLines, fgCredits, fgReceipts, fgBatches, fgVouchers, fgRevocations, fgDecisions, fgRedemptions, fgCommands, fgTerminals, fgAudits, fgOutbox, fgKeys, fgRequests, fgReservations, fgSettlements, fgRefunds, fgLedgerJournals, fgLinkReceipts, fgLinkCredits, fgLinkRedemptions}
}

func projectFunding(s *session) (fundinglotverify.FundingSourceGraphRows, bool, error) {
	var out fundinglotverify.FundingSourceGraphRows
	qualified := true
	if err := projectInto(s, fgUsers, &out.Users, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgTenants, &out.Tenants, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgAccounts, &out.Accounts, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgJournals, &out.Journals, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgLines, &out.Lines, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgAudits, &out.Audits, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgOutbox, &out.Outbox, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgReceipts, &out.Receipts, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgCredits, &out.Credits, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgRedemptions, &out.Redemptions, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgBatches, &out.Batches, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgVouchers, &out.Vouchers, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgRevocations, &out.Revocations, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgDecisions, &out.Decisions, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgCommands, &out.Commands, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	if err := projectInto(s, fgTerminals, &out.CommerceReceipts, &qualified); err != nil {
		return fundinglotverify.FundingSourceGraphRows{}, false, err
	}
	return out, qualified, nil
}

func projectCustomer(s *session) (fundinglotverify.CustomerKeyGraphRows, bool, error) {
	var out fundinglotverify.CustomerKeyGraphRows
	qualified := true
	if err := projectInto(s, fgTenants, &out.Tenants, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgUsers, &out.Users, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgKeys, &out.Keys, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgRequests, &out.Requests, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgReservations, &out.Reservations, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgSettlements, &out.Settlements, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgRefunds, &out.Refunds, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgAccounts, &out.Accounts, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgJournals, &out.Journals, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	if err := projectInto(s, fgLines, &out.Lines, &qualified); err != nil {
		return fundinglotverify.CustomerKeyGraphRows{}, false, err
	}
	return out, qualified, nil
}

func projectLedger(s *session) (fundinglotverify.LedgerRows, bool, error) {
	var out fundinglotverify.LedgerRows
	qualified := true
	if err := projectInto(s, fgAccounts, &out.Accounts, &qualified); err != nil {
		return fundinglotverify.LedgerRows{}, false, err
	}
	if err := projectInto(s, fgLedgerJournals, &out.Journals, &qualified); err != nil {
		return fundinglotverify.LedgerRows{}, false, err
	}
	if err := projectInto(s, fgLines, &out.Lines, &qualified); err != nil {
		return fundinglotverify.LedgerRows{}, false, err
	}
	return out, qualified, nil
}

func projectLinks(s *session) (fundinglotverify.FundingSourceRows, bool, error) {
	var out fundinglotverify.FundingSourceRows
	qualified := true
	if err := projectInto(s, fgLinkReceipts, &out.Receipts, &qualified); err != nil {
		return fundinglotverify.FundingSourceRows{}, false, err
	}
	if err := projectInto(s, fgLinkCredits, &out.Credits, &qualified); err != nil {
		return fundinglotverify.FundingSourceRows{}, false, err
	}
	if err := projectInto(s, fgLinkRedemptions, &out.Redemptions, &qualified); err != nil {
		return fundinglotverify.FundingSourceRows{}, false, err
	}
	return out, qualified, nil
}
