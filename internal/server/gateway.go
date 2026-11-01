package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"golang.org/x/time/rate"
)

func (s *Server) inferenceModels(c *gin.Context) {
	p := c.MustGet("principal").(identity.Principal)
	v, e := s.query(c.Request.Context(), `SELECT m.public_name AS id,'model' AS object,'moni-aigate' AS owned_by,0 AS created FROM models m JOIN tenant_model_grants g ON g.model_id=m.id AND g.tenant_id=$1 JOIN memberships mb ON mb.tenant_id=$1 AND mb.user_id=$2 JOIN api_keys k ON k.id=$3 WHERE m.enabled AND (m.id=ANY(k.scopes) OR '*'=ANY(k.scopes)) AND (mb.allow_all_models OR EXISTS(SELECT 1 FROM member_model_grants mg WHERE mg.tenant_id=$1 AND mg.user_id=$2 AND mg.model_id=m.id)) ORDER BY m.public_name`, p.TenantID, p.UserID, p.KeyID)
	if e != nil {
		s.error(c, e)
		return
	}
	c.JSON(200, gin.H{"object": "list", "data": v})
}
func (s *Server) releasePermit(p identity.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	<-s.global
	s.tenantPermits[p.TenantID]--
	s.keyPermits[p.KeyID]--
	if s.tenantPermits[p.TenantID] == 0 {
		delete(s.tenantPermits, p.TenantID)
	}
	if s.keyPermits[p.KeyID] == 0 {
		delete(s.keyPermits, p.KeyID)
	}
}
func (s *Server) chat(c *gin.Context) {
	s.inference(c, provider.ProtocolChat)
}

func (s *Server) inference(c *gin.Context, protocol string) {
	s.inferenceNamed(c, protocol, "", false)
}

func (s *Server) gemini(c *gin.Context) {
	parts := strings.Split(strings.TrimPrefix(c.Param("action"), "/"), ":")
	if len(parts) != 2 || !provider.ValidGeminiModel(parts[0]) || c.Request.URL.EscapedPath() != c.Request.URL.Path {
		s.fail(c, 400, "invalid_request", "Gemini模型路径无效")
		return
	}
	stream := parts[1] == "streamGenerateContent"
	if parts[1] != "generateContent" && !stream || stream && c.Request.URL.RawQuery != "alt=sse" || !stream && c.Request.URL.RawQuery != "" {
		s.fail(c, 400, "unsupported_parameter", "请选择generateContent或streamGenerateContent?alt=sse")
		return
	}
	s.inferenceNamed(c, provider.ProtocolGemini, parts[0], stream)
}

func (s *Server) inferenceNamed(c *gin.Context, protocol, pathModel string, stream bool) {
	p := c.MustGet("principal").(identity.Principal)
	if !s.checkRate(c, "platform:inference", rate.Every(10*time.Millisecond), 200) || !s.checkRate(c, "tenant:"+p.TenantID, rate.Every(200*time.Millisecond), 40) {
		return
	}

	raw, e := io.ReadAll(c.Request.Body)
	if e != nil {
		s.fail(c, 413, "request_too_large", "请求体超过上限")
		return
	}
	var head struct {
		Model string `json:"model"`
	}
	head.Model = pathModel
	if protocol != provider.ProtocolGemini && (json.Unmarshal(raw, &head) != nil || head.Model == "") {
		s.fail(c, 400, "invalid_request", "缺少有效model")
		return
	}
	model, e := s.Catalog.AuthorizedModel(c.Request.Context(), p.TenantID, p.UserID, p.KeyID, head.Model)
	if errors.Is(e, pgx.ErrNoRows) {
		s.fail(c, 403, "model_forbidden", "模型不存在或未获授权")
		return
	}
	if e != nil {
		s.error(c, e)
		return
	}
	var request *provider.Request
	if protocol == provider.ProtocolGemini {
		request, e = provider.ParseGemini(raw, model.Profile, stream)
	} else {
		request, e = provider.Parse(protocol, raw, model.Profile)
	}
	if e != nil {
		s.providerError(c, e)
		return
	}
	available, e := s.Catalog.CandidateSnapshot(c.Request.Context(), model.ID, model.Revision)
	if e != nil {
		s.error(c, e)
		return
	}
	candidates := make([]routing.Candidate, 0, len(available))
	eligible := 0
	for _, v := range available {
		suffix, err := request.EndpointSuffixForModel(v.UpstreamModel)
		if err != nil {
			s.fail(c, 503, "invalid_route_policy", "渠道模型映射无效")
			return
		}
		reason := v.ExclusionReason
		if reason == "" && !contains(p.AllowedRegions, v.Region) {
			reason = "region_forbidden"
		}
		if reason == "" {
			eligible++
		}
		candidates = append(candidates, routing.Candidate{ChannelID: v.ChannelID, VersionID: v.VersionID, CredentialID: v.CredentialID, CredentialWeight: uint64(v.CredentialWeight), CredentialMaxInflight: v.CredentialMaxInflight, Model: v.UpstreamModel, Region: v.Region, HealthKey: v.CredentialID + ":" + model.ID, Priority: v.Priority, Weight: uint64(v.Weight), MaxInflight: v.MaxInflight, CostPolicy: v.CostPolicy, CostQuote: v.CostQuote, MaxInputCostRate: v.MaxInputCostRate, MaxOutputCostRate: v.MaxOutputCostRate, InputUpperBound: request.InputUpperBound, OutputLimit: request.MaxOutputTokens, ProcurementManaged: v.ProcurementManaged, ProcurementState: v.ProcurementState, ProcurementAvailable: v.ProcurementAvailable, Enabled: reason == "", ExclusionReason: reason, Endpoint: provider.Endpoint{URL: v.URL + suffix, APIKey: v.Secret, Model: v.UpstreamModel, Network: networkPolicy(v.URL, v.AllowedCIDRs)}})
	}
	if eligible == 0 {
		s.fail(c, 503, "no_available_channel", "没有符合条件的可用渠道")
		return
	}
	plan, e := routing.NewPlan(candidates, 2)
	if e != nil {
		s.fail(c, 503, "invalid_route_policy", "渠道配置不可用")
		return
	}
	releaseRuntime, e := s.acquireKeyRuntime(c.Request.Context(), p)
	if e != nil {
		if !s.runtimeLimitFailure(c, e) {
			s.error(c, e)
		}
		return
	}
	defer releaseRuntime()
	releaseAdmission, e := s.admission.acquire(c.Request.Context(), p.KeyID)
	if e != nil {
		s.admissionError(c, e)
		return
	}
	// A retry owns a new ticket. Capture the variable, not the first release,
	// so a panic/early return cannot strand a later admission in the queue.
	defer func() {
		if releaseAdmission != nil {
			releaseAdmission()
		}
	}()
	deadline, _ := c.Request.Context().Deadline()
	identityBody := string(raw)
	if protocol == provider.ProtocolGemini {
		identityBody = protocol + "\x00" + pathModel + "\x00" + fmt.Sprint(stream) + "\x00" + identityBody
	}
	reservation, e := s.Billing.Reserve(c.Request.Context(), billing.ReserveInput{Principal: billing.Principal{TenantID: p.TenantID, UserID: p.UserID, KeyID: p.KeyID, ClientIP: p.ClientIP}, RequestID: c.GetString("request_id"), ModelID: model.ID, IdempotencyKey: c.GetHeader("Idempotency-Key"), RequestHMAC: s.Config.Secrets.Digest(identityBody), ProfileVersion: model.ProfileVersion, RouteRevision: model.Revision, PolicyVersion: "retail-r3", InputUpperBound: request.InputUpperBound, OutputLimit: request.MaxOutputTokens, RequestDeadline: deadline, Streaming: request.Stream})
	if e != nil {
		releaseAdmission()
		releaseAdmission = nil
		s.error(c, e)
		return
	}
	defer s.Billing.EndRequest(reservation.RequestID)
	var attemptID string
	for {
		if releaseAdmission == nil {
			// Safe retries join the tail without holding a channel lease or a
			// database connection. The original request deadline still applies.
			releaseAdmission, e = s.admission.acquire(c.Request.Context(), p.KeyID)
		}
		var attempt *routing.Attempt
		var admittedAttemptID string
		if e == nil {
			attempt, e = plan.Start(c.Request.Context(), s.Routing, func(ctx context.Context, candidate routing.Candidate, sequence int) error {
				a, err := s.Billing.RecordAttempt(ctx, billing.AttemptInput{TenantID: p.TenantID, RequestID: reservation.RequestID, ChannelID: candidate.ChannelID, ChannelVersionID: candidate.VersionID, CredentialID: candidate.CredentialID, UpstreamModel: candidate.Model, Sequence: sequence, RouteDecisions: candidate.Decisions, CostQuote: &candidate.CostQuote})
				if err != nil {
					return err
				}
				attemptID = a.ID
				admittedAttemptID = a.ID
				if err = s.Billing.PrepareDispatch(ctx, billing.DispatchInput{TenantID: p.TenantID, AttemptID: a.ID}); err != nil {
					return err
				}
				return s.guardContext(ctx)
			})
		}
		// Admission scheduling never owns the upstream stream lifecycle.
		// Release before any provider I/O, failure cleanup or Retry-After wait.
		if releaseAdmission != nil {
			releaseAdmission()
			releaseAdmission = nil
		}
		if e != nil {
			ctx, cancel := cleanupContext()
			// This admission never reached Provider.Do. Preserve explicit
			// non-consumption evidence for a prepared (or sending-authorized)
			// attempt instead of leaving its supplier cost unknown forever.
			if admittedAttemptID != "" {
				_, _ = s.Billing.RecordFinalization(ctx, billing.FinalizationInput{TenantID: p.TenantID, RequestID: reservation.RequestID, AttemptID: admittedAttemptID, Source: "gateway", SourceEventID: admittedAttemptID + ":admission-not-sent", Usage: billing.Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"})
			}
			reason := "authorization_revoked"
			if errors.Is(e, routing.ErrUnavailable) || errors.Is(e, routing.ErrAttemptBudget) || errors.Is(e, errAdmissionQueueFull) {
				reason = "no_capacity"
			} else if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				reason = "no_delivery"
			} else if !s.ready.Load() {
				reason = "platform_failure"
			}
			_, _ = s.Billing.Release(ctx, billing.ReleaseInput{TenantID: p.TenantID, RequestID: reservation.RequestID, Reason: reason})
			cancel()
			if errors.Is(e, routing.ErrUnavailable) || errors.Is(e, routing.ErrAttemptBudget) {
				s.fail(c, 503, "no_available_channel", "当前没有可用渠道")
			} else {
				s.admissionError(c, e)
			}
			return
		}
		response, err := s.Provider.Do(c.Request.Context(), attempt.Candidate.Endpoint, request)
		if err != nil {
			s.applyProviderHealth(attempt.Candidate, err)
			reason := transportReason(c.Request.Context(), err)
			f := billing.FinalizationInput{ID: uuid.NewString(), TenantID: p.TenantID, RequestID: reservation.RequestID, AttemptID: attemptID, Source: "adapter", SourceEventID: attemptID + ":final", Usage: billing.Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: reason, DeliveryStage: "none"}
			_, persistErr := s.finalize(p.KeyID, f, false)
			attempt.Finish(outcome(c.Request.Context(), err))
			if persistErr == nil && plan.CanRetry(c.Request.Context(), err) {
				var pe *provider.Error
				if errors.As(err, &pe) && pe.RetryAfter > 0 {
					wait := pe.RetryAfter
					if wait > 2*time.Second {
						wait = 2 * time.Second
					}
					timer := time.NewTimer(wait)
					select {
					case <-timer.C:
					case <-c.Request.Context().Done():
						timer.Stop()
					}
				}
				if c.Request.Context().Err() == nil {
					continue
				}
			}
			_, _ = s.settle(p.KeyID, p.TenantID, reservation.RequestID)
			s.providerError(c, err)
			return
		}
		healthOverride := routing.Outcome("")
		if request.Stream {
			s.Streams.Add(1)
			healthOverride = s.stream(c, p, reservation.RequestID, attemptID, request, response, plan)
			s.Streams.Add(-1)
		} else {
			s.nonStream(c, p, reservation.RequestID, attemptID, response)
		}
		_ = response.Close()
		if healthOverride != "" {
			attempt.Finish(healthOverride)
		} else if response.ProtocolOutcome == "completed" && response.Usage.Status == provider.UsageConfirmed {
			attempt.Finish(routing.Success)
		} else if c.Request.Context().Err() != nil {
			attempt.Finish(routing.Neutral)
		} else {
			attempt.Finish(routing.Failure)
		}
		return
	}
}

func (s *Server) admissionError(c *gin.Context, err error) {
	if errors.Is(err, errAdmissionQueueFull) {
		s.fail(c, 429, "concurrency_limited", "当前并发已满，请稍后重试")
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.fail(c, 504, "request_timeout", "请求已取消或超时，请凭 request_id 查询结果")
	} else {
		s.error(c, err)
	}
}

func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s || v == "*" {
			return true
		}
	}
	return false
}
func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
func (s *Server) finalize(key string, f billing.FinalizationInput, settle bool) (billing.Settlement, error) {
	ctx, cancel := cleanupContext()
	defer cancel()
	release, err := s.admission.acquire(ctx, key)
	if err != nil {
		return billing.Settlement{}, err
	}
	defer release()
	if _, e := s.Billing.RecordFinalization(ctx, f); e != nil {
		return billing.Settlement{}, e
	}
	if !settle {
		return billing.Settlement{}, nil
	}
	return s.Billing.Settle(ctx, billing.SettleInput{TenantID: f.TenantID, RequestID: f.RequestID, Mode: "live"})
}
func (s *Server) settle(key, tenant, request string) (billing.Settlement, error) {
	ctx, cancel := cleanupContext()
	defer cancel()
	release, err := s.admission.acquire(ctx, key)
	if err != nil {
		return billing.Settlement{}, err
	}
	defer release()
	return s.Billing.Settle(ctx, billing.SettleInput{TenantID: tenant, RequestID: request, Mode: "live"})
}
func billingUsage(u provider.Usage) billing.Usage {
	return billing.Usage{Status: string(u.Status), InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TotalTokens: u.TotalTokens, CachedTokens: u.CachedTokens, ReasoningTokens: u.ReasoningTokens}
}
func transportReason(ctx context.Context, e error) string {
	if errors.Is(ctx.Err(), context.Canceled) {
		return "client_cancelled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "platform_timeout"
	}
	var pe *provider.Error
	if errors.As(e, &pe) {
		if pe.Consumption == provider.ConsumptionNotSent {
			return "not_sent"
		}
		if pe.Consumption == provider.ConsumptionRejected {
			return "rejected_no_consumption"
		}
		if pe.Class == provider.ClassTimeout {
			return "platform_timeout"
		}
		if pe.Class == provider.ClassCancelled {
			return "client_cancelled"
		}
	}
	return "provider_error"
}
func outcome(ctx context.Context, e error) routing.Outcome {
	if ctx.Err() != nil {
		return routing.Neutral
	}
	var pe *provider.Error
	if errors.As(e, &pe) {
		switch pe.Class {
		case provider.ClassCaller, provider.ClassCancelled, provider.ClassConfiguration, provider.ClassRateLimit, provider.ClassModel, provider.ClassQuota, provider.ClassPolicy, provider.ClassRejected:
			return routing.Neutral
		}
	}
	return routing.Failure
}
func (s *Server) providerError(c *gin.Context, e error) {
	var pe *provider.Error
	if errors.As(e, &pe) {
		status := pe.HTTPStatus
		if status < 400 || status > 599 {
			status = 502
		}
		s.fail(c, status, pe.Code, "推理请求未能完成，请凭 request_id 查询账单状态")
		return
	}
	s.fail(c, 502, "upstream_error", "上游请求未能完成")
}
func (s *Server) nonStream(c *gin.Context, p identity.Principal, requestID, attemptID string, r *provider.Response) {
	body, e := r.DecodeJSON()
	reason := "done"
	if e != nil {
		reason = transportReason(c.Request.Context(), e)
	} else if c.Request.Context().Err() != nil {
		reason = transportReason(c.Request.Context(), c.Request.Context().Err())
	}
	f := billing.FinalizationInput{ID: uuid.NewString(), TenantID: p.TenantID, RequestID: requestID, AttemptID: attemptID, Source: "adapter", SourceEventID: attemptID + ":final", Usage: billingUsage(r.Usage), ProtocolOutcome: r.ProtocolOutcome, TransportEndReason: reason, DeliveryStage: "none", FinishReason: r.FinishReason}
	result, err := s.finalize(p.KeyID, f, true)
	if err != nil {
		s.fail(c, 503, "billing_pending", "用量或收费待核对，请勿自动重新生成")
		return
	}
	if e != nil {
		s.providerError(c, e)
		return
	}
	if c.Request.Context().Err() != nil {
		s.fail(c, 504, "request_timeout", "请求超时，请查询账单状态")
		return
	}
	if result.Status != "settled" {
		s.fail(c, 503, "billing_pending", "最终用量未确认，请查询账单状态")
		return
	}
	c.Header("Content-Type", "application/json")
	n, writeErr := c.Writer.Write(body)
	stage := "complete_local_write"
	if writeErr != nil || n != len(body) {
		stage = "write_failed"
	}
	s.observeDelivery(p.TenantID, requestID, stage)
}
func (s *Server) stream(c *gin.Context, p identity.Principal, requestID, attemptID string, req *provider.Request, r *provider.Response, plan *routing.Plan) routing.Outcome {
	content := false
	downstreamFailure := false
	stage := "none"
	var streamErr error
	var terminal provider.Event
	for {
		event, e := r.ReadEvent()
		if e != nil {
			streamErr = e
			break
		}
		if event.Kind == "heartbeat" || event.Kind == "usage" {
			continue
		}
		if event.Kind == "done" {
			terminal = *event
			break
		}
		if event.Kind == "chunk" {
			c.Header("Content-Type", "text/event-stream")
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Accel-Buffering", "no")
			n, e := writeNamedEvent(c, event.Type, event.Data)
			if c.Writer.Written() {
				plan.CommitDownstream()
				stage = "committed"
			}
			if event.Content && n > 0 && e == nil {
				content = true
				stage = "content"
			}
			if e != nil {
				downstreamFailure = true
				if !content {
					stage = "unknown"
				}
				streamErr = e
				_ = r.Close()
				break
			}
		}
	}
	reason := "done"
	if streamErr != nil {
		reason = transportReason(c.Request.Context(), streamErr)
		if downstreamFailure {
			reason = "platform_error"
		}
	}
	if c.Request.Context().Err() != nil {
		reason = transportReason(c.Request.Context(), c.Request.Context().Err())
	}
	f := billing.FinalizationInput{ID: uuid.NewString(), TenantID: p.TenantID, RequestID: requestID, AttemptID: attemptID, Source: "adapter", SourceEventID: attemptID + ":final", Usage: billingUsage(r.Usage), ProtocolOutcome: r.ProtocolOutcome, TransportEndReason: reason, DeliveryStage: stage, ContentWritten: content, FinishReason: r.FinishReason}
	result, e := s.finalize(p.KeyID, f, true)
	if streamErr != nil {
		if c.Writer.Written() {
			s.streamError(c, "stream_failed")
		} else {
			s.providerError(c, streamErr)
		}
		if downstreamFailure {
			return routing.Neutral
		}
		return ""
	}
	if e != nil || result.Status != "settled" {
		if c.Writer.Written() {
			s.streamError(c, "billing_pending")
		} else {
			s.fail(c, 503, "billing_pending", "最终用量或收费待核对")
		}
		if downstreamFailure {
			return routing.Neutral
		}
		return ""
	}
	if c.Request.Context().Err() != nil {
		if downstreamFailure {
			return routing.Neutral
		}
		return ""
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	if req.Protocol() == provider.ProtocolChat && req.IncludeUsage {
		usage, _ := provider.UsageJSON(r.Usage)
		data, _ := json.Marshal(gin.H{"id": requestID, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model, "choices": []any{}, "usage": json.RawMessage(usage)})
		if _, e = writeEvent(c, data); e != nil {
			return routing.Neutral
		}
	}
	if req.Protocol() == provider.ProtocolChat {
		_, e = writeEvent(c, []byte("[DONE]"))
	} else {
		_, e = writeNamedEvent(c, terminal.Type, terminal.Data)
	}
	stage = "complete_local_write"
	if e != nil {
		stage = "write_failed"
	}
	s.observeDelivery(p.TenantID, requestID, stage)
	return ""
}
func writeEvent(c *gin.Context, data []byte) (int, error) {
	return writeNamedEvent(c, "", data)
}

func writeNamedEvent(c *gin.Context, name string, data []byte) (int, error) {
	if len(name) > 100 || strings.ContainsAny(name, "\r\n") || strings.ContainsAny(string(data), "\r\n") {
		return 0, errors.New("invalid_stream_frame")
	}
	rc := http.NewResponseController(c.Writer)
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := c.Request.Context().Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e := rc.SetWriteDeadline(deadline); e != nil && !errors.Is(e, http.ErrNotSupported) {
		return 0, e
	}
	frame := make([]byte, 0, len(name)+len(data)+20)
	if name != "" {
		frame = append(frame, "event: "...)
		frame = append(frame, name...)
		frame = append(frame, '\n')
	}
	frame = append(frame, "data: "...)
	frame = append(frame, data...)
	frame = append(frame, '\n', '\n')
	n, e := c.Writer.Write(frame)
	if e == nil {
		// Gin's void Flush method hides a transport's FlushError. The write
		// above has already committed Gin's headers/state, so unwrap wrappers
		// until an error-aware transport can report the actual local flush.
		var writer http.ResponseWriter = c.Writer
		for {
			if _, ok := writer.(interface{ FlushError() error }); ok {
				break
			}
			unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
			if !ok {
				break
			}
			writer = unwrapper.Unwrap()
		}
		e = http.NewResponseController(writer).Flush()
	}
	return n, e
}
func (s *Server) streamError(c *gin.Context, code string) {
	if c.Request.Context().Err() != nil {
		return
	}
	name := ""
	body := gin.H{"error": gin.H{"type": "gateway_error", "code": code, "message": "流已中断，请凭 request_id 查询结果"}}
	if strings.HasPrefix(c.Request.URL.Path, "/v1beta/") {
		name = "error"
		body = gin.H{"error": gin.H{"code": code, "status": 502, "message": "流已中断，请凭 request_id 查询结果"}, "request_id": c.GetString("request_id")}
	}
	switch c.Request.URL.Path {
	case "/v1/messages":
		name = "error"
		body = gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": "流已中断，请凭 request_id 查询结果"}, "request_id": c.GetString("request_id")}
	case "/v1/responses":
		name = "error"
		body = gin.H{"type": "error", "code": code, "message": "流已中断，请凭 request_id 查询结果", "param": nil, "request_id": c.GetString("request_id")}
	}
	b, _ := json.Marshal(body)
	_, _ = writeNamedEvent(c, name, b)
}

func nativeErrorType(status int) string {
	switch status {
	case 400:
		return "invalid_request_error"
	case 401:
		return "authentication_error"
	case 403:
		return "permission_error"
	case 404:
		return "not_found_error"
	case 413:
		return "request_too_large"
	case 429:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

// Persist only normalized credential failures. A bare 401/403 is not enough.
func (s *Server) applyProviderHealth(candidate routing.Candidate, err error) {
	var e *provider.Error
	if !errors.As(err, &e) {
		return
	}
	if e.Class == provider.ClassModel {
		s.isolateBinding(candidate)
		return
	}
	if e.Class == provider.ClassRateLimit {
		duration := e.RetryAfter
		if duration <= 0 {
			duration = 30 * time.Second
		}
		if duration > 5*time.Minute {
			duration = 5 * time.Minute
		}
		s.Routing.Cooldown(candidate.HealthKey, duration)
		return
	}
	if e.Class != provider.ClassCredential {
		return
	}
	ctx, cancel := cleanupContext()
	defer cancel()
	tx, x := s.Pool.Begin(ctx)
	if x != nil {
		return
	}
	defer tx.Rollback(ctx)
	if _, x = tx.Exec(ctx, "UPDATE channel_credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=$1", candidate.CredentialID); x != nil {
		return
	}
	if _, x = tx.Exec(ctx, "INSERT INTO audit_events(actor,action,target) VALUES('system','credential.quarantined',$1)", candidate.CredentialID); x != nil {
		return
	}
	if x = tx.Commit(ctx); x == nil {
		s.Routing.SetQuarantined(candidate.HealthKey, true)
	}
}

// Delivery observations describe local writes only; they never change a settlement.
func (s *Server) observeDelivery(tenant, request, stage string) {
	ctx, cancel := cleanupContext()
	defer cancel()
	_, _ = s.Pool.Exec(ctx, "INSERT INTO delivery_observations(tenant_id,request_id,stage) VALUES($1,$2,$3)", tenant, request, stage)
}
