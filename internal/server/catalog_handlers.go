package server

import (
	"net/netip"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
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
func (s *Server) publishBindings(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.RoutePublishInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 1048576, "expected_revision", "bindings", "cost_policy", "mode", "unknown_cost_policy", "channel_id", "upstream_model", "priority", "weight", "max_inflight", "max_input_cost_rate", "max_output_cost_rate") {
		s.fail(c, 400, "invalid_request", "路由发布参数无效")
		return
	}
	policy := routing.DefaultCostPolicy()
	if in.CostPolicy != nil {
		policy = *in.CostPolicy
	}
	revision, e := s.Catalog.PublishWithCostPolicy(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Expected, in.Bindings, policy)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"revision": revision})
}
func networkPolicy(raw string, cidrs []string) provider.NetworkPolicy {
	return catalog.NetworkPolicy(raw, cidrs)
}
