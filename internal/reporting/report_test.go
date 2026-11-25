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

	"github.com/moni-ai/moni-aigate/internal/economics"
)

func testFilter() Filter {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	return Filter{From: from, To: from.Add(7 * 24 * time.Hour)}
}
func text(value string) *string { return &value }
func testTextAllocation(id string, amount int64) settlementLotFact {
	return settlementLotFact{key: id + "|1", request: sourceKey(textFactSource, id), lotID: "lot-" + id, currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: amount, fiat: amount, issued: amount}
}
func testMediaAllocation(tenant, id string, amount int64) settlementLotFact {
	return settlementLotFact{key: tenant + "|" + id + "|1", request: sourceKey(mediaFactSource, id), lotID: "media-lot-" + id, currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: amount, fiat: amount, issued: amount}
}

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
		}, true},
		{"media model", func(f *Filter) { f.ModelID = "mock-tts" }, true},
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
	r1Allocation := testTextAllocation("r1", 100)
	r1Allocation.refunded = 20
	report, err := aggregate(f, at, facts, attempts, nil, []settlementLotFact{r1Allocation, testTextAllocation("r2", 50)}, []cashRefundFact{{currency: "CNY", amount: "7"}})
	if err != nil {
		t.Fatal(err)
	}
	want := Totals{RequestCount: 3, UnsettledRequests: 1, AttemptCount: 4, UnknownProviderCosts: 1, Charged: "150", Refunded: "20", NetRevenue: "130", KnownProviderCost: "30", PaymentBasis: map[string]string{"CNY": "130"}, CashRefunded: map[string]string{"CNY": "7"}, ProviderCost: map[string]string{"CNY": "30"}, FxPolicyVersions: []string{}}
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
	if r1.RequestCount != 1 || r1.Charged != "100" || r1.Refunded != "20" || r1.AttemptCount != 2 || r1.GrossMargin == nil || *r1.GrossMargin != "50" || r1.ProvisionalGrossMargin == nil || *r1.ProvisionalGrossMargin != "50" || r1.BillConfirmedGrossMargin != nil || r1.PaymentBasis["CNY"] != "80" || r1.ProviderCost["CNY"] != "30" || r1.CashRefunded["CNY"] != "" {
		t.Fatal("net refund basis or mixed-basis costs counted incorrectly", r1)
	}
	// A full consumption refund removes the original edge's payment basis. The
	// remaining supplier cost produces a negative contribution margin, not a clamp.
	facts[0].refunded = text("100")
	r1Allocation.refunded = 100
	one, err := aggregate(f, at, facts[:1], attempts[:2], nil, []settlementLotFact{r1Allocation}, nil)
	if err != nil || one.Summary.GrossMargin == nil || *one.Summary.GrossMargin != "-30" || one.Summary.PaymentBasis["CNY"] != "0" {
		t.Fatal("full refund did not recompute net basis or negative margin", err)
	}
}

func TestOperatingReportPaymentBasisAndUnknowns(t *testing.T) {
	f := testFilter()
	facts := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("90"), refunded: text("0")}}
	attempts := []attemptFact{{id: "a", request: "r", channel: "c", status: "contract", amount: text("10")}}
	paid := testTextAllocation("r", 50)
	paid.fiat = 90
	paid.issued = 100
	unknown := testTextAllocation("r", 30)
	unknown.key = "unknown|1"
	unknown.lotID = "unknown-lot"
	unknown.basisKind = "unknown"
	grant := testTextAllocation("r", 40)
	grant.key = "grant|1"
	grant.lotID = "grant-lot"
	grant.basisKind = "grant"
	grant.currency, grant.buyRateID, grant.fiat = "", "", 0
	refunds := []cashRefundFact{{currency: "CNY", amount: "7"}, {currency: "USD", amount: "11"}}
	report, err := aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{paid, unknown, grant}, refunds)
	if err != nil || report.Summary.PaymentBasis["CNY"] != "45" || report.Summary.CashRefunded["CNY"] != "7" || report.Summary.CashRefunded["USD"] != "11" || report.Summary.GrossMargin != nil {
		t.Fatalf("unknown source or cash-refund columns are wrong: %+v err=%v", report.Summary, err)
	}
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{paid}, refunds)
	if err != nil || report.Summary.GrossMargin != nil {
		t.Fatalf("partial allocation edges produced a margin: %+v err=%v", report.Summary, err)
	}
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{paid, grant}, refunds)
	if err != nil || report.Summary.GrossMargin == nil || *report.Summary.GrossMargin != "35" || report.Summary.PaymentBasis["CNY"] != "45" {
		t.Fatalf("allocated CNY margin mismatch: %+v err=%v", report.Summary, err)
	}
	opening := grant
	opening.basisKind = "opening"
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{paid, opening}, refunds)
	if err != nil || report.Summary.GrossMargin == nil || *report.Summary.GrossMargin != "35" || report.Summary.PaymentBasis["CNY"] != "45" {
		t.Fatalf("opening basis did not contribute exact zero: %+v err=%v", report.Summary, err)
	}
	missingRate := paid
	missingRate.buyRateID = ""
	missingFiat := paid
	missingFiat.fiat = 0
	for _, allocation := range []settlementLotFact{missingRate, missingFiat} {
		report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{allocation, grant}, refunds)
		if err != nil || report.Summary.GrossMargin != nil {
			t.Fatalf("missing rate or fiat produced a margin: %+v err=%v", report.Summary, err)
		}
	}
	usd := testTextAllocation("r", 1)
	usd.key = "usd|1"
	usd.lotID = "usd-lot"
	usd.currency = "USD"
	grant39 := grant
	grant39.charged = 39
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{paid, usd, grant39}, refunds)
	if err != nil || report.Summary.GrossMargin != nil {
		t.Fatalf("multi-currency margin was fabricated: %+v err=%v", report.Summary, err)
	}
	report, err = aggregate(f, f.From, facts, attempts, nil, nil, refunds)
	if err != nil || report.Summary.GrossMargin != nil {
		t.Fatalf("missing allocation edge produced a margin: %+v err=%v", report.Summary, err)
	}
}

func TestOperatingReportSeparatesTextAndMediaIdentities(t *testing.T) {
	f := testFilter()
	facts := []requestFact{
		{row: RequestRow{RequestID: "same", ModelID: "text", CreatedAt: f.From}, charged: text("10"), refunded: text("0")},
		{row: RequestRow{RequestID: "same", ModelID: "media", CreatedAt: f.From}, charged: text("20"), refunded: text("0"), source: mediaFactSource},
	}
	attempts := []attemptFact{
		{id: "attempt", request: "same", channel: "text-channel", status: "contract", amount: text("3")},
		{id: "attempt", request: "same", channel: "media:tts", channelName: "media/tts", status: "completed", source: mediaFactSource},
	}
	report, err := aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{testTextAllocation("same", 10), testMediaAllocation("", "same", 20)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.RequestCount != 2 || report.Summary.AttemptCount != 2 || report.Summary.Charged != "30" || report.Summary.KnownProviderCost != "3" || report.Summary.UnknownProviderCosts != 1 {
		t.Fatalf("text and media facts collided or media cost became known: %+v", report.Summary)
	}
}

func TestOperatingReportExactLargeAmountsAndUnknownUnions(t *testing.T) {
	f := testFilter()
	huge := strings.Repeat("9", 100)
	facts := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("9223372036854775807"), refunded: text("0")}}
	attempts := []attemptFact{{id: "a1", request: "r", channel: "c", status: "contract", amount: &huge}, {id: "a2", request: "r", channel: "c", status: "invoice", amount: &huge}}
	report, err := aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{testTextAllocation("r", 9223372036854775807)}, nil)
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
			r, err := aggregate(f, f.From, facts, a, nil, nil, nil)
			if err != nil || r.Summary.UnknownProviderCosts != 1 || r.Summary.KnownProviderCost != "0" || r.Summary.GrossMargin != nil {
				t.Fatal("unknown cost fabricated as final zero", err)
			}
		})
	}
	facts[0].charged, facts[0].refunded = nil, nil
	r, err := aggregate(f, f.From, facts, nil, nil, nil, nil)
	if err != nil || r.Summary.UnsettledRequests != 1 || r.Summary.GrossMargin != nil {
		t.Fatal("unfinished request reported complete margin")
	}
}

func TestOperatingReportEmptyLimitAndCorruptFacts(t *testing.T) {
	f := testFilter()
	empty, err := aggregate(f, f.From, nil, nil, nil, nil, nil)
	if err != nil || empty.Summary.GrossMargin == nil || *empty.Summary.GrossMargin != "0" || empty.Summary.ProvisionalGrossMargin == nil || *empty.Summary.ProvisionalGrossMargin != "0" || empty.Summary.BillConfirmedGrossMargin == nil || *empty.Summary.BillConfirmedGrossMargin != "0" || empty.Requests == nil || empty.Models == nil || empty.Channels == nil || empty.OfferReports == nil {
		t.Fatal("empty report is not a complete zero result")
	}
	encoded, _ := json.Marshal(empty)
	if !strings.Contains(string(encoded), `"amount_unit":"micro_coin"`) || !strings.Contains(string(encoded), `"provider_cost":{}`) || !strings.Contains(string(encoded), `"fx_policy_versions":[]`) || !strings.Contains(string(encoded), `"offer_reports":[]`) || !strings.Contains(string(encoded), `"cost_as_of":null`) {
		t.Fatal("empty report JSON contract differs", string(encoded))
	}
	if r, err := aggregate(f, f.From, make([]requestFact, MaxRequests+1), nil, nil, nil, nil); !errors.Is(err, ErrRangeTooLarge) || !reflect.DeepEqual(r, Report{}) {
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
			r, err := aggregate(f, f.From, tc.facts, tc.attempts, nil, nil, nil)
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

func TestOperatingReportNetRefundRoundingAndReconsumption(t *testing.T) {
	f := testFilter()
	facts := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("3"), refunded: text("1")}}
	attempts := []attemptFact{{id: "a", request: "r", channel: "c", status: "invoice", amount: text("34")}}
	allocation := settlementLotFact{key: "edge", request: sourceKey(textFactSource, "r"), tenant: "tenant", lotID: "lot", currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: 3, refunded: 1, fiat: 100, issued: 6}
	report, err := aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{allocation}, nil)
	if err != nil || report.Summary.PaymentBasis["CNY"] != "33" || report.Summary.GrossMargin == nil || *report.Summary.GrossMargin != "-1" || report.Summary.BillConfirmedGrossMargin == nil || *report.Summary.BillConfirmedGrossMargin != "-1" {
		t.Fatalf("cumulative net floor did not preserve the original remainder: %+v, %v", report.Summary, err)
	}

	facts = []requestFact{
		{row: RequestRow{RequestID: "first", ModelID: "m", CreatedAt: f.From}, charged: text("6"), refunded: text("6")},
		{row: RequestRow{RequestID: "second", ModelID: "m", CreatedAt: f.From}, charged: text("6"), refunded: text("0")},
	}
	attempts = []attemptFact{
		{id: "a1", request: "first", channel: "c", status: "invoice", amount: text("0")},
		{id: "a2", request: "second", channel: "c", status: "invoice", amount: text("0")},
	}
	first := settlementLotFact{key: "first-edge", request: sourceKey(textFactSource, "first"), tenant: "tenant", lotID: "shared", currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: 6, refunded: 6, fiat: 100, issued: 10}
	second := settlementLotFact{key: "second-edge", request: sourceKey(textFactSource, "second"), tenant: "tenant", lotID: "shared", currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: 6, fiat: 100, issued: 10}
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{first, second}, nil)
	if err != nil || report.Summary.PaymentBasis["CNY"] != "60" || report.Summary.NetRevenue != "6" || report.Summary.BillConfirmedGrossMargin == nil || *report.Summary.BillConfirmedGrossMargin != "60" {
		t.Fatalf("refunded lot re-consumption double counted or lost current cash basis: %+v, %v", report.Summary, err)
	}
	first.refunded = 0
	facts[0].refunded = text("0")
	if report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{first, second}, nil); err == nil || !reflect.DeepEqual(report, Report{}) {
		t.Fatal("per-lot net consumption above issued was capped instead of failing")
	}
}

func TestOperatingReportRejectsBrokenRefundAndLotFacts(t *testing.T) {
	f := testFilter()
	fact := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("3"), refunded: text("1")}}
	attempt := []attemptFact{{id: "a", request: "r", channel: "c", status: "invoice", amount: text("0")}}
	valid := settlementLotFact{key: "edge", request: sourceKey(textFactSource, "r"), tenant: "tenant", lotID: "lot", currency: "CNY", basisKind: "paid", buyRateID: "rate", charged: 3, refunded: 1, fiat: 100, issued: 6}
	for name, mutate := range map[string]func(*settlementLotFact, *requestFact){
		"edge refund above charge":   func(a *settlementLotFact, _ *requestFact) { a.refunded = 4 },
		"missing cumulative refund":  func(a *settlementLotFact, _ *requestFact) { a.refunded = 0 },
		"missing lot identity":       func(a *settlementLotFact, _ *requestFact) { a.lotID = "" },
		"refund above request total": func(_ *settlementLotFact, r *requestFact) { r.refunded = text("2") },
	} {
		t.Run(name, func(t *testing.T) {
			allocation, request := valid, fact[0]
			mutate(&allocation, &request)
			report, err := aggregate(f, f.From, []requestFact{request}, attempt, nil, []settlementLotFact{allocation}, nil)
			if err == nil || !reflect.DeepEqual(report, Report{}) {
				t.Fatal("broken original refund edge was silently accepted")
			}
		})
	}
}

func testRequestEconomics(offer, task string, valuation economics.Currency, rates ...economics.FxRate) *requestEconomicsFact {
	at := testFilter().From
	id := "44444444-4444-4444-8444-444444444444"
	return &requestEconomicsFact{offerVersionID: offer, taskClass: task, fxPolicyVersionID: id, valuationCurrency: valuation, createdAt: at,
		fx: economics.FxPolicy{ID: id, ValuationCurrency: valuation, Source: "fixture", QuotedAt: at.Add(-time.Hour), ValidUntil: at.Add(time.Hour), CreatedBy: "fixture", CreatedAt: at.Add(-time.Hour), Rates: rates}}
}

func TestOperatingReportInnerLatestRevisionAndOuterCorrection(t *testing.T) {
	f := testFilter()
	facts := []requestFact{{row: RequestRow{RequestID: "r", ModelID: "m", CreatedAt: f.From}, charged: text("100"), refunded: text("0")}}
	outerTime, innerTime := f.From.Add(time.Hour), f.From.Add(2*time.Hour)
	attempts := []attemptFact{{id: "a", request: "r", channel: "c", status: "contract", amount: text("10"), currency: "CNY", asOf: &outerTime, revision: "outer-final"}}
	inner := []attemptCostFact{
		{attempt: "a", component: "inner", id: "inner-revision-1", basis: "unknown", asOf: outerTime, revision: 1},
		{attempt: "a", component: "inner", id: "inner-revision-2", basis: "invoice", amount: text("7"), currency: "CNY", asOf: innerTime, revision: 2},
	}
	report, err := aggregate(f, f.From, facts, attempts, inner, []settlementLotFact{testTextAllocation("r", 100)}, nil)
	if err != nil || len(report.Requests) != 1 {
		t.Fatal(err)
	}
	row := report.Requests[0]
	if row.AttemptCount != 1 || row.KnownProviderCost != "17" || row.ProviderCost["CNY"] != "17" || row.ProvisionalGrossMargin == nil || *row.ProvisionalGrossMargin != "83" || row.BillConfirmedGrossMargin != nil || row.CostAsOf == nil || !row.CostAsOf.Equal(innerTime) || row.CostRevision != "inner-revision-2" || report.Channels[0].KnownProviderCost != "17" {
		t.Fatalf("latest inner revision or outer attempt count changed: %+v", row)
	}
	attempts[0].status = "invoice"
	report, err = aggregate(f, f.From, facts, attempts, inner, []settlementLotFact{testTextAllocation("r", 100)}, nil)
	if err != nil || report.Requests[0].BillConfirmedGrossMargin == nil || *report.Requests[0].BillConfirmedGrossMargin != "83" {
		t.Fatal("all-invoice outer and inner costs did not confirm the bill margin", err)
	}
	correctionTime := f.From.Add(3 * time.Hour)
	attempts[0].amount, attempts[0].asOf, attempts[0].revision = text("120"), &correctionTime, "outer-correction"
	report, err = aggregate(f, f.From, facts, attempts, inner, []settlementLotFact{testTextAllocation("r", 100)}, nil)
	if err != nil || report.Requests[0].ProvisionalGrossMargin == nil || *report.Requests[0].ProvisionalGrossMargin != "-27" || report.Requests[0].CostRevision != "outer-correction" || report.Summary.CostRevision != "" {
		t.Fatal("current outer correction did not lower and negate the margin", err)
	}
	unknownTime := f.From.Add(4 * time.Hour)
	inner = append(inner, attemptCostFact{attempt: "a", component: "inner", id: "inner-revision-3", basis: "unknown", asOf: unknownTime, revision: 3})
	report, err = aggregate(f, f.From, facts, attempts, inner, []settlementLotFact{testTextAllocation("r", 100)}, nil)
	if err != nil || report.Summary.GrossMargin != nil || report.Summary.ProvisionalGrossMargin != nil || report.Summary.BillConfirmedGrossMargin != nil || report.Summary.UnknownProviderCosts != 1 || report.Requests[0].AttemptCount != 1 || report.Requests[0].CostRevision != "inner-revision-3" {
		t.Fatal("latest unknown inner cost did not suppress both margins", err)
	}
}

func TestOperatingReportFrozenCrossCurrencyMarginsAndOfferAggregation(t *testing.T) {
	f := testFilter()
	offer := "55555555-5555-4555-8555-555555555555"
	snapshot := testRequestEconomics(offer, "coding_reasoning", economics.CurrencyUSD, economics.FxRate{SourceCurrency: economics.CurrencyCNY, TargetCurrency: economics.CurrencyUSD, RateNum: 1, RateDen: 7})
	facts := []requestFact{{row: RequestRow{RequestID: "cross", ModelID: "m", CreatedAt: f.From, OfferVersionID: offer, TaskClass: "coding_reasoning"}, charged: text("100"), refunded: text("0"), economics: snapshot}}
	attempts := []attemptFact{{id: "a", request: "cross", channel: "c", status: "invoice", amount: text("600"), currency: "CNY"}}
	allocation := testTextAllocation("cross", 100)
	allocation.currency = "USD"
	// The report as-of is after expiry; conversion must still use the frozen
	// snapshot's original observation time and directed rational rate.
	report, err := aggregate(f, f.From.Add(2*time.Hour), facts, attempts, nil, []settlementLotFact{allocation}, nil)
	if err != nil || len(report.Requests) != 1 || len(report.OfferReports) != 1 {
		t.Fatal(err)
	}
	row := report.Requests[0]
	if row.ProvisionalGrossMargin == nil || *row.ProvisionalGrossMargin != "14" || row.BillConfirmedGrossMargin == nil || *row.BillConfirmedGrossMargin != "14" || row.GrossMargin == nil || *row.GrossMargin != "14" || row.GrossMarginCurrency == nil || *row.GrossMarginCurrency != "USD" || row.MarginValuation == nil || *row.MarginValuation != "fx_snapshot" || !reflect.DeepEqual(row.FxPolicyVersions, []string{snapshot.fxPolicyVersionID}) || report.OfferReports[0].TaskClass != "coding_reasoning" {
		t.Fatalf("USD/CNY frozen FX margin or offer dimension mismatch: %+v", row)
	}

	reverseSnapshot := testRequestEconomics(offer, "translation", economics.CurrencyCNY, economics.FxRate{SourceCurrency: economics.CurrencyUSD, TargetCurrency: economics.CurrencyCNY, RateNum: 7, RateDen: 1})
	facts[0].row.RequestID, facts[0].row.TaskClass = "reverse", "translation"
	facts[0].economics = reverseSnapshot
	attempts[0].request, attempts[0].amount, attempts[0].currency = "reverse", text("50"), "USD"
	allocation = testTextAllocation("reverse", 100)
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{allocation}, nil)
	if err != nil || report.Summary.ProvisionalGrossMargin == nil || *report.Summary.ProvisionalGrossMargin != "-250" || report.Summary.GrossMarginCurrency == nil || *report.Summary.GrossMarginCurrency != "CNY" {
		t.Fatalf("CNY/USD cost-upper conversion mismatch: %+v, %v", report.Summary, err)
	}

	// A same-currency original margin can join a converted aggregate only when
	// its currency equals the common valuation currency. Mixed valuation fails.
	facts = []requestFact{
		{row: RequestRow{RequestID: "cross", ModelID: "m", CreatedAt: f.From, OfferVersionID: offer, TaskClass: "coding_reasoning"}, charged: text("100"), refunded: text("0"), economics: snapshot},
		{row: RequestRow{RequestID: "same", ModelID: "m", CreatedAt: f.From, OfferVersionID: offer, TaskClass: "coding_reasoning"}, charged: text("20"), refunded: text("0"), economics: testRequestEconomics(offer, "coding_reasoning", economics.CurrencyUSD)},
	}
	attempts = []attemptFact{
		{id: "a1", request: "cross", channel: "c", status: "invoice", amount: text("600"), currency: "CNY"},
		{id: "a2", request: "same", channel: "c", status: "invoice", amount: text("5"), currency: "USD"},
	}
	usd := testTextAllocation("same", 20)
	usd.currency = "USD"
	cross := testTextAllocation("cross", 100)
	cross.currency = "USD"
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{cross, usd}, nil)
	if err != nil || report.Summary.ProvisionalGrossMargin == nil || *report.Summary.ProvisionalGrossMargin != "29" || report.Summary.BillConfirmedGrossMargin == nil || *report.Summary.BillConfirmedGrossMargin != "29" || report.Summary.MarginValuation == nil || *report.Summary.MarginValuation != "fx_snapshot" || len(report.OfferReports) != 1 || report.OfferReports[0].ProvisionalGrossMargin == nil || *report.OfferReports[0].ProvisionalGrossMargin != "29" {
		t.Fatalf("complete consistent valuation did not aggregate: %+v, %v", report.Summary, err)
	}
	facts[1].row.OfferVersionID, facts[1].row.TaskClass = "", ""
	facts[1].economics = nil
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{cross, usd}, nil)
	if err != nil || report.Summary.ProvisionalGrossMargin == nil || *report.Summary.ProvisionalGrossMargin != "29" || len(report.OfferReports) != 1 {
		t.Fatal("legacy same-valuation request should aggregate without joining offer reports", err)
	}
	facts[1].row.OfferVersionID, facts[1].row.TaskClass = offer, "coding_reasoning"
	facts[1].economics = testRequestEconomics(offer, "coding_reasoning", economics.CurrencyUSD)
	attempts[1].currency = "CNY"
	cny := testTextAllocation("same", 20)
	report, err = aggregate(f, f.From, facts, attempts, nil, []settlementLotFact{cross, cny}, nil)
	if err != nil || report.Summary.ProvisionalGrossMargin != nil || report.Summary.BillConfirmedGrossMargin != nil || report.Summary.GrossMarginCurrency != nil {
		t.Fatal("inconsistent request valuation currencies produced an aggregate margin", err)
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
	profile, schemaID, digest := "11111111-1111-4111-8111-111111111111", "33333333-3333-4333-8333-333333333333", strings.Repeat("ab", 32)
	zeroTotals := Totals{Charged: "0", Refunded: "0", NetRevenue: "0", KnownProviderCost: "0", UnknownProviderCosts: 1}
	r := Report{Requests: []RequestRow{{RequestID: "r", TenantID: "t", TenantName: " \t=SUM(1,2)", ModelID: "m", ModelName: "model,\"quoted\"\nline", CreatedAt: at, Status: "completed", BillingStatus: "settled", Protocol: "openai_chat", ExecutionKind: "synchronous", ProfileVersion: profile, SchemaID: schemaID, SchemaDigest: digest, VariantKey: "default", OfferVersionID: "offer", TaskClass: "coding_reasoning", Totals: Totals{Charged: "9007199254740993", Refunded: "0", NetRevenue: "9007199254740993", AttemptCount: 2, KnownProviderCost: "9007199254740994", GrossMargin: text("-1"), ProviderCost: map[string]string{"CNY": "9007199254740994"}, ProvisionalGrossMargin: text("-1"), GrossMarginCurrency: text("CNY"), MarginValuation: text("original_currency"), CostAsOf: &at, CostRevision: "cost-revision"}},
		{RequestID: "pending", CreatedAt: at, Protocol: "tts", ExecutionKind: "synchronous", ProfileVersion: profile, Totals: zeroTotals},
		{RequestID: "snapshotless", CreatedAt: at, ExecutionKind: "synchronous", Totals: zeroTotals},
		{RequestID: "execution-absent", CreatedAt: at, Protocol: "openai_chat", Totals: zeroTotals},
		{RequestID: "fully-absent", CreatedAt: at, Totals: zeroTotals}}}
	var out bytes.Buffer
	if err := WriteCSV(&out, r); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 || len(rows[0]) != 36 || rows[1][2] != "' \t=SUM(1,2)" || rows[1][4] != "model,\"quoted\"\nline" || rows[1][5] != "2026-11-04T02:00:00.123456Z" || rows[1][8] != "9007199254740993" || rows[1][14] != "-1" || rows[5][14] != "" ||
		!reflect.DeepEqual(rows[0][25:], []string{"offer_version_id", "task_class", "provider_cost_cny", "provider_cost_usd", "cost_as_of", "cost_revision", "provisional_gross_margin", "bill_confirmed_gross_margin", "gross_margin_currency", "margin_valuation", "fx_policy_versions"}) ||
		rows[1][25] != "offer" || rows[1][26] != "coding_reasoning" || rows[1][27] != "9007199254740994" || rows[1][29] != "2026-11-04T02:00:00.123456Z" || rows[1][30] != "cost-revision" || rows[1][31] != "-1" || rows[1][32] != "" || rows[1][33] != "CNY" || rows[1][34] != "original_currency" || rows[5][31] != "" {
		t.Fatal("CSV precision/escaping/appended P0 columns differ", rows)
	}
	for _, row := range rows {
		if len(row) != 36 {
			t.Fatal("CSV metadata changed the fixed column count", row)
		}
	}
	if !reflect.DeepEqual(rows[1][19:25], []string{"openai_chat", "synchronous", profile, schemaID, digest, "default"}) ||
		!reflect.DeepEqual(rows[2][19:25], []string{"tts", "synchronous", profile, "", "", ""}) ||
		!reflect.DeepEqual(rows[3][19:25], []string{"", "synchronous", "", "", "", ""}) ||
		!reflect.DeepEqual(rows[4][19:25], []string{"openai_chat", "", "", "", "", ""}) ||
		!reflect.DeepEqual(rows[5][19:25], []string{"", "", "", "", "", ""}) {
		t.Fatal("CSV complete/empty metadata was not preserved", rows)
	}
	want := errors.New("writer unavailable")
	if err := WriteCSV(failingWriter{want}, r); !errors.Is(err, want) {
		t.Fatal("CSV writer failure hidden", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestOperatingReportCacheZeroAndOtherNoAttemptUnknown(t *testing.T) {
	f := testFilter()
	for _, tc := range []struct {
		name        string
		cache       bool
		wantUnknown int64
		wantMargin  bool
	}{{"explicit cache", true, 0, true}, {"other no-attempt", false, 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			facts := []requestFact{{
				row:     RequestRow{RequestID: "r", ModelID: "m", BillingStatus: "settled", CreatedAt: f.From},
				charged: text("0"), refunded: text("0"), cache: tc.cache,
			}}
			report, err := aggregate(f, f.From, facts, nil, nil, nil, nil)
			if err != nil || report.Summary.UnknownProviderCosts != tc.wantUnknown || report.Summary.KnownProviderCost != "0" || (report.Summary.GrossMargin != nil) != tc.wantMargin {
				t.Fatalf("no-attempt supplier cost classification changed: %+v, err=%v", report.Summary, err)
			}
		})
	}
}

func TestReadSQLHasBoundedCurrentFactsOnly(t *testing.T) {
	if !strings.Contains(requestsSQL, "LIMIT 5001") || !strings.Contains(requestsSQL, "r.created_at >= $1 AND r.created_at < $2") || !strings.Contains(attemptsSQL, "ANY($1::pg_catalog.text[])") || !strings.Contains(mediaRequestsSQL, "q.created_at >= $1 AND q.created_at < $2") || !strings.Contains(mediaAttemptsSQL, "ANY($1::pg_catalog.text[])") {
		t.Fatal("cohort bound or exact interval removed")
	}
	for _, sql := range []string{requestsSQL, attemptsSQL, mediaRequestsSQL, mediaAttemptsSQL} {
		for _, forbidden := range []string{"FOR UPDATE", "FOR SHARE", "INSERT ", "UPDATE ", "DELETE ", "report_totals", "platform_probe", "pp_", "funding_lot", "text_response_cache", "response_body"} {
			if strings.Contains(sql, forbidden) {
				t.Fatalf("operating report now depends on %q", forbidden)
			}
		}
	}
	for _, sql := range []string{settlementLotsSQL, mediaSettlementLotsSQL} {
		if !strings.Contains(sql, "s.lot_id") || !strings.Contains(sql, "funding_consumption_refund_lots") || !strings.Contains(sql, "r.allocation_index=s.allocation_index") || !strings.Contains(sql, "r.lot_id=s.lot_id") {
			t.Fatal("original allocation lot or cumulative refund read was removed")
		}
	}
	if !strings.Contains(attemptsSQL, "latest_cost_correction_id") || !strings.Contains(attemptsSQL, "latest_cost_finalization_id") || !strings.Contains(attemptCostComponentsSQL, "newer.revision>c.revision") {
		t.Fatal("current outer pointer or latest inner revision read was removed")
	}
	for _, sql := range []string{attemptCostComponentsSQL, settlementLotsSQL, mediaSettlementLotsSQL} {
		for _, forbidden := range []string{"FOR UPDATE", "FOR SHARE", "INSERT ", "UPDATE ", "DELETE ", "report_totals", "response_body"} {
			if strings.Contains(sql, forbidden) {
				t.Fatalf("P0 reporting read now depends on %q", forbidden)
			}
		}
	}
}

var _ io.Writer = failingWriter{}
