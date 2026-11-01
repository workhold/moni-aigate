package alerts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

const testID = "11111111-1111-4111-8111-111111111111"

func TestParametersRejectAmbiguousWireValues(t *testing.T) {
	for _, tc := range []struct {
		kind Kind
		raw  string
	}{
		{ChannelQuarantined, `null`}, {ChannelQuarantined, `[]`}, {ChannelQuarantined, `{"x":1}`},
		{ProcurementBudgetLow, `{"threshold_amount":"0","recovery_amount":"0","threshold_amount":"1"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"0","recovery_amount":"0","\u0074hreshold_amount":"1"}`},
		{ProcurementBudgetLow, `{"threshold_amount":0,"recovery_amount":"0"}`},
		{ProcurementBudgetLow, `{"threshold_amount":null,"recovery_amount":"0"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"0"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"00","recovery_amount":"0"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"-0","recovery_amount":"0"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"2","recovery_amount":"1"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"9223372036854775808","recovery_amount":"9223372036854775808"}`},
		{ProcurementBudgetLow, `{"threshold_amount":"\ud800","recovery_amount":"0"}`},
		{CustomerReconciliationAged, `{"minimum_age_seconds":"599","trigger_count":"1","recovery_count":"0"}`},
		{CustomerReconciliationAged, `{"minimum_age_seconds":"604801","trigger_count":"1","recovery_count":"0"}`},
		{CustomerReconciliationAged, `{"minimum_age_seconds":"600","trigger_count":"1001","recovery_count":"0"}`},
		{CustomerReconciliationAged, `{"minimum_age_seconds":"600","trigger_count":"1","recovery_count":"1"}`},
	} {
		if _, err := DecodeParameters(tc.kind, []byte(tc.raw)); err == nil {
			t.Errorf("accepted %s %s", tc.kind, tc.raw)
		}
	}
	for _, tc := range []struct {
		kind Kind
		raw  string
	}{
		{ChannelQuarantined, `{}`},
		{ProcurementBudgetLow, `{"threshold_amount":"0","recovery_amount":"9223372036854775807"}`},
		{CustomerReconciliationAged, `{"minimum_age_seconds":"604800","trigger_count":"1000","recovery_count":"999"}`},
	} {
		if _, err := DecodeParameters(tc.kind, []byte(tc.raw)); err != nil {
			t.Error(tc.raw, err)
		}
	}
}

func TestStrictJSONUnicodeAndLabelBoundaries(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"x":"\ud800"}`), []byte(`{"x":"\udc00"}`), []byte(`{"x":"\ud800\u0041"}`), {'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, []byte(`{"x":"a"} {"x":"b"}`)} {
		if _, err := strictObject(raw, "x"); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := strictObject([]byte(`{"x":"\ud83d\ude00\\\""}`), "x"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"", " x", "x\u00a0", "\u2000x", "a\x00b", "a\u0085b", strings.Repeat("x", 81), strings.Repeat("界", 54)} {
		if validLabel(v) {
			t.Errorf("accepted label %q", v)
		}
	}
	for _, v := range []string{"运营告警", strings.Repeat("x", 80), strings.Repeat("界", 53), "a b", "a\u200bb"} {
		if !validLabel(v) {
			t.Errorf("rejected label %q", v)
		}
	}
}

func TestTimeRequiresCanonicalFiniteUTCAndMicroseconds(t *testing.T) {
	for _, raw := range []string{`null`, `123`, `"0000-01-01T00:00:00.000000Z"`, `"2026-11-01T00:00:00Z"`, `"2026-11-01T00:00:00.0000000Z"`, `"2026-11-01T00:00:00.000000+00:00"`, `"2026-02-30T00:00:00.000000Z"`} {
		var v Time
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, at := range []time.Time{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 1, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("east", 3600))} {
		if _, err := json.Marshal(Time{at}); err == nil {
			t.Errorf("accepted %s", at)
		}
	}
	var v Time
	if err := json.Unmarshal([]byte(`"2026-11-01T00:00:00.123456Z"`), &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) != `"2026-11-01T00:00:00.123456Z"` {
		t.Fatal(string(b), err)
	}
}

func TestSampleSafeProjectionAndWideAmounts(t *testing.T) {
	at := Time{time.Date(2026, 11, 1, 1, 0, 0, 0, time.UTC)}
	wide := strings.Repeat("9", 100)
	n, _ := new(big.Int).SetString(wide, 10)
	available := new(big.Int).Neg(new(big.Int).Mul(n, big.NewInt(2))).String()
	budget := BudgetSample{ProcurementBudgetLow, at, "paused", "0", wide, wide, available, "CNY"}
	b, err := json.Marshal(budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeSample(b); err != nil {
		t.Fatal(err)
	}
	budget.AvailableAmount = "0"
	if _, err = json.Marshal(budget); err == nil {
		t.Fatal("inconsistent money accepted")
	}
	zero := AgingSample{Kind: CustomerReconciliationAged, AsOf: at, MinimumAgeSeconds: "600", ObservedCount: "0"}
	b, err = json.Marshal(zero)
	if err != nil || !strings.Contains(string(b), `"oldest_created_at":null,"oldest_reference_id":null`) {
		t.Fatal(string(b), err)
	}
	old := Time{at.Add(-600 * time.Second)}
	id := testID
	full := AgingSample{CustomerReconciliationAged, at, "600", "1001", true, &old, &id}
	if _, err = json.Marshal(full); err != nil {
		t.Fatal(err)
	}
	full.CountIsLowerBound = false
	if _, err = json.Marshal(full); err == nil {
		t.Fatal("1001 count without lower bound accepted")
	}
	full.CountIsLowerBound = true
	full.ObservedCount = "1002"
	if _, err = json.Marshal(full); err == nil {
		t.Fatal("unbounded count accepted")
	}
	full.ObservedCount = "1"
	full.CountIsLowerBound = false
	old = Time{at.Add(-599 * time.Second)}
	if _, err = json.Marshal(full); err == nil {
		t.Fatal("too-young oldest accepted")
	}
	for _, raw := range []string{`{"kind":"channel_quarantined","as_of":"2026-11-01T00:00:00.000000Z","enabled":null,"quarantined":false}`, `{"kind":"channel_quarantined","as_of":"2026-11-01T00:00:00.000000Z","enabled":true,"quarantined":false,"secret":"bad"}`} {
		if _, err = decodeSample([]byte(raw)); err == nil {
			t.Fatal("unsafe sample accepted", raw)
		}
	}
}

func TestFilterDigestAndCursorBinding(t *testing.T) {
	hash := func(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
	if got := RuleFilterDigest(RuleFilter{}); got != hash(`{"v":1,"route":"rules","state":null,"kind":null,"scope_id":null}`) {
		t.Fatal(got)
	}
	if got := EpisodeFilterDigest(EpisodeFilter{}); got != hash(`{"v":1,"route":"episodes","status":null,"rule_id":null,"kind":null}`) {
		t.Fatal(got)
	}
	if got := EventFilterDigest(testID); got != hash(`{"v":1,"route":"events","episode_id":"`+testID+`"}`) {
		t.Fatal(got)
	}
	c := Cursor{1, Time{time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, testID, RuleFilterDigest(RuleFilter{})}
	raw, err := EncodeCursor(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := DecodeCursor(raw)
	if err != nil || parsed.ID != testID || !validCursor(parsed, c.FilterDigest) {
		t.Fatal(parsed, err)
	}
	if validCursor(parsed, EpisodeFilterDigest(EpisodeFilter{})) {
		t.Fatal("cross-route cursor accepted")
	}
	for _, invalid := range []string{raw + "=", strings.Repeat("a", 513), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"t":null,"id":"` + testID + `","filter_digest":"` + c.FilterDigest + `"}`))} {
		if _, err = DecodeCursor(invalid); err == nil {
			t.Fatal("invalid cursor accepted", invalid)
		}
	}
	for _, digest := range []string{strings.Repeat("z", 64), strings.Repeat("A", 64)} {
		bad := c
		bad.FilterDigest = digest
		if _, err = EncodeCursor(bad); err == nil {
			t.Fatal("invalid digest encoded", digest)
		}
	}
}

func TestFreshnessPriorityAndExactBoundary(t *testing.T) {
	at := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	revision := int64(2)
	old := at.Add(-180 * time.Second)
	for _, tc := range []struct {
		name      string
		state     RuleState
		options   RuntimeOptions
		success   *time.Time
		evaluated *int64
		want      Freshness
	}{
		{"retired before paused", "retired", RuntimeOptions{}, nil, nil, "retired"},
		{"disabled before paused", "disabled", RuntimeOptions{}, nil, nil, "disabled"},
		{"deployment paused", "enabled", RuntimeOptions{}, nil, nil, "paused"},
		{"recovery paused", "enabled", RuntimeOptions{true, true}, &at, &revision, "paused"},
		{"no sample", "enabled", RuntimeOptions{true, false}, nil, nil, "never_evaluated"},
		{"boundary fresh", "enabled", RuntimeOptions{true, false}, &old, &revision, "fresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Store{options: tc.options}
			if got := s.freshness(tc.state, 2, tc.success, tc.evaluated, at); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	s := Store{options: RuntimeOptions{true, false}}
	tooOld := old.Add(-time.Microsecond)
	future := at.Add(time.Microsecond)
	previous := int64(1)
	if s.freshness("enabled", 2, &tooOld, &revision, at) != "stale" || s.freshness("enabled", 2, &future, &revision, at) != "stale" || s.freshness("enabled", 2, &at, &previous, at) != "never_evaluated" {
		t.Fatal("freshness boundary incorrect")
	}
}
