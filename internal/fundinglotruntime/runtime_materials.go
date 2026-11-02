package fundinglotruntime

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"reflect"
	"sort"
	"sync"

	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/migrations"
)

const compatibilityMaterialSHA = "5add8e6ffde8f792547aff8b205b0c34b6ea6ec13ac19f2ab823902e8a987f4b"
const currentManifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"
const basisDescriptorSHA = "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"

//go:embed expected/compatibility-v1.json
var compatibilityMaterial []byte

type materialTuple struct {
	Expected string `json:"expected"`
	Schema   string `json:"schema"`
	Roles    string `json:"roles"`
}
type compatibilityRecord struct {
	Mode       string        `json:"mode"`
	Variant    string        `json:"variant"`
	Historical materialTuple `json:"historical"`
	Current    materialTuple `json:"current"`
}
type runtimeMaterials struct {
	FormatVersion     int                         `json:"format_version"`
	PostgresMajor     uint32                      `json:"postgres_major"`
	DescriptorVersion uint32                      `json:"descriptor_version"`
	BasisVersion      uint32                      `json:"basis_version"`
	BasisDescriptor   string                      `json:"basis_descriptor"`
	HistoricalProfile string                      `json:"historical_profile"`
	CurrentProfile    string                      `json:"current_profile"`
	ManifestDigest    string                      `json:"manifest_digest"`
	ManifestBytes     int                         `json:"manifest_bytes"`
	MaxNameBytes      int                         `json:"max_name_bytes"`
	Manifest          []fundinglotproof.Migration `json:"manifest"`
	Mappings          []compatibilityRecord       `json:"mappings"`
}

var materialCache struct {
	sync.Once
	value *runtimeMaterials
	err   error
}

func runtimeSHA(raw []byte) string {
	s := sha256.Sum256(raw)
	return hex.EncodeToString(s[:])
}

// All values are authenticated static bytes, not observations of a live DB.
// The private result remains immutable and is never returned by a public API.
func loadMaterials() (*runtimeMaterials, error) {
	materialCache.Do(func() {
		materialCache.value, materialCache.err = decodeMaterials(compatibilityMaterial)
	})
	return materialCache.value, materialCache.err
}

func decodeMaterials(raw []byte) (*runtimeMaterials, error) {
	if runtimeSHA(raw) != compatibilityMaterialSHA {
		return nil, ErrRuntime
	}
	var m runtimeMaterials
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF || m.FormatVersion != 1 || m.PostgresMajor != 16 ||
		m.DescriptorVersion != 1 || m.BasisVersion != 1 || m.BasisDescriptor != basisDescriptorSHA ||
		m.HistoricalProfile != "LegacyV1" || m.CurrentProfile != "WalletReadV2" || m.ManifestDigest != currentManifestSHA ||
		m.ManifestBytes != 2459 || m.MaxNameBytes != 35 || len(m.Manifest) != 22 || len(m.Mappings) != 6 {
		return nil, ErrRuntime
	}
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil || len(names) != 22 {
		return nil, ErrRuntime
	}
	sort.Strings(names)
	maxName := 0
	for i, name := range names {
		body, err := migrations.Files.ReadFile(name)
		if err != nil || m.Manifest[i] != (fundinglotproof.Migration{Name: name, SHA256: runtimeSHA(body)}) {
			return nil, ErrRuntime
		}
		maxName = max(maxName, len(name))
	}
	manifest, err := json.Marshal(m.Manifest)
	if err != nil || len(manifest) != m.ManifestBytes || runtimeSHA(manifest) != m.ManifestDigest || maxName != m.MaxNameBytes {
		return nil, ErrRuntime
	}
	return &m, nil
}

type currentCatalog struct {
	profile fundinglotcatalog.Profile
	version uint32
	tuple   materialTuple
}

func catalogDescription(c *fundinglotcatalog.CatalogProof) currentCatalog {
	return currentCatalog{profile: c.Profile(), version: c.DescriptorVersion(), tuple: materialTuple{
		Expected: c.ExpectedSHA256(), Schema: c.SchemaSHA256(), Roles: c.RolesSHA256(),
	}}
}

// This comparison returns ordinary static data, never a runtime capability.
func matchCompatibility(b fundinglotproof.V2Bindings, current currentCatalog) (compatibilityRecord, error) {
	m, err := loadMaterials()
	if err != nil || m == nil || current.profile != fundinglotcatalog.WalletReadV2 || current.version != m.DescriptorVersion ||
		b.CatalogDescriptorVersion != m.DescriptorVersion || b.CatalogPostgresMajor != m.PostgresMajor ||
		b.BasisVersion != m.BasisVersion || b.BasisDescriptorDigest != m.BasisDescriptor ||
		b.ManifestDigest != m.ManifestDigest || !reflect.DeepEqual(b.MigrationManifest, m.Manifest) {
		return compatibilityRecord{}, ErrRuntime
	}
	historical := materialTuple{Expected: b.CatalogExpectedDigest, Schema: b.CatalogSchemaDigest, Roles: b.CatalogRolesDigest}
	var matched *compatibilityRecord
	for i := range m.Mappings {
		r := &m.Mappings[i]
		if r.Mode == b.Mode && r.Historical == historical && r.Current == current.tuple {
			if matched != nil {
				return compatibilityRecord{}, ErrRuntime
			}
			matched = r
		}
	}
	if matched == nil {
		return compatibilityRecord{}, ErrRuntime
	}
	return *matched, nil
}

func init() {
	// Fixed JSON and embedded migration work is outside the issuance window.
	_, _ = loadMaterials()
}
