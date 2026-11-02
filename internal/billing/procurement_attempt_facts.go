package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// ProcurementAttemptBindingFactQuery is the fixed query seam used by the
// maintenance adapter. Implementations cannot issue maintenance authority.
type ProcurementAttemptBindingFactQuery = fundingSourceQuery
type ProcurementAttemptBindingFactRows = fundingSourceRows

// ProcurementAttemptBindingFacts contains the two scoped summaries produced
// from one complete ScanProcurement projection. It is not a domain-wide proof.
type ProcurementAttemptBindingFacts struct {
	budget  fundinglotverify.ProcurementBudgetProjectionCheck
	binding fundinglotverify.ProcurementAttemptBindingCheck
}

func (ProcurementAttemptBindingFacts) String() string {
	return "procurement_attempt_binding_facts_redacted"
}
func (ProcurementAttemptBindingFacts) GoString() string {
	return "procurement_attempt_binding_facts_redacted"
}
func (ProcurementAttemptBindingFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"procurement_attempt_binding_facts":"redacted"}`), nil
}

func (f ProcurementAttemptBindingFacts) BudgetSummary() fundinglotverify.ProcurementBudgetProjectionCheck {
	return f.budget
}

func (f ProcurementAttemptBindingFacts) BindingSummary() fundinglotverify.ProcurementAttemptBindingCheck {
	return f.binding
}

var (
	ErrProcurementAttemptBindingFactsInput     = fundingSourceReadInput
	ErrProcurementAttemptBindingFactsResource  = fundingSourceReadResource
	ErrProcurementAttemptBindingFactsCanceled  = fundingSourceReadCanceled
	ErrProcurementAttemptBindingFactsTimeout   = fundingSourceReadTimeout
	ErrProcurementAttemptBindingFactsExecution = fundingSourceReadExecution
)

// ReadProcurementAttemptBindingFacts reads the complete fixed twelve-table
// projection once and returns independent budget and attempt-binding checks.
// Data mismatches are represented in the summaries and do not fail the read.
func ReadProcurementAttemptBindingFacts(ctx context.Context, query ProcurementAttemptBindingFactQuery, schema string) (ProcurementAttemptBindingFacts, error) {
	if ctx == nil || query == nil || !validProcurementBudgetSchema(schema) {
		return ProcurementAttemptBindingFacts{}, ErrProcurementAttemptBindingFactsInput
	}
	budget, binding, err := readProcurementAttemptBindingFacts(ctx, query, schema)
	if err != nil {
		return ProcurementAttemptBindingFacts{}, err
	}
	return ProcurementAttemptBindingFacts{budget: budget, binding: binding}, nil
}
