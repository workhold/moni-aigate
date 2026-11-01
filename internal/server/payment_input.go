package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

func paymentHeaderOne(h http.Header, name string, optional bool) (string, bool) {
	var value string
	found := false
	for k, v := range h {
		if strings.EqualFold(k, name) {
			if found || len(v) != 1 {
				return "", false
			}
			found = true
			value = v[0]
		}
	}
	return value, found || optional
}
func paymentJSONHeaders(r *http.Request) bool {
	content, ok := paymentHeaderOne(r.Header, "Content-Type", false)
	if !ok {
		return false
	}
	encoding, ok := paymentHeaderOne(r.Header, "Content-Encoding", true)
	if !ok || encoding != "" && !strings.EqualFold(encoding, "identity") {
		return false
	}
	media, params, e := mime.ParseMediaType(content)
	return e == nil && media == "application/json" && (len(params) == 0 || len(params) == 1 && strings.EqualFold(params["charset"], "utf-8"))
}
func (s *Server) paymentBody(c *gin.Context, max int64) ([]byte, bool) {
	r := c.Request
	if r.URL.RawQuery != "" || r.URL.ForceQuery || !paymentJSONHeaders(r) || r.Body == nil {
		s.paymentFailure(c, 400, "invalid_request")
		return nil, false
	}
	if r.ContentLength > max {
		s.paymentFailure(c, 413, "body_too_large")
		return nil, false
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, max+1))
	if int64(len(raw)) > max || r.ContentLength > max {
		s.paymentFailure(c, 413, "body_too_large")
		return nil, false
	}
	if e != nil {
		s.paymentFailure(c, 400, "invalid_request")
		return nil, false
	}
	return raw, true
}
func (s *Server) paymentObject(c *gin.Context, nullable string, fields ...string) (map[string]json.RawMessage, bool) {
	raw, ok := s.paymentBody(c, 8192)
	if !ok {
		return nil, false
	}
	var object map[string]json.RawMessage
	if !decodeCanonicalJSON(bytes.NewReader(raw), &object, 8192, fields...) || len(object) != len(fields) {
		s.paymentFailure(c, 400, "invalid_request")
		return nil, false
	}
	for _, name := range fields {
		value, exists := object[name]
		if !exists || name != nullable && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			s.paymentFailure(c, 400, "invalid_request")
			return nil, false
		}
	}
	return object, true
}
func paymentQueryText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (s *Server) paymentQuery(c *gin.Context, list bool) (string, bool) {
	r := c.Request
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.URL.ForceQuery || len(r.URL.RawQuery) > 4096 || !paymentQueryText(r.URL.RawQuery) || !list && r.URL.RawQuery != "" {
		s.paymentFailure(c, 400, "invalid_request")
		return "", false
	}
	if r.Body != nil {
		b, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(b) != 0 {
			s.paymentFailure(c, 400, "invalid_request")
			return "", false
		}
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || len(query) > 1 || len(query) == 0 && r.URL.RawQuery != "" {
		s.paymentFailure(c, 400, "invalid_request")
		return "", false
	}
	for name, values := range query {
		if name != "cursor" || len(values) != 1 || values[0] == "" || !paymentQueryText(values[0]) {
			s.paymentFailure(c, 400, "invalid_request")
			return "", false
		}
	}
	return query.Get("cursor"), true
}
func (s *Server) paymentPathID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !procurementUUID(id) {
		s.paymentFailure(c, 400, "invalid_request")
		return "", false
	}
	return id, true
}
func paymentBool(raw json.RawMessage) (bool, bool) {
	var value bool
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return false, false
	}
	return value, true
}
func paymentNullableID(raw json.RawMessage) (*string, bool) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true
	}
	id, ok := commerceID(raw)
	if !ok {
		return nil, false
	}
	return &id, true
}
func paymentName(raw json.RawMessage) (string, bool) {
	name, ok := commerceString(raw)
	return name, ok && len(name) >= 1 && len(name) <= 100
}
