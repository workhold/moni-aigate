package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func (s *Server) walletRequestTenant(c *gin.Context, write bool) (string, bool) {
	tenant, ok := s.tenant(c)
	if !ok {
		return "", false
	}
	role := sessionOf(c).Tenant.Role
	if role != "owner" && (write || role != "billing_viewer") {
		s.fail(c, http.StatusForbidden, "forbidden", "钱包申请仅对租户所有者和账单查看员开放")
		return "", false
	}
	if write && (!s.sensitive(c) || !s.financialKey(c)) {
		return "", false
	}
	return tenant, true
}

func (s *Server) walletRequests(c *gin.Context) {
	tenant, ok := s.walletRequestTenant(c, false)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c, "cursor"); !ok {
		return
	}
	s.page(c, `SELECT id,tenant_id,kind,requested_by,note,state,linked_order_id,created_at FROM wallet_requests WHERE tenant_id=$1`, tenant)
}

func (s *Server) createWalletRequest(c *gin.Context) {
	tenant, ok := s.walletRequestTenant(c, true)
	if !ok {
		return
	}
	var in struct {
		Kind string `json:"kind"`
		Note string `json:"note"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "kind", "note") {
		s.commerceInvalid(c)
		return
	}
	actor := sessionOf(c).User.ID
	request, err := s.Billing.CreateWalletRequest(c.Request.Context(), billing.WalletRequestInput{
		TenantID: tenant, ActorID: actor, Kind: in.Kind, Note: in.Note,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		RequestHMAC:    s.digestInput(struct{ TenantID, ActorID, Kind, Note string }{tenant, actor, in.Kind, in.Note}),
	})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, request)
}

func (s *Server) closeWalletRequest(c *gin.Context, platform bool) {
	var tenant string
	var ok bool
	if platform {
		if !s.walletFinancial(c, false) || !s.financialKey(c) {
			return
		}
	} else if tenant, ok = s.walletRequestTenant(c, true); !ok {
		return
	}
	requestID := c.Param("id")
	if !procurementUUID(requestID) {
		s.commerceInvalid(c)
		return
	}
	if platform {
		if err := s.Pool.QueryRow(c.Request.Context(), `SELECT tenant_id FROM wallet_requests WHERE id=$1`, requestID).Scan(&tenant); errors.Is(err, pgx.ErrNoRows) {
			s.fail(c, http.StatusNotFound, "not_found", "申请不存在")
			return
		} else if err != nil {
			s.error(c, err)
			return
		}
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 8192, "reason") {
		s.commerceInvalid(c)
		return
	}
	actor := sessionOf(c).User.ID
	request, err := s.Billing.CloseWalletRequest(c.Request.Context(), billing.WalletRequestCloseInput{
		TenantID: tenant, RequestID: requestID, ActorID: actor, Reason: in.Reason, Platform: platform,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		RequestHMAC:    s.digestInput(struct{ TenantID, ActorID, RequestID, Reason string }{tenant, actor, requestID, in.Reason}),
	})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, request)
}

func (s *Server) customerCloseWalletRequest(c *gin.Context) { s.closeWalletRequest(c, false) }
func (s *Server) platformCloseWalletRequest(c *gin.Context) { s.closeWalletRequest(c, true) }

func (s *Server) platformWalletRequests(c *gin.Context) {
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
	default:
		s.fail(c, http.StatusForbidden, "forbidden", "需要平台或财务读取权限")
		return
	}
	query, ok := s.commerceQuery(c, "tenant_id", "kind", "state", "cursor")
	if !ok {
		return
	}
	tenant, kind, state := query.Get("tenant_id"), query.Get("kind"), query.Get("state")
	if tenant != "" && !procurementUUID(tenant) || kind != "" && kind != "topup" && kind != "cash_refund" || state != "" && state != "open" && state != "linked" && state != "closed" {
		s.commerceInvalid(c)
		return
	}
	s.page(c, `SELECT id,tenant_id,kind,requested_by,note,state,linked_order_id,created_at FROM wallet_requests WHERE ($1='' OR tenant_id=$1) AND ($2='' OR kind=$2) AND ($3='' OR state=$3)`, tenant, kind, state)
}
