//go:build historical

package maintenancescan

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

func TestSourceWalletMaintenanceRejectsFakeAuthorityAndLeaksNoRawValues(t *testing.T) {
	for _, lease := range []*maintenancetrust.ReadLease{nil, {}} {
		if got, err := VerifySourceWalletMaintenance(lease); got != nil || err != fundinglotscan.ErrInput {
			t.Fatal("nil or zero source-wallet lease accepted")
		}
	}
	query := &completeSourcesEmptyQuery{}
	facts, err := fundinglotscan.ReadWallet(context.Background(), query, "private_schema", fundinglotscan.ModeOrdinary)
	report := facts.Summary()
	if err != nil || query.calls != 84 || report.Scope != "complete_source_wallet_graphs_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 84 || report.Status != "mismatch" {
		t.Fatal("local source-wallet fixture did not retain physical coverage and missing-epoch mismatch", err)
	}
	for _, result := range []*SourceWalletResult{
		nil, {}, {facts: facts, mode: fundinglotscan.ModeOrdinary},
		{proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeOrdinary, proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeSandbox, proof: &maintenancetrust.ScanProof{}},
	} {
		permit := &maintenancetrust.Permit{}
		if result.Verify(permit) == nil {
			t.Fatal("descriptive wallet facts fabricated maintenance authority")
		}
		if got, err := result.Summary(permit); err == nil || !reflect.DeepEqual(got, fundinglotscan.WalletReport{}) {
			t.Fatal("unverified source-wallet summary leaked")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			if err != nil || string(raw) != `{"source_wallet_result":"redacted"}` || !strings.Contains(fmt.Sprintf("%v %#v", result, result), "redacted") {
				t.Fatal("source-wallet result exposed private state")
			}
		}
	}
	var decoded SourceWalletResult
	if json.Unmarshal([]byte(`{"facts":{"scope":"complete_source_wallet_graphs_v1"},"mode":1,"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON fabricated source-wallet authority")
	}
	typ := reflect.TypeFor[SourceWalletResult]()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Fatal("source-wallet result exports facts, mode or proof")
		}
	}
}
