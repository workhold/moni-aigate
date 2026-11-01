package periodicbudget

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type ScopeKind string

const (
	ScopeTenant      ScopeKind = "tenant"
	ScopeKey         ScopeKind = "key"
	ScopeProcurement ScopeKind = "procurement"
)

type Scope struct {
	Kind ScopeKind
	ID   string
}
type Subject struct {
	Kind string
	ID   string
}
type Amount struct{ Decimal string }
type Effect struct{ SourceKind, SourceID string }

type lockedControl struct {
	id    string
	scope Scope
}
type txIdentity struct {
	tx  pgx.Tx
	xid string
}

// CustomerLocks and SupplierLocks are transaction-bound proofs of control locks.
// They do not prove the caller's authentication or preceding business locks.
type CustomerLocks struct {
	identity    *txIdentity
	tenant, key string
	controls    []lockedControl
}
type SupplierLocks struct {
	identity *txIdentity
	account  string
	controls []lockedControl
	none     bool
}

type Admission struct {
	At        time.Time
	WindowIDs []string
	proof     *admissionProof
}
type admissionProof struct {
	identity       *txIdentity
	subject        Subject
	source, amount string
	at             time.Time
	windows        []string
}

// txFailure keeps the original cause available for the caller's existing
// transaction policy, while its text and public classification remain fixed.
type txFailure struct {
	code  string
	cause error
}

func (e *txFailure) Error() string { return e.code }
func (e *txFailure) Unwrap() error { return e.cause }
func (e *txFailure) As(target any) bool {
	if own, ok := target.(*domainError); ok {
		*own = domainError{e.code}
		return true
	}
	return false
}
func txCause(code string, err error) error { return &txFailure{code, err} }

func txSQL(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrTxClosed) {
		return txCause("periodic_budget_invariant", err)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23514" {
		if pg.Message == "periodic clock regression" {
			return txCause("periodic_clock_regression", err)
		}
		if pg.Message == "periodic budget exceeded" {
			switch pg.ConstraintName {
			case "pb_budget_tenant", "pb_budget_key":
				return txCause("periodic_budget_exceeded", err)
			case "pb_budget_procurement":
				return txCause("procurement_periodic_budget_exceeded", err)
			}
		}
	}
	return txCause("periodic_budget_unavailable", err)
}

func sourceError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return txCause("periodic_budget_invariant", err)
	}
	return txSQL(err)
}

// A comparable object plus its full transaction xid distinguishes transactions
// even if a wrapper reuses a connection or replaces its underlying transaction.
func comparableTx(tx pgx.Tx) bool {
	if tx == nil {
		return false
	}
	v := reflect.ValueOf(tx)
	if !v.Comparable() {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if v.IsNil() {
			return false
		}
	}
	if v.Kind() == reflect.Pointer && v.Elem().IsZero() {
		return false
	}
	return !v.IsZero()
}
func transactionIdentity(ctx context.Context, tx pgx.Tx) (*txIdentity, error) {
	if !comparableTx(tx) {
		return nil, ErrInvariant
	}
	var xid, isolation string
	if err := tx.QueryRow(ctx, "SELECT pg_current_xact_id()::text,current_setting('transaction_isolation')").Scan(&xid, &isolation); err != nil {
		return nil, txSQL(err)
	}
	if xid == "" || isolation != "read committed" {
		return nil, ErrInvariant
	}
	return &txIdentity{tx, xid}, nil
}
func (id *txIdentity) check(ctx context.Context, tx pgx.Tx) error {
	if id == nil || !comparableTx(tx) || !comparableTx(id.tx) || id.tx != tx {
		return ErrInvariant
	}
	actual, err := transactionIdentity(ctx, tx)
	if err != nil {
		return err
	}
	if actual.xid != id.xid {
		return ErrInvariant
	}
	return nil
}
func dbTime(value pgtype.Timestamptz) (time.Time, error) {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return time.Time{}, ErrInvariant
	}
	at := value.Time.UTC()
	if at.Nanosecond()%1000 != 0 || !ValidUTC6(at.Format(utc6Layout)) {
		return time.Time{}, ErrInvariant
	}
	return at, nil
}
func sameMarker(marker *string, control string) bool {
	if control == "" {
		return marker == nil
	}
	return marker != nil && *marker == control
}
func newAdmission(identity *txIdentity, subject Subject, source, amount string, at time.Time, windows []string) Admission {
	return Admission{at, append([]string{}, windows...), &admissionProof{identity, subject, source, amount, at, append([]string{}, windows...)}}
}
func (a Admission) matches(identity *txIdentity, subject Subject, source, amount string, at time.Time) bool {
	p := a.proof
	if p == nil || p.identity != identity || p.subject != subject || p.source != source || p.amount != amount || p.at != at || a.At != p.at || len(a.WindowIDs) != len(p.windows) {
		return false
	}
	for i := range p.windows {
		if a.WindowIDs[i] != p.windows[i] {
			return false
		}
	}
	return true
}
