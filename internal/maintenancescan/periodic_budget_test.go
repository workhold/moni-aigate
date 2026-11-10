//go:build historical

package maintenancescan

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"strings"
	"testing"
)

func TestVerifierMaintenanceResultZeroAndEncodingDoNotConferAuthority(t *testing.T) {
	for _, r := range []*MaintenanceResult{nil, {}, {report: periodicbudget.Verification{SchemaVersion: 1, Balanced: true, CoverageComplete: true}}} {
		if r.Verify(nil) == nil {
			t.Fatal("unissued result valid")
		}
		if got, err := r.Report(nil); err == nil || got != (periodicbudget.Verification{}) {
			t.Fatal("unissued report returned")
		}
		if got, err := r.Summary(nil); err == nil || got != (MaintenanceSummary{}) {
			t.Fatal("unissued summary returned")
		}
	}
	r := MaintenanceResult{report: periodicbudget.Verification{AsOf: "private-row-value"}}
	raw, err := json.Marshal(r)
	if err != nil || string(raw) != `{"periodic_budget_maintenance":"redacted"}` || strings.Contains(fmt.Sprintf("%v %#v", r, r), "private-row-value") {
		t.Fatal("opaque result leaked")
	}
	var decoded MaintenanceResult
	if json.Unmarshal([]byte(`{"report":{"schema_version":1,"balanced":true},"proof":{}}`), &decoded) != nil || decoded.Verify(nil) == nil {
		t.Fatal("JSON minted result")
	}
}

func TestVerifierMaintenanceRejectsNilAndZeroReadLease(t *testing.T) {
	for _, lease := range []*maintenancetrust.ReadLease{nil, {}} {
		result, err := VerifyMaintenance(lease)
		if result != nil || !errors.Is(err, periodicbudget.ErrVerifyInput) {
			t.Fatal("unissued lease returned result", result, err)
		}
	}
}
