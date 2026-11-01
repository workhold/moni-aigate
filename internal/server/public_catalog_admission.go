package server

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/security"
	"golang.org/x/time/rate"
)

const (
	publicCatalogSources = 4096
	publicCatalogFlights = 4
	publicCatalogIdle    = 10 * time.Minute
)

type publicCatalogSource struct {
	limiter *rate.Limiter
	seen    time.Time
}

// This admission state is intentionally independent of login and paid request
// limiters. The shared unknown source is included in the fixed source bound.
type publicCatalogAdmission struct {
	mu       sync.Mutex
	global   *rate.Limiter
	sources  map[string]*publicCatalogSource
	inflight int
}

func publicCatalogPath(path string) bool {
	return path == "/api/public/models" || strings.HasPrefix(path, "/api/public/models/")
}

func (a *publicCatalogAdmission) acquire(source string, now time.Time) (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.global == nil {
		a.global = rate.NewLimiter(20, 40)
		a.sources = map[string]*publicCatalogSource{"": {limiter: rate.NewLimiter(2, 8), seen: now}}
	}
	if a.inflight >= publicCatalogFlights {
		return nil, false
	}
	if !a.global.AllowN(now, 1) {
		return nil, false
	}
	entry := a.sources[source]
	if entry == nil {
		// Scan only when a new source meets the bound. There is no timer or
		// unbounded history associated with a source.
		if len(a.sources) == publicCatalogSources {
			for key, value := range a.sources {
				if key != "" && now.Sub(value.seen) >= publicCatalogIdle {
					delete(a.sources, key)
				}
			}
		}
		if len(a.sources) < publicCatalogSources {
			entry = &publicCatalogSource{limiter: rate.NewLimiter(2, 8), seen: now}
			a.sources[source] = entry
		} else {
			return nil, false
		}
	}
	entry.seen = now
	if !entry.limiter.AllowN(now, 1) {
		return nil, false
	}
	a.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			a.inflight--
			a.mu.Unlock()
		})
	}, true
}

func (s *Server) admitPublicCatalog(c *gin.Context) (func(), bool) {
	source := security.NormalizeClientIP(trustedClientIP(c))
	release, ok := s.publicCatalog.acquire(source, time.Now())
	if !ok {
		c.Header("Retry-After", "1")
		s.fail(c, 429, "public_catalog_rate_limited", "公开目录请求过于频繁，请稍后重试")
	}
	return release, ok
}
