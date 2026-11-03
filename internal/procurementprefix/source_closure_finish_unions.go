package procurementprefix

import (
	"math/big"
	"strings"
	"unicode"
)

// finishUnionRows checks row-local contracts only. Historical observation cuts,
// original effects, and the presence or absence of a past PB configuration are
// deliberately left to their respective replay domains.
func (s *sourceClosureState) finishUnionRows() error {
	if s == nil || s.w == nil || s.w.b.ctx == nil {
		return ErrInput
	}
	if err := s.w.b.check(); err != nil {
		return err
	}
	for _, raw := range s.tables["pp_reconciliation_sources"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		if !sourceReconciliationValues(raw.value.Interface().(ReconciliationSourceRow)) {
			s.mark("scalar", "pp_reconciliation_sources", raw.key)
		}
	}
	for _, raw := range s.tables["pp_customer_finish_outcomes"] {
		if err := s.w.tick(); err != nil {
			return err
		}
		if !sourceCustomerFinishValues(raw.value.Interface().(CustomerFinishOutcomeRow)) {
			s.mark("scalar", "pp_customer_finish_outcomes", raw.key)
		}
	}
	return s.w.b.check()
}

func sourceFinishAll(present bool, values ...*string) bool {
	for _, value := range values {
		if (value != nil) != present {
			return false
		}
	}
	return true
}

func sourceFinishMoney(value string) (*big.Int, bool) {
	if !signedInteger(value, 64) {
		return nil, false
	}
	return numeric(value, false)
}

// These scalar predicates independently spell the original request contract;
// neither the financial writer nor its replay computes this leaf's expected
// values. Reasons are byte-bounded text, without an added trimming policy.
func sourceFinishReason(value *string) bool {
	return value != nil && validText(*value) && len(*value) >= 1 && len(*value) <= 200
}

func sourceFinishReference(value *string) bool {
	if value == nil || len(*value) < 1 || len(*value) > 256 || !validText(*value) || strings.Contains(*value, "://") {
		return false
	}
	lower := strings.ToLower(*value)
	if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "mg_") || strings.HasPrefix(lower, "bearer") {
		return false
	}
	for _, r := range *value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(":_./-", r) {
			return false
		}
	}
	return true
}

func sourceFinishUsage(status, input, output, total, cached, reasoning *string) bool {
	if !sourceFinishAll(true, status, input, output, total, cached, reasoning) || !sourceOneOf(*status, "confirmed_final", "provisional", "estimated", "missing", "conflicting") {
		return false
	}
	counts := make([]*big.Int, 5)
	for i, value := range []*string{input, output, total, cached, reasoning} {
		var ok bool
		counts[i], ok = sourceFinishMoney(*value)
		if !ok {
			return false
		}
	}
	if counts[3].Cmp(counts[0]) > 0 || counts[4].Cmp(counts[1]) > 0 {
		return false
	}
	return *status != "confirmed_final" || new(big.Int).Add(counts[0], counts[1]).Cmp(counts[2]) == 0
}

func sourceReconciliationValues(r ReconciliationSourceRow) bool {
	if !sourceOneOf(r.Action, "propose", "approve", "release") || r.ActorStatus != "active" || !finite(r.CaseCreatedAt) || !finite(r.ReservationDeadline) || !(finite(r.ApprovalCheckedAt) || nullTime(r.ApprovalCheckedAt)) {
		return false
	}
	if !sourceOneOf(r.BeforeCaseState, "open", "proposed", "approved", "closed") || !sourceOneOf(r.AfterCaseState, "open", "proposed", "approved", "closed") || !sourceOneOf(r.BeforeReservationStatus, "reserved", "pending_reconciliation", "settled", "released") {
		return false
	}
	input := []*string{r.InputUsageStatus, r.InputInputTokens, r.InputOutputTokens, r.InputTotalTokens, r.InputCachedTokens, r.InputReasoningTokens, r.InputEvidenceRef}
	if r.Action == "propose" {
		if !sourceFinishUsage(r.InputUsageStatus, r.InputInputTokens, r.InputOutputTokens, r.InputTotalTokens, r.InputCachedTokens, r.InputReasoningTokens) || *r.InputUsageStatus != "confirmed_final" || !sourceFinishReference(r.InputEvidenceRef) || !sourceFinishReason(r.InputReason) {
			return false
		}
	} else if !sourceFinishAll(false, input...) || r.Action == "approve" && r.InputReason != nil || r.Action == "release" && !sourceFinishReason(r.InputReason) {
		return false
	}
	caseValues := []*string{r.CaseUsageStatus, r.CaseInputTokens, r.CaseOutputTokens, r.CaseTotalTokens, r.CaseCachedTokens, r.CaseReasoningTokens, r.CaseEvidenceRef, r.CaseProposalReason}
	if !sourceFinishAll(false, caseValues...) && (!sourceFinishUsage(r.CaseUsageStatus, r.CaseInputTokens, r.CaseOutputTokens, r.CaseTotalTokens, r.CaseCachedTokens, r.CaseReasoningTokens) || !sourceFinishReference(r.CaseEvidenceRef) || !sourceFinishReason(r.CaseProposalReason)) {
		return false
	}
	switch r.Action {
	case "propose":
		return r.ActorRole == "finance_operator" && r.BeforeCaseState == "open" && r.AfterCaseState == "proposed" && r.BeforeReservationStatus == "pending_reconciliation" && r.AfterRequester != nil && *r.AfterRequester == r.Actor && r.AfterApprover == nil && sourceFinishAll(false, r.RequesterRole, r.RequesterStatus) && nullTime(r.ApprovalCheckedAt) && sourceFinishAll(false, r.DerivedIncomingSourceID, r.FinalizationID, r.CustomerFinishOutcomeID)
	case "approve":
		if r.ActorRole != "finance_approver" || r.BeforeCaseState != "proposed" || r.AfterCaseState != "approved" || r.BeforeRequester == nil || *r.BeforeRequester == r.Actor || !sourceSameOptional(r.AfterRequester, r.BeforeRequester) || r.AfterApprover == nil || *r.AfterApprover != r.Actor || r.RequesterRole == nil || *r.RequesterRole != "finance_operator" || r.RequesterStatus == nil || *r.RequesterStatus != "active" || r.CaseUsageStatus == nil || *r.CaseUsageStatus != "confirmed_final" || !finite(r.ApprovalCheckedAt) || r.CustomerFinishOutcomeID == nil {
			return false
		}
		// Only this check's own clock controls its optional incoming branch.
		return sourceFinishAll(r.ApprovalCheckedAt.Time.Before(r.ReservationDeadline.Time), r.DerivedIncomingSourceID, r.FinalizationID)
	case "release":
		after := "closed"
		if r.BeforeCaseState == "approved" {
			after = "approved"
		}
		return r.ActorRole == "finance_approver" && r.BeforeReservationStatus == "pending_reconciliation" && r.AfterCaseState == after && sourceSameOptional(r.AfterRequester, r.BeforeRequester) && sourceSameOptional(r.AfterApprover, r.BeforeApprover) && sourceFinishAll(false, r.RequesterRole, r.RequesterStatus, r.DerivedIncomingSourceID, r.FinalizationID) && nullTime(r.ApprovalCheckedAt) && r.CustomerFinishOutcomeID != nil
	}
	return false
}

func sourceCustomerFinishValues(r CustomerFinishOutcomeRow) bool {
	if r.SourceID != r.ReconciliationSourceID || !finite(r.ReservationDeadline) || !(finite(r.SettlementDecisionCheckedAt) || nullTime(r.SettlementDecisionCheckedAt)) || !(finite(r.FinancialCheckedAt) || nullTime(r.FinancialCheckedAt)) {
		return false
	}
	if !sourceOneOf(r.BeforeCaseState, "open", "proposed", "approved", "closed") || !sourceOneOf(r.AfterCaseState, "open", "proposed", "approved", "closed") || !sourceOneOf(r.BeforeRequestStatus, "accepted", "dispatching", "succeeded", "failed", "cancelled") || !sourceOneOf(r.AfterRequestStatus, "accepted", "dispatching", "succeeded", "failed", "cancelled") {
		return false
	}
	for _, status := range []string{r.BeforeReservationStatus, r.AfterReservationStatus, r.BeforeBillingStatus, r.AfterBillingStatus} {
		if !sourceOneOf(status, "reserved", "pending_reconciliation", "settled", "released") {
			return false
		}
	}
	money := make([]*big.Int, 6)
	for i, value := range []string{r.ReservationAmount, r.ChargedAmount, r.BeforeKeyHeld, r.BeforeKeySpent, r.AfterKeyHeld, r.AfterKeySpent} {
		var ok bool
		money[i], ok = sourceFinishMoney(value)
		if !ok {
			return false
		}
	}
	reserved, charged := money[0], money[1]
	raw, rawOK := numeric(r.RawAmount, false)
	waived, waivedOK := numeric(r.WaivedAmount, false)
	if !rawOK || !waivedOK || charged.Cmp(reserved) > 0 || new(big.Int).Add(charged, waived).Cmp(raw) != 0 {
		return false
	}
	if r.SelectedMode != "recovery" && !(r.SelectedMode == "release" && r.ResultKind == "new_settlement" && nullTime(r.SettlementDecisionCheckedAt) && sourceOneOf(r.SelectedReason, "released_reconciliation", "released_deadline")) {
		return false
	}
	profile := []*string{r.ProfileBlockModelID, r.ProfileBlockProfileVersion, r.ProfileBlockDisposition, r.ProfileBlockAuditID}
	if r.ResultKind == "new_settlement" && raw.Cmp(reserved) > 0 {
		if !sourceFinishAll(true, profile...) || !sourceOneOf(*r.ProfileBlockDisposition, "created", "reused") {
			return false
		}
	} else if !sourceFinishAll(false, profile...) {
		return false
	}
	switch r.ResultKind {
	case "new_settlement":
		if !sourceFinishAll(true, r.SettlementID, r.CustomerJournalID, r.BillingAuditID, r.BillingOutboxID) || !finite(r.FinancialCheckedAt) || r.SelectedMode == "recovery" && !finite(r.SettlementDecisionCheckedAt) || !sourceOneOf(r.ResultStatus, "settled", "released") {
			return false
		}
		// These are independent clock observations. Expiry in either recovery
		// check cannot be undone by a rollback at the other check. Direct
		// release has only the final financial check and its own initial reason.
		expired := !r.FinancialCheckedAt.Time.Before(r.ReservationDeadline.Time)
		if r.SelectedMode == "recovery" {
			expired = expired || !r.SettlementDecisionCheckedAt.Time.Before(r.ReservationDeadline.Time)
			if !sourceOneOf(r.SelectedReason, "charged_usage", "released_no_delivery", "released_deadline") {
				return false
			}
		}
		if (r.SelectedReason == "released_deadline") != expired {
			return false
		}
		afterCase, afterRequest := "closed", r.BeforeRequestStatus
		if r.BeforeCaseState == "approved" {
			afterCase = "approved"
		}
		if r.ResultStatus == "released" && sourceOneOf(afterRequest, "accepted", "dispatching") {
			afterRequest = "failed"
		}
		if r.AfterReservationStatus != r.ResultStatus || r.AfterBillingStatus != r.ResultStatus || r.AfterCaseState != afterCase || r.AfterCaseReason != r.BeforeCaseReason || r.AfterRequestStatus != afterRequest || new(big.Int).Sub(money[2], reserved).Cmp(money[4]) != 0 || new(big.Int).Add(money[3], charged).Cmp(money[5]) != 0 {
			return false
		}
		if r.SelectedReason == "charged_usage" {
			expectedCharge := raw
			if reserved.Cmp(raw) < 0 {
				expectedCharge = reserved
			}
			if r.ResultStatus != "settled" || charged.Cmp(expectedCharge) != 0 {
				return false
			}
		} else if !sourceOneOf(r.SelectedReason, "released_deadline", "released_no_delivery", "released_reconciliation") || r.ResultStatus != "released" || raw.Sign() != 0 || charged.Sign() != 0 || waived.Sign() != 0 {
			return false
		}
		return true
	case "existing_settlement":
		return r.SettlementID != nil && sourceFinishAll(false, r.CustomerJournalID, r.CustomerTenantControlID, r.CustomerKeyControlID, r.BillingAuditID, r.BillingOutboxID) && nullTime(r.SettlementDecisionCheckedAt) && nullTime(r.FinancialCheckedAt) && sourceOneOf(r.ResultStatus, "settled", "released") && r.AfterCaseState == r.BeforeCaseState && r.AfterCaseReason == r.BeforeCaseReason && r.AfterReservationStatus == r.BeforeReservationStatus && r.AfterRequestStatus == r.BeforeRequestStatus && r.AfterBillingStatus == r.BeforeBillingStatus && money[2].Cmp(money[4]) == 0 && money[3].Cmp(money[5]) == 0
	case "pending":
		if r.SelectedMode != "recovery" || !sourceFinishAll(false, r.SettlementID, r.CustomerJournalID, r.CustomerTenantControlID, r.CustomerKeyControlID, r.BillingAuditID, r.BillingOutboxID) || !finite(r.SettlementDecisionCheckedAt) || !r.SettlementDecisionCheckedAt.Time.Before(r.ReservationDeadline.Time) || !nullTime(r.FinancialCheckedAt) || r.ResultStatus != "pending_reconciliation" || !sourceOneOf(r.SelectedReason, "metering_conflict", "delivery_unknown", "usage_unconfirmed") || raw.Sign() != 0 || charged.Sign() != 0 || waived.Sign() != 0 || r.AfterCaseState != r.BeforeCaseState || r.AfterRequestStatus != r.BeforeRequestStatus || money[2].Cmp(money[4]) != 0 || money[3].Cmp(money[5]) != 0 {
			return false
		}
		if sourceOneOf(r.BeforeReservationStatus, "settled", "released") {
			return r.AfterReservationStatus == r.BeforeReservationStatus && r.AfterBillingStatus == r.BeforeBillingStatus && r.AfterCaseReason == r.BeforeCaseReason
		}
		reason := r.SelectedReason
		if r.BeforeCaseReason == "metering_conflict" {
			reason = "metering_conflict"
		}
		return r.AfterReservationStatus == "pending_reconciliation" && r.AfterBillingStatus == "pending_reconciliation" && r.AfterCaseReason == reason
	}
	return false
}
