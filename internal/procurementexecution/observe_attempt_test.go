package procurementexecution

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	observerAttemptTestTransition = "30000000-0000-0000-0000-000000000001"
	observerAttemptTestEvent      = "30000000-0000-0000-0000-000000000002"
	observerAttemptTestNode       = "30000000-0000-0000-0000-000000000003"
)

// Independent, frozen serialization of the contract's A1-A7 ABI. The compact
// notation is name:type, with ? marking SQL NULL and x/m denoting TEXT casts.
func TestObserveAttemptFrozenProjectionABI(t *testing.T) {
	cases := []struct {
		query observerQuery
		want  string
	}{
		{observerAttemptQuery, "id:t tenant_id:t request_id:t sequence:4 channel_id:t channel_version_id:t credential_id:t upstream_model:t delivery_state:t cost_version_id:t? cost_input_rate:8? cost_output_rate:8? cost_basis:t cost_status:t cost_amount:m? created_at:z route_decisions:j pb_created_xid:x? pb_dispatch_checked_at:z? pp_origin_root_id:t? pp_origin_root_step:8? pp_origin_xid:x? pp_quote_selected_at:z? pp_last_cost_mutation_id:t? pp_last_delivery_source_id:t? pp_dispatch_source_id:t? pp_dispatch_root_id:t? pp_dispatch_root_step:8? pp_dispatch_xid:x? pp_dispatch_account_id:t? pp_dispatch_journal_id:t?"},
		{observerAttemptOriginQuery, "attempt_id:t root_id:t step_no:8 step_kind:t created_xid:x tenant_id:t request_id:t sequence:4 channel_id:t channel_version_id:t credential_id:t upstream_model:t route_decisions:j cost_version_id:t? cost_input_rate:8? cost_output_rate:8? cost_basis:t quote_selected_at:z attempt_created_at:z pb_created_xid:x initial_delivery_state:t initial_cost_status:t initial_cost_amount:m? management_mode:t account_id:t? binding_id:t? procurement_transition_id:t? reserved_event_id:t? account_journal_id:t? admitted_at:z?"},
		{observerAttemptHeadQuery, "attempt_id:t ordinal:8 last_node_id:t? member_count:8? member_tail_id:t? updated_xid:x"},
		{observerAttemptNodeQuery, "id:t attempt_id:t ordinal:8 previous_node_id:t? root_id:t step_no:8 step_kind:t source_id:t created_xid:x previous_ordinal:8"},
		{observerAttemptReservationQuery, "attempt_id:t account_id:t binding_id:t input_bound:8 output_bound:8 upper_amount:m held_amount:m spent_amount:m state:t anomaly:b revision:8 created_at:z updated_at:z pb_created_xid:x pb_control:t? pb_admitted_at:z pp_created_xid:x pp_origin_journal_id:t pp_last_journal_id:t"},
		{observerAttemptTransitionQuery, "id:t root_id:t step_no:8 step_kind:t created_xid:x created_at:z tenant_id:t request_id:t attempt_id:t account_id:t binding_id:t cause_kind:t cause_source_id:t event_id:t journal_id:t event_type:t event_key:t evidence_ref:t held_delta:m spent_delta:m before_revision:8? before_state:t? before_held:m? before_spent:m? before_anomaly:b? after_revision:8 after_state:t after_held:m after_spent:m after_anomaly:b input_bound:8 output_bound:8 upper_amount:8 cost_version_id:t cost_input_rate:8 cost_output_rate:8 cost_basis:t d_attempt_birth:t? d_evidence_choice:t? d_approval_consumed:t? d_recovery:t?"},
		{observerAttemptEventQuery, "id:t event_key:t account_id:t attempt_id:t held_delta:m spent_delta:m type:t evidence_ref:t created_at:z pb_created_xid:x pp_root_id:t pp_root_step:8 pp_transition_id:t pp_journal_id:t pp_created_xid:x"},
	}
	oids := map[string]uint32{"t": pgtype.TextOID, "4": pgtype.Int4OID, "8": pgtype.Int8OID, "b": pgtype.BoolOID, "z": pgtype.TimestamptzOID, "j": pgtype.JSONBOID, "x": pgtype.TextOID, "m": pgtype.TextOID}
	for _, tc := range cases {
		t.Run(tc.query.table, func(t *testing.T) {
			parts := strings.Fields(tc.want)
			if len(parts) != len(tc.query.fields) {
				t.Fatalf("projection length: got %d want %d", len(tc.query.fields), len(parts))
			}
			for n, part := range parts {
				name, kind, _ := strings.Cut(part, ":")
				nullable := strings.HasSuffix(kind, "?")
				kind = strings.TrimSuffix(kind, "?")
				cast := ""
				if kind == "x" || kind == "m" {
					cast = "text"
				}
				want := observerField{name: name, oid: oids[kind], cast: cast, nullable: nullable}
				if tc.query.fields[n] != want {
					t.Errorf("column %d: got %#v want %#v", n, tc.query.fields[n], want)
				}
			}
			if strings.Contains(tc.query.sql(`s.".t`), "SELECT *") || strings.Contains(tc.query.sql(`s.".t`), "xmin") {
				t.Fatal("projection widened beyond ordinary frozen columns")
			}
		})
	}
}

type observerAttemptTestFixture struct {
	i                Invocation
	receipt          AttemptReceipt
	u, o, head, node observerRow
	p, tr, event, j  observerRow
	root             observerRow
	steps            []observerRow
}

func observerAttemptFixture(t *testing.T, managed bool) observerAttemptTestFixture {
	t.Helper()
	var f observerAttemptTestFixture
	var err error
	f.i, err = NewRecordAttempt("s", AttemptInput{TenantID: "tenant", RequestID: "request", Sequence: 1, ChannelID: "channel", ChannelVersionID: "version", CredentialID: "credential", UpstreamModel: "model", RouteDecisions: []byte(`[1]`)})
	if err != nil {
		t.Fatal(err)
	}
	root, source := runnerRoot, runnerAttempt
	f.receipt = AttemptReceipt{Outcome: "applied", RootID: &root, SourceID: &source, AttemptID: runnerAttempt, RequestID: "request", Sequence: 1, DeliveryState: "prepared", CostStatus: "unknown"}
	at := time.Date(2026, 11, 4, 12, 1, 2, 123456000, time.UTC)
	xid := "18446744073709551615"
	f.u = observerRow{"id": runnerAttempt, "tenant_id": "tenant", "request_id": "request", "sequence": int32(1), "channel_id": "channel", "channel_version_id": "version", "credential_id": "credential", "upstream_model": "model", "delivery_state": "prepared", "cost_version_id": "cost-version", "cost_input_rate": int64(1000000), "cost_output_rate": int64(2000000), "cost_basis": "contract", "cost_status": "unknown", "cost_amount": nil, "created_at": at, "route_decisions": []byte(`[{"large":18446744073709551615,"n":1}]`), "pb_created_xid": xid, "pb_dispatch_checked_at": nil, "pp_origin_root_id": runnerRoot, "pp_origin_root_step": int64(1), "pp_origin_xid": xid, "pp_quote_selected_at": at.Add(-time.Second)}
	f.o = observerRow{"attempt_id": runnerAttempt, "root_id": runnerRoot, "step_no": int64(1), "step_kind": "attempt_birth", "created_xid": xid, "tenant_id": "tenant", "request_id": "request", "sequence": int32(1), "channel_id": "channel", "channel_version_id": "version", "credential_id": "credential", "upstream_model": "model", "route_decisions": []byte(`[{"n":1.0,"large":18446744073709551615}]`), "cost_version_id": "cost-version", "cost_input_rate": int64(1000000), "cost_output_rate": int64(2000000), "cost_basis": "contract", "quote_selected_at": at.Add(-time.Second), "attempt_created_at": at.In(time.FixedZone("+08", 8*60*60)), "pb_created_xid": xid, "initial_delivery_state": "prepared", "initial_cost_status": "unknown", "initial_cost_amount": nil, "management_mode": "unmanaged"}
	f.head = observerRow{"attempt_id": runnerAttempt, "ordinal": int64(0), "last_node_id": nil, "member_count": nil, "member_tail_id": nil, "updated_xid": xid}
	f.root = observerRow{"id": runnerRoot, "entry_kind": "attempt_birth", "scope_kind": "request", "tenant_id": "tenant", "request_id": "request", "attempt_id": runnerAttempt, "account_target_id": nil, "created_xid": xid, "session_role_oid": int64(123), "step_count": int64(1), "semantic_result": "unmanaged"}
	f.steps = []observerRow{{"root_id": runnerRoot, "step_no": int64(1), "kind": "attempt_birth", "source_id": runnerAttempt, "created_xid": xid, "d_account_command": nil, "d_attempt_birth": runnerAttempt, "d_procurement_transition": nil}}
	if !managed {
		return f
	}
	f.o["management_mode"] = "managed"
	for k, v := range (observerRow{"account_id": runnerAccount, "binding_id": "binding", "procurement_transition_id": observerAttemptTestTransition, "reserved_event_id": observerAttemptTestEvent, "account_journal_id": runnerJournal, "admitted_at": at.Add(time.Second)}) {
		f.o[k] = v
	}
	f.head["ordinal"], f.head["last_node_id"], f.head["member_count"] = int64(1), observerAttemptTestNode, int64(0)
	f.root["step_count"], f.root["semantic_result"] = int64(2), "managed"
	f.steps = append(f.steps, observerRow{"root_id": runnerRoot, "step_no": int64(2), "kind": "procurement_transition", "source_id": observerAttemptTestTransition, "created_xid": xid, "d_account_command": nil, "d_attempt_birth": nil, "d_procurement_transition": observerAttemptTestTransition})
	f.node = observerRow{"id": observerAttemptTestNode, "attempt_id": runnerAttempt, "ordinal": int64(1), "previous_node_id": nil, "root_id": runnerRoot, "step_no": int64(2), "step_kind": "procurement_transition", "source_id": observerAttemptTestTransition, "created_xid": xid, "previous_ordinal": int64(0)}
	f.p = observerRow{"attempt_id": runnerAttempt, "account_id": runnerAccount, "binding_id": "binding", "input_bound": int64(12), "output_bound": int64(3), "upper_amount": "18", "held_amount": "18", "spent_amount": "0", "state": "prepared", "anomaly": false, "revision": int64(1), "created_at": at, "updated_at": at, "pb_created_xid": xid, "pb_control": nil, "pb_admitted_at": f.o["admitted_at"], "pp_created_xid": xid, "pp_origin_journal_id": runnerJournal, "pp_last_journal_id": runnerJournal}
	f.tr = observerRow{"id": observerAttemptTestTransition, "root_id": runnerRoot, "step_no": int64(2), "step_kind": "procurement_transition", "created_xid": xid, "created_at": at.Add(2 * time.Second), "tenant_id": "tenant", "request_id": "request", "attempt_id": runnerAttempt, "account_id": runnerAccount, "binding_id": "binding", "cause_kind": "attempt_birth", "cause_source_id": runnerAttempt, "event_id": observerAttemptTestEvent, "journal_id": runnerJournal, "event_type": "reserved", "event_key": "reserve:" + runnerAttempt, "evidence_ref": "", "held_delta": "18", "spent_delta": "0", "before_revision": nil, "before_state": nil, "before_held": nil, "before_spent": nil, "before_anomaly": nil, "after_revision": int64(1), "after_state": "prepared", "after_held": "18", "after_spent": "0", "after_anomaly": false, "input_bound": int64(12), "output_bound": int64(3), "upper_amount": int64(18), "cost_version_id": "cost-version", "cost_input_rate": int64(1000000), "cost_output_rate": int64(2000000), "cost_basis": "contract", "d_attempt_birth": runnerAttempt, "d_evidence_choice": nil, "d_approval_consumed": nil, "d_recovery": nil}
	f.event = observerRow{"id": observerAttemptTestEvent, "event_key": "reserve:" + runnerAttempt, "account_id": runnerAccount, "attempt_id": runnerAttempt, "held_delta": "18", "spent_delta": "0", "type": "reserved", "evidence_ref": "", "created_at": at, "pb_created_xid": xid, "pp_root_id": runnerRoot, "pp_root_step": int64(2), "pp_transition_id": observerAttemptTestTransition, "pp_journal_id": runnerJournal, "pp_created_xid": xid}
	f.j = observerRow{"id": runnerJournal, "account_id": runnerAccount, "ordinal": int64(7), "previous_id": "previous-journal", "previous_ordinal": int64(6), "root_id": runnerRoot, "step_no": int64(2), "step_kind": "procurement_transition", "created_xid": xid, "created_at": at, "kind": "reserve", "source_kind": "budget_event", "source_id": observerAttemptTestEvent, "attempt_id": runnerAttempt, "binding_id": "binding", "after_reservation_revision": int64(1), "after_reservation_state": "prepared", "after_reservation_held_amount": "18", "after_reservation_spent_amount": "0", "after_reservation_anomaly": false, "input_bound": int64(12), "output_bound": int64(3), "upper_amount": int64(18), "cost_version_id": "cost-version", "cost_input_rate": int64(1000000), "cost_output_rate": int64(2000000), "cost_basis": "contract", "gate_extra": int64(18), "gate_checked_at": f.tr["created_at"], "pb_decision_at": f.o["admitted_at"], "transition_id": observerAttemptTestTransition, "procurement_event_id": observerAttemptTestEvent}
	for _, prefix := range []string{"before_", "after_"} {
		for k, v := range (observerRow{"label": "original-account", "provider": "provider", "state": "active", "limit_amount": int64(100), "configuration_revision": int64(5), "spent_amount": "9", "blocking_count": int64(0)}) {
			f.j[prefix+k] = v
		}
	}
	f.j["before_held_amount"], f.j["after_held_amount"] = "7", "25"
	return f
}

func observerAttemptCopy(row observerRow) observerRow {
	copy := make(observerRow, len(row))
	for k, v := range row {
		copy[k] = v
	}
	return copy
}

func TestObserveAttemptExactReceiptAndReplayIdentity(t *testing.T) {
	f := observerAttemptFixture(t, false)
	if !observerAttemptReceiptMatches(f.u, f.i, f.receipt) {
		t.Fatal("exact original receipt rejected")
	}
	for _, name := range []string{"id", "tenant_id", "request_id", "channel_id", "channel_version_id", "credential_id", "upstream_model", "delivery_state", "cost_status"} {
		t.Run(name, func(t *testing.T) {
			u := observerAttemptCopy(f.u)
			u[name] = "another"
			if observerAttemptReceiptMatches(u, f.i, f.receipt) {
				t.Fatal("a different original identity/receipt substituted")
			}
		})
	}
	t.Run("sequence", func(t *testing.T) {
		u := observerAttemptCopy(f.u)
		u["sequence"] = int32(2)
		if observerAttemptReceiptMatches(u, f.i, f.receipt) {
			t.Fatal("another sequence substituted")
		}
	})
	t.Run("legacy text remains text", func(t *testing.T) {
		u := observerAttemptCopy(f.u)
		u["id"], u["cost_status"] = "legacy arbitrary TEXT", "old-status"
		r := f.receipt
		r.Outcome, r.RootID, r.SourceID, r.AttemptID, r.CostStatus = "replay", nil, nil, "legacy arbitrary TEXT", "old-status"
		if !observerAttemptReceiptMatches(u, f.i, r) {
			t.Fatal("legacy replay TEXT acquired a new UUID/enum requirement")
		}
	})
	t.Run("replay new route and quote not fifth key", func(t *testing.T) {
		i := f.i
		i.args = f.i.Arguments()
		i.args[7], i.args[8], i.args[9], i.args[10], i.args[11], i.args[12] = []byte(`["different route"]`), true, []byte("different quote"), int64(99), int64(101), []byte("invoice")
		if !observerAttemptReceiptMatches(f.u, i, f.receipt) {
			t.Fatal("new route/quote incorrectly became an idempotency key")
		}
	})
}

func TestObserveAttemptOriginAndManagementUnions(t *testing.T) {
	f := observerAttemptFixture(t, true)
	if !observerAttemptCurrentBirth(f.u) || !observerAttemptOriginMatches(f.u, f.o) {
		t.Fatal("matching tracked birth rejected, including instant/JSONB equality")
	}
	for _, name := range []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid", "pp_quote_selected_at", "pb_created_xid"} {
		t.Run("missing "+name, func(t *testing.T) {
			u := observerAttemptCopy(f.u)
			u[name] = nil
			if observerAttemptCurrentBirth(u) {
				t.Fatal("partial origin accepted as a current tracked birth")
			}
		})
	}
	for _, name := range []string{"pb_dispatch_checked_at", "pp_last_cost_mutation_id", "pp_last_delivery_source_id", "pp_dispatch_source_id", "pp_dispatch_root_id", "pp_dispatch_root_step", "pp_dispatch_xid", "pp_dispatch_account_id", "pp_dispatch_journal_id"} {
		t.Run("successor "+name, func(t *testing.T) {
			u := observerAttemptCopy(f.u)
			u[name] = "set"
			if observerAttemptCurrentBirth(u) {
				t.Fatal("successor silently accepted as supported birth")
			}
		})
	}
	for _, name := range []string{"tenant_id", "request_id", "sequence", "channel_id", "channel_version_id", "credential_id", "upstream_model", "cost_version_id", "cost_input_rate", "cost_output_rate", "cost_basis", "pb_created_xid", "attempt_id", "root_id", "step_no", "created_xid", "quote_selected_at", "attempt_created_at", "route_decisions", "step_kind", "initial_delivery_state", "initial_cost_status", "initial_cost_amount"} {
		t.Run("origin "+name, func(t *testing.T) {
			o := observerAttemptCopy(f.o)
			o[name] = "different"
			if observerAttemptOriginMatches(f.u, o) {
				t.Fatal("different origin field accepted")
			}
		})
	}
	for _, managed := range []bool{false, true} {
		f := observerAttemptFixture(t, managed)
		if got, ok := observerAttemptManagement(f.o); !ok || got != managed || !observerAttemptHeadMatches(f.head, f.o, managed) {
			t.Fatal("valid management/head union rejected")
		}
		for _, name := range []string{"account_id", "binding_id", "procurement_transition_id", "reserved_event_id", "account_journal_id", "admitted_at"} {
			o := observerAttemptCopy(f.o)
			if managed {
				o[name] = nil
			} else {
				o[name] = "present"
			}
			if _, ok := observerAttemptManagement(o); ok {
				t.Fatalf("partial management union accepted: managed=%v field=%s", managed, name)
			}
		}
		h := observerAttemptCopy(f.head)
		h["member_count"] = int64(1)
		if observerAttemptHeadMatches(h, f.o, managed) {
			t.Fatal("foreign member accepted")
		}
	}
}

func TestObserveAttemptExactUpperArithmetic(t *testing.T) {
	cases := []struct {
		name                     string
		in, out, inRate, outRate int64
		want                     int64
		ok                       bool
	}{
		{"zero rates", math.MaxInt64, math.MaxInt64, 0, 0, 0, true},
		{"fraction rounds once", 1, 1, 1, 1, 1, true},
		{"exact max int64 after multiplication", math.MaxInt64, 0, 1000000, 0, math.MaxInt64, true},
		{"one over after exact calculation", math.MaxInt64, 1, 1000000, 1000000, 0, false},
		{"huge product rejected", math.MaxInt64, math.MaxInt64, math.MaxInt64, math.MaxInt64, 0, false},
		{"negative input", -1, 0, 0, 0, 0, false},
		{"zero input", 0, 1, 0, 0, 0, false},
		{"negative output", 1, -1, 0, 0, 0, false},
		{"negative rate", 1, 0, -1, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := observerRow{"input_bound": tc.in, "output_bound": tc.out}
			o := observerRow{"cost_input_rate": tc.inRate, "cost_output_rate": tc.outRate}
			got, ok := observerAttemptUpper(p, o)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("got %d,%v want %d,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
	if _, ok := observerAttemptUpper(observerRow{"input_bound": int64(1), "output_bound": int64(0)}, observerRow{"cost_input_rate": nil, "cost_output_rate": int64(0)}); ok {
		t.Fatal("NULL rate silently became zero")
	}
}

func TestObserveAttemptManagedRelationChecks(t *testing.T) {
	f := observerAttemptFixture(t, true)
	cases := []struct {
		name   string
		row    observerRow
		check  func(observerRow) bool
		fields string
	}{
		{"node", f.node, func(row observerRow) bool { return observerAttemptNodeMatches(row, f.o, f.head) }, "id attempt_id ordinal previous_node_id root_id step_no step_kind source_id created_xid previous_ordinal"},
		{"reservation", f.p, func(row observerRow) bool { return observerAttemptReservationMatches(row, f.o, 18) }, "attempt_id account_id binding_id upper_amount held_amount spent_amount state anomaly revision pb_created_xid pb_admitted_at pp_created_xid pp_origin_journal_id pp_last_journal_id"},
		{"transition", f.tr, func(row observerRow) bool { return observerAttemptTransitionMatches(row, f.p, f.o, 18) }, "id root_id step_no step_kind created_xid tenant_id request_id attempt_id account_id binding_id cause_kind cause_source_id event_id journal_id event_type event_key evidence_ref held_delta spent_delta before_revision before_state before_held before_spent before_anomaly after_revision after_state after_held after_spent after_anomaly input_bound output_bound upper_amount cost_version_id cost_input_rate cost_output_rate cost_basis d_attempt_birth d_evidence_choice d_approval_consumed d_recovery"},
		{"event", f.event, func(row observerRow) bool { return observerAttemptEventMatches(row, f.o, 18) }, "id event_key account_id attempt_id held_delta spent_delta type evidence_ref pb_created_xid pp_root_id pp_root_step pp_transition_id pp_journal_id pp_created_xid"},
		{"journal", f.j, func(row observerRow) bool { return observerAttemptJournalMatches(row, f.p, f.tr, f.o, 18) }, "id account_id ordinal previous_id previous_ordinal root_id step_no step_kind created_xid kind source_kind source_id attempt_id binding_id before_reservation_revision before_reservation_state before_reservation_held_amount before_reservation_spent_amount before_reservation_anomaly after_reservation_revision after_reservation_state after_reservation_held_amount after_reservation_spent_amount after_reservation_anomaly input_bound output_bound upper_amount cost_version_id cost_input_rate cost_output_rate cost_basis gate_extra gate_checked_at pb_decision_at pause_event_id command_source_id transition_id delivery_source_id command_operation_id procurement_event_id dispatch_attempt_id before_label before_provider before_state before_limit_amount before_configuration_revision before_held_amount before_spent_amount before_blocking_count after_label after_provider after_state after_limit_amount after_configuration_revision after_held_amount after_spent_amount after_blocking_count"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.check(tc.row) {
				t.Fatal("matching relation rejected")
			}
			for _, name := range strings.Fields(tc.fields) {
				t.Run(name, func(t *testing.T) {
					row := observerAttemptCopy(tc.row)
					// Keep builtin types realistic: metadata/codec validation is
					// a separate boundary. This checks saved value relations.
					switch value := row[name].(type) {
					case int64:
						row[name] = value + 1
					case bool:
						row[name] = !value
					case time.Time:
						row[name] = value.Add(time.Microsecond)
					default:
						row[name] = "different"
					}
					if name == "previous_id" {
						// The predecessor's exact identity is authenticated by
						// V3/V2; the client requires a real non-NULL predecessor.
						row[name] = nil
					}
					if tc.check(row) {
						t.Fatal("different managed relation accepted")
					}
				})
			}
		})
	}
	// Birth marker is intentionally nullable even after a later real Commander
	// import. The mandatory validators authenticate the actual PB shape.
	f.p["pb_control"] = "existing-control"
	if !observerAttemptReservationMatches(f.p, f.o, 18) {
		t.Fatal("non-NULL PB birth marker rejected without reading the validator")
	}
	// NUMERIC(100,0) is compared exactly and must not wrap/truncate to int64.
	f.j["before_held_amount"] = strings.Repeat("9", 100)
	if observerAttemptJournalMatches(f.j, f.p, f.tr, f.o, 18) {
		t.Fatal("NUMERIC100 wrapped through int64 budget check")
	}
}

func TestObserveAttemptJSONBExactNumericComparison(t *testing.T) {
	cases := []struct {
		a, b  string
		equal bool
	}{
		{`[{"x":18446744073709551615,"n":1}]`, `[{"n":1.0,"x":18446744073709551615}]`, true},
		{`[18446744073709551615]`, `[18446744073709551614]`, false},
		{`[1e3]`, `[1000.000]`, true},
		{`[0.10000000000000000001]`, `[0.1]`, false},
		{`{"x":null}`, `{}`, false},
		{`[true]`, `[1]`, false},
		{`null`, `null`, true},
		{`null`, `null null`, false},
		{``, `null`, false},
	}
	for _, tc := range cases {
		if got := observerAttemptJSONEqual([]byte(tc.a), []byte(tc.b)); got != tc.equal {
			t.Errorf("JSONB exact comparison: %s versus %s got %v", tc.a, tc.b, got)
		}
	}
}

// Probe-only transport uses pgx's real builtin codecs. It records every fixed
// read and parameter. No database, writer, model callback or commercial
// acceptance is represented by these tests.
type observerAttemptTestRows struct {
	fields    []observerField
	data      []observerRow
	index     int
	err, late error
	closed    bool
}

func (r *observerAttemptTestRows) FieldDescriptions() []pgconn.FieldDescription {
	out := make([]pgconn.FieldDescription, len(r.fields))
	for n, f := range r.fields {
		out[n] = pgconn.FieldDescription{Name: f.name, DataTypeOID: f.oid}
	}
	return out
}
func (r *observerAttemptTestRows) Next() bool {
	if r.closed || r.err != nil || r.index >= len(r.data) {
		return false
	}
	r.index++
	return true
}
func (r *observerAttemptTestRows) Scan(dest ...any) error {
	if r.index < 1 || r.index > len(r.data) || len(dest) != len(r.fields) {
		return errors.New("invalid probe test scan")
	}
	m := pgtype.NewMap()
	for n, f := range r.fields {
		encoded, err := m.Encode(f.oid, pgtype.TextFormatCode, r.data[r.index-1][f.name], make([]byte, 0, 32))
		if err == nil {
			err = m.Scan(f.oid, pgtype.TextFormatCode, encoded, dest[n])
		}
		if err != nil {
			r.err = err
			return err
		}
	}
	return nil
}
func (r *observerAttemptTestRows) Err() error { return r.err }
func (r *observerAttemptTestRows) Close()     { r.closed = true; r.err = combine(r.err, r.late) }

type observerAttemptTestRead struct {
	q    observerQuery
	rows []observerRow
	key  string
}
type observerAttemptTestTx struct {
	reads       []observerAttemptTestRead
	validateErr map[string]error
	queryErr    map[string]error
	missing     map[string]bool
	queries     []string
	args        [][]any
}

func (tx *observerAttemptTestTx) exec(context.Context, string) error {
	return errors.New("probe must not exec")
}
func (tx *observerAttemptTestTx) commit(context.Context) error {
	return errors.New("probe must not commit")
}
func (tx *observerAttemptTestTx) rollback(context.Context) error {
	return errors.New("probe must not rollback")
}
func (tx *observerAttemptTestTx) query(_ context.Context, sql string, args ...any) (Rows, error) {
	tx.queries = append(tx.queries, sql)
	tx.args = append(tx.args, args)
	for _, fn := range []string{"pp_validate_attempt", "pp_validate_root"} {
		if strings.Contains(sql, `."`+fn+`"(`) {
			if err := tx.validateErr[fn]; err != nil {
				return nil, err
			}
			return &observerAttemptTestRows{fields: []observerField{{name: fn, oid: observerVoidOID}}, data: []observerRow{{}}}, nil
		}
	}
	for _, read := range tx.reads {
		if strings.Contains(sql, ` FROM "s"."`+read.q.table+`" `) {
			if sql != read.q.sql("s") || len(args) != 1 || args[0] != read.key {
				return nil, errors.New("query or exact key differs from frozen probe")
			}
			if err := tx.queryErr[read.q.table]; err != nil {
				return nil, err
			}
			rows := read.rows
			if tx.missing[read.q.table] {
				rows = nil
			}
			return &observerAttemptTestRows{fields: read.q.fields, data: rows}, nil
		}
	}
	return nil, errors.New("unexpected observer query")
}

func observerAttemptFixtureTx(f observerAttemptTestFixture, managed bool) *observerAttemptTestTx {
	reads := []observerAttemptTestRead{
		{observerAttemptQuery, []observerRow{f.u}, runnerAttempt},
		{observerAttemptOriginQuery, []observerRow{f.o}, runnerAttempt},
		{observerRootQuery, []observerRow{f.root}, runnerRoot},
		{observerStepsQuery, f.steps, runnerRoot},
		{observerAttemptHeadQuery, []observerRow{f.head}, runnerAttempt},
	}
	if managed {
		reads = append(reads,
			observerAttemptTestRead{observerAttemptNodeQuery, []observerRow{f.node}, observerAttemptTestNode},
			observerAttemptTestRead{observerAttemptReservationQuery, []observerRow{f.p}, runnerAttempt},
			observerAttemptTestRead{observerAttemptTransitionQuery, []observerRow{f.tr}, observerAttemptTestTransition},
			observerAttemptTestRead{observerAttemptEventQuery, []observerRow{f.event}, observerAttemptTestEvent},
			observerAttemptTestRead{observerJournalQuery, []observerRow{f.j}, runnerJournal},
		)
	}
	return &observerAttemptTestTx{reads: reads}
}

func TestObserveAttemptTrackedProofRequiresBothValidators(t *testing.T) {
	for _, managed := range []bool{false, true} {
		for _, replay := range []bool{false, true} {
			name := "unmanaged/applied"
			if managed {
				name = "managed/applied"
			}
			if replay {
				name = strings.ReplaceAll(name, "applied", "replay")
			}
			t.Run(name, func(t *testing.T) {
				f := observerAttemptFixture(t, managed)
				if replay {
					f.receipt.Outcome, f.receipt.RootID, f.receipt.SourceID = "replay", nil, nil
					// The newly supplied route and expected quote do not change
					// which original receipt the replay actually returned.
					rate := int64(999)
					f.i, _ = NewRecordAttempt("s", AttemptInput{TenantID: "tenant", RequestID: "request", Sequence: 1, ChannelID: "channel", ChannelVersionID: "version", CredentialID: "credential", UpstreamModel: "model", RouteDecisions: []byte(`["new-route"]`), CostQuote: &CostQuote{VersionID: "new-version", InputRate: &rate, OutputRate: &rate, Basis: "invoice"}})
				}
				tx := observerAttemptFixtureTx(f, managed)
				p := &observationProbe{ctx: context.Background(), tx: tx, schema: "s"}
				if err := p.attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt}); err != nil {
					t.Fatal("complete tracked proof rejected", err)
				}
				var want []string
				for _, read := range tx.reads {
					want = append(want, read.q.sql("s"))
				}
				want = append(want, `SELECT "s"."pp_validate_attempt"($1::pg_catalog.text)`, `SELECT "s"."pp_validate_root"($1::pg_catalog.text)`)
				if !reflect.DeepEqual(tx.queries, want) {
					t.Fatal("fixed proof order/closure changed", tx.queries)
				}
				if !reflect.DeepEqual(tx.args[len(tx.args)-2:], [][]any{{runnerAttempt}, {runnerRoot}}) {
					t.Fatal("validator was pointed at another attempt/root")
				}
				if replay && (f.receipt.RootID != nil || f.receipt.SourceID != nil) {
					t.Fatal("replay captured NULL source was rewritten")
				}
			})
		}
	}
	for _, managed := range []bool{false, true} {
		for _, fn := range []string{"pp_validate_attempt", "pp_validate_root"} {
			for _, code := range []string{"PP902", "PP906", "42501"} {
				t.Run(fn+"/"+code+map[bool]string{true: "/managed", false: "/unmanaged"}[managed], func(t *testing.T) {
					f := observerAttemptFixture(t, managed)
					tx := observerAttemptFixtureTx(f, managed)
					cause := &pgconn.PgError{Code: code}
					tx.validateErr = map[string]error{fn: cause}
					err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
					if err != cause {
						t.Fatal("reverse closure validator was skipped or cause rewritten")
					}
					want := len(tx.reads) + 1
					if fn == "pp_validate_root" {
						want++
					}
					if len(tx.queries) != want {
						t.Fatal("queries continued after a validator exception")
					}
				})
			}
		}
	}
}

func TestObserveAttemptTrackedMissingSourcesNeverBecomePresence(t *testing.T) {
	for _, table := range []string{"pp_operation_roots", "pp_root_steps", "pp_attempt_heads", "pp_attempt_nodes", "procurement_reservations", "pp_procurement_transitions", "procurement_budget_events", "pp_account_journal"} {
		t.Run(table, func(t *testing.T) {
			f := observerAttemptFixture(t, true)
			tx := observerAttemptFixtureTx(f, true)
			tx.missing = map[string]bool{table: true}
			err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
			if err == nil || observationFailureReason(err) != ReasonReceiptIncomplete {
				t.Fatal("missing source promoted to presence")
			}
			for _, query := range tx.queries {
				if strings.Contains(query, `"pp_validate_`) {
					t.Fatal("missing source was supplemented by a validator after local terminal failure")
				}
			}
		})
	}
	t.Run("real read error retained", func(t *testing.T) {
		f := observerAttemptFixture(t, true)
		tx := observerAttemptFixtureTx(f, true)
		cause := &pgconn.PgError{Code: "42501"}
		tx.queryErr = map[string]error{"procurement_reservations": cause}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err != cause || len(tx.queries) != 7 {
			t.Fatal("real read error changed or proof continued")
		}
	})
	t.Run("empty original tenant/request are text values", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		f.i, _ = NewRecordAttempt("s", AttemptInput{TenantID: "", RequestID: "", Sequence: 1, ChannelID: "channel", ChannelVersionID: "version", CredentialID: "credential", UpstreamModel: "model", RouteDecisions: []byte(`[]`)})
		f.receipt.RequestID = ""
		f.u["tenant_id"], f.u["request_id"], f.o["tenant_id"], f.o["request_id"], f.root["tenant_id"], f.root["request_id"] = "", "", "", "", "", ""
		tx := observerAttemptFixtureTx(f, false)
		if err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt}); err != nil {
			t.Fatal("empty original TEXT scope incorrectly became NULL", err)
		}
	})
}

func TestObserveAttemptLegacyClassificationIsRealValidator(t *testing.T) {
	for _, code := range []string{"PP902", "PP906", "PP901", "PP903", "57014"} {
		t.Run(code, func(t *testing.T) {
			f := observerAttemptFixture(t, false)
			f.receipt.Outcome, f.receipt.RootID, f.receipt.SourceID = "replay", nil, nil
			f.receipt.AttemptID = "legacy text"
			f.u["id"], f.u["pp_origin_root_id"], f.u["pp_origin_root_step"], f.u["pp_origin_xid"], f.u["pp_quote_selected_at"] = "legacy text", nil, nil, nil, nil
			cause := &pgconn.PgError{Code: code, Message: "must never infer legacy from this text"}
			tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, []observerRow{f.u}, "legacy text"}}, validateErr: map[string]error{"pp_validate_attempt": cause}}
			p := &observationProbe{ctx: context.Background(), tx: tx, schema: "s"}
			err := p.attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
			if err != cause || len(tx.queries) != 2 || !reflect.DeepEqual(tx.args, [][]any{{"legacy text"}, {"legacy text"}}) {
				t.Fatal("validator cause changed, another ID queried, or reads continued after server error")
			}
			want := ReasonObservationUnavailable
			if code == "PP902" {
				want = ReasonHistoryInvariant
			}
			if code == "PP906" {
				want = ReasonUnsupportedHistory
			}
			if code == "57014" {
				want = ReasonObservationTimeout
			}
			if got := observationFailureReason(err); got != want {
				t.Fatalf("got %s want %s", got, want)
			}
		})
	}
	t.Run("unexpected validator success cannot make legacy present", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		f.u["pp_origin_root_id"] = nil
		f.receipt.Outcome, f.receipt.RootID, f.receipt.SourceID = "replay", nil, nil
		tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, []observerRow{f.u}, runnerAttempt}}}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err == nil || observationFailureReason(err) != ReasonReceiptIncomplete || len(tx.queries) != 2 {
			t.Fatal("unproved legacy promoted to presence or supplemented by more reads")
		}
	})
	t.Run("original absent does not manufacture PP902", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, nil, runnerAttempt}}, validateErr: map[string]error{"pp_validate_attempt": &pgconn.PgError{Code: "PP902"}}}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err == nil || observationFailureReason(err) != ReasonReceiptAbsent || len(tx.queries) != 1 {
			t.Fatal("absent original incorrectly invokes validator")
		}
	})
	t.Run("receipt mismatch stops before source or validator", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		f.u["credential_id"] = "other"
		tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, []observerRow{f.u}, runnerAttempt}}}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err == nil || observationFailureReason(err) != ReasonReceiptMismatch || len(tx.queries) != 1 {
			t.Fatal("a valid source substituted for mismatching receipt")
		}
	})
	t.Run("missing tracked origin uses actual validator", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		cause := &pgconn.PgError{Code: "PP902"}
		tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, []observerRow{f.u}, runnerAttempt}, {observerAttemptOriginQuery, nil, runnerAttempt}}, validateErr: map[string]error{"pp_validate_attempt": cause}}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err != cause || len(tx.queries) != 3 {
			t.Fatal("missing tracked origin misclassified or reads continued")
		}
	})
	t.Run("matching successor keeps actual PP902", func(t *testing.T) {
		f := observerAttemptFixture(t, false)
		f.u["delivery_state"] = "sending"
		f.receipt.Outcome, f.receipt.RootID, f.receipt.SourceID, f.receipt.DeliveryState = "replay", nil, nil, "sending"
		cause := &pgconn.PgError{Code: "PP902"}
		tx := &observerAttemptTestTx{reads: []observerAttemptTestRead{{observerAttemptQuery, []observerRow{f.u}, runnerAttempt}}, validateErr: map[string]error{"pp_validate_attempt": cause}}
		err := (&observationProbe{ctx: context.Background(), tx: tx, schema: "s"}).attempt(&ObservationHandle{invocation: f.i, attempt: f.receipt})
		if err != cause || len(tx.queries) != 2 {
			t.Fatal("matching successor's actual PP902 rewritten to unsupported")
		}
	})
}
