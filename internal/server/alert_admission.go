package server

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type alertBucket struct {
	read, write   *rate.Limiter
	reads, writes int
	seen          time.Time
}
type alertAdmission struct {
	mu            sync.Mutex
	actors        map[string]*alertBucket
	reads, writes int
}

func (s *Server) admitAlert(c *gin.Context, write bool) (func(), bool) {
	start := time.Now()
	a := &s.alertAdmission
	a.mu.Lock()
	if a.actors == nil {
		a.actors = make(map[string]*alertBucket)
	}
	for k, b := range a.actors {
		if b.reads == 0 && b.writes == 0 && start.Sub(b.seen) > 10*time.Minute {
			delete(a.actors, k)
		}
	}
	id := sessionOf(c).User.ID
	b := a.actors[id]
	if b == nil && len(a.actors) >= 4096 {
		a.mu.Unlock()
		s.alertFailure(c, 503, "alert_unavailable")
		return nil, false
	}
	if b == nil {
		b = &alertBucket{read: rate.NewLimiter(1, 20), write: rate.NewLimiter(rate.Every(3*time.Second), 5)}
		a.actors[id] = b
	}
	b.seen = start
	limiter, timeout := b.read, 2*time.Second
	full := a.reads >= 32 || b.reads >= 4
	if write {
		limiter, timeout = b.write, 3*time.Second
		full = a.writes >= 8 || b.writes >= 2
	}
	if full || !limiter.AllowN(start, 1) {
		a.mu.Unlock()
		c.Header("Retry-After", "1")
		s.alertFailure(c, 429, "alert_rate_limited")
		return nil, false
	}
	if write {
		a.writes++
		b.writes++
	} else {
		a.reads++
		b.reads++
	}
	a.mu.Unlock()
	ctx, cancel := context.WithDeadline(c.Request.Context(), start.Add(timeout))
	c.Request = c.Request.WithContext(ctx)
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			a.mu.Lock()
			defer a.mu.Unlock()
			if write {
				a.writes--
				b.writes--
			} else {
				a.reads--
				b.reads--
			}
		})
	}, true
}
