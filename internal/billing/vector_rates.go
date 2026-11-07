package billing

import (
	"bytes"

	"github.com/moni-ai/moni-aigate/internal/metering"
)

// Original immutable numerical facts must match their canonical bytes and
// complete side binding. A successful parse alone does not prove storage form.
func canonicalMeterRates(schemaRaw, schemaDigest, ratesRaw, ratesDigest []byte, side metering.Side) (metering.Schema, metering.Rates, error) {
	invalid := func() (metering.Schema, metering.Rates, error) {
		return metering.Schema{}, metering.Rates{}, fail("invalid_meter_snapshot", "original meter canonical or binding is invalid")
	}
	schema, err := metering.ParseSchema(schemaRaw)
	if err != nil {
		return invalid()
	}
	canonical, _ := schema.CanonicalJSON()
	digest, _ := schema.Digest()
	if !bytes.Equal(canonical, schemaRaw) || !bytes.Equal(digest[:], schemaDigest) {
		return invalid()
	}
	rates, err := metering.ParseRates(schema, side, ratesRaw)
	if err != nil {
		return invalid()
	}
	canonical, _ = rates.CanonicalJSON()
	digest, _ = rates.Digest()
	if !bytes.Equal(canonical, ratesRaw) || !bytes.Equal(digest[:], ratesDigest) {
		return invalid()
	}
	return schema, rates, nil
}
