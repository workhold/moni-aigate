package server

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/strictjson"
	"io"
	"net/http"
	"strings"
)

func billingError(c *gin.Context, s *Server, e error) bool {
	var be *billing.Error
	if !errors.As(e, &be) {
		return false
	}
	status := http.StatusBadRequest
	switch be.Code {
	case "insufficient_balance", "key_budget_exceeded", "periodic_budget_exceeded":
		status = 402
	case "forbidden", "key_ip_forbidden":
		status = 403
	case "invalid_credentials", "key_invalid", "key_revoked", "invalid_key":
		status = 401
	case "idempotency_conflict", "request_in_progress", "request_exists", "request_finished", "already_approved", "self_approval", "refund_exceeds_charge", "revision_conflict", "route_changed", "procurement_approval_required", "procurement_inflight", "procurement_evidence_stale", "supplier_refund_evidence_required":
		status = 409
	case "procurement_not_found":
		status = 404
	case "procurement_execution_forbidden", "procurement_history_invariant", "procurement_execution_protocol", "procurement_history_resource", "procurement_history_exhausted", "procurement_prefix_untracked_domain", "procurement_outcome_unknown":
		status = 503
	case "procurement_periodic_budget_exceeded", "periodic_clock_regression", "periodic_budget_invariant", "periodic_budget_unavailable", "channel_unavailable", "profile_blocked", "no_price", "price_unavailable", "cost_quote_stale", "cost_unknown", "cost_limit_exceeded", "procurement_paused", "procurement_budget_exceeded", "procurement_binding_changed", "procurement_bound_invalid", "procurement_invariant":
		status = 503
	}
	if strings.Contains(be.Code, "conflict") {
		status = 409
	}
	if be.ResourceID != "" {
		c.Header("X-Original-Request-ID", be.ResourceID)
	}
	s.fail(c, status, be.Code, "操作未完成，请检查权限、余额、状态及 request_id")
	return true
}
func (s *Server) financialKey(c *gin.Context) bool {
	key := c.GetHeader("Idempotency-Key")
	if len(key) < 1 || len(key) > 200 {
		s.fail(c, 400, "idempotency_key_required", "资金操作需要 Idempotency-Key")
		return false
	}
	return true
}
func (s *Server) wallet(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	if sessionOf(c).Tenant.Role == "member" {
		s.fail(c, 403, "forbidden", "钱包仅对owner或账单查看员开放")
		return
	}
	v, e := s.Billing.Wallet(c.Request.Context(), billing.Query{TenantID: tid})
	if e != nil {
		s.error(c, e)
		return
	}
	totals, e := s.query(c.Request.Context(), `SELECT coalesce((SELECT sum(amount)::text FROM funding_receipts WHERE tenant_id=$1),'0') AS total_credited,coalesce((SELECT sum(charged_amount)::text FROM settlements WHERE tenant_id=$1),'0') AS total_charged,coalesce((SELECT sum(refunded_amount)::text FROM settlements WHERE tenant_id=$1),'0') AS total_refunded`, tid)
	if e != nil {
		s.error(c, e)
		return
	}
	data := gin.H{"tenant_id": v.TenantID, "available": v.Available, "held": v.Held}
	for k, value := range totals[0] {
		data[k] = value
	}
	s.ok(c, data)
}
func (s *Server) journals(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	if sessionOf(c).Tenant.Role == "member" {
		s.fail(c, 403, "forbidden", "账本仅对owner或账单查看员开放")
		return
	}
	s.page(c, `SELECT j.id,j.kind,j.reference,j.created_at,CASE WHEN j.kind='settled' THEN coalesce((SELECT sum(l.signed_delta)::text FROM journal_lines l JOIN ledger_accounts a ON a.id=l.account_id WHERE l.journal_id=j.id AND a.kind='REVENUE'),'0') ELSE coalesce((SELECT max(abs(l.signed_delta::numeric))::text FROM journal_lines l WHERE l.journal_id=j.id),'0') END AS amount FROM journals j WHERE j.tenant_id=$1`, tid)
}
func requestSQL() string {
	return `SELECT r.id,r.model_id,schema.schema_id,encode(schema.schema_digest,'hex') AS schema_digest,schema.variant_key,convert_from(schema.retail_rates_canonical,'UTF8')::jsonb AS retail_rates,convert_from(schema.retail_bounds_canonical,'UTF8')::jsonb AS retail_bounds,m.public_name AS model_name,r.status,r.billing_status,r.created_at,settlement.id AS settlement_id,settlement.charged_amount,v.amount AS reserved_amount,f.protocol_outcome,f.transport_end_reason,f.delivery_stage,f.finish_reason,d.stage AS final_local_delivery,CASE WHEN f.id IS NULL THEN NULL ELSE f.retail_usage_state END AS usage_status,CASE WHEN f.retail_usage_state='confirmed_final' THEN convert_from(f.retail_quantities_canonical,'UTF8')::jsonb ELSE NULL END AS retail_quantities FROM request_records r JOIN models m ON m.id=r.model_id JOIN request_meter_snapshots schema ON schema.tenant_id=r.tenant_id AND schema.request_id=r.id JOIN reservations v ON v.request_id=r.id LEFT JOIN settlements settlement ON settlement.request_id=r.id LEFT JOIN finalization_records f ON f.id=r.latest_finalization_id LEFT JOIN LATERAL(SELECT stage FROM delivery_observations WHERE request_id=r.id ORDER BY created_at DESC LIMIT 1)d ON true`
}
func (s *Server) requests(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	s.page(c, requestSQL()+` WHERE r.tenant_id=$1 AND ($2<>'member' OR r.owner_id=$3)`, tid, sessionOf(c).Tenant.Role, sessionOf(c).User.ID)
}
func (s *Server) request(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	v, e := s.query(c.Request.Context(), requestSQL()+` WHERE r.tenant_id=$1 AND r.id=$2 AND ($3<>'member' OR r.owner_id=$4)`, tid, c.Param("id"), sessionOf(c).Tenant.Role, sessionOf(c).User.ID)
	if e != nil {
		s.error(c, e)
		return
	}
	if len(v) == 0 {
		s.fail(c, 404, "not_found", "请求不存在")
		return
	}
	s.ok(c, v[0])
}
func (s *Server) credits(c *gin.Context) {
	s.page(c, `SELECT o.id,o.tenant_id,t.name AS tenant_name,o.business_ref,o.amount,o.state,o.requester,o.approver,o.created_at FROM credit_orders o JOIN tenants t ON t.id=o.tenant_id`)
}
func (s *Server) createCredit(c *gin.Context) {
	if !s.financial(c, false) || !s.financialKey(c) {
		return
	}
	var in billing.CreditInput
	if !s.bind(c, &in) {
		return
	}
	in.ActorID = sessionOf(c).User.ID
	in.IdempotencyKey = c.GetHeader("Idempotency-Key")
	in.RequestHMAC = s.digestInput(struct {
		TenantID, BusinessRef, ProofRef string
		Amount                          billing.Money
	}{in.TenantID, in.BusinessRef, in.ProofRef, in.Amount})
	v, e := s.Billing.CreateCredit(c.Request.Context(), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) approveCredit(c *gin.Context) {
	if !s.financial(c, true) || !s.financialKey(c) {
		return
	}
	var tid string
	if e := s.Pool.QueryRow(c.Request.Context(), "SELECT tenant_id FROM credit_orders WHERE id=$1", c.Param("id")).Scan(&tid); e != nil {
		s.error(c, e)
		return
	}
	v, e := s.Billing.ApproveCredit(c.Request.Context(), billing.ApprovalInput{TenantID: tid, OrderID: c.Param("id"), ActorID: sessionOf(c).User.ID})
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) refunds(c *gin.Context) {
	s.page(c, `SELECT o.id,o.tenant_id,t.name AS tenant_name,o.settlement_id,o.business_ref,o.amount,o.reason,o.state,o.requester,o.approver,o.created_at FROM refund_orders o JOIN tenants t ON t.id=o.tenant_id`)
}
func (s *Server) createRefund(c *gin.Context) {
	if !s.financial(c, false) || !s.financialKey(c) {
		return
	}
	var in billing.RefundInput
	if !s.bind(c, &in) {
		return
	}
	in.ActorID = sessionOf(c).User.ID
	if in.BusinessRef == "" {
		in.BusinessRef = "refund:" + in.TenantID + ":" + s.Config.Secrets.Digest(c.GetHeader("Idempotency-Key"))
	}
	in.IdempotencyKey = c.GetHeader("Idempotency-Key")
	in.RequestHMAC = s.digestInput(struct {
		TenantID, SettlementID, BusinessRef, Reason string
		Amount                                      billing.Money
	}{in.TenantID, in.SettlementID, in.BusinessRef, in.Reason, in.Amount})
	v, e := s.Billing.CreateRefund(c.Request.Context(), in)
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) approveRefund(c *gin.Context) {
	if !s.financial(c, true) || !s.financialKey(c) {
		return
	}
	var tid string
	if e := s.Pool.QueryRow(c.Request.Context(), "SELECT tenant_id FROM refund_orders WHERE id=$1", c.Param("id")).Scan(&tid); e != nil {
		s.error(c, e)
		return
	}
	v, e := s.Billing.ApproveRefund(c.Request.Context(), billing.ApprovalInput{TenantID: tid, OrderID: c.Param("id"), ActorID: sessionOf(c).User.ID})
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) reconciliations(c *gin.Context) {
	s.page(c, `SELECT x.id,x.request_id,x.tenant_id,x.reason,x.state,x.state AS status,
 CASE WHEN x.proposed_observation_canonical IS NULL THEN NULL ELSE convert_from(x.proposed_observation_canonical,'UTF8')::jsonb END AS retail_quantities,
	 x.requester,x.approver,x.created_at,v.deadline,s.schema_id FROM reconciliation_cases x JOIN reservations v ON v.request_id=x.request_id JOIN request_meter_snapshots s ON s.tenant_id=x.tenant_id AND s.request_id=x.request_id`)
}

type reconciliationResolveRequest struct {
	Action           string          `json:"action"`
	Reason           string          `json:"reason"`
	RetailQuantities json.RawMessage `json:"retail_quantities"`
	EvidenceRef      string          `json:"evidence_ref"`
}

func reconciliationBillingAction(action string) string {
	switch action {
	case "settle":
		return "propose"
	case "release":
		return "release"
	default:
		return ""
	}
}

func bindReconciliationResolve(c *gin.Context, out *reconciliationResolveRequest) bool {
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 32*1024+1))
	if err != nil || len(raw) > 32*1024 {
		return false
	}
	value, err := strictjson.Parse(raw, 32*1024)
	object, ok := value.(map[string]any)
	if err != nil || !ok || len(object) < 2 || len(object) > 4 {
		return false
	}
	for key := range object {
		if key != "action" && key != "reason" && key != "retail_quantities" && key != "evidence_ref" {
			return false
		}
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return false
	}
	return true
}

func (s *Server) resolve(c *gin.Context) {
	if !s.financialKey(c) {
		return
	}
	var in reconciliationResolveRequest
	if !bindReconciliationResolve(c, &in) {
		s.fail(c, http.StatusBadRequest, "invalid_request", "JSON参数无效或请求过大")
		return
	}
	if in.Action != "release" && in.Action != "settle" {
		s.fail(c, http.StatusBadRequest, "invalid_request", "核对处理方式无效")
		return
	}
	if in.Action == "release" && (len(in.RetailQuantities) != 0 || in.EvidenceRef != "") {
		s.fail(c, http.StatusBadRequest, "invalid_request", "释放不得携带客户用量或收费证据")
		return
	}
	if in.Action == "settle" && len(in.RetailQuantities) == 0 {
		s.fail(c, http.StatusBadRequest, "invalid_request", "提交收费必须提供完整 retail_quantities")
		return
	}
	if in.Action == "release" {
		if !s.financial(c, true) {
			return
		}
	} else if !s.financial(c, false) {
		return
	}
	billingAction := reconciliationBillingAction(in.Action)
	var tid, rid string
	if e := s.Pool.QueryRow(c.Request.Context(), "SELECT tenant_id,request_id FROM reconciliation_cases WHERE id=$1", c.Param("id")).Scan(&tid, &rid); e != nil {
		s.error(c, e)
		return
	}
	v, e := s.Billing.Reconcile(c.Request.Context(), billing.ReconcileInput{TenantID: tid, RequestID: rid, ActorID: sessionOf(c).User.ID, Action: billingAction, Reason: in.Reason, EvidenceRef: in.EvidenceRef, RetailQuantitiesCanonical: in.RetailQuantities})
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) approveResolve(c *gin.Context) {
	if !s.financial(c, true) || !s.financialKey(c) {
		return
	}
	var tid, rid string
	if e := s.Pool.QueryRow(c.Request.Context(), "SELECT tenant_id,request_id FROM reconciliation_cases WHERE id=$1", c.Param("id")).Scan(&tid, &rid); e != nil {
		s.error(c, e)
		return
	}
	v, e := s.Billing.Reconcile(c.Request.Context(), billing.ReconcileInput{TenantID: tid, RequestID: rid, ActorID: sessionOf(c).User.ID, Action: "approve"})
	if e != nil {
		s.error(c, e)
		return
	}
	s.ok(c, v)
}
func (s *Server) digestInput(v any) string {
	b, _ := json.Marshal(v)
	return s.Config.Secrets.Digest(string(b))
}
