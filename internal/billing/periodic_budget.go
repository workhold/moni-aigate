package billing

import (
	"context"
	"errors"
	"time"

	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// BudgetRejection is the complete customer-safe description of a periodic
// admission rejection. Amounts remain decimal micro-COIN strings.
type BudgetRejection struct {
	Scope         string `json:"scope"`
	Reason        string `json:"reason"`
	WindowUsed    string `json:"window_used"`
	LimitAmount   string `json:"limit_amount"`
	WindowResetAt string `json:"window_reset_at"`
}

func (r BudgetRejection) Valid() bool {
	return (r.Scope == "tenant" || r.Scope == "key") && r.Reason == "limit_exceeded" &&
		periodicbudget.ValidNumeric100(r.WindowUsed) && periodicbudget.ValidInt64AmountString(r.LimitAmount) && periodicbudget.ValidUTC6(r.WindowResetAt)
}

// Preserve PostgreSQL's retry identity without exposing its message through
// the billing/HTTP error boundary.
type periodicFailure struct {
	public    *Error
	cause     error
	rejection *BudgetRejection
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
func (e *periodicFailure) BudgetRejection() (BudgetRejection, bool) {
	if e.rejection == nil || !e.rejection.Valid() {
		return BudgetRejection{}, false
	}
	return *e.rejection, true
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

// addBudgetRejection performs a read-only, post-rollback lookup because pb_gate
// deliberately returns only a controlled code. The original safe rejection is
// preserved if a still-violating window cannot be confirmed; no request,
// reservation, event or other business fact is created by this lookup.
func (s *Store) addBudgetRejection(ctx context.Context, err error, tenantID, keyID string, amount Money) error {
	var public *Error
	if err == nil || !errors.As(err, &public) || public.Code != "periodic_budget_exceeded" || amount < 0 || s == nil || s.pool == nil {
		return err
	}
	var rejection BudgetRejection
	var reset time.Time
	original := err
	// Match the Reserve formula exactly: current-window settled spend plus all
	// held amounts (including old windows) plus this request's complete R.
	// Control ID order matches LockCustomerTx/AdmitCustomerTx when both violate.
	readErr := s.pool.QueryRow(ctx, `WITH observation AS (SELECT pb_now() AS at)
 SELECT c.scope,(coalesce(w.spent_amount,0)+c.held_amount)::text,c.limit_amount::text,pb_end(c.period_kind,pb_start(c.period_kind,o.at))
 FROM observation o
 JOIN periodic_budget_controls c ON ((c.scope='tenant' AND c.tenant_id=$1) OR (c.scope='key' AND c.tenant_id=$1 AND c.key_id=$2))
 LEFT JOIN periodic_budget_windows w ON w.control_id=c.id AND w.start_at=pb_start(c.period_kind,o.at)
 WHERE c.enabled AND o.at>=c.effective_from AND coalesce(w.spent_amount,0)+c.held_amount+$3::bigint>c.limit_amount
 ORDER BY c.id LIMIT 1`, tenantID, keyID, int64(amount)).Scan(&rejection.Scope, &rejection.WindowUsed, &rejection.LimitAmount, &reset)
	if readErr != nil {
		return original
	}
	rejection.Reason = "limit_exceeded"
	rejection.WindowResetAt = reset.UTC().Format("2006-01-02T15:04:05.000000Z")
	if !rejection.Valid() {
		return original
	}
	var failure *periodicFailure
	if errors.As(original, &failure) {
		clone := *failure
		clone.rejection = &rejection
		return &clone
	}
	return &periodicFailure{public: public, cause: original, rejection: &rejection}
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
