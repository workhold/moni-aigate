package billing

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func creditRow(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (CreditOrder, error) {
	var o CreditOrder
	sql := "SELECT id,tenant_id,business_ref,proof_ref,amount,state,requester,coalesce(approver,''),coalesce(journal_id,''),requested_by,basis_kind,coalesce(fiat_currency,''),fiat_amount,coalesce(fx_note,''),bonus_amount,coalesce(transfer_ref,''),single_operator,coalesce((SELECT wr.id FROM wallet_requests wr WHERE wr.linked_order_id=credit_orders.id AND wr.kind='topup'),'') FROM credit_orders WHERE tenant_id=$1 AND id=$2"
	if lock {
		sql += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, sql, tenant, id).Scan(&o.ID, &o.TenantID, &o.BusinessRef, &o.ProofRef, &o.Amount, &o.State, &o.Requester, &o.Approver, &o.JournalID, &o.RequestedBy, &o.BasisKind, &o.FiatCurrency, &o.FiatAmount, &o.FxNote, &o.BonusAmount, &o.TransferRef, &o.SingleOperator, &o.RequestID)
	return o, err
}

func (s *Store) CreateCredit(ctx context.Context, in CreditInput) (CreditOrder, error) {
	var o CreditOrder
	if err := normalizeCreditInput(&in); err != nil {
		return o, err
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := walletRole(ctx, tx, in.ActorID, "finance_operator"); err != nil {
			return err
		}
		id := uuid.NewString()
		original, err := claimFinancial(ctx, tx, in.TenantID, "credit", in.IdempotencyKey, in.RequestHMAC, id)
		if err != nil {
			return err
		}
		if original != "" {
			o, err = creditRow(ctx, tx, in.TenantID, original, false)
			if err == nil && !creditInputMatches(in, o) {
				return fail("idempotency_conflict", "credit request identity changed")
			}
			return err
		}
		if in.RequestID != "" {
			var kind, state string
			err = tx.QueryRow(ctx, `SELECT kind,requested_by,state FROM wallet_requests WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, in.TenantID, in.RequestID).Scan(&kind, &in.RequestedBy, &state)
			if errors.Is(err, pgx.ErrNoRows) {
				return fail("invalid_wallet_request", "wallet request is not available for this credit")
			}
			if err != nil {
				return err
			}
			if kind != "topup" {
				return fail("request_link_unavailable", "cash refund requests cannot be linked before 034.4")
			}
			if state != "open" {
				return fail("request_revision_conflict", "wallet request has already changed")
			}
		}
		tag, err := tx.Exec(ctx, `INSERT INTO credit_orders(id,tenant_id,business_ref,amount,state,requester,requested_by,basis_kind,fiat_currency,fiat_amount,fx_note,bonus_amount,transfer_ref,proof_ref) VALUES($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(business_ref) DO NOTHING`, id, in.TenantID, in.BusinessRef, int64(in.Amount), in.ActorID, in.RequestedBy, in.BasisKind, nullableText(in.FiatCurrency), in.FiatAmount, in.FxNote, int64(in.BonusAmount), in.TransferRef, in.ProofRef)
		if err != nil {
			return err
		}
		created := tag.RowsAffected() > 0
		if !created {
			if err = tx.QueryRow(ctx, `SELECT id FROM credit_orders WHERE business_ref=$1`, in.BusinessRef).Scan(&id); err != nil {
				return err
			}
			o, err = creditRow(ctx, tx, in.TenantID, id, false)
			if err != nil {
				return err
			}
			if !creditInputMatches(in, o) {
				return fail("idempotency_conflict", "credit reference already used")
			}
		} else {
			if in.RequestID != "" {
				tag, err = tx.Exec(ctx, `UPDATE wallet_requests SET state='linked',linked_order_id=$2 WHERE id=$1 AND state='open'`, in.RequestID, id)
				if err != nil {
					return err
				}
				if tag.RowsAffected() != 1 {
					return fail("request_revision_conflict", "wallet request has already changed")
				}
				if err = audit(ctx, tx, in.TenantID, in.ActorID, "wallet.request.linked", in.RequestID, "", map[string]any{"kind": "topup", "linked_order_id": id}); err != nil {
					return err
				}
			}
			o, err = creditRow(ctx, tx, in.TenantID, id, false)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE financial_idempotency SET resource_id=$4 WHERE tenant_id=$1 AND operation='credit' AND key=$2 AND request_hmac=$3", in.TenantID, in.IdempotencyKey, in.RequestHMAC, o.ID); err != nil {
			return err
		}
		if created {
			return audit(ctx, tx, in.TenantID, in.ActorID, "credit.created", o.ID, "", map[string]any{"amount": o.Amount, "bonus_amount": o.BonusAmount, "request_id": o.RequestID})
		}
		return nil
	})
	return o, err
}

func (s *Store) ApproveCredit(ctx context.Context, in ApprovalInput) (CreditOrder, error) {
	var o CreditOrder
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		o, err = creditRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		if err = walletRole(ctx, tx, in.ActorID, "finance_approver"); err != nil {
			return err
		}
		if err = walletRole(ctx, tx, o.Requester, "finance_operator"); err != nil {
			return err
		}
		single, err := walletSingleOperator(ctx, tx)
		if err != nil {
			return err
		}
		if (o.Requester == in.ActorID || o.RequestedBy == in.ActorID) && !(single && o.Requester == in.ActorID && o.RequestedBy == in.ActorID) {
			return fail("self_approval", "maker cannot approve own credit")
		}
		var tenant string
		if err = tx.QueryRow(ctx, "SELECT id FROM tenants WHERE id=$1 FOR SHARE", in.TenantID).Scan(&tenant); err != nil {
			return err
		}
		funded, err := s.ApplyFundingTx(ctx, tx, FundingSourceRef{Kind: FundingManualCredit, ID: o.ID}, FundingScope{TenantID: in.TenantID, ActorID: in.ActorID})
		if err != nil {
			return err
		}
		if !funded.Applied {
			return nil
		}
		diff := map[string]any{"amount": o.Amount, "bonus_amount": o.BonusAmount}
		if single && o.Requester == in.ActorID {
			diff["single_operator_exception"] = true
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "credit.approved", o.ID, "", diff); err != nil {
			return err
		}
		o.State = "approved"
		o.Approver = in.ActorID
		o.JournalID = funded.Receipt.JournalID
		o.SingleOperator = single
		return nil
	})
	return o, err
}

func refundRow(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (RefundOrder, error) {
	var o RefundOrder
	sql := "SELECT id,tenant_id,settlement_id,amount,state,requester,coalesce(approver,''),coalesce(journal_id,''),requested_by,single_operator FROM refund_orders WHERE tenant_id=$1 AND id=$2"
	if lock {
		sql += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, sql, tenant, id).Scan(&o.ID, &o.TenantID, &o.SettlementID, &o.Amount, &o.State, &o.Requester, &o.Approver, &o.JournalID, &o.RequestedBy, &o.SingleOperator)
	return o, err
}

func (s *Store) CreateRefund(ctx context.Context, in RefundInput) (RefundOrder, error) {
	var o RefundOrder
	if !boundedString(in.IdempotencyKey, 200) || !boundedString(in.RequestHMAC, 256) || in.Amount <= 0 || !boundedString(in.BusinessRef, 200) || !boundedString(in.Reason, 200) {
		return o, fail("invalid_refund", "positive amount and reference required")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		if err := walletRole(ctx, tx, in.ActorID, "finance_operator"); err != nil {
			return err
		}
		id := uuid.NewString()
		original, err := claimFinancial(ctx, tx, in.TenantID, "refund", in.IdempotencyKey, in.RequestHMAC, id)
		if err != nil {
			return err
		}
		if original != "" {
			o, err = refundRow(ctx, tx, in.TenantID, original, false)
			return err
		}
		var charged, refunded int64
		if err := tx.QueryRow(ctx, "SELECT charged_amount,refunded_amount FROM settlements WHERE tenant_id=$1 AND id=$2", in.TenantID, in.SettlementID).Scan(&charged, &refunded); err != nil {
			return err
		}
		if int64(in.Amount) > charged {
			return fail("refund_exceeds_charge", "refund exceeds remaining charge")
		}
		tag, err := tx.Exec(ctx, "INSERT INTO refund_orders(id,tenant_id,settlement_id,business_ref,amount,requester,requested_by,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(business_ref) DO NOTHING", id, in.TenantID, in.SettlementID, in.BusinessRef, int64(in.Amount), in.ActorID, in.ActorID, in.Reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var tenant, settlement, requester, reason string
			var amount int64
			if err = tx.QueryRow(ctx, "SELECT id,tenant_id,settlement_id,requester,reason,amount FROM refund_orders WHERE business_ref=$1", in.BusinessRef).Scan(&id, &tenant, &settlement, &requester, &reason, &amount); err != nil {
				return err
			}
			if tenant != in.TenantID || settlement != in.SettlementID || requester != in.ActorID || reason != in.Reason || amount != int64(in.Amount) {
				return fail("idempotency_conflict", "refund reference already used")
			}
		}
		if _, err = tx.Exec(ctx, "UPDATE financial_idempotency SET resource_id=$4 WHERE tenant_id=$1 AND operation='refund' AND key=$2 AND request_hmac=$3", in.TenantID, in.IdempotencyKey, in.RequestHMAC, id); err != nil {
			return err
		}
		o, err = refundRow(ctx, tx, in.TenantID, id, false)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return audit(ctx, tx, in.TenantID, in.ActorID, "refund.created", id, "", map[string]any{"amount": in.Amount})
		}
		return nil
	})
	return o, err
}

func (s *Store) ApproveRefund(ctx context.Context, in ApprovalInput) (RefundOrder, error) {
	var o RefundOrder
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		o, err = refundRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		if err = walletRole(ctx, tx, in.ActorID, "finance_approver"); err != nil {
			return err
		}
		if err = walletRole(ctx, tx, o.Requester, "finance_operator"); err != nil {
			return err
		}
		single, err := walletSingleOperator(ctx, tx)
		if err != nil {
			return err
		}
		if (o.Requester == in.ActorID || o.RequestedBy == in.ActorID) && !(single && o.Requester == in.ActorID && o.RequestedBy == in.ActorID) {
			return fail("self_approval", "maker cannot approve own refund")
		}
		if o.State == "approved" {
			return nil
		}
		var requestID string
		if err = tx.QueryRow(ctx, "SELECT request_id FROM settlements WHERE tenant_id=$1 AND id=$2", in.TenantID, o.SettlementID).Scan(&requestID); err != nil {
			return err
		}
		r, err := lockRequest(ctx, tx, in.TenantID, requestID)
		if err != nil {
			return err
		}
		var charged, refunded int64
		err = tx.QueryRow(ctx, "SELECT charged_amount,refunded_amount FROM settlements WHERE id=$1 FOR UPDATE", o.SettlementID).Scan(&charged, &refunded)
		if err != nil {
			return err
		}
		if int64(o.Amount) > charged-refunded {
			return fail("refund_exceeds_charge", "refund exceeds remaining charge")
		}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, "AVAILABLE", "REVENUE")
		if err != nil {
			return err
		}
		lotPlan, err := prepareFundingRefundTx(ctx, tx, r, o.SettlementID, charged, refunded, o.Amount)
		if err != nil {
			return err
		}
		journal, err := postLocked(ctx, tx, in.TenantID, o.ID, "refund", o.SettlementID, in.ActorID, map[string]int64{"AVAILABLE": int64(o.Amount), "REVENUE": -int64(o.Amount)}, accounts)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE settlements SET refunded_amount=refunded_amount+$2 WHERE id=$1", o.SettlementID, int64(o.Amount)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE refund_orders SET state='approved',approver=$2,journal_id=$3,single_operator=$4 WHERE id=$1", o.ID, in.ActorID, journal, single); err != nil {
			return err
		}
		if err = applyFundingRefundTx(ctx, tx, in.TenantID, o.ID, o.SettlementID, journal, lotPlan); err != nil {
			return err
		}
		diff := map[string]any{"amount": o.Amount}
		if single && o.Requester == in.ActorID {
			diff["single_operator_exception"] = true
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "refund.approved", o.ID, requestID, diff); err != nil {
			return err
		}
		if err = emit(ctx, tx, in.TenantID, o.ID, "refunded", map[string]any{"amount": o.Amount}); err != nil {
			return err
		}
		o.State = "approved"
		o.Approver = in.ActorID
		o.JournalID = journal
		o.SingleOperator = single
		return nil
	})
	return o, err
}

func claimFinancial(ctx context.Context, tx pgx.Tx, tenant, operation, key, digest, resource string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "INSERT INTO financial_idempotency(tenant_id,operation,key,request_hmac,resource_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING resource_id", tenant, operation, key, digest, resource).Scan(&id)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var oldDigest string
	if err = tx.QueryRow(ctx, "SELECT resource_id,request_hmac FROM financial_idempotency WHERE tenant_id=$1 AND operation=$2 AND key=$3", tenant, operation, key).Scan(&id, &oldDigest); err != nil {
		return "", err
	}
	if oldDigest != digest {
		return "", fail("idempotency_conflict", "financial idempotency key already used")
	}
	return id, nil
}
