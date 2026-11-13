package mockupstream

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, allowDirectives bool) *httptest.Server {
	t.Helper()
	service, err := New(Config{Candidates: map[string]Candidate{
		"a": {Secret: "a-secret", Models: []string{"qa-text"}, OutputTokens: 8},
		"b": {Secret: "b-secret", Models: []string{"qa-text"}, OutputTokens: 8},
	}}, Options{AllowDirectives: allowDirectives, Now: func() time.Time { return time.Unix(1700000000, 0) }})
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(service.Handler())
}

func post(t *testing.T, client *http.Client, url, body, authHeader, authValue string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(authHeader, authValue)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, data
}

func TestAllSixProtocolsReturnDeterministicJSON(t *testing.T) {
	srv := testServer(t, true)
	defer srv.Close()
	cases := []struct {
		name, path, body, header string
	}{
		{"chat", "/c/a/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2}`, "Authorization"},
		{"responses", "/c/a/v1/responses", `{"model":"qa-text","input":"hello","max_output_tokens":2,"store":false}`, "Authorization"},
		{"messages", "/c/a/v1/messages", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2}`, "x-api-key"},
		{"embeddings", "/c/a/v1/embeddings", `{"model":"qa-text","input":["hello","world"],"dimensions":2}`, "Authorization"},
		{"rerank", "/c/a/v1/rerank", `{"model":"qa-text","query":"hello","documents":["a","b"]}`, "Authorization"},
		{"gemini", "/c/a/v1beta/models/qa-text:generateContent", `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2}}`, "x-goog-api-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := "Bearer a-secret"
			if tc.header == "x-api-key" {
				value = "a-secret"
			} else if tc.header == "x-goog-api-key" {
				value = "a-secret"
			}
			resp, data := post(t, srv.Client(), srv.URL+tc.path, tc.body, tc.header, value)
			if resp.StatusCode != http.StatusOK || resp.Header.Get("X-Moni-Mock") != "1" {
				t.Fatalf("status/header = %d/%q, body=%s", resp.StatusCode, resp.Header.Get("X-Moni-Mock"), data)
			}
			var object map[string]any
			if err := json.Unmarshal(data, &object); err != nil {
				t.Fatal(err)
			}
			if tc.name == "chat" && object["choices"] == nil || tc.name == "responses" && object["output"] == nil || tc.name == "messages" && object["content"] == nil || tc.name == "embeddings" && object["data"] == nil || tc.name == "rerank" && object["results"] == nil || tc.name == "gemini" && object["candidates"] == nil {
				t.Fatalf("protocol result missing: %s", data)
			}
		})
	}
}

func TestStreamingProtocolsUseNativeTerminalEvents(t *testing.T) {
	srv := testServer(t, true)
	defer srv.Close()
	cases := []struct {
		name, path, body, header string
	}{
		{"chat", "/c/a/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":true}`, "Authorization"},
		{"responses", "/c/a/v1/responses", `{"model":"qa-text","input":"hello","max_output_tokens":2,"store":false,"stream":true}`, "Authorization"},
		{"messages", "/c/a/v1/messages", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"max_tokens":2,"stream":true}`, "x-api-key"},
		{"gemini", "/c/a/v1beta/models/qa-text:streamGenerateContent?alt=sse", `{"contents":[{"parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":2}}`, "x-goog-api-key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := "Bearer a-secret"
			if tc.header != "Authorization" {
				value = "a-secret"
			}
			resp, data := post(t, srv.Client(), srv.URL+tc.path, tc.body, tc.header, value)
			if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
				t.Fatalf("status/content type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			text := string(data)
			markers := map[string]string{"chat": "[DONE]", "responses": "response.completed", "messages": "message_stop", "gemini": `"finishReason":"STOP"`}
			if marker := markers[tc.name]; !strings.Contains(text, marker) {
				t.Fatalf("stream missing %q: %s", marker, text)
			}
		})
	}
}

func TestDirectivesReceiptsAndFaults(t *testing.T) {
	srv := testServer(t, true)
	defer srv.Close()
	body := `{"model":"qa-text","messages":[{"role":"user","content":"hello [[mock:nonce=case-1]] [[mock:fault=reject_429]]"}]}`
	resp, _ := post(t, srv.Client(), srv.URL+"/c/a/v1/chat/completions", body, "Authorization", "Bearer a-secret")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("fault status = %d", resp.StatusCode)
	}
	receipts, err := srv.Client().Get(srv.URL + "/__mock/receipts?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer receipts.Body.Close()
	var result []Receipt
	if err := json.NewDecoder(receipts.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Nonce != "case-1" || result[0].Fault != "reject_429" || !result[0].Completed {
		t.Fatalf("unexpected receipt: %#v", result)
	}

	missing, data := post(t, srv.Client(), srv.URL+"/c/b/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"[[mock:fault=missing_usage]] hello"}]}`, "Authorization", "Bearer b-secret")
	if missing.StatusCode != http.StatusOK || !strings.Contains(string(data), `"usage":null`) {
		t.Fatalf("missing usage response = %d %s", missing.StatusCode, data)
	}
}

func TestSecurityAndDropFault(t *testing.T) {
	srv := testServer(t, true)
	defer srv.Close()
	resp, _ := post(t, srv.Client(), srv.URL+"/c/a/v1/chat/completions", `{"model":"qa-text","messages":[{"role":"user","content":"hello"}]}`, "Authorization", "Bearer wrong")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong secret status = %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/c/a/v1/chat/completions", strings.NewReader(`{"model":"qa-text","messages":[{"role":"user","content":"[[mock:fault=drop_after_headers]]"}]}`))
	req.Header.Set("Authorization", "Bearer a-secret")
	resp, err := srv.Client().Do(req)
	if err == nil {
		_, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil {
			t.Fatal("drop_after_headers unexpectedly returned a complete response")
		}
	}
}

func TestSSEIsFrameDelimited(t *testing.T) {
	srv := testServer(t, false)
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/c/a/v1/chat/completions", strings.NewReader(`{"model":"qa-text","messages":[{"role":"user","content":"hello"}],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer a-secret")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if lines < 6 {
		t.Fatalf("too few SSE lines: %d", lines)
	}
}
