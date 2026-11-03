// Package procurementbirthschema renders an isolated A1 request-birth relation
// slice. It does not install a writer, guards, roles, or runtime permissions.
package procurementbirthschema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

//go:embed materials/*.sql
var files embed.FS

var (
	ErrMaterials  = errors.New("procurement_birth_materials_invalid")
	ErrIdentifier = errors.New("procurement_birth_schema_invalid")
)

// MaterialDigest identifies reviewed, unrendered bytes in assembly order.
type MaterialDigest struct {
	Name   string
	SHA256 string
}

// These pins describe this explicit slice, never the full relation candidate.
var a1Pins = [...]MaterialDigest{
	{"a1-tables-v1.sql", "483e429714616143337db75318a23a01b755a7585088879e2ec465d4aa6b0d74"},
	{"a1-original-columns-v1.sql", "2a0e056155a7947047bca5d1473ece29b575df34f2e6bf536d79704a8cfe08d7"},
	{"a1-constraints-v1.sql", "471931a538b4a304c5b0999fcacb5e8bb24ddcce48d2cd785cd88e96b2f087aa"},
}

type material struct {
	pin  MaterialDigest
	body string
}

func digest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func originals() ([]material, error) {
	result := make([]material, len(a1Pins))
	for i, pin := range a1Pins {
		raw, err := files.ReadFile("materials/" + pin.Name)
		if err != nil || digest(string(raw)) != pin.SHA256 {
			return nil, ErrMaterials
		}
		result[i] = material{pin: pin, body: string(raw)}
	}
	return result, nil
}

// A1Digests verifies every fixed original and returns an owned inventory.
func A1Digests() ([]MaterialDigest, error) {
	if _, err := originals(); err != nil {
		return nil, err
	}
	return append([]MaterialDigest(nil), a1Pins[:]...), nil
}

// A1SQL safely quotes one explicit schema and renders only four new tables,
// four nullable request columns, five keys, eight checks and ten deferred native
// locator foreign keys. The caller must separately install the actual birth
// writer, both-end exact guards, deferred validators and reviewed permissions.
func A1SQL(schema string) (string, error) {
	if schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) {
		return "", ErrIdentifier
	}
	originals, err := originals()
	if err != nil {
		return "", err
	}
	return assemble(originals, pgx.Identifier{schema}.Sanitize())
}

func assemble(materials []material, quotedSchema string) (string, error) {
	if len(materials) != len(a1Pins) {
		return "", ErrMaterials
	}
	var output strings.Builder
	for i, item := range materials {
		if item.pin != a1Pins[i] || digest(item.body) != a1Pins[i].SHA256 || !strings.Contains(item.body, "{{MONI_SCHEMA}}") {
			return "", ErrMaterials
		}
		output.WriteString(item.body)
		output.WriteByte('\n')
	}
	// A schema containing the placeholder is quoted, never recursively replaced.
	return strings.ReplaceAll(output.String(), "{{MONI_SCHEMA}}", quotedSchema), nil
}
