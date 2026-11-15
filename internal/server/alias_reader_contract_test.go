package server

import (
	"strings"
	"testing"
)

func TestAliasDirectoryQueriesRequireCurrentSchemaAndKeepAuthorization(t *testing.T) {
	for name, query := range map[string]string{
		"inference models": inferenceModelsSQL,
		"console models":   activeModelAliasesSQL,
	} {
		if strings.Contains(query, "to_regclass") || strings.Contains(query, "ModelAliasTableAvailable") {
			t.Fatalf("%s retained legacy table probing", name)
		}
		if !strings.Contains(query, "model_aliases") || !strings.Contains(query, "state='active'") {
			t.Fatalf("%s does not require active aliases from current schema", name)
		}
	}
	for _, required := range []string{"tenant_model_grants", "memberships", "api_keys", "member_model_grants", "m.id=ANY(k.scopes)", "m.enabled"} {
		if !strings.Contains(inferenceModelsSQL, required) {
			t.Fatalf("inference model query lost guard %q", required)
		}
	}
	if strings.Contains(inferenceModelsSQL, "upstream_model") || strings.Contains(inferenceModelsSQL, "secret") {
		t.Fatal("inference model directory contains upstream or secret fields")
	}
}
