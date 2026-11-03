package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ffPtr(s string) *string    { return &s }
func ffTime(seconds int64) Time { return Time{Time: time.Unix(seconds, 0).UTC(), Valid: true} }
func ffAudit(id, actor, action, target, role, payload string) AuditEventRow {
	return AuditEventRow{ID: id, Actor: actor, TenantScope: ffPtr("tenant"), Action: action, Target: target, RequestID: "request", SanitizedDiff: payload, PPRootID: ffPtr("root"), PPRootStep: ffPtr("1"), PPSourceKind: ffPtr("reconciliation"), PPSourceID: ffPtr("source"), PPSourceRole: ffPtr(role), PPCreatedXID: ffPtr("123")}
}
func ffFixture(action string) Rows {
	q := RequestRecordRow{ID: "request", TenantID: "tenant", KeyID: "key", ModelID: "model", ProfileVersion: "profile", InputRate: "400000", OutputRate: "900000"}
	s := ReconciliationSourceRow{ID: "source", RootID: "root", StepNo: "1", StepKind: "reconciliation", CreatedXID: "123", TenantID: "tenant", RequestID: "request", CaseID: "case", Action: action, Actor: "checker", ActorRole: "finance_approver", ActorStatus: "active", BeforeCaseState: "open", AfterCaseState: "closed", BeforeCaseReason: "delivery_unknown", CaseCreatedAt: ffTime(1), BeforeReservationStatus: "pending_reconciliation", ReservationDeadline: ffTime(100), CustomerFinishOutcomeID: ffPtr("outcome"), AuditID: "reconcile-audit", InputReason: ffPtr("manual-check")}
	o := CustomerFinishOutcomeRow{ID: "outcome", RootID: "root", StepNo: "1", StepKind: "reconciliation", SourceID: "source", CreatedXID: "123", TenantID: "tenant", RequestID: "request", ReconciliationSourceID: "source", CaseID: "case", CustomerReservationID: "reservation", ReservationAmount: "10", ReservationDeadline: ffTime(100), ResultKind: "new_settlement", SelectedMode: "release", SelectedReason: "released_reconciliation", FinancialCheckedAt: ffTime(50), BeforeCaseState: "open", AfterCaseState: "closed", BeforeCaseReason: "delivery_unknown", AfterCaseReason: "delivery_unknown", BeforeReservationStatus: "pending_reconciliation", AfterReservationStatus: "released", BeforeRequestStatus: "dispatching", AfterRequestStatus: "failed", BeforeBillingStatus: "pending_reconciliation", AfterBillingStatus: "released", BeforeKeyHeld: "50", AfterKeyHeld: "40", BeforeKeySpent: "7", AfterKeySpent: "7", RawAmount: "0", ChargedAmount: "0", WaivedAmount: "0", ResultStatus: "released", SettlementID: ffPtr("settlement"), CustomerJournalID: ffPtr("journal"), BillingAuditID: ffPtr("billing-audit"), BillingOutboxID: ffPtr("outbox"), DNewSettlement: ffPtr("settlement")}
	usage := `{"status":"confirmed_final","input_tokens":3,"output_tokens":5,"total_tokens":8,"cached_tokens":1,"reasoning_tokens":2}`
	originals := OriginalRows{RequestRecords: []RequestRecordRow{q}, Reservations: []ReservationRow{{ID: "reservation", TenantID: "tenant", RequestID: "request", Amount: "10", Status: "released", Deadline: ffTime(100)}}, ReconciliationCases: []ReconciliationCaseRow{{ID: "case", TenantID: "tenant", RequestID: "request", CreatedAt: ffTime(1)}}, AuditEvents: []AuditEventRow{ffAudit("reconcile-audit", "checker", "reconciliation.released", "case", "reconciliation", `{"reason":"manual-check"}`)}}
	prefix := PrefixRows{}
	if action == "approve" {
		s.InputReason = nil
		s.BeforeCaseState = "proposed"
		s.AfterCaseState = "approved"
		s.BeforeRequester = ffPtr("maker")
		s.AfterRequester = ffPtr("maker")
		s.AfterApprover = ffPtr("checker")
		s.RequesterRole = ffPtr("finance_operator")
		s.RequesterStatus = ffPtr("active")
		s.CaseUsageStatus = ffPtr("confirmed_final")
		s.CaseInputTokens = ffPtr("3")
		s.CaseOutputTokens = ffPtr("5")
		s.CaseTotalTokens = ffPtr("8")
		s.CaseCachedTokens = ffPtr("1")
		s.CaseReasoningTokens = ffPtr("2")
		s.CaseEvidenceRef = ffPtr("invoice:1")
		s.CaseProposalReason = ffPtr("verified")
		s.ApprovalCheckedAt = ffTime(40)
		s.PriorLatestID = ffPtr("prior")
		s.DerivedIncomingSourceID = ffPtr("incoming")
		s.FinalizationID = ffPtr("final")
		o.LatestFinalizationID = ffPtr("final")
		o.SelectedMode = "recovery"
		o.SelectedReason = "charged_usage"
		o.SettlementDecisionCheckedAt = ffTime(45)
		o.BeforeCaseState = "approved"
		o.AfterCaseState = "approved"
		o.AfterReservationStatus = "settled"
		o.AfterBillingStatus = "settled"
		o.AfterRequestStatus = "dispatching"
		o.RawAmount = "6"
		o.ChargedAmount = "6"
		o.AfterKeySpent = "13"
		o.ResultStatus = "settled"
		originals.FinalizationRecords = []FinalizationRecordRow{{ID: "prior", TenantID: "tenant", RequestID: "request", ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true, FinishReason: "stop"}, {ID: "final", TenantID: "tenant", RequestID: "request", EventID: "meter", ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "content", ContentWritten: true, FinishReason: "stop"}}
		originals.MeteringEvents = []MeteringEventRow{{ID: "meter", TenantID: "tenant", RequestID: "request", NormalizedUsage: usage}}
		prefix.IncomingSources = []IncomingSourceRow{{ID: "incoming", RootID: "root", StepNo: "2", StepKind: "incoming", CreatedXID: "123", TenantID: "tenant", RequestID: "request", InputFinalizationID: "final", Source: "approved_reconciliation", SourceEventID: "case", UsageStatus: "confirmed_final", InputTokens: "3", OutputTokens: "5", TotalTokens: "8", CachedTokens: "1", ReasoningTokens: "2", ProtocolOutcome: "completed", TransportEndReason: "done", RawDeliveryStage: "content", RawContentWritten: true, FinishReason: "stop", ReconcileSourceID: ffPtr("source")}}
		originals.AuditEvents[0] = ffAudit("reconcile-audit", "checker", "reconciliation.approved", "case", "reconciliation", `{"outcome":"settled"}`)
	}
	if action == "propose" {
		s.Actor = "maker"
		s.ActorRole = "finance_operator"
		s.AfterCaseState = "proposed"
		s.AfterRequester = ffPtr("maker")
		s.InputUsageStatus = ffPtr("confirmed_final")
		s.InputInputTokens = ffPtr("3")
		s.InputOutputTokens = ffPtr("5")
		s.InputTotalTokens = ffPtr("8")
		s.InputCachedTokens = ffPtr("1")
		s.InputReasoningTokens = ffPtr("2")
		s.InputEvidenceRef = ffPtr("invoice:1")
		s.CustomerFinishOutcomeID = nil
		originals.AuditEvents[0] = ffAudit("reconcile-audit", "maker", "reconciliation.proposed", "case", "reconciliation", `{"usage":`+usage+`,"evidence_ref":"invoice:1","reason":"manual-check"}`)
		prefix.ReconciliationSources = []ReconciliationSourceRow{s}
		return Rows{OriginalRows: originals, PrefixRows: prefix}
	}
	settlement := SettlementRow{ID: "settlement", TenantID: "tenant", RequestID: "request", FinalizationID: o.LatestFinalizationID, ChargedAmount: o.ChargedAmount, RawRetailAmount: o.RawAmount, WaivedRetailAmount: o.WaivedAmount, RefundedAmount: "0", Status: o.ResultStatus, Reason: o.SelectedReason, JournalID: "journal", PPRootID: ffPtr("root"), PPRootStep: ffPtr("1"), PPSourceKind: ffPtr("reconciliation"), PPSourceID: ffPtr("source"), PPSourceRole: ffPtr("settlement"), PPCreatedXID: ffPtr("123")}
	originals.Settlements = []SettlementRow{settlement}
	originals.Journals = []JournalRow{{ID: "journal", TenantID: "tenant", OperationID: "settlement", Kind: o.ResultStatus, Reference: "request", Actor: "system", Currency: "CNY", CreatedTxid: "123", PPRootID: ffPtr("root"), PPRootStep: ffPtr("1"), PPSourceKind: ffPtr("reconciliation"), PPSourceID: ffPtr("source"), PPSourceRole: ffPtr("customer_journal"), PPCreatedXID: ffPtr("123")}}
	originals.AuditEvents = append(originals.AuditEvents, ffAudit("billing-audit", "system", "billing."+o.ResultStatus, "settlement", "settlement", `{"amount":"`+o.ChargedAmount+`","reason":"`+o.SelectedReason+`"}`))
	released := "10"
	if action == "approve" {
		released = "4"
	}
	originals.ProjectionOutbox = []ProjectionOutboxRow{{EventID: "outbox", TenantID: "tenant", OperationID: "settlement", Kind: o.ResultStatus, SchemaVersion: "1", SanitizedPayload: `{"amount":"` + o.ChargedAmount + `","released":"` + released + `","request_id":"request"}`, PPRootID: ffPtr("root"), PPRootStep: ffPtr("1"), PPSourceKind: ffPtr("reconciliation"), PPSourceID: ffPtr("source"), PPSourceRole: ffPtr("settlement"), PPCreatedXID: ffPtr("123")}}
	deltas := []string{"-10", released}
	kinds := []string{"HELD", "AVAILABLE"}
	afters := []string{"90", "110"}
	if action == "approve" {
		kinds = append(kinds, "REVENUE")
		deltas = append(deltas, "6")
		afters = []string{"90", "104", "106"}
	}
	for i, kind := range kinds {
		id := strings.ToLower(kind)
		tenant := ffPtr("tenant")
		if kind == "REVENUE" {
			tenant = nil
		}
		originals.LedgerAccounts = append(originals.LedgerAccounts, LedgerAccountRow{ID: id, TenantID: tenant, Kind: kind, Balance: "999"})
		originals.JournalLines = append(originals.JournalLines, JournalLineRow{ID: "line-" + id, JournalID: "journal", AccountID: id, SignedDelta: deltas[i], PPRootID: ffPtr("root"), PPRootStep: ffPtr("1"), PPSourceKind: ffPtr("reconciliation"), PPSourceID: ffPtr("source"), PPSourceRole: ffPtr("ledger_line"), PPCreatedXID: ffPtr("123")})
		prefix.CustomerFinishLedgerEffects = append(prefix.CustomerFinishLedgerEffects, CustomerFinishLedgerEffectRow{FinishOutcomeID: "outcome", RootID: "root", StepNo: "1", StepKind: "reconciliation", SourceID: "source", CreatedXID: "123", TenantID: "tenant", AccountID: id, AccountTenantID: tenant, AccountKind: kind, BeforeBalance: "100", AfterBalance: afters[i], SignedDelta: deltas[i], JournalLineID: "line-" + id})
	}
	prefix.ReconciliationSources = []ReconciliationSourceRow{s}
	prefix.CustomerFinishOutcomes = []CustomerFinishOutcomeRow{o}
	return Rows{OriginalRows: originals, PrefixRows: prefix}
}
func ffCheck(t *testing.T, rows Rows) reconciliationFinishResult {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := checkReconciliationFinish(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func ffClean(t *testing.T, rows Rows) {
	t.Helper()
	v := ffCheck(t, rows)
	if v.Stats != (AttemptCost{}) {
		t.Fatalf("mismatch: %+v", v.Stats)
	}
	if v.Complete || len(v.Unimplemented) != 3 {
		t.Fatalf("partial domain issued complete: %+v", v)
	}
}
func TestReconciliationFinishIndependentBranches(t *testing.T) {
	for _, action := range []string{"propose", "release", "approve"} {
		t.Run(action, func(t *testing.T) { ffClean(t, ffFixture(action)) })
	}
	t.Run("propose_deadline_binds_original", func(t *testing.T) {
		v := ffFixture("propose")
		v.PrefixRows.ReconciliationSources[0].ReservationDeadline = ffTime(200)
		if n := ffCheck(t, v).Stats.ReconciliationRows; n != 1 {
			t.Fatalf("forged proposal deadline accepted: %d", n)
		}
	})
	t.Run("approved_release_retains_case", func(t *testing.T) {
		v := ffFixture("release")
		s := &v.PrefixRows.ReconciliationSources[0]
		s.BeforeCaseState = "approved"
		s.AfterCaseState = "approved"
		s.BeforeRequester = ffPtr("same_checker")
		s.AfterRequester = s.BeforeRequester
		s.BeforeApprover = ffPtr("checker")
		s.AfterApprover = s.BeforeApprover
		o := &v.PrefixRows.CustomerFinishOutcomes[0]
		o.BeforeCaseState = "approved"
		o.AfterCaseState = "approved"
		ffClean(t, v)
	})
	t.Run("current_balances_refunds_not_historical", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.Settlements[0].RefundedAmount = "6"
		v.OriginalRows.APIKeys = []APIKeyRow{{ID: "key", TenantID: "tenant", Held: "9999", Spent: "9999"}}
		ffClean(t, v)
	})
}

func TestReconciliationFinishRejectsNaturalObjectMutations(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*Rows)
		counter string
	}{
		{"self_approve", func(v *Rows) { v.PrefixRows.ReconciliationSources[0].BeforeRequester = ffPtr("checker") }, "source"},
		{"maker_revoked", func(v *Rows) { v.PrefixRows.ReconciliationSources[0].RequesterStatus = ffPtr("disabled") }, "source"},
		{"ignored_input_not_typed_null", func(v *Rows) { v.PrefixRows.ReconciliationSources[0].InputReason = ffPtr("ignored") }, "source"},
		{"manufactured_delivery", func(v *Rows) { v.PrefixRows.IncomingSources[0].RawDeliveryStage = "none" }, "source"},
		{"wrong_usage", func(v *Rows) { v.PrefixRows.IncomingSources[0].InputTokens = "4" }, "source"},
		{"wrong_clock", func(v *Rows) { v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(43) }, "finish"},
		{"expired_charge", func(v *Rows) { v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(100) }, "finish"},
		{"ceil_per_dimension", func(v *Rows) { v.PrefixRows.CustomerFinishOutcomes[0].RawAmount = "7" }, "finish"},
		{"key_hold", func(v *Rows) { v.PrefixRows.CustomerFinishOutcomes[0].AfterKeyHeld = "41" }, "finish"},
		{"wrong_settlement", func(v *Rows) { v.OriginalRows.Settlements[0].RawRetailAmount = "5" }, "finish"},
		{"numeric_money_payload", func(v *Rows) { v.OriginalRows.AuditEvents[1].SanitizedDiff = `{"amount":6,"reason":"charged_usage"}` }, "finish"},
		{"ledger_delta", func(v *Rows) { v.PrefixRows.CustomerFinishLedgerEffects[0].SignedDelta = "-9" }, "ledger"},
		{"ledger_before_overflow", func(v *Rows) { v.PrefixRows.CustomerFinishLedgerEffects[0].BeforeBalance = "9223372036854775808" }, "ledger"},
		{"platform_revenue_is_not_tenant", func(v *Rows) { v.PrefixRows.CustomerFinishLedgerEffects[2].AccountTenantID = ffPtr("tenant") }, "ledger"},
		{"missing_zero_available_line", func(v *Rows) { v.PrefixRows.CustomerFinishLedgerEffects = v.PrefixRows.CustomerFinishLedgerEffects[:1] }, "ledger"},
		{"extra_original_line", func(v *Rows) {
			v.OriginalRows.JournalLines = append(v.OriginalRows.JournalLines, JournalLineRow{ID: "extra", JournalID: "journal"})
		}, "ledger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := ffFixture("approve")
			tt.change(&v)
			s := ffCheck(t, v).Stats
			var count uint64
			switch tt.counter {
			case "source":
				count = s.ReconciliationRows
			case "finish":
				count = s.CustomerFinishRows
			case "ledger":
				count = s.CustomerLedgerRows
			}
			if count == 0 {
				t.Fatalf("mutation accepted: %+v", s)
			}
		})
	}
}

func ffPending(v *Rows, reason string) {
	o := &v.PrefixRows.CustomerFinishOutcomes[0]
	o.ResultKind = "pending"
	o.SelectedReason = reason
	o.ResultStatus = "pending_reconciliation"
	o.FinancialCheckedAt = Time{}
	o.RawAmount = "0"
	o.ChargedAmount = "0"
	o.WaivedAmount = "0"
	o.SettlementID = nil
	o.CustomerJournalID = nil
	o.BillingAuditID = nil
	o.BillingOutboxID = nil
	o.DNewSettlement = nil
	o.AfterReservationStatus = o.BeforeReservationStatus
	o.AfterBillingStatus = "pending_reconciliation"
	o.AfterRequestStatus = o.BeforeRequestStatus
	o.AfterCaseReason = reason
	o.AfterCaseState = o.BeforeCaseState
	o.AfterKeyHeld = o.BeforeKeyHeld
	o.AfterKeySpent = o.BeforeKeySpent
	v.OriginalRows.AuditEvents = v.OriginalRows.AuditEvents[:1]
	v.OriginalRows.AuditEvents[0].SanitizedDiff = `{"outcome":"pending_reconciliation"}`
	v.OriginalRows.JournalLines = nil
	v.OriginalRows.Journals = nil
	v.OriginalRows.Settlements = nil
	v.OriginalRows.ProjectionOutbox = nil
	v.PrefixRows.CustomerFinishLedgerEffects = nil
}
func TestReconciliationFinishPendingAndExisting(t *testing.T) {
	t.Run("pending_changes_reason", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.FinalizationRecords[1].DeliveryStage = "unknown"
		v.OriginalRows.FinalizationRecords[1].ContentWritten = false
		ffPending(&v, "delivery_unknown")
		v.PrefixRows.CustomerFinishOutcomes[0].BeforeBillingStatus = "held"
		ffClean(t, v)
		v.PrefixRows.CustomerFinishOutcomes[0].AfterBillingStatus = "held"
		if ffCheck(t, v).Stats.CustomerFinishRows != 1 {
			t.Fatal("pending update erased")
		}
	})
	t.Run("pending_preserves_conflict_reason", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.FinalizationRecords[1].DeliveryStage = "unknown"
		v.OriginalRows.FinalizationRecords[1].ContentWritten = false
		ffPending(&v, "delivery_unknown")
		v.PrefixRows.ReconciliationSources[0].BeforeCaseReason = "metering_conflict"
		o := &v.PrefixRows.CustomerFinishOutcomes[0]
		o.BeforeCaseReason = "metering_conflict"
		o.AfterCaseReason = "metering_conflict"
		ffClean(t, v)
	})
	t.Run("existing_no_new_effect", func(t *testing.T) {
		v := ffFixture("approve")
		o := &v.PrefixRows.CustomerFinishOutcomes[0]
		o.ResultKind = "existing_settlement"
		o.FinancialCheckedAt = Time{}
		o.SettlementDecisionCheckedAt = Time{}
		o.CustomerJournalID = nil
		o.BillingAuditID = nil
		o.BillingOutboxID = nil
		o.DNewSettlement = nil
		o.AfterCaseState = o.BeforeCaseState
		o.AfterCaseReason = o.BeforeCaseReason
		o.AfterReservationStatus = o.BeforeReservationStatus
		o.AfterBillingStatus = o.BeforeBillingStatus
		o.AfterRequestStatus = o.BeforeRequestStatus
		o.AfterKeyHeld = o.BeforeKeyHeld
		o.AfterKeySpent = o.BeforeKeySpent
		v.OriginalRows.AuditEvents = v.OriginalRows.AuditEvents[:1]
		v.PrefixRows.CustomerFinishLedgerEffects = nil
		v.OriginalRows.JournalLines = nil
		ffClean(t, v)
		v.PrefixRows.CustomerFinishLedgerEffects = []CustomerFinishLedgerEffectRow{{FinishOutcomeID: "outcome", AccountID: "forged"}}
		if ffCheck(t, v).Stats.CustomerLedgerRows != 1 {
			t.Fatal("existing forged effect accepted")
		}
	})
}

func TestReconciliationFinishStrictPayloads(t *testing.T) {
	for _, raw := range []string{`{"amount":"6","amount":"6","reason":"charged_usage"}`, `{"amount":"6","\u0061mount":"6","reason":"charged_usage"}`, `{"amount":"6","reason":"charged_usage","extra":0}`, `{"amount":"6","reason":"charged_usage"} {}`, `{"amount":"6","reason":"\ud800"}`, `{"amount":"6","reason":"\udc00"}`, `{"amount":"6","reason":"\u0000"}`} {
		t.Run(raw, func(t *testing.T) {
			v := ffFixture("approve")
			v.OriginalRows.AuditEvents[1].SanitizedDiff = raw
			if ffCheck(t, v).Stats.CustomerFinishRows != 1 {
				t.Fatal("malformed payload accepted")
			}
		})
	}
}
func TestReconciliationFinishCounterNaturalKeys(t *testing.T) {
	t.Run("wrong_kind_is_one_account", func(t *testing.T) {
		v := ffFixture("approve")
		v.PrefixRows.CustomerFinishLedgerEffects[0].AccountKind = "BAD"
		if n := ffCheck(t, v).Stats.CustomerLedgerRows; n != 1 {
			t.Fatalf("one account kind defect counted %d", n)
		}
	})
	v := ffFixture("approve")
	e := &v.PrefixRows.CustomerFinishLedgerEffects[0]
	e.AfterBalance = "1"
	e.BeforeBalance = "2"
	e.SignedDelta = "4"
	if n := ffCheck(t, v).Stats.CustomerLedgerRows; n != 1 {
		t.Fatalf("one ledger effect counted %d times", n)
	}
	// An unrelated original line with the same account text remains another
	// physical object; it must not be merged into the effect's natural key.
	v.OriginalRows.JournalLines = append(v.OriginalRows.JournalLines, JournalLineRow{ID: "held", JournalID: "journal"})
	if n := ffCheck(t, v).Stats.CustomerLedgerRows; n != 2 {
		t.Fatalf("effect and orphan line merged: %d", n)
	}
	// The same outcome with several bad fields remains a single finish object.
	v = ffFixture("approve")
	o := &v.PrefixRows.CustomerFinishOutcomes[0]
	o.RawAmount = "7"
	o.AfterKeySpent = "90"
	o.AfterBillingStatus = "released"
	o.AfterRequestStatus = "failed"
	if n := ffCheck(t, v).Stats.CustomerFinishRows; n != 1 {
		t.Fatalf("one outcome counted %d times", n)
	}
}
func TestReconciliationFinishCancelAndZeroResult(t *testing.T) {
	v := ffFixture("approve")
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	got, err := checkReconciliationFinish(w, v)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, reconciliationFinishResult{}) {
		t.Fatalf("cancellation result: %+v %v", got, err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	w = &replayWork{b: budget{ctx: ctx, deadline: time.Now().Add(-time.Second)}}
	got, err = checkReconciliationFinish(w, v)
	if !errors.Is(err, ErrTimeout) || !reflect.DeepEqual(got, reconciliationFinishResult{}) {
		t.Fatalf("timeout result: %+v %v", got, err)
	}
}
