package periodicbudget

import (
	"context"
	"github.com/jackc/pgx/v5"
)

func (r *Reader) windows(ctx context.Context, actor string, target readTarget, cursor string) (WindowPage, error) {
	out := WindowPage{Data: []BudgetWindowView{}}
	if !ValidUUID(actor) || !target.valid() {
		return WindowPage{}, ErrInvalidRequest
	}
	e := r.read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := readAuthorize(ctx, tx, actor, target); e != nil {
			return e
		}
		control, e := readControl(ctx, tx, target)
		if e != nil {
			return e
		}
		binding := target.binding(false)
		var pos CursorPosition
		if cursor != "" {
			pos, e = r.cursor.Decode(binding, cursor)
			if e != nil {
				return e
			}
		}
		if control == "" {
			return nil
		}
		query := `SELECT id,pricing_time(start_at),pricing_time(end_at),spent_amount::text,held_amount::text FROM periodic_budget_windows WHERE control_id=$1`
		args := []any{control}
		if cursor != "" {
			query += ` AND (start_at,id COLLATE "C") < ($2::timestamptz,$3::text COLLATE "C")`
			args = append(args, pos.At, pos.ID)
		}
		query += ` ORDER BY start_at DESC,id COLLATE "C" DESC LIMIT 51`
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		all := make([]BudgetWindowView, 0, 51)
		for rows.Next() {
			var w BudgetWindowView
			if e = rows.Scan(&w.ID, &w.StartAt, &w.EndAt, &w.SpentAmount, &w.HeldAmount); e != nil {
				return e
			}
			if w.Validate() != nil {
				return ErrInvariant
			}
			all = append(all, w)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		rows.Close()
		if len(all) > 50 {
			last := all[49]
			out.NextCursor, e = r.cursor.Encode(binding, CursorPosition{At: last.StartAt, ID: last.ID})
			if e != nil {
				return ErrInvariant
			}
			all = all[:50]
		}
		out.Data = all
		return nil
	})
	if e != nil {
		return WindowPage{}, e
	}
	return out, nil
}
func (r *Reader) allocations(ctx context.Context, actor string, target readTarget, cursor string) (AllocationPage, error) {
	out := AllocationPage{Data: []BudgetAllocationView{}}
	if !ValidUUID(actor) || !target.valid() {
		return AllocationPage{}, ErrInvalidRequest
	}
	e := r.read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if e := readAuthorize(ctx, tx, actor, target); e != nil {
			return e
		}
		control, e := readControl(ctx, tx, target)
		if e != nil {
			return e
		}
		binding := target.binding(true)
		var pos CursorPosition
		if cursor != "" {
			pos, e = r.cursor.Decode(binding, cursor)
			if e != nil {
				return e
			}
		}
		if control == "" {
			return nil
		}
		query := `SELECT id,CASE WHEN origin='new' THEN subject_type WHEN subject_type='customer_request' THEN 'legacy_customer' ELSE 'legacy_supplier' END,
   subject_id,window_id,upper_amount::text,held_amount::text,spent_amount::text,state,pricing_time(created_at) FROM periodic_budget_allocations WHERE control_id=$1`
		args := []any{control}
		if cursor != "" {
			query += ` AND (created_at,id COLLATE "C") < ($2::timestamptz,$3::text COLLATE "C")`
			args = append(args, pos.At, pos.ID)
		}
		query += ` ORDER BY created_at DESC,id COLLATE "C" DESC LIMIT 51`
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return e
		}
		defer rows.Close()
		all := make([]BudgetAllocationView, 0, 51)
		for rows.Next() {
			var a BudgetAllocationView
			if e = rows.Scan(&a.ID, &a.SubjectKind, &a.SubjectID, &a.WindowID, &a.UpperAmount, &a.HeldAmount, &a.SpentAmount, &a.State, &a.CreatedAt); e != nil {
				return e
			}
			if a.Validate() != nil {
				return ErrInvariant
			}
			all = append(all, a)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		rows.Close()
		if len(all) > 50 {
			last := all[49]
			out.NextCursor, e = r.cursor.Encode(binding, CursorPosition{At: last.CreatedAt, ID: last.ID})
			if e != nil {
				return ErrInvariant
			}
			all = all[:50]
		}
		out.Data = all
		return nil
	})
	if e != nil {
		return AllocationPage{}, e
	}
	return out, nil
}
func (r *Reader) PlatformTenantWindows(c context.Context, a, id, s string) (WindowPage, error) {
	return r.windows(c, a, readTarget{kind: readPlatformTenant, id: id}, s)
}
func (r *Reader) WalletWindows(c context.Context, a, t, s string) (WindowPage, error) {
	return r.windows(c, a, readTarget{kind: readWallet, id: t, tenant: t}, s)
}
func (r *Reader) KeyWindows(c context.Context, a, t, k, s string) (WindowPage, error) {
	return r.windows(c, a, readTarget{kind: readKey, id: k, tenant: t}, s)
}
func (r *Reader) ProcurementWindows(c context.Context, a, id, s string) (WindowPage, error) {
	return r.windows(c, a, readTarget{kind: readProcurement, id: id}, s)
}
func (r *Reader) PlatformTenantAllocations(c context.Context, a, id, s string) (AllocationPage, error) {
	return r.allocations(c, a, readTarget{kind: readPlatformTenant, id: id}, s)
}
func (r *Reader) WalletAllocations(c context.Context, a, t, s string) (AllocationPage, error) {
	return r.allocations(c, a, readTarget{kind: readWallet, id: t, tenant: t}, s)
}
func (r *Reader) KeyAllocations(c context.Context, a, t, k, s string) (AllocationPage, error) {
	return r.allocations(c, a, readTarget{kind: readKey, id: k, tenant: t}, s)
}
func (r *Reader) ProcurementAllocations(c context.Context, a, id, s string) (AllocationPage, error) {
	return r.allocations(c, a, readTarget{kind: readProcurement, id: id}, s)
}
