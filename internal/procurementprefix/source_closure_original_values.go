package procurementprefix

import (
	"math/big"
	"strings"
	"unicode"
)

// originalValueRows checks only values available in the fixed original
// projection. It does not authenticate an old row's missing history or turn
// today's balance/configuration into a past observation.
func (s *sourceClosureState) originalValueRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	for table, rows := range s.tables {
		if strings.HasPrefix(table, "pp_") {
			continue
		}
		for _, row := range rows {
			if err := s.w.tick(); err != nil {
				return err
			}
			if !sourceOriginalValues(row.value.Interface()) {
				s.mark("scalar", table, row.key)
			}
		}
	}
	return s.w.b.check()
}

func sourceOriginalNatural(value string, positive bool) bool {
	return unsignedInteger(value, 63, positive)
}
func sourceOriginalAmount(values ...string) bool {
	for _, value := range values {
		if _, ok := numeric(value, false); !ok {
			return false
		}
	}
	return true
}
func sourceOriginalMoney(values ...string) bool {
	for _, value := range values {
		if !sourceOriginalNatural(value, false) {
			return false
		}
	}
	return true
}
func sourceOriginalOptionalMoney(values ...*string) bool {
	for _, value := range values {
		if value != nil && !sourceOriginalMoney(*value) {
			return false
		}
	}
	return true
}

// The original procurement DDL permits the nil UUID. PB explicitly excludes it.
// Ordinary historical TEXT identifiers do not inherit either UUID restriction.
func sourceOriginalUUID(value string, nonzero bool) bool {
	if len(value) != 36 || nonzero && value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, c := range []byte(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func sourceOriginalOptionalPBUUID(values ...*string) bool {
	for _, value := range values {
		if value != nil && !sourceOriginalUUID(*value, true) {
			return false
		}
	}
	return true
}
func sourceOriginalDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func sourceOriginalBounded(value string, min, max int) bool {
	if !validText(value) || len(value) < min || len(value) > max {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func sourceOriginalLabel(value string) bool {
	return sourceOriginalBounded(value, 1, 100) && strings.TrimSpace(value) != ""
}
func sourceOriginalTail(count string, tail *string) bool {
	return sourceOriginalNatural(count, false) && ((count == "0") == (tail == nil))
}
func sourceOriginalBalance(kind string, tenant *string, balance string) bool {
	if !signedInteger(balance, 64) {
		return false
	}
	switch kind {
	case "AVAILABLE", "HELD":
		return tenant != nil && sourceOriginalMoney(balance)
	case "FUNDING_CLEARING", "REVENUE":
		return tenant == nil
	case "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING":
		return tenant == nil && (balance == "0" || strings.HasPrefix(balance, "-"))
	}
	return false
}

func sourceOriginalValues(row any) bool {
	switch r := row.(type) {
	case ChannelRow, ChannelCredentialRow, ChannelVersionRow, TenantRow, UserRow:
		return true // The safe projection has only unconstrained TEXT identities.
	case ChannelVersionCredentialRow:
		return sourceOriginalBetween(r.Position, 1, 32) && sourceOriginalBetween(r.Weight, 1, 1000000) && sourceOriginalBetween(r.MaxInflight, 1, 1000)
	case RequestRecordRow:
		return sourceOneOf(r.Status, "accepted", "dispatching", "succeeded", "failed", "cancelled") && sourceOneOf(r.BillingStatus, "reserved", "pending_reconciliation", "settled", "released") && sourceOriginalMoney(r.InputRate, r.OutputRate, r.InputUpperBound, r.OutputLimit, r.RouteRevision)
	case CostVersionRow:
		return sourceOneOf(r.Basis, "unknown", "contract", "invoice") && sourceOriginalOptionalMoney(r.InputRate, r.OutputRate)
	case ProcurementAccountRow:
		return sourceOriginalLabel(r.Label) && sourceOriginalLabel(r.Provider) && sourceOneOf(r.State, "active", "paused") && sourceOriginalMoney(r.LimitAmount) && sourceOriginalAmount(r.HeldAmount, r.SpentAmount) && sourceOriginalNatural(r.Revision, true)
	case CredentialProcurementBindingRow:
		return sourceOriginalUUID(r.OperationID, false)
	case UpstreamAttemptRow:
		quote := sourceOneOf(r.CostBasis, "unknown", "contract", "invoice") && sourceOriginalOptionalMoney(r.CostInputRate, r.CostOutputRate)
		if r.CostVersionID == nil {
			quote = quote && r.CostInputRate == nil && r.CostOutputRate == nil && r.CostBasis == "unknown"
		}
		cost := r.CostStatus == "unknown" && r.CostAmount == nil || sourceOneOf(r.CostStatus, "contract", "invoice") && r.CostAmount != nil && sourceOriginalAmount(*r.CostAmount)
		return sourceOriginalBetween(r.Sequence, 1, 2) && sourceDelivery(r.DeliveryState) && quote && cost
	case ProcurementReservationRow:
		if !sourceOriginalNatural(r.InputBound, true) || !sourceOriginalMoney(r.OutputBound, r.UpperAmount) || !sourceOriginalAmount(r.HeldAmount, r.SpentAmount) || !sourceOriginalNatural(r.Revision, true) || !sourceState(r.State) {
			return false
		}
		return (!sourceOneOf(r.State, "prepared", "sending") || r.HeldAmount == r.UpperAmount && r.SpentAmount == "0" && !r.Anomaly) && (!sourceOneOf(r.State, "settled", "released") || r.HeldAmount == "0") && (r.State != "released" || r.SpentAmount == "0")
	case ProcurementBudgetEventRow:
		return validText(r.EventKey) && len(r.EventKey) >= 1 && len(r.EventKey) <= 400 && sourceOriginalBounded(r.Type, 1, 80) && sourceOriginalBounded(r.EvidenceRef, 0, 256)
	case ProviderCostCorrectionRow:
		return sourceOriginalMoney(r.Amount) && sourceOneOf(r.Basis, "contract", "invoice")
	case ProcurementCostProposalRow:
		if !sourceOriginalUUID(r.ID, false) || !sourceOriginalMoney(r.Amount, r.ExpectedEvidenceCount) || !sourceOriginalNatural(r.ExpectedReservationRevision, true) || !sourceOneOf(r.Basis, "contract", "invoice") || !sourceOriginalBounded(r.EvidenceRef, 1, 256) || !sourceOriginalBounded(r.Reason, 1, 400) {
			return false
		}
		if r.State == "proposed" {
			return r.Approver == nil && r.ApprovalOperationID == nil && nullTime(r.ApprovedAt)
		}
		return r.State == "approved" && r.Approver != nil && *r.Approver != r.Requester && r.ApprovalOperationID != nil && *r.ApprovalOperationID != r.ID && sourceOriginalUUID(*r.ApprovalOperationID, false) && finite(r.ApprovedAt)
	case ProcurementCommandRow:
		return sourceOriginalUUID(r.OperationID, false) && sourceOriginalDigest(r.InputDigest) && sourceOneOf(r.Kind, "account_create", "account_configure", "credential_bind", "balance_observe") && (r.Status == "applied" && r.Reason == "" || r.Status == "rejected" && sourceOneOf(r.Reason, "not_found", "revision_conflict", "already_bound", "credential_revoked"))
	case ProcurementBalanceObservationRow:
		return sourceOneOf(r.Status, "known", "unknown", "unsupported", "error") && (r.Status == "known") == (r.Amount != nil) && sourceOriginalOptionalMoney(r.Amount) && r.Currency == "CNY" && sourceOriginalBounded(r.Source, 1, 100) && sourceOriginalBounded(r.Semantic, 1, 100) && sourceOriginalBounded(r.EvidenceRef, 1, 256)
	case AuditEventRow, ProjectionOutboxRow:
		return true // Known procurement payloads have a separate strict leaf.
	case ProcurementEvidenceIdentityRow:
		return sourceOriginalDigest(r.EvidenceDigest) && sourceOriginalUUID(r.EventID, false)
	case MeteringEventRow:
		return r.SchemaVersion == "1" && len(r.Source) >= 1 && len(r.Source) <= 80 && len(r.SourceEventID) >= 1 && len(r.SourceEventID) <= 200
	case MeteringEventConflictRow:
		return true // The original six-field Usage is checked independently.
	case FinalizationRecordRow:
		return sourceOriginalDigest(r.InputDigest) && sourceOneOf(r.ProtocolOutcome, "completed", "failed", "incomplete", "cancelled", "unknown") && sourceOneOf(r.TransportEndReason, "done", "eof", "provider_error", "platform_error", "platform_timeout", "client_cancelled", "connection_error", "unknown", "not_sent", "rejected_no_consumption") && sourceOneOf(r.DeliveryStage, "none", "committed", "content", "unknown") && len(r.FinishReason) <= 80
	case ReservationRow:
		return sourceOriginalMoney(r.Amount) && sourceOneOf(r.Status, "reserved", "pending_reconciliation", "settled", "released")
	case SettlementRow:
		if !sourceOriginalMoney(r.ChargedAmount, r.RefundedAmount) || !sourceOriginalAmount(r.RawRetailAmount, r.WaivedRetailAmount) || !sourceOneOf(r.Status, "settled", "released") {
			return false
		}
		charge, _ := numeric(r.ChargedAmount, false)
		refund, _ := numeric(r.RefundedAmount, false)
		return refund.Cmp(charge) <= 0
	case ReconciliationCaseRow:
		return sourceOneOf(r.State, "open", "proposed", "approved", "closed") && (r.Approver == nil || r.Requester == nil || *r.Approver != *r.Requester)
	case BillingProfileBlockRow:
		return true // This projection has no additional original scalar CHECK.
	case LedgerAccountRow:
		return sourceOriginalBalance(r.Kind, r.TenantID, r.Balance)
	case JournalRow:
		return r.Currency == "CNY" && sourceOriginalNatural(r.CreatedTxid, true)
	case JournalLineRow:
		return signedInteger(r.SignedDelta, 64)
	case APIKeyRow:
		if !sourceOriginalMoney(r.Spent, r.Held) || !sourceOriginalOptionalMoney(r.LifetimeLimit) {
			return false
		}
		if r.LifetimeLimit == nil {
			return true
		}
		held, _ := numeric(r.Held, false)
		spent, _ := numeric(r.Spent, false)
		limit, _ := numeric(*r.LifetimeLimit, false)
		return new(big.Int).Add(held, spent).Cmp(limit) <= 0
	case PeriodicBudgetControlRow:
		identity := r.Scope == "tenant" && r.TenantID != nil && r.KeyID == nil && r.AccountID == nil || r.Scope == "key" && r.TenantID != nil && r.KeyID != nil && r.AccountID == nil || r.Scope == "procurement" && r.TenantID == nil && r.KeyID == nil && r.AccountID != nil
		return identity && sourceOriginalUUID(r.ID, true) && sourceOneOf(r.PeriodKind, "utc_day", "utc_month") && sourceOriginalMoney(r.LimitAmount) && sourceOriginalNatural(r.Revision, true) && sourceOriginalAmount(r.HeldAmount, r.SpentAmount) && sourceOriginalTail(r.EventSeq, r.LastEventID) && unsignedInteger(r.CreatedXID, 64, true)
	case PeriodicBudgetWindowRow:
		return sourceOriginalUUID(r.ID, true) && sourceOriginalAmount(r.HeldAmount, r.SpentAmount) && sourceOriginalTail(r.Revision, r.LastEventID) && ((r.ObservationID == nil) == (r.ObservationCreatedXID == nil)) && (r.ObservationID == nil || sourceOriginalUUID(*r.ObservationID, true) && unsignedInteger(*r.ObservationCreatedXID, 64, true))
	case PeriodicBudgetAllocationRow:
		admission := r.Origin == "new" && r.WindowID != nil && finite(r.AdmittedAt) || sourceOneOf(r.Origin, "legacy_import", "legacy_catchup") && r.WindowID == nil && nullTime(r.AdmittedAt)
		return sourceOriginalUUID(r.ID, true) && sourceOriginalUUID(r.SubjectID, true) && sourceOriginalUUID(r.OriginSourceID, true) && sourceOneOf(r.SubjectType, "customer_request", "supplier_attempt") && admission && sourceOriginalMoney(r.UpperAmount) && sourceOriginalAmount(r.Baseline, r.SourceHeld, r.SourceSpent, r.HeldAmount, r.SpentAmount) && sourceOneOf(r.State, "held", "disputed", "settled", "released") && sourceOriginalTail(r.Revision, r.LastEventID) && (r.SubjectType != "customer_request" || r.Baseline == "0" && r.State != "disputed") && (r.Origin != "legacy_catchup" || r.SubjectType == "supplier_attempt") && unsignedInteger(r.CreatedXID, 64, true)
	case PeriodicBudgetEventRow:
		return sourceOriginalUUID(r.ID, true) && sourceOriginalUUID(r.SourceID, true) && sourceOneOf(r.SourceKind, "reservation", "settlement", "procurement_event", "legacy_import") && sourceOriginalNatural(r.Seq, true) && sourceOriginalMoney(r.AllocationBeforeRevision) && sourceOriginalOptionalMoney(r.WindowBeforeRevision) && sourceOriginalAmount(r.BeforeHeld, r.BeforeSpent, r.AfterHeld, r.AfterSpent, r.AfterSourceSpent) && sourceOneOf(r.AfterState, "held", "disputed", "settled", "released") && unsignedInteger(r.CreatedXID, 64, true)
	case PeriodicBudgetOperationRow:
		return sourceOriginalUUID(r.OperationID, true) && sourceOriginalUUID(r.ResourceID, true) && sourceOneOf(r.Kind, "tenant_periodic_budget.configure", "key_periodic_budget.configure", "procurement_periodic_budget.configure") && (r.Scope == "platform" || sourceOriginalUUID(r.Scope, true)) && len(r.Target) <= 200 && sourceOriginalNatural(r.Revision, true) && sourceOriginalDigest(r.InputFingerprint) && unsignedInteger(r.CreatedXID, 64, true) && sourceOriginalOptionalPBUUID(r.ObservationID)
	case PeriodicBudgetConfigurationVersionRow:
		return sourceOriginalNatural(r.Revision, true) && sourceOneOf(r.PeriodKind, "utc_day", "utc_month") && sourceOriginalMoney(r.LimitAmount) && unsignedInteger(r.CreatedXID, 64, true)
	case PeriodicBudgetObservationOriginRow:
		return sourceOriginalUUID(r.ControlID, true) && r.Version == "1" && unsignedInteger(r.CreatedXID, 64, true)
	case PeriodicBudgetObservationHeadRow:
		return sourceOriginalUUID(r.ControlID, true) && sourceOriginalTail(r.Ordinal, r.LastObservationID) && unsignedInteger(r.UpdatedXID, 64, true) && sourceOriginalOptionalPBUUID(r.LastObservationID)
	case PeriodicBudgetObservationJournalRow:
		window := r.WindowID == nil && r.WindowRevision == nil && r.WindowLastEventID == nil || r.WindowID != nil && r.WindowRevision != nil && sourceOriginalTail(*r.WindowRevision, r.WindowLastEventID)
		kind := r.Kind == "command" && finite(r.ObservedAt) && requestPointer(r.CommandSourceID, r.SourceID) && r.WindowSourceID == nil || r.Kind == "window_birth" && requestPointer(r.WindowID, r.SourceID) && requestPointer(r.WindowRevision, "0") && r.WindowLastEventID == nil && nullTime(r.ObservedAt) && r.CommandSourceID == nil && requestPointer(r.WindowSourceID, r.SourceID)
		return sourceOriginalUUID(r.ID, true) && sourceOriginalUUID(r.ControlID, true) && sourceOriginalUUID(r.SourceID, true) && sourceOriginalUUID(r.ConfigurationOperationID, true) && sourceOriginalNatural(r.Ordinal, true) && (r.Ordinal == "1") == (r.PreviousID == nil) && sourceOriginalTail(r.EventSeq, r.LastEventID) && sourceOriginalNatural(r.ConfigurationRevision, true) && unsignedInteger(r.CreatedXID, 64, true) && sourceOriginalOptionalPBUUID(r.PreviousID, r.LastEventID, r.WindowID, r.WindowLastEventID) && window && kind
	}
	return false
}

func sourceOriginalBetween(value string, lower, upper int64) bool {
	if !sourceOriginalNatural(value, false) {
		return false
	}
	n, _ := new(big.Int).SetString(value, 10)
	return n.Cmp(big.NewInt(lower)) >= 0 && n.Cmp(big.NewInt(upper)) <= 0
}
