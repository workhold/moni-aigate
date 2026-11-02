package billing

import (
	"context"
	"strings"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

// FundingSourceFactQuery and FundingSourceFactRows preserve the narrow
// internal seam used by fixed raw readers. Implementations cannot issue proof.
type FundingSourceFactQuery = fundingSourceQuery
type FundingSourceFactRows = fundingSourceRows

// FundingSourceFacts contains only a local, non-authoritative relation check.
// It does not establish complete source coverage or a same-transaction link.
type FundingSourceFacts struct {
	check fundinglotverify.FundingSourceLinksCheck
}

func (FundingSourceFacts) String() string   { return "funding_source_facts_redacted" }
func (FundingSourceFacts) GoString() string { return "funding_source_facts_redacted" }
func (FundingSourceFacts) MarshalJSON() ([]byte, error) {
	return []byte(`{"funding_source_facts":"redacted"}`), nil
}

func (f FundingSourceFacts) LocalSummary() fundinglotverify.FundingSourceLinksCheck {
	return f.check
}

var (
	ErrFundingSourceFactsInput     = fundingSourceReadInput
	ErrFundingSourceFactsResource  = fundingSourceReadResource
	ErrFundingSourceFactsCanceled  = fundingSourceReadCanceled
	ErrFundingSourceFactsTimeout   = fundingSourceReadTimeout
	ErrFundingSourceFactsExecution = fundingSourceReadExecution
)

func validFundingSourceSchema(schema string) bool {
	return schema != "" && schema != "pg_catalog" && schema != "information_schema" &&
		!strings.HasPrefix(schema, "pg_") && !strings.ContainsRune(schema, 0)
}

// ReadFundingSourceFacts runs the one fixed manual-credit/voucher source
// reader. Its check remains descriptive, even when its scoped relations agree.
func ReadFundingSourceFacts(ctx context.Context, query FundingSourceFactQuery, schema string) (FundingSourceFacts, error) {
	if query == nil || !validFundingSourceSchema(schema) {
		return FundingSourceFacts{}, ErrFundingSourceFactsInput
	}
	_, check, err := readFundingSourceFacts(ctx, query, schema)
	if err != nil {
		return FundingSourceFacts{}, err
	}
	return FundingSourceFacts{check: check}, nil
}
