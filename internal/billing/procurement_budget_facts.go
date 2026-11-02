package billing

import (
	"context"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// ProcurementBudgetFactQuery and ProcurementBudgetFactRows are the fixed
// reader seam used by maintenance. They cannot issue maintenance authority.
type ProcurementBudgetFactQuery = fundingSourceQuery
type ProcurementBudgetFactRows = fundingSourceRows

// ProcurementBudgetFacts contains only the scoped, non-authoritative budget
// projection summary. It does not prove complete procurement history.
type ProcurementBudgetFacts struct {
	check fundinglotverify.ProcurementBudgetProjectionCheck
}

func (ProcurementBudgetFacts) String() string   { return "procurement_budget_facts_redacted" }
func (ProcurementBudgetFacts) GoString() string { return "procurement_budget_facts_redacted" }
func (ProcurementBudgetFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"procurement_budget_facts":"redacted"}`), nil
}

func (f ProcurementBudgetFacts) LocalSummary() fundinglotverify.ProcurementBudgetProjectionCheck {
	return f.check
}

var (
	ErrProcurementBudgetFactsInput     = fundingSourceReadInput
	ErrProcurementBudgetFactsResource  = fundingSourceReadResource
	ErrProcurementBudgetFactsCanceled  = fundingSourceReadCanceled
	ErrProcurementBudgetFactsTimeout   = fundingSourceReadTimeout
	ErrProcurementBudgetFactsExecution = fundingSourceReadExecution
)

func validProcurementBudgetSchema(schema string) bool {
	return schema != "" && schema != "pg_catalog" && schema != "information_schema" &&
		!strings.HasPrefix(schema, "pg_") && !strings.ContainsRune(schema, 0)
}

// ReadProcurementBudgetFacts scans the complete fixed three-table projection
// on the supplied snapshot and returns only its local consistency summary.
func ReadProcurementBudgetFacts(ctx context.Context, query ProcurementBudgetFactQuery, schema string) (ProcurementBudgetFacts, error) {
	if query == nil || !validProcurementBudgetSchema(schema) {
		return ProcurementBudgetFacts{}, ErrProcurementBudgetFactsInput
	}
	if ctx == nil {
		return ProcurementBudgetFacts{}, ErrProcurementBudgetFactsInput
	}
	_, check, err := readProcurementBudgetFacts(ctx, query, schema)
	if err != nil {
		return ProcurementBudgetFacts{}, err
	}
	return ProcurementBudgetFacts{check: check}, nil
}
