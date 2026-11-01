package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxqa"
)

type child struct {
	cmd          *exec.Cmd
	done         chan struct{}
	err          error // published by closing done
	expectedExit bool  // set only for a successful stop signal or confirmed startup refusal
}

type session struct {
	ctx                context.Context
	env                *paymentsandboxqa.Environment
	binary             string
	report             reporter
	mock, receiver     *child
	children           []*child
	mockDB, receiverDB *pgxpool.Pool
	client             *http.Client
	transport          *http.Transport
}

// Subprocesses need none of the administrator/database/transport environment.
// Their own immutable manifest is the sole runtime credential/configuration.
func componentEnvironment(values []string) []string {
	out := make([]string, 0, len(values))
	for _, entry := range values {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH", "HOME", "TMPDIR", "TZ", "LANG", "LC_ALL", "LC_COLLATE", "LC_CTYPE", "LC_MESSAGES", "LC_MONETARY", "LC_NUMERIC", "LC_TIME":
			out = append(out, entry)
		}
	}
	return out
}

func (s *session) start(component string) (*child, error) {
	path := s.env.MockManifestPath
	if component == "receiver" {
		path = s.env.ReceiverManifestPath
	}
	c := &child{cmd: exec.Command(s.binary, "serve", "--component", component, "--manifest-file", path), done: make(chan struct{})}
	c.cmd.Env = componentEnvironment(os.Environ())
	c.cmd.Stdout, c.cmd.Stderr = io.Discard, io.Discard
	if c.cmd.Start() != nil {
		return nil, failure("component_start_failed")
	}
	s.children = append(s.children, c)
	go func() { c.err = c.cmd.Wait(); close(c.done) }()
	return c, nil
}

func (s *session) ready(c *child, origins ...string) error {
	ctx, stop := context.WithTimeout(s.ctx, 15*time.Second)
	defer stop()
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil {
			return failure("listener_not_ready")
		}
		if err = eventually(ctx, 25*time.Millisecond, func() (bool, error) {
			select {
			case <-c.done:
				return false, failure("component_early_exit")
			default:
			}
			conn, e := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(ctx, "tcp4", u.Host)
			if e != nil {
				return false, nil
			}
			_ = conn.Close()
			return true, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func stopChild(c *child, signal os.Signal, graceful bool) error {
	if c == nil {
		return nil
	}
	select {
	case <-c.done:
		return failure("component_exited_before_stop")
	default:
	}
	if c.cmd.Process.Signal(signal) != nil {
		return failure("component_signal_failed")
	}
	c.expectedExit = true
	t := time.NewTimer(20 * time.Second)
	defer t.Stop()
	select {
	case <-c.done:
		if graceful && c.err != nil {
			return failure("component_graceful_exit_failed")
		}
		return nil
	case <-t.C:
		_ = c.cmd.Process.Kill()
		<-c.done
		return failure("component_stop_timeout")
	}
}

func (s *session) duplicateRefused(component string) error {
	c, err := s.start(component)
	if err != nil {
		return err
	}
	t := time.NewTimer(15 * time.Second)
	defer t.Stop()
	select {
	case <-c.done:
		if c.err == nil {
			return failure("duplicate_instance_not_refused")
		}
		c.expectedExit = true
		return nil
	case <-s.ctx.Done():
		return failure("qa_cancelled")
	case <-t.C:
		return failure("duplicate_instance_not_refused")
	}
}

func (s *session) stopChildren() bool {
	failed := false
	for i := len(s.children) - 1; i >= 0; i-- {
		c := s.children[i]
		select {
		case <-c.done:
			if !c.expectedExit {
				failed = true
			}
			continue
		default:
		}
		if stopChild(c, syscall.SIGTERM, true) != nil {
			failed = true
		}
	}
	return failed
}

func requireRunning(c *child) error {
	if c == nil || c.done == nil {
		return failure("component_not_running")
	}
	select {
	case <-c.done:
		return failure("component_not_running")
	default:
		return nil
	}
}

func (s *session) cleanup() error {
	failed := s.stopChildren()
	if s.transport != nil {
		s.transport.CloseIdleConnections()
	}
	if s.mockDB != nil {
		s.mockDB.Close()
	}
	if s.receiverDB != nil {
		s.receiverDB.Close()
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	funds := s.env.VerifyFundsUnchanged(ctx)
	if funds == nil {
		s.report.pass("nonempty_application_funding_digest_unchanged")
	}
	closed := s.env.Close(ctx)
	if closed == nil {
		s.report.pass("exact_environment_cleanup")
	}
	if failed || funds != nil || closed != nil {
		return failure("cleanup_or_funding_verification_failed")
	}
	return nil
}

func eventually(ctx context.Context, delay time.Duration, predicate func() (bool, error)) error {
	for {
		ok, err := predicate()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return failure("condition_timeout")
		case <-t.C:
		}
	}
}
