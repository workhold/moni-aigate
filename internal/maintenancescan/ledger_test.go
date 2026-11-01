package maintenancescan

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"reflect"
	"strings"
	"testing"
	"time"
)

type privateLedgerTestRows struct {
	values [][]any
	index  int
}

func (r *privateLedgerTestRows) Next() (bool, error) { r.index++; return r.index < len(r.values), nil }
func (r *privateLedgerTestRows) Scan(dest ...any) error {
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) {
		return fmt.Errorf("test scan arity")
	}
	for i, v := range r.values[r.index] {
		target := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			target.SetZero()
		} else {
			target.Set(reflect.ValueOf(v))
		}
	}
	return nil
}
func (*privateLedgerTestRows) Err() error   { return nil }
func (*privateLedgerTestRows) Close() error { return nil }

type privateLedgerTestQuery struct {
	pages []*privateLedgerTestRows
	index int
}

func (q *privateLedgerTestQuery) Query(string, ...any) (billing.LedgerFactRows, error) {
	if q.index >= len(q.pages) {
		return nil, fmt.Errorf("test unexpected query")
	}
	r := q.pages[q.index]
	q.index++
	return r, nil
}
func privateLedgerFacts(t *testing.T) billing.LedgerFacts {
	t.Helper()
	tenant := "private-tenant"
	q := &privateLedgerTestQuery{pages: []*privateLedgerTestRows{
		{index: -1, values: [][]any{{"a", &tenant, "AVAILABLE", "100"}, {"b", nil, "FUNDING_CLEARING", "-100"}}},
		{index: -1, values: [][]any{{"private-id", tenant, "op", "credit", "private-ref", "private-actor", "CNY", "1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
		{index: -1, values: [][]any{{"l1", "private-id", "a", "100"}, {"l2", "private-id", "b", "-100"}}},
	}}
	facts, err := billing.ReadLedgerFacts(context.Background(), q, "private_schema")
	if err != nil || !facts.LocalSummary().RowsConsistent {
		t.Fatal("nonempty private facts fixture failed", err)
	}
	return facts
}

func TestFundingLedgerMaintenanceInputZeroResultAndRedaction(t *testing.T) {
	if result, err := VerifyLedgerMaintenance(nil); result != nil || err != billing.ErrLedgerFactsInput {
		t.Fatal("nil lease accepted")
	}
	if result, err := VerifyLedgerMaintenance(&maintenancetrust.ReadLease{}); result != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	for _, result := range []*MaintenanceLedgerResult{nil, {}, {facts: privateLedgerFacts(t)}} {
		if result.Verify(&maintenancetrust.Permit{}) == nil {
			t.Fatal("result without exact lease proof accepted")
		}
		got, err := result.Summary(&maintenancetrust.Permit{})
		if err == nil || got != (fundinglotverify.LedgerRowsCheck{}) {
			t.Fatal("invalid result leaked local summary")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			rendered := fmt.Sprintf("%v %#v %s", result, result, raw)
			if err != nil || strings.Contains(rendered, "private-") || !strings.Contains(rendered, "redacted") {
				t.Fatal("result exposes private ledger facts")
			}
		}
	}
	var decoded MaintenanceLedgerResult
	if json.Unmarshal([]byte(`{"facts":{"consistent":true},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON minted ledger proof")
	}
	c := fundingLedgerLeaseContext{lease: &maintenancetrust.ReadLease{}}
	if c.Value("secret") != nil || c.Value(nil) != nil {
		t.Fatal("lease context forwards values")
	}
}
