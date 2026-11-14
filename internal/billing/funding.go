package billing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type FundingSourceKind string

const (
	FundingManualCredit   FundingSourceKind = "manual_credit"
	FundingVoucherGrant   FundingSourceKind = "voucher_grant"
	FundingSandboxCapture FundingSourceKind = "sandbox_capture"
	FundingWalletOpening  FundingSourceKind = "wallet_opening"
	FundingManualBonus    FundingSourceKind = "manual_bonus"
	FundingManualUnknown  FundingSourceKind = "manual_unknown"
)

type FundingSourceRef struct {
	Kind FundingSourceKind
	ID   string
}
type FundingScope struct{ TenantID, ActorID, CommandID string }
type FundingReceipt struct {
	ID, TenantID        string
	SourceKind          FundingSourceKind
	SourceID            string
	Amount              Money
	Currency, JournalID string
	CreatedAt           time.Time
	BasisKind           string
	FiatCurrency        string
	FiatAmount          *FiatMoney
	BuyRateVersionID    string
	ParentRef           string
}
type FundingResult struct {
	Receipt      FundingReceipt
	RedemptionID string
	Applied      bool
}

func fundingReceiptRow(ctx context.Context, tx pgx.Tx, kind FundingSourceKind, id string) (FundingReceipt, error) {
	var r FundingReceipt
	e := tx.QueryRow(ctx, `SELECT id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at,basis_kind,coalesce(fiat_currency,''),fiat_amount,coalesce(buy_rate_version_id,''),parent_ref FROM funding_receipts WHERE source_kind=$1 AND source_id=$2`, string(kind), id).Scan(&r.ID, &r.TenantID, &r.SourceKind, &r.SourceID, &r.Amount, &r.Currency, &r.JournalID, &r.CreatedAt, &r.BasisKind, &r.FiatCurrency, &r.FiatAmount, &r.BuyRateVersionID, &r.ParentRef)
	return r, e
}

// ApplyFundingTx owns the only funding journal/receipt/credited write. The
// caller must already hold the source root and authorization parent locks in
// the frozen order. This function does not acquire those locks, retry or commit.
func (s *Store) ApplyFundingTx(ctx context.Context, tx pgx.Tx, source FundingSourceRef, scope FundingScope) (FundingResult, error) {
	var result FundingResult
	if tx == nil || source.ID == "" || scope.TenantID == "" || scope.ActorID == "" {
		return result, fail("invalid_funding_source", "funding source required")
	}
	if source.Kind == FundingSandboxCapture && (!voucherID(source.ID) || !voucherID(scope.TenantID) || scope.ActorID != sandboxFundingActor || scope.CommandID != "") {
		return result, fail("invalid_funding_source", "funding source unavailable")
	}
	clearing := "FUNDING_CLEARING"
	var amount Money
	var requester string
	var sandbox sandboxFundingSource
	switch source.Kind {
	case FundingManualCredit:
		return s.applyCreditTx(ctx, tx, source, scope)
	case FundingVoucherGrant:
		if scope.CommandID == "" {
			return result, fail("invalid_funding_source", "voucher command required")
		}
		clearing = "OPERATING_GRANT_CLEARING"
		var eligible bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vouchers v JOIN voucher_batches b ON b.id=v.batch_id JOIN voucher_policy p ON p.singleton
   JOIN commerce_commands c ON c.operation_id=$2
   JOIN tenants t ON t.id=$3 JOIN memberships m ON m.tenant_id=t.id AND m.user_id=$4 JOIN users u ON u.id=m.user_id
   WHERE v.id=$1 AND b.state='active' AND p.enabled AND c.kind='voucher.redeem' AND c.scope='tenant:'||t.id AND c.actor=u.id
   AND t.status='active' AND m.status='active' AND m.role='owner' AND u.status='active'
   AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.voucher_id=v.id))`, source.ID, scope.CommandID, scope.TenantID, scope.ActorID).Scan(&eligible); e != nil {
			return result, e
		}
		if !eligible {
			return result, fail("voucher_unavailable", "voucher unavailable")
		}
		if e := tx.QueryRow(ctx, `SELECT b.face_amount FROM vouchers v JOIN voucher_batches b ON b.id=v.batch_id WHERE v.id=$1`, source.ID).Scan(&amount); e != nil {
			return result, e
		}
	case FundingSandboxCapture:
		var now time.Time
		if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			return result, e
		}
		var e error
		sandbox, e = readSandboxFundingSource(ctx, tx, source, scope, now)
		if e != nil {
			return result, e
		}
		if sandbox.Receipt != nil {
			if e = verifyFundingReceiptLotTx(ctx, tx, *sandbox.Receipt); e != nil {
				return result, e
			}
			result.Receipt = *sandbox.Receipt
			return result, nil
		}
		amount, clearing = sandbox.Amount, "SANDBOX_FUNDING_CLEARING"
	case FundingWalletOpening:
		if scope.CommandID != "" || !voucherID(source.ID) {
			return result, fail("invalid_funding_source", "opening source unavailable")
		}
		var tenant, actor string
		if e := tx.QueryRow(ctx, `SELECT tenant_id,amount,actor FROM wallet_openings WHERE id=$1`, source.ID).Scan(&tenant, &amount, &actor); e != nil {
			return result, e
		}
		if tenant != scope.TenantID || actor != scope.ActorID || amount <= 0 {
			return result, fail("funding_integrity", "funding source integrity failed")
		}
		receipt, e := fundingReceiptRow(ctx, tx, source.Kind, source.ID)
		if e == nil {
			var valid bool
			if e = tx.QueryRow(ctx, `SELECT funding_journal_valid($1)`, receipt.JournalID).Scan(&valid); e != nil {
				return result, e
			}
			if !valid || receipt.TenantID != tenant || receipt.Amount != amount {
				return result, fail("funding_integrity", "funding source integrity failed")
			}
			if e = verifyFundingReceiptLotTx(ctx, tx, receipt); e != nil {
				return result, e
			}
			result.Receipt = receipt
			return result, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return result, e
		}
		clearing = "WALLET_OPENING_CLEARING"
	default:
		return result, fail("invalid_funding_source", "funding source unavailable")
	}
	if amount <= 0 {
		return result, fail("funding_integrity", "funding source integrity failed")
	}
	accounts, e := lockAccounts(ctx, tx, scope.TenantID, "AVAILABLE", clearing)
	if e != nil {
		return result, e
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return result, e
	}
	operation, kind := source.ID, "credit"
	var batch string
	reference := source.ID
	switch source.Kind {
	case FundingManualCredit:
		var stable bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM credit_orders WHERE id=$1 AND tenant_id=$2 AND state='pending' AND amount=$3 AND requester=$4 AND approver IS NULL AND journal_id IS NULL)`, source.ID, scope.TenantID, int64(amount), requester).Scan(&stable); e != nil {
			return result, e
		}
		if !stable {
			return result, fail("funding_integrity", "funding source integrity failed")
		}
	case FundingVoucherGrant:
		if e = tx.QueryRow(ctx, `SELECT b.id FROM vouchers v JOIN voucher_batches b ON b.id=v.batch_id WHERE v.id=$1 AND b.state='active' AND b.face_amount=$2 AND b.expires_at>$3 AND b.activated_at<=$3 AND NOT EXISTS(SELECT 1 FROM voucher_redemptions r WHERE r.voucher_id=v.id)`, source.ID, int64(amount), now).Scan(&batch); errors.Is(e, pgx.ErrNoRows) {
			return result, fail("voucher_unavailable", "voucher unavailable")
		} else if e != nil {
			return result, e
		}
		operation = uuid.NewString()
		kind = "voucher_grant"
	case FundingSandboxCapture:
		current, err := readSandboxFundingSource(ctx, tx, source, scope, now)
		if err != nil {
			return result, err
		}
		if current.Receipt != nil || current.OrderID != sandbox.OrderID || current.Amount != amount {
			return result, fail("funding_integrity", "funding source integrity failed")
		}
		kind, reference = "sandbox_capture", current.OrderID
	case FundingWalletOpening:
		var stable bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wallet_openings WHERE id=$1 AND tenant_id=$2 AND actor=$3 AND amount=$4)
 AND NOT EXISTS(SELECT 1 FROM funding_receipts WHERE source_kind='wallet_opening' AND source_id=$1)`, source.ID, scope.TenantID, scope.ActorID, int64(amount)).Scan(&stable); e != nil {
			return result, e
		}
		if !stable {
			return result, fail("funding_integrity", "funding source integrity failed")
		}
		kind = "wallet_opening"
	default:
		return result, fail("invalid_funding_source", "funding source unavailable")
	}
	journal, e := postLocked(ctx, tx, scope.TenantID, operation, kind, reference, scope.ActorID, map[string]int64{"AVAILABLE": int64(amount), clearing: -int64(amount)}, accounts)
	if e != nil {
		return result, e
	}
	receipt := FundingReceipt{ID: uuid.NewString(), TenantID: scope.TenantID, SourceKind: source.Kind, SourceID: source.ID, Amount: amount, Currency: "COIN", JournalID: journal, CreatedAt: now, ParentRef: source.ID}
	switch source.Kind {
	case FundingVoucherGrant:
		receipt.BasisKind = "grant"
	case FundingSandboxCapture:
		receipt.BasisKind = "paid"
		receipt.FiatCurrency = sandbox.FiatCurrency
		receipt.FiatAmount = &sandbox.FiatAmount
		receipt.BuyRateVersionID, e = coinRateAt(ctx, tx, sandbox.FiatCurrency)
		if e != nil {
			return result, e
		}
	case FundingWalletOpening:
		receipt.BasisKind = "opening"
	}
	switch source.Kind {
	case FundingManualCredit:
		if _, e = tx.Exec(ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, source.ID, scope.ActorID, journal); e != nil {
			return result, e
		}
	case FundingVoucherGrant:
		if _, e = tx.Exec(ctx, `INSERT INTO voucher_redemptions(id,tenant_id,voucher_id,batch_id,operation_id,actor,amount,journal_id,funding_receipt_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, operation, scope.TenantID, source.ID, batch, scope.CommandID, scope.ActorID, int64(amount), journal, receipt.ID, now); e != nil {
			return result, e
		}
		result.RedemptionID = operation
	case FundingSandboxCapture:
		// The caller owns payment audit and the final processing-generation CAS.
	case FundingWalletOpening:
		// The owner command owns the immutable opening source and audit.
	default:
		return result, fail("invalid_funding_source", "funding source unavailable")
	}
	if _, e = tx.Exec(ctx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at,basis_kind,fiat_currency,fiat_amount,buy_rate_version_id,parent_ref) VALUES($1,$2,$3,$4,$5,'COIN',$6,$7,$8,$9,$10,$11,$12)`, receipt.ID, receipt.TenantID, string(receipt.SourceKind), receipt.SourceID, int64(amount), journal, now, receipt.BasisKind, nullableText(receipt.FiatCurrency), receipt.FiatAmount, nullableText(receipt.BuyRateVersionID), receipt.ParentRef); e != nil {
		return FundingResult{}, e
	}
	if e = createFundingLotTx(ctx, tx, receipt, operation); e != nil {
		return FundingResult{}, e
	}
	if e = emit(ctx, tx, scope.TenantID, operation, "credited", map[string]any{"amount": amount}); e != nil {
		return FundingResult{}, e
	}
	result.Receipt = receipt
	result.Applied = true
	return result, nil
}
