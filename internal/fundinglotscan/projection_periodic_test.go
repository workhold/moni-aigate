package fundinglotscan

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func newPeriodicProjectionSession(t *testing.T) *session {
	t.Helper()
	tables, index, err := registry(periodicBindings())
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 18 {
		t.Fatalf("PB physical groups = %d, want 18", len(tables))
	}
	for i := range tables {
		tables[i].eof = true
		row := make(rawRow, len(tables[i].spec.columns))
		for j, col := range tables[i].spec.columns {
			row[j] = cell{kind: col.kind, valid: true, text: col.name + "/raw"}
			if col.kind == timeKind {
				row[j].instant = pgtype.Timestamptz{Time: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Valid: true}
			}
		}
		tables[i].rows = []rawRow{row}
	}
	return &session{ctx: context.Background(), tables: tables, index: index}
}

func periodicProjectionCell(t *testing.T, s *session, table, column string) *cell {
	t.Helper()
	ti, ok := s.index[table]
	if !ok {
		t.Fatalf("missing physical table %q", table)
	}
	ci, ok := s.tables[ti].spec.index[column]
	if !ok {
		t.Fatalf("missing physical column %q.%q", table, column)
	}
	return &s.tables[ti].rows[0][ci]
}

func TestProjectPeriodicPreservesNativeTimesAndSourceValues(t *testing.T) {
	s := newPeriodicProjectionSession(t)
	putText := func(table, column, value string) {
		c := periodicProjectionCell(t, s, table, column)
		c.text, c.valid = value, true
	}
	putTime := func(table, column string, value pgtype.Timestamptz) {
		c := periodicProjectionCell(t, s, table, column)
		c.instant, c.valid = value, value.Valid
	}
	zeroFinite := pgtype.Timestamptz{Valid: true}
	future := pgtype.Timestamptz{Time: time.Date(12000, 1, 2, 3, 4, 5, 6000, time.UTC), Valid: true}
	positiveInfinity := pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}
	negativeInfinity := pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.NegativeInfinity}
	putTime("periodic_budget_controls", "tracking_started_at", zeroFinite)
	putTime("periodic_budget_controls", "effective_from", future)
	putTime("periodic_budget_controls", "max_observed_window_start", pgtype.Timestamptz{})
	putTime("periodic_budget_windows", "start_at", negativeInfinity)
	putTime("periodic_budget_windows", "end_at", positiveInfinity)
	putTime("request_records", "pb_admitted_at", zeroFinite)
	putTime("upstream_attempts", "pb_dispatch_checked_at", positiveInfinity)
	putTime("procurement_reservations", "pb_admitted_at", pgtype.Timestamptz{})
	putTime("audit_events", "created_at", negativeInfinity)

	putText("request_records", "id", "")
	putText("request_records", "tenant_id", "租户/legacy")
	putText("request_records", "pb_created_xid", "18446744073709551615")
	periodicProjectionCell(t, s, "request_records", "pb_tenant_control").valid = false
	putText("request_records", "pb_key_control", "")
	putText("upstream_attempts", "delivery_state", "unknown")
	amount100 := strings.Repeat("9", 100)
	putText("procurement_reservations", "held_amount", amount100)
	periodicProjectionCell(t, s, "procurement_reservations", "pb_created_xid").valid = false
	putText("procurement_reservations", "pb_control", "")
	putText("procurement_budget_events", "held_delta", "-"+amount100)
	putText("procurement_budget_events", "pb_created_xid", "18446744073709551615")
	putText("periodic_budget_configuration_versions", "revision", "10")
	putText("periodic_budget_operations", "safe_input", `{"tenant":"租户"}`)
	putText("periodic_budget_operations", "result_snapshot", `null`)
	putText("audit_events", "sanitized_diff", `{"pb":"源行"}`)

	out, qualified, err := projectPeriodic(s)
	if err != nil || !qualified || len(s.rejected) != 0 {
		t.Fatalf("project native facts: qualified=%v rejected=%d err=%v", qualified, len(s.rejected), err)
	}
	groups := reflect.ValueOf(out)
	for i := range groups.NumField() {
		if groups.Field(i).Len() != 1 {
			t.Fatalf("%s rows = %d, want 1", groups.Type().Field(i).Name, groups.Field(i).Len())
		}
	}
	if out.Controls[0].TrackingStartedAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true}) ||
		out.Controls[0].EffectiveFrom != (fundinglotverify.PeriodicBudgetGraphTime{Value: future.Time, Valid: true}) ||
		out.Controls[0].MaxObservedWindowStart != (fundinglotverify.PeriodicBudgetGraphTime{}) ||
		out.Windows[0].StartAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true, Infinity: -1}) ||
		out.Windows[0].EndAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true, Infinity: 1}) ||
		out.Requests[0].AdmittedAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true}) ||
		out.Attempts[0].DispatchCheckedAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true, Infinity: 1}) ||
		out.Procurement[0].AdmittedAt != (fundinglotverify.PeriodicBudgetGraphTime{}) ||
		out.Audits[0].CreatedAt != (fundinglotverify.PeriodicBudgetGraphTime{Valid: true, Infinity: -1}) {
		t.Fatal("PB native timestamp state was changed")
	}
	request := out.Requests[0]
	if request.ID != "" || request.TenantID != "租户/legacy" || request.CreatedXID == nil || *request.CreatedXID != "18446744073709551615" || request.TenantControl != nil || request.KeyControl == nil || *request.KeyControl != "" {
		t.Fatal("PB source identity, xid8 or nullable stamp was changed")
	}
	if out.Attempts[0].DeliveryState != "unknown" || out.Procurement[0].HeldAmount != amount100 || out.Procurement[0].Anomaly || out.Procurement[0].CreatedXID != nil || out.Procurement[0].ControlID == nil || *out.Procurement[0].ControlID != "" || out.ProcurementEvents[0].HeldDelta != "-"+amount100 || out.ProcurementEvents[0].CreatedXID == nil || *out.ProcurementEvents[0].CreatedXID != "18446744073709551615" || out.Versions[0].Revision != "10" {
		t.Fatal("PB source enum, NUMERIC100, bool, xid8 or revision was changed")
	}
	if string(out.Operations[0].SafeInput) != `{"tenant":"租户"}` || string(out.Operations[0].ResultSnapshot) != `null` || string(out.Audits[0].SanitizedDiff) != `{"pb":"源行"}` {
		t.Fatal("JSONB text did not reach PB byte fields unchanged")
	}
	out.Operations[0].SafeInput[0] = '!'
	out.Audits[0].SanitizedDiff[0] = '!'
	second, qualified, err := projectPeriodic(s)
	if err != nil || !qualified || string(second.Operations[0].SafeInput) != `{"tenant":"租户"}` || string(second.Audits[0].SanitizedDiff) != `{"pb":"源行"}` {
		t.Fatal("PB byte projection aliases a prior view or source")
	}
}

func TestPeriodicBindingsTypedKeysAndNativeColumns(t *testing.T) {
	bindings := periodicBindings()
	for _, b := range bindings {
		if !b.valid {
			t.Fatalf("invalid PB binding %s", b.table)
		}
		for _, c := range b.columns {
			if c.kind == bytesKind {
				t.Fatalf("PB JSONB column %s.%s declared raw bytea", b.table, c.name)
			}
		}
	}
	versions := bindings[5]
	if versions.table != "periodic_budget_configuration_versions" || !reflect.DeepEqual(versions.keys, []key{{name: "control_id"}, {name: "revision", numeric: true}}) {
		t.Fatal("PB configuration key must be the physical TEXT/BIGINT tuple")
	}
	s := newPeriodicProjectionSession(t)
	for _, field := range [][2]string{
		{"periodic_budget_controls", "enabled"},
		{"periodic_budget_configuration_versions", "enabled"},
		{"procurement_reservations", "anomaly"},
	} {
		if periodicProjectionCell(t, s, field[0], field[1]).kind != boolKind {
			t.Fatalf("PB bool %s.%s lost its native type", field[0], field[1])
		}
	}
}

func TestProjectPeriodicRequiresEOFAndReturnsZeroOnError(t *testing.T) {
	for _, table := range []string{"periodic_budget_controls", "audit_events"} {
		t.Run(table, func(t *testing.T) {
			s := newPeriodicProjectionSession(t)
			s.tables[s.index[table]].eof = false
			out, qualified, err := projectPeriodic(s)
			if !errors.Is(err, ErrExecution) || qualified || !reflect.DeepEqual(out, fundinglotverify.PeriodicBudgetGraphRows{}) {
				t.Fatalf("missing EOF returned nonzero/qualified facts: qualified=%v err=%v", qualified, err)
			}
		})
	}
	s := newPeriodicProjectionSession(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ctx = ctx
	out, qualified, err := projectPeriodic(s)
	if !errors.Is(err, ErrCanceled) || qualified || !reflect.DeepEqual(out, fundinglotverify.PeriodicBudgetGraphRows{}) {
		t.Fatalf("cancellation returned nonzero/qualified facts: qualified=%v err=%v", qualified, err)
	}
}
