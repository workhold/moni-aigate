// This tool exports UNREVIEWED metadata candidates. It is not a runtime checker.
package main

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"sync"
	"time"
)

type object = map[string]any
type document = map[string]any

type sourcePin struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type fileDigest struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}

type refKey struct{ class, oid, sub uint32 }

type profile struct {
	sandbox                        bool
	schema                         string
	owner, app, worker, projection string
	roleOIDs                       map[string]uint32
	schemaOID                      uint32
	base, after                    document
	baseRoles, afterRoles          document
	baseIDs                        map[string]string
	createdRoles                   []string
	schemaCreated                  bool
	backendMu                      sync.Mutex
	closedBackends                 []backendIdentity
	cleanupDeadline                time.Time
}

type backendIdentity struct {
	pid         uint32
	started     time.Time
	application string
	connection  *pgx.Conn
}

type rawObject struct {
	class, oid uint32
	name       string
	data       object
}

type resolver struct {
	refs       map[refKey]object
	reverse    map[string]refKey
	types      map[uint32]object
	builtins   map[refKey]object
	attributes map[uint32]map[uint32]string
	names      []string
	lookup     func(refKey) (object, error)
}

func array(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

func number(v any) (uint32, bool) {
	// PostgreSQL's jsonb OID encoder deliberately emits decimal strings; the
	// wrapper is a typed structural field, never an arbitrary text substitution.
	if s, ok := v.(string); ok {
		if s == "" || len(s) > 1 && s[0] == '0' {
			return 0, false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, e := strconv.ParseUint(s, 10, 32)
		return uint32(n), e == nil
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, e := n.Int64()
	return uint32(i), e == nil && i >= 0 && i <= 1<<32-1
}
