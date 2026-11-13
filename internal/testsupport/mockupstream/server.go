package mockupstream

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Options struct {
	AllowDirectives bool
	Now             func() time.Time
}

type Receipt struct {
	Seq        uint64    `json:"seq"`
	Candidate  string    `json:"candidate"`
	Protocol   string    `json:"protocol"`
	Model      string    `json:"model"`
	Stream     bool      `json:"stream"`
	BodySHA256 string    `json:"body_sha256"`
	Nonce      string    `json:"nonce,omitempty"`
	Fault      string    `json:"fault,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	Completed  bool      `json:"completed"`
}

type Server struct {
	cfg             Config
	allowDirectives bool
	now             func() time.Time
	nextSeq         atomic.Uint64
	mu              sync.RWMutex
	receipts        []Receipt
	completion      map[uint64]int
}

func New(cfg Config, opts Options) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Server{
		cfg:             cfg,
		allowDirectives: opts.AllowDirectives,
		now:             opts.Now,
		completion:      make(map[uint64]int),
	}, nil
}

func (s *Server) Handler() http.Handler { return http.HandlerFunc(s.serveHTTP) }

func (s *Server) Receipts(since uint64) []Receipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Receipt, 0, len(s.receipts))
	for _, receipt := range s.receipts {
		if receipt.Seq > since {
			result = append(result, receipt)
		}
	}
	return result
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" || r.URL.Path == "/health/live" || r.URL.Path == "/health/ready" {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": map[string]string{"type": "method_not_allowed"}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path == "/__mock/receipts" {
		s.serveReceipts(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}

	candidateName, protocol, model, stream, ok := parseEndpoint(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	candidate, exists := s.cfg.Candidates[candidateName]
	if !exists {
		writeError(w, http.StatusNotFound, "unknown_candidate")
		return
	}
	if !authorized(r, protocol, candidate.Secret) {
		writeError(w, http.StatusUnauthorized, "invalid_api_key")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if model == "" {
		if object, ok := document.(map[string]any); ok {
			model, _ = object["model"].(string)
		}
	}
	if !candidate.supportsModel(model) {
		writeError(w, http.StatusNotFound, "model_not_found")
		return
	}
	if protocol != "gemini" {
		if object, ok := document.(map[string]any); ok {
			if requested, exists := object["stream"].(bool); exists {
				stream = requested
			}
		}
	}
	directives := parseDirectives(body)
	faultName := candidate.DefaultFault
	if s.allowDirectives && directives.fault != "" {
		faultName = directives.fault
	}
	fault, err := parseFault(faultName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid_fault_configuration")
		return
	}
	seq := s.recordReceipt(Receipt{
		Candidate:  candidateName,
		Protocol:   protocol,
		Model:      model,
		Stream:     stream,
		BodySHA256: sha256Hex(body),
		Nonce:      directives.nonce,
		Fault:      faultName,
		ReceivedAt: s.now().UTC(),
	})

	if fault.kind == "reject_429" || fault.kind == "reject_429_unlisted" {
		writeError(w, http.StatusTooManyRequests, fault.kind)
		s.complete(seq)
		return
	}
	if fault.kind == "http_500" {
		writeError(w, http.StatusInternalServerError, "upstream_failure")
		s.complete(seq)
		return
	}
	if fault.kind == "drop_after_headers" {
		dropAfterHeaders(w)
		return
	}

	inputTokens := inputTokens(document)
	outputTokens := responseOutputTokens(document, protocol, candidate.OutputTokens)
	text := fmt.Sprintf("mock:%s:%d", candidateName, seq)
	if fault.kind == "stall_first_event" && stream {
		beginSSE(w)
		_ = waitForCancel(r.Context())
		return
	}

	if stream {
		beginSSE(w)
		if !s.delay(r.Context(), candidate.TTFBMS, fault) {
			return
		}
		if err := s.streamResponse(r.Context(), w, seq, protocol, model, text, inputTokens, outputTokens, fault, candidate.EventIntervalMS); err != nil {
			if fault.kind == "drop_after_events" {
				dropConnection(w)
			}
			return
		}
		s.complete(seq)
		return
	}

	if !s.delay(r.Context(), candidate.TTFBMS, fault) {
		return
	}
	value, err := jsonResponse(protocol, model, text, inputTokens, outputTokens, document, seq, fault)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "mock_response_error")
		return
	}
	if fault.kind == "invalid_json" {
		beginJSON(w)
		_, _ = io.WriteString(w, "{\"broken\"")
		return
	}
	beginJSON(w)
	_, err = w.Write(value)
	if err == nil {
		s.complete(seq)
	}
}

func (s *Server) serveReceipts(w http.ResponseWriter, r *http.Request) {
	if !s.allowDirectives {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_since")
		return
	}
	writeJSON(w, http.StatusOK, s.Receipts(since))
}

func (s *Server) recordReceipt(receipt Receipt) uint64 {
	receipt.Seq = s.nextSeq.Add(1)
	s.mu.Lock()
	s.receipts = append(s.receipts, receipt)
	s.completion[receipt.Seq] = len(s.receipts) - 1
	s.mu.Unlock()
	return receipt.Seq
}

func (s *Server) complete(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index, ok := s.completion[seq]; ok {
		s.receipts[index].Completed = true
	}
}

func (s *Server) delay(ctx context.Context, baseMS int, fault parsedFault) bool {
	ms := baseMS
	if fault.kind == "slow_ttfb" {
		ms = fault.value
	}
	if ms <= 0 {
		return true
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func waitForCancel(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func beginJSON(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Moni-Mock", "1")
}

func beginSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Moni-Mock", "1")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	beginJSON(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, kind string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": kind, "message": kind}})
}

func dropAfterHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Moni-Mock", "1")
	w.WriteHeader(http.StatusOK)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	dropConnection(w)
}

func dropConnection(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		if conn, _, err := hijacker.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

func authorized(r *http.Request, protocol, secret string) bool {
	got := ""
	switch protocol {
	case "messages":
		got = r.Header.Get("x-api-key")
	case "gemini":
		got = r.Header.Get("x-goog-api-key")
	default:
		value := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if strings.HasPrefix(value, prefix) {
			got = strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return got != "" && got == secret
}

func parseEndpoint(path string) (candidate, protocol, model string, stream, ok bool) {
	if !strings.HasPrefix(path, "/c/") {
		return "", "", "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/c/"), "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "v1" {
		if len(parts) >= 4 && parts[1] == "v1beta" && strings.HasPrefix(parts[2], "models") {
			return parseGeminiEndpoint(parts)
		}
		return "", "", "", false, false
	}
	switch parts[2] {
	case "chat":
		if len(parts) != 4 || parts[3] != "completions" {
			return "", "", "", false, false
		}
		return parts[0], "chat", "", false, true
	case "responses", "messages", "embeddings", "rerank":
		if len(parts) != 3 {
			return "", "", "", false, false
		}
		return parts[0], parts[2], "", false, true
	default:
		return "", "", "", false, false
	}
}

func parseGeminiEndpoint(parts []string) (candidate, protocol, model string, stream, ok bool) {
	if len(parts) != 4 || parts[2] != "models" {
		return "", "", "", false, false
	}
	modelAction := parts[3]
	if strings.HasSuffix(modelAction, ":generateContent") {
		return parts[0], "gemini", strings.TrimSuffix(modelAction, ":generateContent"), false, true
	}
	if strings.HasSuffix(modelAction, ":streamGenerateContent") {
		return parts[0], "gemini", strings.TrimSuffix(modelAction, ":streamGenerateContent"), true, true
	}
	return "", "", "", false, false
}

type directives struct{ fault, nonce string }

var directivePattern = regexp.MustCompile(`\[\[mock:(fault|nonce)=([^\]]+)\]\]`)

func parseDirectives(raw []byte) directives {
	var out directives
	for _, match := range directivePattern.FindAllSubmatch(raw, -1) {
		value := string(match[2])
		switch string(match[1]) {
		case "fault":
			out.fault = value
		case "nonce":
			out.nonce = value
		}
	}
	return out
}

type parsedFault struct {
	kind  string
	value int
}

func parseFault(value string) (parsedFault, error) {
	if value == "" {
		return parsedFault{}, nil
	}
	known := map[string]bool{
		"reject_429": true, "reject_429_unlisted": true, "http_500": true,
		"drop_after_headers": true, "stall_first_event": true, "missing_usage": true,
		"conflicting_usage": true, "error_after_usage": true, "reasoning_events": true,
		"invalid_json": true,
	}
	if known[value] {
		return parsedFault{kind: value}, nil
	}
	for _, prefix := range []string{"drop_after_events:", "slow_ttfb:"} {
		if strings.HasPrefix(value, prefix) {
			n, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
			if err != nil || n < 0 || n > 10_000 {
				return parsedFault{}, fmt.Errorf("invalid fault %q", value)
			}
			return parsedFault{kind: strings.TrimSuffix(prefix, ":"), value: n}, nil
		}
	}
	return parsedFault{}, fmt.Errorf("unknown fault %q", value)
}

func parseSince(value string) (uint64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func inputTokens(document any) int64 {
	bytes := textBytes(document, "")
	if bytes < 1 {
		bytes = 1
	}
	return int64((bytes + 3) / 4)
}

func textBytes(value any, key string) int {
	if text, ok := value.(string); ok {
		if isTextKey(key) {
			return len([]byte(stripDirectives(text)))
		}
		return 0
	}
	switch typed := value.(type) {
	case []any:
		total := 0
		for _, item := range typed {
			total += textBytes(item, key)
		}
		return total
	case map[string]any:
		total := 0
		for childKey, item := range typed {
			total += textBytes(item, childKey)
		}
		return total
	default:
		return 0
	}
}

func isTextKey(key string) bool {
	switch key {
	case "content", "text", "input", "instructions", "system", "query", "document", "documents", "prompt", "parts":
		return true
	default:
		return false
	}
}

func stripDirectives(value string) string { return directivePattern.ReplaceAllString(value, "") }

func responseOutputTokens(document any, protocol string, candidate int) int64 {
	if protocol == "embeddings" || protocol == "rerank" {
		return 0
	}
	limit := int64(candidate)
	if object, ok := document.(map[string]any); ok {
		for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if value, exists := object[key]; exists {
				if number, ok := asInt64(value); ok && number > 0 && number < limit {
					limit = number
				}
			}
		}
		if config, ok := object["generationConfig"].(map[string]any); ok {
			if number, ok := asInt64(config["maxOutputTokens"]); ok && number > 0 && number < limit {
				limit = number
			}
		}
	}
	return limit
}

func asInt64(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if ok {
		parsed, err := number.Int64()
		return parsed, err == nil
	}
	float, ok := value.(float64)
	if !ok || float != math.Trunc(float) || float > math.MaxInt64 || float < math.MinInt64 {
		return 0, false
	}
	return int64(float), true
}

type rawSSE string

func writeSSE(w http.ResponseWriter, event string, value any) error {
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if done, ok := value.(string); ok && done == "[DONE]" {
		_, err := io.WriteString(w, "data: [DONE]\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return err
	}
	if invalid, ok := value.(rawSSE); ok {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", string(invalid)); err != nil {
			return err
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func (s *Server) streamResponse(ctx context.Context, w http.ResponseWriter, seq uint64, protocol, model, text string, input, output int64, fault parsedFault, intervalMS int) error {
	write := func(event string, value any) error {
		if err := writeSSE(w, event, value); err != nil {
			return err
		}
		if intervalMS > 0 {
			timer := time.NewTimer(time.Duration(intervalMS) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	limit := fault.value
	count := 0
	writeEvent := func(event string, value any) error {
		if fault.kind == "drop_after_events" && count >= limit {
			return io.ErrUnexpectedEOF
		}
		err := write(event, value)
		count++
		return err
	}

	var err error
	switch protocol {
	case "chat":
		err = streamChat(writeEvent, model, text, input, output, fault)
	case "responses":
		err = streamResponses(writeEvent, model, text, input, output, fault)
	case "messages":
		err = streamMessages(writeEvent, model, text, input, output, fault)
	case "gemini":
		err = streamGemini(writeEvent, model, text, input, output, fault)
	default:
		return fmt.Errorf("streaming unsupported for %s", protocol)
	}
	if err != nil {
		return err
	}
	if fault.kind == "drop_after_events" || fault.kind == "error_after_usage" || fault.kind == "reasoning_events" || fault.kind == "invalid_json" {
		return nil
	}
	return nil
}

func streamChat(write func(string, any) error, model, text string, input, output int64, fault parsedFault) error {
	if fault.kind == "invalid_json" {
		return writeRawSSE(write, "", "{")
	}
	if err := write("", map[string]any{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}}}); err != nil {
		return err
	}
	if err := write("", map[string]any{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}); err != nil {
		return err
	}
	if fault.kind != "missing_usage" {
		usage := map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output}
		if err := write("", map[string]any{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": usage}); err != nil {
			return err
		}
		if fault.kind == "conflicting_usage" {
			if err := write("", map[string]any{"id": "chatcmpl_mock", "object": "chat.completion.chunk", "model": model, "choices": []any{}, "usage": map[string]any{"prompt_tokens": input, "completion_tokens": output + 1, "total_tokens": input + output + 1}}); err != nil {
				return err
			}
		}
	}
	if fault.kind == "error_after_usage" {
		return write("error", map[string]any{"error": map[string]string{"type": "upstream_error", "message": "mock stream failure"}})
	}
	return write("", "[DONE]")
}

func streamResponses(write func(string, any) error, model, text string, input, output int64, fault parsedFault) error {
	id := "resp_mock"
	itemID := "msg_mock"
	if fault.kind == "invalid_json" {
		return writeRawSSE(write, "response.created", "{")
	}
	if err := write("response.created", responseEvent("response.created", map[string]any{"id": id, "object": "response", "status": "in_progress", "model": model, "output": []any{}})); err != nil {
		return err
	}
	if fault.kind == "reasoning_events" {
		return write("response.reasoning.delta", map[string]any{"type": "response.reasoning.delta", "delta": "hidden"})
	}
	if err := write("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": itemID, "output_index": 0, "content_index": 0, "delta": text, "logprobs": []any{}}); err != nil {
		return err
	}
	if fault.kind == "error_after_usage" {
		partial := responseBody(model, text, id, output, map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output, "input_tokens_details": map[string]int64{"cached_tokens": 0}, "output_tokens_details": map[string]int64{"reasoning_tokens": 0}})
		partial["status"] = "in_progress"
		if err := write("response.in_progress", responseEvent("response.in_progress", partial)); err != nil {
			return err
		}
		return write("error", map[string]any{"type": "error", "error": map[string]string{"type": "server_error", "message": "mock stream failure"}})
	}
	usage := map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output, "input_tokens_details": map[string]int64{"cached_tokens": 0}, "output_tokens_details": map[string]int64{"reasoning_tokens": 0}}
	if fault.kind == "missing_usage" {
		usage = nil
	}
	response := responseBody(model, text, id, output, usage)
	if fault.kind == "conflicting_usage" {
		response["usage"] = map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output + 1}
	}
	return write("response.completed", responseEvent("response.completed", response))
}

func streamMessages(write func(string, any) error, model, text string, input, output int64, fault parsedFault) error {
	id := "msg_mock"
	if fault.kind == "invalid_json" {
		return writeRawSSE(write, "message_start", "{")
	}
	start := map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int64{"input_tokens": input, "output_tokens": 0}}
	if err := write("message_start", map[string]any{"type": "message_start", "message": start}); err != nil {
		return err
	}
	if err := write("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}}); err != nil {
		return err
	}
	if err := write("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": text}}); err != nil {
		return err
	}
	if err := write("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
		return err
	}
	if fault.kind == "error_after_usage" {
		if err := write("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": nil, "stop_sequence": nil}, "usage": map[string]int64{"input_tokens": input, "output_tokens": output}}); err != nil {
			return err
		}
		return write("error", map[string]any{"type": "error", "error": map[string]string{"type": "server_error", "message": "mock stream failure"}})
	}
	if fault.kind != "missing_usage" {
		usage := map[string]int64{"input_tokens": input, "output_tokens": output}
		if fault.kind == "conflicting_usage" {
			usage["input_tokens"] = input - 1
		}
		if err := write("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": usage}); err != nil {
			return err
		}
	} else if err := write("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": nil}); err != nil {
		return err
	}
	return write("message_stop", map[string]any{"type": "message_stop"})
}

func streamGemini(write func(string, any) error, model, text string, input, output int64, fault parsedFault) error {
	if fault.kind == "invalid_json" {
		return writeRawSSE(write, "", "{")
	}
	first := map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]string{"text": text}}}}}, "usageMetadata": geminiUsage(input, output)}
	if err := write("", first); err != nil {
		return err
	}
	if fault.kind == "error_after_usage" {
		return write("", map[string]any{"error": map[string]string{"message": "mock stream failure"}})
	}
	usage := geminiUsage(input, output)
	if fault.kind == "missing_usage" {
		usage = nil
	}
	if fault.kind == "conflicting_usage" {
		usage = map[string]int64{"promptTokenCount": input, "candidatesTokenCount": output, "totalTokenCount": input + output + 1}
	}
	finish := map[string]any{"candidates": []any{map[string]any{"index": 0, "finishReason": "STOP"}}}
	if usage != nil {
		finish["usageMetadata"] = usage
	}
	if err := write("", finish); err != nil {
		return err
	}
	if usage != nil {
		if err := write("", map[string]any{"usageMetadata": usage}); err != nil {
			return err
		}
	}
	return nil
}

func writeRawSSE(write func(string, any) error, event, raw string) error {
	return write(event, rawSSE(raw))
}

func jsonResponse(protocol, model, text string, input, output int64, document any, seq uint64, fault parsedFault) ([]byte, error) {
	if fault.kind == "error_after_usage" {
		return json.Marshal(map[string]any{"error": map[string]string{"type": "upstream_error", "message": "mock response failure"}})
	}
	switch protocol {
	case "chat":
		usage := any(map[string]int64{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output})
		if fault.kind == "missing_usage" {
			usage = nil
		}
		if fault.kind == "conflicting_usage" {
			usage = map[string]int64{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output + 1}
		}
		return json.Marshal(map[string]any{"id": fmt.Sprintf("chatcmpl_mock_%d", seq), "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": usage})
	case "responses":
		var usage any = map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": input + output, "input_tokens_details": map[string]int64{"cached_tokens": 0}, "output_tokens_details": map[string]int64{"reasoning_tokens": 0}}
		if fault.kind == "missing_usage" {
			usage = nil
		}
		if fault.kind == "conflicting_usage" {
			usage = map[string]int64{"input_tokens": input, "output_tokens": output, "total_tokens": input + output + 1}
		}
		return json.Marshal(responseBody(model, text, fmt.Sprintf("resp_mock_%d", seq), output, usage))
	case "messages":
		var usage any = map[string]int64{"input_tokens": input, "output_tokens": output}
		if fault.kind == "missing_usage" {
			usage = nil
		}
		if fault.kind == "conflicting_usage" {
			usage = map[string]int64{"input_tokens": input - 1, "output_tokens": output}
		}
		return json.Marshal(map[string]any{"id": fmt.Sprintf("msg_mock_%d", seq), "type": "message", "role": "assistant", "model": model, "content": []any{map[string]string{"type": "text", "text": text}}, "stop_reason": "end_turn", "stop_sequence": nil, "usage": usage})
	case "embeddings":
		return embeddingResponse(model, document, input)
	case "rerank":
		return rerankResponse(model, document, input)
	case "gemini":
		var usage any = geminiUsage(input, output)
		if fault.kind == "missing_usage" {
			usage = nil
		}
		if fault.kind == "conflicting_usage" {
			usage = map[string]int64{"promptTokenCount": input, "candidatesTokenCount": output, "totalTokenCount": input + output + 1}
		}
		return json.Marshal(map[string]any{"responseId": fmt.Sprintf("gemini_mock_%d", seq), "modelVersion": model, "candidates": []any{map[string]any{"index": 0, "content": map[string]any{"role": "model", "parts": []any{map[string]string{"text": text}}}, "finishReason": "STOP"}}, "usageMetadata": usage})
	default:
		return nil, fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func responseBody(model, text, id string, output int64, usage any) map[string]any {
	return map[string]any{"id": id, "object": "response", "created_at": int64(1700000000), "status": "completed", "model": model, "output": []any{map[string]any{"type": "message", "id": "msg_mock", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}, "logprobs": []any{}}}}}, "max_output_tokens": output, "usage": usage}
}

func responseEvent(typ string, response map[string]any) map[string]any {
	return map[string]any{"type": typ, "sequence_number": 0, "response": response}
}

func geminiUsage(input, output int64) map[string]int64 {
	return map[string]int64{"promptTokenCount": input, "candidatesTokenCount": output, "totalTokenCount": input + output}
}

func embeddingResponse(model string, document any, input int64) ([]byte, error) {
	count, dimension, encoding := embeddingShape(document)
	data := make([]any, 0, count)
	for index := 0; index < count; index++ {
		values := make([]float32, dimension)
		for i := range values {
			values[i] = float32(index+1) / float32(i+2)
		}
		var vector any = values
		if encoding == "base64" {
			vector = base64Vector(values)
		}
		data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": vector})
	}
	return json.Marshal(map[string]any{"object": "list", "model": model, "data": data, "usage": map[string]int64{"prompt_tokens": input, "total_tokens": input}})
}

func embeddingShape(document any) (count, dimension int, encoding string) {
	count, dimension, encoding = 1, 3, "float"
	if object, ok := document.(map[string]any); ok {
		switch input := object["input"].(type) {
		case []any:
			count = len(input)
			if count == 0 {
				count = 1
			}
		}
		if value, ok := asInt64(object["dimensions"]); ok && value > 0 && value <= 8192 {
			dimension = int(value)
		}
		if value, ok := object["encoding_format"].(string); ok && value == "base64" {
			encoding = value
		}
	}
	return count, dimension, encoding
}

func base64Vector(values []float32) string {
	raw := make([]byte, len(values)*4)
	for i, value := range values {
		bits := math.Float32bits(value)
		raw[i*4], raw[i*4+1], raw[i*4+2], raw[i*4+3] = byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func rerankResponse(model string, document any, input int64) ([]byte, error) {
	count, topN := 1, 1
	if object, ok := document.(map[string]any); ok {
		if docs, ok := object["documents"].([]any); ok && len(docs) > 0 {
			count = len(docs)
		}
		if value, ok := asInt64(object["top_n"]); ok && value > 0 && int(value) < count {
			topN = int(value)
		} else {
			topN = count
		}
	}
	results := make([]any, 0, topN)
	for i := 0; i < topN; i++ {
		results = append(results, map[string]any{"index": i, "relevance_score": float64(topN-i) / float64(topN)})
	}
	return json.Marshal(map[string]any{"id": "rank_mock", "model": model, "results": results, "usage": map[string]int64{"prompt_tokens": input, "total_tokens": input}})
}
