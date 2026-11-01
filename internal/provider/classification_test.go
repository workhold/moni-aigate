package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderFailureAttributionUsesCodesNotBare403(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		class  FailureClass
	}{
		{"bare_401", 401, `{"error":{"message":"invalid_api_key secret-canary"}}`, ClassRejected},
		{"credential_401", 401, `{"error":{"code":"invalid_api_key"}}`, ClassCredential},
		{"bare_403", 403, `{"error":{"message":"invalid_api_key secret-canary"}}`, ClassRejected},
		{"bare_404", 404, `{"error":{"message":"model_not_found secret-canary"}}`, ClassRejected},
		{"model_404", 404, `{"error":{"code":"model_not_found"}}`, ClassModel},
		{"model_403", 403, `{"error":{"code":"model_not_found"}}`, ClassModel},
		{"credential_403", 403, `{"error":{"code":"invalid_api_key"}}`, ClassCredential},
		{"quota_429", 429, `{"error":{"type":"insufficient_quota"}}`, ClassQuota},
		{"rate_429", 429, `{"error":{"code":"rate_limit_exceeded"}}`, ClassRateLimit},
		{"policy", 400, `{"error":{"code":"content_policy_violation"}}`, ClassPolicy},
		{"server_not_caller", 500, `{"error":{"code":"invalid_api_key"}}`, ClassUpstream},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			_, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), fixtureRequest(t, false))
			var e *Error
			if !errors.As(err, &e) || e.Class != tc.class || e.UpstreamStatus != tc.status {
				t.Fatalf("got %v expected %s", err, tc.class)
			}
		})
	}
}
