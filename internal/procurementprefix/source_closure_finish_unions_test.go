package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func sourceFinishClock(hour int) Time {
	return Time{Time: time.Date(2026, 11, 4, hour, 0, 0, 0, time.UTC), Valid: true}
}

// Literal rows are independent of the financial writer and finishReplay. The
// amounts below are small enough for a reviewer to calculate by hand.
func sourceReconcileLiteral(action string) ReconciliationSourceRow {
	r := ReconciliationSourceRow{
		ID: "11111111-1111-4111-8111-111111111111", RootID: "22222222-2222-4222-8222-222222222222", StepNo: "1", StepKind: "reconciliation", CreatedXID: "7",
		TenantID: "tenant", RequestID: "request", CaseID: "case", Action: action, Actor: "operator", ActorRole: "finance_operator", ActorStatus: "active",
		BeforeCaseReason: "usage_unconfirmed", CaseCreatedAt: sourceFinishClock(8), BeforeCaseState: "open", AfterCaseState: "proposed", BeforeReservationStatus: "pending_reconciliation",
		AfterRequester: sourcePtr("operator"), ReservationDeadline: sourceFinishClock(10), AuditID: "reconcile-audit",
	}
	if action == "propose" {
		r.InputUsageStatus, r.InputInputTokens, r.InputOutputTokens = sourcePtr("confirmed_final"), sourcePtr("5"), sourcePtr("3")
		r.InputTotalTokens, r.InputCachedTokens, r.InputReasoningTokens = sourcePtr("8"), sourcePtr("2"), sourcePtr("1")
		r.InputEvidenceRef, r.InputReason = sourcePtr("ticket:财务/8"), sourcePtr("provider confirmed")
		return r
	}
	r.Actor, r.ActorRole, r.BeforeCaseState = "approver", "finance_approver", "proposed"
	r.BeforeRequester, r.AfterRequester = sourcePtr("operator"), sourcePtr("operator")
	r.CustomerFinishOutcomeID = sourcePtr("33333333-3333-4333-8333-333333333333")
	if action == "release" {
		r.AfterCaseState, r.InputReason = "closed", sourcePtr("operator release")
		return r
	}
	r.AfterCaseState, r.AfterApprover = "approved", sourcePtr("approver")
	r.RequesterRole, r.RequesterStatus = sourcePtr("finance_operator"), sourcePtr("active")
	r.CaseUsageStatus, r.CaseInputTokens, r.CaseOutputTokens = sourcePtr("confirmed_final"), sourcePtr("5"), sourcePtr("3")
	r.CaseTotalTokens, r.CaseCachedTokens, r.CaseReasoningTokens = sourcePtr("8"), sourcePtr("2"), sourcePtr("1")
	r.CaseEvidenceRef, r.CaseProposalReason = sourcePtr("ticket:8"), sourcePtr("provider confirmed")
	r.ApprovalCheckedAt, r.DerivedIncomingSourceID, r.FinalizationID = sourceFinishClock(9), sourcePtr("incoming"), sourcePtr("finalization")
	return r
}

func sourceFinishLiteral(kind string) CustomerFinishOutcomeRow {
	r := CustomerFinishOutcomeRow{
		ID: "33333333-3333-4333-8333-333333333333", RootID: "22222222-2222-4222-8222-222222222222", StepNo: "2", StepKind: "customer_finish", SourceID: "11111111-1111-4111-8111-111111111111", CreatedXID: "7",
		TenantID: "tenant", RequestID: "request", ReconciliationSourceID: "11111111-1111-4111-8111-111111111111", CaseID: "case", CustomerReservationID: "reservation", ReservationAmount: "10", ReservationDeadline: sourceFinishClock(10),
		ResultKind: kind, SelectedMode: "recovery", SelectedReason: "charged_usage", SettlementDecisionCheckedAt: sourceFinishClock(9), FinancialCheckedAt: sourceFinishClock(9),
		BeforeCaseState: "proposed", AfterCaseState: "closed", BeforeCaseReason: "usage_unconfirmed", AfterCaseReason: "usage_unconfirmed", BeforeReservationStatus: "pending_reconciliation", AfterReservationStatus: "settled",
		BeforeRequestStatus: "succeeded", AfterRequestStatus: "succeeded", BeforeBillingStatus: "pending_reconciliation", AfterBillingStatus: "settled",
		BeforeKeyHeld: "15", AfterKeyHeld: "5", BeforeKeySpent: "2", AfterKeySpent: "9", RawAmount: "7", ChargedAmount: "7", WaivedAmount: "0", ResultStatus: "settled",
		SettlementID: sourcePtr("settlement"), CustomerJournalID: sourcePtr("journal"), BillingAuditID: sourcePtr("billing-audit"), BillingOutboxID: sourcePtr("billing-outbox"), DNewSettlement: sourcePtr("11111111-1111-4111-8111-111111111111"),
	}
	if kind == "new_settlement" {
		return r
	}
	r.CustomerJournalID, r.BillingAuditID, r.BillingOutboxID, r.DNewSettlement = nil, nil, nil, nil
	r.FinancialCheckedAt = Time{}
	r.AfterCaseState, r.AfterKeyHeld, r.AfterKeySpent = r.BeforeCaseState, r.BeforeKeyHeld, r.BeforeKeySpent
	r.AfterReservationStatus, r.AfterBillingStatus = r.BeforeReservationStatus, r.BeforeBillingStatus
	if kind == "existing_settlement" {
		r.SelectedReason, r.SettlementDecisionCheckedAt = "legacy/provider-reason", Time{}
		return r
	}
	r.SettlementID, r.SelectedReason, r.ResultStatus = nil, "delivery_unknown", "pending_reconciliation"
	r.RawAmount, r.ChargedAmount, r.WaivedAmount = "0", "0", "0"
	r.AfterReservationStatus, r.AfterBillingStatus, r.AfterCaseReason = "pending_reconciliation", "pending_reconciliation", "delivery_unknown"
	return r
}

func sourceFinishOverrun() CustomerFinishOutcomeRow {
	r := sourceFinishLiteral("new_settlement")
	r.RawAmount, r.ChargedAmount, r.WaivedAmount, r.AfterKeySpent = "12", "10", "2", "12"
	r.ProfileBlockModelID, r.ProfileBlockProfileVersion = sourcePtr("model"), sourcePtr("profile-1")
	r.ProfileBlockDisposition, r.ProfileBlockAuditID = sourcePtr("created"), sourcePtr("profile-audit")
	return r
}

func TestSourceFinishUnionReconcileActionsAndClockBranches(t *testing.T) {
	for _, action := range []string{"propose", "approve", "release"} {
		if !sourceReconciliationValues(sourceReconcileLiteral(action)) {
			t.Fatalf("literal %s rejected", action)
		}
	}
	for _, hour := range []int{9, 10, 11} {
		r := sourceReconcileLiteral("approve")
		r.ApprovalCheckedAt = sourceFinishClock(hour)
		if hour >= 10 {
			r.DerivedIncomingSourceID, r.FinalizationID = nil, nil
		}
		// Case creation and the independent approval clock need not increase.
		r.CaseCreatedAt = sourceFinishClock(12)
		if !sourceReconciliationValues(r) {
			t.Fatalf("clock/deadline branch %d rejected", hour)
		}
		r.DerivedIncomingSourceID = sourcePtr("incoming")
		r.FinalizationID = nil
		if sourceReconciliationValues(r) {
			t.Fatal("half derived output accepted")
		}
		if hour < 10 {
			r.DerivedIncomingSourceID = nil
		} else {
			r.FinalizationID = sourcePtr("finalization")
		}
		if sourceReconciliationValues(r) {
			t.Fatal("derived output did not use approval clock")
		}
	}
	for _, status := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
		r := sourceReconcileLiteral("approve")
		r.BeforeReservationStatus = status
		r.BeforeApprover = sourcePtr("prior-approver")
		if !sourceReconciliationValues(r) {
			t.Fatalf("approve before snapshot %s was narrowed", status)
		}
	}
	for _, state := range []string{"open", "proposed", "approved", "closed"} {
		r := sourceReconcileLiteral("release")
		r.BeforeCaseState = state
		if state == "approved" {
			r.AfterCaseState = "approved"
		}
		// Release is not a newly invented two-person approval operation.
		r.Actor, r.BeforeRequester, r.AfterRequester = "operator", sourcePtr("operator"), sourcePtr("operator")
		if !sourceReconciliationValues(r) {
			t.Fatalf("release original before %s rejected", state)
		}
	}
	propose := sourceReconcileLiteral("propose")
	propose.BeforeRequester, propose.BeforeApprover = sourcePtr("old-maker"), sourcePtr("old-approver")
	propose.InputReason = sourcePtr(" \n")
	if !sourceReconciliationValues(propose) {
		t.Fatal("row contract invented optional-before or reason trimming restrictions")
	}
}

func TestSourceFinishUnionReconcileNullAndValueMutations(t *testing.T) {
	inputNames := []string{"InputUsageStatus", "InputInputTokens", "InputOutputTokens", "InputTotalTokens", "InputCachedTokens", "InputReasoningTokens", "InputEvidenceRef", "InputReason"}
	caseNames := []string{"CaseUsageStatus", "CaseInputTokens", "CaseOutputTokens", "CaseTotalTokens", "CaseCachedTokens", "CaseReasoningTokens", "CaseEvidenceRef", "CaseProposalReason"}
	for _, name := range inputNames {
		t.Run("propose missing "+name, func(t *testing.T) {
			r := sourceReconcileLiteral("propose")
			reflect.ValueOf(&r).Elem().FieldByName(name).SetZero()
			if sourceReconciliationValues(r) {
				t.Fatal("partial consumed proposal input accepted")
			}
		})
		for _, action := range []string{"approve", "release"} {
			if action == "release" && name == "InputReason" {
				continue
			}
			t.Run(action+" ignored "+name, func(t *testing.T) {
				r := sourceReconcileLiteral(action)
				reflect.ValueOf(&r).Elem().FieldByName(name).Set(reflect.ValueOf(sourcePtr("0")))
				if sourceReconciliationValues(r) {
					t.Fatal("ignored input was not SQL NULL")
				}
			})
		}
	}
	for _, name := range caseNames {
		r := sourceReconcileLiteral("approve")
		reflect.ValueOf(&r).Elem().FieldByName(name).SetZero()
		if sourceReconciliationValues(r) {
			t.Fatalf("partial case snapshot accepted: %s", name)
		}
		r = sourceReconcileLiteral("release")
		reflect.ValueOf(&r).Elem().FieldByName(name).Set(reflect.ValueOf(sourcePtr("0")))
		if sourceReconciliationValues(r) {
			t.Fatalf("one-column case snapshot accepted: %s", name)
		}
	}
	mutations := map[string]func(*ReconciliationSourceRow){
		"unknown action":      func(r *ReconciliationSourceRow) { r.Action = "refund" },
		"inactive actor":      func(r *ReconciliationSourceRow) { r.ActorStatus = "disabled" },
		"wrong actor role":    func(r *ReconciliationSourceRow) { r.ActorRole = "platform_admin" },
		"unknown before case": func(r *ReconciliationSourceRow) { r.BeforeCaseState = "other" },
		"wrong after case":    func(r *ReconciliationSourceRow) { r.AfterCaseState = "closed" },
		"unknown reservation": func(r *ReconciliationSourceRow) { r.BeforeReservationStatus = "other" },
		"same maker approver": func(r *ReconciliationSourceRow) {
			r.BeforeRequester = sourcePtr(r.Actor)
			r.AfterRequester = sourcePtr(r.Actor)
		},
		"missing maker":            func(r *ReconciliationSourceRow) { r.BeforeRequester = nil; r.AfterRequester = nil },
		"maker changed":            func(r *ReconciliationSourceRow) { r.AfterRequester = sourcePtr("other") },
		"approver changed":         func(r *ReconciliationSourceRow) { r.AfterApprover = sourcePtr("other") },
		"maker role missing":       func(r *ReconciliationSourceRow) { r.RequesterRole = nil },
		"maker role wrong":         func(r *ReconciliationSourceRow) { r.RequesterRole = sourcePtr("finance_approver") },
		"maker inactive":           func(r *ReconciliationSourceRow) { r.RequesterStatus = sourcePtr("disabled") },
		"maker status missing":     func(r *ReconciliationSourceRow) { r.RequesterStatus = nil },
		"no finish":                func(r *ReconciliationSourceRow) { r.CustomerFinishOutcomeID = nil },
		"nonfinal approve":         func(r *ReconciliationSourceRow) { r.CaseUsageStatus = sourcePtr("provisional") },
		"wrong total":              func(r *ReconciliationSourceRow) { r.CaseTotalTokens = sourcePtr("9") },
		"cached exceeds input":     func(r *ReconciliationSourceRow) { r.CaseCachedTokens = sourcePtr("6") },
		"reasoning exceeds output": func(r *ReconciliationSourceRow) { r.CaseReasoningTokens = sourcePtr("4") },
		"negative count":           func(r *ReconciliationSourceRow) { r.CaseInputTokens = sourcePtr("-1") },
		"int64 overflow":           func(r *ReconciliationSourceRow) { r.CaseInputTokens = sourcePtr("9223372036854775808") },
		"credential evidence":      func(r *ReconciliationSourceRow) { r.CaseEvidenceRef = sourcePtr("sk-secret") },
		"URL evidence":             func(r *ReconciliationSourceRow) { r.CaseEvidenceRef = sourcePtr("https://provider/8") },
		"empty reason":             func(r *ReconciliationSourceRow) { r.CaseProposalReason = sourcePtr("") },
		"oversized reason":         func(r *ReconciliationSourceRow) { r.CaseProposalReason = sourcePtr(strings.Repeat("x", 201)) },
		"clock missing":            func(r *ReconciliationSourceRow) { r.ApprovalCheckedAt = Time{} },
		"clock infinite": func(r *ReconciliationSourceRow) {
			r.ReservationDeadline = Time{Valid: true, InfinityModifier: pgtype.Infinity}
		},
		"creation invalid null": func(r *ReconciliationSourceRow) { r.CaseCreatedAt.Valid = false },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := sourceReconcileLiteral("approve")
			mutate(&r)
			if sourceReconciliationValues(r) {
				t.Fatal("invalid row accepted")
			}
		})
	}
	for _, status := range []string{"provisional", "estimated", "missing", "conflicting"} {
		r := sourceReconcileLiteral("approve")
		r.Action, r.AfterCaseState, r.InputReason = "release", "closed", sourcePtr("release")
		r.AfterApprover, r.RequesterRole, r.RequesterStatus = nil, nil, nil
		r.ApprovalCheckedAt, r.DerivedIncomingSourceID, r.FinalizationID = Time{}, nil, nil
		r.CaseUsageStatus, r.CaseTotalTokens = sourcePtr(status), sourcePtr("99")
		if !sourceReconciliationValues(r) {
			t.Fatalf("DDL case snapshot %s acceptance narrowed", status)
		}
		r.CaseInputTokens, r.CaseOutputTokens, r.CaseTotalTokens = sourcePtr("9223372036854775807"), sourcePtr("9223372036854775807"), sourcePtr("0")
		if !sourceReconciliationValues(r) {
			t.Fatalf("nonfinal %s sum incorrectly constrained", status)
		}
	}
}

func TestSourceFinishUnionResultsAndNonmonotonicClocks(t *testing.T) {
	for _, kind := range []string{"new_settlement", "existing_settlement", "pending"} {
		if !sourceCustomerFinishValues(sourceFinishLiteral(kind)) {
			t.Fatalf("literal %s rejected", kind)
		}
	}
	for _, disposition := range []string{"created", "reused"} {
		r := sourceFinishOverrun()
		r.ProfileBlockDisposition = sourcePtr(disposition)
		// PB controls are independently optional; either may have been absent
		// at the real historical operation regardless of today's database.
		for _, controls := range [][2]*string{{nil, nil}, {sourcePtr("tenant-control"), nil}, {nil, sourcePtr("key-control")}, {sourcePtr("tenant-control"), sourcePtr("key-control")}} {
			r.CustomerTenantControlID, r.CustomerKeyControlID = controls[0], controls[1]
			if !sourceCustomerFinishValues(r) {
				t.Fatalf("overrun %s and controls rejected", disposition)
			}
		}
	}
	for _, mode := range []string{"recovery", "release"} {
		for _, financialHour := range []int{8, 10, 12} {
			r := sourceFinishLiteral("new_settlement")
			r.SelectedMode, r.SelectedReason, r.ResultStatus = mode, "released_deadline", "released"
			r.RawAmount, r.ChargedAmount, r.WaivedAmount, r.AfterKeySpent = "0", "0", "0", "2"
			r.AfterReservationStatus, r.AfterBillingStatus = "released", "released"
			r.BeforeRequestStatus, r.AfterRequestStatus = "dispatching", "failed"
			r.SettlementDecisionCheckedAt, r.FinancialCheckedAt = sourceFinishClock(11), sourceFinishClock(financialHour)
			if mode == "release" {
				r.SettlementDecisionCheckedAt = Time{}
			}
			want := mode == "recovery" || financialHour >= 10
			if sourceCustomerFinishValues(r) != want {
				t.Fatalf("nonmonotonic %s clocks %d: want valid=%v", mode, financialHour, want)
			}
		}
	}
	for _, reason := range []string{"released_no_delivery", "released_reconciliation", "released_deadline"} {
		r := sourceFinishLiteral("new_settlement")
		r.SelectedReason, r.ResultStatus, r.AfterReservationStatus, r.AfterBillingStatus = reason, "released", "released", "released"
		r.RawAmount, r.ChargedAmount, r.WaivedAmount, r.AfterKeySpent = "0", "0", "0", "2"
		if reason == "released_reconciliation" {
			r.SelectedMode, r.SettlementDecisionCheckedAt = "release", Time{}
		}
		if reason == "released_deadline" {
			r.SettlementDecisionCheckedAt = sourceFinishClock(10)
		}
		if !sourceCustomerFinishValues(r) {
			t.Fatalf("release reason %s rejected", reason)
		}
	}
	for _, hour := range []int{10, 11} {
		pending := sourceFinishLiteral("pending")
		pending.SettlementDecisionCheckedAt = sourceFinishClock(hour)
		if sourceCustomerFinishValues(pending) {
			t.Fatalf("pending bypassed decision deadline at %d", hour)
		}
		charged := sourceFinishLiteral("new_settlement")
		charged.SettlementDecisionCheckedAt = sourceFinishClock(hour)
		charged.FinancialCheckedAt = sourceFinishClock(8)
		if sourceCustomerFinishValues(charged) {
			t.Fatalf("financial rollback revived expired decision at %d", hour)
		}
		charged.SelectedReason, charged.ResultStatus, charged.AfterReservationStatus, charged.AfterBillingStatus = "released_no_delivery", "released", "released", "released"
		charged.RawAmount, charged.ChargedAmount, charged.WaivedAmount, charged.AfterKeySpent = "0", "0", "0", "2"
		if sourceCustomerFinishValues(charged) {
			t.Fatalf("expired decision lost deadline reason at %d", hour)
		}
		charged.SelectedReason = "released_deadline"
		if !sourceCustomerFinishValues(charged) {
			t.Fatalf("deadline release rejected after financial rollback at %d", hour)
		}
	}
	existing := sourceFinishLiteral("existing_settlement")
	existing.RawAmount, existing.ChargedAmount, existing.WaivedAmount, existing.SelectedReason = "12", "10", "2", "arbitrary historical reason"
	if !sourceCustomerFinishValues(existing) {
		t.Fatal("existing reason or unreferenced historic profile was narrowed")
	}
	for _, state := range []string{"open", "proposed", "approved", "closed"} {
		for _, status := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
			r := sourceFinishLiteral("existing_settlement")
			r.SelectedReason = ""
			r.BeforeCaseState, r.AfterCaseState = state, state
			r.BeforeReservationStatus, r.AfterReservationStatus = status, status
			r.BeforeBillingStatus, r.AfterBillingStatus = status, status
			if !sourceCustomerFinishValues(r) {
				t.Fatalf("existing unchanged state or original reason narrowed: %s %s", state, status)
			}
		}
	}
	for _, before := range []string{"reserved", "pending_reconciliation", "settled", "released"} {
		for _, reason := range []string{"metering_conflict", "delivery_unknown", "usage_unconfirmed"} {
			r := sourceFinishLiteral("pending")
			r.BeforeReservationStatus, r.SelectedReason = before, reason
			r.BeforeCaseReason, r.AfterCaseReason = "metering_conflict", "metering_conflict"
			if before == "settled" || before == "released" {
				r.AfterReservationStatus, r.AfterBillingStatus = before, r.BeforeBillingStatus
			}
			if !sourceCustomerFinishValues(r) {
				t.Fatalf("markPending fast return/sticky %s %s rejected", before, reason)
			}
		}
	}
	max := "9223372036854775807"
	limit := sourceFinishLiteral("new_settlement")
	limit.ReservationAmount, limit.RawAmount, limit.ChargedAmount = max, max, max
	limit.BeforeKeyHeld, limit.AfterKeyHeld, limit.BeforeKeySpent, limit.AfterKeySpent = max, "0", "0", max
	if !sourceCustomerFinishValues(limit) {
		t.Fatal("int64 endpoint rejected")
	}
	limit = sourceFinishOverrun()
	limit.RawAmount, limit.WaivedAmount = "1"+strings.Repeat("0", 99), strings.Repeat("9", 98)+"0"
	if !sourceCustomerFinishValues(limit) {
		t.Fatal("numeric100 endpoint was truncated to money")
	}
}

func TestSourceFinishUnionExactNewModeReasonClockMatrix(t *testing.T) {
	// The accepted reason sets are written out for every pair of observations.
	// Equality is expired. No relative ordering of the two clocks is assumed.
	cases := []struct {
		mode              string
		decision, finance int
		reasons           []string
	}{
		{"recovery", 8, 8, []string{"charged_usage", "released_no_delivery"}},
		{"recovery", 8, 10, []string{"released_deadline"}},
		{"recovery", 8, 12, []string{"released_deadline"}},
		{"recovery", 10, 8, []string{"released_deadline"}},
		{"recovery", 10, 10, []string{"released_deadline"}},
		{"recovery", 10, 12, []string{"released_deadline"}},
		{"recovery", 12, 8, []string{"released_deadline"}},
		{"recovery", 12, 10, []string{"released_deadline"}},
		{"recovery", 12, 12, []string{"released_deadline"}},
		{"release", 0, 8, []string{"released_reconciliation"}},
		{"release", 0, 10, []string{"released_deadline"}},
		{"release", 0, 12, []string{"released_deadline"}},
	}
	for _, tc := range cases {
		for _, reason := range []string{"charged_usage", "released_no_delivery", "released_reconciliation", "released_deadline"} {
			r := sourceFinishLiteral("new_settlement")
			r.SelectedMode, r.SelectedReason, r.FinancialCheckedAt = tc.mode, reason, sourceFinishClock(tc.finance)
			r.SettlementDecisionCheckedAt = Time{}
			if tc.mode == "recovery" {
				r.SettlementDecisionCheckedAt = sourceFinishClock(tc.decision)
			}
			if reason != "charged_usage" {
				r.ResultStatus, r.AfterReservationStatus, r.AfterBillingStatus = "released", "released", "released"
				r.RawAmount, r.ChargedAmount, r.WaivedAmount, r.AfterKeySpent = "0", "0", "0", "2"
			}
			want := false
			for _, accepted := range tc.reasons {
				if reason == accepted {
					want = true
				}
			}
			if got := sourceCustomerFinishValues(r); got != want {
				t.Fatalf("%s D=%d F=%d reason=%s: valid=%v want=%v", tc.mode, tc.decision, tc.finance, reason, got, want)
			}
		}
	}
	// New-result reachability does not constrain the reason stored in an
	// existing historical settlement, whose two observation clocks are NULL.
	for _, reason := range []string{"charged_usage", "released_no_delivery", "released_reconciliation", "released_deadline", "arbitrary legacy reason"} {
		r := sourceFinishLiteral("existing_settlement")
		r.SelectedReason = reason
		if !sourceCustomerFinishValues(r) {
			t.Fatalf("new-result reason restriction leaked into existing: %s", reason)
		}
	}
}

func TestSourceFinishUnionFinishNullAndValueMutations(t *testing.T) {
	for _, name := range []string{"SettlementID", "CustomerJournalID", "BillingAuditID", "BillingOutboxID"} {
		r := sourceFinishLiteral("new_settlement")
		reflect.ValueOf(&r).Elem().FieldByName(name).SetZero()
		if sourceCustomerFinishValues(r) {
			t.Fatalf("new output NULL accepted: %s", name)
		}
	}
	profileFields := []string{"ProfileBlockModelID", "ProfileBlockProfileVersion", "ProfileBlockDisposition", "ProfileBlockAuditID"}
	for _, name := range profileFields {
		r := sourceFinishOverrun()
		reflect.ValueOf(&r).Elem().FieldByName(name).SetZero()
		if sourceCustomerFinishValues(r) {
			t.Fatalf("overrun partial profile accepted: %s", name)
		}
		r = sourceFinishLiteral("new_settlement")
		reflect.ValueOf(&r).Elem().FieldByName(name).Set(reflect.ValueOf(sourcePtr("extra")))
		if sourceCustomerFinishValues(r) {
			t.Fatalf("nonoverrun profile present: %s", name)
		}
	}
	invalidDisposition := sourceFinishOverrun()
	invalidDisposition.ProfileBlockDisposition = sourcePtr("updated")
	if sourceCustomerFinishValues(invalidDisposition) {
		t.Fatal("profile disposition was not closed")
	}
	for _, kind := range []string{"existing_settlement", "pending"} {
		for _, name := range append([]string{"CustomerJournalID", "CustomerTenantControlID", "CustomerKeyControlID", "BillingAuditID", "BillingOutboxID"}, profileFields...) {
			r := sourceFinishLiteral(kind)
			reflect.ValueOf(&r).Elem().FieldByName(name).Set(reflect.ValueOf(sourcePtr("extra")))
			if sourceCustomerFinishValues(r) {
				t.Fatalf("%s forbidden output %s", kind, name)
			}
		}
	}
	mutations := map[string]func(*CustomerFinishOutcomeRow){
		"wrong source":              func(r *CustomerFinishOutcomeRow) { r.SourceID = "other" },
		"unknown kind":              func(r *CustomerFinishOutcomeRow) { r.ResultKind = "refund" },
		"unknown mode":              func(r *CustomerFinishOutcomeRow) { r.SelectedMode = "other" },
		"wrong release mode reason": func(r *CustomerFinishOutcomeRow) { r.SelectedMode = "release"; r.SettlementDecisionCheckedAt = Time{} },
		"missing decision clock":    func(r *CustomerFinishOutcomeRow) { r.SettlementDecisionCheckedAt = Time{} },
		"missing financial clock":   func(r *CustomerFinishOutcomeRow) { r.FinancialCheckedAt = Time{} },
		"invalid null envelope":     func(r *CustomerFinishOutcomeRow) { r.FinancialCheckedAt.Valid = false },
		"infinite deadline":         func(r *CustomerFinishOutcomeRow) { r.ReservationDeadline.InfinityModifier = pgtype.Infinity },
		"nonmicrosecond clock": func(r *CustomerFinishOutcomeRow) {
			r.FinancialCheckedAt.Time = r.FinancialCheckedAt.Time.Add(time.Nanosecond)
		},
		"expired charge":        func(r *CustomerFinishOutcomeRow) { r.FinancialCheckedAt = sourceFinishClock(10) },
		"unknown reason":        func(r *CustomerFinishOutcomeRow) { r.SelectedReason = "other" },
		"wrong result status":   func(r *CustomerFinishOutcomeRow) { r.ResultStatus = "reserved" },
		"after reservation":     func(r *CustomerFinishOutcomeRow) { r.AfterReservationStatus = "released" },
		"after billing":         func(r *CustomerFinishOutcomeRow) { r.AfterBillingStatus = "released" },
		"case not closed":       func(r *CustomerFinishOutcomeRow) { r.AfterCaseState = "proposed" },
		"approved case lost":    func(r *CustomerFinishOutcomeRow) { r.BeforeCaseState = "approved" },
		"case reason changed":   func(r *CustomerFinishOutcomeRow) { r.AfterCaseReason = "other" },
		"request changed":       func(r *CustomerFinishOutcomeRow) { r.AfterRequestStatus = "failed" },
		"held arithmetic":       func(r *CustomerFinishOutcomeRow) { r.AfterKeyHeld = "6" },
		"spent arithmetic":      func(r *CustomerFinishOutcomeRow) { r.AfterKeySpent = "8" },
		"raw mismatch":          func(r *CustomerFinishOutcomeRow) { r.RawAmount = "8" },
		"waived mismatch":       func(r *CustomerFinishOutcomeRow) { r.WaivedAmount = "1" },
		"charge not minimum":    func(r *CustomerFinishOutcomeRow) { r.ChargedAmount = "6"; r.WaivedAmount = "1"; r.AfterKeySpent = "8" },
		"charge above reserved": func(r *CustomerFinishOutcomeRow) { r.RawAmount = "11"; r.ChargedAmount = "11"; r.AfterKeySpent = "13" },
		"unknown case":          func(r *CustomerFinishOutcomeRow) { r.BeforeCaseState = "other" },
		"unknown request":       func(r *CustomerFinishOutcomeRow) { r.BeforeRequestStatus = "sending"; r.AfterRequestStatus = "sending" },
		"unknown reservation":   func(r *CustomerFinishOutcomeRow) { r.BeforeReservationStatus = "other" },
		"unknown billing":       func(r *CustomerFinishOutcomeRow) { r.BeforeBillingStatus = "other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			r := sourceFinishLiteral("new_settlement")
			mutate(&r)
			if sourceCustomerFinishValues(r) {
				t.Fatal("invalid new finish accepted")
			}
		})
	}
	for _, field := range []string{"ReservationAmount", "ChargedAmount", "BeforeKeyHeld", "BeforeKeySpent", "AfterKeyHeld", "AfterKeySpent", "RawAmount", "WaivedAmount"} {
		for _, invalid := range []string{"", "NaN", "-1", "-0", "01", "+1", "1.0", "1e0", "null", strings.Repeat("9", 101)} {
			r := sourceFinishLiteral("new_settlement")
			reflect.ValueOf(&r).Elem().FieldByName(field).SetString(invalid)
			if sourceCustomerFinishValues(r) {
				t.Fatalf("invalid numeric %s %s", field, invalid)
			}
		}
	}
	for _, kind := range []string{"existing_settlement", "pending"} {
		for _, field := range []string{"AfterCaseState", "AfterCaseReason", "AfterReservationStatus", "AfterRequestStatus", "AfterBillingStatus", "AfterKeyHeld", "AfterKeySpent"} {
			r := sourceFinishLiteral(kind)
			v := reflect.ValueOf(&r).Elem().FieldByName(field)
			if strings.HasPrefix(field, "AfterKey") {
				v.SetString("1")
			} else {
				v.SetString("changed")
			}
			if sourceCustomerFinishValues(r) {
				t.Fatalf("%s wrong target %s", kind, field)
			}
		}
	}
	for _, mutate := range []func(*CustomerFinishOutcomeRow){
		func(r *CustomerFinishOutcomeRow) { r.SettlementID = nil },
		func(r *CustomerFinishOutcomeRow) { r.SettlementDecisionCheckedAt = sourceFinishClock(9) },
		func(r *CustomerFinishOutcomeRow) { r.FinancialCheckedAt = sourceFinishClock(9) },
		func(r *CustomerFinishOutcomeRow) { r.SelectedMode = "release" },
	} {
		r := sourceFinishLiteral("existing_settlement")
		mutate(&r)
		if sourceCustomerFinishValues(r) {
			t.Fatal("existing result contract violated")
		}
	}
	for _, mutate := range []func(*CustomerFinishOutcomeRow){
		func(r *CustomerFinishOutcomeRow) { r.SettlementID = sourcePtr("extra") },
		func(r *CustomerFinishOutcomeRow) { r.SettlementDecisionCheckedAt = Time{} },
		func(r *CustomerFinishOutcomeRow) { r.FinancialCheckedAt = sourceFinishClock(9) },
		func(r *CustomerFinishOutcomeRow) { r.SelectedMode = "release" },
		func(r *CustomerFinishOutcomeRow) { r.SelectedReason = "unknown" },
		func(r *CustomerFinishOutcomeRow) { r.ResultStatus = "released" },
		func(r *CustomerFinishOutcomeRow) { r.RawAmount = "1"; r.WaivedAmount = "1" },
	} {
		r := sourceFinishLiteral("pending")
		mutate(&r)
		if sourceCustomerFinishValues(r) {
			t.Fatal("pending result contract violated")
		}
	}
}

func TestSourceFinishUnionNaturalKeysAndIncompleteEntry(t *testing.T) {
	r, f := sourceReconcileLiteral("approve"), sourceFinishLiteral("new_settlement")
	rows := Rows{PrefixRows: PrefixRows{ReconciliationSources: []ReconciliationSourceRow{r}, CustomerFinishOutcomes: []CustomerFinishOutcomeRow{f}}}
	s := sourceTestRows(t, rows)
	s.bad = make(map[string]keySet)
	if err := s.finishUnionRows(); err != nil || len(s.bad["scalar"]) != 0 {
		t.Fatalf("literal rows marked invalid: %v %v", s.bad, err)
	}
	// Two physical tables may share the same natural ID. Multiple defects in
	// either row still contribute one per table, not one per failure reason.
	rows.PrefixRows.CustomerFinishOutcomes[0].ID = r.ID
	rows.PrefixRows.CustomerFinishOutcomes[0].AfterKeyHeld = "6"
	rows.PrefixRows.CustomerFinishOutcomes[0].AfterKeySpent = "8"
	rows.PrefixRows.ReconciliationSources[0].ActorRole = "wrong"
	rows.PrefixRows.ReconciliationSources[0].CaseTotalTokens = sourcePtr("9")
	rows.PrefixRows.ReconciliationSources = append(rows.PrefixRows.ReconciliationSources, rows.PrefixRows.ReconciliationSources[0])
	s = sourceTestRows(t, rows)
	s.bad = make(map[string]keySet)
	if err := s.finishUnionRows(); err != nil {
		t.Fatal(err)
	}
	if err := s.finishUnionRows(); err != nil || len(s.bad["scalar"]) != 2 {
		t.Fatalf("natural keys failed to deduplicate: %v %v", s.bad, err)
	}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := checkSourceClosure(w, rows)
	if err != nil || got.Stats.InvalidScalarRows != 2 || got.Complete || len(got.Qualified) != 0 || len(got.Unimplemented) == 0 {
		t.Fatalf("partial source leaf changed proof gate: %+v %v", got, err)
	}
}

func TestSourceFinishUnionCancellationResourceAndZeroResults(t *testing.T) {
	if err := (*sourceClosureState)(nil).finishUnionRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil work did not fail closed")
	}
	rows := Rows{PrefixRows: PrefixRows{CustomerFinishOutcomes: []CustomerFinishOutcomeRow{sourceFinishLiteral("new_settlement")}}}
	s := sourceTestRows(t, rows)
	s.w.b.deadline = time.Now().Add(-time.Second)
	if err := s.finishUnionRows(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("deadline was restarted: %v", err)
	}
	rows.PrefixRows.CustomerFinishOutcomes = make([]CustomerFinishOutcomeRow, 600)
	for i := range rows.PrefixRows.CustomerFinishOutcomes {
		rows.PrefixRows.CustomerFinishOutcomes[i] = sourceFinishLiteral("new_settlement")
	}
	s = sourceTestRows(t, rows)
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 3}
	if err := s.finishUnionRows(); !errors.Is(err, ErrCanceled) {
		t.Fatalf("leaf loop ignored shared cancellation: %v", err)
	}
	for _, which := range []string{"cancel", "timeout", "resource"} {
		input := Rows{PrefixRows: PrefixRows{CustomerFinishOutcomes: []CustomerFinishOutcomeRow{sourceFinishLiteral("new_settlement")}}}
		w := sourceUsageWork(t)
		want := ErrCanceled
		switch which {
		case "cancel":
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			w.b.ctx = ctx
		case "timeout":
			w.b.deadline, want = time.Now().Add(-time.Second), ErrTimeout
		case "resource":
			input.PrefixRows.CustomerFinishOutcomes[0].SelectedReason, want = strings.Repeat("x", int(fieldLimit)+1), ErrResource
		}
		got, err := checkSourceClosure(w, input)
		if !errors.Is(err, want) || !reflect.DeepEqual(got, sourceClosureResult{}) {
			t.Fatalf("%s exposed partial result: %+v %v", which, got, err)
		}
	}
}
