package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourceBusinessID = "44444444-4444-4444-8444-444444444444"
const sourceBusinessRoot = "55555555-5555-4555-8555-555555555555"

func sourceBusinessAttemptLiteral(managed bool) AttemptOriginRow {
	r := AttemptOriginRow{AttemptID: "attempt", RootID: sourceBusinessRoot, StepNo: "1", StepKind: "attempt_birth", CreatedXID: "7", TenantID: "tenant", RequestID: "request", Sequence: "1", ChannelID: "channel", ChannelVersionID: "channel-version", CredentialID: "credential", UpstreamModel: "model", RouteDecisions: "[]", CostBasis: "unknown", QuoteSelectedAt: sourceFinishClock(9), AttemptCreatedAt: sourceFinishClock(8), PBCreatedXID: "7", InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged"}
	if managed {
		r.ManagementMode, r.CostVersionID, r.CostInputRate, r.CostOutputRate, r.CostBasis = "managed", sourcePtr("cost-version"), sourcePtr("1"), sourcePtr("1"), "contract"
		r.AccountID, r.BindingID, r.ProcurementTransitionID, r.ReservedEventID, r.AccountJournalID = sourcePtr("account"), sourcePtr("binding"), sourcePtr("transition"), sourcePtr("event"), sourcePtr("journal")
		r.AdmittedAt = sourceFinishClock(7)
	}
	return r
}

func sourceBusinessIncomingLiteral() IncomingSourceRow {
	return IncomingSourceRow{ID: sourceBusinessID, RootID: sourceBusinessRoot, StepNo: "1", StepKind: "incoming", CreatedXID: "7", TenantID: "tenant", RequestID: "request", InputFinalizationID: "", InputDigest: strings.Repeat("a", 64), Source: "adapter", SourceEventID: "source-event", UsageStatus: "confirmed_final", InputTokens: "2", OutputTokens: "3", TotalTokens: "5", CachedTokens: "1", ReasoningTokens: "2", ProtocolOutcome: "completed", TransportEndReason: "done", RawDeliveryStage: "content", RawContentWritten: true, FinishReason: "stop", EffectiveObservedAt: sourceFinishClock(9)}
}

func sourceBusinessCommandLiteral() AccountCommandSourceRow {
	return AccountCommandSourceRow{ID: sourceBusinessID, RootID: sourceBusinessRoot, StepNo: "1", StepKind: "account_command", CreatedXID: "7", CreatedAt: sourceFinishClock(8), OperationID: "00000000-0000-0000-0000-000000000000", ActorID: "00000000-0000-0000-0000-000000000000", ActorRole: "admin", ActorStatus: "active", Kind: "account_create", TargetID: sourceBusinessID, InputLabel: "供应商账户", InputProvider: "provider", InputState: "active", InputLimitAmount: "100", InputDigest: strings.Repeat("b", 64), CommandStatus: "applied", CommandCreatedAt: sourceFinishClock(7), AuditID: "audit", JournalID: sourcePtr("journal")}
}

func sourceBusinessTransitionLiteral(event string) ProcurementTransitionRow {
	r := ProcurementTransitionRow{ID: sourceBusinessID, RootID: sourceBusinessRoot, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: sourceFinishClock(8), TenantID: "tenant", RequestID: "request", AttemptID: "attempt", AccountID: "account", BindingID: "binding", CauseKind: "evidence_choice", CauseSourceID: "choice", EventID: "event", JournalID: "journal", EventType: event, EventKey: "settle:attempt", EvidenceRef: "meter:8", HeldDelta: "-1", SpentDelta: "1", BeforeRevision: sourcePtr("3"), BeforeState: sourcePtr("unknown"), BeforeHeld: sourcePtr("1"), BeforeSpent: sourcePtr("0"), BeforeAnomaly: sourceBusinessBool(false), AfterRevision: "4", AfterState: "settled", AfterHeld: "0", AfterSpent: "1", InputBound: "100000", OutputBound: "100000", UpperAmount: "1", CostVersionID: "cost-version", CostInputRate: "1", CostOutputRate: "1", CostBasis: "contract"}
	switch event {
	case "reserved":
		r.CauseKind, r.CauseSourceID, r.EventKey, r.EvidenceRef = "attempt_birth", "attempt", "reserve:attempt", ""
		r.BeforeRevision, r.BeforeState, r.BeforeHeld, r.BeforeSpent, r.BeforeAnomaly = nil, nil, nil, nil, nil
		r.AfterRevision, r.AfterState, r.AfterHeld, r.AfterSpent, r.HeldDelta, r.SpentDelta = "1", "prepared", "1", "0", "1", "0"
	case "released_no_consumption":
		r.EventKey, r.AfterState, r.AfterSpent, r.SpentDelta = "release:attempt", "released", "0", "0"
	case "consumption_unknown":
		r.BeforeRevision, r.BeforeState = sourcePtr("1"), sourcePtr("prepared")
		r.EventKey, r.AfterRevision, r.AfterState, r.AfterHeld, r.AfterSpent, r.HeldDelta, r.SpentDelta = "unknown:attempt", "2", "unknown", "1", "0", "0", "0"
	case "evidence_disputed":
		r.BeforeRevision, r.BeforeState, r.BeforeHeld, r.BeforeSpent = sourcePtr("4"), sourcePtr("settled"), sourcePtr("0"), sourcePtr("1")
		r.EventKey, r.AfterRevision, r.AfterState, r.AfterAnomaly, r.HeldDelta, r.SpentDelta = "conflict:attempt:meter:8", "5", "disputed", true, "0", "0"
	case "cost_corrected":
		r.CauseKind, r.EventKey = "approval_consumed", "correction:approval-op"
		r.BeforeRevision, r.BeforeState, r.BeforeHeld, r.BeforeSpent = sourcePtr("4"), sourcePtr("settled"), sourcePtr("0"), sourcePtr("1")
		r.HeldDelta, r.SpentDelta = "0", "0" // A real same-amount correction.
	case "expired_without_permit":
		r.CauseKind, r.EvidenceRef, r.EventKey = "recovery", "", "release:attempt"
		r.BeforeRevision, r.BeforeState = sourcePtr("1"), sourcePtr("prepared")
		r.AfterRevision, r.AfterState, r.AfterSpent, r.SpentDelta = "2", "released", "0", "0"
	case "recovered_unknown":
		r.CauseKind, r.EvidenceRef, r.EventKey = "recovery", "", "unknown:attempt"
		r.BeforeRevision, r.BeforeState = sourcePtr("2"), sourcePtr("sending")
		r.AfterRevision, r.AfterState, r.AfterHeld, r.AfterSpent, r.HeldDelta, r.SpentDelta = "3", "unknown", "1", "0", "0", "0"
	}
	return r
}

func sourceBusinessBool(v bool) *bool { return &v }

func sourceBusinessJournalLiteral(kind string) AccountJournalRow {
	r := AccountJournalRow{ID: sourceBusinessID, AccountID: "account", Ordinal: "2", PreviousID: sourcePtr("previous"), PreviousOrdinal: "1", RootID: sourceBusinessRoot, StepNo: "2", StepKind: "procurement_transition", CreatedXID: "7", CreatedAt: sourceFinishClock(8), Kind: kind, SourceKind: "budget_event", SourceID: "event", BeforeLabel: sourcePtr("account"), BeforeProvider: sourcePtr("provider"), BeforeState: sourcePtr("active"), BeforeLimitAmount: sourcePtr("100"), BeforeConfigurationRevision: sourcePtr("1"), BeforeHeldAmount: sourcePtr("1"), BeforeSpentAmount: sourcePtr("0"), BeforeBlockingCount: sourcePtr("0"), AfterLabel: "account", AfterProvider: "provider", AfterState: "active", AfterLimitAmount: "100", AfterConfigurationRevision: "1", AfterHeldAmount: "0", AfterSpentAmount: "1", AfterBlockingCount: "0", AttemptID: sourcePtr("attempt"), BindingID: sourcePtr("binding"), BeforeReservationRevision: sourcePtr("3"), BeforeReservationState: sourcePtr("unknown"), BeforeReservationHeldAmount: sourcePtr("1"), BeforeReservationSpentAmount: sourcePtr("0"), BeforeReservationAnomaly: sourceBusinessBool(false), AfterReservationRevision: sourcePtr("4"), AfterReservationState: sourcePtr("settled"), AfterReservationHeldAmount: sourcePtr("0"), AfterReservationSpentAmount: sourcePtr("1"), AfterReservationAnomaly: sourceBusinessBool(false), InputBound: sourcePtr("100000"), OutputBound: sourcePtr("100000"), UpperAmount: sourcePtr("1"), CostVersionID: sourcePtr("cost-version"), CostInputRate: sourcePtr("1"), CostOutputRate: sourcePtr("1"), CostBasis: sourcePtr("contract"), TransitionID: sourcePtr("transition"), ProcurementEventID: sourcePtr("event")}
	if sourceOneOf(kind, "create", "configure", "configure_rejected") {
		r.StepKind, r.SourceKind, r.SourceID = "account_command", "command", "operation"
		r.CommandSourceID, r.CommandOperationID, r.TransitionID, r.ProcurementEventID = sourcePtr("command-source"), sourcePtr("operation"), nil, nil
		r.AttemptID, r.BindingID, r.BeforeReservationRevision, r.BeforeReservationState, r.BeforeReservationHeldAmount, r.BeforeReservationSpentAmount, r.BeforeReservationAnomaly = nil, nil, nil, nil, nil, nil, nil
		r.AfterReservationRevision, r.AfterReservationState, r.AfterReservationHeldAmount, r.AfterReservationSpentAmount, r.AfterReservationAnomaly = nil, nil, nil, nil, nil
		r.InputBound, r.OutputBound, r.UpperAmount, r.CostVersionID, r.CostInputRate, r.CostOutputRate, r.CostBasis = nil, nil, nil, nil, nil, nil, nil
		r.AfterHeldAmount, r.AfterSpentAmount = "1", "0"
		if kind == "create" {
			r.Ordinal, r.PreviousID, r.PreviousOrdinal = "1", nil, "0"
			r.BeforeLabel, r.BeforeProvider, r.BeforeState, r.BeforeLimitAmount, r.BeforeConfigurationRevision, r.BeforeHeldAmount, r.BeforeSpentAmount, r.BeforeBlockingCount = nil, nil, nil, nil, nil, nil, nil, nil
			r.AfterHeldAmount = "0"
		} else if kind == "configure" {
			r.AfterLimitAmount, r.AfterConfigurationRevision = "50", "2"
		}
		return r
	}
	switch kind {
	case "reserve":
		r.BeforeHeldAmount, r.AfterHeldAmount, r.AfterSpentAmount = sourcePtr("0"), "1", "0"
		r.BeforeReservationRevision, r.BeforeReservationState, r.BeforeReservationHeldAmount, r.BeforeReservationSpentAmount, r.BeforeReservationAnomaly = nil, nil, nil, nil, nil
		r.AfterReservationRevision, r.AfterReservationState, r.AfterReservationHeldAmount, r.AfterReservationSpentAmount = sourcePtr("1"), sourcePtr("prepared"), sourcePtr("1"), sourcePtr("0")
		r.GateExtra, r.GateCheckedAt, r.PBDecisionAt = sourcePtr("1"), sourceFinishClock(9), sourceFinishClock(7)
	case "dispatch":
		r.SourceKind, r.StepKind, r.SourceID = "dispatch", "attempt_delivery", "attempt"
		r.TransitionID, r.ProcurementEventID, r.DeliverySourceID, r.DispatchAttemptID = nil, nil, sourcePtr("delivery"), sourcePtr("attempt")
		r.AfterHeldAmount, r.AfterSpentAmount = "1", "0"
		r.BeforeReservationRevision, r.BeforeReservationState = sourcePtr("1"), sourcePtr("prepared")
		r.AfterReservationRevision, r.AfterReservationState, r.AfterReservationHeldAmount, r.AfterReservationSpentAmount = sourcePtr("2"), sourcePtr("sending"), sourcePtr("1"), sourcePtr("0")
		r.GateExtra, r.GateCheckedAt, r.PBDecisionAt = sourcePtr("0"), sourceFinishClock(7), sourceFinishClock(9)
	case "evidence_disputed":
		r.BeforeHeldAmount, r.BeforeSpentAmount = sourcePtr("0"), sourcePtr("1")
		r.AfterState, r.AfterConfigurationRevision, r.AfterBlockingCount = "paused", "2", "1"
		r.BeforeReservationRevision, r.BeforeReservationState, r.BeforeReservationHeldAmount, r.BeforeReservationSpentAmount = sourcePtr("4"), sourcePtr("settled"), sourcePtr("0"), sourcePtr("1")
		r.AfterReservationRevision, r.AfterReservationState, r.AfterReservationAnomaly = sourcePtr("5"), sourcePtr("disputed"), sourceBusinessBool(true)
		r.PauseEventID = sourcePtr("event")
	case "cost_corrected":
		r.BeforeState, r.BeforeConfigurationRevision, r.BeforeHeldAmount, r.BeforeSpentAmount, r.BeforeBlockingCount = sourcePtr("paused"), sourcePtr("2"), sourcePtr("0"), sourcePtr("1"), sourcePtr("1")
		r.AfterState, r.AfterConfigurationRevision, r.AfterSpentAmount = "paused", "2", "0"
		r.BeforeReservationRevision, r.BeforeReservationState, r.BeforeReservationHeldAmount, r.BeforeReservationSpentAmount, r.BeforeReservationAnomaly = sourcePtr("5"), sourcePtr("disputed"), sourcePtr("0"), sourcePtr("1"), sourceBusinessBool(true)
		r.AfterReservationRevision, r.AfterReservationSpentAmount = sourcePtr("6"), sourcePtr("0")
	}
	return r
}

func sourceBusinessRun(t *testing.T, rows Rows, semantic bool) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	if semantic {
		if err := s.semanticRows(); err != nil {
			t.Fatal(err)
		}
	} else {
		s.bad = make(map[string]keySet)
	}
	if err := s.businessUnionRows(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceBusinessUnionSelectedQuotePreservesOriginalAcceptance(t *testing.T) {
	for _, basis := range []string{"unknown", "contract", "invoice"} {
		for _, input := range []*string{nil, sourcePtr("0"), sourcePtr("9223372036854775807")} {
			for _, output := range []*string{nil, sourcePtr("0"), sourcePtr("2")} {
				r := sourceBusinessAttemptLiteral(false)
				r.CostVersionID, r.CostBasis, r.CostInputRate, r.CostOutputRate = sourcePtr("selected-version"), basis, input, output
				s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{r}}}, true)
				if len(s.bad["scalar"]) != 0 {
					t.Fatalf("selected unmanaged quote narrowed: %s %v %v", basis, input, output)
				}
			}
		}
	}
	for _, managed := range []bool{false, true} {
		r := sourceBusinessAttemptLiteral(managed)
		if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{r}}}, true); len(s.bad["scalar"]) != 0 {
			t.Fatal("literal birth rejected")
		}
	}
	for _, mutate := range []func(*AttemptOriginRow){
		func(r *AttemptOriginRow) { r.Sequence = "3" },
		func(r *AttemptOriginRow) { r.PBCreatedXID = "8" },
		func(r *AttemptOriginRow) { r.CostVersionID = nil },
		func(r *AttemptOriginRow) { r.CostInputRate = nil },
		func(r *AttemptOriginRow) { r.CostOutputRate = nil },
		func(r *AttemptOriginRow) { r.CostBasis = "unknown" },
		func(r *AttemptOriginRow) { r.CostInputRate = sourcePtr("-1") },
	} {
		r := sourceBusinessAttemptLiteral(true)
		mutate(&r)
		if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{r}}}, true); len(s.bad["scalar"]) != 1 {
			t.Fatal("managed quote/birth mutation accepted")
		}
	}
	for _, which := range []string{"input", "output", "basis"} {
		r := sourceBusinessAttemptLiteral(false)
		switch which {
		case "input":
			r.CostInputRate = sourcePtr("0")
		case "output":
			r.CostOutputRate = sourcePtr("0")
		case "basis":
			r.CostBasis = "contract"
		}
		if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{r}}}, true); len(s.bad["scalar"]) != 1 {
			t.Fatal("absent selected version accepted quote fields")
		}
	}
}

func TestSourceBusinessUnionUpperAndTransitions(t *testing.T) {
	for _, tc := range []struct{ input, output, ri, ro, upper string }{
		{"1", "1", "1", "1", "1"}, // Combined ceil is 1, separate ceils would be 2.
		{"1", "0", "0", "0", "0"},
		{"1000000", "0", "9223372036854775807", "0", "9223372036854775807"},
	} {
		if !sourceBusinessUpper(tc.input, tc.output, tc.ri, tc.ro, tc.upper) {
			t.Fatalf("literal upper rejected: %+v", tc)
		}
	}
	for _, tc := range [][5]string{{"1", "1", "1", "1", "2"}, {"0", "1", "1", "1", "1"}, {"9223372036854775807", "9223372036854775807", "9223372036854775807", "9223372036854775807", "9223372036854775807"}} {
		if sourceBusinessUpper(tc[0], tc[1], tc[2], tc[3], tc[4]) {
			t.Fatal("wrong or overflowing bound accepted")
		}
	}
	for _, event := range []string{"reserved", "released_no_consumption", "consumption_unknown", "cost_confirmed", "evidence_disputed", "cost_corrected", "expired_without_permit", "recovered_unknown"} {
		t.Run(event, func(t *testing.T) {
			r := sourceBusinessTransitionLiteral(event)
			if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{ProcurementTransitions: []ProcurementTransitionRow{r}}}, true); len(s.bad["scalar"]) != 0 {
				t.Fatalf("literal event rejected: %v", s.bad)
			}
			for _, mutate := range []func(*ProcurementTransitionRow){
				func(r *ProcurementTransitionRow) { r.CostBasis = "unknown" },
				func(r *ProcurementTransitionRow) { r.UpperAmount = "2" },
				func(r *ProcurementTransitionRow) { r.HeldDelta = "99" },
				func(r *ProcurementTransitionRow) { r.SpentDelta = "99" },
				func(r *ProcurementTransitionRow) { r.EventKey = "wrong" },
				func(r *ProcurementTransitionRow) { r.AfterRevision = "9" },
				func(r *ProcurementTransitionRow) { r.AfterState = "sending" },
				func(r *ProcurementTransitionRow) { r.EvidenceRef = "bad\nref" },
			} {
				bad := r
				mutate(&bad)
				if sourceBusinessValues(bad) {
					t.Fatal("event mutation accepted")
				}
			}
			if event == "reserved" {
				for _, field := range []string{"BeforeRevision", "BeforeState", "BeforeHeld", "BeforeSpent"} {
					bad := r
					reflect.ValueOf(&bad).Elem().FieldByName(field).Set(reflect.ValueOf(sourcePtr("0")))
					if sourceBusinessValues(bad) {
						t.Fatalf("reserved before %s not NULL", field)
					}
				}
			} else {
				for _, field := range []string{"BeforeRevision", "BeforeState", "BeforeHeld", "BeforeSpent", "BeforeAnomaly"} {
					bad := r
					reflect.ValueOf(&bad).Elem().FieldByName(field).SetZero()
					if sourceBusinessValues(bad) {
						t.Fatalf("partial before %s", field)
					}
				}
			}
		})
	}
	// Same amount and same tuple keep the revision even at its int64 maximum.
	correction := sourceBusinessTransitionLiteral("cost_corrected")
	correction.BeforeRevision, correction.AfterRevision = sourcePtr("9223372036854775807"), "9223372036854775807"
	if !sourceBusinessValues(correction) {
		t.Fatal("unchanged correction at max revision rejected")
	}
	correction.AfterSpent, correction.SpentDelta, correction.AfterAnomaly = "2", "1", true
	if sourceBusinessValues(correction) {
		t.Fatal("changed correction wrapped max revision")
	}
	dispute := sourceBusinessTransitionLiteral("evidence_disputed")
	dispute.BeforeSpent, dispute.AfterSpent = sourcePtr(strings.Repeat("9", 100)), strings.Repeat("9", 100)
	if !sourceBusinessValues(dispute) {
		t.Fatal("100-digit spent or max(U-S,0) handling narrowed")
	}
}

func TestSourceBusinessUnionUnknownAndConfirmedTargets(t *testing.T) {
	for _, state := range []string{"prepared", "sending"} {
		r := sourceBusinessTransitionLiteral("consumption_unknown")
		r.BeforeState = sourcePtr(state)
		if !sourceBusinessValues(r) {
			t.Fatalf("unknown from %s rejected", state)
		}
	}
	unknown := sourceBusinessTransitionLiteral("consumption_unknown")
	unknown.BeforeState, unknown.AfterRevision = sourcePtr("unknown"), "1"
	if sourceBusinessValues(unknown) {
		t.Fatal("already unknown must skip, not emit an unchanged transition")
	}
	for _, tc := range []struct {
		name, spent string
		anomaly     bool
		valid       bool
	}{
		{"within_upper", "1", false, true},
		{"over_upper_unmarked", "2", false, false},
		{"over_upper_marked", "2", true, true},
		// Actual token bounds are joined elsewhere. A single token beyond a
		// zero-priced bound can leave the amount at or below U.
		{"token_over_bound_cost_within_upper", "1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sourceBusinessTransitionLiteral("cost_confirmed")
			r.AfterSpent, r.SpentDelta, r.AfterAnomaly = tc.spent, tc.spent, tc.anomaly
			if got := sourceBusinessValues(r); got != tc.valid {
				t.Fatalf("accepted=%v, want %v", got, tc.valid)
			}
		})
	}
	dispute := sourceBusinessTransitionLiteral("evidence_disputed")
	if !sourceBusinessValues(dispute) {
		t.Fatal("literal dispute key rejected")
	}
	for _, key := range []string{"conflict:other-attempt:meter:8", "conflict:attempt:other-meter", "conflict:"} {
		bad := dispute
		bad.EventKey = key
		if sourceBusinessValues(bad) {
			t.Fatalf("dispute key %q does not bind actual attempt and evidence", key)
		}
	}
}

func TestSourceBusinessUnionAccountJournal(t *testing.T) {
	for _, kind := range []string{"create", "configure", "configure_rejected", "reserve", "dispatch", "cost_confirmed", "evidence_disputed", "cost_corrected"} {
		t.Run(kind, func(t *testing.T) {
			r := sourceBusinessJournalLiteral(kind)
			if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AccountJournal: []AccountJournalRow{r}}}, true); len(s.bad["scalar"]) != 0 {
				t.Fatalf("literal journal rejected: %v", s.bad)
			}
			for _, mutate := range []func(*AccountJournalRow){
				func(r *AccountJournalRow) { r.StepKind = "incoming" },
				func(r *AccountJournalRow) { r.AfterConfigurationRevision = "9" },
				func(r *AccountJournalRow) { r.AfterHeldAmount = "99" },
				func(r *AccountJournalRow) { r.AfterSpentAmount = "99" },
				func(r *AccountJournalRow) { r.AfterBlockingCount = "99" },
			} {
				bad := r
				mutate(&bad)
				if sourceBusinessValues(bad) {
					t.Fatal("journal mutation accepted")
				}
			}
			if sourceOneOf(kind, "reserve", "dispatch", "cost_confirmed", "evidence_disputed", "cost_corrected") {
				for _, field := range []string{"CostBasis", "CostInputRate", "CostOutputRate", "InputBound", "OutputBound", "UpperAmount", "AfterReservationRevision", "AfterReservationState", "AfterReservationHeldAmount", "AfterReservationSpentAmount", "AfterReservationAnomaly"} {
					bad := r
					reflect.ValueOf(&bad).Elem().FieldByName(field).SetZero()
					if sourceBusinessValues(bad) {
						t.Fatalf("p/quote NULL accepted: %s", field)
					}
				}
			}
		})
	}
	for _, mutate := range []func(*AccountJournalRow){
		func(r *AccountJournalRow) { r.BeforeState = sourcePtr("paused") },
		func(r *AccountJournalRow) { r.BeforeBlockingCount = sourcePtr("1") },
		func(r *AccountJournalRow) { r.BeforeLimitAmount = sourcePtr("0"); r.AfterLimitAmount = "0" },
		func(r *AccountJournalRow) { r.GateExtra = sourcePtr("0") },
		func(r *AccountJournalRow) { r.GateCheckedAt = Time{} },
		func(r *AccountJournalRow) { r.PBDecisionAt = Time{} },
		func(r *AccountJournalRow) { r.AfterReservationState = sourcePtr("sending") },
	} {
		r := sourceBusinessJournalLiteral("reserve")
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("invalid admission gate accepted")
		}
	}
	for _, mutate := range []func(*AccountJournalRow){
		func(r *AccountJournalRow) { r.SourceID = "other-attempt" },
		func(r *AccountJournalRow) { r.GateExtra = sourcePtr("1") },
		func(r *AccountJournalRow) { r.AfterState = "paused"; r.AfterConfigurationRevision = "2" },
		func(r *AccountJournalRow) { r.AfterReservationRevision = sourcePtr("1") },
	} {
		r := sourceBusinessJournalLiteral("dispatch")
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("dispatch changed more than permitted p fields")
		}
	}
	for _, mutate := range []func(*AccountJournalRow){
		func(r *AccountJournalRow) { r.PauseEventID = nil },
		func(r *AccountJournalRow) { r.PauseEventID = sourcePtr("other-event") },
		func(r *AccountJournalRow) { r.AfterState = "active"; r.AfterConfigurationRevision = "1" },
	} {
		r := sourceBusinessJournalLiteral("evidence_disputed")
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("anomaly pause provenance omitted")
		}
	}
	paused := sourceBusinessJournalLiteral("evidence_disputed")
	paused.BeforeState, paused.BeforeConfigurationRevision = sourcePtr("paused"), sourcePtr("2")
	if !sourceBusinessValues(paused) {
		t.Fatal("already-paused event incorrectly changed config revision")
	}
	paused.PauseEventID = nil
	if sourceBusinessValues(paused) {
		t.Fatal("already paused account lost real anomaly event")
	}
	noop := sourceBusinessJournalLiteral("configure_rejected")
	noop.BeforeConfigurationRevision, noop.AfterConfigurationRevision = sourcePtr("9223372036854775807"), "9223372036854775807"
	if !sourceBusinessValues(noop) {
		t.Fatal("no config change at max revision rejected")
	}
	noop.AfterProvider = "new provider"
	if sourceBusinessValues(noop) {
		t.Fatal("config revision overflow accepted")
	}
}

func TestSourceBusinessUnionInputMeterDeliveryAndCommand(t *testing.T) {
	incoming := sourceBusinessIncomingLiteral()
	for _, status := range []string{"confirmed_final", "provisional", "estimated", "missing", "conflicting"} {
		r := incoming
		r.UsageStatus = status
		if status != "confirmed_final" {
			r.InputTokens, r.OutputTokens, r.TotalTokens = "9223372036854775807", "9223372036854775807", "0"
		}
		if !sourceBusinessValues(r) {
			t.Fatalf("incoming five-status acceptance narrowed: %s", status)
		}
	}
	for _, mutate := range []func(*IncomingSourceRow){
		func(r *IncomingSourceRow) { r.TotalTokens = "6" },
		func(r *IncomingSourceRow) { r.CachedTokens = "3" },
		func(r *IncomingSourceRow) { r.ReasoningTokens = "4" },
		func(r *IncomingSourceRow) { r.InputTokens = "9223372036854775808" },
		func(r *IncomingSourceRow) { r.InputTokens = "-1" },
		func(r *IncomingSourceRow) { r.InputDigest = strings.Repeat("A", 64) },
		func(r *IncomingSourceRow) { r.InputDigest = strings.Repeat("a", 63) },
		func(r *IncomingSourceRow) { r.ObservedAtInput = sourceFinishClock(8) },
		func(r *IncomingSourceRow) { r.ObservedAtInput = sourceFinishClock(9); r.ObservedAtInput.Valid = false },
	} {
		r := incoming
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("incoming scalar mutation accepted")
		}
	}
	incoming.ObservedAtInput = sourceFinishClock(9)
	incoming.ObservedAtInput.Time = incoming.ObservedAtInput.Time.In(time.FixedZone("other zone", 8*60*60))
	if !sourceBusinessValues(incoming) {
		t.Fatal("time equality compared wall representation instead of instant")
	}
	for _, mode := range []string{"dispatch_permit", "finalization", "recovery"} {
		for _, after := range []string{"prepared", "sending", "completed", "failed", "unknown"} {
			r := AttemptDeliverySourceRow{Mode: mode, CreatedXID: "7", DispatchXID: sourcePtr("7"), AfterDeliveryState: after}
			want := mode == "dispatch_permit" || mode == "finalization" && sourceOneOf(after, "completed", "failed", "unknown") || mode == "recovery" && sourceOneOf(after, "failed", "unknown")
			if sourceBusinessValues(r) != want {
				t.Fatalf("delivery mode target %s %s", mode, after)
			}
		}
	}
	if sourceBusinessValues(AttemptDeliverySourceRow{Mode: "dispatch_permit", CreatedXID: "7", DispatchXID: sourcePtr("8")}) {
		t.Fatal("permit xid not checked")
	}
	for _, tc := range []struct {
		row  any
		want bool
	}{
		{RequestOriginRow{CreatedXID: "7", PBCreatedXID: "7"}, true},
		{RequestOriginRow{CreatedXID: "7", PBCreatedXID: "8"}, false},
		{MeterChoiceRow{MeterEventID: "meter", MeterOriginID: "meter", CollisionKind: "other_confirmed_usage", CompetingMeterID: sourcePtr("other")}, true},
		{MeterChoiceRow{MeterEventID: "meter", MeterOriginID: "other", CollisionKind: "none"}, false},
		{MeterChoiceRow{MeterEventID: "meter", MeterOriginID: "meter", CollisionKind: "other_confirmed_usage", CompetingMeterID: sourcePtr("meter")}, false},
		{ConflictSourceRow{SourceID: "choice", MeterChoiceID: "choice"}, true},
		{ConflictSourceRow{SourceID: "other", MeterChoiceID: "choice"}, false},
		{RequestDecisionRow{BeforeStatus: "dispatching", AfterStatus: "succeeded", Branch: "normal"}, true},
		{RequestDecisionRow{BeforeStatus: "accepted", AfterStatus: "accepted", Branch: "conflict"}, true},
		{RequestDecisionRow{BeforeStatus: "dispatching", AfterStatus: "dispatching", Branch: "normal"}, false},
		{RequestDecisionRow{BeforeStatus: "accepted", AfterStatus: "failed", Branch: "conflict"}, false},
		{RequestDecisionRow{BeforeStatus: "sending", AfterStatus: "failed", Branch: "normal"}, false},
		{AttemptNodeRow{StepKind: "incoming"}, true},
		{AttemptNodeRow{StepKind: "account_command"}, false},
		{AttemptNodeRow{StepKind: "request_birth"}, false},
		{AttemptNodeRow{StepKind: "reconciliation"}, false},
	} {
		if sourceBusinessValues(tc.row) != tc.want {
			t.Fatalf("literal row expectation failed: %+v", tc.row)
		}
	}
	command := sourceBusinessCommandLiteral()
	if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AccountCommandSources: []AccountCommandSourceRow{command}}}, true); len(s.bad["scalar"]) != 0 {
		t.Fatal("nil UUID original command input or independent clocks rejected")
	}
	for _, field := range []string{"OperationID", "ActorID", "InputDigest"} {
		bad := command
		reflect.ValueOf(&bad).Elem().FieldByName(field).SetString("invalid")
		if sourceBusinessValues(bad) {
			t.Fatalf("command invalid %s", field)
		}
	}
	command.Kind, command.InputTargetID, command.TargetID = "account_configure", "00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000"
	command.InputExpectedRevision, command.CommandStatus, command.CommandReason, command.JournalID = sourcePtr("1"), "rejected", "not_found", nil
	if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{AccountCommandSources: []AccountCommandSourceRow{command}}}, true); len(s.bad["scalar"]) != 0 {
		t.Fatal("canonical nil target for not_found rejected")
	}
	command.InputTargetID = "ABCDEFAB-CDEF-4DEF-8DEF-ABCDEFABCDEF"
	if sourceBusinessValues(command) {
		t.Fatal("noncanonical configure input accepted")
	}
}

func TestSourceBusinessUnionCostPriorityResetEvidenceAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		status, mode, state, decision, amount, basis string
		want                                         bool
	}{
		{"invoice", "managed", "disputed", "skip_already_known", "", "", true},
		{"invoice", "managed", "disputed", "skip_disputed", "", "", false},
		{"unknown", "managed", "disputed", "skip_disputed", "", "", true},
		{"unknown", "managed", "disputed", "write_no_consumption", "0", "contract", false},
		{"unknown", "managed", "prepared", "write_no_consumption", "0", "contract", true},
		{"unknown", "unmanaged", "", "write_no_consumption", "1", "contract", false},
		{"unknown", "unmanaged", "", "write_no_consumption", "0", "invoice", false},
		{"unknown", "unmanaged", "", "write_final_usage", "1", "invoice", true},
		{"unknown", "unmanaged", "", "write_final_usage", "1", "unknown", false},
		{"unknown", "unmanaged", "", "skip_no_final_usage_or_rate", "", "", true},
		{"unknown", "unmanaged", "", "skip_already_known", "", "", false},
	} {
		r := NormalCostDecisionRow{BeforeCostStatus: tc.status, ManagementMode: tc.mode, Decision: tc.decision}
		if tc.state != "" {
			r.BeforeProcurementState = sourcePtr(tc.state)
		}
		if tc.amount != "" {
			r.SelectedAmount, r.SelectedBasis = sourcePtr(tc.amount), sourcePtr(tc.basis)
		}
		if sourceBusinessValues(r) != tc.want {
			t.Fatalf("normal row priority %+v", tc)
		}
	}
	for _, cause := range []string{"normal_cost_decision", "approval_consumed", "unmanaged_correction", "procurement_dispute_reset", "incoming_conflict_reset"} {
		known := CostMutationRow{CauseKind: cause, AfterStatus: "invoice", AfterAmount: sourcePtr("0")}
		unknown := CostMutationRow{CauseKind: cause, AfterStatus: "unknown"}
		reset := strings.HasSuffix(cause, "_reset")
		if sourceBusinessValues(known) == reset || sourceBusinessValues(unknown) != reset {
			t.Fatalf("mutation cause union %s", cause)
		}
	}
	for _, state := range []*string{nil, sourcePtr("prepared"), sourcePtr("sending"), sourcePtr("unknown"), sourcePtr("settled"), sourcePtr("released"), sourcePtr("disputed")} {
		for _, cause := range []string{"procurement_dispute", "incoming_conflict"} {
			for _, policy := range []string{"applied", "suppressed_adjudicated"} {
				r := CostResetDecisionRow{CauseKind: cause, ProcurementStateBefore: state, Policy: policy}
				if policy == "applied" {
					r.CostMutationID = sourcePtr("mutation")
				}
				want := false
				if cause == "procurement_dispute" {
					want = state != nil && policy == "applied"
				} else {
					terminal := state != nil && (*state == "settled" || *state == "released")
					want = terminal && policy == "suppressed_adjudicated" || !terminal && policy == "applied"
				}
				if sourceBusinessValues(r) != want {
					t.Fatal("reset exact state/policy union")
				}
			}
		}
	}
	e := EvidenceChoiceRow{AttemptID: "attempt", ProcurementBeforeState: "settled", IdentityAttemptID: "attempt", EvidenceDigest: strings.Repeat("a", 64), IdentityDigest: strings.Repeat("a", 64), MemberCountBefore: "1", MemberTailBefore: sourcePtr("old"), MemberCountAfter: "1", MemberTailAfter: sourcePtr("old"), Disposition: "existing_member"}
	if !sourceBusinessValues(e) {
		t.Fatal("existing member identity and unchanged cut rejected")
	}
	for _, mutate := range []func(*EvidenceChoiceRow){
		func(r *EvidenceChoiceRow) { r.IdentityAttemptID = "other" },
		func(r *EvidenceChoiceRow) { r.IdentityDigest = strings.Repeat("b", 64) },
		func(r *EvidenceChoiceRow) {
			r.EvidenceDigest = strings.Repeat("A", 64)
			r.IdentityDigest = r.EvidenceDigest
		},
		func(r *EvidenceChoiceRow) { r.MemberCountAfter = "2" },
		func(r *EvidenceChoiceRow) { r.MemberTailAfter = sourcePtr("other") },
		func(r *EvidenceChoiceRow) { r.ProcurementBeforeState = "other" },
	} {
		bad := e
		mutate(&bad)
		if sourceBusinessValues(bad) {
			t.Fatal("evidence row mutation accepted")
		}
	}
	e.Disposition, e.MemberCountAfter, e.MemberTailAfter = "new_member", "2", sourcePtr("new")
	if !sourceBusinessValues(e) {
		t.Fatal("new member count increment rejected")
	}
	e.MemberCountBefore, e.MemberCountAfter = "9223372036854775807", "0"
	if sourceBusinessValues(e) {
		t.Fatal("member counter wrapped")
	}
	member := EvidenceMemberRow{IdentityDigest: strings.Repeat("b", 64), SourceID: "choice", EvidenceChoiceID: "choice"}
	if !sourceBusinessValues(member) {
		t.Fatal("member literal rejected")
	}
	member.SourceID = "other"
	if sourceBusinessValues(member) {
		t.Fatal("member source identity accepted")
	}
	for _, p := range []string{"prepared", "sending"} {
		for _, d := range []string{"prepared", "sending", "completed", "failed", "unknown"} {
			for _, permit := range []*string{nil, sourcePtr("permit")} {
				wantDecision := "recovered_unknown"
				if p == "prepared" && d == "prepared" && permit == nil {
					wantDecision = "expired_without_permit"
				}
				r := RecoverySourceRow{ProcurementBeforeState: p, DeliveryBeforeState: d, PermitSourceID: permit, Decision: wantDecision, CheckedAt: sourceFinishClock(10), RequestDeadline: sourceFinishClock(10)}
				if !sourceBusinessValues(r) {
					t.Fatal("eligible recovery rejected")
				}
				r.CheckedAt = sourceFinishClock(9)
				if sourceBusinessValues(r) {
					t.Fatal("recovery before deadline accepted")
				}
				r.CheckedAt = sourceFinishClock(11)
				if wantDecision == "recovered_unknown" {
					r.Decision = "expired_without_permit"
				} else {
					r.Decision = "recovered_unknown"
				}
				if sourceBusinessValues(r) {
					t.Fatal("recovery permit branch guessed")
				}
			}
		}
	}
}

func TestSourceBusinessUnionApprovalCorrectionAndSignedLedger(t *testing.T) {
	approval := ApprovalConsumptionRow{ProposalID: "00000000-0000-0000-0000-000000000000", ApprovalOperationID: sourceBusinessID, ConsumedRevision: "4", ConsumedState: "settled", ConsumedHeld: "0", ConsumedSpent: "1", AfterRevision: "4", AfterState: "settled", AfterHeld: "0", AfterSpent: "1"}
	if !sourceBusinessValues(approval) {
		t.Fatal("same-tuple approval and nil original operation ID rejected")
	}
	for _, mutate := range []func(*ApprovalConsumptionRow){
		func(r *ApprovalConsumptionRow) { r.ConsumedState = "prepared" },
		func(r *ApprovalConsumptionRow) { r.AfterHeld = "1" },
		func(r *ApprovalConsumptionRow) { r.AfterState = "unknown" },
		func(r *ApprovalConsumptionRow) { r.AfterRevision = "5" },
		func(r *ApprovalConsumptionRow) { r.AfterSpent = "2" },
		func(r *ApprovalConsumptionRow) { r.ApprovalOperationID = r.ProposalID },
		func(r *ApprovalConsumptionRow) { r.ProposalID = "invalid" },
	} {
		r := approval
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("approval target/revision/identity mutation accepted")
		}
	}
	correction := UnmanagedCorrectionSourceRow{OperationID: "freeform/operation:8", EvidenceRef: "statement:8", EffectiveAuditActor: "system"}
	if !sourceBusinessValues(correction) {
		t.Fatal("unmanaged original operation string narrowed to UUID")
	}
	correction.InputActorID, correction.EffectiveAuditActor = sourcePtr("any original actor"), "any original actor"
	if !sourceBusinessValues(correction) {
		t.Fatal("unmanaged actor gained new role or ID restriction")
	}
	for _, mutate := range []func(*UnmanagedCorrectionSourceRow){
		func(r *UnmanagedCorrectionSourceRow) { r.OperationID = "" },
		func(r *UnmanagedCorrectionSourceRow) { r.OperationID = strings.Repeat("x", 201) },
		func(r *UnmanagedCorrectionSourceRow) { r.InputActorID = sourcePtr(""); r.EffectiveAuditActor = "" },
		func(r *UnmanagedCorrectionSourceRow) { r.InputActorID = nil },
		func(r *UnmanagedCorrectionSourceRow) { r.EffectiveAuditActor = "other" },
		func(r *UnmanagedCorrectionSourceRow) { r.EvidenceRef = "https://supplier/bill" },
	} {
		r := correction
		mutate(&r)
		if sourceBusinessValues(r) {
			t.Fatal("correction actor/input mutation accepted")
		}
	}
	ledger := CustomerFinishLedgerEffectRow{FinishOutcomeID: "outcome", RootID: sourceBusinessRoot, StepNo: "1", StepKind: "reconciliation", SourceID: sourceBusinessID, CreatedXID: "7", TenantID: "tenant", AccountID: "revenue", AccountKind: "REVENUE", BeforeBalance: "-10", AfterBalance: "-3", SignedDelta: "7", JournalLineID: "line"}
	if s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{CustomerFinishLedgerEffects: []CustomerFinishLedgerEffectRow{ledger}}}, true); len(s.bad["scalar"]) != 0 {
		t.Fatal("signed REVENUE rejected by generic+business union")
	}
	for _, tc := range []struct {
		kind, before, after, delta string
		tenant                     *string
		want                       bool
	}{
		{"REVENUE", "-9223372036854775808", "-1", "9223372036854775807", nil, true},
		{"REVENUE", "9223372036854775807", "-9223372036854775808", "1", nil, false},
		{"REVENUE", "0", "0", "0", sourcePtr("tenant"), false},
		{"AVAILABLE", "10", "17", "7", sourcePtr("tenant"), true},
		{"HELD", "10", "0", "-10", sourcePtr("tenant"), true},
		{"HELD", "-10", "0", "10", sourcePtr("tenant"), false},
		{"AVAILABLE", "0", "-1", "-1", sourcePtr("tenant"), false},
		{"AVAILABLE", "10", "17", "7", sourcePtr("other"), false},
		{"AVAILABLE", "10", "17", "7", nil, false},
		{"REVENUE", "-10", "-2", "7", nil, false},
	} {
		r := ledger
		r.AccountKind, r.BeforeBalance, r.AfterBalance, r.SignedDelta, r.AccountTenantID = tc.kind, tc.before, tc.after, tc.delta, tc.tenant
		s := sourceBusinessRun(t, Rows{PrefixRows: PrefixRows{CustomerFinishLedgerEffects: []CustomerFinishLedgerEffectRow{r}}}, true)
		if (len(s.bad["scalar"]) == 0) != tc.want {
			t.Fatalf("ledger original acceptance %+v: %v", tc, s.bad)
		}
	}
}

func TestSourceBusinessUnionDedupBudgetsAndIncompleteEntry(t *testing.T) {
	in := sourceBusinessIncomingLiteral()
	in.TotalTokens, in.CachedTokens = "99", "99"
	tr := sourceBusinessTransitionLiteral("reserved")
	tr.AfterRevision, tr.HeldDelta = "2", "99"
	// Identical text IDs in different physical tables remain two scalar rows.
	rows := Rows{PrefixRows: PrefixRows{IncomingSources: []IncomingSourceRow{in, in}, ProcurementTransitions: []ProcurementTransitionRow{tr}}}
	s := sourceBusinessRun(t, rows, true)
	if err := s.businessUnionRows(); err != nil || len(s.bad["scalar"]) != 2 {
		t.Fatalf("natural row identity not deduplicated: %v %v", s.bad, err)
	}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := checkSourceClosure(w, rows)
	if err != nil || got.Stats.InvalidScalarRows != 2 || got.Complete || len(got.Qualified) != 0 || len(got.Unimplemented) == 0 {
		t.Fatalf("business scalar leaf changed entry proof gate: %+v %v", got, err)
	}
	if err := (*sourceClosureState)(nil).businessUnionRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil shared work accepted")
	}
	s = sourceTestRows(t, rows)
	s.w.b.deadline = time.Now().Add(-time.Second)
	if err := s.businessUnionRows(); !errors.Is(err, ErrTimeout) {
		t.Fatal("business leaf restarted absolute deadline")
	}
	many := Rows{PrefixRows: PrefixRows{IncomingSources: make([]IncomingSourceRow, 600)}}
	for i := range many.PrefixRows.IncomingSources {
		many.PrefixRows.IncomingSources[i] = sourceBusinessIncomingLiteral()
	}
	s = sourceTestRows(t, many)
	s.w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 3}
	if err := s.businessUnionRows(); !errors.Is(err, ErrCanceled) {
		t.Fatalf("business loop ignored shared cancellation: %v", err)
	}
	for _, which := range []string{"cancel", "timeout", "resource"} {
		input := Rows{PrefixRows: PrefixRows{IncomingSources: []IncomingSourceRow{sourceBusinessIncomingLiteral()}}}
		work := sourceUsageWork(t)
		want := ErrCanceled
		switch which {
		case "cancel":
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			work.b.ctx = ctx
		case "timeout":
			work.b.deadline, want = time.Now().Add(-time.Second), ErrTimeout
		case "resource":
			input.PrefixRows.IncomingSources[0].Source, want = strings.Repeat("x", int(fieldLimit)+1), ErrResource
		}
		got, err := checkSourceClosure(work, input)
		if !errors.Is(err, want) || !reflect.DeepEqual(got, sourceClosureResult{}) {
			t.Fatalf("%s exposed partial result: %+v %v", which, got, err)
		}
	}
}
