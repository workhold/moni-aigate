package billing

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// U is determined by the original attempt's complete supplier bounds and cost,
// including when its plan-readiness gate leaves canonical snapshot columns NULL.
// This query never selects a current price or reconstructs per-million rates.
const supplierReservationMeterSQL = `SELECT m.canonical,s.schema_digest,c.rates_canonical,c.rates_digest,
 meter_bounds_canonical(s.schema_id,'supplier',qi.quantity,qo.quantity)
 FROM attempt_meter_snapshots s
 JOIN upstream_attempts a ON a.tenant_id=s.tenant_id AND a.request_id=s.request_id AND a.id=s.attempt_id
 JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
 JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
  AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key AND c.basis=s.cost_basis
 JOIN attempt_meter_bound_lines qi ON qi.tenant_id=s.tenant_id AND qi.attempt_id=s.attempt_id AND qi.side='supplier' AND qi.dimension='text_input_token' AND qi.unit='token'
 JOIN attempt_meter_bound_lines qo ON qo.tenant_id=s.tenant_id AND qo.attempt_id=s.attempt_id AND qo.side='supplier' AND qo.dimension='text_output_token' AND qo.unit='token'
 WHERE s.tenant_id=$1 AND s.request_id=$2 AND s.attempt_id=$3 AND s.quote_status='known' AND c.basis IN ('contract','invoice')
 AND (SELECT count(*) FROM attempt_meter_bound_lines b WHERE b.attempt_id=s.attempt_id AND b.side='supplier')=2`

func storedSupplierReservationAmount(ctx context.Context, tx pgx.Tx, tenant, request, attempt string) (int64, error) {
	var schema, schemaDigest, rates, ratesDigest, bounds []byte
	if err := tx.QueryRow(ctx, supplierReservationMeterSQL, tenant, request, attempt).Scan(&schema, &schemaDigest, &rates, &ratesDigest, &bounds); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fail("invalid_meter_snapshot", "original supplier quote or complete bounds are missing")
		}
		return 0, err
	}
	return canonicalSupplierReservationAmount(schema, schemaDigest, rates, ratesDigest, bounds)
}

func canonicalSupplierReservationAmount(schemaRaw, schemaDigest, ratesRaw, ratesDigest, boundsRaw []byte) (int64, error) {
	schema, rates, err := canonicalMeterRates(schemaRaw, schemaDigest, ratesRaw, ratesDigest, metering.Supplier)
	if err != nil {
		return 0, err
	}
	bounds, err := metering.ParseBounds(schema, metering.Supplier, boundsRaw)
	if err != nil {
		return 0, fail("invalid_meter_snapshot", "original supplier bounds are invalid")
	}
	canonical, _ := bounds.CanonicalJSON()
	if !bytes.Equal(canonical, boundsRaw) {
		return 0, fail("invalid_meter_snapshot", "original supplier bounds are not canonical")
	}
	upper, err := metering.ReservationAmount(bounds, rates)
	if errors.Is(err, metering.ErrAmountOverflow) {
		return 0, fail("procurement_bound_invalid", "procurement hard upper bound cannot be represented")
	}
	return upper, err
}
