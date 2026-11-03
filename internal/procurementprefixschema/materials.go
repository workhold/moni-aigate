// Package procurementprefixschema provides isolated procurement relation SQL
// materials. It neither connects to a database nor authorizes a business writer.
package procurementprefixschema

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
	ErrMaterials  = errors.New("procurement_relation_materials_invalid")
	ErrIdentifier = errors.New("procurement_relation_schema_invalid")
)

// MaterialDigest identifies original, unrendered bytes in assembly order.
type MaterialDigest struct {
	Name   string
	SHA256 string
}

// These are reviewed bytes, never checksums discovered from a live database.
var pins = [...]MaterialDigest{
	{"tables-v1.sql", "fd849f1de5e21304b5c0f36b9a5f065e38be19d76efaee33d9ff338090f95fd6"},
	{"original-columns-v1.sql", "51758a5ec48436615a71d4ca3db60bcc35408fd8a7c3884aea39dff3b51c9e0a"},
	{"source-constraints-v1.sql", "4099f2303a39745d3112ca1a4e143bc884c356387a99e32da499396cfe9dda3a"},
	{"account-constraints-v1.sql", "fc990854a869952f54d212bc1e042c8b56991d8feff19827376b04c81b7862bf"},
	{"cost-constraints-v1.sql", "460c7d02c1e6dd791118cb2d5e1327a6efb39f74a969a46995fb2e1932f7e2ae"},
	{"original-constraints-v1.sql", "66c5d54de859c70873f426220933691c31740e5e63e9c8833ea21964f410f01b"},
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
	result := make([]material, len(pins))
	for i, pin := range pins {
		raw, err := files.ReadFile("materials/" + pin.Name)
		if err != nil || digest(string(raw)) != pin.SHA256 {
			return nil, ErrMaterials
		}
		result[i] = material{pin, string(raw)}
	}
	return result, nil
}

// Digests returns an owned inventory after verifying every embedded original.
func Digests() ([]MaterialDigest, error) {
	if _, err := originals(); err != nil {
		return nil, err
	}
	return append([]MaterialDigest(nil), pins[:]...), nil
}

// SQL quotes one explicit schema and renders the fixed material once. Tables
// precede every key, and all keys precede checks and deferred foreign keys.
// This is relation DDL only: no roots, validators, guards, triggers or grants.
func SQL(schema string) (string, error) {
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
	if len(materials) != len(pins) {
		return "", ErrMaterials
	}
	var output strings.Builder
	constraintPhases := make([][3]string, 0, len(pins)-2)
	for i, item := range materials {
		if item.pin != pins[i] || digest(item.body) != pins[i].SHA256 || !strings.Contains(item.body, "{{MONI_SCHEMA}}") {
			return "", ErrMaterials
		}
		if i < 2 {
			if strings.Contains(item.body, "-- @phase") {
				return "", ErrMaterials
			}
			output.WriteString(item.body)
			output.WriteByte('\n')
			continue
		}
		phases, err := splitPhases(item.body)
		if err != nil {
			return "", err
		}
		constraintPhases = append(constraintPhases, phases)
	}
	for phase := 0; phase < 3; phase++ {
		for _, item := range constraintPhases {
			output.WriteString(item[phase])
			output.WriteByte('\n')
		}
	}
	// ReplaceAll does not interpret placeholders appearing in the quoted input.
	return strings.ReplaceAll(output.String(), "{{MONI_SCHEMA}}", quotedSchema), nil
}

func splitPhases(body string) ([3]string, error) {
	var result [3]string
	markers := [...]string{"-- @phase keys\n", "-- @phase checks\n", "-- @phase foreign-keys\n"}
	if strings.Count(body, "-- @phase") != len(markers) {
		return result, ErrMaterials
	}
	last := -1
	var positions [3]int
	for i, marker := range markers {
		pos := strings.Index(body, marker)
		if strings.Count(body, marker) != 1 || pos <= last {
			return result, ErrMaterials
		}
		positions[i], last = pos, pos
	}
	for i, marker := range markers {
		end := len(body)
		if i+1 < len(markers) {
			end = positions[i+1]
		}
		result[i] = body[positions[i]+len(marker) : end]
	}
	return result, nil
}
