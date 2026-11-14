package catalog

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

func pricingDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func pricingSchema(raw, digest []byte, id, expectedDigest string) (metering.Schema, error) {
	s, err := metering.ParseSchema(raw)
	if err != nil {
		return s, pricingFail("pricing_invariant")
	}
	canonical, _ := s.CanonicalJSON()
	d, _ := s.Digest()
	sid, _ := s.ID()
	if sid != id || hex.EncodeToString(d[:]) != expectedDigest || !bytes.Equal(raw, canonical) || !bytes.Equal(digest, d[:]) {
		return metering.Schema{}, pricingFail("pricing_invariant")
	}
	return s, nil
}

func loadPricingSchema(ctx context.Context, tx pgx.Tx, id, digest string) (metering.Schema, error) {
	var raw, stored []byte
	if err := tx.QueryRow(ctx, `SELECT canonical,schema_digest FROM meter_schemas WHERE id=$1`, id).Scan(&raw, &stored); err != nil {
		return metering.Schema{}, err
	}
	return pricingSchema(raw, stored, id, digest)
}

// Schema/rate parsing is against the immutable original, never today's model.
// JSONB may reorder object keys; the metering parser preserves array ordering.
func validatePriceRates(raw *json.RawMessage, schema metering.Schema, side metering.Side) error {
	rates, err := metering.ParseRates(schema, side, *raw)
	if err != nil {
		return pricingFail("pricing_invariant")
	}
	dims, _ := schema.Dimensions()
	entries, _ := rates.Entries()
	if len(dims) != 2 || len(entries) != 2 || dims[0].Dimension != "text_input_token" || dims[1].Dimension != "text_output_token" || dims[1].MaximumQuantity == 0 && entries[1].Numerator != 0 {
		return pricingFail("pricing_invariant")
	}
	bounds, err := metering.ManagementTokenBounds(schema, side, dims[0].MaximumQuantity, dims[1].MaximumQuantity)
	if err != nil {
		return pricingFail("pricing_invariant")
	}
	if _, err = metering.ReservationAmount(bounds, rates); err != nil {
		return pricingFail("pricing_invariant")
	}
	*raw, _ = rates.CanonicalJSON()
	return nil
}

func validateGroupPriceSchema(v *GroupPriceView, schema metering.Schema) error {
	return validatePriceRates(&v.Rates, schema, metering.Retail)
}

func validatePublishedRates(ctx context.Context, tx pgx.Tx, id, digest string, side metering.Side, rates *json.RawMessage) error {
	schema, err := loadPricingSchema(ctx, tx, id, digest)
	if err != nil {
		if err == pgx.ErrNoRows {
			return pricingFail("pricing_invariant")
		}
		return err
	}
	if side == metering.Supplier && string(*rates) == "null" {
		return nil
	}
	return validatePriceRates(rates, schema, side)
}

func groupPriceSafe(c GroupPriceCommand, rates json.RawMessage) map[string]any {
	return map[string]any{"model_id": c.ModelID, "schema_id": c.SchemaID, "schema_digest": c.SchemaDigest, "variant_key": c.VariantKey, "currency": "COIN", "rates": rates, "effective_at": pricingStamp(c.EffectiveAt)}
}
