package server

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type periodicBudgetAdmissionKind uint8

const (
	periodicBudgetRead periodicBudgetAdmissionKind = iota
	periodicBudgetOperation
	periodicBudgetWrite
)

type periodicBudgetActor struct {
	read, operation, write    *rate.Limiter
	reads, operations, writes int
	seen                      time.Time
}

// All read operations share the total read counters. Operation lookups have
// additional counters and their own rate bucket, so they cannot exhaust or
// borrow the ordinary-read rate allowance.
type periodicBudgetAdmission struct {
	mu                        sync.Mutex
	actors                    map[string]*periodicBudgetActor
	reads, operations, writes int
}

func (a *periodicBudgetAdmission) acquire(actor string, kind periodicBudgetAdmissionKind, now time.Time) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if kind > periodicBudgetWrite || actor == "" {
		return nil, false
	}
	if a.actors == nil {
		a.actors = make(map[string]*periodicBudgetActor)
	}
	for id, v := range a.actors {
		if v.reads == 0 && v.writes == 0 && now.Sub(v.seen) >= 10*time.Minute {
			delete(a.actors, id)
		}
	}
	v := a.actors[actor]
	if v == nil {
		if len(a.actors) >= 4096 {
			return nil, false
		}
		v = &periodicBudgetActor{read: rate.NewLimiter(5, 10), operation: rate.NewLimiter(2, 4), write: rate.NewLimiter(1, 2), seen: now}
		a.actors[actor] = v
	}
	// A later sampler must not add rate tokens after an earlier observation.
	if now.Before(v.seen) {
		now = v.seen
	}
	v.seen = now
	limiter := v.read
	switch kind {
	case periodicBudgetWrite:
		if a.writes >= 8 || v.writes >= 1 {
			return nil, false
		}
		limiter = v.write
	case periodicBudgetOperation:
		if a.reads >= 32 || v.reads >= 4 || a.operations >= 8 || v.operations >= 1 {
			return nil, false
		}
		limiter = v.operation
	default:
		if a.reads >= 32 || v.reads >= 4 {
			return nil, false
		}
	}
	if !limiter.AllowN(now, 1) {
		return nil, false
	}
	if kind == periodicBudgetWrite {
		a.writes++
		v.writes++
	} else {
		a.reads++
		v.reads++
		if kind == periodicBudgetOperation {
			a.operations++
			v.operations++
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			if kind == periodicBudgetWrite {
				a.writes--
				v.writes--
			} else {
				a.reads--
				v.reads--
				if kind == periodicBudgetOperation {
					a.operations--
					v.operations--
				}
			}
		})
	}, true
}

func (s *Server) admitPeriodicBudget(c *gin.Context, kind periodicBudgetAdmissionKind) (func(), bool) {
	started := time.Now()
	release, ok := s.periodicAdmission.acquire(sessionOf(c).User.ID, kind, started)
	if !ok {
		c.Header("Retry-After", "1")
		s.periodicBudgetFailure(c, 429, "rate_limited")
		return nil, false
	}
	duration := 5 * time.Second
	if kind == periodicBudgetWrite {
		duration = 10 * time.Second
	}
	ctx, cancel := context.WithDeadline(c.Request.Context(), started.Add(duration))
	c.Request = c.Request.WithContext(ctx)
	var once sync.Once
	done := func() { once.Do(func() { cancel(); release() }) }
	deadline, _ := ctx.Deadline()
	if ctx.Err() != nil || !s.ready.Load() {
		done()
		s.periodicBudgetFailure(c, 503, "instance_unavailable")
		return nil, false
	}
	if time.Until(deadline) < time.Second {
		done()
		s.periodicBudgetFailure(c, 503, "periodic_budget_unavailable")
		return nil, false
	}
	// Guard owns an independent one-second bound. Do not pass a cancelled
	// customer context into the permanent ownership fence.
	if s.guardContext(ctx) != nil || !s.ready.Load() {
		done()
		s.periodicBudgetFailure(c, 503, "instance_unavailable")
		return nil, false
	}
	if ctx.Err() != nil {
		done()
		s.periodicBudgetFailure(c, 503, "periodic_budget_unavailable")
		return nil, false
	}
	return done, true
}
