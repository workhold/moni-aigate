package maintenancescan

import (
	"context"
	"errors"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// FundingFinancialResult binds four local summaries to one completed
// ScanFunding lease. It is not complete seven-domain funding verification.
type FundingFinancialResult struct {
	facts billing.FundingFinancialFacts
	proof *maintenancetrust.ScanProof
}

func (FundingFinancialResult) String() string   { return "funding_financial_result_redacted" }
func (FundingFinancialResult) GoString() string { return "funding_financial_result_redacted" }
func (FundingFinancialResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_financial_result":"redacted"}`), nil
}
func (r *FundingFinancialResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil ||
		r.facts.LedgerSummary().Scope != "supplied_ledger_rows" ||
		r.facts.SourceLinksSummary().Scope != "manual_voucher_receipt_links" ||
		r.facts.SourceGraphSummary().Scope != "manual_voucher_funding_graph_v1" ||
		r.facts.CustomerKeySummary().Scope != "customer_key_financial_graph_v1" {
		return billing.ErrFundingFinancialFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanFunding)
}
func (r *FundingFinancialResult) LedgerSummary(permit *maintenancetrust.Permit) (fundinglotverify.LedgerRowsCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.LedgerRowsCheck{}, err
	}
	return r.facts.LedgerSummary(), nil
}
func (r *FundingFinancialResult) SourceLinksSummary(permit *maintenancetrust.Permit) (fundinglotverify.FundingSourceLinksCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.FundingSourceLinksCheck{}, err
	}
	return r.facts.SourceLinksSummary(), nil
}
func (r *FundingFinancialResult) SourceGraphSummary(permit *maintenancetrust.Permit) (fundinglotverify.FundingSourceGraphCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.FundingSourceGraphCheck{}, err
	}
	return r.facts.SourceGraphSummary(), nil
}
func (r *FundingFinancialResult) CustomerKeySummary(permit *maintenancetrust.Permit) (fundinglotverify.CustomerKeyGraphCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.CustomerKeyGraphCheck{}, err
	}
	return r.facts.CustomerKeySummary(), nil
}

// VerifyFundingFinancialMaintenance is the expanded fixed ScanFunding reader.
// A Permit can choose this or the legacy three-table reader once. All tables,
// cores and EOF observations complete before the sole Finish invocation.
func VerifyFundingFinancialMaintenance(lease *maintenancetrust.ReadLease) (*FundingFinancialResult, error) {
	if lease == nil {
		return nil, billing.ErrFundingFinancialFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanFunding); err != nil {
		return nil, fundingFinancialMaintenanceFailure(lease, err)
	}
	var schema string
	if err := lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return nil, fundingFinancialMaintenanceFailure(lease, err)
	}
	facts, err := billing.ReadFundingFinancialFacts(fundingSourceLeaseContext{lease: lease}, fundingSourceLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, fundingFinancialMaintenanceFailure(lease, err)
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, fundingFinancialMaintenanceFailure(lease, err)
	}
	if proof == nil {
		return nil, billing.ErrFundingFinancialFactsExecution
	}
	return &FundingFinancialResult{facts: facts, proof: proof}, nil
}

// Close every adapter/core/Finish failure over the same five safe categories.
func fundingFinancialMaintenanceFailure(lease *maintenancetrust.ReadLease, err error) error {
	if errors.Is(err, billing.ErrFundingFinancialFactsInput) || errors.Is(err, maintenancetrust.ErrInput) {
		return billing.ErrFundingFinancialFactsInput
	}
	if errors.Is(err, billing.ErrFundingFinancialFactsResource) {
		return billing.ErrFundingFinancialFactsResource
	}
	if errors.Is(err, billing.ErrFundingFinancialFactsTimeout) || errors.Is(err, maintenancetrust.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) || lease != nil && errors.Is(lease.Err(), maintenancetrust.ErrTimeout) {
		return billing.ErrFundingFinancialFactsTimeout
	}
	if errors.Is(err, billing.ErrFundingFinancialFactsCanceled) || errors.Is(err, context.Canceled) {
		return billing.ErrFundingFinancialFactsCanceled
	}
	return billing.ErrFundingFinancialFactsExecution
}
