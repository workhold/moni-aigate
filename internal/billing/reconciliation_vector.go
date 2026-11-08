package billing

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/metering"
)

// canonicalRetailQuantities validates a customer evidence vector against the
// immutable schema captured by the request. It deliberately does not consult
// the current model profile, price, or bounds.
func canonicalRetailQuantities(ctx context.Context, tx pgx.Tx, r financialRequest, raw []byte) ([]byte, []byte, Usage, error) {
	invalid := func() ([]byte, []byte, Usage, error) {
		return nil, nil, Usage{}, fail("invalid_evidence", "complete retail quantities bound to the request schema are required")
	}
	if len(raw) == 0 {
		return invalid()
	}
	var schemaRaw, schemaDigest []byte
	if err := tx.QueryRow(ctx, `SELECT m.canonical,s.schema_digest
FROM request_meter_snapshots s
JOIN meter_schemas m ON m.id=s.schema_id AND m.schema_digest=s.schema_digest
WHERE s.tenant_id=$1 AND s.request_id=$2`, r.TenantID, r.ID).Scan(&schemaRaw, &schemaDigest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalid()
		}
		return nil, nil, Usage{}, err
	}
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		return invalid()
	}
	canonicalSchema, _ := schema.CanonicalJSON()
	digest, _ := schema.Digest()
	if !bytes.Equal(canonicalSchema, schemaRaw) || !bytes.Equal(digest[:], schemaDigest) {
		return invalid()
	}
	quantities, err := metering.ParseQuantities(schema, metering.Retail, raw)
	if err != nil {
		return invalid()
	}
	canonical, _ := quantities.CanonicalJSON()
	if !bytes.Equal(canonical, raw) {
		return invalid()
	}
	quantityDigest, err := quantities.Digest()
	if err != nil {
		return invalid()
	}
	entries, err := quantities.Entries()
	if err != nil || len(entries) != 2 || entries[0].Dimension != "text_input_token" || entries[1].Dimension != "text_output_token" || entries[0].Unit != "token" || entries[1].Unit != "token" {
		return invalid()
	}
	dimensions, err := schema.Dimensions()
	if err != nil || len(dimensions) != len(entries) {
		return invalid()
	}
	for i, entry := range entries {
		if entry.Quantity > dimensions[i].MaximumQuantity {
			return invalid()
		}
	}
	total, err := checkedAdd(entries[0].Quantity, entries[1].Quantity)
	if err != nil {
		return nil, nil, Usage{}, fail("invalid_evidence", "retail quantities exceed the supported total")
	}
	return canonical, quantityDigest[:], Usage{Status: "confirmed_final", InputTokens: entries[0].Quantity, OutputTokens: entries[1].Quantity, TotalTokens: total}, nil
}
