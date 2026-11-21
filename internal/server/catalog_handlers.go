package server

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func (s *Server) createModel(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.ModelInput
	if !s.bind(c, &in) {
		return
	}
	id, e := s.Catalog.CreateModel(c.Request.Context(), sessionOf(c).User.ID, in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"id": id})
}
func (s *Server) updateModel(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !s.bind(c, &in) {
		return
	}
	if e := s.Catalog.SetModelEnabled(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Enabled); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) saveChannel(c *gin.Context, id string) {
	if !s.admin(c) {
		return
	}
	var in catalog.ChannelInput
	if !s.bind(c, &in) {
		return
	}
	for _, raw := range in.AllowedCIDRs {
		if _, e := netip.ParsePrefix(raw); e != nil {
			s.fail(c, 400, "invalid_configuration", "出口CIDR格式无效")
			return
		}
	}
	if in.BaseURL != "" {
		u, e := url.Parse(in.BaseURL)
		if e != nil || (u.Scheme == "http" && len(in.AllowedCIDRs) == 0) {
			s.fail(c, 400, "invalid_configuration", "HTTP渠道须显式允许出口CIDR")
			return
		}
	}
	id, e := s.Catalog.SaveChannel(c.Request.Context(), sessionOf(c).User.ID, id, in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"id": id, "requires_route_publish": true})
}
func (s *Server) createChannel(c *gin.Context) { s.saveChannel(c, "") }
func (s *Server) updateChannel(c *gin.Context) { s.saveChannel(c, c.Param("id")) }
func (s *Server) routeRevisions(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var after *catalog.RouteRevisionCursor
	if raw := c.Query("cursor"); raw != "" {
		if len(raw) > 512 {
			s.fail(c, 400, "invalid_cursor", "分页游标无效")
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		var value cursor
		if err != nil || json.Unmarshal(decoded, &value) != nil || value.ID == "" || value.CreatedAt.IsZero() {
			s.fail(c, 400, "invalid_cursor", "分页游标无效")
			return
		}
		after = &catalog.RouteRevisionCursor{CreatedAt: value.CreatedAt, ID: value.ID}
	}
	rows, next, err := s.Catalog.ListRouteRevisions(c.Request.Context(), c.Param("id"), after)
	if err != nil {
		s.error(c, err)
		return
	}
	data := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		data = append(data, gin.H{"id": row.ID, "revision": row.Revision, "created_at": row.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z"), "profile_version": row.ProfileVersion, "release_intent": row.ReleaseIntent, "active": row.Active, "current_profile": row.CurrentProfile, "bindings": row.Bindings})
	}
	nextCursor := ""
	if next != nil {
		raw, _ := json.Marshal(cursor{CreatedAt: next.CreatedAt, ID: next.ID})
		nextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	c.JSON(200, gin.H{"data": data, "request_id": c.GetString("request_id"), "next_cursor": nextCursor})
}

func (s *Server) activateRouteRevision(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Expected int64 `json:"expected_revision"`
	}
	if !s.bind(c, &in) {
		return
	}
	target, e := strconv.ParseInt(c.Param("revision"), 10, 64)
	if e != nil || target <= 0 {
		s.fail(c, 400, "invalid_request", "路由版本无效")
		return
	}
	revision, e := s.Catalog.ActivateRouteRevision(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Expected, target)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"revision": revision})
}
func (s *Server) publishBindings(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.RoutePublishInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 1048576, "expected_revision", "bindings", "cost_policy", "mode", "unknown_cost_policy", "channel_id", "physical_model_id", "capability", "expose_to_customer", "upstream_model", "priority", "weight", "max_inflight", "max_input_cost_rate", "max_output_cost_rate", "release_intent", "release_evidence_id", "scoring", "auto", "allow_degradation", "quality_floor", "min_margin_rate", "stickiness", "enabled", "max_age_s", "break_on", "context_limit", "input_upper_bound", "output_limit", "supports_tools", "supports_parallel_tool_calls", "usage_semantics", "modalities", "quality_tier", "quality_score", "max_concurrency", "supported_parameters", "failure_threshold", "open_ms", "half_open_tokens", "cache_ttl_seconds") {
		s.fail(c, 400, "invalid_request", "路由发布参数无效")
		return
	}
	var revision int64
	breaker, e := in.BreakerConfig()
	if e != nil {
		s.fail(c, 400, "invalid_request", "熔断参数无效")
		return
	}
	if in.ReleaseIntent == catalog.ReleaseCommercial {
		revision, e = s.Catalog.PublishDynamicWithDataPlanePolicy(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Expected, in.Bindings, in.DynamicPolicy(), catalog.ReleaseCommercial, breaker, in.CacheTTL())
	} else {
		revision, e = s.Catalog.PublishDynamicWithDataPlanePolicy(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Expected, in.Bindings, in.DynamicPolicy(), catalog.ReleaseTestOnly, breaker, in.CacheTTL())
	}
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"revision": revision})
}
func networkPolicy(raw string, cidrs []string) provider.NetworkPolicy {
	return catalog.NetworkPolicy(raw, cidrs)
}
