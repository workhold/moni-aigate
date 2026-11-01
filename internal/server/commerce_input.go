package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/security"
)

var commerceTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?Z$`)

func commerceObject(r io.Reader, fields ...string) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if !decodeCanonicalJSON(r, &obj, 8192, fields...) || len(obj) != len(fields) {
		return nil, false
	}
	for _, field := range fields {
		raw, ok := obj[field]
		if !ok || string(raw) == "null" {
			return nil, false
		}
	}
	return obj, true
}

func commerceString(raw json.RawMessage) (string, bool) {
	var value string
	if json.Unmarshal(raw, &value) != nil || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

func commerceID(raw json.RawMessage) (string, bool) {
	value, ok := commerceString(raw)
	return value, ok && procurementUUID(value)
}

func commerceInteger(raw json.RawMessage, positive bool) (int64, bool) {
	value, ok := commerceString(raw)
	if !ok || !procurementDecimal(value, positive) {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	return n, err == nil
}

func commerceCount(raw json.RawMessage) (int, bool) {
	value := string(raw)
	n, err := strconv.Atoi(value)
	return n, err == nil && n >= 1 && n <= 100 && strconv.Itoa(n) == value
}

func commerceExpiry(raw json.RawMessage) (time.Time, bool) {
	value, ok := commerceString(raw)
	if !ok || !commerceTimePattern.MatchString(value) {
		return time.Time{}, false
	}
	v, err := time.Parse(time.RFC3339Nano, value)
	return v, err == nil
}

func commerceReason(raw json.RawMessage) (string, bool) {
	value, ok := commerceString(raw)
	return value, ok && len(value) >= 1 && len(value) <= 200
}

func commercePurpose(raw json.RawMessage) (string, bool) {
	value, ok := commerceString(raw)
	return value, ok && security.ValidReference(value, 256)
}

func commerceCode(raw json.RawMessage) (string, bool) {
	value, ok := commerceString(raw)
	if !ok || len(value) != 85 || !strings.HasPrefix(value, "mav1.") || value[41] != '.' || !procurementUUID(value[5:41]) {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value[42:])
	return value, err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == value[42:]
}

func commerceProductFits(amount int64, count int) bool {
	return new(big.Int).Mul(big.NewInt(amount), big.NewInt(int64(count))).IsInt64()
}

func (s *Server) commerceInvalid(c *gin.Context) {
	s.fail(c, http.StatusBadRequest, "invalid_request", "请求格式或参数无效")
}

// Query parsing cannot use URL.Query: it silently discards malformed pairs.
func (s *Server) commerceQuery(c *gin.Context, fields ...string) (url.Values, bool) {
	if c.Request.ContentLength != 0 || len(c.Request.TransferEncoding) != 0 || len(c.Request.URL.RawQuery) > 4096 {
		s.commerceInvalid(c)
		return nil, false
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		s.commerceInvalid(c)
		return nil, false
	}
	allowed := map[string]bool{}
	for _, name := range fields {
		allowed[name] = true
	}
	for name, values := range query {
		if !allowed[name] || !utf8.ValidString(name) || len(values) != 1 || values[0] == "" || !utf8.ValidString(values[0]) {
			s.commerceInvalid(c)
			return nil, false
		}
		for _, r := range values[0] {
			if unicode.IsControl(r) {
				s.commerceInvalid(c)
				return nil, false
			}
		}
	}
	return query, true
}
