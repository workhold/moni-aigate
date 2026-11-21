package server

import (
	"encoding/json"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/routing"
)

func TestChatRouteEnvelopeIsStrictAndDetached(t *testing.T) {
	raw := []byte(`{"model":"retail-model","messages":[{"role":"user","content":"hello"}],"route":{"order":["physical-a"],"only":["channel-a"],"ignore":["channel-b"],"allow_fallbacks":false,"require_parameters":true,"sort":"price","max_price":{"input":"10","output":"20"},"data_collection":"deny"}}`)
	stripped, shape, err := parseChatRoute(raw)
	if err != nil {
		t.Fatal(err)
	}
	if shape.Preference.AllowFallbacks || !shape.Preference.RequireParameters || shape.Preference.Sort != "price" || shape.Preference.MaxPrice.Input == nil || *shape.Preference.MaxPrice.Input != "10" || shape.Preference.DataCollection != "deny" {
		t.Fatalf("route preference=%+v", shape)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(stripped, &fields) != nil {
		t.Fatal(err)
	}
	if _, exists := fields["route"]; exists {
		t.Fatal("route leaked into the provider envelope")
	}
	invalid := []string{
		`{"model":"m","route":null}`,
		`{"model":"m","route":{"unknown":true}}`,
		`{"model":"m","route":{"sort":"quality"}}`,
		`{"model":"m","route":{"allow_fallbacks":null}}`,
		`{"model":"m","route":{"only":[]}}`,
		`{"model":"m","route":{"max_price":{"input":null}}}`,
		`{"model":"m","route":{"max_price":{"input":"01"}}}`,
		`{"model":"m","route":{"order":["a","a"]}}`,
		`{"model":"m","route":{"only":["a"],"only":["b"]}}`,
	}
	for _, raw := range invalid {
		if _, _, err := parseChatRoute([]byte(raw)); err == nil {
			t.Fatalf("invalid route accepted: %s", raw)
		}
	}
}

func TestRouteSelectorsRequireKnownNonemptyIntersection(t *testing.T) {
	candidates := []routing.Candidate{{BindingID: "b1", ChannelID: "c1", PhysicalModelID: "p1", Model: "upstream"}}
	if validateRouteSelectors(candidates, routing.RoutePreference{Only: []string{"p1"}, Ignore: []string{"c1"}}) != nil {
		t.Fatal("known selectors were rejected")
	}
	for _, preference := range []routing.RoutePreference{{Only: []string{"unknown"}}, {Order: []string{"unknown"}}, {Ignore: []string{"unknown"}}, {Only: []string{"c2"}}} {
		if validateRouteSelectors(candidates, preference) == nil {
			t.Fatalf("unknown or empty selector accepted: %+v", preference)
		}
	}
}
