package periodicbudget

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
)

func readOperation(ctx context.Context, tx pgx.Tx, operation, scope string) (CommandReceipt, error) {
	var out CommandReceipt
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT operation_id,kind,scope,actor,target,input_fingerprint,resource_id,revision::text,pricing_time(created_at),result_snapshot FROM periodic_budget_operations WHERE operation_id=$1 AND scope=$2`, operation, scope).Scan(
		&out.Operation.OperationID, &out.Operation.Kind, &out.Operation.Scope, &out.Operation.Actor, &out.Operation.Target, &out.Operation.InputFingerprint, &out.Operation.ResourceID, &out.Operation.Revision, &out.Operation.CreatedAt, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	out.Result, e = readDecodeView(raw)
	if e != nil {
		return CommandReceipt{}, e
	}
	if out.Validate() != nil {
		return CommandReceipt{}, ErrInvariant
	}
	return out, nil
}
func (r *Reader) PlatformOperation(ctx context.Context, actor, operation string) (CommandReceipt, error) {
	var out CommandReceipt
	if !ValidUUID(actor) || !ValidUUID(operation) {
		return out, ErrInvalidRequest
	}
	e := r.read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := readPlatformIdentity(ctx, tx, actor); e != nil {
			return e
		}
		receipt, e := readOperation(ctx, tx, operation, "platform")
		if e != nil {
			return e
		}
		kind := readPlatformTenant
		if receipt.Result.Scope == "procurement" {
			kind = readProcurement
		} else if receipt.Result.Scope != "tenant" {
			return ErrInvariant
		}
		target := readTarget{kind: kind, id: receipt.Result.ScopeID}
		if e = readAuthorize(ctx, tx, actor, target); e != nil {
			return e
		}
		id, e := readControl(ctx, tx, target)
		if e != nil {
			return e
		}
		if id != receipt.Operation.ResourceID {
			return ErrInvariant
		}
		out = receipt
		return nil
	})
	if e != nil {
		return CommandReceipt{}, e
	}
	return out, nil
}
func (r *Reader) TenantOperation(ctx context.Context, actor, tenant, operation string) (CommandReceipt, error) {
	var out CommandReceipt
	if !ValidUUID(actor) || !ValidUUID(tenant) || !ValidUUID(operation) {
		return out, ErrInvalidRequest
	}
	e := r.read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := readTenantIdentity(ctx, tx, actor, tenant); e != nil {
			return e
		}
		receipt, e := readOperation(ctx, tx, operation, tenant)
		if e != nil {
			return e
		}
		if receipt.Result.Scope != "key" || receipt.Operation.Kind != "key_periodic_budget.configure" {
			return ErrInvariant
		}
		target := readTarget{kind: readKey, id: receipt.Result.ScopeID, tenant: tenant}
		if e = readAuthorize(ctx, tx, actor, target); e != nil {
			return e
		}
		id, e := readControl(ctx, tx, target)
		if e != nil {
			return e
		}
		if id != receipt.Operation.ResourceID {
			return ErrInvariant
		}
		out = receipt
		return nil
	})
	if e != nil {
		return CommandReceipt{}, e
	}
	return out, nil
}
