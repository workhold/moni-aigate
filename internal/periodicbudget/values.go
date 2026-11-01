package periodicbudget

import (
	"errors"
	"strconv"
	"time"
)

const utc6Layout = "2006-01-02T15:04:05.000000Z"

// These aliases keep protocol values lossless when read from a database or JSON.
// DTO Validate methods establish the corresponding public value constraints.
type UUID = string
type Int64AmountString = string
type Revision = string
type PositiveRevision = string
type Numeric100 = string
type UTC6 = string
type LowerHex64 = string

type domainError struct{ code string }

func (e domainError) Error() string { return e.code }

var (
	ErrInvalidRequest            error = domainError{"invalid_request"}
	ErrInvalidCursor             error = domainError{"invalid_cursor"}
	ErrRequestTooLarge           error = domainError{"request_too_large"}
	ErrForbidden                 error = domainError{"forbidden"}
	ErrNotFound                  error = domainError{"not_found"}
	ErrConflict                  error = domainError{"periodic_budget_conflict"}
	ErrCalendarImmutable         error = domainError{"periodic_calendar_immutable"}
	ErrMaintenance               error = domainError{"periodic_legacy_import_requires_maintenance"}
	ErrOperationConflict         error = domainError{"policy_operation_conflict"}
	ErrRevisionExhausted         error = domainError{"periodic_revision_exhausted"}
	ErrClockRegression           error = domainError{"periodic_clock_regression"}
	ErrInvariant                 error = domainError{"periodic_budget_invariant"}
	ErrOutcomeUnknown            error = domainError{"periodic_budget_outcome_unknown"}
	ErrUnavailable               error = domainError{"periodic_budget_unavailable"}
	ErrRateLimited               error = domainError{"rate_limited"}
	ErrBudgetExceeded            error = domainError{"periodic_budget_exceeded"}
	ErrProcurementBudgetExceeded error = domainError{"procurement_periodic_budget_exceeded"}
)

// ErrorCode recognizes only this package's controlled errors, including wrappers.
// An unrelated error with the same text is deliberately not classified.
func ErrorCode(err error) string {
	var own domainError
	if !errors.As(err, &own) {
		return ""
	}
	switch own.code {
	case "invalid_request", "invalid_cursor", "request_too_large", "forbidden", "not_found",
		"periodic_budget_conflict", "periodic_calendar_immutable", "periodic_legacy_import_requires_maintenance",
		"policy_operation_conflict", "periodic_revision_exhausted", "periodic_clock_regression",
		"periodic_budget_invariant", "periodic_budget_outcome_unknown", "periodic_budget_unavailable", "rate_limited",
		"periodic_budget_exceeded", "procurement_periodic_budget_exceeded":
		return own.code
	}
	return ""
}

func ValidUUID(v string) bool {
	if len(v) != 36 || v == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i := range v {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if v[i] != '-' {
				return false
			}
		} else if !lowerHex(v[i]) {
			return false
		}
	}
	return true
}

func lowerHex(v byte) bool { return v >= '0' && v <= '9' || v >= 'a' && v <= 'f' }

func canonicalDecimal(v string, maximumDigits int) bool {
	if len(v) == 0 || len(v) > maximumDigits || len(v) > 1 && v[0] == '0' {
		return false
	}
	for i := range v {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

func ValidInt64AmountString(v string) bool {
	if !canonicalDecimal(v, 19) {
		return false
	}
	_, err := strconv.ParseInt(v, 10, 64)
	return err == nil
}

func ValidRevision(v string) bool         { return ValidInt64AmountString(v) }
func ValidPositiveRevision(v string) bool { return v != "0" && ValidRevision(v) }
func ValidNumeric100(v string) bool       { return canonicalDecimal(v, 100) }

func ValidUTC6(v string) bool {
	if len(v) != 27 || v[:4] == "0000" {
		return false
	}
	t, err := time.Parse(utc6Layout, v)
	return err == nil && t.Year() >= 1 && t.Year() <= 9999 && t.Format(utc6Layout) == v
}

func ValidLowerHex64(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := range v {
		if !lowerHex(v[i]) {
			return false
		}
	}
	return true
}

func validPeriod(v string) bool { return v == "utc_day" || v == "utc_month" }
