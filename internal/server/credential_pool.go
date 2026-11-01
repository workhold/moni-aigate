package server

import (
	"io"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func (s *Server) credentialPool(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	v, err := s.Catalog.CredentialPool(c.Request.Context(), c.Param("id"), c.Query("version_id"))
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

// Reject ambiguous nested member fields as well as unknown fields. Neither
// decoder errors nor the request body may enter a response or an audit event.
func decodeCredentialPool(r io.Reader, in *catalog.CredentialPoolInput) bool {
	return decodeCanonicalJSON(r, in, 262144, "expected_version", "members", "credential_id", "secret", "label", "weight", "max_inflight") && in.ExpectedVersion != "" && in.Members != nil
}

func (s *Server) updateCredentialPool(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.CredentialPoolInput
	if !decodeCredentialPool(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "凭据池参数无效，请刷新渠道版本后重试")
		return
	}
	v, err := s.Catalog.UpdateCredentialPool(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) revokeCredential(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	if err := s.Catalog.RevokeCredential(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), c.Param("credentialID")); err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
