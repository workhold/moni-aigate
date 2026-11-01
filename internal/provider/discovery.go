package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DiscoveryFormatOpenAI    = "openai"
	DiscoveryFormatAnthropic = "anthropic"
	DiscoveryFormatGemini    = "gemini"

	discoveryPageBytes  = 1 << 20
	discoveryTotalBytes = 4 << 20
	discoveryMaxPages   = 10
	discoveryMaxModels  = 1000
)

type DiscoveryOptions struct {
	Format string
}

// DiscoveryResult contains only normalized model identifiers. Provider metadata
// and pagination tokens must never be persisted or exposed as model capability.
type DiscoveryResult struct {
	Models []string
	Pages  int
}

// ValidDiscoveredModelID is deliberately narrower than arbitrary upstream
// metadata. A discovered identifier is a suggestion, never a URL or capability.
func ValidDiscoveredModelID(id string) bool {
	if len(id) == 0 || len(id) > 200 || !discoveryAlnum(id[0]) || strings.Contains(id, "..") || strings.Contains(id, "//") {
		return false
	}
	for _, b := range []byte(id) {
		if !discoveryAlnum(b) && b != '.' && b != '_' && b != '-' && b != '/' && b != ':' {
			return false
		}
	}
	return true
}

func discoveryAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// DiscoverModels fetches a bounded, complete snapshot from a channel's configured
// base URL. It performs no generation, financial writes, health changes or retry.
// All pagination stays on the validated origin and its fixed /models resource.
func (c *Client) DiscoverModels(ctx context.Context, endpoint Endpoint, options DiscoveryOptions) (DiscoveryResult, error) {
	zero := DiscoveryResult{}
	if options.Format != DiscoveryFormatOpenAI && options.Format != DiscoveryFormatAnthropic && options.Format != DiscoveryFormatGemini {
		return zero, failure("invalid_discovery_format", 400, ClassCaller, ConsumptionNotSent)
	}
	base, allow, err := validateEndpoint(endpoint)
	if err != nil {
		return zero, err
	}
	// Do not reinterpret encoded path separators or traversal in a configured
	// base when adding the one permitted discovery suffix.
	if base.EscapedPath() != base.Path || strings.Contains(base.Path, "\\") || strings.Contains(base.Path, "//") {
		return zero, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
	}
	for _, segment := range strings.Split(base.Path, "/") {
		if segment == "." || segment == ".." {
			return zero, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
		}
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/models"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	models, cursors := map[string]bool{}, map[string]bool{}
	totalBytes, entries, cursor := 0, 0, ""
	for page := 1; page <= discoveryMaxPages; page++ {
		if totalBytes >= discoveryTotalBytes {
			return zero, discoveryBoundError()
		}
		if err := ctx.Err(); err != nil {
			return zero, discoveryCallError(ctx, err)
		}
		u := *base
		query := url.Values{}
		switch options.Format {
		case DiscoveryFormatAnthropic:
			query.Set("limit", "100")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		case DiscoveryFormatGemini:
			query.Set("pageSize", "100")
			if cursor != "" {
				query.Set("pageToken", cursor)
			}
		}
		u.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return zero, failure("invalid_endpoint", 503, ClassConfiguration, ConsumptionNotSent)
		}
		request.Header.Set("Accept", "application/json")
		switch options.Format {
		case DiscoveryFormatOpenAI:
			request.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
		case DiscoveryFormatAnthropic:
			request.Header.Set("x-api-key", endpoint.APIKey)
			request.Header.Set("anthropic-version", "2023-06-01")
		case DiscoveryFormatGemini:
			request.Header.Set("x-goog-api-key", endpoint.APIKey)
		}
		var written atomic.Bool
		transport := c.transport(endpoint, &u, allow, &written)
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			transport.CloseIdleConnections()
			return zero, discoveryCallError(ctx, err)
		}
		body, err := readDiscoveryPage(response, discoveryTotalBytes-totalBytes)
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if err != nil {
			return zero, discoveryCallError(ctx, err)
		}
		totalBytes += len(body)
		if totalBytes > discoveryTotalBytes {
			return zero, discoveryBoundError()
		}
		ids, next, count, err := parseDiscoveryPage(body, options.Format)
		if err != nil {
			return zero, err
		}
		entries += count
		if entries > discoveryMaxModels {
			return zero, discoveryBoundError()
		}
		for _, id := range ids {
			models[id] = true
		}
		if next == "" {
			result := DiscoveryResult{Models: make([]string, 0, len(models)), Pages: page}
			for id := range models {
				result.Models = append(result.Models, id)
			}
			sort.Strings(result.Models)
			return result, nil
		}
		if cursors[next] || page == discoveryMaxPages {
			return zero, discoveryBoundError()
		}
		cursors[next], cursor = true, next
	}
	return zero, discoveryBoundError()
}

func readDiscoveryPage(response *http.Response, remaining int) ([]byte, error) {
	if response.StatusCode != http.StatusOK {
		return nil, failure("discovery_upstream_rejected", 502, ClassUpstream, ConsumptionUnknown)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, discoveryProtocolError()
	}
	limit := discoveryPageBytes
	if remaining < limit {
		limit = remaining
	}
	if limit <= 0 {
		return nil, discoveryBoundError()
	}
	// Bound the read itself by both budgets. The extra byte detects overflow;
	// do not consume another full page after approaching the total byte limit.
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, discoveryBoundError()
	}
	return body, nil
}

func parseDiscoveryPage(body []byte, format string) ([]string, string, int, error) {
	if !utf8.Valid(body) {
		return nil, "", 0, discoveryProtocolError()
	}
	m, err := object(body)
	if err != nil || m["error"] != nil && !isNull(m["error"]) {
		return nil, "", 0, discoveryProtocolError()
	}
	field, idField := "data", "id"
	if format == DiscoveryFormatGemini {
		field, idField = "models", "name"
	}
	var rows []json.RawMessage
	if json.Unmarshal(m[field], &rows) != nil || rows == nil {
		return nil, "", 0, discoveryProtocolError()
	}
	if len(rows) > discoveryMaxModels {
		return nil, "", 0, discoveryBoundError()
	}
	ids := make([]string, 0, len(rows))
	for _, raw := range rows {
		row, err := object(raw)
		var id string
		if err != nil || !isString(row[idField]) || json.Unmarshal(row[idField], &id) != nil {
			return nil, "", 0, discoveryProtocolError()
		}
		if format == DiscoveryFormatGemini {
			id = strings.TrimPrefix(id, "models/")
			if !ValidGeminiModel(id) {
				return nil, "", 0, discoveryProtocolError()
			}
			if raw, exists := row["supportedGenerationMethods"]; exists {
				var methods []string
				if json.Unmarshal(raw, &methods) != nil || methods == nil {
					return nil, "", 0, discoveryProtocolError()
				}
				generation := false
				for _, method := range methods {
					generation = generation || method == "generateContent"
				}
				if !generation {
					continue
				}
			}
		} else if !ValidDiscoveredModelID(id) {
			return nil, "", 0, discoveryProtocolError()
		}
		ids = append(ids, id)
	}
	next := ""
	switch format {
	case DiscoveryFormatOpenAI:
		for _, key := range []string{"has_more", "hasMore"} {
			if raw, exists := m[key]; exists {
				var more bool
				if !boolean(raw, &more) || more {
					return nil, "", 0, discoveryProtocolError()
				}
			}
		}
		for _, key := range []string{"next_page", "next", "nextPageToken", "next_page_token", "next_cursor", "nextCursor"} {
			if raw, exists := m[key]; exists && !isNull(raw) {
				if !isString(raw) || json.Unmarshal(raw, &next) != nil || next != "" {
					return nil, "", 0, discoveryProtocolError()
				}
			}
		}
	case DiscoveryFormatAnthropic:
		var more bool
		if !boolean(m["has_more"], &more) {
			return nil, "", 0, discoveryProtocolError()
		}
		if more {
			if !isString(m["last_id"]) || json.Unmarshal(m["last_id"], &next) != nil || !ValidDiscoveredModelID(next) || len(rows) == 0 {
				return nil, "", 0, discoveryProtocolError()
			}
		}
	case DiscoveryFormatGemini:
		if raw, exists := m["nextPageToken"]; exists {
			if !isString(raw) || json.Unmarshal(raw, &next) != nil {
				return nil, "", 0, discoveryProtocolError()
			}
		}
	}
	if next != "" && !validDiscoveryCursor(next) {
		return nil, "", 0, discoveryProtocolError()
	}
	return ids, next, len(rows), nil
}

func validDiscoveryCursor(value string) bool {
	if len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func discoveryCallError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failure("discovery_timeout", 504, ClassTimeout, ConsumptionUnknown)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return failure("discovery_cancelled", 499, ClassCancelled, ConsumptionUnknown)
	}
	var safe *Error
	if errors.As(err, &safe) {
		return safe
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return failure("discovery_timeout", 504, ClassTimeout, ConsumptionUnknown)
	}
	return failure("discovery_network_failed", 502, ClassNetwork, ConsumptionUnknown)
}

func discoveryProtocolError() error {
	return failure("discovery_invalid_response", 502, ClassProtocol, ConsumptionUnknown)
}

func discoveryBoundError() error {
	return failure("discovery_limit_exceeded", 502, ClassProtocol, ConsumptionUnknown)
}
