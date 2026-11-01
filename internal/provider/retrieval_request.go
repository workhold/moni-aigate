package provider

import (
	"encoding/json"
	"unicode/utf8"
)

const (
	maxRetrievalBatch      = 128
	maxEmbeddingDimensions = 8192
	maxEmbeddingTokenID    = 1<<31 - 1
)

// Expectations are captured during validation, never inferred from the
// provider response or mutable public Request fields.
type retrievalExpectation struct {
	count           int
	dimensions      int
	encoding        string
	documents       []string
	topN            int
	returnDocuments bool
}

func (e retrievalExpectation) clone() retrievalExpectation {
	e.documents = append([]string(nil), e.documents...)
	return e
}

func parseRetrieval(raw []byte, p Profile) (*Request, error) {
	if len(raw) > MaxRequestBytes {
		return nil, failure("request_too_large", 413, ClassCaller, ConsumptionNotSent)
	}
	if !utf8.Valid(raw) {
		return nil, badRequest()
	}
	f, err := object(raw)
	if err != nil || !stringValue(f["model"], p.Model) {
		return nil, badRequest()
	}
	base := map[string]bool{"model": true}
	optional := map[string]bool{}
	if p.ProtocolName() == ProtocolEmbeddings {
		base["input"] = true
		optional = map[string]bool{"encoding_format": true, "dimensions": true, "user": true}
	} else {
		base["query"], base["documents"] = true, true
		optional = map[string]bool{"top_n": true, "return_documents": true}
	}
	if p.AllowedParameters != nil {
		optional = map[string]bool{}
		for _, k := range p.AllowedParameters {
			optional[k] = true
		}
	}
	for k := range f {
		if !base[k] && !optional[k] {
			return nil, failure("unsupported_parameter", 400, ClassCaller, ConsumptionNotSent)
		}
	}
	r := &Request{Model: p.Model, ProfileVersion: p.Version, InputUpperBound: p.InputTokenUpperBound, profile: cloneProfile(p), fields: f}
	if p.ProtocolName() == ProtocolEmbeddings {
		count, tokens, valid := embeddingInputCount(f["input"])
		if !valid || tokens > p.InputTokenUpperBound {
			return nil, badRequest()
		}
		r.retrieval = retrievalExpectation{count: count, encoding: "float"}
		if v, ok := f["encoding_format"]; ok {
			if !stringValue(v, "float") && !stringValue(v, "base64") {
				return nil, badRequest()
			}
			_ = json.Unmarshal(v, &r.retrieval.encoding)
		}
		if v, ok := f["dimensions"]; ok {
			n, valid := integer(v)
			if !valid || n < 1 || n > maxEmbeddingDimensions {
				return nil, badRequest()
			}
			r.retrieval.dimensions = int(n)
		}
		if v, ok := f["user"]; ok {
			var user string
			if !nonemptyString(v) || json.Unmarshal(v, &user) != nil || len(user) > 256 {
				return nil, badRequest()
			}
		}
	} else {
		if !nonemptyString(f["query"]) {
			return nil, badRequest()
		}
		var docs []json.RawMessage
		if json.Unmarshal(f["documents"], &docs) != nil || len(docs) == 0 || len(docs) > maxRetrievalBatch {
			return nil, badRequest()
		}
		r.retrieval = retrievalExpectation{count: len(docs), topN: len(docs)}
		for _, raw := range docs {
			var text string
			if !nonemptyString(raw) || json.Unmarshal(raw, &text) != nil {
				return nil, badRequest()
			}
			r.retrieval.documents = append(r.retrieval.documents, text)
		}
		if v, ok := f["top_n"]; ok {
			n, valid := integer(v)
			if !valid || n < 1 || n > int64(len(docs)) {
				return nil, badRequest()
			}
			r.retrieval.topN = int(n)
		}
		if v, ok := f["return_documents"]; ok && !boolean(v, &r.retrieval.returnDocuments) {
			return nil, badRequest()
		}
	}
	return r, nil
}

func embeddingInputCount(raw []byte) (int, int64, bool) {
	if nonemptyString(raw) {
		return 1, 0, true
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
		return 0, 0, false
	}
	if isString(values[0]) {
		if len(values) > maxRetrievalBatch {
			return 0, 0, false
		}
		for _, value := range values {
			if !nonemptyString(value) {
				return 0, 0, false
			}
		}
		return len(values), 0, true
	}
	if _, ok := integer(values[0]); ok {
		return 1, int64(len(values)), validTokenIDs(values)
	}
	if len(values) > maxRetrievalBatch {
		return 0, 0, false
	}
	var count int64
	for _, value := range values {
		var tokens []json.RawMessage
		if json.Unmarshal(value, &tokens) != nil || !validTokenIDs(tokens) {
			return 0, 0, false
		}
		count += int64(len(tokens))
	}
	return len(values), count, true
}

func validTokenIDs(tokens []json.RawMessage) bool {
	if len(tokens) == 0 {
		return false
	}
	for _, raw := range tokens {
		n, valid := integer(raw)
		if !valid || n < 0 || n > maxEmbeddingTokenID {
			return false
		}
	}
	return true
}
