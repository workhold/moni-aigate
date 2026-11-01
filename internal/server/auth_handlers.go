package server

import (
	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/security"
	"golang.org/x/time/rate"
	"net/http"
	"strings"
	"time"
)

func (s *Server) cookie(c *gin.Context, raw string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("moni_session", raw, 43200, "/", "", s.Config.CookieSecure, true)
}
func (s *Server) login(c *gin.Context) {
	if !s.validOrigin(c) {
		s.fail(c, 403, "origin_invalid", "请求来源无效")
		return
	}
	if !s.checkRate(c, "login:"+trustedClientIP(c), rate.Every(12*time.Second), 5) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !s.bind(c, &in) {
		return
	}
	old, _ := c.Cookie("moni_session")
	raw, mfa, enroll, e := s.Identity.Login(c.Request.Context(), in.Email, in.Password, s.Config.Secrets.Digest(old))
	if e != nil {
		s.error(c, e)
		return
	}
	s.cookie(c, raw)
	s.ok(c, gin.H{"mfa_required": mfa, "enrollment_required": enroll})
}

func trustedClientIP(c *gin.Context) string {
	// X-Real-IP is a single address supplied by the configured edge, not an
	// appendable forwarding chain. Ambiguous input keeps the transport identity.
	values := c.Request.Header.Values("X-Real-IP")
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return c.RemoteIP()
	}
	return c.ClientIP()
}
func (s *Server) me(c *gin.Context) { s.ok(c, sessionOf(c)) }
func (s *Server) logout(c *gin.Context) {
	if e := s.Identity.Logout(c.Request.Context(), sessionOf(c).Digest, sessionOf(c).FamilyID); e != nil {
		s.error(c, e)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("moni_session", "", -1, "/", "", s.Config.CookieSecure, true)
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) setupMFA(c *gin.Context) {
	secret, codes, e := s.Identity.SetupMFA(c.Request.Context(), sessionOf(c))
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"secret": secret, "otpauth_url": security.OTPURL(sessionOf(c).User.Email, secret), "recovery_codes": codes})
}
func (s *Server) mfa(c *gin.Context, enroll bool) {
	if !s.checkRate(c, "mfa:"+sessionOf(c).User.ID, rate.Every(10*time.Second), 5) {
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if !s.bind(c, &in) {
		return
	}
	raw, e := s.Identity.VerifyMFA(c.Request.Context(), sessionOf(c), in.Code, enroll)
	if e != nil {
		s.error(c, e)
		return
	}
	s.cookie(c, raw)
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) enableMFA(c *gin.Context) { s.mfa(c, true) }
func (s *Server) verifyMFA(c *gin.Context) { s.mfa(c, false) }
func (s *Server) reauth(c *gin.Context) {
	if !s.checkRate(c, "reauth:"+sessionOf(c).User.ID, rate.Every(12*time.Second), 5) {
		return
	}
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !s.bind(c, &in) {
		return
	}
	if e := s.Identity.Reauthenticate(c.Request.Context(), sessionOf(c), in.Password, in.Code); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) switchTenant(c *gin.Context) {
	var in struct {
		TenantID string `json:"tenant_id"`
	}
	if !s.bind(c, &in) {
		return
	}
	raw, e := s.Identity.Switch(c.Request.Context(), sessionOf(c), in.TenantID)
	if e != nil {
		s.error(c, e)
		return
	}
	s.cookie(c, raw)
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) createKey(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	if s.Config.RecoveryMode {
		s.fail(c, 503, "recovery_mode", "恢复模式暂停变更")
		return
	}
	var in identity.KeyInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 65536, "name", "scopes", "lifetime_limit", "expires_at", "allowed_regions", "allowed_client_cidrs", "rpm_limit", "burst_limit", "concurrency_limit") {
		s.fail(c, 400, "invalid_request", "API Key 参数无效")
		return
	}
	v, e := s.Identity.CreateKey(c.Request.Context(), sessionOf(c), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) keyAccessPolicy(c *gin.Context) {
	v, e := s.Identity.KeyAccessPolicy(c.Request.Context(), sessionOf(c), c.Param("id"))
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) updateKeyAccessPolicy(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	var in identity.KeyAccessPolicyInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "expected_revision", "allowed_client_cidrs") {
		s.fail(c, 400, "invalid_request", "来源访问策略参数无效")
		return
	}
	v, e := s.Identity.UpdateKeyAccessPolicy(c.Request.Context(), sessionOf(c), c.Param("id"), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) keyRuntimeLimits(c *gin.Context) {
	v, e := s.Identity.KeyRuntimeLimits(c.Request.Context(), sessionOf(c), c.Param("id"))
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) updateKeyRuntimeLimits(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	var in identity.KeyLimitsInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "expected_revision", "rpm_limit", "burst_limit", "concurrency_limit") {
		s.fail(c, 400, "invalid_request", "运行限额参数无效")
		return
	}
	v, e := s.Identity.UpdateKeyRuntimeLimits(c.Request.Context(), sessionOf(c), c.Param("id"), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) keyConfiguration(c *gin.Context) {
	v, e := s.Identity.KeyConfiguration(c.Request.Context(), sessionOf(c), c.Param("id"))
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) updateKeyConfiguration(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	var in identity.KeyConfigurationInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 65536, "expected_revision", "name", "scopes", "allowed_regions", "lifetime_limit", "expires_at") {
		s.fail(c, 400, "invalid_request", "基本配置参数无效")
		return
	}
	v, e := s.Identity.UpdateKeyConfiguration(c.Request.Context(), sessionOf(c), c.Param("id"), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) revokeKey(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	if e := s.Identity.RevokeKey(c.Request.Context(), sessionOf(c), c.Param("id")); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) createUser(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"platform_role"`
	}
	if !s.bind(c, &in) {
		return
	}
	id, e := s.Identity.CreateUser(c.Request.Context(), sessionOf(c).User.ID, in.Email, in.Password, in.Role)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"id": id})
}
func (s *Server) createTenant(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Name          string   `json:"name"`
		Email         string   `json:"email"`
		Password      string   `json:"password"`
		OwnerEmail    string   `json:"owner_email"`
		OwnerPassword string   `json:"owner_password"`
		ModelIDs      []string `json:"model_ids"`
	}
	if !s.bind(c, &in) {
		return
	}
	if in.Email == "" {
		in.Email = in.OwnerEmail
	}
	if in.Password == "" {
		in.Password = in.OwnerPassword
	}
	id, e := s.Identity.CreateTenant(c.Request.Context(), sessionOf(c).User.ID, in.Name, in.Email, in.Password, in.ModelIDs)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"id": id})
}
func (s *Server) updateTenant(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Status   *string   `json:"status"`
		ModelIDs *[]string `json:"model_ids"`
		Regions  *[]string `json:"allowed_regions"`
	}
	if !s.bind(c, &in) {
		return
	}
	if e := s.Identity.UpdateTenant(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Status, in.ModelIDs, in.Regions); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) addMember(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		All      bool     `json:"allow_all_models"`
		ModelIDs []string `json:"model_ids"`
	}
	if !s.bind(c, &in) {
		return
	}
	id, e := s.Identity.AddMember(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), in.Email, in.Password, in.Role, in.All, in.ModelIDs)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"user_id": id})
}
func (s *Server) updateMember(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in struct {
		Role     *string   `json:"role"`
		Status   *string   `json:"status"`
		All      *bool     `json:"allow_all_models"`
		ModelIDs *[]string `json:"model_ids"`
	}
	if !s.bind(c, &in) {
		return
	}
	if e := s.Identity.UpdateMember(c.Request.Context(), sessionOf(c).User.ID, c.Param("id"), c.Param("uid"), in.Role, in.Status, in.All, in.ModelIDs); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}

func (s *Server) customerMembers(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	if sessionOf(c).Tenant.Role != "owner" {
		s.fail(c, 403, "forbidden", "需要租户owner权限")
		return
	}
	v, e := s.query(c.Request.Context(), `SELECT m.user_id AS id,m.user_id,u.email,m.role,m.status,m.allow_all_models,ARRAY(SELECT model_id FROM member_model_grants g WHERE g.tenant_id=m.tenant_id AND g.user_id=m.user_id) AS model_ids FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.tenant_id=$1 ORDER BY u.email LIMIT 1000`, tid)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) customerAddMember(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	var in struct {
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		All      bool     `json:"allow_all_models"`
		ModelIDs []string `json:"model_ids"`
	}
	if !s.bind(c, &in) {
		return
	}
	id, e := s.Identity.AddMember(c.Request.Context(), sessionOf(c).User.ID, tid, in.Email, in.Password, in.Role, in.All, in.ModelIDs, true)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"user_id": id})
}
func (s *Server) customerUpdateMember(c *gin.Context) {
	if !s.sensitive(c) {
		return
	}
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	var in struct {
		Role     *string   `json:"role"`
		Status   *string   `json:"status"`
		All      *bool     `json:"allow_all_models"`
		ModelIDs *[]string `json:"model_ids"`
	}
	if !s.bind(c, &in) {
		return
	}
	if e := s.Identity.UpdateMember(c.Request.Context(), sessionOf(c).User.ID, tid, c.Param("id"), in.Role, in.Status, in.All, in.ModelIDs, true); e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}

func (s *Server) changePassword(c *gin.Context) {
	if !s.checkRate(c, "password:"+sessionOf(c).User.ID, rate.Every(12*time.Second), 5) {
		return
	}
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
		Code    string `json:"code"`
	}
	if !s.bind(c, &in) {
		return
	}
	if len(in.New) < 12 || len(in.New) > 72 {
		s.fail(c, 400, "invalid_password", "新密码须为12至72字节")
		return
	}
	if e := s.Identity.ChangePassword(c.Request.Context(), sessionOf(c), in.Current, in.New, in.Code); e != nil {
		s.error(c, e)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("moni_session", "", -1, "/", "", s.Config.CookieSecure, true)
	s.ok(c, gin.H{"reauthenticate": true})
}
