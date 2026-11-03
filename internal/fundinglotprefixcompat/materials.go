// Package fundinglotprefixcompat compares ordinary historical tuple data to
// fixed reviewed V3 mappings. It issues no database or funding authority.
package fundinglotprefixcompat

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"

	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/migrations"
)

const materialSHA = "9e35abdac16b9a1c9df1e644f89f9c0cf52d3f686b83c90a1623b600fbd1dd9c"
const legacyDescriptor = "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"
const manifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"

var ErrMaterial = errors.New("periodic_prefix_compatibility_failed")

//go:embed expected/compatibility-v1.json
var material []byte

type Tuple struct {
	Expected string `json:"expected"`
	Schema   string `json:"schema"`
	Roles    string `json:"roles"`
}
type Record struct {
	Mode              string `json:"mode"`
	Variant           string `json:"variant"`
	HistoricalProfile string `json:"historical_profile"`
	BasisDescriptor   string `json:"basis_descriptor"`
	Historical        Tuple  `json:"historical"`
	Current           Tuple  `json:"current"`
}
type materials struct {
	FormatVersion     uint32   `json:"format_version"`
	PostgresMajor     uint32   `json:"postgres_major"`
	DescriptorVersion uint32   `json:"descriptor_version"`
	BasisVersion      uint32   `json:"basis_version"`
	CurrentProfile    string   `json:"current_profile"`
	ManifestDigest    string   `json:"manifest_digest"`
	Mappings          []Record `json:"mappings"`
	manifest          []fundinglotproof.Migration
}

var cache struct {
	sync.Once
	value *materials
	err   error
}

func digest(raw []byte) string {
	x := sha256.Sum256(raw)
	return hex.EncodeToString(x[:])
}
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}
func validTuple(t Tuple) bool {
	return validDigest(t.Expected) && validDigest(t.Schema) && validDigest(t.Roles)
}

func decode(raw []byte) (*materials, error) {
	if len(raw) != 9711 || digest(raw) != materialSHA {
		return nil, ErrMaterial
	}
	var out materials
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || out.FormatVersion != 1 || out.PostgresMajor != 16 ||
		out.DescriptorVersion != 1 || out.BasisVersion != 1 || out.CurrentProfile != "PeriodicPrefixV3" || out.ManifestDigest != manifestSHA || len(out.Mappings) != 12 {
		return nil, ErrMaterial
	}
	manifest, err := migrations.Original22Manifest()
	if err != nil || digest(manifest) != manifestSHA || json.Unmarshal(manifest, &out.manifest) != nil || len(out.manifest) != 22 {
		return nil, ErrMaterial
	}
	seen := map[[3]string]bool{}
	for _, r := range out.Mappings {
		key := [3]string{r.Mode, r.Variant, r.HistoricalProfile}
		if seen[key] || r.Mode != "ordinary" && r.Mode != "sandbox" || r.Variant == "" || !validTuple(r.Historical) || !validTuple(r.Current) {
			return nil, ErrMaterial
		}
		seen[key] = true
		switch r.HistoricalProfile {
		case "LegacyV1":
			if r.BasisDescriptor != legacyDescriptor {
				return nil, ErrMaterial
			}
		case "PeriodicPrefixV3":
			if r.BasisDescriptor != fundinglotbasis.PeriodicPrefixDescriptorSHA256 || r.Historical != r.Current {
				return nil, ErrMaterial
			}
		default:
			return nil, ErrMaterial
		}
	}
	return &out, nil
}
func load() (*materials, error) {
	cache.Do(func() { cache.value, cache.err = decode(material) })
	return cache.value, cache.err
}

// Match requires exact fixed history, descriptor and complete tuple pairs.
// The caller must authenticate current and the original transaction separately.
func Match(b fundinglotproof.V2Bindings, currentVersion uint32, current Tuple) (Record, error) {
	m, err := load()
	if err != nil || m == nil || currentVersion != m.DescriptorVersion || b.CatalogDescriptorVersion != m.DescriptorVersion ||
		b.CatalogPostgresMajor != m.PostgresMajor || b.BasisVersion != m.BasisVersion || b.ManifestDigest != manifestSHA || !reflect.DeepEqual(b.MigrationManifest, m.manifest) {
		return Record{}, ErrMaterial
	}
	return MatchTuples(b.Mode, b.BasisDescriptorDigest, Tuple{b.CatalogExpectedDigest, b.CatalogSchemaDigest, b.CatalogRolesDigest}, current)
}

// MatchTuples returns only a copy of a fixed static mapping, never a proof.
func MatchTuples(mode, descriptor string, historical, current Tuple) (Record, error) {
	m, err := load()
	if err != nil || m == nil {
		return Record{}, ErrMaterial
	}
	var matched *Record
	for i := range m.Mappings {
		r := &m.Mappings[i]
		if r.Mode == mode && r.BasisDescriptor == descriptor && r.Historical == historical && r.Current == current {
			if matched != nil {
				return Record{}, ErrMaterial
			}
			matched = r
		}
	}
	if matched == nil {
		return Record{}, ErrMaterial
	}
	return *matched, nil
}

func init() { _, _ = load() }
