package catalog

import (
	"strings"
	"testing"
)

func TestAliasReaderSQLRequiresCurrentSchemaAndPreservesAuthorization(t *testing.T) {
	if !strings.Contains(modelNameLockSQL, "hashtextextended('model-name:'||$1,0)") {
		t.Fatal("model creation does not use the current phase1 name lock")
	}
	for name, query := range map[string]string{
		"authorized model":        authorizedModelSQL,
		"public alias projection": activePublicModelAliasesSQL,
	} {
		if strings.Contains(query, "to_regclass") || strings.Contains(query, "ModelAliasTableAvailable") {
			t.Fatalf("%s retained legacy table probing", name)
		}
		if !strings.Contains(query, "model_aliases") || !strings.Contains(query, "state='active'") {
			t.Fatalf("%s does not require active aliases from current schema", name)
		}
	}
	for _, required := range []string{"tenant_model_grants", "memberships", "api_keys", "member_model_grants", "m.id=ANY(k.scopes)", "m.enabled"} {
		if !strings.Contains(authorizedModelSQL, required) {
			t.Fatalf("authorized model query lost guard %q", required)
		}
	}
}
