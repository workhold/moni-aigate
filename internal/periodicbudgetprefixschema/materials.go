// Package periodicbudgetprefixschema contains the frozen, isolated B-P2 SQL
// candidate. It neither connects to a database nor installs a serving schema.
package periodicbudgetprefixschema

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

const sqlSHA = "b225c3ff61b735d6830aed1d25edc27bc4784f8e251cb2c1d656196d8d9c630e"
const permissionsSHA = "aaea2994d5a79726df8b5c1767b5adb054d14ee8cb81fa12b43b87d24a2f28bf"

//go:embed materials/candidate-v1.sql
var candidateSQL string

//go:embed materials/permissions-v1.sql
var permissionSQL string

var ErrMaterials = errors.New("periodic_prefix_materials_invalid")
var ErrIdentifiers = errors.New("periodic_prefix_identifiers_invalid")

// MaterialsDigests identifies the original, unrendered material bytes.
type MaterialsDigests struct {
	SQLSHA256         string
	PermissionsSHA256 string
}

func digest(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func validMaterials() bool {
	return digest(candidateSQL) == sqlSHA && digest(permissionSQL) == permissionsSHA
}

// SQL returns the fixed candidate, without executing or rewriting it.
func SQL() (string, error) {
	if !validMaterials() {
		return "", ErrMaterials
	}
	return strings.Clone(candidateSQL), nil
}

// Digests returns digests only after checking both embedded originals.
func Digests() (MaterialsDigests, error) {
	if !validMaterials() {
		return MaterialsDigests{}, ErrMaterials
	}
	return MaterialsDigests{SQLSHA256: sqlSHA, PermissionsSHA256: permissionsSHA}, nil
}

func validIdentifier(v string) bool {
	return v != "" && len(v) <= 63 && !strings.ContainsRune(v, '\x00')
}

// Permissions renders only the two SQL identifiers in the frozen template.
func Permissions(app, projection string) (string, error) {
	if !validIdentifier(app) || !validIdentifier(projection) || app == projection {
		return "", ErrIdentifiers
	}
	if !validMaterials() {
		return "", ErrMaterials
	}
	return strings.NewReplacer("{{app}}", pgx.Identifier{app}.Sanitize(),
		"{{projection}}", pgx.Identifier{projection}.Sanitize()).Replace(permissionSQL), nil
}
