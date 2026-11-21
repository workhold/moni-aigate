package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFirstStreamEventRequiresExplicitNoConsumptionProfile(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "trusted_rejection"}[trusted], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: error\ndata: {\"error\":{\"code\":\"trusted_rejection\"}}\n\n")
			}))
			defer srv.Close()
			profile := fixtureProfile()
			if trusted {
				profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
			}
			request, err := ParseChat([]byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":true}`), profile)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := testContext(t)
			defer cancel()
			response, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = response.ReadEvent()
			var providerErr *Error
			if !errors.As(err, &providerErr) || providerErr.Class != ClassProtocol && providerErr.Class != ClassRejected {
				t.Fatalf("unexpected first-event classification: %v", err)
			}
			if got, want := providerErr.Consumption == ConsumptionRejected, trusted; got != want || SafeToRetry(err) != trusted {
				t.Fatalf("trusted=%v consumption=%s retry=%v err=%v", trusted, providerErr.Consumption, SafeToRetry(err), err)
			}
		})
	}
}

func TestNoConsumptionEventCodeProfileValidation(t *testing.T) {
	profile := fixtureProfile()
	profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
	if err := profile.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, codes := range [][]string{{"trusted_rejection", "trusted_rejection"}, {"UPPER"}, {""}, {"contains space"}, {string(make([]byte, 81))}} {
		profile.NoConsumptionEventCodes = codes
		if err := profile.Validate(); err == nil {
			t.Fatalf("invalid no-consumption event codes accepted: %q", codes)
		}
	}
}
