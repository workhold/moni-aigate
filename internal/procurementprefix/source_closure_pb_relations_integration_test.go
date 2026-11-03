package procurementprefix

import (
	"context"
	"strings"
	"testing"
)

// Exercise the real private entry, rather than treating a leaf's isolated
// report as proof that integration consumes these raw relations.
func TestSourcePBRelationsFullEntryConfigurationAndAudit(t *testing.T) {
	for _, kind := range []string{"create", "change", "noop", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			rows := sourcePBRelFixture()
			if kind == "change" || kind == "noop" {
				sourcePBRelAddOperation(&rows, kind == "change")
			}
			if kind == "legacy" {
				sourcePBRelLegacy(&rows)
			}
			check := func() sourceClosureResult {
				w, _, err := newReplayWork(context.Background(), rows)
				if err != nil {
					t.Fatal(err)
				}
				got, err := checkSourceClosure(w, rows)
				if err != nil || got.Complete || len(got.Qualified) != 0 || len(got.Unimplemented) == 0 {
					t.Fatalf("private entry lost scope boundary: %+v %v", got, err)
				}
				return got
			}
			if got := check(); got.Stats != (SourceClosure{}) {
				t.Fatalf("legal raw configuration rejected by integrated leaves: %+v", got.Stats)
			}
			// Valid JSON and local scalar values; only the original relation is
			// wrong. A scalar parser cannot replace this content check.
			rows.OriginalRows.AuditEvents[0].Actor = sourcePBRelTestID(999)
			if got := check(); got.Stats.InvalidScalarRows != 0 || got.Stats.OriginalPointerRows != 2 {
				t.Fatalf("raw audit/operation relation was not consumed: %+v", got.Stats)
			}
			rows.OriginalRows.AuditEvents[0].Actor = rows.OriginalRows.PeriodicBudgetOperations[0].Actor
			rows.OriginalRows.PeriodicBudgetControls[0].HeldAmount = "1"
			if got := check(); got.Stats.InvalidScalarRows != 0 || got.Stats.OriginalPointerRows != 1 {
				t.Fatalf("empty event prefix did not constrain current control buckets: %+v", got.Stats)
			}
		})
	}
}

// The exact cut, rather than today's amount or another leaf's report, must
// constrain a covered command. Uncovered legacy remains a missing-evidence
// case and never becomes qualified merely because its JSON is well formed.
func TestSourcePBSnapshotFullEntryCoveredCutAndLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "covered"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			rows := sourcePBRelFixture()
			if legacy {
				sourcePBRelLegacy(&rows)
			}
			rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot = strings.Replace(
				rows.OriginalRows.PeriodicBudgetOperations[0].ResultSnapshot,
				`"carry_held":"0"`, `"carry_held":"1"`, 1)
			w, _, err := newReplayWork(context.Background(), rows)
			if err != nil {
				t.Fatal(err)
			}
			got, err := checkSourceClosure(w, rows)
			if err != nil || got.Complete || len(got.Qualified) != 0 {
				t.Fatalf("private entry lost missing-evidence boundary: %+v %v", got, err)
			}
			want := SourceClosure{}
			if !legacy {
				want.OriginalPointerRows = 1 // Only the actual command's amount is wrong.
			}
			if got.Stats != want {
				t.Fatalf("covered cut0/legacy boundary: got %+v want %+v", got.Stats, want)
			}
		})
	}
}
