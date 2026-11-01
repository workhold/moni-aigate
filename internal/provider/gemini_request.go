package provider

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

var geminiModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// ValidGeminiModel accepts one model segment, never a path, action or query.
// Vertex resource names and publisher paths require a separate adapter.
func ValidGeminiModel(model string) bool {
	return geminiModel.MatchString(model) && !strings.Contains(model, "..")
}

// EndpointSuffixForModel uses an immutable binding's model for Gemini's native
// URL. It never interpolates credentials or caller-supplied query parameters.
func (r *Request) EndpointSuffixForModel(upstream string) (string, error) {
	if r == nil {
		return "", badRequest()
	}
	if r.Protocol() != ProtocolGemini {
		return r.EndpointSuffix(), nil
	}
	if !ValidGeminiModel(upstream) {
		return "", failure("invalid_upstream_model", 503, ClassConfiguration, ConsumptionNotSent)
	}
	action := ":generateContent"
	if r.stream {
		action = ":streamGenerateContent"
	}
	return "/models/" + upstream + action, nil
}

// ParseGemini validates the actual native body. The public model and streaming
// action come from the authenticated URL and cannot be overridden in JSON.
func ParseGemini(raw []byte, p Profile, stream bool) (*Request, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.ProtocolName() != ProtocolGemini {
		return nil, badRequest()
	}
	if len(raw) > MaxRequestBytes {
		return nil, failure("request_too_large", 413, ClassCaller, ConsumptionNotSent)
	}
	if !utf8.Valid(raw) {
		return nil, badRequest()
	}
	f, err := object(raw)
	if err != nil || !only(f, "contents", "systemInstruction", "generationConfig") {
		return nil, badRequest()
	}
	var contents []json.RawMessage
	if json.Unmarshal(f["contents"], &contents) != nil || len(contents) == 0 || len(contents) > 128 {
		return nil, badRequest()
	}
	for _, raw := range contents {
		if err := geminiTextContent(raw, false); err != nil {
			return nil, err
		}
	}
	if raw, exists := f["systemInstruction"]; exists {
		if err := geminiTextContent(raw, true); err != nil {
			return nil, err
		}
	}
	r := &Request{Model: p.Model, Stream: stream, stream: stream, ProfileVersion: p.Version, InputUpperBound: p.InputTokenUpperBound, profile: cloneProfile(p), fields: f, output: p.DefaultOutputLimit}
	config := map[string]json.RawMessage{}
	if raw, exists := f["generationConfig"]; exists {
		config, err = object(raw)
		if err != nil || !only(config, "maxOutputTokens", "candidateCount", "temperature", "topP", "topK", "stopSequences") {
			return nil, badRequest()
		}
	}
	allowed := map[string]bool{"temperature": true, "topP": true, "topK": true, "stopSequences": true}
	if p.AllowedParameters != nil {
		allowed = map[string]bool{}
		for _, key := range p.AllowedParameters {
			allowed[key] = true
		}
	}
	for key := range config {
		if key != "maxOutputTokens" && key != "candidateCount" && !allowed[key] {
			return nil, failure("unsupported_parameter", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	if raw, exists := config["maxOutputTokens"]; exists {
		value, valid := integer(raw)
		if !valid || value <= 0 || value > p.OutputLimit {
			return nil, badRequest()
		}
		r.output = value
	}
	if raw, exists := config["candidateCount"]; exists {
		if value, valid := integer(raw); !valid || value != 1 {
			return nil, failure("unsupported_n", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	for _, key := range []string{"temperature", "topP"} {
		if raw, exists := config[key]; exists {
			var value float64
			upper := 1.0
			if key == "temperature" {
				upper = 2
			}
			if !finiteNumber(raw, &value) || value < 0 || value > upper {
				return nil, badRequest()
			}
		}
	}
	if raw, exists := config["topK"]; exists {
		if value, valid := integer(raw); !valid || value < 1 || value > 1000 {
			return nil, badRequest()
		}
	}
	if raw, exists := config["stopSequences"]; exists {
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > 4 {
			return nil, badRequest()
		}
		for _, value := range values {
			var text string
			if !nonemptyString(value) || json.Unmarshal(value, &text) != nil || len(text) > 256 {
				return nil, badRequest()
			}
		}
	}
	r.MaxOutputTokens = r.output
	return r, nil
}

func geminiTextContent(raw []byte, system bool) error {
	m, err := object(raw)
	if err != nil || !only(m, "role", "parts") {
		return badRequest()
	}
	if role, exists := m["role"]; exists && !stringValue(role, "user") && (system || !stringValue(role, "model")) {
		return badRequest()
	}
	var parts []json.RawMessage
	if json.Unmarshal(m["parts"], &parts) != nil || len(parts) == 0 || len(parts) > 128 {
		return badRequest()
	}
	for _, raw := range parts {
		part, err := object(raw)
		if err != nil || !only(part, "text") || !nonemptyString(part["text"]) {
			return badRequest()
		}
	}
	return nil
}

func (r *Request) geminiUpstreamJSON(model string) ([]byte, error) {
	if !ValidGeminiModel(model) {
		return nil, failure("invalid_upstream_model", 503, ClassConfiguration, ConsumptionNotSent)
	}
	f := map[string]json.RawMessage{}
	for key, value := range r.fields {
		f[key] = value
	}
	config := map[string]json.RawMessage{}
	if raw, exists := f["generationConfig"]; exists {
		var err error
		config, err = object(raw)
		if err != nil {
			return nil, badRequest()
		}
	}
	config["maxOutputTokens"], _ = json.Marshal(r.output)
	config["candidateCount"] = json.RawMessage("1")
	f["generationConfig"], _ = json.Marshal(config)
	return json.Marshal(f)
}
