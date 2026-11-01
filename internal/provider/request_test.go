package provider

import (
	"encoding/json"
	"testing"
)

func fixtureProfile() Profile {
	return Profile{Version: "p1", Model: "public-model", InputTokenUpperBound: 8192, ContextLimit: 10240, OutputLimit: 2048, DefaultOutputLimit: 128, SupportsTools: true, SupportsParallelToolCalls: true}
}
func fixtureRequest(t *testing.T, stream bool) *Request {
	t.Helper()
	suffix := ""
	if stream {
		suffix = `,"stream":true`
	}
	r, e := ParseChat([]byte(`{"model":"public-model","messages":[{"role":"user","content":"你好"}]`+suffix+`}`), fixtureProfile())
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestRequestContractAndForcedMetering(t *testing.T) {
	p := fixtureProfile()
	p.OutputLimitField = "max_completion_tokens"
	r, e := ParseChat([]byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":false},"max_tokens":33,"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"tool_choice":{"type":"function","function":{"name":"weather"}}}`), p)
	if e != nil {
		t.Fatal(e)
	}
	if r.IncludeUsage || !r.Stream || r.MaxOutputTokens != 33 || r.InputUpperBound != 8192 {
		t.Fatalf("wrong normalized request: %+v", r)
	}
	b, e := r.UpstreamJSON("upstream-model")
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	if string(m["model"]) != `"upstream-model"` || string(m["max_completion_tokens"]) != "33" || m["max_tokens"] != nil || string(m["stream_options"]) != `{"include_usage":true}` {
		t.Fatalf("wrong upstream payload: %s", b)
	}
}

func TestRejectUnsupportedOrAmbiguousRequestsBeforeDispatch(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate":           `{"model":"public-model","model":"other","messages":[]}`,
		"nested_duplicate":    `{"model":"public-model","messages":[{"role":"user","content":"x","content":"y"}]}`,
		"media":               `{"model":"public-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"http://localhost"}}]}]}`,
		"unknown":             `{"model":"public-model","messages":[{"role":"user","content":"x"}],"service_tier":"flex"}`,
		"n2":                  `{"model":"public-model","messages":[{"role":"user","content":"x"}],"n":2}`,
		"two_limits":          `{"model":"public-model","messages":[{"role":"user","content":"x"}],"max_tokens":1,"max_completion_tokens":1}`,
		"overflow":            `{"model":"public-model","messages":[{"role":"user","content":"x"}],"max_tokens":9223372036854775808}`,
		"null":                `{"model":"public-model","messages":[{"role":"user","content":"x"}],"stream":null}`,
		"hosted_tool":         `{"model":"public-model","messages":[{"role":"user","content":"x"}],"tools":[{"type":"web_search"}]}`,
		"unknown_tool_choice": `{"model":"public-model","messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{"name":"a"}}],"tool_choice":{"type":"function","function":{"name":"b"}}}`,
		"empty_assistant":     `{"model":"public-model","messages":[{"role":"assistant","content":null}]}`,
		"negative_limit":      `{"model":"public-model","messages":[{"role":"user","content":"x"}],"max_tokens":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := ParseChat([]byte(raw), fixtureProfile()); e == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	p := fixtureProfile()
	p.AllowedParameters = []string{"audio"}
	if p.Validate() == nil {
		t.Fatal("profile allowed an unimplemented billing dimension")
	}
	p = fixtureProfile()
	p.ContextLimit = p.InputTokenUpperBound - 1
	if p.Validate() == nil {
		t.Fatal("accepted input bound exceeding context")
	}
}

func TestFunctionConversationAndTextParts(t *testing.T) {
	raw := `{"model":"public-model","messages":[{"role":"user","content":[{"type":"text","text":"weather"}]},{"role":"assistant","content":null,"tool_calls":[{"id":"call1","type":"function","function":{"name":"weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call1","content":"sunny"}]}`
	if _, e := ParseChat([]byte(raw), fixtureProfile()); e != nil {
		t.Fatal(e)
	}
	p := fixtureProfile()
	p.SupportsTools = false
	if _, e := ParseChat([]byte(raw), p); e == nil {
		t.Fatal("tool conversation bypassed profile")
	}
}

func TestUsageExistenceRelationsAndSnapshots(t *testing.T) {
	for name, raw := range map[string]string{"missing": "", "null": "null", "zero": `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`, "fraction": `{"prompt_tokens":1.5,"completion_tokens":0,"total_tokens":1}`, "negative": `{"prompt_tokens":-1,"completion_tokens":0,"total_tokens":-1}`, "overflow": `{"prompt_tokens":9223372036854775807,"completion_tokens":1,"total_tokens":0}`, "cached": `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"prompt_tokens_details":{"cached_tokens":2}}`, "reasoning": `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"completion_tokens_details":{"reasoning_tokens":2}}`, "sum": `{"prompt_tokens":1,"completion_tokens":1,"total_tokens":0}`} {
		t.Run(name, func(t *testing.T) {
			var tr usageTracker
			tr.observe([]byte(raw))
			u := tr.finalize()
			want := UsageConflicting
			if name == "missing" || name == "null" {
				want = UsageMissing
			}
			if name == "zero" {
				want = UsageConfirmed
			}
			if u.Status != want {
				t.Fatalf("status=%s want=%s", u.Status, want)
			}
		})
	}
	const one = `{"prompt_tokens":4,"completion_tokens":1,"total_tokens":5}`
	const two = `{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}`
	var final usageTracker
	final.observe([]byte(one))
	final.observe([]byte(one))
	if u := final.finalize(); u.OutputTokens != 1 || u.Status != UsageConfirmed {
		t.Fatal(u)
	}
	final.observe([]byte(two))
	if final.finalize().Status != UsageConflicting {
		t.Fatal("conflicting final snapshots accepted")
	}
	cumulative := usageTracker{semantics: "cumulative"}
	cumulative.observe([]byte(one))
	cumulative.observe([]byte(two))
	if u := cumulative.finalize(); u.OutputTokens != 2 || u.TotalTokens != 6 || u.Status != UsageConfirmed {
		t.Fatal(u)
	}
	cumulative.observe([]byte(one))
	if cumulative.finalize().Status != UsageConflicting {
		t.Fatal("decreasing cumulative snapshot accepted")
	}
}

func TestRequestSizeAndUnknownUsageDimension(t *testing.T) {
	raw := append([]byte(`{"model":"public-model","messages":[{"role":"user","content":"`), make([]byte, MaxRequestBytes)...)
	if _, e := ParseChat(raw, fixtureProfile()); e == nil {
		t.Fatal("oversized request accepted")
	}
	u, _ := parseUsage([]byte(`{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"extra_charged_tokens":1}`))
	if u.Status != UsageConflicting {
		t.Fatal("unmodeled usage dimension accepted", u)
	}
}
