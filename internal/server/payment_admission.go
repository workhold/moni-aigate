package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type paymentAdmissionBucket struct {
	limiter *rate.Limiter
	seen    time.Time
}
type paymentAdmission struct {
	mu      sync.Mutex
	global  *rate.Limiter
	buckets map[string]*paymentAdmissionBucket
	active  int
}
type paymentNotificationAdmission struct {
	mu     sync.Mutex
	global *rate.Limiter
	active int
}

func (s *Server) admitPayment(c *gin.Context, tenant string) (func(), bool) {
	a := &s.paymentAdmission
	a.mu.Lock()
	if a.global == nil {
		a.global = rate.NewLimiter(10, 20)
	}
	if a.buckets == nil {
		a.buckets = map[string]*paymentAdmissionBucket{}
	}
	now := time.Now()
	scope := "platform"
	if tenant != "" {
		scope = "tenant:" + tenant
	}
	key := sessionOf(c).User.ID + "\x00" + scope
	bucket := a.buckets[key]
	if bucket == nil && len(a.buckets) >= 4096 {
		for id, b := range a.buckets {
			if now.Sub(b.seen) > 10*time.Minute {
				delete(a.buckets, id)
			}
		}
	}
	allowed := a.active < 4 && (bucket != nil || len(a.buckets) < 4096)
	if allowed && bucket == nil {
		bucket = &paymentAdmissionBucket{limiter: rate.NewLimiter(1, 2)}
		a.buckets[key] = bucket
	}
	if allowed {
		bucket.seen = now
		allowed = a.global.AllowN(now, 1) && bucket.limiter.AllowN(now, 1)
	}
	if !allowed {
		a.mu.Unlock()
		s.paymentRateLimited(c)
		return nil, false
	}
	a.active++
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	c.Request = c.Request.WithContext(ctx)
	clearDeadline := paymentTransportDeadline(c, ctx)
	var once sync.Once
	return func() { once.Do(func() { clearDeadline(); cancel(); a.mu.Lock(); a.active--; a.mu.Unlock() }) }, true
}
func (s *Server) admitPaymentNotification(c *gin.Context) (func(), bool) {
	a := &s.paymentNotifyAdmission
	a.mu.Lock()
	if a.global == nil {
		a.global = rate.NewLimiter(10, 20)
	}
	allowed := a.active < 4 && a.global.Allow()
	if !allowed {
		a.mu.Unlock()
		s.paymentRateLimited(c)
		return nil, false
	}
	a.active++
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	c.Request = c.Request.WithContext(ctx)
	clearDeadline := paymentTransportDeadline(c, ctx)
	var once sync.Once
	return func() { once.Do(func() { clearDeadline(); cancel(); a.mu.Lock(); a.active--; a.mu.Unlock() }) }, true
}
func (s *Server) paymentRateLimited(c *gin.Context) {
	c.Header("Retry-After", "1")
	s.paymentFailure(c, 429, "rate_limited")
}

// Context alone cannot interrupt a stalled HTTP body read. The native server
// supports transport deadlines; unit recorders may not implement them. Clear
// both before a keep-alive connection handles its next request.
func paymentTransportDeadline(c *gin.Context, ctx context.Context) func() {
	controller := http.NewResponseController(c.Writer)
	deadline, _ := ctx.Deadline()
	readSet := controller.SetReadDeadline(deadline) == nil
	writeSet := controller.SetWriteDeadline(deadline) == nil
	return func() {
		if readSet {
			_ = controller.SetReadDeadline(time.Time{})
		}
		if writeSet {
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}
}
