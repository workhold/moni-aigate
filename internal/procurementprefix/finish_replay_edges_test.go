package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// Hand supplied expected amounts for the fixed R=10 fixture. This is fixture
// construction, not an implementation of pricing or settlement selection.
func ffRewrite(v *Rows, reason, status, raw, charge, waived, spent, available, afterAvailable, revenueAfter string) {
	o := &v.PrefixRows.CustomerFinishOutcomes[0]
	o.SelectedReason = reason
	o.ResultStatus = status
	o.RawAmount = raw
	o.ChargedAmount = charge
	o.WaivedAmount = waived
	o.AfterKeySpent = spent
	o.AfterReservationStatus = status
	o.AfterBillingStatus = status
	if status == "released" {
		o.AfterRequestStatus = "failed"
	}
	s := &v.OriginalRows.Settlements[0]
	s.Reason = reason
	s.Status = status
	s.RawRetailAmount = raw
	s.ChargedAmount = charge
	s.WaivedRetailAmount = waived
	v.OriginalRows.Journals[0].Kind = status
	v.OriginalRows.AuditEvents[0].SanitizedDiff = `{"outcome":"` + status + `"}`
	v.OriginalRows.AuditEvents[1].Action = "billing." + status
	v.OriginalRows.AuditEvents[1].SanitizedDiff = `{"amount":"` + charge + `","reason":"` + reason + `"}`
	v.OriginalRows.ProjectionOutbox[0].Kind = status
	v.OriginalRows.ProjectionOutbox[0].SanitizedPayload = `{"amount":"` + charge + `","released":"` + available + `","request_id":"request"}`
	v.OriginalRows.JournalLines[1].SignedDelta = available
	v.PrefixRows.CustomerFinishLedgerEffects[1].SignedDelta = available
	v.PrefixRows.CustomerFinishLedgerEffects[1].AfterBalance = afterAvailable
	if charge == "0" {
		v.OriginalRows.JournalLines = v.OriginalRows.JournalLines[:2]
		v.PrefixRows.CustomerFinishLedgerEffects = v.PrefixRows.CustomerFinishLedgerEffects[:2]
	} else {
		v.OriginalRows.JournalLines[2].SignedDelta = charge
		v.PrefixRows.CustomerFinishLedgerEffects[2].SignedDelta = charge
		v.PrefixRows.CustomerFinishLedgerEffects[2].AfterBalance = revenueAfter
	}
}
func TestReconciliationFinishFinancialClockAndProfile(t *testing.T) {
	t.Run("final_deadline_resets_raw_too", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.RequestRecords[0].InputRate = "6000000"
		v.OriginalRows.RequestRecords[0].OutputRate = "9000000"
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(100)
		ffRewrite(&v, "released_deadline", "released", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v)
	})
	t.Run("zero_price_still_settles_with_two_zero_lines", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.RequestRecords[0].InputRate = "0"
		v.OriginalRows.RequestRecords[0].OutputRate = "0"
		ffRewrite(&v, "charged_usage", "settled", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v)
	})
	t.Run("charge_equals_R_retains_zero_available", func(t *testing.T) {
		v := ffFixture("approve")
		v.OriginalRows.RequestRecords[0].InputRate = "6000000"
		v.OriginalRows.RequestRecords[0].OutputRate = "9000000"
		ffRewrite(&v, "charged_usage", "settled", "63", "10", "53", "17", "0", "100", "110")
		o := &v.PrefixRows.CustomerFinishOutcomes[0]
		o.ProfileBlockModelID = ffPtr("model")
		o.ProfileBlockProfileVersion = ffPtr("profile")
		o.ProfileBlockDisposition = ffPtr("reused")
		o.ProfileBlockAuditID = ffPtr("profile-audit")
		v.OriginalRows.BillingProfileBlocks = []BillingProfileBlockRow{{ModelID: "model", ProfileVersion: "profile", Reason: "usage_exceeded_reservation", RequestID: "older-request"}}
		v.OriginalRows.AuditEvents = append(v.OriginalRows.AuditEvents, ffAudit("profile-audit", "system", "billing.profile_blocked", "model", "profile_block", `{"waived":"53"}`))
		ffClean(t, v)
		o.ProfileBlockDisposition = ffPtr("created")
		if ffCheck(t, v).Stats.CustomerFinishRows != 1 {
			t.Fatal("created block with other request accepted")
		}
	})
	t.Run("approval_at_deadline_no_derived_incoming", func(t *testing.T) {
		v := ffFixture("approve")
		s := &v.PrefixRows.ReconciliationSources[0]
		s.ApprovalCheckedAt = ffTime(100)
		s.DerivedIncomingSourceID = nil
		s.FinalizationID = nil
		o := &v.PrefixRows.CustomerFinishOutcomes[0]
		o.LatestFinalizationID = ffPtr("prior")
		o.SettlementDecisionCheckedAt = ffTime(100)
		o.FinancialCheckedAt = ffTime(101)
		v.OriginalRows.Settlements[0].FinalizationID = o.LatestFinalizationID
		v.PrefixRows.IncomingSources = nil
		ffRewrite(&v, "released_deadline", "released", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v)
	})
	t.Run("transport_cancel_charges_even_without_content", func(t *testing.T) {
		v := ffFixture("approve")
		f := &v.OriginalRows.FinalizationRecords[1]
		f.ProtocolOutcome = "cancelled"
		f.TransportEndReason = "client_cancelled"
		f.ContentWritten = false
		f.DeliveryStage = "none"
		f.FinishReason = "not_sent"
		ffClean(t, v)
	})
}
func ffPB(v *Rows, legacy bool) {
	o := &v.PrefixRows.CustomerFinishOutcomes[0]
	q := &v.OriginalRows.RequestRecords[0]
	o.CustomerTenantControlID = ffPtr("tenant-control")
	o.CustomerKeyControlID = ffPtr("key-control")
	q.PBCreatedXID = ffPtr("17")
	q.PBAdmittedAt = ffTime(10)
	for _, scope := range []string{"tenant", "key"} {
		control := scope + "-control"
		allocation := scope + "-allocation"
		window := scope + "-window"
		key := (*string)(nil)
		if scope == "key" {
			key = ffPtr("key")
		}
		v.OriginalRows.PeriodicBudgetControls = append(v.OriginalRows.PeriodicBudgetControls, PeriodicBudgetControlRow{ID: control, Scope: scope, TenantID: ffPtr("tenant"), KeyID: key, CreatedXID: "17"})
		a := PeriodicBudgetAllocationRow{ID: allocation, ControlID: control, SubjectType: "customer_request", SubjectID: "request", Origin: "new", OriginSourceID: "reservation", UpperAmount: "10", Baseline: "0", WindowID: ffPtr(window), AdmittedAt: ffTime(10), CreatedXID: "17"}
		first := PeriodicBudgetEventRow{ID: scope + "-reserve", ControlID: control, AllocationID: allocation, SourceKind: "reservation", SourceID: "reservation", AllocationBeforeRevision: "0", WindowBeforeRevision: ffPtr("0"), BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "10", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "held", HeldDelta: "10", SpentDelta: "0", CreatedXID: "17"}
		last := PeriodicBudgetEventRow{ID: scope + "-finish", ControlID: control, AllocationID: allocation, SourceKind: "settlement", SourceID: "settlement", AllocationBeforeRevision: "1", WindowBeforeRevision: ffPtr("2"), BeforeHeld: "10", BeforeSpent: "0", AfterHeld: "0", AfterSpent: "6", AfterSourceSpent: "6", AfterState: "settled", HeldDelta: "-10", SpentDelta: "6", CreatedXID: "123"}
		if legacy {
			a.Origin = "legacy_import"
			a.OriginSourceID = "request"
			a.WindowID = nil
			a.AdmittedAt = Time{}
			first.SourceKind = "legacy_import"
			first.SourceID = "request"
			first.WindowBeforeRevision = nil
			last.WindowBeforeRevision = nil
		} else {
			v.OriginalRows.PeriodicBudgetWindows = append(v.OriginalRows.PeriodicBudgetWindows, PeriodicBudgetWindowRow{ID: window, ControlID: control, StartAt: ffTime(0), EndAt: ffTime(20)})
			if scope == "tenant" {
				q.PBTenantControl = ffPtr(control)
			} else {
				q.PBKeyControl = ffPtr(control)
			}
		}
		v.OriginalRows.PeriodicBudgetAllocations = append(v.OriginalRows.PeriodicBudgetAllocations, a)
		v.OriginalRows.PeriodicBudgetEvents = append(v.OriginalRows.PeriodicBudgetEvents, first, last)
		v.PrefixRows.CustomerFinishPBEffects = append(v.PrefixRows.CustomerFinishPBEffects, CustomerFinishPBEffectRow{FinishOutcomeID: "outcome", RootID: "root", StepNo: "1", StepKind: "reconciliation", SourceID: "source", CreatedXID: "123", Scope: scope, ControlID: control, AllocationID: allocation, EventID: last.ID, OriginalSourceKind: "settlement", OriginalSourceID: "settlement"})
	}
}
func TestReconciliationFinishDualScopePB(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "new"
		if legacy {
			name = "legacy_import"
		}
		t.Run(name, func(t *testing.T) { v := ffFixture("approve"); ffPB(&v, legacy); ffClean(t, v) })
	}
	mutations := []struct {
		name   string
		mutate func(*Rows)
	}{
		{"missing_scope", func(v *Rows) { v.PrefixRows.CustomerFinishPBEffects = v.PrefixRows.CustomerFinishPBEffects[:1] }},
		{"wrong_scope_event", func(v *Rows) { v.PrefixRows.CustomerFinishPBEffects[1].EventID = "tenant-finish" }},
		{"event_delta", func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[1].SpentDelta = "5" }},
		{"no_original_reserve", func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents = v.OriginalRows.PeriodicBudgetEvents[1:] }},
		{"reset_current_window", func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].WindowID = ffPtr("future") }},
		{"baseline", func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].Baseline = "1" }},
		{"no_customer_catchup", func(v *Rows) { v.OriginalRows.PeriodicBudgetAllocations[0].Origin = "legacy_catchup" }},
		{"wrong_creation_xid", func(v *Rows) { v.OriginalRows.PeriodicBudgetEvents[1].CreatedXID = "122" }},
		{"extra_original_finish", func(v *Rows) {
			v.OriginalRows.PeriodicBudgetEvents = append(v.OriginalRows.PeriodicBudgetEvents, PeriodicBudgetEventRow{ID: "unlisted", SourceKind: "settlement", SourceID: "settlement"})
		}},
	}
	for _, tt := range mutations {
		t.Run(tt.name, func(t *testing.T) {
			v := ffFixture("approve")
			ffPB(&v, false)
			tt.mutate(&v)
			if ffCheck(t, v).Stats.CustomerPBRows == 0 {
				t.Fatal("forged PB accepted")
			}
		})
	}
	t.Run("multi_error_one_scope", func(t *testing.T) {
		v := ffFixture("approve")
		ffPB(&v, false)
		v.OriginalRows.PeriodicBudgetEvents[1].SpentDelta = "9"
		v.OriginalRows.PeriodicBudgetEvents[1].AfterSpent = "9"
		v.OriginalRows.PeriodicBudgetAllocations[0].Baseline = "9"
		if n := ffCheck(t, v).Stats.CustomerPBRows; n != 1 {
			t.Fatalf("one scope counted %d", n)
		}
	})
}

func TestReconciliationFinishCancellationDuringJSON(t *testing.T) {
	v := ffFixture("approve")
	base := context.Background()
	ctx := &ffCountingContext{Context: base, remaining: 50}
	w := &replayWork{b: budget{ctx: ctx}}
	v.OriginalRows.AuditEvents[1].SanitizedDiff = `{"amount":"` + string(make([]byte, 32000)) + `","reason":"charged_usage"}`
	got, err := checkReconciliationFinish(w, v)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, reconciliationFinishResult{}) {
		t.Fatalf("partial result on in-loop cancellation: %+v %v", got, err)
	}
}

type ffCountingContext struct {
	context.Context
	remaining int
}

func (c *ffCountingContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
