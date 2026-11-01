package server

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type commerceBucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

type commerceAdmission struct {
	mu      sync.Mutex
	global  *rate.Limiter
	buckets map[string]*commerceBucket
	active  int
}

func (s *Server) admitCommerce(c *gin.Context, tenant string) (func(), bool) {
	a := &s.commerce
	a.mu.Lock()
	if a.global == nil {
		a.global = rate.NewLimiter(20, 40)
		a.buckets = map[string]*commerceBucket{}
	}
	now := time.Now()
	key, perSecond, burst := "platform:"+sessionOf(c).User.ID, rate.Limit(1), 2
	if tenant != "" {
		key, perSecond, burst = "wallet:"+sessionOf(c).User.ID+":"+tenant, 2, 4
	}
	bucket := a.buckets[key]
	if bucket == nil && len(a.buckets) >= 4096 {
		for id, existing := range a.buckets {
			if now.Sub(existing.seen) > 10*time.Minute {
				delete(a.buckets, id)
			}
		}
	}
	allowed := a.active < 4 && (bucket != nil || len(a.buckets) < 4096)
	if allowed && bucket == nil {
		bucket = &commerceBucket{limiter: rate.NewLimiter(perSecond, burst)}
		a.buckets[key] = bucket
	}
	if allowed {
		bucket.seen = now
		allowed = a.global.AllowN(now, 1) && bucket.limiter.AllowN(now, 1)
	}
	if !allowed {
		a.mu.Unlock()
		c.Header("Retry-After", "1")
		s.fail(c, 429, "rate_limited", "操作过于频繁，请稍后查询或重试")
		return nil, false
	}
	a.active++
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	c.Request = c.Request.WithContext(ctx)
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			a.mu.Lock()
			a.active--
			a.mu.Unlock()
		})
	}, true
}
