package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// A verified finding has already been printed as JSON. It is a distinct CLI
// outcome, not an execution failure and not permission to repair business data.
type periodicVerificationExit struct{ code int }

func (e *periodicVerificationExit) Error() string { return "periodic budget verification finding" }

func periodicVerificationFailure(err error) error {
	if err == nil {
		return nil
	}
	var finding *periodicVerificationExit
	if errors.As(err, &finding) {
		return err
	}
	code := "execution"
	for _, candidate := range []struct {
		err  error
		code string
	}{{periodicbudget.ErrVerifyInput, "input"}, {periodicbudget.ErrVerifyAuthority, "authority"}, {periodicbudget.ErrVerifySchema, "schema"}, {periodicbudget.ErrVerifyTimeout, "timeout"}} {
		if errors.Is(err, candidate.err) {
			code = candidate.code
			break
		}
	}
	return fmt.Errorf("periodic_budget_verification_failed:%s", code)
}

func periodicVerificationTimeout(args []string) (time.Duration, error) {
	flags := flag.NewFlagSet("verify-periodic-budgets", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	timeout := flags.Duration("timeout", 30*time.Second, "maximum complete read-only verification duration")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *timeout < time.Second || *timeout > 10*time.Minute {
		return 0, periodicbudget.ErrVerifyInput
	}
	return *timeout, nil
}

// This offline command deliberately branches before serving configuration,
// ownership, workers and listeners. Only its explicit DATABASE_URL is needed.
func runPeriodicVerification(args []string, output io.Writer) error {
	timeout, err := periodicVerificationTimeout(args)
	if err != nil {
		return err
	}
	raw := os.Getenv("DATABASE_URL")
	if raw == "" {
		return periodicbudget.ErrVerifyInput
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return periodicbudget.ErrVerifyInput
	}
	cfg.MaxConns = 1
	cfg.MinConns = 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return periodicbudget.ErrVerifyExecution
	}
	defer pool.Close()
	verifier, err := periodicbudget.NewCurrentVerifier(pool, periodicbudget.VerifyOptions{Timeout: timeout})
	if err != nil {
		return err
	}
	result, err := verifier.Verify(ctx)
	if err != nil {
		return err
	}
	return writePeriodicVerification(output, result)
}

func writePeriodicVerification(output io.Writer, result periodicbudget.Verification) error {
	code := 0
	switch result.Status {
	case periodicbudget.VerificationBalanced:
		if !result.Balanced || !result.CoverageComplete {
			return periodicbudget.ErrVerifyExecution
		}
	case periodicbudget.VerificationMismatch:
		if result.Balanced {
			return periodicbudget.ErrVerifyExecution
		}
		code = 2
	case periodicbudget.VerificationIndeterminate:
		if result.Balanced || result.CoverageComplete {
			return periodicbudget.ErrVerifyExecution
		}
		code = 3
	default:
		return periodicbudget.ErrVerifyExecution
	}
	if output == nil || json.NewEncoder(output).Encode(result) != nil {
		return periodicbudget.ErrVerifyExecution
	}
	if code != 0 {
		return &periodicVerificationExit{code: code}
	}
	return nil
}
