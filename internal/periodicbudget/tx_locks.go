package periodicbudget

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func LockCustomerTx(ctx context.Context, tx pgx.Tx, tenantID, keyID string) (CustomerLocks, error) {
	if !ValidUUID(tenantID) || !ValidUUID(keyID) {
		return CustomerLocks{}, ErrInvalidRequest
	}
	identity, err := transactionIdentity(ctx, tx)
	if err != nil {
		return CustomerLocks{}, err
	}
	for _, scope := range []Scope{{ScopeTenant, tenantID}, {ScopeKey, keyID}} {
		if _, err = tx.Exec(ctx, "SELECT pb_sync_unconfigured($1,$2)", string(scope.Kind), scope.ID); err != nil {
			return CustomerLocks{}, txSQL(err)
		}
	}
	rows, err := tx.Query(ctx, `SELECT id,scope FROM periodic_budget_controls
 WHERE (scope='tenant' AND tenant_id=$1) OR (scope='key' AND tenant_id=$1 AND key_id=$2)
 ORDER BY id FOR NO KEY UPDATE`, tenantID, keyID)
	if err != nil {
		return CustomerLocks{}, txSQL(err)
	}
	defer rows.Close()
	controls := []lockedControl{}
	seen := map[string]bool{}
	for rows.Next() {
		var id, kind string
		if err = rows.Scan(&id, &kind); err != nil {
			return CustomerLocks{}, txSQL(err)
		}
		if !ValidUUID(id) || (kind != "tenant" && kind != "key") || seen[kind] || len(controls) == 2 {
			return CustomerLocks{}, ErrInvariant
		}
		seen[kind] = true
		scopeID := tenantID
		if kind == "key" {
			scopeID = keyID
		}
		controls = append(controls, lockedControl{id, Scope{ScopeKind(kind), scopeID}})
	}
	if err = rows.Err(); err != nil {
		return CustomerLocks{}, txSQL(err)
	}
	return CustomerLocks{identity, tenantID, keyID, controls}, nil
}

func LockSupplierTx(ctx context.Context, tx pgx.Tx, accountID string) (SupplierLocks, error) {
	if accountID != "" && !ValidUUID(accountID) {
		return SupplierLocks{}, ErrInvalidRequest
	}
	identity, err := transactionIdentity(ctx, tx)
	if err != nil {
		return SupplierLocks{}, err
	}
	if accountID == "" {
		return SupplierLocks{identity: identity, none: true, controls: []lockedControl{}}, nil
	}
	if _, err = tx.Exec(ctx, "SELECT pb_sync_unconfigured('procurement',$1)", accountID); err != nil {
		return SupplierLocks{}, txSQL(err)
	}
	rows, err := tx.Query(ctx, "SELECT id FROM periodic_budget_controls WHERE scope='procurement' AND account_id=$1 FOR NO KEY UPDATE", accountID)
	if err != nil {
		return SupplierLocks{}, txSQL(err)
	}
	defer rows.Close()
	controls := []lockedControl{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return SupplierLocks{}, txSQL(err)
		}
		if !ValidUUID(id) || len(controls) != 0 {
			return SupplierLocks{}, ErrInvariant
		}
		controls = append(controls, lockedControl{id, Scope{ScopeProcurement, accountID}})
	}
	if err = rows.Err(); err != nil {
		return SupplierLocks{}, txSQL(err)
	}
	return SupplierLocks{identity: identity, account: accountID, controls: controls}, nil
}

func (locks CustomerLocks) control(kind ScopeKind) string {
	for _, c := range locks.controls {
		if c.scope.Kind == kind {
			return c.id
		}
	}
	return ""
}
func (locks SupplierLocks) control() string {
	if len(locks.controls) == 1 {
		return locks.controls[0].id
	}
	return ""
}
