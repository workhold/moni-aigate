package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

type geminiState struct {
	finish        string
	terminal      map[string]json.RawMessage
	usage         usageTracker
	finalUsage    bool
	unpriced      bool
	candidates    int64
	hasCandidates bool
}

func (r *Response) decodeGeminiJSON() ([]byte, error) {
	if r.stream {
		return nil, protocolError("expected_non_stream")
	}
	defer r.Close()
	body, err := io.ReadAll(io.LimitReader(&observedReader{response: r}, MaxResponseBytes+1))
	if err != nil {
		return nil, r.readFailure(err)
	}
	if len(body) > MaxResponseBytes {
		return nil, r.readFailure(protocolError("upstream_response_too_large"))
	}
	m, err := geminiResponseObject(body)
	if err != nil {
		return nil, r.readFailure(protocolError("upstream_invalid_json"))
	}
	clean, finish, _, _, err := r.normalizeGemini(m, false)
	if err != nil {
		return nil, r.readFailure(err)
	}
	r.gemini.finish = finish
	r.FinishReason = geminiFinish(finish)
	r.observeGeminiUsage(m["usageMetadata"])
	r.finishGeminiUsage()
	r.ProtocolOutcome, r.TransportEndReason = "completed", "complete_body"
	if r.Usage.Status == UsageConfirmed {
		clean["usageMetadata"] = geminiUsageJSON(r.Usage)
	}
	return json.Marshal(clean)
}

// Gemini has no [DONE] frame. A validated finish must be followed by a clean
// frame-boundary EOF; the final usage may arrive in a later usage-only frame.
func (r *Response) readGeminiEvent() (*Event, error) {
	if !r.stream {
		return nil, protocolError("expected_stream")
	}
	if r.done {
		return nil, io.EOF
	}
	fail := func(err error) (*Event, error) {
		err = r.readFailure(err)
		_ = r.Close()
		return nil, err
	}
	typ, data, err := r.frame()
	if err != nil {
		if errors.Is(err, io.EOF) && r.gemini.finish != "" && r.ctx.Err() == nil && r.timeoutCode.Load() == nil {
			r.finishGeminiUsage()
			r.done, r.ProtocolOutcome, r.TransportEndReason = true, "completed", "complete_stream"
			if r.Usage.Status == UsageConfirmed {
				r.gemini.terminal["usageMetadata"] = geminiUsageJSON(r.Usage)
			}
			terminal := &Event{Kind: "done", Data: encoded(r.gemini.terminal), Usage: r.Usage, FinishReason: r.FinishReason, Progress: true}
			_ = r.Close()
			return terminal, nil
		}
		return fail(err)
	}
	if typ != "" && typ != "message" {
		return fail(protocolError("upstream_invalid_event"))
	}
	if len(data) == 0 {
		return &Event{Kind: "heartbeat", Usage: r.Usage}, nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return fail(protocolError("upstream_invalid_event"))
	}
	m, err := geminiResponseObject(data)
	if err != nil {
		return fail(protocolError("upstream_invalid_json"))
	}
	clean, finish, visible, hasParts, err := r.normalizeGemini(m, true)
	if err != nil {
		return fail(err)
	}
	if r.gemini.finish != "" && hasParts {
		return fail(protocolError("upstream_content_after_finish"))
	}
	if finish != "" {
		if r.gemini.finish != "" && r.gemini.finish != finish {
			return fail(protocolError("upstream_conflicting_finish"))
		}
		r.gemini.finish, r.FinishReason = finish, geminiFinish(finish)
		terminal := nativeClean(clean, "modelVersion", "responseId")
		terminal["candidates"] = encoded([]any{map[string]any{"index": 0, "finishReason": finish}})
		r.gemini.terminal = terminal
	}
	before := r.Usage
	r.observeGeminiUsage(m["usageMetadata"])
	progress := visible || r.Usage.Status == UsageProvisional && (r.Usage.InputTokens > before.InputTokens || r.Usage.OutputTokens > before.OutputTokens)
	r.protocolEvent(progress)
	if !visible {
		return &Event{Kind: "usage", Usage: r.Usage, FinishReason: r.FinishReason, Progress: progress}, nil
	}
	// Never leak an early finish or provisional usage before the caller's
	// financial commit. Content sharing its frame is emitted exactly once.
	var candidates []map[string]json.RawMessage
	_ = json.Unmarshal(clean["candidates"], &candidates)
	delete(candidates[0], "finishReason")
	clean["candidates"] = encoded(candidates)
	delete(clean, "usageMetadata")
	return &Event{Kind: "chunk", Data: encoded(clean), Content: true, Progress: progress, Usage: r.Usage}, nil
}

func (r *Response) observeGeminiUsage(raw []byte) {
	if len(raw) == 0 {
		return
	}
	translated := geminiUsage(raw)
	u, complete := parseUsage(translated)
	// Aggregating visible candidates with hidden thoughts must not hide a
	// regression in the original candidatesTokenCount counter.
	m, _ := object(translated)
	if _, exists := m["completion_tokens"]; exists && u.Status != UsageConflicting {
		candidates := u.OutputTokens - u.ReasoningTokens
		if r.gemini.hasCandidates && candidates < r.gemini.candidates {
			translated, complete = []byte(`{"invalid":1}`), false
		} else {
			r.gemini.candidates, r.gemini.hasCandidates = candidates, true
		}
	}
	r.gemini.usage.semantics = "cumulative"
	r.Usage = r.gemini.usage.observe(translated)
	if r.gemini.finish != "" {
		r.gemini.finalUsage = complete && r.Usage.Status != UsageConflicting
	}
}

func (r *Response) finishGeminiUsage() {
	r.Usage = r.gemini.usage.finalize()
	if r.gemini.unpriced {
		r.Usage.Status = UsageConflicting
		return
	}
	if !r.gemini.finalUsage && r.Usage.Status != UsageConflicting {
		r.Usage.Status = UsageMissing
	}
}

func geminiFinish(value string) string {
	if value == "MAX_TOKENS" {
		return "length"
	}
	return "stop"
}

func (r *Response) normalizeGemini(m map[string]json.RawMessage, stream bool) (map[string]json.RawMessage, string, bool, bool, error) {
	bad := func() (map[string]json.RawMessage, string, bool, bool, error) {
		return nil, "", false, false, protocolError("upstream_invalid_gemini_output")
	}
	if _, exists := m["error"]; exists {
		return nil, "", false, false, protocolError("upstream_error")
	}
	for _, key := range []string{"groundingMetadata", "urlContextMetadata", "groundingAttributions"} {
		if raw, exists := m[key]; exists && !isNull(raw) {
			r.gemini.unpriced = true
		}
	}
	if raw, exists := m["promptFeedback"]; exists {
		feedback, err := object(raw)
		if err != nil {
			return bad()
		}
		if reason, exists := feedback["blockReason"]; exists && !stringValue(reason, "BLOCK_REASON_UNSPECIFIED") {
			return nil, "", false, false, protocolError("upstream_blocked")
		}
	}
	clean := map[string]json.RawMessage{"modelVersion": encoded(r.profile.Model)}
	if raw, exists := m["responseId"]; exists {
		var id string
		if !isString(raw) || json.Unmarshal(raw, &id) != nil || !retrievalID(id) {
			return bad()
		}
		clean["responseId"] = raw
	}
	var candidates []json.RawMessage
	if raw, exists := m["candidates"]; exists {
		if json.Unmarshal(raw, &candidates) != nil || candidates == nil || len(candidates) > 1 {
			return bad()
		}
	}
	if len(candidates) == 0 {
		if !stream || m["usageMetadata"] == nil {
			return bad()
		}
		clean["candidates"] = json.RawMessage(`[]`)
		return clean, "", false, false, nil
	}
	candidate, err := object(candidates[0])
	if err != nil {
		return bad()
	}
	for _, key := range []string{"groundingMetadata", "urlContextMetadata", "groundingAttributions"} {
		if raw, exists := candidate[key]; exists && !isNull(raw) {
			r.gemini.unpriced = true
		}
	}
	if raw, exists := candidate["index"]; exists {
		if value, valid := integer(raw); !valid || value != 0 {
			return bad()
		}
	}
	finish := ""
	if raw, exists := candidate["finishReason"]; exists {
		if !stringValue(raw, "STOP") && !stringValue(raw, "MAX_TOKENS") {
			return nil, "", false, false, protocolError("upstream_unsupported_finish")
		}
		_ = json.Unmarshal(raw, &finish)
	}
	if !stream && finish == "" {
		return bad()
	}
	out := map[string]json.RawMessage{"index": json.RawMessage(`0`)}
	visible, hasParts := false, false
	if raw, exists := candidate["content"]; exists {
		content, err := object(raw)
		if err != nil || !only(content, "role", "parts") || !stringValue(content["role"], "model") {
			return bad()
		}
		var parts []json.RawMessage
		if json.Unmarshal(content["parts"], &parts) != nil || parts == nil || len(parts) > 128 {
			return bad()
		}
		hasParts = len(parts) > 0
		publicParts := []map[string]string{}
		for _, raw := range parts {
			part, err := object(raw)
			if err != nil || !only(part, "text", "thought", "thoughtSignature") || !isString(part["text"]) {
				return bad()
			}
			thought := false
			if raw, exists := part["thought"]; exists && !boolean(raw, &thought) {
				return bad()
			}
			if raw, exists := part["thoughtSignature"]; exists && !isString(raw) {
				return bad()
			}
			if !thought {
				var text string
				_ = json.Unmarshal(part["text"], &text)
				publicParts = append(publicParts, map[string]string{"text": text})
				visible = visible || text != ""
			}
		}
		out["content"] = encoded(map[string]any{"role": "model", "parts": publicParts})
	} else if finish == "" {
		return bad()
	}
	if finish != "" {
		out["finishReason"] = encoded(finish)
	}
	clean["candidates"] = encoded([]any{out})
	return clean, finish, visible, hasParts, nil
}

// Duplicate or malformed usage is billing uncertainty, not malformed result
// structure. Preserve a completed protocol so the caller keeps the hold pending.
func geminiResponseObject(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, badRequest()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, badRequest()
	}
	m := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, badRequest()
		}
		key, valid := token.(string)
		if !valid {
			return nil, badRequest()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, badRequest()
		}
		if _, exists := m[key]; exists {
			if key != "usageMetadata" {
				return nil, badRequest()
			}
			value = json.RawMessage(`{"duplicate_usage":true}`)
		}
		if key != "usageMetadata" {
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.UseNumber()
			if checkJSON(decoder, 1) != nil {
				return nil, badRequest()
			}
		}
		m[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, badRequest()
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, badRequest()
	}
	return m, nil
}
