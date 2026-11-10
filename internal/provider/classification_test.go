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
		name    string
		status  int
		body    string
		class   FailureClass
		isolate bool
	}{
		{"bare_401", 401, `{"error":{"message":"invalid_api_key secret-canary"}}`, ClassRejected, false},
		{"credential_401", 401, `{"error":{"code":"invalid_api_key"}}`, ClassCredential, false},
		{"bare_403", 403, `{"error":{"message":"invalid_api_key secret-canary"}}`, ClassRejected, false},
		{"bare_404", 404, `{"error":{"message":"model_not_found secret-canary"}}`, ClassRejected, false},
		{"model_404", 404, `{"error":{"code":"model_not_found"}}`, ClassModel, true},
		{"model_403", 403, `{"error":{"code":"model_not_found"}}`, ClassModel, true},
		{"unsupported_model", 400, `{"error":{"code":"unsupported_model"}}`, ClassModel, true},
		{"context_length_exceeded", 400, `{"error":{"code":"context_length_exceeded"}}`, ClassModel, false},
		{"credential_403", 403, `{"error":{"code":"invalid_api_key"}}`, ClassCredential, false},
		{"quota_429", 429, `{"error":{"type":"insufficient_quota"}}`, ClassQuota, false},
		{"rate_429", 429, `{"error":{"code":"rate_limit_exceeded"}}`, ClassRateLimit, false},
		{"policy", 400, `{"error":{"code":"content_policy_violation"}}`, ClassPolicy, false},
		{"server_not_caller", 500, `{"error":{"code":"invalid_api_key"}}`, ClassUpstream, false},
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
			if !errors.As(err, &e) || e.Class != tc.class || e.BindingIsolation != tc.isolate || e.UpstreamStatus != tc.status {
				t.Fatalf("got %v expected %s", err, tc.class)
			}
		})
	}
}
