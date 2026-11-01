package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

func TestPeriodicVerificationArgumentsBeforeDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, args := range [][]string{{"--timeout", "0s"}, {"--timeout", "999ms"}, {"--timeout", "11m"}, {"--timeout", "bad"}, {"--repair"}, {"tenant-id"}} {
		var output bytes.Buffer
		if err := runPeriodicVerification(args, &output); err == nil || output.Len() != 0 {
			t.Fatal("invalid input did not fail before database access")
		}
	}
	for _, test := range []struct {
		args []string
		want time.Duration
	}{{nil, 30 * time.Second}, {[]string{"--timeout", "1s"}, time.Second}, {[]string{"--timeout=10m"}, 10 * time.Minute}} {
		got, err := periodicVerificationTimeout(test.args)
		if err != nil || got != test.want {
			t.Fatal("valid whole-scan budget rejected")
		}
	}
	if err := runPeriodicVerification(nil, &bytes.Buffer{}); err == nil {
		t.Fatal("missing explicit offline database URL accepted")
	}
}

func TestPeriodicVerificationMachineOutcomes(t *testing.T) {
	for _, test := range []struct {
		status periodicbudget.VerificationStatus
		code   int
		full   bool
	}{{periodicbudget.VerificationBalanced, 0, true}, {periodicbudget.VerificationMismatch, 2, true}, {periodicbudget.VerificationMismatch, 2, false}, {periodicbudget.VerificationIndeterminate, 3, false}} {
		v := periodicbudget.Verification{SchemaVersion: 1, Status: test.status, Balanced: test.code == 0, CoverageComplete: test.full}
		if test.code == 2 {
			v.Mismatches.MissingAllocations = 9007199254740993
		}
		var output bytes.Buffer
		err := writePeriodicVerification(&output, v)
		if test.code == 0 && err != nil {
			t.Fatal(err)
		}
		if test.code != 0 {
			var finding *periodicVerificationExit
			if !errors.As(err, &finding) || finding.code != test.code {
				t.Fatal("finding lost its machine exit status")
			}
		}
		var raw map[string]any
		if json.Unmarshal(output.Bytes(), &raw) != nil || raw["status"] != string(test.status) || raw["coverage_complete"] != test.full {
			t.Fatal("invalid report envelope")
		}
		counts := raw["mismatches"].(map[string]any)
		want := "0"
		if test.code == 2 {
			want = "9007199254740993"
		}
		if counts["missing_allocations"] != want {
			t.Fatal("counter lost decimal string precision")
		}
	}
}

type periodicBrokenWriter struct{}

func (periodicBrokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic write failure")
}

func TestPeriodicVerificationOutputFailureIsExecutionFailure(t *testing.T) {
	for _, writer := range []interface{ Write([]byte) (int, error) }{nil, periodicBrokenWriter{}} {
		err := writePeriodicVerification(writer, periodicbudget.Verification{Status: periodicbudget.VerificationMismatch})
		var outcome *periodicVerificationExit
		if !errors.Is(err, periodicbudget.ErrVerifyExecution) || errors.As(err, &outcome) {
			t.Fatal("failed output was reported as a completed finding")
		}
	}
}

func TestPeriodicVerificationRejectsInconsistentStatusWithoutOutput(t *testing.T) {
	for _, v := range []periodicbudget.Verification{{Status: "unknown"}, {Status: periodicbudget.VerificationBalanced}, {Status: periodicbudget.VerificationMismatch, Balanced: true}, {Status: periodicbudget.VerificationIndeterminate, CoverageComplete: true}} {
		var output bytes.Buffer
		if !errors.Is(writePeriodicVerification(&output, v), periodicbudget.ErrVerifyExecution) || output.Len() != 0 {
			t.Fatal("invalid report reached output")
		}
	}
}

func TestPeriodicVerificationFailuresStaySafe(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  string
	}{{periodicbudget.ErrVerifyInput, "input"}, {periodicbudget.ErrVerifyAuthority, "authority"}, {periodicbudget.ErrVerifySchema, "schema"}, {periodicbudget.ErrVerifyTimeout, "timeout"}, {periodicbudget.ErrVerifyExecution, "execution"}, {errors.New("untrusted connection detail"), "execution"}} {
		wrapped := fmt.Errorf("private cause: %w", test.cause)
		got := periodicVerificationFailure(wrapped)
		if got == nil || got.Error() != "periodic_budget_verification_failed:"+test.code {
			t.Fatal("CLI failure did not use its fixed safe code")
		}
	}
	if periodicVerificationFailure(nil) != nil {
		t.Fatal("successful verification became an error")
	}
	finding := &periodicVerificationExit{code: 3}
	if periodicVerificationFailure(finding) != finding {
		t.Fatal("finding was converted to an execution error")
	}
}
