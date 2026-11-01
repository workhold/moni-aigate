package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/alerts"
	"github.com/moni-ai/moni-aigate/internal/identity"
)

func alertTestContext(actor string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("session", &identity.Session{User: identity.User{ID: actor, PlatformRole: "admin"}})
	return c, w
}

func TestAlertInputExactParametersAndLosslessNumbers(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{}`, true},
		{`{"threshold_amount":"0","recovery_amount":"9223372036854775807"}`, true},
		{`{"threshold_amount":"9007199254740993","recovery_amount":"9007199254740994"}`, true},
		{`{"threshold_amount":1,"recovery_amount":"2"}`, false},
		{`{"threshold_amount":"01","recovery_amount":"2"}`, false},
		{`{"threshold_amount":"1","recovery_amount":"0"}`, false},
		{`{"threshold_amount":"0","recovery_amount":"9223372036854775808"}`, false},
		{`{"threshold_amount":"0","recovery_amount":"1","extra":0}`, false},
		{`{"threshold_amount":"0","threshold_amount":"0","recovery_amount":"1"}`, false},
		{`{"minimum_age_seconds":"600","trigger_count":"1000","recovery_count":"999"}`, true},
		{`{"minimum_age_seconds":"604800","trigger_count":"1","recovery_count":"0"}`, true},
		{`{"minimum_age_seconds":"599","trigger_count":"1","recovery_count":"0"}`, false},
		{`{"minimum_age_seconds":"604801","trigger_count":"1","recovery_count":"0"}`, false},
		{`{"minimum_age_seconds":"600","trigger_count":"1001","recovery_count":"0"}`, false},
		{`{"minimum_age_seconds":"600","trigger_count":"1","recovery_count":"1"}`, false},
		{`null`, false}, {`[]`, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			p, ok := alertParameters(json.RawMessage(tc.raw))
			if ok != tc.valid {
				t.Fatalf("valid=%v want %v", ok, tc.valid)
			}
			if ok {
				raw, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				var a, b any
				_ = json.Unmarshal(raw, &a)
				_ = json.Unmarshal([]byte(tc.raw), &b)
				x, _ := json.Marshal(a)
				y, _ := json.Marshal(b)
				if string(x) != string(y) {
					t.Fatalf("parameters changed: %s", raw)
				}
			}
		})
	}
	for _, v := range []string{"", " padded", "padded ", "a\n", strings.Repeat("中", 54), strings.Repeat("a", 81)} {
		raw, _ := json.Marshal(v)
		if _, ok := alertLabel(raw); ok {
			t.Fatalf("invalid label accepted: %q", v)
		}
	}
	if _, ok := alertLabel(json.RawMessage(`"中文 😀"`)); !ok {
		t.Fatal("valid unicode label rejected")
	}
}

func TestAlertAdmissionIndependentBoundedAndOnceRelease(t *testing.T) {
	s := &Server{}
	var releases []func()
	for range 4 {
		c, _ := alertTestContext("actor")
		done, ok := s.admitAlert(c, false)
		if !ok {
			t.Fatal("read slot unexpectedly unavailable")
		}
		deadline, ok := c.Request.Context().Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Fatal("read deadline missing")
		}
		releases = append(releases, done)
	}
	c, w := alertTestContext("actor")
	if _, ok := s.admitAlert(c, false); ok || w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatal("fifth concurrent read was not bounded")
	}
	for range 2 {
		c, _ := alertTestContext("actor")
		done, ok := s.admitAlert(c, true)
		if !ok {
			t.Fatal("reads blocked independent write budget")
		}
		releases = append(releases, done)
	}
	c, w = alertTestContext("actor")
	if _, ok := s.admitAlert(c, true); ok || w.Code != 429 {
		t.Fatal("third write was not bounded")
	}
	for _, done := range releases {
		done()
		done()
	}
	if s.alertAdmission.reads != 0 || s.alertAdmission.writes != 0 {
		t.Fatal("permits leaked or double-released")
	}
	if len(s.commerce.buckets) != 0 {
		t.Fatal("alert traffic occupied commerce buckets")
	}
}

func TestAlertAdmissionGlobalCapacityAndIdleEviction(t *testing.T) {
	s := &Server{}
	var releases []func()
	for i := 0; i < 32; i++ {
		c, _ := alertTestContext(uuid.NewString())
		done, ok := s.admitAlert(c, false)
		if !ok {
			t.Fatal("global read capacity too small")
		}
		releases = append(releases, done)
	}
	c, w := alertTestContext(uuid.NewString())
	if _, ok := s.admitAlert(c, false); ok || w.Code != 429 {
		t.Fatal("global capacity exceeded")
	}
	for _, done := range releases {
		done()
	}
	s.alertAdmission.actors = make(map[string]*alertBucket)
	for i := 0; i < 4096; i++ {
		s.alertAdmission.actors[uuid.NewString()] = &alertBucket{seen: time.Now()}
	}
	c, w = alertTestContext("new")
	if _, ok := s.admitAlert(c, false); ok || w.Code != 503 {
		t.Fatal("actor map grew without bound")
	}
	s.alertAdmission.actors["expired"] = &alertBucket{seen: time.Now().Add(-11 * time.Minute)}
	// Removing one idle bucket is sufficient; active stale buckets must survive.
	for key := range s.alertAdmission.actors {
		if key != "expired" {
			delete(s.alertAdmission.actors, key)
			break
		}
	}
	c, _ = alertTestContext("new")
	done, ok := s.admitAlert(c, false)
	if !ok {
		t.Fatal("expired idle bucket not evicted")
	}
	done()
	if _, exists := s.alertAdmission.actors["expired"]; exists {
		t.Fatal("expired bucket retained")
	}
}

func TestAlertResponsesBoundedFailClosedAndPrivate(t *testing.T) {
	s := &Server{}
	c, w := alertTestContext("actor")
	s.alertJSON(c, map[string]string{"secret": strings.Repeat("s", 256<<10)})
	if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("oversized data partly exposed")
	}
	c, w = alertTestContext("actor")
	alertPage(s, c, alerts.Page[alerts.RuleView]{}, nil)
	if w.Code != 503 {
		t.Fatal("nil page should not masquerade as empty")
	}
	c, w = alertTestContext("actor")
	alertPage(s, c, alerts.Page[alerts.RuleView]{Data: []alerts.RuleView{}}, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) || !strings.Contains(w.Body.String(), `"next_cursor":null`) {
		t.Fatal(w.Body.String())
	}
	c, w = alertTestContext("actor")
	s.alertError(c, errors.New("private SQL and credentials"))
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "SQL") {
		t.Fatal("raw failure leaked")
	}
}

func TestAlertCursorBoundToFilterAndStrictQueries(t *testing.T) {
	digest := alerts.RuleFilterDigest(alerts.RuleFilter{})
	raw, err := alerts.EncodeCursor(alerts.Cursor{V: 1, T: alerts.Time{Time: time.Now().UTC().Truncate(time.Microsecond)}, ID: uuid.NewString(), FilterDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := alertCursor(raw, digest); !ok {
		t.Fatal("valid cursor rejected")
	}
	if _, ok := alertCursor(raw, strings.Repeat("0", 64)); ok {
		t.Fatal("cursor crossed filters")
	}
	for _, path := range []string{"/?", "/?limit=50", "/?cursor=", "/?cursor=a&cursor=b", "/?cursor=%FF", "/?cursor=%"} {
		c, w := alertTestContext("actor")
		c.Request = httptest.NewRequest("GET", path, nil)
		if _, ok := (&Server{}).alertQuery(c, "cursor"); ok || w.Code != 400 {
			t.Fatalf("query accepted: %s", path)
		}
	}
}
