package billing

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// coinRateAt is used only while posting a paid funding receipt. It never
// supplies a fallback rate and never participates in reservation or settlement.
func coinRateAt(ctx context.Context, tx pgx.Tx, currency string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM coin_rate_versions WHERE fiat_currency=$1 AND effective_at<=clock_timestamp() ORDER BY effective_at DESC,id DESC LIMIT 1`, currency).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fail("coin_rate_unavailable", "coin rate version is unavailable for the payment currency")
	}
	return id, err
}
