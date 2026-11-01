package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"golang.org/x/time/rate"
)

func TestPublicCatalogStrictQueryAndCursor(t *testing.T) {
	id := uuid.NewString()
	filter := catalog.PublicCatalogFilter{Prefix: "model_%\\", Protocol: "openai_chat"}
	cursor := encodePublicCatalogCursor(filter, catalog.PublicModel{ID: id, PublicName: "model_z"})
	query := "q=" + url.QueryEscape(" "+filter.Prefix+" ") + "&protocol=openai_chat&cursor=" + cursor
	got, ok := parsePublicCatalogQuery(query)
	if !ok || got.Prefix != filter.Prefix || got.Protocol != filter.Protocol || got.AfterName != "model_z" || got.AfterID != id {
		t.Fatal("normalized filter cursor failed", got, ok)
	}
	for _, query := range []string{"", "q=&protocol=", "q=%C2%A0hello%C2%A0", "q=has+space", "protocol=openai_responses", "protocol=anthropic_messages", "protocol=openai_embeddings", "protocol=jina_rerank", "protocol=gemini_generate_content"} {
		if _, ok := parsePublicCatalogQuery(query); !ok {
			t.Fatal("valid public query rejected", query)
		}
	}
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	boundary := fmt.Sprintf(`{"v":1,"name":"model_z","id":%q,"filter":%q}`, id, publicCatalogFilterDigest("", ""))
	for _, query := range []string{
		"secret=private-input-canary", "q=a&q=b", "Q=a", "protocol=openai_chat&protocol=openai_chat", "protocol=unknown",
		"q=%FF", "q=%ZZ", "q=%", "q=a;b", "q=%00", "q=%0A", "q=" + strings.Repeat("x", 101), strings.Repeat("x", 4097),
		"cursor=", "cursor=a&cursor=b", "cursor=" + cursor, "cursor=" + cursor + "=", "cursor=" + strings.Repeat("A", 1025),
		"cursor=" + encode(`null`), "cursor=" + encode(`[]`), "cursor=" + encode(boundary+`{}`),
		"cursor=" + encode(strings.Replace(boundary, `"v":1`, `"v":1,"v":1`, 1)),
		"cursor=" + encode(strings.Replace(boundary, `"v":1`, `"V":1`, 1)),
		"cursor=" + encode(strings.Replace(boundary, `"v":1`, `"v":1,"extra":true`, 1)),
		"cursor=" + encode(strings.Replace(boundary, `"v":1`, `"v":1.0`, 1)),
		"cursor=" + encode(strings.Replace(boundary, id, strings.ToUpper(id), 1)),
		"cursor=" + encode(strings.Replace(boundary, id, uuid.Nil.String(), 1)),
		"cursor=" + encode(strings.Replace(boundary, "model_z", "model z", 1)),
		"cursor=" + encode(strings.Replace(boundary, "model_z", `model\u0000z`, 1)),
		"cursor=" + encode(strings.Replace(boundary, "model_z", strings.Repeat("x", 769), 1)),
	} {
		if _, ok := parsePublicCatalogQuery(query); ok {
			t.Fatal("ambiguous or cross-filter public query accepted", query)
		}
	}
}

func TestPublicCatalogStrictPublicationBody(t *testing.T) {
	for _, value := range []string{`{"expected_revision":"1","public_visible":true}`, `{"expected_revision":"9223372036854775807","public_visible":false}`} {
		var body modelPublicationBody
		if revision, ok := decodeModelPublication(strings.NewReader(value), &body); !ok || revision <= 0 || body.PublicVisible == nil {
			t.Fatal("valid publication body rejected")
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"expected_revision":"1"}`, `{"public_visible":true}`, `{"expected_revision":"1","public_visible":null}`,
		`{"expected_revision":"1","public_visible":"true"}`, `{"expected_revision":1,"public_visible":true}`,
		`{"Expected_Revision":"1","public_visible":true}`, `{"expected_revision":"1","public_visible":true,"secret":"private-marker"}`,
		`{"expected_revision":"1","public_visible":true,"public_visible":false}`, `{"expected_revision":"1","public_visible":true}{}`,
		strings.Repeat(" ", 8193) + `{"expected_revision":"1","public_visible":true}`,
	} {
		var body modelPublicationBody
		if _, ok := decodeModelPublication(strings.NewReader(raw), &body); ok {
			t.Fatal("ambiguous publication body accepted")
		}
	}
	for _, revision := range []string{"0", "-1", "+1", "01", "1.0", " 1", "9223372036854775808"} {
		var body modelPublicationBody
		if _, ok := decodeModelPublication(strings.NewReader(fmt.Sprintf(`{"expected_revision":%q,"public_visible":true}`, revision)), &body); ok {
			t.Fatal("noncanonical revision accepted", revision)
		}
	}
}

func TestPublicCatalogAdmissionRatesCapacityAndBoundedSources(t *testing.T) {
	now := time.Now()
	var global publicCatalogAdmission
	for i := range 40 {
		release, ok := global.acquire(fmt.Sprint(i), now)
		if !ok {
			t.Fatal("global burst smaller than contract", i)
		}
		release()
	}
	if _, ok := global.acquire("exhausted", now); ok {
		t.Fatal("global burst exceeded")
	}
	if release, ok := global.acquire("refilled", now.Add(50*time.Millisecond)); !ok {
		t.Fatal("20 rps global refill failed")
	} else {
		release()
	}
	var perIP publicCatalogAdmission
	for range 8 {
		release, ok := perIP.acquire("same-source", now)
		if !ok {
			t.Fatal("IP burst smaller than contract")
		}
		release()
	}
	if _, ok := perIP.acquire("same-source", now); ok {
		t.Fatal("IP burst exceeded")
	}
	if release, ok := perIP.acquire("same-source", now.Add(500*time.Millisecond)); !ok {
		t.Fatal("2 rps source refill failed")
	} else {
		release()
	}
	var permits publicCatalogAdmission
	var releases []func()
	for range publicCatalogFlights {
		release, ok := permits.acquire("same-source", now)
		if !ok {
			t.Fatal("in-flight capacity smaller than contract")
		}
		releases = append(releases, release)
	}
	if _, ok := permits.acquire("another-source", now); ok {
		t.Fatal("in-flight capacity exceeded")
	}
	var releasing sync.WaitGroup
	for _, release := range releases {
		for range 2 {
			releasing.Add(1)
			go func() { defer releasing.Done(); release() }()
		}
	}
	releasing.Wait()
	if permits.inflight != 0 {
		t.Fatal("concurrent duplicate releases changed permit accounting", permits.inflight)
	}
	// Isolate source bookkeeping from the independently tested global rate.
	bounded := publicCatalogAdmission{global: rate.NewLimiter(rate.Inf, 40), sources: map[string]*publicCatalogSource{"": {limiter: rate.NewLimiter(2, 8), seen: now}}}
	for i := range publicCatalogSources - 1 {
		release, ok := bounded.acquire(fmt.Sprint(i), now)
		if !ok {
			t.Fatal("source capacity unexpectedly exhausted", i)
		}
		release()
	}
	if _, ok := bounded.acquire("new-source-without-slot", now); ok {
		t.Fatal("new source at capacity borrowed the unknown bucket")
	}
	for range 8 {
		release, ok := bounded.acquire("", now)
		if !ok {
			t.Fatal("unknown source lost its reserved bucket at capacity")
		}
		release()
	}
	if _, ok := bounded.acquire("", now); ok || len(bounded.sources) != publicCatalogSources {
		t.Fatal("unknown source escaped its rate or source memory bound")
	}
	if release, ok := bounded.acquire("new-after-idle", now.Add(publicCatalogIdle)); !ok {
		t.Fatal("idle source eviction failed")
	} else {
		release()
	}
	if len(bounded.sources) != 2 {
		t.Fatal("idle source history retained", len(bounded.sources))
	}
}

func TestPublicCatalogAdmissionCoversGuardAndReturnsPermits(t *testing.T) {
	s := &Server{}
	s.Ready(true)
	entered := make(chan struct{}, publicCatalogFlights)
	gate := make(chan struct{})
	var guardCalls atomic.Int64
	s.Guard = func(ctx context.Context) error {
		guardCalls.Add(1)
		entered <- struct{}{}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	engine := s.Engine()
	var group sync.WaitGroup
	var cancelFirst context.CancelFunc
	for i := range publicCatalogFlights {
		ctx, cancel := context.WithCancel(context.Background())
		if i == 0 {
			cancelFirst = cancel
		} else {
			defer cancel()
		}
		group.Add(1)
		go func() {
			defer group.Done()
			req := httptest.NewRequest("GET", "/api/public/models?secret=private-marker", nil).WithContext(ctx)
			engine.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	for range publicCatalogFlights {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(gate)
			group.Wait()
			t.Fatal("public permit did not cover Guard")
		}
	}
	start := time.Now()
	full := httptest.NewRecorder()
	engine.ServeHTTP(full, httptest.NewRequest("GET", "/api/public/models", nil))
	if full.Code != 429 || full.Header().Get("Retry-After") != "1" || time.Since(start) >= 200*time.Millisecond || guardCalls.Load() != 4 {
		t.Fatal("full public admission queued or reached Guard", full.Code, guardCalls.Load())
	}
	cancelFirst()
	close(gate)
	group.Wait()
	if s.publicCatalog.inflight != 0 || len(s.limits) != 0 || len(s.limitSeen) != 0 {
		t.Fatal("cancel/error leaked permits or polluted other limiters")
	}
	// A panic in the anonymous query and a fenced Guard must both release.
	s.Guard = nil
	panicResponse := httptest.NewRecorder()
	engine.ServeHTTP(panicResponse, httptest.NewRequest("GET", "/api/public/models", nil))
	if panicResponse.Code != 500 || s.publicCatalog.inflight != 0 {
		t.Fatal("panic leaked public permit", panicResponse.Code)
	}
	s.Guard = func(context.Context) error { return errors.New("private-guard-marker") }
	failed := httptest.NewRecorder()
	engine.ServeHTTP(failed, httptest.NewRequest("GET", "/api/public/models", nil))
	if failed.Code != 503 || !strings.Contains(failed.Body.String(), "instance_unavailable") || strings.Contains(failed.Body.String(), "private-guard-marker") || s.publicCatalog.inflight != 0 || s.ready.Load() {
		t.Fatal("guard error leaked or reopened the instance", failed.Code)
	}
}

func TestPublicCatalogAdmissionSourceTrustAndSafeInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		remote string
		header []string
		want   string
	}{
		{"10.1.2.3:1234", []string{"192.0.2.1"}, "192.0.2.1"},
		{"203.0.113.2:1234", []string{"192.0.2.1"}, "203.0.113.2"},
		{"10.1.2.3:1234", []string{"192.0.2.1", "192.0.2.2"}, "10.1.2.3"},
		{"10.1.2.3:1234", []string{"192.0.2.1,192.0.2.2"}, "10.1.2.3"},
		{"[::ffff:192.0.2.1]:1234", nil, "192.0.2.1"},
		{"invalid-source", nil, ""},
	} {
		s := &Server{Config: config.Config{TrustedProxyCIDRs: []string{"10.0.0.0/8"}}}
		s.Ready(true)
		req := httptest.NewRequest("GET", "/api/public/models?secret=private-input-marker", nil)
		req.RemoteAddr = tc.remote
		for _, value := range tc.header {
			req.Header.Add("X-Real-IP", value)
		}
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: "invalid-session-must-not-be-read"})
		rec := httptest.NewRecorder()
		s.Engine().ServeHTTP(rec, req)
		if rec.Code != 400 || rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("X-Request-ID") == "" || strings.Contains(rec.Body.String(), "private-input-marker") || s.publicCatalog.sources[tc.want] == nil {
			t.Fatal("public source trust or anonymous error boundary changed", tc.want, rec.Code, rec.Body.String())
		}
	}
}
