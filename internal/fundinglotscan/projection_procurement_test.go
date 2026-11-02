package fundinglotscan

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func TestProcurementBindingsMatchFixedDTOContracts(t *testing.T) {
	want := []struct {
		table, keys, fields string
	}{
		{"channels", "id", "id"},
		{"channel_credentials", "id", "id channel_id"},
		{"channel_versions", "id", "id channel_id credential_id"},
		{"channel_version_credentials", "channel_version_id credential_id", "channel_version_id channel_id credential_id position weight max_inflight"},
		{"tenants", "id", "id"},
		{"request_records", "id", "id tenant_id input_upper_bound output_limit policy_version latest_finalization_id request_deadline"},
		{"cost_versions", "id", "id channel_id upstream_model input_rate output_rate basis effective_at created_at"},
		{"procurement_accounts", "id", "id label provider state limit_amount held_amount spent_amount revision created_at"},
		{"credential_procurement_bindings", "id", "id credential_id channel_id account_id operation_id bound_at"},
		{"upstream_attempts", "id", "id tenant_id request_id sequence channel_id channel_version_id credential_id upstream_model delivery_state cost_version_id cost_input_rate cost_output_rate cost_basis cost_status cost_amount created_at"},
		{"procurement_reservations", "attempt_id", "attempt_id account_id binding_id input_bound output_bound upper_amount held_amount spent_amount state revision anomaly created_at updated_at"},
		{"procurement_budget_events", "id", "id event_key account_id attempt_id held_delta spent_delta type evidence_ref created_at"},
		{"users", "id", "id"},
		{"provider_cost_corrections", "id", "id tenant_id attempt_id operation_id amount basis evidence_ref created_at"},
		{"procurement_cost_proposals", "id", "id tenant_id attempt_id amount basis evidence_ref reason requester expected_reservation_revision expected_evidence_count state approver approval_operation_id created_at approved_at"},
		{"procurement_commands", "operation_id", "operation_id actor kind target_id input_digest status reason before_state after_state created_at"},
		{"procurement_balance_observations", "id", "id account_id operation_id actor status amount currency source semantic observed_at provider_as_of evidence_ref created_at"},
		{"audit_events", "id", "id actor action target sanitized_diff request_id tenant_scope created_at"},
		{"projection_outbox", "event_id", "event_id tenant_id operation_id kind sanitized_payload schema_version created_at"},
		{"procurement_evidence_identities", "attempt_id evidence_digest", "attempt_id evidence_digest event_id created_at"},
		{"metering_events", "id", "id tenant_id request_id source source_event_id normalized_usage attempt_id schema_version happened_at received_at"},
		{"metering_event_conflicts", "id", "id event_id normalized_usage created_at"},
		{"finalization_records", "id", "id tenant_id request_id event_id input_digest protocol_outcome transport_end_reason delivery_stage finish_reason policy_version content_written created_at"},
	}
	bindings := procurementBindings()
	groups := reflect.TypeFor[fundinglotverify.ProcurementEvidenceGraphRows]()
	if len(bindings) != 23 || len(bindings) != groups.NumField() || len(bindings) != len(want) {
		t.Fatal("procurement projection must declare exactly all 23 raw groups")
	}
	for i, b := range bindings {
		if !b.valid || b.table != want[i].table || b.target != groups.Field(i).Type.Elem() {
			t.Fatalf("raw group %s has the wrong fixed binding", groups.Field(i).Name)
		}
		var keys, fields []string
		for _, k := range b.keys {
			if k.numeric {
				t.Fatalf("procurement key %s.%s must use physical TEXT ordering", b.table, k.name)
			}
			keys = append(keys, k.name)
		}
		for _, c := range b.columns {
			fields = append(fields, c.name)
			if c.kind == bytesKind {
				t.Fatalf("procurement JSONB %s.%s must project as text", b.table, c.name)
			}
		}
		if strings.Join(keys, " ") != want[i].keys || strings.Join(fields, " ") != want[i].fields {
			t.Fatalf("raw group %s changed its physical key or explicit columns", groups.Field(i).Name)
		}
	}
}

func procurementProjectionTestSession(t *testing.T) *session {
	t.Helper()
	tables, index, err := registry(procurementBindings())
	if err != nil {
		t.Fatal(err)
	}
	for i := range tables {
		tables[i].eof = true
	}
	return &session{ctx: context.Background(), tables: tables, index: index}
}

func procurementProjectionTestText(value string) cell {
	return cell{kind: textKind, valid: true, text: value}
}

func procurementProjectionTestTime(value time.Time) cell {
	return cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Time: value, Valid: true}}
}

func procurementProjectionTestRow(t *testing.T, s *session, name string, values map[string]cell) {
	t.Helper()
	ti, exists := s.index[name]
	if !exists {
		t.Fatalf("unknown fixed table %s", name)
	}
	table := &s.tables[ti]
	row := make(rawRow, len(table.spec.columns))
	for ci, c := range table.spec.columns {
		switch c.kind {
		case textKind:
			row[ci] = procurementProjectionTestText(name + "/" + c.name)
			switch c.name {
			case "position", "weight", "max_inflight", "sequence", "schema_version":
				row[ci].text = "7"
			}
		case boolKind:
			row[ci] = cell{kind: boolKind, valid: true, boolean: true}
		case timeKind:
			row[ci] = procurementProjectionTestTime(time.Date(2026, 11, 2, 5, 6, 7, 123456000, time.UTC))
		default:
			t.Fatalf("unexpected procurement scalar kind for %s.%s", name, c.name)
		}
	}
	for name, c := range values {
		ci, exists := table.spec.index[name]
		if !exists {
			t.Fatalf("unknown fixed column %s.%s", table.spec.name, name)
		}
		row[ci] = c
	}
	table.rows = append(table.rows, row)
}

func TestProjectProcurementPreservesRawFieldsAndNullableValues(t *testing.T) {
	s := procurementProjectionTestSession(t)
	for _, b := range procurementBindings() {
		procurementProjectionTestRow(t, s, b.table, nil)
	}
	nullText := cell{kind: textKind}
	wide := "1" + strings.Repeat("0", 99)
	usage := `{"status": "confirmed_final", "input_tokens": 9007199254740993, "output_tokens": 0, "total_tokens": 9007199254740993, "cached_tokens": 0, "reasoning_tokens": 0}`
	procurementProjectionTestRow(t, s, "request_records", map[string]cell{"latest_finalization_id": nullText})
	procurementProjectionTestRow(t, s, "request_records", map[string]cell{"latest_finalization_id": procurementProjectionTestText("")})
	procurementProjectionTestRow(t, s, "cost_versions", map[string]cell{"input_rate": nullText, "output_rate": procurementProjectionTestText("9223372036854775807")})
	procurementProjectionTestRow(t, s, "upstream_attempts", map[string]cell{"cost_version_id": nullText, "cost_input_rate": nullText, "cost_output_rate": procurementProjectionTestText("0"), "cost_amount": procurementProjectionTestText(wide)})
	procurementProjectionTestRow(t, s, "procurement_accounts", map[string]cell{"held_amount": procurementProjectionTestText(wide), "spent_amount": procurementProjectionTestText("9007199254740993")})
	procurementProjectionTestRow(t, s, "procurement_budget_events", map[string]cell{"held_delta": procurementProjectionTestText("-9223372036854775807"), "spent_delta": procurementProjectionTestText(wide)})
	procurementProjectionTestRow(t, s, "procurement_balance_observations", map[string]cell{"amount": nullText, "provider_as_of": {kind: timeKind}})
	procurementProjectionTestRow(t, s, "audit_events", map[string]cell{"tenant_scope": nullText})
	procurementProjectionTestRow(t, s, "metering_events", map[string]cell{"attempt_id": nullText, "normalized_usage": procurementProjectionTestText(usage)})
	out, qualified, err := projectProcurement(s)
	if err != nil || !qualified || len(s.rejected) != 0 {
		t.Fatalf("representable raw rows failed qualification: qualified=%v err=%v", qualified, err)
	}
	groups := reflect.ValueOf(out)
	for i := 0; i < groups.NumField(); i++ {
		if groups.Field(i).Len() == 0 {
			t.Fatalf("raw group %s was omitted", groups.Type().Field(i).Name)
		}
	}
	if out.Requests[1].LatestFinalizationID != nil || out.Requests[2].LatestFinalizationID == nil || *out.Requests[2].LatestFinalizationID != "" {
		t.Fatal("latest finalization SQL NULL and empty TEXT were merged")
	}
	if out.CostVersions[1].InputRate != nil || out.CostVersions[1].OutputRate == nil || *out.CostVersions[1].OutputRate != "9223372036854775807" {
		t.Fatal("nullable quote rates lost their exact native text values")
	}
	a := out.Attempts[1]
	if a.CostVersionID != nil || a.CostInputRate != nil || a.CostOutputRate == nil || *a.CostOutputRate != "0" || a.CostAmount == nil || *a.CostAmount != wide {
		t.Fatal("attempt cost snapshot lost NULL, zero or wide NUMERIC values")
	}
	if out.Accounts[1].HeldAmount != wide || out.Accounts[1].SpentAmount != "9007199254740993" || out.BudgetEvents[1].HeldDelta != "-9223372036854775807" || out.BudgetEvents[1].SpentDelta != wide {
		t.Fatal("procurement balances or signed events were narrowed")
	}
	if out.Observations[1].Amount != nil || out.Observations[1].ProviderAsOf != nil || out.Audits[1].TenantScope != nil || out.Metering[1].AttemptID != nil || out.Metering[1].UsageJSON != usage {
		t.Fatal("nullable provider observations, audit tenant or metering raw JSON changed")
	}
	if out.Accounts[0].Provider != "procurement_accounts/provider" || out.Reservations[0].Revision != "procurement_reservations/revision" || !out.Reservations[0].Anomaly {
		t.Fatal("provider configuration or reservation revision/anomaly mapping changed")
	}
	if out.Audits[0].Diff != "audit_events/sanitized_diff" || out.Audits[0].RequestID != "audit_events/request_id" || out.Audits[0].TenantScope == nil || *out.Audits[0].TenantScope != "audit_events/tenant_scope" {
		t.Fatal("audit fields were mapped in old SQL order instead of DTO order")
	}
	if out.Outbox[0].ID != "projection_outbox/event_id" || out.Outbox[0].Payload != "projection_outbox/sanitized_payload" || out.Outbox[0].SchemaVersion != 7 {
		t.Fatal("physical event_id/payload/schema did not map to their outbox DTO fields")
	}
	if out.Identities[0].Digest != "procurement_evidence_identities/evidence_digest" || out.Metering[0].Source != "metering_events/source" || out.Metering[0].UsageJSON != "metering_events/normalized_usage" || out.Metering[0].SchemaVersion != 7 {
		t.Fatal("identity or meter fixed raw fields were misplaced")
	}
	if out.Finalizations[0].FinishReason != "finalization_records/finish_reason" || out.Finalizations[0].PolicyVersion != "finalization_records/policy_version" || !out.Finalizations[0].ContentWritten {
		t.Fatal("finalization text and native boolean fields were misplaced")
	}
}

func TestProjectProcurementNativeTimeQualificationAcrossAllFields(t *testing.T) {
	fields := 0
	for bi, b := range procurementBindings() {
		for fi, c := range b.columns {
			if c.kind != timeKind {
				continue
			}
			fields++
			optional := b.table == "procurement_cost_proposals" && c.name == "approved_at" || b.table == "procurement_balance_observations" && c.name == "provider_as_of"
			cases := []struct {
				name      string
				value     cell
				qualified bool
			}{
				{"positive_infinity", cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}}, false},
				{"negative_infinity", cell{kind: timeKind, valid: true, instant: pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.NegativeInfinity}}, false},
				{"sql_null", cell{kind: timeKind}, optional},
				{"finite_year_one", procurementProjectionTestTime(time.Time{}), true},
				{"finite_outside_business_range", procurementProjectionTestTime(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)), true},
			}
			for _, tc := range cases {
				t.Run(b.table+"/"+c.name+"/"+tc.name, func(t *testing.T) {
					s := procurementProjectionTestSession(t)
					procurementProjectionTestRow(t, s, b.table, map[string]cell{c.name: tc.value})
					if b.table != "finalization_records" {
						procurementProjectionTestRow(t, s, "finalization_records", nil)
					}
					out, qualified, err := projectProcurement(s)
					if err != nil || qualified != tc.qualified {
						t.Fatalf("native qualification=%v want %v err=%v", qualified, tc.qualified, err)
					}
					wantRejected := 0
					if !tc.qualified {
						wantRejected = 1
					}
					if len(s.rejected) != wantRejected || len(out.Finalizations) != 1 {
						t.Fatal("native rejection lost deduplication or stopped later raw projection")
					}
					if tc.qualified {
						field := reflect.ValueOf(out).Field(bi).Index(0).Field(fi)
						if !tc.value.instant.Valid {
							if !field.IsNil() {
								t.Fatal("nullable native time became a non-NULL pointer")
							}
						} else {
							if field.Kind() == reflect.Pointer {
								field = field.Elem()
							}
							if !reflect.DeepEqual(field.Interface(), tc.value.instant.Time) {
								t.Fatal("finite native time changed during DTO projection")
							}
						}
					}
					table := &s.tables[s.index[b.table]]
					if !reflect.DeepEqual(table.rows[0][table.spec.index[c.name]], tc.value) {
						t.Fatal("projection overwrote the private native source value")
					}
				})
			}
		}
	}
	if fields != 23 {
		t.Fatalf("native timestamp coverage changed: got %d want 23", fields)
	}
}

func TestProjectProcurementErrorsReturnNoPartialRows(t *testing.T) {
	t.Run("missing_final_eof", func(t *testing.T) {
		s := procurementProjectionTestSession(t)
		procurementProjectionTestRow(t, s, "channels", nil)
		s.tables[s.index["finalization_records"]].eof = false
		out, qualified, err := projectProcurement(s)
		if err != ErrExecution || qualified || !reflect.DeepEqual(out, fundinglotverify.ProcurementEvidenceGraphRows{}) {
			t.Fatal("incomplete physical table yielded partial procurement input")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		s := procurementProjectionTestSession(t)
		procurementProjectionTestRow(t, s, "channels", nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.ctx = ctx
		out, qualified, err := projectProcurement(s)
		if err != ErrCanceled || qualified || !reflect.DeepEqual(out, fundinglotverify.ProcurementEvidenceGraphRows{}) {
			t.Fatal("cancellation yielded partial procurement input")
		}
	})
}
