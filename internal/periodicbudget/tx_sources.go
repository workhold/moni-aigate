package periodicbudget

import (
	"context"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type customerSource struct {
	request, tenant, key, reservation, amount, status string
	tenantMarker, keyMarker                           *string
	at                                                pgtype.Timestamptz
	current                                           bool
}

func readCustomer(ctx context.Context, tx pgx.Tx, request string) (customerSource, error) {
	var s customerSource
	err := tx.QueryRow(ctx, `SELECT r.id,r.tenant_id,r.key_id,v.id,v.amount::text,v.status,
 r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at,pb_current_tuple(r.pb_created_xid,r.xmin)
 FROM request_records r JOIN reservations v ON v.request_id=r.id AND v.tenant_id=r.tenant_id WHERE r.id=$1`, request).
		Scan(&s.request, &s.tenant, &s.key, &s.reservation, &s.amount, &s.status, &s.tenantMarker, &s.keyMarker, &s.at, &s.current)
	if err != nil {
		return s, sourceError(err)
	}
	if !ValidUUID(s.request) || !ValidUUID(s.tenant) || !ValidUUID(s.key) || !ValidUUID(s.reservation) || !ValidInt64AmountString(s.amount) {
		return s, ErrInvariant
	}
	return s, nil
}
func (s customerSource) rangeMatches(locks CustomerLocks) bool {
	return s.tenant == locks.tenant && s.key == locks.key
}
func (s customerSource) marker(kind ScopeKind) *string {
	if kind == ScopeTenant {
		return s.tenantMarker
	}
	return s.keyMarker
}
func (s customerSource) newProof(locks CustomerLocks) (time.Time, error) {
	if !s.rangeMatches(locks) || !s.current || !sameMarker(s.tenantMarker, locks.control(ScopeTenant)) || !sameMarker(s.keyMarker, locks.control(ScopeKey)) {
		return time.Time{}, ErrInvariant
	}
	return dbTime(s.at)
}
func (s customerSource) pending() bool {
	return s.status == "reserved" || s.status == "pending_reconciliation"
}

type supplierSource struct {
	attempt, account, upper, held, spent, state   string
	anomaly                                       bool
	marker                                        *string
	at                                            pgtype.Timestamptz
	current, attemptCurrent                       bool
	request, tenant, credential, channel, binding string
}

func readSupplier(ctx context.Context, tx pgx.Tx, attempt string) (supplierSource, error) {
	var s supplierSource
	err := tx.QueryRow(ctx, `SELECT p.attempt_id,p.account_id,p.upper_amount::text,p.held_amount::text,p.spent_amount::text,p.state,p.anomaly,
 p.pb_control,p.pb_admitted_at,pb_current_tuple(p.pb_created_xid,p.xmin),pb_current_tuple(a.pb_created_xid,a.xmin),
 a.request_id,a.tenant_id,a.credential_id,a.channel_id,p.binding_id
 FROM procurement_reservations p JOIN upstream_attempts a ON a.id=p.attempt_id WHERE p.attempt_id=$1`, attempt).
		Scan(&s.attempt, &s.account, &s.upper, &s.held, &s.spent, &s.state, &s.anomaly, &s.marker, &s.at, &s.current, &s.attemptCurrent, &s.request, &s.tenant, &s.credential, &s.channel, &s.binding)
	if err != nil {
		return s, sourceError(err)
	}
	if !ValidUUID(s.attempt) || !ValidUUID(s.account) || !ValidUUID(s.request) || !ValidUUID(s.tenant) || !ValidUUID(s.credential) || !ValidUUID(s.channel) || !ValidUUID(s.binding) || !ValidInt64AmountString(s.upper) || !ValidNumeric100(s.held) || !ValidNumeric100(s.spent) {
		return s, ErrInvariant
	}
	return s, nil
}
func (s supplierSource) newProof(locks SupplierLocks) (time.Time, error) {
	if locks.none || s.account != locks.account || !s.current || !s.attemptCurrent || !sameMarker(s.marker, locks.control()) || s.state != "prepared" || s.held != s.upper || s.spent != "0" || s.anomaly {
		return time.Time{}, ErrInvariant
	}
	return dbTime(s.at)
}

type procurementEffect struct {
	id, attempt, account, heldDelta, spentDelta, kind, key string
	current                                                bool
}

func readProcurementEffect(ctx context.Context, tx pgx.Tx, id string) (procurementEffect, error) {
	var e procurementEffect
	err := tx.QueryRow(ctx, `SELECT id,attempt_id,account_id,held_delta::text,spent_delta::text,type,event_key,pb_current_tuple(pb_created_xid,xmin)
 FROM procurement_budget_events WHERE id=$1`, id).Scan(&e.id, &e.attempt, &e.account, &e.heldDelta, &e.spentDelta, &e.kind, &e.key, &e.current)
	if err != nil {
		return e, sourceError(err)
	}
	if !ValidUUID(e.id) || !ValidUUID(e.attempt) || !ValidUUID(e.account) {
		return e, ErrInvariant
	}
	if _, ok := new(big.Int).SetString(e.heldDelta, 10); !ok {
		return e, ErrInvariant
	}
	if _, ok := new(big.Int).SetString(e.spentDelta, 10); !ok {
		return e, ErrInvariant
	}
	return e, nil
}
func sumEquals(base, delta, want string) bool {
	b, ok := new(big.Int).SetString(base, 10)
	if !ok {
		return false
	}
	d, ok := new(big.Int).SetString(delta, 10)
	if !ok {
		return false
	}
	return b.Add(b, d).String() == want
}
