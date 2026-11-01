package server

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func (s *Server) customerReport(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	if sessionOf(c).Tenant.Role == "member" {
		s.fail(c, 403, "forbidden", "报表仅对owner或账单查看员开放")
		return
	}
	s.report(c, tid, false)
}
func (s *Server) platformReport(c *gin.Context) { s.report(c, c.Query("tenant_id"), true) }
func (s *Server) report(c *gin.Context, tid string, platform bool) {
	if tid != "" {
		if _, e := uuid.Parse(tid); e != nil {
			s.fail(c, 400, "invalid_tenant", "租户ID无效")
			return
		}
	}
	// One statement provides a consistent projection snapshot and pending count.
	rows, e := s.query(c.Request.Context(), `SELECT
 coalesce((SELECT sum(credited)::text FROM report_totals WHERE consumer=a.consumer AND ($1='' OR tenant_id=$1)),'0') AS credited,
 coalesce((SELECT sum(charged)::text FROM report_totals WHERE consumer=a.consumer AND ($1='' OR tenant_id=$1)),'0') AS charged,
 coalesce((SELECT sum(refunded)::text FROM report_totals WHERE consumer=a.consumer AND ($1='' OR tenant_id=$1)),'0') AS refunded,
 coalesce((SELECT sum(released)::text FROM report_totals WHERE consumer=a.consumer AND ($1='' OR tenant_id=$1)),'0') AS released,
 coalesce((SELECT sum(requests)::bigint FROM report_totals WHERE consumer=a.consumer AND ($1='' OR tenant_id=$1)),0) AS requests,
 (SELECT count(*) FROM projection_outbox o WHERE ($1='' OR o.tenant_id=$1) AND NOT EXISTS(SELECT 1 FROM projection_receipts r WHERE r.consumer=a.consumer AND r.event_id=o.event_id)) AS projection_pending,
 clock_timestamp() AS as_of FROM active_report_projection a WHERE singleton`, tid)
	if e != nil {
		s.error(c, e)
		return
	}
	if len(rows) != 1 {
		s.fail(c, 503, "projection_unavailable", "报表投影暂不可用")
		return
	}
	data := rows[0]
	data["tenant_id"] = tid
	if platform {
		costs, e := s.query(c.Request.Context(), `SELECT coalesce(sum(cost_amount) FILTER(WHERE cost_status<>'unknown'),'0')::text AS known_provider_cost,count(*) FILTER(WHERE cost_status='unknown' OR cost_amount IS NULL) AS unknown_provider_costs FROM upstream_attempts WHERE ($1='' OR tenant_id=$1)`, tid)
		if e != nil {
			s.error(c, e)
			return
		}
		var probes int64
		if e = s.Pool.QueryRow(c.Request.Context(), "SELECT count(*) FROM platform_probe_attempts WHERE cost_amount IS NULL OR cost_basis='unknown'").Scan(&probes); e != nil {
			s.error(c, e)
			return
		}
		data["unknown_probe_costs"] = probes
		for k, v := range costs[0] {
			data[k] = v
		}
	}
	s.ok(c, data)
}
func (s *Server) costs(c *gin.Context) {
	s.page(c, `SELECT a.id,a.tenant_id,t.name AS tenant_name,a.request_id,a.channel_id,ch.name AS channel_name,a.upstream_model,a.cost_amount::text AS cost_amount,a.cost_status,a.cost_version_id,a.cost_basis,a.created_at FROM upstream_attempts a JOIN tenants t ON t.id=a.tenant_id JOIN channels ch ON ch.id=a.channel_id WHERE ($1='' OR a.tenant_id=$1)`, c.Query("tenant_id"))
}
func (s *Server) correctCost(c *gin.Context) {
	if !s.financial(c, false) || !s.financialKey(c) {
		return
	}
	var in struct {
		TenantID  string         `json:"tenant_id"`
		AttemptID string         `json:"attempt_id"`
		Amount    *billing.Money `json:"amount"`
		Basis     string         `json:"basis"`
		Evidence  string         `json:"evidence_ref"`
	}
	if !s.bind(c, &in) {
		return
	}
	if in.Amount == nil {
		s.fail(c, 400, "invalid_cost", "请显式填写成本金额")
		return
	}
	e := s.Billing.RecordCostCorrection(c.Request.Context(), billing.CostCorrectionInput{TenantID: in.TenantID, AttemptID: in.AttemptID, Amount: *in.Amount, Basis: in.Basis, EvidenceRef: in.Evidence, ActorID: sessionOf(c).User.ID, OperationID: "cost:" + in.TenantID + ":" + s.Config.Secrets.Digest(c.GetHeader("Idempotency-Key"))})
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, gin.H{"ok": true})
}
func (s *Server) costVersions(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	s.page(c, `SELECT v.id,v.channel_id,ch.name AS channel_name,ch.management_revision::text AS channel_revision,v.upstream_model,v.input_rate::text AS input_rate,v.output_rate::text AS output_rate,v.basis,v.effective_at,v.created_at FROM cost_versions v JOIN channels ch ON ch.id=v.channel_id`)
}
func (s *Server) publishCost(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in catalog.CostVersionInput
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "channel_id", "upstream_model", "input_rate", "output_rate", "basis", "effective_at", "expected_channel_revision") {
		s.fail(c, 400, "invalid_request", "成本版本参数无效")
		return
	}
	result, e := s.Catalog.PublishCostVersion(c.Request.Context(), sessionOf(c).User.ID, in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, result)
}
func (s *Server) attempts(c *gin.Context) {
	// Platform only: no credential value, endpoint, prompt, or output is returned.
	s.page(c, `SELECT a.id,a.request_id,a.tenant_id,a.sequence,a.channel_id,a.channel_version_id,a.credential_id,a.upstream_model,a.delivery_state,a.cost_status,a.cost_amount::text AS cost_amount,a.created_at,a.route_decisions,r.route_revision,b.priority,b.weight,(i.channel_version_id IS NOT NULL) AS binding_isolated,i.reason AS isolation_reason,i.created_at AS isolated_at FROM upstream_attempts a JOIN request_records r ON r.id=a.request_id LEFT JOIN route_policy_versions p ON p.model_id=r.model_id AND p.revision=r.route_revision LEFT JOIN model_bindings b ON b.route_policy_version_id=p.id AND b.channel_id=a.channel_id AND b.channel_version_id=a.channel_version_id AND b.upstream_model=a.upstream_model LEFT JOIN route_binding_isolations i ON i.channel_version_id=a.channel_version_id AND i.upstream_model=a.upstream_model WHERE a.request_id=$1`, c.Param("id"))
}

func (s *Server) probesList(c *gin.Context) {
	s.page(c, `SELECT p.id,p.channel_id,c.name AS channel_name,p.channel_version_id,p.credential_id,p.model_id,p.upstream_model,p.status,p.cost_basis,p.cost_amount::text AS cost_amount,p.created_at,p.completed_at FROM platform_probe_attempts p JOIN channels c ON c.id=p.channel_id`)
}
