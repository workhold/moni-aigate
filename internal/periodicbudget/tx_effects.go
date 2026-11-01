package periodicbudget

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type allocationProof struct {
	id, control, kind, subject, origin, source, upper, baseline string
	window                                                      *string
	at                                                          pgtype.Timestamptz
	held, spent, sourceHeld, sourceSpent, state                 string
	revision                                                    int64
}

func readAllocation(ctx context.Context, tx pgx.Tx, control, kind, subject string) (allocationProof, bool, error) {
	var a allocationProof
	err := tx.QueryRow(ctx, `SELECT id,control_id,subject_type,subject_id,origin,origin_source_id,upper_amount::text,baseline::text,window_id,admitted_at,
 held_amount::text,spent_amount::text,source_held::text,source_spent::text,state,revision
 FROM periodic_budget_allocations WHERE control_id=$1 AND subject_type=$2 AND subject_id=$3`, control, kind, subject).
		Scan(&a.id, &a.control, &a.kind, &a.subject, &a.origin, &a.source, &a.upper, &a.baseline, &a.window, &a.at, &a.held, &a.spent, &a.sourceHeld, &a.sourceSpent, &a.state, &a.revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, false, nil
	}
	if err != nil {
		return a, false, txSQL(err)
	}
	if !ValidUUID(a.id) || !ValidUUID(a.control) || !ValidUUID(a.subject) || !ValidUUID(a.source) || !ValidInt64AmountString(a.upper) || !ValidNumeric100(a.baseline) || !ValidNumeric100(a.held) || !ValidNumeric100(a.spent) || !ValidNumeric100(a.sourceHeld) || !ValidNumeric100(a.sourceSpent) || a.revision < 0 {
		return a, false, ErrInvariant
	}
	return a, true, nil
}
func (a allocationProof) sourceMatches(ctx context.Context, tx pgx.Tx, control, kind, subject, upper string, marker *string, at pgtype.Timestamptz) error {
	if a.control != control || a.kind != kind || a.subject != subject || a.upper != upper {
		return ErrInvariant
	}
	if marker != nil {
		if *marker != control || a.origin != "new" || a.window == nil || !ValidUUID(*a.window) || a.baseline != "0" {
			return ErrInvariant
		}
		original, err := dbTime(at)
		if err != nil {
			return err
		}
		allocated, err := dbTime(a.at)
		if err != nil || allocated != original {
			return ErrInvariant
		}
		var matches bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM periodic_budget_windows WHERE id=$1 AND control_id=$2 AND start_at<=$3 AND $3<end_at)", *a.window, control, original).Scan(&matches); err != nil {
			return txSQL(err)
		}
		if !matches {
			return ErrInvariant
		}
	} else {
		if a.origin != "legacy_import" && (kind != "supplier_attempt" || a.origin != "legacy_catchup") {
			return ErrInvariant
		}
		if a.window != nil || a.at.Valid {
			return ErrInvariant
		}
	}
	return nil
}
func insertAllocation(ctx context.Context, tx pgx.Tx, control, kind, subject, origin, source string, window *string) (allocationProof, error) {
	_, err := tx.Exec(ctx, `INSERT INTO periodic_budget_allocations(id,control_id,subject_type,subject_id,origin,origin_source_id,window_id,upper_amount,baseline,source_spent)
 VALUES($1,$2,$3,$4,$5,$6,$7,0,0,0)`, uuid.NewString(), control, kind, subject, origin, source, window)
	if err != nil {
		return allocationProof{}, txSQL(err)
	}
	a, found, err := readAllocation(ctx, tx, control, kind, subject)
	if err != nil {
		return a, err
	}
	if !found {
		return a, ErrInvariant
	}
	return a, nil
}

type appliedEffect struct {
	allocation                                                                   string
	beforeRevision                                                               int64
	beforeHeld, beforeSpent, afterHeld, afterSpent, afterSourceSpent, afterState string
}

func readAppliedEffect(ctx context.Context, tx pgx.Tx, control, kind, source string) (appliedEffect, bool, error) {
	var e appliedEffect
	err := tx.QueryRow(ctx, `SELECT allocation_id,allocation_before_revision,before_held::text,before_spent::text,after_held::text,after_spent::text,after_source_spent::text,after_state
 FROM periodic_budget_events WHERE control_id=$1 AND source_kind=$2 AND source_id=$3`, control, kind, source).
		Scan(&e.allocation, &e.beforeRevision, &e.beforeHeld, &e.beforeSpent, &e.afterHeld, &e.afterSpent, &e.afterSourceSpent, &e.afterState)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, false, nil
	}
	if err != nil {
		return e, false, txSQL(err)
	}
	if !ValidUUID(e.allocation) || e.beforeRevision < 0 || !ValidNumeric100(e.beforeHeld) || !ValidNumeric100(e.beforeSpent) || !ValidNumeric100(e.afterHeld) || !ValidNumeric100(e.afterSpent) || !ValidNumeric100(e.afterSourceSpent) {
		return e, false, ErrInvariant
	}
	return e, true, nil
}
func appendEffect(ctx context.Context, tx pgx.Tx, control, allocation, kind, source string) error {
	_, err := tx.Exec(ctx, "INSERT INTO periodic_budget_events(id,control_id,allocation_id,source_kind,source_id) VALUES($1,$2,$3,$4,$5)", uuid.NewString(), control, allocation, kind, source)
	return txSQL(err)
}
func gateWindow(ctx context.Context, tx pgx.Tx, control string, at any, extra string) (string, error) {
	if _, err := tx.Exec(ctx, "SELECT pb_gate($1,$2,$3::bigint)", control, at, extra); err != nil {
		return "", txSQL(err)
	}
	var window string
	if err := tx.QueryRow(ctx, "SELECT id FROM periodic_budget_windows WHERE control_id=$1 AND start_at=pb_start((SELECT period_kind FROM periodic_budget_controls WHERE id=$1),$2)", control, at).Scan(&window); err != nil {
		return "", sourceError(err)
	}
	if !ValidUUID(window) {
		return "", ErrInvariant
	}
	return window, nil
}
