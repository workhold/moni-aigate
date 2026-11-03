package procurementprefix

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func correctionRelationPtr(s string) *string { return &s }
func correctionRelationAt(second int) Time {
	return Time{Time: time.Date(2026, 11, 4, 0, 0, second, 123000, time.UTC), Valid: true}
}

// Literal sources and originals intentionally have different clocks. Neither
// the writer, another replay domain nor the tested relation builds expected.
func correctionRelationFixture(kind string) Rows {
	root, source := "00000000-0000-0000-0000-000000000201", "00000000-0000-0000-0000-000000000101"
	operation := "procurement:custom-operation"
	rows := Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{{ID: "attempt", TenantID: "tenant", RequestID: "request", CostStatus: "invoice", CostAmount: correctionRelationPtr("999"), DeliveryState: "completed"}}}}
	if kind == "approval_consumed" {
		operation = "procurement:00000000-0000-0000-0000-000000000301"
		rows.PrefixRows.ApprovalConsumptions = []ApprovalConsumptionRow{{ID: source, RootID: root, StepNo: "3", StepKind: kind, CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", ProposalID: "00000000-0000-0000-0000-000000000401", ProposalBirthID: "00000000-0000-0000-0000-000000000102", Requester: "maker", Approver: "reviewer", ApprovalOperationID: "00000000-0000-0000-0000-000000000301", ProviderCorrectionID: "correction"}}
		rows.PrefixRows.ProposalBirths = []ProposalBirthRow{{ID: "00000000-0000-0000-0000-000000000102", ProposalID: "00000000-0000-0000-0000-000000000401", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", Requester: "maker", ReservationRevision: "3", MemberCount: "2"}}
		rows.OriginalRows.ProcurementCostProposals = []ProcurementCostProposalRow{{ID: "00000000-0000-0000-0000-000000000401", TenantID: "tenant", AttemptID: "attempt", Requester: "maker", Amount: "7", Basis: "invoice", EvidenceRef: "invoice:7", Reason: "original proposal", ExpectedReservationRevision: "3", ExpectedEvidenceCount: "2", State: "approved", Approver: correctionRelationPtr("reviewer"), ApprovalOperationID: correctionRelationPtr("00000000-0000-0000-0000-000000000301"), CreatedAt: correctionRelationAt(1), ApprovedAt: correctionRelationAt(2)}}
	} else {
		rows.PrefixRows.UnmanagedCorrectionSources = []UnmanagedCorrectionSourceRow{{ID: source, RootID: root, StepNo: "3", StepKind: kind, CreatedXID: "7", TenantID: "tenant", RequestID: "request", AttemptID: "attempt", OperationID: operation, ProviderCorrectionID: "correction", Amount: "7", Basis: "invoice", EvidenceRef: "invoice:7", EffectiveAuditActor: "system"}}
	}
	rows.OriginalRows.ProviderCostCorrections = []ProviderCostCorrectionRow{{ID: "correction", TenantID: "tenant", AttemptID: "attempt", OperationID: operation, Amount: "7", Basis: "invoice", EvidenceRef: "invoice:7", CreatedAt: correctionRelationAt(29), PPRootID: &root, PPRootStep: correctionRelationPtr("3"), PPSourceKind: &kind, PPSourceID: &source, PPSourceRole: correctionRelationPtr("correction"), PPCreatedXID: correctionRelationPtr("7")}}
	return rows
}

func correctionRelationRun(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if err := s.correctionRelationRows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func correctionRelationWant(t *testing.T, s *sourceClosureState, objects ...[2]string) {
	t.Helper()
	want := make(keySet)
	for _, object := range objects {
		want[tupleKey(object[0], tupleKey(object[1]))] = struct{}{}
	}
	got := s.bad["pointer"]
	if len(got) != len(want) {
		t.Fatalf("wrong physical defect set: got=%v want=%v", got, want)
	}
	for key := range want {
		if _, found := got[key]; !found {
			t.Fatalf("missing physical defect %q: got=%v", key, got)
		}
	}
}

func correctionRelationClean(t *testing.T, rows Rows) {
	t.Helper()
	correctionRelationWant(t, correctionRelationRun(t, rows))
}

func correctionRelationOwner(kind string) [2]string {
	table := "pp_unmanaged_correction_sources"
	if kind == "approval_consumed" {
		table = "pp_approval_consumptions"
	}
	return [2]string{table, "00000000-0000-0000-0000-000000000101"}
}

func TestSourceCorrectionRelationTypedContentsAndPointers(t *testing.T) {
	for _, kind := range []string{"approval_consumed", "unmanaged_correction"} {
		t.Run(kind, func(t *testing.T) {
			correctionRelationClean(t, correctionRelationFixture(kind))
			for _, field := range []string{"ID", "TenantID", "AttemptID", "OperationID", "Amount", "Basis", "EvidenceRef", "PPRootID", "PPRootStep", "PPSourceKind", "PPSourceID", "PPSourceRole", "PPCreatedXID", "CreatedAt"} {
				t.Run(field, func(t *testing.T) {
					rows := correctionRelationFixture(kind)
					v := reflect.ValueOf(&rows.OriginalRows.ProviderCostCorrections[0]).Elem().FieldByName(field)
					if field == "CreatedAt" {
						v.Set(reflect.ValueOf(Time{}))
					} else if v.Kind() == reflect.Pointer {
						v.Set(reflect.ValueOf(correctionRelationPtr("different")))
					} else {
						value := "different"
						if field == "Amount" {
							value = "8"
						} else if field == "Basis" {
							value = "contract"
						}
						v.SetString(value)
					}
					id := rows.OriginalRows.ProviderCostCorrections[0].ID
					correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner(kind), [2]string{"provider_cost_corrections", id})
				})
			}
			for _, field := range []string{"ID", "RootID", "StepNo", "StepKind", "CreatedXID", "TenantID", "RequestID", "AttemptID", "ProviderCorrectionID"} {
				rows := correctionRelationFixture(kind)
				var source any = &rows.PrefixRows.UnmanagedCorrectionSources
				if kind == "approval_consumed" {
					source = &rows.PrefixRows.ApprovalConsumptions
				}
				reflect.ValueOf(source).Elem().Index(0).FieldByName(field).SetString("different")
				owner := correctionRelationOwner(kind)
				if field == "ID" {
					owner[1] = "different"
				}
				correctionRelationWant(t, correctionRelationRun(t, rows), owner, [2]string{"provider_cost_corrections", "correction"})
			}
		})
	}
}

func TestSourceCorrectionRelationApprovalDependencies(t *testing.T) {
	mutations := map[string]func(*Rows){
		"missing attempt": func(r *Rows) { r.OriginalRows.UpstreamAttempts = nil },
		"duplicate attempt": func(r *Rows) {
			r.OriginalRows.UpstreamAttempts = append(r.OriginalRows.UpstreamAttempts, r.OriginalRows.UpstreamAttempts[0])
		},
		"attempt tenant":   func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].TenantID = "other" },
		"attempt request":  func(r *Rows) { r.OriginalRows.UpstreamAttempts[0].RequestID = "other" },
		"missing proposal": func(r *Rows) { r.OriginalRows.ProcurementCostProposals = nil },
		"duplicate proposal": func(r *Rows) {
			r.OriginalRows.ProcurementCostProposals = append(r.OriginalRows.ProcurementCostProposals, r.OriginalRows.ProcurementCostProposals[0])
		},
		"missing birth": func(r *Rows) { r.PrefixRows.ProposalBirths = nil },
		"duplicate birth": func(r *Rows) {
			r.PrefixRows.ProposalBirths = append(r.PrefixRows.ProposalBirths, r.PrefixRows.ProposalBirths[0])
		},
		"proposal tenant":        func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].TenantID = "other" },
		"proposal attempt":       func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].AttemptID = "other" },
		"proposal requester":     func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Requester = "other" },
		"proposal state":         func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].State = "proposed" },
		"proposal approver":      func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Approver = correctionRelationPtr("other") },
		"proposal null approver": func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Approver = nil },
		"proposal operation": func(r *Rows) {
			r.OriginalRows.ProcurementCostProposals[0].ApprovalOperationID = correctionRelationPtr("other")
		},
		"proposal null op":       func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].ApprovalOperationID = nil },
		"proposal approved time": func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].ApprovedAt = Time{} },
		"proposal revision":      func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].ExpectedReservationRevision = "4" },
		"proposal member count":  func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].ExpectedEvidenceCount = "3" },
		"source proposal":        func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ProposalID = "other" },
		"source birth":           func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ProposalBirthID = "other" },
		"source requester":       func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].Requester = "other" },
		"source approver":        func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].Approver = "other" },
		"source operation":       func(r *Rows) { r.PrefixRows.ApprovalConsumptions[0].ApprovalOperationID = "other" },
		"proposal amount":        func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Amount = "8" },
		"proposal basis":         func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].Basis = "contract" },
		"proposal evidence":      func(r *Rows) { r.OriginalRows.ProcurementCostProposals[0].EvidenceRef = "invoice:8" },
	}
	for _, field := range []string{"ProposalID", "TenantID", "RequestID", "AttemptID", "Requester", "ReservationRevision", "MemberCount"} {
		name := field
		mutations["birth "+field] = func(r *Rows) {
			reflect.ValueOf(&r.PrefixRows.ProposalBirths[0]).Elem().FieldByName(name).SetString("other")
		}
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			rows := correctionRelationFixture("approval_consumed")
			mutate(&rows)
			correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner("approval_consumed"), [2]string{"provider_cost_corrections", "correction"})
		})
	}
	for _, field := range []string{"Amount", "Basis", "EvidenceRef", "OperationID"} {
		rows := correctionRelationFixture("unmanaged_correction")
		value := map[string]string{"Amount": "8", "Basis": "contract", "EvidenceRef": "invoice:8", "OperationID": "different-operation"}[field]
		reflect.ValueOf(&rows.PrefixRows.UnmanagedCorrectionSources[0]).Elem().FieldByName(field).SetString(value)
		correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"})
	}
}

func correctionRelationNull(o *ProviderCostCorrectionRow) {
	o.PPRootID, o.PPRootStep, o.PPSourceKind, o.PPSourceID, o.PPSourceRole, o.PPCreatedXID = nil, nil, nil, nil, nil, nil
}

func TestSourceCorrectionRelationKnownOperationAndMissingOutput(t *testing.T) {
	for _, kind := range []string{"approval_consumed", "unmanaged_correction"} {
		for _, mode := range []string{"extra", "missing specified", "wrong specified operation", "missing attempt", "missing proposal"} {
			if mode == "missing proposal" && kind != "approval_consumed" {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				rows := correctionRelationFixture(kind)
				extra := rows.OriginalRows.ProviderCostCorrections[0]
				extra.ID = "extra"
				correctionRelationNull(&extra)
				rows.OriginalRows.ProviderCostCorrections = append(rows.OriginalRows.ProviderCostCorrections, extra)
				want := [][2]string{correctionRelationOwner(kind), {"provider_cost_corrections", "correction"}, {"provider_cost_corrections", "extra"}}
				switch mode {
				case "missing specified":
					rows.OriginalRows.ProviderCostCorrections = rows.OriginalRows.ProviderCostCorrections[1:]
					want = [][2]string{correctionRelationOwner(kind), {"provider_cost_corrections", "extra"}}
				case "wrong specified operation":
					rows.OriginalRows.ProviderCostCorrections[0].OperationID = "unrelated-operation"
				case "missing attempt":
					rows.OriginalRows.UpstreamAttempts = nil
				case "missing proposal":
					rows.OriginalRows.ProcurementCostProposals = nil
				}
				correctionRelationWant(t, correctionRelationRun(t, rows), want...)
			})
		}
		rows := correctionRelationFixture(kind)
		rows.OriginalRows.ProviderCostCorrections = nil
		correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner(kind))
		for _, partial := range []bool{false, true} {
			rows = correctionRelationFixture(kind)
			correctionRelationNull(&rows.OriginalRows.ProviderCostCorrections[0])
			if partial {
				rows.OriginalRows.ProviderCostCorrections[0].PPSourceID = correctionRelationPtr("source")
			}
			correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner(kind), [2]string{"provider_cost_corrections", "correction"})
		}
	}
	// A tracked original with no declaration cannot become a legacy exception.
	rows := correctionRelationFixture("unmanaged_correction")
	rows.PrefixRows.UnmanagedCorrectionSources = nil
	correctionRelationWant(t, correctionRelationRun(t, rows), [2]string{"provider_cost_corrections", "correction"})
	for _, field := range []string{"PPRootID", "PPRootStep", "PPSourceKind", "PPSourceID", "PPSourceRole", "PPCreatedXID"} {
		rows = correctionRelationFixture("unmanaged_correction")
		rows.PrefixRows.UnmanagedCorrectionSources = nil
		correctionRelationNull(&rows.OriginalRows.ProviderCostCorrections[0])
		reflect.ValueOf(&rows.OriginalRows.ProviderCostCorrections[0]).Elem().FieldByName(field).Set(reflect.ValueOf(correctionRelationPtr("orphan")))
		correctionRelationWant(t, correctionRelationRun(t, rows), [2]string{"provider_cost_corrections", "correction"})
	}
}

func correctionRelationTwoUnmanaged() Rows {
	rows := correctionRelationFixture("unmanaged_correction")
	s := rows.PrefixRows.UnmanagedCorrectionSources[0]
	s.ID, s.RootID, s.OperationID, s.ProviderCorrectionID = "source-two", "root-two", "operation-two", "correction-two"
	o := rows.OriginalRows.ProviderCostCorrections[0]
	o.ID, o.OperationID, o.PPRootID, o.PPSourceID = s.ProviderCorrectionID, s.OperationID, &s.RootID, &s.ID
	rows.PrefixRows.UnmanagedCorrectionSources = append(rows.PrefixRows.UnmanagedCorrectionSources, s)
	rows.OriginalRows.ProviderCostCorrections = append(rows.OriginalRows.ProviderCostCorrections, o)
	return rows
}

func TestSourceCorrectionRelationSharedIdentityAndPhysicalDuplicates(t *testing.T) {
	rows := correctionRelationTwoUnmanaged()
	correctionRelationClean(t, rows)
	rows.PrefixRows.UnmanagedCorrectionSources[1].ID = rows.PrefixRows.UnmanagedCorrectionSources[0].ID
	rows.OriginalRows.ProviderCostCorrections[1].PPSourceID = correctionRelationPtr(rows.PrefixRows.UnmanagedCorrectionSources[0].ID)
	correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"}, [2]string{"provider_cost_corrections", "correction-two"})
	rows = correctionRelationFixture("unmanaged_correction")
	rows.OriginalRows.ProviderCostCorrections = append(rows.OriginalRows.ProviderCostCorrections, rows.OriginalRows.ProviderCostCorrections[0])
	correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"})
	for _, sharedID := range []bool{false, true} {
		rows = correctionRelationFixture("approval_consumed")
		u := correctionRelationFixture("unmanaged_correction").PrefixRows.UnmanagedCorrectionSources[0]
		u.ID, u.RootID, u.OperationID, u.ProviderCorrectionID = "unmanaged-source", "unmanaged-root", rows.OriginalRows.ProviderCostCorrections[0].OperationID, "unmanaged-original"
		o := rows.OriginalRows.ProviderCostCorrections[0]
		o.ID, o.PPRootID, o.PPSourceID, o.PPSourceKind = u.ProviderCorrectionID, &u.RootID, &u.ID, correctionRelationPtr("unmanaged_correction")
		rows.PrefixRows.UnmanagedCorrectionSources = []UnmanagedCorrectionSourceRow{u}
		want := [][2]string{correctionRelationOwner("approval_consumed"), {"pp_unmanaged_correction_sources", u.ID}, {"provider_cost_corrections", "correction"}}
		if sharedID {
			rows.PrefixRows.UnmanagedCorrectionSources[0].ProviderCorrectionID = "correction"
		} else {
			rows.OriginalRows.ProviderCostCorrections = append(rows.OriginalRows.ProviderCostCorrections, o)
			want = append(want, [2]string{"provider_cost_corrections", u.ProviderCorrectionID})
		}
		correctionRelationWant(t, correctionRelationRun(t, rows), want...)
	}
}

func TestSourceCorrectionRelationLegacyAndHistoricalAcceptance(t *testing.T) {
	for _, kind := range []string{"approval_consumed", "unmanaged_correction"} {
		rows := correctionRelationFixture(kind)
		rows.OriginalRows.UpstreamAttempts[0].CostAmount = correctionRelationPtr("123456")
		rows.OriginalRows.UpstreamAttempts[0].DeliveryState = "unknown"
		old := rows.OriginalRows.ProviderCostCorrections[0]
		old.ID, old.OperationID = "old-original", "procurement:unclaimed-old-operation"
		correctionRelationNull(&old)
		rows.OriginalRows.ProviderCostCorrections = append(rows.OriginalRows.ProviderCostCorrections, old)
		correctionRelationClean(t, rows)
		correctionRelationClean(t, Rows{OriginalRows: OriginalRows{ProviderCostCorrections: []ProviderCostCorrectionRow{old}}})
	}
	rows := correctionRelationTwoUnmanaged()
	for i := range rows.PrefixRows.UnmanagedCorrectionSources {
		rows.PrefixRows.UnmanagedCorrectionSources[i].Amount = "0"
		rows.PrefixRows.UnmanagedCorrectionSources[i].Basis = "contract"
		rows.PrefixRows.UnmanagedCorrectionSources[i].InputActorID = correctionRelationPtr("different-actor")
		rows.PrefixRows.UnmanagedCorrectionSources[i].EffectiveAuditActor = "different-actor"
		rows.OriginalRows.ProviderCostCorrections[i].Amount = "0"
		rows.OriginalRows.ProviderCostCorrections[i].Basis = "contract"
	}
	correctionRelationClean(t, rows)
	rows = correctionRelationTwoUnmanaged()
	rows.PrefixRows.UnmanagedCorrectionSources[1].Basis = "contract"
	rows.OriginalRows.ProviderCostCorrections[1].Basis = "contract"
	correctionRelationClean(t, rows) // Same absolute amount, separate basis-only operation.
	for i, j := 0, len(rows.PrefixRows.UnmanagedCorrectionSources)-1; i < j; i, j = i+1, j-1 {
		rows.PrefixRows.UnmanagedCorrectionSources[i], rows.PrefixRows.UnmanagedCorrectionSources[j] = rows.PrefixRows.UnmanagedCorrectionSources[j], rows.PrefixRows.UnmanagedCorrectionSources[i]
		rows.OriginalRows.ProviderCostCorrections[i], rows.OriginalRows.ProviderCostCorrections[j] = rows.OriginalRows.ProviderCostCorrections[j], rows.OriginalRows.ProviderCostCorrections[i]
	}
	correctionRelationClean(t, rows)
}

func TestSourceCorrectionRelationArrayOrderAndRawImmutable(t *testing.T) {
	rows := correctionRelationTwoUnmanaged()
	before := correctionRelationTwoUnmanaged()
	correctionRelationClean(t, rows)
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("relation modified raw sources or originals")
	}
	rows.OriginalRows.ProviderCostCorrections[0].Amount = "8"
	for i := 0; i < 32; i++ {
		rows.PrefixRows.UnmanagedCorrectionSources[0], rows.PrefixRows.UnmanagedCorrectionSources[1] = rows.PrefixRows.UnmanagedCorrectionSources[1], rows.PrefixRows.UnmanagedCorrectionSources[0]
		if i%2 == 0 {
			rows.OriginalRows.ProviderCostCorrections[0], rows.OriginalRows.ProviderCostCorrections[1] = rows.OriginalRows.ProviderCostCorrections[1], rows.OriginalRows.ProviderCostCorrections[0]
		}
		correctionRelationWant(t, correctionRelationRun(t, rows), correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"})
	}
}

func TestSourceCorrectionRelationPhysicalDedupAndBudget(t *testing.T) {
	rows := correctionRelationFixture("unmanaged_correction")
	rows.OriginalRows.ProviderCostCorrections[0].Amount = "8"
	rows.OriginalRows.ProviderCostCorrections[0].Basis = "contract"
	rows.OriginalRows.ProviderCostCorrections[0].PPSourceRole = correctionRelationPtr("wrong")
	s := correctionRelationRun(t, rows)
	correctionRelationWant(t, s, correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"})
	if err := s.correctionRelationRows(); err != nil {
		t.Fatal(err)
	}
	correctionRelationWant(t, s, correctionRelationOwner("unmanaged_correction"), [2]string{"provider_cost_corrections", "correction"})
	rows = correctionRelationFixture("unmanaged_correction")
	rows.PrefixRows.UnmanagedCorrectionSources[0].ID = "correction"
	rows.OriginalRows.ProviderCostCorrections[0].Amount = "8"
	correctionRelationWant(t, correctionRelationRun(t, rows), [2]string{"pp_unmanaged_correction_sources", "correction"}, [2]string{"provider_cost_corrections", "correction"})
	if !errors.Is((*sourceClosureState)(nil).correctionRelationRows(), ErrInput) {
		t.Fatal("nil input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s = sourceTestRows(t, rows)
	s.w.b.ctx = ctx
	if err := s.correctionRelationRows(); !errors.Is(err, ErrCanceled) || len(s.bad["pointer"]) != 0 {
		t.Fatalf("pre-cancel did not stop before relation changes: %v %v", err, s.bad["pointer"])
	}
	s = sourceTestRows(t, rows)
	s.w.b.deadline = time.Now().Add(-time.Second)
	if err := s.correctionRelationRows(); !errors.Is(err, ErrTimeout) {
		t.Fatalf("absolute deadline was restarted: %v", err)
	}
	large := Rows{}
	for i := 0; i < 1200; i++ {
		large.OriginalRows.ProviderCostCorrections = append(large.OriginalRows.ProviderCostCorrections, ProviderCostCorrectionRow{ID: fmt.Sprint(i), OperationID: fmt.Sprint(i)})
	}
	s = sourceTestRows(t, large)
	s.w.b.steps = 0
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 10}
	if err := s.correctionRelationRows(); !errors.Is(err, ErrCanceled) || s.w.b.steps >= 1200 {
		t.Fatalf("index loop failed to stop on shared cancellation: %v steps=%d", err, s.w.b.steps)
	}
	s = sourceTestRows(t, large)
	s.w.b.steps = 255
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 3}
	if err := s.correctionRelationRows(); !errors.Is(err, ErrCanceled) || s.w.b.steps != 256 {
		t.Fatalf("shared work counter was not reused: %v steps=%d", err, s.w.b.steps)
	}
}
