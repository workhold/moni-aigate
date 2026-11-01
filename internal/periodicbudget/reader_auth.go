package periodicbudget

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func readPlatformIdentity(ctx context.Context, tx pgx.Tx, actor string) error {
	var status, role string
	e := tx.QueryRow(ctx, `SELECT status,platform_role FROM users WHERE id=$1`, actor).Scan(&status, &role)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrForbidden
	}
	if e != nil {
		return e
	}
	if status != "active" || (role != "admin" && role != "finance_operator" && role != "finance_approver") {
		return ErrForbidden
	}
	return nil
}
func readTenantIdentity(ctx context.Context, tx pgx.Tx, actor, tenant string) (string, error) {
	var ts, ms, us, role string
	e := tx.QueryRow(ctx, `SELECT t.status,m.status,u.status,m.role FROM tenants t JOIN memberships m ON m.tenant_id=t.id JOIN users u ON u.id=m.user_id WHERE t.id=$1 AND m.user_id=$2`, tenant, actor).Scan(&ts, &ms, &us, &role)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	if e != nil {
		return "", e
	}
	if ts != "active" || ms != "active" || us != "active" || (role != "owner" && role != "member" && role != "billing_viewer") {
		return "", ErrForbidden
	}
	return role, nil
}
func readAuthorize(ctx context.Context, tx pgx.Tx, actor string, t readTarget) error {
	if t.kind == readPlatformTenant || t.kind == readProcurement {
		if e := readPlatformIdentity(ctx, tx, actor); e != nil {
			return e
		}
		var exists bool
		query := `SELECT EXISTS(SELECT 1 FROM tenants WHERE id=$1)`
		if t.kind == readProcurement {
			query = `SELECT EXISTS(SELECT 1 FROM procurement_accounts WHERE id=$1)`
		}
		if e := tx.QueryRow(ctx, query, t.id).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
		return nil
	}
	role, e := readTenantIdentity(ctx, tx, actor, t.tenant)
	if e != nil {
		return e
	}
	if t.kind == readWallet {
		if role != "owner" && role != "billing_viewer" {
			return ErrForbidden
		}
		return nil
	}
	var owner string
	e = tx.QueryRow(ctx, `SELECT owner_id FROM api_keys WHERE tenant_id=$1 AND id=$2`, t.tenant, t.id).Scan(&owner)
	if errors.Is(e, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if role != "owner" && owner != actor {
		return ErrNotFound
	}
	return nil
}
func readControl(ctx context.Context, tx pgx.Tx, t readTarget) (string, error) {
	var id string
	e := tx.QueryRow(ctx, `SELECT id FROM periodic_budget_controls WHERE scope=$1 AND CASE scope WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id ELSE account_id END=$2`, t.scope(), t.id).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if !ValidUUID(id) {
		return "", ErrInvariant
	}
	return id, nil
}
