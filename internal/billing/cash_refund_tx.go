package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// cashRefundTransaction preserves unknown COMMIT outcomes. A known database
// rejection may use the same bounded serialization retry as the existing store;
// a missing commit reply destroys the exact connection and is never replayed.
func (s *Store) cashRefundTransaction(ctx context.Context, fn func(pgx.Tx) error) error {
	for attempt := 0; ; attempt++ {
		conn, err := s.pool.Acquire(ctx)
		if err != nil {
			return err
		}
		tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			conn.Release()
			return err
		}
		if _, err = tx.Exec(ctx, "SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)"); err == nil {
			err = fn(tx)
		}
		commitCalled := err == nil
		if commitCalled {
			err = tx.Commit(ctx)
		}
		if err == nil {
			conn.Release()
			return nil
		}
		var databaseError *pgconn.PgError
		knownDatabaseOutcome := errors.Is(err, pgx.ErrTxCommitRollback) || errors.As(err, &databaseError) && !strings.HasPrefix(databaseError.Code, "08")
		if commitCalled && !knownDatabaseOutcome {
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = conn.Conn().Close(cleanup)
			cancel()
			conn.Release()
			return fail("cash_refund_commit_unknown", "cash-refund commit is unconfirmed; read the original order before retrying")
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = tx.Rollback(cleanup)
		cancel()
		conn.Release()
		if attempt >= 2 || ctx.Err() != nil || databaseError == nil || !oneOf(databaseError.Code, "40P01", "40001") {
			return err
		}
	}
}

func cashRefundActorAllowed(ctx context.Context, tx pgx.Tx, actor, required string, single bool) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT wallet_cash_refund_actor_valid($1,$2,$3)`, actor, required, single).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fail("forbidden", "financial role required")
	}
	return nil
}

func cashRefundAnyActorAllowed(ctx context.Context, tx pgx.Tx, actor string, single bool) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT wallet_cash_refund_actor_valid($1,'finance_operator',$2) OR wallet_cash_refund_actor_valid($1,'finance_approver',$2)`, actor, single).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fail("forbidden", "financial role required")
	}
	return nil
}

func applyCashRefundLotTx(ctx context.Context, tx pgx.Tx, tenant, orderID, operation, journal, kind string, balance *fundingLotBalance, available, refundHeld, retired int64) error {
	next, err := projectFundingLotChange(balance, available, 0, 0, refundHeld, retired)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed,delta_refund_held,delta_retired,refund_order_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,0,0,$9,$10,$11)`, uuid.NewString(), tenant, balance.id, operation, journal, kind, next.revision, available, refundHeld, retired, orderID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE funding_lot_balances SET available_amount=$3,held_amount=$4,consumed_amount=$5,refund_held_amount=$6,retired_amount=$7,revision=$8 WHERE tenant_id=$1 AND lot_id=$2 AND revision=$9 AND available_amount=$10 AND held_amount=$11 AND consumed_amount=$12 AND refund_held_amount=$13 AND retired_amount=$14`, tenant, balance.id, next.available, next.held, next.consumed, next.refundHeld, next.retired, next.revision, balance.revision, balance.available, balance.held, balance.consumed, balance.refundHeld, balance.retired)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return lotPlanInvariant()
	}
	*balance = next
	return nil
}

func closeCashRefundRequestTx(ctx context.Context, tx pgx.Tx, order CashRefundOrder, actor string) error {
	var requestID string
	err := tx.QueryRow(ctx, `UPDATE wallet_requests SET state='closed' WHERE linked_order_id=$1 AND kind='cash_refund' AND state='linked' RETURNING id`, order.ID).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return audit(ctx, tx, order.TenantID, actor, "wallet.request.closed", requestID, "", map[string]any{"kind": "cash_refund", "linked_order_id": order.ID})
}
