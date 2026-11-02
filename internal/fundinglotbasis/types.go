// Package fundinglotbasis fingerprints a fixed legacy business projection.
// It authenticates no transaction, role, installation, or financial operation.
package fundinglotbasis

import (
	"context"
	"errors"
	"reflect"
)

const (
	rowLimit          = 100000
	byteLimit         = 64 << 20
	keyLimit          = 262144
	recordLimit       = 1048576
	pageSize          = 128
	metadataRowLimit  = 2048
	metadataByteLimit = 1 << 20
	metadataTextLimit = 65536
)

var (
	ErrInput     = errors.New("funding_basis_input")
	ErrResource  = errors.New("funding_basis_resource")
	ErrCanceled  = errors.New("funding_basis_canceled")
	ErrTimeout   = errors.New("funding_basis_timeout")
	ErrMetadata  = errors.New("funding_basis_metadata")
	ErrExecution = errors.New("funding_basis_execution")
)

type Query interface {
	Query(string, ...any) (Rows, error)
}
type Rows interface {
	Next() (bool, error)
	Scan(...any) error
	Err() error
	Close() error
}

type Report struct {
	Scope               string `json:"scope"`
	PhysicalEOFComplete bool   `json:"physical_eof_complete"`
	ScannedTables       uint64 `json:"scanned_tables"`
	ScannedColumns      uint64 `json:"scanned_columns"`
	SourceRows          uint64 `json:"source_rows"`
	SourceBytes         uint64 `json:"source_bytes"`
	BasisSHA256         string `json:"basis_sha256"`
	ManifestSHA256      string `json:"manifest_sha256"`
	DescriptorSHA256    string `json:"descriptor_sha256"`
	SendMetadataSHA256  string `json:"send_metadata_sha256"`
}

type factsState struct{ report Report }
type Facts struct{ state *factsState }

func (Facts) String() string               { return "funding_basis_facts_redacted" }
func (Facts) GoString() string             { return "funding_basis_facts_redacted" }
func (Facts) MarshalJSON() ([]byte, error) { return []byte(`{"funding_basis_facts":"redacted"}`), nil }
func (f Facts) Summary() Report {
	if f.state == nil {
		return Report{}
	}
	return f.state.report
}

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return r.IsNil()
	}
	return false
}
func contextFailure(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	if ctx.Err() != nil {
		return ErrCanceled
	}
	return ErrExecution
}
func failure(ctx context.Context, e error) error {
	if e == ErrResource {
		return e
	}
	if ctx.Err() != nil {
		return contextFailure(ctx)
	}
	switch e {
	case ErrInput, ErrMetadata, ErrExecution, ErrCanceled, ErrTimeout:
		return e
	}
	return ErrExecution
}
