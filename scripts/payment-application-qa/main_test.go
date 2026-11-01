package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPrepareCLIRequiresExplicitScopeAndDedicatedDatabase(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "")
	for _, args := range [][]string{nil, {"--unknown=secret"}, {"--prepare-only", "extra"}, {"--prepare-only", "--process"}, {"--prepare-only", "--browser-hold"}} {
		var out bytes.Buffer
		if run(context.Background(), args, &out) == nil || out.Len() != 0 {
			t.Fatal("invalid flags must fail without echo")
		}
	}
	var out bytes.Buffer
	if run(context.Background(), []string{"--prepare-only"}, &out) != nil || out.String() != "SKIP dedicated_database_absent\n" {
		t.Fatal("database skip scope")
	}
	out.Reset()
	err := run(context.Background(), []string{"--prepare-only", "--require-database"}, &out)
	if err != failure("dedicated_database_required") || strings.Contains(out.String(), "secret") {
		t.Fatal("database requirement")
	}
}

func TestProcessEnvironmentDoesNotInheritRuntimeSecretsOrTransport(t *testing.T) {
	got := processEnvironment([]string{"PATH=/safe", "HOME=/private", "TZ=UTC", "DATABASE_URL=secret", "TEST_DATABASE_URL=secret", "PGSERVICE=secret", "PGOPTIONS=secret", "HTTP_PROXY=secret", "HTTPS_PROXY=secret", "ALL_PROXY=secret", "NO_PROXY=secret", "MASTER_KEY=secret", "AUTH_SECRET=secret", "PAYMENTS_APP_MANIFEST=secret", "CONTROL_TOKEN=secret"})
	if strings.Join(got, "\n") != "PATH=/safe\nHOME=/private\nTZ=UTC" {
		t.Fatal("child inherited non-allowlisted environment")
	}
}
