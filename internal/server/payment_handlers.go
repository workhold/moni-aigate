package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/internal/payments"
)

type paymentService interface {
	SandboxNotificationsEnabled() bool
	PublishSKU(context.Context, payments.PublishSKUInput) (payments.PublishSKUResult, error)
	ActivateSKU(context.Context, payments.ActivateSKUInput) (payments.OperationReceipt, error)
	CreateOrder(context.Context, payments.CreateOrderInput) (payments.OrderResult, error)
	CancelOrder(context.Context, payments.CancelOrderInput) (payments.OperationReceipt, error)
	GetPolicy(context.Context, string) (payments.PolicyView, error)
	UpdatePolicy(context.Context, payments.UpdatePolicyInput) (payments.PolicyView, error)
	PlatformCapabilities(context.Context, string) (payments.PaymentCapabilitiesView, error)
	WalletCapabilities(context.Context, payments.Reader) (payments.PaymentCapabilitiesView, error)
	ListPlatformSKUs(context.Context, string, payments.Page) (payments.PageResult[payments.PlatformSKUView], error)
	GetPlatformSKU(context.Context, string, string) (payments.PlatformSKUView, error)
	ListWalletSKUs(context.Context, payments.Reader, payments.Page) (payments.PageResult[payments.SKUVersionView], error)
	GetPlatformOrder(context.Context, string, string) (payments.OrderView, error)
	GetWalletOrder(context.Context, payments.Reader, string) (payments.OrderView, error)
	ListPlatformOrders(context.Context, string, payments.Page) (payments.PageResult[payments.OrderView], error)
	ListWalletOrders(context.Context, payments.Reader, payments.Page) (payments.PageResult[payments.OrderView], error)
	ListPlatformObservations(context.Context, string, string, payments.Page) (payments.PageResult[payments.ObservationView], error)
	ReceiveNotification(context.Context, payments.NotificationInput) (payments.NotificationACK, bool, error)
}

func (s *Server) InitializePayments(ctx context.Context, options payments.Options) error {
	s.Payments = nil
	service, e := payments.NewService(ctx, s.Pool, options)
	if e != nil {
		return e
	}
	s.Payments = service
	return nil
}
func (s *Server) registerPayments(a, p *gin.RouterGroup) {
	p.GET("/payment-policy", s.paymentPolicy)
	p.PUT("/payment-policy", s.paymentPolicyUpdate)
	p.GET("/payment-capabilities", s.paymentCapabilities(false))
	p.GET("/payment-skus", s.paymentSKUs(false))
	p.POST("/payment-skus", s.paymentPublishSKU)
	p.GET("/payment-skus/:id", s.paymentSKU)
	p.PUT("/payment-skus/:id/active", s.paymentActivateSKU)
	p.GET("/payment-orders", s.paymentOrders(false))
	p.GET("/payment-orders/:id", s.paymentOrder(false))
	p.GET("/payment-orders/:id/observations", s.paymentObservations)
	a.GET("/wallet/payment-capabilities", s.paymentCapabilities(true))
	a.GET("/wallet/payment-skus", s.paymentSKUs(true))
	a.GET("/wallet/payment-orders", s.paymentOrders(true))
	a.POST("/wallet/payment-orders", s.paymentCreateOrder)
	a.GET("/wallet/payment-orders/:id", s.paymentOrder(true))
	a.POST("/wallet/payment-orders/:id/cancel", s.paymentCancelOrder)
}
func (s *Server) registerPaymentNotifications(r *gin.Engine) {
	if s.Payments != nil && s.Payments.SandboxNotificationsEnabled() {
		r.POST("/api/payments/sandbox/notifications", s.paymentNotification)
	}
}
func (s *Server) paymentFailure(c *gin.Context, status int, code string) {
	c.Header("Cache-Control", "no-store")
	s.fail(c, status, code, "支付操作未完成；结果不明时请保留原操作编号核查")
}
func (s *Server) paymentError(c *gin.Context, err error, notification bool) {
	code, status := "commerce_unavailable", 503
	if notification {
		code = "payment_unavailable"
	}
	if errors.Is(err, payments.ErrPaymentsOutcomeUnknown) {
		if notification {
			code = "payment_outcome_unknown"
		} else {
			code = "commerce_outcome_unknown"
		}
	}
	if errors.Is(err, payments.ErrPaymentsGuard) {
		code = "instance_unavailable"
	}
	if errors.Is(err, payments.ErrPaymentsRecovery) {
		code = "recovery_mode"
	}
	var pe *payments.PaymentError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "invalid_request", "invalid_cursor":
			code, status = pe.Code, 400
		case "forbidden":
			code, status = pe.Code, 403
		case "not_found":
			code, status = pe.Code, 404
		case "commerce_operation_conflict", "payment_policy_conflict", "payments_runtime_disabled":
			code, status = pe.Code, 409
		case "payment_signature_invalid":
			if notification {
				code, status = pe.Code, 401
			}
		case "payment_environment_invalid":
			if notification {
				code, status = pe.Code, 400
			}
		case "payment_event_conflict":
			if notification {
				code, status = pe.Code, 409
			}
		case "payment_key_unavailable":
			if notification {
				code = pe.Code
			}
		}
	}
	s.paymentFailure(c, status, code)
}

type paymentResponseBuffer struct {
	bytes.Buffer
	limit int
}

func (b *paymentResponseBuffer) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.Len() {
		return 0, payments.ErrPaymentsIntegrity
	}
	return b.Buffer.Write(raw)
}
func (s *Server) paymentJSON(c *gin.Context, status int, value any, notification bool) {
	limit := 128 << 10
	if notification {
		limit = 8192
	}
	buffer := paymentResponseBuffer{limit: limit}
	if e := json.NewEncoder(&buffer).Encode(value); e != nil || c.Request.Context().Err() != nil {
		// A transport/encoding failure after a mutating call cannot be reclassified
		// into a precommit denial or an ACK; clients retain the original operation.
		s.paymentError(c, payments.ErrPaymentsOutcomeUnknown, notification)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Data(status, "application/json; charset=utf-8", buffer.Bytes())
}
func (s *Server) paymentReply(c *gin.Context, value any, err error) {
	if err != nil {
		s.paymentError(c, err, false)
		return
	}
	s.paymentJSON(c, 200, gin.H{"data": commerceWire(value), "request_id": c.GetString("request_id")}, false)
}
func paymentPageReply[T any](s *Server, c *gin.Context, page payments.PageResult[T], err error) {
	if err != nil {
		s.paymentError(c, err, false)
		return
	}
	s.paymentJSON(c, 200, gin.H{"data": commerceWire(page.Data), "next_cursor": page.NextCursor, "request_id": c.GetString("request_id")}, false)
}
func (s *Server) paymentReader(c *gin.Context, wallet, admin bool) (payments.Reader, bool) {
	reader := payments.Reader{ActorID: sessionOf(c).User.ID}
	if wallet {
		tenant, ok := s.tenant(c)
		if !ok {
			return reader, false
		}
		reader.TenantID = tenant
		role := sessionOf(c).Tenant.Role
		if role != "owner" && role != "billing_viewer" {
			s.paymentFailure(c, 403, "forbidden")
			return reader, false
		}
	} else {
		role := sessionOf(c).User.PlatformRole
		if admin && role != "admin" || !admin && role != "admin" && role != "finance_operator" && role != "finance_approver" {
			s.paymentFailure(c, 403, "forbidden")
			return reader, false
		}
	}
	if s.Payments == nil {
		s.paymentError(c, payments.ErrPaymentsUnavailable, false)
		return reader, false
	}
	return reader, true
}
func (s *Server) paymentWriter(c *gin.Context, wallet bool) (string, bool) {
	reader, ok := s.paymentReader(c, wallet, !wallet)
	if !ok {
		return "", false
	}
	if wallet && sessionOf(c).Tenant.Role != "owner" {
		s.paymentFailure(c, 403, "forbidden")
		return "", false
	}
	if !s.sensitive(c) {
		return "", false
	}
	return reader.TenantID, true
}
func paymentCommandMeta(c *gin.Context, operation, tenant string) payments.CommandMeta {
	return payments.CommandMeta{OperationID: operation, ActorID: sessionOf(c).User.ID, TenantID: tenant, RequestID: c.GetString("request_id")}
}
func (s *Server) paymentPolicy(c *gin.Context) {
	reader, ok := s.paymentReader(c, false, true)
	if !ok {
		return
	}
	if _, ok = s.paymentQuery(c, false); !ok {
		return
	}
	v, e := s.Payments.GetPolicy(c.Request.Context(), reader.ActorID)
	s.paymentReply(c, v, e)
}
func (s *Server) paymentCapabilities(wallet bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		reader, ok := s.paymentReader(c, wallet, false)
		if !ok {
			return
		}
		if _, ok = s.paymentQuery(c, false); !ok {
			return
		}
		var v payments.PaymentCapabilitiesView
		var e error
		if wallet {
			v, e = s.Payments.WalletCapabilities(c.Request.Context(), reader)
		} else {
			v, e = s.Payments.PlatformCapabilities(c.Request.Context(), reader.ActorID)
		}
		s.paymentReply(c, v, e)
	}
}
func (s *Server) paymentSKUs(wallet bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		reader, ok := s.paymentReader(c, wallet, false)
		if !ok {
			return
		}
		cursor, ok := s.paymentQuery(c, true)
		if !ok {
			return
		}
		page := payments.Page{Cursor: cursor}
		if wallet {
			v, e := s.Payments.ListWalletSKUs(c.Request.Context(), reader, page)
			paymentPageReply(s, c, v, e)
		} else {
			v, e := s.Payments.ListPlatformSKUs(c.Request.Context(), reader.ActorID, page)
			paymentPageReply(s, c, v, e)
		}
	}
}
func (s *Server) paymentSKU(c *gin.Context) {
	reader, ok := s.paymentReader(c, false, false)
	if !ok {
		return
	}
	id, ok := s.paymentPathID(c)
	if !ok {
		return
	}
	if _, ok = s.paymentQuery(c, false); !ok {
		return
	}
	v, e := s.Payments.GetPlatformSKU(c.Request.Context(), reader.ActorID, id)
	s.paymentReply(c, v, e)
}
func (s *Server) paymentOrders(wallet bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		reader, ok := s.paymentReader(c, wallet, false)
		if !ok {
			return
		}
		cursor, ok := s.paymentQuery(c, true)
		if !ok {
			return
		}
		page := payments.Page{Cursor: cursor}
		if wallet {
			v, e := s.Payments.ListWalletOrders(c.Request.Context(), reader, page)
			paymentPageReply(s, c, v, e)
		} else {
			v, e := s.Payments.ListPlatformOrders(c.Request.Context(), reader.ActorID, page)
			paymentPageReply(s, c, v, e)
		}
	}
}
func (s *Server) paymentOrder(wallet bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		reader, ok := s.paymentReader(c, wallet, false)
		if !ok {
			return
		}
		id, ok := s.paymentPathID(c)
		if !ok {
			return
		}
		if _, ok = s.paymentQuery(c, false); !ok {
			return
		}
		var v payments.OrderView
		var e error
		if wallet {
			v, e = s.Payments.GetWalletOrder(c.Request.Context(), reader, id)
		} else {
			v, e = s.Payments.GetPlatformOrder(c.Request.Context(), reader.ActorID, id)
		}
		s.paymentReply(c, v, e)
	}
}
func (s *Server) paymentObservations(c *gin.Context) {
	reader, ok := s.paymentReader(c, false, false)
	if !ok {
		return
	}
	id, ok := s.paymentPathID(c)
	if !ok {
		return
	}
	cursor, ok := s.paymentQuery(c, true)
	if !ok {
		return
	}
	v, e := s.Payments.ListPlatformObservations(c.Request.Context(), reader.ActorID, id, payments.Page{Cursor: cursor})
	paymentPageReply(s, c, v, e)
}
func (s *Server) paymentPublishSKU(c *gin.Context) {
	if _, ok := s.paymentWriter(c, false); !ok {
		return
	}
	done, ok := s.admitPayment(c, "")
	if !ok {
		return
	}
	defer done()
	object, ok := s.paymentObject(c, "", "operation_id", "sku_id", "expected_revision", "display_name", "amount")
	if !ok {
		return
	}
	operation, opOK := commerceID(object["operation_id"])
	sku, skuOK := commerceID(object["sku_id"])
	revision, revOK := commerceInteger(object["expected_revision"], false)
	name, nameOK := paymentName(object["display_name"])
	amount, amountOK := commerceInteger(object["amount"], true)
	if !opOK || !skuOK || !revOK || !nameOK || !amountOK {
		s.paymentFailure(c, 400, "invalid_request")
		return
	}
	v, e := s.Payments.PublishSKU(c.Request.Context(), payments.PublishSKUInput{CommandMeta: paymentCommandMeta(c, operation, ""), SKUID: sku, ExpectedRevision: revision, DisplayName: name, Amount: billing.FiatMoney(amount)})
	if e != nil {
		s.paymentError(c, e, false)
		return
	}
	status := 200
	if v.Created && v.Receipt.Status == "accepted" {
		status = 201
	}
	s.paymentJSON(c, status, gin.H{"data": commerceWire(v.Receipt), "request_id": c.GetString("request_id")}, false)
}
func (s *Server) paymentActivateSKU(c *gin.Context) {
	if _, ok := s.paymentWriter(c, false); !ok {
		return
	}
	done, ok := s.admitPayment(c, "")
	if !ok {
		return
	}
	defer done()
	sku, ok := s.paymentPathID(c)
	if !ok {
		return
	}
	object, ok := s.paymentObject(c, "version_id", "operation_id", "expected_revision", "version_id", "enabled")
	if !ok {
		return
	}
	operation, opOK := commerceID(object["operation_id"])
	revision, revOK := commerceInteger(object["expected_revision"], true)
	version, versionOK := paymentNullableID(object["version_id"])
	enabled, enabledOK := paymentBool(object["enabled"])
	if !opOK || !revOK || !versionOK || !enabledOK {
		s.paymentFailure(c, 400, "invalid_request")
		return
	}
	v, e := s.Payments.ActivateSKU(c.Request.Context(), payments.ActivateSKUInput{CommandMeta: paymentCommandMeta(c, operation, ""), SKUID: sku, ExpectedRevision: revision, VersionID: version, Enabled: enabled})
	s.paymentReply(c, v, e)
}
func (s *Server) paymentCreateOrder(c *gin.Context) {
	tenant, ok := s.paymentWriter(c, true)
	if !ok {
		return
	}
	done, ok := s.admitPayment(c, tenant)
	if !ok {
		return
	}
	defer done()
	object, ok := s.paymentObject(c, "", "operation_id", "sku_version_id")
	if !ok {
		return
	}
	operation, opOK := commerceID(object["operation_id"])
	version, versionOK := commerceID(object["sku_version_id"])
	if !opOK || !versionOK {
		s.paymentFailure(c, 400, "invalid_request")
		return
	}
	v, e := s.Payments.CreateOrder(c.Request.Context(), payments.CreateOrderInput{CommandMeta: paymentCommandMeta(c, operation, tenant), SKUVersionID: version})
	if e != nil {
		s.paymentError(c, e, false)
		return
	}
	response := gin.H{"data": commerceWire(v.Receipt), "request_id": c.GetString("request_id")}
	status := 200
	if v.Receipt.Status == "accepted" {
		if v.Order == nil {
			s.paymentError(c, payments.ErrPaymentsOutcomeUnknown, false)
			return
		}
		response["order"] = commerceWire(v.Order)
		if v.Created {
			status = 202
		}
	}
	s.paymentJSON(c, status, response, false)
}
func (s *Server) paymentCancelOrder(c *gin.Context) {
	tenant, ok := s.paymentWriter(c, true)
	if !ok {
		return
	}
	done, ok := s.admitPayment(c, tenant)
	if !ok {
		return
	}
	defer done()
	order, ok := s.paymentPathID(c)
	if !ok {
		return
	}
	object, ok := s.paymentObject(c, "", "operation_id")
	if !ok {
		return
	}
	operation, opOK := commerceID(object["operation_id"])
	if !opOK {
		s.paymentFailure(c, 400, "invalid_request")
		return
	}
	v, e := s.Payments.CancelOrder(c.Request.Context(), payments.CancelOrderInput{CommandMeta: paymentCommandMeta(c, operation, tenant), OrderID: order})
	s.paymentReply(c, v, e)
}
func (s *Server) paymentPolicyUpdate(c *gin.Context) {
	if _, ok := s.paymentWriter(c, false); !ok {
		return
	}
	done, ok := s.admitPayment(c, "")
	if !ok {
		return
	}
	defer done()
	object, ok := s.paymentObject(c, "", "expected_revision", "enabled")
	if !ok {
		return
	}
	revision, revOK := commerceInteger(object["expected_revision"], true)
	enabled, enabledOK := paymentBool(object["enabled"])
	if !revOK || !enabledOK {
		s.paymentFailure(c, 400, "invalid_request")
		return
	}
	v, e := s.Payments.UpdatePolicy(c.Request.Context(), payments.UpdatePolicyInput{ActorID: sessionOf(c).User.ID, RequestID: c.GetString("request_id"), ExpectedRevision: revision, Enabled: enabled})
	s.paymentReply(c, v, e)
}
func (s *Server) paymentNotification(c *gin.Context) {
	done, ok := s.admitPaymentNotification(c)
	if !ok {
		return
	}
	defer done()
	raw, ok := s.paymentBody(c, 65536)
	if !ok {
		return
	}
	headers, e := p.ReadSignedHeaders(c.Request.Header)
	if e != nil {
		s.paymentFailure(c, 401, "payment_signature_invalid")
		return
	}
	if s.Payments == nil {
		s.paymentError(c, payments.ErrPaymentsUnavailable, true)
		return
	}
	ack, first, e := s.Payments.ReceiveNotification(c.Request.Context(), payments.NotificationInput{Headers: headers, Body: raw})
	if e != nil {
		s.paymentError(c, e, true)
		return
	}
	if ack.Status != "received" || ack.Environment != "sandbox" || !procurementUUID(ack.ReceiptID) || ack.EventID == "" || ack.ReceivedAt.IsZero() {
		s.paymentError(c, payments.ErrPaymentsOutcomeUnknown, true)
		return
	}
	status := 200
	if first {
		status = 202
	}
	s.paymentJSON(c, status, gin.H{"data": gin.H{"receipt_id": ack.ReceiptID, "event_id": ack.EventID, "status": ack.Status, "environment": ack.Environment, "received_at": ack.ReceivedAt.UTC().Format("2006-01-02T15:04:05.000000Z")}, "request_id": c.GetString("request_id")}, true)
}
