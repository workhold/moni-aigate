package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func cashRefundWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == "cash_refund_one_open_per_lot" {
		return fail("cash_refund_open_exists", "another unfinished cash refund already exists for this source")
	}
	return err
}

func cashRefundCreateMatches(in CashRefundCreateInput, o CashRefundOrder) bool {
	return o.TenantID == in.TenantID && o.Maker == in.ActorID && o.LotID == in.LotID && o.BusinessRef == in.BusinessRef && o.Reason == in.Reason && o.RequestID == in.RequestID
}

func cashRefundWithPreviewTx(ctx context.Context, tx pgx.Tx, o CashRefundOrder) (CashRefundOrder, error) {
	if o.State != "requested" {
		return o, nil
	}
	preview, err := (&Store{}).previewCashRefundTx(ctx, tx, o.TenantID, o.LotID)
	if err != nil {
		return o, err
	}
	o.PreviewCoinAmount, o.PreviewFiatAmount, o.PreviewFiatCurrency = preview.PreviewCoinAmount, preview.PreviewFiatAmount, preview.PreviewFiatCurrency
	return o, nil
}

func (s *Store) CreateCashRefund(ctx context.Context, in CashRefundCreateInput) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validCashRefundCreate(in) {
		return result, fail("invalid_cash_refund", "invalid cash-refund create input")
	}
	err := s.cashRefundTransaction(ctx, func(tx pgx.Tx) error {
		if err := walletRole(ctx, tx, in.ActorID, "finance_operator"); err != nil {
			return err
		}
		id := uuid.NewString()
		original, err := claimFinancial(ctx, tx, in.TenantID, "cash_refund_create", in.IdempotencyKey, in.RequestHMAC, id)
		if err != nil {
			return err
		}
		if original != "" {
			result, err = cashRefundOrderRow(ctx, tx, in.TenantID, original, false)
			if err == nil && !cashRefundCreateMatches(in, result) {
				return fail("idempotency_conflict", "cash-refund request identity changed")
			}
			if err != nil {
				return err
			}
			result, err = cashRefundWithPreviewTx(ctx, tx, result)
			return err
		}
		requestedBy := in.ActorID
		if in.RequestID != "" {
			var kind, state string
			if err = tx.QueryRow(ctx, `SELECT kind,requested_by,state FROM wallet_requests WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, in.TenantID, in.RequestID).Scan(&kind, &requestedBy, &state); errors.Is(err, pgx.ErrNoRows) {
				return fail("invalid_wallet_request", "cash-refund request is not available")
			} else if err != nil {
				return err
			}
			if kind != "cash_refund" {
				return fail("request_link_unavailable", "only a cash-refund request can be linked")
			}
			if state != "open" {
				return fail("request_revision_conflict", "wallet request has already changed")
			}
		}
		preview, err := s.previewCashRefundTx(ctx, tx, in.TenantID, in.LotID)
		if err != nil {
			return err
		}
		if preview.PreviewCoinAmount == nil || *preview.PreviewCoinAmount <= 0 {
			return fail("cash_refund_unavailable", "the source has no available coins to refund")
		}
		tag, err := tx.Exec(ctx, `INSERT INTO cash_refund_orders(id,tenant_id,lot_id,business_ref,reason,requested_by,maker) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(business_ref) DO NOTHING`, id, in.TenantID, in.LotID, in.BusinessRef, in.Reason, requestedBy, in.ActorID)
		if err != nil {
			return cashRefundWriteError(err)
		}
		created := tag.RowsAffected() > 0
		if !created {
			result, err = cashRefundBusinessRow(ctx, tx, in.BusinessRef)
			if err == nil && !cashRefundCreateMatches(in, result) {
				return fail("idempotency_conflict", "cash-refund business reference already used")
			}
			if err != nil {
				return err
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
				if err = audit(ctx, tx, in.TenantID, in.ActorID, "wallet.request.linked", in.RequestID, "", map[string]any{"kind": "cash_refund", "linked_order_id": id}); err != nil {
					return err
				}
			}
			result, err = cashRefundOrderRow(ctx, tx, in.TenantID, id, false)
			if err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE financial_idempotency SET resource_id=$4 WHERE tenant_id=$1 AND operation='cash_refund_create' AND key=$2 AND request_hmac=$3`, in.TenantID, in.IdempotencyKey, in.RequestHMAC, result.ID); err != nil {
			return err
		}
		if created {
			if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund.created", result.ID, "", map[string]any{"lot_id": result.LotID, "request_id": result.RequestID}); err != nil {
				return err
			}
		}
		result, err = cashRefundWithPreviewTx(ctx, tx, result)
		return err
	})
	if err != nil {
		return CashRefundOrder{}, err
	}
	return result, nil
}

func cashRefundReplay(ctx context.Context, tx pgx.Tx, in CashRefundActionInput, operation string) (CashRefundOrder, bool, error) {
	original, err := claimFinancial(ctx, tx, in.TenantID, operation, in.IdempotencyKey, in.RequestHMAC, in.OrderID)
	if err != nil {
		return CashRefundOrder{}, false, err
	}
	if original == "" {
		return CashRefundOrder{}, false, nil
	}
	if original != in.OrderID {
		return CashRefundOrder{}, false, fail("idempotency_conflict", "cash-refund operation is bound to another order")
	}
	result, err := cashRefundOrderRow(ctx, tx, in.TenantID, original, false)
	return result, true, err
}

func (s *Store) ApproveCashRefund(ctx context.Context, in CashRefundActionInput) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validCashRefundAction(in) {
		return result, fail("invalid_cash_refund", "invalid cash-refund approval")
	}
	err := s.cashRefundTransaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		if replay, ok, err := cashRefundReplay(ctx, tx, in, "cash_refund_approve"); err != nil {
			return err
		} else if ok {
			result = replay
			return nil
		}
		if result.State == "approved" || result.State == "transfer_executed" || result.State == "completed" {
			return nil
		}
		if result.State != "requested" {
			return fail("cash_refund_state_conflict", "only a requested cash refund can be approved")
		}
		single, err := walletSingleOperator(ctx, tx)
		if err != nil {
			return err
		}
		var authorized bool
		if err = tx.QueryRow(ctx, `SELECT wallet_cash_refund_approval_valid($1,$2,$3,$4)`, result.Maker, in.ActorID, result.RequestedBy, single).Scan(&authorized); err != nil {
			return err
		}
		if !authorized {
			return fail("self_approval", "maker cannot approve own cash refund")
		}
		var tenant string
		if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, in.TenantID).Scan(&tenant); err != nil {
			return err
		}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, "AVAILABLE", "REFUND_HELD")
		if err != nil {
			return err
		}
		source, err := cashRefundSourceTx(ctx, tx, in.TenantID, result.LotID, true)
		if err != nil {
			return err
		}
		k := source.balance.available
		if k <= 0 {
			return fail("cash_refund_unavailable", "the source has no available coins to refund")
		}
		var approvedAt time.Time
		if err = tx.QueryRow(ctx, `UPDATE cash_refund_orders SET state='approved',approver=$3,single_operator=$4,approved_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND state='requested' RETURNING approved_at`, in.TenantID, in.OrderID, in.ActorID, single).Scan(&approvedAt); errors.Is(err, pgx.ErrNoRows) {
			return fail("cash_refund_state_conflict", "cash-refund state changed before approval")
		} else if err != nil {
			return err
		}
		currentID, err := coinRateAtTime(ctx, tx, source.currency, approvedAt)
		if err != nil {
			return err
		}
		buy, err := cashRefundRateByID(ctx, tx, source.buyRateID, source.currency)
		if err != nil {
			return err
		}
		current, err := cashRefundRateByID(ctx, tx, currentID, source.currency)
		if err != nil {
			return err
		}
		fiat, err := calculateCashRefundFiat(k, int64(source.fiat), int64(source.issued), buy, current)
		if err != nil {
			return err
		}
		operation := uuid.NewString()
		journal, err := postLocked(ctx, tx, in.TenantID, operation, "cash_refund_hold", in.OrderID, in.ActorID, map[string]int64{"AVAILABLE": -k, "REFUND_HELD": k}, accounts)
		if err != nil {
			return err
		}
		if err = applyCashRefundLotTx(ctx, tx, in.TenantID, in.OrderID, operation, journal, "refund_hold", &source.balance, -k, k, 0); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE cash_refund_orders SET coin_amount=$3,fiat_currency=$4,fiat_amount=$5,buy_rate_version_id=$6,refund_rate_version_id=$7,hold_journal_id=$8 WHERE tenant_id=$1 AND id=$2 AND state='approved' AND approver=$9 AND approved_at=$10`, in.TenantID, in.OrderID, k, source.currency, fiat, source.buyRateID, currentID, journal, in.ActorID, approvedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fail("cash_refund_state_conflict", "cash-refund approval could not be frozen")
		}
		diff := map[string]any{"coin_amount": Money(k), "fiat_amount": fiat, "fiat_currency": source.currency}
		if single && result.Maker == in.ActorID {
			diff["single_operator_exception"] = true
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund.approved", in.OrderID, "", diff); err != nil {
			return err
		}
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, false)
		return err
	})
	if err != nil {
		return CashRefundOrder{}, err
	}
	return result, nil
}

func (s *Store) ExecuteCashRefund(ctx context.Context, in CashRefundActionInput) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validCashRefundAction(in) || !security.ValidReference(in.TransferOrderRef, 256) {
		return result, fail("invalid_cash_refund", "a controlled transfer-order reference is required")
	}
	err := s.cashRefundTransaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		if replay, ok, err := cashRefundReplay(ctx, tx, in, "cash_refund_execute"); err != nil {
			return err
		} else if ok {
			result = replay
			return nil
		}
		if result.State == "transfer_executed" || result.State == "completed" {
			return nil
		}
		if result.State != "approved" {
			return fail("cash_refund_state_conflict", "only an approved cash refund can be executed")
		}
		if err = cashRefundActorAllowed(ctx, tx, in.ActorID, "finance_operator", result.SingleOperator); err != nil {
			return err
		}
		if in.ActorID == result.Approver {
			if !result.SingleOperator {
				return fail("self_approval", "approver cannot execute the same cash refund")
			}
			var bothAdmin bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users a JOIN users e ON e.id=$2 AND e.platform_role='admin' AND e.status='active' WHERE a.id=$1 AND a.platform_role='admin' AND a.status='active')`, result.Approver, in.ActorID).Scan(&bothAdmin); err != nil {
				return err
			}
			if !bothAdmin {
				return fail("forbidden", "single-operator exception requires two admin identities")
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE cash_refund_orders SET state='transfer_executed',transfer_executor=$3,transfer_order_ref=$4 WHERE tenant_id=$1 AND id=$2 AND state='approved'`, in.TenantID, in.OrderID, in.ActorID, in.TransferOrderRef)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fail("cash_refund_state_conflict", "cash-refund state changed before execution")
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund.transfer_executed", in.OrderID, "", map[string]any{"transfer_order_ref": in.TransferOrderRef}); err != nil {
			return err
		}
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, false)
		return err
	})
	if err != nil {
		return CashRefundOrder{}, err
	}
	return result, nil
}

func (s *Store) CompleteCashRefund(ctx context.Context, in CashRefundActionInput) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validCashRefundAction(in) || !security.ValidReference(in.TransferProofRef, 256) || in.PaidFiatAmount == nil || *in.PaidFiatAmount < 0 {
		return result, fail("invalid_cash_refund", "a controlled transfer proof and paid fiat amount are required")
	}
	err := s.cashRefundTransaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		if replay, ok, err := cashRefundReplay(ctx, tx, in, "cash_refund_complete"); err != nil {
			return err
		} else if ok {
			result = replay
			return nil
		}
		if result.State == "completed" {
			return nil
		}
		if result.State != "transfer_executed" {
			return fail("cash_refund_state_conflict", "only an executed cash refund can be completed")
		}
		if err = cashRefundAnyActorAllowed(ctx, tx, in.ActorID, result.SingleOperator); err != nil {
			return err
		}
		if result.FiatAmount == nil || *in.PaidFiatAmount != *result.FiatAmount {
			return fail("cash_refund_amount_mismatch", "paid fiat amount differs from the frozen refund amount")
		}
		if result.CoinAmount == nil || *result.CoinAmount <= 0 {
			return fail("cash_refund_state_conflict", "cash-refund frozen coin amount is missing")
		}
		k := int64(*result.CoinAmount)
		var tenant string
		if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, in.TenantID).Scan(&tenant); err != nil {
			return err
		}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, "REFUND_HELD", "FUNDING_CLEARING")
		if err != nil {
			return err
		}
		source, err := cashRefundSourceTx(ctx, tx, in.TenantID, result.LotID, true)
		if err != nil {
			return err
		}
		if source.balance.refundHeld != k {
			return fail("cash_refund_state_conflict", "cash-refund held balance does not match the order")
		}
		operation := uuid.NewString()
		journal, err := postLocked(ctx, tx, in.TenantID, operation, "cash_refund_retire", in.OrderID, in.ActorID, map[string]int64{"REFUND_HELD": -k, "FUNDING_CLEARING": k}, accounts)
		if err != nil {
			return err
		}
		if err = applyCashRefundLotTx(ctx, tx, in.TenantID, in.OrderID, operation, journal, "refund_retire", &source.balance, 0, -k, k); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE cash_refund_orders SET state='completed',transfer_proof_ref=$3,paid_fiat_amount=$4,retire_journal_id=$5 WHERE tenant_id=$1 AND id=$2 AND state='transfer_executed'`, in.TenantID, in.OrderID, in.TransferProofRef, *in.PaidFiatAmount, journal)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fail("cash_refund_state_conflict", "cash-refund state changed before completion")
		}
		if err = closeCashRefundRequestTx(ctx, tx, result, in.ActorID); err != nil {
			return err
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund.completed", in.OrderID, "", map[string]any{"paid_fiat_amount": *in.PaidFiatAmount}); err != nil {
			return err
		}
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, false)
		return err
	})
	if err != nil {
		return CashRefundOrder{}, err
	}
	return result, nil
}

func (s *Store) releaseCashRefund(ctx context.Context, in CashRefundActionInput, target string) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validCashRefundAction(in) || !validWalletOperatorReason(in.Reason) || target != "cancelled" && target != "failed" {
		return result, fail("invalid_cash_refund", "a release reason and valid target state are required")
	}
	err := s.cashRefundTransaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, true)
		if err != nil {
			return err
		}
		operationName := "cash_refund_cancel"
		if target == "failed" {
			operationName = "cash_refund_fail"
		}
		if replay, ok, err := cashRefundReplay(ctx, tx, in, operationName); err != nil {
			return err
		} else if ok {
			result = replay
			return nil
		}
		if result.State == target {
			return nil
		}
		if target == "cancelled" && result.State == "requested" {
			if in.ActorID != result.Maker {
				return fail("forbidden", "only the maker can cancel a requested cash refund")
			}
			single, err := walletSingleOperator(ctx, tx)
			if err != nil {
				return err
			}
			if err = cashRefundActorAllowed(ctx, tx, in.ActorID, "finance_operator", single); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE cash_refund_orders SET state='cancelled',reason=$3 WHERE tenant_id=$1 AND id=$2 AND state='requested'`, in.TenantID, in.OrderID, in.Reason)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fail("cash_refund_state_conflict", "cash-refund state changed before cancellation")
			}
			if err = closeCashRefundRequestTx(ctx, tx, result, in.ActorID); err != nil {
				return err
			}
			if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund.cancelled", in.OrderID, "", map[string]any{"reason": in.Reason, "held": false}); err != nil {
				return err
			}
			result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, false)
			return err
		}
		if target == "cancelled" {
			if result.State != "approved" || in.ActorID != result.Maker && in.ActorID != result.Approver {
				return fail("cash_refund_state_conflict", "only an approved, unexecuted cash refund can be cancelled by its maker or approver")
			}
			required := "finance_operator"
			if in.ActorID == result.Approver {
				required = "finance_approver"
			}
			if err = cashRefundActorAllowed(ctx, tx, in.ActorID, required, result.SingleOperator); err != nil {
				return err
			}
		} else {
			if result.State != "approved" && result.State != "transfer_executed" {
				return fail("cash_refund_state_conflict", "only an approved or executed cash refund can fail")
			}
			if err = cashRefundAnyActorAllowed(ctx, tx, in.ActorID, result.SingleOperator); err != nil {
				return err
			}
		}
		if result.CoinAmount == nil || *result.CoinAmount <= 0 {
			return fail("cash_refund_state_conflict", "cash-refund held amount is missing")
		}
		k := int64(*result.CoinAmount)
		var tenant string
		if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, in.TenantID).Scan(&tenant); err != nil {
			return err
		}
		accounts, err := lockAccounts(ctx, tx, in.TenantID, "REFUND_HELD", "AVAILABLE")
		if err != nil {
			return err
		}
		source, err := cashRefundSourceTx(ctx, tx, in.TenantID, result.LotID, true)
		if err != nil {
			return err
		}
		if source.balance.refundHeld != k {
			return fail("cash_refund_state_conflict", "cash-refund held balance does not match the order")
		}
		operation := uuid.NewString()
		journal, err := postLocked(ctx, tx, in.TenantID, operation, "cash_refund_release", in.OrderID, in.ActorID, map[string]int64{"REFUND_HELD": -k, "AVAILABLE": k}, accounts)
		if err != nil {
			return err
		}
		if err = applyCashRefundLotTx(ctx, tx, in.TenantID, in.OrderID, operation, journal, "refund_release", &source.balance, k, -k, 0); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE cash_refund_orders SET state=$3,reason=$4,release_journal_id=$5 WHERE tenant_id=$1 AND id=$2 AND state=$6`, in.TenantID, in.OrderID, target, in.Reason, journal, result.State)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return fail("cash_refund_state_conflict", "cash-refund state changed before release")
		}
		if err = closeCashRefundRequestTx(ctx, tx, result, in.ActorID); err != nil {
			return err
		}
		if err = audit(ctx, tx, in.TenantID, in.ActorID, "cash_refund."+target, in.OrderID, "", map[string]any{"reason": in.Reason, "held": true}); err != nil {
			return err
		}
		result, err = cashRefundOrderRow(ctx, tx, in.TenantID, in.OrderID, false)
		return err
	})
	if err != nil {
		return CashRefundOrder{}, err
	}
	return result, nil
}

func (s *Store) CancelCashRefund(ctx context.Context, in CashRefundActionInput) (CashRefundOrder, error) {
	return s.releaseCashRefund(ctx, in, "cancelled")
}

func (s *Store) FailCashRefund(ctx context.Context, in CashRefundActionInput) (CashRefundOrder, error) {
	return s.releaseCashRefund(ctx, in, "failed")
}
