//go:build historical

package maintenancescan

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

func TestSandboxCaptureReceiptMaintenanceInputsAndResultRedaction(t *testing.T) {
	if result, err := VerifySandboxCaptureReceiptLinksMaintenance(nil); result != nil || err != billing.ErrFundingSourceFactsInput {
		t.Fatal("nil lease accepted")
	}
	if result, err := VerifySandboxCaptureReceiptLinksMaintenance(&maintenancetrust.ReadLease{}); result != nil || err == nil {
		t.Fatal("zero lease accepted")
	}
	for _, result := range []*SandboxCaptureReceiptLinksResult{nil, {}, {facts: billing.SandboxCaptureReceiptFacts{}}} {
		if result.Verify(&maintenancetrust.Permit{}) == nil {
			t.Fatal("result without exact lease proof accepted")
		}
		if got, err := result.Summary(&maintenancetrust.Permit{}); err == nil || got.Scope != "" || got.Scanned.Receipts != 0 || got.Scanned.Captures != 0 {
			t.Fatal("invalid result exposed direct-link facts")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			rendered := fmt.Sprintf("%v %#v %s", result, result, raw)
			if err != nil || strings.Contains(rendered, "private-") || !strings.Contains(rendered, "redacted") {
				t.Fatal("result exposes sandbox or funding facts")
			}
		}
	}
	var decoded SandboxCaptureReceiptLinksResult
	if json.Unmarshal([]byte(`{"facts":{"direct_links_consistent":true},"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON minted a maintenance proof")
	}
}
