package periodicbudget

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func PermitDispatchTx(ctx context.Context, tx pgx.Tx, customer CustomerLocks, supplier SupplierLocks, requestID, attemptID string) (time.Time, error) {
	if !ValidUUID(requestID) || !ValidUUID(attemptID) {
		return time.Time{}, ErrInvalidRequest
	}
	if err := customer.identity.check(ctx, tx); err != nil {
		return time.Time{}, err
	}
	if err := supplier.identity.check(ctx, tx); err != nil {
		return time.Time{}, err
	}
	s, err := readCustomer(ctx, tx, requestID)
	if err != nil {
		return time.Time{}, err
	}
	if !s.rangeMatches(customer) || !s.pending() {
		return time.Time{}, ErrInvariant
	}
	var request, tenant, state string
	var account, binding, currentBinding *string
	err = tx.QueryRow(ctx, `SELECT a.request_id,a.tenant_id,a.delivery_state,p.account_id,p.binding_id,b.id
 FROM upstream_attempts a LEFT JOIN procurement_reservations p ON p.attempt_id=a.id
 LEFT JOIN credential_procurement_bindings b ON b.credential_id=a.credential_id AND b.channel_id=a.channel_id WHERE a.id=$1`, attemptID).
		Scan(&request, &tenant, &state, &account, &binding, &currentBinding)
	if err != nil {
		return time.Time{}, sourceError(err)
	}
	if request != requestID || tenant != customer.tenant || state != "prepared" {
		return time.Time{}, ErrInvariant
	}
	if supplier.none {
		if account != nil || binding != nil || currentBinding != nil {
			return time.Time{}, ErrInvariant
		}
	} else {
		if account == nil || *account != supplier.account || binding == nil || currentBinding == nil || *binding != *currentBinding {
			return time.Time{}, ErrInvariant
		}
	}
	var at pgtype.Timestamptz
	err = tx.QueryRow(ctx, `UPDATE upstream_attempts SET delivery_state='sending'
 WHERE id=$1 AND request_id=$2 AND tenant_id=$3 AND delivery_state='prepared' RETURNING pb_dispatch_checked_at`, attemptID, requestID, customer.tenant).Scan(&at)
	if err != nil {
		return time.Time{}, sourceError(err)
	}
	return dbTime(at)
}
