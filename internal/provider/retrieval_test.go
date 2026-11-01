package provider

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func retrievalProfile(protocol string) Profile {
	return Profile{Protocol: protocol, Version: "retrieval-v1", Model: "public-model", InputTokenUpperBound: 8192, ContextLimit: 8192}
}

func retrievalRequest(t *testing.T, protocol, raw string) *Request {
	t.Helper()
	r, err := Parse(protocol, []byte(raw), retrievalProfile(protocol))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func decodeRetrievalFixture(request *Request, body string) ([]byte, *Response, error) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Response{body: io.NopCloser(strings.NewReader(body)), ctx: ctx, cancel: cancel, profile: request.Profile(), model: request.Model, retrieval: request.retrieval.clone(), Usage: Usage{Status: UsageMissing}}
	data, err := r.DecodeJSON()
	return data, r, err
}

const embeddingFixture = `{"object":"list","model":"upstream-model","data":[{"object":"embedding","index":0,"embedding":[0.25,-0.5]}],"usage":{"prompt_tokens":10,"total_tokens":10}}`
const rerankFixture = `{"id":"ranking-1","model":"upstream-model","results":[{"index":1,"relevance_score":0.9,"document":{"text":"secret-canary","debug":"secret-canary"}},{"index":0,"relevance_score":0.2}],"usage":{"total_tokens":10}}`
const embeddingRequest = `{"model":"public-model","input":"hello"}`
const rerankRequest = `{"model":"public-model","query":"hello","documents":["original zero","original one"]}`

func TestRetrievalProfileKeepsGenerationOutputRequired(t *testing.T) {
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		p := retrievalProfile(protocol)
		if !p.InputOnly() || p.Validate() != nil {
			t.Fatal("input-only profile rejected", protocol)
		}
		for name, change := range map[string]func(*Profile){
			"positive_output":   func(p *Profile) { p.OutputLimit = 1 },
			"negative_output":   func(p *Profile) { p.OutputLimit = -1 },
			"default_output":    func(p *Profile) { p.DefaultOutputLimit = 1 },
			"output_field":      func(p *Profile) { p.OutputLimitField = "max_tokens" },
			"tools":             func(p *Profile) { p.SupportsTools = true },
			"parallel":          func(p *Profile) { p.SupportsParallelToolCalls = true },
			"input_capacity":    func(p *Profile) { p.ContextLimit = p.InputTokenUpperBound - 1 },
			"unknown_parameter": func(p *Profile) { p.AllowedParameters = []string{"temperature"} },
		} {
			t.Run(protocol+"/"+name, func(t *testing.T) {
				p := retrievalProfile(protocol)
				change(&p)
				if p.Validate() == nil {
					t.Fatal("invalid retrieval profile accepted")
				}
			})
		}
	}
	for _, protocol := range []string{ProtocolChat, ProtocolResponses, ProtocolMessages, "unknown"} {
		p := retrievalProfile(protocol)
		if p.InputOnly() || p.Validate() == nil {
			t.Fatal("zero output bypassed generation profile", protocol)
		}
	}
}

func TestRetrievalRequestsCaptureShapeAndOnlyReplaceModel(t *testing.T) {
	for name, tc := range map[string]struct {
		protocol, raw, suffix string
		count                 int
	}{
		"text":            {ProtocolEmbeddings, embeddingRequest, "/embeddings", 1},
		"texts":           {ProtocolEmbeddings, `{"model":"public-model","input":["hello","world"],"encoding_format":"float","dimensions":2,"user":"caller"}`, "/embeddings", 2},
		"tokens":          {ProtocolEmbeddings, `{"model":"public-model","input":[0,1,2147483647]}`, "/embeddings", 1},
		"token_batches":   {ProtocolEmbeddings, `{"model":"public-model","input":[[0,1],[2]],"encoding_format":"base64"}`, "/embeddings", 2},
		"rerank_defaults": {ProtocolRerank, rerankRequest, "/rerank", 2},
		"rerank_top":      {ProtocolRerank, `{"model":"public-model","query":"q","documents":["a","b"],"top_n":1,"return_documents":true}`, "/rerank", 2},
	} {
		t.Run(name, func(t *testing.T) {
			r := retrievalRequest(t, tc.protocol, tc.raw)
			if r.Stream || r.MaxOutputTokens != 0 || r.InputUpperBound != 8192 || r.retrieval.count != tc.count || r.EndpointSuffix() != tc.suffix {
				t.Fatal("wrong retrieval contract", r)
			}
			r.Stream, r.MaxOutputTokens = true, 9999
			payload, err := r.UpstreamJSON("private-model")
			if err != nil {
				t.Fatal(err)
			}
			got, _ := object(payload)
			original, _ := object([]byte(tc.raw))
			if len(got) != len(original) || !stringValue(got["model"], "private-model") {
				t.Fatal("retrieval request changed fields", string(payload))
			}
			for key, raw := range original {
				if key != "model" && string(got[key]) != string(raw) {
					t.Fatal("retrieval request changed input", key)
				}
			}
		})
	}
}

func TestRetrievalRequestBoundariesAndStrictFields(t *testing.T) {
	for _, input := range []string{`null`, `""`, `[]`, `[""]`, `["a",1]`, `[1,"a"]`, `[-1]`, `[2147483648]`, `[1.5]`, `[1e0]`, `[null]`, `[true]`, `[[]]`, `[[1],[]]`, `[[1],[null]]`, `[[[1]]]`} {
		if _, err := Parse(ProtocolEmbeddings, []byte(`{"model":"public-model","input":`+input+`}`), retrievalProfile(ProtocolEmbeddings)); err == nil {
			t.Fatal("invalid input accepted", input)
		}
	}
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		raw := embeddingRequest
		if protocol == ProtocolRerank {
			raw = rerankRequest
		}
		for _, field := range []string{`"stream":false`, `"stream":true`, `"tools":[]`, `"max_tokens":0`, `"max_output_tokens":0`, `"max_completion_tokens":0`, `"unknown":1`, `"model":"public-model"`} {
			candidate := strings.TrimSuffix(raw, "}") + "," + field + "}"
			if _, err := Parse(protocol, []byte(candidate), retrievalProfile(protocol)); err == nil {
				t.Fatal("unsupported or duplicate field accepted", candidate)
			}
		}
		if _, err := Parse(ProtocolChat, []byte(raw), retrievalProfile(protocol)); err == nil {
			t.Fatal("wrong protocol accepted")
		}
	}
	for _, field := range []string{`"encoding_format":"bad"`, `"encoding_format":null`, `"dimensions":0`, `"dimensions":8193`, `"dimensions":1.5`, `"dimensions":null`, `"user":""`, `"user":null`, `"user":"` + strings.Repeat("x", 257) + `"`} {
		if _, err := Parse(ProtocolEmbeddings, []byte(strings.TrimSuffix(embeddingRequest, "}")+","+field+"}"), retrievalProfile(ProtocolEmbeddings)); err == nil {
			t.Fatal("invalid embedding option", field)
		}
	}
	for _, suffix := range []string{`"query":""`, `"query":null`, `"documents":[]`, `"documents":[""]`, `"documents":[{"text":"a"}]`, `"documents":[null]`, `"top_n":0`, `"top_n":3`, `"top_n":1.0`, `"return_documents":null`, `"return_documents":1`} {
		f, _ := object([]byte(rerankRequest))
		option, _ := object([]byte("{" + suffix + "}"))
		for k, v := range option {
			f[k] = v
		}
		raw, _ := json.Marshal(f)
		if _, err := Parse(ProtocolRerank, raw, retrievalProfile(ProtocolRerank)); err == nil {
			t.Fatal("invalid rerank option", suffix)
		}
	}
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		values := make([]string, 128)
		for i := range values {
			values[i] = "x"
		}
		f := map[string]any{"model": "public-model"}
		key := "input"
		if protocol == ProtocolRerank {
			key = "documents"
			f["query"] = "q"
		}
		f[key] = values
		raw, _ := json.Marshal(f)
		if _, err := Parse(protocol, raw, retrievalProfile(protocol)); err != nil {
			t.Fatal("128 batch rejected", err)
		}
		f[key] = append(values, "x")
		raw, _ = json.Marshal(f)
		if _, err := Parse(protocol, raw, retrievalProfile(protocol)); err == nil {
			t.Fatal("129 batch accepted")
		}
	}
	p := retrievalProfile(ProtocolEmbeddings)
	p.InputTokenUpperBound = 3
	for _, input := range []string{`[1,2,3,4]`, `[[1,2],[3,4]]`} {
		if _, err := Parse(ProtocolEmbeddings, []byte(`{"model":"public-model","input":`+input+`}`), p); err == nil {
			t.Fatal("aggregate token bound bypassed", input)
		}
	}
	if _, err := Parse(ProtocolEmbeddings, []byte(`{"model":"public-model","input":[[1,2],[3]]}`), p); err != nil {
		t.Fatal("exact aggregate bound rejected", err)
	}
	large := []byte(`{"model":"public-model","input":"` + strings.Repeat("a", MaxRequestBytes) + `"}`)
	_, err := Parse(ProtocolEmbeddings, large, retrievalProfile(ProtocolEmbeddings))
	var known *Error
	if !errors.As(err, &known) || known.HTTPStatus != 413 {
		t.Fatal("request size limit lost", err)
	}
}

func TestRetrievalResultsWhitelistAndOriginalDocuments(t *testing.T) {
	r := retrievalRequest(t, ProtocolEmbeddings, `{"model":"public-model","input":["a","b"],"dimensions":2}`)
	body := `{"object":"list","debug":"secret-canary","model":"private-model","data":[{"object":"embedding","index":1,"embedding":[1,2],"secret":"secret-canary"},{"object":"embedding","index":0,"embedding":[3,4]}],"usage":{"prompt_tokens":10,"total_tokens":10}}`
	data, response, err := decodeRetrievalFixture(r, body)
	if err != nil || response.Usage.Status != UsageConfirmed || response.Usage.InputTokens != 10 || response.Usage.OutputTokens != 0 || response.ProtocolOutcome != "completed" || strings.Contains(string(data), "secret-canary") || strings.Contains(string(data), "private-model") {
		t.Fatal("embedding normalization", string(data), response.Usage, err)
	}
	for _, returned := range []bool{false, true} {
		raw := strings.TrimSuffix(rerankRequest, "}") + `,"return_documents":false}`
		if returned {
			raw = strings.Replace(raw, `"return_documents":false`, `"return_documents":true`, 1)
		}
		r := retrievalRequest(t, ProtocolRerank, raw)
		data, response, err = decodeRetrievalFixture(r, strings.TrimSuffix(rerankFixture, "}")+`,"debug":"secret-canary"}`)
		if err != nil || response.Usage.Status != UsageConfirmed || strings.Contains(string(data), "secret-canary") || strings.Contains(string(data), "upstream-model") {
			t.Fatal("rerank normalization", string(data), err)
		}
		var output struct {
			Results []struct {
				Index    int
				Document *struct{ Text string }
			}
		}
		if json.Unmarshal(data, &output) != nil || len(output.Results) != 2 {
			t.Fatal("results missing", string(data))
		}
		if returned {
			if output.Results[0].Document == nil || output.Results[1].Document == nil || output.Results[0].Document.Text != "original one" || output.Results[1].Document.Text != "original zero" {
				t.Fatal("provider document replaced original", string(data))
			}
		} else if output.Results[0].Document != nil || output.Results[1].Document != nil {
			t.Fatal("unsolicited documents returned", string(data))
		}
	}
	request := retrievalRequest(t, ProtocolRerank, strings.TrimSuffix(rerankRequest, "}")+`,"top_n":1,"return_documents":true}`)
	data, _, err = decodeRetrievalFixture(request, `{"results":[{"index":1,"relevance_score":1}],"usage":{"total_tokens":10}}`)
	if err != nil || !strings.Contains(string(data), `"text":"original one"`) {
		t.Fatal("top_n original document result rejected", string(data), err)
	}
}

func float32Vector(values ...float32) string {
	b := make([]byte, len(values)*4)
	for i, n := range values {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(n))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestEmbeddingFloatAndBase64Dimensions(t *testing.T) {
	for _, encoding := range []string{"float", "base64"} {
		for _, n := range []int{1, 8192} {
			values := make([]float32, n)
			var vector any = values
			if encoding == "base64" {
				vector = float32Vector(values...)
			}
			v, _ := json.Marshal(vector)
			dim, ok := embeddingDimensions(v, encoding)
			if !ok || dim != n {
				t.Fatal("valid vector rejected", encoding, n)
			}
		}
	}
	for _, tc := range []struct{ encoding, raw string }{
		{"float", `[]`}, {"float", `null`}, {"float", `[null]`}, {"float", `["1"]`}, {"float", `[true]`}, {"float", `[1e400]`}, {"float", `[[1]]`},
		{"base64", `""`}, {"base64", `"!!!!"`}, {"base64", `"AAAA"`}, {"base64", `"AAAAAA"`}, {"base64", `"AAAAAB=="`}, {"base64", `[1]`},
		{"base64", `"` + float32Vector(float32(math.Inf(1))) + `"`}, {"base64", `"` + float32Vector(float32(math.NaN())) + `"`}, {"base64", `"AAAA\nAA=="`},
	} {
		if _, ok := embeddingDimensions([]byte(tc.raw), tc.encoding); ok {
			t.Fatal("invalid vector accepted", tc)
		}
	}
	tooMany, _ := json.Marshal(make([]float32, 8193))
	if _, ok := embeddingDimensions(tooMany, "float"); ok {
		t.Fatal("oversize float vector accepted")
	}
	b64, _ := json.Marshal(float32Vector(make([]float32, 8193)...))
	if _, ok := embeddingDimensions(b64, "base64"); ok {
		t.Fatal("oversize base64 vector accepted")
	}
	r := retrievalRequest(t, ProtocolEmbeddings, `{"model":"public-model","input":"hello","encoding_format":"base64","dimensions":2}`)
	body := strings.Replace(embeddingFixture, `[0.25,-0.5]`, `"`+float32Vector(.25, -.5)+`"`, 1)
	if _, response, err := decodeRetrievalFixture(r, body); err != nil || response.Usage.Status != UsageConfirmed {
		t.Fatal("base64 response rejected", err)
	}
}

func TestRetrievalInvalidResultStructureNeverConfirms(t *testing.T) {
	for name, tc := range map[string]struct{ protocol, request, body string }{
		"missing_index":          {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"index":0,`, "", 1)},
		"out_of_range":           {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"index":0`, `"index":1`, 1)},
		"negative_index":         {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"index":0`, `"index":-1`, 1)},
		"float_index":            {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"index":0`, `"index":0.0`, 1)},
		"empty_vector":           {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `[0.25,-0.5]`, `[]`, 1)},
		"wrong_encoding":         {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `[0.25,-0.5]`, `"AAAAAA=="`, 1)},
		"wrong_object":           {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"object":"list"`, `"object":"other"`, 1)},
		"wrong_dimension":        {ProtocolEmbeddings, `{"model":"public-model","input":"a","dimensions":3}`, embeddingFixture},
		"duplicate_index":        {ProtocolEmbeddings, `{"model":"public-model","input":["a","b"]}`, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1]},{"object":"embedding","index":0,"embedding":[2]}]}`},
		"mixed_dimensions":       {ProtocolEmbeddings, `{"model":"public-model","input":["a","b"]}`, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[1]},{"object":"embedding","index":1,"embedding":[2,3]}]}`},
		"missing_batch_item":     {ProtocolEmbeddings, `{"model":"public-model","input":["a","b"]}`, embeddingFixture},
		"too_many_results":       {ProtocolRerank, strings.TrimSuffix(rerankRequest, "}") + `,"top_n":1}`, rerankFixture},
		"unordered_score":        {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"relevance_score":0.2`, `"relevance_score":0.95`, 1)},
		"negative_score":         {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"relevance_score":0.2`, `"relevance_score":-0.1`, 1)},
		"oversize_score":         {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"relevance_score":0.9`, `"relevance_score":1.1`, 1)},
		"null_score":             {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"relevance_score":0.9`, `"relevance_score":null`, 1)},
		"duplicate_rerank_index": {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"index":0`, `"index":1`, 1)},
		"invalid_id":             {ProtocolRerank, rerankRequest, strings.Replace(rerankFixture, `"ranking-1"`, `"bad\nid"`, 1)},
		"error_body":             {ProtocolEmbeddings, embeddingRequest, `{"error":{"message":"secret-canary"}}`},
		"duplicate_result_key":   {ProtocolEmbeddings, embeddingRequest, strings.Replace(embeddingFixture, `"index":0`, `"index":0,"index":0`, 1)},
	} {
		t.Run(name, func(t *testing.T) {
			data, r, err := decodeRetrievalFixture(retrievalRequest(t, tc.protocol, tc.request), tc.body)
			if err == nil || data != nil || r.ProtocolOutcome != "failed" || r.Usage.Status == UsageConfirmed || SafeToRetry(err) || strings.Contains(err.Error(), "secret-canary") {
				t.Fatal("invalid result accepted or leaked", string(data), err)
			}
		})
	}
}

func TestRetrievalUsageUncertaintyKeepsCompletedOutcome(t *testing.T) {
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		raw, body := embeddingRequest, embeddingFixture
		old := `{"prompt_tokens":10,"total_tokens":10}`
		if protocol == ProtocolRerank {
			raw, body = rerankRequest, rerankFixture
			old = `{"total_tokens":10}`
		}
		for name, tc := range map[string]struct {
			usage  string
			status UsageStatus
		}{
			"missing":           {`null`, UsageMissing},
			"empty":             {`{}`, UsageMissing},
			"no_total":          {`{"prompt_tokens":10}`, UsageMissing},
			"mismatch":          {`{"prompt_tokens":9,"total_tokens":10}`, UsageConflicting},
			"completion":        {`{"prompt_tokens":10,"total_tokens":10,"completion_tokens":1}`, UsageConflicting},
			"unknown_dimension": {`{"prompt_tokens":10,"total_tokens":10,"search_units":0}`, UsageConflicting},
			"fractional":        {`{"prompt_tokens":10,"total_tokens":1.5}`, UsageConflicting},
			"negative":          {`{"prompt_tokens":-1,"total_tokens":-1}`, UsageConflicting},
			"null_counter":      {`{"prompt_tokens":null,"total_tokens":10}`, UsageConflicting},
			"duplicate_counter": {`{"prompt_tokens":10,"total_tokens":10,"total_tokens":10}`, UsageConflicting},
			"zero":              {`{"prompt_tokens":0,"total_tokens":0,"completion_tokens":0}`, UsageConfirmed},
			"final":             {`{"prompt_tokens":10,"total_tokens":10,"completion_tokens":0}`, UsageConfirmed},
		} {
			t.Run(protocol+"/"+name, func(t *testing.T) {
				data, r, err := decodeRetrievalFixture(retrievalRequest(t, protocol, raw), strings.Replace(body, old, tc.usage, 1))
				if err != nil || r.ProtocolOutcome != "completed" || r.TransportEndReason != "complete_body" || r.Usage.Status != tc.status {
					t.Fatal("usage changed protocol outcome", r.Usage, err)
				}
				m, _ := object(data)
				if tc.status != UsageConfirmed && m["usage"] != nil {
					t.Fatal("untrusted usage forwarded", string(data))
				}
				if tc.status == UsageConfirmed && (r.Usage.OutputTokens != 0 || r.Usage.TotalTokens != r.Usage.InputTokens) {
					t.Fatal("non-input billing", r.Usage)
				}
			})
		}
		for _, candidate := range []string{strings.Replace(body, `,"usage":`+old, ``, 1), strings.TrimSuffix(body, "}") + `,"usage":` + old + `}`} {
			_, r, err := decodeRetrievalFixture(retrievalRequest(t, protocol, raw), candidate)
			if err != nil || r.ProtocolOutcome != "completed" || r.Usage.Status == UsageConfirmed {
				t.Fatal("missing/duplicate root usage released or confirmed", r.Usage, err)
			}
		}
	}
	if u := inputOnlyUsage([]byte(`{"total_tokens":10}`), ProtocolEmbeddings); u.Status != UsageMissing {
		t.Fatal("embedding prompt invented", u)
	}
	if u := inputOnlyUsage([]byte(`{"total_tokens":10}`), ProtocolRerank); u.Status != UsageConfirmed || u.InputTokens != 10 {
		t.Fatal("Jina total rejected", u)
	}
}

func TestRetrievalHTTPContractSinglePost(t *testing.T) {
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			raw, wire := embeddingRequest, embeddingFixture
			if protocol == ProtocolRerank {
				raw, wire = strings.TrimSuffix(rerankRequest, "}")+`,"return_documents":true}`, rerankFixture
			}
			request := retrievalRequest(t, protocol, raw)
			request.Model = "mutable-canary"
			var posts atomic.Int64
			seen := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				body, _ := io.ReadAll(r.Body)
				m, err := object(body)
				if err != nil || r.URL.Path != "/v1"+request.EndpointSuffix() || r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer secret-canary" || r.Header.Get("Accept") != "application/json" || !stringValue(m["model"], "provider-model") || m["stream"] != nil || m["max_tokens"] != nil {
					seen <- "invalid HTTP request"
				} else {
					seen <- ""
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, wire)
			}))
			defer srv.Close()
			ctx, cancel := testContext(t)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL+"/v1"+request.EndpointSuffix()), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(request.retrieval.documents) > 0 {
				request.retrieval.documents[1] = "mutable-canary"
			}
			data, err := r.DecodeJSON()
			if err != nil || r.ProtocolOutcome != "completed" || r.Usage.Status != UsageConfirmed || posts.Load() != 1 || strings.Contains(string(data), "secret-canary") || strings.Contains(string(data), "mutable-canary") || !strings.Contains(string(data), `"model":"public-model"`) {
				t.Fatal("HTTP contract", string(data), err)
			}
			if err := <-seen; err != "" {
				t.Fatal(err)
			}
			if _, err := r.ReadEvent(); err == nil {
				t.Fatal("retrieval streaming accepted")
			}
		})
	}
}

func TestRetrievalHTTPBodyCancellationAndResponseLimit(t *testing.T) {
	for _, protocol := range []string{ProtocolEmbeddings, ProtocolRerank} {
		t.Run(protocol, func(t *testing.T) {
			raw := embeddingRequest
			if protocol == ProtocolRerank {
				raw = rerankRequest
			}
			var posts atomic.Int64
			started := make(chan struct{})
			socketCancelled := make(chan struct{})
			releaseHandler := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				// HTTP/1 starts background disconnect detection after the request
				// body is consumed. Waiting before draining it hides socket closure.
				_, _ = io.Copy(io.Discard, r.Body)
				_ = r.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-r.Context().Done():
					close(socketCancelled)
				case <-releaseHandler:
				}
			}))
			defer func() {
				close(releaseHandler)
				srv.Close()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, err := NewClient(ClientOptions{}).Do(ctx, fixtureEndpoint(t, srv.URL), retrievalRequest(t, protocol, raw))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("retrieval handler did not start")
			}
			cancel()
			_, err = r.DecodeJSON()
			var known *Error
			if !errors.As(err, &known) || known.Class != ClassCancelled || r.ProtocolOutcome != "failed" || posts.Load() != 1 || SafeToRetry(err) {
				t.Fatal("cancelled retrieval replayed or completed", err)
			}
			select {
			case <-socketCancelled:
			case <-time.After(2 * time.Second):
				t.Fatal("retrieval cancellation did not close the upstream socket")
			}
			_, response, err := decodeRetrievalFixture(retrievalRequest(t, protocol, raw), strings.Repeat(" ", MaxResponseBytes+1))
			if !errors.As(err, &known) || known.Code != "upstream_response_too_large" || response.ProtocolOutcome != "failed" {
				t.Fatal("response limit", err)
			}
		})
	}
}
