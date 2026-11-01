package maintenancetrust

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

type Rows struct{ state *leaseQuery }
type Row struct {
	state   *leaseQuery
	failure error
}

// driver is immutable once returned by Query. Its methods are called only by
// the lease's short method owner; all other fields are protected by gate.mu.
type leaseQuery struct {
	lease                                             *leaseState
	driver                                            pgx.Rows
	one                                               bool
	current, consumed, eof, errSeen, closed, released bool
}

func (Rows) String() string               { return "maintenance_rows_redacted" }
func (Rows) GoString() string             { return "maintenance_rows_redacted" }
func (Rows) MarshalJSON() ([]byte, error) { return []byte(`{"rows":"redacted"}`), nil }
func (Row) String() string                { return "maintenance_row_redacted" }
func (Row) GoString() string              { return "maintenance_row_redacted" }
func (Row) MarshalJSON() ([]byte, error)  { return []byte(`{"row":"redacted"}`), nil }

func (l *ReadLease) querySQL(sql string, args []any, one bool) (*leaseQuery, error) {
	if l == nil || l.state == nil || l.state.snapshot == nil || l.state.snapshot.gate == nil {
		return nil, ErrInput
	}
	ls := l.state
	if err := ls.beginMethod(); err != nil {
		return nil, err
	}
	defer ls.endMethod()
	gs := ls.snapshot.gate
	gs.mu.Lock()
	pending := ls.query != nil
	gs.mu.Unlock()
	if pending {
		return nil, ErrGateBusy
	}
	if strings.TrimSpace(sql) == "" {
		return nil, ls.fail(ErrInput)
	}
	owned, err := leaseArguments(args)
	if err != nil {
		return nil, ls.fail(err)
	}
	arguments := make([]any, 1, len(owned)+1)
	arguments[0] = pgx.QueryExecModeSimpleProtocol
	arguments = append(arguments, owned...)
	driver, err := ls.snapshot.rawTx.Query(ls.call.ctx, sql, arguments...)
	if err != nil {
		return nil, ls.fail(ErrExecution)
	}
	gs.mu.Lock()
	err = ls.validLocked()
	var q *leaseQuery
	if err == nil {
		q = &leaseQuery{lease: ls, driver: driver, one: one}
		ls.query = q
	}
	gs.mu.Unlock()
	if err != nil {
		return nil, ls.fail(err)
	}
	return q, nil
}
func (l *ReadLease) Query(sql string, args ...any) (*Rows, error) {
	q, err := l.querySQL(sql, args, false)
	if err != nil {
		return nil, err
	}
	return &Rows{state: q}, nil
}
func (l *ReadLease) QueryRow(sql string, args ...any) *Row {
	q, err := l.querySQL(sql, args, true)
	return &Row{state: q, failure: err}
}

func (q *leaseQuery) enter() error {
	if q == nil || q.lease == nil || q.lease.snapshot == nil || q.lease.snapshot.gate == nil {
		return ErrInput
	}
	return q.lease.beginMethod()
}
func (q *leaseQuery) validLocked() bool { return q.driver != nil && !q.released && q.lease.query == q }
func (q *leaseQuery) releaseLocked() {
	if q.eof && q.errSeen && q.closed && !q.released {
		q.released = true
		if q.lease.query == q {
			q.lease.query = nil
		}
	}
}
func (q *leaseQuery) postDriver() error {
	gs := q.lease.snapshot.gate
	gs.mu.Lock()
	err := q.lease.validLocked()
	gs.mu.Unlock()
	if err != nil {
		return q.lease.fail(err)
	}
	return nil
}

func (r *Rows) Next() (bool, error) {
	if r == nil || r.state == nil {
		return false, ErrInput
	}
	q := r.state
	if err := q.enter(); err != nil {
		return false, err
	}
	defer q.lease.endMethod()
	gs := q.lease.snapshot.gate
	gs.mu.Lock()
	valid := q.validLocked() && !q.one
	skipped := q.current && !q.consumed
	eof := q.eof
	gs.mu.Unlock()
	if !valid || skipped {
		return false, q.lease.fail(ErrTransaction)
	}
	if eof {
		return false, nil
	}
	next := q.driver.Next()
	var err error
	if !next {
		err = q.driver.Err()
	}
	if err != nil {
		return false, q.lease.fail(ErrExecution)
	}
	if err = q.postDriver(); err != nil {
		return false, err
	}
	gs.mu.Lock()
	q.current = next
	q.consumed = false
	q.eof = !next
	gs.mu.Unlock()
	return next, nil
}

func (r *Rows) Scan(dest ...any) error {
	if r == nil || r.state == nil {
		return ErrInput
	}
	q := r.state
	if err := q.enter(); err != nil {
		return err
	}
	defer q.lease.endMethod()
	gs := q.lease.snapshot.gate
	gs.mu.Lock()
	valid := q.validLocked() && !q.one && q.current && !q.consumed && !q.eof
	gs.mu.Unlock()
	if !valid {
		return q.lease.fail(ErrTransaction)
	}
	targets, decode, err := leaseDestinations(dest)
	if err != nil {
		return q.lease.fail(err)
	}
	if q.driver.Scan(decode...) != nil {
		return q.lease.fail(ErrExecution)
	}
	leaseOwnTargets(targets)
	gs.mu.Lock()
	err = q.lease.validLocked()
	if err == nil {
		leaseAssign(targets)
		q.consumed = true
	}
	gs.mu.Unlock()
	if err != nil {
		return q.lease.fail(err)
	}
	return nil
}

func (r *Rows) Err() error {
	if r == nil || r.state == nil || r.state.lease == nil {
		return ErrInput
	}
	q := r.state
	ls := q.lease
	gs := ls.snapshot.gate
	gs.mu.Lock()
	released := q.released
	var releasedError error
	if released {
		releasedError = ls.validLocked()
	}
	gs.mu.Unlock()
	if released {
		return releasedError
	}
	if err := q.enter(); err != nil {
		return err
	}
	defer ls.endMethod()
	gs.mu.Lock()
	valid := q.validLocked() && !q.one
	if valid && q.eof {
		q.errSeen = true
		q.releaseLocked()
	}
	gs.mu.Unlock()
	if !valid {
		return ls.fail(ErrTransaction)
	}
	return nil
}

func (r *Rows) Close() error {
	if r == nil || r.state == nil || r.state.lease == nil {
		return ErrInput
	}
	q := r.state
	ls := q.lease
	gs := ls.snapshot.gate
	gs.mu.Lock()
	inert := q.released || ls.revoked || ls.finished || ls.released
	gs.mu.Unlock()
	if inert {
		return nil
	}
	if err := q.enter(); err != nil {
		return err
	}
	defer ls.endMethod()
	gs.mu.Lock()
	valid := q.validLocked() && !q.one && q.eof
	closed := q.closed
	gs.mu.Unlock()
	if !valid {
		return ls.fail(ErrTransaction)
	}
	if !closed {
		q.driver.Close()
		if err := q.postDriver(); err != nil {
			return err
		}
	}
	gs.mu.Lock()
	q.closed = true
	q.releaseLocked()
	gs.mu.Unlock()
	return nil
}

func (r *Row) Scan(dest ...any) error {
	if r == nil {
		return ErrInput
	}
	if r.failure != nil {
		return r.failure
	}
	if r.state == nil {
		return ErrInput
	}
	q := r.state
	if err := q.enter(); err != nil {
		return err
	}
	defer q.lease.endMethod()
	gs := q.lease.snapshot.gate
	gs.mu.Lock()
	valid := q.validLocked() && q.one
	gs.mu.Unlock()
	if !valid {
		return q.lease.fail(ErrTransaction)
	}
	targets, decode, err := leaseDestinations(dest)
	if err != nil {
		return q.lease.fail(err)
	}
	if !q.driver.Next() {
		return q.lease.fail(ErrExecution)
	}
	if err = q.postDriver(); err != nil {
		return err
	}
	if q.driver.Scan(decode...) != nil {
		return q.lease.fail(ErrExecution)
	}
	leaseOwnTargets(targets)
	if err = q.postDriver(); err != nil {
		return err
	}
	if q.driver.Next() || q.driver.Err() != nil {
		return q.lease.fail(ErrExecution)
	}
	q.driver.Close()
	gs.mu.Lock()
	err = q.lease.validLocked()
	if err == nil {
		leaseAssign(targets)
		q.eof, q.errSeen, q.closed = true, true, true
		q.releaseLocked()
	}
	gs.mu.Unlock()
	if err != nil {
		return q.lease.fail(err)
	}
	return nil
}
