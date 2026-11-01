package paymentreceiver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReceiverRejectsUntrustedTransportBeforeRepository(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		headers            http.Header
		body               string
		status             int
	}{
		{"route", "POST", "/v1/checkout", nil, "{}", 404},
		{"method", "GET", notificationPath, nil, "", 405},
		{"query", "POST", notificationPath + "?event=x", http.Header{"Content-Type": {"application/json"}}, "{}", 400},
		{"encoding", "POST", notificationPath, http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, "{}", 415},
		{"duplicate type", "POST", notificationPath, http.Header{"Content-Type": {"application/json", "application/json"}}, "{}", 415},
		{"media parameter", "POST", notificationPath, http.Header{"Content-Type": {"application/json; q=1"}}, "{}", 415},
		{"bearer", "POST", notificationPath, http.Header{"Content-Type": {"application/json"}, "Authorization": {"Bearer private-test-token"}}, "{}", 400},
		{"body bound", "POST", notificationPath, http.Header{"Content-Type": {"application/json"}}, strings.Repeat("x", 65537), 413},
		{"signature missing", "POST", notificationPath, http.Header{"Content-Type": {"application/json"}}, "{}", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header = tc.headers
			w := httptest.NewRecorder()
			newReceiverHTTP(nil).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
			if strings.Contains(w.Body.String(), "private-test-token") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unsafe error response")
			}
		})
	}
}
