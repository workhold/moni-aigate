package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type mailFixture struct{ subject, challenge, outbox, token string }

func seedIdentityMail(t *testing.T, f *identityFixture, purpose, to string, age, ttl time.Duration, rawPayload string) mailFixture {
	t.Helper()
	m := mailFixture{subject: uuid.NewString(), challenge: uuid.NewString(), outbox: uuid.NewString(), token: "ic1_" + security.Random(32)}
	created := time.Now().UTC().Add(-age)
	var user, revision, emailCipher any
	if purpose == "password_reset" {
		user = f.owner
		revision = int64(1)
	} else {
		ciphertext, err := f.store.Secrets.SealFor(to, "identity-registration", m.challenge)
		if err != nil {
			t.Fatal(err)
		}
		emailCipher = ciphertext
	}
	f.exec(t, `INSERT INTO identity_mail_subjects(id,purpose,email_digest,window_started_at,last_seen_at,next_issue_at)
 VALUES($1,$2,$3,$4,$4,$4)`, m.subject, purpose, f.store.Secrets.Digest("identity-mail-subject:v1:"+purpose+"\x00"+to), created)
	f.exec(t, `INSERT INTO identity_challenges(id,subject_id,purpose,token_digest,user_id,password_revision,registration_email_ciphertext,created_at,expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, m.challenge, m.subject, purpose, f.store.Secrets.Digest("identity-challenge:v1:"+purpose+":"+m.token), user, revision, emailCipher, created, created.Add(ttl))
	if rawPayload == "" {
		path := "/register/complete"
		if purpose == "password_reset" {
			path = "/password/reset"
		}
		body, err := json.Marshal(mailPayload{Version: 1, To: to, Purpose: purpose, Link: "https://portal.fixture.invalid" + path + "#token=" + m.token})
		if err != nil {
			t.Fatal(err)
		}
		rawPayload = string(body)
	}
	ciphertext, err := f.store.Secrets.SealFor(rawPayload, "identity-mail-outbox", m.outbox)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO identity_mail_outbox(id,challenge_id,payload_ciphertext,created_at,next_attempt_at)
 VALUES($1,$2,$3,$4,$4)`, m.outbox, m.challenge, ciphertext, created)
	return m
}

// Move fixture scheduling timestamps without sleeping for real 60-second
// leases/backoffs. Only this isolated owner fixture bypasses the state trigger;
// every production transition under test still runs with the trigger enabled.
func seedMailSchedule(t *testing.T, f *identityFixture, query string, args ...any) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, sql := range []string{`ALTER TABLE identity_mail_outbox DISABLE TRIGGER identity_outbox_guard`} {
		if _, err = tx.Exec(f.ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE identity_mail_outbox ENABLE TRIGGER identity_outbox_guard`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func oneMailClaim(t *testing.T, f *identityFixture) MailClaim {
	t.Helper()
	claims, err := f.store.ClaimMail(f.ctx, MailPurposes{Registration: true, PasswordReset: true}, 1)
	if err != nil || len(claims) != 1 {
		t.Fatal("expected one durable claim", len(claims), err)
	}
	return claims[0]
}

func TestMailOutboxClaimsAreUniqueAndDoNotLockChallenge(t *testing.T) {
	f := newIdentityFixture(t)
	m := seedIdentityMail(t, f, "registration", "one@fixture.invalid", 0, time.Hour, "")
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, `SELECT id FROM identity_challenges WHERE id=$1 FOR UPDATE`, m.challenge); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	claims, err := f.store.ClaimMail(ctx, MailPurposes{Registration: true}, 2)
	if err != nil || len(claims) != 1 || claims[0].Generation != 1 || claims[0].Attempt != 1 || time.Until(claims[0].LeaseUntil) < 58*time.Second {
		t.Fatal("claim implicitly locked identity parent or changed lease", err, len(claims))
	}
	if ok, err := f.store.FinishMail(ctx, claims[0], MailResult{Outcome: "accepted"}); err != nil || !ok {
		t.Fatal("ACK locked parent identity row", ok, err)
	}
	if err = blocker.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		seedIdentityMail(t, f, "registration", fmt.Sprintf("concurrent-%d@fixture.invalid", i), 0, time.Hour, "")
	}
	var group sync.WaitGroup
	results := make(chan []MailClaim, 12)
	errors := make(chan error, 12)
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			items, err := f.store.ClaimMail(f.ctx, MailPurposes{Registration: true}, 2)
			results <- items
			errors <- err
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for items := range results {
		if len(items) > 2 {
			t.Fatal("repository overclaimed slots")
		}
		for _, item := range items {
			if seen[item.ID] {
				t.Fatal("two claimers received the same current generation")
			}
			seen[item.ID] = true
		}
	}
	if len(seen) != 12 {
		t.Fatal("eligible claims were lost", len(seen))
	}
}

func TestMailOutboxPrepareGenerationCASAndAcceptedSecretErasure(t *testing.T) {
	f := newIdentityFixture(t)
	m := seedIdentityMail(t, f, "registration", "prepare@fixture.invalid", 0, time.Hour, "")
	first := oneMailClaim(t, f)
	prepared, err := f.store.PrepareMail(f.ctx, first)
	if err != nil || prepared.Status != "ready" || prepared.Delivery.ID != m.outbox || prepared.RemainingLease < 58*time.Second || !strings.HasSuffix(prepared.Delivery.Link, m.token) {
		t.Fatal("valid versioned encrypted payload not prepared", prepared.Status, err)
	}
	seedMailSchedule(t, f, `UPDATE identity_mail_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, m.outbox)
	if short, err := f.store.PrepareMail(f.ctx, first); err != nil || short.Status != "lease_short" || short.Delivery.Link != "" {
		t.Fatal("expired lease exposed send material", short.Status, err)
	}
	second := oneMailClaim(t, f)
	if second.Generation != 2 || second.Attempt != 2 {
		t.Fatal("crash recovery reset generation/count")
	}
	if stale, err := f.store.PrepareMail(f.ctx, first); err != nil || stale.Status != "stale" {
		t.Fatal("stale claim prepared a payload", err)
	}
	if ok, err := f.store.FinishMail(f.ctx, first, MailResult{Outcome: "accepted"}); err != nil || ok {
		t.Fatal("old generation overwrote recovered claim", ok, err)
	}
	if ok, err := f.store.FinishMail(f.ctx, second, MailResult{Outcome: "accepted"}); err != nil || !ok {
		t.Fatal("current claim not ACKed", ok, err)
	}
	var state string
	var payload *string
	var lease, sent *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT state,payload_ciphertext,lease_until,sent_at FROM identity_mail_outbox WHERE id=$1`, m.outbox).Scan(&state, &payload, &lease, &sent); err != nil || state != "sent" || payload != nil || lease != nil || sent == nil {
		t.Fatal("accepted mail retained token or lease", state, err)
	}
}

func TestMailOutboxRetryBackoffAndFifthClaimCrashCleanup(t *testing.T) {
	f := newIdentityFixture(t)
	m := seedIdentityMail(t, f, "registration", "retry@fixture.invalid", 0, time.Hour, "")
	backoffs := []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute}
	for i := range 5 {
		claim := oneMailClaim(t, f)
		if claim.Attempt != i+1 || claim.Generation != int64(i+1) {
			t.Fatal("retry count/generation reset")
		}
		if i == 4 {
			// Simulate process loss after the final claim, without any ACK.
			seedMailSchedule(t, f, `UPDATE identity_mail_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, m.outbox)
			break
		}
		if ok, err := f.store.FinishMail(f.ctx, claim, MailResult{Outcome: "retry", Code: "mail_temporary"}); err != nil || !ok {
			t.Fatal("retry transition rejected", ok, err)
		}
		var state string
		var delay float64
		if err := f.pool.QueryRow(f.ctx, `SELECT state,extract(epoch FROM(next_attempt_at-clock_timestamp()))::float8 FROM identity_mail_outbox WHERE id=$1`, m.outbox).Scan(&state, &delay); err != nil || state != "pending" || delay < backoffs[i].Seconds()-1 || delay > backoffs[i].Seconds()+1 {
			t.Fatal("incorrect retry delay", i+1, state, delay, err)
		}
		seedMailSchedule(t, f, `UPDATE identity_mail_outbox SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, m.outbox)
	}
	if claims, err := f.store.ClaimMail(f.ctx, MailPurposes{Registration: true}, 2); err != nil || len(claims) != 0 {
		t.Fatal("sixth attempt was claimed", len(claims), err)
	}
	cleaned, err := f.store.CleanupMail(f.ctx, 100)
	if err != nil || cleaned.Dead != 1 {
		t.Fatal("fifth crash claim remained permanently sending", cleaned, err)
	}
	var state, code string
	var payload *string
	if err := f.pool.QueryRow(f.ctx, `SELECT state,last_error_code,payload_ciphertext FROM identity_mail_outbox WHERE id=$1`, m.outbox).Scan(&state, &code, &payload); err != nil || state != "dead" || code != "mail_attempts_exhausted" || payload != nil {
		t.Fatal("dead claim retained secret or claimed definite no delivery", state, code, err)
	}
}

func TestMailOutboxCleanupCancelsExpiredAndChangedPasswordButKeepsActive(t *testing.T) {
	f := newIdentityFixture(t)
	expired := seedIdentityMail(t, f, "registration", "expired@fixture.invalid", 2*time.Hour, time.Hour, "")
	active := seedIdentityMail(t, f, "registration", "active@fixture.invalid", 0, time.Hour, "")
	reset := seedIdentityMail(t, f, "password_reset", "owner@identity.test", 0, time.Hour, "")
	f.exec(t, `UPDATE users SET password_hash='changed-test-hash' WHERE id=$1`, f.owner)
	cleaned, err := f.store.CleanupMail(f.ctx, 100)
	if err != nil || cleaned.Cancelled != 2 || cleaned.SecretsCleared != 1 {
		t.Fatal("cleanup missed proof invalidation", cleaned, err)
	}
	for _, id := range []string{expired.outbox, reset.outbox} {
		var state string
		var payload *string
		if err := f.pool.QueryRow(f.ctx, `SELECT state,payload_ciphertext FROM identity_mail_outbox WHERE id=$1`, id).Scan(&state, &payload); err != nil || state != "cancelled" || payload != nil {
			t.Fatal("invalid proof still sendable", state, err)
		}
	}
	var email *string
	var consumed, revoked *time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT registration_email_ciphertext,consumed_at,revoked_at FROM identity_challenges WHERE id=$1`, active.challenge).Scan(&email, &consumed, &revoked); err != nil || email == nil || consumed != nil || revoked != nil {
		t.Fatal("cleanup consumed/revoked or erased an active proof", err)
	}
	claims, err := f.store.ClaimMail(f.ctx, MailPurposes{Registration: true}, 2)
	if err != nil || len(claims) != 1 || claims[0].ID != active.outbox {
		t.Fatal("invalid proofs remained eligible", len(claims), err)
	}
}

func TestMailOutboxCleanupRetentionAndLimit(t *testing.T) {
	f := newIdentityFixture(t)
	for i := range 3 {
		seedIdentityMail(t, f, "registration", fmt.Sprintf("retention-%d@fixture.invalid", i), 8*24*time.Hour, time.Hour, "")
	}
	first, err := f.store.CleanupMail(f.ctx, 2)
	if err != nil || first.Cancelled != 2 || first.OutboxesDeleted != 2 || first.ChallengesDeleted != 2 || first.SubjectsDeleted != 2 {
		t.Fatal("bounded parent-first retention cleanup failed", first, err)
	}
	second, err := f.store.CleanupMail(f.ctx, 2)
	if err != nil || second.OutboxesDeleted != 1 || second.ChallengesDeleted != 1 || second.SubjectsDeleted != 1 {
		t.Fatal("remaining retention candidates not cleaned", second, err)
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM identity_mail_subjects`).Scan(&count); err != nil || count != 0 {
		t.Fatal("retained metadata or secret survived", count, err)
	}
}

func TestMailOutboxInvalidPayloadAndPurposeBindingNeverEscape(t *testing.T) {
	f := newIdentityFixture(t)
	for i, raw := range []string{`{"v":1,"to":"invalid@fixture.invalid","purpose":"registration","link":"private-payload-canary"}`, `{"v":1,"v":1,"to":"invalid@fixture.invalid","purpose":"registration","link":"private-payload-canary"}`, `{"V":1,"to":"invalid@fixture.invalid","purpose":"registration","link":"private-payload-canary"}`, `{"v":2,"to":"invalid@fixture.invalid","purpose":"registration","link":"private-payload-canary"}`} {
		seedIdentityMail(t, f, "registration", fmt.Sprintf("invalid-%d@fixture.invalid", i), 0, time.Hour, raw)
		claim := oneMailClaim(t, f)
		prepared, err := f.store.PrepareMail(f.ctx, claim)
		if err != nil || prepared.Status != "dead" || prepared.Code != "mail_payload_invalid" || prepared.Delivery.Link != "" || prepared.Delivery.To != "" {
			t.Fatal("malformed payload returned delivery material", prepared.Status, prepared.Code, err)
		}
		if _, err := f.store.FinishMail(f.ctx, claim, MailResult{Outcome: "dead", Code: prepared.Code}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.ClaimMail(f.ctx, MailPurposes{Registration: true}, 3); err != ErrMailOutboxInput {
		t.Fatal("unbounded claim limit accepted", err)
	}
	if _, err := f.store.FinishMail(f.ctx, MailClaim{ID: uuid.NewString(), Generation: 1}, MailResult{Outcome: "dead", Code: "private-payload-canary"}); err != ErrMailOutboxInput {
		t.Fatal("arbitrary persisted error text accepted", err)
	}
}

func TestMailOutboxMissingKeysAndEncryptedIdentityBinding(t *testing.T) {
	for _, scenario := range []string{"missing_key", "different_outbox", "different_purpose", "different_recipient", "different_token"} {
		t.Run(scenario, func(t *testing.T) {
			f := newIdentityFixture(t)
			m := seedIdentityMail(t, f, "registration", "bound@fixture.invalid", 0, time.Hour, "")
			claim := oneMailClaim(t, f)
			store := f.store
			wantCode := "mail_payload_invalid"
			if scenario == "missing_key" {
				store = New(f.pool, &security.Secrets{Keys: map[string][]byte{}, Pepper: f.store.Secrets.Pepper})
				wantCode = "mail_decrypt"
			} else {
				payload := mailPayload{Version: 1, To: "bound@fixture.invalid", Purpose: "registration", Link: "https://portal.fixture.invalid/register/complete#token=" + m.token}
				sealedID := m.outbox
				switch scenario {
				case "different_outbox":
					sealedID = uuid.NewString()
					wantCode = "mail_decrypt"
				case "different_purpose":
					payload.Purpose = "password_reset"
				case "different_recipient":
					payload.To = "another@fixture.invalid"
				case "different_token":
					payload.Link = "https://portal.fixture.invalid/register/complete#token=ic1_" + security.Random(32)
				}
				body, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				ciphertext, err := f.store.Secrets.SealFor(string(body), "identity-mail-outbox", sealedID)
				if err != nil {
					t.Fatal(err)
				}
				// Only the isolated table owner may replace a ciphertext. The
				// runtime must still reject wrong AD or plaintext bindings.
				f.exec(t, `UPDATE identity_mail_outbox SET payload_ciphertext=$2 WHERE id=$1`, m.outbox, ciphertext)
			}
			prepared, err := store.PrepareMail(f.ctx, claim)
			if err != nil || prepared.Status != "dead" || prepared.Code != wantCode || prepared.Delivery != (MailDelivery{}) {
				t.Fatal("unbound or undecryptable material escaped preparation", prepared.Status, prepared.Code, err)
			}
			if ok, err := store.FinishMail(f.ctx, claim, MailResult{Outcome: "dead", Code: prepared.Code}); err != nil || !ok {
				t.Fatal("unusable material did not terminate", ok, err)
			}
		})
	}
}

func TestMailOutboxClaimedProofInvalidationCancelsPreparationAndRetry(t *testing.T) {
	for _, terminalColumn := range []string{"consumed_at", "revoked_at"} {
		t.Run(terminalColumn, func(t *testing.T) {
			f := newIdentityFixture(t)
			m := seedIdentityMail(t, f, "registration", "inactive@fixture.invalid", 0, time.Hour, "")
			claim := oneMailClaim(t, f)
			f.exec(t, `UPDATE identity_challenges SET `+terminalColumn+`=clock_timestamp() WHERE id=$1`, m.challenge)
			prepared, err := f.store.PrepareMail(f.ctx, claim)
			if err != nil || prepared.Status != "cancelled" || prepared.Code != "mail_proof_inactive" || prepared.Delivery != (MailDelivery{}) {
				t.Fatal("inactive proof still exposed delivery", prepared.Status, err)
			}
			// This also models a proof invalidated while SMTP was in progress.
			if ok, err := f.store.FinishMail(f.ctx, claim, MailResult{Outcome: "retry", Code: "mail_network"}); err != nil || !ok {
				t.Fatal("inactive retry transition failed", ok, err)
			}
			var state, code string
			var payload *string
			if err := f.pool.QueryRow(f.ctx, `SELECT state,last_error_code,payload_ciphertext FROM identity_mail_outbox WHERE id=$1`, m.outbox).Scan(&state, &code, &payload); err != nil || state != "cancelled" || code != "mail_proof_inactive" || payload != nil {
				t.Fatal("inactive proof requeued or retained its payload", state, code, err)
			}
		})
	}
}

func TestMailOutboxFifthTemporaryFailureTerminates(t *testing.T) {
	f := newIdentityFixture(t)
	m := seedIdentityMail(t, f, "registration", "fifth@fixture.invalid", 0, time.Hour, "")
	for attempt := 1; attempt <= 5; attempt++ {
		claim := oneMailClaim(t, f)
		if claim.Attempt != attempt {
			t.Fatal("attempt count changed", claim.Attempt)
		}
		if ok, err := f.store.FinishMail(f.ctx, claim, MailResult{Outcome: "retry", Code: "mail_temporary"}); err != nil || !ok {
			t.Fatal("temporary failure could not finish", ok, err)
		}
		if attempt < 5 {
			seedMailSchedule(t, f, `UPDATE identity_mail_outbox SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, m.outbox)
		}
	}
	var state, code string
	var payload *string
	if err := f.pool.QueryRow(f.ctx, `SELECT state,last_error_code,payload_ciphertext FROM identity_mail_outbox WHERE id=$1`, m.outbox).Scan(&state, &code, &payload); err != nil || state != "dead" || code != "mail_attempts_exhausted" || payload != nil {
		t.Fatal("fifth temporary failure retained a retryable secret", state, code, err)
	}
}

func TestMailOutboxPurposeFiltersAndDisabledRepositoryNeedNoDatabase(t *testing.T) {
	if claims, err := New(nil, nil).ClaimMail(context.Background(), MailPurposes{}, 2); err != nil || len(claims) != 0 {
		t.Fatal("disabled mail accessed the database", err)
	}
	f := newIdentityFixture(t)
	seedIdentityMail(t, f, "registration", "filtered@fixture.invalid", 0, time.Hour, "")
	reset := seedIdentityMail(t, f, "password_reset", "owner@identity.test", 0, time.Hour, "")
	claims, err := f.store.ClaimMail(f.ctx, MailPurposes{PasswordReset: true}, 2)
	if err != nil || len(claims) != 1 || claims[0].ID != reset.outbox || claims[0].Purpose != "password_reset" {
		t.Fatal("disabled purpose was claimed", len(claims), err)
	}
}

func TestMailOutboxCleanupSkipsLockedSubjectAndNeverLocksUser(t *testing.T) {
	f := newIdentityFixture(t)
	registration := seedIdentityMail(t, f, "registration", "locked@fixture.invalid", 2*time.Hour, time.Hour, "")
	reset := seedIdentityMail(t, f, "password_reset", "owner@identity.test", 2*time.Hour, time.Hour, "")
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, `SELECT id FROM identity_mail_subjects WHERE id=$1 FOR UPDATE`, registration.subject); err != nil {
		t.Fatal(err)
	}
	if _, err = blocker.Exec(f.ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, f.owner); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	cleaned, err := f.store.CleanupMail(ctx, 100)
	if err != nil || cleaned.Cancelled != 1 {
		t.Fatal("cleanup waited on an unrelated user or locked subject", cleaned, err)
	}
	var resetState, registrationState string
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT state FROM identity_mail_outbox WHERE id=$1),(SELECT state FROM identity_mail_outbox WHERE id=$2)`, reset.outbox, registration.outbox).Scan(&resetState, &registrationState); err != nil || resetState != "cancelled" || registrationState != "pending" {
		t.Fatal("cleanup changed a skipped parent's child", resetState, registrationState, err)
	}
}

func TestMailOutboxProvisionedAppRoleSupportsWorkerLifecycle(t *testing.T) {
	f := newIdentityFixture(t)
	cfg := f.pool.Config()
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("role fixtures require the dedicated local moni_test database on port 55439")
	}
	var schema string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	appRole, projectionRole := "mail_app_"+suffix, "mail_projection_"+suffix
	var roles []string
	var app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, role := range roles {
			if _, err := f.pool.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("mail fixture role grant cleanup failed", err)
			}
			if _, err := f.pool.Exec(ctx, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Error("mail fixture role cleanup failed", err)
			}
		}
	})
	for _, role := range []string{appRole, projectionRole} {
		if _, err := f.pool.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOINHERIT NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role)
	}
	if err := operations.ProvisionFor(f.ctx, f.pool, schema, appRole, projectionRole); err != nil {
		t.Fatal(err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{appRole}.Sanitize())
		return err
	}
	var err error
	app, err = pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	store := New(app, f.store.Secrets)
	m := seedIdentityMail(t, f, "registration", "app@fixture.invalid", 0, time.Hour, "")
	claims, err := store.ClaimMail(f.ctx, MailPurposes{Registration: true}, 1)
	if err != nil || len(claims) != 1 || claims[0].ID != m.outbox {
		t.Fatal("provisioned app could not claim", len(claims), err)
	}
	prepared, err := store.PrepareMail(f.ctx, claims[0])
	if err != nil || prepared.Status != "ready" {
		t.Fatal("provisioned app could not prepare", prepared.Status, err)
	}
	if ok, err := store.FinishMail(f.ctx, claims[0], MailResult{Outcome: "accepted"}); err != nil || !ok {
		t.Fatal("provisioned app could not ACK", ok, err)
	}
	seedIdentityMail(t, f, "registration", "app-retention@fixture.invalid", 8*24*time.Hour, time.Hour, "")
	cleaned, err := store.CleanupMail(f.ctx, 100)
	if err != nil || cleaned.Cancelled != 1 || cleaned.SecretsCleared != 1 || cleaned.OutboxesDeleted != 1 || cleaned.ChallengesDeleted != 1 || cleaned.SubjectsDeleted != 1 {
		t.Fatal("provisioned app could not clean parent-first lifecycle", cleaned, err)
	}
}
