package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/security"
)

const IdentityPurposeRegistration = "registration"
const IdentityPurposePasswordReset = "password_reset"

var ErrIdentityRequestInvalid = errors.New("identity_request_invalid")
var ErrIdentityProofInvalid = errors.New("identity_proof_invalid")
var ErrIdentityUnavailable = errors.New("identity_unavailable")

func NormalizeIdentityEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) == 0 || len(email) > 254 {
		return "", ErrIdentityRequestInvalid
	}
	for _, r := range email {
		if r < 33 || r > 126 {
			return "", ErrIdentityRequestInvalid
		}
	}
	a, e := mail.ParseAddress(email)
	if e != nil || a.Name != "" || a.Address != email {
		return "", ErrIdentityRequestInvalid
	}
	return email, nil
}

func ValidIdentityToken(raw string) bool {
	if len(raw) != 47 || !strings.HasPrefix(raw, "ic1_") {
		return false
	}
	b, e := base64.RawURLEncoding.DecodeString(raw[4:])
	return e == nil && len(b) == 32 && "ic1_"+base64.RawURLEncoding.EncodeToString(b) == raw
}

func identityPurpose(p string) bool {
	return p == IdentityPurposeRegistration || p == IdentityPurposePasswordReset
}
func (s *Store) identityTokenDigest(p, token string) string {
	return s.Secrets.Digest("identity-challenge:v1:" + p + ":" + token)
}
func (s *Store) identitySubjectDigest(p, email string) string {
	return s.Secrets.Digest("identity-mail-subject:v1:" + p + "\x00" + email)
}

func (s *Store) Initialized(ctx context.Context) (bool, error) {
	var initialized bool
	e := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_initialization WHERE singleton)`).Scan(&initialized)
	return initialized, e
}

// RequestIdentityMail returns no eligibility or queue information. HTTP owns
// startup feature policy, origin checking and anonymous rate limits.
func (s *Store) RequestIdentityMail(ctx context.Context, purpose, email, origin string) error {
	normalized, e := NormalizeIdentityEmail(email)
	if e != nil || !identityPurpose(purpose) {
		return ErrIdentityRequestInvalid
	}
	// A trusted origin must still be structurally an origin when this internal
	// method is called without the HTTP adapter (for example by integration tests).
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Scheme+"://"+u.Host != origin || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return ErrIdentityRequestInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var user string
	var revision int64
	if purpose == IdentityPurposeRegistration {
		var initialized, exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_initialization),EXISTS(SELECT 1 FROM users WHERE email=$1)`, normalized).Scan(&initialized, &exists); e != nil {
			return e
		}
		if !initialized {
			return ErrIdentityUnavailable
		}
		if exists {
			return nil
		}
	} else {
		// Lock the user before the mail subject, matching password/MFA/session paths.
		if e = tx.QueryRow(ctx, `SELECT id,password_revision FROM users WHERE email=$1 AND status='active' FOR UPDATE`, normalized).Scan(&user, &revision); errors.Is(e, pgx.ErrNoRows) {
			return nil
		} else if e != nil {
			return e
		}
	}
	digest := s.identitySubjectDigest(purpose, normalized)
	if _, e = tx.Exec(ctx, `INSERT INTO identity_mail_subjects(purpose,email_digest) VALUES($1,$2) ON CONFLICT(purpose,email_digest) DO NOTHING`, purpose, digest); e != nil {
		return e
	}
	var subject string
	var window, next, now time.Time
	var count int
	if e = tx.QueryRow(ctx, `SELECT id::text,window_started_at,window_count,next_issue_at FROM identity_mail_subjects WHERE purpose=$1 AND email_digest=$2 FOR UPDATE`, purpose, digest).Scan(&subject, &window, &count, &next); e != nil {
		return e
	}
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return e
	}
	if now.Before(next) || now.Before(window.Add(time.Hour)) && count >= 5 {
		if _, e = tx.Exec(ctx, `UPDATE identity_mail_subjects SET last_seen_at=$2 WHERE id=$1`, subject, now); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	if !now.Before(window.Add(time.Hour)) {
		window = now
		count = 0
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_mail_subjects SET window_started_at=$2,window_count=$3,next_issue_at=$4,last_seen_at=$5 WHERE id=$1`, subject, window, count+1, now.Add(time.Minute), now); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_challenges SET revoked_at=$2,registration_email_ciphertext=NULL WHERE subject_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL`, subject, now); e != nil {
		return e
	}
	challenge, outbox, token := uuid.NewString(), uuid.NewString(), "ic1_"+security.Random(32)
	ttl := 15 * time.Minute
	var regEmail, uid, rev any
	path := "/password/reset"
	if purpose == IdentityPurposeRegistration {
		ttl = 30 * time.Minute
		path = "/register/complete"
		regEmail, e = s.Secrets.SealFor(normalized, "identity-registration", challenge)
		if e != nil {
			return e
		}
	} else {
		uid = user
		rev = revision
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_challenges(id,subject_id,purpose,token_digest,user_id,password_revision,registration_email_ciphertext,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, challenge, subject, purpose, s.identityTokenDigest(purpose, token), uid, rev, regEmail, now, now.Add(ttl)); e != nil {
		return e
	}
	payload, e := json.Marshal(struct {
		V       int    `json:"v"`
		To      string `json:"to"`
		Purpose string `json:"purpose"`
		Link    string `json:"link"`
	}{1, normalized, purpose, origin + path + "#token=" + token})
	if e != nil {
		return e
	}
	encrypted, e := s.Secrets.SealFor(string(payload), "identity-mail-outbox", outbox)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_mail_outbox(id,challenge_id,payload_ciphertext,created_at,next_attempt_at) VALUES($1,$2,$3,$4,$4)`, outbox, challenge, encrypted, now); e != nil {
		return e
	}
	if e = audit(ctx, tx, "identity-self-service", "", "identity.mail.request", challenge); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type identityProof struct {
	id, subject, purpose, digest string
	user                         *string
	revision                     *int64
	email                        *string
	expires                      time.Time
	consumed, revoked            *time.Time
}

func (s *Store) locateIdentityProof(ctx context.Context, purpose, token string) (identityProof, error) {
	var p identityProof
	e := s.Pool.QueryRow(ctx, `SELECT id::text,subject_id::text,user_id FROM identity_challenges WHERE purpose=$1 AND token_digest=$2`, purpose, s.identityTokenDigest(purpose, token)).Scan(&p.id, &p.subject, &p.user)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrIdentityProofInvalid
	}
	return p, e
}
func lockIdentityProof(ctx context.Context, tx pgx.Tx, p *identityProof) error {
	var subject string
	if e := tx.QueryRow(ctx, `SELECT id::text FROM identity_mail_subjects WHERE id=$1 FOR UPDATE`, p.subject).Scan(&subject); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrIdentityProofInvalid
		}
		return e
	}
	e := tx.QueryRow(ctx, `SELECT purpose,token_digest,user_id,password_revision,registration_email_ciphertext,expires_at,consumed_at,revoked_at FROM identity_challenges WHERE id=$1 AND subject_id=$2 FOR UPDATE`, p.id, p.subject).Scan(&p.purpose, &p.digest, &p.user, &p.revision, &p.email, &p.expires, &p.consumed, &p.revoked)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrIdentityProofInvalid
	}
	return e
}
func (s *Store) validIdentityProof(ctx context.Context, tx pgx.Tx, p identityProof, purpose, token string) error {
	var now time.Time
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return e
	}
	if p.purpose != purpose || p.digest != s.identityTokenDigest(purpose, token) || p.consumed != nil || p.revoked != nil || !now.Before(p.expires) {
		return ErrIdentityProofInvalid
	}
	return nil
}
func consumeIdentityProof(ctx context.Context, tx pgx.Tx, p identityProof) error {
	if _, e := tx.Exec(ctx, `UPDATE identity_challenges SET consumed_at=clock_timestamp(),registration_email_ciphertext=NULL WHERE id=$1`, p.id); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `UPDATE identity_challenges SET revoked_at=clock_timestamp(),registration_email_ciphertext=NULL WHERE subject_id=$1 AND id<>$2 AND consumed_at IS NULL AND revoked_at IS NULL`, p.subject, p.id)
	return e
}

func (s *Store) CompleteRegistration(ctx context.Context, token, password, tenantName string) error {
	tenantName = strings.TrimSpace(tenantName)
	if !ValidIdentityToken(token) {
		return ErrIdentityProofInvalid
	}
	if len(password) < 12 || len(password) > 72 || len(tenantName) == 0 || len(tenantName) > 200 || strings.IndexFunc(tenantName, unicode.IsControl) >= 0 {
		return ErrIdentityRequestInvalid
	}
	p, e := s.locateIdentityProof(ctx, IdentityPurposeRegistration, token)
	if e != nil {
		return e
	}
	hash, e := security.HashPassword(password)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockIdentityProof(ctx, tx, &p); e != nil {
		return e
	}
	if e = s.validIdentityProof(ctx, tx, p, IdentityPurposeRegistration, token); e != nil {
		return e
	}
	if p.email == nil {
		return ErrIdentityProofInvalid
	}
	email, e := s.Secrets.OpenFor(*p.email, "identity-registration", p.id)
	if e != nil {
		return e
	}
	normalized, e := NormalizeIdentityEmail(email)
	if e != nil || normalized != email {
		return ErrIdentityProofInvalid
	}
	var digest string
	if e = tx.QueryRow(ctx, `SELECT email_digest FROM identity_mail_subjects WHERE id=$1`, p.subject).Scan(&digest); e != nil {
		return e
	}
	if digest != s.identitySubjectDigest(IdentityPurposeRegistration, email) {
		return ErrIdentityProofInvalid
	}
	uid, tid := uuid.NewString(), uuid.NewString()
	if _, e = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role,status,email_verified_at) VALUES($1,$2,$3,'none','active',clock_timestamp())`, uid, email, hash); e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return ErrIdentityProofInvalid
		}
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO tenants(id,name) VALUES($1,$2)`, tid, tenantName); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role,status,allow_all_models) VALUES($1,$2,'owner','active',false)`, tid, uid); e != nil {
		return e
	}
	// Recheck after unique-insert/constraint waits; transaction time is not proof time.
	if e = s.validIdentityProof(ctx, tx, p, IdentityPurposeRegistration, token); e != nil {
		return e
	}
	var initialized bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_initialization)`).Scan(&initialized); e != nil {
		return e
	}
	if !initialized {
		return ErrIdentityUnavailable
	}
	if e = consumeIdentityProof(ctx, tx, p); e != nil {
		return e
	}
	if e = audit(ctx, tx, uid, tid, "identity.registration.complete", p.id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) CompletePasswordReset(ctx context.Context, token, password, mfaCode string) error {
	if !ValidIdentityToken(token) {
		return ErrIdentityProofInvalid
	}
	if len(password) < 12 || len(password) > 72 || len(mfaCode) > 128 || strings.IndexFunc(mfaCode, unicode.IsControl) >= 0 {
		return ErrIdentityRequestInvalid
	}
	p, e := s.locateIdentityProof(ctx, IdentityPurposePasswordReset, token)
	if e != nil {
		return e
	}
	if p.user == nil {
		return ErrIdentityProofInvalid
	}
	user := *p.user
	hash, e := security.HashPassword(password)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var email string
	var revision int64
	var mfa bool
	if e = tx.QueryRow(ctx, `SELECT email,password_revision,mfa_enabled FROM users WHERE id=$1 AND status='active' FOR UPDATE`, user).Scan(&email, &revision, &mfa); errors.Is(e, pgx.ErrNoRows) {
		return ErrIdentityProofInvalid
	} else if e != nil {
		return e
	}
	if e = lockIdentityProof(ctx, tx, &p); e != nil {
		return e
	}
	if e = s.validIdentityProof(ctx, tx, p, IdentityPurposePasswordReset, token); e != nil {
		return e
	}
	if p.user == nil || *p.user != user || p.revision == nil || *p.revision != revision {
		return ErrIdentityProofInvalid
	}
	var digest string
	if e = tx.QueryRow(ctx, `SELECT email_digest FROM identity_mail_subjects WHERE id=$1`, p.subject).Scan(&digest); e != nil {
		return e
	}
	if digest != s.identitySubjectDigest(IdentityPurposePasswordReset, email) {
		return ErrIdentityProofInvalid
	}
	if mfa {
		if e = s.verifyMFATx(ctx, tx, user, mfaCode, false); e != nil {
			if errors.Is(e, ErrInvalid) || errors.Is(e, ErrForbidden) {
				return ErrIdentityProofInvalid
			}
			return e
		}
	}
	rows, e := tx.Query(ctx, `SELECT id FROM session_families WHERE user_id=$1 ORDER BY id FOR UPDATE`, user)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	// This check must follow all family waits and rolls MFA writes back on expiry.
	if e = s.validIdentityProof(ctx, tx, p, IdentityPurposePasswordReset, token); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE users SET password_hash=$2,email_verified_at=COALESCE(email_verified_at,clock_timestamp()) WHERE id=$1`, user, hash); e != nil {
		return e
	}
	if e = consumeIdentityProof(ctx, tx, p); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE session_families SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE user_id=$1`, user); e != nil {
		return e
	}
	if e = audit(ctx, tx, user, "", "identity.password_reset.complete", p.id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
