package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) applyCreditTx(ctx context.Context, tx pgx.Tx, source FundingSourceRef, scope FundingScope) (FundingResult, error) {
	var result FundingResult
	if scope.CommandID != "" {
		return result, fail("invalid_funding_source", "manual funding has no commerce command")
	}
	o, err := creditRow(ctx, tx, scope.TenantID, source.ID, false)
	if err != nil {
		return result, err
	}
	if o.State == "approved" {
		var pair bool
		if err = tx.QueryRow(ctx, `SELECT wallet_credit_pair_valid($1)`, o.ID).Scan(&pair); err != nil {
			return result, err
		}
		if !pair {
			return result, fail("funding_integrity", "credit sources are incomplete")
		}
		kinds := []FundingSourceKind{FundingManualCredit}
		if o.BasisKind == "paid" && o.BonusAmount > 0 {
			kinds = append(kinds, FundingManualBonus)
		} else if o.BasisKind == "grant" {
			kinds = []FundingSourceKind{FundingManualBonus}
		} else if o.BasisKind == "unknown" {
			kinds = []FundingSourceKind{FundingManualUnknown}
		}
		for _, kind := range kinds {
			receipt, err := fundingReceiptRow(ctx, tx, kind, o.ID)
			if err != nil {
				return result, err
			}
			var valid bool
			if err = tx.QueryRow(ctx, `SELECT funding_journal_valid($1)`, receipt.JournalID).Scan(&valid); err != nil {
				return result, err
			}
			if !valid {
				return result, fail("funding_integrity", "funding source integrity failed")
			}
			if err = verifyFundingReceiptLotTx(ctx, tx, receipt); err != nil {
				return result, err
			}
			if receipt.JournalID == o.JournalID {
				result.Receipt = receipt
			}
		}
		if result.Receipt.ID == "" {
			return result, fail("funding_integrity", "primary funding journal is missing")
		}
		return result, nil
	}
	if o.State != "pending" {
		return result, fail("invalid_funding_source", "funding source unavailable")
	}
	var authorized bool
	if err = tx.QueryRow(ctx, `SELECT wallet_approval_snapshot_valid($1,$2,$3)`, o.Requester, scope.ActorID, o.RequestedBy).Scan(&authorized); err != nil {
		return result, err
	}
	if !authorized {
		return result, fail("forbidden", "financial role required")
	}
	var existing bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM funding_receipts WHERE parent_ref=$1)`, o.ID).Scan(&existing); err != nil {
		return result, err
	}
	if existing {
		return result, fail("funding_integrity", "partial credit sources require review")
	}
	var primary FundingReceipt
	var paidAt time.Time
	switch o.BasisKind {
	case "paid":
		primary, err = writeCreditSourceTx(ctx, tx, o, scope, FundingManualCredit, time.Time{})
		if err == nil {
			paidAt = primary.CreatedAt
		}
		if err == nil && o.BonusAmount > 0 {
			_, err = writeCreditSourceTx(ctx, tx, o, scope, FundingManualBonus, paidAt)
		}
	case "grant":
		primary, err = writeCreditSourceTx(ctx, tx, o, scope, FundingManualBonus, time.Time{})
	case "unknown":
		primary, err = writeCreditSourceTx(ctx, tx, o, scope, FundingManualUnknown, time.Time{})
	default:
		return result, fail("invalid_funding_source", "invalid credit basis")
	}
	if err != nil {
		return FundingResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3,single_operator=wallet_single_operator() WHERE id=$1`, o.ID, scope.ActorID, primary.JournalID); err != nil {
		return FundingResult{}, err
	}
	var requestID string
	if err = tx.QueryRow(ctx, `UPDATE wallet_requests SET state='closed' WHERE linked_order_id=$1 AND kind='topup' AND state='linked' RETURNING id`, o.ID).Scan(&requestID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return FundingResult{}, err
	}
	if requestID != "" {
		if err = audit(ctx, tx, scope.TenantID, scope.ActorID, "wallet.request.closed", requestID, "", map[string]any{"kind": "topup", "linked_order_id": o.ID}); err != nil {
			return FundingResult{}, err
		}
	}
	result.Receipt = primary
	result.Applied = true
	return result, nil
}

func writeCreditSourceTx(ctx context.Context, tx pgx.Tx, o CreditOrder, scope FundingScope, kind FundingSourceKind, paidAt time.Time) (FundingReceipt, error) {
	var receipt FundingReceipt
	amount := o.Amount
	clearing := "FUNDING_CLEARING"
	operation := o.ID
	journalKind := "credit"
	if kind == FundingManualBonus {
		amount = o.BonusAmount
		clearing = "OPERATING_GRANT_CLEARING"
		operation = uuid.NewString()
		journalKind = "credit_bonus"
	} else if kind == FundingManualUnknown {
		clearing = "OPERATING_GRANT_CLEARING"
	}
	if amount <= 0 {
		return receipt, fail("funding_integrity", "funding source integrity failed")
	}
	accounts, err := lockAccounts(ctx, tx, scope.TenantID, "AVAILABLE", clearing)
	if err != nil {
		return receipt, err
	}
	var now time.Time
	if paidAt.IsZero() {
		err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	} else if kind == FundingManualBonus {
		err = tx.QueryRow(ctx, `SELECT greatest(clock_timestamp(),$1::timestamptz+interval '1 microsecond')`, paidAt).Scan(&now)
	} else {
		now = paidAt
	}
	if err != nil {
		return receipt, err
	}
	journal, err := postLocked(ctx, tx, scope.TenantID, operation, journalKind, o.ID, scope.ActorID, map[string]int64{"AVAILABLE": int64(amount), clearing: -int64(amount)}, accounts)
	if err != nil {
		return receipt, err
	}
	receipt = FundingReceipt{ID: uuid.NewString(), TenantID: scope.TenantID, SourceKind: kind, SourceID: o.ID, Amount: amount, Currency: "COIN", JournalID: journal, CreatedAt: now, BasisKind: o.BasisKind, ParentRef: o.ID}
	if kind == FundingManualBonus {
		receipt.BasisKind = "grant"
	}
	if kind == FundingManualCredit {
		receipt.FiatCurrency = o.FiatCurrency
		receipt.FiatAmount = o.FiatAmount
	}
	if _, err = tx.Exec(ctx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at,basis_kind,fiat_currency,fiat_amount,parent_ref) VALUES($1,$2,$3,$4,$5,'COIN',$6,$7,$8,$9,$10,$11)`, receipt.ID, receipt.TenantID, string(receipt.SourceKind), receipt.SourceID, int64(amount), journal, now, receipt.BasisKind, nullableText(receipt.FiatCurrency), receipt.FiatAmount, receipt.ParentRef); err != nil {
		return FundingReceipt{}, err
	}
	if err = createFundingLotTx(ctx, tx, receipt, operation); err != nil {
		return FundingReceipt{}, err
	}
	if err = emit(ctx, tx, scope.TenantID, operation, "credited", map[string]any{"amount": amount}); err != nil {
		return FundingReceipt{}, err
	}
	return receipt, nil
}
