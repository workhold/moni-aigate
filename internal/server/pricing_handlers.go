package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/catalog"
)

func (s *Server) registerPricing(a, p *gin.RouterGroup) {
	a.GET("/tenant/pricing", s.ownTenantPricing)
	a.GET("/models/:id/retail-price", s.retailPrice)
	p.GET("/price-groups", s.priceGroups)
	p.POST("/price-groups", s.createPriceGroup)
	p.GET("/price-groups/:id", s.priceGroup)
	p.PUT("/price-groups/:id", s.updatePriceGroup)
	p.GET("/price-groups/:id/price-versions", s.groupPrices)
	p.POST("/price-groups/:id/price-versions", s.publishGroupPrice)
	p.GET("/tenants/:id/pricing", s.tenantPricing)
	p.PUT("/tenants/:id/pricing", s.assignTenantPricing)
	p.GET("/tenants/:id/pricing-preview", s.pricingPreview)
	p.GET("/pricing-operations/:id", s.pricingOperation)
}
func (s *Server) pricingError(c *gin.Context, e error) {
	code, status := "pricing_busy", 503
	var pe *catalog.PricingError
	if errors.As(e, &pe) {
		switch pe.Code {
		case "invalid_request", "invalid_cursor", "invalid_price":
			code, status = pe.Code, 400
		case "forbidden":
			code, status = pe.Code, 403
		case "not_found":
			code, status = pe.Code, 404
		case "pricing_revision_conflict", "price_group_retired", "price_effective_conflict", "policy_operation_conflict", "pricing_revision_exhausted":
			code, status = pe.Code, 409
		case "pricing_busy", "pricing_invariant":
			code = pe.Code
		}
	}
	s.fail(c, status, code, "定价操作未完成；结果不明时请按原操作编号查询")
}
func (s *Server) pricingReader(c *gin.Context) bool {
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
		return true
	}
	s.fail(c, 403, "forbidden", "需要当前平台定价查看权限")
	return false
}
func (s *Server) pricingWriter(c *gin.Context) bool {
	if !s.admin(c) || !s.sensitive(c) {
		return false
	}
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		s.commerceInvalid(c)
		return false
	}
	return true
}
func (s *Server) priceGroups(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	q, ok := s.commerceQuery(c, "cursor")
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "read", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.PriceGroups(c.Request.Context(), sessionOf(c).User.ID, q.Get("cursor"))
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"data": v.Data, "next_cursor": v.NextCursor, "request_id": c.GetString("request_id")})
}
func (s *Server) priceGroup(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "read", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.PriceGroup(c.Request.Context(), sessionOf(c).User.ID, id)
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) groupPrices(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	q, ok := s.commerceQuery(c, "cursor")
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "read", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.GroupPrices(c.Request.Context(), sessionOf(c).User.ID, id, q.Get("cursor"))
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"data": v.Data, "next_cursor": v.NextCursor, "request_id": c.GetString("request_id")})
}
func (s *Server) pricingOperation(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "read", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.GetPricingOperation(c.Request.Context(), sessionOf(c).User.ID, id)
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) tenantPricing(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "read", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.TenantPricing(c.Request.Context(), sessionOf(c).User.ID, id)
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) ownTenantPricing(c *gin.Context) {
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "own", tid)
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.OwnTenantPricing(c.Request.Context(), sessionOf(c).User.ID, tid)
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) retailPrice(c *gin.Context) {
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	tid, ok := s.tenant(c)
	if !ok {
		return
	}
	done, ok := s.admitPricing(c, "own", tid)
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.RetailPrice(c.Request.Context(), sessionOf(c).User.ID, tid, id)
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) pricingPreview(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	q, ok := s.commerceQuery(c, "price_group_id", "cursor")
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	target := q.Get("price_group_id")
	var group *string
	if target != "default" {
		if !procurementUUID(target) {
			s.commerceInvalid(c)
			return
		}
		group = &target
	}
	done, ok := s.admitPricing(c, "preview", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.PreviewTenantPricing(c.Request.Context(), sessionOf(c).User.ID, id, group, q.Get("cursor"))
	if e != nil {
		s.pricingError(c, e)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"data": v.Data, "context": v.Context, "next_cursor": v.NextCursor, "request_id": c.GetString("request_id")})
}
func (s *Server) pricingResult(c *gin.Context, status int, operation catalog.PricingOperationView, result any) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"data": catalog.PricingOperationReceipt{Operation: operation, Result: result}, "request_id": c.GetString("request_id")})
}
func (s *Server) createPriceGroup(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "label")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, opOK := commerceID(obj["operation_id"])
	label, labelOK := commerceString(obj["label"])
	if !opOK || !labelOK || !validPricingLabel(label) {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.CreatePriceGroup(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.GroupCreateCommand{OperationID: op, Label: label})
	if e != nil {
		s.pricingError(c, e)
		return
	}
	status := 201
	if v.Replayed {
		status = 200
	}
	s.pricingResult(c, status, v.Operation, v.Result)
}
func validPricingLabel(label string) bool {
	return strings.TrimSpace(label) == label && len(label) >= 1 && len(label) <= 100
}
func (s *Server) updatePriceGroup(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "expected_revision", "label", "state")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	rev, b := commerceInteger(obj["expected_revision"], true)
	label, d := commerceString(obj["label"])
	state, e := commerceString(obj["state"])
	if !a || !b || !d || !e || !validPricingLabel(label) || (state != "active" && state != "retired") {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.UpdatePriceGroup(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.GroupUpdateCommand{OperationID: op, GroupID: id, ExpectedRevision: rev, Label: label, State: state})
	if err != nil {
		s.pricingError(c, err)
		return
	}
	s.pricingResult(c, 200, v.Operation, v.Result)
}
func (s *Server) publishGroupPrice(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "model_id", "input_rate", "output_rate", "effective_at")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	model, b := commerceID(obj["model_id"])
	in, d := commerceInteger(obj["input_rate"], false)
	out, e := commerceInteger(obj["output_rate"], false)
	at, f := commerceExpiry(obj["effective_at"])
	if !a || !b || !d || !e || !f || at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.PublishGroupPrice(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.GroupPriceCommand{OperationID: op, GroupID: id, ModelID: model, InputRate: in, OutputRate: out, EffectiveAt: at.UTC().Truncate(time.Microsecond)})
	if err != nil {
		s.pricingError(c, err)
		return
	}
	status := http.StatusCreated
	if v.Replayed {
		status = 200
	}
	s.pricingResult(c, status, v.Operation, v.Result)
}
func (s *Server) assignTenantPricing(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "expected_revision", "price_group_id")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	rev, b := commerceInteger(obj["expected_revision"], true)
	var group *string
	valid := true
	if string(obj["price_group_id"]) != "null" {
		v, ok := commerceID(obj["price_group_id"])
		valid = ok
		group = &v
	}
	if !a || !b || !valid {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, e := s.Catalog.AssignTenantPricing(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.TenantPricingCommand{OperationID: op, TenantID: id, ExpectedRevision: rev, GroupID: group})
	if e != nil {
		s.pricingError(c, e)
		return
	}
	s.pricingResult(c, 200, v.Operation, v.Result)
}
