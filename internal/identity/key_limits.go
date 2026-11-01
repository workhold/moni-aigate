package identity

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrKeyLimitsInvalid = errors.New("invalid_key_runtime_limits")

type KeyLimitsInput struct {
	ExpectedRevision string `json:"expected_revision"`
	RPMLimit         *int   `json:"rpm_limit"`
	BurstLimit       *int   `json:"burst_limit"`
	ConcurrencyLimit *int   `json:"concurrency_limit"`
}

type KeyLimitsView struct {
	ID               string `json:"id"`
	RPMLimit         int    `json:"rpm_limit"`
	BurstLimit       int    `json:"burst_limit"`
	ConcurrencyLimit int    `json:"concurrency_limit"`
	LimitRevision    string `json:"limit_revision"`
}

func keyRuntimeLimitValues(rpm, burst, concurrency *int, defaults bool) (KeyLimitsView, error) {
	result := KeyLimitsView{RPMLimit: 60, BurstLimit: 20, ConcurrencyLimit: 16}
	if !defaults && (rpm == nil || burst == nil || concurrency == nil) {
		return KeyLimitsView{}, ErrKeyLimitsInvalid
	}
	if rpm != nil {
		result.RPMLimit = *rpm
	}
	if burst != nil {
		result.BurstLimit = *burst
	}
	if concurrency != nil {
		result.ConcurrencyLimit = *concurrency
	}
	if result.RPMLimit < 1 || result.RPMLimit > 6000 || result.BurstLimit < 1 || result.BurstLimit > 200 || result.ConcurrencyLimit < 1 || result.ConcurrencyLimit > 16 {
		return KeyLimitsView{}, ErrKeyLimitsInvalid
	}
	return result, nil
}

func (s *Store) KeyRuntimeLimits(ctx context.Context, actor *Session, id string) (KeyLimitsView, error) {
	result := KeyLimitsView{}
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyLimitsInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT k.id,k.rpm_limit,k.burst_limit,k.concurrency_limit,k.limit_revision::text
 FROM api_keys k JOIN tenants t ON t.id=k.tenant_id
 JOIN memberships m ON m.tenant_id=t.id AND m.user_id=$2 JOIN users u ON u.id=m.user_id
 WHERE k.tenant_id=$1 AND k.id=$3 AND t.status='active' AND m.status='active' AND u.status='active'
 AND (m.role='owner' OR (m.role IN ('member','billing_viewer') AND k.owner_id=$2))`, actor.Tenant.ID, actor.User.ID, id).Scan(&result.ID, &result.RPMLimit, &result.BurstLimit, &result.ConcurrencyLimit, &result.LimitRevision)
	return result, keyPolicyPermissionError(err)
}

func (s *Store) UpdateKeyRuntimeLimits(ctx context.Context, actor *Session, id string, in KeyLimitsInput) (KeyLimitsView, error) {
	result := KeyLimitsView{}
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyLimitsInvalid
	}
	expected, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != in.ExpectedRevision {
		return result, ErrKeyLimitsInvalid
	}
	desired, err := keyRuntimeLimitValues(in.RPMLimit, in.BurstLimit, in.ConcurrencyLimit, false)
	if err != nil {
		return result, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return result, err
	}
	defer rollbackKeyLimits(tx)
	var locked, role string
	if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, actor.Tenant.ID).Scan(&locked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, actor.Tenant.ID, actor.User.ID).Scan(&role); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if role != "owner" && role != "member" {
		return result, ErrForbidden
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, actor.User.ID).Scan(&locked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	var before KeyLimitsView
	var owner string
	var revoked bool
	if err = tx.QueryRow(ctx, `SELECT id,owner_id,rpm_limit,burst_limit,concurrency_limit,limit_revision::text,revoked_at IS NOT NULL FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, actor.Tenant.ID, id).Scan(&before.ID, &owner, &before.RPMLimit, &before.BurstLimit, &before.ConcurrencyLimit, &before.LimitRevision, &revoked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if revoked || (role != "owner" && owner != actor.User.ID) {
		return result, ErrForbidden
	}
	if before.LimitRevision != in.ExpectedRevision {
		return result, ErrConflict
	}
	if before.RPMLimit == desired.RPMLimit && before.BurstLimit == desired.BurstLimit && before.ConcurrencyLimit == desired.ConcurrencyLimit {
		return before, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `UPDATE api_keys SET rpm_limit=$3,burst_limit=$4,concurrency_limit=$5 WHERE tenant_id=$1 AND id=$2 RETURNING id,rpm_limit,burst_limit,concurrency_limit,limit_revision::text`, actor.Tenant.ID, id, desired.RPMLimit, desired.BurstLimit, desired.ConcurrencyLimit).Scan(&result.ID, &result.RPMLimit, &result.BurstLimit, &result.ConcurrencyLimit, &result.LimitRevision); err != nil {
		return KeyLimitsView{}, err
	}
	diff := map[string]any{"before": keyRuntimeLimitsAudit(before), "after": keyRuntimeLimitsAudit(result)}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,$2,'key.runtime_limits.update',$3,$4)`, actor.User.ID, actor.Tenant.ID, id, diff); err != nil {
		return KeyLimitsView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return KeyLimitsView{}, err
	}
	return result, nil
}

func keyRuntimeLimitsAudit(v KeyLimitsView) map[string]any {
	return map[string]any{"rpm_limit": v.RPMLimit, "burst_limit": v.BurstLimit, "concurrency_limit": v.ConcurrencyLimit, "limit_revision": v.LimitRevision}
}

// WithKeyRuntimeLimits holds the same authorization parents as dispatch while
// admitting against the latest runtime tuple. The callback must do memory-only
// work: it must not acquire another database connection or call this Store.
// A caller that acquires a permit in the callback must release it if Commit
// fails. Tokens consumed by the callback are never automatically refunded.
func (s *Store) WithKeyRuntimeLimits(ctx context.Context, p Principal, callback func(KeyLimitsView) error) error {
	if callback == nil || !canonicalKeyID(p.TenantID) || !canonicalKeyID(p.UserID) || !canonicalKeyID(p.KeyID) {
		return ErrInvalid
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return err
	}
	defer rollbackKeyLimits(tx)
	var locked string
	if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, p.TenantID).Scan(&locked); err != nil {
		return keyPolicyPermissionError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, p.TenantID, p.UserID).Scan(&locked); err != nil {
		return keyPolicyPermissionError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, p.UserID).Scan(&locked); err != nil {
		return keyPolicyPermissionError(err)
	}
	var current KeyLimitsView
	var owner string
	var revoked bool
	var expires *time.Time
	if err = tx.QueryRow(ctx, `SELECT id,owner_id,rpm_limit,burst_limit,concurrency_limit,limit_revision::text,revoked_at IS NOT NULL,expires_at FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR SHARE`, p.TenantID, p.KeyID).Scan(&current.ID, &owner, &current.RPMLimit, &current.BurstLimit, &current.ConcurrencyLimit, &current.LimitRevision, &revoked, &expires); err != nil {
		return keyPolicyPermissionError(err)
	}
	if owner != p.UserID || revoked {
		return ErrInvalid
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if expires != nil && !expires.After(now) {
		return ErrInvalid
	}
	if _, err = keyRuntimeLimitValues(&current.RPMLimit, &current.BurstLimit, &current.ConcurrencyLimit, false); err != nil {
		return err
	}
	if err = callback(current); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func rollbackKeyLimits(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
