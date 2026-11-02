package maintenancescan

import (
	"strings"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

// SandboxCaptureReceiptLinksResult binds one fixed direct-link summary to a
// completed ScanSandboxSources lease. It does not prove payment or ledger facts.
type SandboxCaptureReceiptLinksResult struct {
	facts billing.SandboxCaptureReceiptFacts
	proof *maintenancetrust.ScanProof
}

func (SandboxCaptureReceiptLinksResult) String() string {
	return "sandbox_capture_receipt_links_result_redacted"
}
func (SandboxCaptureReceiptLinksResult) GoString() string {
	return "sandbox_capture_receipt_links_result_redacted"
}
func (SandboxCaptureReceiptLinksResult) MarshalJSON() ([]byte, error) {
	return []byte(`{"sandbox_capture_receipt_links_result":"redacted"}`), nil
}

func (r *SandboxCaptureReceiptLinksResult) Verify(permit *maintenancetrust.Permit) error {
	if r == nil || r.proof == nil {
		return billing.ErrFundingSourceFactsInput
	}
	return r.proof.Verify(permit, maintenancetrust.ScanSandboxSources)
}

func (r *SandboxCaptureReceiptLinksResult) Summary(permit *maintenancetrust.Permit) (fundinglotverify.FundingSandboxCaptureLinkCheck, error) {
	if err := r.Verify(permit); err != nil {
		return fundinglotverify.FundingSandboxCaptureLinkCheck{}, err
	}
	return r.facts.LocalSummary(), nil
}

// VerifySandboxCaptureReceiptLinksMaintenance reads only the fixed direct
// relationship between payment_captures and sandbox_capture receipts. All
// evidence, effect, journal and ledger relations remain outside this scope.
func VerifySandboxCaptureReceiptLinksMaintenance(lease *maintenancetrust.ReadLease) (*SandboxCaptureReceiptLinksResult, error) {
	if lease == nil {
		return nil, billing.ErrFundingSourceFactsInput
	}
	defer lease.Abort()
	if err := lease.Require(maintenancetrust.ScanSandboxSources); err != nil {
		return nil, err
	}
	var schema string
	if err := lease.QueryRow("SELECT pg_catalog.current_schema()::pg_catalog.text").Scan(&schema); err != nil {
		return nil, err
	}
	if schema == "" || schema == "pg_catalog" || schema == "information_schema" || strings.HasPrefix(schema, "pg_") || strings.ContainsRune(schema, 0) {
		return nil, billing.ErrFundingSourceFactsExecution
	}
	facts, err := billing.ReadSandboxCaptureReceiptFacts(fundingSourceLeaseContext{lease: lease}, fundingSourceLeaseQuery{lease: lease}, schema)
	if err != nil {
		return nil, err
	}
	proof, err := lease.Finish()
	if err != nil {
		return nil, err
	}
	return &SandboxCaptureReceiptLinksResult{facts: facts, proof: proof}, nil
}
