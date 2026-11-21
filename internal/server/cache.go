package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strconv"

	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/strictjson"
)

type textCacheDescriptor struct {
	protocol      string
	tenantID      string
	modelID       string
	routeRevision int64
	requestDigest []byte
}

func newTextCacheDescriptor(request *provider.Request, protocol, tenantID, modelID string, routeRevision int64, ttlSeconds int) (*textCacheDescriptor, error) {
	if ttlSeconds == 0 || request.Stream {
		return nil, nil
	}
	normalized, err := request.UpstreamJSON(modelID)
	if err != nil {
		return nil, err
	}
	value, err := strictjson.Parse(normalized, provider.MaxRequestBytes)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok || containsToolSemantics("", object) {
		return nil, nil
	}
	delete(object, "model")
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, frame := range [][]byte{
		[]byte("moni-aigate.text-response-cache.v1"), []byte(protocol), []byte(tenantID), []byte(modelID),
		[]byte(strconv.FormatInt(routeRevision, 10)), canonical,
	} {
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(frame)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(frame)
	}
	return &textCacheDescriptor{protocol: protocol, tenantID: tenantID, modelID: modelID, routeRevision: routeRevision, requestDigest: h.Sum(nil)}, nil
}

func rewriteCachedResponseModel(body []byte, protocol, requestedName string) ([]byte, error) {
	if protocol == provider.ProtocolGemini {
		return bytes.Clone(body), nil
	}
	value, err := strictjson.Parse(body, provider.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, strictjson.ErrInvalidJSON
	}
	if _, exists := object["model"]; exists {
		object["model"] = requestedName
	}
	return json.Marshal(object)
}

func cacheableResponseBody(body []byte) bool {
	value, err := strictjson.Parse(body, provider.MaxResponseBytes)
	return err == nil && !containsToolSemantics("", value)
}

func containsToolSemantics(key string, value any) bool {
	switch key {
	case "tools", "tool_choice", "parallel_tool_calls", "tool_calls", "tool_call_id", "function_call", "function", "tool_use", "tool_result":
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for name, child := range typed {
			if containsToolSemantics(name, child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsToolSemantics("", child) {
				return true
			}
		}
	case string:
		if (key == "type" || key == "finish_reason" || key == "stop_reason") && (typed == "function_call" || typed == "tool_use" || typed == "tool_calls" || typed == "tool_result") {
			return true
		}
	}
	return false
}
