package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

var releaseEvidenceJSONFields = []string{
	"channel_id", "channel_version_id", "model_id", "profile_version", "upstream_model", "variant_key", "supplier", "protocol", "qualification", "observed_at",
	"evidence_commit", "evidence_ref", "evidence_sha256", "source_mode", "source_repository", "source_commit", "license_ref", "notice_ref", "modification_ref",
	"boundary_ref", "usage_ref", "retry_ref", "cost_ref", "cost_status", "open_issue_codes", "credential_ids",
}

func (s *Server) recordReleaseEvidence(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.ReleaseEvidenceInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 1048576, releaseEvidenceJSONFields...) {
		s.fail(c, 400, "release_evidence_invalid", "发布资格证据格式无效")
		return
	}
	value, err := s.Catalog.RecordChannelProfileEvidence(c.Request.Context(), sessionOf(c).User.ID, in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, value)
}
func (s *Server) withdrawReleaseEvidence(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Reason string `json:"reason_code"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 4096, "reason_code") {
		s.fail(c, 400, "release_evidence_invalid", "撤回理由格式无效")
		return
	}
	if err := s.Catalog.WithdrawChannelProfileEvidence(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Reason); err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) releaseEvidence(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	value, err := s.Catalog.ChannelProfileEvidence(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, value)
}
func (s *Server) releaseEvidenceList(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	values, next, err := s.Catalog.ListChannelProfileEvidence(c.Request.Context(), c.Query("channel_id"), c.Query("model_id"), c.Query("cursor"))
	if err != nil {
		s.error(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": values, "next_cursor": next, "request_id": c.GetString("request_id")})
}
