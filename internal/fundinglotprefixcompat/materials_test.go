package fundinglotprefixcompat

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
)

func bindingsFor(r Record, m *materials) fundinglotproof.V2Bindings {
	return fundinglotproof.V2Bindings{
		Mode: r.Mode, BasisVersion: 1, BasisDescriptorDigest: r.BasisDescriptor,
		ManifestDigest: manifestSHA, MigrationManifest: append([]fundinglotproof.Migration(nil), m.manifest...),
		CatalogDescriptorVersion: 1, CatalogPostgresMajor: 16,
		CatalogExpectedDigest: r.Historical.Expected, CatalogSchemaDigest: r.Historical.Schema, CatalogRolesDigest: r.Historical.Roles,
	}
}

func TestFixedTwelveCompatibilityRoutes(t *testing.T) {
	m, err := load()
	if err != nil || m == nil || len(m.Mappings) != 12 {
		t.Fatal("fixed compatibility material unavailable")
	}
	for _, r := range m.Mappings {
		t.Run(r.Mode+"/"+r.Variant+"/"+r.HistoricalProfile, func(t *testing.T) {
			b := bindingsFor(r, m)
			got, err := Match(b, 1, r.Current)
			if err != nil || got != r {
				t.Fatal("fixed route rejected")
			}
			got.Mode, got.Current.Expected = "changed", "changed"
			if again, err := Match(b, 1, r.Current); err != nil || again != r {
				t.Fatal("returned data aliases material")
			}
			for _, mutate := range []func(*fundinglotproof.V2Bindings){
				func(b *fundinglotproof.V2Bindings) { b.Mode = "foreign" },
				func(b *fundinglotproof.V2Bindings) { b.BasisVersion++ },
				func(b *fundinglotproof.V2Bindings) { b.BasisDescriptorDigest = "foreign" },
				func(b *fundinglotproof.V2Bindings) { b.ManifestDigest = "foreign" },
				func(b *fundinglotproof.V2Bindings) { b.MigrationManifest = b.MigrationManifest[:21] },
				func(b *fundinglotproof.V2Bindings) { b.MigrationManifest[0].SHA256 = "foreign" },
				func(b *fundinglotproof.V2Bindings) {
					b.MigrationManifest[0], b.MigrationManifest[1] = b.MigrationManifest[1], b.MigrationManifest[0]
				},
				func(b *fundinglotproof.V2Bindings) { b.CatalogDescriptorVersion++ },
				func(b *fundinglotproof.V2Bindings) { b.CatalogPostgresMajor++ },
				func(b *fundinglotproof.V2Bindings) { b.CatalogExpectedDigest = "foreign" },
				func(b *fundinglotproof.V2Bindings) { b.CatalogSchemaDigest = "foreign" },
				func(b *fundinglotproof.V2Bindings) { b.CatalogRolesDigest = "foreign" },
			} {
				changed := bindingsFor(r, m)
				mutate(&changed)
				if got, err := Match(changed, 1, r.Current); err != ErrMaterial || got != (Record{}) {
					t.Fatal("foreign history/descriptor/tuple accepted or partial result returned")
				}
			}
			if got, err := Match(b, 2, r.Current); err != ErrMaterial || got != (Record{}) {
				t.Fatal("foreign current descriptor version accepted")
			}
			for _, other := range m.Mappings {
				if other.Current == r.Current {
					continue
				}
				if got, err := Match(b, 1, other.Current); err != ErrMaterial || got != (Record{}) {
					t.Fatal("cross-mode/variant mapping accepted")
				}
			}
		})
	}
}

func TestCompatibilityDerivedFromIndependentSourceArtifacts(t *testing.T) {
	// These files are separately reviewed static sources. No live catalog or
	// the loader's mapping table is used as the expected tuple source here.
	raw, err := os.ReadFile("../fundinglotruntime/expected/compatibility-v1.json")
	if err != nil || digest(raw) != "5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b" {
		t.Fatal("original historical mapping changed")
	}
	var old struct {
		Mappings []struct {
			Mode, Variant string
			Historical    Tuple
		} `json:"mappings"`
	}
	if json.Unmarshal(raw, &old) != nil || len(old.Mappings) != 6 {
		t.Fatal("original source decode")
	}
	raw, err = os.ReadFile("../../docs/verification/periodic-prefix-profile-roots.json")
	var roots map[string]struct {
		Expected string                                    `json:"expected_sha256"`
		Variants map[string]struct{ Schema, Roles string } `json:"variants"`
	}
	if err != nil || json.Unmarshal(raw, &roots) != nil {
		t.Fatal("independent static roots unavailable")
	}
	m, err := load()
	if err != nil {
		t.Fatal(err)
	}
	for i, origin := range old.Mappings {
		current := roots[origin.Mode]
		v := current.Variants[origin.Variant]
		tuple := Tuple{current.Expected, v.Schema, v.Roles}
		if m.Mappings[2*i].Historical != origin.Historical || m.Mappings[2*i].Current != tuple || m.Mappings[2*i+1].Historical != tuple || m.Mappings[2*i+1].Current != tuple {
			t.Fatal("mapping not derived from exact historical and reviewed current tuple")
		}
	}
}

func TestCompatibilityMaterialRejectsChangedBytesAndConcurrentCopies(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, append(bytes.Clone(material), '\n'), bytes.Replace(material, []byte("LegacyV1"), []byte("WalletV2"), 1)} {
		if got, err := decode(raw); got != nil || err != ErrMaterial {
			t.Fatal("changed material accepted")
		}
	}
	m, err := load()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, record := range m.Mappings {
		wg.Add(1)
		go func(r Record) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				got, e := Match(bindingsFor(r, m), 1, r.Current)
				if e != nil || !reflect.DeepEqual(got, r) {
					t.Error("concurrent immutable lookup changed")
				}
			}
		}(record)
	}
	wg.Wait()
}
