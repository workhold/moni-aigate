package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const originalValueUUID = "11111111-1111-4111-8111-111111111111"

// Each row is a hand-written local-value oracle. Physical times/JSON and
// relationships have their own leaves and are not inferred from this fixture.
func TestSourceOriginalValuesAll40ProjectedTypes(t *testing.T) {
	p := sourcePtr
	rows := []any{
		ChannelRow{ID: "original TEXT identity"}, ChannelCredentialRow{}, ChannelVersionRow{}, TenantRow{}, UserRow{},
		ChannelVersionCredentialRow{Position: "32", Weight: "1000000", MaxInflight: "1000"},
		RequestRecordRow{Status: "cancelled", BillingStatus: "reserved", InputRate: "0", OutputRate: "0", InputUpperBound: "0", OutputLimit: "0", RouteRevision: "0"},
		CostVersionRow{Basis: "unknown", InputRate: p("0")},
		ProcurementAccountRow{Label: " name ", Provider: " provider ", State: "paused", LimitAmount: "0", HeldAmount: "2", SpentAmount: "3", Revision: "1"},
		CredentialProcurementBindingRow{OperationID: "00000000-0000-0000-0000-000000000000"},
		UpstreamAttemptRow{Sequence: "2", DeliveryState: "unknown", CostVersionID: p("quote"), CostInputRate: p("0"), CostBasis: "unknown", CostStatus: "unknown"},
		ProcurementReservationRow{InputBound: "1", OutputBound: "0", UpperAmount: "0", HeldAmount: "0", SpentAmount: "0", State: "prepared", Revision: "1"},
		ProcurementBudgetEventRow{EventKey: "original\nkey", Type: "unknown_future_type", EvidenceRef: ""},
		ProviderCostCorrectionRow{Amount: "0", Basis: "invoice"},
		ProcurementCostProposalRow{ID: originalValueUUID, Amount: "0", Basis: "contract", EvidenceRef: "invoice:1", Reason: "reason", ExpectedReservationRevision: "1", ExpectedEvidenceCount: "0", State: "proposed"},
		ProcurementCommandRow{OperationID: originalValueUUID, InputDigest: strings.Repeat("a", 64), Kind: "credential_bind", Status: "rejected", Reason: "not_found"},
		ProcurementBalanceObservationRow{Status: "known", Amount: p("0"), Currency: "CNY", Source: "manual", Semantic: "balance", EvidenceRef: "evidence"},
		AuditEventRow{Action: "other.subsystem"}, ProjectionOutboxRow{Kind: "other_subsystem"},
		ProcurementEvidenceIdentityRow{EvidenceDigest: strings.Repeat("f", 64), EventID: originalValueUUID},
		MeteringEventRow{SchemaVersion: "1", Source: "provider", SourceEventID: "event"}, MeteringEventConflictRow{},
		FinalizationRecordRow{InputDigest: strings.Repeat("0", 64), ProtocolOutcome: "unknown", TransportEndReason: "connection_error", DeliveryStage: "unknown"},
		ReservationRow{Amount: "0", Status: "pending_reconciliation"},
		SettlementRow{ChargedAmount: "0", RefundedAmount: "0", RawRetailAmount: "0", WaivedRetailAmount: "0", Status: "released"},
		ReconciliationCaseRow{State: "closed"}, BillingProfileBlockRow{},
		LedgerAccountRow{Kind: "REVENUE", Balance: "-9223372036854775808"},
		JournalRow{Currency: "CNY", CreatedTxid: "1"}, JournalLineRow{SignedDelta: "-9223372036854775808"},
		APIKeyRow{Spent: "0", Held: "0", LifetimeLimit: p("0")},
		PeriodicBudgetControlRow{ID: originalValueUUID, Scope: "tenant", TenantID: p("tenant"), PeriodKind: "utc_month", LimitAmount: "0", Revision: "1", HeldAmount: "0", SpentAmount: "0", EventSeq: "0", CreatedXID: "1"},
		PeriodicBudgetWindowRow{ID: originalValueUUID, HeldAmount: "0", SpentAmount: "0", Revision: "0"},
		PeriodicBudgetAllocationRow{ID: originalValueUUID, SubjectID: originalValueUUID, OriginSourceID: originalValueUUID, SubjectType: "customer_request", Origin: "legacy_import", UpperAmount: "0", Baseline: "0", SourceHeld: "0", SourceSpent: "0", HeldAmount: "0", SpentAmount: "0", State: "released", Revision: "0", CreatedXID: "1"},
		PeriodicBudgetEventRow{ID: originalValueUUID, SourceID: originalValueUUID, SourceKind: "legacy_import", Seq: "1", AllocationBeforeRevision: "0", BeforeHeld: "0", BeforeSpent: "0", AfterHeld: "0", AfterSpent: "0", AfterSourceSpent: "0", AfterState: "settled", CreatedXID: "1"},
		PeriodicBudgetOperationRow{OperationID: originalValueUUID, ResourceID: originalValueUUID, Kind: "tenant_periodic_budget.configure", Scope: originalValueUUID, Revision: "1", InputFingerprint: strings.Repeat("f", 64), CreatedXID: "1"},
		PeriodicBudgetConfigurationVersionRow{Revision: "1", PeriodKind: "utc_day", LimitAmount: "0", CreatedXID: "1"},
		PeriodicBudgetObservationOriginRow{ControlID: originalValueUUID, Version: "1", CreatedXID: "1"},
		PeriodicBudgetObservationHeadRow{ControlID: originalValueUUID, Ordinal: "0", UpdatedXID: "1"},
		PeriodicBudgetObservationJournalRow{ID: originalValueUUID, ControlID: originalValueUUID, Ordinal: "1", Kind: "window_birth", SourceID: originalValueUUID, EventSeq: "0", ConfigurationRevision: "1", ConfigurationOperationID: originalValueUUID, WindowID: p(originalValueUUID), WindowRevision: p("0"), WindowSourceID: p(originalValueUUID), CreatedXID: "1"},
	}
	seen := map[reflect.Type]bool{}
	for _, row := range rows {
		t.Run(reflect.TypeOf(row).Name(), func(t *testing.T) {
			if !sourceOriginalValues(row) {
				t.Fatalf("rejected original local value: %+v", row)
			}
		})
		seen[reflect.TypeOf(row)] = true
	}
	layout := reflect.TypeOf(OriginalRows{})
	if len(seen) != 40 || layout.NumField() != 40 {
		t.Fatal("oracle must cover every original projection type")
	}
	for i := 0; i < layout.NumField(); i++ {
		if !seen[layout.Field(i).Type.Elem()] {
			t.Fatalf("missing original oracle: %s", layout.Field(i).Name)
		}
	}
}

func TestSourceOriginalValuesRejectLocalContradictions(t *testing.T) {
	p := sourcePtr
	for _, row := range []any{
		ChannelVersionCredentialRow{Position: "33", Weight: "1", MaxInflight: "1"},
		CostVersionRow{Basis: "unknown", OutputRate: p("-1")},
		ProcurementAccountRow{Label: "name", Provider: "provider", State: "active", LimitAmount: "0", HeldAmount: "-1", SpentAmount: "0", Revision: "1"},
		UpstreamAttemptRow{Sequence: "1", DeliveryState: "prepared", CostInputRate: p("0"), CostBasis: "unknown", CostStatus: "unknown"},
		UpstreamAttemptRow{Sequence: "1", DeliveryState: "prepared", CostBasis: "unknown", CostStatus: "unknown", CostAmount: p("0")},
		ProcurementReservationRow{InputBound: "1", OutputBound: "0", UpperAmount: "9223372036854775808", HeldAmount: "0", SpentAmount: "0", State: "unknown", Revision: "1"},
		ProcurementReservationRow{InputBound: "1", OutputBound: "0", UpperAmount: "1", HeldAmount: "0", SpentAmount: "0", State: "sending", Revision: "1"},
		ProcurementReservationRow{InputBound: "1", OutputBound: "0", UpperAmount: "1", HeldAmount: "0", SpentAmount: "1", State: "released", Revision: "1"},
		ProcurementCostProposalRow{ID: originalValueUUID, Amount: "0", Basis: "contract", EvidenceRef: "e", Reason: "r", ExpectedReservationRevision: "1", ExpectedEvidenceCount: "0", State: "approved", Requester: "same", Approver: p("same"), ApprovalOperationID: p(originalValueUUID)},
		ProcurementBalanceObservationRow{Status: "unknown", Amount: p("0"), Currency: "CNY", Source: "manual", Semantic: "s", EvidenceRef: "e"},
		SettlementRow{ChargedAmount: "0", RefundedAmount: "1", RawRetailAmount: "0", WaivedRetailAmount: "0", Status: "settled"},
		ReconciliationCaseRow{State: "open", Requester: p("same"), Approver: p("same")},
		LedgerAccountRow{TenantID: p("tenant"), Kind: "AVAILABLE", Balance: "-1"},
		LedgerAccountRow{Kind: "OPERATING_GRANT_CLEARING", Balance: "1"},
		LedgerAccountRow{TenantID: p("tenant"), Kind: "REVENUE", Balance: "0"},
		APIKeyRow{Spent: "9223372036854775807", Held: "1", LifetimeLimit: p("9223372036854775807")},
		PeriodicBudgetWindowRow{ID: originalValueUUID, HeldAmount: "0", SpentAmount: "0", Revision: "0", LastEventID: p("event")},
		PeriodicBudgetObservationHeadRow{ControlID: originalValueUUID, Ordinal: "1", UpdatedXID: "1"},
	} {
		t.Run(reflect.TypeOf(row).Name(), func(t *testing.T) {
			if sourceOriginalValues(row) {
				t.Fatalf("accepted original contradiction: %+v", row)
			}
		})
	}
	for _, kind := range []string{"FUNDING_CLEARING", "REVENUE"} {
		for _, balance := range []string{"-9223372036854775808", "0", "9223372036854775807"} {
			if !sourceOriginalBalance(kind, nil, balance) {
				t.Fatalf("platform %s signed balance rejected: %s", kind, balance)
			}
		}
	}
	if sourceOriginalValues(struct{}{}) || sourceOriginalUUID("00000000-0000-0000-0000-000000000000", true) || !sourceOriginalUUID("00000000-0000-0000-0000-000000000000", false) {
		t.Fatal("unknown type or original/PB UUID acceptance boundary changed")
	}
}

func TestSourceOriginalValueEntryNaturalKeysAndCancellation(t *testing.T) {
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}
	r := Rows{OriginalRows: OriginalRows{ProcurementAccounts: []ProcurementAccountRow{{ID: "same", Label: "name", Provider: "provider", State: "active", LimitAmount: "0", HeldAmount: "0", SpentAmount: "0", Revision: "1", CreatedAt: stamp}}}}
	check := func(r Rows) sourceClosureResult {
		w, _, err := newReplayWork(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		got, err := checkSourceClosure(w, r)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := check(r); got.Stats.InvalidScalarRows != 0 || got.Complete || len(got.Qualified) != 0 {
		t.Fatalf("valid local source or completeness: %+v", got)
	}
	r.OriginalRows.ProcurementAccounts[0].HeldAmount = "-1"
	r.OriginalRows.ProcurementAccounts[0].Revision = "0"
	r.OriginalRows.ProcurementAccounts = append(r.OriginalRows.ProcurementAccounts, r.OriginalRows.ProcurementAccounts[0])
	r.OriginalRows.LedgerAccounts = []LedgerAccountRow{{ID: "same", Kind: "AVAILABLE", Balance: "-1", TenantID: sourcePtr("tenant")}}
	if got := check(r); got.Stats.InvalidScalarRows != 2 || got.Stats.DuplicateRows != 1 {
		t.Fatalf("physical table/PK scalar dedup failed: %+v", got.Stats)
	}
	w, _, err := newReplayWork(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 2}
	w.b.steps = 255
	got, err := checkSourceClosure(w, r)
	if !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, sourceClosureResult{}) {
		t.Fatalf("cancellation returned partial source result: %+v %v", got, err)
	}
	for _, state := range []*sourceClosureState{nil, {}, {w: &replayWork{}}} {
		if err := state.originalValueRows(); !errors.Is(err, ErrInput) {
			t.Fatal("invalid local value entry did not fail closed")
		}
	}
}

func TestSourceOriginalValuesExplicitOptionalPBUUIDs(t *testing.T) {
	p := sourcePtr
	journal := PeriodicBudgetObservationJournalRow{ID: originalValueUUID, ControlID: originalValueUUID, Ordinal: "2", PreviousID: p(originalValueUUID), Kind: "command", SourceID: originalValueUUID, EventSeq: "1", LastEventID: p(originalValueUUID), ConfigurationRevision: "1", ConfigurationOperationID: originalValueUUID, WindowID: p(originalValueUUID), WindowRevision: p("1"), WindowLastEventID: p(originalValueUUID), CreatedXID: "1", CommandSourceID: p(originalValueUUID), ObservedAt: Time{Valid: true, Time: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}}
	operation := PeriodicBudgetOperationRow{OperationID: originalValueUUID, ResourceID: originalValueUUID, Kind: "key_periodic_budget.configure", Scope: "platform", Revision: "1", InputFingerprint: strings.Repeat("a", 64), CreatedXID: "1"}
	head := PeriodicBudgetObservationHeadRow{ControlID: originalValueUUID, Ordinal: "1", LastObservationID: p(originalValueUUID), UpdatedXID: "1"}
	for _, tc := range []struct {
		row   any
		field string
	}{{operation, "ObservationID"}, {head, "LastObservationID"}, {journal, "PreviousID"}, {journal, "LastEventID"}, {journal, "WindowID"}, {journal, "WindowLastEventID"}} {
		t.Run(reflect.TypeOf(tc.row).Name()+"/"+tc.field, func(t *testing.T) {
			if !sourceOriginalValues(tc.row) {
				t.Fatal("hand-written PB base must be locally valid")
			}
			for _, bad := range []string{"invalid", "", "00000000-0000-0000-0000-000000000000"} {
				copy := reflect.New(reflect.TypeOf(tc.row)).Elem()
				copy.Set(reflect.ValueOf(tc.row))
				copy.FieldByName(tc.field).Set(reflect.ValueOf(p(bad)))
				if sourceOriginalValues(copy.Interface()) {
					t.Fatalf("accepted explicitly constrained optional PB UUID: %q", bad)
				}
			}
		})
	}
}
