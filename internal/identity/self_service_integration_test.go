package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/migrations"
)

const selfServiceOrigin = "https://identity.test"
const selfServicePassword = "self-service-fixture-new-password"

// Registration must exercise a real empty database, rather than trusting an
// application-inserted marker on the ordinary owner/member identity fixture.
func newSelfServiceEmptyFixture(t *testing.T) *identityFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required: self-service identity needs real PostgreSQL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "self_service_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		admin.Close()
	})
	if err = migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	secrets := &security.Secrets{Keys: map[string][]byte{"test": []byte(strings.Repeat("e", 32))}, Active: "test", Pepper: []byte(strings.Repeat("a", 32))}
	return &identityFixture{ctx: ctx, pool: pool, store: New(pool, secrets)}
}

func selfServiceMailToken(t *testing.T, f *identityFixture, purpose, email string) (string, string) {
	t.Helper()
	normalized := strings.ToLower(strings.TrimSpace(email))
	digest := f.store.Secrets.Digest("identity-mail-subject:v1:" + purpose + "\x00" + normalized)
	var outbox, challenge, encrypted string
	if err := f.pool.QueryRow(f.ctx, `SELECT o.id::text,c.id::text,o.payload_ciphertext
 FROM identity_mail_subjects s JOIN identity_challenges c ON c.subject_id=s.id
 JOIN identity_mail_outbox o ON o.challenge_id=c.id
 WHERE s.purpose=$1 AND s.email_digest=$2 ORDER BY c.created_at DESC,c.id DESC LIMIT 1`, purpose, digest).Scan(&outbox, &challenge, &encrypted); err != nil {
		t.Fatal(err)
	}
	plain, err := f.store.Secrets.OpenFor(encrypted, "identity-mail-outbox", outbox)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal([]byte(plain), &fields); err != nil || len(fields) != 4 {
		t.Fatal("identity email payload must contain exactly v/to/purpose/link", err)
	}
	var payload struct {
		Version int    `json:"v"`
		To      string `json:"to"`
		Purpose string `json:"purpose"`
		Link    string `json:"link"`
	}
	if err = json.Unmarshal([]byte(plain), &payload); err != nil || payload.Version != 1 || payload.To != normalized || payload.Purpose != purpose {
		t.Fatal("incorrect version or target in encrypted identity email", err)
	}
	link, err := url.Parse(payload.Link)
	if err != nil || link.Scheme+"://"+link.Host != selfServiceOrigin || link.RawQuery != "" {
		t.Fatal("identity mail link did not use the trusted origin and fragment", err)
	}
	fragment, err := url.ParseQuery(link.Fragment)
	if err != nil || len(fragment) != 1 || len(fragment["token"]) != 1 {
		t.Fatal("identity mail fragment is not a single token", err)
	}
	token := fragment.Get("token")
	if !ValidIdentityToken(token) || strings.Contains(encrypted, token) || strings.Contains(encrypted, normalized) {
		t.Fatal("identity mail did not retain the raw token exclusively inside encryption")
	}
	if _, err = f.store.Secrets.OpenFor(encrypted, "identity-mail-outbox", uuid.NewString()); err == nil {
		t.Fatal("identity email ciphertext was not bound to its outbox record")
	}
	return challenge, token
}

func selfServiceRequest(t *testing.T, f *identityFixture, purpose, email string) (string, string) {
	t.Helper()
	if err := f.store.RequestIdentityMail(f.ctx, purpose, email, selfServiceOrigin); err != nil {
		t.Fatal(err)
	}
	return selfServiceMailToken(t, f, purpose, email)
}

type selfServiceResetState struct {
	passwordHash, secret string
	revision, lastStep   int64
	enabled              bool
	consumed, revoked    bool
	usedCodes, active    int
}

func selfServiceState(t *testing.T, f *identityFixture, challenge, user string) selfServiceResetState {
	t.Helper()
	var state selfServiceResetState
	if err := f.pool.QueryRow(f.ctx, `SELECT u.password_hash,u.mfa_secret,u.password_revision,u.mfa_last_step,u.mfa_enabled,
 c.consumed_at IS NOT NULL,c.revoked_at IS NOT NULL,
 (SELECT count(*) FROM mfa_recovery_codes WHERE user_id=u.id AND used_at IS NOT NULL),
 (SELECT count(*) FROM session_families WHERE user_id=u.id AND revoked_at IS NULL)
 FROM users u JOIN identity_challenges c ON c.id=$1 WHERE u.id=$2`, challenge, user).Scan(&state.passwordHash, &state.secret, &state.revision, &state.lastStep, &state.enabled, &state.consumed, &state.revoked, &state.usedCodes, &state.active); err != nil {
		t.Fatal(err)
	}
	return state
}

func selfServiceEnableMFA(t *testing.T, f *identityFixture) ([]string, *Session) {
	t.Helper()
	_, session := f.login(t, "owner@identity.test")
	secret, codes, err := f.store.SetupMFA(f.ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err = f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	code, err := security.TOTP(secret, now.Unix()/30)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := f.store.VerifyMFA(f.ctx, session, code, true)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := f.store.Session(f.ctx, raw)
	if err != nil || !verified.User.MFAEnabled || verified.MFARequired {
		t.Fatal("fixture did not enable current MFA", err)
	}
	return codes, verified
}

func TestSelfServiceRegistrationInitializationMinimumPrivilegesAndSingleUse(t *testing.T) {
	f := newSelfServiceEmptyFixture(t)
	initialized, err := f.store.Initialized(f.ctx)
	if err != nil || initialized {
		t.Fatal("empty identity database was marked initialized", initialized, err)
	}
	if err = f.store.RequestIdentityMail(f.ctx, IdentityPurposeRegistration, "new@identity.test", selfServiceOrigin); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatal("public registration could precede bootstrap", err)
	}
	var users, messages int
	if err = f.pool.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM identity_mail_outbox)").Scan(&users, &messages); err != nil || users != 0 || messages != 0 {
		t.Fatal("uninitialized registration created identity facts", users, messages, err)
	}
	if err = f.store.Bootstrap(f.ctx, "admin@identity.test", identityTestPassword); err != nil {
		t.Fatal(err)
	}
	if initialized, err = f.store.Initialized(f.ctx); err != nil || !initialized {
		t.Fatal("bootstrap and initialization did not commit together", initialized, err)
	}
	challenge, token := selfServiceRequest(t, f, IdentityPurposeRegistration, "  NEW@identity.test  ")
	if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM users").Scan(&users); err != nil || users != 1 {
		t.Fatal("requesting registration created a user before email proof", users, err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			done <- f.store.CompleteRegistration(f.ctx, token, selfServicePassword, "  New self-service tenant  ")
		}()
	}
	successes := 0
	for range 2 {
		if err = <-done; err == nil {
			successes++
		} else if !errors.Is(err, ErrIdentityProofInvalid) {
			t.Fatal("registration returned a noncanonical proof error", err)
		}
	}
	if successes != 1 {
		t.Fatal("registration token did not authorize exactly one account", successes)
	}
	var user, tenant, platformRole, userStatus, memberRole, memberStatus, tenantName string
	var verified, allModels, consumed bool
	var revision int64
	if err = f.pool.QueryRow(f.ctx, `SELECT u.id,m.tenant_id,u.platform_role,u.status,m.role,m.status,t.name,
 u.email_verified_at IS NOT NULL,m.allow_all_models,c.consumed_at IS NOT NULL,u.password_revision
 FROM users u JOIN memberships m ON m.user_id=u.id JOIN tenants t ON t.id=m.tenant_id
 JOIN identity_challenges c ON c.id=$1 WHERE u.email='new@identity.test'`, challenge).Scan(&user, &tenant, &platformRole, &userStatus, &memberRole, &memberStatus, &tenantName, &verified, &allModels, &consumed, &revision); err != nil {
		t.Fatal(err)
	}
	if platformRole != "none" || userStatus != "active" || memberRole != "owner" || memberStatus != "active" || tenantName != "New self-service tenant" || !verified || allModels || !consumed || revision != 1 {
		t.Fatal("registration did not create the minimum ordinary account permissions")
	}
	var tenantGrants, memberGrants, keys, journals, credits, sessions, tenants int
	if err = f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM tenant_model_grants WHERE tenant_id=$1),
 (SELECT count(*) FROM member_model_grants WHERE user_id=$2),
 (SELECT count(*) FROM api_keys WHERE owner_id=$2),(SELECT count(*) FROM journals),
 (SELECT count(*) FROM credit_orders),(SELECT count(*) FROM sessions),(SELECT count(*) FROM tenants)`, tenant, user).Scan(&tenantGrants, &memberGrants, &keys, &journals, &credits, &sessions, &tenants); err != nil {
		t.Fatal(err)
	}
	if tenantGrants != 0 || memberGrants != 0 || keys != 0 || journals != 0 || credits != 0 || sessions != 0 || tenants != 1 {
		t.Fatal("registration granted model, key, funds or automatic login beyond its contract")
	}
	raw, mfa, enrollment, err := f.store.Login(f.ctx, "new@identity.test", selfServicePassword, "")
	if err != nil || raw == "" || mfa || enrollment {
		t.Fatal("ordinary registered user could not log in separately", err)
	}
	current, err := f.store.Session(f.ctx, raw)
	if err != nil || current.Tenant == nil || current.Tenant.ID != tenant {
		t.Fatal("registered login did not select the new tenant", err)
	}
}

func TestSelfServicePasswordResetSingleUseRevisionAndAllSessionFamilies(t *testing.T) {
	f := newIdentityFixture(t)
	rawA, _ := f.login(t, "owner@identity.test")
	rawB, _ := f.login(t, "owner@identity.test")
	key, rawKey := f.createPolicyKey(t, f.owner, nil)
	challenge, token := selfServiceRequest(t, f, IdentityPurposePasswordReset, "owner@identity.test")
	before := selfServiceState(t, f, challenge, f.owner)
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- f.store.CompletePasswordReset(f.ctx, token, selfServicePassword, "") }()
	}
	successes := 0
	for range 2 {
		err := <-done
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrIdentityProofInvalid) {
			t.Fatal("reset returned a noncanonical proof error", err)
		}
	}
	after := selfServiceState(t, f, challenge, f.owner)
	if successes != 1 || after.revision != before.revision+1 || !after.consumed || after.active != 0 || after.passwordHash == before.passwordHash || after.enabled {
		t.Fatal("reset was not one atomic password/proof/session transition", successes)
	}
	for _, old := range []string{rawA, rawB} {
		if _, err := f.store.Session(f.ctx, old); !errors.Is(err, ErrInvalid) {
			t.Fatal("an old session family survived password reset", err)
		}
	}
	var families, audits int
	var verified bool
	if err := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM session_families WHERE user_id=$1),
 (SELECT count(*) FROM audit_events WHERE action='identity.password_reset.complete' AND target=$2),
 email_verified_at IS NOT NULL FROM users WHERE id=$1`, f.owner, challenge).Scan(&families, &audits, &verified); err != nil || families != 2 || audits != 1 || !verified {
		t.Fatal("reset auto-issued a family or omitted its unique audited proof", families, audits, verified, err)
	}
	if principal, err := f.store.APIKey(f.ctx, rawKey); err != nil || principal.KeyID != key {
		t.Fatal("password reset unexpectedly revoked or replaced an independent API key", err)
	}
	if _, _, _, err := f.store.Login(f.ctx, "owner@identity.test", identityTestPassword, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("old password remained valid after reset", err)
	}
	if _, mfa, _, err := f.store.Login(f.ctx, "owner@identity.test", selfServicePassword, ""); err != nil || mfa {
		t.Fatal("new password did not become the next login proof", err)
	}
}

func TestSelfServiceOrdinaryPasswordChangeInvalidatesOlderResetProof(t *testing.T) {
	f := newIdentityFixture(t)
	_, session := f.login(t, "owner@identity.test")
	challenge, token := selfServiceRequest(t, f, IdentityPurposePasswordReset, "owner@identity.test")
	before := selfServiceState(t, f, challenge, f.owner)
	if err := f.store.ChangePassword(f.ctx, session, identityTestPassword, "ordinary-fixture-password-change", ""); err != nil {
		t.Fatal(err)
	}
	changed := selfServiceState(t, f, challenge, f.owner)
	if changed.revision != before.revision+1 {
		t.Fatal("ordinary ChangePassword did not advance the database password revision")
	}
	if err := f.store.CompletePasswordReset(f.ctx, token, selfServicePassword, ""); !errors.Is(err, ErrIdentityProofInvalid) {
		t.Fatal("a previous password revision authorized a later reset", err)
	}
	if after := selfServiceState(t, f, challenge, f.owner); after != changed || after.consumed {
		t.Fatal("stale reset proof changed a newer password or consumed its token")
	}
}

func TestSelfServiceResetRechecksNewMFAAndBadProofDoesNotConsume(t *testing.T) {
	f := newIdentityFixture(t)
	// The email was requested before MFA was enabled. Redemption must read the
	// current factor state, rather than trusting the request-time account view.
	challenge, token := selfServiceRequest(t, f, IdentityPurposePasswordReset, "owner@identity.test")
	codes, session := selfServiceEnableMFA(t, f)
	before := selfServiceState(t, f, challenge, f.owner)
	wrong := "wrong-recovery-code"
	for _, code := range []string{"", wrong} {
		if err := f.store.CompletePasswordReset(f.ctx, token, selfServicePassword, code); !errors.Is(err, ErrIdentityProofInvalid) {
			t.Fatal("reset bypassed current MFA or disclosed a different proof error", err)
		}
		if after := selfServiceState(t, f, challenge, f.owner); after != before {
			t.Fatal("incorrect MFA consumed a token, factor or session")
		}
	}
	if err := f.store.CompletePasswordReset(f.ctx, token, selfServicePassword, codes[0]); err != nil {
		t.Fatal(err)
	}
	after := selfServiceState(t, f, challenge, f.owner)
	if !after.enabled || after.secret != before.secret || after.usedCodes != 1 || after.revision != before.revision+1 || !after.consumed || after.active != 0 {
		t.Fatal("successful factor proof was not atomic with reset and session revocation")
	}
	if _, err := f.store.VerifyMFA(f.ctx, session, codes[0], false); !errors.Is(err, ErrInvalid) {
		t.Fatal("a recovery code was reused after reset", err)
	}
}

func TestSelfServiceResetAndMFAVerificationCompeteForOneRecoveryCode(t *testing.T) {
	f := newIdentityFixture(t)
	codes, _ := selfServiceEnableMFA(t, f)
	_, pending := f.login(t, "owner@identity.test")
	challenge, token := selfServiceRequest(t, f, IdentityPurposePasswordReset, "owner@identity.test")
	start := make(chan struct{})
	resetDone, factorDone := make(chan error, 1), make(chan error, 1)
	go func() {
		<-start
		resetDone <- f.store.CompletePasswordReset(f.ctx, token, selfServicePassword, codes[0])
	}()
	go func() {
		<-start
		_, err := f.store.VerifyMFA(f.ctx, pending, codes[0], false)
		factorDone <- err
	}()
	close(start)
	resetErr, factorErr := <-resetDone, <-factorDone
	if (resetErr == nil) == (factorErr == nil) {
		t.Fatal("one recovery code did not authorize exactly one concurrent operation", resetErr, factorErr)
	}
	if resetErr != nil && !errors.Is(resetErr, ErrIdentityProofInvalid) {
		t.Fatal("reset lost the recovery-code race with a noncanonical error", resetErr)
	}
	if factorErr != nil && !errors.Is(factorErr, ErrInvalid) {
		t.Fatal("MFA verification lost the recovery-code race unexpectedly", factorErr)
	}
	after := selfServiceState(t, f, challenge, f.owner)
	if after.usedCodes != 1 || !after.enabled || after.consumed != (resetErr == nil) {
		t.Fatal("concurrent recovery-code use left inconsistent factor or token state")
	}
}

func selfServiceWaitForBlocker(t *testing.T, ctx context.Context, f *identityFixture, blocker pgx.Tx) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity a WHERE $1::int=ANY(pg_blocking_pids(a.pid)))`, int32(blocker.Conn().PgConn().PID())).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("reset did not reach its family lock", ctx.Err())
		case <-deadline.C:
			t.Fatal("reset did not wait on its isolated family blocker")
		case <-ticker.C:
		}
	}
}

func TestSelfServiceResetFamilyLockCrossingExpiryRollsBackFactorAndPassword(t *testing.T) {
	f := newIdentityFixture(t)
	codes, session := selfServiceEnableMFA(t, f)
	challenge, token := selfServiceRequest(t, f, IdentityPurposePasswordReset, "owner@identity.test")
	// Only this random owner-controlled schema relaxes the metadata guard while
	// preparing a short-lived fixture. No production transition shortens TTL.
	setup, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer setup.Rollback(f.ctx)
	if _, err = setup.Exec(f.ctx, "ALTER TABLE identity_challenges DISABLE TRIGGER identity_challenge_guard"); err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err = setup.QueryRow(f.ctx, "UPDATE identity_challenges SET expires_at=clock_timestamp()+interval '10 seconds' WHERE id=$1 RETURNING expires_at", challenge).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if _, err = setup.Exec(f.ctx, "ALTER TABLE identity_challenges ENABLE TRIGGER identity_challenge_guard"); err != nil {
		t.Fatal(err)
	}
	if err = setup.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	before := selfServiceState(t, f, challenge, f.owner)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(f.ctx)
	if _, err = blocker.Exec(f.ctx, "SELECT id FROM session_families WHERE id=$1 FOR UPDATE", session.FamilyID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 25*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.store.CompletePasswordReset(ctx, token, selfServicePassword, codes[0]) }()
	selfServiceWaitForBlocker(t, ctx, f, blocker)
	var now time.Time
	if err = f.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil || !now.Before(expires) {
		t.Fatal("fixture did not reach the family lock before proof expiry", err)
	}
	if _, err = f.pool.Exec(ctx, "SELECT pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ($1::timestamptz-clock_timestamp())))+0.02)", expires); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrIdentityProofInvalid) {
		t.Fatal("a family lock wait extended the email proof lifetime", err)
	}
	if after := selfServiceState(t, f, challenge, f.owner); after != before {
		t.Fatal("expiry after valid MFA failed to roll back password, factor, token or family changes")
	}
	if _, err = f.store.VerifyMFA(f.ctx, session, codes[0], false); err != nil {
		t.Fatal("expiry consumed a recovery code from the aborted reset", err)
	}
}
