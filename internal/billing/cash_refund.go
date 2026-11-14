package billing

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type CashRefundOrder struct {
	ID                      string     `json:"id"`
	TenantID                string     `json:"tenant_id"`
	LotID                   string     `json:"lot_id"`
	BusinessRef             string     `json:"business_ref"`
	CoinAmount              *Money     `json:"coin_amount"`
	Reason                  string     `json:"reason"`
	State                   string     `json:"state"`
	RequestedBy             string     `json:"requested_by"`
	Maker                   string     `json:"maker"`
	Approver                string     `json:"approver,omitempty"`
	TransferExecutor        string     `json:"transfer_executor,omitempty"`
	FiatCurrency            string     `json:"fiat_currency,omitempty"`
	FiatAmount              *FiatMoney `json:"fiat_amount"`
	BuyRateVersionID        string     `json:"buy_rate_version_id,omitempty"`
	RefundRateVersionID     string     `json:"refund_rate_version_id,omitempty"`
	SingleOperator          bool       `json:"single_operator"`
	TransferOrderRef        string     `json:"transfer_order_ref,omitempty"`
	TransferProofRef        string     `json:"transfer_proof_ref,omitempty"`
	PaidFiatAmount          *FiatMoney `json:"paid_fiat_amount"`
	HoldJournalID           string     `json:"hold_journal_id,omitempty"`
	RetireJournalID         string     `json:"retire_journal_id,omitempty"`
	ReleaseJournalID        string     `json:"release_journal_id,omitempty"`
	ApprovedAt              *time.Time `json:"approved_at"`
	CreatedAt               time.Time  `json:"created_at"`
	RequestID               string     `json:"request_id,omitempty"`
	SingleOperatorException bool       `json:"single_operator_exception"`
	PreviewCoinAmount       *Money     `json:"preview_coin_amount,omitempty"`
	PreviewFiatAmount       *FiatMoney `json:"preview_fiat_amount,omitempty"`
	PreviewFiatCurrency     string     `json:"preview_fiat_currency,omitempty"`
}

type CashRefundCreateInput struct {
	IdempotencyKey string
	RequestHMAC    string
	TenantID       string
	ActorID        string
	LotID          string
	BusinessRef    string
	Reason         string
	RequestID      string
}

type CashRefundActionInput struct {
	IdempotencyKey   string
	RequestHMAC      string
	TenantID         string
	OrderID          string
	ActorID          string
	TransferOrderRef string
	TransferProofRef string
	PaidFiatAmount   *FiatMoney
	Reason           string
}

type cashRefundRate struct {
	id, currency string
	num, den     int64
}

type cashRefundSource struct {
	lotID, receiptID, currency, buyRateID string
	fiat                                  FiatMoney
	issued                                Money
	balance                               fundingLotBalance
}

func calculateCashRefundFiat(k, f, c int64, buy, current cashRefundRate) (FiatMoney, error) {
	if k <= 0 || f <= 0 || c <= 0 || k > c || buy.num <= 0 || buy.den <= 0 || current.num <= 0 || current.den <= 0 || buy.currency == "" || buy.currency != current.currency {
		return 0, fail("invalid_cash_refund", "invalid cash-refund amount or rate")
	}
	left := new(big.Int).Mul(big.NewInt(current.num), big.NewInt(buy.den))
	right := new(big.Int).Mul(big.NewInt(current.den), big.NewInt(buy.num))
	numerator := new(big.Int).Mul(big.NewInt(k), big.NewInt(f))
	denominator := big.NewInt(c)
	if left.Cmp(right) < 0 {
		numerator.Mul(numerator, big.NewInt(current.num))
		numerator.Mul(numerator, big.NewInt(buy.den))
		denominator.Mul(denominator, big.NewInt(current.den))
		denominator.Mul(denominator, big.NewInt(buy.num))
	}
	amount := new(big.Int).Quo(numerator, denominator)
	if !amount.IsInt64() {
		return 0, fail("amount_overflow", "cash-refund fiat amount exceeds supported range")
	}
	return FiatMoney(amount.Int64()), nil
}

const cashRefundOrderColumns = `id,tenant_id,lot_id,business_ref,coin_amount,reason,state,requested_by,maker,coalesce(approver,''),coalesce(transfer_executor,''),coalesce(fiat_currency,''),fiat_amount,coalesce(buy_rate_version_id,''),coalesce(refund_rate_version_id,''),single_operator,coalesce(transfer_order_ref,''),coalesce(transfer_proof_ref,''),paid_fiat_amount,coalesce(hold_journal_id,''),coalesce(retire_journal_id,''),coalesce(release_journal_id,''),approved_at,created_at,coalesce((SELECT wr.id FROM wallet_requests wr WHERE wr.linked_order_id=cash_refund_orders.id AND wr.kind='cash_refund'),'')`

func scanCashRefundOrder(row pgx.Row) (CashRefundOrder, error) {
	var o CashRefundOrder
	err := row.Scan(&o.ID, &o.TenantID, &o.LotID, &o.BusinessRef, &o.CoinAmount, &o.Reason, &o.State, &o.RequestedBy, &o.Maker, &o.Approver, &o.TransferExecutor, &o.FiatCurrency, &o.FiatAmount, &o.BuyRateVersionID, &o.RefundRateVersionID, &o.SingleOperator, &o.TransferOrderRef, &o.TransferProofRef, &o.PaidFiatAmount, &o.HoldJournalID, &o.RetireJournalID, &o.ReleaseJournalID, &o.ApprovedAt, &o.CreatedAt, &o.RequestID)
	o.SingleOperatorException = o.SingleOperator && o.Approver == o.Maker && o.RequestedBy == o.Approver
	return o, err
}

func cashRefundOrderRow(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (CashRefundOrder, error) {
	query := `SELECT ` + cashRefundOrderColumns + ` FROM cash_refund_orders WHERE tenant_id=$1 AND id=$2`
	if lock {
		query += ` FOR UPDATE`
	}
	return scanCashRefundOrder(tx.QueryRow(ctx, query, tenant, id))
}

func cashRefundBusinessRow(ctx context.Context, tx pgx.Tx, businessRef string) (CashRefundOrder, error) {
	return scanCashRefundOrder(tx.QueryRow(ctx, `SELECT `+cashRefundOrderColumns+` FROM cash_refund_orders WHERE business_ref=$1`, businessRef))
}

func cashRefundSourceTx(ctx context.Context, tx pgx.Tx, tenant, lot string, lock bool) (cashRefundSource, error) {
	var source cashRefundSource
	var class, basis string
	query := `SELECT l.id,f.id,l.issued_amount,b.available_amount,b.held_amount,b.consumed_amount,b.refund_held_amount,b.retired_amount,b.revision,f.fiat_amount,coalesce(f.fiat_currency,''),coalesce(f.buy_rate_version_id,''),l.refund_class,f.basis_kind
 FROM funding_lots l JOIN funding_receipts f ON f.id=l.funding_receipt_id AND f.tenant_id=l.tenant_id
 JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id WHERE l.tenant_id=$1 AND l.id=$2`
	if lock {
		query += ` FOR UPDATE OF b`
	}
	var issued int64
	err := tx.QueryRow(ctx, query, tenant, lot).Scan(&source.lotID, &source.receiptID, &issued, &source.balance.available, &source.balance.held, &source.balance.consumed, &source.balance.refundHeld, &source.balance.retired, &source.balance.revision, &source.fiat, &source.currency, &source.buyRateID, &class, &basis)
	source.balance.id = source.lotID
	source.issued = Money(issued)
	if err != nil {
		return source, err
	}
	if class != "cash_original" || basis != "paid" || source.currency == "" || source.buyRateID == "" || source.fiat <= 0 || issued <= 0 || source.balance.available < 0 || source.balance.refundHeld < 0 || source.balance.retired < 0 || source.balance.revision < 1 {
		return source, fail("cash_refund_ineligible", "only an available paid cash-original source can be refunded")
	}
	return source, nil
}

func cashRefundRateByID(ctx context.Context, tx pgx.Tx, id, currency string) (cashRefundRate, error) {
	var rate cashRefundRate
	err := tx.QueryRow(ctx, `SELECT id,fiat_currency,rate_num,rate_den FROM coin_rate_versions WHERE id=$1 AND fiat_currency=$2`, id, currency).Scan(&rate.id, &rate.currency, &rate.num, &rate.den)
	if errors.Is(err, pgx.ErrNoRows) {
		return rate, fail("coin_rate_unavailable", "the frozen buy-rate version is unavailable")
	}
	return rate, err
}

func (s *Store) previewCashRefundTx(ctx context.Context, tx pgx.Tx, tenant, lot string) (CashRefundOrder, error) {
	var preview CashRefundOrder
	source, err := cashRefundSourceTx(ctx, tx, tenant, lot, false)
	if err != nil {
		return preview, err
	}
	if source.balance.available < 0 {
		return preview, fail("cash_refund_ineligible", "source availability is invalid")
	}
	buy, err := cashRefundRateByID(ctx, tx, source.buyRateID, source.currency)
	if err != nil {
		return preview, err
	}
	currentID, err := coinRateAt(ctx, tx, source.currency)
	if err != nil {
		return preview, err
	}
	current, err := cashRefundRateByID(ctx, tx, currentID, source.currency)
	if err != nil {
		return preview, err
	}
	available := Money(source.balance.available)
	var fiat FiatMoney
	if available > 0 {
		fiat, err = calculateCashRefundFiat(source.balance.available, int64(source.fiat), int64(source.issued), buy, current)
		if err != nil {
			return preview, err
		}
	}
	preview.PreviewCoinAmount = &available
	preview.PreviewFiatAmount = &fiat
	preview.PreviewFiatCurrency = source.currency
	return preview, nil
}

func validCashRefundCreate(in CashRefundCreateInput) bool {
	return validWalletOperatorOperation(in.TenantID) && validWalletOperatorOperation(in.ActorID) && validWalletOperatorOperation(in.LotID) &&
		(in.RequestID == "" || validWalletOperatorOperation(in.RequestID)) && security.ValidReference(in.BusinessRef, 200) && validWalletOperatorReason(in.Reason) &&
		boundedString(in.IdempotencyKey, 200) && boundedString(in.RequestHMAC, 256)
}

func validCashRefundAction(in CashRefundActionInput) bool {
	return validWalletOperatorOperation(in.TenantID) && validWalletOperatorOperation(in.OrderID) && validWalletOperatorOperation(in.ActorID) && boundedString(in.IdempotencyKey, 200) && boundedString(in.RequestHMAC, 256)
}

func (s *Store) CashRefund(ctx context.Context, tenant, id string) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !validWalletOperatorOperation(tenant) || !validWalletOperatorOperation(id) {
		return result, fail("invalid_cash_refund", "invalid cash-refund identity")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundOrderRow(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if result.State == "requested" {
			preview, err := s.previewCashRefundTx(ctx, tx, tenant, result.LotID)
			if err != nil {
				return err
			}
			result.PreviewCoinAmount, result.PreviewFiatAmount, result.PreviewFiatCurrency = preview.PreviewCoinAmount, preview.PreviewFiatAmount, preview.PreviewFiatCurrency
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fail("cash_refund_not_found", "cash refund not found")
	}
	return result, err
}

func (s *Store) CashRefundByBusinessRef(ctx context.Context, businessRef string) (CashRefundOrder, error) {
	var result CashRefundOrder
	if !security.ValidReference(businessRef, 200) {
		return result, fail("invalid_cash_refund", "invalid cash-refund business reference")
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		result, err = cashRefundBusinessRow(ctx, tx, businessRef)
		if err != nil {
			return err
		}
		if result.State == "requested" {
			preview, err := s.previewCashRefundTx(ctx, tx, result.TenantID, result.LotID)
			if err != nil {
				return err
			}
			result.PreviewCoinAmount, result.PreviewFiatAmount, result.PreviewFiatCurrency = preview.PreviewCoinAmount, preview.PreviewFiatAmount, preview.PreviewFiatCurrency
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, fail("cash_refund_not_found", "cash refund not found")
	}
	return result, err
}
