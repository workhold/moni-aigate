package server

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"golang.org/x/time/rate"
)

const keyRuntimeCapacity = 4096

type keyRuntimeState struct {
	policy       identity.KeyLimitsView
	limiter      *rate.Limiter
	lastObserved time.Time
}

type runtimeLimitError struct {
	status int
	code   string
}

func (e *runtimeLimitError) Error() string { return e.code }

// The identity transaction holds the Key's SHARE lock through this callback,
// synchronizing with configuration UPDATE. No database operation runs under mu.
// A failed/unknown database commit cannot let this request proceed to Reserve.
func (s *Server) acquireKeyRuntime(ctx context.Context, p identity.Principal) (func(), error) {
	return s.acquireKeyRuntimeUsing(ctx, p, s.Identity.WithKeyRuntimeLimits)
}

func (s *Server) acquireKeyRuntimeUsing(ctx context.Context, p identity.Principal, withLimits func(context.Context, identity.Principal, func(identity.KeyLimitsView) error) error) (func(), error) {
	var release func()
	err := withLimits(ctx, p, func(policy identity.KeyLimitsView) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		release, err = s.acquireKeyRuntimeAt(p, policy, time.Now())
		return err
	})
	if err != nil {
		if release != nil {
			release()
		}
		return nil, err
	}
	return release, nil
}

// acquireKeyRuntimeAt is a single atomic local admission. now is explicit for
// deterministic clock tests; production always supplies the observation time.
func (s *Server) acquireKeyRuntimeAt(p identity.Principal, policy identity.KeyLimitsView, now time.Time) (func(), error) {
	invalid := func() (func(), error) { return nil, &runtimeLimitError{503, "limiter_unavailable"} }
	revision, err := strconv.ParseInt(policy.LimitRevision, 10, 64)
	if err != nil || revision <= 0 || strconv.FormatInt(revision, 10) != policy.LimitRevision || policy.ID != p.KeyID || p.KeyID == "" || p.TenantID == "" || policy.RPMLimit < 1 || policy.RPMLimit > 6000 || policy.BurstLimit < 1 || policy.BurstLimit > 200 || policy.ConcurrencyLimit < 1 || policy.ConcurrencyLimit > 16 {
		return invalid()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keyRuntime == nil {
		s.keyRuntime = make(map[string]*keyRuntimeState)
	}
	state := s.keyRuntime[p.KeyID]
	if state == nil {
		// No idle TTL: at 1 RPM a 200-token bucket can take 200 minutes
		// to fill. Dropping its debt or revision would silently grant a burst.
		if len(s.keyRuntime) >= keyRuntimeCapacity {
			return invalid()
		}
		state = &keyRuntimeState{policy: policy, limiter: rate.NewLimiter(rate.Limit(float64(policy.RPMLimit)/60), policy.BurstLimit), lastObserved: now}
		s.keyRuntime[p.KeyID] = state
	} else {
		previous, err := strconv.ParseInt(state.policy.LimitRevision, 10, 64)
		if err != nil || revision < previous || revision == previous && state.policy != policy {
			return invalid()
		}
		// Callers can sample time before waiting for mu. Never let a delayed
		// observation rewind rate.Limiter's clock and accrue an interval twice.
		if now.Before(state.lastObserved) {
			now = state.lastObserved
		}
		state.lastObserved = now
		if revision > previous {
			// Both mutations use the same timestamp. Advance under the old
			// rate/capacity first; increasing either never creates token credit.
			state.limiter.SetLimitAt(now, rate.Limit(float64(policy.RPMLimit)/60))
			state.limiter.SetBurstAt(now, policy.BurstLimit)
			state.policy = policy
		}
	}
	if s.tenantPermits[p.TenantID] >= 32 || s.keyPermits[p.KeyID] >= policy.ConcurrencyLimit || len(s.global) >= cap(s.global) {
		return nil, &runtimeLimitError{429, "concurrency_limited"}
	}
	select {
	case s.global <- struct{}{}:
		// Probe admission uses the same channel without mu. Reserve a global
		// slot before consuming a token so even that capacity race is free.
		if !state.limiter.AllowN(now, 1) {
			<-s.global
			return nil, &runtimeLimitError{429, "rate_limited"}
		}
		if s.tenantPermits == nil {
			s.tenantPermits = make(map[string]int)
		}
		if s.keyPermits == nil {
			s.keyPermits = make(map[string]int)
		}
		s.tenantPermits[p.TenantID]++
		s.keyPermits[p.KeyID]++
		var once sync.Once
		return func() { once.Do(func() { s.releasePermit(p) }) }, nil
	default:
		return nil, &runtimeLimitError{429, "concurrency_limited"}
	}
}

func (s *Server) runtimeLimitFailure(c *gin.Context, err error) bool {
	var limited *runtimeLimitError
	if !errors.As(err, &limited) {
		return false
	}
	message := "请求过于频繁，请稍后重试"
	if limited.code == "concurrency_limited" {
		message = "当前并发已满，请稍后重试"
	} else if limited.status == 503 {
		message = "限流服务暂不可用"
	}
	s.fail(c, limited.status, limited.code, message)
	return true
}
