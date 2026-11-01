package periodicbudget

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type commanderTarget struct{ scope, id, tenant string }

func (t commanderTarget) valid() bool {
	return ValidUUID(t.id) && (t.scope == "tenant" || t.scope == "procurement" || t.scope == "key" && ValidUUID(t.tenant))
}
func (t commanderTarget) identity() string {
	if t.scope == "key" {
		return t.tenant
	}
	return "platform"
}
func (t commanderTarget) kind() string { return t.scope + "_periodic_budget.configure" }
func (t commanderTarget) path() string {
	switch t.scope {
	case "tenant":
		return "/api/platform/tenants/" + t.id + "/periodic-budget"
	case "key":
		return "/api/keys/" + t.id + "/periodic-budget"
	case "procurement":
		return "/api/platform/procurement-accounts/" + t.id + "/periodic-budget"
	}
	return ""
}
func commanderLockRow(ctx context.Context, tx pgx.Tx, sql string, args ...any) (bool, error) {
	var id string
	err := tx.QueryRow(ctx, sql, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
func commanderAuthorize(ctx context.Context, tx pgx.Tx, actor string, target commanderTarget) error {
	var targetFound, userFound bool
	var err error
	if target.scope == "key" {
		tenantFound, e := commanderLockRow(ctx, tx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, target.tenant)
		if e != nil {
			return e
		}
		membershipFound, e := commanderLockRow(ctx, tx, `SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, target.tenant, actor)
		if e != nil {
			return e
		}
		userFound, e = commanderLockRow(ctx, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, actor)
		if e != nil {
			return e
		}
		targetFound, e = commanderLockRow(ctx, tx, `SELECT id FROM api_keys WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, target.tenant, target.id)
		if e != nil {
			return e
		}
		// A new statement after every root/actor/Key wait establishes current
		// permission; none of these states come from a pre-lock snapshot.
		var tenantStatus, memberStatus, userStatus, role string
		err = tx.QueryRow(ctx, `SELECT t.status,m.status,u.status,m.role FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=$1 AND u.id=$2`, target.tenant, actor).Scan(&tenantStatus, &memberStatus, &userStatus, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if !tenantFound || !membershipFound || !userFound || tenantStatus != "active" || memberStatus != "active" || userStatus != "active" || (role != "owner" && role != "member") {
			return ErrForbidden
		}
		if !targetFound {
			return ErrNotFound
		}
		var owner string
		var revoked pgtype.Timestamptz
		if err = tx.QueryRow(ctx, `SELECT owner_id,revoked_at FROM api_keys WHERE tenant_id=$1 AND id=$2`, target.tenant, target.id).Scan(&owner, &revoked); err != nil {
			return err
		}
		if role != "owner" && owner != actor {
			return ErrNotFound
		}
		if revoked.Valid {
			return ErrForbidden
		}
		return nil
	}
	if target.scope == "tenant" {
		targetFound, err = commanderLockRow(ctx, tx, `SELECT id FROM tenants WHERE id=$1 FOR NO KEY UPDATE`, target.id)
		if err != nil {
			return err
		}
		userFound, err = commanderLockRow(ctx, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, actor)
	} else {
		userFound, err = commanderLockRow(ctx, tx, `SELECT id FROM users WHERE id=$1 FOR SHARE`, actor)
		if err != nil {
			return err
		}
		targetFound, err = commanderLockRow(ctx, tx, `SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE`, target.id)
	}
	if err != nil {
		return err
	}
	var activeAdmin bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND platform_role='admin')`, actor).Scan(&activeAdmin); err != nil {
		return err
	}
	if !userFound || !activeAdmin {
		return ErrForbidden
	}
	if !targetFound {
		return ErrNotFound
	}
	return nil
}
