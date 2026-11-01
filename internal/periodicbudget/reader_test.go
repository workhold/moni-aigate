package periodicbudget

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReaderConstructionAndStrictSnapshot(t *testing.T) {
	if _, e := NewReader(nil, make([]byte, 32)); ErrorCode(e) != "periodic_budget_unavailable" {
		t.Fatal("nil pool accepted")
	}
	var reader *Reader
	if _, e := reader.PlatformTenantBudget(context.Background(), "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"); ErrorCode(e) != "periodic_budget_unavailable" {
		t.Fatal("nil Reader did not fail closed")
	}
	raw := []byte(`{"scope":"tenant","scope_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","revision":"0","period_kind":null,"enabled":false,"limit_amount":null,"state":"unconfigured","effective_from":null,"tracking_started_at":null,"current_window":null,"carry_held":"0","as_of":"2026-11-01T00:00:00.000000Z"}`)
	if _, e := readDecodeView(raw); e != nil {
		t.Fatal("valid unconfigured snapshot refused")
	}
	for _, change := range []func(map[string]json.RawMessage){
		func(m map[string]json.RawMessage) { delete(m, "enabled") },
		func(m map[string]json.RawMessage) { m["enabled"] = json.RawMessage(`null`) },
		func(m map[string]json.RawMessage) { m["revision"] = json.RawMessage(`0`) },
		func(m map[string]json.RawMessage) { m["unknown"] = json.RawMessage(`false`) },
		func(m map[string]json.RawMessage) { m["current_window"] = json.RawMessage(`{}`) },
	} {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		change(m)
		bad, _ := json.Marshal(m)
		if _, e := readDecodeView(bad); ErrorCode(e) != "periodic_budget_invariant" {
			t.Fatal("inexact snapshot accepted")
		}
	}
}
