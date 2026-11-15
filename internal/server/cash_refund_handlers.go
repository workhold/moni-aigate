package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/security"
)

const cashRefundListSQL = `SELECT o.id,o.tenant_id,t.name AS tenant_name,o.lot_id,o.business_ref,o.coin_amount::text AS coin_amount,o.reason,o.state,o.requested_by,o.maker,o.approver,o.transfer_executor,o.fiat_currency,o.fiat_amount::text AS fiat_amount,o.buy_rate_version_id,o.refund_rate_version_id,o.single_operator,(o.single_operator AND o.approver=o.maker AND o.requested_by=o.approver) AS single_operator_exception,o.transfer_order_ref,o.transfer_proof_ref,o.paid_fiat_amount::text AS paid_fiat_amount,o.hold_journal_id,o.retire_journal_id,o.release_journal_id,o.approved_at,o.created_at,(SELECT wr.id FROM wallet_requests wr WHERE wr.linked_order_id=o.id AND wr.kind='cash_refund') AS request_id,
 CASE WHEN o.state='requested' THEN b.available_amount::text END AS preview_coin_amount,
 CASE WHEN o.state='requested' THEN wallet_cash_refund_fiat_amount(b.available_amount,f.fiat_amount,l.issued_amount,buy.rate_num,buy.rate_den,cur.rate_num,cur.rate_den)::text END AS preview_fiat_amount,
 CASE WHEN o.state='requested' THEN f.fiat_currency END AS preview_fiat_currency
 FROM cash_refund_orders o JOIN tenants t ON t.id=o.tenant_id
 JOIN funding_lots l ON l.tenant_id=o.tenant_id AND l.id=o.lot_id
 JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id
 JOIN funding_lot_balances b ON b.tenant_id=l.tenant_id AND b.lot_id=l.id
 LEFT JOIN coin_rate_versions buy ON buy.id=f.buy_rate_version_id
 LEFT JOIN coin_rate_versions cur ON cur.id=(SELECT x.id FROM coin_rate_versions x WHERE x.fiat_currency=f.fiat_currency AND x.effective_at<=clock_timestamp() ORDER BY x.effective_at DESC,x.id DESC LIMIT 1)`

func (s *Server) cashRefundReader(c *gin.Context) bool {
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
		return true
	default:
		s.fail(c, http.StatusForbidden, "forbidden", "需要平台或财务读取权限")
		return false
	}
}

func (s *Server) cashRefundAnyFinancial(c *gin.Context) bool {
	switch sessionOf(c).User.PlatformRole {
	case "finance_operator", "finance_approver":
		return true
	case "admin":
		// Existing orders carry their own approval-time snapshot. The domain
		// transaction rechecks it; the HTTP layer does not require today's switch.
		return true
	default:
		s.fail(c, http.StatusForbidden, "forbidden", "需要钱包财务权限")
		return false
	}
}

func (s *Server) cashRefunds(c *gin.Context) {
	if !s.cashRefundReader(c) {
		return
	}
	query, ok := s.commerceQuery(c, "tenant_id", "state", "business_ref", "cursor")
	if !ok {
		return
	}
	tenant, state, business := query.Get("tenant_id"), query.Get("state"), query.Get("business_ref")
	if tenant != "" && !procurementUUID(tenant) || business != "" && !security.ValidReference(business, 200) {
		s.commerceInvalid(c)
		return
	}
	switch state {
	case "", "requested", "approved", "transfer_executed", "completed", "cancelled", "failed":
	default:
		s.commerceInvalid(c)
		return
	}
	s.page(c, cashRefundListSQL+` WHERE ($1='' OR o.tenant_id=$1) AND ($2='' OR o.state=$2) AND ($3='' OR o.business_ref=$3)`, tenant, state, business)
}

func (s *Server) cashRefund(c *gin.Context) {
	if !s.cashRefundReader(c) {
		return
	}
	id := c.Param("id")
	if !procurementUUID(id) {
		s.commerceInvalid(c)
		return
	}
	var tenant string
	if err := s.Pool.QueryRow(c.Request.Context(), `SELECT tenant_id FROM cash_refund_orders WHERE id=$1`, id).Scan(&tenant); errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, http.StatusNotFound, "cash_refund_not_found", "现金退款单不存在")
		return
	} else if err != nil {
		s.error(c, err)
		return
	}
	order, err := s.Billing.CashRefund(c.Request.Context(), tenant, id)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, order)
}

func (s *Server) cashRefundByBusiness(c *gin.Context) {
	if !s.cashRefundReader(c) {
		return
	}
	order, err := s.Billing.CashRefundByBusinessRef(c.Request.Context(), strings.TrimPrefix(c.Param("ref"), "/"))
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, order)
}

type cashRefundCreateRequest struct {
	TenantID    string `json:"tenant_id"`
	LotID       string `json:"lot_id"`
	BusinessRef string `json:"business_ref"`
	Reason      string `json:"reason"`
	RequestID   string `json:"request_id"`
}

func (s *Server) createCashRefund(c *gin.Context) {
	if !s.walletFinancial(c, false) || !s.financialKey(c) {
		return
	}
	var in cashRefundCreateRequest
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "tenant_id", "lot_id", "business_ref", "reason", "request_id") {
		s.commerceInvalid(c)
		return
	}
	actor := sessionOf(c).User.ID
	order, err := s.Billing.CreateCashRefund(c.Request.Context(), billing.CashRefundCreateInput{
		IdempotencyKey: c.GetHeader("Idempotency-Key"), RequestHMAC: s.digestInput(struct {
			TenantID, ActorID, LotID, BusinessRef, Reason, RequestID string
		}{in.TenantID, actor, in.LotID, in.BusinessRef, in.Reason, in.RequestID}),
		TenantID: in.TenantID, ActorID: actor, LotID: in.LotID, BusinessRef: in.BusinessRef, Reason: in.Reason, RequestID: in.RequestID,
	})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, order)
}

func (s *Server) cashRefundTenant(c *gin.Context, id string) (string, bool) {
	if !procurementUUID(id) {
		s.commerceInvalid(c)
		return "", false
	}
	var tenant string
	if err := s.Pool.QueryRow(c.Request.Context(), `SELECT tenant_id FROM cash_refund_orders WHERE id=$1`, id).Scan(&tenant); errors.Is(err, pgx.ErrNoRows) {
		s.fail(c, http.StatusNotFound, "cash_refund_not_found", "现金退款单不存在")
		return "", false
	} else if err != nil {
		s.error(c, err)
		return "", false
	}
	return tenant, true
}

func (s *Server) cashRefundAction(c *gin.Context, action string) {
	if !s.financialKey(c) {
		return
	}
	switch action {
	case "approve":
		if !s.walletFinancial(c, true) {
			return
		}
	case "execute":
		if !s.walletFinancial(c, false) {
			return
		}
	default:
		if !s.cashRefundAnyFinancial(c) {
			return
		}
	}
	tenant, ok := s.cashRefundTenant(c, c.Param("id"))
	if !ok {
		return
	}
	actor := sessionOf(c).User.ID
	in := billing.CashRefundActionInput{IdempotencyKey: c.GetHeader("Idempotency-Key"), TenantID: tenant, OrderID: c.Param("id"), ActorID: actor}
	switch action {
	case "approve":
		var body map[string]json.RawMessage
		if !decodeCanonicalJSON(c.Request.Body, &body, 1024) || len(body) != 0 {
			s.commerceInvalid(c)
			return
		}
	case "execute":
		var body struct {
			TransferOrderRef string `json:"transfer_order_ref"`
		}
		if !decodeCanonicalJSON(c.Request.Body, &body, 4096, "transfer_order_ref") {
			s.commerceInvalid(c)
			return
		}
		in.TransferOrderRef = body.TransferOrderRef
	case "complete":
		var body struct {
			TransferProofRef string             `json:"transfer_proof_ref"`
			PaidFiatAmount   *billing.FiatMoney `json:"paid_fiat_amount"`
		}
		if !decodeCanonicalJSON(c.Request.Body, &body, 4096, "transfer_proof_ref", "paid_fiat_amount") {
			s.commerceInvalid(c)
			return
		}
		in.TransferProofRef, in.PaidFiatAmount = body.TransferProofRef, body.PaidFiatAmount
	case "cancel", "fail":
		var body struct {
			Reason string `json:"reason"`
		}
		if !decodeCanonicalJSON(c.Request.Body, &body, 4096, "reason") {
			s.commerceInvalid(c)
			return
		}
		in.Reason = body.Reason
	default:
		s.fail(c, http.StatusNotFound, "not_found", "现金退款动作不存在")
		return
	}
	in.RequestHMAC = s.digestInput(struct {
		TenantID, ActorID, OrderID, Action, TransferOrderRef, TransferProofRef, Reason string
		PaidFiatAmount                                                                 *billing.FiatMoney
	}{tenant, actor, in.OrderID, action, in.TransferOrderRef, in.TransferProofRef, in.Reason, in.PaidFiatAmount})
	var order billing.CashRefundOrder
	var err error
	switch action {
	case "approve":
		order, err = s.Billing.ApproveCashRefund(c.Request.Context(), in)
	case "execute":
		order, err = s.Billing.ExecuteCashRefund(c.Request.Context(), in)
	case "complete":
		order, err = s.Billing.CompleteCashRefund(c.Request.Context(), in)
	case "cancel":
		order, err = s.Billing.CancelCashRefund(c.Request.Context(), in)
	case "fail":
		order, err = s.Billing.FailCashRefund(c.Request.Context(), in)
	}
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, order)
}

func (s *Server) approveCashRefund(c *gin.Context)  { s.cashRefundAction(c, "approve") }
func (s *Server) executeCashRefund(c *gin.Context)  { s.cashRefundAction(c, "execute") }
func (s *Server) completeCashRefund(c *gin.Context) { s.cashRefundAction(c, "complete") }
func (s *Server) cancelCashRefund(c *gin.Context)   { s.cashRefundAction(c, "cancel") }
func (s *Server) failCashRefund(c *gin.Context)     { s.cashRefundAction(c, "fail") }
