package periodicbudget

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func AdmitCustomerTx(ctx context.Context, tx pgx.Tx, locks CustomerLocks, requestID string, amount Amount) (Admission, error) {
	if !ValidUUID(requestID) || !ValidInt64AmountString(amount.Decimal) {
		return Admission{}, ErrInvalidRequest
	}
	if err := locks.identity.check(ctx, tx); err != nil {
		return Admission{}, err
	}
	s, err := readCustomer(ctx, tx, requestID)
	if err != nil {
		return Admission{}, err
	}
	at, err := s.newProof(locks)
	if err != nil {
		return Admission{}, err
	}
	if !s.pending() || s.amount != amount.Decimal {
		return Admission{}, ErrInvariant
	}
	windows := make([]string, 0, len(locks.controls))
	for _, c := range locks.controls {
		window, err := gateWindow(ctx, tx, c.id, at, s.amount)
		if err != nil {
			return Admission{}, err
		}
		windows = append(windows, window)
	}
	return newAdmission(locks.identity, Subject{"customer_request", requestID}, s.reservation, s.amount, at, windows), nil
}

func RecordCustomerReserveTx(ctx context.Context, tx pgx.Tx, locks CustomerLocks, admission Admission, requestID string) error {
	if !ValidUUID(requestID) {
		return ErrInvalidRequest
	}
	if err := locks.identity.check(ctx, tx); err != nil {
		return err
	}
	s, err := readCustomer(ctx, tx, requestID)
	if err != nil {
		return err
	}
	at, err := s.newProof(locks)
	if err != nil {
		return err
	}
	if !admission.matches(locks.identity, Subject{"customer_request", requestID}, s.reservation, s.amount, at) || len(admission.WindowIDs) != len(locks.controls) {
		return ErrInvariant
	}
	for i, c := range locks.controls {
		a, found, err := readAllocation(ctx, tx, c.id, "customer_request", requestID)
		if err != nil {
			return err
		}
		e, applied, err := readAppliedEffect(ctx, tx, c.id, "reservation", s.reservation)
		if err != nil {
			return err
		}
		if found {
			if err = a.sourceMatches(ctx, tx, c.id, "customer_request", requestID, s.amount, s.marker(c.scope.Kind), s.at); err != nil {
				return err
			}
			if a.origin != "new" || a.source != s.reservation || a.window == nil || *a.window != admission.WindowIDs[i] {
				return ErrInvariant
			}
		}
		if applied {
			if !found || a.revision < 1 || e.allocation != a.id || e.beforeRevision != 0 || e.beforeHeld != "0" || e.beforeSpent != "0" || e.afterHeld != s.amount || e.afterSpent != "0" || e.afterSourceSpent != "0" || e.afterState != "held" {
				return ErrInvariant
			}
			continue
		}
		if !s.pending() {
			return ErrInvariant
		}
		if found {
			if a.revision != 0 {
				return ErrInvariant
			}
		} else {
			window := admission.WindowIDs[i]
			a, err = insertAllocation(ctx, tx, c.id, "customer_request", requestID, "new", s.reservation, &window)
			if err != nil {
				return err
			}
		}
		if err = appendEffect(ctx, tx, c.id, a.id, "reservation", s.reservation); err != nil {
			return err
		}
	}
	return nil
}

func FinishCustomerTx(ctx context.Context, tx pgx.Tx, locks CustomerLocks, requestID, settlementID string) error {
	if !ValidUUID(requestID) || !ValidUUID(settlementID) {
		return ErrInvalidRequest
	}
	if err := locks.identity.check(ctx, tx); err != nil {
		return err
	}
	s, err := readCustomer(ctx, tx, requestID)
	if err != nil {
		return err
	}
	if !s.rangeMatches(locks) {
		return ErrInvariant
	}
	var id, request, tenant, status, charged string
	if err = tx.QueryRow(ctx, "SELECT id,request_id,tenant_id,status,charged_amount::text FROM settlements WHERE id=$1", settlementID).Scan(&id, &request, &tenant, &status, &charged); err != nil {
		return sourceError(err)
	}
	if id != settlementID || request != requestID || tenant != locks.tenant || !ValidInt64AmountString(charged) || (status != "settled" && status != "released") || s.status != status {
		return ErrInvariant
	}
	for _, c := range locks.controls {
		a, found, err := readAllocation(ctx, tx, c.id, "customer_request", requestID)
		if err != nil {
			return err
		}
		e, applied, err := readAppliedEffect(ctx, tx, c.id, "settlement", settlementID)
		if err != nil {
			return err
		}
		if !found {
			// A source already closed before this control began tracking is not
			// retroactively assigned to a period, and customer catchup is forbidden.
			if applied || s.marker(c.scope.Kind) != nil {
				return ErrInvariant
			}
			continue
		}
		if err = a.sourceMatches(ctx, tx, c.id, "customer_request", requestID, s.amount, s.marker(c.scope.Kind), s.at); err != nil {
			return err
		}
		if a.origin == "new" && a.source != s.reservation || a.origin == "legacy_import" && a.source != requestID {
			return ErrInvariant
		}
		if applied {
			if a.revision < 2 || e.allocation != a.id || e.beforeRevision < 1 || e.afterHeld != "0" || e.afterSpent != charged || e.afterSourceSpent != charged || e.afterState != status || a.held != "0" || a.spent != charged || a.sourceHeld != "0" || a.sourceSpent != charged || a.state != status {
				return ErrInvariant
			}
			continue
		}
		if a.revision != 1 || a.state != "held" || a.held != s.amount || a.sourceHeld != s.amount || a.spent != "0" || a.sourceSpent != "0" {
			return ErrInvariant
		}
		originalKind, originalID := "reservation", s.reservation
		if a.origin == "legacy_import" {
			originalKind, originalID = "legacy_import", requestID
		}
		original, exists, err := readAppliedEffect(ctx, tx, c.id, originalKind, originalID)
		if err != nil {
			return err
		}
		if !exists || original.allocation != a.id || original.beforeRevision != 0 || original.beforeHeld != "0" || original.beforeSpent != "0" || original.afterHeld != s.amount || original.afterSpent != "0" || original.afterSourceSpent != "0" || original.afterState != "held" {
			return ErrInvariant
		}
		if err = appendEffect(ctx, tx, c.id, a.id, "settlement", settlementID); err != nil {
			return err
		}
	}
	return nil
}
