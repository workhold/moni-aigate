// payment-application-qa installs an isolated offline APP/M/Q/worker environment.
// --prepare-only proves constructors; it does not claim ordinary-main acceptance.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/paymentruntime"
	"github.com/moni-ai/moni-aigate/internal/payments"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

type failure string

func (e failure) Error() string { return string(e) }

type reporter struct{ out io.Writer }

func (r reporter) pass(stage string) { _, _ = fmt.Fprintln(r.out, "PASS", stage) }
func (r reporter) skip(stage string) { _, _ = fmt.Fprintln(r.out, "SKIP", stage) }

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		code := "payment_application_qa_failed"
		var safe failure
		if errors.As(err, &safe) {
			code = string(safe)
		}
		_, _ = fmt.Fprintln(os.Stderr, "FAIL", code)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) (result error) {
	flags := flag.NewFlagSet("payment-application-qa", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	require := flags.Bool("require-database", false, "fail when the dedicated test database is absent")
	prepare := flags.Bool("prepare-only", false, "validate the offline environment and constructors without ordinary-main processes")
	process := flags.Bool("process", false, "exercise real ordinary-main and mock processes; requires the dedicated database InstanceLock window")
	hold := flags.Bool("browser-hold", false, "hold the final process for private browser QA until a stop file or signal")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return failure("usage_invalid")
	}
	if *prepare == *process || *hold && !*process {
		return failure("explicit_qa_scope_required")
	}
	report := reporter{out: out}
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if *require {
			return failure("dedicated_database_required")
		}
		report.skip("dedicated_database_absent")
		return nil
	}
	root, err := os.MkdirTemp("", "moni-payment-application-qa-")
	if err != nil {
		return failure("private_directory_failed")
	}
	defer func() {
		if os.RemoveAll(root) != nil {
			result = failure("private_directory_cleanup_failed")
		}
	}()
	if os.Chmod(root, 0700) != nil {
		return failure("private_directory_failed")
	}
	if !filepath.IsAbs(root) {
		return failure("private_directory_failed")
	}
	if *process {
		return exerciseProcesses(ctx, dsn, root, report, *hold)
	}
	life, stop := context.WithTimeout(ctx, 90*time.Second)
	defer stop()
	return prepareEnvironment(life, dsn, root, report)
}

func prepareEnvironment(ctx context.Context, dsn, root string, report reporter) (result error) {
	env, err := paymentsandboxqa.CreateApplication(ctx, dsn, root)
	if err != nil {
		return failure("offline_application_environment_failed")
	}
	_, _ = fmt.Fprintln(report.out, "environment_id", env.Protocol.ID)
	var pools []*pgxpool.Pool
	defer func() {
		for _, pool := range pools {
			pool.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if env.VerifyLegacyFundingUnchanged(cleanup) != nil {
			result = failure("retained_a_funding_changed")
		}
		if env.Close(cleanup) != nil {
			result = failure("environment_cleanup_failed")
		}
		if result == nil {
			report.pass("environment_cleanup")
		}
	}()
	report.pass("offline_builder_eight_roles")
	a, err := payments.LoadSandboxOptions(ctx, env.ApplicationManifestPath)
	if err != nil {
		return failure("application_manifest_failed")
	}
	w, err := payments.LoadWorkerOptions(ctx, env.WorkerManifestPath)
	if err != nil || payments.ValidateOptionsPair(a, w) != nil {
		return failure("paired_manifests_failed")
	}
	report.pass("paired_manifest_validation")
	for _, component := range []string{"app", "projector"} {
		raw, err := env.RuntimeDSN(component)
		if err != nil {
			return failure("runtime_dsn_failed")
		}
		var config *pgxpool.Config
		if component == "app" {
			config, err = payments.ApplicationPoolConfig(ctx, raw, a)
		} else {
			config, err = payments.ProjectionPoolConfig(ctx, raw, a)
		}
		if err != nil {
			return failure("pinned_pool_failed")
		}
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			return failure("runtime_pool_failed")
		}
		pools = append(pools, pool)
		role := operations.PaymentApplicationRole
		if component == "projector" {
			role = operations.PaymentProjectionRole
		}
		if operations.VerifyPaymentRole(ctx, pool, role) != nil {
			return failure("runtime_role_failed")
		}
	}
	life := payments.Lifecycle{Mode: payments.ModeSandbox, Guard: func(context.Context) error { return nil }}
	a, err = a.BindLifecycle(life)
	if err != nil {
		return failure("application_lifecycle_failed")
	}
	w, err = w.BindLifecycle(life)
	if err != nil {
		return failure("worker_lifecycle_failed")
	}
	if _, err = payments.NewService(ctx, pools[0], a); err != nil {
		return failure("application_constructor_failed")
	}
	processor, err := payments.OpenProcessor(ctx, w)
	if err != nil {
		return failure("worker_constructor_failed")
	}
	if processor.Close(ctx) != nil {
		return failure("worker_shutdown_failed")
	}
	report.pass("actual_application_worker_projector_constructors")
	for _, component := range []paymentruntime.Component{paymentruntime.Mock, paymentruntime.Receiver} {
		path := env.MockManifestPath
		if component == paymentruntime.Receiver {
			path = env.ReceiverManifestPath
		}
		options, err := paymentruntime.Load(ctx, path, component)
		if err != nil {
			return failure("peer_manifest_failed")
		}
		runtime, err := paymentruntime.Open(ctx, options)
		if err != nil {
			return failure("peer_constructor_failed")
		}
		checkErr := runtime.Check(ctx)
		closeErr := runtime.Close(ctx)
		if checkErr != nil || closeErr != nil {
			return failure("peer_lifecycle_failed")
		}
	}
	report.pass("original_mock_receiver_identity_proofs")
	if env.VerifyFundsUnchanged(ctx) != nil || env.VerifyLegacyFundingUnchanged(ctx) != nil {
		return failure("retained_a_funding_changed")
	}
	report.pass("retained_a_funding_unchanged")
	report.skip("ordinary_main_process_acceptance_requires_process_suite")
	report.skip("database_commit_reply_loss_no_wire_fault_facility")
	return nil
}
