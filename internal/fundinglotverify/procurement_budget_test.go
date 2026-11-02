package fundinglotverify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func validProcurementBudgetRows() ProcurementBudgetRows {
	return ProcurementBudgetRows{
		Accounts:     []ProcurementBudgetAccountRow{{ID: "account-a", HeldAmount: "5", SpentAmount: "7"}},
		Reservations: []ProcurementBudgetReservationRow{{AttemptID: "attempt-a", AccountID: "account-a", UpperAmount: "20", HeldAmount: "5", SpentAmount: "7", State: "unknown"}},
		Events: []ProcurementBudgetEventRow{
			{ID: "event-a", EventKey: "reserve:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "12", SpentDelta: "10"},
			{ID: "event-b", EventKey: "settle:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "-7", SpentDelta: "-3"},
		},
	}
}

func TestCheckProcurementBudgetProjectionMatchesReservationsAndSignedEvents(t *testing.T) {
	rows := validProcurementBudgetRows()
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Balanced || got.Scope != "procurement_budget_projection_v1" ||
		got.Scanned != (ProcurementBudgetRowCounts{Accounts: 1, Reservations: 1, Events: 2}) ||
		got.Mismatches != (ProcurementBudgetProjectionMismatches{}) {
		t.Fatalf("unexpected projection result: %+v", got)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "account-a") || strings.Contains(string(raw), "attempt-a") {
		t.Fatalf("summary disclosed source identifiers: %s %v", raw, err)
	}
}

func TestCheckProcurementBudgetProjectionUsesBigIntegerForAggregateTotals(t *testing.T) {
	const max = "9223372036854775807"
	rows := ProcurementBudgetRows{
		Accounts: []ProcurementBudgetAccountRow{{ID: "a", HeldAmount: "18446744073709551614", SpentAmount: "0"}},
		Reservations: []ProcurementBudgetReservationRow{
			{AttemptID: "x", AccountID: "a", UpperAmount: max, HeldAmount: max, SpentAmount: "0", State: "unknown"},
			{AttemptID: "y", AccountID: "a", UpperAmount: max, HeldAmount: max, SpentAmount: "0", State: "unknown"},
		},
		Events: []ProcurementBudgetEventRow{
			{ID: "e1", EventKey: "k1", AccountID: "a", AttemptID: "x", HeldDelta: max, SpentDelta: "0"},
			{ID: "e2", EventKey: "k2", AccountID: "a", AttemptID: "y", HeldDelta: max, SpentDelta: "0"},
		},
	}
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil || !got.Balanced {
		t.Fatalf("large exact aggregate failed: %+v %v", got, err)
	}
}

func TestCheckProcurementBudgetProjectionCountsProjectionOrphanAndDuplicateMismatches(t *testing.T) {
	rows := validProcurementBudgetRows()
	rows.Accounts[0].HeldAmount = "6"
	rows.Accounts[0].SpentAmount = "8"
	rows.Reservations = append(rows.Reservations,
		ProcurementBudgetReservationRow{AttemptID: "attempt-a", AccountID: "account-a", UpperAmount: "0", HeldAmount: "0", SpentAmount: "0", State: "released"},
		ProcurementBudgetReservationRow{AttemptID: "orphan-attempt", AccountID: "no-account", UpperAmount: "1", HeldAmount: "0", SpentAmount: "0", State: "released"},
	)
	rows.Events = append(rows.Events,
		ProcurementBudgetEventRow{ID: "event-a", EventKey: "another-key", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "100", SpentDelta: "0"},
		ProcurementBudgetEventRow{ID: "event-c", EventKey: "settle:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "0", SpentDelta: "0"},
		ProcurementBudgetEventRow{ID: "event-orphan", EventKey: "orphan", AccountID: "no-account", AttemptID: "missing", HeldDelta: "0", SpentDelta: "0"},
	)
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	want := ProcurementBudgetProjectionMismatches{
		DuplicateReservations: 1, DuplicateEvents: 1, DuplicateEventKeys: 1,
		OrphanReservations: 1, OrphanEvents: 1,
		AccountHeldReservationMismatches: 1, AccountSpentReservationMismatches: 1,
		AccountHeldEventMismatches: 1, AccountSpentEventMismatches: 1,
	}
	if got.Balanced || got.Mismatches != want {
		t.Fatalf("got %+v, want %+v", got.Mismatches, want)
	}
}

func TestCheckProcurementBudgetProjectionCountsOrphansOnDuplicateRows(t *testing.T) {
	rows := validProcurementBudgetRows()
	rows.Reservations = append(rows.Reservations,
		ProcurementBudgetReservationRow{AttemptID: "missing", AccountID: "absent", UpperAmount: "1", HeldAmount: "0", SpentAmount: "0", State: "released"},
		ProcurementBudgetReservationRow{AttemptID: "missing", AccountID: "absent", UpperAmount: "1", HeldAmount: "0", SpentAmount: "0", State: "released"},
	)
	rows.Events = append(rows.Events,
		ProcurementBudgetEventRow{ID: "orphan-event", EventKey: "orphan-event-key", AccountID: "absent", AttemptID: "missing", HeldDelta: "0", SpentDelta: "0"},
		ProcurementBudgetEventRow{ID: "orphan-event", EventKey: "orphan-event-key-2", AccountID: "absent", AttemptID: "missing", HeldDelta: "0", SpentDelta: "0"},
	)
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	want := ProcurementBudgetProjectionMismatches{
		DuplicateReservations: 1, DuplicateEvents: 1,
		OrphanReservations: 2, OrphanEvents: 2,
	}
	if err != nil || got.Mismatches != want {
		t.Fatalf("duplicate orphan rows must retain row-level diagnostics: %+v %v", got.Mismatches, err)
	}
}

func TestCheckProcurementBudgetProjectionFlagsAmountStateAndTextShape(t *testing.T) {
	rows := validProcurementBudgetRows()
	rows.Accounts[0].SpentAmount = "01"
	rows.Reservations[0].UpperAmount = "9223372036854775808"
	rows.Reservations[0].HeldAmount = "-1"
	rows.Reservations[0].State = "not-a-state"
	rows.Events[0].EventKey = ""
	rows.Events[0].HeldDelta = ""
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	if got.Balanced || got.Mismatches.InvalidTextRows != 1 || got.Mismatches.InvalidAmountRows != 3 || got.Mismatches.ReservationStateMismatches != 1 {
		t.Fatalf("malformed stored rows were not reported locally: %+v", got)
	}
	rows = validProcurementBudgetRows()
	rows.Events[0].HeldDelta = "0"
	rows.Reservations[0].State = "prepared"
	rows.Reservations[0].Anomaly = true
	got, err = CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil || got.Mismatches.ReservationBucketMismatches != 1 {
		t.Fatalf("prepared anomaly/bucket mismatch not reported: %+v %v", got, err)
	}
}

func TestCheckProcurementBudgetProjectionValidatesAmountsOnDuplicateRows(t *testing.T) {
	rows := validProcurementBudgetRows()
	duplicate := rows.Accounts[0]
	duplicate.HeldAmount = ""
	rows.Accounts = append(rows.Accounts, duplicate)
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil || got.Mismatches.DuplicateAccounts != 1 || got.Mismatches.InvalidTextRows != 1 || got.Mismatches.InvalidAmountRows != 1 {
		t.Fatalf("duplicate row skipped independent shape accounting: %+v %v", got, err)
	}
}

func TestCheckProcurementBudgetProjectionRejectsResourceAndContextFailuresAtomically(t *testing.T) {
	rows := ProcurementBudgetRows{Accounts: make([]ProcurementBudgetAccountRow, fundingSourceRowLimit+1)}
	if got, err := CheckProcurementBudgetProjection(context.Background(), rows); !errors.Is(err, ErrProcurementBudgetRowsResource) || got != (ProcurementBudgetProjectionCheck{}) {
		t.Fatalf("row bound did not fail atomically: %+v %v", got, err)
	}
	rows = ProcurementBudgetRows{Accounts: []ProcurementBudgetAccountRow{{ID: "a", HeldAmount: "0", SpentAmount: strings.Repeat("x", fundingSourceByteLimit)}}}
	if got, err := CheckProcurementBudgetProjection(context.Background(), rows); !errors.Is(err, ErrProcurementBudgetRowsResource) || got != (ProcurementBudgetProjectionCheck{}) {
		t.Fatalf("byte bound did not fail atomically: %+v %v", got, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := CheckProcurementBudgetProjection(canceled, validProcurementBudgetRows()); !errors.Is(err, ErrProcurementBudgetRowsCanceled) || got != (ProcurementBudgetProjectionCheck{}) {
		t.Fatalf("canceled input leaked a result: %+v %v", got, err)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer stop()
	if got, err := CheckProcurementBudgetProjection(deadline, validProcurementBudgetRows()); !errors.Is(err, ErrProcurementBudgetRowsTimeout) || got != (ProcurementBudgetProjectionCheck{}) {
		t.Fatalf("expired input leaked a result: %+v %v", got, err)
	}
	if got, err := CheckProcurementBudgetProjection(nil, validProcurementBudgetRows()); !errors.Is(err, ErrProcurementBudgetRowsInput) || got != (ProcurementBudgetProjectionCheck{}) {
		t.Fatalf("nil context accepted: %+v %v", got, err)
	}
}

func TestCheckProcurementBudgetProjectionCountsDuplicateAccountsAndRows(t *testing.T) {
	rows := validProcurementBudgetRows()
	rows.Accounts = append(rows.Accounts, rows.Accounts[0])
	rows.Reservations = append(rows.Reservations, ProcurementBudgetReservationRow{AttemptID: "orphan", AccountID: "account-a", UpperAmount: "1", HeldAmount: "1", SpentAmount: "0", State: "prepared"})
	rows.Events = append(rows.Events, ProcurementBudgetEventRow{ID: "event-c", EventKey: "settle:attempt-a", AccountID: "account-a", AttemptID: "attempt-a", HeldDelta: "0", SpentDelta: "0"})
	got, err := CheckProcurementBudgetProjection(context.Background(), rows)
	if err != nil || got.Mismatches.DuplicateAccounts != 1 || got.Mismatches.DuplicateReservations != 0 || got.Mismatches.DuplicateEvents != 0 || got.Mismatches.DuplicateEventKeys != 1 || got.Balanced {
		t.Fatalf("duplicate identity counters unexpected: %+v %v", got, err)
	}
}

func TestProcurementBudgetCanonicalIntegers(t *testing.T) {
	valid := []string{"0", "1", "-1", "9223372036854775808", strings.Repeat("9", 100)}
	for _, value := range valid {
		var ok bool
		if strings.HasPrefix(value, "-") {
			_, ok = procurementBudgetSigned(value)
		} else {
			_, ok = procurementBudgetUnsigned(value)
		}
		if !ok {
			t.Fatalf("valid canonical integer rejected: %q", value)
		}
	}
	invalid := []string{"", "+1", "01", "-0", "-01", strings.Repeat("9", 101)}
	for _, value := range invalid {
		if _, ok := procurementBudgetUnsigned(value); ok {
			t.Fatalf("invalid unsigned integer accepted: %q", value)
		}
	}
	if _, ok := procurementBudgetSigned("-0"); ok {
		t.Fatal("signed negative zero accepted")
	}
}
