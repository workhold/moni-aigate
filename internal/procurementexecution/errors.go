package procurementexecution

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Domain string

const (
	DomainProcurement Domain = "procurement"
	DomainBilling     Domain = "billing"
	DomainLookup      Domain = "lookup"
	DomainExecution   Domain = "execution"
)

// Failure exposes fixed public metadata while retaining the exact original
// cause. Business-package As/Is wrappers are a later, separate adapter slice.
type Failure struct {
	domain      Domain
	state, code string
	message     string
	cause       error
}

func (e *Failure) Error() string         { return e.code }
func (e *Failure) Unwrap() error         { return e.cause }
func (e *Failure) Domain() Domain        { return e.domain }
func (e *Failure) SQLState() string      { return e.state }
func (e *Failure) PublicCode() string    { return e.code }
func (e *Failure) PublicMessage() string { return e.message }
func (e *Failure) Is(target error) bool {
	return e.domain == DomainLookup && target == pgx.ErrNoRows
}

// ProtocolError classifies a local protocol failure without manufacturing a
// PostgreSQL error. A real scan/driver cause remains reachable with errors.As.
func ProtocolError(cause error) error {
	return &Failure{domain: DomainExecution, state: "PP903", code: "procurement_execution_protocol", message: "procurement_execution_protocol", cause: cause}
}

type classification struct {
	code, message string
}

var attemptErrors = map[string]classification{
	"PP101": {"invalid_attempt", "credential is not a member of the frozen channel version"},
	"PP102": {"key_ip_forbidden", "request source is not authorized"},
	"PP103": {"invalid_key", "key is not active"},
	"PP105": {"binding_unavailable", "published model binding is quarantined"},
	"PP106": {"request_finished", "request is not eligible for a new attempt"},
	"PP107": {"unpublished_binding", "attempt is not part of the request route snapshot"},
	"PP108": {"request_deadline", "request is no longer eligible for dispatch"},
	"PP109": {"unsafe_retry", "prior attempt has no trustworthy non-consumption evidence"},
	"PP110": {"cost_unknown", "controlled procurement requires a complete known quote"},
	"PP111": {"cost_quote_stale", "configured procurement quote changed before dispatch"},
	"PP112": {"cost_limit_exceeded", "configured procurement rate exceeds route limit"},
	"PP113": {"procurement_bound_invalid", "procurement hard upper bound cannot be represented"},
	"PP114": {"procurement_paused", "supplier procurement account is paused"},
	"PP115": {"procurement_budget_exceeded", "shared supplier procurement budget is exhausted"},
	"PP116": {"idempotency_conflict", "attempt sequence already used"},
	"PP117": {"channel_unavailable", "channel is not available"},
	"PP406": {"forbidden", "key model scope is not authorized"},
	"PP407": {"forbidden", "key channel region is not authorized"},
	"PP408": {"request_deadline", "request deadline expired"},
	"PP409": {"request_deadline", "request deadline expired while waiting for procurement"},
	"PP410": {"unsafe_retry", "prior attempt has no terminal evidence"},
	"PP411": {"cost_quote_stale", "procurement quote changed before attempt"},
	"PP412": {"cost_quote_stale", "configured procurement quote changed before reservation"},
	"PP413": {"cost_unknown", "controlled procurement requires a complete quote"},
	"PP414": {"cost_unknown", "controlled procurement requires both known cost dimensions"},
	"PP415": {"cost_unknown", "route requires a known procurement quote"},
}

var closureErrors = map[string]string{
	"PP901": "procurement_execution_forbidden",
	"PP902": "procurement_history_invariant",
	"PP903": "procurement_execution_protocol",
	"PP904": "procurement_history_resource",
	"PP905": "procurement_history_exhausted",
	"PP906": "procurement_prefix_untracked_domain",
}

func validRoot(root Root) bool {
	return root == CreateAccount || root == ConfigureAccount || root == RecordAttempt
}

// ClassifyPP maps only the fixed root domain. In particular, PP101's Message
// assumes the original billing outer validators already succeeded. Direct SQL
// attack diagnostics must not use this as a replacement for those validators.
// Non-PP errors, unknown PP states and states from another root pass unchanged.
// No PB Message/Context classification or transaction retry occurs here.
func ClassifyPP(root Root, err error) error {
	if err == nil || !validRoot(root) {
		return err
	}
	var existing *Failure
	if errors.As(err, &existing) {
		return err
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	if code, ok := closureErrors[pg.Code]; ok {
		return &Failure{domain: DomainExecution, state: pg.Code, code: code, message: code, cause: err}
	}
	if root == CreateAccount || root == ConfigureAccount {
		var code string
		switch pg.Code {
		case "PP001":
			code = "invalid_procurement_configuration"
		case "PP002":
			code = "procurement_operation_conflict"
		case "PP003":
			code = "procurement_forbidden"
		default:
			return err
		}
		return &Failure{domain: DomainProcurement, state: pg.Code, code: code, message: code, cause: err}
	}
	if pg.Code == "PP190" {
		return &Failure{domain: DomainLookup, state: pg.Code, code: "not_found", message: "resource not found", cause: err}
	}
	if c, ok := attemptErrors[pg.Code]; ok {
		return &Failure{domain: DomainBilling, state: pg.Code, code: c.code, message: c.message, cause: err}
	}
	return err
}
