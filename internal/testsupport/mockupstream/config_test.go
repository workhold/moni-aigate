package mockupstream

import "testing"

func TestConfigValidationAndCandidateNames(t *testing.T) {
	cfg := Config{Candidates: map[string]Candidate{
		"b": {Secret: "b", Models: []string{"m"}, OutputTokens: 1},
		"a": {Secret: "a", Models: []string{"m"}, OutputTokens: 1},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := cfg.CandidateNames(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("candidate names = %#v", got)
	}
	bad := cfg
	bad.Candidates = map[string]Candidate{"a": {Secret: "secret", Models: []string{"m"}, OutputTokens: 1, DefaultFault: "unknown"}}
	if err := bad.Validate(); err == nil {
		t.Fatal("unknown default fault accepted")
	}
}

func TestFaultGrammarCoversContract(t *testing.T) {
	for _, name := range []string{
		"reject_429", "reject_429_unlisted", "http_500", "drop_after_headers",
		"drop_after_events:2", "stall_first_event", "missing_usage", "conflicting_usage",
		"error_after_usage", "reasoning_events", "invalid_json", "slow_ttfb:25",
	} {
		if _, err := parseFault(name); err != nil {
			t.Errorf("fault %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"drop_after_events:-1", "slow_ttfb:x", "not_a_fault"} {
		if _, err := parseFault(name); err == nil {
			t.Errorf("fault %q accepted", name)
		}
	}
}
