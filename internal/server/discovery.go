package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"golang.org/x/time/rate"
)

func (s *Server) discoveryFailure(c *gin.Context, status int, code, id string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": "模型发现未完成，请查看历史记录或重新发现"}, "request_id": c.GetString("request_id"), "discovery_id": id})
}

func (s *Server) discoverModels(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	// This narrow operation never accepts an arbitrary endpoint or secret.
	var in struct {
		Format          string `json:"format"`
		ExpectedVersion string `json:"expected_version"`
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		s.fail(c, 400, "invalid_request", "发现参数无效")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	// Decoding into a map first detects repeated top-level configuration keys.
	var fields map[string]json.RawMessage
	var keys []string
	if json.Unmarshal(raw, &fields) == nil {
		probe := json.NewDecoder(bytes.NewReader(raw))
		if token, e := probe.Token(); e == nil && token == json.Delim('{') {
			for probe.More() {
				token, e := probe.Token()
				if e != nil {
					break
				}
				var value json.RawMessage
				if probe.Decode(&value) != nil {
					break
				}
				keys = append(keys, token.(string))
			}
		}
	}
	if decoder.Decode(&in) != nil || decoder.Decode(new(any)) != io.EOF || fields == nil || len(fields) != len(keys) || !catalog.ValidDiscoveryFormat(in.Format) || in.ExpectedVersion == "" {
		s.fail(c, 400, "invalid_request", "请选择目录格式并刷新渠道版本")
		return
	}
	if !s.checkRate(c, "discovery:"+c.Param("id"), rate.Every(time.Minute), 1) {
		return
	}
	if !s.acquireProbe() {
		s.fail(c, 429, "discovery_capacity_limited", "当前平台发现或探测并发已满")
		return
	}
	defer s.releaseProbe()
	target, err := s.Catalog.BeginModelDiscovery(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.ExpectedVersion, in.Format)
	if err != nil {
		s.error(c, err)
		return
	}
	result := provider.DiscoveryResult{}
	err = s.guardContext(c.Request.Context())
	if err == nil {
		err = s.Catalog.RecheckModelDiscovery(c.Request.Context(), target.ID)
	}
	if err == nil {
		result, err = s.Provider.DiscoverModels(c.Request.Context(), target.Endpoint, provider.DiscoveryOptions{Format: in.Format})
	}
	code := ""
	if err != nil {
		code = "discovery_failed"
		var failure *provider.Error
		if errors.As(err, &failure) {
			code = failure.Code
		}
	}
	ctx, cancel := cleanupContext()
	defer cancel()
	if e := s.Catalog.FinishModelDiscovery(ctx, target.ID, result, code); e != nil {
		s.discoveryFailure(c, 503, "discovery_pending", target.ID)
		return
	}
	if err != nil {
		s.discoveryFailure(c, 503, code, target.ID)
		return
	}
	v, err := s.Catalog.ModelDiscovery(ctx, c.Param("id"), target.ID)
	if err != nil {
		s.discoveryFailure(c, 503, "discovery_pending", target.ID)
		return
	}
	s.ok(c, v)
}

func (s *Server) modelDiscoveries(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	s.page(c, catalog.DiscoveryListSQL(), c.Param("id"))
}

func (s *Server) modelDiscovery(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	v, err := s.Catalog.ModelDiscovery(c.Request.Context(), c.Param("id"), c.Param("discoveryID"))
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}
