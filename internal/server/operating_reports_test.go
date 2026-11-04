package server

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/reporting"
)

func TestOperatingReportStrictFilter(t *testing.T) {
	valid := url.Values{"from": {"2026-11-01T08:00:00+08:00"}, "to": {"2026-11-02T00:00:00Z"}, "tenant_id": {uuid.NewString()}, "model_id": {uuid.NewString()}}
	f, err := operatingReportFilter(valid.Encode())
	if err != nil || f.From.Location() != time.UTC || f.From.Hour() != 0 || f.To.Sub(f.From) != 24*time.Hour || f.TenantID != valid.Get("tenant_id") {
		t.Fatal("incorrect UTC filter", f, err)
	}
	for _, raw := range []string{
		"", "from=2026-11-01&to=2026-11-02", valid.Encode() + "&from=2026-11-01T00:00:00Z",
		valid.Encode() + "&cursor=ignored", valid.Encode() + "&model_id=", valid.Encode() + "&broken=%xx",
		"from=2026-11-01T00:00:00&to=2026-11-02T00:00:00Z",
		"from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z",
		"from=2026-11-01T00:00:00Z&to=2026-11-01T00:00:00Z",
		"from=2026-09-01T00:00:00Z&to=2026-11-01T00:00:00Z",
		"from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z&model_id=invalid",
	} {
		if _, err := operatingReportFilter(raw); !errors.Is(err, reporting.ErrInvalidFilter) {
			t.Errorf("filter accepted or wrong error: %q %v", raw, err)
		}
	}
}

func TestOperatingReportFailureDoesNotDownloadJSON(t *testing.T) {
	for _, csv := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/?from=2026-11-01T00:00:00Z&to=2026-11-02T00:00:00Z", nil)
		(&Server{}).serveOperatingReport(c, csv)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"report_unavailable"`) || w.Header().Get("Content-Disposition") != "" || w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal("error masqueraded as file", w.Code, w.Header(), w.Body.String())
		}
	}
}
