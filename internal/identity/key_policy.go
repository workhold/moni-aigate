package identity

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

var ErrKeyPolicyInvalid = errors.New("invalid_key_access_policy")

type KeyAccessPolicyInput struct {
	ExpectedRevision   string   `json:"expected_revision"`
	AllowedClientCIDRs []string `json:"allowed_client_cidrs"`
}

type KeyAccessPolicyView struct {
	ID                   string   `json:"id"`
	AllowedClientCIDRs   []string `json:"allowed_client_cidrs"`
	AccessPolicyRevision string   `json:"access_policy_revision"`
}

// KeyAccessPolicy uses the same current-role visibility as the key list. Revoked
// and expired keys remain inspectable without exposing their authentication data.
func (s *Store) KeyAccessPolicy(ctx context.Context, actor *Session, id string) (KeyAccessPolicyView, error) {
	result := KeyAccessPolicyView{}
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyPolicyInvalid
	}
	err := s.Pool.QueryRow(ctx, `SELECT k.id,k.allowed_client_cidrs,k.access_policy_revision::text
 FROM api_keys k JOIN tenants t ON t.id=k.tenant_id
 JOIN memberships m ON m.tenant_id=t.id AND m.user_id=$2
 JOIN users u ON u.id=m.user_id
 WHERE k.tenant_id=$1 AND k.id=$3 AND t.status='active' AND m.status='active' AND u.status='active'
 AND (m.role='owner' OR (m.role IN ('member','billing_viewer') AND k.owner_id=$2))`, actor.Tenant.ID, actor.User.ID, id).Scan(&result.ID, &result.AllowedClientCIDRs, &result.AccessPolicyRevision)
	return result, keyPolicyPermissionError(err)
}

// UpdateKeyAccessPolicy replaces only source restrictions. Current permissions
// and the CAS revision are read after the same parent/key locks used by billing.
func (s *Store) UpdateKeyAccessPolicy(ctx context.Context, actor *Session, id string, in KeyAccessPolicyInput) (KeyAccessPolicyView, error) {
	result := KeyAccessPolicyView{}
	if actor == nil || actor.Tenant == nil {
		return result, ErrForbidden
	}
	if !canonicalKeyID(id) {
		return result, ErrKeyPolicyInvalid
	}
	expected, err := strconv.ParseInt(in.ExpectedRevision, 10, 64)
	if err != nil || expected <= 0 || strconv.FormatInt(expected, 10) != in.ExpectedRevision || in.AllowedClientCIDRs == nil {
		return result, ErrKeyPolicyInvalid
	}
	cidrs, err := security.NormalizeClientCIDRs(in.AllowedClientCIDRs)
	if err != nil {
		return result, ErrKeyPolicyInvalid
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	var lockedID, role string
	if err = tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id=$1 AND status='active' FOR SHARE`, actor.Tenant.ID).Scan(&lockedID); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if err = tx.QueryRow(ctx, `SELECT role FROM memberships WHERE tenant_id=$1 AND user_id=$2 AND status='active' FOR SHARE`, actor.Tenant.ID, actor.User.ID).Scan(&role); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if role != "owner" && role != "member" {
		return result, ErrForbidden
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' FOR SHARE`, actor.User.ID).Scan(&lockedID); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	var owner string
	var revoked bool
	var before KeyAccessPolicyView
	if err = tx.QueryRow(ctx, `SELECT id,owner_id,allowed_client_cidrs,access_policy_revision::text,revoked_at IS NOT NULL FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, actor.Tenant.ID, id).Scan(&before.ID, &owner, &before.AllowedClientCIDRs, &before.AccessPolicyRevision, &revoked); err != nil {
		return result, keyPolicyPermissionError(err)
	}
	if revoked || (role != "owner" && owner != actor.User.ID) {
		return result, ErrForbidden
	}
	if before.AccessPolicyRevision != in.ExpectedRevision {
		return result, ErrConflict
	}
	if slices.Equal(before.AllowedClientCIDRs, cidrs) {
		return before, tx.Commit(ctx)
	}
	if err = tx.QueryRow(ctx, `UPDATE api_keys SET allowed_client_cidrs=$3 WHERE tenant_id=$1 AND id=$2 RETURNING id,allowed_client_cidrs,access_policy_revision::text`, actor.Tenant.ID, id, cidrs).Scan(&result.ID, &result.AllowedClientCIDRs, &result.AccessPolicyRevision); err != nil {
		return KeyAccessPolicyView{}, err
	}
	// No secret, digest, source header, or unrelated key attributes enter audit.
	diff := map[string]any{
		"before": map[string]any{"allowed_client_cidrs": before.AllowedClientCIDRs, "access_policy_revision": before.AccessPolicyRevision},
		"after":  map[string]any{"allowed_client_cidrs": result.AllowedClientCIDRs, "access_policy_revision": result.AccessPolicyRevision},
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,$2,'key.access_policy.update',$3,$4)`, actor.User.ID, actor.Tenant.ID, id, diff); err != nil {
		return KeyAccessPolicyView{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return KeyAccessPolicyView{}, err
	}
	return result, nil
}

func keyPolicyPermissionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrForbidden
	}
	return err
}

func canonicalKeyID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
