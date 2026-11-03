package maintenancescan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
)

func TestPrefixMaintenanceWrappersRejectAbsentAuthority(t *testing.T) {
	for _, lease := range []*maintenancetrust.ReadLease{nil, {}} {
		if result, err := VerifyPrefixSourcesMaintenance(lease); result != nil || err != fundinglotscan.ErrInput {
			t.Fatal("nil or zero lease issued prefix-source result")
		}
		if result, err := VerifyPrefixWalletMaintenance(lease); result != nil || err != fundinglotscan.ErrInput {
			t.Fatal("nil or zero lease issued prefix-wallet result")
		}
		if mode, schema, err := prefixLeaseSource(lease); mode != 0 || schema != "" || err != fundinglotscan.ErrInput {
			t.Fatal("nil or zero lease yielded caller-selected source identity")
		}
	}
}

func TestPrefixSourcesMaintenanceDescriptiveFactsGrantNoAuthority(t *testing.T) {
	query := &completeSourcesEmptyQuery{}
	facts, err := fundinglotscan.ReadPrefix(context.Background(), query, "private_schema", fundinglotscan.ModeOrdinary)
	report := facts.Summary()
	if err != nil || query.calls != 71 || report.Scope != "complete_business_source_prefix_graphs_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 71 || report.ScannedColumns != 635 {
		t.Fatal("fixed local prefix-source fixture lost complete physical scope", err)
	}
	for _, result := range []*PrefixSourcesResult{
		nil, {}, {facts: facts, mode: fundinglotscan.ModeOrdinary},
		{proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeOrdinary, proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeSandbox, proof: &maintenancetrust.ScanProof{}},
	} {
		permit := &maintenancetrust.Permit{}
		if result.Verify(permit) == nil {
			t.Fatal("untrusted local prefix facts fabricated source authority")
		}
		if got, err := result.Summary(permit); err == nil || !reflect.DeepEqual(got, fundinglotscan.PrefixReport{}) {
			t.Fatal("unverified prefix-source summary leaked")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			if err != nil || string(raw) != `{"prefix_sources_result":"redacted"}` || !strings.Contains(fmt.Sprintf("%v %#v", result, result), "redacted") {
				t.Fatal("prefix-source result exposed private state")
			}
		}
	}
	var decoded PrefixSourcesResult
	if json.Unmarshal([]byte(`{"facts":{"scope":"complete_business_source_prefix_graphs_v1","scanned_tables":71},"mode":1,"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON fabricated prefix-source authority")
	}
	assertPrefixResultPrivate(t, reflect.TypeFor[PrefixSourcesResult]())
}

func TestPrefixWalletMaintenanceDescriptiveFactsGrantNoAuthority(t *testing.T) {
	query := &completeSourcesEmptyQuery{}
	facts, err := fundinglotscan.ReadWalletPrefix(context.Background(), query, "private_schema", fundinglotscan.ModeOrdinary)
	report := facts.Summary()
	if err != nil || query.calls != 87 || report.Scope != "complete_source_wallet_prefix_graphs_v1" || !report.PhysicalEOFComplete || report.ScannedTables != 87 || report.ScannedColumns != 804 || report.Status != "mismatch" {
		t.Fatal("local prefix-wallet fixture lost physical coverage or missing-epoch mismatch", err)
	}
	for _, result := range []*PrefixWalletResult{
		nil, {}, {facts: facts, mode: fundinglotscan.ModeOrdinary},
		{proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeOrdinary, proof: &maintenancetrust.ScanProof{}},
		{facts: facts, mode: fundinglotscan.ModeSandbox, proof: &maintenancetrust.ScanProof{}},
	} {
		permit := &maintenancetrust.Permit{}
		if result.Verify(permit) == nil {
			t.Fatal("untrusted local prefix facts fabricated wallet authority")
		}
		if got, err := result.Summary(permit); err == nil || !reflect.DeepEqual(got, fundinglotscan.PrefixWalletReport{}) {
			t.Fatal("unverified prefix-wallet summary leaked")
		}
		if result != nil {
			raw, err := json.Marshal(result)
			if err != nil || string(raw) != `{"prefix_wallet_result":"redacted"}` || !strings.Contains(fmt.Sprintf("%v %#v", result, result), "redacted") {
				t.Fatal("prefix-wallet result exposed private state")
			}
		}
	}
	var decoded PrefixWalletResult
	if json.Unmarshal([]byte(`{"facts":{"scope":"complete_source_wallet_prefix_graphs_v1","scanned_tables":87},"mode":1,"proof":{}}`), &decoded) != nil || decoded.Verify(&maintenancetrust.Permit{}) == nil {
		t.Fatal("JSON fabricated prefix-wallet authority")
	}
	assertPrefixResultPrivate(t, reflect.TypeFor[PrefixWalletResult]())
}

func assertPrefixResultPrivate(t *testing.T, typ reflect.Type) {
	t.Helper()
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).IsExported() {
			t.Fatal("prefix result exports raw facts, mode or proof")
		}
	}
}

func TestPrefixMaintenanceRetainsFirstSafeReadFailure(t *testing.T) {
	for _, tc := range []struct{ cause, input, want error }{
		{maintenancetrust.ErrTimeout, fundinglotscan.ErrCanceled, fundinglotscan.ErrTimeout},
		{fundinglotscan.ErrResource, fundinglotscan.ErrCanceled, fundinglotscan.ErrResource},
		{maintenancetrust.ErrExecution, fundinglotscan.ErrCanceled, fundinglotscan.ErrExecution},
		{maintenancetrust.ErrInput, fundinglotscan.ErrCanceled, fundinglotscan.ErrInput},
		{nil, fundinglotscan.ErrCanceled, fundinglotscan.ErrCanceled},
		{errors.New("private SQL / DSN / driver details"), fundinglotscan.ErrCanceled, fundinglotscan.ErrExecution},
		{maintenancetrust.ErrInput, fundinglotscan.ErrResource, fundinglotscan.ErrResource},
	} {
		query := &completeSourcesLeaseQuery{failure: tc.cause}
		got := prefixLeaseReadFailure(nil, query, tc.input)
		if got != tc.want || errors.Unwrap(got) != nil || strings.Contains(got.Error(), "private") {
			t.Fatal("prefix reader lost fixed failure cause or exposed driver details", got)
		}
	}
}
