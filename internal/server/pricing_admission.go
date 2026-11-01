package server

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type pricingBucket struct {
	limiter *rate.Limiter
	seen    time.Time
	active  int
}
type pricingGate struct {
	limiter *rate.Limiter
	active  int
}
type pricingAdmission struct {
	mu      sync.Mutex
	buckets map[string]*pricingBucket
	gates   map[string]*pricingGate
}
type pricingAdmissionSpec struct {
	globalRate                rate.Limit
	globalBurst, globalActive int
	actorRate                 rate.Limit
	actorBurst, actorActive   int
	timeout                   time.Duration
}

func pricingSpec(kind string) pricingAdmissionSpec {
	switch kind {
	case "write":
		return pricingAdmissionSpec{10, 10, 4, 2, 2, 1, 8 * time.Second}
	case "preview":
		return pricingAdmissionSpec{4, 4, 2, 1, 1, 1, 4 * time.Second}
	case "own":
		return pricingAdmissionSpec{50, 50, 12, 5, 5, 2, 3 * time.Second}
	default:
		return pricingAdmissionSpec{30, 30, 8, 5, 5, 2, 4 * time.Second}
	}
}
func (s *Server) admitPricing(c *gin.Context, kind, tenant string) (func(), bool) {
	started := time.Now()
	a := &s.pricing
	spec := pricingSpec(kind)
	a.mu.Lock()
	if a.buckets == nil {
		a.buckets = map[string]*pricingBucket{}
		a.gates = map[string]*pricingGate{}
	}
	// All identity entries, including tenant budgets, share a bounded map. Active
	// entries cannot be evicted to replenish rate or concurrency allowances.
	for k, v := range a.buckets {
		if v.active == 0 && started.Sub(v.seen) > 10*time.Minute {
			delete(a.buckets, k)
		}
	}
	actor := sessionOf(c).User.ID
	var buckets []*pricingBucket
	var gates []*pricingGate
	acquire := func(mode string, sp pricingAdmissionSpec) int {
		gate := a.gates[mode]
		if gate == nil {
			gate = &pricingGate{limiter: rate.NewLimiter(sp.globalRate, sp.globalBurst)}
			a.gates[mode] = gate
		}
		if gate.active >= sp.globalActive {
			return 429
		}
		keys := []string{mode + ":actor:" + actor}
		if mode == "own" {
			keys = append(keys, mode+":tenant:"+tenant)
		}
		local := make([]*pricingBucket, 0, len(keys))
		missing := 0
		for _, k := range keys {
			if a.buckets[k] == nil {
				missing++
			}
		}
		if len(a.buckets)+missing > 4096 {
			return 503
		}
		for i, k := range keys {
			b := a.buckets[k]
			maxActive := sp.actorActive
			if mode == "own" && i == 1 {
				maxActive = 4
			}
			if b != nil && b.active >= maxActive {
				return 429
			}
		}
		for i, k := range keys {
			b := a.buckets[k]
			if b == nil {
				r, burst := sp.actorRate, sp.actorBurst
				if mode == "own" && i == 1 {
					r, burst = 10, 10
				}
				b = &pricingBucket{limiter: rate.NewLimiter(r, burst)}
				a.buckets[k] = b
			}
			b.seen = started
			local = append(local, b)
		}
		if !gate.limiter.AllowN(started, 1) {
			return 429
		}
		for _, b := range local {
			if !b.limiter.AllowN(started, 1) {
				return 429
			}
		}
		gate.active++
		gates = append(gates, gate)
		for _, b := range local {
			b.active++
			buckets = append(buckets, b)
		}
		return 0
	}
	status := 0
	if kind == "preview" {
		status = acquire("read", pricingSpec("read"))
	}
	if status == 0 {
		status = acquire(kind, spec)
	}
	if status != 0 {
		for _, g := range gates {
			g.active--
		}
		for _, b := range buckets {
			b.active--
		}
		a.mu.Unlock()
		if status == 429 {
			c.Header("Retry-After", "1")
		}
		s.fail(c, status, map[int]string{429: "rate_limited", 503: "pricing_busy"}[status], "定价服务繁忙，请稍后查询")
		return nil, false
	}
	a.mu.Unlock()
	ctx, cancel := context.WithDeadline(c.Request.Context(), started.Add(spec.timeout))
	c.Request = c.Request.WithContext(ctx)
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			a.mu.Lock()
			for _, g := range gates {
				g.active--
			}
			for _, b := range buckets {
				b.active--
			}
			a.mu.Unlock()
		})
	}, true
}
