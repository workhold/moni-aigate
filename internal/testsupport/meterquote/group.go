package meterquote

import (
	"context"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/catalog"
)

// BindGroup is only for current 024 fixtures. Historical prefix fixtures must
// retain their old ABI and cannot use this helper to opt into the new schema.
func BindGroup(t testing.TB, ctx context.Context, db Querier, c catalog.GroupPriceCommand) catalog.GroupPriceCommand {
	t.Helper()
	if err := db.QueryRow(ctx, `SELECT p.schema_id,encode(p.schema_digest,'hex'),'default' FROM models m JOIN model_capability_versions p ON p.id=m.profile_version AND p.model_id=m.id WHERE m.id=$1`, c.ModelID).Scan(&c.SchemaID, &c.SchemaDigest, &c.VariantKey); err != nil {
		t.Fatal(err)
	}
	return c
}
