//go:build historical

package billing

import (
	"context"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// SandboxCaptureReceiptFacts is a redacted local direct-link check. It does
// not establish payment evidence provenance or validate ledger effects.
type SandboxCaptureReceiptFacts struct {
	check fundinglotverify.FundingSandboxCaptureLinkCheck
}

func (SandboxCaptureReceiptFacts) String() string {
	return "sandbox_capture_receipt_facts_redacted"
}
func (SandboxCaptureReceiptFacts) GoString() string {
	return "sandbox_capture_receipt_facts_redacted"
}
func (SandboxCaptureReceiptFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"sandbox_capture_receipt_facts":"redacted"}`), nil
}

func (f SandboxCaptureReceiptFacts) LocalSummary() fundinglotverify.FundingSandboxCaptureLinkCheck {
	return f.check
}

// ReadSandboxCaptureReceiptFacts runs the one fixed full-table direct-link
// reader. Its summary remains descriptive and non-authoritative.
func ReadSandboxCaptureReceiptFacts(ctx context.Context, query FundingSourceFactQuery, schema string) (SandboxCaptureReceiptFacts, error) {
	if ctx == nil {
		return SandboxCaptureReceiptFacts{}, ErrFundingSourceFactsInput
	}
	if query == nil || !validFundingSourceSchema(schema) {
		return SandboxCaptureReceiptFacts{}, ErrFundingSourceFactsInput
	}
	_, check, err := readSandboxCaptureReceiptFacts(ctx, query, schema)
	if err != nil {
		return SandboxCaptureReceiptFacts{}, err
	}
	return SandboxCaptureReceiptFacts{check: check}, nil
}
