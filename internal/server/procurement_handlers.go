package server

import (
	"bytes"
	"encoding/base64"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// Procurement belongs to the platform. Tenant membership never grants access
// to supplier account identities, costs, observations, or evidence references.
func (s *Server) procurementReader(c *gin.Context) bool {
	switch sessionOf(c).User.PlatformRole {
	case "admin", "finance_operator", "finance_approver":
		return true
	default:
		s.fail(c, 403, "forbidden", "需要平台采购或财务查看权限")
		return false
	}
}

func procurementUUID(id string) bool {
	v, err := uuid.Parse(id)
	return err == nil && v != uuid.Nil && v.String() == id
}

func procurementDecimal(value string, positive bool) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == value
}

func (s *Server) procurementID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !procurementUUID(id) {
		s.fail(c, 400, "invalid_procurement_id", "采购资源ID无效")
		return "", false
	}
	return id, true
}

// A cursor is an opaque, fixed ordering boundary, not a client supplied SQL
// value. Reject ambiguous encodings before the shared bounded paginator.
func (s *Server) procurementPage(c *gin.Context, sql string, args ...any) {
	values, present := c.Request.URL.Query()["cursor"]
	if present {
		valid := len(values) == 1 && values[0] != "" && len(values[0]) <= 512
		if valid {
			raw, err := base64.RawURLEncoding.DecodeString(values[0])
			var boundary cursor
			valid = err == nil && base64.RawURLEncoding.EncodeToString(raw) == values[0] &&
				decodeCanonicalJSON(bytes.NewReader(raw), &boundary, 384, "t", "id") &&
				!boundary.CreatedAt.IsZero() && procurementUUID(boundary.ID)
		}
		if !valid {
			s.fail(c, 400, "invalid_cursor", "分页游标无效")
			return
		}
	}
	s.page(c, sql, args...)
}

func (s *Server) procurementFilter(c *gin.Context, name string) (string, bool) {
	values, present := c.Request.URL.Query()[name]
	if !present {
		return "", true
	}
	if len(values) != 1 || !procurementUUID(values[0]) {
		s.fail(c, 400, "invalid_procurement_filter", "采购筛选ID无效")
		return "", false
	}
	return values[0], true
}

func decodeProcurementCreate(r io.Reader, in *procurement.CreateInput) bool {
	return decodeCanonicalJSON(r, in, 16384, "operation_id", "label", "provider", "state", "limit_amount") &&
		procurementUUID(in.OperationID) && procurementDecimal(in.LimitAmount, false)
}

func decodeProcurementConfiguration(r io.Reader, in *procurement.ConfigurationInput) bool {
	return decodeCanonicalJSON(r, in, 16384, "operation_id", "expected_revision", "label", "provider", "state", "limit_amount") &&
		procurementUUID(in.OperationID) && procurementDecimal(in.ExpectedRevision, true) && procurementDecimal(in.LimitAmount, false)
}

func decodeProcurementBinding(r io.Reader, in *procurement.BindingInput) bool {
	return decodeCanonicalJSON(r, in, 16384, "operation_id", "channel_id", "credential_id", "account_id", "expected_channel_revision") &&
		procurementUUID(in.OperationID) && procurementUUID(in.ChannelID) && procurementUUID(in.CredentialID) &&
		procurementUUID(in.AccountID) && procurementDecimal(in.ExpectedChannelRevision, true)
}

func decodeProcurementObservation(r io.Reader, in *procurement.ObservationInput) bool {
	return decodeCanonicalJSON(r, in, 16384, "operation_id", "status", "amount", "currency", "source", "semantic", "observed_at", "provider_as_of", "evidence_ref") &&
		procurementUUID(in.OperationID) && (in.Amount == nil || procurementDecimal(*in.Amount, false))
}

func (s *Server) procurementAccounts(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	s.procurementPage(c, procurement.AccountListSQL())
}

func (s *Server) createProcurementAccount(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in procurement.CreateInput
	if !decodeProcurementCreate(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "采购账户参数无效")
		return
	}
	v, err := s.Procurement.CreateAccount(c.Request.Context(), sessionOf(c).User.ID, in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) procurementConfiguration(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	v, err := s.Procurement.Account(c.Request.Context(), id)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) configureProcurementAccount(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	var in procurement.ConfigurationInput
	if !decodeProcurementConfiguration(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "采购账户完整配置与版本无效")
		return
	}
	v, err := s.Procurement.ConfigureAccount(c.Request.Context(), sessionOf(c).User.ID, id, in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) procurementBindings(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	account, ok := s.procurementFilter(c, "account_id")
	if !ok {
		return
	}
	channel, ok := s.procurementFilter(c, "channel_id")
	if !ok {
		return
	}
	s.procurementPage(c, `SELECT * FROM (`+procurement.BindingListSQL()+`) bindings WHERE ($1='' OR account_id=$1) AND ($2='' OR channel_id=$2)`, account, channel)
}

func (s *Server) bindProcurementCredential(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	var in procurement.BindingInput
	if !decodeProcurementBinding(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "采购绑定身份与渠道版本无效")
		return
	}
	v, err := s.Procurement.BindCredential(c.Request.Context(), sessionOf(c).User.ID, in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) procurementOperation(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	v, err := s.Procurement.Command(c.Request.Context(), id)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) procurementEvents(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	s.procurementPage(c, procurement.EventsListSQL(), id)
}

func (s *Server) procurementReservations(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	s.procurementPage(c, procurement.ReservationsListSQL(), id)
}

func (s *Server) procurementObservations(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	s.procurementPage(c, procurement.ObservationListSQL(), id)
}

func (s *Server) observeProcurementBalance(c *gin.Context) {
	if !s.admin(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	var in procurement.ObservationInput
	if !decodeProcurementObservation(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "人工余额证据参数无效")
		return
	}
	v, err := s.Procurement.AddObservation(c.Request.Context(), sessionOf(c).User.ID, id, in)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

type procurementProposalBody struct {
	OperationID string  `json:"operation_id"`
	TenantID    string  `json:"tenant_id"`
	AttemptID   string  `json:"attempt_id"`
	Amount      *string `json:"amount"`
	Basis       string  `json:"basis"`
	EvidenceRef string  `json:"evidence_ref"`
	Reason      string  `json:"reason"`
}

func decodeProcurementProposal(r io.Reader, in *procurementProposalBody) bool {
	return decodeCanonicalJSON(r, in, 16384, "operation_id", "tenant_id", "attempt_id", "amount", "basis", "evidence_ref", "reason") &&
		procurementUUID(in.OperationID) && procurementUUID(in.TenantID) && procurementUUID(in.AttemptID) &&
		in.Amount != nil && procurementDecimal(*in.Amount, false)
}

func (s *Server) proposeProcurementCost(c *gin.Context) {
	if !s.financial(c, false) {
		return
	}
	var in procurementProposalBody
	if !decodeProcurementProposal(c.Request.Body, &in) {
		s.fail(c, 400, "invalid_request", "采购成本提案参数无效")
		return
	}
	amount, _ := strconv.ParseInt(*in.Amount, 10, 64)
	v, err := s.Billing.ProposeProcurementCost(c.Request.Context(), billing.ProcurementCostProposalInput{OperationID: in.OperationID, TenantID: in.TenantID, AttemptID: in.AttemptID, ActorID: sessionOf(c).User.ID, Amount: billing.Money(amount), Basis: in.Basis, EvidenceRef: in.EvidenceRef, Reason: in.Reason})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) procurementCostProposals(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	operation, ok := s.procurementFilter(c, "operation_id")
	if !ok {
		return
	}
	s.procurementPage(c, `SELECT id,tenant_id,attempt_id,amount::text AS amount,basis,evidence_ref,reason,requester,state,approver,approval_operation_id,expected_reservation_revision::text AS expected_revision,expected_evidence_count::text AS expected_evidence_count,created_at,approved_at FROM procurement_cost_proposals WHERE ($1='' OR id=$1)`, operation)
}

func (s *Server) procurementCostProposal(c *gin.Context) {
	if !s.procurementReader(c) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	v, err := s.Billing.GetProcurementCostProposal(c.Request.Context(), id)
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}

func (s *Server) approveProcurementCost(c *gin.Context) {
	if !s.financial(c, true) {
		return
	}
	id, ok := s.procurementID(c)
	if !ok {
		return
	}
	var in struct {
		OperationID string `json:"operation_id"`
	}
	if !decodeCanonicalJSON(c.Request.Body, &in, 16384, "operation_id") || !procurementUUID(in.OperationID) {
		s.fail(c, 400, "invalid_request", "采购成本审批操作身份无效")
		return
	}
	v, err := s.Billing.ApproveProcurementCost(c.Request.Context(), billing.ProcurementCostApprovalInput{ProposalID: id, OperationID: in.OperationID, ActorID: sessionOf(c).User.ID})
	if err != nil {
		s.error(c, err)
		return
	}
	s.ok(c, v)
}
