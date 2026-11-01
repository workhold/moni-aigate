// payment-sandbox-qa builds and exercises the isolated sandbox as real processes.
// It never starts the ordinary gateway or connects a payment to application funds.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

type failure string

func (e failure) Error() string { return string(e) }

type reporter struct{ out io.Writer }

func (r reporter) pass(step string) { _, _ = fmt.Fprintln(r.out, "PASS", step) }
func (r reporter) skip(step string) { _, _ = fmt.Fprintln(r.out, "SKIP", step) }

func main() {
	life, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(life, os.Args[1:], os.Stdout); err != nil {
		// Only this package's fixed stage codes may be written to the report.
		code := "payment_sandbox_qa_failed"
		var safe failure
		if errors.As(err, &safe) {
			code = string(safe)
		}
		fmt.Fprintln(os.Stderr, "FAIL", code)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) (result error) {
	f := flag.NewFlagSet("payment-sandbox-qa", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	require := f.Bool("require-database", false, "fail instead of skip if the dedicated database is absent")
	long := f.Bool("long-retry", false, "also wait for twenty real callback attempts (about 78 minutes)")
	if f.Parse(args) != nil || f.NArg() != 0 {
		return failure("usage_invalid")
	}
	r := reporter{out}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if *require {
			return failure("dedicated_database_required")
		}
		r.skip("dedicated_database_absent")
		return nil
	}
	repo, err := repositoryRoot()
	if err != nil {
		return err
	}
	privateParent := filepath.Join(repo, ".local")
	if err = os.MkdirAll(privateParent, 0700); err != nil {
		return failure("private_directory_failed")
	}
	private, err := os.MkdirTemp(privateParent, "payment-sandbox-qa-")
	if err != nil {
		return failure("private_directory_failed")
	}
	if err = os.Chmod(private, 0700); err != nil {
		_ = os.RemoveAll(private)
		return failure("private_directory_failed")
	}
	defer func() {
		if os.RemoveAll(private) != nil {
			result = failure("private_directory_cleanup_failed")
		}
	}()
	binary := filepath.Join(private, "moni-payment-sandbox")
	buildCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/moni-payment-sandbox")
	build.Dir, build.Stdout, build.Stderr = repo, io.Discard, io.Discard
	err = build.Run()
	cancel()
	if err != nil {
		return failure("component_build_failed")
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		return failure("binary_digest_failed")
	}
	h := sha256.Sum256(b)
	_, _ = fmt.Fprintln(out, "binary_sha256", hex.EncodeToString(h[:]))
	version := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	version.Dir = repo
	if v, x := version.Output(); x == nil {
		s := strings.TrimSpace(string(v))
		if decoded, x := hex.DecodeString(s); x == nil && len(decoded) == 20 {
			_, _ = fmt.Fprintln(out, "source_commit", s)
		}
	}
	r.pass("component_build")
	shortCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
	err = exercise(shortCtx, dsn, private, binary, r, false)
	stop()
	if err != nil {
		return err
	}
	r.skip("database_commit_reply_loss_no_wire_fault_facility")
	if !*long {
		r.skip("twenty_real_callback_attempts_use_long_retry")
		return nil
	}
	longCtx, stop := context.WithTimeout(ctx, 100*time.Minute)
	defer stop()
	return exercise(longCtx, dsn, private, binary, r, true)
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", failure("repository_unavailable")
	}
	for {
		b, x := os.ReadFile(filepath.Join(dir, "go.mod"))
		if x == nil && strings.Contains(string(b), "module github.com/moni-ai/moni-aigate\n") {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", failure("repository_unavailable")
		}
		dir = next
	}
}

// Every return first reaps all children, closes observation pools, verifies the
// original nonempty financial baseline, then removes only this environment.
func exercise(ctx context.Context, dsn, private, binary string, report reporter, long bool) (result error) {
	env, err := paymentsandboxqa.Create(ctx, dsn, private)
	if err != nil {
		return failure("offline_environment_failed")
	}
	_, _ = fmt.Fprintln(report.out, "environment_id", env.Protocol.ID)
	s := &session{ctx: ctx, env: env, binary: binary, report: report}
	defer func() {
		if err := s.cleanup(); err != nil {
			result = err
		}
	}()
	s.mockDB, err = env.OpenPool(ctx, "mock")
	if err != nil {
		return failure("mock_observer_failed")
	}
	s.receiverDB, err = env.OpenPool(ctx, "receiver")
	if err != nil {
		return failure("receiver_observer_failed")
	}
	if long {
		return s.longRetry()
	}
	return s.short()
}
