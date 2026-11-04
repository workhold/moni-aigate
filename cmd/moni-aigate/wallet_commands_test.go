package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestWalletCommandsValidateOfflineIntentWithoutServingConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MIGRATION_DATABASE_URL", "")
	t.Setenv("MAINTENANCE_MODE", "")
	args := []string{"--tenant", uuid.NewString(), "--actor", uuid.NewString(), "--reference", "opening:approved-1", "--amount", "17"}
	var output bytes.Buffer
	err := runWalletSourcesCommand("open-wallet", args, &output)
	if err == nil || !strings.Contains(err.Error(), "MAINTENANCE_MODE=true") || output.Len() != 0 {
		t.Fatal(err, output.String())
	}
	t.Setenv("MAINTENANCE_MODE", "true")
	for _, amount := range []string{"0", "-1", "+17", "017", "1.5", "9223372036854775808"} {
		bad := append([]string(nil), args...)
		bad[len(bad)-1] = amount
		if err = runWalletSourcesCommand("open-wallet", bad, &output); err == nil || output.Len() != 0 {
			t.Fatal("noncanonical amount accepted", amount, err)
		}
	}
	if err = runWalletSourcesCommand("check-wallet-sources", []string{"--tenant", "other-tenant"}, &output); err == nil {
		t.Fatal("invalid scope accepted")
	}
	if err = runWalletSourcesCommand("check-wallet-sources", nil, &output); err == nil || !strings.Contains(err.Error(), "database configuration required") {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "postgres://secret-user:secret-password@/private?bad%escape")
	err = runWalletSourcesCommand("check-wallet-sources", nil, &output)
	if err == nil || strings.Contains(err.Error(), "secret-") || strings.Contains(err.Error(), "postgres://") {
		t.Fatal("connection details leaked", err)
	}
}
