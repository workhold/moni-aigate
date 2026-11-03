package migrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Fixed historical material is independent of the current migration inventory.
// Its exact names select the original files; future files cannot extend it.
const original22ManifestSHA = "ba5e965dabdbee6469f0cfa45c26b244fe7bfecde11ce85ecb33fffd9c14b1de"
const original22ManifestBytes = 2459
const original22Count = 22
const original22MaxNameBytes = 35

//go:embed original22_manifest.json
var original22Canonical []byte

var (
	ErrOriginal22Manifest       = errors.New("original_22_migration_material_failed")
	ErrVerifyOriginal22Snapshot = errors.New("original_22_migration_snapshot_verification_failed")
)

type original22Entry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func original22Digest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// The private FS seam is only for material tests. No public API accepts a
// filesystem, target, manifest, or replacement historical checksum.
func original22Material(files fs.FS, canonical []byte) ([]original22Entry, error) {
	if original22Nil(files) || len(canonical) != original22ManifestBytes || original22Digest(canonical) != original22ManifestSHA {
		return nil, ErrOriginal22Manifest
	}
	var entries []original22Entry
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&entries) != nil || decoder.Decode(new(any)) != io.EOF || len(entries) != original22Count {
		return nil, ErrOriginal22Manifest
	}
	previous := ""
	for _, entry := range entries {
		if entry.Name <= previous || len(entry.Name) > original22MaxNameBytes || !fs.ValidPath(entry.Name) || strings.Contains(entry.Name, "/") || len(entry.SHA256) != 64 {
			return nil, ErrOriginal22Manifest
		}
		body, err := fs.ReadFile(files, entry.Name)
		if err != nil || original22Digest(body) != entry.SHA256 {
			return nil, ErrOriginal22Manifest
		}
		previous = entry.Name
	}
	return entries, nil
}

// Original22Manifest returns an owned copy of the unchanged historical
// canonical JSON, after authenticating all 22 original embedded SQL bodies.
// Additional current migrations neither alter nor invalidate this material.
func Original22Manifest() ([]byte, error) {
	if _, err := original22Material(Files, original22Canonical); err != nil {
		return nil, ErrOriginal22Manifest
	}
	return bytes.Clone(original22Canonical), nil
}

func original22Nil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

// VerifyOriginal22SnapshotTx verifies only the exact historical 001--022 rows
// in an explicit schema, within the caller's existing RR RO or RW transaction.
// It does not begin/end transactions, install objects, inspect roles/catalogs,
// or authorize maintenance. A driver error may abort the caller's transaction.
func VerifyOriginal22SnapshotTx(ctx context.Context, tx pgx.Tx, schema string) error {
	if ctx == nil || ctx.Err() != nil || original22Nil(tx) || schema == "" || len(schema) > 63 || !utf8.ValidString(schema) || strings.ContainsRune(schema, 0) {
		return ErrVerifyOriginal22Snapshot
	}
	entries, err := original22Material(Files, original22Canonical)
	if err != nil || ctx.Err() != nil {
		return ErrVerifyOriginal22Snapshot
	}
	var isolation, access string
	settings := tx.QueryRow(ctx, `SELECT pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')`)
	if original22Nil(settings) || settings.Scan(&isolation, &access) != nil || ctx.Err() != nil ||
		isolation != "repeatable read" || access != "on" && access != "off" {
		return ErrVerifyOriginal22Snapshot
	}
	// LIMIT supplies one extra-row sentinel without trusting a PK. CASE bounds
	// physical text before transport, so corrupt history cannot send large data.
	query := `SELECT CASE WHEN pg_catalog.octet_length(name) OPERATOR(pg_catalog.>=) 1::pg_catalog.int4
 AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4 THEN name ELSE NULL END,
 CASE WHEN pg_catalog.octet_length(checksum) OPERATOR(pg_catalog.=) 64::pg_catalog.int4 THEN checksum ELSE NULL END
 FROM ONLY ` + pgx.Identifier{schema, "schema_migrations"}.Sanitize() + ` LIMIT 23`
	rows, err := tx.Query(ctx, query, pgx.QueryExecModeSimpleProtocol)
	if err != nil || original22Nil(rows) {
		if !original22Nil(rows) {
			rows.Close()
		}
		return ErrVerifyOriginal22Snapshot
	}
	defer rows.Close()
	expected := make(map[string]string, original22Count)
	for _, entry := range entries {
		expected[entry.Name] = entry.SHA256
	}
	seen := make(map[string]bool, original22Count)
	count := 0
	for rows.Next() {
		if ctx.Err() != nil || count >= original22Count {
			return ErrVerifyOriginal22Snapshot
		}
		var name, checksum *string
		if rows.Scan(&name, &checksum) != nil || ctx.Err() != nil || name == nil || checksum == nil ||
			len(*name) < 1 || len(*name) > original22MaxNameBytes || len(*checksum) != 64 || seen[*name] || expected[*name] != *checksum {
			return ErrVerifyOriginal22Snapshot
		}
		seen[*name] = true
		count++
	}
	if rows.Err() != nil || ctx.Err() != nil || count != original22Count {
		return ErrVerifyOriginal22Snapshot
	}
	return nil
}
