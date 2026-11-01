package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestConfigurationAbsenceAndUnsafeArgumentNeverLeaks(t *testing.T) {
	t.Setenv("TEST_DATABASE_URL", "")
	var out bytes.Buffer
	if err := run(context.Background(), nil, &out); err != nil || out.String() != "SKIP dedicated_database_absent\n" {
		t.Fatal("missing dedicated configuration must be an explicit skip")
	}
	out.Reset()
	if err := run(context.Background(), []string{"--require-database"}, &out); err != failure("dedicated_database_required") || out.Len() != 0 {
		t.Fatal("required dedicated database must fail without a false PASS")
	}
	if err := run(context.Background(), []string{"--database-url=credential-must-not-echo"}, &out); err != failure("usage_invalid") || strings.Contains(out.String(), "credential") {
		t.Fatal("unsafe configuration arguments must not reach output")
	}
}

func TestComponentEnvironmentDoesNotInheritAdministratorCredentials(t *testing.T) {
	got := componentEnvironment([]string{"PATH=/safe", "TEST_DATABASE_URL=secret", "DATABASE_URL=secret", "PGPASSWORD=secret", "PGSERVICE=secret", "pgpassfile=secret", "HTTPS_PROXY=secret", "all_proxy=secret", "AUTH_SECRET=secret", "MASTER_KEY=secret", "PROJECTION_DATABASE_URL=secret", "SMTP_PASSWORD=secret", "LC_SECRET=secret", "UNRECOGNIZED_NEW_SECRET=secret", "HOME=/private", "TZ=UTC"})
	if strings.Join(got, "\n") != "PATH=/safe\nHOME=/private\nTZ=UTC" {
		t.Fatal("component inherited environment credentials or transport overrides")
	}
}

func TestUnexpectedChildTerminationCannotPassCleanupOrFinalLiveness(t *testing.T) {
	for _, expected := range []bool{false, true} {
		for _, exitErr := range []error{nil, failure("child_failed")} {
			done := make(chan struct{})
			close(done)
			c := &child{done: done, err: exitErr, expectedExit: expected}
			if failed := (&session{children: []*child{c}}).stopChildren(); failed == expected {
				t.Fatal("cleanup confused expected stop/refusal and unexpected termination")
			}
			if requireRunning(c) == nil {
				t.Fatal("terminated worker was accepted as a live final scheduler")
			}
		}
	}
	if requireRunning(nil) == nil || requireRunning(&child{}) == nil {
		t.Fatal("missing worker was accepted as live")
	}
	if requireRunning(&child{done: make(chan struct{})}) != nil {
		t.Fatal("live worker was rejected")
	}
}

func realFailedSequence() []failedAttempt {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var all []failedAttempt
	for i := 1; i <= 20; i++ {
		if i > 1 {
			start = all[i-2].observed.Add(retryDelay(i - 1))
		}
		observed := start.Add(time.Millisecond)
		outcome, code := "failed", "network"
		all = append(all, failedAttempt{generation: i, number: i, delivery: uuid.NewString(), digest: "fixture-digest", claimed: start, lease: start.Add(time.Minute), observed: &observed, outcome: &outcome, code: &code})
	}
	return all
}

func TestLongRetryOracleRejectsArtificialOrInconsistentEvidence(t *testing.T) {
	if err := validateFailedAttempts(realFailedSequence(), "fixture-digest"); err != nil {
		t.Fatal("complete real-delay sequence was rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func([]failedAttempt) []failedAttempt
	}{
		{"early_claim", func(a []failedAttempt) []failedAttempt { a[1].claimed = a[0].observed.Add(time.Millisecond); return a }},
		{"changed_body", func(a []failedAttempt) []failedAttempt { a[7].digest = "other"; return a }},
		{"reused_delivery", func(a []failedAttempt) []failedAttempt { a[7].delivery = a[1].delivery; return a }},
		{"unobserved_prior_claim", func(a []failedAttempt) []failedAttempt { a[1].observed = nil; return a }},
		{"synthetic_http_failure", func(a []failedAttempt) []failedAttempt { status := 503; a[1].status = &status; return a }},
		{"twenty_first", func(a []failedAttempt) []failedAttempt { return append(a, a[19]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if validateFailedAttempts(tc.mutate(realFailedSequence()), "fixture-digest") == nil {
				t.Fatal("invalid runtime evidence was accepted")
			}
		})
	}
}

// This is a genuine executable/process/HTTP test. SQL/repository fixtures and
// unit oracle vectors above are never counted as process acceptance.
func TestShortActualProcessesPG(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Minute)
	defer stop()
	var out bytes.Buffer
	if err := run(ctx, []string{"--require-database"}, &out); err != nil {
		t.Fatalf("actual-process QA failed: %v\n%s", err, out.String())
	}
	for _, required := range []string{"PASS actual_mock_receiver_processes_and_three_listeners", "PASS both_second_instances_refused", "PASS actual_callback_committed_ack_and_stable_duplicate_receipt", "PASS sigterm_restart_same_manifest_after_confirmed_commits", "PASS sigkill_restart_same_manifest_after_confirmed_commits", "PASS nonempty_application_funding_digest_unchanged", "PASS exact_environment_cleanup", "SKIP database_commit_reply_loss_no_wire_fault_facility", "SKIP twenty_real_callback_attempts_use_long_retry"} {
		if !strings.Contains(out.String(), required+"\n") {
			t.Fatalf("missing honest coverage result %q", required)
		}
	}
}
