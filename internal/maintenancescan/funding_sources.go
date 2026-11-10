//go:build historical

package maintenancescan

import (
	"context"
	"strings"
	"time"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// FundingSourceLinksResult binds the local manual/voucher crosswalk to one
// completed ScanFunding lease. The proof does not establish whole-source
// coverage, payment-source verification, or readiness for maintenance init.
type FundingSourceLinksResult struct {
	facts billing.FundingSourceFacts
	proof *maintenancetrust.ScanProof
}

func (FundingSourceLinksResult) String() string   { return "funding_source_links_result_redacted" }
func (FundingSourceLinksResult) GoString() string { return "funding_source_links_result_redacted" }
func (FundingSourceLinksResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_source_links_result":"redacted"}`), nil
}

func (r *FundingSourceLinksResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return billing.ErrFundingSourceFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanFunding)
}

func (r *FundingSourceLinksResult) Summary(permit *maintenancetrust.Permit) (fundinglotverify.FundingSourceLinksCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.FundingSourceLinksCheck{}, err
	}
	return r.facts.LocalSummary(), nil
}

type fundingSourceLeaseQuery struct{ lease *maintenancetrust.ReadLease }

func (q fundingSourceLeaseQuery) Query(sql string, args ...any) (billing.FundingSourceFactRows, error) {
	return q.lease.Query(sql, args...)
}

// fundingSourceLeaseContext translates ReadLease's fixed timeout category to
// the standard context sentinel consumed by billing, without exposing
// maintenancetrust errors or its private context to the domain reader.
type fundingSourceLeaseContext struct{ lease *maintenancetrust.ReadLease }

func (c fundingSourceLeaseContext) Deadline() (time.Time, bool) { return c.lease.Deadline() }
func (c fundingSourceLeaseContext) Done() <-chan struct{}       { return c.lease.Done() }
func (c fundingSourceLeaseContext) Err() error {
	if c.lease == nil {
		return context.Canceled
	}
	switch c.lease.Err() {
	case nil:
		return nil
	case maintenancetrust.ErrTimeout:
		return context.DeadlineExceeded
	default:
		return context.Canceled
	}
}
func (fundingSourceLeaseContext) Value(any) any { return nil }

var _ context.Context = fundingSourceLeaseContext{}

// VerifyFundingSourceLinksMaintenance scans all rows in the three fixed
// source-link tables on the original T2 snapshot and returns only a scoped
// manual-credit/voucher report. Sandbox capture receipts remain uncovered.
func VerifyFundingSourceLinksMaintenance(lease *maintenancetrust.ReadLease) (*FundingSourceLinksResult, error) {
	if lease == nil {
		return nil, billing.ErrFundingSourceFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanFunding); err != nil {
		return nil, err
	}
	var schema string
	if err := lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return nil, err
	}
	if schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") {
		return nil, billing.ErrFundingSourceFactsExecution
	}
	facts, err := billing.ReadFundingSourceFacts(fundingSourceLeaseContext{lease: lease}, fundingSourceLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, err
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, err
	}
	return &FundingSourceLinksResult{facts: facts, proof: proof}, nil
}
