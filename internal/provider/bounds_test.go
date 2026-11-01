package provider

import "testing"

func TestIndependentInputAndOutputBoundsRemainConservative(t *testing.T) {
	for _, protocol := range []string{ProtocolChat, ProtocolResponses, ProtocolMessages} {
		t.Run(protocol, func(t *testing.T) {
			p := Profile{Version: "bounds-v1", Model: "model", Protocol: protocol, InputTokenUpperBound: 120, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20}
			raw := []byte(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`)
			if protocol == ProtocolResponses {
				raw = []byte(`{"model":"model","input":"hello","store":false}`)
			}
			r, err := Parse(protocol, raw, p)
			if err != nil || r.InputUpperBound != 120 || r.MaxOutputTokens != 20 {
				t.Fatalf("independent maxima rejected: %v %+v", err, r)
			}
			p.InputTokenUpperBound = 121
			if p.Validate() == nil {
				t.Fatal("input bound exceeded context")
			}
			p.InputTokenUpperBound = 120
			p.OutputLimit = 121
			if p.Validate() == nil {
				t.Fatal("output bound exceeded context")
			}
		})
	}
}
