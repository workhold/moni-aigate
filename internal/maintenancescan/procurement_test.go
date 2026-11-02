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

type procurementFixtureRows struct{ index int }

func (r *procurementFixtureRows) Next() (bool, error) { r.index++; return false, nil }
func (*procurementFixtureRows) Scan(...any) error     { return fmt.Errorf("unexpected row") }
func (*procurementFixtureRows) Err() error            { return nil }
func (*procurementFixtureRows) Close() error          { return nil }

type procurementFixtureQuery struct{ calls int }

func (q *procurementFixtureQuery) Query(string, ...any) (billing.ProcurementBudgetFactRows, error) {
	if q.calls >= 3 {
		return nil, fmt.Errorf("unexpected query")
	}
	q.calls++
	return &procurementFixtureRows{}, nil
}

func TestProcurementBudgetMaintenanceRejectsMissingAuthorityAndRedactsFacts(t *testing.T) {
	if got, err := VerifyProcurementBudgetMaintenance(nil); got != nil || err != billing.ErrProcurementBudgetFactsInput {
		t.Fatal("nil lease accepted")
	}
	if got, err := VerifyProcurementBudgetMaintenance(&maintenancetrust.ReadLease{}); got != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	facts, err := billing.ReadProcurementBudgetFacts(context.Background(), &procurementFixtureQuery{}, "private_schema")
	if err != nil || facts.LocalSummary() != (fundinglotverify.ProcurementBudgetProjectionCheck{
		Scope: "procurement_budget_projection_v1", Balanced: true,
	}) {
		t.Fatal("empty local fixture failed", err, facts.LocalSummary())
	}
	for _, result := range []*ProcurementBudgetMaintenanceResult{nil, {}, {check: facts.LocalSummary()}} {
		if result.Verify(&maintenancetrust.Permit{}) == nil {
			t.Fatal("result without exact ScanProcurement proof accepted")
		}
		got, err := result.Summary(&maintenancetrust.Permit{})
		if err == nil || got != (fundinglotverify.ProcurementBudgetProjectionCheck{}) {
			t.Fatal("invalid result leaked local summary")
		}
		if result == nil {
			continue
		}
		raw, err := json.Marshal(result)
		rendered := fmt.Sprintf("%v %#v %s", result, result, raw)
		if err != nil || strings.Contains(rendered, "private-") || !strings.Contains(rendered, "redacted") {
			t.Fatal("maintenance result exposed procurement details")
		}
	}
	var decoded ProcurementBudgetMaintenanceResult
	if json.Unmarshal([]byte(`{"check":{"scope":"procurement_budget_projection_v1","balanced":true},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON minted a ScanProcurement proof")
	}
}

func TestProcurementBudgetMaintenanceSummaryScopeIsClosed(t *testing.T) {
	facts := fundinglotverify.ProcurementBudgetProjectionCheck{Scope: "wallet_full_source_v1", Balanced: true}
	result := &ProcurementBudgetMaintenanceResult{check: facts}
	if result.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("wrong local scope accepted")
	}
	got, err := result.Summary(&maintenancetrust.Permit{})
	if err == nil || !reflect.DeepEqual(got, fundinglotverify.ProcurementBudgetProjectionCheck{}) {
		t.Fatal("wrong scope summary leaked")
	}
}
