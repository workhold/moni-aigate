package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

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
	p.POST("/price-groups/:id/price-versions/compile", s.compileGroupPrices)
	p.POST("/price-groups/:id/price-versions/compile-preview", s.previewGroupPriceCompile)
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
		case "base_price_changed", "base_price_missing":
			code, status = pe.Code, 409
		case "pricing_busy", "pricing_invariant":
			code = pe.Code
		}
		if pe.Code == "base_price_missing" {
			c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": "部分模型没有当前公共默认价", "missing_model_ids": pe.MissingModelIDs}, "request_id": c.GetString("request_id")})
			return
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
	obj, ok := pricingBody(c, "operation_id", "model_id", "schema_id", "schema_digest", "variant_key", "input_rate", "output_rate", "effective_at")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	model, b := commerceID(obj["model_id"])
	schema, schemaOK := commerceID(obj["schema_id"])
	digest, digestOK := commerceString(obj["schema_digest"])
	variant, variantOK := commerceString(obj["variant_key"])
	in, d := commerceInteger(obj["input_rate"], false)
	out, e := commerceInteger(obj["output_rate"], false)
	at, f := commerceExpiry(obj["effective_at"])
	if !a || !b || !schemaOK || !digestOK || !variantOK || !d || !e || !f || at.IsZero() || at.Year() < 1 || at.Year() > 9999 || at.Nanosecond()%1000 != 0 {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.PublishGroupPrice(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.GroupPriceCommand{OperationID: op, GroupID: id, ModelID: model, SchemaID: schema, SchemaDigest: digest, VariantKey: variant, InputRate: in, OutputRate: out, EffectiveAt: at.UTC()})
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

func (s *Server) compileGroupPriceBody(c *gin.Context) (catalog.GroupPriceCompileCommand, bool) {
	var out catalog.GroupPriceCompileCommand
	obj, ok := pricingBodyOptional(c, []string{"operation_id", "multiplier", "model_ids", "effective_at"}, "expected_base_version_ids")
	if !ok {
		return out, false
	}
	op, a := commerceID(obj["operation_id"])
	multiplier, b := commerceString(obj["multiplier"])
	at, cOK := commerceExpiry(obj["effective_at"])
	var rawIDs []json.RawMessage
	if json.Unmarshal(obj["model_ids"], &rawIDs) != nil || len(rawIDs) < 1 || len(rawIDs) > 100 {
		return out, false
	}
	ids := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, valid := commerceID(raw)
		if !valid {
			return out, false
		}
		ids = append(ids, id)
	}
	var expected map[string]json.RawMessage
	var versions map[string]string
	if raw, present := obj["expected_base_version_ids"]; present {
		var valid bool
		if string(raw) != "null" {
			expected, valid = strictPricingRawObject(raw)
		}
		if !valid || expected == nil {
			return out, false
		}
		versions = make(map[string]string, len(expected))
	}
	if expected != nil {
		for model, raw := range expected {
			version, valid := commerceID(raw)
			if !valid {
				return out, false
			}
			versions[model] = version
		}
	}
	sort.Strings(ids)
	return catalog.GroupPriceCompileCommand{OperationID: op, GroupID: c.Param("id"), Multiplier: multiplier, ModelIDs: ids, EffectiveAt: at.UTC(), ExpectedBaseVersionIDs: versions}, a && b && cOK
}

func (s *Server) compileGroupPrices(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	command, ok := s.compileGroupPriceBody(c)
	if !ok {
		s.commerceInvalid(c)
		return
	}
	command.GroupID = id
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.CompileGroupPrices(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, command)
	if err != nil {
		s.pricingError(c, err)
		return
	}
	status := http.StatusCreated
	if v.Replayed {
		status = http.StatusOK
	}
	s.pricingResult(c, status, v.Operation, v.Result)
}

func (s *Server) previewGroupPriceCompile(c *gin.Context) {
	if !s.pricingReader(c) {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	command, ok := s.compileGroupPriceBody(c)
	if !ok {
		s.commerceInvalid(c)
		return
	}
	command.GroupID = id
	done, ok := s.admitPricing(c, "preview", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.PreviewGroupPriceCompile(c.Request.Context(), sessionOf(c).User.ID, command)
	if err != nil {
		s.pricingError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	s.ok(c, v)
}
func (s *Server) publishDefaultPrice(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "model_id", "schema_id", "schema_digest", "variant_key", "input_rate", "output_rate", "effective_at")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	model, b := commerceID(obj["model_id"])
	schema, d := commerceID(obj["schema_id"])
	digest, e := commerceString(obj["schema_digest"])
	variant, f := commerceString(obj["variant_key"])
	in, g := commerceInteger(obj["input_rate"], false)
	out, h := commerceInteger(obj["output_rate"], false)
	at, i := commerceExpiry(obj["effective_at"])
	if !a || !b || !d || !e || !f || !g || !h || !i || variant != "default" || at.IsZero() || at.Nanosecond()%1000 != 0 {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.PublishDefaultPrice(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.DefaultPriceCommand{OperationID: op, ModelID: model, SchemaID: schema, SchemaDigest: digest, VariantKey: variant, InputRate: in, OutputRate: out, EffectiveAt: at.UTC()})
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
func (s *Server) publishCostPrice(c *gin.Context) {
	if !s.pricingWriter(c) {
		return
	}
	obj, ok := pricingBody(c, "operation_id", "channel_id", "upstream_model", "schema_id", "schema_digest", "variant_key", "basis", "input_rate", "output_rate", "effective_at", "expected_channel_revision")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	op, a := commerceID(obj["operation_id"])
	channel, b := commerceID(obj["channel_id"])
	upstream, d := commerceString(obj["upstream_model"])
	schema, e := commerceID(obj["schema_id"])
	digest, f := commerceString(obj["schema_digest"])
	variant, g := commerceString(obj["variant_key"])
	basis, h := commerceString(obj["basis"])
	rev, j := commerceInteger(obj["expected_channel_revision"], true)
	at, k := commerceExpiry(obj["effective_at"])
	var in, out *int64
	parse := func(raw []byte) (*int64, bool) {
		if string(raw) == "null" {
			return nil, true
		}
		n, ok := commerceInteger(raw, false)
		return &n, ok
	}
	in, l := parse(obj["input_rate"])
	out, m := parse(obj["output_rate"])
	if !a || !b || !d || !e || !f || !g || !h || !j || !k || !l || !m || variant != "default" || !((basis == "unknown" && in == nil && out == nil) || (basis != "unknown" && in != nil && out != nil)) || at.IsZero() || at.Nanosecond()%1000 != 0 {
		s.commerceInvalid(c)
		return
	}
	done, ok := s.admitPricing(c, "write", "")
	if !ok {
		return
	}
	defer done()
	v, err := s.Catalog.PublishCostPrice(c.Request.Context(), catalog.PricingActor{UserID: sessionOf(c).User.ID}, catalog.CostPriceCommand{OperationID: op, ChannelID: channel, UpstreamModel: upstream, SchemaID: schema, SchemaDigest: digest, VariantKey: variant, Basis: basis, InputRate: in, OutputRate: out, EffectiveAt: at.UTC(), ExpectedChannelRevision: rev})
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
