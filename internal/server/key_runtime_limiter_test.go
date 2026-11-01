package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/identity"
)

func runtimeFixture() (*Server, identity.Principal, identity.KeyLimitsView, time.Time) {
	return &Server{global: make(chan struct{}, 100)}, identity.Principal{TenantID: "tenant", UserID: "owner", KeyID: "key"}, identity.KeyLimitsView{ID: "key", RPMLimit: 60, BurstLimit: 20, ConcurrencyLimit: 16, LimitRevision: "1"}, time.Unix(1700000000, 0)
}
func runtimeTake(t *testing.T, s *Server, p identity.Principal, v identity.KeyLimitsView, now time.Time) func() {
	t.Helper()
	release, err := s.acquireKeyRuntimeAt(p, v, now)
	if err != nil || release == nil {
		t.Fatal("admission failed", err)
	}
	return release
}
func runtimeDenied(t *testing.T, s *Server, p identity.Principal, v identity.KeyLimitsView, now time.Time, status int, code string) {
	t.Helper()
	release, err := s.acquireKeyRuntimeAt(p, v, now)
	var e *runtimeLimitError
	if release != nil || !errors.As(err, &e) || e.status != status || e.code != code {
		t.Fatal("unexpected admission", err)
	}
}
func runtimeEmpty(t *testing.T, s *Server) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.global) != 0 || len(s.keyPermits) != 0 || len(s.tenantPermits) != 0 {
		t.Fatal("stranded runtime permits")
	}
}
func TestKeyRuntimeBucketPreservesCreditAcrossConfiguration(t *testing.T) {
	s, p, v, now := runtimeFixture()
	for range 20 {
		runtimeTake(t, s, p, v, now)()
	}
	runtimeDenied(t, s, p, v, now, 429, "rate_limited")
	bucket := s.keyRuntime[p.KeyID].limiter
	// Half a second accrued at the OLD 1 token/sec. Raising to 10/sec
	// and a 200-token burst must not manufacture 5 or 200 fresh tokens.
	v.RPMLimit, v.BurstLimit, v.LimitRevision = 600, 200, "2"
	runtimeDenied(t, s, p, v, now.Add(500*time.Millisecond), 429, "rate_limited")
	if s.keyRuntime[p.KeyID].limiter != bucket {
		t.Fatal("configuration replaced the bucket")
	}
	runtimeTake(t, s, p, v, now.Add(550*time.Millisecond))()
	runtimeDenied(t, s, p, v, now.Add(550*time.Millisecond), 429, "rate_limited")
	// At 10/sec, another half second creates five tokens. Lowering both
	// rate and capacity truncates that balance to two, without a refill.
	v.RPMLimit, v.BurstLimit, v.LimitRevision = 1, 2, "3"
	at := now.Add(1050 * time.Millisecond)
	for range 2 {
		runtimeTake(t, s, p, v, at)()
	}
	runtimeDenied(t, s, p, v, at, 429, "rate_limited")
	runtimeDenied(t, s, p, v, at.Add(59*time.Second), 429, "rate_limited")
	runtimeTake(t, s, p, v, at.Add(time.Minute))()
	runtimeEmpty(t, s)
}
func TestKeyRuntimeOutOfOrderObservationsCannotDuplicateCredit(t *testing.T) {
	s, p, v, now := runtimeFixture()
	v.BurstLimit = 2
	for range 2 {
		runtimeTake(t, s, p, v, now)()
	}
	// A later-sampled request can win mu ahead of an earlier-sampled one.
	// The two seconds earned exactly two tokens, regardless of lock order.
	runtimeTake(t, s, p, v, now.Add(2*time.Second))()
	runtimeTake(t, s, p, v, now.Add(time.Second))()
	runtimeDenied(t, s, p, v, now.Add(2*time.Second), 429, "rate_limited")
	// A configuration update with an older sample must not rewind either.
	v.RPMLimit, v.BurstLimit, v.LimitRevision = 600, 200, "2"
	runtimeDenied(t, s, p, v, now.Add(time.Second), 429, "rate_limited")
	runtimeDenied(t, s, p, v, now.Add(2*time.Second), 429, "rate_limited")
	runtimeTake(t, s, p, v, now.Add(2100*time.Millisecond))()
	runtimeDenied(t, s, p, v, now.Add(2100*time.Millisecond), 429, "rate_limited")
	runtimeEmpty(t, s)
}
func TestKeyRuntimeRevisionAndIdentityFailClosed(t *testing.T) {
	s, p, v, now := runtimeFixture()
	runtimeTake(t, s, p, v, now)()
	latest := v
	latest.LimitRevision = "9223372036854775807"
	latest.BurstLimit = 2
	runtimeTake(t, s, p, latest, now)()
	runtimeDenied(t, s, p, v, now, 503, "limiter_unavailable")
	corrupt := latest
	corrupt.RPMLimit++
	runtimeDenied(t, s, p, corrupt, now, 503, "limiter_unavailable")
	for _, revision := range []string{"", "0", "01", "9223372036854775808", "-1"} {
		bad := latest
		bad.LimitRevision = revision
		runtimeDenied(t, s, p, bad, now, 503, "limiter_unavailable")
	}
	bad := latest
	bad.ID = "another"
	runtimeDenied(t, s, p, bad, now, 503, "limiter_unavailable")
	for _, bad := range []identity.KeyLimitsView{
		{ID: p.KeyID, RPMLimit: 0, BurstLimit: 20, ConcurrencyLimit: 16, LimitRevision: "1"},
		{ID: p.KeyID, RPMLimit: 6001, BurstLimit: 20, ConcurrencyLimit: 16, LimitRevision: "1"},
		{ID: p.KeyID, RPMLimit: 60, BurstLimit: 201, ConcurrencyLimit: 16, LimitRevision: "1"},
		{ID: p.KeyID, RPMLimit: 60, BurstLimit: 20, ConcurrencyLimit: 17, LimitRevision: "1"},
	} {
		runtimeDenied(t, s, p, bad, now, 503, "limiter_unavailable")
	}
	if s.keyRuntime[p.KeyID].policy != latest {
		t.Fatal("stale caller rolled back observed policy")
	}
	runtimeEmpty(t, s)
}
func TestKeyRuntimeConcurrencyTighteningKeepsLeasesAndTokens(t *testing.T) {
	s, p, v, now := runtimeFixture()
	v.ConcurrencyLimit = 3
	releases := []func(){runtimeTake(t, s, p, v, now), runtimeTake(t, s, p, v, now), runtimeTake(t, s, p, v, now)}
	v.ConcurrencyLimit, v.LimitRevision = 1, "2"
	runtimeDenied(t, s, p, v, now, 429, "concurrency_limited")
	if s.keyPermits[p.KeyID] != 3 || len(s.global) != 3 {
		t.Fatal("configuration cleared in-flight count")
	}
	releases[0]()
	releases[1]()
	runtimeDenied(t, s, p, v, now, 429, "concurrency_limited")
	releases[2]()
	runtimeTake(t, s, p, v, now)()
	for _, release := range releases {
		release()
	}
	// Only four grants consumed tokens; denied concurrency checks consumed none.
	v.ConcurrencyLimit, v.LimitRevision = 16, "3"
	for range 16 {
		runtimeTake(t, s, p, v, now)()
	}
	runtimeDenied(t, s, p, v, now, 429, "rate_limited")
	runtimeEmpty(t, s)
}
func TestKeyRuntimeAtomicConcurrentAdmissionAndOnceRelease(t *testing.T) {
	s, p, v, now := runtimeFixture()
	v.BurstLimit = 10
	v.ConcurrencyLimit = 4
	start := make(chan struct{})
	results := make(chan func(), 64)
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := s.acquireKeyRuntimeAt(p, v, now)
			if err == nil {
				results <- release
			} else {
				var e *runtimeLimitError
				if !errors.As(err, &e) || e.code != "concurrency_limited" {
					t.Error(err)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var grants []func()
	for release := range results {
		grants = append(grants, release)
	}
	if len(grants) != 4 || s.keyPermits[p.KeyID] != 4 {
		t.Fatal("concurrent admission exceeded limit", len(grants))
	}
	for _, release := range grants {
		for range 4 {
			wg.Add(1)
			go func(release func()) { defer wg.Done(); release() }(release)
		}
	}
	wg.Wait()
	runtimeEmpty(t, s)
	for range 6 {
		runtimeTake(t, s, p, v, now)()
	}
	runtimeDenied(t, s, p, v, now, 429, "rate_limited")
	runtimeEmpty(t, s)
}
func TestKeyRuntimeGlobalAndTenantDenialsDoNotSpendKeyCredit(t *testing.T) {
	for _, boundary := range []string{"global", "tenant"} {
		t.Run(boundary, func(t *testing.T) {
			s, p, v, now := runtimeFixture()
			v.BurstLimit = 1
			if boundary == "global" {
				s.global = make(chan struct{}, 1)
				s.global <- struct{}{}
			} else {
				s.tenantPermits = map[string]int{p.TenantID: 32}
			}
			runtimeDenied(t, s, p, v, now, 429, "concurrency_limited")
			if boundary == "global" {
				<-s.global
			} else {
				delete(s.tenantPermits, p.TenantID)
			}
			runtimeTake(t, s, p, v, now)()
			runtimeDenied(t, s, p, v, now, 429, "rate_limited")
			runtimeEmpty(t, s)
		})
	}
}
func TestKeyRuntimeCapacityRetainsDebtAndExistingKeys(t *testing.T) {
	s, p, v, now := runtimeFixture()
	v.BurstLimit = 1
	for i := range keyRuntimeCapacity {
		p.KeyID = fmt.Sprint(i)
		v.ID = p.KeyID
		runtimeTake(t, s, p, v, now)()
	}
	p.KeyID = "new"
	v.ID = p.KeyID
	runtimeDenied(t, s, p, v, now.Add(24*time.Hour), 503, "limiter_unavailable")
	p.KeyID = "0"
	v.ID = p.KeyID
	runtimeTake(t, s, p, v, now.Add(time.Second))()
	if len(s.keyRuntime) != keyRuntimeCapacity {
		t.Fatal("runtime state was evicted")
	}
	runtimeEmpty(t, s)

	s, p, v, now = runtimeFixture()
	v.RPMLimit = 1
	v.BurstLimit = 200
	for range 200 {
		runtimeTake(t, s, p, v, now)()
	}
	// Idle for > the generic limiter's ten-minute TTL: exactly 11 tokens,
	// never a new 200-token burst, including after an observed capacity edit.
	v.LimitRevision = "2"
	for range 11 {
		runtimeTake(t, s, p, v, now.Add(11*time.Minute))()
	}
	runtimeDenied(t, s, p, v, now.Add(11*time.Minute), 429, "rate_limited")
	runtimeEmpty(t, s)
	restarted, _, _, _ := runtimeFixture()
	for range 200 {
		runtimeTake(t, restarted, p, v, now.Add(11*time.Minute))()
	}
	runtimeDenied(t, restarted, p, v, now.Add(11*time.Minute), 429, "rate_limited")
	runtimeEmpty(t, restarted)
}
func TestKeyRuntimeCommitFailureReleasesOnlyPermit(t *testing.T) {
	s, p, v, _ := runtimeFixture()
	v.BurstLimit = 1
	failure := errors.New("synthetic failed or unknown commit")
	release, err := s.acquireKeyRuntimeUsing(context.Background(), p, func(ctx context.Context, p identity.Principal, callback func(identity.KeyLimitsView) error) error {
		if err := callback(v); err != nil {
			return err
		}
		return failure
	})
	if release != nil || !errors.Is(err, failure) {
		t.Fatal("commit failure granted request", err)
	}
	runtimeEmpty(t, s)
	// A failed commit has no money facts, but the admitted rate token stays
	// consumed rather than overwriting a newer bucket with an attempted refund.
	s.mu.Lock()
	state := s.keyRuntime[p.KeyID]
	now := time.Now()
	tokens := state.limiter.TokensAt(now)
	s.mu.Unlock()
	if tokens >= 1 {
		t.Fatal("failed commit refunded token")
	}
	runtimeDenied(t, s, p, v, now, 429, "rate_limited")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	release, err = s.acquireKeyRuntimeUsing(ctx, p, func(ctx context.Context, p identity.Principal, callback func(identity.KeyLimitsView) error) error {
		called = true
		return callback(v)
	})
	if !called || release != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled admission escaped", err)
	}
	runtimeEmpty(t, s)
}
