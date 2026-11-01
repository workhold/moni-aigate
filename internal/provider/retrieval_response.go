package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

func (r *Response) decodeRetrievalJSON() ([]byte, error) {
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
	if !utf8.Valid(body) {
		return nil, r.readFailure(protocolError("upstream_invalid_json"))
	}
	m, err := retrievalResponseObject(body)
	if err != nil {
		return nil, r.readFailure(protocolError("upstream_invalid_json"))
	}
	if _, exists := m["error"]; exists {
		return nil, r.readFailure(protocolError("upstream_error"))
	}
	var clean map[string]json.RawMessage
	if r.profile.ProtocolName() == ProtocolEmbeddings {
		clean, err = r.normalizeEmbeddings(m)
	} else {
		clean, err = r.normalizeRerank(m)
	}
	if err != nil {
		return nil, r.readFailure(err)
	}
	// Successful protocol completion is separate from trusted billing evidence.
	// The caller withholds this result until UsageConfirmed; an unconfirmed
	// counter must keep the hold pending rather than trigger a no-delivery release.
	r.ProtocolOutcome = "completed"
	r.TransportEndReason = "complete_body"
	r.FinishReason = "stop"
	r.Usage = inputOnlyUsage(m["usage"], r.profile.ProtocolName())
	if r.Usage.Status == UsageConfirmed {
		clean["usage"], _ = json.Marshal(map[string]int64{"prompt_tokens": r.Usage.InputTokens, "total_tokens": r.Usage.TotalTokens})
	}
	return json.Marshal(clean)
}

// Usage is evidence rather than result structure: duplicate/malformed counters
// must become UsageConflicting after a structurally valid result completes.
// All other response values retain the common recursive duplicate/depth guard.
func retrievalResponseObject(raw []byte) (map[string]json.RawMessage, error) {
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
		key, ok := token.(string)
		if !ok {
			return nil, badRequest()
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, badRequest()
		}
		if _, exists := m[key]; exists {
			if key != "usage" {
				return nil, badRequest()
			}
			value = json.RawMessage(`{"duplicate_usage":true}`)
		}
		if key != "usage" {
			valueDecoder := json.NewDecoder(bytes.NewReader(value))
			valueDecoder.UseNumber()
			if checkJSON(valueDecoder, 1) != nil {
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

func (r *Response) normalizeEmbeddings(m map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if !stringValue(m["object"], "list") || r.retrieval.count < 1 {
		return nil, protocolError("upstream_invalid_embeddings")
	}
	var data []json.RawMessage
	if json.Unmarshal(m["data"], &data) != nil || len(data) != r.retrieval.count {
		return nil, protocolError("upstream_invalid_embeddings")
	}
	seen := make([]bool, r.retrieval.count)
	cleanData := make([]map[string]json.RawMessage, 0, len(data))
	dimension := r.retrieval.dimensions
	for _, raw := range data {
		item, err := object(raw)
		if err != nil || !stringValue(item["object"], "embedding") {
			return nil, protocolError("upstream_invalid_embeddings")
		}
		index, valid := integer(item["index"])
		if !valid || index < 0 || index >= int64(len(seen)) || seen[index] {
			return nil, protocolError("upstream_invalid_embeddings")
		}
		seen[index] = true
		n, valid := embeddingDimensions(item["embedding"], r.retrieval.encoding)
		if !valid || (dimension != 0 && n != dimension) {
			return nil, protocolError("upstream_invalid_embeddings")
		}
		dimension = n
		cleanData = append(cleanData, map[string]json.RawMessage{"object": json.RawMessage(`"embedding"`), "index": item["index"], "embedding": item["embedding"]})
	}
	dataJSON, _ := json.Marshal(cleanData)
	modelJSON, _ := json.Marshal(r.model)
	return map[string]json.RawMessage{"object": json.RawMessage(`"list"`), "model": modelJSON, "data": dataJSON}, nil
}

func embeddingDimensions(raw []byte, encoding string) (int, bool) {
	if encoding == "base64" {
		var value string
		if !isString(raw) || json.Unmarshal(raw, &value) != nil || value == "" || len(value) > base64.StdEncoding.EncodedLen(maxEmbeddingDimensions*4) || strings.ContainsAny(value, "\r\n") {
			return 0, false
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || len(decoded) == 0 || len(decoded)%4 != 0 || len(decoded)/4 > maxEmbeddingDimensions {
			return 0, false
		}
		for i := 0; i < len(decoded); i += 4 {
			n := float64(math.Float32frombits(binary.LittleEndian.Uint32(decoded[i : i+4])))
			if math.IsNaN(n) || math.IsInf(n, 0) {
				return 0, false
			}
		}
		return len(decoded) / 4, true
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 || len(values) > maxEmbeddingDimensions {
		return 0, false
	}
	for _, value := range values {
		if !finiteNumber(value, nil) {
			return 0, false
		}
	}
	return len(values), true
}

func finiteNumber(raw []byte, out *float64) bool {
	var number float64
	if len(raw) == 0 || isNull(raw) || json.Unmarshal(raw, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return false
	}
	if out != nil {
		*out = number
	}
	return true
}

func (r *Response) normalizeRerank(m map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	var results []json.RawMessage
	if r.retrieval.topN < 1 || json.Unmarshal(m["results"], &results) != nil || len(results) != r.retrieval.topN {
		return nil, protocolError("upstream_invalid_rerank")
	}
	seen := make([]bool, r.retrieval.count)
	previous := math.Inf(1)
	cleanResults := make([]map[string]json.RawMessage, 0, len(results))
	for _, raw := range results {
		item, err := object(raw)
		if err != nil {
			return nil, protocolError("upstream_invalid_rerank")
		}
		index, valid := integer(item["index"])
		var score float64
		if !valid || index < 0 || index >= int64(len(seen)) || seen[index] || !finiteNumber(item["relevance_score"], &score) || score < 0 || score > 1 || score > previous {
			return nil, protocolError("upstream_invalid_rerank")
		}
		seen[index], previous = true, score
		clean := map[string]json.RawMessage{"index": item["index"], "relevance_score": item["relevance_score"]}
		if r.retrieval.returnDocuments {
			if int(index) >= len(r.retrieval.documents) {
				return nil, protocolError("upstream_invalid_rerank")
			}
			clean["document"], _ = json.Marshal(map[string]string{"text": r.retrieval.documents[index]})
		}
		cleanResults = append(cleanResults, clean)
	}
	resultsJSON, _ := json.Marshal(cleanResults)
	modelJSON, _ := json.Marshal(r.model)
	clean := map[string]json.RawMessage{"results": resultsJSON, "model": modelJSON}
	if raw, exists := m["id"]; exists {
		var id string
		if !nonemptyString(raw) || json.Unmarshal(raw, &id) != nil || !retrievalID(id) {
			return nil, protocolError("upstream_invalid_rerank")
		}
		clean["id"] = raw
	}
	return clean, nil
}

func retrievalID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && !strings.ContainsRune("-_.:", c) {
			return false
		}
	}
	return true
}

func inputOnlyUsage(raw []byte, protocol string) Usage {
	missing := Usage{Status: UsageMissing}
	conflicting := Usage{Status: UsageConflicting}
	if len(raw) == 0 || isNull(raw) {
		return missing
	}
	m, err := object(raw)
	if err != nil || !only(m, "prompt_tokens", "completion_tokens", "total_tokens") {
		return conflicting
	}
	values := map[string]int64{}
	for key, raw := range m {
		n, valid := integer(raw)
		if !valid || n < 0 || (key == "completion_tokens" && n != 0) {
			return conflicting
		}
		values[key] = n
	}
	total, totalPresent := values["total_tokens"]
	prompt, promptPresent := values["prompt_tokens"]
	if totalPresent && promptPresent && total != prompt {
		return conflicting
	}
	if !totalPresent || (protocol == ProtocolEmbeddings && !promptPresent) {
		return missing
	}
	return Usage{Status: UsageConfirmed, InputTokens: total, TotalTokens: total}
}
