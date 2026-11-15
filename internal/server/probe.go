package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/provider"
	"github.com/moni-ai/moni-aigate/internal/routecost"
	"github.com/moni-ai/moni-aigate/internal/routing"
	"golang.org/x/time/rate"
)

// Platform probes have their own small budget and also consume the global
// generation budget. They never acquire a customer's tenant/Key money permit.
func (s *Server) acquireProbe() bool {
	select {
	case s.probes <- struct{}{}:
	default:
		return false
	}
	select {
	case s.global <- struct{}{}:
		return true
	default:
		<-s.probes
		return false
	}
}

func (s *Server) releaseProbe() {
	<-s.global
	<-s.probes
}

func (s *Server) saveProbe(id, status string, usage provider.Usage) error {
	ctx, cancel := cleanupContext()
	defer cancel()
	_, err := s.Pool.Exec(ctx, "SELECT meter_finish_probe($1,$2,$3,$4,$5)", id, status, usage.Status, usage.InputTokens, usage.OutputTokens)
	return err
}

func (s *Server) probe(c *gin.Context) {
	if !s.admin(c) || !s.checkRate(c, "probe:"+c.Param("id"), rate.Every(time.Minute), 1) {
		return
	}
	if !s.acquireProbe() {
		s.fail(c, 429, "probe_capacity_limited", "当前平台探测或全局并发已满")
		return
	}
	defer s.releaseProbe()
	ctx := c.Request.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		s.error(c, err)
		return
	}
	defer tx.Rollback(ctx)
	var modelID, upstream, profileVersion, version string
	var profileJSON []byte
	var maxInflight, priority int
	var weight uint64
	// A probe uses one complete published binding. Updating current_version
	// without publishing must not combine a new endpoint with an old binding.
	err = tx.QueryRow(ctx, `SELECT m.id,b.upstream_model,m.profile_version,meter_model_profile(m.id),b.channel_version_id,b.max_inflight,b.priority,b.weight FROM model_bindings b JOIN route_policy_versions p ON p.id=b.route_policy_version_id JOIN models m ON m.id=p.model_id AND m.active_revision=p.revision WHERE b.channel_id=$1 AND m.enabled AND NOT EXISTS(SELECT 1 FROM route_binding_isolations i WHERE i.channel_version_id=b.channel_version_id AND i.upstream_model=b.upstream_model) ORDER BY m.id LIMIT 1 FOR SHARE OF m`, c.Param("id")).Scan(&modelID, &upstream, &profileVersion, &profileJSON, &version, &maxInflight, &priority, &weight)
	if errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, 400, "probe_model_required", "请先将渠道发布到一个已启用模型，再发起平台探测")
		return
	}
	if err != nil {
		s.error(c, err)
		return
	}
	var channel, baseURL, region, channelKind, integrationMode string
	var cidrs []string
	err = tx.QueryRow(ctx, `SELECT id FROM channels WHERE id=$1 AND enabled AND NOT quarantined FOR SHARE`, c.Param("id")).Scan(&channel)
	if err != nil {
		s.error(c, err)
		return
	}
	err = tx.QueryRow(ctx, `SELECT base_url,allowed_cidrs,region,channel_kind,integration_mode FROM channel_versions WHERE id=$1 AND channel_id=$2`, version, channel).Scan(&baseURL, &cidrs, &region, &channelKind, &integrationMode)
	if err != nil {
		s.error(c, err)
		return
	}
	var isolated bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM route_binding_isolations WHERE channel_version_id=$1 AND upstream_model=$2)", version, upstream).Scan(&isolated); err != nil {
		s.error(c, err)
		return
	}
	if isolated {
		s.fail(c, 503, "probe_channel_unavailable", "当前渠道模型版本已隔离")
		return
	}
	// Probes keep the unknown-procurement posture, but the routing plan still
	// needs the same complete current quote identity as a customer candidate.
	stored := routecost.StoredInput{ChannelID: channel, UpstreamModel: upstream}
	err = tx.QueryRow(ctx, `WITH quote_clock AS MATERIALIZED (SELECT clock_timestamp() AS at)
 SELECT s.canonical,s.schema_digest,'default',coalesce(q.id,''),coalesce(q.basis,'unknown'),q.rates_canonical,q.rates_digest
 FROM model_capability_versions c JOIN meter_schemas s ON s.id=c.schema_id AND s.schema_digest=c.schema_digest CROSS JOIN quote_clock clock
 LEFT JOIN LATERAL (SELECT cv.id,cv.basis,cv.rates_canonical,cv.rates_digest FROM cost_versions cv WHERE cv.channel_id=$2 AND cv.upstream_model=$3
  AND cv.schema_id=s.id AND cv.schema_digest=s.schema_digest AND cv.variant_key='default' AND cv.effective_at<=clock.at
  ORDER BY cv.effective_at DESC,cv.created_at DESC,cv.id DESC LIMIT 1) q ON true
 WHERE c.id=$1 AND c.model_id=$4`, profileVersion, channel, upstream, modelID).Scan(&stored.SchemaCanonical, &stored.SchemaDigest, &stored.VariantKey, &stored.VersionID, &stored.Basis, &stored.RatesCanonical, &stored.RatesDigest)
	if err != nil {
		s.error(c, err)
		return
	}
	quote, err := routecost.ParseStoredQuote(stored)
	if err != nil {
		s.fail(c, 503, "invalid_route_policy", "探测渠道配置不可用")
		return
	}
	var profile provider.Profile
	if json.Unmarshal(profileJSON, &profile) != nil {
		s.fail(c, 503, "invalid_profile", "模型能力配置不可用")
		return
	}
	body := gin.H{"model": profile.Model, "messages": []gin.H{{"role": "user", "content": "ping"}}, "max_tokens": 1}
	if profile.ProtocolName() == provider.ProtocolResponses {
		body = gin.H{"model": profile.Model, "input": "ping", "max_output_tokens": 1, "store": false}
	} else if profile.ProtocolName() == provider.ProtocolGemini {
		body = gin.H{"contents": []gin.H{{"role": "user", "parts": []gin.H{{"text": "ping"}}}}, "generationConfig": gin.H{"maxOutputTokens": 1, "candidateCount": 1}}
	} else if profile.ProtocolName() == provider.ProtocolEmbeddings {
		body = gin.H{"model": profile.Model, "input": "ping", "encoding_format": "float"}
	} else if profile.ProtocolName() == provider.ProtocolRerank {
		body = gin.H{"model": profile.Model, "query": "ping", "documents": []string{"ping"}, "top_n": 1, "return_documents": false}
	} else if profile.ProtocolName() == provider.ProtocolChat && profile.OutputLimitField == "max_completion_tokens" {
		delete(body, "max_tokens")
		body["max_completion_tokens"] = 1
	}
	raw, _ := json.Marshal(body)
	request, err := provider.Parse(profile.ProtocolName(), raw, profile)
	if err != nil {
		s.providerError(c, err)
		return
	}
	suffix, err := request.EndpointSuffixForModel(upstream)
	if err != nil {
		s.providerError(c, err)
		return
	}
	// Read the entire frozen pool using the transaction's existing connection.
	// Unpublished current_version changes cannot replace these members, and a
	// revoked primary does not prevent a live secondary from being selected.
	rows, err := tx.Query(ctx, `SELECT p.credential_id,p.weight,p.max_inflight,k.secret_ciphertext,k.revoked_at IS NOT NULL FROM channel_version_credentials p JOIN channel_credentials k ON k.id=p.credential_id AND k.channel_id=p.channel_id WHERE p.channel_version_id=$1 AND p.channel_id=$2 ORDER BY p.position`, version, channel)
	if err != nil {
		s.error(c, err)
		return
	}
	candidates := []routing.Candidate{}
	for rows.Next() {
		candidate := routing.Candidate{ChannelID: channel, VersionID: version, Model: upstream, Region: region, Priority: priority, Weight: weight, MaxInflight: maxInflight, Enabled: true, CostQuote: quote, Endpoint: provider.Endpoint{URL: baseURL + suffix, Model: upstream, Network: networkPolicy(baseURL, cidrs)}}
		var ciphertext string
		var revoked bool
		if err = rows.Scan(&candidate.CredentialID, &candidate.CredentialWeight, &candidate.CredentialMaxInflight, &ciphertext, &revoked); err != nil {
			break
		}
		candidate.HealthKey = candidate.CredentialID + ":" + modelID
		if revoked {
			candidate.Enabled = false
			candidate.ExclusionReason = "credential_revoked"
		} else {
			candidate.Endpoint.APIKey, err = s.Config.Secrets.OpenFor(ciphertext, "provider:platform", candidate.CredentialID)
			if err != nil {
				break
			}
		}
		candidates = append(candidates, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		s.error(c, err)
		return
	}
	plan, err := routing.NewPlan(candidates, 1)
	if err != nil {
		s.fail(c, 503, "invalid_route_policy", "探测渠道配置不可用")
		return
	}
	id := uuid.NewString()
	committed := false
	attempt, err := plan.Start(ctx, s.Routing, func(admitCtx context.Context, selected routing.Candidate, _ int) error {
		var locked string
		if e := tx.QueryRow(admitCtx, `SELECT id FROM channel_credentials WHERE id=$1 AND channel_id=$2 FOR SHARE`, selected.CredentialID, channel).Scan(&locked); e != nil {
			return e
		}
		// A new READ COMMITTED statement after the credential lock observes a
		// completed revocation before dispatch. Membership is the frozen one.
		var dispatchable bool
		if e := tx.QueryRow(admitCtx, `SELECT EXISTS(SELECT 1 FROM channel_version_credentials p JOIN channel_credentials k ON k.id=p.credential_id AND k.channel_id=p.channel_id WHERE p.channel_version_id=$1 AND p.channel_id=$2 AND p.credential_id=$3 AND k.revoked_at IS NULL)`, version, channel, selected.CredentialID).Scan(&dispatchable); e != nil {
			return e
		}
		if !dispatchable {
			return routing.ErrUnavailable
		}
		if _, e := tx.Exec(admitCtx, `INSERT INTO platform_probe_attempts(id,actor,channel_id,credential_id,channel_version_id,model_id,profile_version,upstream_model) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, id, sessionOf(c).User.ID, channel, selected.CredentialID, version, modelID, profileVersion, upstream); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "INSERT INTO audit_events(actor,action,target) VALUES($1,'channel.probe',$2)", sessionOf(c).User.ID, id); e != nil {
			return e
		}
		if e := tx.Commit(ctx); e != nil {
			return e
		}
		committed = true
		return nil
	})
	if err != nil {
		if committed {
			_ = s.saveProbe(id, "failed", provider.Usage{Status: provider.UsageMissing})
		}
		if errors.Is(err, routing.ErrUnavailable) {
			s.fail(c, 503, "probe_channel_unavailable", "渠道探测当前没有可用并发或健康许可")
		} else {
			s.error(c, err)
		}
		return
	}
	defer attempt.Finish(routing.Neutral)
	candidate := attempt.Candidate
	if err = s.guardContext(ctx); err != nil {
		_ = s.saveProbe(id, "failed", provider.Usage{Status: provider.UsageMissing})
		s.error(c, err)
		return
	}
	start := time.Now()
	response, callErr := s.Provider.Do(ctx, candidate.Endpoint, request)
	usage := provider.Usage{Status: provider.UsageMissing}
	if callErr == nil {
		_, callErr = response.DecodeJSON()
		usage = response.Usage
		_ = response.Close()
	}
	// The provider's socket/body has exited before returning its shared channel
	// lease. Terminal audit persistence uses a separate bounded cleanup context.
	if callErr != nil {
		s.applyProviderHealth(candidate, callErr)
		attempt.Finish(outcome(ctx, callErr))
	} else if usage.Status == provider.UsageConfirmed {
		attempt.Finish(routing.Success)
	} else {
		attempt.Finish(routing.Failure)
	}
	verified := callErr == nil && usage.Status == provider.UsageConfirmed
	status := "completed"
	if !verified {
		status = "failed"
	}
	if err = s.saveProbe(id, status, usage); err != nil {
		s.fail(c, 503, "probe_pending", "探测记录待核对")
		return
	}
	// The probe response separates the 035 evidence dimensions instead of
	// collapsing them into one boolean: protocol/discovery success against the
	// bound upstream model, reliable usage, the current procurement cost
	// basis, and - on failure - the consumption classification that governs
	// cross-channel retry. A downstream model list or HTTP 200 alone is never
	// seller qualification.
	consumption := "none"
	errorClass := ""
	if callErr != nil {
		var pe *provider.Error
		if errors.As(callErr, &pe) {
			consumption = string(pe.Consumption)
			errorClass = string(pe.Class)
		} else {
			consumption = "unknown"
		}
	}
	s.ok(c, gin.H{"id": id, "credential_id": candidate.CredentialID, "ok": verified, "message": "仅代表该凭据本次协议与用量校验；供应商成本单独核对", "latency_ms": time.Since(start).Milliseconds(), "cost_status": "unknown", "evidence": gin.H{"channel_kind": channelKind, "integration_mode": integrationMode, "upstream_model": upstream, "protocol_success": callErr == nil, "usage_status": usage.Status, "cost_basis": quote.Basis(), "error_class": errorClass, "consumption": consumption}})
}
