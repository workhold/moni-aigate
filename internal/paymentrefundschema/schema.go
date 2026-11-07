// Package paymentrefundschema owns the isolated C1-M mock refund schema
// extension. It has no database or runtime side effects; installation is a
// later payment sandbox versioning step.
package paymentrefundschema

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
)

var ErrSchema = errors.New("payment_refund_schema_invalid")

//go:embed c1m.sql
var files embed.FS

const Version = 1

func SQL() (string, error) {
	b, err := files.ReadFile("c1m.sql")
	if err != nil || len(b) == 0 {
		return "", ErrSchema
	}
	return string(b), nil
}

func Digest() (string, error) {
	s, err := SQL()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:]), nil
}
