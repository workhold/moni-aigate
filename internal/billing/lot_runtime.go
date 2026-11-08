package billing

import (
	"context"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// These helpers participate in the caller's financial transaction. Accounts
// must already be locked before any lot balance is locked; no helper commits.
type fundingLotBalance struct {
	id                                  string
	available, held, consumed, revision int64
}

func verifyFundingReceiptLotTx(ctx context.Context, tx pgx.Tx, receipt FundingReceipt) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM funding_lots l
 JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id
 JOIN funding_lot_events e ON e.lot_id=l.id AND e.tenant_id=l.tenant_id AND e.revision=1
 WHERE l.id=$1 AND l.funding_receipt_id=$1 AND l.tenant_id=$2 AND l.source_kind=$3 AND l.source_id=$4
 AND l.issued_amount=$5 AND l.fifo_at=$6 AND e.kind='funding' AND e.journal_id=$7
 AND e.delta_available=$5 AND e.delta_held=0 AND e.delta_consumed=0)`, receipt.ID, receipt.TenantID, string(receipt.SourceKind), receipt.SourceID, int64(receipt.Amount), receipt.CreatedAt, receipt.JournalID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return lotPlanInvariant()
	}
	return nil
}

func createFundingLotTx(ctx context.Context, tx pgx.Tx, receipt FundingReceipt, operation string) error {
	class := "nonrefundable"
	if receipt.SourceKind == FundingSandboxCapture {
		class = "sandbox_original"
	}
	if receipt.Amount <= 0 || !voucherID(receipt.ID) || operation == "" {
		return lotPlanInvariant()
	}
	_, err := tx.Exec(ctx, `INSERT INTO funding_lots(id,tenant_id,funding_receipt_id,source_kind,source_id,refund_class,issued_amount,fifo_at)
 VALUES($1,$2,$1,$3,$4,$5,$6,$7)`, receipt.ID, receipt.TenantID, string(receipt.SourceKind), receipt.SourceID, class, int64(receipt.Amount), receipt.CreatedAt)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,held_amount,consumed_amount,revision) VALUES($1,$2,$3,0,0,1)`, receipt.ID, receipt.TenantID, int64(receipt.Amount)); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 VALUES($1,$2,$3,$4,$5,'funding',1,$6,0,0)`, uuid.NewString(), receipt.TenantID, receipt.ID, operation, receipt.JournalID, int64(receipt.Amount))
	return err
}

func lockFundingLotBalancesTx(ctx context.Context, tx pgx.Tx, tenant string, ids []string) (map[string]*fundingLotBalance, error) {
	result := make(map[string]*fundingLotBalance, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := tx.Query(ctx, `SELECT lot_id,available_amount,held_amount,consumed_amount,revision FROM funding_lot_balances
 WHERE tenant_id=$1 AND lot_id=ANY($2::text[]) ORDER BY lot_id COLLATE "C" FOR UPDATE`, tenant, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		b := &fundingLotBalance{}
		if err = rows.Scan(&b.id, &b.available, &b.held, &b.consumed, &b.revision); err != nil {
			return nil, err
		}
		if b.available < 0 || b.held < 0 || b.consumed < 0 || b.revision < 1 {
			return nil, lotPlanInvariant()
		}
		result[b.id] = b
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(result) != len(ids) {
		return nil, lotPlanInvariant()
	}
	return result, nil
}

func changeFundingLotTx(ctx context.Context, tx pgx.Tx, tenant, operation, journal, kind string, b *fundingLotBalance, available, held, consumed int64) error {
	next, err := projectFundingLotChange(b, available, held, consumed)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), tenant, b.id, operation, journal, kind, next.revision, available, held, consumed)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,revision=$6
 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$7 AND available_amount=$8 AND held_amount=$9 AND consumed_amount=$10`, tenant, b.id, next.available, next.held, next.consumed, next.revision, b.revision, b.available, b.held, b.consumed)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return lotPlanInvariant()
	}
	*b = next
	return nil
}

func projectFundingLotChange(b *fundingLotBalance, available, held, consumed int64) (fundingLotBalance, error) {
	if b == nil {
		return fundingLotBalance{}, lotPlanInvariant()
	}
	a, err := checkedAdd(b.available, available)
	if err != nil {
		return fundingLotBalance{}, err
	}
	h, err := checkedAdd(b.held, held)
	if err != nil {
		return fundingLotBalance{}, err
	}
	c, err := checkedAdd(b.consumed, consumed)
	if err != nil {
		return fundingLotBalance{}, err
	}
	revision, err := checkedAdd(b.revision, 1)
	if err != nil {
		return fundingLotBalance{}, err
	}
	if a < 0 || h < 0 || c < 0 {
		return fundingLotBalance{}, lotPlanInvariant()
	}
	next := *b
	next.available, next.held, next.consumed, next.revision = a, h, c, revision
	return next, nil
}

type fundingReservePlan struct {
	allocations []fundingLotAllocation
	balances    map[string]*fundingLotBalance
}

func prepareFundingReserveTx(ctx context.Context, tx pgx.Tx, tenant string, amount Money) (fundingReservePlan, error) {
	plan := fundingReservePlan{balances: map[string]*fundingLotBalance{}}
	if amount < 0 {
		return plan, lotPlanInvariant()
	}
	if amount == 0 {
		return plan, nil
	}
	rows, err := tx.Query(ctx, `SELECT b.lot_id,l.fifo_at,b.available_amount,b.held_amount,b.consumed_amount,b.revision
 FROM funding_lot_balances b JOIN funding_lots l ON l.id=b.lot_id AND l.tenant_id=b.tenant_id
 WHERE b.tenant_id=$1 AND b.available_amount>0 ORDER BY b.lot_id COLLATE "C" FOR UPDATE OF b`, tenant)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	candidates := []fundingLotCandidate{}
	for rows.Next() {
		b := &fundingLotBalance{}
		var at time.Time
		if err = rows.Scan(&b.id, &at, &b.available, &b.held, &b.consumed, &b.revision); err != nil {
			return fundingReservePlan{}, err
		}
		if b.available <= 0 || b.held < 0 || b.consumed < 0 || b.revision < 1 {
			return fundingReservePlan{}, lotPlanInvariant()
		}
		plan.balances[b.id] = b
		candidates = append(candidates, fundingLotCandidate{lotID: b.id, fifoAt: at, available: Money(b.available)})
	}
	if err = rows.Err(); err != nil {
		return fundingReservePlan{}, err
	}
	plan.allocations, err = planReserveFundingLots(candidates, amount)
	return plan, err
}

func applyFundingReserveTx(ctx context.Context, tx pgx.Tx, r Reservation, journal string, plan fundingReservePlan) error {
	if err := validateFundingLotAllocations(plan.allocations, r.Amount); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, a := range plan.allocations {
		balance := plan.balances[a.lotID]
		next, err := projectFundingLotChange(balance, -int64(a.amount), int64(a.amount), 0)
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO funding_reservation_lots(tenant_id,reservation_id,request_id,lot_id,allocation_index,amount) VALUES($1,$2,$3,$4,$5,$6)`, r.TenantID, r.ID, r.RequestID, a.lotID, a.index, int64(a.amount))
		batch.Queue(`INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 VALUES($1,$2,$3,$4,$5,'reserve',$6,$7,$8,0)`, uuid.NewString(), r.TenantID, a.lotID, r.ID, journal, next.revision, -int64(a.amount), int64(a.amount))
		batch.Queue(`UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,revision=$6
 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$7 AND available_amount=$8 AND held_amount=$9 AND consumed_amount=$10`, r.TenantID, a.lotID, next.available, next.held, next.consumed, next.revision, balance.revision, balance.available, balance.held, balance.consumed)
		*balance = next
	}
	return closeBatch(tx.SendBatch(ctx, batch), nil)
}

func prepareFundingFinishTx(ctx context.Context, tx pgx.Tx, r financialRequest) (fundingReservePlan, error) {
	plan := fundingReservePlan{}
	rows, err := tx.Query(ctx, `SELECT allocation_index,lot_id,amount FROM funding_reservation_lots WHERE tenant_id=$1 AND reservation_id=$2 AND request_id=$3 ORDER BY allocation_index`, r.TenantID, r.ReservationID, r.ID)
	if err != nil {
		return plan, err
	}
	ids := []string{}
	for rows.Next() {
		var a fundingLotAllocation
		if err = rows.Scan(&a.index, &a.lotID, &a.amount); err != nil {
			rows.Close()
			return fundingReservePlan{}, err
		}
		plan.allocations = append(plan.allocations, a)
		ids = append(ids, a.lotID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fundingReservePlan{}, err
	}
	if err = validateFundingLotAllocations(plan.allocations, Money(r.Amount)); err != nil {
		return fundingReservePlan{}, err
	}
	plan.balances, err = lockFundingLotBalancesTx(ctx, tx, r.TenantID, ids)
	return plan, err
}

func applyFundingFinishTx(ctx context.Context, tx pgx.Tx, r financialRequest, settlement, journal string, charge Money, plan fundingReservePlan) error {
	finish, err := planFinishFundingLots(plan.allocations, Money(r.Amount), charge)
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, f := range finish {
		a := f.allocation
		balance := plan.balances[a.lotID]
		next, err := projectFundingLotChange(balance, int64(f.released), -int64(a.amount), int64(f.charged))
		if err != nil {
			return err
		}
		batch.Queue(`INSERT INTO funding_settlement_lots(tenant_id,settlement_id,reservation_id,allocation_index,lot_id,charged_amount,released_amount) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.TenantID, settlement, r.ReservationID, a.index, a.lotID, int64(f.charged), int64(f.released))
		batch.Queue(`INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 VALUES($1,$2,$3,$4,$5,'finish',$6,$7,$8,$9)`, uuid.NewString(), r.TenantID, a.lotID, settlement, journal, next.revision, int64(f.released), -int64(a.amount), int64(f.charged))
		batch.Queue(`UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,revision=$6
 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$7 AND available_amount=$8 AND held_amount=$9 AND consumed_amount=$10`, r.TenantID, a.lotID, next.available, next.held, next.consumed, next.revision, balance.revision, balance.available, balance.held, balance.consumed)
		*balance = next
	}
	return closeBatch(tx.SendBatch(ctx, batch), nil)
}

type fundingRefundPlan struct {
	refunds  []fundingLotRefund
	balances map[string]*fundingLotBalance
}

func prepareFundingRefundTx(ctx context.Context, tx pgx.Tx, r financialRequest, settlement string, charged, refunded int64, amount Money) (fundingRefundPlan, error) {
	plan := fundingRefundPlan{}
	rows, err := tx.Query(ctx, `SELECT s.allocation_index,s.lot_id,a.amount,s.charged_amount,
 COALESCE((SELECT sum(f.amount::numeric) FROM funding_consumption_refund_lots f WHERE f.tenant_id=s.tenant_id AND f.settlement_id=s.settlement_id AND f.allocation_index=s.allocation_index),0)::bigint
 FROM funding_settlement_lots s JOIN funding_reservation_lots a ON a.tenant_id=s.tenant_id AND a.reservation_id=s.reservation_id AND a.allocation_index=s.allocation_index AND a.lot_id=s.lot_id
 WHERE s.tenant_id=$1 AND s.settlement_id=$2 AND s.reservation_id=$3 ORDER BY s.allocation_index`, r.TenantID, settlement, r.ReservationID)
	if err != nil {
		return plan, err
	}
	original := []fundingLotCharged{}
	allocations := []fundingLotAllocation{}
	ids := []string{}
	totalCharged, totalRefunded := new(big.Int), new(big.Int)
	for rows.Next() {
		var e fundingLotCharged
		if err = rows.Scan(&e.allocation.index, &e.allocation.lotID, &e.allocation.amount, &e.charged, &e.refunded); err != nil {
			rows.Close()
			return fundingRefundPlan{}, err
		}
		original = append(original, e)
		allocations = append(allocations, e.allocation)
		ids = append(ids, e.allocation.lotID)
		totalCharged.Add(totalCharged, big.NewInt(int64(e.charged)))
		totalRefunded.Add(totalRefunded, big.NewInt(int64(e.refunded)))
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fundingRefundPlan{}, err
	}
	if err = validateFundingLotAllocations(allocations, Money(r.Amount)); err != nil {
		return fundingRefundPlan{}, err
	}
	if totalCharged.Cmp(big.NewInt(charged)) != 0 || totalRefunded.Cmp(big.NewInt(refunded)) != 0 {
		return fundingRefundPlan{}, lotPlanInvariant()
	}
	plan.refunds, err = planRefundFundingLots(original, amount)
	if err != nil {
		return fundingRefundPlan{}, err
	}
	plan.balances, err = lockFundingLotBalancesTx(ctx, tx, r.TenantID, ids)
	return plan, err
}

func applyFundingRefundTx(ctx context.Context, tx pgx.Tx, tenant, refund, settlement, journal string, plan fundingRefundPlan) error {
	for _, f := range plan.refunds {
		if _, err := tx.Exec(ctx, `INSERT INTO funding_consumption_refund_lots(tenant_id,refund_order_id,settlement_id,allocation_index,lot_id,refund_index,amount) VALUES($1,$2,$3,$4,$5,$6,$7)`, tenant, refund, settlement, f.allocationIndex, f.lotID, f.index, int64(f.amount)); err != nil {
			return err
		}
		if err := changeFundingLotTx(ctx, tx, tenant, refund, journal, "refund", plan.balances[f.lotID], int64(f.amount), 0, -int64(f.amount)); err != nil {
			return err
		}
	}
	return nil
}
