package mailworker

import (
	"context"
	"encoding/json"
	"net"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/maildeliver"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

const processWindowOrigin = "http://127.0.0.1:8081"

// TestMailWorkerProcessHelper is run in a separate test process by
// TestMailWorkerProcessKillAfterSMTPAcceptance. It is deliberately kept in
// this package so the test exercises Worker.Run and the real identity Store,
// rather than a second test-only delivery loop.
func TestMailWorkerProcessHelper(t *testing.T) {
	if os.Getenv("MONI_MAIL_PROCESS_CHILD") != "1" {
		return
	}

	dsn := os.Getenv("MONI_MAIL_PROCESS_DSN")
	schema := os.Getenv("MONI_MAIL_PROCESS_SCHEMA")
	origin := os.Getenv("MONI_MAIL_PROCESS_ORIGIN")
	port, err := strconv.Atoi(os.Getenv("MONI_MAIL_PROCESS_PORT"))
	if err != nil || dsn == "" || schema == "" || origin == "" || port < 1 || port > 65535 {
		t.Fatal("invalid worker child configuration")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 2
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	secrets := processWindowSecrets()
	store := identity.New(pool, secrets)
	transport, err := maildeliver.NewSMTP(config.MailConfig{
		Transport: "smtp",
		Host:      "127.0.0.1",
		Port:      port,
		From:      "noreply@fixture.invalid",
		TLSMode:   "plain_loopback",
	}, origin)
	if err != nil {
		t.Fatal(err)
	}
	var sender Sender = transport
	if os.Getenv("MONI_MAIL_PROCESS_MODE") == "kill-after-accept" {
		sender = processWindowGateSender{sender: sender, marker: os.Getenv("MONI_MAIL_PROCESS_GATE")}
	}
	w, err := New(store, sender, Options{
		Origin: origin,
		Enabled: func() identity.MailPurposes {
			return identity.MailPurposes{Registration: true}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if os.Getenv("MONI_MAIL_PROCESS_MODE") == "kill-after-accept" {
		// The parent terminates this process after the SMTP server has returned
		// DATA's 250 reply and this child has entered the gate sender. The
		// context is only a last-resort bound for a failed parent assertion.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := w.Run(ctx); err != nil {
			t.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

type processWindowGateSender struct {
	sender Sender
	marker string
}

func (s processWindowGateSender) Send(ctx context.Context, message maildeliver.Message) error {
	if err := s.sender.Send(ctx, message); err != nil {
		return err
	}
	if err := os.WriteFile(s.marker, []byte("sender-returned\n"), 0o600); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

type processSMTPFixture struct {
	listener net.Listener
	accepted string
	wg       sync.WaitGroup
	mu       sync.Mutex
	bodies   []string
}

func newProcessSMTPFixture(t *testing.T, accepted string) *processSMTPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &processSMTPFixture{listener: listener, accepted: accepted}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.serve(conn)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		f.wg.Wait()
	})
	return f
}

func (f *processSMTPFixture) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	wire := textproto.NewConn(conn)
	if wire.PrintfLine("220 local process fixture") != nil {
		return
	}
	for {
		line, err := wire.ReadLine()
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "):
			if wire.PrintfLine("250 local process fixture") != nil {
				return
			}
		case strings.HasPrefix(line, "MAIL FROM:"):
			if wire.PrintfLine("250 accepted") != nil {
				return
			}
		case strings.HasPrefix(line, "RCPT TO:"):
			if wire.PrintfLine("250 accepted") != nil {
				return
			}
		case line == "DATA":
			if wire.PrintfLine("354 send data") != nil {
				return
			}
			body, err := wire.ReadDotBytes()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.bodies = append(f.bodies, string(body))
			f.mu.Unlock()
			if err := appendProcessMarker(f.accepted, "accepted\n"); err != nil {
				return
			}
			_ = wire.PrintfLine("250 accepted")
			return
		case line == "QUIT":
			_ = wire.PrintfLine("221 closing")
			return
		default:
			_ = wire.PrintfLine("500 invalid command")
		}
	}
}

func appendProcessMarker(path, value string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(value)
	return err
}

type processMailPayload struct {
	Version int    `json:"v"`
	To      string `json:"to"`
	Purpose string `json:"purpose"`
	Link    string `json:"link"`
}

func processWindowSecrets() *security.Secrets {
	return &security.Secrets{
		Keys:   map[string][]byte{"test": []byte(strings.Repeat("e", 32))},
		Active: "test",
		Pepper: []byte(strings.Repeat("a", 32)),
	}
}

func seedProcessWindowMail(t *testing.T, pool *pgxpool.Pool, secrets *security.Secrets, origin string) (string, string) {
	t.Helper()
	const purpose = identity.IdentityPurposeRegistration
	const recipient = "process-window@fixture.invalid"
	subject, challenge, outbox := uuid.NewString(), uuid.NewString(), uuid.NewString()
	token := "ic1_" + security.Random(32)
	registration, err := secrets.SealFor(recipient, "identity-registration", challenge)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(processMailPayload{
		Version: 1,
		To:      recipient,
		Purpose: purpose,
		Link:    origin + "/register/complete#token=" + token,
	})
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := secrets.SealFor(string(payload), "identity-mail-outbox", outbox)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err = pool.Exec(ctx, `INSERT INTO identity_mail_subjects(id,purpose,email_digest)
VALUES($1,$2,$3)`, subject, purpose, secrets.Digest("identity-mail-subject:v1:"+purpose+"\x00"+recipient)); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO identity_challenges(id,subject_id,purpose,token_digest,registration_email_ciphertext,created_at,expires_at)
VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '1 hour')`, challenge, subject, purpose, secrets.Digest("identity-challenge:v1:"+purpose+":"+token), registration); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO identity_mail_outbox(id,challenge_id,payload_ciphertext)
VALUES($1,$2,$3)`, outbox, challenge, ciphertext); err != nil {
		t.Fatal(err)
	}
	return outbox, token
}

func expireProcessWindowLease(t *testing.T, pool *pgxpool.Pool, outbox string) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "ALTER TABLE identity_mail_outbox DISABLE TRIGGER identity_outbox_guard"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE identity_mail_outbox
SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, outbox); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), "ALTER TABLE identity_mail_outbox ENABLE TRIGGER identity_outbox_guard"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func waitProcessMarker(path string, minimum string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), minimum) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestMailWorkerProcessKillAfterSMTPAcceptance proves the one ambiguous
// boundary that in-process tests cannot prove: DATA was accepted by the local
// SMTP server, then the worker process died before FinishMail could ACK it.
// The restarted worker reclaims the expired lease and uses the same durable
// outbox payload, message identity, and proof link exactly once per attempt.
func TestMailWorkerProcessKillAfterSMTPAcceptance(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required: process-window identity test needs PostgreSQL")
	}
	base, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if base.ConnConfig.Host != "127.0.0.1" || base.ConnConfig.Port != 55439 || base.ConnConfig.Database != "moni_test" || base.ConnConfig.User != "moni_test" {
		t.Skip("process-window identity test requires postgres://moni_test@127.0.0.1:55439/moni_test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "mail_process_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	secrets := processWindowSecrets()
	outbox, token := seedProcessWindowMail(t, pool, secrets, processWindowOrigin)
	accepted := filepath.Join(t.TempDir(), "smtp-accepted")
	gate := filepath.Join(t.TempDir(), "sender-returned")
	smtpFixture := newProcessSMTPFixture(t, accepted)
	port := smtpFixture.listener.Addr().(*net.TCPAddr).Port

	start := func(mode, marker string) (*exec.Cmd, *strings.Builder) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestMailWorkerProcessHelper$", "-test.v")
		output := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = output, output
		cmd.Env = append(os.Environ(),
			"MONI_MAIL_PROCESS_CHILD=1",
			"MONI_MAIL_PROCESS_MODE="+mode,
			"MONI_MAIL_PROCESS_DSN="+dsn,
			"MONI_MAIL_PROCESS_SCHEMA="+schema,
			"MONI_MAIL_PROCESS_ORIGIN="+processWindowOrigin,
			"MONI_MAIL_PROCESS_PORT="+strconv.Itoa(port),
			"MONI_MAIL_PROCESS_GATE="+marker,
		)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd, output
	}

	first, firstOutput := start("kill-after-accept", gate)
	firstWaited := false
	defer func() {
		if !firstWaited && first.Process != nil {
			_ = first.Process.Signal(syscall.SIGKILL)
			_ = first.Wait()
		}
	}()
	if !waitProcessMarker(accepted, "accepted\n", 10*time.Second) || !waitProcessMarker(gate, "sender-returned\n", 10*time.Second) {
		t.Fatalf("worker did not reach SMTP accepted→ACK window: %s", firstOutput.String())
	}
	if err = first.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	err = first.Wait()
	firstWaited = true
	if err == nil {
		t.Fatal("worker survived the forced process failure")
	}
	var state string
	var attempt, generation int
	var payload *string
	if err = pool.QueryRow(ctx, `SELECT state,attempt_count,claim_generation,payload_ciphertext FROM identity_mail_outbox WHERE id=$1`, outbox).Scan(&state, &attempt, &generation, &payload); err != nil {
		t.Fatal(err)
	}
	if state != "sending" || attempt != 1 || generation != 1 || payload == nil {
		t.Fatalf("forced failure did not leave an unacknowledged durable claim: state=%s attempt=%d generation=%d payload=%t", state, attempt, generation, payload != nil)
	}
	expireProcessWindowLease(t, pool, outbox)

	second, secondOutput := start("resume", filepath.Join(t.TempDir(), "unused"))
	secondWaited := false
	defer func() {
		if !secondWaited && second.Process != nil {
			_ = second.Process.Kill()
			_ = second.Wait()
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var sent *time.Time
		if err = pool.QueryRow(ctx, `SELECT state,attempt_count,claim_generation,payload_ciphertext,sent_at FROM identity_mail_outbox WHERE id=$1`, outbox).Scan(&state, &attempt, &generation, &payload, &sent); err != nil {
			t.Fatal(err)
		}
		if state == "sent" && attempt == 2 && generation == 2 && payload == nil && sent != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if state != "sent" || attempt != 2 || generation != 2 || payload != nil {
		t.Fatalf("restart did not ACK the recovered claim: state=%s attempt=%d generation=%d payload=%t output=%s", state, attempt, generation, payload != nil, secondOutput.String())
	}
	err = second.Wait()
	secondWaited = true
	if err != nil {
		t.Fatalf("restarted worker failed: %v: %s", err, secondOutput.String())
	}
	smtpFixture.mu.Lock()
	bodies := append([]string(nil), smtpFixture.bodies...)
	smtpFixture.mu.Unlock()
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[0], "Message-ID: <"+outbox+"@moni-aigate.local>") || !strings.Contains(bodies[0], token) {
		t.Fatalf("recovery did not resend the same durable message identity: %d bodies", len(bodies))
	}
}
