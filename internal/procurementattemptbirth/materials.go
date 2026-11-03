// Package procurementattemptbirth renders the isolated, prospective
// birth-exact-link-a2-v1 candidate. It does not connect to PostgreSQL, install a
// serving profile, change ordinary billing.New, or issue runtime authority.
// Original 001-022, B-P1, identity and A0 are separate prerequisites.
package procurementattemptbirth

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

//go:embed materials/*.sql materials/*.json
var files embed.FS

const Candidate = "birth-exact-link-a2-v1"

var (
	ErrBindings  = errors.New("procurement_attempt_birth_bindings_invalid")
	ErrMaterials = errors.New("procurement_attempt_birth_materials_invalid")
)

// Bindings is trusted installation input, never business root input. Rendering
// authenticates template bytes and quoting; catalog/role verification remains a
// separate installation obligation.
type Bindings struct {
	Schema    string
	App       string
	Writer    string
	AppOID    uint32
	WriterOID uint32
}

// MaterialDigest identifies exact, unrendered reviewed bytes in assembly order.
type MaterialDigest struct {
	Name   string
	SHA256 string
}

func validIdentifier(v string) bool {
	return v != "" && len(v) <= 63 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
}
func validBindings(b Bindings) bool {
	return validIdentifier(b.Schema) && validIdentifier(b.App) && validIdentifier(b.Writer) && b.App != b.Writer && b.AppOID != 0 && b.WriterOID != 0 && b.AppOID != b.WriterOID
}
func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

func originals() (map[string][]byte, error) {
	entries, err := files.ReadDir("materials")
	if err != nil || len(entries) != len(materialPins) {
		return nil, ErrMaterials
	}
	result := make(map[string][]byte, len(materialPins))
	for _, pin := range materialPins {
		b, e := files.ReadFile("materials/" + pin.Name)
		if e != nil || digest(b) != pin.SHA256 {
			return nil, ErrMaterials
		}
		if _, exists := result[pin.Name]; exists {
			return nil, ErrMaterials
		}
		result[pin.Name] = b
	}
	return result, nil
}

func Digests() ([]MaterialDigest, error) {
	if _, err := originals(); err != nil {
		return nil, err
	}
	return append([]MaterialDigest(nil), materialPins[:]...), nil
}

func render(body string, b Bindings) string {
	// Dollar quotes are parsed before quoted SQL identifiers. Pick a tag absent
	// from every inserted name, before a single nonrecursive token replacement.
	for n := uint64(0); ; n++ {
		tag := "$moni_ab_" + strconv.FormatUint(n, 10) + "$"
		if !strings.Contains(b.Schema, tag) && !strings.Contains(b.App, tag) && !strings.Contains(b.Writer, tag) {
			body = strings.ReplaceAll(body, "$$", tag)
			break
		}
	}
	literal := func(v string) string {
		return "pg_catalog.convert_from(pg_catalog.decode('" + hex.EncodeToString([]byte(v)) + "','hex'),'UTF8')"
	}
	return strings.NewReplacer(
		"{{MONI_SCHEMA}}", pgx.Identifier{b.Schema}.Sanitize(),
		"{{MONI_SCHEMA_TEXT}}", literal(b.Schema),
		"{{MONI_APP}}", pgx.Identifier{b.App}.Sanitize(),
		"{{MONI_APP_TEXT}}", literal(b.App),
		"{{MONI_WRITER}}", pgx.Identifier{b.Writer}.Sanitize(),
		"{{MONI_WRITER_TEXT}}", literal(b.Writer),
		"{{MONI_APP_OID}}", strconv.FormatUint(uint64(b.AppOID), 10),
		"{{MONI_WRITER_OID}}", strconv.FormatUint(uint64(b.WriterOID), 10),
	).Replace(body)
}

// SQL renders the complete relations, fixed functions and 68 hooks. The caller
// must install SQL and Permissions atomically and verify the actual catalog
// before COMMIT. This method never discovers or mutates a database.
func SQL(b Bindings) (string, error) {
	if !validBindings(b) {
		return "", ErrBindings
	}
	raw, err := originals()
	if err != nil {
		return "", err
	}
	var result strings.Builder
	for _, pin := range materialPins {
		if strings.HasSuffix(pin.Name, ".sql") && pin.Name != "permissions-v1.sql" {
			result.Write(raw[pin.Name])
			result.WriteByte('\n')
		}
	}
	return render(result.String(), b), nil
}

// Permissions renders only the exact A2 ownership and ACL changes. It must be
// applied after SQL in the same owner-controlled installation transaction.
func Permissions(b Bindings) (string, error) {
	if !validBindings(b) {
		return "", ErrBindings
	}
	raw, err := originals()
	if err != nil {
		return "", err
	}
	return render(string(raw["permissions-v1.sql"]), b), nil
}

// RelationDescriptor returns static expected DDL, never a live catalog proof.
func RelationDescriptor() ([]byte, error) {
	r, e := originals()
	if e != nil {
		return nil, e
	}
	return append([]byte(nil), r["relation-descriptor-v1.json"]...), nil
}

// CatalogDescriptor returns static function/trigger expectations. There is no
// public Check, Qualified/profile constructor, or automatic provisioning here.
func CatalogDescriptor() ([]byte, error) {
	r, e := originals()
	if e != nil {
		return nil, e
	}
	return append([]byte(nil), r["catalog-v1.json"]...), nil
}
