//go:build historical

package maintenancescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

type financialEmptyFixtureQuery struct{ calls int }

func (q *financialEmptyFixtureQuery) Query(string, ...any) (billing.FundingSourceFactRows, error) {
	q.calls++
	return &fundingLinksFixtureRows{index: -1}, nil
}

func TestFundingFinancialMaintenanceNoFakeAuthorityOrRawExports(t *testing.T) {
	if got, err := VerifyFundingFinancialMaintenance(nil); got != nil || err != billing.ErrFundingFinancialFactsInput {
		t.Fatal("nil lease accepted")
	}
	if got, err := VerifyFundingFinancialMaintenance(&maintenancetrust.ReadLease{}); got != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	q := &financialEmptyFixtureQuery{}
	facts, err := billing.ReadFundingFinancialFacts(context.Background(), q, "private_schema")
	if err != nil || q.calls != 21 || !facts.LedgerSummary().RowsConsistent || !facts.SourceGraphSummary().ScopedGraphConsistent || !facts.CustomerKeySummary().Balanced {
		t.Fatal("empty local fixture failed", err)
	}
	for _, result := range []*FundingFinancialResult{nil, {}, {facts: facts}, {proof: &maintenancetrust.ScanProof{}}} {
		permit := &maintenancetrust.Permit{}
		if result.Verify(permit) == nil {
			t.Fatal("untrusted local graph issued proof")
		}
		if got, err := result.LedgerSummary(permit); err == nil || got != (fundinglotverify.LedgerRowsCheck{}) {
			t.Fatal("ledger summary leaked without proof")
		}
		if got, err := result.SourceLinksSummary(permit); err == nil || got != (fundinglotverify.FundingSourceLinksCheck{}) {
			t.Fatal("source summary leaked without proof")
		}
		if got, err := result.SourceGraphSummary(permit); err == nil || got != (fundinglotverify.FundingSourceGraphCheck{}) {
			t.Fatal("source graph leaked without proof")
		}
		if got, err := result.CustomerKeySummary(permit); err == nil || got != (fundinglotverify.CustomerKeyGraphCheck{}) {
			t.Fatal("key summary leaked without proof")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			if err != nil || !strings.Contains(string(raw), "redacted") || !strings.Contains(fmt.Sprintf("%v %#v", result, result), "redacted") {
				t.Fatal("result exposed private graph")
			}
		}
	}
	var decoded FundingFinancialResult
	if json.Unmarshal([]byte(`{"facts":{"ledger":{"rows_consistent":true}},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON fabricated maintenance result")
	}
}

func TestFundingFinancialMaintenanceClosesAllFailureCategories(t *testing.T) {
	for _, item := range []struct{ err, want error }{
		{nil, billing.ErrFundingFinancialFactsExecution},
		{errors.New("schema SQL private driver"), billing.ErrFundingFinancialFactsExecution},
		{errors.New("Finish private detail"), billing.ErrFundingFinancialFactsExecution},
		{maintenancetrust.ErrTransaction, billing.ErrFundingFinancialFactsExecution},
		{maintenancetrust.ErrInput, billing.ErrFundingFinancialFactsInput},
		{fmt.Errorf("private: %w", billing.ErrFundingFinancialFactsResource), billing.ErrFundingFinancialFactsResource},
		{context.Canceled, billing.ErrFundingFinancialFactsCanceled},
		{context.DeadlineExceeded, billing.ErrFundingFinancialFactsTimeout},
		{maintenancetrust.ErrTimeout, billing.ErrFundingFinancialFactsTimeout},
	} {
		if got := fundingFinancialMaintenanceFailure(nil, item.err); got != item.want || strings.Contains(got.Error(), "private") {
			t.Fatal("maintenance failure escaped safe categories", got)
		}
	}
}
