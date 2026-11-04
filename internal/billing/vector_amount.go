package billing

import (
	"context"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// schemaAmount prices two text-token quantities with the published schema.
// Rates are the existing per-million integers; metering reduces them and
// applies one final ceil.
func schemaAmount(ctx context.Context, tx pgx.Tx, schemaID string, side metering.Side, input, output, inputPerMillion, outputPerMillion int64) (*big.Int, error) {
	if input < 0 || output < 0 || inputPerMillion < 0 || outputPerMillion < 0 {
		return nil, fail("invalid_amount", "quantities and prices must be nonnegative")
	}
	var canonical []byte
	if err := tx.QueryRow(ctx, `SELECT canonical FROM meter_schemas WHERE id=$1`, schemaID).Scan(&canonical); err != nil {
		return nil, err
	}
	schema, err := metering.ParseSchema(canonical)
	if err != nil {
		return nil, fail("invalid_profile", "meter schema is not usable")
	}
	quantities, err := metering.LegacyTokenQuantities(schema, side, input, output)
	if err != nil {
		return nil, fail("invalid_amount", "usage is outside the published schema")
	}
	rates, err := metering.LegacyTokenRates(schema, side, inputPerMillion, outputPerMillion)
	if err != nil {
		return nil, fail("invalid_amount", "price is outside the published schema")
	}
	return metering.Amount(quantities, rates)
}

func schemaReservation(ctx context.Context, tx pgx.Tx, schemaID string, side metering.Side, input, output, inputPerMillion, outputPerMillion int64) (int64, error) {
	amount, err := schemaAmount(ctx, tx, schemaID, side, input, output, inputPerMillion, outputPerMillion)
	if err != nil {
		return 0, err
	}
	if !amount.IsInt64() {
		return 0, fail("amount_overflow", "amount exceeds supported range")
	}
	return amount.Int64(), nil
}
