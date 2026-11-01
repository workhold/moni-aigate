package billing

import (
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// Preserve PostgreSQL's retry identity without exposing its message through
// the billing/HTTP error boundary.
type periodicFailure struct {
	public *Error
	cause  error
}

func (e *periodicFailure) Error() string { return e.public.Error() }
func (e *periodicFailure) Unwrap() error { return e.cause }
func (e *periodicFailure) As(target any) bool {
	if out, ok := target.(**Error); ok {
		*out = e.public
		return true
	}
	return false
}

func periodicError(err error) error {
	if err == nil {
		return nil
	}
	var public *Error
	if errors.As(err, &public) {
		return err
	}
	code := periodicbudget.ErrorCode(err)
	if code == "" {
		return err
	}
	switch code {
	case "periodic_budget_exceeded", "procurement_periodic_budget_exceeded", "periodic_clock_regression", "periodic_budget_invariant", "periodic_budget_unavailable":
	default:
		code = "periodic_budget_invariant"
	}
	return &periodicFailure{public: &Error{Code: code, Message: "periodic budget operation could not complete"}, cause: err}
}

func dispatchTimeEligible(at time.Time, keyExpires *time.Time, deadline, requestDeadline time.Time) error {
	if keyExpires != nil && !at.Before(*keyExpires) {
		return fail("invalid_key", "key is not active")
	}
	if !at.Before(deadline) || !at.Before(requestDeadline) {
		return fail("request_deadline", "request is no longer eligible for dispatch")
	}
	return nil
}
