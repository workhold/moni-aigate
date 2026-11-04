package reporting

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testFilter() Filter {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return Filter{From: from, To: from.Add(7 * 24 * time.Hour)}
}
func text(value string) *string { return &value }

func TestFilterAndMicrosecondWindow(t *testing.T) {
	base := testFilter()
	for _, tc := range []struct {
		name   string
		change func(*Filter)
		valid  bool
	}{
		{"valid", func(*Filter) {}, true},
		{"exact 31 days", func(f *Filter) { f.To = f.From.Add(31 * 24 * time.Hour) }, true},
		{"one nanosecond over", func(f *Filter) { f.To = f.From.Add(31*24*time.Hour + time.Nanosecond) }, false},
		{"empty", func(f *Filter) { f.To = f.From }, false},
		{"reverse", func(f *Filter) { f.To = f.From.Add(-time.Second) }, false},
		{"missing", func(f *Filter) { f.From = time.Time{} }, false},
		{"tenant invalid", func(f *Filter) { f.TenantID = "not-an-id" }, false},
		{"model invalid", func(f *Filter) { f.ModelID = "not-an-id" }, false},
		{"scoped", func(f *Filter) {
			f.TenantID = "10000000-0000-0000-0000-000000000001"
			f.ModelID = "10000000-0000-0000-0000-000000000002"
			f.Protocol = "openai_chat"
			f.ExecutionKind = "synchronous"
		}, true},
		{"unknown protocol", func(f *Filter) { f.Protocol = "chat" }, false},
		{"unknown execution", func(f *Filter) { f.ExecutionKind = "async" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.change(&f)
			err := f.Validate()
			if (err == nil) != tc.valid || err != nil && !errors.Is(err, ErrInvalidFilter) {
				t.Fatal("unexpected filter validation", err)
			}
		})
	}
	from := base.From.Add(1234 * time.Nanosecond)
	to := base.From.Add(9876 * time.Nanosecond)
	for _, stored := range []time.Time{base.From, base.From.Add(time.Microsecond), base.From.Add(2 * time.Microsecond), base.From.Add(9 * time.Microsecond), base.From.Add(10 * time.Microsecond)} {
		want := !stored.Before(from) && stored.Before(to)
		got := !stored.Before(ceilMicro(from)) && stored.Before(ceilMicro(to))
		if got != want {
			t.Fatal("nanosecond bound changed the stored microsecond cohort")
		}
	}
	if !ceilMicro(base.From.In(time.FixedZone("+08", 8*60*60))).Equal(base.From) {
		t.Fatal("timezone changed the interval")
	}
}

func TestOperatingReportExactRequestAndAttemptAccounting(t *testing.T) {
	f := testFilter()
	at := f.From.Add(time.Hour)
	facts := []requestFact{
		{row: RequestRow{RequestID: "r1", TenantID: "t1", TenantName: "one", ModelID: "m1", ModelName: "model one", CreatedAt: at}, charged: text("100"), refunded: text("20")},
		{row: RequestRow{RequestID: "r2", TenantID: "t2", TenantName: "two", ModelID: "m1", ModelName: "model one", CreatedAt: at.Add(time.Hour)}, charged: text("50"), refunded: text("0")},
		{row: RequestRow{RequestID: "r3", TenantID: "t1", ModelID: "m2", ModelName: "model two", CreatedAt: at.Add(time.Hour)}},
	}
	attempts := []attemptFact{
		{id: "a1", request: "r1", channel: "c1", channelName: "channel one", status: "contract", amount: text("10")},
		{id: "a2", request: "r1", channel: "c2", channelName: "channel two", status: "invoice", amount: text("20")},
		{id: "a3", request: "r2", channel: "c1", channelName: "channel one", status: "unknown", amount: text("999")},
		{id: "a4", request: "r3", channel: "c3", channelName: "channel three", status: "contract", amount: text("0")},
	}
	report, err := aggregate(f, at, facts, attempts)
	if err != nil {
		t.Fatal(err)
	}
	want := Totals{RequestCount: 3, UnsettledRequests: 1, AttemptCount: 4, UnknownProviderCosts: 1, Charged: "150", Refunded: "20", NetRevenue: "130", KnownProviderCost: "30"}
	if !reflect.DeepEqual(report.Summary, want) {
		t.Fatalf("income duplicated across attempts or unknown cost counted: got %#v", report.Summary)
	}
	if len(report.Models) != 2 || report.Models[0].ModelID != "m1" || report.Models[0].Charged != "150" || report.Models[0].UnknownProviderCosts != 1 || report.Models[0].GrossMargin != nil {
		t.Fatal("model aggregation incorrect", report.Models)
	}
	if len(report.Channels) != 3 || report.Channels[0].RequestCount != 2 || report.Channels[0].AttemptCount != 2 || report.Channels[0].KnownProviderCost != "10" || report.Channels[0].UnknownProviderCosts != 1 {
		t.Fatal("channel costs lost fallback or unknown", report.Channels)
	}
	if len(report.Requests) != 3 || report.Requests[0].RequestID != "r3" || report.Requests[1].RequestID != "r2" || report.Requests[2].RequestID != "r1" {
		t.Fatal("request ordering not deterministic", report.Requests)
	}
	r1 := report.Requests[2]
	if r1.RequestCount != 1 || r1.Charged != "100" || r1.Refunded != "20" || r1.AttemptCount != 2 || r1.GrossMargin == nil || *r1.GrossMargin != "50" {
		t.Fatal("one request's sale or both costs counted incorrectly", r1)
	}
	// Monetary totals can become negative after a full consumption refund,
	// even though each persisted sale/refund/cost is nonnegative.
	facts[0].refunded = text("100")
	one, err := aggregate(f, at, facts[:1], attempts[:2])
	if err != nil || one.Summary.GrossMargin == nil || *one.Summary.GrossMargin != "-30" {
		t.Fatal("negative margin was clamped or rejected", err)
	}
}

func TestOperatingReportExactLargeAmountsAndUnknownUnions(t *testing.T) {
	f := testFilter()
	huge := strings.Repeat("9", 100)
	facts := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("9223372036854775807"), refunded: text("0")}}
	attempts := []attemptFact{{id: "a1", request: "r", channel: "c", status: "contract", amount: &huge}, {id: "a2", request: "r", channel: "c", status: "invoice", amount: &huge}}
	report, err := aggregate(f, f.From, facts, attempts)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := new(big.Int).SetString(huge, 10)
	n.Mul(n, big.NewInt(2))
	if report.Summary.KnownProviderCost != n.String() || report.Summary.GrossMargin == nil || report.Channels[0].RequestCount != 1 || report.Channels[0].AttemptCount != 2 {
		t.Fatal("NUMERIC100 summed through int64/float or channel request duplicated")
	}
	margin := new(big.Int).Sub(big.NewInt(9223372036854775807), n).String()
	if *report.Summary.GrossMargin != margin {
		t.Fatal("large margin lost exact digits")
	}
	for _, tc := range []struct {
		name, status string
		cost         *string
	}{
		{"null invoice", "invoice", nil}, {"null contract", "contract", nil}, {"unknown nonzero", "unknown", text("123")}, {"other state", "estimated", text("0")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := attempts[:1]
			a[0] = attemptFact{id: "a", request: "r", channel: "c", status: tc.status, amount: tc.cost}
			r, err := aggregate(f, f.From, facts, a)
			if err != nil || r.Summary.UnknownProviderCosts != 1 || r.Summary.KnownProviderCost != "0" || r.Summary.GrossMargin != nil {
				t.Fatal("unknown cost fabricated as final zero", err)
			}
		})
	}
	facts[0].charged, facts[0].refunded = nil, nil
	r, err := aggregate(f, f.From, facts, nil)
	if err != nil || r.Summary.UnsettledRequests != 1 || r.Summary.GrossMargin != nil {
		t.Fatal("unfinished request reported complete margin")
	}
}

func TestOperatingReportEmptyLimitAndCorruptFacts(t *testing.T) {
	f := testFilter()
	empty, err := aggregate(f, f.From, nil, nil)
	if err != nil || empty.Summary.GrossMargin == nil || *empty.Summary.GrossMargin != "0" || empty.Requests == nil || empty.Models == nil || empty.Channels == nil {
		t.Fatal("empty report is not a complete zero result")
	}
	encoded, _ := json.Marshal(empty)
	if strings.Contains(string(encoded), ":null") || !strings.Contains(string(encoded), `"amount_unit":"micro_cny"`) {
		t.Fatal("empty report JSON contract differs", string(encoded))
	}
	if r, err := aggregate(f, f.From, make([]requestFact, MaxRequests+1), nil); !errors.Is(err, ErrRangeTooLarge) || !reflect.DeepEqual(r, Report{}) {
		t.Fatal("5001 requests silently truncated")
	}
	for _, tc := range []struct {
		name     string
		facts    []requestFact
		attempts []attemptFact
	}{
		{"partial settlement", []requestFact{{row: RequestRow{RequestID: "r"}, charged: text("1")}}, nil},
		{"refund exceeds sale", []requestFact{{row: RequestRow{RequestID: "r"}, charged: text("1"), refunded: text("2")}}, nil},
		{"noncanonical amount", []requestFact{{row: RequestRow{RequestID: "r"}, charged: text("01"), refunded: text("0")}}, nil},
		{"negative amount", []requestFact{{row: RequestRow{RequestID: "r"}, charged: text("-1"), refunded: text("0")}}, nil},
		{"duplicate request", []requestFact{{row: RequestRow{RequestID: "r"}}, {row: RequestRow{RequestID: "r"}}}, nil},
		{"foreign attempt", nil, []attemptFact{{id: "a", request: "another"}}},
		{"duplicate attempt", []requestFact{{row: RequestRow{RequestID: "r"}}}, []attemptFact{{id: "a", request: "r"}, {id: "a", request: "r"}}},
		{"over two attempts", []requestFact{{row: RequestRow{RequestID: "r"}}}, []attemptFact{{id: "a", request: "r"}, {id: "b", request: "r"}, {id: "c", request: "r"}}},
		{"invalid known cost", []requestFact{{row: RequestRow{RequestID: "r"}}}, []attemptFact{{id: "a", request: "r", status: "contract", amount: text("1.5")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := aggregate(f, f.From, tc.facts, tc.attempts)
			if err == nil || !reflect.DeepEqual(r, Report{}) {
				t.Fatal("incomplete/invalid facts published partial report")
			}
		})
	}
	if r, err := Read(context.Background(), nil, f); err == nil || !reflect.DeepEqual(r, Report{}) {
		t.Fatal("nil pool must return an unavailable zero report")
	}
	if r, err := Read(nil, nil, f); err == nil || !reflect.DeepEqual(r, Report{}) {
		t.Fatal("nil context accepted")
	}
}

func TestCSVExactNumbersQuotedTextAndFormulaProtection(t *testing.T) {
	for _, raw := range []string{"=SUM(1,2)", "+cmd", "-1+1", "@SUM(A1)", " \t\r\n=evil", "\u2003+evil", "\x00@evil", "\ufeff=evil"} {
		if got := csvText(raw); got != "'"+raw {
			t.Fatalf("formula escaped incorrectly %q -> %q", raw, got)
		}
	}
	for _, raw := range []string{"normal", "123", "", "a=b", "\nhello"} {
		if csvText(raw) != raw {
			t.Fatal("ordinary text changed")
		}
	}
	at := time.Date(2026, 11, 4, 10, 0, 0, 123456000, time.FixedZone("+08", 8*60*60))
	r := Report{Requests: []RequestRow{{RequestID: "r", TenantID: "t", TenantName: " \t=SUM(1,2)", ModelID: "m", ModelName: "model,\"quoted\"\nline", CreatedAt: at, Status: "completed", BillingStatus: "settled", Totals: Totals{Charged: "9007199254740993", Refunded: "0", NetRevenue: "9007199254740993", AttemptCount: 2, KnownProviderCost: "9007199254740994", GrossMargin: text("-1")}},
		{RequestID: "pending", CreatedAt: at, Totals: Totals{Charged: "0", Refunded: "0", NetRevenue: "0", KnownProviderCost: "0", UnknownProviderCosts: 1}}}}
	var out bytes.Buffer
	if err := WriteCSV(&out, r); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || len(rows[0]) != 21 || rows[0][15] != "protocol" || rows[0][20] != "variant_key" || rows[1][2] != "' \t=SUM(1,2)" || rows[1][4] != "model,\"quoted\"\nline" || rows[1][5] != "2026-11-04T02:00:00.123456Z" || rows[1][8] != "9007199254740993" || rows[1][14] != "-1" || rows[2][14] != "" {
		t.Fatal("CSV precision/escaping/unknown contract differs", rows)
	}
	want := errors.New("writer unavailable")
	if err := WriteCSV(failingWriter{want}, r); !errors.Is(err, want) {
		t.Fatal("CSV writer failure hidden", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReadSQLHasBoundedCurrentFactsOnly(t *testing.T) {
	if !strings.Contains(requestsSQL, "LIMIT 5001") || !strings.Contains(requestsSQL, "r.created_at >= $1 AND r.created_at < $2") || !strings.Contains(requestsSQL, "request_meter_snapshots") || strings.Contains(requestsSQL, "m.profile") || !strings.Contains(attemptsSQL, "ANY($1::pg_catalog.text[])") {
		t.Fatal("cohort bound or exact interval removed")
	}
	for _, sql := range []string{requestsSQL, attemptsSQL} {
		for _, forbidden := range []string{"FOR UPDATE", "FOR SHARE", "INSERT ", "UPDATE ", "DELETE ", "report_totals", "platform_probe", "pp_", "funding_lot"} {
			if strings.Contains(sql, forbidden) {
				t.Fatalf("operating report now depends on %q", forbidden)
			}
		}
	}
}

var _ io.Writer = failingWriter{}
