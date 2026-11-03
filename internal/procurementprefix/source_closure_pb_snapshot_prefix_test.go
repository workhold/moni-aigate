package procurementprefix

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

func snapshotPrefixID(n int) string      { return fmt.Sprintf("10000000-0000-0000-0000-%012d", n) }
func snapshotPrefixPtr(s string) *string { return &s }
func snapshotPrefixAt(s string) Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return Time{Valid: true, Time: t}
}

// These receipts and effects are hand-authored. No SQL writer, old checker,
// other relation leaf, or computed financial expected is used by the fixtures.
func snapshotPrefixFixture() Rows {
	cid, tenant, actor := snapshotPrefixID(10), snapshotPrefixID(1), snapshotPrefixID(2)
	op, obs := snapshotPrefixID(20), snapshotPrefixID(30)
	at := snapshotPrefixAt("2026-11-01T12:00:00Z")
	c := PeriodicBudgetControlRow{ID: cid, Scope: "tenant", TenantID: &tenant, PeriodKind: "utc_day", Enabled: true, LimitAmount: "100", Revision: "1", TrackingStartedAt: at, EffectiveFrom: snapshotPrefixAt("2026-11-02T00:00:00Z"), MaxObservedWindowStart: snapshotPrefixAt("2026-11-01T00:00:00Z"), HeldAmount: "0", SpentAmount: "0", EventSeq: "0", CreatedXID: "1", ConfigurationOperationID: op}
	o := PeriodicBudgetOperationRow{OperationID: op, Kind: "tenant_periodic_budget.configure", Actor: actor, Scope: "platform", Target: "/api/platform/tenants/" + tenant + "/periodic-budget", ResourceID: cid, Revision: "1", SafeInput: `{"expected_revision":"0","period_kind":"utc_day","enabled":true,"limit_amount":"100"}`, InputFingerprint: strings.Repeat("a", 64), CreatedXID: "1", CreatedAt: at, ObservationID: &obs}
	o.ResultSnapshot = snapshotPrefixJSON(c, o, "0", "")
	j := PeriodicBudgetObservationJournalRow{ID: obs, ControlID: cid, Ordinal: "1", Kind: "command", SourceID: op, EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: op, CreatedXID: "1", ObservedAt: at, CommandSourceID: &op}
	return Rows{OriginalRows: OriginalRows{
		PeriodicBudgetControls:              []PeriodicBudgetControlRow{c},
		PeriodicBudgetOperations:            []PeriodicBudgetOperationRow{o},
		PeriodicBudgetConfigurationVersions: []PeriodicBudgetConfigurationVersionRow{{ControlID: cid, Revision: "1", OperationID: op, PeriodKind: "utc_day", Enabled: true, LimitAmount: "100", Actor: actor, CreatedXID: "1", CreatedAt: at}},
		PeriodicBudgetObservationOrigins:    []PeriodicBudgetObservationOriginRow{{ControlID: cid, Version: "1", CreatedXID: "1", TrackingStartedAt: at}},
		PeriodicBudgetObservationHeads:      []PeriodicBudgetObservationHeadRow{{ControlID: cid, Ordinal: "1", LastObservationID: &obs, UpdatedXID: "1"}},
		PeriodicBudgetObservationJournal:    []PeriodicBudgetObservationJournalRow{j},
	}}
}

func snapshotPrefixJSON(c PeriodicBudgetControlRow, o PeriodicBudgetOperationRow, carry, window string) string {
	id := ""
	switch c.Scope {
	case "tenant":
		id = *c.TenantID
	case "key":
		id = *c.KeyID
	case "procurement":
		id = *c.AccountID
	}
	if window == "" {
		window = "null"
	}
	return `{"scope":"` + c.Scope + `","scope_id":"` + id + `","revision":"` + o.Revision + `","period_kind":"` + c.PeriodKind + `","enabled":true,"limit_amount":"100","state":"scheduled","effective_from":"` + c.EffectiveFrom.Time.UTC().Format(sourcePBUTC6) + `","tracking_started_at":"` + c.TrackingStartedAt.Time.UTC().Format(sourcePBUTC6) + `","current_window":` + window + `,"carry_held":"` + carry + `","as_of":"` + o.CreatedAt.Time.UTC().Format(sourcePBUTC6) + `"}`
}

func snapshotPrefixWindowJSON(w PeriodicBudgetWindowRow, h, s string) string {
	return `{"id":"` + w.ID + `","start_at":"` + w.StartAt.Time.UTC().Format(sourcePBUTC6) + `","end_at":"` + w.EndAt.Time.UTC().Format(sourcePBUTC6) + `","spent_amount":"` + s + `","held_amount":"` + h + `"}`
}

func snapshotPrefixHead(r *OriginalRows) {
	j := r.PeriodicBudgetObservationJournal[len(r.PeriodicBudgetObservationJournal)-1]
	r.PeriodicBudgetObservationHeads[0].Ordinal = fmt.Sprint(len(r.PeriodicBudgetObservationJournal))
	r.PeriodicBudgetObservationHeads[0].LastObservationID = snapshotPrefixPtr(j.ID)
	r.PeriodicBudgetObservationHeads[0].UpdatedXID = j.CreatedXID
}

func snapshotPrefixBirth(r *OriginalRows, wid int, start, end, cut string, tail *string) PeriodicBudgetWindowRow {
	c := r.PeriodicBudgetControls[0]
	w := PeriodicBudgetWindowRow{ID: snapshotPrefixID(wid), ControlID: c.ID, StartAt: snapshotPrefixAt(start), EndAt: snapshotPrefixAt(end), HeldAmount: "0", SpentAmount: "0", Revision: "0", ObservationID: snapshotPrefixPtr(snapshotPrefixID(wid + 100)), ObservationCreatedXID: snapshotPrefixPtr("2")}
	previous := r.PeriodicBudgetObservationJournal[len(r.PeriodicBudgetObservationJournal)-1]
	j := PeriodicBudgetObservationJournalRow{ID: *w.ObservationID, ControlID: c.ID, Ordinal: fmt.Sprint(len(r.PeriodicBudgetObservationJournal) + 1), PreviousID: snapshotPrefixPtr(previous.ID), Kind: "window_birth", SourceID: w.ID, EventSeq: cut, LastEventID: tail, ConfigurationRevision: "1", ConfigurationOperationID: c.ConfigurationOperationID, WindowID: &w.ID, WindowRevision: snapshotPrefixPtr("0"), CreatedXID: "2", WindowSourceID: &w.ID}
	r.PeriodicBudgetWindows = append(r.PeriodicBudgetWindows, w)
	r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
	snapshotPrefixHead(r)
	return w
}

func snapshotPrefixAllocation(c string, id int, w *string) PeriodicBudgetAllocationRow {
	a := PeriodicBudgetAllocationRow{ID: snapshotPrefixID(id), ControlID: c, SubjectType: "supplier_attempt", SubjectID: snapshotPrefixID(id + 1000), Origin: "new", OriginSourceID: snapshotPrefixID(id + 2000), WindowID: w, AdmittedAt: snapshotPrefixAt("2026-11-02T01:00:00Z"), UpperAmount: "20", Baseline: "0", SourceHeld: "0", SourceSpent: "0", HeldAmount: "0", SpentAmount: "0", State: "held", Revision: "0", CreatedXID: "2", CreatedAt: snapshotPrefixAt("2026-11-02T01:00:00Z")}
	if w == nil {
		a.Origin, a.AdmittedAt = "legacy_import", Time{}
	}
	return a
}

func snapshotPrefixEvent(c string, id, aid int, seq, ar string, wr *string, bh, bs, ah, as, dh, ds string) PeriodicBudgetEventRow {
	return PeriodicBudgetEventRow{ID: snapshotPrefixID(id), ControlID: c, AllocationID: snapshotPrefixID(aid), SourceKind: "procurement_event", SourceID: snapshotPrefixID(id + 5000), Seq: seq, AllocationBeforeRevision: ar, WindowBeforeRevision: wr, BeforeHeld: bh, BeforeSpent: bs, AfterHeld: ah, AfterSpent: as, AfterSourceSpent: as, AfterState: "held", HeldDelta: dh, SpentDelta: ds, CreatedXID: "2", CreatedAt: snapshotPrefixAt("2026-11-02T01:00:00Z")}
}

func snapshotPrefixCommand(r *OriginalRows, id int, at, cut string, tail *string, w *PeriodicBudgetWindowRow, revision string, windowTail *string, held, spent, carry string) {
	c := r.PeriodicBudgetControls[0]
	o := r.PeriodicBudgetOperations[0]
	o.OperationID, o.ObservationID, o.CreatedAt, o.CreatedXID = snapshotPrefixID(id), snapshotPrefixPtr(snapshotPrefixID(id+100)), snapshotPrefixAt(at), "3"
	o.SafeInput = `{"expected_revision":"1","period_kind":"` + c.PeriodKind + `","enabled":true,"limit_amount":"100"}`
	window := ""
	if w != nil {
		window = snapshotPrefixWindowJSON(*w, held, spent)
	}
	o.ResultSnapshot = snapshotPrefixJSON(c, o, carry, window)
	// All post-effective receipts are active; this is a fixed ABI spelling,
	// not an expected amount computed by any verifier.
	if !o.CreatedAt.Time.Before(c.EffectiveFrom.Time) {
		o.ResultSnapshot = strings.Replace(o.ResultSnapshot, `"state":"scheduled"`, `"state":"active"`, 1)
	}
	prev := r.PeriodicBudgetObservationJournal[len(r.PeriodicBudgetObservationJournal)-1]
	j := PeriodicBudgetObservationJournalRow{ID: *o.ObservationID, ControlID: c.ID, Ordinal: fmt.Sprint(len(r.PeriodicBudgetObservationJournal) + 1), PreviousID: snapshotPrefixPtr(prev.ID), Kind: "command", SourceID: o.OperationID, EventSeq: cut, LastEventID: tail, ConfigurationRevision: "1", ConfigurationOperationID: c.ConfigurationOperationID, CreatedXID: o.CreatedXID, ObservedAt: o.CreatedAt, CommandSourceID: &o.OperationID}
	if w != nil {
		j.WindowID, j.WindowRevision, j.WindowLastEventID = &w.ID, snapshotPrefixPtr(revision), windowTail
	}
	r.PeriodicBudgetOperations = append(r.PeriodicBudgetOperations, o)
	r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
	snapshotPrefixHead(r)
}

// At cuts3/4/5/6 control held is21/14/14/14. Window2 held is7/0/0/0,
// spent0/5/5/5, and revision1/2/2/3. Carry is always14 (legacy4 + old10).
func snapshotPrefixInterleaved() Rows {
	rows := snapshotPrefixFixture()
	r := &rows.OriginalRows
	c := r.PeriodicBudgetControls[0].ID
	w1 := snapshotPrefixBirth(r, 40, "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "1", snapshotPrefixPtr(snapshotPrefixID(71)))
	w2 := snapshotPrefixBirth(r, 41, "2026-11-02T00:00:00Z", "2026-11-03T00:00:00Z", "2", snapshotPrefixPtr(snapshotPrefixID(72)))
	r.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{snapshotPrefixAllocation(c, 50, nil), snapshotPrefixAllocation(c, 51, &w1.ID), snapshotPrefixAllocation(c, 52, &w2.ID)}
	r.PeriodicBudgetEvents = []PeriodicBudgetEventRow{
		snapshotPrefixEvent(c, 71, 50, "1", "0", nil, "0", "0", "4", "0", "4", "0"),
		snapshotPrefixEvent(c, 72, 51, "2", "0", snapshotPrefixPtr("0"), "0", "0", "10", "0", "10", "0"),
		snapshotPrefixEvent(c, 73, 52, "3", "0", snapshotPrefixPtr("0"), "0", "0", "7", "0", "7", "0"),
		snapshotPrefixEvent(c, 74, 52, "4", "1", snapshotPrefixPtr("1"), "7", "0", "0", "5", "-7", "5"),
		snapshotPrefixEvent(c, 75, 51, "5", "1", snapshotPrefixPtr("1"), "10", "0", "10", "0", "0", "0"),
		snapshotPrefixEvent(c, 76, 52, "6", "2", snapshotPrefixPtr("2"), "0", "5", "0", "5", "0", "0"),
	}
	snapshotPrefixCommand(r, 81, "2026-11-02T01:00:00Z", "3", snapshotPrefixPtr(snapshotPrefixID(73)), &w2, "1", snapshotPrefixPtr(snapshotPrefixID(73)), "7", "0", "14")
	snapshotPrefixCommand(r, 82, "2026-11-02T02:00:00Z", "4", snapshotPrefixPtr(snapshotPrefixID(74)), &w2, "2", snapshotPrefixPtr(snapshotPrefixID(74)), "0", "5", "14")
	snapshotPrefixCommand(r, 83, "2026-11-02T03:00:00Z", "5", snapshotPrefixPtr(snapshotPrefixID(75)), &w2, "2", snapshotPrefixPtr(snapshotPrefixID(74)), "0", "5", "14")
	snapshotPrefixCommand(r, 84, "2026-11-02T02:30:00Z", "6", snapshotPrefixPtr(snapshotPrefixID(76)), &w2, "3", snapshotPrefixPtr(snapshotPrefixID(76)), "0", "5", "14")
	return rows
}

func snapshotPrefixRun(ctx context.Context, rows Rows) (*sourceClosureState, SourceClosure, error) {
	w, _, err := newReplayWork(ctx, rows)
	if err != nil {
		return nil, SourceClosure{}, err
	}
	s := &sourceClosureState{w: w, tables: make(map[string][]sourceRawRow), bad: make(map[string]keySet)}
	if err = s.physicalRows(reflect.ValueOf(rows.OriginalRows)); err != nil {
		return nil, SourceClosure{}, err
	}
	if err = s.pbSnapshotPrefixRows(); err != nil {
		return nil, SourceClosure{}, err
	}
	return s, SourceClosure{InvalidScalarRows: uint64(len(s.bad["scalar"])), DuplicateRows: uint64(len(s.bad["duplicate"])), OriginalPointerRows: uint64(len(s.bad["pointer"]))}, nil
}

func snapshotPrefixClean(t *testing.T, rows Rows) {
	t.Helper()
	_, got, err := snapshotPrefixRun(context.Background(), rows)
	if err != nil || got != (SourceClosure{}) {
		t.Fatalf("hand-derived prefix rejected: %+v %v", got, err)
	}
}

func snapshotPrefixHas(s *sourceClosureState, table string, parts ...string) bool {
	_, ok := s.bad["pointer"][tupleKey(table, tupleKey(parts...))]
	return ok
}

func snapshotPrefixReject(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s, got, err := snapshotPrefixRun(context.Background(), rows)
	if err != nil || got.OriginalPointerRows == 0 {
		t.Fatalf("invalid raw prefix accepted: %+v %v", got, err)
	}
	return s
}

func TestSourcePBSnapshotPrefixEmptyScopesAndCurrentProjectionIndependence(t *testing.T) {
	snapshotPrefixClean(t, Rows{})
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			rows := snapshotPrefixFixture()
			r := &rows.OriginalRows
			c, o := &r.PeriodicBudgetControls[0], &r.PeriodicBudgetOperations[0]
			c.Scope = scope
			if scope == "key" {
				c.KeyID = snapshotPrefixPtr(snapshotPrefixID(3))
				o.Scope = *c.TenantID
			}
			if scope == "procurement" {
				c.TenantID = nil
				c.AccountID = snapshotPrefixPtr(snapshotPrefixID(4))
			}
			o.Kind = scope + "_periodic_budget.configure"
			o.ResultSnapshot = snapshotPrefixJSON(*c, *o, "0", "")
			// Deliberately unrelated, well-shaped current projections. A raw
			// command prefix must never take its initial balance from these.
			c.HeldAmount, c.SpentAmount, c.EventSeq, c.LastEventID = "999", "777", "99", snapshotPrefixPtr(snapshotPrefixID(999))
			snapshotPrefixClean(t, rows)
		})
	}
	rows := snapshotPrefixFixture()
	r := &rows.OriginalRows
	r.PeriodicBudgetOperations, r.PeriodicBudgetObservationJournal = nil, nil
	r.PeriodicBudgetObservationHeads[0].Ordinal, r.PeriodicBudgetObservationHeads[0].LastObservationID = "0", nil
	snapshotPrefixClean(t, rows)
}

func TestSourcePBSnapshotPrefixInterleavedCarryAmountsRevisionAndLatestTail(t *testing.T) {
	rows := snapshotPrefixInterleaved()
	snapshotPrefixClean(t, rows)
	for _, mutation := range []string{"carry", "held", "spent", "revision", "old_tail", "global_tail", "same_amount_other_cut"} {
		t.Run(mutation, func(t *testing.T) {
			v := snapshotPrefixInterleaved()
			r := &v.OriginalRows
			o := &r.PeriodicBudgetOperations[3]
			j := &r.PeriodicBudgetObservationJournal[5]
			ownerTable, owner := pbRelOps, o.OperationID
			switch mutation {
			case "carry":
				o.ResultSnapshot = strings.Replace(o.ResultSnapshot, `"carry_held":"14"`, `"carry_held":"10"`, 1)
			case "held":
				o.ResultSnapshot = strings.Replace(o.ResultSnapshot, `"held_amount":"0"`, `"held_amount":"1"`, 1)
			case "spent":
				o.ResultSnapshot = strings.Replace(o.ResultSnapshot, `"spent_amount":"5"`, `"spent_amount":"4"`, 1)
			case "revision":
				j.WindowRevision = snapshotPrefixPtr("1")
				ownerTable, owner = pbRelJournal, j.ID
			case "old_tail":
				j.WindowLastEventID = snapshotPrefixPtr(snapshotPrefixID(73))
				ownerTable, owner = pbRelJournal, j.ID
			case "global_tail":
				j.WindowLastEventID = j.LastEventID
				ownerTable, owner = pbRelJournal, j.ID
			case "same_amount_other_cut":
				j = &r.PeriodicBudgetObservationJournal[6]
				j.EventSeq, j.LastEventID = "5", snapshotPrefixPtr(snapshotPrefixID(75))
				ownerTable, owner = pbRelJournal, j.ID
			}
			s := snapshotPrefixReject(t, v)
			if len(s.bad["pointer"]) != 1 || !snapshotPrefixHas(s, ownerTable, owner) {
				t.Fatalf("wrong dynamic-field owner: %v", s.bad["pointer"])
			}
		})
	}
	for i := range rows.OriginalRows.PeriodicBudgetWindows {
		w := &rows.OriginalRows.PeriodicBudgetWindows[i]
		w.HeldAmount, w.SpentAmount, w.Revision, w.LastEventID = "555", "444", "33", snapshotPrefixPtr(snapshotPrefixID(888))
	}
	for i := range rows.OriginalRows.PeriodicBudgetAllocations {
		a := &rows.OriginalRows.PeriodicBudgetAllocations[i]
		a.HeldAmount, a.SpentAmount, a.Revision, a.LastEventID = "222", "111", "22", snapshotPrefixPtr(snapshotPrefixID(889))
	}
	snapshotPrefixClean(t, rows)
}

func TestSourcePBSnapshotPrefixSameCutBirthOrderAndFutureWindow(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(fmt.Sprint(before), func(t *testing.T) {
			rows := snapshotPrefixFixture()
			r := &rows.OriginalRows
			w := snapshotPrefixBirth(r, 40, "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z", "0", nil)
			if before {
				cmd, birth := r.PeriodicBudgetObservationJournal[0], r.PeriodicBudgetObservationJournal[1]
				birth.Ordinal, birth.PreviousID, cmd.Ordinal, cmd.PreviousID = "1", nil, "2", &birth.ID
				cmd.WindowID, cmd.WindowRevision = &w.ID, snapshotPrefixPtr("0")
				r.PeriodicBudgetObservationJournal = []PeriodicBudgetObservationJournalRow{birth, cmd}
				r.PeriodicBudgetOperations[0].ResultSnapshot = snapshotPrefixJSON(r.PeriodicBudgetControls[0], r.PeriodicBudgetOperations[0], "0", snapshotPrefixWindowJSON(w, "0", "0"))
				snapshotPrefixHead(r)
			}
			// A later terminal projection says nothing about empty-window
			// existence at the same financial cut.
			r.PeriodicBudgetWindows[0].HeldAmount = "19"
			snapshotPrefixClean(t, rows)
			cmdIndex := 0
			if before {
				cmdIndex = 1
			}
			cmd := &r.PeriodicBudgetObservationJournal[cmdIndex]
			if before {
				cmd.WindowID, cmd.WindowRevision = nil, nil
			} else {
				cmd.WindowID, cmd.WindowRevision = &w.ID, snapshotPrefixPtr("0")
			}
			s := snapshotPrefixReject(t, rows)
			if !snapshotPrefixHas(s, pbRelJournal, cmd.ID) {
				t.Fatal("birth visibility did not constrain command")
			}
		})
	}
	rows := snapshotPrefixInterleaved()
	rows.OriginalRows.PeriodicBudgetObservationJournal[1].EventSeq = "2"
	rows.OriginalRows.PeriodicBudgetObservationJournal[1].LastEventID = snapshotPrefixPtr(snapshotPrefixID(72))
	s := snapshotPrefixReject(t, rows)
	if !snapshotPrefixHas(s, pbRelEvents, snapshotPrefixID(72)) || !snapshotPrefixHas(s, pbRelJournal, snapshotPrefixID(140)) {
		t.Fatal("birth containing its own window effect was accepted")
	}
}

func TestSourcePBSnapshotPrefixNegativeCorrectionAndNumeric100(t *testing.T) {
	rows := snapshotPrefixInterleaved()
	r := &rows.OriginalRows
	// A later absolute cost correction moves the budget spent below its old
	// baseline: budget5 -> 0 with a real negative delta. This leaf follows
	// the saved budget effect, without trying to reconstruct today's source.
	r.PeriodicBudgetEvents[5].AfterSpent, r.PeriodicBudgetEvents[5].SpentDelta = "0", "-5"
	r.PeriodicBudgetEvents[5].AfterSourceSpent = "1"
	r.PeriodicBudgetOperations[4].ResultSnapshot = strings.Replace(r.PeriodicBudgetOperations[4].ResultSnapshot, `"spent_amount":"5"`, `"spent_amount":"0"`, 1)
	snapshotPrefixClean(t, rows)
	for _, amount := range []string{"9223372036854775808", strings.Repeat("9", 100)} {
		v := snapshotPrefixFixture()
		r := &v.OriginalRows
		c := r.PeriodicBudgetControls[0].ID
		r.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{snapshotPrefixAllocation(c, 50, nil)}
		r.PeriodicBudgetEvents = []PeriodicBudgetEventRow{snapshotPrefixEvent(c, 71, 50, "1", "0", nil, "0", "0", amount, "0", amount, "0")}
		snapshotPrefixCommand(r, 81, "2026-11-01T12:01:00Z", "1", snapshotPrefixPtr(snapshotPrefixID(71)), nil, "", nil, "", "", amount)
		snapshotPrefixClean(t, v)
	}
	for _, kind := range []string{"digits101", "negative_zero", "control_overflow", "negative_intermediate"} {
		t.Run(kind, func(t *testing.T) {
			v := snapshotPrefixInterleaved()
			r := &v.OriginalRows
			switch kind {
			case "digits101":
				r.PeriodicBudgetEvents[0].AfterHeld = strings.Repeat("1", 101)
			case "negative_zero":
				r.PeriodicBudgetEvents[4].HeldDelta = "-0"
			case "control_overflow":
				max := strings.Repeat("9", 100)
				r.PeriodicBudgetEvents[0].AfterHeld, r.PeriodicBudgetEvents[0].HeldDelta = max, max
			case "negative_intermediate":
				r.PeriodicBudgetEvents[3].BeforeHeld, r.PeriodicBudgetEvents[3].HeldDelta = "8", "-8"
			}
			snapshotPrefixReject(t, v)
		})
	}
}

func TestSourcePBSnapshotPrefixBadSuffixDoesNotPoisonEarlierCuts(t *testing.T) {
	rows := snapshotPrefixInterleaved()
	r := &rows.OriginalRows
	// No command observes event6. Its malformed arithmetic remains an event
	// finding, while every earlier command has an independently complete cut.
	r.PeriodicBudgetOperations = r.PeriodicBudgetOperations[:4]
	r.PeriodicBudgetObservationJournal = r.PeriodicBudgetObservationJournal[:6]
	snapshotPrefixHead(r)
	r.PeriodicBudgetEvents[5].AfterHeld = "1"
	s := snapshotPrefixReject(t, rows)
	for _, o := range r.PeriodicBudgetOperations {
		if snapshotPrefixHas(s, pbRelOps, o.OperationID) {
			t.Fatalf("future malformed arithmetic poisoned earlier command %s", o.OperationID)
		}
	}
	if !snapshotPrefixHas(s, pbRelEvents, snapshotPrefixID(76)) {
		t.Fatal("bad suffix event lost")
	}
	// A truly broken earlier prefix cannot be skipped to the same final sum.
	r.PeriodicBudgetEvents[1].AfterHeld = "11"
	s = snapshotPrefixReject(t, rows)
	if !snapshotPrefixHas(s, pbRelOps, snapshotPrefixID(81)) {
		t.Fatal("bad earlier prefix was skipped")
	}
}

func TestSourcePBSnapshotPrefixLocatedPhysicalDuplicateSuffix(t *testing.T) {
	for _, position := range []string{"same", "different", "invalid"} {
		t.Run(position, func(t *testing.T) {
			rows := snapshotPrefixInterleaved()
			r := &rows.OriginalRows
			r.PeriodicBudgetOperations = r.PeriodicBudgetOperations[:4]
			r.PeriodicBudgetObservationJournal = r.PeriodicBudgetObservationJournal[:6]
			snapshotPrefixHead(r)
			duplicate := r.PeriodicBudgetEvents[5]
			if position == "different" {
				duplicate.Seq = "7"
			}
			if position == "invalid" {
				duplicate.Seq = "06"
			}
			r.PeriodicBudgetEvents = append(r.PeriodicBudgetEvents, duplicate)
			s := snapshotPrefixReject(t, rows)
			if len(s.bad["duplicate"]) != 1 || !snapshotPrefixHas(s, pbRelEvents, duplicate.ID) {
				t.Fatal("physical duplicate suffix was consumed or disappeared")
			}
			for _, op := range r.PeriodicBudgetOperations {
				bad := snapshotPrefixHas(s, pbRelOps, op.OperationID)
				if position == "same" && bad {
					t.Fatalf("known duplicate at seq6 poisoned earlier cut: %s", op.OperationID)
				}
				if position != "same" && !bad {
					t.Fatalf("unlocatable duplicate supplied an earlier cut: %s", op.OperationID)
				}
			}
			if position == "same" {
				for _, journal := range r.PeriodicBudgetObservationJournal {
					if snapshotPrefixHas(s, pbRelJournal, journal.ID) {
						t.Fatalf("known duplicate suffix poisoned earlier observation %s", journal.ID)
					}
				}
			}
		})
	}
}

func TestSourcePBSnapshotPrefixEventAndCutMutations(t *testing.T) {
	for _, name := range []string{"missing_seq", "duplicate_seq", "invalid_seq", "missing_allocation", "allocation_control", "foreign_window", "duplicate_effect", "before", "after", "delta", "allocation_revision", "window_revision", "null_window_revision", "cut_tail", "cut_zero_tail", "cut_future"} {
		t.Run(name, func(t *testing.T) {
			rows := snapshotPrefixInterleaved()
			r := &rows.OriginalRows
			switch name {
			case "missing_seq":
				r.PeriodicBudgetEvents = append(r.PeriodicBudgetEvents[:1], r.PeriodicBudgetEvents[2:]...)
			case "duplicate_seq":
				r.PeriodicBudgetEvents[2].Seq = "2"
			case "invalid_seq":
				r.PeriodicBudgetEvents[5].Seq = "01"
			case "missing_allocation":
				r.PeriodicBudgetEvents[2].AllocationID = snapshotPrefixID(999)
			case "allocation_control":
				r.PeriodicBudgetAllocations[2].ControlID = snapshotPrefixID(999)
			case "foreign_window":
				r.PeriodicBudgetWindows[1].ControlID = snapshotPrefixID(999)
			case "duplicate_effect":
				r.PeriodicBudgetEvents[5].SourceID = r.PeriodicBudgetEvents[2].SourceID
			case "before":
				r.PeriodicBudgetEvents[3].BeforeHeld, r.PeriodicBudgetEvents[3].HeldDelta = "8", "-8"
			case "after":
				r.PeriodicBudgetEvents[3].AfterSpent = "6"
			case "delta":
				r.PeriodicBudgetEvents[3].HeldDelta = "-6"
			case "allocation_revision":
				r.PeriodicBudgetEvents[3].AllocationBeforeRevision = "0"
			case "window_revision":
				r.PeriodicBudgetEvents[3].WindowBeforeRevision = snapshotPrefixPtr("0")
			case "null_window_revision":
				r.PeriodicBudgetEvents[3].WindowBeforeRevision = nil
			case "cut_tail":
				r.PeriodicBudgetObservationJournal[3].LastEventID = snapshotPrefixPtr(snapshotPrefixID(72))
			case "cut_zero_tail":
				r.PeriodicBudgetObservationJournal[0].LastEventID = snapshotPrefixPtr(snapshotPrefixID(71))
			case "cut_future":
				r.PeriodicBudgetObservationJournal[6].EventSeq = "7"
			}
			snapshotPrefixReject(t, rows)
		})
	}
}

func TestSourcePBSnapshotPrefixLegacyAndCoverageClaims(t *testing.T) {
	legacy := func(rows *Rows) {
		r := &rows.OriginalRows
		r.PeriodicBudgetObservationOrigins, r.PeriodicBudgetObservationHeads, r.PeriodicBudgetObservationJournal = nil, nil, nil
		for i := range r.PeriodicBudgetOperations {
			r.PeriodicBudgetOperations[i].ObservationID = nil
		}
		for i := range r.PeriodicBudgetWindows {
			r.PeriodicBudgetWindows[i].ObservationID, r.PeriodicBudgetWindows[i].ObservationCreatedXID = nil, nil
		}
	}
	rows := snapshotPrefixInterleaved()
	legacy(&rows)
	rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"carry_held":"0"`, `"carry_held":"999"`, 1)
	snapshotPrefixClean(t, rows) // No exact historical cut is invented.
	for _, claim := range []string{"operation", "window_pointer", "window_stamp", "head", "journal", "origin"} {
		t.Run(claim, func(t *testing.T) {
			v := snapshotPrefixInterleaved()
			keep := v.OriginalRows
			legacy(&v)
			r := &v.OriginalRows
			switch claim {
			case "operation":
				r.PeriodicBudgetOperations[0].ObservationID = keep.PeriodicBudgetOperations[0].ObservationID
			case "window_pointer":
				r.PeriodicBudgetWindows[0].ObservationID = snapshotPrefixPtr(snapshotPrefixID(140))
			case "window_stamp":
				r.PeriodicBudgetWindows[0].ObservationCreatedXID = snapshotPrefixPtr("2")
			case "head":
				r.PeriodicBudgetObservationHeads = keep.PeriodicBudgetObservationHeads
			case "journal":
				r.PeriodicBudgetObservationJournal = keep.PeriodicBudgetObservationJournal[:1]
			case "origin":
				r.PeriodicBudgetObservationOrigins = keep.PeriodicBudgetObservationOrigins
			}
			if claim == "operation" {
				r.PeriodicBudgetOperations[0].ObservationID = snapshotPrefixPtr(snapshotPrefixID(30))
			}
			snapshotPrefixReject(t, v)
		})
	}
}

func TestSourcePBSnapshotPrefixObservationSourceAndConfigurationMutations(t *testing.T) {
	for _, field := range []string{"origin_stamp", "origin_tracking", "head_tail", "head_ordinal", "previous", "ordinal", "command_pointer", "command_xid", "observed_at", "kind", "configuration_operation", "configuration_version", "birth_pointer", "birth_stamp", "birth_revision", "birth_source", "duplicate_source", "duplicate_window_start"} {
		t.Run(field, func(t *testing.T) {
			rows := snapshotPrefixInterleaved()
			r := &rows.OriginalRows
			switch field {
			case "origin_stamp":
				r.PeriodicBudgetObservationOrigins[0].CreatedXID = "2"
			case "origin_tracking":
				r.PeriodicBudgetObservationOrigins[0].TrackingStartedAt.Time = r.PeriodicBudgetObservationOrigins[0].TrackingStartedAt.Time.Add(time.Microsecond)
			case "head_tail":
				r.PeriodicBudgetObservationHeads[0].LastObservationID = snapshotPrefixPtr(snapshotPrefixID(30))
			case "head_ordinal":
				r.PeriodicBudgetObservationHeads[0].Ordinal = "6"
			case "previous":
				r.PeriodicBudgetObservationJournal[3].PreviousID = snapshotPrefixPtr(snapshotPrefixID(30))
			case "ordinal":
				r.PeriodicBudgetObservationJournal[3].Ordinal = "3"
			case "command_pointer":
				r.PeriodicBudgetOperations[1].ObservationID = snapshotPrefixPtr(snapshotPrefixID(30))
			case "command_xid":
				r.PeriodicBudgetObservationJournal[3].CreatedXID = "4"
			case "observed_at":
				r.PeriodicBudgetObservationJournal[3].ObservedAt.Time = r.PeriodicBudgetObservationJournal[3].ObservedAt.Time.Add(time.Microsecond)
			case "kind":
				r.PeriodicBudgetObservationJournal[3].Kind = "window_birth"
			case "configuration_operation":
				r.PeriodicBudgetObservationJournal[3].ConfigurationOperationID = snapshotPrefixID(81)
			case "configuration_version":
				r.PeriodicBudgetConfigurationVersions[0].OperationID = snapshotPrefixID(81)
			case "birth_pointer":
				r.PeriodicBudgetWindows[0].ObservationID = snapshotPrefixPtr(snapshotPrefixID(141))
			case "birth_stamp":
				r.PeriodicBudgetWindows[0].ObservationCreatedXID = snapshotPrefixPtr("5")
			case "birth_revision":
				r.PeriodicBudgetObservationJournal[1].WindowRevision = snapshotPrefixPtr("1")
			case "birth_source":
				r.PeriodicBudgetObservationJournal[1].WindowSourceID = snapshotPrefixPtr(snapshotPrefixID(41))
			case "duplicate_source":
				j := r.PeriodicBudgetObservationJournal[3]
				j.ID = snapshotPrefixID(999)
				r.PeriodicBudgetObservationJournal = append(r.PeriodicBudgetObservationJournal, j)
			case "duplicate_window_start":
				w := r.PeriodicBudgetWindows[0]
				w.ID = snapshotPrefixID(999)
				r.PeriodicBudgetWindows = append(r.PeriodicBudgetWindows, w)
			}
			snapshotPrefixReject(t, rows)
		})
	}
}

func TestSourcePBSnapshotPrefixCalendarAndXIDAreNotHistoryOrder(t *testing.T) {
	rows := snapshotPrefixInterleaved()
	r := &rows.OriginalRows
	// SQL timestamps compare instants; xid8 values are exact identity stamps,
	// not increasing clocks. A later command may have an earlier wall clock.
	loc := time.FixedZone("offset", 9*3600)
	for i := range r.PeriodicBudgetOperations {
		r.PeriodicBudgetOperations[i].CreatedAt.Time = r.PeriodicBudgetOperations[i].CreatedAt.Time.In(loc)
	}
	for i := range r.PeriodicBudgetObservationJournal {
		if r.PeriodicBudgetObservationJournal[i].ObservedAt.Valid {
			r.PeriodicBudgetObservationJournal[i].ObservedAt.Time = r.PeriodicBudgetObservationJournal[i].ObservedAt.Time.In(loc)
		}
	}
	r.PeriodicBudgetOperations[4].CreatedXID = "0"
	r.PeriodicBudgetObservationJournal[6].CreatedXID = "0"
	r.PeriodicBudgetObservationHeads[0].UpdatedXID = "0"
	snapshotPrefixClean(t, rows)
	for _, pair := range [][2]string{{"2024-02-29T12:00:00Z", "2024-03-01T00:00:00Z"}, {"9998-12-31T23:59:59Z", "9999-01-01T00:00:00Z"}} {
		v := snapshotPrefixFixture()
		r := &v.OriginalRows
		c, o := &r.PeriodicBudgetControls[0], &r.PeriodicBudgetOperations[0]
		c.PeriodKind, c.TrackingStartedAt, c.EffectiveFrom = "utc_month", snapshotPrefixAt(pair[0]), snapshotPrefixAt(pair[1])
		o.CreatedAt = c.TrackingStartedAt
		o.ResultSnapshot = snapshotPrefixJSON(*c, *o, "0", "")
		r.PeriodicBudgetConfigurationVersions[0].PeriodKind = "utc_month"
		r.PeriodicBudgetObservationOrigins[0].TrackingStartedAt = c.TrackingStartedAt
		r.PeriodicBudgetObservationJournal[0].ObservedAt = o.CreatedAt
		snapshotPrefixClean(t, v)
	}
}

func TestSourcePBSnapshotPrefixDuplicateIdentityAndPermutation(t *testing.T) {
	for _, table := range []string{pbRelControls, pbRelWindows, pbRelAllocs, pbRelEvents, pbRelOps, pbRelVersions, pbRelOrigins, pbRelHeads, pbRelJournal} {
		t.Run(table, func(t *testing.T) {
			rows := snapshotPrefixInterleaved()
			rv := reflect.ValueOf(&rows.OriginalRows).Elem()
			for i := 0; i < rv.NumField(); i++ {
				if rv.Type().Field(i).Tag.Get("json") == table {
					rv.Field(i).Set(reflect.Append(rv.Field(i), rv.Field(i).Index(0)))
				}
			}
			snapshotPrefixReject(t, rows)
		})
	}
	rows := snapshotPrefixInterleaved()
	rows.OriginalRows.PeriodicBudgetEvents[2].AllocationID = rows.OriginalRows.PeriodicBudgetAllocations[1].ID
	var expected keySet
	for seed := int64(0); seed < 64; seed++ {
		v := rows
		v.OriginalRows.PeriodicBudgetEvents = append([]PeriodicBudgetEventRow(nil), rows.OriginalRows.PeriodicBudgetEvents...)
		rand.New(rand.NewSource(seed)).Shuffle(len(v.OriginalRows.PeriodicBudgetEvents), func(i, j int) {
			v.OriginalRows.PeriodicBudgetEvents[i], v.OriginalRows.PeriodicBudgetEvents[j] = v.OriginalRows.PeriodicBudgetEvents[j], v.OriginalRows.PeriodicBudgetEvents[i]
		})
		s := snapshotPrefixReject(t, v)
		if expected == nil {
			expected = s.bad["pointer"]
		} else if !reflect.DeepEqual(expected, s.bad["pointer"]) {
			t.Fatalf("error ownership depended on permutation %d", seed)
		}
	}
}

// Relocate only fixture identities, including literal JSON references. The
// financial constants and hand-derived expectations stay byte-for-byte fixed.
func snapshotPrefixRelocate(rows Rows, prefix string) Rows {
	r := reflect.ValueOf(&rows.OriginalRows).Elem()
	for i := 0; i < r.NumField(); i++ {
		array := r.Field(i)
		copy := reflect.MakeSlice(array.Type(), array.Len(), array.Len())
		reflect.Copy(copy, array)
		array.Set(copy)
		for j := 0; j < array.Len(); j++ {
			row := array.Index(j)
			for k := 0; k < row.NumField(); k++ {
				field := row.Field(k)
				if field.Kind() == reflect.String {
					field.SetString(strings.ReplaceAll(field.String(), "10000000-", prefix+"0000000-"))
				} else if field.Kind() == reflect.Pointer && !field.IsNil() && field.Type().Elem().Kind() == reflect.String {
					value := strings.ReplaceAll(field.Elem().String(), "10000000-", prefix+"0000000-")
					field.Set(reflect.ValueOf(&value))
				}
			}
		}
	}
	return rows
}

func snapshotPrefixMerge(rows ...Rows) Rows {
	var out Rows
	dst := reflect.ValueOf(&out.OriginalRows).Elem()
	for _, input := range rows {
		src := reflect.ValueOf(input.OriginalRows)
		for i := 0; i < dst.NumField(); i++ {
			dst.Field(i).Set(reflect.AppendSlice(dst.Field(i), src.Field(i)))
		}
	}
	return out
}

func TestSourcePBSnapshotPrefixForeignAllocationDoesNotPoisonItsOwner(t *testing.T) {
	first := snapshotPrefixInterleaved()
	second := snapshotPrefixRelocate(snapshotPrefixInterleaved(), "2")
	third := snapshotPrefixRelocate(snapshotPrefixInterleaved(), "3")
	snapshotPrefixClean(t, second)
	snapshotPrefixClean(t, snapshotPrefixMerge(first, second, third))
	first.OriginalRows.PeriodicBudgetEvents[1].AllocationID = second.OriginalRows.PeriodicBudgetAllocations[1].ID
	var expected keySet
	for seed := int64(0); seed < 64; seed++ {
		rows := snapshotPrefixMerge(first, second, third)
		rand.New(rand.NewSource(seed)).Shuffle(len(rows.OriginalRows.PeriodicBudgetEvents), func(i, j int) {
			rows.OriginalRows.PeriodicBudgetEvents[i], rows.OriginalRows.PeriodicBudgetEvents[j] = rows.OriginalRows.PeriodicBudgetEvents[j], rows.OriginalRows.PeriodicBudgetEvents[i]
		})
		s := snapshotPrefixReject(t, rows)
		if !snapshotPrefixHas(s, pbRelEvents, snapshotPrefixID(72)) || !snapshotPrefixHas(s, pbRelOps, snapshotPrefixID(81)) {
			t.Fatal("declaring control's bad prefix was not rejected")
		}
		for _, valid := range []Rows{second, third} {
			v := reflect.ValueOf(valid.OriginalRows)
			for i := 0; i < v.NumField(); i++ {
				table := v.Type().Field(i).Tag.Get("json")
				for j := 0; j < v.Field(i).Len(); j++ {
					raw := v.Field(i).Index(j)
					key := sourceRowKey(raw, sourcePhysicalLayouts[table].pk)
					if _, bad := s.bad["pointer"][tupleKey(table, key)]; bad {
						t.Fatalf("foreign reference poisoned correct %s %s", table, key)
					}
				}
			}
		}
		if expected == nil {
			expected = s.bad["pointer"]
		} else if !reflect.DeepEqual(expected, s.bad["pointer"]) {
			t.Fatalf("foreign owner finding changed at permutation %d", seed)
		}
	}
}

func snapshotPrefixMany(count int) Rows {
	rows := snapshotPrefixFixture()
	r := &rows.OriginalRows
	cid := r.PeriodicBudgetControls[0].ID
	r.PeriodicBudgetAllocations = []PeriodicBudgetAllocationRow{snapshotPrefixAllocation(cid, 50, nil)}
	for i := 1; i <= count; i++ {
		e := snapshotPrefixEvent(cid, 10000+i, 50, fmt.Sprint(i), fmt.Sprint(i-1), nil, "0", "0", "0", "0", "0", "0")
		r.PeriodicBudgetEvents = append(r.PeriodicBudgetEvents, e)
		snapshotPrefixCommand(r, 20000+i, "2026-11-01T12:01:00Z", fmt.Sprint(i), &e.ID, nil, "", nil, "", "", "0")
	}
	return rows
}

func TestSourcePBSnapshotPrefixBoundedWorkAndCrossControlPhysicalDuplicates(t *testing.T) {
	var small uint64
	for _, size := range []int{128, 256} {
		rows := snapshotPrefixMany(size)
		s, got, err := snapshotPrefixRun(context.Background(), rows)
		if err != nil || got != (SourceClosure{}) {
			t.Fatalf("zero effects lost exact cuts: %+v %v", got, err)
		}
		if size == 128 {
			small = s.w.b.steps
		} else if s.w.b.steps > 3*small {
			t.Fatalf("command/event work grew quadratically: %d -> %d", small, s.w.b.steps)
		}
	}
	for _, size := range []int{128, 256} {
		rows := snapshotPrefixMany(size)
		c := rows.OriginalRows.PeriodicBudgetControls[0]
		for i := 1; i < size; i++ {
			rows.OriginalRows.PeriodicBudgetControls = append(rows.OriginalRows.PeriodicBudgetControls, c)
		}
		s, _, err := snapshotPrefixRun(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshotPrefixHas(s, pbRelControls, c.ID) {
			t.Fatal("duplicate control was chosen as an owner")
		}
		if size == 128 {
			small = s.w.b.steps
		} else if s.w.b.steps > 3*small {
			t.Fatalf("duplicate control repeatedly scanned its journal: %d -> %d", small, s.w.b.steps)
		}
	}
	first, second, third := snapshotPrefixInterleaved(), snapshotPrefixRelocate(snapshotPrefixInterleaved(), "2"), snapshotPrefixRelocate(snapshotPrefixInterleaved(), "3")
	second.OriginalRows.PeriodicBudgetEvents[0].ID = first.OriginalRows.PeriodicBudgetEvents[0].ID
	rows := snapshotPrefixMerge(first, second, third)
	s := snapshotPrefixReject(t, rows)
	for _, c := range []Rows{first, second} {
		if !snapshotPrefixHas(s, pbRelOps, c.OriginalRows.PeriodicBudgetOperations[0].OperationID) {
			t.Fatal("ambiguous physical event was assigned to one owner")
		}
	}
	if snapshotPrefixHas(s, pbRelControls, third.OriginalRows.PeriodicBudgetControls[0].ID) || snapshotPrefixHas(s, pbRelOps, third.OriginalRows.PeriodicBudgetOperations[1].OperationID) {
		t.Fatal("duplicate physical event contaminated unrelated control")
	}
	// The same shared absolute deadline still terminates a repeated-control
	// workload; there is no per-control or per-chain time extension.
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	state := &sourceClosureState{w: w, tables: make(map[string][]sourceRawRow), bad: make(map[string]keySet)}
	if err = state.physicalRows(reflect.ValueOf(rows.OriginalRows)); err != nil {
		t.Fatal(err)
	}
	w.b.deadline = time.Now().Add(-time.Second)
	if !errors.Is(state.pbSnapshotPrefixRows(), ErrTimeout) {
		t.Fatal("duplicate workload reset original deadline")
	}
}

func TestSourcePBSnapshotPrefixStrictJSONAndSharedFailureBudget(t *testing.T) {
	for _, json := range []string{
		strings.Replace(snapshotPrefixFixture().OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"carry_held":"0"`, `"carry_held":"0","carry_\u0068eld":"0"`, 1),
		strings.Replace(snapshotPrefixFixture().OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"enabled":true`, `"enabled":"true"`, 1),
		strings.Replace(snapshotPrefixFixture().OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"carry_held":"0"`, `"carry_held":null`, 1),
		strings.Replace(snapshotPrefixFixture().OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `"carry_held":"0"`, `"carry_held":"\ud800"`, 1),
		strings.Replace(snapshotPrefixFixture().OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot, `12:00:00.000000Z`, `12:00:00Z`, 1),
		strings.Repeat(" ", 8193),
	} {
		rows := snapshotPrefixFixture()
		rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = json
		_, got, err := snapshotPrefixRun(context.Background(), rows)
		if err != nil || got.InvalidScalarRows != 1 {
			t.Fatalf("strict JSON not charged to its operation: %+v %v", got, err)
		}
	}
	for _, code := range []error{ErrCanceled, ErrTimeout, ErrResource} {
		rows := snapshotPrefixFixture()
		ctx := context.Background()
		if code == ErrCanceled {
			c, cancel := context.WithCancel(ctx)
			cancel()
			ctx = c
		}
		if code == ErrTimeout {
			c, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
			defer cancel()
			ctx = c
		}
		if code == ErrResource {
			rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = strings.Repeat(" ", 65537)
		}
		s, got, err := snapshotPrefixRun(ctx, rows)
		if !errors.Is(err, code) || s != nil || got != (SourceClosure{}) {
			t.Fatalf("execution error leaked partial result: %+v %v", got, err)
		}
	}
	rows := snapshotPrefixInterleaved()
	before := fmt.Sprintf("%#v", rows)
	snapshotPrefixClean(t, rows)
	if after := fmt.Sprintf("%#v", rows); after != before {
		t.Fatal("leaf changed caller input")
	}
	var nilState *sourceClosureState
	if !errors.Is(nilState.pbSnapshotPrefixRows(), ErrInput) {
		t.Fatal("nil input accepted")
	}
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	w.b.deadline = time.Now().Add(-time.Second)
	s := &sourceClosureState{w: w}
	if !errors.Is(s.pbSnapshotPrefixRows(), ErrTimeout) {
		t.Fatal("leaf restarted absolute deadline")
	}
}
