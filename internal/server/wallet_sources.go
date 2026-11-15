package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func walletSourceID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id.String() == v
}

func (s *Server) walletSources(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	if sessionOf(c).Tenant.Role != "owner" && sessionOf(c).Tenant.Role != "billing_viewer" {
		s.fail(c, http.StatusForbidden, "forbidden", "资金来源仅对租户所有者或账单查看者开放")
		return
	}
	s.sourcePage(c, tid)
}

func (s *Server) walletSourcePlatformTenant(c *gin.Context) (string, bool) {
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
	default:
		s.fail(c, http.StatusForbidden, "forbidden", "需要平台财务查看权限")
		return "", false
	}
	values := c.Request.URL.Query()["tenant_id"]
	// Exactly one explicit tenant scope; a typo must not turn into a global read.
	if len(values) != 1 || !walletSourceID(values[0]) {
		s.fail(c, http.StatusBadRequest, "invalid_tenant", "请选择有效租户")
		return "", false
	}
	return values[0], true
}

func (s *Server) platformWalletSources(c *gin.Context) {
	tid, ok := s.walletSourcePlatformTenant(c)
	if ok {
		s.sourcePage(c, tid)
	}
}

func (s *Server) sourcePage(c *gin.Context, tenant string) {
	s.page(c, `SELECT l.id,l.id AS lot_id,l.source_kind,l.refund_class,l.issued_amount::text AS issued_amount,
 b.available_amount::text AS available_amount,b.held_amount::text AS held_amount,b.consumed_amount::text AS consumed_amount,b.refund_held_amount::text AS refund_held_amount,b.retired_amount::text AS retired_amount,l.fifo_at AS created_at
 FROM funding_lots l JOIN funding_lot_balances b ON b.lot_id=l.id AND b.tenant_id=l.tenant_id WHERE l.tenant_id=$1`, tenant)
}

func (s *Server) checkWalletSources(c *gin.Context) {
	tid, ok := s.walletSourcePlatformTenant(c)
	if !ok {
		return
	}
	result, err := s.Billing.CheckWalletSources(c.Request.Context(), tid)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, result)
}

func (s *Server) requestFunding(c *gin.Context) {
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	// Keep the exact request-detail ownership rule, including members' own keys.
	var allowed bool
	err := s.Pool.QueryRow(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM request_records r
 WHERE r.tenant_id=$1 AND r.id=$2 AND ($3<>'member' OR r.owner_id=$4))`, tid, c.Param("id"), sessionOf(c).Tenant.Role, sessionOf(c).User.ID).Scan(&allowed)
	if err != nil {
		s.error(c, err)
		return
	}
	if !allowed {
		s.fail(c, http.StatusNotFound, "not_found", "请求不存在")
		return
	}
	s.page(c, `SELECT l.id,l.id AS lot_id,l.source_kind,l.refund_class,l.fifo_at AS created_at,
 a.allocation_index::text AS allocation_index,a.amount::text AS reserved_amount,
 s.charged_amount::text AS charged_amount,s.released_amount::text AS released_amount,
 (SELECT coalesce(sum(x.amount::numeric),0)::text FROM funding_consumption_refund_lots x
  WHERE x.tenant_id=a.tenant_id AND x.settlement_id=s.settlement_id AND x.allocation_index=a.allocation_index) AS refunded_amount
 FROM funding_reservation_lots a JOIN funding_lots l ON l.id=a.lot_id AND l.tenant_id=a.tenant_id
 LEFT JOIN funding_settlement_lots s ON s.tenant_id=a.tenant_id AND s.reservation_id=a.reservation_id AND s.allocation_index=a.allocation_index
 WHERE a.tenant_id=$1 AND a.request_id=$2`, tid, c.Param("id"))
}
