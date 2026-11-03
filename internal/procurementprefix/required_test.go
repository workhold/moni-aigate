package procurementprefix

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	old "github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

const requiredFixtureUsage = `{"status":"confirmed_final","input_tokens":10,"output_tokens":2,"total_tokens":12,"cached_tokens":0,"reasoning_tokens":0}`

func requiredPtr(s string) *string { return &s }
func requiredFixtureHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
func requiredFixtureSnapshot(state, revision string) string {
	return fmt.Sprintf(`{"id":"account-a","label":"fixture","provider":"fixture","state":%q,"limit_amount":"1000","held_amount":"0","spent_amount":"0","revision":%q,"created_at":"1970-01-01T00:00:50Z"}`, state, revision)
}
func requiredFixture() old.ProcurementEvidenceGraphRows {
	return old.ProcurementEvidenceGraphRows{
		Requests:     []old.ProcurementEvidenceRequestRow{{ID: "request-a", TenantID: "tenant-a", PolicyVersion: "policy-a"}},
		Accounts:     []old.ProcurementEvidenceAccountRow{{ID: "account-a", Label: "fixture", Provider: "fixture", State: "active", LimitAmount: "1000", HeldAmount: "10", SpentAmount: "0", Revision: "1", CreatedAt: time.Unix(50, 0).UTC()}},
		Attempts:     []old.ProcurementAttemptRow{{ID: "attempt-a", TenantID: "tenant-a", RequestID: "request-a", CostInputRate: requiredPtr("1000000"), CostOutputRate: requiredPtr("6000000"), CostBasis: "contract", CostStatus: "unknown"}},
		Reservations: []old.ProcurementEvidenceReservationRow{{AttemptID: "attempt-a", AccountID: "account-a", UpperAmount: "10", InputBound: "100", OutputBound: "20", HeldAmount: "10", SpentAmount: "0", State: "prepared", Revision: "1"}},
		BudgetEvents: []old.ProcurementEvidenceBudgetEventRow{{ID: "reserve-a", AccountID: "account-a", AttemptID: "attempt-a", EventKey: "reserve:attempt-a", Type: "reserved", HeldDelta: "10", SpentDelta: "0"}},
		Commands:     []old.ProcurementCommandRow{{OperationID: "10000000-0000-0000-0000-000000000001", Kind: "account_create", TargetID: "account-a", Status: "applied", Before: "null", After: requiredFixtureSnapshot("active", "1")}},
	}
}

// The fixture writes the persisted ABI as literal bytes; it does not call any
// parser, digest, snapshot or business helper under test to calculate expected.
func requiredFixtureFinal(r *old.ProcurementEvidenceGraphRows, n int, stored, original string) string {
	meter := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
	final := fmt.Sprintf("40000000-0000-0000-0000-%012d", n)
	source := fmt.Sprintf("source-%d", n)
	raw := fmt.Sprintf(`{"ID":%q,"TenantID":"tenant-a","RequestID":"request-a","AttemptID":"attempt-a","Source":"provider","SourceEventID":%q,"Usage":%s,"ProtocolOutcome":"completed","TransportEndReason":"done","DeliveryStage":%q,"FinishReason":"","ContentWritten":false,"ObservedAt":"0001-01-01T00:00:00Z"}`, final, source, requiredFixtureUsage, original)
	r.Metering = append(r.Metering, old.ProcurementEvidenceMeterRow{ID: meter, TenantID: "tenant-a", RequestID: "request-a", AttemptID: requiredPtr("attempt-a"), Source: "provider", SourceEventID: source, SchemaVersion: 1, UsageJSON: requiredFixtureUsage})
	r.Finalizations = append(r.Finalizations, old.ProcurementEvidenceFinalizationRow{ID: final, TenantID: "tenant-a", RequestID: "request-a", EventID: meter, InputDigest: requiredFixtureHash(raw), ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: stored, ContentWritten: stored == "content", PolicyVersion: "policy-a"})
	r.Requests[0].LatestFinalizationID = requiredPtr(final)
	return meter
}
func requiredAllFixture() old.ProcurementEvidenceGraphRows {
	r := requiredFixture()
	first := requiredFixtureFinal(&r, 1, "committed", "committed")
	requiredFixtureFinal(&r, 2, "content", "none")
	conflictUsage := strings.ReplaceAll(strings.ReplaceAll(requiredFixtureUsage, `"input_tokens":10`, `"input_tokens":11`), `"total_tokens":12`, `"total_tokens":13`)
	r.Conflicts = []old.ProcurementEvidenceConflictRow{{ID: "conflict-a", EventID: first, UsageJSON: conflictUsage}}
	r.Attempts = append(r.Attempts, old.ProcurementAttemptRow{ID: "attempt-b", TenantID: "tenant-a", RequestID: "request-b", CostStatus: "unknown"})
	r.Identities = []old.ProcurementEvidenceIdentityRow{
		{AttemptID: "attempt-a", EventID: first, Digest: "609eee13d39c7cb5c517fd20816973d05309cb3ffe0a96bd0da564adc2c2fea9"},
		{AttemptID: "attempt-b", EventID: first, Digest: requiredFixtureHash(`{"Usage":` + conflictUsage + `,"Reason":"unknown"}`)},
	}
	r.Reservations[0].State = "settled"
	r.Reservations[0].Revision = "4"
	r.Reservations[0].SpentAmount = "15"
	r.Attempts[0].CostStatus = "invoice"
	r.Attempts[0].CostAmount = requiredPtr("15")
	r.Accounts[0].State = "paused"
	r.Accounts[0].Revision = "2"
	r.BudgetEvents = append(r.BudgetEvents,
		old.ProcurementEvidenceBudgetEventRow{ID: "confirm-a", AccountID: "account-a", AttemptID: "attempt-a", EventKey: "settle:attempt-a", Type: "cost_confirmed", EvidenceRef: first, HeldDelta: "-10", SpentDelta: "22"},
		old.ProcurementEvidenceBudgetEventRow{ID: "correct-a", AccountID: "account-a", AttemptID: "attempt-a", Type: "cost_corrected", HeldDelta: "0", SpentDelta: "-7"},
		old.ProcurementEvidenceBudgetEventRow{ID: "dispute-a", AccountID: "account-a", AttemptID: "attempt-a", Type: "evidence_disputed", HeldDelta: "0", SpentDelta: "0"})
	r.Corrections = []old.ProcurementCostCorrectionRow{{ID: "correction-a", TenantID: "tenant-a", AttemptID: "attempt-a", Amount: "15", Basis: "invoice"}, {ID: "correction-b", TenantID: "tenant-a", AttemptID: "attempt-a", Amount: "16", Basis: "invoice"}}
	r.Proposals = []old.ProcurementCostProposalRow{{ID: "proposal-a", AttemptID: "attempt-a", ExpectedRevision: "1", ExpectedEvidenceCount: "0", State: "approved"}}
	return r
}
func requiredFixtureCheck(t *testing.T, r old.ProcurementEvidenceGraphRows) propositionKeys {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := enumerateRequiredBase(w, r)
	if err != nil {
		t.Fatal(err)
	}
	base, err := old.CheckProcurementEvidenceGraph(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	keys, counts := reflect.ValueOf(got), reflect.ValueOf(base.Unproven)
	for i := 0; i < keys.NumField(); i++ {
		if uint64(keys.Field(i).Len()) != counts.Field(i).Uint() {
			t.Fatalf("%s: Required %d != unchanged Base %d", keys.Type().Field(i).Name, keys.Field(i).Len(), counts.Field(i).Uint())
		}
	}
	return got
}
func requiredExpected(parts ...[]string) keySet {
	out := make(keySet)
	for _, p := range parts {
		out.add(p...)
	}
	return out
}
func TestRequiredExactElevenNaturalObjectSets(t *testing.T) {
	if got := requiredFixtureCheck(t, requiredFixture()); !reflect.DeepEqual(got, newPropositionKeys()) {
		t.Fatal("prepared fixture manufactured history gaps")
	}
	r := requiredAllFixture()
	got := requiredFixtureCheck(t, r)
	want := newPropositionKeys()
	// Explicit hand-counted natural objects for all eleven propositions.
	want.OriginalDeliveryPredecessor = requiredExpected([]string{"40000000-0000-0000-0000-000000000002"})
	want.FinalizationOrder = requiredExpected([]string{"request-a"})
	want.ConflictIncoming = requiredExpected([]string{"conflict-a"})
	want.ConflictReason = requiredExpected([]string{"attempt-b", r.Identities[1].Digest})
	want.IncomingBinding = requiredExpected([]string{"attempt-b", r.Identities[1].Digest})
	want.ProcurementEventOrder = requiredExpected([]string{"attempt-a"})
	want.CostRecordedEligibility = requiredExpected([]string{"attempt-a"})
	want.CurrentCostOrder = requiredExpected([]string{"attempt-a"})
	want.ProposalBarrier = requiredExpected([]string{"proposal-a"})
	want.AccountPauseOrdering = requiredExpected([]string{"account-a"})
	want.AccountAdmissionOrder = requiredExpected([]string{"account-a"})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("natural objects differ:\ngot %#v\nwant %#v", got, want)
	}
	// Duplicate physical/compound identities do not manufacture new objects.
	v := reflect.ValueOf(&r).Elem()
	for i := 0; i < v.NumField(); i++ {
		a := v.Field(i)
		if a.Len() > 0 {
			a.Set(reflect.Append(a, a.Index(0)))
		}
	}
	if dup := requiredFixtureCheck(t, r); !reflect.DeepEqual(dup, want) {
		t.Fatal("duplicates amplified Required")
	}
}
func TestRequiredDistinctCorrectionsAndBranchBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*old.ProcurementEvidenceGraphRows)
		field  string
		want   int
	}{
		{"same_value_correction", func(r *old.ProcurementEvidenceGraphRows) { r.Corrections[1].Amount = "15" }, "CurrentCostOrder", 0},
		{"explicit_pause_command", func(r *old.ProcurementEvidenceGraphRows) {
			r.Commands = append(r.Commands, old.ProcurementCommandRow{OperationID: "10000000-0000-0000-0000-000000000002", Kind: "account_configure", TargetID: "account-a", Status: "applied", Before: requiredFixtureSnapshot("active", "1"), After: requiredFixtureSnapshot("paused", "2")})
		}, "AccountPauseOrdering", 0},
		{"no_anomaly_pause", func(r *old.ProcurementEvidenceGraphRows) { r.BudgetEvents[1].SpentDelta = "0" }, "AccountPauseOrdering", 0},
		{"single_complex", func(r *old.ProcurementEvidenceGraphRows) { r.BudgetEvents = r.BudgetEvents[:3] }, "ProcurementEventOrder", 0},
		{"invalid_upper", func(r *old.ProcurementEvidenceGraphRows) { r.Reservations[0].UpperAmount = "01" }, "ProcurementEventOrder", 0},
		{"invalid_count", func(r *old.ProcurementEvidenceGraphRows) { r.Proposals[0].ExpectedEvidenceCount = "-1" }, "ProposalBarrier", 0},
		{"pending_proposal", func(r *old.ProcurementEvidenceGraphRows) { r.Proposals[0].State = "pending" }, "ProposalBarrier", 0},
		{"equal_barrier", func(r *old.ProcurementEvidenceGraphRows) {
			r.Proposals[0].ExpectedRevision = "4"
			r.Proposals[0].ExpectedEvidenceCount = "1"
		}, "ProposalBarrier", 0},
		{"unmanaged_known", func(r *old.ProcurementEvidenceGraphRows) { r.Reservations = nil }, "CurrentCostOrder", 1},
		{"unmanaged_unknown", func(r *old.ProcurementEvidenceGraphRows) {
			r.Reservations = nil
			r.Attempts[0].CostStatus = "unknown"
			r.Attempts[0].CostAmount = nil
		}, "CurrentCostOrder", 1},
		{"unmanaged_unknown_no_tenant_conflict", func(r *old.ProcurementEvidenceGraphRows) {
			r.Reservations = nil
			r.Conflicts = nil
			r.Attempts[0].CostStatus = "unknown"
			r.Attempts[0].CostAmount = nil
		}, "CurrentCostOrder", 0},
		{"different_tenant_corrections", func(r *old.ProcurementEvidenceGraphRows) {
			for i := range r.Corrections {
				r.Corrections[i].TenantID = "tenant-b"
			}
		}, "CurrentCostOrder", 0},
		{"invalid_usage", func(r *old.ProcurementEvidenceGraphRows) {
			for i := range r.Metering {
				r.Metering[i].UsageJSON = `{}`
			}
		}, "CostRecordedEligibility", 0},
		{"self_digest", func(r *old.ProcurementEvidenceGraphRows) {
			r.Finalizations = r.Finalizations[:1]
			r.Metering = r.Metering[:1]
		}, "OriginalDeliveryPredecessor", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := requiredAllFixture()
			tc.mutate(&r)
			got := requiredFixtureCheck(t, r)
			if n := reflect.ValueOf(got).FieldByName(tc.field).Len(); n != tc.want {
				t.Fatalf("%s=%d want %d", tc.field, n, tc.want)
			}
		})
	}
}
func TestRequiredShuffledIndependentDifferentialMatrix(t *testing.T) {
	rng := rand.New(rand.NewSource(103))
	for i := 0; i < 300; i++ {
		r := requiredAllFixture()
		switch rng.Intn(6) {
		case 0:
			r.Reservations = nil
		case 1:
			r.Reservations[0].State = "unknown"
		case 2:
			r.Reservations[0].Revision = "0"
		case 3:
			r.Reservations[0].UpperAmount = "invalid"
		}
		switch rng.Intn(4) {
		case 0:
			r.Conflicts = nil
		case 1:
			r.Conflicts[0].UsageJSON = `[]`
		case 2:
			r.Conflicts[0].EventID = "orphan"
		}
		switch rng.Intn(5) {
		case 0:
			r.Metering[0].UsageJSON = `{"status":"confirmed_final","status":"missing"}`
		case 1:
			r.Metering[0].AttemptID = nil
		case 2:
			r.Metering[0].SourceEventID = r.Metering[1].SourceEventID
		case 3:
			r.Finalizations[0].DeliveryStage = "bad"
		}
		switch rng.Intn(4) {
		case 0:
			r.Corrections[1].Amount = "15"
		case 1:
			r.Attempts[0].CostStatus = "unknown"
			r.Attempts[0].CostAmount = nil
		case 2:
			r.Corrections = nil
		}
		switch rng.Intn(4) {
		case 0:
			r.Proposals[0].ExpectedEvidenceCount = "2"
		case 1:
			r.Proposals[0].ExpectedRevision = "0"
		case 2:
			r.Proposals[0].State = "pending"
		}
		switch rng.Intn(4) {
		case 0:
			r.Accounts[0].Revision = "3"
		case 1:
			r.Accounts[0].State = "active"
		case 2:
			r.Commands = nil
		}
		// A malformed snapshot remains a mismatch and cannot become proof.
		if rng.Intn(7) == 0 && len(r.Commands) > 0 {
			r.Commands[0].After = `{"id":"\ud800"}`
		}
		value := reflect.ValueOf(&r).Elem()
		for j := 0; j < value.NumField(); j++ {
			a := value.Field(j)
			rng.Shuffle(a.Len(), func(k, l int) {
				x := reflect.New(a.Type().Elem()).Elem()
				x.Set(a.Index(k))
				a.Index(k).Set(a.Index(l))
				a.Index(l).Set(x)
			})
		}
		raw, _ := json.Marshal(r)
		requiredFixtureCheck(t, r)
		after, _ := json.Marshal(r)
		if string(raw) != string(after) {
			t.Fatal("enumeration mutated raw input")
		}
	}
}

func TestRequiredRawEntryUsesOnlyCurrentOriginalRows(t *testing.T) {
	rows := Rows{OriginalRows: OriginalRows{MeteringEventConflicts: []MeteringEventConflictRow{{ID: "conflict-raw", EventID: "missing", NormalizedUsage: requiredFixtureUsage, CreatedAt: Time{Time: time.Unix(50, 0).UTC(), Valid: true}}}}}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := enumerateRequired(w, rows)
	want := newPropositionKeys()
	want.ConflictIncoming = requiredExpected([]string{"conflict-raw"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("raw projection entry got %#v %v", got, err)
	}
}
