package periodicbudget

import "testing"

func TestFingerprintIndependentPostgresPythonGolden(t *testing.T) {
	const actor = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const scope = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const target = "/api/keys/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget"
	in := PutPeriodicBudget{actor, "9007199254740993", "utc_month", false, "9223372036854775807"}
	got, err := Fingerprint("key_periodic_budget.configure", actor, scope, target, in)
	if err != nil || got != "2a00c71a0d596018c8ae1fdba6be1c70bcb2385548b96f9b32ddd2ecb0b3d34d" {
		t.Fatalf("fingerprint golden mismatch: %s %v", got, err)
	}
	in.OperationID = scope
	replayed, err := Fingerprint("key_periodic_budget.configure", actor, scope, target, in)
	if err != nil || replayed != got {
		t.Fatal("operation ID participated in fingerprint")
	}
	in.Enabled = true
	changed, err := Fingerprint("key_periodic_budget.configure", actor, scope, target, in)
	if err != nil || changed == got {
		t.Fatal("boolean absent from fingerprint")
	}
	for _, parts := range [][4]string{
		{"unknown", actor, scope, target},
		{"key_periodic_budget.configure", actor, "platform", target},
		{"key_periodic_budget.configure", "AAAAAAAa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", scope, target},
		{"key_periodic_budget.configure", actor, scope, target + "/"},
		{"key_periodic_budget.configure", actor, scope, target + "?query=1"},
		{"key_periodic_budget.configure", actor, scope, "https://example.invalid" + target},
		{"tenant_periodic_budget.configure", actor, "platform", target},
	} {
		if out, err := Fingerprint(parts[0], parts[1], parts[2], parts[3], in); out != "" || ErrorCode(err) != "invalid_request" {
			t.Fatal("noncanonical command identity accepted")
		}
	}
}
