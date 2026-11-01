package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/security"
)

var ErrInvalid = errors.New("invalid_credentials")
var ErrForbidden = errors.New("forbidden")
var ErrConflict = errors.New("conflict")
var ErrReauth = errors.New("reauth_required")

type Store struct {
	Pool    *pgxpool.Pool
	Secrets *security.Secrets
	// EnforcePlatformMFA is a startup policy for enrollment, not a bypass of
	// factors already enabled by a user. Its zero value makes enrollment optional.
	EnforcePlatformMFA bool
}

func New(p *pgxpool.Pool, s *security.Secrets) *Store { return &Store{Pool: p, Secrets: s} }

type User struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	PlatformRole string `json:"platform_role"`
	MFAEnabled   bool   `json:"mfa_enabled"`
}
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}
type Session struct {
	User                 User       `json:"user"`
	Tenant               *Tenant    `json:"tenant"`
	Tenants              []Tenant   `json:"tenants"`
	CSRF                 string     `json:"csrf_token"`
	ReauthenticatedUntil *time.Time `json:"reauthenticated_until"`
	MFARequired          bool       `json:"mfa_required"`
	EnrollmentRequired   bool       `json:"enrollment_required"`
	Digest               string     `json:"-"`
	FamilyID             string     `json:"-"`
	Verified             bool       `json:"-"`
}
type Principal struct {
	TenantID, UserID, KeyID string
	Scopes, AllowedRegions  []string
	AllowedClientCIDRs      []string
	AccessPolicyRevision    string
	ClientIP                string
	RPMLimit                int
	BurstLimit              int
	ConcurrencyLimit        int
	LimitRevision           string
}

func audit(ctx context.Context, tx pgx.Tx, actor, tenant, action, target string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target) VALUES($1,NULLIF($2,''),$3,$4)`, actor, tenant, action, target)
	return e
}
func (s *Store) Bootstrap(ctx context.Context, email, password string) error {
	h, e := security.HashPassword(password)
	if e != nil {
		return e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(782193403)"); e != nil {
		return e
	}
	var n int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return ErrConflict
	}
	id := uuid.NewString()
	if _, e = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,$3,'admin')`, id, strings.ToLower(strings.TrimSpace(email)), h); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_initialization(initialized_by) VALUES($1)`, id); e != nil {
		return e
	}
	if e = audit(ctx, tx, id, "", "bootstrap", id); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) issueSession(ctx context.Context, tx pgx.Tx, user, tenant, old string, verified, reauth bool, requireOld ...bool) (string, error) {
	family := uuid.NewString()
	if len(requireOld) > 0 && requireOld[0] {
		var revoked *time.Time
		if e := tx.QueryRow(ctx, `SELECT f.id,f.revoked_at FROM sessions x JOIN session_families f ON f.id=x.family_id WHERE x.digest=$1 AND x.user_id=$2 AND x.expires_at>clock_timestamp() FOR UPDATE OF f`, old, user).Scan(&family, &revoked); e != nil {
			return "", ErrInvalid
		}
		if revoked != nil {
			return "", ErrInvalid
		}
	} else {
		if _, e := tx.Exec(ctx, "INSERT INTO session_families(id,user_id) VALUES($1,$2)", family, user); e != nil {
			return "", e
		}
	}
	raw := security.Random(32)
	var tid any
	if tenant != "" {
		tid = tenant
	}
	if old != "" {
		if _, e := tx.Exec(ctx, "DELETE FROM sessions WHERE digest=$1 AND user_id=$2", old, user); e != nil {
			return "", e
		}
	}
	_, e := tx.Exec(ctx, `INSERT INTO sessions(digest,family_id,user_id,tenant_id,csrf_token,mfa_verified,reauthenticated_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,CASE WHEN $7 THEN clock_timestamp() ELSE NULL END,clock_timestamp()+interval '12 hours')`, s.Secrets.Digest(raw), family, user, tid, security.Random(24), verified, reauth)
	return raw, e
}

func (s *Store) Login(ctx context.Context, email, password, old string) (string, bool, bool, error) {
	var id, hash, role string
	var mfa bool
	e := s.Pool.QueryRow(ctx, "SELECT id,password_hash,platform_role,mfa_enabled FROM users WHERE email=$1 AND status='active'", strings.ToLower(strings.TrimSpace(email))).Scan(&id, &hash, &role, &mfa)
	if errors.Is(e, pgx.ErrNoRows) {
		security.CheckPassword("$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW", password)
		return "", false, false, ErrInvalid
	}
	if e != nil {
		return "", false, false, e
	}
	if !security.CheckPassword(hash, password) {
		return "", false, false, ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", false, false, e
	}
	defer tx.Rollback(ctx)
	// Serialize session creation with password changes and MFA enrollment.
	var lockedHash string
	if e = tx.QueryRow(ctx, "SELECT password_hash,platform_role,mfa_enabled FROM users WHERE id=$1 AND status='active' FOR UPDATE", id).Scan(&lockedHash, &role, &mfa); e != nil {
		return "", false, false, e
	}
	if lockedHash != hash {
		return "", false, false, ErrInvalid
	}
	var tenant string
	e = tx.QueryRow(ctx, `SELECT m.tenant_id FROM memberships m JOIN tenants t ON t.id=m.tenant_id WHERE m.user_id=$1 AND m.status='active' AND t.status='active' ORDER BY m.tenant_id LIMIT 1`, id).Scan(&tenant)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return "", false, false, e
	}
	enroll := s.EnforcePlatformMFA && role != "none" && !mfa
	must := mfa || enroll
	raw, e := s.issueSession(ctx, tx, id, tenant, old, !must, !mfa)
	if e != nil {
		return "", false, false, e
	}
	if e = audit(ctx, tx, id, tenant, "login", id); e != nil {
		return "", false, false, e
	}
	e = tx.Commit(ctx)
	return raw, mfa, enroll, e
}
func (s *Store) Session(ctx context.Context, raw string) (*Session, error) {
	if len(raw) < 32 {
		return nil, ErrInvalid
	}
	v := &Session{Tenants: []Tenant{}, Digest: s.Secrets.Digest(raw)}
	var tenant *string
	var reauth *time.Time
	e := s.Pool.QueryRow(ctx, `SELECT u.id,u.email,u.platform_role,u.mfa_enabled,s.tenant_id,s.csrf_token,s.mfa_verified,s.reauthenticated_at,s.family_id FROM sessions s JOIN users u ON u.id=s.user_id JOIN session_families f ON f.id=s.family_id WHERE f.revoked_at IS NULL AND s.digest=$1 AND s.expires_at>clock_timestamp() AND u.status='active'`, v.Digest).Scan(&v.User.ID, &v.User.Email, &v.User.PlatformRole, &v.User.MFAEnabled, &tenant, &v.CSRF, &v.Verified, &reauth, &v.FamilyID)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, ErrInvalid
	}
	if e != nil {
		return nil, e
	}
	rows, e := s.Pool.Query(ctx, `SELECT t.id,t.name,m.role FROM memberships m JOIN tenants t ON t.id=m.tenant_id WHERE m.user_id=$1 AND m.status='active' AND t.status='active' ORDER BY t.name,t.id`, v.User.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var t Tenant
		if e = rows.Scan(&t.ID, &t.Name, &t.Role); e != nil {
			return nil, e
		}
		v.Tenants = append(v.Tenants, t)
		if tenant != nil && t.ID == *tenant {
			copy := t
			v.Tenant = &copy
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if reauth != nil {
		until := reauth.Add(10 * time.Minute)
		v.ReauthenticatedUntil = &until
	}
	v.EnrollmentRequired = s.EnforcePlatformMFA && v.User.PlatformRole != "none" && !v.User.MFAEnabled
	v.MFARequired = (v.User.MFAEnabled || v.EnrollmentRequired) && !v.Verified
	return v, nil
}
func (s *Store) Logout(ctx context.Context, digest string, family ...string) error {
	id := ""
	if len(family) > 0 {
		id = family[0]
	} else {
		e := s.Pool.QueryRow(ctx, "SELECT family_id FROM sessions WHERE digest=$1", digest).Scan(&id)
		if e != nil {
			return e
		}
	}
	_, e := s.Pool.Exec(ctx, "UPDATE session_families SET revoked_at=clock_timestamp() WHERE id=$1", id)
	return e
}

func (s *Store) Switch(ctx context.Context, v *Session, tenant string) (string, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM users WHERE id=$1 FOR UPDATE", v.User.ID); e != nil {
		return "", e
	}
	var ok bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN tenants t ON t.id=m.tenant_id WHERE m.user_id=$1 AND t.id=$2 AND m.status='active' AND t.status='active')`, v.User.ID, tenant).Scan(&ok); e != nil {
		return "", e
	}
	if !ok {
		return "", ErrForbidden
	}
	raw, e := s.issueSession(ctx, tx, v.User.ID, tenant, v.Digest, v.Verified, false, true)
	if e != nil {
		return "", e
	}
	return raw, tx.Commit(ctx)
}
func (s *Store) SetupMFA(ctx context.Context, v *Session) (string, []string, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", nil, e
	}
	defer tx.Rollback(ctx)
	var enabled bool
	if e = tx.QueryRow(ctx, "SELECT mfa_enabled FROM users WHERE id=$1 FOR UPDATE", v.User.ID).Scan(&enabled); e != nil {
		return "", nil, e
	}
	if enabled {
		return "", nil, ErrConflict
	}
	var recent bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions x JOIN session_families f ON f.id=x.family_id WHERE x.digest=$1 AND x.user_id=$2 AND f.revoked_at IS NULL AND x.expires_at>clock_timestamp() AND x.reauthenticated_at>clock_timestamp()-interval '10 minutes')`, v.Digest, v.User.ID).Scan(&recent); e != nil {
		return "", nil, e
	}
	if !recent {
		return "", nil, ErrReauth
	}
	secret := security.NewTOTPSecret()
	encrypted, e := s.Secrets.SealFor(secret, "mfa:user", v.User.ID)
	if e != nil {
		return "", nil, e
	}
	if _, e = tx.Exec(ctx, "UPDATE users SET mfa_pending_secret=$2 WHERE id=$1", v.User.ID, encrypted); e != nil {
		return "", nil, e
	}
	if _, e = tx.Exec(ctx, "DELETE FROM mfa_recovery_codes WHERE user_id=$1", v.User.ID); e != nil {
		return "", nil, e
	}
	codes := make([]string, 8)
	for i := range codes {
		codes[i] = security.Random(12)
		if _, e = tx.Exec(ctx, "INSERT INTO mfa_recovery_codes(user_id,digest) VALUES($1,$2)", v.User.ID, s.Secrets.Digest(codes[i])); e != nil {
			return "", nil, e
		}
	}
	if e = audit(ctx, tx, v.User.ID, "", "mfa.setup", v.User.ID); e != nil {
		return "", nil, e
	}
	return secret, codes, tx.Commit(ctx)
}
func (s *Store) verifyMFATx(ctx context.Context, tx pgx.Tx, user, code string, enroll bool) error {
	var encrypted, pending string
	var last int64
	var enabled bool
	var now time.Time
	if e := tx.QueryRow(ctx, "SELECT mfa_secret,mfa_pending_secret,mfa_enabled,mfa_last_step,clock_timestamp() FROM users WHERE id=$1 FOR UPDATE", user).Scan(&encrypted, &pending, &enabled, &last, &now); e != nil {
		return e
	}
	if enroll {
		if enabled || pending == "" {
			return ErrForbidden
		}
		encrypted = pending
	}
	if !enroll && !enabled {
		return ErrForbidden
	}
	secret, e := s.Secrets.OpenFor(encrypted, "mfa:user", user)
	if e != nil {
		return e
	}
	step, ok := security.VerifyTOTP(secret, code, now, last)
	if ok {
		_, e = tx.Exec(ctx, "UPDATE users SET mfa_last_step=$2,mfa_enabled=true,mfa_secret=$3,mfa_pending_secret='' WHERE id=$1", user, step, encrypted)
		return e
	}
	if !enroll {
		tag, e := tx.Exec(ctx, "UPDATE mfa_recovery_codes SET used_at=clock_timestamp() WHERE user_id=$1 AND digest=$2 AND used_at IS NULL", user, s.Secrets.Digest(code))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 1 {
			return nil
		}
	}
	return ErrInvalid
}
func (s *Store) VerifyMFA(ctx context.Context, v *Session, code string, enroll bool) (string, error) {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(ctx)
	if e = s.verifyMFATx(ctx, tx, v.User.ID, code, enroll); e != nil {
		return "", e
	}
	if enroll {
		if _, e = tx.Exec(ctx, "UPDATE session_families SET revoked_at=clock_timestamp() WHERE user_id=$1 AND id<>$2", v.User.ID, v.FamilyID); e != nil {
			return "", e
		}
	}
	tenant := ""
	if v.Tenant != nil {
		tenant = v.Tenant.ID
	}
	raw, e := s.issueSession(ctx, tx, v.User.ID, tenant, v.Digest, true, true, true)
	if e != nil {
		return "", e
	}
	if e = audit(ctx, tx, v.User.ID, tenant, "mfa.verify", v.User.ID); e != nil {
		return "", e
	}
	return raw, tx.Commit(ctx)
}

// passwordProof locks the user before any family, preventing login/MFA/password
// races. No stale Session fields decide whether a fresh second factor is needed.
func (s *Store) passwordProof(ctx context.Context, tx pgx.Tx, v *Session, password, code string) error {
	var hash string
	var mfa bool
	if e := tx.QueryRow(ctx, "SELECT password_hash,mfa_enabled FROM users WHERE id=$1 AND status='active' FOR UPDATE", v.User.ID).Scan(&hash, &mfa); e != nil {
		return e
	}
	if !security.CheckPassword(hash, password) {
		return ErrInvalid
	}
	var valid bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions x JOIN session_families f ON f.id=x.family_id WHERE x.digest=$1 AND x.user_id=$2 AND f.revoked_at IS NULL AND x.expires_at>clock_timestamp())`, v.Digest, v.User.ID).Scan(&valid); e != nil {
		return e
	}
	if !valid {
		return ErrInvalid
	}
	if mfa {
		return s.verifyMFATx(ctx, tx, v.User.ID, code, false)
	}
	return nil
}
func (s *Store) Reauthenticate(ctx context.Context, v *Session, password, code string) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = s.passwordProof(ctx, tx, v, password, code); e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, "UPDATE sessions SET reauthenticated_at=clock_timestamp() WHERE digest=$1", v.Digest)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrInvalid
	}
	if e = audit(ctx, tx, v.User.ID, "", "session.reauthenticate", v.User.ID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) ChangePassword(ctx context.Context, v *Session, current, newPassword, code string) error {
	hash, e := security.HashPassword(newPassword)
	if e != nil {
		return ErrInvalid
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = s.passwordProof(ctx, tx, v, current, code); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE users SET password_hash=$2 WHERE id=$1", v.User.ID, hash); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE session_families SET revoked_at=clock_timestamp() WHERE user_id=$1", v.User.ID); e != nil {
		return e
	}
	if e = audit(ctx, tx, v.User.ID, "", "password.change", v.User.ID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) APIKey(ctx context.Context, raw string) (Principal, error) {
	v := Principal{}
	if !strings.HasPrefix(raw, "mg_") || len(raw) > 128 {
		return v, ErrInvalid
	}
	e := s.Pool.QueryRow(ctx, `SELECT k.tenant_id,k.owner_id,k.id,k.scopes,k.allowed_regions,k.allowed_client_cidrs,k.access_policy_revision::text,k.rpm_limit,k.burst_limit,k.concurrency_limit,k.limit_revision::text FROM api_keys k JOIN tenants t ON t.id=k.tenant_id JOIN memberships m ON (m.tenant_id,m.user_id)=(k.tenant_id,k.owner_id) JOIN users u ON u.id=k.owner_id WHERE k.digest=$1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>clock_timestamp()) AND t.status='active' AND m.status='active' AND u.status='active'`, s.Secrets.Digest(raw)).Scan(&v.TenantID, &v.UserID, &v.KeyID, &v.Scopes, &v.AllowedRegions, &v.AllowedClientCIDRs, &v.AccessPolicyRevision, &v.RPMLimit, &v.BurstLimit, &v.ConcurrencyLimit, &v.LimitRevision)
	if errors.Is(e, pgx.ErrNoRows) {
		e = ErrInvalid
	}
	return v, e
}
