// Package fundinglotscan reads one fixed, untrusted business-source union.
// It never authenticates a database, issues maintenance proof, or writes funds.
package fundinglotscan

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const (
	rowLimit   = 100000
	byteLimit  = 16 << 20
	fieldLimit = 65536
	pageSize   = 128
	tableCount = 68
)

var (
	ErrInput     = errors.New("funding_source_union_input")
	ErrResource  = errors.New("funding_source_union_resource")
	ErrCanceled  = errors.New("funding_source_union_canceled")
	ErrTimeout   = errors.New("funding_source_union_timeout")
	ErrExecution = errors.New("funding_source_union_execution")
)

type Mode uint8

const (
	ModeOrdinary Mode = iota + 1
	ModeSandbox
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

// Report is descriptive only. All identity-bearing input and completion state
// remains private; a report cannot be supplied to a maintenance constructor.
type Report struct {
	Scope                      string `json:"scope"`
	Mode                       Mode   `json:"mode"`
	Status                     string `json:"status"`
	PhysicalEOFComplete        bool   `json:"physical_eof_complete"`
	ScannedTables              uint64 `json:"scanned_tables"`
	SourceRows                 uint64 `json:"source_rows"`
	SourceBytes                uint64 `json:"source_bytes"`
	InputMismatchFields        uint64 `json:"input_mismatch_fields"`
	JournalMetadataMismatches  uint64 `json:"journal_metadata_mismatches"`
	OrdinaryBoundaryMismatches uint64 `json:"ordinary_boundary_mismatches"`
	// Fixed order: ledger, links, funding, customer, procurement, PB, sandbox.
	Algorithms       [7]string                                      `json:"algorithms"`
	CoverageComplete bool                                           `json:"coverage_complete"`
	Ledger           fundinglotverify.LedgerRowsCheck               `json:"ledger"`
	Links            fundinglotverify.FundingSourceLinksCheck       `json:"links"`
	Funding          fundinglotverify.FundingSourceGraphCheck       `json:"funding"`
	Customer         fundinglotverify.CustomerKeyGraphCheck         `json:"customer"`
	Procurement      fundinglotverify.ProcurementEvidenceGraphCheck `json:"procurement"`
	Periodic         fundinglotverify.PeriodicBudgetGraphCheck      `json:"periodic"`
	Sandbox          fundinglotverify.SandboxSourceGraphCheck       `json:"sandbox"`
}

type Facts struct{ state *session }

func (Facts) String() string   { return "funding_source_union_facts_redacted" }
func (Facts) GoString() string { return "funding_source_union_facts_redacted" }
func (Facts) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_source_union_facts":"redacted"}`), nil
}
func (f Facts) Summary() Report {
	if f.state == nil {
		return Report{}
	}
	return f.state.report
}

type scalarKind uint8

const (
	textKind scalarKind = iota + 1
	boolKind
	timeKind
	bytesKind
)

type column struct {
	name string
	kind scalarKind
}
type key struct {
	name    string
	numeric bool
}
type tableSpec struct {
	name    string
	columns []column
	keys    []key
	index   map[string]int
}
type cell struct {
	kind    scalarKind
	valid   bool
	text    string
	boolean bool
	instant pgtype.Timestamptz
	data    []byte
}
type rawRow []cell
type tableData struct {
	spec tableSpec
	rows []rawRow
	eof  bool
}
type cellRef struct {
	table       string
	row, column int
}
type session struct {
	ctx         context.Context
	query       Query
	schema      string
	mode        Mode
	tables      []tableData
	index       map[string]int
	rejected    map[cellRef]bool
	rows, bytes int
	report      Report
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
