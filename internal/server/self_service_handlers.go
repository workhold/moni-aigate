package server

import (
	"errors"
	"mime"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
	"golang.org/x/time/rate"
)

type identityCapability struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}
type identityCapabilities struct {
	EmailDeliveryConfigured bool               `json:"email_delivery_configured"`
	Registration            identityCapability `json:"registration"`
	PasswordReset           identityCapability `json:"password_reset"`
}

func (s *Server) selfServiceCapabilities(c *gin.Context) (identityCapabilities, error) {
	v := identityCapabilities{EmailDeliveryConfigured: s.Config.Mail.Configured()}
	capability := func(flag bool) identityCapability {
		if s.Config.RecoveryMode {
			return identityCapability{Reason: "recovery_mode"}
		}
		if !flag {
			return identityCapability{Reason: "feature_disabled"}
		}
		if !v.EmailDeliveryConfigured {
			return identityCapability{Reason: "mail_not_configured"}
		}
		return identityCapability{Available: true, Reason: "available"}
	}
	v.Registration = capability(s.Config.AuthRegistrationEnabled)
	v.PasswordReset = capability(s.Config.AuthPasswordResetEnabled)
	if v.Registration.Available {
		initialized, err := s.Identity.Initialized(c.Request.Context())
		if err != nil {
			return v, err
		}
		if !initialized {
			v.Registration = identityCapability{Reason: "setup_required"}
		}
	}
	return v, nil
}
func (s *Server) authCapabilities(c *gin.Context) {
	if c.Request.URL.RawQuery != "" {
		s.fail(c, 400, "invalid_request", "请求参数无效")
		return
	}
	v, err := s.selfServiceCapabilities(c)
	if err != nil {
		s.fail(c, 503, "identity_unavailable", "自助身份服务暂不可用")
		return
	}
	s.ok(c, v)
}
func selfServiceMutation(path string) bool {
	switch path {
	case "/api/auth/registration/request", "/api/auth/registration/complete", "/api/auth/password-reset/request", "/api/auth/password-reset/complete":
		return true
	}
	return false
}
func (s *Server) selfServiceIP(c *gin.Context) bool {
	return s.checkRate(c, "identity:ip:"+security.NormalizeClientIP(trustedClientIP(c)), rate.Limit(1.0/12), 5)
}
func (s *Server) selfServiceInput(c *gin.Context, purpose string) bool {
	if !s.validOrigin(c) {
		s.fail(c, 403, "forbidden", "请求来源无效")
		return false
	}
	media, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" || len(params) > 1 || (len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8")) || c.Request.URL.RawQuery != "" {
		s.fail(c, 400, "invalid_request", "请求参数无效")
		return false
	}
	v, err := s.selfServiceCapabilities(c)
	if err != nil || (purpose == "registration" && !v.Registration.Available) || (purpose == "password_reset" && !v.PasswordReset.Available) {
		s.fail(c, 503, "identity_unavailable", "自助身份服务暂不可用")
		return false
	}
	return true
}
func (s *Server) selfServiceSubject(c *gin.Context, purpose, subject string, requestMail bool) bool {
	status := s.rateDecision("identity:subject:"+s.Config.Secrets.Digest(purpose+"\x00"+subject), rate.Limit(1.0/60), 5)
	if status == 0 {
		return true
	}
	if status == 503 {
		s.fail(c, 503, "limiter_unavailable", "限流服务暂不可用")
	} else if requestMail {
		s.acceptIdentityMail(c)
	} else {
		s.fail(c, 429, "rate_limited", "请求过于频繁，请稍后重试")
	}
	return false
}
func (s *Server) acceptIdentityMail(c *gin.Context) {
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"accepted": true}, "request_id": c.GetString("request_id")})
}
func (s *Server) requestIdentityMail(purpose string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.selfServiceInput(c, purpose) {
			return
		}
		var in struct {
			Email *string `json:"email"`
		}
		if !decodeCanonicalJSON(c.Request.Body, &in, 4096, "email") || in.Email == nil {
			s.fail(c, 400, "invalid_request", "请求参数无效")
			return
		}
		email, err := identity.NormalizeIdentityEmail(*in.Email)
		if err != nil {
			s.fail(c, 400, "invalid_request", "请求参数无效")
			return
		}
		if !s.selfServiceSubject(c, purpose, email, true) {
			return
		}
		if err = s.Identity.RequestIdentityMail(c.Request.Context(), purpose, email, s.Config.Origin); err != nil {
			s.identityServiceError(c, err)
			return
		}
		s.acceptIdentityMail(c)
	}
}
func (s *Server) identityHashPermit(c *gin.Context) (func(), bool) {
	s.identityHashesMu.Lock()
	if s.identityHashes >= 4 {
		s.identityHashesMu.Unlock()
		s.fail(c, 429, "rate_limited", "身份操作繁忙，请稍后重试")
		return nil, false
	}
	s.identityHashes++
	s.identityHashesMu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { s.identityHashesMu.Lock(); s.identityHashes--; s.identityHashesMu.Unlock() }) }, true
}
func (s *Server) completeIdentityRegistration(c *gin.Context) {
	if !s.selfServiceInput(c, "registration") {
		return
	}
	var in struct {
		Token      *string `json:"token"`
		Password   *string `json:"password"`
		TenantName *string `json:"tenant_name"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 4096, "token", "password", "tenant_name") || in.Token == nil || in.Password == nil || in.TenantName == nil {
		s.fail(c, 400, "invalid_request", "请求参数无效")
		return
	}
	if !s.selfServiceSubject(c, "registration", *in.Token, false) {
		return
	}
	release, ok := s.identityHashPermit(c)
	if !ok {
		return
	}
	defer release()
	if err := s.Identity.CompleteRegistration(c.Request.Context(), *in.Token, *in.Password, *in.TenantName); err != nil {
		s.identityServiceError(c, err)
		return
	}
	s.ok(c, gin.H{"completed": true, "login_required": true})
}
func (s *Server) completeIdentityPasswordReset(c *gin.Context) {
	if !s.selfServiceInput(c, "password_reset") {
		return
	}
	var in struct {
		Token    *string `json:"token"`
		Password *string `json:"password"`
		MFACode  *string `json:"mfa_code"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 4096, "token", "password", "mfa_code") || in.Token == nil || in.Password == nil || in.MFACode == nil {
		s.fail(c, 400, "invalid_request", "请求参数无效")
		return
	}
	if !s.selfServiceSubject(c, "password_reset", *in.Token, false) {
		return
	}
	release, ok := s.identityHashPermit(c)
	if !ok {
		return
	}
	defer release()
	if err := s.Identity.CompletePasswordReset(c.Request.Context(), *in.Token, *in.Password, *in.MFACode); err != nil {
		s.identityServiceError(c, err)
		return
	}
	s.ok(c, gin.H{"completed": true, "login_required": true})
}
func (s *Server) identityServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, identity.ErrIdentityRequestInvalid):
		s.fail(c, 400, "invalid_request", "请求参数无效")
	case errors.Is(err, identity.ErrIdentityProofInvalid):
		s.fail(c, 400, "identity_proof_invalid", "邮箱证明无效，或账号当前所需验证未通过")
	default:
		s.fail(c, 503, "identity_unavailable", "自助身份服务暂不可用")
	}
}
