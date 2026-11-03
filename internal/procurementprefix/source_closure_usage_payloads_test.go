package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Hand-written vectors spell the six persisted fields independently of the
// billing writer, the request replay parser, and the implementation under test.
const sourceUsagePayloadJSON = `{"status":"confirmed_final","input_tokens":5,"output_tokens":3,"total_tokens":8,"cached_tokens":2,"reasoning_tokens":1}`

func sourceUsageWork(t *testing.T) *replayWork {
	t.Helper()
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSourceUsagePayloadStatusAndExactIntegerVectors(t *testing.T) {
	w := sourceUsageWork(t)
	for _, raw := range []string{
		sourceUsagePayloadJSON,
		`{ "reasoning_tokens":1, "cached_tokens":2, "total_tokens":8, "output_tokens":3, "input_tokens":5, "st\u0061tus":"confirmed_f\u0069nal" }`,
		`{"status":"confirmed_final","input_tokens":0,"output_tokens":0,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`,
		`{"status":"confirmed_final","input_tokens":9223372036854775807,"output_tokens":0,"total_tokens":9223372036854775807,"cached_tokens":9223372036854775807,"reasoning_tokens":0}`,
		`{"status":"confirmed_final","input_tokens":9223372036854775806,"output_tokens":1,"total_tokens":9223372036854775807,"cached_tokens":0,"reasoning_tokens":1}`,
	} {
		if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || !valid {
			t.Fatalf("legal original usage rejected: %v %v %s", valid, err, raw)
		}
	}
	for _, status := range []string{"provisional", "estimated", "missing", "conflicting"} {
		raw := strings.Replace(sourceUsagePayloadJSON, "confirmed_final", status, 1)
		raw = strings.Replace(raw, `"total_tokens":8`, `"total_tokens":99`, 1)
		if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || !valid {
			t.Fatalf("legacy %s non-final total was narrowed: %v %v", status, valid, err)
		}
		if valid, err := sourceParseUsagePayload(w, raw, true); err != nil || valid {
			t.Fatalf("non-final %s accepted as reconciliation proposal", status)
		}
	}
	// Two individually valid int64 values may have a sum above MaxInt64. A
	// provisional report allows this; confirmed_final cannot supply that total.
	overflowSum := `{"status":"provisional","input_tokens":9223372036854775807,"output_tokens":9223372036854775807,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`
	if valid, err := sourceParseUsagePayload(w, overflowSum, false); err != nil || !valid {
		t.Fatal("non-final sum incorrectly constrained")
	}
	if valid, err := sourceParseUsagePayload(w, strings.Replace(overflowSum, "provisional", "confirmed_final", 1), false); err != nil || valid {
		t.Fatal("confirmed usage sum overflow accepted")
	}
}

func TestSourceUsagePayloadClosedShapeAndSingleFieldMutations(t *testing.T) {
	w := sourceUsageWork(t)
	fields := []string{`"status":"confirmed_final"`, `"input_tokens":5`, `"output_tokens":3`, `"total_tokens":8`, `"cached_tokens":2`, `"reasoning_tokens":1`}
	for _, field := range fields {
		t.Run("missing "+field, func(t *testing.T) {
			raw := strings.Replace(sourceUsagePayloadJSON, field+",", "", 1)
			if raw == sourceUsagePayloadJSON {
				raw = strings.Replace(raw, ","+field, "", 1)
			}
			if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || valid {
				t.Fatal("missing member was defaulted")
			}
		})
		t.Run("duplicate "+field, func(t *testing.T) {
			raw := strings.Replace(sourceUsagePayloadJSON, field, field+","+field, 1)
			if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || valid {
				t.Fatal("same-valued duplicate accepted")
			}
		})
	}
	for _, field := range fields[1:] {
		name := strings.SplitN(field, ":", 2)[0]
		for _, invalid := range []string{"-1", "-0", "+1", "01", "1.0", "1e0", "9223372036854775808", `"1"`, "null", "true", "[]", "{}"} {
			t.Run(name+"="+invalid, func(t *testing.T) {
				raw := strings.Replace(sourceUsagePayloadJSON, field, name+":"+invalid, 1)
				if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || valid {
					t.Fatalf("invalid integer representation accepted: %s", raw)
				}
			})
		}
	}
	for name, raw := range map[string]string{
		"SQL-looking JSON null":    "null",
		"array":                    "[]",
		"empty object":             "{}",
		"unknown member":           strings.Replace(sourceUsagePayloadJSON, "{", `{"extra":1,`, 1),
		"escaped duplicate":        strings.Replace(sourceUsagePayloadJSON, `"status":`, `"st\u0061tus":"confirmed_final","status":`, 1),
		"case-sensitive key":       strings.Replace(sourceUsagePayloadJSON, `"status":`, `"Status":`, 1),
		"missing status":           strings.Replace(sourceUsagePayloadJSON, `"confirmed_final"`, `""`, 1),
		"unknown status":           strings.Replace(sourceUsagePayloadJSON, `"confirmed_final"`, `"future"`, 1),
		"null status":              strings.Replace(sourceUsagePayloadJSON, `"confirmed_final"`, `null`, 1),
		"numeric status":           strings.Replace(sourceUsagePayloadJSON, `"confirmed_final"`, `1`, 1),
		"cached exceeds input":     strings.Replace(sourceUsagePayloadJSON, `"cached_tokens":2`, `"cached_tokens":6`, 1),
		"reasoning exceeds output": strings.Replace(sourceUsagePayloadJSON, `"reasoning_tokens":1`, `"reasoning_tokens":4`, 1),
		"incorrect final total":    strings.Replace(sourceUsagePayloadJSON, `"total_tokens":8`, `"total_tokens":7`, 1),
		"trailing value":           sourceUsagePayloadJSON + "{}",
		"invalid UTF8":             strings.Replace(sourceUsagePayloadJSON, "confirmed_final", "confirmed_\xfffinal", 1),
		"raw NUL":                  sourceUsagePayloadJSON + "\x00",
		"escaped NUL key":          strings.Replace(sourceUsagePayloadJSON, `"status"`, `"sta\u0000tus"`, 1),
		"unpaired high surrogate":  strings.Replace(sourceUsagePayloadJSON, `"status"`, `"st\ud800atus"`, 1),
		"unpaired low surrogate":   strings.Replace(sourceUsagePayloadJSON, `"confirmed_final"`, `"confirmed_\udc00final"`, 1),
		"bad surrogate pair":       strings.Replace(sourceUsagePayloadJSON, `"status"`, `"sta\ud800\u0061tus"`, 1),
		"invalid unicode escape":   strings.Replace(sourceUsagePayloadJSON, `"status"`, `"sta\u00xxtus"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if valid, err := sourceParseUsagePayload(w, raw, false); err != nil || valid {
				t.Fatalf("malformed Usage accepted: %v %v", valid, err)
			}
		})
	}
}

func TestSourceUsagePayloadPhysicalRowsAndNullableProposal(t *testing.T) {
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	proposal := sourceUsagePayloadJSON
	rows := Rows{OriginalRows: OriginalRows{
		MeteringEvents:         []MeteringEventRow{{ID: "same", Source: "adapter", SourceEventID: "event", SchemaVersion: "1", NormalizedUsage: sourceUsagePayloadJSON, HappenedAt: stamp, ReceivedAt: stamp}},
		MeteringEventConflicts: []MeteringEventConflictRow{{ID: "same", EventID: "event", NormalizedUsage: sourceUsagePayloadJSON, CreatedAt: stamp}},
		ReconciliationCases:    []ReconciliationCaseRow{{ID: "same", State: "proposed", ProposedUsage: &proposal, CreatedAt: stamp}, {ID: "absent", State: "open", CreatedAt: stamp}},
	}}
	s := sourceTestRows(t, rows)
	if err := s.usagePayloadRows(); err != nil || len(s.bad["scalar"]) != 0 {
		t.Fatal("legal physical Usage rows rejected", err)
	}
	if rows.OriginalRows.ReconciliationCases[1].ProposedUsage != nil || proposal != sourceUsagePayloadJSON {
		t.Fatal("nullable input was rewritten")
	}
	// Per-table natural keys prevent three distinct rows sharing one textual ID
	// from cancelling each other. Two errors within one row still count once.
	rows.OriginalRows.MeteringEvents[0].NormalizedUsage = strings.Replace(sourceUsagePayloadJSON, `"cached_tokens":2`, `"cached_tokens":9`, 1)
	rows.OriginalRows.MeteringEventConflicts[0].NormalizedUsage = `{"status":"missing","extra":null}`
	proposal = "null"
	s = sourceTestRows(t, rows)
	s.bad = make(map[string]keySet)
	if err := s.usagePayloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("Usage physical row identities were conflated", err)
	}
	s.mark("scalar", "metering_events", tupleKey("same"))
	if err := s.usagePayloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("same-row errors counted twice", err)
	}
	if proposal != "null" || rows.OriginalRows.ReconciliationCases[1].ProposedUsage != nil {
		t.Fatal("JSON literal null was conflated with SQL NULL")
	}
	for _, table := range []string{"metering_events", "metering_event_conflicts", "reconciliation_cases"} {
		if _, found := s.bad["scalar"][tupleKey(table, tupleKey("same"))]; !found {
			t.Fatalf("missing physical natural key for %s", table)
		}
	}
}

func TestSourceUsagePayloadEntryKeepsIncompleteAndLegacyBoundary(t *testing.T) {
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	rows := Rows{OriginalRows: OriginalRows{MeteringEvents: []MeteringEventRow{{ID: "legacy-meter", TenantID: "tenant", RequestID: "request", Source: "adapter", SourceEventID: "event", SchemaVersion: "1", NormalizedUsage: sourceUsagePayloadJSON, HappenedAt: stamp, ReceivedAt: stamp}}}}
	for _, status := range []string{"confirmed_final", "provisional", "estimated", "missing", "conflicting"} {
		rows.OriginalRows.MeteringEvents[0].NormalizedUsage = strings.Replace(sourceUsagePayloadJSON, "confirmed_final", status, 1)
		w, _, err := newReplayWork(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		got, err := checkSourceClosure(w, rows)
		if err != nil || got.Stats.InvalidScalarRows != 0 || got.Complete || len(got.Qualified) != 0 {
			t.Fatalf("valid legacy Usage changed qualification boundary: %+v %v", got, err)
		}
	}
	rows.OriginalRows.MeteringEvents[0].NormalizedUsage = strings.Replace(sourceUsagePayloadJSON, `,"cached_tokens":2`, "", 1)
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	got, err := checkSourceClosure(w, rows)
	if err != nil || got.Stats.InvalidScalarRows != 1 || got.Complete || len(got.Qualified) != 0 {
		t.Fatalf("Usage leaf absent from entry or prematurely qualified: %+v %v", got, err)
	}
}

func TestSourceUsagePayloadBudgetCancellationAndZeroResults(t *testing.T) {
	w := sourceUsageWork(t)
	exact := strings.Repeat(" ", int(fieldLimit)-len(sourceUsagePayloadJSON)) + sourceUsagePayloadJSON
	if valid, err := sourceParseUsagePayload(w, exact, false); err != nil || !valid {
		t.Fatal("exact 65536-byte Usage transport rejected", err)
	}
	if valid, err := sourceParseUsagePayload(w, " "+exact, false); !errors.Is(err, ErrResource) || valid {
		t.Fatal("oversized Usage returned data result", err)
	}
	ctx := &replayCancelContext{Context: context.Background(), remaining: 12}
	if valid, err := sourceParseUsagePayload(&replayWork{b: budget{ctx: ctx}}, exact, false); !errors.Is(err, ErrCanceled) || valid {
		t.Fatal("mid-JSON cancellation returned a data verdict", err)
	}
	expired := &replayWork{b: budget{ctx: context.Background(), deadline: time.Now().Add(-time.Second)}}
	if valid, err := sourceParseUsagePayload(expired, sourceUsagePayloadJSON, false); !errors.Is(err, ErrTimeout) || valid {
		t.Fatal("Usage parser renewed the absolute deadline", err)
	}
	if valid, err := sourceParseUsagePayload(nil, sourceUsagePayloadJSON, false); !errors.Is(err, ErrInput) || valid {
		t.Fatal("nil work was accepted")
	}
	rows := Rows{OriginalRows: OriginalRows{MeteringEvents: []MeteringEventRow{{ID: "m", NormalizedUsage: exact}}}}
	ctx = &replayCancelContext{Context: context.Background(), remaining: 15}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	w.b.ctx = ctx
	if result, err := checkSourceClosure(w, rows); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("source entry exposed a partial report on cancellation", err)
	}
}
