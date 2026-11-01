package identity

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Only the fixture clock is moved; production always checks PostgreSQL time.
// This avoids a five-minute test while exercising the real durable counters.
func selfServiceAdvanceIssueFixture(t *testing.T, f *identityFixture, purpose, email string, newHour bool) {
	t.Helper()
	tx, e := f.pool.Begin(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(f.ctx)
	if _, e = tx.Exec(f.ctx, `ALTER TABLE identity_mail_subjects DISABLE TRIGGER identity_subject_guard`); e != nil {
		t.Fatal(e)
	}
	q := `UPDATE identity_mail_subjects SET next_issue_at=clock_timestamp()-interval '1 second' WHERE purpose=$1 AND email_digest=$2`
	if newHour {
		q = `UPDATE identity_mail_subjects SET next_issue_at=clock_timestamp()-interval '1 second',window_started_at=clock_timestamp()-interval '2 hours' WHERE purpose=$1 AND email_digest=$2`
	}
	if _, e = tx.Exec(f.ctx, q, purpose, f.store.identitySubjectDigest(purpose, email)); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(f.ctx, `ALTER TABLE identity_mail_subjects ENABLE TRIGGER identity_subject_guard`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(f.ctx); e != nil {
		t.Fatal(e)
	}
}

func TestSelfServiceIssuanceSuppressionDurableLimitsReplacementAndPurpose(t *testing.T) {
	f := newSelfServiceEmptyFixture(t)
	if e := f.store.Bootstrap(f.ctx, "admin@identity.test", identityTestPassword); e != nil {
		t.Fatal(e)
	}
	// Ineligible identities create neither fake subjects nor email evidence.
	for _, v := range []struct{ purpose, email string }{{IdentityPurposeRegistration, "admin@identity.test"}, {IdentityPurposePasswordReset, "missing@identity.test"}} {
		if e := f.store.RequestIdentityMail(f.ctx, v.purpose, v.email, selfServiceOrigin); e != nil {
			t.Fatal(e)
		}
	}
	f.exec(t, `UPDATE users SET status='suspended' WHERE email='admin@identity.test'`)
	if e := f.store.RequestIdentityMail(f.ctx, IdentityPurposePasswordReset, "admin@identity.test", selfServiceOrigin); e != nil {
		t.Fatal(e)
	}
	f.exec(t, `UPDATE users SET status='active' WHERE email='admin@identity.test'`)
	var subjects, challenges, outboxes int
	if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM identity_mail_subjects),(SELECT count(*) FROM identity_challenges),(SELECT count(*) FROM identity_mail_outbox)`).Scan(&subjects, &challenges, &outboxes); e != nil || subjects != 0 || challenges != 0 || outboxes != 0 {
		t.Fatal("ineligible requests persisted identity facts", subjects, challenges, outboxes, e)
	}
	const email = "new@identity.test"
	done := make(chan error, 2)
	for range 2 {
		go func() {
			done <- f.store.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, email, selfServiceOrigin)
		}()
	}
	for range 2 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	first, firstToken := selfServiceMailToken(t, f, IdentityPurposeRegistration, email)
	// A new Store instance must observe the same cooldown persisted in SQL.
	restarted := New(f.pool, f.store.Secrets)
	if e := restarted.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, email, selfServiceOrigin); e != nil {
		t.Fatal(e)
	}
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM identity_challenges`).Scan(&challenges); e != nil || challenges != 1 {
		t.Fatal("cooldown or concurrent requests duplicated proof", challenges, e)
	}
	previous := first
	for issue := 2; issue <= 5; issue++ {
		selfServiceAdvanceIssueFixture(t, f, IdentityPurposeRegistration, email, false)
		current, _ := selfServiceRequest(t, f, IdentityPurposeRegistration, email)
		var revoked, cleared bool
		if e := f.pool.QueryRow(f.ctx, `SELECT revoked_at IS NOT NULL,registration_email_ciphertext IS NULL FROM identity_challenges WHERE id=$1`, previous).Scan(&revoked, &cleared); e != nil || !revoked || !cleared {
			t.Fatal("new issue did not revoke and erase old registration secret", e)
		}
		previous = current
	}
	selfServiceAdvanceIssueFixture(t, f, IdentityPurposeRegistration, email, false)
	if e := restarted.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, email, selfServiceOrigin); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM identity_challenges`).Scan(&challenges); e != nil || challenges != 5 {
		t.Fatal("hourly cap did not persist across Store recreation", challenges, e)
	}
	if e := f.pool.QueryRow(f.ctx, `SELECT window_count FROM identity_mail_subjects WHERE purpose=$1 AND email_digest=$2`, IdentityPurposeRegistration, f.store.identitySubjectDigest(IdentityPurposeRegistration, email)).Scan(&count); e != nil || count != 5 {
		t.Fatal("incorrect hourly counter", count, e)
	}
	if e := f.store.CompletePasswordReset(f.ctx, firstToken, selfServicePassword, ""); !errors.Is(e, ErrIdentityProofInvalid) {
		t.Fatal("registration token accepted for reset", e)
	}
	if e := f.store.CompleteRegistration(f.ctx, firstToken, selfServicePassword, "Tenant"); !errors.Is(e, ErrIdentityProofInvalid) {
		t.Fatal("replaced token accepted", e)
	}
	selfServiceAdvanceIssueFixture(t, f, IdentityPurposeRegistration, email, true)
	if e := restarted.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, email, selfServiceOrigin); e != nil {
		t.Fatal(e)
	}
	if e := f.pool.QueryRow(f.ctx, `SELECT window_count FROM identity_mail_subjects WHERE purpose=$1 AND email_digest=$2`, IdentityPurposeRegistration, f.store.identitySubjectDigest(IdentityPurposeRegistration, email)).Scan(&count); e != nil || count != 1 {
		t.Fatal("new fixed-hour window did not reset to first issue", count, e)
	}
	_, resetToken := selfServiceRequest(t, f, IdentityPurposePasswordReset, "admin@identity.test")
	if e := f.store.CompleteRegistration(f.ctx, resetToken, selfServicePassword, "Tenant"); !errors.Is(e, ErrIdentityProofInvalid) {
		t.Fatal("reset token accepted for registration", e)
	}
}

func TestSelfServiceRequestAuditFailureRollsBackProofAndOutbox(t *testing.T) {
	f := newSelfServiceEmptyFixture(t)
	if e := f.store.Bootstrap(f.ctx, "admin@identity.test", identityTestPassword); e != nil {
		t.Fatal(e)
	}
	f.exec(t, `CREATE FUNCTION fail_identity_mail_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='identity.mail.request' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$`)
	f.exec(t, `CREATE TRIGGER fail_identity_mail_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION fail_identity_mail_audit()`)
	if e := f.store.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, "new@identity.test", selfServiceOrigin); e == nil || errors.Is(e, pgx.ErrNoRows) {
		t.Fatal("audit failure was hidden", e)
	}
	var subjects, challenges, outboxes int
	if e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM identity_mail_subjects),(SELECT count(*) FROM identity_challenges),(SELECT count(*) FROM identity_mail_outbox)`).Scan(&subjects, &challenges, &outboxes); e != nil || subjects != 0 || challenges != 0 || outboxes != 0 {
		t.Fatal("audit failure committed partial mail proof", subjects, challenges, outboxes, e)
	}
}
