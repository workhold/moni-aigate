package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

type processChild struct {
	cmd      *exec.Cmd
	done     chan struct{}
	err      error
	expected bool
	log      string
}
type processSession struct {
	ctx                         context.Context
	env                         *paymentsandboxqa.ApplicationEnvironment
	root, appBinary, mockBinary string
	report                      reporter
	app, mock                   *processChild
	children                    []*processChild
	appDB, mockDB               *pgxpool.Pool
	transport                   *http.Transport
	client                      *http.Client
	master, auth                string
	admin, owner                *httpActor
	sku, version                string
	orders                      []string
}

func processSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", failure("entropy_failed")
	}
	return base64.StdEncoding.EncodeToString(raw[:]), nil
}
func processEnvironment(values []string) []string {
	var out []string
	for _, entry := range values {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", "HOME", "TMPDIR", "TZ", "LANG", "LC_ALL", "LC_CTYPE":
			out = append(out, entry)
		}
	}
	return out
}
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", failure("repository_unavailable")
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(raw), "module github.com/moni-ai/moni-aigate\n") {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", failure("repository_unavailable")
		}
		dir = next
	}
}
func buildProcesses(ctx context.Context, root string, report reporter) (string, string, error) {
	repo, err := repositoryRoot()
	if err != nil {
		return "", "", err
	}
	app, mock := filepath.Join(root, "moni-aigate"), filepath.Join(root, "moni-payment-sandbox")
	for _, item := range []struct{ path, target, label string }{{app, "./cmd/moni-aigate", "application_binary_sha256"}, {mock, "./cmd/moni-payment-sandbox", "mock_binary_sha256"}} {
		build, cancel := context.WithTimeout(ctx, 120*time.Second)
		cmd := exec.CommandContext(build, "go", "build", "-o", item.path, item.target)
		cmd.Dir = repo
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		err := cmd.Run()
		cancel()
		if err != nil {
			return "", "", failure("process_build_failed")
		}
		raw, err := os.ReadFile(item.path)
		if err != nil {
			return "", "", failure("process_digest_failed")
		}
		h := sha256.Sum256(raw)
		_, _ = io.WriteString(report.out, item.label+" "+hex.EncodeToString(h[:])+"\n")
	}
	return app, mock, nil
}

func exerciseProcesses(ctx context.Context, dsn, root string, report reporter, hold bool) (result error) {
	appBinary, mockBinary, err := buildProcesses(ctx, root, report)
	if err != nil {
		return err
	}
	report.pass("current_source_process_builds")
	env, err := paymentsandboxqa.CreateApplication(ctx, dsn, root)
	if err != nil {
		return failure("offline_application_environment_failed")
	}
	s := &processSession{ctx: ctx, env: env, root: root, appBinary: appBinary, mockBinary: mockBinary, report: report}
	defer func() {
		if err := s.cleanup(); err != nil {
			result = err
		}
	}()
	_, _ = io.WriteString(report.out, "environment_id "+env.Protocol.ID+"\n")
	s.master, err = processSecret()
	if err != nil {
		return err
	}
	s.auth, err = processSecret()
	if err != nil {
		return err
	}
	s.appDB, err = env.OpenPool(ctx, "app")
	if err != nil {
		return failure("application_observer_failed")
	}
	s.mockDB, err = env.OpenPool(ctx, "mock")
	if err != nil {
		return failure("mock_observer_failed")
	}
	s.transport = &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, ForceAttemptHTTP2: false}
	s.client = &http.Client{Transport: s.transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err = s.exercise(); err != nil {
		return err
	}
	if hold {
		return s.browserHold()
	}
	return nil
}

func (s *processSession) start(kind, mode string, recovery, proof bool) (*processChild, error) {
	args := []string{"serve"}
	binary := s.appBinary
	env := processEnvironment(os.Environ())
	if kind == "mock" {
		binary = s.mockBinary
		args = append(args, "--component", "mock", "--manifest-file", s.env.MockManifestPath)
	} else {
		appDSN, err := s.env.RuntimeDSN("app")
		if err != nil {
			return nil, failure("application_dsn_failed")
		}
		projectionDSN, err := s.env.RuntimeDSN("projector")
		if err != nil {
			return nil, failure("projection_dsn_failed")
		}
		if !proof {
			for _, target := range []*string{&appDSN, &projectionDSN} {
				u, err := url.Parse(*target)
				if err != nil {
					return nil, failure("ordinary_off_dsn_failed")
				}
				q := u.Query()
				q.Set("search_path", s.env.Schemas["app"]+",pg_catalog")
				u.RawQuery = q.Encode()
				*target = u.String()
			}
		}
		u, err := url.Parse(s.env.ReceiverOrigin)
		if err != nil {
			return nil, failure("application_origin_failed")
		}
		recoveryValue := "false"
		if recovery {
			recoveryValue = "true"
		}
		env = append(env, "DATABASE_URL="+appDSN, "PROJECTION_DATABASE_URL="+projectionDSN, "LISTEN_ADDR="+u.Host, "PUBLIC_ORIGIN="+s.env.ReceiverOrigin, "MASTER_KEY_ID=qa", "MASTER_KEY="+s.master, "AUTH_SECRET="+s.auth, "COOKIE_SECURE=false", "MFA_ENFORCE_PLATFORM=false", "PAYMENTS_MODE="+mode, "RECOVERY_MODE="+recoveryValue, "OPERATIONS_ALERTS_ENABLED=false", "VOUCHERS_ENABLED=false", "AUTH_REGISTRATION_ENABLED=false", "AUTH_PASSWORD_RESET_ENABLED=false")
		if proof {
			env = append(env, "PAYMENTS_APP_MANIFEST="+s.env.ApplicationManifestPath, "PAYMENTS_WORKER_MANIFEST="+s.env.WorkerManifestPath)
		}
	}
	path := filepath.Join(s.root, "child-"+kind+"-"+time.Now().Format("150405.000000000")+".log")
	log, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, failure("private_child_log_failed")
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	cmd.Stdout = log
	cmd.Stderr = log
	child := &processChild{cmd: cmd, done: make(chan struct{}), log: path}
	err = cmd.Start()
	_ = log.Close()
	if err != nil {
		return nil, failure("child_start_failed")
	}
	s.children = append(s.children, child)
	go func() { child.err = cmd.Wait(); close(child.done) }()
	return child, nil
}
func waitStage(ctx context.Context, timeout time.Duration, stage string, predicate func(context.Context) (bool, error)) error {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		ok, err := predicate(bounded)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return failure(stage)
		case <-timer.C:
		}
	}
}
func childRunning(c *processChild) bool {
	if c == nil {
		return false
	}
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}
func (s *processSession) ready(c *processChild, kind string, recovery bool) error {
	return waitStage(s.ctx, 20*time.Second, "process_readiness_timeout", func(ctx context.Context) (bool, error) {
		if !childRunning(c) {
			return false, failure("process_early_exit")
		}
		if kind == "mock" {
			for _, origin := range []string{s.env.APIOrigin, s.env.ControlOrigin} {
				path := "/v1/checkout"
				if origin == s.env.ControlOrigin {
					path = "/_qa/v1/commands"
				}
				res, err := s.request(ctx, http.MethodPost, origin+path, nil, nil, "", nil)
				if err != nil {
					return false, nil
				}
				if res.status != 401 {
					return false, failure("mock_readiness_invalid")
				}
			}
			return true, nil
		}
		res, err := s.request(ctx, http.MethodGet, s.env.ReceiverOrigin+"/health/ready", nil, nil, "", nil)
		if err != nil {
			return false, nil
		}
		var body struct {
			Status   string `json:"status"`
			Recovery bool   `json:"recovery_mode"`
		}
		return res.status == 200 && json.Unmarshal(res.body, &body) == nil && body.Status == "ready" && body.Recovery == recovery, nil
	})
}
func (s *processSession) startApp(mode string, recovery bool) error {
	c, err := s.start("app", mode, recovery, true)
	if err != nil {
		return err
	}
	s.app = c
	return s.ready(c, "app", recovery)
}
func (s *processSession) startMock() error {
	c, err := s.start("mock", "", false, true)
	if err != nil {
		return err
	}
	s.mock = c
	return s.ready(c, "mock", false)
}
func stopProcess(c *processChild, signal os.Signal, graceful bool) error {
	if !childRunning(c) {
		return failure("process_exited_before_stop")
	}
	if c.cmd.Process.Signal(signal) != nil {
		return failure("process_signal_failed")
	}
	c.expected = true
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	select {
	case <-c.done:
		if graceful && c.err != nil {
			return failure("process_graceful_exit_failed")
		}
		return nil
	case <-timer.C:
		_ = c.cmd.Process.Kill()
		<-c.done
		return failure("process_drain_timeout")
	}
}
func (s *processSession) stopApp(signal os.Signal, graceful bool) error {
	if err := stopProcess(s.app, signal, graceful); err != nil {
		return err
	}
	return waitStage(s.ctx, 5*time.Second, "application_connections_not_drained", func(ctx context.Context) (bool, error) {
		var n int
		err := s.appDB.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=ANY($1::text[])`, []string{s.env.Roles["app_worker"], s.env.Roles["app_projector"]}).Scan(&n)
		if err != nil {
			return false, failure("drain_observation_failed")
		}
		return n == 0, nil
	})
}
func (s *processSession) refused(c *processChild, expected string) error {
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-c.done:
		if c.err == nil {
			return failure("refusal_process_succeeded")
		}
		raw, err := os.ReadFile(c.log)
		if err != nil || !strings.Contains(string(raw), expected) {
			return failure("refusal_reason_not_proven")
		}
		c.expected = true
		return nil
	case <-s.ctx.Done():
		return failure("qa_cancelled")
	case <-timer.C:
		return failure("refusal_process_remained_running")
	}
}
func (s *processSession) cleanup() error {
	failed := false
	for i := len(s.children) - 1; i >= 0; i-- {
		c := s.children[i]
		if childRunning(c) {
			if stopProcess(c, syscall.SIGTERM, true) != nil {
				failed = true
			}
		} else if !c.expected {
			failed = true
		}
	}
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
	if s.appDB != nil {
		s.appDB.Close()
	}
	if s.mockDB != nil {
		s.mockDB.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.env.VerifyLegacyFundingUnchanged(ctx) != nil {
		failed = true
	} else {
		s.report.pass("retained_a_funding_unchanged")
	}
	observer, err := s.env.OpenPool(ctx, "app_owner")
	if err != nil {
		failed = true
	} else {
		var roles []string
		for key, name := range s.env.Roles {
			if !strings.HasSuffix(key, "_owner") {
				roles = append(roles, name)
			}
		}
		if waitStage(ctx, 5*time.Second, "runtime_connections_remained", func(ctx context.Context) (bool, error) {
			var n int
			if observer.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename=ANY($1::text[])`, roles).Scan(&n) != nil {
				return false, failure("cleanup_observation_failed")
			}
			return n == 0, nil
		}) != nil {
			failed = true
		}
		observer.Close()
	}
	if s.env.Close(ctx) != nil {
		failed = true
	} else {
		s.report.pass("exact_eight_role_environment_cleanup")
	}
	if failed {
		return failure("process_cleanup_or_funding_failed")
	}
	return nil
}
