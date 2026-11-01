package maintenancetrust

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func pureMaintenanceIdentity() maintenanceIdentity {
	return maintenanceIdentity{databaseName: "moni_test", databaseOID: 9, databaseOwner: 10, loginName: "owner", sessionName: "owner", loginOID: 10, login: true,
		schemaName: "target", currentSchema: "target", schemaOID: 11, rawOwnerName: "owner", rawOwnerOID: 10, effectiveOwnerName: "owner", effectiveOwnerOID: 10,
		serverAddress: "127.0.0.1", remoteAddress: "127.0.0.1", serverPort: 55439, remotePort: 55439}
}

func TestAuthorityMaintenanceIdentityRequiresActualOwnerAndEndpoint(t *testing.T) {
	base := pureMaintenanceIdentity()
	if !base.matches("owner", "127.0.0.1", purePins()) {
		t.Fatal("valid ordinary identity")
	}
	for _, tc := range []struct {
		name   string
		change func(*maintenanceIdentity)
	}{
		{"not LOGIN", func(v *maintenanceIdentity) { v.login = false }},
		{"session authorization", func(v *maintenanceIdentity) { v.sessionName = "other" }},
		{"role switch", func(v *maintenanceIdentity) { v.loginName = "other" }},
		{"owner membership only", func(v *maintenanceIdentity) { v.loginOID = 99 }},
		{"unowned superuser", func(v *maintenanceIdentity) { v.loginOID = 99; v.superuser = true }},
		{"raw owner OID", func(v *maintenanceIdentity) { v.rawOwnerOID = 99 }},
		{"effective owner OID", func(v *maintenanceIdentity) { v.effectiveOwnerOID = 99 }},
		{"raw owner name", func(v *maintenanceIdentity) { v.rawOwnerName = "other" }},
		{"schema", func(v *maintenanceIdentity) { v.schemaOID = 99 }},
		{"current schema", func(v *maintenanceIdentity) { v.currentSchema = "other" }},
		{"database", func(v *maintenanceIdentity) { v.databaseOID = 99 }},
		{"server IP", func(v *maintenanceIdentity) { v.serverAddress = "127.0.0.2" }},
		{"remote IP", func(v *maintenanceIdentity) { v.remoteAddress = "127.0.0.2" }},
		{"server port", func(v *maintenanceIdentity) { v.serverPort = 5432 }},
		{"remote port", func(v *maintenanceIdentity) { v.remotePort = 5432 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.change(&v)
			if v.matches("owner", "127.0.0.1", purePins()) {
				t.Fatal("identity drift accepted")
			}
		})
	}
	if base.matches("other", "127.0.0.1", purePins()) || base.matches("owner", "localhost", purePins()) {
		t.Fatal("config mismatch accepted")
	}
	if !reflect.TypeOf(base).Comparable() {
		t.Fatal("exit identity cannot be compared in full")
	}
}

func TestAuthorityPublicOwnerUsesActualDatabaseOwner(t *testing.T) {
	p := purePins()
	p.AppSchema.Name = "public"
	p.AppSchema.NamespaceOwner.Name = "pg_database_owner"
	p.AppSchema.NamespaceOwner.OID = 6171
	id := pureMaintenanceIdentity()
	id.schemaName = "public"
	id.currentSchema = "public"
	id.rawOwnerName = "pg_database_owner"
	id.rawOwnerOID = 6171
	if !id.matches("owner", "127.0.0.1", p) {
		t.Fatal("builtin database-owner resolution")
	}
	id.databaseOwner = 99
	if id.matches("owner", "127.0.0.1", p) {
		t.Fatal("public resolved to caller instead of actual datdba")
	}
}

func TestAuthoritySandboxInstallerIsNarrowActualDBOwner(t *testing.T) {
	p := pureSandboxPins()
	id := pureMaintenanceIdentity()
	id.loginName = "moni_test"
	id.sessionName = "moni_test"
	id.loginOID = 99
	id.databaseOwner = 99
	id.superuser = true
	if !id.matches("moni_test", "127.0.0.1", p) {
		t.Fatal("valid sandbox installer")
	}
	for _, change := range []func(*maintenanceIdentity){
		func(v *maintenanceIdentity) { v.superuser = false }, func(v *maintenanceIdentity) { v.databaseOwner = 10 },
		func(v *maintenanceIdentity) { v.login = false }, func(v *maintenanceIdentity) { v.remoteAddress = "::1"; v.serverAddress = "::1" },
		func(v *maintenanceIdentity) { v.databaseName = "other" }, func(v *maintenanceIdentity) { v.serverPort = 5432 },
	} {
		v := id
		change(&v)
		if v.matches("moni_test", "127.0.0.1", p) {
			t.Fatal("sandbox exception expanded")
		}
	}
	if id.matches("other", "127.0.0.1", p) || id.matches("moni_test", "localhost", p) {
		t.Fatal("sandbox config alias accepted")
	}
}

func TestAuthorityRuntimeRoleSetIsCompleteAndClosed(t *testing.T) {
	ids, err := maintenanceRuntimeOIDs(purePins())
	if err != nil || !reflect.DeepEqual(ids, []uint32{12, 13}) {
		t.Fatal("ordinary runtime set")
	}
	ids, err = maintenanceRuntimeOIDs(pureSandboxPins())
	if err != nil || !reflect.DeepEqual(ids, []uint32{12, 13, 14, 18, 20}) {
		t.Fatal("sandbox five-runtime set")
	}
	p := pureSandboxPins()
	p.Worker.OID = p.App.OID
	if _, err = maintenanceRuntimeOIDs(p); err != ErrIdentity {
		t.Fatal("duplicate runtime identity accepted")
	}
}

func TestAuthorityLeafErrorsAndRedaction(t *testing.T) {
	if authorityLeafFailure(nil, ErrSchema) != ErrInput {
		t.Fatal("nil context")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if authorityLeafFailure(ctx, ErrIdentity) != ErrTimeout {
		t.Fatal("cancellation precedence")
	}
	for _, v := range []any{pureMaintenanceIdentity(), manifestEvidence{}, accountsEvidence{}} {
		b, err := json.Marshal(v)
		if err != nil || len(b) == 0 {
			t.Fatal("redacted encoding")
		}
		if fmt.Sprintf("%#v", v) == "" {
			t.Fatal("redacted formatting")
		}
		for _, secret := range []string{"moni_test", "127.0.0.1", "target", "owner"} {
			if containsAuthorityText(string(b), secret) || containsAuthorityText(fmt.Sprintf("%#v", v), secret) {
				t.Fatal("private authority identity leaked")
			}
		}
	}
}

func containsAuthorityText(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
