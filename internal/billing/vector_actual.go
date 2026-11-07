package billing

import (
	"bytes"
	"context"
	"errors"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// These reads use the original immutable version even when the plan-readiness
// gate left snapshot canonical columns NULL. They never select a current price
// or reconstruct per-million rates. Finalization canonical/digests are already
// checked against the observation by the database's inference.final.v1 guard.
const retailActualMeterSQL = `SELECT m.canonical,s.schema_digest,p.rates_canonical,p.rates_digest,f.retail_quantities_canonical
 FROM request_meter_snapshots s
 JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
 JOIN price_versions p ON p.id=s.price_version_id AND p.model_id=s.model_id
  AND p.schema_id=s.schema_id AND p.schema_digest=s.schema_digest AND p.variant_key=s.variant_key
  AND p.price_group_id IS NOT DISTINCT FROM s.pricing_group_id
 JOIN finalization_records f ON f.tenant_id=s.tenant_id AND f.request_id=s.request_id
 WHERE s.tenant_id=$1 AND s.request_id=$2 AND f.id=$3 AND f.retail_usage_state='confirmed_final'`

const supplierActualMeterSQL = `SELECT m.canonical,s.schema_digest,c.rates_canonical,c.rates_digest,f.supplier_quantities_canonical
 FROM attempt_meter_snapshots s
 JOIN upstream_attempts a ON a.tenant_id=s.tenant_id AND a.request_id=s.request_id AND a.id=s.attempt_id
 JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
 JOIN cost_versions c ON c.id=s.cost_version_id AND c.channel_id=a.channel_id AND c.upstream_model=a.upstream_model
  AND c.schema_id=s.schema_id AND c.schema_digest=s.schema_digest AND c.variant_key=s.variant_key
  AND c.basis=s.cost_basis AND c.basis IN ('contract','invoice')
 JOIN finalization_records f ON f.tenant_id=s.tenant_id AND f.request_id=s.request_id AND f.attempt_id=s.attempt_id
 WHERE s.tenant_id=$1 AND s.request_id=$2 AND f.id=$3 AND s.attempt_id=$4 AND f.supplier_usage_state='confirmed_final'`

func storedActualAmount(ctx context.Context, tx pgx.Tx, tenant, request, finalization, attempt string, side metering.Side) (*big.Int, error) {
	query := retailActualMeterSQL
	args := []any{tenant, request, finalization}
	if side == metering.Supplier {
		query = supplierActualMeterSQL
		args = append(args, attempt)
	} else if side != metering.Retail {
		return nil, fail("invalid_meter_snapshot", "invalid actual meter side")
	}
	var schema, schemaDigest, rates, ratesDigest, quantities []byte
	if err := tx.QueryRow(ctx, query, args...).Scan(&schema, &schemaDigest, &rates, &ratesDigest, &quantities); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fail("invalid_meter_snapshot", "original price or confirmed quantities are missing")
		}
		return nil, err
	}
	return canonicalActualAmount(schema, schemaDigest, rates, ratesDigest, quantities, side)
}

func canonicalActualAmount(schemaRaw, schemaDigest, ratesRaw, ratesDigest, quantitiesRaw []byte, side metering.Side) (*big.Int, error) {
	invalid := func() (*big.Int, error) {
		return nil, fail("invalid_meter_snapshot", "original meter canonical or binding is invalid")
	}
	schema, rates, err := canonicalMeterRates(schemaRaw, schemaDigest, ratesRaw, ratesDigest, side)
	if err != nil {
		return nil, err
	}
	quantities, err := metering.ParseQuantities(schema, side, quantitiesRaw)
	if err != nil {
		return invalid()
	}
	canonical, _ := quantities.CanonicalJSON()
	if !bytes.Equal(canonical, quantitiesRaw) {
		return invalid()
	}
	return metering.Amount(quantities, rates)
}
