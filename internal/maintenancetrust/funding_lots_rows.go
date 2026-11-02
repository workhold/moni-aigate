package maintenancetrust

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// No phase/query/rows handle is exported. Only the one synchronous owner calls
// pgx; cancellation callbacks revoke state and close sockets without draining.
type fundingLotsRows struct {
	phase                                             *fundingLotsPhase
	driver                                            pgx.Rows
	current, consumed, eof, errSeen, closed, released bool
}

func (p *fundingLotsPhase) beginOperation(ctx context.Context) error {
	if p == nil || p.snapshot == nil || p.snapshot.gate == nil || ctx == nil {
		return ErrInput
	}
	gs := p.snapshot.gate
	if !gs.mu.TryLock() {
		return ErrGateBusy
	}
	if p.operation {
		gs.mu.Unlock()
		return ErrGateBusy
	}
	err := p.validLocked(ctx)
	if err == nil {
		p.operation = true
	}
	gs.mu.Unlock()
	return err
}
func (p *fundingLotsPhase) endOperation() {
	gs := p.snapshot.gate
	gs.mu.Lock()
	p.operation = false
	gs.mu.Unlock()
}

func (p *fundingLotsPhase) query(ctx context.Context, sql string, args ...any) (*fundingLotsRows, error) {
	if err := p.beginOperation(ctx); err != nil {
		return nil, err
	}
	defer p.endOperation()
	gs := p.snapshot.gate
	gs.mu.Lock()
	pending := p.pending != nil
	gs.mu.Unlock()
	if pending {
		return nil, p.fail(ErrTransaction)
	}
	if strings.TrimSpace(sql) == "" {
		return nil, p.fail(ErrInput)
	}
	owned, err := leaseArguments(args)
	if err != nil {
		return nil, p.fail(err)
	}
	parameters := make([]any, 1, len(owned)+1)
	parameters[0] = pgx.QueryExecModeSimpleProtocol
	parameters = append(parameters, owned...)
	driver, err := p.snapshot.rawTx.Query(p.call.ctx, sql, parameters...)
	if err != nil || driver == nil {
		return nil, p.fail(ErrExecution)
	}
	gs.mu.Lock()
	err = p.validLocked(ctx)
	var rows *fundingLotsRows
	if err == nil {
		rows = &fundingLotsRows{phase: p, driver: driver}
		p.pending = rows
	}
	gs.mu.Unlock()
	if err != nil {
		return nil, p.fail(err)
	}
	return rows, nil
}
func (r *fundingLotsRows) enter() error {
	if r == nil || r.phase == nil || r.phase.snapshot == nil || r.phase.snapshot.gate == nil {
		return ErrInput
	}
	return r.phase.beginOperation(r.phase.call.ctx)
}
func (r *fundingLotsRows) validLocked() bool {
	return r.driver != nil && !r.released && r.phase.pending == r
}
func (r *fundingLotsRows) releaseLocked() {
	if r.eof && r.errSeen && r.closed && !r.released {
		r.released = true
		if r.phase.pending == r {
			r.phase.pending = nil
		}
	}
}
func (r *fundingLotsRows) Next() (bool, error) {
	if err := r.enter(); err != nil {
		return false, err
	}
	defer r.phase.endOperation()
	gs := r.phase.snapshot.gate
	gs.mu.Lock()
	valid := r.validLocked()
	skipped := r.current && !r.consumed
	eof := r.eof
	gs.mu.Unlock()
	if !valid || skipped {
		return false, r.phase.fail(ErrTransaction)
	}
	if eof {
		return false, nil
	}
	next := r.driver.Next()
	if !next && r.driver.Err() != nil {
		return false, r.phase.fail(ErrExecution)
	}
	gs.mu.Lock()
	err := r.phase.validLocked(r.phase.call.ctx)
	if err == nil {
		r.current = next
		r.consumed = false
		r.eof = !next
	}
	gs.mu.Unlock()
	if err != nil {
		return false, r.phase.fail(err)
	}
	return next, nil
}
func (r *fundingLotsRows) Scan(dest ...any) error {
	if err := r.enter(); err != nil {
		return err
	}
	defer r.phase.endOperation()
	gs := r.phase.snapshot.gate
	gs.mu.Lock()
	valid := r.validLocked() && r.current && !r.consumed && !r.eof
	gs.mu.Unlock()
	if !valid {
		return r.phase.fail(ErrTransaction)
	}
	targets, decode, err := leaseDestinations(dest)
	if err != nil {
		return r.phase.fail(err)
	}
	if r.driver.Scan(decode...) != nil {
		return r.phase.fail(ErrExecution)
	}
	leaseOwnTargets(targets)
	gs.mu.Lock()
	err = r.phase.validLocked(r.phase.call.ctx)
	if err == nil {
		leaseAssign(targets)
		r.consumed = true
	}
	gs.mu.Unlock()
	if err != nil {
		return r.phase.fail(err)
	}
	return nil
}
func (r *fundingLotsRows) Err() error {
	if r == nil || r.phase == nil || r.phase.snapshot == nil || r.phase.snapshot.gate == nil {
		return ErrInput
	}
	gs := r.phase.snapshot.gate
	gs.mu.Lock()
	released := r.released
	gs.mu.Unlock()
	if released {
		return r.phase.verify(r.phase.call.ctx)
	}
	if err := r.enter(); err != nil {
		return err
	}
	defer r.phase.endOperation()
	gs.mu.Lock()
	valid := r.validLocked()
	if valid && r.eof {
		r.errSeen = true
		r.releaseLocked()
	}
	gs.mu.Unlock()
	if !valid {
		return r.phase.fail(ErrTransaction)
	}
	return nil
}
func (r *fundingLotsRows) Close() error {
	if r == nil || r.phase == nil || r.phase.snapshot == nil || r.phase.snapshot.gate == nil {
		return ErrInput
	}
	gs := r.phase.snapshot.gate
	gs.mu.Lock()
	inert := r.released || r.phase.revoked || r.phase.released
	gs.mu.Unlock()
	if inert {
		return nil
	}
	if err := r.enter(); err != nil {
		return err
	}
	defer r.phase.endOperation()
	gs.mu.Lock()
	valid := r.validLocked() && r.eof
	closed := r.closed
	gs.mu.Unlock()
	if !valid {
		return r.phase.fail(ErrTransaction)
	}
	if !closed {
		r.driver.Close()
		if err := r.phase.verify(r.phase.call.ctx); err != nil {
			return r.phase.fail(err)
		}
	}
	gs.mu.Lock()
	r.closed = true
	r.releaseLocked()
	gs.mu.Unlock()
	return nil
}

type fundingLotsSourceQuery struct {
	phase   *fundingLotsPhase
	failure error
}
type fundingLotsSourceRows struct {
	rows  *fundingLotsRows
	query *fundingLotsSourceQuery
}

func (q *fundingLotsSourceQuery) observe(err error) error {
	if err != nil && q.failure == nil {
		q.failure = err
	}
	return err
}
func (q *fundingLotsSourceQuery) Query(sql string, args ...any) (fundinglotscan.Rows, error) {
	r, e := q.phase.query(q.phase.call.ctx, sql, args...)
	if e != nil {
		return nil, q.observe(e)
	}
	return &fundingLotsSourceRows{r, q}, nil
}
func (r *fundingLotsSourceRows) Next() (bool, error) {
	more, e := r.rows.Next()
	return more, r.query.observe(e)
}
func (r *fundingLotsSourceRows) Scan(dest ...any) error { return r.query.observe(r.rows.Scan(dest...)) }
func (r *fundingLotsSourceRows) Err() error             { return r.query.observe(r.rows.Err()) }
func (r *fundingLotsSourceRows) Close() error           { return r.query.observe(r.rows.Close()) }

type fundingLotsBasisQuery struct{ source *fundingLotsSourceQuery }

func (q fundingLotsBasisQuery) Query(sql string, args ...any) (fundinglotbasis.Rows, error) {
	return q.source.Query(sql, args...)
}

func (p *fundingLotsPhase) noPending(ctx context.Context) error {
	gs := p.snapshot.gate
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if err := p.validLocked(ctx); err != nil {
		return err
	}
	if p.pending != nil || p.operation {
		return ErrTransaction
	}
	return nil
}
