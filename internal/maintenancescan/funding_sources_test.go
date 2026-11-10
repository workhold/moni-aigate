//go:build historical

package maintenancescan

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

type fundingLinksFixtureRows struct {
	values [][]any
	index  int
}

func (r *fundingLinksFixtureRows) Next() (bool, error) {
	r.index++
	return r.index < len(r.values), nil
}
func (r *fundingLinksFixtureRows) Scan(dest ...any) error {
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) {
		return fmt.Errorf("fixture scan")
	}
	for i, value := range r.values[r.index] {
		target := reflect.ValueOf(dest[i]).Elem()
		if value == nil {
			target.SetZero()
			continue
		}
		actual := reflect.ValueOf(value)
		if target.Kind() == reflect.Pointer && actual.Kind() == reflect.String {
			copy := reflect.New(target.Type().Elem())
			copy.Elem().Set(actual)
			target.Set(copy)
		} else {
			target.Set(actual)
		}
	}
	return nil
}
func (*fundingLinksFixtureRows) Err() error   { return nil }
func (*fundingLinksFixtureRows) Close() error { return nil }

type fundingLinksFixtureQuery struct{ index int }

func (q *fundingLinksFixtureQuery) Query(string, ...any) (billing.FundingSourceFactRows, error) {
	if q.index >= 3 {
		return nil, fmt.Errorf("unexpected fixture query")
	}
	q.index++
	return &fundingLinksFixtureRows{index: -1}, nil
}

func TestFundingSourceLinksMaintenanceHasNoFakeAuthorityAndRedactsLocalFacts(t *testing.T) {
	if got, err := VerifyFundingSourceLinksMaintenance(nil); got != nil || err != billing.ErrFundingSourceFactsInput {
		t.Fatal("nil lease accepted")
	}
	if got, err := VerifyFundingSourceLinksMaintenance(&maintenancetrust.ReadLease{}); got != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	facts, err := billing.ReadFundingSourceFacts(context.Background(), &fundingLinksFixtureQuery{}, "private_schema")
	if err != nil || !facts.LocalSummary().ScopedLinksConsistent {
		t.Fatal("empty local fixture failed", err)
	}
	for _, result := range []*FundingSourceLinksResult{nil, {}, {facts: facts}} {
		if result.Verify(&maintenancetrust.Permit{}) == nil {
			t.Fatal("result without exact ScanFunding proof accepted")
		}
		got, err := result.Summary(&maintenancetrust.Permit{})
		if err == nil || got != (fundinglotverify.FundingSourceLinksCheck{}) {
			t.Fatal("invalid result leaked local source summary")
		}
		if result == nil {
			continue
		}
		raw, err := json.Marshal(result)
		rendered := fmt.Sprintf("%v %#v %s", result, result, raw)
		if err != nil || strings.Contains(rendered, "private-") || !strings.Contains(rendered, "redacted") {
			t.Fatal("maintenance result exposed source details")
		}
	}
	var decoded FundingSourceLinksResult
	if json.Unmarshal([]byte(`{"facts":{"scope":"manual_voucher_receipt_links","scoped_links_consistent":true},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON minted a ScanFunding proof")
	}
}
