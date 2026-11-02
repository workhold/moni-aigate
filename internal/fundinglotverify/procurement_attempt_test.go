package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func procurementAttemptPtr(value string) *string { return &value }

func validProcurementAttemptRows() ProcurementAttemptBindingRows {
	return ProcurementAttemptBindingRows{
		Channels:        []ProcurementAttemptChannelRow{{ID: "channel-a"}},
		Credentials:     []ProcurementAttemptCredentialRow{{ID: "credential-a", ChannelID: "channel-a"}},
		Versions:        []ProcurementAttemptVersionRow{{ID: "version-a", ChannelID: "channel-a", CredentialID: "credential-a"}},
		PoolMemberships: []ProcurementAttemptPoolMemberRow{{ChannelVersionID: "version-a", ChannelID: "channel-a", CredentialID: "credential-a", Position: 1, Weight: 1, MaxInflight: 10}},
		Tenants:         []ProcurementAttemptTenantRow{{ID: "tenant-a"}},
		Requests:        []ProcurementAttemptRequestRow{{ID: "request-a", TenantID: "tenant-a", InputUpperBound: "100", OutputLimit: "20"}},
		CostVersions:    []ProcurementAttemptCostVersionRow{{ID: "cost-a", ChannelID: "channel-a", UpstreamModel: "model-a", InputRate: procurementAttemptPtr("2000000"), OutputRate: procurementAttemptPtr("1000000"), Basis: "contract", EffectiveAt: time.Unix(100, 0), CreatedAt: time.Unix(90, 0)}},
		Accounts:        []ProcurementBudgetAccountRow{{ID: "account-a", HeldAmount: "220", SpentAmount: "0"}},
		Bindings:        []ProcurementAttemptBindingRow{{ID: "binding-a", CredentialID: "credential-a", ChannelID: "channel-a", AccountID: "account-a", OperationID: "operation-a", BoundAt: time.Unix(150, 0)}},
		Attempts: []ProcurementAttemptRow{{ID: "attempt-a", TenantID: "tenant-a", RequestID: "request-a", Sequence: 1,
			ChannelID: "channel-a", ChannelVersionID: "version-a", CredentialID: "credential-a", UpstreamModel: "model-a", DeliveryState: "prepared",
			CostVersionID: procurementAttemptPtr("cost-a"), CostInputRate: procurementAttemptPtr("2000000"), CostOutputRate: procurementAttemptPtr("1000000"),
			CostBasis: "contract", CostStatus: "unknown", CreatedAt: time.Unix(200, 0)}},
		Reservations: []ProcurementAttemptReservationRow{{AttemptID: "attempt-a", AccountID: "account-a", BindingID: "binding-a", InputBound: "100", OutputBound: "20", UpperAmount: "220", HeldAmount: "220", SpentAmount: "0", State: "prepared"}},
		BudgetEvents: []ProcurementBudgetEventRow{{ID: "event-a", EventKey: "reserve-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "220", SpentDelta: "0"}},
	}
}

func TestCheckProcurementAttemptBindingGraphBalancedSnapshotAndLegacyAttempt(t *testing.T) {
	rows := validProcurementAttemptRows()
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || !got.Balanced || got.Scope != "procurement_attempt_binding_graph_v1" || got.Mismatches != (ProcurementAttemptBindingMismatches{}) {
		t.Fatalf("valid graph did not balance: %+v %v", got, err)
	}
	if got.Scanned != (ProcurementAttemptBindingCounts{Channels: 1, Credentials: 1, Versions: 1, PoolMemberships: 1, Tenants: 1, Requests: 1, CostVersions: 1, Accounts: 1, Bindings: 1, Attempts: 1, Reservations: 1, BudgetEvents: 1}) {
		t.Fatalf("source row counts are incomplete: %+v", got.Scanned)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "channel-a") || strings.Contains(string(raw), "attempt-a") {
		t.Fatalf("summary disclosed source identifiers: %s %v", raw, err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	scanned, ok := encoded["scanned"].(map[string]any)
	if !ok || scanned["pool_memberships"] != float64(1) || scanned["budget_events"] != float64(1) {
		t.Fatalf("scanned JSON keys are not stable snake_case: %s", raw)
	}

	rows.Bindings[0].BoundAt = time.Unix(300, 0)
	rows.Reservations = nil // A binding created after this historical attempt must not backfill it.
	got, err = CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || !got.Balanced {
		t.Fatalf("later binding incorrectly invalidated the legacy attempt: %+v %v", got, err)
	}
}

func TestCheckProcurementAttemptBindingGraphUsesExactCeilingAndKnownQuote(t *testing.T) {
	rows := validProcurementAttemptRows()
	rows.Requests[0].InputUpperBound, rows.Requests[0].OutputLimit = "1", "0"
	rows.CostVersions[0].InputRate, rows.CostVersions[0].OutputRate = procurementAttemptPtr("1"), procurementAttemptPtr("0")
	rows.Attempts[0].CostInputRate, rows.Attempts[0].CostOutputRate = procurementAttemptPtr("1"), procurementAttemptPtr("0")
	rows.Reservations[0].InputBound, rows.Reservations[0].OutputBound = "1", "0"
	rows.Reservations[0].UpperAmount, rows.Reservations[0].HeldAmount = "1", "1" // ceil(1 / 1,000,000) is exactly 1.
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || !got.Balanced {
		t.Fatalf("exact big-integer ceiling failed: %+v %v", got, err)
	}

	rows = validProcurementAttemptRows()
	rows.Attempts[0].CostVersionID = nil
	rows.Attempts[0].CostInputRate, rows.Attempts[0].CostOutputRate = nil, nil
	rows.Attempts[0].CostBasis = "unknown"
	got, err = CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || got.Mismatches.AttemptCostQuoteRows != 0 || got.Mismatches.ReservationUpperBoundRows != 1 {
		t.Fatalf("reservation without a known procurement quote was not isolated: %+v %v", got.Mismatches, err)
	}

	rows = validProcurementAttemptRows()
	rows.Attempts[0].CostStatus, rows.Attempts[0].CostAmount = "invoice", procurementAttemptPtr("999")
	got, err = CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || !got.Balanced {
		t.Fatalf("final supplier-cost correction was incorrectly tied to quote basis: %+v %v", got, err)
	}
}

func TestCheckProcurementAttemptBindingGraphReportsRelationAndReservationMismatches(t *testing.T) {
	rows := validProcurementAttemptRows()
	rows.Attempts[0].TenantID = "tenant-missing"
	rows.Attempts[0].ChannelVersionID = "version-missing"
	rows.Attempts[0].CostVersionID = procurementAttemptPtr("cost-missing")
	rows.Reservations[0].BindingID = "binding-missing"
	rows.Reservations[0].InputBound = "101"
	rows.Reservations[0].UpperAmount = "220"
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := ProcurementAttemptBindingMismatches{
		AttemptRequestTenantRows: 1, AttemptVersionIdentityRows: 1, AttemptPoolMembershipRows: 1,
		AttemptCostQuoteRows: 1, ReservationIdentityRows: 1, ReservationRequestBoundRows: 1,
		ReservationUpperBoundRows: 1,
	}
	if got.Balanced || got.Mismatches != want {
		t.Fatalf("graph relation mismatches differ: got %+v want %+v", got.Mismatches, want)
	}

	rows = validProcurementAttemptRows()
	rows.Reservations = nil
	got, err = CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || got.Mismatches.AttemptReservationMissingRows != 1 {
		t.Fatalf("managed attempt without a reservation was not detected: %+v %v", got.Mismatches, err)
	}
}

func TestCheckProcurementAttemptBindingGraphCountsIndependentUniqueKeysWithoutCascading(t *testing.T) {
	rows := validProcurementAttemptRows()
	rows.Channels = append(rows.Channels, rows.Channels[0])
	rows.PoolMemberships = append(rows.PoolMemberships,
		rows.PoolMemberships[0], // duplicate PK and duplicate position
		ProcurementAttemptPoolMemberRow{ChannelVersionID: "version-a", ChannelID: "channel-a", CredentialID: "credential-b", Position: 1, Weight: 1, MaxInflight: 1}, // unique PK, duplicate position
	)
	rows.Credentials = append(rows.Credentials, ProcurementAttemptCredentialRow{ID: "credential-b", ChannelID: "channel-a"})
	rows.Bindings = append(rows.Bindings, rows.Bindings[0])
	rows.Attempts = append(rows.Attempts, rows.Attempts[0])
	rows.Reservations = append(rows.Reservations, rows.Reservations[0])
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mismatches.DuplicateChannelRows != 1 || got.Mismatches.DuplicatePoolMembershipRows != 1 || got.Mismatches.DuplicatePoolPositionRows != 2 ||
		got.Mismatches.DuplicateBindingRows != 1 || got.Mismatches.DuplicateBindingCredentialRows != 1 || got.Mismatches.DuplicateBindingOperationRows != 1 ||
		got.Mismatches.DuplicateAttemptRows != 1 || got.Mismatches.DuplicateAttemptRequestSequenceRows != 1 || got.Mismatches.DuplicateAttemptRequestChannelRows != 1 ||
		got.Mismatches.DuplicateReservationRows != 1 {
		t.Fatalf("unique key rows were not independently counted: %+v", got.Mismatches)
	}
	if got.Mismatches.AttemptPoolMembershipRows != 0 || got.Mismatches.VersionPrimaryMembershipRows != 0 || got.Mismatches.AttemptReservationMissingRows != 0 {
		t.Fatalf("duplicate rows cascaded into graph absences: %+v", got.Mismatches)
	}
}

func TestCheckProcurementAttemptBindingGraphSeparatesTextAmountEnumSequenceAndPoolBounds(t *testing.T) {
	rows := validProcurementAttemptRows()
	rows.Requests[0].InputUpperBound, rows.Requests[0].OutputLimit = "01", "+1"
	rows.CostVersions[0].InputRate, rows.CostVersions[0].OutputRate = procurementAttemptPtr("-0"), procurementAttemptPtr("01")
	rows.Accounts[0].HeldAmount, rows.Accounts[0].SpentAmount = "-1", strings.Repeat("9", 101)
	rows.Attempts[0].CostInputRate, rows.Attempts[0].CostOutputRate = procurementAttemptPtr("9223372036854775808"), procurementAttemptPtr("01")
	rows.Attempts[0].CostAmount = procurementAttemptPtr("01")
	rows.Attempts[0].Sequence = 3
	rows.Attempts[0].DeliveryState = "not-a-state"
	rows.Reservations[0].InputBound, rows.Reservations[0].OutputBound = "0", "01"
	rows.Reservations[0].UpperAmount, rows.Reservations[0].HeldAmount, rows.Reservations[0].SpentAmount = "9223372036854775808", "-1", "01"
	rows.Reservations[0].State = "not-a-state"
	rows.BudgetEvents[0].HeldDelta, rows.BudgetEvents[0].SpentDelta = "-0", "+1"
	rows.PoolMemberships[0].Position = 33
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mismatches.InvalidAmountRows != 6 || got.Mismatches.InvalidAttemptSequenceRows != 1 || got.Mismatches.InvalidEnumRows != 2 || got.Mismatches.InvalidPoolBoundsRows != 1 {
		t.Fatalf("row-level validation was not separated or counted once: %+v", got.Mismatches)
	}

	rows = validProcurementAttemptRows()
	rows.Requests[0].InputUpperBound = ""
	rows.Credentials[0].ChannelID = string([]byte{0xff})
	got, err = CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || got.Mismatches.InvalidTextRows != 2 {
		t.Fatalf("invalid UTF-8 and required text were not counted per source row: %+v %v", got.Mismatches, err)
	}
}

func TestCheckProcurementAttemptBindingGraphFlagsUnrepresentableUpperQuote(t *testing.T) {
	const max = "9223372036854775807"
	rows := validProcurementAttemptRows()
	rows.Requests[0].InputUpperBound, rows.Requests[0].OutputLimit = max, "0"
	rows.CostVersions[0].InputRate, rows.CostVersions[0].OutputRate = procurementAttemptPtr(max), procurementAttemptPtr("0")
	rows.Attempts[0].CostInputRate, rows.Attempts[0].CostOutputRate = procurementAttemptPtr(max), procurementAttemptPtr("0")
	rows.Reservations[0].InputBound, rows.Reservations[0].OutputBound = max, "0"
	rows.Reservations[0].UpperAmount = max
	got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows)
	if err != nil || got.Mismatches.ReservationUpperBoundRows != 1 || got.Mismatches.InvalidAmountRows != 0 {
		t.Fatalf("unrepresentable exact quote upper bound was not isolated: %+v %v", got.Mismatches, err)
	}
}

func TestCheckProcurementAttemptBindingGraphResourceAndContextErrorsAreAtomic(t *testing.T) {
	rows := ProcurementAttemptBindingRows{Channels: make([]ProcurementAttemptChannelRow, fundingSourceRowLimit+1)}
	if got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows); !errors.Is(err, ErrProcurementAttemptBindingRowsResource) || got != (ProcurementAttemptBindingCheck{}) {
		t.Fatalf("row limit did not fail atomically: %+v %v", got, err)
	}
	rows = ProcurementAttemptBindingRows{Channels: []ProcurementAttemptChannelRow{{ID: strings.Repeat("x", fundingSourceByteLimit)}}}
	if got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows); err != nil || !got.Balanced {
		t.Fatalf("exact byte limit rejected: %+v %v", got, err)
	}
	rows.Channels[0].ID += "x"
	if got, err := CheckProcurementAttemptBindingGraph(context.Background(), rows); !errors.Is(err, ErrProcurementAttemptBindingRowsResource) || got != (ProcurementAttemptBindingCheck{}) {
		t.Fatalf("byte limit did not fail atomically: %+v %v", got, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := CheckProcurementAttemptBindingGraph(canceled, validProcurementAttemptRows()); !errors.Is(err, ErrProcurementAttemptBindingRowsCanceled) || got != (ProcurementAttemptBindingCheck{}) {
		t.Fatalf("cancellation leaked a partial result: %+v %v", got, err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	if got, err := CheckProcurementAttemptBindingGraph(deadline, validProcurementAttemptRows()); !errors.Is(err, ErrProcurementAttemptBindingRowsTimeout) || got != (ProcurementAttemptBindingCheck{}) {
		t.Fatalf("deadline leaked a partial result: %+v %v", got, err)
	}
	if got, err := CheckProcurementAttemptBindingGraph(nil, validProcurementAttemptRows()); !errors.Is(err, ErrProcurementAttemptBindingRowsInput) || got != (ProcurementAttemptBindingCheck{}) {
		t.Fatalf("nil context was accepted: %+v %v", got, err)
	}
}
