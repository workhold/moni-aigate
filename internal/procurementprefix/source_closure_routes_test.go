package procurementprefix

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourceRouteChannel = "00000000-0000-4000-8000-000000000001"
const sourceRouteVersion = "00000000-0000-4000-8000-000000000002"
const sourceRouteCanonical = `[{"channel_id":"00000000-0000-4000-8000-000000000001","version_id":"00000000-0000-4000-8000-000000000002","priority":-9223372036854775808,"weight":18446744073709551615,"reason":"selected"}]`

func TestSourceStrictRouteIndependentValues(t *testing.T) {
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	// PG JSONB key order/spacing changes preserve the original five-field value.
	expanded := `[ { "weight": 18446744073709551615, "reason": "selected", "version_id": "00000000-0000-4000-8000-000000000002", "priority": -9223372036854775808, "channel_id": "00000000-0000-4000-8000-000000000001" } ]`
	for _, raw := range []string{sourceRouteCanonical, expanded} {
		canonical, valid, err := sourceParseRoute(w, raw, sourceRouteChannel, sourceRouteVersion)
		if err != nil || !valid || canonical != sourceRouteCanonical {
			t.Fatalf("valid=%v err=%v canonical=%s", valid, err, canonical)
		}
	}
	tests := []struct{ name, raw string }{
		{"invalid UTF8", "[\xff]"}, {"not array", "null"}, {"wrong shape", "{}"}, {"not objects", "[null]"}, {"unknown field", strings.Replace(sourceRouteCanonical, `"reason":"selected"`, `"reason":"selected","extra":1`, 1)},
		{"missing", strings.Replace(sourceRouteCanonical, `,"reason":"selected"`, "", 1)},
		{"duplicate", strings.Replace(sourceRouteCanonical, `"reason":"selected"`, `"reason":"selected","reason":"selected"`, 1)},
		{"zero weight", strings.Replace(sourceRouteCanonical, "18446744073709551615", "0", 1)},
		{"uint64 overflow", strings.Replace(sourceRouteCanonical, "18446744073709551615", "18446744073709551616", 1)},
		{"quoted number", strings.Replace(sourceRouteCanonical, "18446744073709551615", `"18446744073709551615"`, 1)},
		{"exponent", strings.Replace(sourceRouteCanonical, "18446744073709551615", "1e0", 1)},
		{"decimal", strings.Replace(sourceRouteCanonical, "18446744073709551615", "1.0", 1)},
		{"int64 underflow", strings.Replace(sourceRouteCanonical, "-9223372036854775808", "-9223372036854775809", 1)},
		{"unknown reason", strings.Replace(sourceRouteCanonical, `"selected"`, `"future_reason"`, 1)},
		{"missing selected", strings.Replace(sourceRouteCanonical, `"selected"`, `"lower_priority"`, 1)},
		{"duplicate channel", sourceRouteCanonical[:len(sourceRouteCanonical)-1] + "," + sourceRouteCanonical[1:]},
		{"trailing object", sourceRouteCanonical + "{}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, valid, err := sourceParseRoute(w, tc.raw, sourceRouteChannel, sourceRouteVersion)
			if err != nil || valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
		})
	}
	if _, valid, err := sourceParseRoute(w, sourceRouteCanonical, sourceRouteVersion, sourceRouteVersion); err != nil || valid {
		t.Fatal("selected channel mismatch accepted")
	}
	if canonical, valid, err := sourceParseRoute(w, "[]", "not-a-uuid", "not-a-uuid"); err != nil || !valid || canonical != "[]" {
		t.Fatal("empty original route list must preserve its original accepted branch")
	}
	// Original uuid.Parse accepts these spellings. No lowercase or UUID cast is
	// added to original channel/version TEXT identities by the new pure reader.
	noncanonical := strings.ReplaceAll(sourceRouteCanonical, "00000000-0000-4000-8000-000000000001", "00000000000040008000000000000001")
	if _, valid, err := sourceParseRoute(w, noncanonical, "00000000000040008000000000000001", sourceRouteVersion); err != nil || !valid {
		t.Fatal("original UUID spelling acceptance narrowed")
	}
}
func TestSourceRouteRowCountTransportAndCancellation(t *testing.T) {
	var raw strings.Builder
	raw.WriteByte('[')
	for i := 0; i < 1000; i++ {
		if i > 0 {
			raw.WriteByte(',')
		}
		reason := "lower_priority"
		if i == 0 {
			reason = "selected"
		}
		fmt.Fprintf(&raw, `{"channel_id":"00000000-0000-4000-8000-%012d","version_id":"%s","priority":9223372036854775807,"weight":18446744073709551615,"reason":"%s"}`, i+1, sourceRouteVersion, reason)
	}
	raw.WriteByte(']')
	w, _, err := newReplayWork(context.Background(), Rows{})
	if err != nil {
		t.Fatal(err)
	}
	canonical, valid, err := sourceParseRoute(w, raw.String(), sourceRouteChannel, sourceRouteVersion)
	if err != nil || !valid || len(canonical) > 262144 {
		t.Fatal("1000 fixed legal decisions rejected")
	}
	expanded := strings.Repeat(" ", 262145-len(raw.String())) + raw.String()
	if len(expanded) <= 262144 || len(expanded) > 272144 {
		t.Fatal("bad transport boundary fixture")
	}
	if got, valid, err := sourceParseRoute(w, expanded, sourceRouteChannel, sourceRouteVersion); err != nil || !valid || got != canonical {
		t.Fatal("transport spaces were confused with original Go business byte size")
	}
	more := raw.String()[:len(raw.String())-1] + `,{"channel_id":"00000000-0000-4000-8000-000000002001","version_id":"00000000-0000-4000-8000-000000000002","priority":0,"weight":1,"reason":"lower_priority"}]`
	if _, valid, err := sourceParseRoute(w, more, sourceRouteChannel, sourceRouteVersion); err != nil || valid {
		t.Fatal("1001 decisions accepted")
	}
	ctx := &replayCancelContext{Context: context.Background(), remaining: 12}
	canceling := &replayWork{b: budget{ctx: ctx}}
	if result, valid, err := sourceParseRoute(canceling, expanded, sourceRouteChannel, sourceRouteVersion); !errors.Is(err, ErrCanceled) || valid || result != "" {
		t.Fatal("mid-JSON cancellation did not return zero result")
	}
	expired := &replayWork{b: budget{ctx: context.Background(), deadline: time.Now().Add(-time.Second)}}
	if result, valid, err := sourceParseRoute(expired, sourceRouteCanonical, sourceRouteChannel, sourceRouteVersion); !errors.Is(err, ErrTimeout) || valid || result != "" {
		t.Fatal("absolute deadline not preserved")
	}
}
func TestSourceRouteIntegrationAndNaturalRowCounts(t *testing.T) {
	stamp := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	base := UpstreamAttemptRow{ID: "attempt", Sequence: "1", CreatedAt: stamp, RouteDecisions: "[]"}
	rows := Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{base}}}
	run := func(rows Rows) sourceClosureResult {
		w, _, err := newReplayWork(context.Background(), rows)
		if err != nil {
			t.Fatal(err)
		}
		got, err := checkSourceClosure(w, rows)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run(rows); got.Stats.InvalidScalarRows != 0 {
		t.Fatal("valid physical legacy attempt fixture rejected")
	}
	rows.OriginalRows.UpstreamAttempts[0].RouteDecisions = "{}"
	if got := run(rows); got.Stats.InvalidScalarRows != 1 {
		t.Fatal("route phase missing from source entry")
	}
	// The same textual ID in two physical types denotes two rows. Within each
	// row several invalid values still count once for InvalidScalarRows.
	s := sourceTestRows(t, Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{{ID: "same", RouteDecisions: "{}"}}}, PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{{AttemptID: "same", RouteDecisions: "{}"}}}})
	s.bad = make(map[string]keySet)
	if err := s.routeRows(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["scalar"]) != 2 {
		t.Fatal("different physical rows with the same ID were conflated")
	}
	s.mark("scalar", "upstream_attempts", tupleKey("same"))
	if len(s.bad["scalar"]) != 2 {
		t.Fatal("same-row second violation was counted twice")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w, _, err := newReplayWork(ctx, rows)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if result, err := checkSourceClosure(w, rows); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("source entry returned partial data on canceled strict JSON work")
	}
}
