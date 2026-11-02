package maintenancescan

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

func TestProcurementMaintenanceRejectsMissingAuthorityAndRedactsResult(t *testing.T) {
	if got, err := VerifyProcurementMaintenance(nil); got != nil || err != billing.ErrProcurementAttemptBindingFactsInput {
		t.Fatal("nil lease accepted")
	}
	if got, err := VerifyProcurementMaintenance(&maintenancetrust.ReadLease{}); got != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	for _, result := range []*ProcurementMaintenanceResult{nil, {}, {
		budget:  fundinglotverify.ProcurementBudgetProjectionCheck{Scope: "procurement_budget_projection_v1", Balanced: true},
		binding: fundinglotverify.ProcurementAttemptBindingCheck{Scope: "procurement_attempt_binding_graph_v1", Balanced: true},
	}} {
		if result.Verify(&maintenancetrust.Permit{}) == nil {
			t.Fatal("result without exact ScanProcurement proof accepted")
		}
		budget, err := result.BudgetSummary(&maintenancetrust.Permit{})
		if err == nil || budget != (fundinglotverify.ProcurementBudgetProjectionCheck{}) {
			t.Fatal("invalid result leaked budget summary")
		}
		binding, err := result.BindingSummary(&maintenancetrust.Permit{})
		if err == nil || binding != (fundinglotverify.ProcurementAttemptBindingCheck{}) {
			t.Fatal("invalid result leaked attempt summary")
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
	var decoded ProcurementMaintenanceResult
	if json.Unmarshal([]byte(`{"budget":{"scope":"procurement_budget_projection_v1","balanced":true},"binding":{"scope":"procurement_attempt_binding_graph_v1","balanced":true},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON minted a ScanProcurement proof")
	}
}

func TestProcurementMaintenanceSummaryScopesAreClosed(t *testing.T) {
	result := &ProcurementMaintenanceResult{
		budget:  fundinglotverify.ProcurementBudgetProjectionCheck{Scope: "wallet_full_source_v1", Balanced: true},
		binding: fundinglotverify.ProcurementAttemptBindingCheck{Scope: "procurement_full_source_v1", Balanced: true},
	}
	if result.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("wrong local scopes accepted")
	}
	if got, err := result.BudgetSummary(&maintenancetrust.Permit{}); err == nil || got != (fundinglotverify.ProcurementBudgetProjectionCheck{}) {
		t.Fatal("wrong budget scope leaked")
	}
	if got, err := result.BindingSummary(&maintenancetrust.Permit{}); err == nil || got != (fundinglotverify.ProcurementAttemptBindingCheck{}) {
		t.Fatal("wrong binding scope leaked")
	}
}

func TestProcurementMaintenanceMapsFailureCategories(t *testing.T) {
	cases := []struct {
		input, want error
	}{
		{billing.ErrProcurementAttemptBindingFactsInput, billing.ErrProcurementAttemptBindingFactsInput},
		{billing.ErrProcurementAttemptBindingFactsResource, billing.ErrProcurementAttemptBindingFactsResource},
		{billing.ErrProcurementAttemptBindingFactsCanceled, billing.ErrProcurementAttemptBindingFactsCanceled},
		{billing.ErrProcurementAttemptBindingFactsTimeout, billing.ErrProcurementAttemptBindingFactsTimeout},
		{billing.ErrProcurementAttemptBindingFactsExecution, billing.ErrProcurementAttemptBindingFactsExecution},
	}
	for _, tc := range cases {
		if got := procurementMaintenanceFailure(nil, tc.input); got != tc.want {
			t.Fatalf("failure category changed: input=%v got=%v want=%v", tc.input, got, tc.want)
		}
	}
}
