package maintenancetrust

import (
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// This adapter is never returned by the public observation API. Its private
// owner retains busy and only the fixed bounded source reader supplies SQL.
type fundingLotsObservationRows struct {
	run                             *fundingLotsObservationRun
	driver                          pgx.Rows
	current, consumed, eof, errSeen bool
	closed, released                bool
}

func (r *fundingLotsObservationRun) rowFailure(err error) error {
	if r.failure == nil {
		r.failure = err
	}
	st := r.snapshot
	st.gate.mu.Lock()
	st.invalidateLocked(err)
	st.gate.mu.Unlock()
	st.gate.closeSockets()
	return err
}

func (r *fundingLotsObservationRun) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	if err := r.check(r.snapshot.ctx); err != nil {
		return nil, r.rowFailure(err)
	}
	if r.pending != nil || strings.TrimSpace(sql) == "" {
		return nil, r.rowFailure(ErrTransaction)
	}
	owned, err := leaseArguments(args)
	if err != nil {
		return nil, r.rowFailure(err)
	}
	rows, err := r.snapshot.rawTx.Query(r.snapshot.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, owned...)...)
	if err != nil || rows == nil {
		return nil, r.rowFailure(executionError(r.snapshot.ctx))
	}
	r.pending = &fundingLotsObservationRows{run: r, driver: rows}
	return r.pending, nil
}

func (r *fundingLotsObservationRows) valid() error {
	if err := r.run.check(r.run.snapshot.ctx); err != nil {
		return r.run.rowFailure(err)
	}
	if r.driver == nil || r.released || r.run.pending != r {
		return r.run.rowFailure(ErrTransaction)
	}
	return nil
}

func (r *fundingLotsObservationRows) Next() (bool, error) {
	if err := r.valid(); err != nil {
		return false, err
	}
	if r.current && !r.consumed {
		return false, r.run.rowFailure(ErrTransaction)
	}
	if r.eof {
		return false, nil
	}
	more := r.driver.Next()
	if !more && r.driver.Err() != nil {
		return false, r.run.rowFailure(executionError(r.run.snapshot.ctx))
	}
	if err := r.run.check(r.run.snapshot.ctx); err != nil {
		return false, r.run.rowFailure(err)
	}
	r.current, r.consumed, r.eof = more, false, !more
	return more, nil
}

func (r *fundingLotsObservationRows) Scan(dest ...any) error {
	if err := r.valid(); err != nil {
		return err
	}
	if !r.current || r.consumed || r.eof {
		return r.run.rowFailure(ErrTransaction)
	}
	targets, local, err := leaseDestinations(dest)
	if err != nil {
		return r.run.rowFailure(err)
	}
	if r.driver.Scan(local...) != nil {
		return r.run.rowFailure(executionError(r.run.snapshot.ctx))
	}
	leaseOwnTargets(targets)
	if err = r.run.check(r.run.snapshot.ctx); err != nil {
		return r.run.rowFailure(err)
	}
	leaseAssign(targets)
	r.consumed = true
	return nil
}

func (r *fundingLotsObservationRows) release() {
	if r.eof && r.errSeen && r.closed {
		r.released = true
		r.run.pending = nil
	}
}

func (r *fundingLotsObservationRows) Err() error {
	if r.released {
		return r.run.check(r.run.snapshot.ctx)
	}
	if err := r.valid(); err != nil {
		return err
	}
	if r.driver.Err() != nil {
		return r.run.rowFailure(executionError(r.run.snapshot.ctx))
	}
	if r.eof {
		r.errSeen = true
		r.release()
	}
	return nil
}

func (r *fundingLotsObservationRows) Close() error {
	if r.released || r.run.failure != nil {
		return nil
	}
	if err := r.valid(); err != nil {
		return err
	}
	if !r.eof || !r.errSeen {
		// Do not drain an unfinished result. The resource error which led the
		// bounded reader here retains priority at the public boundary.
		return r.run.rowFailure(ErrTransaction)
	}
	r.driver.Close()
	if r.driver.Err() != nil {
		return r.run.rowFailure(executionError(r.run.snapshot.ctx))
	}
	r.closed = true
	r.release()
	return nil
}
