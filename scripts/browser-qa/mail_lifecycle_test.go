package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// No DSN, address, subject, proof, password, body, query or internal identifier
// is serializable in this evidence. Raw test errors are fixed stage codes.
type mailProcessEvidence struct {
	Window                        string `json:"window"`
	BinarySHA256                  string `json:"binary_sha256"`
	InitialGeneration             int64  `json:"initial_generation"`
	InitialAttempt                int    `json:"initial_attempt"`
	InitialLeaseRemainingMS       int64  `json:"initial_lease_remaining_ms"`
	PhysicalSMTPBarrier           bool   `json:"physical_smtp_barrier"`
	PhysicalDatabaseWaiter        bool   `json:"physical_database_waiter"`
	DatabaseWaitAgeMS             int64  `json:"database_wait_age_ms"`
	KillAndRoleExitMS             int64  `json:"kill_and_role_exit_ms"`
	SMTPElapsedUpperMS            int64  `json:"smtp_elapsed_upper_ms"`
	SMTPUnexpiredAtKill           bool   `json:"smtp_unexpired_at_kill"`
	FirstFinal250BeforeKill       bool   `json:"first_final_250_before_kill"`
	SIGKILLConfirmed              bool   `json:"sigkill_confirmed"`
	ReplacementReady              bool   `json:"replacement_ready"`
	OriginalLeasePreserved        bool   `json:"original_lease_preserved"`
	OriginalLeaseExpiredBeforeAck bool   `json:"original_lease_expired_before_ack"`
	FinalGeneration               int64  `json:"final_generation"`
	FinalAttempt                  int    `json:"final_attempt"`
	MessagesPersisted             int    `json:"messages_persisted"`
	Final250Replies               int    `json:"final_250_replies"`
	SameOutboxChallengeProof      bool   `json:"same_outbox_challenge_proof"`
	SameMessageIdentity           bool   `json:"same_message_identity"`
	ProofUnconsumed               bool   `json:"proof_unconsumed"`
	PayloadAndLeaseCleared        bool   `json:"payload_and_lease_cleared"`
	UsersTenantsFundsUnchanged    bool   `json:"users_tenants_funds_unchanged"`
	GracefulFinalShutdown         bool   `json:"graceful_final_shutdown"`
	NoRolesSchemasOrConnections   bool   `json:"no_roles_schemas_or_connections"`
	ListenersClosed               bool   `json:"listeners_closed"`
	PrivateFilesRemoved           bool   `json:"private_files_removed"`
	ElapsedMS                     int64  `json:"elapsed_ms"`
}

func mailLifecycleDatabase(raw string) (*pgxpool.Config, error) {
	parsed, err := pgxpool.ParseConfig(raw)
	if err != nil || raw == "" || parsed.ConnConfig.Host != "127.0.0.1" || parsed.ConnConfig.Port != 55439 || parsed.ConnConfig.Database != "moni_test" {
		return nil, stage("mail_lifecycle_exact_test_database_required")
	}
	for _, fallback := range parsed.ConnConfig.Fallbacks {
		if fallback.Host != "127.0.0.1" || fallback.Port != 55439 {
			return nil, stage("mail_lifecycle_exact_test_database_required")
		}
	}
	return testDatabase(raw)
}

func TestMailLifecycleRejectsOtherDatabasesBeforeIO(t *testing.T) {
	for _, raw := range []string{"", "invalid", "postgres://qa:private-marker@localhost:55439/moni_test", "postgres://qa:private-marker@127.0.0.1:5432/moni_test", "postgres://qa:private-marker@[::1]:55439/moni_test", "postgres://qa:private-marker@127.0.0.1:55439/moni", "postgres://qa:private-marker@127.0.0.1:55439/moni_test?host=127.0.0.1,localhost"} {
		if _, err := mailLifecycleDatabase(raw); err == nil || strings.Contains(err.Error(), "private-marker") || strings.Contains(err.Error(), "postgres://") {
			t.Fatal("mail lifecycle accepted another database or reflected input")
		}
	}
	if _, err := mailLifecycleDatabase("postgres://qa@127.0.0.1:55439/moni_test?sslmode=disable"); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := run(context.Background(), options{mailLifecycle: func(context.Context, *privateLifecycle) error { return nil }}, &output); err == nil || err.Error() != "mail_lifecycle_requires_identity_fixture" || output.Len() != 0 {
		t.Fatal("private lifecycle hook escaped fixture isolation")
	}
}

type privateMailState struct {
	outbox, challenge, proofDigest, state string
	generation                            int64
	attempt                               int
	lease, sentAt                         *time.Time
	payloadCleared, proofUnconsumed       bool
	proofUnrevoked                        bool
	dbNow                                 time.Time
	outboxes, challenges, users, tenants  int
	keys, requests, funds                 int
}

func readPrivateMailState(ctx context.Context, owner *pgxpool.Pool) (privateMailState, error) {
	var s privateMailState
	err := owner.QueryRow(ctx, `SELECT o.id::text,o.challenge_id::text,c.token_digest,o.state,o.claim_generation,o.attempt_count,
 o.lease_until,o.sent_at,o.payload_ciphertext IS NULL,c.consumed_at IS NULL,c.revoked_at IS NULL,clock_timestamp(),
 (SELECT count(*) FROM identity_mail_outbox),(SELECT count(*) FROM identity_challenges),(SELECT count(*) FROM users),
 (SELECT count(*) FROM tenants),(SELECT count(*) FROM api_keys),(SELECT count(*) FROM request_records),(SELECT count(*) FROM funding_receipts)
 FROM identity_mail_outbox o JOIN identity_challenges c ON c.id=o.challenge_id`).Scan(&s.outbox, &s.challenge, &s.proofDigest, &s.state, &s.generation, &s.attempt, &s.lease, &s.sentAt, &s.payloadCleared, &s.proofUnconsumed, &s.proofUnrevoked, &s.dbNow, &s.outboxes, &s.challenges, &s.users, &s.tenants, &s.keys, &s.requests, &s.funds)
	if err != nil {
		return privateMailState{}, stage("mail_lifecycle_state_read_failed")
	}
	if s.outboxes != 1 || s.challenges != 1 || s.users != 3 || s.tenants != 0 || s.keys != 0 || s.requests != 0 || s.funds != 0 {
		return privateMailState{}, stage("mail_lifecycle_unexpected_identity_or_business_facts")
	}
	return s, nil
}

func waitFinishMailBlocked(ctx context.Context, pool *pgxpool.Pool, appRole string, blockerPID int) (int64, error) {
	for {
		var queryAge int64
		err := pool.QueryRow(ctx, `SELECT ceil(extract(epoch FROM (clock_timestamp()-a.query_start))*1000)::bigint
 FROM pg_stat_activity a WHERE a.datname=current_database() AND a.usename=$1 AND a.state='active'
 AND a.wait_event_type='Lock' AND $2=ANY(pg_blocking_pids(a.pid))
 AND position('WITH proof AS MATERIALIZED' IN a.query)>0 AND position('UPDATE identity_mail_outbox o SET' IN a.query)>0 LIMIT 1`, appRole, blockerPID).Scan(&queryAge)
		if err == nil {
			if queryAge < 0 || queryAge >= 4500 {
				return 0, stage("mail_lifecycle_finish_window_expired")
			}
			return queryAge, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, stage("mail_lifecycle_finish_waiter_unavailable")
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, stage("mail_lifecycle_finish_waiter_not_observed")
		case <-timer.C:
		}
	}
}

func privateMailRequest(ctx context.Context, origin string) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/auth/registration/request", strings.NewReader(`{"email":"recovery@moni.invalid"}`))
	if err != nil {
		return stage("mail_lifecycle_request_failed")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer c.CloseIdleConnections()
	response, err := c.Do(r)
	if err != nil {
		return stage("mail_lifecycle_request_failed")
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return stage("mail_lifecycle_request_rejected")
	}
	return nil
}

func samePrivateMessages(live *privateLifecycle, initial privateMailState, expected int) bool {
	messages, confirmed := live.fixture.snapshot()
	if len(messages) != expected || confirmed != expected {
		return false
	}
	var firstLink string
	for _, record := range messages {
		m, err := mail.ReadMessage(strings.NewReader(record.Body))
		if err != nil || m.Header.Get("Message-ID") != "<"+initial.outbox+"@moni-aigate.local>" || m.Header.Get("To") != "recovery@moni.invalid" || strings.Trim(record.Recipient, "<>") != "recovery@moni.invalid" {
			return false
		}
		body, err := io.ReadAll(io.LimitReader(m.Body, 16385))
		if err != nil || len(body) > 16384 {
			return false
		}
		link := ""
		for _, line := range strings.Split(string(body), "\r\n") {
			if strings.HasPrefix(line, live.origin+"/register/complete#token=") {
				if link != "" {
					return false
				}
				link = line
			}
		}
		if link == "" || !live.proofMatches(link, initial.proofDigest) || firstLink != "" && firstLink != link {
			return false
		}
		firstLink = link
	}
	raw, err := os.ReadFile(live.fixture.path)
	var persisted []fixtureMail
	info, statErr := os.Stat(live.fixture.path)
	if err != nil || statErr != nil || info.Mode().Perm() != 0600 || json.Unmarshal(raw, &persisted) != nil || len(persisted) != expected {
		return false
	}
	for i := range messages {
		if persisted[i] != messages[i] {
			return false
		}
	}
	return true
}

func exerciseMailWindow(ctx context.Context, live *privateLifecycle, b *fixtureMailBarrier, point fixtureMailPoint, evidence *mailProcessEvidence) error {
	evidence.BinarySHA256 = live.binaryHash
	if err := privateMailRequest(ctx, live.origin); err != nil {
		return err
	}
	observe, stopObserve := context.WithTimeout(ctx, 15*time.Second)
	defer stopObserve()
	select {
	case <-b.reached:
		evidence.PhysicalSMTPBarrier = true
	case <-observe.Done():
		return stage("mail_lifecycle_smtp_barrier_not_observed")
	}
	// Start before issuing the initial DB query, including its entire RTT. The
	// lease predates SMTP, so this conservatively bounds SMTP execution age.
	initialReadStarted := time.Now()
	initial, err := readPrivateMailState(observe, live.owner)
	if err != nil {
		return err
	}
	if initial.state != "sending" || initial.generation != 1 || initial.attempt != 1 || initial.lease == nil || initial.payloadCleared || !initial.proofUnconsumed || !initial.proofUnrevoked {
		return stage("mail_lifecycle_original_claim_invalid")
	}
	evidence.InitialGeneration, evidence.InitialAttempt = initial.generation, initial.attempt
	evidence.InitialLeaseRemainingMS = initial.lease.Sub(initial.dbNow).Milliseconds()
	if evidence.InitialLeaseRemainingMS < 45000 || evidence.InitialLeaseRemainingMS > 60000 {
		return stage("mail_lifecycle_original_lease_invalid")
	}
	smtpElapsedUpper := func() time.Duration {
		return 60*time.Second - initial.lease.Sub(initial.dbNow) + time.Since(initialReadStarted)
	}
	messages, confirmed := live.fixture.snapshot()
	wantBefore := 0
	if point == mailBeforeAcceptedReply {
		wantBefore = 1
	}
	if len(messages) != wantBefore || confirmed != 0 {
		return stage("mail_lifecycle_pre_kill_mail_count_invalid")
	}
	var blocker pgx.Tx
	defer func() {
		if blocker != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = blocker.Rollback(cleanup)
			cancel()
		}
	}()
	if point == mailBeforeAcceptedReply {
		blocker, err = live.owner.Begin(observe)
		if err != nil {
			return stage("mail_lifecycle_ack_barrier_begin_failed")
		}
		var locked string
		if blocker.QueryRow(observe, `SELECT id::text FROM identity_mail_outbox WHERE id=$1 FOR UPDATE`, initial.outbox).Scan(&locked) != nil || locked != initial.outbox {
			return stage("mail_lifecycle_ack_barrier_lock_failed")
		}
		var blockerPID int
		if blocker.QueryRow(observe, `SELECT pg_backend_pid()`).Scan(&blockerPID) != nil {
			return stage("mail_lifecycle_ack_barrier_pid_failed")
		}
		releasedAt := time.Now()
		killCtx, cancelKill := context.WithTimeout(ctx, 4500*time.Millisecond)
		defer cancelKill()
		b.release()
		age, err := waitFinishMailBlocked(killCtx, live.owner, live.appRole, blockerPID)
		if err != nil {
			return err
		}
		observedAt := time.Now()
		evidence.PhysicalDatabaseWaiter, evidence.DatabaseWaitAgeMS = true, age
		still, err := readPrivateMailState(killCtx, live.owner)
		if err != nil || still.state != "sending" || still.generation != 1 || still.lease == nil || !still.lease.Equal(*initial.lease) {
			return stage("mail_lifecycle_ack_was_not_pending")
		}
		messages, confirmed := live.fixture.snapshot()
		if len(messages) != 1 || confirmed != 1 {
			return stage("mail_lifecycle_first_final_250_not_confirmed")
		}
		evidence.FirstFinal250BeforeKill = true
		// <9s covers the production 10s per-IO bound as well as its 20s total
		// timeout. A timed-out Send retry cannot masquerade as accepted here.
		if elapsed := smtpElapsedUpper(); elapsed < 0 || elapsed >= 9*time.Second {
			return stage("mail_lifecycle_smtp_timeout_could_precede_kill")
		}
		if err := live.killAndWait(killCtx); err != nil {
			return err
		}
		evidence.KillAndRoleExitMS = time.Since(releasedAt).Milliseconds()
		if time.Since(releasedAt) >= 4500*time.Millisecond || time.Duration(age)*time.Millisecond+time.Since(observedAt) >= 4500*time.Millisecond {
			return stage("mail_lifecycle_finish_timeout_preceded_kill")
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		err = blocker.Rollback(cleanup)
		cancel()
		blocker = nil
		if err != nil {
			return stage("mail_lifecycle_ack_barrier_release_failed")
		}
	} else {
		if elapsed := smtpElapsedUpper(); elapsed < 0 || elapsed >= 9*time.Second {
			return stage("mail_lifecycle_smtp_timeout_could_precede_kill")
		}
		killedAt := time.Now()
		killCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := live.killAndWait(killCtx)
		cancel()
		if err != nil {
			return err
		}
		evidence.KillAndRoleExitMS = time.Since(killedAt).Milliseconds()
		b.release()
	}
	evidence.SIGKILLConfirmed = true
	evidence.SMTPElapsedUpperMS = smtpElapsedUpper().Milliseconds()
	if elapsed := smtpElapsedUpper(); elapsed < 0 || elapsed >= 9*time.Second {
		return stage("mail_lifecycle_smtp_window_not_proven")
	}
	evidence.SMTPUnexpiredAtKill = true
	afterKill, err := readPrivateMailState(ctx, live.owner)
	if err != nil {
		return err
	}
	if afterKill.state != "sending" || afterKill.generation != 1 || afterKill.attempt != 1 || afterKill.lease == nil || !afterKill.lease.Equal(*initial.lease) || afterKill.outbox != initial.outbox || afterKill.challenge != initial.challenge {
		return stage("mail_lifecycle_sigkill_changed_original_claim")
	}
	restartCtx, stopRestart := context.WithTimeout(ctx, 15*time.Second)
	err = live.restart(restartCtx)
	stopRestart()
	if err != nil {
		return err
	}
	evidence.ReplacementReady = true
	afterReady, err := readPrivateMailState(ctx, live.owner)
	if err != nil || afterReady.state != "sending" || afterReady.generation != 1 || afterReady.attempt != 1 || afterReady.lease == nil || !afterReady.lease.Equal(*initial.lease) {
		return stage("mail_lifecycle_restart_did_not_preserve_original_lease")
	}
	evidence.OriginalLeasePreserved = true
	finish, stopFinish := context.WithTimeout(ctx, 75*time.Second)
	defer stopFinish()
	for {
		current, err := readPrivateMailState(finish, live.owner)
		if err != nil {
			return err
		}
		if current.state == "sent" {
			if current.generation != 2 || current.attempt != 2 || current.outbox != initial.outbox || current.challenge != initial.challenge || current.proofDigest != initial.proofDigest || !current.proofUnconsumed || !current.proofUnrevoked || !current.payloadCleared || current.lease != nil || current.sentAt == nil || current.sentAt.Before(*initial.lease) {
				return stage("mail_lifecycle_recovered_claim_invalid")
			}
			want := 1
			if point == mailBeforeAcceptedReply {
				want = 2
			}
			if !samePrivateMessages(live, initial, want) {
				return stage("mail_lifecycle_recovery_changed_message_or_proof")
			}
			messages, confirmed := live.fixture.snapshot()
			evidence.FinalGeneration, evidence.FinalAttempt = current.generation, current.attempt
			evidence.MessagesPersisted, evidence.Final250Replies = len(messages), confirmed
			evidence.SameOutboxChallengeProof, evidence.SameMessageIdentity = true, true
			evidence.ProofUnconsumed, evidence.PayloadAndLeaseCleared = true, true
			evidence.OriginalLeaseExpiredBeforeAck, evidence.UsersTenantsFundsUnchanged = true, true
			return nil
		}
		if current.state != "sending" || current.outbox != initial.outbox || current.challenge != initial.challenge || current.generation > 2 {
			return stage("mail_lifecycle_unexpected_recovery_transition")
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-finish.Done():
			timer.Stop()
			return stage("mail_lifecycle_original_lease_recovery_timeout")
		case <-timer.C:
		}
	}
}

// This is the only opt-in for these two windows. Never run it in parallel with
// full PG tests or another real main: InstanceLock is database-wide.
func TestMailLifecycleRealMainRecovery(t *testing.T) {
	if os.Getenv("MAIL_LIFECYCLE_REAL_MAIN_TEST") != "1" {
		t.Skip("explicit isolated mail real-main opt-in required")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("dedicated local PostgreSQL test database is required")
	}
	cfg, err := mailLifecycleDatabase(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("MAIL_LIFECYCLE_BINARY")
	if binary == "" {
		binary = filepath.Join("..", "..", "bin", "moni-aigate")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal("mail lifecycle binary path unavailable")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Skip("prebuilt real main binary is required")
	}
	admin, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("mail lifecycle owner pool unavailable")
	}
	defer admin.Close()
	for _, point := range []fixtureMailPoint{mailBeforeBanner, mailBeforeAcceptedReply} {
		name := "claimed_before_smtp_acceptance"
		if point == mailBeforeAcceptedReply {
			name = "smtp_accepted_before_database_ack"
		}
		t.Run(name, func(t *testing.T) {
			started := time.Now()
			evidence := mailProcessEvidence{Window: name}
			root := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
			defer cancel()
			var barrier *fixtureMailBarrier
			err := run(ctx, options{binary: binary, listen: "127.0.0.1:0", root: root, database: os.Getenv("TEST_DATABASE_URL"), identityFixture: true,
				mailFixtureReady: func(_ context.Context, f *mailFixture) error {
					var err error
					barrier, err = f.barrierAt(point)
					return err
				},
				mailLifecycle: func(ctx context.Context, live *privateLifecycle) error {
					return exerciseMailWindow(ctx, live, barrier, point, &evidence)
				},
			}, io.Discard)
			if err != nil {
				t.Fatal("mail lifecycle window failed", err)
			}
			paths, _ := filepath.Glob(filepath.Join(root, ".local", "browser-qa", "run-*", "manifest.json"))
			var m manifest
			if len(paths) != 1 {
				t.Fatal("mail lifecycle cleanup manifest missing")
			}
			raw, readErr := os.ReadFile(paths[0])
			if readErr != nil || json.Unmarshal(raw, &m) != nil || m.Status != "stopped" || len(m.Remaining) != 0 || m.ServeShutdown != "graceful" || !m.MailboxRemoved || !m.CounterReadValid || m.MailAccepted != evidence.MessagesPersisted {
				t.Fatal("mail lifecycle cleanup did not complete")
			}
			if m.BusinessCounts != (businessCounters{}) || m.MockCounts != (mockCounters{}) {
				t.Fatal("mail lifecycle created model or commercial facts")
			}
			cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if privateWaitRoleExit(cleanup, admin, m.AppRole, m.ProjectionRole) != nil {
				t.Fatal("mail lifecycle left role connections")
			}
			var remains bool
			if admin.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1) OR EXISTS(SELECT 1 FROM pg_roles WHERE rolname=ANY($2::text[]))`, m.Schema, []string{m.AppRole, m.ProjectionRole}).Scan(&remains) != nil || remains {
				t.Fatal("mail lifecycle left schema or roles")
			}
			for _, address := range []string{strings.TrimPrefix(m.AppOrigin, "http://"), strings.TrimPrefix(m.MockOrigin, "http://"), m.MailAddress} {
				c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
				if err == nil {
					c.Close()
					t.Fatal("mail lifecycle left listener")
				}
			}
			if os.RemoveAll(root) != nil {
				t.Fatal("mail lifecycle private files cleanup failed")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("mail lifecycle private files survived")
			}
			evidence.GracefulFinalShutdown, evidence.NoRolesSchemasOrConnections = true, true
			evidence.ListenersClosed, evidence.PrivateFilesRemoved = true, true
			evidence.ElapsedMS = time.Since(started).Milliseconds()
			safe, _ := json.Marshal(evidence)
			t.Log("mail_process_evidence=" + string(safe))
		})
	}
}
