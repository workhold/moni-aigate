package periodicbudget

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func commanderExecute(ctx context.Context, tx pgx.Tx, actor string, target commanderTarget, in PutPeriodicBudget) (ConfigureResult, error) {
	if err := commanderAuthorize(ctx, tx, actor, target); err != nil {
		return ConfigureResult{}, err
	}
	original, found, err := commanderOriginal(ctx, tx, actor, target, in)
	if err != nil {
		return ConfigureResult{}, err
	}
	if found {
		return ConfigureResult{Receipt: original}, nil
	}
	var id, revision, period, limit string
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT id,revision::text,period_kind,enabled,limit_amount::text FROM periodic_budget_controls
 WHERE scope=$1 AND CASE scope WHEN 'tenant' THEN tenant_id WHEN 'key' THEN key_id ELSE account_id END=$2 FOR NO KEY UPDATE`, target.scope, target.id).Scan(&id, &revision, &period, &enabled, &limit)
	created := errors.Is(err, pgx.ErrNoRows)
	if !created && err != nil {
		return ConfigureResult{}, err
	}
	if created {
		if in.ExpectedRevision != "0" {
			return ConfigureResult{}, ErrConflict
		}
		if _, err = tx.Exec(ctx, `SELECT pb_scope_sync($1,$2,true)`, target.scope, target.id); err != nil {
			return ConfigureResult{}, err
		}
		sources, err := commanderLegacySources(ctx, tx, target)
		if err != nil {
			return ConfigureResult{}, err
		}
		if len(sources) > 1000 {
			return ConfigureResult{}, ErrMaintenance
		}
		id = uuid.NewString()
		var tenant, key, account any
		switch target.scope {
		case "tenant":
			tenant = target.id
		case "key":
			tenant, key = target.tenant, target.id
		case "procurement":
			account = target.id
		}
		_, err = tx.Exec(ctx, `INSERT INTO periodic_budget_controls(id,scope,tenant_id,key_id,account_id,period_kind,enabled,limit_amount,configuration_operation_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8::bigint,$9)`, id, target.scope, tenant, key, account, in.PeriodKind, in.Enabled, in.LimitAmount, in.OperationID)
		if err != nil {
			return ConfigureResult{}, err
		}
		if err = commanderImport(ctx, tx, target.scope, id, sources); err != nil {
			return ConfigureResult{}, err
		}
	} else {
		if !ValidUUID(id) || !ValidPositiveRevision(revision) || !validPeriod(period) || !ValidInt64AmountString(limit) {
			return ConfigureResult{}, ErrInvariant
		}
		if in.ExpectedRevision != revision {
			return ConfigureResult{}, ErrConflict
		}
		if in.PeriodKind != period {
			return ConfigureResult{}, ErrCalendarImmutable
		}
		if enabled != in.Enabled || limit != in.LimitAmount {
			if revision == "9223372036854775807" {
				return ConfigureResult{}, ErrRevisionExhausted
			}
			_, err = tx.Exec(ctx, `UPDATE periodic_budget_controls SET enabled=$2,limit_amount=$3::bigint,configuration_operation_id=$4 WHERE id=$1`, id, in.Enabled, in.LimitAmount, in.OperationID)
			if err != nil {
				return ConfigureResult{}, err
			}
		}
	}
	receipt, err := commanderInsertReceipt(ctx, tx, actor, id, target, in)
	if err != nil {
		return ConfigureResult{}, err
	}
	return ConfigureResult{Receipt: receipt, Created: created}, nil
}
func commanderLegacySources(ctx context.Context, tx pgx.Tx, target commanderTarget) ([]string, error) {
	query := `SELECT r.id FROM request_records r JOIN reservations v ON v.request_id=r.id AND v.tenant_id=r.tenant_id
 WHERE r.tenant_id=$1 AND ($2::text IS NULL OR r.key_id=$2) AND v.status IN ('reserved','pending_reconciliation') ORDER BY r.id COLLATE "C" LIMIT 1001`
	args := []any{target.id, nil}
	if target.scope == "key" {
		args = []any{target.tenant, target.id}
	}
	if target.scope == "procurement" {
		query = `SELECT attempt_id FROM procurement_reservations WHERE account_id=$1 AND state IN ('prepared','sending','unknown','disputed') ORDER BY attempt_id COLLATE "C" LIMIT 1001`
		args = []any{target.id}
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 1001)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if !ValidUUID(id) {
			return nil, ErrInvariant
		}
		out = append(out, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func commanderImport(ctx context.Context, tx pgx.Tx, scope, control string, sources []string) error {
	kind := "customer_request"
	if scope == "procurement" {
		kind = "supplier_attempt"
	}
	for _, source := range sources {
		allocation := uuid.NewString()
		_, err := tx.Exec(ctx, `INSERT INTO periodic_budget_allocations(id,control_id,subject_type,subject_id,origin,origin_source_id,upper_amount,baseline,source_spent)
 VALUES($1,$2,$3,$4,'legacy_import',$4,0,0,0)`, allocation, control, kind, source)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO periodic_budget_events(id,control_id,allocation_id,source_kind,source_id) VALUES($1,$2,$3,'legacy_import',$4)`, uuid.NewString(), control, allocation, source)
		if err != nil {
			return err
		}
	}
	return nil
}
