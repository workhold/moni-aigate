package procurementprefix

import "testing"

// These expectations follow the three original branch decisions separately.
// A later execution point can sample an earlier wall-clock time; no helper or
// timestamp sorting is used to derive the hand-supplied prices or outcomes.
func ffLateApproval(v *Rows, latest bool) {
	s := &v.PrefixRows.ReconciliationSources[0]
	s.ApprovalCheckedAt = ffTime(100)
	s.DerivedIncomingSourceID, s.FinalizationID = nil, nil
	v.PrefixRows.IncomingSources = nil
	v.PrefixRows.RequestDecisions = nil
	o := &v.PrefixRows.CustomerFinishOutcomes[0]
	o.LatestFinalizationID = s.PriorLatestID
	if !latest {
		s.PriorLatestID, o.LatestFinalizationID = nil, nil
	}
	v.OriginalRows.Settlements[0].FinalizationID = o.LatestFinalizationID
	v.OriginalRows.FinalizationRecords = v.OriginalRows.FinalizationRecords[:1]
	v.OriginalRows.FinalizationRecords[0].EventID = "meter"
}

func TestReconciliationFinishIndependentRegressingClocks(t *testing.T) {
	t.Run("pre_deadline_approval_then_decision_and_finance_regress", func(t *testing.T) {
		v := ffFixture("approve")
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(35)
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(30)
		ffClean(t, v) // R=10, raw=ceil(3*.4+5*.9)=6, charge=6.
	})
	t.Run("late_approval_does_not_create_usage_when_decision_regresses", func(t *testing.T) {
		v := ffFixture("approve")
		ffLateApproval(&v, true)
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(90)
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(85)
		ffClean(t, v) // Uses the existing prior finalization, no derived input.
	})
	t.Run("late_approval_no_latest_can_reach_recovery_release", func(t *testing.T) {
		v := ffFixture("approve")
		ffLateApproval(&v, false)
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(90)
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(85)
		ffRewrite(&v, "released_no_delivery", "released", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v) // Still incomplete: the historical sent/absence cut is unproved.
	})
	t.Run("late_approval_no_latest_can_reach_pending", func(t *testing.T) {
		v := ffFixture("approve")
		ffLateApproval(&v, false)
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(90)
		ffPending(&v, "delivery_unknown")
		ffClean(t, v) // The sibling sent=true branch stays explicit, also unproved.
	})
	t.Run("expired_decision_remains_released_if_financial_clock_regresses", func(t *testing.T) {
		v := ffFixture("approve")
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(100)
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(90)
		ffRewrite(&v, "released_deadline", "released", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v)
		bad := ffFixture("approve")
		bad.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(100)
		bad.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(90)
		if ffCheck(t, bad).Stats.CustomerFinishRows != 1 {
			t.Fatal("financial clock regression revived an already selected deadline release")
		}
	})
	t.Run("financial_clock_crossing_deadline_clears_previously_selected_charge", func(t *testing.T) {
		v := ffFixture("approve")
		v.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(90)
		v.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(100)
		ffRewrite(&v, "released_deadline", "released", "0", "0", "0", "7", "10", "110", "")
		ffClean(t, v)
		bad := ffFixture("approve")
		bad.PrefixRows.CustomerFinishOutcomes[0].SettlementDecisionCheckedAt = ffTime(90)
		bad.PrefixRows.CustomerFinishOutcomes[0].FinancialCheckedAt = ffTime(100)
		if ffCheck(t, bad).Stats.CustomerFinishRows != 1 {
			t.Fatal("earlier eligibility retained charge at the final deadline")
		}
	})
}
