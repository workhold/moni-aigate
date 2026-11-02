package fundinglotscan

import "github.com/moni-ai/moni-aigate/internal/fundinglotverify"

// walletBindings is the WL25 contract's complete original-row projection.
// Embedded sequence fields follow their declaration paths, rather than DDL
// position. Shared business columns remain part of the same physical read.
func walletBindings() []binding {
	b := []binding{
		bind[fundinglotverify.WalletSequenceTenantRow]("tenants", "id", "id fl_created_xid"),
		bind[fundinglotverify.LedgerAccountRow]("ledger_accounts", "id", "id tenant_id kind balance"),
		bind[fundinglotverify.WalletLotsJournalRow]("journals", "id", "id tenant_id operation_id kind currency created_txid reference actor created_at"),
		bind[fundinglotverify.LedgerLineRow]("journal_lines", "id", "id journal_id account_id signed_delta"),
		bind[fundinglotverify.WalletLotsReceiptRow]("funding_receipts", "id", "id tenant_id source_kind source_id amount currency journal_id created_at"),
		bind[fundinglotverify.WalletLotsRequestRow]("request_records", "id", "id tenant_id owner_id billing_status"),
		bind[fundinglotverify.WalletLotsReservationRow]("reservations", "id", "id tenant_id request_id amount status"),
		bind[fundinglotverify.WalletLotsSettlementRow]("settlements", "id", "id tenant_id request_id charged_amount refunded_amount status reason journal_id"),
		bind[fundinglotverify.WalletLotsRefundRow]("refund_orders", "id", "id tenant_id settlement_id amount state requester approver journal_id"),
		bind[fundinglotverify.WalletLotsEpochRow]("funding_lot_epoch", "singleton", "singleton id schema_version database_oid schema_oid basis_digest manifest_digest descriptor_digest runtime_proof_digest tenant_count receipt_count settlement_count approved_refund_count opening_count legacy_source_count available_total held_total actor created_at"),
		bind[fundinglotverify.WalletLotsAttestationRow]("funding_lot_runtime_attestations", "singleton", "singleton epoch_id proof_version mode database_oid schema_oid schema_owner_oid app_role_oid projector_role_oid worker_role_oid predecessor_payment_digest @proof_canonical proof_sha256 manifest_digest descriptor_digest created_at"),
		bind[fundinglotverify.WalletLotsLegacyReceiptRow]("funding_lot_legacy_receipts", "receipt_id", "receipt_id epoch_id tenant_id source_kind source_id amount journal_id"),
		bind[fundinglotverify.WalletLotsLegacySandboxReceiptRow]("funding_lot_legacy_sandbox_receipts", "receipt_id", "receipt_id epoch_id tenant_id capture_id amount journal_id"),
		bind[fundinglotverify.WalletLotsLegacySettlementRow]("funding_lot_legacy_settlements", "settlement_id", "settlement_id epoch_id tenant_id reservation_id request_id charged_amount refunded_amount_at_cutover journal_id"),
		bind[fundinglotverify.WalletLotsLegacyApprovedRefundRow]("funding_lot_legacy_approved_refunds", "refund_order_id", "refund_order_id epoch_id tenant_id settlement_id amount journal_id"),
		bind[fundinglotverify.WalletLotsLegacyOpeningRow]("funding_lot_legacy_openings", "id", "id epoch_id tenant_id kind account_id reservation_id amount observed_at"),
		bind[fundinglotverify.WalletLotsLegacySourceRow]("funding_wallet_legacy_sources", "kind source_id", "epoch_id tenant_id kind source_id journal_id journal_operation_id amount request_id reservation_id settlement_id"),
		bind[fundinglotverify.WalletLotsOperationRow]("funding_wallet_operations", "id", "id tenant_id epoch_id seq previous_seq previous_id kind source_id journal_id journal_operation_id event_count available_delta held_delta consumed_delta issued_delta created_xid created_at"),
		bind[fundinglotverify.WalletSequenceHeadRow]("funding_wallet_heads", "tenant_id", "tenant_id epoch_id tail_seq tail_operation_id"),
		bind[fundinglotverify.WalletLotsLotRow]("funding_lots", "id", "id tenant_id origin_kind funding_receipt_id legacy_opening_id legacy_refund_order_id issued_amount currency refund_class fifo_at created_at"),
		bind[fundinglotverify.WalletLotsEventRow]("funding_lot_events", "id", "id tenant_id lot_id wallet_operation_id wallet_seq kind operation_id journal_id created_xid available_delta inference_held_delta consumed_delta issued_delta previous_event_id before_revision after_revision before_available before_held before_consumed after_available after_held after_consumed created_at"),
		bind[fundinglotverify.WalletLotsBalanceRow]("funding_lot_balances", "lot_id", "lot_id tenant_id available_amount inference_held_amount consumed_amount revision last_event_id"),
		bind[fundinglotverify.WalletLotsReservationLotRow]("funding_reservation_lots", "id", "id tenant_id reservation_id lot_id allocation_index amount created_xid created_at"),
		bind[fundinglotverify.WalletLotsSettlementLotRow]("funding_settlement_lots", "id", "id tenant_id settlement_id reservation_lot_id charged_amount released_amount created_xid created_at"),
		bind[fundinglotverify.WalletLotsRefundLotRow]("funding_consumption_refund_lots", "id", "id tenant_id refund_order_id lot_id settlement_lot_id allocation_index amount origin created_xid created_at"),
	}
	// This fixed source column has a larger bound in the frozen C0 DDL. No
	// caller or other bytea field can use it to relax the ordinary field cap.
	for i := range b[10].columns {
		if b[10].columns[i].name == "proof_canonical" {
			b[10].columns[i].limit = 262144
		}
	}
	return b
}

func projectWallet(s *session) (fundinglotverify.WalletLotsRows, bool, error) {
	var out fundinglotverify.WalletLotsRows
	qualified := true
	b := walletBindings()
	if err := projectInto(s, b[0], &out.Tenants, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[1], &out.Accounts, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[2], &out.Journals, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[3], &out.Lines, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[4], &out.Receipts, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[5], &out.Requests, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[6], &out.Reservations, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[7], &out.Settlements, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[8], &out.Refunds, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[9], &out.Epochs, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[10], &out.Attestations, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[11], &out.LegacyReceipts, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[12], &out.LegacySandboxReceipts, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[13], &out.LegacySettlements, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[14], &out.LegacyApprovedRefunds, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[15], &out.LegacyOpenings, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[16], &out.LegacySources, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[17], &out.Operations, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[18], &out.Heads, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[19], &out.Lots, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[20], &out.Events, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[21], &out.Balances, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[22], &out.ReservationLots, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[23], &out.SettlementLots, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	if err := projectInto(s, b[24], &out.RefundLots, &qualified); err != nil {
		return fundinglotverify.WalletLotsRows{}, false, err
	}
	return out, qualified, nil
}
