package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestScalarCanonicalDomains(t *testing.T) {
	for _, s := range []string{"", "+1", "01", "-0", "-01", "1.0", "1e2", " 1", "١"} {
		if signedInteger(s, 64) {
			t.Fatalf("noncanonical signed number accepted: %q", s)
		}
		if _, ok := numeric(s, true); ok {
			t.Fatalf("noncanonical numeric accepted: %q", s)
		}
	}
	for _, s := range []string{"-9223372036854775808", "9223372036854775807", "0"} {
		if !signedInteger(s, 64) {
			t.Fatal("valid signed bound rejected")
		}
	}
	if signedInteger("9223372036854775808", 64) || signedInteger("32768", 16) || !signedInteger("-32768", 16) {
		t.Fatal("signed width mismatch")
	}
	if !unsignedInteger("18446744073709551615", 64, true) || unsignedInteger("18446744073709551616", 64, false) || unsignedInteger("-1", 64, false) || unsignedInteger("0", 64, true) || !unsignedInteger("0", 32, false) {
		t.Fatal("unsigned width/zero rule mismatch")
	}
	if _, ok := numeric(strings.Repeat("9", 100), false); !ok {
		t.Fatal("N100 bound rejected")
	}
	if _, ok := numeric("-"+strings.Repeat("9", 100), true); !ok {
		t.Fatal("negative N100 delta rejected")
	}
	if _, ok := numeric(strings.Repeat("9", 101), false); ok {
		t.Fatal("oversized N100 accepted")
	}
	if _, ok := numeric("-1", false); ok {
		t.Fatal("negative amount accepted")
	}
	if !validText("é中") || validText("x\x00") || validText("x\xff") {
		t.Fatal("text byte domain mismatch")
	}
	stamp := Time{Time: time.Date(2026, 11, 3, 0, 0, 0, 123000, time.UTC), Valid: true}
	if !finite(stamp) || finite(Time{}) || finite(Time{Time: stamp.Time, Valid: true, InfinityModifier: pgtype.Infinity}) || finite(Time{Time: stamp.Time.Add(time.Nanosecond), Valid: true}) {
		t.Fatal("timestamp envelope mismatch")
	}
	if !nullTime(Time{}) || nullTime(Time{Time: stamp.Time}) || nullTime(Time{InfinityModifier: pgtype.NegativeInfinity}) {
		t.Fatal("malformed NULL timestamp hidden")
	}
}

func TestSharedResourceBounds(t *testing.T) {
	ctx := context.Background()
	rows := Rows{OriginalRows: OriginalRows{Channels: make([]ChannelRow, 100000)}}
	got, err := budgetRows(ctx, rows)
	if err != nil || got.TotalRows != 100000 || got.OriginalRows.Channels != 100000 {
		t.Fatal("row boundary rejected")
	}
	rows.PrefixRows.RequestHeads = []RequestHeadRow{{}}
	if got, err := budgetRows(ctx, rows); !errors.Is(err, ErrResource) || got != (Scan{}) {
		t.Fatal("row budget reset at prefix boundary")
	}
	rows = Rows{OriginalRows: OriginalRows{Channels: make([]ChannelRow, 256)}}
	for i := range rows.OriginalRows.Channels {
		rows.OriginalRows.Channels[i].ID = strings.Repeat("x", 65536)
	}
	got, err = budgetRows(ctx, rows)
	if err != nil || got.LogicalBytes != 16<<20 {
		t.Fatal("exact shared bytes boundary rejected")
	}
	rows.PrefixRows.RequestHeads = []RequestHeadRow{{RequestID: "x"}}
	if got, err := budgetRows(ctx, rows); !errors.Is(err, ErrResource) || got != (Scan{}) {
		t.Fatal("byte budget reset at prefix boundary")
	}
	rows = Rows{OriginalRows: OriginalRows{Channels: []ChannelRow{{ID: strings.Repeat("中", 21846)}}}}
	if _, err := budgetRows(ctx, rows); !errors.Is(err, ErrResource) {
		t.Fatal("UTF8 field counted by characters")
	}
	rows = Rows{OriginalRows: OriginalRows{UpstreamAttempts: []UpstreamAttemptRow{{RouteDecisions: strings.Repeat("x", 272144)}}}}
	if _, err := budgetRows(ctx, rows); err != nil {
		t.Fatal("route transport field bound rejected")
	}
	rows.OriginalRows.UpstreamAttempts[0].RouteDecisions += "x"
	if _, err := budgetRows(ctx, rows); !errors.Is(err, ErrResource) {
		t.Fatal("route transport field bound exceeded")
	}
	rows = Rows{PrefixRows: PrefixRows{AttemptOrigins: []AttemptOriginRow{{RouteDecisions: strings.Repeat("x", 272144)}}}}
	if _, err := budgetRows(ctx, rows); err != nil {
		t.Fatal("source route transport field bound rejected")
	}
	rows.PrefixRows.AttemptOrigins[0].UpstreamModel = strings.Repeat("x", 65537)
	if _, err := budgetRows(ctx, rows); !errors.Is(err, ErrResource) {
		t.Fatal("route exception spread to another field")
	}
	got, err = budgetRows(ctx, Rows{PrefixRows: PrefixRows{EvidenceChoices: []EvidenceChoiceRow{{}}}})
	if err != nil || got.LogicalBytes != 3 {
		t.Fatalf("NULL bool must still cost one byte: %v %d", err, got.LogicalBytes)
	}
	got, err = budgetRows(ctx, Rows{PrefixRows: PrefixRows{AttemptDeliverySources: []AttemptDeliverySourceRow{{}}}})
	if err != nil || got.LogicalBytes != 32 {
		t.Fatal("NULL time must still cost32")
	}
}

func TestBudgetCancellationAndZeroResult(t *testing.T) {
	if got, err := budgetRows(nil, Rows{}); !errors.Is(err, ErrInput) || got != (Scan{}) {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := budgetRows(ctx, Rows{}); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(got, Scan{}) {
		t.Fatal("cancellation changed result")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if got, err := budgetRows(ctx, Rows{}); !errors.Is(err, ErrTimeout) || got != (Scan{}) {
		t.Fatal("absolute deadline not enforced")
	}
}
