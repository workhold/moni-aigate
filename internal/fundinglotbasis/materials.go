package fundinglotbasis

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"sync"
)

const descriptorSHA = "54f701af2f993fd9dd6fb6db21f2cc323704ab6d2e962610413d044fdaa0c4e5"
const manifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"
const sendSHA = "1bd800f0014a159d1528e716275263c7b6c97e6fb91a037d034e1fbbe028684b"

//go:embed materials/descriptor-v1.json
var descriptorCanonical []byte

//go:embed materials/manifest-v1.json
var manifestCanonical []byte

//go:embed materials/send-v1.json
var sendCanonical []byte

type column struct {
	ordinal   int32
	name      string
	oid       uint32
	typmod    int32
	notNull   bool
	collation uint32
}
type table struct {
	name        string
	columns     []column
	primaryKey  []string
	pkColumns   []column
	pkIndexes   []int
	pkCanonical []byte
}
type manifestEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type typeMetadata struct {
	OID          uint32 `json:"oid"`
	Name         string `json:"name"`
	NamespaceOID uint32 `json:"namespace_oid"`
	OwnerOID     uint32 `json:"owner_oid"`
	Kind         string `json:"kind"`
	Defined      bool   `json:"defined"`
	Length       int32  `json:"length"`
	ByValue      bool   `json:"by_value"`
	Alignment    string `json:"alignment"`
	Storage      string `json:"storage"`
	ElementOID   uint32 `json:"element_oid"`
	BaseOID      uint32 `json:"base_oid"`
	RelationOID  uint32 `json:"relation_oid"`
	CollationOID uint32 `json:"collation_oid"`
	SendOID      uint32 `json:"send_oid"`
}
type functionMetadata struct {
	OID                 uint32   `json:"oid"`
	Name                string   `json:"name"`
	NamespaceOID        uint32   `json:"namespace_oid"`
	OwnerOID            uint32   `json:"owner_oid"`
	LanguageOID         uint32   `json:"language_oid"`
	Kind                string   `json:"kind"`
	SecurityDefiner     bool     `json:"security_definer"`
	Leakproof           bool     `json:"leakproof"`
	Strict              bool     `json:"strict"`
	ReturnsSet          bool     `json:"returns_set"`
	Volatility          string   `json:"volatility"`
	Parallel            string   `json:"parallel"`
	CostText            string   `json:"cost_text"`
	RowsText            string   `json:"rows_text"`
	VariadicOID         uint32   `json:"variadic_oid"`
	SupportOID          uint32   `json:"support_oid"`
	ArgumentCount       int32    `json:"argument_count"`
	DefaultCount        int32    `json:"default_count"`
	ResultOID           uint32   `json:"result_oid"`
	ArgumentTypeOIDs    []uint32 `json:"argument_type_oids"`
	AllArgumentTypeOIDs any      `json:"all_argument_type_oids"`
	ArgumentModes       any      `json:"argument_modes"`
	ArgumentNames       any      `json:"argument_names"`
	ArgumentDefaults    any      `json:"argument_defaults"`
	TransformTypeOIDs   any      `json:"transform_type_oids"`
	SourceSymbol        string   `json:"source_symbol"`
	Binary              any      `json:"binary"`
	SQLBody             any      `json:"sql_body"`
	Config              any      `json:"config"`
	ACL                 any      `json:"acl"`
}
type sendProfile struct {
	FormatVersion int                `json:"format_version"`
	Types         []typeMetadata     `json:"types"`
	Functions     []functionMetadata `json:"functions"`
}
type materials struct {
	tables   []table
	manifest []manifestEntry
	send     sendProfile
}

var fixedOnce sync.Once
var fixedMaterials materials
var fixedError error

func sha(raw []byte) string { x := sha256.Sum256(raw); return hex.EncodeToString(x[:]) }
func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrMetadata
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrMetadata
	}
	return nil
}
func loadMaterials() (*materials, error) {
	fixedOnce.Do(func() { fixedMaterials, fixedError = parseMaterials() })
	if fixedError != nil {
		return nil, ErrMetadata
	}
	return &fixedMaterials, nil
}
func parseMaterials() (materials, error) {
	var out materials
	if len(descriptorCanonical) != 39415 || sha(descriptorCanonical) != descriptorSHA || len(manifestCanonical) != 2459 || sha(manifestCanonical) != manifestSHA || len(sendCanonical) != 12360 || sha(sendCanonical) != sendSHA {
		return out, ErrMetadata
	}
	var raw []struct {
		Table      string              `json:"table"`
		PrimaryKey []string            `json:"primary_key"`
		Columns    [][]json.RawMessage `json:"columns"`
	}
	if decode(descriptorCanonical, &raw) != nil || len(raw) != 109 || decode(manifestCanonical, &out.manifest) != nil || len(out.manifest) != 22 || decode(sendCanonical, &out.send) != nil || out.send.FormatVersion != 1 || len(out.send.Types) != 15 || len(out.send.Functions) != 16 {
		return out, ErrMetadata
	}
	count, pks := 0, 0
	for i, r := range raw {
		if r.Table == "" || i > 0 && raw[i-1].Table >= r.Table || len(r.PrimaryKey) < 1 || len(r.PrimaryKey) > 4 {
			return materials{}, ErrMetadata
		}
		t := table{name: r.Table, primaryKey: append([]string(nil), r.PrimaryKey...)}
		for j, fields := range r.Columns {
			if len(fields) != 6 {
				return materials{}, ErrMetadata
			}
			var c column
			for k, d := range []any{&c.ordinal, &c.name, &c.oid, &c.typmod, &c.notNull, &c.collation} {
				if decode(fields[k], d) != nil {
					return materials{}, ErrMetadata
				}
			}
			if c.ordinal != int32(j+1) || c.name == "" || c.oid == 0 {
				return materials{}, ErrMetadata
			}
			t.columns = append(t.columns, c)
		}
		for _, pk := range r.PrimaryKey {
			found := false
			for j, c := range t.columns {
				if c.name == pk {
					if !c.notNull {
						return materials{}, ErrMetadata
					}
					t.pkColumns = append(t.pkColumns, c)
					t.pkIndexes = append(t.pkIndexes, j)
					found = true
					break
				}
			}
			if !found {
				return materials{}, ErrMetadata
			}
		}
		t.pkCanonical, _ = json.Marshal(r.PrimaryKey)
		count += len(t.columns)
		pks += len(t.pkColumns)
		out.tables = append(out.tables, t)
	}
	if count != 976 || pks != 128 {
		return materials{}, ErrMetadata
	}
	return out, nil
}
