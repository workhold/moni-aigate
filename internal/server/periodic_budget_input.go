package server

import (
	"io"
	"mime"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

func periodicQueryText(v string) bool {
	if !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (s *Server) periodicBudgetQuery(c *gin.Context, list bool) (string, bool) {
	r := c.Request
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.URL.ForceQuery || len(r.URL.RawQuery) > 4096 || !periodicQueryText(r.URL.RawQuery) || !list && r.URL.RawQuery != "" {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return "", false
	}
	// ContentLength zero alone does not prove that a custom transport supplied
	// no body. Bound the probe to one byte and never silently ignore input.
	if r.Body != nil {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(b) != 0 {
			s.periodicBudgetFailure(c, 400, "invalid_request")
			return "", false
		}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 || len(q) == 0 && r.URL.RawQuery != "" {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return "", false
	}
	for key, values := range q {
		if !periodicQueryText(key) || key != "cursor" || len(values) != 1 || values[0] == "" || !periodicQueryText(values[0]) {
			s.periodicBudgetFailure(c, 400, "invalid_request")
			return "", false
		}
	}
	return q.Get("cursor"), true
}

func (s *Server) periodicBudgetInput(c *gin.Context) (periodicbudget.PutPeriodicBudget, bool) {
	var zero periodicbudget.PutPeriodicBudget
	r := c.Request
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return zero, false
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	encoding := r.Header.Get("Content-Encoding")
	if err != nil || media != "application/json" || len(params) > 1 || len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8") || encoding != "" && !strings.EqualFold(encoding, "identity") || len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) > 1 {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return zero, false
	}
	if r.Body == nil {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return zero, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, periodicbudget.MaxConfigureBytes+1))
	if len(raw) > periodicbudget.MaxConfigureBytes {
		s.periodicBudgetFailure(c, 413, "request_too_large")
		return zero, false
	}
	if err != nil {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return zero, false
	}
	in, err := periodicbudget.ParseConfigure(raw)
	if err != nil {
		s.periodicBudgetError(c, err)
		return zero, false
	}
	return in, true
}

func (s *Server) periodicBudgetID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !periodicbudget.ValidUUID(id) {
		s.periodicBudgetFailure(c, 400, "invalid_request")
		return "", false
	}
	return id, true
}
