package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/payments"
)

// InitializeVouchers must succeed before the real main opens a listener. New
// keeps its existing signature; an uninitialized commerce surface fails closed.
func (s *Server) InitializeVouchers(ctx context.Context) error {
	v, err := billing.NewVoucherService(ctx, s.Billing, billing.VoucherOptions{
		RuntimeAllowed: s.Config.VouchersEnabled, RecoveryMode: s.Config.RecoveryMode, Keys: s.Config.VoucherKeys,
	})
	if err != nil {
		return err
	}
	s.Vouchers = v
	return nil
}

func (s *Server) registerWalletCommerce(a *gin.RouterGroup) {
	a.GET("/wallet/commerce-capabilities", s.walletCommerceCapabilities)
	a.GET("/wallet/commerce-operations/:id", s.walletCommerceOperation)
	a.GET("/wallet/funding-summary", s.walletFundingSummary)
	a.GET("/wallet/funding-receipts", s.walletFundingReceipts)
	a.GET("/wallet/voucher-redemptions/:id", s.walletVoucherRedemption)
	a.POST("/wallet/voucher-redemptions", s.redeemVoucher)
}

func (s *Server) registerPlatformCommerce(p *gin.RouterGroup) {
	p.GET("/commerce-capabilities", s.platformCommerceCapabilities)
	p.GET("/commerce-operations/:id", s.platformCommerceOperation)
	p.GET("/voucher-policy", s.voucherPolicy)
	p.PUT("/voucher-policy", s.updateVoucherPolicy)
	p.GET("/voucher-batches", s.voucherBatches)
	p.POST("/voucher-batches", s.createVoucherBatch)
	p.GET("/voucher-batches/:id", s.voucherBatch)
	p.POST("/voucher-batches/:id/approve", func(c *gin.Context) { s.voucherBatchMutation(c, "approve") })
	p.POST("/voucher-batches/:id/void", func(c *gin.Context) { s.voucherBatchMutation(c, "void") })
	p.GET("/voucher-batches/:id/revocations", s.voucherRevocations)
	p.POST("/voucher-batches/:id/revocations", func(c *gin.Context) { s.voucherBatchMutation(c, "revocation") })
	p.GET("/voucher-revocations/:id", s.voucherRevocation)
	p.POST("/voucher-revocations/:id/approve", func(c *gin.Context) { s.voucherBatchMutation(c, "revocation_approve") })
}

func (s *Server) commerceAvailable(c *gin.Context) bool {
	if s.Vouchers == nil {
		s.fail(c, 503, "commerce_unavailable", "商业服务暂不可用")
		return false
	}
	return true
}

func (s *Server) commerceReader(c *gin.Context, wallet bool) (billing.VoucherReader, bool) {
	reader := billing.VoucherReader{ActorID: sessionOf(c).User.ID}
	if wallet {
		tenant, ok := s.tenant(c)
		if !ok {
			return reader, false
		}
		if role := sessionOf(c).Tenant.Role; role != "owner" && role != "billing_viewer" {
			s.fail(c, 403, "forbidden", "需要钱包查看权限")
			return reader, false
		}
		reader.TenantID = tenant
	} else {
		switch sessionOf(c).User.PlatformRole {
		case "admin", "finance_operator", "finance_approver":
		default:
			s.fail(c, 403, "forbidden", "需要平台财务查看权限")
			return reader, false
		}
	}
	return reader, s.commerceAvailable(c)
}

func (s *Server) commerceWriter(c *gin.Context, role string) (string, bool) {
	var tenant string
	if role == "owner" {
		var ok bool
		tenant, ok = s.tenant(c)
		if !ok {
			return "", false
		}
		if sessionOf(c).Tenant.Role != "owner" {
			s.fail(c, 403, "forbidden", "需要当前租户所有者权限")
			return "", false
		}
	} else if sessionOf(c).User.PlatformRole != role {
		s.fail(c, 403, "forbidden", "需要相应平台操作权限")
		return "", false
	}
	if !s.sensitive(c) || !s.commerceAvailable(c) {
		return "", false
	}
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		s.commerceInvalid(c)
		return "", false
	}
	return tenant, true
}

func (s *Server) commercePathID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !procurementUUID(id) {
		s.commerceInvalid(c)
		return "", false
	}
	return id, true
}

func voucherCommand(c *gin.Context, operation, tenant string) billing.VoucherCommandContext {
	return billing.VoucherCommandContext{OperationID: operation, ActorID: sessionOf(c).User.ID, TenantID: tenant, RequestID: c.GetString("request_id")}
}

func (s *Server) commerceError(c *gin.Context, err error) {
	var ve *billing.VoucherError
	code, status := "commerce_unavailable", http.StatusServiceUnavailable
	if errors.As(err, &ve) {
		switch ve.Code {
		case "invalid_request", "invalid_cursor":
			code, status = ve.Code, 400
		case "forbidden":
			code, status = ve.Code, 403
		case "not_found":
			code, status = ve.Code, 404
		case "commerce_operation_conflict", "voucher_policy_conflict", "vouchers_runtime_disabled":
			code, status = ve.Code, 409
		case "voucher_key_unavailable", "commerce_unavailable", "commerce_outcome_unknown":
			code = ve.Code
		}
	}
	s.fail(c, status, code, "操作未完成，请按原操作编号查询结果")
}

// Only safe domain views enter this mapper, never input, DB rows or the
// internal create result that also carries one-time secrets.
func commerceWire(value any) any { return commerceWireValue(reflect.ValueOf(value)) }
func commerceWireValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return commerceWireValue(value.Elem())
	}
	if instant, ok := value.Interface().(time.Time); ok {
		return instant.UTC().Format("2006-01-02T15:04:05.000000Z")
	}
	switch value.Kind() {
	case reflect.Struct:
		result := map[string]any{}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if field.PkgPath != "" || name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			result[name] = commerceWireValue(value.Field(i))
		}
		return result
	case reflect.Slice, reflect.Array:
		result := make([]any, value.Len())
		for i := range result {
			result[i] = commerceWireValue(value.Index(i))
		}
		return result
	default:
		return value.Interface()
	}
}

func (s *Server) commerceReply(c *gin.Context, value any, err error) {
	if err != nil {
		s.commerceError(c, err)
		return
	}
	s.ok(c, commerceWire(value))
}

func commercePageReply[T any](s *Server, c *gin.Context, page billing.VoucherPage[T], err error) {
	if err != nil {
		s.commerceError(c, err)
		return
	}
	c.JSON(200, gin.H{"data": commerceWire(page.Data), "next_cursor": page.NextCursor, "request_id": c.GetString("request_id")})
}

func (s *Server) platformCommerceCapabilities(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.PlatformCommerceCapabilities(c.Request.Context(), reader.ActorID)
	if err == nil {
		if s.Payments == nil {
			s.paymentFailure(c, 503, "commerce_unavailable")
			return
		}
		capability, paymentErr := s.Payments.PlatformCapabilities(c.Request.Context(), reader.ActorID)
		if paymentErr != nil {
			s.paymentError(c, paymentErr, false)
			return
		}
		v.PaymentsMode = capability.Mode
	}
	s.commerceReply(c, v, err)
}
func (s *Server) walletCommerceCapabilities(c *gin.Context) {
	reader, ok := s.commerceReader(c, true)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.WalletCommerceCapabilities(c.Request.Context(), reader)
	if err == nil {
		if s.Payments == nil {
			s.paymentFailure(c, 503, "commerce_unavailable")
			return
		}
		capability, paymentErr := s.Payments.WalletCapabilities(c.Request.Context(), payments.Reader{ActorID: reader.ActorID, TenantID: reader.TenantID})
		if paymentErr != nil {
			s.paymentError(c, paymentErr, false)
			return
		}
		v.PaymentsMode = capability.Mode
	}
	s.commerceReply(c, v, err)
}
func (s *Server) voucherPolicy(c *gin.Context) {
	if !s.admin(c) || !s.commerceAvailable(c) {
		return
	}
	if _, ok := s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetVoucherPolicy(c.Request.Context(), sessionOf(c).User.ID)
	s.commerceReply(c, v, err)
}
func (s *Server) updateVoucherPolicy(c *gin.Context) {
	if _, ok := s.commerceWriter(c, "admin"); !ok {
		return
	}
	obj, ok := commerceObject(c.Request.Body, "expected_revision", "enabled")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	revision, ok := commerceInteger(obj["expected_revision"], true)
	var enabled bool
	if !ok || json.Unmarshal(obj["enabled"], &enabled) != nil {
		s.commerceInvalid(c)
		return
	}
	release, ok := s.admitCommerce(c, "")
	if !ok {
		return
	}
	defer release()
	v, err := s.Vouchers.UpdateVoucherPolicy(c.Request.Context(), billing.UpdateVoucherPolicyInput{ActorID: sessionOf(c).User.ID, RequestID: c.GetString("request_id"), ExpectedRevision: revision, Enabled: enabled})
	s.commerceReply(c, v, err)
}
func (s *Server) voucherBatches(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	query, ok := s.commerceQuery(c, "cursor")
	if !ok {
		return
	}
	v, err := s.Vouchers.ListVoucherBatches(c.Request.Context(), reader.ActorID, billing.VoucherPageInput{Cursor: query.Get("cursor")})
	commercePageReply(s, c, v, err)
}
func (s *Server) voucherBatch(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetVoucherBatch(c.Request.Context(), reader.ActorID, id)
	s.commerceReply(c, v, err)
}
func (s *Server) voucherRevocations(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	query, ok := s.commerceQuery(c, "cursor")
	if !ok {
		return
	}
	v, err := s.Vouchers.ListVoucherRevocations(c.Request.Context(), reader.ActorID, id, billing.VoucherPageInput{Cursor: query.Get("cursor")})
	commercePageReply(s, c, v, err)
}
func (s *Server) voucherRevocation(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetVoucherRevocation(c.Request.Context(), reader.ActorID, id)
	s.commerceReply(c, v, err)
}
func (s *Server) platformCommerceOperation(c *gin.Context) {
	reader, ok := s.commerceReader(c, false)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetPlatformCommerceOperation(c.Request.Context(), reader.ActorID, id)
	s.commerceReply(c, v, err)
}
func (s *Server) walletCommerceOperation(c *gin.Context) {
	reader, ok := s.commerceReader(c, true)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetWalletCommerceOperation(c.Request.Context(), reader, id)
	s.commerceReply(c, v, err)
}
func (s *Server) walletVoucherRedemption(c *gin.Context) {
	reader, ok := s.commerceReader(c, true)
	if !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetVoucherRedemption(c.Request.Context(), reader, id)
	s.commerceReply(c, v, err)
}
func (s *Server) walletFundingSummary(c *gin.Context) {
	reader, ok := s.commerceReader(c, true)
	if !ok {
		return
	}
	if _, ok = s.commerceQuery(c); !ok {
		return
	}
	v, err := s.Vouchers.GetFundingSummary(c.Request.Context(), reader)
	s.commerceReply(c, v, err)
}
func (s *Server) walletFundingReceipts(c *gin.Context) {
	reader, ok := s.commerceReader(c, true)
	if !ok {
		return
	}
	query, ok := s.commerceQuery(c, "cursor", "source_kind")
	if !ok {
		return
	}
	kind := billing.FundingSourceKind(query.Get("source_kind"))
	if kind != "" && kind != billing.FundingManualCredit && kind != billing.FundingVoucherGrant && kind != billing.FundingSandboxCapture {
		s.commerceInvalid(c)
		return
	}
	v, err := s.Vouchers.ListFundingReceipts(c.Request.Context(), reader, billing.FundingReceiptPageInput{Cursor: query.Get("cursor"), SourceKind: kind})
	commercePageReply(s, c, v, err)
}

func (s *Server) createVoucherBatch(c *gin.Context) {
	if _, ok := s.commerceWriter(c, "finance_operator"); !ok {
		return
	}
	obj, ok := commerceObject(c.Request.Body, "operation_id", "face_amount", "count", "expires_at", "purpose_ref", "reason")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	operation, idOK := commerceID(obj["operation_id"])
	amount, amountOK := commerceInteger(obj["face_amount"], true)
	count, countOK := commerceCount(obj["count"])
	expires, expiresOK := commerceExpiry(obj["expires_at"])
	purpose, purposeOK := commercePurpose(obj["purpose_ref"])
	reason, reasonOK := commerceReason(obj["reason"])
	if !idOK || !amountOK || !countOK || !expiresOK || !purposeOK || !reasonOK || !commerceProductFits(amount, count) {
		s.commerceInvalid(c)
		return
	}
	release, ok := s.admitCommerce(c, "")
	if !ok {
		return
	}
	defer release()
	v, err := s.Vouchers.CreateVoucherBatch(c.Request.Context(), billing.CreateVoucherBatchInput{Command: voucherCommand(c, operation, ""), FaceAmount: billing.Money(amount), Count: count, ExpiresAt: expires, PurposeRef: purpose, Reason: reason})
	if err != nil {
		s.commerceError(c, err)
		return
	}
	response := gin.H{"data": commerceWire(v.Receipt), "secrets_available": false, "request_id": c.GetString("request_id")}
	status := http.StatusOK
	if v.Receipt.Status == "accepted" {
		if v.Batch == nil {
			s.commerceError(c, nil)
			return
		}
		response["batch"] = commerceWire(v.Batch)
		if v.Created && v.SecretsAvailable && len(v.Codes) == v.Batch.Count {
			status = http.StatusCreated
			response["secrets_available"] = true
			response["codes"] = v.Codes
		}
	}
	c.JSON(status, response)
}

func (s *Server) voucherBatchMutation(c *gin.Context, action string) {
	role := "finance_operator"
	if action == "approve" || action == "revocation_approve" {
		role = "finance_approver"
	}
	if _, ok := s.commerceWriter(c, role); !ok {
		return
	}
	id, ok := s.commercePathID(c)
	if !ok {
		return
	}
	fields := []string{"operation_id", "expected_revision"}
	switch action {
	case "void":
		fields = append(fields, "reason")
	case "revocation":
		fields = append(fields, "expected_redeemed_count", "reason")
	case "revocation_approve":
		fields = []string{"operation_id"}
	}
	obj, ok := commerceObject(c.Request.Body, fields...)
	if !ok {
		s.commerceInvalid(c)
		return
	}
	operation, ok := commerceID(obj["operation_id"])
	if !ok {
		s.commerceInvalid(c)
		return
	}
	var revision, redeemed int64
	var reason string
	if action != "revocation_approve" {
		revision, ok = commerceInteger(obj["expected_revision"], true)
		if !ok {
			s.commerceInvalid(c)
			return
		}
	}
	if action == "void" || action == "revocation" {
		reason, ok = commerceReason(obj["reason"])
		if !ok {
			s.commerceInvalid(c)
			return
		}
	}
	if action == "revocation" {
		redeemed, ok = commerceInteger(obj["expected_redeemed_count"], false)
		if !ok {
			s.commerceInvalid(c)
			return
		}
	}
	release, ok := s.admitCommerce(c, "")
	if !ok {
		return
	}
	defer release()
	command := voucherCommand(c, operation, "")
	var receipt billing.VoucherOperationReceipt
	var err error
	switch action {
	case "approve":
		receipt, err = s.Vouchers.ApproveVoucherBatch(c.Request.Context(), billing.ApproveVoucherBatchInput{Command: command, BatchID: id, ExpectedRevision: revision})
	case "void":
		receipt, err = s.Vouchers.VoidVoucherBatch(c.Request.Context(), billing.VoidVoucherBatchInput{Command: command, BatchID: id, ExpectedRevision: revision, Reason: reason})
	case "revocation":
		receipt, err = s.Vouchers.ProposeVoucherRevocation(c.Request.Context(), billing.ProposeVoucherRevocationInput{Command: command, BatchID: id, ExpectedRevision: revision, ExpectedRedeemedCount: redeemed, Reason: reason})
	case "revocation_approve":
		receipt, err = s.Vouchers.ApproveVoucherRevocation(c.Request.Context(), billing.ApproveVoucherRevocationInput{Command: command, ProposalID: id})
	}
	s.commerceReply(c, receipt, err)
}

func (s *Server) redeemVoucher(c *gin.Context) {
	tenant, ok := s.commerceWriter(c, "owner")
	if !ok {
		return
	}
	obj, ok := commerceObject(c.Request.Body, "operation_id", "code")
	if !ok {
		s.commerceInvalid(c)
		return
	}
	operation, idOK := commerceID(obj["operation_id"])
	code, codeOK := commerceCode(obj["code"])
	if !idOK || !codeOK {
		s.commerceInvalid(c)
		return
	}
	release, ok := s.admitCommerce(c, tenant)
	if !ok {
		return
	}
	defer release()
	v, err := s.Vouchers.RedeemVoucher(c.Request.Context(), billing.RedeemVoucherInput{Command: voucherCommand(c, operation, tenant), Code: code})
	s.commerceReply(c, v, err)
}
