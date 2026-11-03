package procurementprefix

import "testing"

// Each counter uses (physical kind, natural PK); a missing output slot belongs
// to its decision, and each EvidenceChoice retains its own ID even when many
// choices share one immutable (attempt,digest) identity. Cause is not identity.
func TestRequestReplayCounterNaturalObjects(t *testing.T) {
	t.Run("incoming_and_choice_same_id", func(t *testing.T) {
		f := rqFixture{}
		f.add("r1", "", "none", false, "none", false, "normal", "", "none", 10)
		f.add("r2", "", "none", false, "none", false, "normal", "", "none", 10)
		rqClean(t, f.rows)
		in := &f.rows.PrefixRows.IncomingSources[0]
		in.ID = f.rows.PrefixRows.MeterChoices[1].ID
		f.rows.PrefixRows.MeterChoices[0].IncomingSourceID = in.ID
		f.rows.PrefixRows.RequestDecisions[0].IncomingSourceID = in.ID
		in.RequestID = "absent-request"
		in.InputDigest = rqDigest(*in)
		f.rows.PrefixRows.MeterChoices[1].CollisionKind = "source_payload_mismatch"
		if got := rqRun(t, f.rows); got.Stats.MeterChoiceRows != 3 {
			t.Fatalf("one bad incoming plus two distinct bad choices = 3, got %+v", got.Stats)
		}
	})
	t.Run("multiple_choices_one_identity", func(t *testing.T) {
		f := rqFixture{}
		for i := 0; i < 3; i++ {
			index := f.add("r", "a", "none", false, "none", false, "normal", "", "none", 10)
			f.evidence(index, i > 0)
		}
		rqClean(t, f.rows)
		f.rows.PrefixRows.EvidenceChoices[1].IdentityEventID = "wrong-event"
		f.rows.PrefixRows.EvidenceChoices[2].IdentityEventID = "wrong-event"
		if got := rqRun(t, f.rows); got.Stats.MeterIdentityRows != 2 {
			t.Fatalf("two choices must not collapse to one identity: %+v", got.Stats)
		}
	})
	t.Run("missing_normal_targets", func(t *testing.T) {
		f := rqFixture{}
		f.add("r1", "", "none", false, "none", false, "normal", "", "none", 10)
		f.add("r2", "", "none", false, "none", false, "normal", "", "none", 10)
		f.rows.OriginalRows.FinalizationRecords = nil
		for i := range f.rows.PrefixRows.RequestDecisions {
			f.rows.PrefixRows.RequestDecisions[i].FinalizationID = nil
		}
		if got := rqRun(t, f.rows); got.Stats.FinalizationRows != 2 {
			t.Fatalf("two absent decision outputs must stay distinct: %+v", got.Stats)
		}
	})
	t.Run("missing_conflict_targets", func(t *testing.T) {
		f := rqFixture{}
		f.add("anchor", "", "none", false, "none", false, "normal", "", "none", 10)
		f.add("r1", "", "none", false, "none", false, "conflict", "meter-1", "source_payload_mismatch", 20)
		f.add("r2", "", "none", false, "none", false, "conflict", "meter-1", "source_payload_mismatch", 30)
		rqClean(t, f.rows)
		f.rows.OriginalRows.MeteringEventConflicts = nil
		for i := 1; i < len(f.rows.PrefixRows.RequestDecisions); i++ {
			f.rows.PrefixRows.RequestDecisions[i].ConflictSourceID = nil
		}
		if got := rqRun(t, f.rows); got.Stats.ConflictRows != 2 {
			t.Fatalf("two absent conflict outputs must stay distinct: %+v", got.Stats)
		}
	})
	t.Run("one_decision_multiple_causes", func(t *testing.T) {
		f := rqFixture{}
		f.add("r", "", "none", false, "none", false, "normal", "", "none", 10)
		d := &f.rows.PrefixRows.RequestDecisions[0]
		d.BeforeLatestID = rqString("missing-predecessor")
		d.NormalizedDeliveryStage = "content"
		if got := rqRun(t, f.rows); got.Stats.RequestStickyRows != 1 {
			t.Fatalf("same decision must count once: %+v", got.Stats)
		}
	})
}
