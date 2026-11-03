// Package procurementaccountwriter contains the isolated account-command
// writer candidate. It does not install roles, select a serving profile, or
// authorize startup or ordinary financial traffic.
package procurementaccountwriter

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

const sqlSHA256 = "b00efdf4f28d89a0065dd45779f7f6614a9cf229dda38599c311e11fb9cd1665"
const permissionsSHA256 = "d4cef1413d34fcd6e4013044b74257a8cc159688c5b9891864c5f9268d76aa53"

var (
	ErrBindings  = errors.New("procurement_account_writer_bindings_invalid")
	ErrMaterials = errors.New("procurement_account_writer_materials_invalid")
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
	sql, e1 := materials.ReadFile("materials/account-writer-v1.sql")
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
		tag := "$moni_ac_" + strconv.FormatUint(n, 10) + "$"
		if !strings.Contains(b.Schema, tag) && !strings.Contains(b.App, tag) && !strings.Contains(b.Writer, tag) {
			body = strings.ReplaceAll(body, "$$", tag)
			break
		}
	}
	// NewReplacer performs one pass over the original template. A quoted
	// identifier containing placeholder text is never interpreted a second time.
	return strings.NewReplacer(
		"{{MONI_SCHEMA}}", pgx.Identifier{b.Schema}.Sanitize(),
		"{{MONI_APP}}", pgx.Identifier{b.App}.Sanitize(),
		"{{MONI_WRITER}}", pgx.Identifier{b.Writer}.Sanitize(),
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

// Permissions renders only the frozen account candidate's ownership and ACL
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
