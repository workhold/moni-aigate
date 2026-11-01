package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/alerts"
	"github.com/moni-ai/moni-aigate/internal/alertworker"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/maildeliver"
	"github.com/moni-ai/moni-aigate/internal/mailworker"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/payments"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/server"
	"github.com/moni-ai/moni-aigate/internal/telemetry"
	"github.com/moni-ai/moni-aigate/migrations"
	"github.com/moni-ai/moni-aigate/web"
)

var version = "0.1.0-dev"

func main() {
	if e := run(); e != nil {
		var outcome *periodicVerificationExit
		if errors.As(e, &outcome) {
			os.Exit(outcome.code)
		}
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "version" {
		fmt.Println(version)
		return nil
	}
	if command == "init-env" {
		return initEnv()
	}
	if command == "verify-periodic-budgets" {
		return periodicVerificationFailure(runPeriodicVerification(os.Args[2:], os.Stdout))
	}
	c, e := config.Load()
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	dbURL := c.DatabaseURL
	if command == "migrate" && os.Getenv("MIGRATION_DATABASE_URL") != "" {
		dbURL = os.Getenv("MIGRATION_DATABASE_URL")
	}
	paymentStart := paymentStartup{application: payments.DisabledOptions()}
	if command == "serve" {
		paymentStart, e = preparePaymentStartup(ctx, c, os.Getenv("PROJECTION_DATABASE_URL"))
		if e != nil {
			return e
		}
	}
	pc := paymentStart.applicationPool
	if pc == nil {
		pc, e = pgxpool.ParseConfig(dbURL)
	}
	if e != nil {
		return fmt.Errorf("invalid database configuration")
	}
	pc.MaxConns = 32
	if !paymentStart.environment {
		pc.ConnConfig.ConnectTimeout = 5 * time.Second
	}
	pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	pc.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return fmt.Errorf("database initialization failed")
	}
	defer pool.Close()
	if e = pool.Ping(ctx); e != nil {
		return fmt.Errorf("database unavailable")
	}
	if command == "migrate" {
		if e = migrations.Apply(ctx, pool); e != nil {
			return fmt.Errorf("migration failed: %w", e)
		}
		fmt.Println("migrations applied")
		return nil
	}
	if e = migrations.Verify(ctx, pool); e != nil {
		return e
	}
	if command == "bootstrap" {
		flags := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
		email := flags.String("email", "", "initial admin email")
		stdin := flags.Bool("password-stdin", false, "read initial password from stdin")
		if e = flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if *email == "" || !*stdin {
			return fmt.Errorf("usage: moni-aigate bootstrap --email admin@example.com --password-stdin")
		}
		reader := bufio.NewReader(os.Stdin)
		password, e := reader.ReadString('\n')
		if e != nil && len(password) == 0 {
			return fmt.Errorf("unable to read password")
		}
		if e = identity.New(pool, c.Secrets).Bootstrap(ctx, *email, strings.TrimRight(password, "\r\n")); e != nil {
			return fmt.Errorf("bootstrap refused or failed")
		}
		fmt.Println("administrator created; MFA is optional unless enforced by deployment policy")
		return nil
	}
	if command == "provision" {
		if e = operations.Provision(ctx, pool); e != nil {
			return fmt.Errorf("database role provisioning failed")
		}
		fmt.Println("database roles provisioned")
		return nil
	}
	if command == "check-ledger" {
		v, e := billing.New(pool).VerifyLedger(ctx, billing.Query{})
		if e != nil {
			return fmt.Errorf("ledger check failed")
		}
		fmt.Printf("balanced=%v account_mismatches=%d unbalanced_journals=%d\n", v.Balanced, v.AccountMismatches, v.UnbalancedJournals)
		if !v.Balanced {
			return fmt.Errorf("ledger mismatch")
		}
		return nil
	}
	if command == "rebuild-reports" || command == "rewrap-secrets" {
		if os.Getenv("MAINTENANCE_MODE") != "true" {
			return fmt.Errorf("stop app, drain writes and set MAINTENANCE_MODE=true")
		}
		conn, e := pool.Acquire(ctx)
		if e != nil {
			return e
		}
		defer conn.Release()
		var held bool
		if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(782193402)").Scan(&held); e != nil || !held {
			return fmt.Errorf("stop the active app before offline maintenance")
		}
		defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock(782193402)")
		if command == "rewrap-secrets" {
			if e = operations.Rewrap(ctx, pool, c.Secrets); e != nil {
				return fmt.Errorf("rewrap failed; retain the original keyring")
			}
			fmt.Println("credentials rewrapped; retain old keys for retained backups")
			return nil
		}
		flags := flag.NewFlagSet("rebuild-reports", flag.ContinueOnError)
		consumer := flags.String("consumer", "", "new unique projection version")
		if e = flags.Parse(os.Args[2:]); e != nil {
			return e
		}
		if *consumer == "" {
			return fmt.Errorf("--consumer required")
		}
		n, e := billing.New(pool).Rebuild(ctx, *consumer)
		if e != nil {
			return fmt.Errorf("report rebuild failed")
		}
		fmt.Printf("rebuilt %d source events and switched projection\n", n)
		return nil
	}
	if command != "serve" {
		return fmt.Errorf("unknown command")
	}
	required, e := operations.RecoveryRequired(ctx, pool)
	if e != nil {
		return fmt.Errorf("recovery gate unavailable")
	}
	c.RecoveryMode = c.RecoveryMode || required
	instance, e := operations.AcquireInstance(ctx, pool, operations.InstanceLockKey)
	if e != nil {
		return e
	}
	defer instance.Close()
	// Read-only recovery can open immediately. It cannot dispatch, probe or
	// recover funds in the background; reopening paid traffic requires restart.
	if !c.RecoveryMode {
		if e = instance.DrainPredecessor(ctx, pool); e != nil {
			return fmt.Errorf("predecessor drain failed")
		}
	}
	callsCtx, cancelCalls := context.WithCancel(context.Background())
	defer cancelCalls()
	log := telemetry.New(os.Stdout)
	app := server.New(c, pool, web.FS, log)
	if e = app.InitializeAlerts(); e != nil {
		return fmt.Errorf("operations alert service initialization failed")
	}
	commerceStart, finishCommerceStart := context.WithTimeout(ctx, 5*time.Second)
	e = app.InitializeVouchers(commerceStart)
	finishCommerceStart()
	if e != nil {
		return fmt.Errorf("voucher service initialization failed")
	}
	if e = app.InitializePeriodicBudgets(); e != nil {
		return fmt.Errorf("periodic budget service initialization failed")
	}
	app.Guard = func(check context.Context) error {
		err := instance.Check(check)
		if err != nil {
			app.Ready(false)
			cancelCalls()
			cancel()
		}
		return err
	}
	paymentLife := payments.Lifecycle{Guard: app.Guard, Mode: payments.Mode(c.PaymentsMode), Recovery: c.RecoveryMode}
	paymentStart.application, e = paymentStart.application.BindLifecycle(paymentLife)
	if e != nil {
		return fmt.Errorf("payment lifecycle configuration invalid")
	}
	if paymentStart.environment {
		paymentStart.worker, e = paymentStart.worker.BindLifecycle(paymentLife)
		if e != nil || payments.ValidateOptionsPair(paymentStart.application, paymentStart.worker) != nil {
			return fmt.Errorf("payment lifecycle pair invalid")
		}
	}
	if e = app.InitializePayments(ctx, paymentStart.application); e != nil {
		return fmt.Errorf("payment service initialization failed")
	}
	var paymentProcessor *payments.Processor
	var paymentRunner *payments.Runner
	if paymentStart.environment {
		paymentProcessor, e = payments.OpenProcessor(ctx, paymentStart.worker)
		if e != nil {
			return fmt.Errorf("payment processor initialization failed")
		}
		defer func() { _ = paymentProcessor.Close(context.Background()) }()
		paymentRunner, e = payments.NewRunner(paymentProcessor)
		if e != nil {
			return fmt.Errorf("payment runner initialization failed")
		}
		defer func() { _ = paymentRunner.Close(context.Background()) }()
	}
	var projector *billing.Store
	var projectionPool *pgxpool.Pool
	if raw := os.Getenv("PROJECTION_DATABASE_URL"); raw != "" {
		if paymentStart.projectionPool != nil {
			projectionPool, e = pgxpool.NewWithConfig(ctx, paymentStart.projectionPool)
		} else {
			projectionPool, e = pgxpool.New(ctx, raw)
		}
		if e != nil {
			return fmt.Errorf("projection database unavailable")
		}
		defer projectionPool.Close()
		var appUser, projectUser string
		if e = pool.QueryRow(ctx, "SELECT current_user").Scan(&appUser); e != nil {
			return e
		}
		if e = projectionPool.QueryRow(ctx, "SELECT current_user").Scan(&projectUser); e != nil {
			return fmt.Errorf("projection role unavailable")
		}
		if appUser == projectUser {
			return fmt.Errorf("projection role must be separate from serving role")
		}
		// Direct GRANT/REVOKE statements do not remove privileges inherited
		// through another role. Verify the effective alert matrix before any
		// listener or background worker can start, even when evaluation is off.
		roleCheck, finishRoleCheck := context.WithTimeout(ctx, 5*time.Second)
		e = operations.VerifyAlertRole(roleCheck, pool, operations.AlertApplicationRole)
		if e == nil {
			e = operations.VerifyAlertRole(roleCheck, projectionPool, operations.AlertProjectionRole)
		}
		if e != nil {
			finishRoleCheck()
			return fmt.Errorf("operations alert database role verification failed")
		}
		if e == nil {
			e = operations.VerifyPeriodicBudgetRole(roleCheck, pool, operations.PeriodicBudgetApplicationRole)
		}
		if e == nil {
			e = operations.VerifyPeriodicBudgetRole(roleCheck, projectionPool, operations.PeriodicBudgetProjectionRole)
		}
		if e == nil {
			e = operations.VerifyPaymentRole(roleCheck, projectionPool, operations.PaymentProjectionRole)
		}
		finishRoleCheck()
		if e != nil {
			return fmt.Errorf("periodic budget database role verification failed")
		}
		projector = billing.New(projectionPool)
	} else {
		return fmt.Errorf("PROJECTION_DATABASE_URL required with a separate projection database role")
	}
	httpServer := &http.Server{Addr: c.Addr, Handler: app.Engine(), BaseContext: func(net.Listener) context.Context { return callsCtx }, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 65 * time.Second, MaxHeaderBytes: 32 << 10}
	// Identity delivery has its own lifecycle and never occupies the serial
	// financial recovery slot. Disabled transport still permits guarded expiry
	// cleanup, with no sender or delivery origin configured.
	var identityWorker *mailworker.Worker
	onMailError := func(code string) { log.Write(telemetry.Record{Stage: "identity_mail", Code: code}) }
	// A normal SIGTERM cancels the mail lifecycle while existing HTTP calls
	// drain. Finish an already-started ownership check under its own one-second
	// bound, rather than passing shutdown cancellation into the fencing session.
	// Actual database/check failures still run the original fail-closed Guard.
	mailGuard := mailLifecycleGuard(ctx, app.Guard)
	// The observer uses two dedicated APP connections and no queue shared with
	// model requests, financial recovery, projections or identity delivery.
	var observer *alertworker.Worker
	var observerPool *pgxpool.Pool
	if c.OperationsAlertsEnabled && !c.RecoveryMode {
		observerConfig := pool.Config().Copy()
		observerConfig.MaxConns, observerConfig.MinConns = 2, 0
		observerConfig.ConnConfig.RuntimeParams["application_name"] = "moni-aigate-alerts"
		observerPool, e = pgxpool.NewWithConfig(ctx, observerConfig)
		if e != nil {
			return fmt.Errorf("operations alert database unavailable")
		}
		defer observerPool.Close()
		repository, observerErr := alerts.NewStore(observerPool, alerts.RuntimeOptions{DeploymentEnabled: true})
		if observerErr != nil {
			return fmt.Errorf("operations alert repository unavailable")
		}
		observer, e = alertworker.New(repository, alertworker.Options{
			Guard:   mailGuard,
			OnError: func(code string) { log.Write(telemetry.Record{Stage: "operations_alert", Code: code}) },
		})
		if e != nil {
			return fmt.Errorf("operations alert worker unavailable")
		}
	}
	if c.Mail.Configured() {
		smtpSender, mailErr := maildeliver.NewSMTP(c.Mail, c.Origin)
		if mailErr != nil {
			return fmt.Errorf("identity mail configuration unavailable")
		}
		identityWorker, e = mailworker.New(app.Identity, smtpSender, mailworker.Options{
			Origin: c.Origin, Guard: mailGuard, OnError: onMailError,
			Enabled: func() identity.MailPurposes {
				return identity.MailPurposes{
					Registration:  !c.RecoveryMode && c.AuthRegistrationEnabled,
					PasswordReset: !c.RecoveryMode && c.AuthPasswordResetEnabled,
				}
			},
		})
	} else {
		identityWorker, e = mailworker.NewCleanup(app.Identity, mailGuard, onMailError)
	}
	if e != nil {
		return fmt.Errorf("identity mail worker unavailable")
	}
	mailContext, stopMail := context.WithCancel(ctx)
	mailDone := make(chan struct{})
	defer func() { stopMail(); <-mailDone }()
	app.Ready(true)
	if paymentRunner != nil {
		go func() {
			if paymentRunner.Run(ctx) != nil && ctx.Err() == nil {
				log.Write(telemetry.Record{Stage: "payments", Code: "worker_stopped"})
				app.Ready(false)
				cancelCalls()
				cancel()
			}
		}()
	}
	if observer != nil {
		observerContext, stopObserver := context.WithCancel(ctx)
		observerDone := make(chan struct{})
		defer func() { stopObserver(); <-observerDone }()
		go func() {
			defer close(observerDone)
			if observer.Run(observerContext) != nil && observerContext.Err() == nil {
				app.Ready(false)
				cancel()
			}
		}()
	}
	go func() {
		defer close(mailDone)
		if identityWorker.Run(mailContext) != nil && mailContext.Err() == nil {
			log.Write(telemetry.Record{Stage: "identity_mail", Code: "worker_stopped"})
			app.Ready(false)
			cancel()
		}
	}()
	drained := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				check, done := context.WithTimeout(ctx, 3*time.Second)
				err := app.Guard(check)
				done()
				if err != nil {
					app.Ready(false)
					cancel()
					return
				}
				if !c.RecoveryMode {
					job, done := context.WithTimeout(ctx, 5*time.Second)
					_, err = app.Billing.Recover(job, billing.RecoverInput{Limit: 100})
					done()
					if err != nil {
						log.Write(telemetry.Record{Stage: "recovery", Code: "worker_failed"})
					}
					job, done = context.WithTimeout(ctx, 5*time.Second)
					_, err = app.Billing.RecoverProcurement(job, 100)
					done()
					if err != nil {
						log.Write(telemetry.Record{Stage: "procurement_recovery", Code: "worker_failed"})
					}
					job, done = context.WithTimeout(ctx, 5*time.Second)
					// Drain a bounded backlog; a single 100-event batch per 10s
					// would make reports fall permanently behind modest traffic.
					for batch := 0; batch < 50; batch++ {
						var n int
						n, err = projector.Project(job, billing.ProjectInput{Limit: 200})
						if err != nil || n < 200 {
							break
						}
					}
					done()
					if err != nil {
						log.Write(telemetry.Record{Stage: "projection", Code: "worker_failed"})
					}
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		app.Ready(false)
		shutdown, done := context.WithTimeout(context.Background(), 310*time.Second)
		defer done()
		paymentDrained := make(chan struct{})
		go func() {
			defer close(paymentDrained)
			if paymentRunner != nil {
				_ = paymentRunner.Close(shutdown)
			}
		}()
		if httpServer.Shutdown(shutdown) != nil {
			cancelCalls()
			_ = httpServer.Close()
		}
		<-paymentDrained
		close(drained)
	}()
	log.Write(telemetry.Record{Stage: "startup", Code: version})
	e = httpServer.ListenAndServe()
	if e == http.ErrServerClosed {
		<-drained
		return nil
	}
	return fmt.Errorf("HTTP service stopped")
}
func initEnv() error {
	flags := flag.NewFlagSet("init-env", flag.ContinueOnError)
	out := flags.String("out", ".env", "new env file; never overwritten")
	if e := flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	f, e := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("environment file already exists or cannot be created")
	}
	defer f.Close()
	encode := func() string {
		raw := security.Random(32)
		b, _ := base64.RawURLEncoding.DecodeString(raw)
		return base64.StdEncoding.EncodeToString(b)
	}
	_, e = fmt.Fprintf(f, "MASTER_KEY_ID=primary\nMASTER_KEY=%s\nAUTH_SECRET=%s\nPOSTGRES_PASSWORD=%s\nAPP_DB_PASSWORD=%s\nPROJECTION_DB_PASSWORD=%s\nPUBLIC_ORIGIN=http://localhost:8080\nCOOKIE_SECURE=false\nMFA_ENFORCE_PLATFORM=false\nLISTEN_ADDR=0.0.0.0:8080\n", encode(), encode(), security.Random(24), security.Random(24), security.Random(24))
	if e != nil {
		return e
	}
	fmt.Println("private environment file created; configure database URLs and HTTPS before production")
	return nil
}

// mailLifecycleGuard preserves normal HTTP draining while mail work cancels.
// Independent ownership failures retain the original fencing semantics.
func mailLifecycleGuard(lifecycle context.Context, guard func(context.Context) error) func(context.Context) error {
	return func(check context.Context) error {
		if lifecycle.Err() != nil || check.Err() != nil {
			return context.Canceled
		}
		bounded, stop := context.WithTimeout(context.WithoutCancel(check), time.Second)
		defer stop()
		return guard(bounded)
	}
}
