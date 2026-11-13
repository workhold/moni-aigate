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

func TestDeepSeekCacheUsageNormalizesOnlyCompleteCacheEvidence(t *testing.T) {
	raw := []byte(`{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4},"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6}`)
	var tracker usageTracker
	tracker.observe(raw)
	u := tracker.finalize()
	if u.Status != UsageConfirmed || u.InputTokens != 10 || u.OutputTokens != 3 || u.TotalTokens != 13 || u.CachedTokens != 4 {
		t.Fatalf("DeepSeek usage was not normalized: %+v", u)
	}
	normalized, err := UsageJSON(u)
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &output); err != nil {
		t.Fatal(err)
	}
	if output["prompt_cache_hit_tokens"] != nil || output["prompt_cache_miss_tokens"] != nil || string(output["prompt_tokens_details"]) != `{"cached_tokens":4}` {
		t.Fatalf("usage was not normalized to the common contract: %s", normalized)
	}

	for name, raw := range map[string]string{
		"missing_miss":    `{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_cache_hit_tokens":4}`,
		"missing_hit":     `{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_cache_miss_tokens":6}`,
		"cache_mismatch":  `{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":5}`,
		"detail_mismatch": `{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":3},"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6}`,
	} {
		t.Run(name, func(t *testing.T) {
			var tracker usageTracker
			tracker.observe([]byte(raw))
			u := tracker.finalize()
			if name == "cache_mismatch" || name == "detail_mismatch" {
				if u.Status != UsageConflicting {
					t.Fatalf("inconsistent cache evidence was accepted: %+v", u)
				}
				return
			}
			if u.Status == UsageConfirmed {
				t.Fatalf("partial cache evidence was charged: %+v", u)
			}
			if _, err := UsageJSON(u); err == nil {
				t.Fatal("partial cache evidence crossed the settlement boundary")
			}
		})
	}

	var detailOnly usageTracker
	detailOnly.observe([]byte(`{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":4}}`))
	if u := detailOnly.finalize(); u.Status != UsageConfirmed || u.CachedTokens != 4 {
		t.Fatalf("standard cached_tokens contract changed: %+v", u)
	}
}

func TestChatUsageVendorExtensions(t *testing.T) {
	for name, tc := range map[string]struct {
		raw    string
		status UsageStatus
		cached int64
	}{
		"deepseek_partition":      {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4}`, UsageConfirmed, 6},
		"deepseek_all_miss":       {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":0,"prompt_cache_miss_tokens":10}`, UsageConfirmed, 0},
		"deepseek_bad_sum":        {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":5}`, UsageConflicting, 0},
		"deepseek_negative":       {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":-1,"prompt_cache_miss_tokens":11}`, UsageConflicting, 0},
		"deepseek_hit_over":       {`{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6,"prompt_cache_hit_tokens":6}`, UsageConflicting, 0},
		"details_agree":           {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4,"prompt_tokens_details":{"cached_tokens":6}}`, UsageConfirmed, 6},
		"details_disagree":        {`{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4,"prompt_tokens_details":{"cached_tokens":5}}`, UsageConflicting, 0},
		"metadata_cost":           {`{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"cost":0.001,"is_byok":false}`, UsageConfirmed, 0},
		"unknown_dimension":       {`{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"search_units":1}`, UsageConflicting, 0},
		"duplicate_hit_key":       {`{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"prompt_cache_hit_tokens":1,"prompt_cache_hit_tokens":1}`, UsageConflicting, 0},
		"unknown_details_charged": {`{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"prompt_tokens_details":{"audio_tokens":1}}`, UsageConflicting, 0},
	} {
		t.Run(name, func(t *testing.T) {
			var tr usageTracker
			tr.observe([]byte(tc.raw))
			u := tr.finalize()
			if u.Status != tc.status || (tc.status == UsageConfirmed && u.CachedTokens != tc.cached) {
				t.Fatalf("status=%s cached=%d want %s/%d", u.Status, u.CachedTokens, tc.status, tc.cached)
			}
		})
	}
}
