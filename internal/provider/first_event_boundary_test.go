package provider

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNoConsumptionEventWhitelistOnlyAppliesBeforeAnyProtocolEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"event","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `event: error
data: {"error":{"code":"trusted_rejection"}}

`)
	}))
	defer srv.Close()
	profile := fixtureProfile()
	profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
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
	first, err := response.ReadEvent()
	if err != nil || first == nil || !first.Content {
		t.Fatalf("missing first content event: %+v %v", first, err)
	}
	_, err = response.ReadEvent()
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Consumption != ConsumptionUnknown || SafeToRetry(err) {
		t.Fatalf("post-content error inherited first-event rejection: %v", err)
	}
}

func TestResponsesFirstFailureReadsNestedErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `event: response.failed
data: {"type":"response.failed","response":{"error":{"code":"trusted_rejection"}}}

`)
	}))
	defer srv.Close()
	profile := fixtureProfile()
	profile.Protocol = ProtocolResponses
	profile.OutputLimitField = "max_output_tokens"
	profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
	request, err := Parse(ProtocolResponses, []byte(`{"model":"public-model","input":"hello","stream":true}`), profile)
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
	if !SafeToRetry(err) {
		t.Fatalf("nested trusted first failure was not retryable: %v", err)
	}
}

func TestNoConsumptionEventCodesAreDeepCopied(t *testing.T) {
	profile := fixtureProfile()
	profile.NoConsumptionEventCodes = []string{"trusted_rejection"}
	request, err := ParseChat([]byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":true}`), profile)
	if err != nil {
		t.Fatal(err)
	}
	profile.NoConsumptionEventCodes[0] = "mutated"
	returned := request.Profile()
	returned.NoConsumptionEventCodes[0] = "also_mutated"
	if got := request.Profile().NoConsumptionEventCodes[0]; got != "trusted_rejection" {
		t.Fatalf("request retry policy aliases caller memory: %q", got)
	}
}
