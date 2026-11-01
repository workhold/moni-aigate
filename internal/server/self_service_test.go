package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/config"
	"golang.org/x/time/rate"
)

func TestSelfServiceHashPermitHasIndependentBoundAndIdempotentRelease(t *testing.T) {
	s := &Server{}
	releases := []func(){}
	for i := 0; i < 4; i++ {
		c, _ := selfServiceUnitContext()
		release, ok := s.identityHashPermit(c)
		if !ok {
			t.Fatal("hash permit rejected below bound")
		}
		releases = append(releases, release)
	}
	c, w := selfServiceUnitContext()
	if release, ok := s.identityHashPermit(c); ok || release != nil || w.Code != 429 {
		t.Fatal("hash concurrency not bounded")
	}
	var wg sync.WaitGroup
	for _, release := range releases {
		for j := 0; j < 3; j++ {
			wg.Add(1)
			go func(r func()) { defer wg.Done(); r() }(release)
		}
	}
	wg.Wait()
	if s.identityHashes != 0 {
		t.Fatal("hash permit overrelease or leak")
	}
	c, _ = selfServiceUnitContext()
	release, ok := s.identityHashPermit(c)
	if !ok {
		t.Fatal("released hash capacity not reusable")
	}
	release()
}
func TestSelfServiceAnonymousIPUsesTrustedAddressAndDoesNotExposeSubject(t *testing.T) {
	s := &Server{limits: map[string]*rate.Limiter{}, limitSeen: map[string]time.Time{}}
	for i := 0; i < 6; i++ {
		c, w := selfServiceUnitContext()
		c.Request.RemoteAddr = "192.0.2.50:2345"
		c.Request.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", i+1))
		allowed := s.selfServiceIP(c)
		if allowed != (i < 5) || i == 5 && w.Code != 429 {
			t.Fatal("identity IP burst differs", i, w.Code)
		}
	}
	if len(s.limits) != 1 {
		t.Fatal("IP limiter created unbounded identities")
	}
	for key := range s.limits {
		if strings.Contains(key, "@") || !strings.HasPrefix(key, "identity:ip:") {
			t.Fatal("limiter retained raw subject")
		}
	}
}
func TestSelfServiceCapabilitiesWithoutMailOrRecoveryNeedNoDatabase(t *testing.T) {
	for _, tc := range []struct {
		registration, reset, recovery bool
		reason                        string
	}{{false, true, false, "mail_not_configured"}, {false, false, false, "feature_disabled"}, {true, true, true, "recovery_mode"}} {
		s := &Server{Config: config.Config{AuthRegistrationEnabled: tc.registration, AuthPasswordResetEnabled: tc.reset, RecoveryMode: tc.recovery}}
		c, _ := selfServiceUnitContext()
		v, err := s.selfServiceCapabilities(c)
		if err != nil || v.PasswordReset.Available || v.EmailDeliveryConfigured || v.PasswordReset.Reason != tc.reason {
			t.Fatal("unconfigured capability requires DB or leaks config", fmt.Sprint(err))
		}
	}
}

func selfServiceUnitContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	engine.SetTrustedProxies(nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/password-reset/request", strings.NewReader("{}"))
	c.Request.RemoteAddr = "192.0.2.1:1234"
	return c, w
}
