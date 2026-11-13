package catalog

import (
	"testing"

	"github.com/google/uuid"
)

func TestModelAliasNameAndResolutionContract(t *testing.T) {
	for _, value := range []string{"gpt-4o", "gpt-4o-2024-08-06", "模型.一"} {
		if !ValidModelAliasName(value) {
			t.Fatalf("valid alias rejected: %q", value)
		}
	}
	for _, value := range []string{"", "has space", "has\nnewline", "has\u2003space", string([]byte{0xff}), string(make([]byte, 101))} {
		if ValidModelAliasName(value) {
			t.Fatalf("invalid alias accepted: %q", value)
		}
	}
	modelID := uuid.NewString()
	aliases := []ModelAlias{{Alias: "gpt-4o", ModelID: modelID, State: "active"}, {Alias: "old", ModelID: modelID, State: "retired"}}
	if got, ok := ResolveModelName("public", "public", modelID, aliases); !ok || got != modelID {
		t.Fatal("public name did not resolve", got, ok)
	}
	if got, ok := ResolveModelName("gpt-4o", "public", modelID, aliases); !ok || got != modelID {
		t.Fatal("active alias did not resolve", got, ok)
	}
	for _, name := range []string{"old", "other"} {
		if got, ok := ResolveModelName(name, "public", modelID, aliases); ok || got != "" {
			t.Fatal("retired or unknown name resolved", name, got, ok)
		}
	}
	if got, ok := ResolveModelName("gpt-4o", "public", uuid.NewString(), aliases); ok || got != "" {
		t.Fatal("alias crossed logical model boundary", got, ok)
	}
}
