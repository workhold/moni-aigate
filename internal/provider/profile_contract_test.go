package provider

import "testing"

func TestProfileRejectsUnsupportedReasoningOutput(t *testing.T) {
	for _, mode := range []string{"summary_v1", "raw_reasoning", "true"} {
		p := fixtureProfile()
		p.Protocol = ProtocolResponses
		p.ReasoningOutput = mode
		if err := p.Validate(); err == nil || err.(*Error).Code != "invalid_profile_reasoning_output" {
			t.Fatalf("reasoning mode %q was not rejected: %v", mode, err)
		}
	}
	for _, mode := range []string{"", "none"} {
		p := fixtureProfile()
		p.Protocol = ProtocolResponses
		p.ReasoningOutput = mode
		if err := p.Validate(); err != nil {
			t.Fatalf("unsupported-mode declaration %q rejected: %v", mode, err)
		}
	}
}
