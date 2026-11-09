package migrations

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// VerifyReadOnlyTx checks the historical fixture manifest in the caller's
// existing RR READ ONLY snapshot. It never begins, commits, installs or locks.
func VerifyReadOnlyTx(ctx context.Context, tx pgx.Tx) error {
	p, e := plan("", false)
	return verifyReadOnlyTx(ctx, tx, p, e)
}

// VerifyCurrentReadOnlyTx checks the single current MVP baseline in the
// caller's existing RR READ ONLY snapshot.
func VerifyCurrentReadOnlyTx(ctx context.Context, tx pgx.Tx) error {
	p, e := currentPlan()
	return verifyReadOnlyTx(ctx, tx, p, e)
}

func verifyReadOnlyTx(ctx context.Context, tx pgx.Tx, p migrationPlan, planErr error) error {
	if ctx == nil || tx == nil {
		return fmt.Errorf("read-only migration verification requires context and transaction")
	}
	var isolation, readOnly string
	if e := tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation'),current_setting('transaction_read_only')`).Scan(&isolation, &readOnly); e != nil {
		return e
	}
	if isolation != "repeatable read" || readOnly != "on" {
		return fmt.Errorf("migration verification requires repeatable read read only")
	}
	if planErr != nil {
		return planErr
	}
	applied, _, e := readHistory(ctx, tx)
	if e != nil {
		return e
	}
	return validateHistory(p, applied, true)
}
