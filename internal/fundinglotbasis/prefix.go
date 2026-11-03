package fundinglotbasis

import (
	"context"
	_ "embed"
	"sync"
)

// PeriodicPrefixDescriptorSHA256 identifies the frozen 112-table descriptor.
// It does not identify a live schema or authenticate a transaction.
const PeriodicPrefixDescriptorSHA256 = "104914e6176e3d02a289e4e0acf0d476a69dcfb2e8772cf29336b520a7ec82bc"

//go:embed materials/descriptor-prefix-v1.json
var prefixDescriptorCanonical []byte

var prefixOnce sync.Once
var prefixMaterials materials
var prefixError error

func loadPrefixMaterials() (*materials, error) {
	prefixOnce.Do(func() {
		prefixMaterials, prefixError = parseFixedMaterials(descriptorMaterial{
			prefixDescriptorCanonical, PeriodicPrefixDescriptorSHA256,
			"fixed_112_table_prefix_basis_v1", 40655, 112, 1004, 131,
		})
	})
	if prefixError != nil {
		return nil, ErrMetadata
	}
	return &prefixMaterials, nil
}

// ReadPrefix enumerates the fixed legacy basis and periodic observation source
// columns through the same reader as Read. Facts remain descriptive and confer
// no transaction, role, installation, or financial authority.
func ReadPrefix(ctx context.Context, q Query, schema string) (Facts, error) {
	return readFixed(ctx, q, schema, loadPrefixMaterials)
}
