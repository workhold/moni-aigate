package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/config"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/media"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/realtime"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"github.com/moni-ai/moni-aigate/internal/security"
	"github.com/moni-ai/moni-aigate/internal/tasks"
	"github.com/moni-ai/moni-aigate/internal/telemetry"
	"golang.org/x/time/rate"
)

type Server struct {
	Guard                  func(context.Context) error
	Config                 config.Config
	Pool                   *pgxpool.Pool
	Identity               *identity.Store
	Catalog                *catalog.Store
	Billing                *billing.Store
	Vouchers               *billing.VoucherService
	Payments               paymentService
	Procurement            *procurement.Store
	Alerts                 alertService
	PeriodicReader         *periodicbudget.Reader
	PeriodicCommander      *periodicbudget.Commander
	Provider               *provider.Client
	ImageAdapter           provider.SynchronousMediaAdapter
	ImageProfile           media.Profile
	ImageService           *media.Orchestrator
	Realtime               *realtime.Service
	RealtimeAdapter        *provider.MockRealtimeAdapter
	RealtimeTimeout        time.Duration
	Routing                *routing.Engine
	TaskStore              *tasks.PostgresStore
	TaskAdapter            provider.TaskAdapter
	MediaAdapter           provider.SynchronousMediaAdapter
	RealtimeService        *realtime.Service
	TaskGrants             sync.Map
	MediaIdempotency       sync.Map
	SpeechIdempotencyLocks sync.Map
	Log                    *telemetry.Logger
	ready                  atomic.Bool
	Requests               atomic.Uint64
	Failures               atomic.Uint64
	Streams                atomic.Int64
	mu                     sync.Mutex
	limits                 map[string]*rate.Limiter
	limitSeen              map[string]time.Time
	keyRuntime             map[string]*keyRuntimeState
	global                 chan struct{}
	probes                 chan struct{}
	tenantPermits          map[string]int
	keyPermits             map[string]int
	admission              admissionQueue
	publicCatalog          publicCatalogAdmission
	identityHashesMu       sync.Mutex
	identityHashes         int
	commerce               commerceAdmission
	pricing                pricingAdmission
	alertAdmission         alertAdmission
	periodicAdmission      periodicBudgetAdmission
	paymentAdmission       paymentAdmission
	paymentNotifyAdmission paymentNotificationAdmission
	assets                 fs.FS
	imageMu                sync.Mutex
	imageByIdempotency     map[string]*imageRequestRecord
	imageByID              map[string]*imageRequestRecord
}

func New(c config.Config, p *pgxpool.Pool, assets fs.FS, log *telemetry.Logger) *Server {
	identities := identity.New(p, c.Secrets)
	identities.EnforcePlatformMFA = c.MFAEnforcePlatform
	taskStore, _ := tasks.NewPostgresStore(p, "")
	runtime := newMediaProviderRuntime(c)
	return &Server{Config: c, Pool: p, Identity: identities, Catalog: catalog.New(p, c.Secrets), Billing: billing.New(p), Procurement: procurement.New(p), Provider: provider.NewClient(c.ProviderTimeouts.ClientOptions()), ImageAdapter: runtime.mediaAdapter, ImageProfile: runtime.imageProfile, ImageService: runtime.imageService, Realtime: runtime.realtimeService, RealtimeAdapter: runtime.realtimeAdapter, RealtimeTimeout: realtime.DefaultTurnTTL, Routing: routing.NewEngine(routing.Config{}), TaskStore: taskStore, TaskAdapter: runtime.taskAdapter, MediaAdapter: runtime.mediaAdapter, Log: log, limits: map[string]*rate.Limiter{}, limitSeen: map[string]time.Time{}, keyRuntime: map[string]*keyRuntimeState{}, global: make(chan struct{}, 100), probes: make(chan struct{}, 4), tenantPermits: map[string]int{}, keyPermits: map[string]int{}, assets: assets, imageByIdempotency: make(map[string]*imageRequestRecord), imageByID: make(map[string]*imageRequestRecord)}
}
func (s *Server) Ready(v bool) { s.ready.Store(v) }
func (s *Server) Engine() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Only an explicitly trusted transport peer may supply the single client IP
	// header. The edge proxy must overwrite it rather than append client input.
	r.RemoteIPHeaders = []string{"X-Real-IP"}
	if err := r.SetTrustedProxies(s.Config.TrustedProxyCIDRs); err != nil {
		panic("invalid trusted proxy configuration")
	}
	r.Use(s.metadata())
	r.GET("/health/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "alive"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
		defer cancel()
		if !s.ready.Load() || s.guardContext(ctx) != nil || s.Pool.Ping(ctx) != nil {
			c.JSON(503, gin.H{"status": "unready"})
			return
		}
		c.JSON(200, gin.H{"status": "ready", "recovery_mode": s.Config.RecoveryMode})
	})
	r.GET("/metrics", s.metrics)
	r.POST("/api/auth/login", s.login)
	r.GET("/api/auth/capabilities", s.authCapabilities)
	r.POST("/api/auth/registration/request", s.requestIdentityMail("registration"))
	r.POST("/api/auth/registration/complete", s.completeIdentityRegistration)
	r.POST("/api/auth/password-reset/request", s.requestIdentityMail("password_reset"))
	r.POST("/api/auth/password-reset/complete", s.completeIdentityPasswordReset)
	r.GET("/api/public/models", s.publicModels)
	r.GET("/api/public/models/:id", s.publicModel)
	s.registerPaymentNotifications(r)
	a := r.Group("/api")
	a.Use(s.session())
	a.GET("/me", s.me)
	a.POST("/auth/logout", s.logout)
	a.POST("/auth/mfa/setup", s.setupMFA)
	a.POST("/auth/mfa/enable", s.enableMFA)
	a.POST("/auth/mfa/verify", s.verifyMFA)
	a.POST("/auth/reauth", s.reauth)
	a.Use(s.requireMFA())
	a.POST("/auth/password", s.changePassword)
	a.GET("/recovery/status", s.recoveryStatus)
	a.POST("/recovery/propose", s.proposeRecovery)
	a.POST("/recovery/approve", s.approveRecovery)
	a.POST("/tenant/switch", s.switchTenant)
	a.GET("/members", s.customerMembers)
	a.POST("/members", s.customerAddMember)
	a.PATCH("/members/:id", s.customerUpdateMember)
	a.GET("/models", s.models)
	a.GET("/prices", s.prices)
	a.GET("/keys", s.keys)
	a.POST("/keys", s.createKey)
	a.GET("/keys/:id/access-policy", s.keyAccessPolicy)
	a.PUT("/keys/:id/access-policy", s.updateKeyAccessPolicy)
	a.GET("/keys/:id/runtime-limits", s.keyRuntimeLimits)
	a.PUT("/keys/:id/runtime-limits", s.updateKeyRuntimeLimits)
	a.GET("/keys/:id/configuration", s.keyConfiguration)
	a.PUT("/keys/:id/configuration", s.updateKeyConfiguration)
	a.DELETE("/keys/:id", s.revokeKey)
	a.GET("/wallet", s.wallet)
	a.GET("/wallet/sources", s.walletSources)
	a.GET("/journals", s.journals)
	a.GET("/reports/summary", s.customerReport)
	a.GET("/requests", s.requests)
	a.GET("/requests/:id", s.request)
	a.GET("/requests/:id/funding", s.requestFunding)
	s.registerWalletCommerce(a)
	p := a.Group("/platform")
	p.Use(s.platform())
	p.GET("/wallet-sources", s.platformWalletSources)
	p.GET("/wallet-sources/check", s.checkWalletSources)
	s.registerPlatformCommerce(p)
	s.registerPayments(a, p)
	s.registerPricing(a, p)
	s.registerAlerts(p)
	s.registerPeriodicBudgets(a, p)
	p.GET("/tenants", s.tenants)
	p.GET("/users", s.users)
	p.POST("/users", s.createUser)
	p.POST("/tenants", s.createTenant)
	p.PATCH("/tenants/:id", s.updateTenant)
	p.GET("/tenants/:id/members", s.members)
	p.POST("/tenants/:id/members", s.addMember)
	p.PATCH("/tenants/:id/members/:uid", s.updateMember)
	p.POST("/channel-operations/preflight", s.preflightChannelOperation)
	p.POST("/channel-operations", s.applyChannelOperation)
	p.GET("/channel-operations", s.channelOperations)
	p.GET("/channel-operations/:operationID", s.channelOperation)
	p.GET("/channels", s.channels)
	p.POST("/channels", s.createChannel)
	p.PATCH("/channels/:id", s.updateChannel)
	p.GET("/channels/:id/credential-pool", s.credentialPool)
	p.PUT("/channels/:id/credential-pool", s.updateCredentialPool)
	p.DELETE("/channels/:id/credentials/:credentialID", s.revokeCredential)
	p.POST("/channels/:id/probe", s.probe)
	p.POST("/channels/:id/discover-models", s.discoverModels)
	p.GET("/channels/:id/model-discoveries", s.modelDiscoveries)
	p.GET("/channels/:id/model-discoveries/:discoveryID", s.modelDiscovery)
	p.GET("/models", s.platformModels)
	p.POST("/models", s.createModel)
	p.PATCH("/models/:id", s.updateModel)
	p.GET("/models/:id/aliases", s.modelAliases)
	p.POST("/models/:id/aliases", s.createModelAlias)
	p.POST("/model-aliases/:alias/retire", s.retireModelAlias)
	p.GET("/models/:id/publication", s.modelPublication)
	p.PUT("/models/:id/publication", s.setModelPublication)
	p.PUT("/models/:id/bindings", s.publishBindings)
	p.POST("/price-versions", s.publishDefaultPrice)
	p.GET("/credits", s.credits)
	p.POST("/credits", s.createCredit)
	p.POST("/credits/:id/approve", s.approveCredit)
	p.GET("/refunds", s.refunds)
	p.POST("/refunds", s.createRefund)
	p.POST("/refunds/:id/approve", s.approveRefund)
	p.GET("/reconciliations", s.reconciliations)
	p.POST("/reconciliations/:id/resolve", s.resolve)
	p.POST("/reconciliations/:id/approve", s.approveResolve)
	p.GET("/audit", s.audits)
	p.GET("/reports/summary", s.platformReport)
	p.GET("/reports/operations", s.operatingReport)
	p.GET("/reports/operations.csv", s.operatingReportCSV)
	p.GET("/costs", s.costs)
	p.GET("/probes", s.probesList)
	p.POST("/cost-corrections", s.correctCost)
	p.GET("/cost-versions", s.costVersions)
	p.POST("/cost-versions", s.publishCostPrice)
	p.GET("/procurement-accounts", s.procurementAccounts)
	p.POST("/procurement-accounts", s.createProcurementAccount)
	p.GET("/procurement-accounts/:id/configuration", s.procurementConfiguration)
	p.PUT("/procurement-accounts/:id/configuration", s.configureProcurementAccount)
	p.GET("/procurement-accounts/:id/events", s.procurementEvents)
	p.GET("/procurement-accounts/:id/reservations", s.procurementReservations)
	p.GET("/procurement-accounts/:id/balance-observations", s.procurementObservations)
	p.POST("/procurement-accounts/:id/balance-observations", s.observeProcurementBalance)
	p.GET("/procurement-bindings", s.procurementBindings)
	p.POST("/procurement-bindings", s.bindProcurementCredential)
	p.GET("/procurement-operations/:id", s.procurementOperation)
	p.GET("/procurement-cost-proposals", s.procurementCostProposals)
	p.POST("/procurement-cost-proposals", s.proposeProcurementCost)
	p.GET("/procurement-cost-proposals/:id", s.procurementCostProposal)
	p.POST("/procurement-cost-proposals/:id/approve", s.approveProcurementCost)
	p.GET("/requests/:id/attempts", s.attempts)
	v := r.Group("/v1")
	v.Use(s.apiKey())
	v.GET("/models", s.inferenceModels)
	v.POST("/tasks", s.createTask)
	v.GET("/tasks/:id", s.taskStatus)
	v.POST("/tasks/:id/submit", s.submitTask)
	v.POST("/tasks/:id/cancel", s.cancelTask)
	v.POST("/tasks/:id/result-grant", s.taskResultGrant)
	v.GET("/tasks/:id/result", s.taskResult)
	v.POST("/images/generations", s.imageGeneration)
	v.GET("/images/generations/:id", s.imageGenerationResult)
	v.POST("/audio/speech", s.synthesizeSpeech)
	v.GET("/realtime", s.realtime)
	v.POST("/chat/completions", s.chat)
	v.POST("/responses", func(c *gin.Context) { s.inference(c, provider.ProtocolResponses) })
	v.POST("/messages", func(c *gin.Context) { s.inference(c, provider.ProtocolMessages) })
	v.POST("/embeddings", func(c *gin.Context) { s.inference(c, provider.ProtocolEmbeddings) })
	v.POST("/rerank", func(c *gin.Context) { s.inference(c, provider.ProtocolRerank) })
	g := r.Group("/v1beta")
	g.Use(s.apiKey())
	g.POST("/models/*action", s.gemini)
	r.NoRoute(s.static)
	return r
}
func (s *Server) metadata() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.NewString()
		c.Set("request_id", id)
		c.Header("X-Request-ID", id)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(c.Request.URL.Path, "/api") || strings.HasPrefix(c.Request.URL.Path, "/v1") {
			c.Header("Cache-Control", "no-store")
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		start := time.Now()
		s.Requests.Add(1)
		defer func() {
			if recover() != nil {
				c.Abort()
				if !c.Writer.Written() {
					s.fail(c, 500, "internal_error", "请求处理失败")
				}
			}
			if c.Writer.Status() >= 400 {
				s.Failures.Add(1)
			}
			if s.Log != nil {
				s.Log.Write(telemetry.Record{RequestID: id, Stage: "http", Code: fmt.Sprint(c.Writer.Status()), DurationMS: time.Since(start).Milliseconds()})
			}
		}()
		duration := 15 * time.Second
		if inferencePath(c.Request.URL.Path) {
			duration = 300 * time.Second
		} else if publicCatalogPath(c.Request.URL.Path) {
			duration = 2 * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), duration)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		if c.Request.Method == http.MethodPost && selfServiceMutation(c.Request.URL.Path) && !s.selfServiceIP(c) {
			return
		}
		if publicCatalogPath(c.Request.URL.Path) {
			release, ok := s.admitPublicCatalog(c)
			if !ok {
				return
			}
			// Cover the ownership Guard, pool wait, query, row close and
			// response, including every error and cancellation path.
			defer release()
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || inferencePath(c.Request.URL.Path) {
			if !s.ready.Load() || s.guardContext(ctx) != nil {
				s.fail(c, 503, "instance_unavailable", "服务实例暂不可用")
				return
			}
		}
		c.Next()
	}
}
func inferencePath(path string) bool {
	return strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v1beta/")
}
func (s *Server) ok(c *gin.Context, v any) {
	c.JSON(200, gin.H{"data": v, "request_id": c.GetString("request_id")})
}
func (s *Server) fail(c *gin.Context, status int, code, message string) {
	if c.Request.URL.Path == "/v1/messages" {
		c.AbortWithStatusJSON(status, gin.H{"type": "error", "error": gin.H{"type": nativeErrorType(status), "message": message}, "request_id": c.GetString("request_id")})
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1beta/") {
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "status": status, "message": message}, "request_id": c.GetString("request_id")})
	} else if strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"message": message, "type": "gateway_error", "param": nil, "code": code}})
	} else {
		c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}, "request_id": c.GetString("request_id")})
	}
}
func (s *Server) error(c *gin.Context, e error) {
	if e == nil {
		return
	}
	if errors.Is(e, errMediaProviderUnavailable) {
		s.fail(c, http.StatusServiceUnavailable, errMediaProviderUnavailable.Error(), "媒体供应商不可用")
		return
	}
	if procurementExecutionError(c, s, e) {
		return
	}
	if errors.Is(e, identity.ErrReauth) {
		s.fail(c, 403, "reauth_required", "敏感操作需要再次验证身份")
		return
	}
	if errors.Is(e, identity.ErrInvalid) {
		s.fail(c, 401, "invalid_credentials", "身份凭据无效或已过期")
		return
	}
	if errors.Is(e, identity.ErrKeyPolicyInvalid) {
		s.fail(c, 400, "invalid_key_access_policy", "来源访问策略无效，请检查版本与 IP 或 CIDR 配置")
		return
	}
	if errors.Is(e, identity.ErrKeyLimitsInvalid) {
		s.fail(c, 400, "invalid_key_runtime_limits", "运行限额无效，请检查版本与 RPM、突发及并发配置")
		return
	}
	if errors.Is(e, identity.ErrKeyConfigurationInvalid) {
		s.fail(c, 400, "invalid_key_configuration", "基本配置无效，请检查名称、模型、地域、额度和有效期")
		return
	}
	if errors.Is(e, identity.ErrKeyBudgetBelowUsage) {
		s.fail(c, 409, "key_budget_below_usage", "累计额度不能低于当前已消费和冻结金额之和")
		return
	}
	if errors.Is(e, identity.ErrForbidden) {
		s.fail(c, 403, "forbidden", "没有访问权限")
		return
	}
	if errors.Is(e, pgx.ErrNoRows) {
		s.fail(c, 404, "not_found", "资源不存在")
		return
	}
	if errors.Is(e, catalog.ErrInvalid) {
		s.fail(c, 400, "invalid_configuration", "配置无效，请检查参数与引用")
		return
	}
	if errors.Is(e, catalog.ErrModelAliasInvalid) {
		s.fail(c, 400, "invalid_model_alias", "模型别名参数无效")
		return
	}
	if errors.Is(e, catalog.ErrModelAliasConflict) {
		s.fail(c, 409, "model_name_conflict", "模型公开名或别名已存在")
		return
	}
	if errors.Is(e, catalog.ErrModelAliasRetired) {
		s.fail(c, 409, "model_alias_retired", "模型别名已经退役")
		return
	}
	if errors.Is(e, catalog.ErrModelAliasTargetUnavailable) {
		s.fail(c, 409, "model_alias_target_unavailable", "目标模型未启用")
		return
	}
	if errors.Is(e, catalog.ErrModelAliasTableUnavailable) {
		s.fail(c, 503, "model_alias_unavailable", "模型别名能力尚未安装")
		return
	}
	if errors.Is(e, procurement.ErrInvalid) {
		s.fail(c, 400, "invalid_procurement_configuration", "采购参数无效，请检查金额、版本与引用")
		return
	}
	if errors.Is(e, procurement.ErrForbidden) {
		s.fail(c, 403, "forbidden", "没有采购管理权限")
		return
	}
	if errors.Is(e, procurement.ErrConflict) {
		s.fail(c, 409, "procurement_operation_conflict", "采购操作身份、状态或版本冲突")
		return
	}
	if errors.Is(e, catalog.ErrConflict) || errors.Is(e, identity.ErrConflict) {
		s.fail(c, 409, "conflict", "资源状态或版本冲突")
		return
	}
	if billingError(c, s, e) {
		return
	}
	var pe *pgconn.PgError
	if errors.As(e, &pe) && (pe.Code == "23505" || pe.Code == "23503" || pe.Code == "23514") {
		s.fail(c, 409, "conflict", "资源重复或关联约束不满足")
		return
	}
	s.fail(c, 503, "system_unavailable", "服务暂不可用，请凭 request_id 查询结果")
}
func (s *Server) bind(c *gin.Context, v any) bool {
	if e := c.ShouldBindJSON(v); e != nil {
		s.fail(c, 400, "invalid_request", "JSON参数无效或请求过大")
		return false
	}
	return true
}
func sessionOf(c *gin.Context) *identity.Session {
	v, _ := c.Get("session")
	return v.(*identity.Session)
}
func (s *Server) rateDecision(key string, r rate.Limit, burst int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	l := s.limits[key]
	if l == nil {
		if len(s.limits) >= 4096 {
			for k, last := range s.limitSeen {
				if now.Sub(last) > 10*time.Minute {
					delete(s.limits, k)
					delete(s.limitSeen, k)
				}
			}
		}
		if len(s.limits) >= 4096 {
			return 503
		}
		l = rate.NewLimiter(r, burst)
		s.limits[key] = l
	}
	s.limitSeen[key] = now
	if !l.Allow() {
		return 429
	}
	return 0
}
func (s *Server) checkRate(c *gin.Context, key string, r rate.Limit, burst int) bool {
	code := s.rateDecision(key, r, burst)
	if code == 0 {
		return true
	}
	if code == 503 {
		s.fail(c, 503, "limiter_unavailable", "限流服务暂不可用")
	} else {
		s.fail(c, 429, "rate_limited", "请求过于频繁，请稍后重试")
	}
	return false
}

func (s *Server) validOrigin(c *gin.Context) bool {
	origin := c.GetHeader("Origin")
	if origin == "" {
		referer := c.GetHeader("Referer")
		if referer != "" {
			u, e := url.Parse(referer)
			if e == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	return origin == s.Config.Origin
}
func (s *Server) session() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, _ := c.Cookie("moni_session")
		v, e := s.Identity.Session(c.Request.Context(), raw)
		if e != nil {
			s.error(c, e)
			return
		}
		c.Set("session", v)
		if c.Request.Method != "GET" && c.Request.Method != "HEAD" {
			if !s.validOrigin(c) || c.GetHeader("X-CSRF-Token") != v.CSRF {
				s.fail(c, 403, "csrf_invalid", "会话校验失败，请刷新页面")
				return
			}
		}
		c.Next()
	}
}
func (s *Server) requireMFA() gin.HandlerFunc {
	return func(c *gin.Context) {
		v := sessionOf(c)
		if v.MFARequired || v.EnrollmentRequired {
			s.fail(c, 403, "mfa_required", "请先完成双因素认证")
			return
		}
		c.Next()
	}
}
func (s *Server) platform() gin.HandlerFunc {
	return func(c *gin.Context) {
		v := sessionOf(c)
		if v.User.PlatformRole == "none" {
			s.fail(c, 403, "forbidden", "需要平台权限")
			return
		}
		if c.Request.Method != "GET" {
			if v.ReauthenticatedUntil == nil || !v.ReauthenticatedUntil.After(time.Now()) {
				s.fail(c, 403, "reauth_required", "敏感操作需要再次验证身份")
				return
			}
			if s.Config.RecoveryMode {
				s.fail(c, 503, "recovery_mode", "恢复模式暂不接受业务变更")
				return
			}
		}
		c.Next()
	}
}
func (s *Server) admin(c *gin.Context) bool {
	if sessionOf(c).User.PlatformRole != "admin" {
		s.fail(c, 403, "forbidden", "需要平台管理员权限")
		return false
	}
	return true
}
func (s *Server) financial(c *gin.Context, approve bool) bool {
	role := sessionOf(c).User.PlatformRole
	if (approve && role != "finance_approver") || (!approve && role != "finance_operator") {
		s.fail(c, 403, "forbidden", "需要相应财务操作权限")
		return false
	}
	return true
}
func (s *Server) tenant(c *gin.Context) (string, bool) {
	v := sessionOf(c)
	if v.Tenant == nil {
		s.fail(c, 403, "tenant_required", "请先选择可访问的租户")
		return "", false
	}
	return v.Tenant.ID, true
}
func (s *Server) apiKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.ready.Load() || s.Config.RecoveryMode {
			s.fail(c, 503, "unavailable", "当前不接受收费调用")
			return
		}
		auth := c.GetHeader("Authorization")
		key := c.GetHeader("X-Api-Key")
		if strings.HasPrefix(c.Request.URL.Path, "/v1beta/") && (len(c.Request.Header.Values("X-Goog-Api-Key")) > 0 || len(c.Request.Header.Values("X-Api-Key")) > 0 || c.Request.URL.Query().Has("key")) {
			s.fail(c, 401, "invalid_credentials", "Gemini调用仅接受唯一Bearer API Key")
			return
		}
		authCount, keyCount := len(c.Request.Header.Values("Authorization")), len(c.Request.Header.Values("X-Api-Key"))
		if authCount > 1 || keyCount > 1 || (authCount > 0 && keyCount > 0) {
			s.fail(c, 401, "invalid_credentials", "请提供唯一 API Key 凭据")
			return
		}
		var token string
		if auth != "" && strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		} else if c.Request.URL.Path == "/v1/messages" {
			token = key
		}
		if token == "" || strings.TrimSpace(token) != token {
			s.fail(c, 401, "invalid_credentials", "需要有效 API Key")
			return
		}
		p, e := s.Identity.APIKey(c.Request.Context(), token)
		if e != nil {
			s.error(c, e)
			return
		}
		p.ClientIP = security.NormalizeClientIP(trustedClientIP(c))
		if !security.ClientIPAllowed(p.ClientIP, p.AllowedClientCIDRs) {
			s.fail(c, 403, "key_ip_forbidden", "当前请求来源未获授权")
			return
		}
		c.Set("principal", p)
		c.Next()
	}
}
func (s *Server) metrics(c *gin.Context) {
	// Forwarded client identity must never grant access to local-only metrics.
	host := c.RemoteIP()
	if host != "127.0.0.1" && host != "::1" {
		s.fail(c, 403, "forbidden", "指标仅供本机运维读取")
		return
	}
	dropped := uint64(0)
	if s.Log != nil {
		dropped = s.Log.Dropped.Load()
	}
	c.Data(200, "text/plain; version=0.0.4", []byte(fmt.Sprintf("moni_requests_total %d\nmoni_errors_total %d\nmoni_active_streams %d\nmoni_logs_dropped_total %d\n", s.Requests.Load(), s.Failures.Load(), s.Streams.Load(), dropped)))
}

func (s *Server) sensitive(c *gin.Context) bool {
	v := sessionOf(c)
	if v.ReauthenticatedUntil == nil || !v.ReauthenticatedUntil.After(time.Now()) {
		s.fail(c, 403, "reauth_required", "敏感操作需要再次验证身份")
		return false
	}
	if s.Config.RecoveryMode {
		s.fail(c, 503, "recovery_mode", "恢复模式暂停变更")
		return false
	}
	return true
}

// guardContext uses a fresh bounded context: a cancelled customer must not revoke
// the process ownership. The guard itself permanently fences on a failed ping.
func (s *Server) guardContext(_ context.Context) error {
	if s.Guard == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Guard(ctx); err != nil {
		s.Ready(false)
		return err
	}
	return nil
}
