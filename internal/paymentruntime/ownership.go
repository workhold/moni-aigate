package paymentruntime

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ownerCheck struct {
	done     chan struct{}
	deadline time.Time
	err      error
}
type ownership struct {
	mu                     sync.Mutex
	conn                   *pgxpool.Conn
	namespace, key         int32
	lost, closing, running bool
	pending                []*ownerCheck
	workerDone             chan struct{}
	pingCancel             context.CancelFunc
	onLost                 func()
}

func acquireOwner(ctx context.Context, p *pgxpool.Pool, c Config, onLost func()) (*ownership, error) {
	conn, e := p.Acquire(ctx)
	if e != nil {
		return nil, ErrOwnershipLost
	}
	namespace := int32(782193403)
	if c.manifest.Component == Receiver {
		namespace = 782193404
	}
	key := int32(ownPin(c.manifest).OID)
	var held bool
	e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1::integer,$2::integer)", namespace, key).Scan(&held)
	if e != nil || !held {
		if e != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = conn.Conn().Close(cleanup)
			cancel()
		}
		conn.Release()
		return nil, ErrOwnershipLost
	}
	return &ownership{conn: conn, namespace: namespace, key: key, onLost: onLost}, nil
}
func (o *ownership) fenceLocked() {
	if o.lost {
		return
	}
	o.lost = true
	if o.pingCancel != nil {
		o.pingCancel()
	}
	for _, w := range o.pending {
		w.err = ErrOwnershipLost
		close(w.done)
	}
	o.pending = nil
	if o.onLost != nil {
		o.onLost()
	}
}
func (o *ownership) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	o.mu.Lock()
	if o.lost || o.closing || o.conn == nil {
		o.mu.Unlock()
		return ErrOwnershipLost
	}
	w := &ownerCheck{done: make(chan struct{}), deadline: time.Now().Add(time.Second)}
	o.pending = append(o.pending, w)
	if !o.running {
		o.running = true
		o.workerDone = make(chan struct{})
		go o.runChecks(o.workerDone)
	}
	o.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.done:
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return w.err
	}
}
func (o *ownership) runChecks(done chan struct{}) {
	defer close(done)
	for {
		o.mu.Lock()
		if o.lost || o.closing || len(o.pending) == 0 {
			o.running = false
			o.mu.Unlock()
			return
		}
		batch := o.pending
		o.pending = nil
		deadline := batch[0].deadline
		for _, w := range batch {
			if w.deadline.Before(deadline) {
				deadline = w.deadline
			}
		}
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		o.pingCancel = cancel
		conn := o.conn
		o.mu.Unlock()
		// A batch is immutable before Ping starts. Later registrations wait for a
		// fresh Ping; cancellation of one HTTP caller never cancels this check.
		e := conn.Ping(ctx)
		cancel()
		o.mu.Lock()
		o.pingCancel = nil
		if e != nil {
			o.fenceLocked()
		}
		for _, w := range batch {
			if o.lost || o.closing {
				w.err = ErrOwnershipLost
			}
			close(w.done)
		}
		o.mu.Unlock()
	}
}
func (o *ownership) close() error {
	o.mu.Lock()
	o.closing = true
	// An ordinary close drains the already bounded Ping instead of cancelling
	// it: pgx cancellation can close the lock connection and look like a loss.
	for _, w := range o.pending {
		w.err = ErrOwnershipLost
		close(w.done)
	}
	o.pending = nil
	done := o.workerDone
	wasLost := o.lost
	o.mu.Unlock()
	if done != nil {
		<-done
	}
	o.mu.Lock()
	conn := o.conn
	o.conn = nil
	o.mu.Unlock()
	if conn == nil {
		return nil
	}
	defer conn.Release()
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if wasLost {
		_ = conn.Conn().Close(cleanup)
		return nil
	}
	var unlocked bool
	e := conn.QueryRow(cleanup, "SELECT pg_advisory_unlock($1::integer,$2::integer)", o.namespace, o.key).Scan(&unlocked)
	if e != nil || !unlocked {
		_ = conn.Conn().Close(cleanup)
		return ErrOwnershipLost
	}
	return nil
}
