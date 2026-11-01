package server

import (
	"context"
	"errors"
	"sync"
)

var (
	errAdmissionQueueFull = errors.New("key_admission_queue_full")
	errAdmissionQueueKey  = errors.New("invalid_key_admission_key")
)

const admissionQueueLimit = 16

// admissionQueue schedules short local admission and completion phases. Callers
// already hold runtime permits and must defer release before database work.
// The zero value is ready for use; lanes retain no credentials or request data.
type admissionQueue struct {
	mu    sync.Mutex
	lanes map[string]*admissionLane
}

type admissionLane struct {
	// The first ticket owns admission. Later tickets wait in registration order.
	tickets []*admissionTicket
}

type admissionTicket struct {
	ready chan struct{}
}

func (q *admissionQueue) acquire(ctx context.Context, key string) (func(), error) {
	if key == "" {
		return nil, errAdmissionQueueKey
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.mu.Lock()
	if err := ctx.Err(); err != nil {
		q.mu.Unlock()
		return nil, err
	}
	if q.lanes == nil {
		q.lanes = make(map[string]*admissionLane)
	}
	lane := q.lanes[key]
	if lane == nil {
		lane = &admissionLane{}
		q.lanes[key] = lane
	}
	if len(lane.tickets) >= admissionQueueLimit {
		q.mu.Unlock()
		return nil, errAdmissionQueueFull
	}
	ticket := &admissionTicket{ready: make(chan struct{})}
	lane.tickets = append(lane.tickets, ticket)
	if len(lane.tickets) == 1 {
		close(ticket.ready)
	}
	q.mu.Unlock()

	select {
	case <-ctx.Done():
		q.remove(key, lane, ticket)
		return nil, ctx.Err()
	case <-ticket.ready:
		// Cancellation can race with the previous owner's handoff. Return the
		// grant immediately if cancellation won before this final check.
		if err := ctx.Err(); err != nil {
			q.remove(key, lane, ticket)
			return nil, err
		}
		return func() { q.remove(key, lane, ticket) }, nil
	}
}

func (q *admissionQueue) remove(key string, lane *admissionLane, ticket *admissionTicket) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// A late/repeated release must not affect a new lane for the same Key.
	if q.lanes[key] != lane {
		return
	}
	for i, current := range lane.tickets {
		if current != ticket {
			continue
		}
		copy(lane.tickets[i:], lane.tickets[i+1:])
		lane.tickets[len(lane.tickets)-1] = nil
		lane.tickets = lane.tickets[:len(lane.tickets)-1]
		if len(lane.tickets) == 0 {
			delete(q.lanes, key)
		} else if i == 0 {
			close(lane.tickets[0].ready)
		}
		return
	}
}
