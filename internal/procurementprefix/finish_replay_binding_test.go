package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestReconciliationFinishDerivedDecisionPredecessor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
	}{
		{"missing_decision", func(r *Rows) { r.PrefixRows.RequestDecisions = nil }},
		{"duplicate_decision_is_not_arbitrarily_selected", func(r *Rows) {
			r.PrefixRows.RequestDecisions = append(r.PrefixRows.RequestDecisions, r.PrefixRows.RequestDecisions[0])
		}},
		{"different_decision_same_incoming_is_not_selected", func(r *Rows) {
			d := r.PrefixRows.RequestDecisions[0]
			d.ID = "second"
			d.RequestID = "other"
			r.PrefixRows.RequestDecisions = append(r.PrefixRows.RequestDecisions, d)
		}},
		{"wrong_request", func(r *Rows) { r.PrefixRows.RequestDecisions[0].RequestID = "other" }},
		{"wrong_tenant", func(r *Rows) { r.PrefixRows.RequestDecisions[0].TenantID = "other" }},
		{"wrong_root", func(r *Rows) { r.PrefixRows.RequestDecisions[0].RootID = "other" }},
		{"wrong_xid", func(r *Rows) { r.PrefixRows.RequestDecisions[0].CreatedXID = "124" }},
		{"decision_is_not_incoming", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepKind = "incoming" }},
		{"incoming_kind_is_exact", func(r *Rows) { r.PrefixRows.IncomingSources[0].StepKind = "request_decision" }},
		{"incoming_must_follow_source", func(r *Rows) { r.PrefixRows.IncomingSources[0].StepNo = "1" }},
		{"decision_must_follow_incoming", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "2" }},
		{"decision_step_not_zero", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "0" }},
		{"decision_step_not_negative", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "-1" }},
		{"decision_step_not_noncanonical", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "04" }},
		{"decision_step_not_overflow", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "9223372036854775808" }},
		{"decision_step_not_empty", func(r *Rows) { r.PrefixRows.RequestDecisions[0].StepNo = "" }},
		{"conflict_would_rollback_whole_reconciliation", func(r *Rows) { r.PrefixRows.RequestDecisions[0].Branch = "conflict" }},
		{"normal_cannot_have_conflict_companion", func(r *Rows) { r.PrefixRows.RequestDecisions[0].ConflictSourceID = ffPtr("conflict") }},
		{"prior_latest_is_not_nullable_zero", func(r *Rows) { r.PrefixRows.RequestDecisions[0].BeforeLatestID = nil }},
		{"prior_latest_exact_even_when_delivery_equal", func(r *Rows) {
			f := r.OriginalRows.FinalizationRecords[0]
			f.ID = "identical-delivery"
			r.OriginalRows.FinalizationRecords = append(r.OriginalRows.FinalizationRecords, f)
			r.PrefixRows.RequestDecisions[0].BeforeLatestID = ffPtr(f.ID)
		}},
		{"wrong_finalization", func(r *Rows) { r.PrefixRows.RequestDecisions[0].FinalizationID = ffPtr("prior") }},
		{"missing_finalization", func(r *Rows) { r.PrefixRows.RequestDecisions[0].FinalizationID = nil }},
		{"after_latest_must_be_this_finalization", func(r *Rows) { r.PrefixRows.RequestDecisions[0].AfterLatestID = ffPtr("prior") }},
		{"incoming_id_not_source_text", func(r *Rows) { r.PrefixRows.RequestDecisions[0].IncomingSourceID = "approved_reconciliation" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ffFixture("approve")
			tc.mutate(&r)
			got := ffCheck(t, r)
			if got.Stats.ReconciliationRows != 1 {
				t.Fatalf("expected one source mismatch, got %+v", got.Stats)
			}
			if got.Complete {
				t.Fatal("local binding granted full-history completion")
			}
		})
	}
	// Current latest may have advanced after the historical approve. The bridge
	// uses the actual decision cut, never today's projection as its predecessor.
	r := ffFixture("approve")
	r.OriginalRows.RequestRecords[0].LatestFinalizationID = ffPtr("later")
	ffClean(t, r)
	r.PrefixRows.RequestDecisions[0].BeforeLatestID = ffPtr("wrong")
	r.PrefixRows.RequestDecisions[0].AfterLatestID = ffPtr("wrong")
	r.PrefixRows.RequestDecisions[0].RootID = "wrong"
	if n := ffCheck(t, r).Stats.ReconciliationRows; n != 1 {
		t.Fatalf("multiple bridge errors count one physical source: %d", n)
	}
}

func TestReconciliationFinishDerivedDecisionNullAndCrossRequest(t *testing.T) {
	r := ffFixture("approve")
	s := &r.PrefixRows.ReconciliationSources[0]
	s.PriorLatestID = nil
	d := &r.PrefixRows.RequestDecisions[0]
	d.BeforeLatestID = nil
	in := &r.PrefixRows.IncomingSources[0]
	in.ProtocolOutcome, in.TransportEndReason, in.RawDeliveryStage, in.FinishReason = "unknown", "unknown", "unknown", ""
	in.RawContentWritten = false
	f := &r.OriginalRows.FinalizationRecords[1]
	f.ProtocolOutcome, f.TransportEndReason, f.DeliveryStage, f.FinishReason = "unknown", "unknown", "unknown", ""
	f.ContentWritten = false
	ffPending(&r, "delivery_unknown")
	ffClean(t, r)
	r.PrefixRows.RequestDecisions[0].BeforeLatestID = ffPtr("")
	if n := ffCheck(t, r).Stats.ReconciliationRows; n != 1 {
		t.Fatalf("NULL cut accepted empty ID: %d", n)
	}
}

func TestReconciliationFinishDerivedDecisionIndexCancellation(t *testing.T) {
	rows := Rows{PrefixRows: PrefixRows{RequestDecisions: make([]RequestDecisionRow, 1000)}}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 3}
	w.b.steps = 255
	got, err := checkReconciliationFinish(w, rows)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, reconciliationFinishResult{}) {
		t.Fatalf("cancel during decision index returned result: %+v %v", got, err)
	}
}
