package server

import (
	"bytes"
	"testing"

	"github.com/moni-ai/moni-aigate/internal/provider"
)

func cacheTestRequest(t *testing.T, raw string, stream bool) *provider.Request {
	t.Helper()
	profile := provider.Profile{Version: "cache-profile", Model: "alias", Protocol: provider.ProtocolChat, InputTokenUpperBound: 1000, ContextLimit: 2000, OutputLimit: 100, DefaultOutputLimit: 20, SupportsTools: true, SupportsParallelToolCalls: true}
	request, err := provider.Parse(provider.ProtocolChat, []byte(raw), profile)
	if err != nil {
		t.Fatal(err)
	}
	if request.Stream != stream {
		t.Fatalf("stream=%v want %v", request.Stream, stream)
	}
	return request
}

func TestCachedResponseUsesCurrentRequestedAlias(t *testing.T) {
	body, err := rewriteCachedResponseModel([]byte(`{"model":"alias-a","choices":[]}`), provider.ProtocolChat, "alias-b")
	if err != nil || !bytes.Contains(body, []byte(`"model":"alias-b"`)) || bytes.Contains(body, []byte("alias-a")) {
		t.Fatalf("cached response retained original alias: %s %v", body, err)
	}
	gemini, err := rewriteCachedResponseModel([]byte(`{"modelVersion":"upstream"}`), provider.ProtocolGemini, "alias-b")
	if err != nil || !bytes.Contains(gemini, []byte("upstream")) {
		t.Fatalf("Gemini native model version was rewritten: %s %v", gemini, err)
	}
}

func TestTextCacheDigestNormalizesAndSeparatesScope(t *testing.T) {
	first := cacheTestRequest(t, `{"model":"alias","messages":[{"role":"user","content":"hello"}],"max_tokens":20}`, false)
	second := cacheTestRequest(t, `{ "messages" : [ { "content" : "hello", "role" : "user" } ], "model" : "alias", "max_tokens" : 20 }`, false)
	a, err := newTextCacheDescriptor(first, provider.ProtocolChat, "tenant-a", "logical-model", 7, 60)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newTextCacheDescriptor(second, provider.ProtocolChat, "tenant-a", "logical-model", 7, 60)
	if err != nil || a == nil || b == nil || !bytes.Equal(a.requestDigest, b.requestDigest) {
		t.Fatalf("equivalent normalized requests did not share digest: %v", err)
	}
	otherRoute, err := newTextCacheDescriptor(first, provider.ProtocolChat, "tenant-a", "logical-model", 8, 60)
	if err != nil || bytes.Equal(a.requestDigest, otherRoute.requestDigest) {
		t.Fatalf("route revision not separated: %v", err)
	}
	otherTenant, err := newTextCacheDescriptor(first, provider.ProtocolChat, "tenant-b", "logical-model", 7, 60)
	if err != nil || bytes.Equal(a.requestDigest, otherTenant.requestDigest) {
		t.Fatalf("tenant not separated: %v", err)
	}
	disabled, err := newTextCacheDescriptor(first, provider.ProtocolChat, "tenant-a", "logical-model", 7, 0)
	if disabled != nil || err != nil {
		t.Fatalf("cache default-off was not respected: %+v %v", disabled, err)
	}
}

func TestTextCacheEligibilityRejectsStreamsAndToolSemantics(t *testing.T) {
	streaming := cacheTestRequest(t, `{"model":"alias","messages":[{"role":"user","content":"hello"}],"stream":true,"max_tokens":20}`, true)
	if got, err := newTextCacheDescriptor(streaming, provider.ProtocolChat, "tenant", "model", 1, 60); got != nil || err != nil {
		t.Fatalf("stream became cacheable: %+v %v", got, err)
	}
	for _, raw := range []string{
		`{"model":"alias","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}],"max_tokens":20}`,
		`{"model":"alias","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call","type":"function","function":{"name":"weather","arguments":"{}"}}]}],"max_tokens":20}`,
	} {
		request := cacheTestRequest(t, raw, false)
		if got, err := newTextCacheDescriptor(request, provider.ProtocolChat, "tenant", "model", 1, 60); got != nil || err != nil {
			t.Fatalf("tool input became cacheable: %+v %v", got, err)
		}
	}
	for _, body := range []string{
		`{"choices":[{"message":{"content":"safe"},"finish_reason":"stop"}]}`,
	} {
		if !cacheableResponseBody([]byte(body)) {
			t.Fatalf("plain JSON response rejected: %s", body)
		}
	}
	for _, body := range []string{
		`{"choices":[{"message":{"content":"call"},"finish_reason":"tool_calls"}]}`,
		`{"output":[{"type":"function_call","name":"weather"}]}`,
		`{"content":[{"type":"tool_use","name":"weather"}]}`,
		`not-json`,
	} {
		if cacheableResponseBody([]byte(body)) {
			t.Fatalf("tool/invalid response became cacheable: %s", body)
		}
	}
}
