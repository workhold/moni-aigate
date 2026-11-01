package server

import (
	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func (s *Server) preflightChannelOperation(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.ChannelOperationInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 65536, "action", "targets", "channel_id", "expected_version", "expected_revision") {
		s.fail(c, 400, "invalid_request", "批量操作参数无效，请重新选择渠道后预检")
		return
	}
	v, err := s.Catalog.PreviewChannelOperation(c.Request.Context(), in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) applyChannelOperation(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		OperationID string                           `json:"operation_id"`
		Action      string                           `json:"action"`
		Targets     []catalog.ChannelOperationTarget `json:"targets"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 65536, "operation_id", "action", "targets", "channel_id", "expected_version", "expected_revision") {
		s.fail(c, 400, "invalid_request", "批量操作参数无效，请重新选择渠道后预检")
		return
	}
	v, err := s.Catalog.ApplyChannelOperation(c.Request.Context(), sessionOf(c).User.ID, in.OperationID, catalog.ChannelOperationInput{Action: in.Action, Targets: in.Targets})
	if err != nil {
		s.error(c, err)
		return
	}
	// A rejected receipt is a committed decision, not a successful state change.
	// The UI must inspect v.Status before reporting the operation's outcome.
	s.ok(c, v)
}

func (s *Server) channelOperations(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	s.page(c, catalog.ChannelOperationListSQL())
}

func (s *Server) channelOperation(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	v, err := s.Catalog.ChannelOperation(c.Request.Context(), c.Param("operationID"))
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}
