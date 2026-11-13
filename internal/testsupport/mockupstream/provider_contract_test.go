package mockupstream_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/testsupport/mockupstream"
)

func providerFixture(t *testing.T) (*httptest.Server, *provider.Client) {
	t.Helper()
	service, err := mockupstream.New(mockupstream.Config{Candidates: map[string]mockupstream.Candidate{
		"a": {Secret: "a-secret", Models: []string{"qa-text"}, OutputTokens: 8},
	}}, mockupstream.Options{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service.Handler())
	t.Cleanup(server.Close)
	return server, provider.NewClient(provider.ClientOptions{ConnectTimeout: time.Second, HeaderTimeout: time.Second, FirstEventTimeout: time.Second, IdleTimeout: time.Second, ProgressTimeout: time.Second})
}

func endpointFor(t *testing.T, raw, model, key string) provider.Endpoint {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	return provider.Endpoint{URL: raw, Model: model, APIKey: key, Network: provider.NetworkPolicy{AllowHTTP: true, AllowedCIDRs: []string{"127.0.0.0/8", "::1/128"}, AllowedPorts: []uint16{uint16(port)}}}
}

func providerProfile(protocol string) provider.Profile {
	profile := provider.Profile{Protocol: protocol, Version: "mock-v1", Model: "qa-text", InputTokenUpperBound: 100, ContextLimit: 128, OutputLimit: 16, DefaultOutputLimit: 8, UsageSemantics: "final", OutputLimitField: "max_tokens"}
	if protocol == provider.ProtocolResponses {
		profile.OutputLimitField = "max_output_tokens"
	} else if protocol == provider.ProtocolGemini {
		profile.OutputLimitField = "maxOutputTokens"
		profile.UsageSemantics = "cumulative"
	} else if protocol == provider.ProtocolEmbeddings || protocol == provider.ProtocolRerank {
		profile.OutputLimit = 0
		profile.DefaultOutputLimit = 0
		profile.OutputLimitField = ""
		profile.UsageSemantics = "final"
	}
	return profile
}

func TestProviderParsesEveryMockJSONProtocol(t *testing.T) {
	server, client := providerFixture(t)
	cases := []struct {
		name, protocol, path, body string
	}{
		{"chat", provider.ProtocolChat, "/c/a/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2}`},
		{"responses", provider.ProtocolResponses, "/c/a/v1/responses", `{"model":"qa-text","input":"hello","max_output_tokens":2,"store":false}`},
		{"messages", provider.ProtocolMessages, "/c/a/v1/messages", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2}`},
		{"embeddings", provider.ProtocolEmbeddings, "/c/a/v1/embeddings", `{"model":"qa-text","input":["hello","world"],"dimensions":3}`},
		{"rerank", provider.ProtocolRerank, "/c/a/v1/rerank", `{"model":"qa-text","query":"hello","documents":["a","b"]}`},
		{"gemini", provider.ProtocolGemini, "/c/a/v1beta/models/qa-text:generateContent", `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := providerProfile(tc.protocol)
			request, err := provider.Parse(tc.protocol, []byte(tc.body), profile)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := client.Do(ctx, endpointFor(t, server.URL+tc.path, "qa-text", "a-secret"), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := response.DecodeJSON(); err != nil {
				t.Fatal(err)
			}
			if response.ProtocolOutcome != "completed" || response.Usage.Status != provider.UsageConfirmed {
				t.Fatalf("unconfirmed mock response: outcome=%s usage=%#v", response.ProtocolOutcome, response.Usage)
			}
		})
	}
}

func TestProviderParsesMockSSEProtocols(t *testing.T) {
	server, client := providerFixture(t)
	cases := []struct {
		name, protocol, path, body string
	}{
		{"chat", provider.ProtocolChat, "/c/a/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":true}`},
		{"responses", provider.ProtocolResponses, "/c/a/v1/responses", `{"model":"qa-text","input":"hello","max_output_tokens":2,"store":false,"stream":true}`},
		{"messages", provider.ProtocolMessages, "/c/a/v1/messages", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":true}`},
		{"gemini", provider.ProtocolGemini, "/c/a/v1beta/models/qa-text:streamGenerateContent", `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile := providerProfile(tc.protocol)
			var request *provider.Request
			var err error
			if tc.protocol == provider.ProtocolGemini {
				request, err = provider.ParseGemini([]byte(tc.body), profile, true)
			} else {
				request, err = provider.Parse(tc.protocol, []byte(tc.body), profile)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			response, err := client.Do(ctx, endpointFor(t, server.URL+tc.path, "qa-text", "a-secret"), request)
			if err != nil {
				t.Fatal(err)
			}
			for {
				event, err := response.ReadEvent()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("event=%#v: %v", event, err)
				}
				if event.Kind == "done" {
					break
				}
			}
			if response.ProtocolOutcome != "completed" || response.Usage.Status != provider.UsageConfirmed {
				t.Fatalf("unconfirmed mock stream: outcome=%s usage=%#v", response.ProtocolOutcome, response.Usage)
			}
		})
	}
}
