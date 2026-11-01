package periodicbudget

import (
	"context"
	"math/big"

	"github.com/jackc/pgx/v5"
)

func AdmitSupplierTx(ctx context.Context, tx pgx.Tx, locks SupplierLocks, attemptID string, amount Amount) (Admission, error) {
	if !ValidUUID(attemptID) || !ValidInt64AmountString(amount.Decimal) {
		return Admission{}, ErrInvalidRequest
	}
	if err := locks.identity.check(ctx, tx); err != nil {
		return Admission{}, err
	}
	if locks.none {
		return Admission{}, ErrInvariant
	}
	s, err := readSupplier(ctx, tx, attemptID)
	if err != nil {
		return Admission{}, err
	}
	at, err := s.newProof(locks)
	if err != nil {
		return Admission{}, err
	}
	if s.upper != amount.Decimal {
		return Admission{}, ErrInvariant
	}
	windows := make([]string, 0, len(locks.controls))
	for _, c := range locks.controls {
		window, err := gateWindow(ctx, tx, c.id, at, s.upper)
		if err != nil {
			return Admission{}, err
		}
		windows = append(windows, window)
	}
	return newAdmission(locks.identity, Subject{"supplier_attempt", attemptID}, attemptID, s.upper, at, windows), nil
}

func ApplySupplierEffectTx(ctx context.Context, tx pgx.Tx, locks SupplierLocks, attemptID string, effect Effect) error {
	if !ValidUUID(attemptID) || effect.SourceKind != "procurement_event" || !ValidUUID(effect.SourceID) {
		return ErrInvalidRequest
	}
	if err := locks.identity.check(ctx, tx); err != nil {
		return err
	}
	if locks.none {
		return ErrInvariant
	}
	s, err := readSupplier(ctx, tx, attemptID)
	if err != nil {
		return err
	}
	pe, err := readProcurementEffect(ctx, tx, effect.SourceID)
	if err != nil {
		return err
	}
	if s.account != locks.account || pe.attempt != attemptID || pe.account != locks.account {
		return ErrInvariant
	}
	if len(locks.controls) == 0 {
		if s.marker != nil || !pe.current {
			return ErrInvariant
		}
		return nil
	}
	c := locks.controls[0]
	a, found, err := readAllocation(ctx, tx, c.id, "supplier_attempt", attemptID)
	if err != nil {
		return err
	}
	prior, applied, err := readAppliedEffect(ctx, tx, c.id, "procurement_event", effect.SourceID)
	if err != nil {
		return err
	}
	if found {
		if err = a.sourceMatches(ctx, tx, c.id, "supplier_attempt", attemptID, s.upper, s.marker, s.at); err != nil {
			return err
		}
		if err = validateSupplierOrigin(ctx, tx, a, s); err != nil {
			return err
		}
	}
	if applied {
		if !found || a.revision < 1 || prior.allocation != a.id || !sumEquals(prior.beforeHeld, pe.heldDelta, prior.afterHeld) {
			return ErrInvariant
		}
		return nil
	}
	if !pe.current {
		return ErrInvariant
	}
	if !found {
		origin := "legacy_catchup"
		var window *string
		if s.marker != nil {
			at, err := s.newProof(locks)
			if err != nil {
				return err
			}
			if pe.kind != "reserved" || pe.key != "reserve:"+attemptID || pe.heldDelta != s.upper || pe.spentDelta != "0" {
				return ErrInvariant
			}
			var id string
			if err = tx.QueryRow(ctx, "SELECT pb_observe($1,$2)", c.id, at).Scan(&id); err != nil {
				return txSQL(err)
			}
			if !ValidUUID(id) {
				return ErrInvariant
			}
			window = &id
			origin = "new"
		} else {
			var held, spent string
			if err = tx.QueryRow(ctx, `SELECT coalesce(sum(held_delta),0)::text,coalesce(sum(spent_delta),0)::text
 FROM procurement_budget_events WHERE attempt_id=$1 AND account_id=$2 AND id<>$3`, attemptID, s.account, pe.id).Scan(&held, &spent); err != nil {
				return txSQL(err)
			}
			priorSpent, ok := new(big.Int).SetString(spent, 10)
			if held != "0" || !ok || priorSpent.Sign() < 0 || !sumEquals(held, pe.heldDelta, s.held) || !sumEquals(spent, pe.spentDelta, s.spent) {
				return ErrInvariant
			}
		}
		a, err = insertAllocation(ctx, tx, c.id, "supplier_attempt", attemptID, origin, pe.id, window)
		if err != nil {
			return err
		}
	} else {
		if a.revision < 1 || !sumEquals(a.sourceHeld, pe.heldDelta, s.held) || !sumEquals(a.sourceSpent, pe.spentDelta, s.spent) {
			return ErrInvariant
		}
	}
	return appendEffect(ctx, tx, c.id, a.id, "procurement_event", pe.id)
}

func validateSupplierOrigin(ctx context.Context, tx pgx.Tx, a allocationProof, s supplierSource) error {
	if a.origin == "legacy_import" {
		if a.source != s.attempt {
			return ErrInvariant
		}
		return nil
	}
	origin, err := readProcurementEffect(ctx, tx, a.source)
	if err != nil {
		return err
	}
	if origin.attempt != s.attempt || origin.account != s.account {
		return ErrInvariant
	}
	if a.origin == "new" && (origin.kind != "reserved" || origin.key != "reserve:"+s.attempt || origin.heldDelta != s.upper || origin.spentDelta != "0") {
		return ErrInvariant
	}
	return nil
}
