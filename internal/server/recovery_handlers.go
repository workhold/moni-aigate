package server

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"time"
)

func (s *Server) recoveryAccess(c *gin.Context, write bool) bool {
	v := sessionOf(c)
	if v.User.PlatformRole == "none" {
		s.fail(c, 403, "forbidden", "需要平台权限")
		return false
	}
	if write && (v.ReauthenticatedUntil == nil || !v.ReauthenticatedUntil.After(time.Now())) {
		s.fail(c, 403, "reauth_required", "需要再次验证身份")
		return false
	}
	return true
}
func (s *Server) recoveryError(c *gin.Context, e error) {
	if errors.Is(e, operations.ErrRecoveryConflict) {
		s.fail(c, 409, "recovery_conflict", "恢复阶段、提案或账本校验冲突")
		return
	}
	if errors.Is(e, operations.ErrRecoveryForbidden) {
		s.fail(c, 403, "forbidden", "恢复需要不同的财务制单及审批账号")
		return
	}
	s.error(c, e)
}
func (s *Server) recoveryStatus(c *gin.Context) {
	if !s.recoveryAccess(c, false) {
		return
	}
	v, e := operations.RecoveryState(c.Request.Context(), s.Pool)
	if e != nil {
		s.recoveryError(c, e)
		return
	}
	s.ok(c, gin.H{"gate": v, "recovery_mode": s.Config.RecoveryMode, "restart_required": s.Config.RecoveryMode && !v.Required})
}
func (s *Server) proposeRecovery(c *gin.Context) {
	if !s.recoveryAccess(c, true) || !s.financial(c, false) {
		return
	}
	var in operations.RecoveryProposal
	if !s.bind(c, &in) {
		return
	}
	in.ActorID = sessionOf(c).User.ID
	v, e := operations.ProposeRecovery(c.Request.Context(), s.Pool, in)
	if e != nil {
		s.recoveryError(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) approveRecovery(c *gin.Context) {
	if !s.recoveryAccess(c, true) || !s.financial(c, true) {
		return
	}
	var in operations.RecoveryApproval
	if !s.bind(c, &in) {
		return
	}
	in.ActorID = sessionOf(c).User.ID
	v, e := operations.ApproveRecovery(c.Request.Context(), s.Pool, in)
	if e != nil {
		s.recoveryError(c, e)
		return
	}
	s.ok(c, gin.H{"gate": v, "restart_required": true})
}
