package fundinglotscan

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

func TestSandboxProjectionStaticContract(t *testing.T) {
	bindings := sandboxBindings()
	target := reflect.TypeFor[fundinglotverify.SandboxSourceGraphRows]()
	if len(bindings) != 33 || target.NumField() != len(bindings) {
		t.Fatalf("binding count=%d, graph groups=%d", len(bindings), target.NumField())
	}
	expectedKeys := map[string]string{
		"payment_sandbox_environment": "singleton", "payment_runtime_attestations": "singleton", "payment_policy": "singleton",
		"payment_skus": "id", "payment_sku_versions": "id", "payment_sku_changes": "operation_id", "payment_orders": "id",
		"payment_cancel_intents": "order_id", "payment_command_outbox": "id", "payment_command_observations": "id", "payment_query_attempts": "query_id",
		"payment_inbox": "id", "payment_event_conflicts": "id", "payment_verified_queries": "id", "payment_query_conflicts": "id",
		"payment_verified_evidence": "id", "payment_evidence_processing": "evidence_id",
		"payment_provider_revisions":     "environment_id merchant_id order_id provider_order_revision#",
		"payment_transaction_identities": "id", "payment_order_capture_selections": "order_id", "payment_captures": "id", "payment_effects": "capture_id", "payment_review_facts": "id",
		"users": "id", "tenants": "id", "commerce_commands": "operation_id", "commerce_operation_receipts": "operation_id", "funding_receipts": "id",
		"ledger_accounts": "id", "journals": "id", "journal_lines": "id", "audit_events": "id", "projection_outbox": "event_id",
	}
	expectedBytes := map[string]bool{
		"payment_runtime_attestations.proof_canonical": true,
		"payment_command_outbox.canonical_parameters":  true,
		"payment_command_observations.canonical_reply": true,
		"payment_query_attempts.canonical_request":     true,
		"payment_inbox.canonical_body":                 true,
		"payment_verified_queries.canonical_body":      true,
	}
	seen := make(map[string]bool)
	for i, b := range bindings {
		if !b.valid || seen[b.table] || target.Field(i).Type.Elem() != b.target {
			t.Fatalf("binding %d does not match graph group %s", i, target.Field(i).Name)
		}
		seen[b.table] = true
		var keyNames []string
		for _, key := range b.keys {
			name := key.name
			if key.numeric {
				name += "#"
			}
			keyNames = append(keyNames, name)
		}
		if want, ok := expectedKeys[b.table]; !ok || strings.Join(keyNames, " ") != want {
			t.Fatalf("unexpected physical key for %s: %v", b.table, keyNames)
		}
		for fi, column := range b.columns {
			if want := sandboxProjectionColumn(b.table, b.target.Field(fi).Name); column.name != want {
				t.Errorf("%s.%s maps to %s, want %s", b.table, b.target.Field(fi).Name, column.name, want)
			}
			identity := b.table + "." + column.name
			if (column.kind == bytesKind) != expectedBytes[identity] {
				t.Errorf("raw bytea classification differs for %s", identity)
			}
			delete(expectedBytes, identity)
		}
	}
	if len(expectedBytes) != 0 {
		t.Fatalf("missing raw bytea bindings: %v", expectedBytes)
	}
}

// Derive the ordinary physical spelling independently of the binding tokens;
// the three shared aliases are the only exceptions to DTO field spelling.
func sandboxProjectionColumn(table, field string) string {
	if table == "projection_outbox" && field == "ID" {
		return "event_id"
	}
	if table == "projection_outbox" && field == "Payload" {
		return "sanitized_payload"
	}
	if table == "audit_events" && field == "Diff" {
		return "sanitized_diff"
	}
	if field == "SKUID" {
		return "sku_id"
	}
	field = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`).ReplaceAllString(field, `${1}_${2}`)
	field = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(field, `${1}_${2}`)
	return strings.ToLower(field)
}

func sandboxProjectionFixture(t *testing.T, withRows bool) *session {
	t.Helper()
	tables, index, err := registry(sandboxBindings())
	if err != nil {
		t.Fatal(err)
	}
	s := &session{ctx: context.Background(), tables: tables, index: index}
	for ti := range s.tables {
		table := &s.tables[ti]
		table.eof = true
		if !withRows {
			continue
		}
		row := make(rawRow, len(table.spec.columns))
		for ci, column := range table.spec.columns {
			value := cell{kind: column.kind, valid: true}
			switch column.kind {
			case textKind:
				value.text = "原:" + table.spec.name + "/" + column.name
				if column.name == "schema_version" {
					value.text = "7"
				}
			case boolKind:
				value.boolean = column.name != "before_enabled"
			case timeKind:
				value.instant = pgtype.Timestamptz{Time: time.Time{}, Valid: true}
			case bytesKind:
				value.data = []byte{0xff, 0, byte(ci), 0xfe}
			}
			row[ci] = value
		}
		table.rows = []rawRow{row}
	}
	return s
}

func sandboxProjectionCell(t *testing.T, s *session, table, column string) *cell {
	t.Helper()
	ti, ok := s.index[table]
	if !ok {
		t.Fatalf("missing table %s", table)
	}
	ci, ok := s.tables[ti].spec.index[column]
	if !ok || len(s.tables[ti].rows) == 0 {
		t.Fatalf("missing row or column %s.%s", table, column)
	}
	return &s.tables[ti].rows[0][ci]
}

func TestSandboxProjectionPreservesEveryField(t *testing.T) {
	s := sandboxProjectionFixture(t, true)
	// The projection preserves a numeric source string without floating-point
	// conversion, even when its eventual business shape is invalid.
	amount := strings.Repeat("9", 100)
	sandboxProjectionCell(t, s, "payment_verified_evidence", "amount").text = amount
	out, qualified, err := projectSandbox(s)
	if err != nil || !qualified || len(s.rejected) != 0 {
		t.Fatalf("project: qualified=%t err=%v rejected=%d", qualified, err, len(s.rejected))
	}
	graph := reflect.ValueOf(out)
	for gi, binding := range sandboxBindings() {
		group := graph.Field(gi)
		if group.Len() != 1 {
			t.Fatalf("%s rows=%d, want 1", binding.table, group.Len())
		}
		row := group.Index(0)
		for fi := 0; fi < row.NumField(); fi++ {
			field := row.Type().Field(fi)
			column := sandboxProjectionColumn(binding.table, field.Name)
			original := sandboxProjectionCell(t, s, binding.table, column)
			value := row.Field(fi)
			if value.Kind() == reflect.Pointer {
				if value.IsNil() {
					t.Fatalf("unexpected NULL in %s.%s", binding.table, field.Name)
				}
				value = value.Elem()
			}
			switch {
			case value.Type() == reflect.TypeFor[time.Time]():
				if !value.Interface().(time.Time).Equal(original.instant.Time) {
					t.Errorf("timestamp changed for %s.%s", binding.table, field.Name)
				}
			case value.Kind() == reflect.String:
				if value.String() != original.text {
					t.Errorf("text changed for %s.%s", binding.table, field.Name)
				}
			case value.Kind() == reflect.Bool:
				if value.Bool() != original.boolean {
					t.Errorf("bool changed for %s.%s", binding.table, field.Name)
				}
			case value.Kind() == reflect.Int32:
				if value.Int() != 7 {
					t.Errorf("schema version changed for %s.%s", binding.table, field.Name)
				}
			case value.Kind() == reflect.Slice:
				if !bytes.Equal(value.Bytes(), original.data) {
					t.Errorf("bytea changed for %s.%s", binding.table, field.Name)
				}
				original.data[0] ^= 0xff
				if bytes.Equal(value.Bytes(), original.data) {
					t.Errorf("bytea aliases source for %s.%s", binding.table, field.Name)
				}
			default:
				t.Fatalf("unhandled DTO type %s", value.Type())
			}
		}
	}
	if *out.Evidence[0].Amount != amount || !out.Orders[0].CreatedAt.IsZero() {
		t.Fatal("source number or finite year-one time was replaced")
	}
}

func TestSandboxProjectionNullableOriginals(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "null", true: "empty"}[empty], func(t *testing.T) {
			s := sandboxProjectionFixture(t, true)
			parameters := sandboxProjectionCell(t, s, "payment_command_outbox", "canonical_parameters")
			parameters.valid = empty
			parameters.data = nil
			if empty {
				parameters.data = []byte{}
			}
			for _, column := range []string{"external_command_id", "original_outbox_id", "parameter_digest", "current_query_id", "last_error_code", "lease_until", "last_submission_unknown_at"} {
				value := sandboxProjectionCell(t, s, "payment_command_outbox", column)
				value.valid = false
				value.instant = pgtype.Timestamptz{}
			}
			out, qualified, err := projectSandbox(s)
			if err != nil || !qualified {
				t.Fatalf("project: qualified=%t err=%v", qualified, err)
			}
			row := out.CommandOutbox[0]
			if empty {
				if row.CanonicalParameters == nil || *row.CanonicalParameters == nil || len(*row.CanonicalParameters) != 0 {
					t.Fatal("empty bytea collapsed into NULL")
				}
			} else if row.CanonicalParameters != nil {
				t.Fatal("NULL bytea acquired a value")
			}
			if row.ExternalCommandID != nil || row.OriginalOutboxID != nil || row.ParameterDigest != nil || row.CurrentQueryID != nil || row.LastErrorCode != nil || row.LeaseUntil != nil || row.LastSubmissionUnknownAt != nil {
				t.Fatal("nullable source field acquired a value")
			}
		})
	}
}

func TestSandboxProjectionNativeQualification(t *testing.T) {
	for _, native := range []struct {
		name  string
		value pgtype.Timestamptz
	}{
		{"null", pgtype.Timestamptz{}},
		{"positive_infinity", pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.Infinity}},
		{"negative_infinity", pgtype.Timestamptz{Valid: true, InfinityModifier: pgtype.NegativeInfinity}},
	} {
		t.Run(native.name, func(t *testing.T) {
			s := sandboxProjectionFixture(t, true)
			value := sandboxProjectionCell(t, s, "payment_sandbox_environment", "created_at")
			value.valid, value.instant = native.value.Valid, native.value
			out, qualified, err := projectSandbox(s)
			if err != nil || qualified || len(s.rejected) != 1 {
				t.Fatalf("project: qualified=%t err=%v rejected=%d", qualified, err, len(s.rejected))
			}
			if len(out.Environments) != 1 || len(out.Captures) != 1 || len(out.Outbox) != 1 {
				t.Fatal("native mismatch dropped a source row or stopped later projections")
			}
			if value.instant != native.value {
				t.Fatal("projection changed the original native envelope")
			}
		})
	}
}

func TestSandboxProjectionFailureReturnsZero(t *testing.T) {
	t.Run("unfinished_last_table", func(t *testing.T) {
		s := sandboxProjectionFixture(t, true)
		s.tables[s.index["projection_outbox"]].eof = false
		out, qualified, err := projectSandbox(s)
		if !errors.Is(err, ErrExecution) || qualified || !reflect.DeepEqual(out, fundinglotverify.SandboxSourceGraphRows{}) {
			t.Fatal("unfinished scan returned partial graph or qualification")
		}
	})
	t.Run("canceled_empty_graph", func(t *testing.T) {
		s := sandboxProjectionFixture(t, false)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s.ctx = ctx
		out, qualified, err := projectSandbox(s)
		if !errors.Is(err, ErrCanceled) || qualified || !reflect.DeepEqual(out, fundinglotverify.SandboxSourceGraphRows{}) {
			t.Fatal("canceled empty graph returned success or partial input")
		}
	})
}
