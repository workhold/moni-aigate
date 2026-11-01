package migrations

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrVerifySnapshot omits database history values and driver diagnostics.
var ErrVerifySnapshot = errors.New("migration_snapshot_verification_failed")

// VerifySnapshotTx verifies the complete embedded migration history in a
// caller-owned RR transaction, including an offline maintenance write snapshot.
// It neither authorizes maintenance nor checks objects, roles, or business data.
// It never begins, finishes, installs, or acquires advisory/business row locks.
func VerifySnapshotTx(ctx context.Context, tx pgx.Tx) error {
	if ctx == nil || tx == nil || ctx.Err() != nil {
		return ErrVerifySnapshot
	}
	var isolation, access string
	if err := tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation'),current_setting('transaction_read_only')`).Scan(&isolation, &access); err != nil {
		return ErrVerifySnapshot
	}
	if isolation != "repeatable read" || (access != "on" && access != "off") {
		return ErrVerifySnapshot
	}
	p, err := plan("", false)
	if err != nil {
		return ErrVerifySnapshot
	}
	applied, _, err := readHistory(ctx, tx)
	if err != nil {
		return ErrVerifySnapshot
	}
	if validateHistory(p, applied, true) != nil {
		return ErrVerifySnapshot
	}
	return nil
}
