package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const geminiBody = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":20}}`
const geminiResult = `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"},{"text":"private-thought","thought":true,"thoughtSignature":"private-signature"}]},"finishReason":"STOP","debug":"private-metadata"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":15,"cachedContentTokenCount":3,"promptTokensDetails":[{"modality":"TEXT","tokenCount":10}],"cacheTokensDetails":[{"modality":"TEXT","tokenCount":3}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":3}],"toolUsePromptTokenCount":0},"modelVersion":"private-upstream","responseId":"gemini_fixture-1","debug":"private-metadata"}`
const geminiUsageFixture = `{"promptTokenCount":10,"candidatesTokenCount":3,"thoughtsTokenCount":2,"totalTokenCount":15,"cachedContentTokenCount":3}`

func geminiProfile() Profile {
	return Profile{Protocol: ProtocolGemini, Version: "gemini-fixture-v1", Model: "public-model", InputTokenUpperBound: 100, ContextLimit: 120, OutputLimit: 20, DefaultOutputLimit: 20, OutputLimitField: "maxOutputTokens", UsageSemantics: "cumulative"}
}

func geminiRequest(t *testing.T, stream bool) *Request {
	t.Helper()
	r, err := ParseGemini([]byte(geminiBody), geminiProfile(), stream)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func geminiResponseFixture(body string, stream bool) *Response {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Response{ctx: ctx, cancel: cancel, body: io.NopCloser(strings.NewReader(body)), profile: geminiProfile(), model: "public-model", stream: stream, Usage: Usage{Status: UsageMissing}}
	if stream {
		r.reader = bufio.NewReaderSize(&observedReader{response: r}, 128)
	}
	return r
}

func geminiFinishFrame(usage bool) string {
	value := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":" world"}]},"finishReason":"STOP"}],"modelVersion":"private-upstream"`
	if usage {
		value += `,"usageMetadata":` + geminiUsageFixture
	}
	return value + `}`
}

func TestGeminiProfilesRequestsAndURLContract(t *testing.T) {
	for _, name := range []string{"a", "gemini-2.5-flash", "gemini_1.5.latest", strings.Repeat("a", 100)} {
		if !ValidGeminiModel(name) {
			t.Fatal("safe model rejected", name)
		}
	}
	for _, name := range []string{"", ".model", "models/a", "../a", "a..b", "a:b", "a%2fb", "a?key=x", "a\\b", "模型", strings.Repeat("a", 101)} {
		if ValidGeminiModel(name) {
			t.Fatal("unsafe model accepted", name)
		}
		if _, err := geminiRequest(t, false).EndpointSuffixForModel(name); err == nil {
			t.Fatal("unsafe binding escaped into endpoint", name)
		}
	}
	for name, change := range map[string]func(*Profile){
		"zero_output":        func(p *Profile) { p.OutputLimit, p.DefaultOutputLimit = 0, 0 },
		"tools":              func(p *Profile) { p.SupportsTools = true },
		"parallel":           func(p *Profile) { p.SupportsParallelToolCalls = true },
		"wrong_output_field": func(p *Profile) { p.OutputLimitField = "max_tokens" },
		"unsafe_model":       func(p *Profile) { p.Model = "unsafe/path" },
		"final_snapshots":    func(p *Profile) { p.UsageSemantics = "final" },
		"unknown_parameter":  func(p *Profile) { p.AllowedParameters = []string{"thinkingConfig"} },
	} {
		t.Run(name, func(t *testing.T) {
			p := geminiProfile()
			change(&p)
			if p.Validate() == nil {
				t.Fatal("invalid Gemini profile accepted")
			}
		})
	}
	for _, stream := range []bool{false, true} {
		r := geminiRequest(t, stream)
		r.Model, r.Stream, r.MaxOutputTokens = "mutable-model", !stream, 9999
		suffix, err := r.EndpointSuffixForModel("provider-model")
		want := "/models/provider-model:generateContent"
		if stream {
			want = "/models/provider-model:streamGenerateContent"
		}
		if err != nil || suffix != want || r.InputUpperBound != 100 || r.Protocol() != ProtocolGemini {
			t.Fatal("native path/bound mismatch", suffix, err)
		}
		body, err := r.UpstreamJSON("provider-model")
		m, parseErr := object(body)
		config, configErr := object(m["generationConfig"])
		if err != nil || parseErr != nil || configErr != nil || !only(m, "contents", "generationConfig") || string(config["maxOutputTokens"]) != "20" || string(config["candidateCount"]) != "1" {
			t.Fatal("native payload was mutated", string(body), err)
		}
	}
	if _, err := Parse(ProtocolGemini, []byte(geminiBody), geminiProfile()); err != nil {
		t.Fatal("default non-stream parser", err)
	}
	p := geminiProfile()
	p.OutputLimitField, p.UsageSemantics = "", ""
	if p.Validate() != nil {
		t.Fatal("profile defaults rejected")
	}
}

func TestGeminiRequestBoundariesRejectBeforeDispatch(t *testing.T) {
	for _, raw := range []string{
		`{"contents":[]}`, `{"contents":null}`, `{"contents":[{"parts":[]}]}`,
		`{"contents":[{"role":"system","parts":[{"text":"hi"}]}]}`,
		`{"contents":[{"parts":[{"text":""}]}]}`,
		`{"contents":[{"parts":[{"text":"hi","inlineData":{}}]}]}`,
		`{"contents":[{"parts":[{"functionCall":{"name":"tool","args":{}}}]}]}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"systemInstruction":{"role":"model","parts":[{"text":"system"}]}}`,
		`{"contents":[{"parts":[{"text":"hi","text":"second"}]}]}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":2}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":0}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":21}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":1.5}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"temperature":2.1}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"topP":null}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"topK":1001}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"stopSequences":[]}}`,
		`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`,
	} {
		if _, err := ParseGemini([]byte(raw), geminiProfile(), false); err == nil {
			t.Fatal("invalid request accepted", raw)
		}
	}
	for _, field := range []string{`"model":"public-model"`, `"stream":false`, `"cachedContent":"cache/id"`, `"tools":[]`, `"safetySettings":[]`, `"generation_config":{}`} {
		raw := strings.TrimSuffix(geminiBody, "}") + "," + field + "}"
		if _, err := ParseGemini([]byte(raw), geminiProfile(), true); err == nil {
			t.Fatal("unknown field accepted", field)
		}
	}
	for _, count := range []int{128, 129} {
		contents := make([]any, count)
		for i := range contents {
			contents[i] = map[string]any{"parts": []any{map[string]string{"text": "hello"}}}
		}
		raw, _ := json.Marshal(map[string]any{"contents": contents})
		_, err := ParseGemini(raw, geminiProfile(), false)
		if (err == nil) != (count == 128) {
			t.Fatal("content count limit", count, err)
		}
	}
	p := geminiProfile()
	p.AllowedParameters = []string{}
	if _, err := ParseGemini([]byte(`{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"temperature":1}}`), p, false); err == nil {
		t.Fatal("optional parameter bypassed profile")
	}
	if _, err := ParseGemini([]byte(`{"contents":[{"parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"help"}]}}`), p, false); err != nil {
		t.Fatal("native role/config defaults rejected", err)
	}
	if _, err := ParseGemini([]byte(strings.Repeat(" ", MaxRequestBytes+1)), geminiProfile(), false); err == nil {
		t.Fatal("request size limit bypassed")
	}
}

func TestGeminiJSONWhitelistAndInclusiveThinkingUsage(t *testing.T) {
	r := geminiResponseFixture(geminiResult, false)
	data, err := r.DecodeJSON()
	if err != nil || r.ProtocolOutcome != "completed" || r.FinishReason != "stop" || r.Usage.Status != UsageConfirmed || r.Usage.InputTokens != 10 || r.Usage.OutputTokens != 5 || r.Usage.TotalTokens != 15 || r.Usage.ReasoningTokens != 2 || r.Usage.CachedTokens != 3 {
		t.Fatal("Gemini usage/finish", string(data), r.Usage, err)
	}
	for _, secret := range []string{"private-thought", "private-signature", "private-metadata", "private-upstream", "promptTokensDetails", "toolUsePromptTokenCount"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private metadata escaped", secret)
		}
	}
	if !strings.Contains(string(data), `"modelVersion":"public-model"`) || !strings.Contains(string(data), `"thoughtsTokenCount":2`) || !strings.Contains(string(data), `"text":"hello"`) {
		t.Fatal("public response", string(data))
	}
}

func TestGeminiUsageUncertaintyKeepsCompletedProtocol(t *testing.T) {
	for name, usage := range map[string]string{
		"missing": "", "null": "null", "incomplete": `{"promptTokenCount":10}`, "conflict": `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":14}`,
		"negative":     `{"promptTokenCount":-1,"candidatesTokenCount":3,"totalTokenCount":2}`,
		"unknown":      `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"searchUnits":1}`,
		"tool":         `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"toolUsePromptTokenCount":1}`,
		"duplicate":    `{"promptTokenCount":10,"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13}`,
		"overflow":     `{"promptTokenCount":10,"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1,"totalTokenCount":1}`,
		"cache_subset": `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"cachedContentTokenCount":11}`,
		"media":        `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"promptTokensDetails":[{"modality":"IMAGE","tokenCount":10}]}`,
		"wrong_detail": `{"promptTokenCount":10,"candidatesTokenCount":3,"totalTokenCount":13,"promptTokensDetails":[{"modality":"TEXT","tokenCount":9}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]json.RawMessage
			_ = json.Unmarshal([]byte(geminiResult), &m)
			if usage == "" {
				delete(m, "usageMetadata")
			} else {
				m["usageMetadata"] = json.RawMessage(usage)
			}
			raw, _ := json.Marshal(m)
			r := geminiResponseFixture(string(raw), false)
			data, err := r.DecodeJSON()
			if err != nil || r.ProtocolOutcome != "completed" || r.Usage.Status == UsageConfirmed || strings.Contains(string(data), `"usageMetadata"`) {
				t.Fatal("uncertain usage was released or confirmed", r.Usage, err)
			}
		})
	}
	duplicate := strings.TrimSuffix(geminiResult, "}") + `,"usageMetadata":` + geminiUsageFixture + `}`
	r := geminiResponseFixture(duplicate, false)
	if _, err := r.DecodeJSON(); err != nil || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConflicting {
		t.Fatal("duplicate root usage lost pending contract", r.Usage, err)
	}
}

func TestGeminiStreamFinishAndTailUsageAreReleasedAfterCleanEOF(t *testing.T) {
	first := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1,"totalTokenCount":11}}`
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		wire := sse(first, ending) + sse(geminiFinishFrame(false), ending) + sse(`{"usageMetadata":`+geminiUsageFixture+`}`, ending) + sse(`{"usageMetadata":`+geminiUsageFixture+`}`, ending)
		r := geminiResponseFixture(wire, true)
		chunks, dones := 0, 0
		var text string
		for {
			event, err := r.ReadEvent()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind == "chunk" {
				chunks++
				text += string(event.Data)
				if strings.Contains(string(event.Data), "finishReason") || strings.Contains(string(event.Data), "usageMetadata") {
					t.Fatal("terminal or usage escaped before settle", string(event.Data))
				}
			}
			if event.Kind == "done" {
				dones++
				if strings.Contains(string(event.Data), "hello") || strings.Contains(string(event.Data), " world") || !strings.Contains(string(event.Data), `"finishReason":"STOP"`) || !strings.Contains(string(event.Data), `"totalTokenCount":15`) {
					t.Fatal("terminal duplicated content/lost usage", string(event.Data))
				}
			}
		}
		if chunks != 2 || dones != 1 || strings.Count(text, `"text":"hello"`) != 1 || strings.Count(text, `"text":" world"`) != 1 || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConfirmed || r.Usage.OutputTokens != 5 || r.Usage.TotalTokens != 15 {
			t.Fatal("stream final quantities", chunks, dones, r.Usage)
		}
	}
}

func TestGeminiStreamUncertainUsageAndInvalidTermination(t *testing.T) {
	early := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}],"usageMetadata":` + geminiUsageFixture + `}`
	for name, wire := range map[string]string{
		"early_only":         sse(early, "\n") + sse(geminiFinishFrame(false), "\n"),
		"missing":            sse(geminiFinishFrame(false), "\n"),
		"counter_regression": sse(early, "\n") + sse(strings.Replace(geminiFinishFrame(true), `"candidatesTokenCount":3`, `"candidatesTokenCount":2`, 1), "\n"),
		"duplicate_final":    sse(strings.TrimSuffix(geminiFinishFrame(true), "}")+`,"usageMetadata":`+geminiUsageFixture+`}`, "\n"),
		"candidate_regression_masked_by_thoughts": sse(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"thoughtsTokenCount":0,"totalTokenCount":15}}`, "\n") + sse(geminiFinishFrame(true), "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			r := geminiResponseFixture(wire, true)
			for {
				event, err := r.ReadEvent()
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == "done" {
					break
				}
			}
			if r.ProtocolOutcome != "completed" || r.Usage.Status == UsageConfirmed {
				t.Fatal("unconfirmed stream was charged", r.Usage)
			}
		})
	}
	for name, wire := range map[string]string{
		"bare_eof":             sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}`, "\n"),
		"partial_tail":         sse(geminiFinishFrame(true), "\n") + `data: {`,
		"done_marker":          sse(geminiFinishFrame(true), "\n") + sse("[DONE]", "\n"),
		"content_after_finish": sse(geminiFinishFrame(true), "\n") + sse(early, "\n"),
		"conflicting_finish":   sse(geminiFinishFrame(true), "\n") + sse(`{"candidates":[{"index":0,"finishReason":"MAX_TOKENS"}]}`, "\n"),
		"late_error":           sse(geminiFinishFrame(true), "\n") + sse(`{"error":{"message":"secret-canary"}}`, "\n"),
		"safety_finish":        sse(`{"candidates":[{"index":0,"finishReason":"SAFETY"}]}`, "\n"),
	} {
		t.Run(name, func(t *testing.T) {
			r := geminiResponseFixture(wire, true)
			var end error
			for i := 0; i < 8; i++ {
				event, err := r.ReadEvent()
				if err != nil {
					end = err
					break
				}
				if event.Kind == "done" {
					t.Fatal("invalid stream emitted done")
				}
			}
			if end == nil || r.ProtocolOutcome != "failed" || SafeToRetry(end) || strings.Contains(end.Error(), "secret-canary") {
				t.Fatal("invalid stream policy", end)
			}
		})
	}
}

func TestGeminiUnpricedHostedToolEvidenceKeepsBillingPending(t *testing.T) {
	for _, field := range []string{"groundingMetadata", "urlContextMetadata", "groundingAttributions"} {
		body := strings.Replace(geminiResult, `"index":0`, `"`+field+`":{"secret":"private-tool-proof"},"index":0`, 1)
		for _, stream := range []bool{false, true} {
			wire := body
			if stream {
				wire = sse(wire, "\n")
			}
			r := geminiResponseFixture(wire, stream)
			var data []byte
			var err error
			if stream {
				for {
					var event *Event
					event, err = r.ReadEvent()
					if err != nil || event.Kind == "done" {
						if event != nil {
							data = event.Data
						}
						break
					}
				}
			} else {
				data, err = r.DecodeJSON()
			}
			if err != nil || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConflicting || strings.Contains(string(data), "private-tool-proof") || strings.Contains(string(data), "usageMetadata") {
				t.Fatal("hosted proof lost pending contract", field, stream, r.Usage, err)
			}
		}
	}
}

func TestGeminiInvalidResultsAndBoundedResponses(t *testing.T) {
	for _, body := range []string{
		`{"candidates":[]}`, `{"candidates":[{"index":1,"finishReason":"STOP"}]}`,
		`{"candidates":[{"index":0,"finishReason":"STOP"},{"index":1,"finishReason":"STOP"}]}`,
		`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{}}]},"finishReason":"STOP"}]}`,
		`{"candidates":[{"content":{"role":"user","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}`,
		`{"candidates":[{"finishReason":"SAFETY"}]}`, `{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`,
		strings.Repeat(" ", MaxResponseBytes+1),
	} {
		r := geminiResponseFixture(body, false)
		if _, err := r.DecodeJSON(); err == nil || r.ProtocolOutcome != "failed" || SafeToRetry(err) {
			t.Fatal("invalid response accepted", err)
		}
	}
	r := geminiResponseFixture(sse(strings.Repeat("x", MaxEventBytes+1), "\n"), true)
	if _, err := r.ReadEvent(); err == nil {
		t.Fatal("event limit bypassed")
	}
}

func TestGeminiHTTPUsesNativePathHeaderAndSinglePost(t *testing.T) {
	for _, stream := range []bool{false, true} {
		request := geminiRequest(t, stream)
		request.Model = "mutable-model"
		suffix, err := request.EndpointSuffixForModel("provider-model")
		if err != nil {
			t.Fatal(err)
		}
		var posts atomic.Int64
		seen := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			posts.Add(1)
			raw, _ := io.ReadAll(r.Body)
			m, e := object(raw)
			query := ""
			if stream {
				query = "alt=sse"
			}
			if e != nil || r.Method != http.MethodPost || r.URL.Path != "/v1beta"+suffix || r.URL.RawQuery != query || r.Header.Get("x-goog-api-key") != "secret-canary" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || m["model"] != nil || m["stream"] != nil {
				seen <- "native path/header/body mismatch"
			} else {
				seen <- ""
			}
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, b := range []byte(sse(geminiResult, "\r\n")) {
					_, _ = w.Write([]byte{b})
					w.(http.Flusher).Flush()
				}
			} else {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, geminiResult)
			}
		}))
		ctx, cancel := testContext(t)
		endpoint := fixtureEndpoint(t, srv.URL+"/v1beta"+suffix)
		r, err := NewClient(ClientOptions{}).Do(ctx, endpoint, request)
		if err != nil {
			cancel()
			srv.Close()
			t.Fatal(err)
		}
		if stream {
			for {
				event, e := r.ReadEvent()
				if e != nil {
					t.Fatal(e)
				}
				if event.Kind == "done" {
					break
				}
			}
		} else {
			if _, err = r.DecodeJSON(); err != nil {
				t.Fatal(err)
			}
		}
		if violation := <-seen; violation != "" {
			t.Fatal(violation)
		}
		if posts.Load() != 1 || r.Usage.Status != UsageConfirmed || r.profile.Model != "public-model" {
			t.Fatal("native call replayed/lost model", posts.Load(), r.Usage)
		}
		endpoint.URL += "?key=secret-canary"
		if _, err := NewClient(ClientOptions{}).Do(ctx, endpoint, request); err == nil || posts.Load() != 1 {
			t.Fatal("configured query key was accepted")
		}
		cancel()
		srv.Close()
	}
}

func TestGeminiCancellationClosesRealSocketWithoutReplay(t *testing.T) {
	var posts atomic.Int64
	closed := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]}}]}`, "\n"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(closed)
		case <-release:
		}
	}))
	defer func() { close(release); srv.Close() }()
	ctx, cancel := testContext(t)
	defer cancel()
	r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), geminiRequest(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if event, err := r.ReadEvent(); err != nil || !event.Content {
		t.Fatal("first content", err)
	}
	cancel()
	_, err = r.ReadEvent()
	var known *Error
	if !errors.As(err, &known) || known.Class != ClassCancelled || r.ProtocolOutcome != "failed" || SafeToRetry(err) || posts.Load() != 1 {
		t.Fatal("cancel policy", err, posts.Load())
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("native cancellation retained socket")
	}
}
