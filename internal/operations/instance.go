package operations

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const InstanceLockKey int64 = 782193402

var ErrInstanceLost = errors.New("active instance ownership lost")

// Instance batches queued checks on the dedicated lock session. A check only
// shares a Ping that starts after it registers; it never reuses an in-flight or
// cached result. Any failed check permanently fences this process.
type Instance struct {
	mu         sync.Mutex
	conn       *pgxpool.Conn
	key        int64
	lost       bool
	pending    []*instanceCheck
	current    []*instanceCheck
	running    bool
	workerDone chan struct{}
	pingCancel context.CancelFunc
	closing    bool
	closeDone  chan struct{}
}

type instanceCheck struct {
	ctx       context.Context
	done      chan struct{}
	err       error
	completed bool
}

func AcquireInstance(ctx context.Context, pool *pgxpool.Pool, key int64) (*Instance, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	var held bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&held); err != nil || !held {
		if err != nil {
			// If the acquisition result is unknown, the session might hold the
			// lock. A pooled live connection must not retain that ownership.
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = conn.Conn().Close(cleanup)
			cancel()
		}
		conn.Release()
		return nil, errors.New("another active instance holds the service lock")
	}
	return &Instance{conn: conn, key: key}, nil
}
func (i *Instance) Check(ctx context.Context) error {
	i.mu.Lock()
	if i.lost || i.conn == nil {
		i.mu.Unlock()
		return ErrInstanceLost
	}
	if ctx.Err() != nil {
		i.fenceLocked()
		i.mu.Unlock()
		return ErrInstanceLost
	}
	w := &instanceCheck{ctx: ctx, done: make(chan struct{})}
	// The registration lock also protects batch snapshots. Once a Ping has
	// started its snapshot is immutable, so later callers await the next Ping.
	i.pending = append(i.pending, w)
	if !i.running {
		i.running = true
		i.workerDone = make(chan struct{})
		go i.runChecks(i.workerDone)
	}
	i.mu.Unlock()
	select {
	case <-ctx.Done():
		i.mu.Lock()
		i.fenceLocked()
		i.mu.Unlock()
		return ErrInstanceLost
	case <-w.done:
		// A caller whose deadline expires as the batch publishes must not
		// turn its own failed check into successful admission.
		if ctx.Err() != nil {
			i.mu.Lock()
			i.fenceLocked()
			i.mu.Unlock()
			return ErrInstanceLost
		}
		return w.err
	}
}

func completeInstanceCheck(w *instanceCheck, err error) {
	if !w.completed {
		w.err = err
		w.completed = true
		close(w.done)
	}
}

// Call with mu held. Cancellation is conservative: a timed-out caller fences
// the whole session rather than allowing its peers to use a successful batch.
func (i *Instance) fenceLocked() {
	i.lost = true
	if i.pingCancel != nil {
		i.pingCancel()
	}
	for _, w := range i.current {
		completeInstanceCheck(w, ErrInstanceLost)
	}
	for _, w := range i.pending {
		completeInstanceCheck(w, ErrInstanceLost)
	}
	i.pending = nil
}

func cancelledInstanceCheck(checks []*instanceCheck) bool {
	for _, w := range checks {
		if w.ctx.Err() != nil {
			return true
		}
	}
	return false
}

func (i *Instance) runChecks(done chan struct{}) {
	for {
		i.mu.Lock()
		if i.lost || len(i.pending) == 0 {
			i.current = nil
			i.running = false
			i.workerDone = nil
			close(done)
			i.mu.Unlock()
			return
		}
		batch := i.pending
		i.pending = nil
		i.current = batch
		if cancelledInstanceCheck(batch) {
			i.fenceLocked()
			i.mu.Unlock()
			continue
		}
		deadline := time.Now().Add(time.Second)
		for _, w := range batch {
			if d, ok := w.ctx.Deadline(); ok && d.Before(deadline) {
				deadline = d
			}
		}
		pingCtx, cancel := context.WithDeadline(context.Background(), deadline)
		i.pingCancel = cancel
		conn := i.conn
		i.mu.Unlock()

		// The real network operation starts strictly after the batch snapshot.
		// Only this worker accesses conn until it returns, including on cancel.
		err := conn.Ping(pingCtx)
		cancel()
		i.mu.Lock()
		i.pingCancel = nil
		if err != nil || i.lost || cancelledInstanceCheck(batch) || cancelledInstanceCheck(i.pending) {
			i.fenceLocked()
		} else {
			for _, w := range batch {
				completeInstanceCheck(w, nil)
			}
		}
		i.current = nil
		i.mu.Unlock()
	}
}

func (i *Instance) Close() {
	i.mu.Lock()
	if i.closing {
		done := i.closeDone
		i.mu.Unlock()
		<-done
		return
	}
	i.closing = true
	i.closeDone = make(chan struct{})
	wasLost := i.lost
	i.fenceLocked()
	workerDone := i.workerDone
	i.mu.Unlock()
	// Never hold mu while waiting. Cancellation is not proof that the actual
	// driver call has exited, and Release must not race an active Ping.
	if workerDone != nil {
		<-workerDone
	}
	i.mu.Lock()
	conn := i.conn
	i.conn = nil
	i.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if conn != nil {
		if !conn.Conn().IsClosed() {
			if wasLost {
				// A check may fail before doing I/O, leaving a live session with
				// its advisory lock. Never return that session to the pool.
				_ = conn.Conn().Close(ctx)
			} else if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", i.key); err != nil {
				_ = conn.Conn().Close(ctx)
			}
		}
		conn.Release()
	}
	close(i.closeDone)
}

// DrainPredecessor waits for durable request deadlines of the old process.
// After losing its lock the old process may have an already admitted network call;
// the new owner cannot accept calls until those local execution windows expire.
func (i *Instance) DrainPredecessor(ctx context.Context, pool *pgxpool.Pool) error {
	for {
		if err := i.Check(ctx); err != nil {
			return err
		}
		var milliseconds int64
		err := pool.QueryRow(ctx, `SELECT coalesce(ceil(extract(epoch FROM (max(r.request_deadline)+interval '5 seconds'-clock_timestamp()))*1000),0)::bigint FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE v.status IN ('reserved','pending_reconciliation') AND r.request_deadline+interval '5 seconds'>clock_timestamp()`).Scan(&milliseconds)
		if err != nil {
			return err
		}
		if milliseconds <= 0 {
			return nil
		}
		wait := time.Duration(milliseconds) * time.Millisecond
		if wait > time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
