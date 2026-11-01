package mockpsp

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type faultPlan struct{ control, boundary, kind, target string }

func readFaultPlan(ctx context.Context, tx pgx.Tx, kind, target string) (faultPlan, error) {
	var p faultPlan
	err := tx.QueryRow(ctx, `SELECT control_operation_id,boundary,target_kind,target_id FROM mock_fault_plans WHERE target_kind=$1 AND target_id=$2 AND remaining=1`, kind, target).Scan(&p.control, &p.boundary, &p.kind, &p.target)
	if errors.Is(err, pgx.ErrNoRows) {
		return faultPlan{}, nil
	}
	return p, err
}

func consumePlanTx(ctx context.Context, tx pgx.Tx, p faultPlan) error {
	if p.control == "" {
		return fail("mock_unavailable")
	}
	tag, err := tx.Exec(ctx, `UPDATE mock_fault_plans SET remaining=0,consumed_at=clock_timestamp() WHERE control_operation_id=$1 AND boundary=$2 AND target_kind=$3 AND target_id=$4 AND remaining=1`, p.control, p.boundary, p.kind, p.target)
	if err == nil && tag.RowsAffected() != 1 {
		return fail("mock_unavailable")
	}
	return err
}

func (c *repoConnection) consumePlan(ctx context.Context, p faultPlan) error {
	return c.transact(ctx, func(tx pgx.Tx) error { return consumePlanTx(ctx, tx, p) })
}

func (c *repoConnection) finishAfterPlan(ctx context.Context, p faultPlan, boundary string, first bool) error {
	if p.boundary != boundary {
		return nil
	}
	if err := c.consumePlan(ctx, p); err != nil {
		return ErrDisconnect
	}
	if first {
		return ErrDisconnect
	}
	return nil
}
