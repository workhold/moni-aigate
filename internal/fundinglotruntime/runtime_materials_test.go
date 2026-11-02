package fundinglotruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

// Frozen values are independent contract literals. The Python checker separately
// recomputes them from both complete original catalog documents and the exact
// four-column delta; these tests never derive an oracle through Go's matcher.
var materialExpectedRecords = []compatibilityRecord{
	{Mode: "ordinary", Variant: "named-isolated", Historical: materialTuple{
		Expected: "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "b032e7b1626df0cb1fc3160619d808fd382c966ccb66258a61cc3fb1e29b3609",
	}, Current: materialTuple{
		Expected: "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "57ed93278687cbac78081e4e29189d65f5218b0c02ce32e635826db7a1a34413",
	}},
	{Mode: "ordinary", Variant: "named-minimal-login", Historical: materialTuple{
		Expected: "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "d7d632a6e77082c79a5dce0e558fe46e9d37c02c4c63702371f4dd84fbd41b62",
	}, Current: materialTuple{
		Expected: "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "ee33f5673c7a7f93d2e950d58d0713112d67453778149f5fa9a7b3ef3266815f",
	}},
	{Mode: "ordinary", Variant: "named-image-login", Historical: materialTuple{
		Expected: "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "b1a1c15e35149789b1cba3ecb58197704b0e6c1a4c296d26efc0fbd33ee58f5f",
	}, Current: materialTuple{
		Expected: "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20",
		Schema:   "089382152edefa4195ecb0f247a46c56bb4b41aa325d30b74fc1ce92a8b01e24",
		Roles:    "bb99b7c11d267d7e3f0e5be62b36a5eda65638afefc3eab3936b8c60442da168",
	}},
	{Mode: "ordinary", Variant: "public-minimal-login", Historical: materialTuple{
		Expected: "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
		Schema:   "3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926",
		Roles:    "4c8037ece6c9a07569c922b31088188e30f593021413bad656ef36e356a2b644",
	}, Current: materialTuple{
		Expected: "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20",
		Schema:   "3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926",
		Roles:    "c3d7f7f4f3631153911c8867ae2d4d35eb5e4e45543836541ddbb2bf885aa3f1",
	}},
	{Mode: "ordinary", Variant: "public-image-login", Historical: materialTuple{
		Expected: "a25e8a070e0808854ea2c12fa6b006a72879555ccc9e45a151d4287d78feeff2",
		Schema:   "3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926",
		Roles:    "864d06014ceb1a5bc28bafcbf81b216dc0476abc2b2bf61fb380f7d2952173b3",
	}, Current: materialTuple{
		Expected: "c9466842df4a55490f53b7c0ba9694ee61b5fd5e011912cd2083f2ec191cdf20",
		Schema:   "3b694f847d07a7ddcdcfcac0f64459a65ce55f65bc938bc8700cad690e7ba926",
		Roles:    "d140513eecfbf603d78ae65a5d527d39b4054f50666c9e4229315dc3f12890d0",
	}},
	{Mode: "sandbox", Variant: "full-sandbox", Historical: materialTuple{
		Expected: "a3a7f855a5ac58ee583dce071c02707499c2a9e3d4bd2ec8b7b1b60228d55c4f",
		Schema:   "ed7698eb58d76621f18b7e6aae0000f262549a4b565d9a04e946c1436bff0b02",
		Roles:    "186b82132de40cf01a46b04dd7aace17248a73b53d52f5228f07d376fb11dfbf",
	}, Current: materialTuple{
		Expected: "42a63f7178748ea5dd4ce6964cbe9cd77d0131ca8dad795fbd61a4735b206706",
		Schema:   "ed7698eb58d76621f18b7e6aae0000f262549a4b565d9a04e946c1436bff0b02",
		Roles:    "28e771bd6f6b96817910252d21d17f09f9d4753f99841562a788f27d90622f4e",
	}},
}

func materialTestSHA(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func materialBindings(t *testing.T, record compatibilityRecord) fundinglotproof.V2Bindings {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "fundinglotproof", "testdata", record.Mode+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var b fundinglotproof.V2Input
	if json.Unmarshal(raw, &b) != nil {
		t.Fatal("invalid independently committed V2 fixture")
	}
	m, err := decodeMaterials(compatibilityMaterial)
	if err != nil {
		t.Fatal(err)
	}
	b.CatalogExpectedDigest, b.CatalogSchemaDigest, b.CatalogRolesDigest = record.Historical.Expected, record.Historical.Schema, record.Historical.Roles
	b.CatalogDescriptorVersion, b.CatalogPostgresMajor = 1, 16
	b.BasisVersion, b.BasisDescriptorDigest = 1, "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"
	b.MigrationManifest, b.ManifestDigest = append([]fundinglotproof.Migration(nil), m.Manifest...), "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"
	if _, err := fundinglotproof.EncodeV2(b); err != nil {
		t.Fatalf("positive complete descriptor must remain valid V2 format: %v", err)
	}
	return b
}

func materialCurrent(record compatibilityRecord) currentCatalog {
	return currentCatalog{profile: fundinglotcatalog.WalletReadV2, version: 1, tuple: record.Current}
}

func TestRuntimeMaterialExactBytesManifestAndSixMappings(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("expected", "compatibility-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 7374 || materialTestSHA(raw) != "5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b" || !bytes.Equal(raw, compatibilityMaterial) {
		t.Fatal("reviewed embedded artifact bytes changed")
	}
	m, err := decodeMaterials(raw)
	if err != nil || m == nil {
		t.Fatalf("reviewed materials rejected: %v", err)
	}
	if !reflect.DeepEqual(m.Mappings, materialExpectedRecords) || m.FormatVersion != 1 || m.PostgresMajor != 16 || m.DescriptorVersion != 1 || m.BasisVersion != 1 ||
		m.HistoricalProfile != "LegacyV1" || m.CurrentProfile != "WalletReadV2" || m.BasisDescriptor != "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5" {
		t.Fatal("complete frozen material or a mapping differs from independent literals")
	}
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(paths) != 22 || len(m.Manifest) != 22 {
		t.Fatal("current migration count drift")
	}
	sort.Strings(paths)
	maxName := 0
	for i, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		if m.Manifest[i] != (fundinglotproof.Migration{Name: name, SHA256: materialTestSHA(body)}) {
			t.Fatalf("manifest entry %d differs from actual source bytes", i)
		}
		maxName = max(maxName, len(name))
	}
	manifestRaw, err := json.Marshal(m.Manifest)
	if err != nil || len(manifestRaw) != 2459 || maxName != 35 || m.ManifestBytes != 2459 || m.MaxNameBytes != 35 ||
		materialTestSHA(manifestRaw) != "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de" || m.ManifestDigest != materialTestSHA(manifestRaw) {
		t.Fatal("full manifest bounds or digest changed")
	}
}

func TestRuntimeMaterialSHAFirstRejectsEveryAlteredArtifact(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte(`null`), []byte(`{}`), append(bytes.Clone(compatibilityMaterial), '\n'),
		bytes.Replace(compatibilityMaterial, []byte(`"format_version": 1`), []byte(`"format_version": 2`), 1),
		bytes.Replace(compatibilityMaterial, []byte(`"named-isolated"`), []byte(`"unknown"`), 1),
		bytes.Replace(compatibilityMaterial, []byte(`"mappings": [`), []byte(`"mappings": [], "mappings": [`), 1)} {
		m, err := decodeMaterials(raw)
		if m != nil || err != ErrRuntime {
			t.Fatal("altered/unreviewed artifact must fail before use")
		}
	}
	for i := range compatibilityMaterial {
		raw := bytes.Clone(compatibilityMaterial)
		raw[i] ^= 1
		if m, err := decodeMaterials(raw); m != nil || err != ErrRuntime {
			t.Fatalf("single byte mutation %d accepted", i)
		}
	}
}

func TestRuntimeCompatibilitySixUniqueFullTuples(t *testing.T) {
	for i, record := range materialExpectedRecords {
		t.Run(record.Variant, func(t *testing.T) {
			b := materialBindings(t, record)
			for j, current := range materialExpectedRecords {
				got, err := matchCompatibility(b, materialCurrent(current))
				if i == j {
					if err != nil || got != record {
						t.Fatal("complete historical/current tuple was not uniquely resolved")
					}
				} else if err != ErrRuntime || got != (compatibilityRecord{}) {
					t.Fatal("cross-variant or cross-mode tuple accepted")
				}
			}
		})
	}
}

func TestRuntimeCompatibilityRejectsKnownRootSplices(t *testing.T) {
	for i, record := range materialExpectedRecords {
		for j, other := range materialExpectedRecords {
			if i == j {
				continue
			}
			b := materialBindings(t, record)
			// Every replacement is a real reviewed roles digest, but it must
			// never authenticate with another variant's current roles tuple.
			b.CatalogRolesDigest = other.Historical.Roles
			if got, err := matchCompatibility(b, materialCurrent(record)); got != (compatibilityRecord{}) || err != ErrRuntime {
				t.Fatalf("historical roles from %s accepted as %s", other.Variant, record.Variant)
			}
			if record.Historical.Schema != other.Historical.Schema {
				b = materialBindings(t, record)
				b.CatalogSchemaDigest = other.Historical.Schema
				if got, err := matchCompatibility(b, materialCurrent(record)); got != (compatibilityRecord{}) || err != ErrRuntime {
					t.Fatal("historical schema/roles cross-splice accepted")
				}
			}
			if record.Current.Schema != other.Current.Schema {
				current := materialCurrent(record)
				current.tuple.Schema = other.Current.Schema
				if got, err := matchCompatibility(materialBindings(t, record), current); got != (compatibilityRecord{}) || err != ErrRuntime {
					t.Fatal("current schema/roles cross-splice accepted")
				}
			}
		}
	}
}

func TestRuntimeCompatibilityRejectsUnknownAndPartialBindings(t *testing.T) {
	for _, record := range materialExpectedRecords {
		t.Run(record.Variant, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				mutate func(*fundinglotproof.V2Bindings)
			}{
				{"unknown_mode", func(b *fundinglotproof.V2Bindings) { b.Mode = "unknown" }},
				{"opposite_mode", func(b *fundinglotproof.V2Bindings) {
					if b.Mode == "ordinary" {
						b.Mode = "sandbox"
					} else {
						b.Mode = "ordinary"
					}
				}},
				{"postgres_major", func(b *fundinglotproof.V2Bindings) { b.CatalogPostgresMajor = 17 }},
				{"descriptor_version", func(b *fundinglotproof.V2Bindings) { b.CatalogDescriptorVersion++ }},
				{"basis_version", func(b *fundinglotproof.V2Bindings) { b.BasisVersion++ }},
				{"basis_descriptor", func(b *fundinglotproof.V2Bindings) { b.BasisDescriptorDigest = strings.Repeat("0", 64) }},
				{"historical_root", func(b *fundinglotproof.V2Bindings) { b.CatalogExpectedDigest = strings.Repeat("0", 64) }},
				{"incomplete_sandbox_candidate", func(b *fundinglotproof.V2Bindings) {
					b.CatalogExpectedDigest = "e59039d07816d11d45ed42cc91c9f64928c93f67f2f77bab5e4c1451f9bc1fc9"
				}},
				{"schema_root", func(b *fundinglotproof.V2Bindings) { b.CatalogSchemaDigest = strings.Repeat("0", 64) }},
				{"roles_root", func(b *fundinglotproof.V2Bindings) { b.CatalogRolesDigest = strings.Repeat("0", 64) }},
				{"manifest_digest", func(b *fundinglotproof.V2Bindings) { b.ManifestDigest = strings.Repeat("0", 64) }},
				{"nil_manifest", func(b *fundinglotproof.V2Bindings) { b.MigrationManifest = nil }},
				{"historical_prefix", func(b *fundinglotproof.V2Bindings) { b.MigrationManifest = b.MigrationManifest[:21] }},
				{"extra_migration", func(b *fundinglotproof.V2Bindings) {
					b.MigrationManifest = append(b.MigrationManifest, fundinglotproof.Migration{Name: "023_future.sql", SHA256: strings.Repeat("a", 64)})
				}},
				{"migration_name", func(b *fundinglotproof.V2Bindings) { b.MigrationManifest[0].Name = "001_other.sql" }},
				{"migration_sha", func(b *fundinglotproof.V2Bindings) { b.MigrationManifest[0].SHA256 = strings.Repeat("a", 64) }},
				{"migration_order", func(b *fundinglotproof.V2Bindings) {
					b.MigrationManifest[0], b.MigrationManifest[1] = b.MigrationManifest[1], b.MigrationManifest[0]
				}},
				{"migration_duplicate", func(b *fundinglotproof.V2Bindings) { b.MigrationManifest[1] = b.MigrationManifest[0] }},
				{"self_consistent_unreviewed_manifest", func(b *fundinglotproof.V2Bindings) {
					b.MigrationManifest[0].SHA256 = strings.Repeat("a", 64)
					raw, err := json.Marshal(b.MigrationManifest)
					if err != nil {
						t.Fatal(err)
					}
					b.ManifestDigest = materialTestSHA(raw)
				}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					b := materialBindings(t, record)
					tc.mutate(&b)
					got, err := matchCompatibility(b, materialCurrent(record))
					if err != ErrRuntime || got != (compatibilityRecord{}) {
						t.Fatal("unknown or incomplete historical descriptor accepted")
					}
				})
			}
		})
	}
}

func TestRuntimeCompatibilityRejectsCurrentProfileAndTupleChanges(t *testing.T) {
	for _, record := range materialExpectedRecords {
		t.Run(record.Variant, func(t *testing.T) {
			for _, mutate := range []func(*currentCatalog){
				func(c *currentCatalog) { c.profile = fundinglotcatalog.LegacyV1 },
				func(c *currentCatalog) { c.profile = 0 },
				func(c *currentCatalog) { c.profile = fundinglotcatalog.Profile(255) },
				func(c *currentCatalog) { c.version++ },
				func(c *currentCatalog) { c.version = 0 },
				func(c *currentCatalog) { c.tuple.Expected = record.Historical.Expected },
				func(c *currentCatalog) { c.tuple.Expected = strings.Repeat("0", 64) },
				func(c *currentCatalog) { c.tuple.Schema = strings.Repeat("0", 64) },
				func(c *currentCatalog) { c.tuple.Roles = record.Historical.Roles },
				func(c *currentCatalog) { c.tuple.Roles = strings.Repeat("0", 64) },
			} {
				current := materialCurrent(record)
				mutate(&current)
				if got, err := matchCompatibility(materialBindings(t, record), current); got != (compatibilityRecord{}) || err != ErrRuntime {
					t.Fatal("unreviewed current profile/version/tuple accepted")
				}
			}
		})
	}
}

func TestRuntimeCompatibilityDoesNotReplaceBusinessOrIdentityBinding(t *testing.T) {
	for _, record := range materialExpectedRecords {
		b := materialBindings(t, record)
		// These are deliberately ordinary comparison values, never authenticated
		// pins or current-business basis. The composer separately binds identity
		// and the epoch; static compatibility must not enforce a random UUID/OID
		// or force historical basis to equal later business rows.
		b.BasisDigest = strings.Repeat("7", 64)
		b.EpochID = "aa3986d8-4d98-4e88-b8b7-d972b3ccfb33"
		b.Database.OID++
		if got, err := matchCompatibility(b, materialCurrent(record)); err != nil || got != record {
			t.Fatal("descriptive static compatibility incorrectly replaced runtime binding")
		}
	}
	if got := catalogDescription(nil); got != (currentCatalog{}) {
		t.Fatal("nil catalog description became a valid current tuple")
	}
}
