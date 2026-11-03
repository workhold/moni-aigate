// Package procurementrequestbirth renders the isolated four-table A1 request
// birth candidate. It never installs a serving profile or controls the full
// Reserve transaction. Apply procurementbirthschema.A1SQL before these materials.
package procurementrequestbirth

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

//go:embed materials/*.sql
var materials embed.FS

const sqlSHA256 = "325910df461aac3d8d068eb078bac08c18e75741765ee79aa316d5107373db4b"
const permissionsSHA256 = "32464fd4a1e01ebcff8c2c2793fb334e8be3d20be11e5e6497a0d785d6cfe0ef"

var (
	ErrBindings  = errors.New("procurement_request_birth_bindings_invalid")
	ErrMaterials = errors.New("procurement_request_birth_materials_invalid")
)

// Bindings is installation input. These names and OIDs never form business
// root arguments. The installer must independently authenticate their roles;
// rendering these values is not a catalog proof or a running permission.
type Bindings struct {
	Schema    string
	App       string
	Writer    string
	AppOID    uint32
	WriterOID uint32
}

type MaterialsDigests struct {
	SQLSHA256         string
	PermissionsSHA256 string
}

func validIdentifier(value string) bool {
	return value != "" && len(value) <= 63 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validBindings(b Bindings) bool {
	return validIdentifier(b.Schema) && validIdentifier(b.App) && validIdentifier(b.Writer) && b.App != b.Writer && b.AppOID != 0 && b.WriterOID != 0 && b.AppOID != b.WriterOID
}

func digest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func originals() (string, string, error) {
	sql, e1 := materials.ReadFile("materials/request-birth-writer-v1.sql")
	permissions, e2 := materials.ReadFile("materials/permissions-v1.sql")
	if e1 != nil || e2 != nil || digest(string(sql)) != sqlSHA256 || digest(string(permissions)) != permissionsSHA256 {
		return "", "", ErrMaterials
	}
	return string(sql), string(permissions), nil
}

func Digests() (MaterialsDigests, error) {
	if _, _, err := originals(); err != nil {
		return MaterialsDigests{}, err
	}
	return MaterialsDigests{sqlSHA256, permissionsSHA256}, nil
}

func render(body string, b Bindings) string {
	// Identifier quotes are parsed inside PL/pgSQL, whereas dollar quoting is
	// parsed outside the function body. Choose a delimiter that cannot occur in
	// any inserted name before inserting those names, including valid $$ names.
	for n := uint64(0); ; n++ {
		tag := "$moni_rb_" + strconv.FormatUint(n, 10) + "$"
		if !strings.Contains(b.Schema, tag) && !strings.Contains(b.App, tag) && !strings.Contains(b.Writer, tag) {
			body = strings.ReplaceAll(body, "$$", tag)
			break
		}
	}
	// NewReplacer performs one pass over the original template. A quoted
	// identifier containing placeholder text is never interpreted a second time.
	return strings.NewReplacer(
		"{{MONI_SCHEMA}}", pgx.Identifier{b.Schema}.Sanitize(),
		"{{MONI_SCHEMA_TEXT}}", "pg_catalog.convert_from(pg_catalog.decode('"+hex.EncodeToString([]byte(b.Schema))+"','hex'),'UTF8')",
		"{{MONI_APP}}", pgx.Identifier{b.App}.Sanitize(),
		"{{MONI_APP_TEXT}}", "pg_catalog.convert_from(pg_catalog.decode('"+hex.EncodeToString([]byte(b.App))+"','hex'),'UTF8')",
		"{{MONI_WRITER}}", pgx.Identifier{b.Writer}.Sanitize(),
		"{{MONI_WRITER_TEXT}}", "pg_catalog.convert_from(pg_catalog.decode('"+hex.EncodeToString([]byte(b.Writer))+"','hex'),'UTF8')",
		"{{MONI_APP_OID}}", strconv.FormatUint(uint64(b.AppOID), 10),
		"{{MONI_WRITER_OID}}", strconv.FormatUint(uint64(b.WriterOID), 10),
	).Replace(body)
}

// SQL renders the fixed functions and triggers. It executes nothing.
func SQL(b Bindings) (string, error) {
	if !validBindings(b) {
		return "", ErrBindings
	}
	sql, _, err := originals()
	if err != nil {
		return "", err
	}
	return render(sql, b), nil
}

// Permissions renders only the frozen A1 candidate's ownership and ACL
// changes. It is not the full B-P4 privilege matrix or a production provisioner.
func Permissions(b Bindings) (string, error) {
	if !validBindings(b) {
		return "", ErrBindings
	}
	_, permissions, err := originals()
	if err != nil {
		return "", err
	}
	return render(permissions, b), nil
}
