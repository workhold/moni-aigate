package procurementprefix

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// These original wire documents and fixed hashes are independent fixtures, not
// output from a PB writer, its fingerprint helper, or another verifier.
const sourcePBInputJSON = `{"expected_revision":"0","period_kind":"utc_day","enabled":true,"limit_amount":"100"}`
const sourcePBSnapshotJSON = `{"scope":"tenant","scope_id":"00000000-0000-0000-0000-000000000001","revision":"1","period_kind":"utc_day","enabled":true,"limit_amount":"100","state":"scheduled","effective_from":"2026-11-02T00:00:00.000000Z","tracking_started_at":"2026-11-01T12:00:00.000000Z","current_window":null,"carry_held":"0","as_of":"2026-11-01T12:00:00.000000Z"}`
const sourcePBWindowJSON = `{"id":"00000000-0000-0000-0000-000000000004","start_at":"2026-11-01T00:00:00.000000Z","end_at":"2026-11-02T00:00:00.000000Z","spent_amount":"0","held_amount":"0"}`

func sourcePBTestRow() PeriodicBudgetOperationRow {
	return PeriodicBudgetOperationRow{
		OperationID: "00000000-0000-0000-0000-000000000003", Kind: "tenant_periodic_budget.configure",
		Actor: "00000000-0000-0000-0000-000000000009", Scope: "platform",
		Target:     "/api/platform/tenants/00000000-0000-0000-0000-000000000001/periodic-budget",
		ResourceID: "00000000-0000-0000-0000-000000000002", Revision: "1", SafeInput: sourcePBInputJSON,
		InputFingerprint: "20d12d9fbe14a4caee46329f3af9fcd11285816e6caf29af115c7e407ce13c4b",
		ResultSnapshot:   sourcePBSnapshotJSON, CreatedXID: "1",
		CreatedAt: Time{Time: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC), Valid: true},
	}
}

// Test-only framing uses a complete buffer with binary.Write, unlike the
// implementation's incremental stream. The fixed independent goldens below
// pin this recipe before it builds additional accepted fixture variants.
func sourcePBTestFingerprint(parts ...string) string {
	var framed bytes.Buffer
	for _, part := range parts {
		_ = binary.Write(&framed, binary.BigEndian, uint32(len([]byte(part))))
		framed.WriteString(part)
	}
	digest := sha256.Sum256(framed.Bytes())
	return hex.EncodeToString(digest[:])
}

func sourcePBTestRehash(row *PeriodicBudgetOperationRow, expected, period, enabled, limit string) {
	row.InputFingerprint = sourcePBTestFingerprint(row.Kind, row.Actor, row.Scope, row.Target, expected, period, enabled, limit)
}

func sourcePBTestValid(t *testing.T, row PeriodicBudgetOperationRow) {
	t.Helper()
	if valid, err := sourcePBOperation(sourceUsageWork(t), row); err != nil || !valid {
		t.Fatalf("legal original PB operation rejected: %v %v %+v", valid, err, row)
	}
}

func sourcePBTestInvalid(t *testing.T, row PeriodicBudgetOperationRow) {
	t.Helper()
	if valid, err := sourcePBOperation(sourceUsageWork(t), row); err != nil || valid {
		t.Fatalf("invalid original PB operation accepted or returned execution error: %v %v %+v", valid, err, row)
	}
}

func sourcePBTestRemoveMember(raw, member string) string {
	if strings.Contains(raw, member+",") {
		return strings.Replace(raw, member+",", "", 1)
	}
	return strings.Replace(raw, ","+member, "", 1)
}

func TestSourcePBPayloadIndependentFingerprintGoldensAndTextActorABI(t *testing.T) {
	for _, fixture := range []struct{ actor, hash string }{
		{"00000000-0000-0000-0000-000000000009", "20d12d9fbe14a4caee46329f3af9fcd11285816e6caf29af115c7e407ce13c4b"},
		{"原 actor\n� 🧪", "6fd16155e0b390c7be483730c6d84a319e68b840f79cd7a381624483d0601dab"},
		{"", "38288eea12350afa9a19864109851a1f20d17ea7b36574c766305a8679b43ba6"},
	} {
		row := sourcePBTestRow()
		row.Actor, row.InputFingerprint = fixture.actor, fixture.hash
		if got := sourcePBTestFingerprint(row.Kind, row.Actor, row.Scope, row.Target, "0", "utc_day", "true", "100"); got != fixture.hash {
			t.Fatal("independent framing fixture differs from fixed golden", got)
		}
		sourcePBTestValid(t, row)
		row.OperationID = "00000000-0000-0000-0000-000000000099"
		sourcePBTestValid(t, row) // operation_id is deliberately absent from framing.
		row.Actor += "x"
		sourcePBTestInvalid(t, row)
	}
	row := sourcePBTestRow()
	row.SafeInput = ` { "limit_amount":"1\u00300", "enabled":true, "period_kind":"utc_\u0064ay", "\u0065xpected_revision":"0" } `
	sourcePBTestValid(t, row)
	row.SafeInput = sourcePBInputJSON
	row.InputFingerprint = sourcePBTestFingerprint("moni-aigate:periodic:v1", row.Kind, row.Actor, row.Scope, row.Target, "0", "utc_day", "true", "100")
	sourcePBTestInvalid(t, row)
	row.InputFingerprint = sourcePBTestFingerprint(row.Kind, row.Actor, row.Scope, row.Target, "0", "utc_day", "true", "100", row.OperationID)
	sourcePBTestInvalid(t, row)
}

func TestSourcePBPayloadFullAcceptedScopePeriodStateAndAmounts(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		for _, period := range []string{"utc_day", "utc_month"} {
			for _, state := range []string{"scheduled", "active", "disabled"} {
				for _, window := range []bool{false, true} {
					row := sourcePBTestRow()
					row.Kind = scope + "_periodic_budget.configure"
					switch scope {
					case "key":
						row.Scope = "00000000-0000-0000-0000-000000000005"
						row.Target = "/api/keys/00000000-0000-0000-0000-000000000001/periodic-budget"
					case "procurement":
						row.Target = "/api/platform/procurement-accounts/00000000-0000-0000-0000-000000000001/periodic-budget"
					}
					row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"scope":"tenant"`, `"scope":"`+scope+`"`, 1)
					row.SafeInput = strings.Replace(row.SafeInput, "utc_day", period, 1)
					row.ResultSnapshot = strings.Replace(row.ResultSnapshot, "utc_day", period, 1)
					windowRaw := sourcePBWindowJSON
					if period == "utc_month" {
						row.ResultSnapshot = strings.Replace(row.ResultSnapshot, "2026-11-02T00:00:00", "2026-12-01T00:00:00", 1)
						windowRaw = strings.Replace(windowRaw, "2026-11-02T00:00:00", "2026-12-01T00:00:00", 1)
					}
					enabled := "true"
					if state == "disabled" {
						enabled = "false"
						row.SafeInput = strings.Replace(row.SafeInput, `"enabled":true`, `"enabled":false`, 1)
						row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"enabled":true`, `"enabled":false`, 1)
					}
					row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"state":"scheduled"`, `"state":"`+state+`"`, 1)
					if state == "active" {
						// The independent literal dates cover a later day/month with
						// a window covering that operation's original as_of.
						at := "2026-11-03T12:00:00.000000Z"
						windowRaw = strings.ReplaceAll(windowRaw, "2026-11-01", "2026-11-03")
						windowRaw = strings.ReplaceAll(windowRaw, "2026-11-02", "2026-11-04")
						if period == "utc_month" {
							at = "2026-12-03T12:00:00.000000Z"
							windowRaw = strings.Replace(sourcePBWindowJSON, "2026-11-01", "2026-12-01", 1)
							windowRaw = strings.Replace(windowRaw, "2026-11-02", "2027-01-01", 1)
							// Restore the window start to its natural month boundary.
						}
						row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"as_of":"2026-11-01T12:00:00.000000Z"`, `"as_of":"`+at+`"`, 1)
						row.CreatedAt.Time, _ = time.Parse(sourcePBUTC6, at)
					}
					if window {
						windowRaw = strings.Replace(windowRaw, `"spent_amount":"0"`, `"spent_amount":"`+strings.Repeat("9", 100)+`"`, 1)
						windowRaw = strings.Replace(windowRaw, `"held_amount":"0"`, `"held_amount":"9223372036854775808"`, 1)
						row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"current_window":null`, `"current_window":`+windowRaw, 1)
					}
					row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"carry_held":"0"`, `"carry_held":"`+strings.Repeat("8", 100)+`"`, 1)
					sourcePBTestRehash(&row, "0", period, enabled, "100")
					sourcePBTestValid(t, row)
				}
			}
		}
	}
	for _, limit := range []string{"0", "9223372036854775807"} {
		row := sourcePBTestRow()
		row.SafeInput = strings.Replace(row.SafeInput, `"limit_amount":"100"`, `"limit_amount":"`+limit+`"`, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"limit_amount":"100"`, `"limit_amount":"`+limit+`"`, 1)
		sourcePBTestRehash(&row, "0", "utc_day", "true", limit)
		sourcePBTestValid(t, row)
	}
	for _, item := range []struct{ expected, revision string }{{"1", "1"}, {"1", "2"}, {"9223372036854775806", "9223372036854775807"}, {"9223372036854775807", "9223372036854775807"}} {
		row := sourcePBTestRow()
		row.Revision = item.revision
		row.SafeInput = strings.Replace(row.SafeInput, `"expected_revision":"0"`, `"expected_revision":"`+item.expected+`"`, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"revision":"1"`, `"revision":"`+item.revision+`"`, 1)
		sourcePBTestRehash(&row, item.expected, "utc_day", "true", "100")
		sourcePBTestValid(t, row)
	}
}

func TestSourcePBPayloadExactJSONFieldAndTypeMatrix(t *testing.T) {
	for _, name := range []string{"expected_revision", "period_kind", "enabled", "limit_amount"} {
		original := map[string]string{"expected_revision": `"0"`, "period_kind": `"utc_day"`, "enabled": "true", "limit_amount": `"100"`}[name]
		missing := sourcePBTestRow()
		missing.SafeInput = sourcePBTestRemoveMember(missing.SafeInput, `"`+name+`":`+original)
		sourcePBTestInvalid(t, missing)
		for _, bad := range []string{"null", "0", "[]", "{}", `""`} {
			row := sourcePBTestRow()
			row.SafeInput = strings.Replace(row.SafeInput, `"`+name+`":`+original, `"`+name+`":`+bad, 1)
			sourcePBTestInvalid(t, row)
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`, sourcePBInputJSON + `{}`, sourcePBInputJSON + `true`,
		strings.Replace(sourcePBInputJSON, `"expected_revision":"0",`, "", 1),
		strings.Replace(sourcePBInputJSON, `"enabled":true`, `"Enabled":true`, 1),
		strings.Replace(sourcePBInputJSON, `"enabled":true`, `"enabled":"true"`, 1),
		strings.Replace(sourcePBInputJSON, `"enabled":true`, `"enabled":true,"\u0065nabled":true`, 1),
		strings.Replace(sourcePBInputJSON, `"enabled":true`, `"enabled":true,"enabled":false`, 1),
		strings.Replace(sourcePBInputJSON, `"enabled":true`, `"enabled":true,"operation_id":"x"`, 1),
		strings.Replace(sourcePBInputJSON, `"period_kind":"utc_day"`, `"period_kind":"UTC_DAY"`, 1),
	} {
		row := sourcePBTestRow()
		row.SafeInput = raw
		sourcePBTestInvalid(t, row)
	}
	for _, number := range []string{"", "-1", "-0", "+1", "01", "1.0", "1e0", " 1", "1 ", "9223372036854775808"} {
		for _, name := range []string{"expected_revision", "limit_amount"} {
			row := sourcePBTestRow()
			old := `"0"`
			if name == "limit_amount" {
				old = `"100"`
			}
			row.SafeInput = strings.Replace(row.SafeInput, `"`+name+`":`+old, `"`+name+`":"`+number+`"`, 1)
			sourcePBTestInvalid(t, row)
		}
	}
	for name, original := range map[string]string{
		"scope": `"tenant"`, "scope_id": `"00000000-0000-0000-0000-000000000001"`, "revision": `"1"`, "period_kind": `"utc_day"`, "enabled": "true", "limit_amount": `"100"`, "state": `"scheduled"`, "effective_from": `"2026-11-02T00:00:00.000000Z"`, "tracking_started_at": `"2026-11-01T12:00:00.000000Z"`, "carry_held": `"0"`, "as_of": `"2026-11-01T12:00:00.000000Z"`,
	} {
		missing := sourcePBTestRow()
		missing.ResultSnapshot = sourcePBTestRemoveMember(missing.ResultSnapshot, `"`+name+`":`+original)
		sourcePBTestInvalid(t, missing)
		for _, bad := range []string{"null", "0", "[]", "{}"} {
			row := sourcePBTestRow()
			row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"`+name+`":`+original, `"`+name+`":`+bad, 1)
			sourcePBTestInvalid(t, row)
		}
		row := sourcePBTestRow()
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"`+name+`":`+original, `"`+name+`":`+original+`,"`+name+`":`+original, 1)
		sourcePBTestInvalid(t, row)
	}
	for _, raw := range []string{
		"null", "[]", "{}", sourcePBSnapshotJSON + `{}`,
		strings.Replace(sourcePBSnapshotJSON, `"current_window":null,`, "", 1),
		strings.Replace(sourcePBSnapshotJSON, `"current_window":null`, `"current_window":"null"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"current_window":null`, `"current_window":null,"extra":false`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"scope":"tenant"`, `"scope":"tenant","\u0073cope":"tenant"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"enabled":true`, `"enabled":"true"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"revision":"1"`, `"revision":"0"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"state":"scheduled"`, `"state":"unconfigured"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"limit_amount":"100"`, `"limit_amount":"9223372036854775808"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"scope_id":"00000000-0000-0000-0000-000000000001"`, `"scope_id":"00000000-0000-0000-0000-000000000000"`, 1),
		strings.Replace(sourcePBSnapshotJSON, `"scope_id":"00000000-0000-0000-0000-000000000001"`, `"scope_id":"ABCDEF00-0000-0000-0000-000000000001"`, 1),
	} {
		row := sourcePBTestRow()
		row.ResultSnapshot = raw
		sourcePBTestInvalid(t, row)
	}
	for _, bad := range []string{"-1", "-0", "+1", "01", "1e0", strings.Repeat("9", 101)} {
		row := sourcePBTestRow()
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"carry_held":"0"`, `"carry_held":"`+bad+`"`, 1)
		sourcePBTestInvalid(t, row)
	}
}

func TestSourcePBPayloadWindowClosedShapeAndNaturalCalendar(t *testing.T) {
	for name, original := range map[string]string{"id": `"00000000-0000-0000-0000-000000000004"`, "start_at": `"2026-11-01T00:00:00.000000Z"`, "end_at": `"2026-11-02T00:00:00.000000Z"`, "spent_amount": `"0"`, "held_amount": `"0"`} {
		missing := sourcePBTestRow()
		missingWindow := sourcePBTestRemoveMember(sourcePBWindowJSON, `"`+name+`":`+original)
		missing.ResultSnapshot = strings.Replace(missing.ResultSnapshot, `"current_window":null`, `"current_window":`+missingWindow, 1)
		sourcePBTestInvalid(t, missing)
		for _, bad := range []string{"null", "0", "[]", "{}"} {
			row := sourcePBTestRow()
			window := strings.Replace(sourcePBWindowJSON, `"`+name+`":`+original, `"`+name+`":`+bad, 1)
			row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"current_window":null`, `"current_window":`+window, 1)
			sourcePBTestInvalid(t, row)
		}
	}
	for _, window := range []string{
		strings.Replace(sourcePBWindowJSON, `,"spent_amount":"0"`, "", 1),
		strings.Replace(sourcePBWindowJSON, `"spent_amount":"0"`, `"spent_amount":"0","as_of":"x"`, 1),
		strings.Replace(sourcePBWindowJSON, `"held_amount":"0"`, `"held_amount":"0","held_amount":"0"`, 1),
		strings.Replace(sourcePBWindowJSON, `"held_amount":"0"`, `"held_amount":"`+strings.Repeat("9", 101)+`"`, 1),
		strings.Replace(sourcePBWindowJSON, `"spent_amount":"0"`, `"spent_amount":"01"`, 1),
		strings.Replace(sourcePBWindowJSON, "2026-11-01T00:00:00", "2026-11-01T01:00:00", 1),
		strings.Replace(sourcePBWindowJSON, "2026-11-02T00:00:00", "2026-11-03T00:00:00", 1),
		strings.ReplaceAll(sourcePBWindowJSON, "2026-11", "2026-10"),
		strings.Replace(sourcePBWindowJSON, "00000000-0000-0000-0000-000000000004", "00000000-0000-0000-0000-000000000000", 1),
	} {
		row := sourcePBTestRow()
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"current_window":null`, `"current_window":`+window, 1)
		sourcePBTestInvalid(t, row)
	}
	for _, item := range []struct{ period, tracking, effective, at, start, end string }{
		{"utc_month", "2024-02-29T12:00:00.000000Z", "2024-03-01T00:00:00.000000Z", "2024-02-01T00:00:00.000000Z", "2024-02-01T00:00:00.000000Z", "2024-03-01T00:00:00.000000Z"},
		{"utc_month", "2024-12-31T23:59:59.999999Z", "2025-01-01T00:00:00.000000Z", "2025-01-01T00:00:00.000000Z", "2025-01-01T00:00:00.000000Z", "2025-02-01T00:00:00.000000Z"},
		{"utc_day", "0001-01-01T12:00:00.000000Z", "0001-01-02T00:00:00.000000Z", "0001-01-01T00:00:00.000000Z", "0001-01-01T00:00:00.000000Z", "0001-01-02T00:00:00.000000Z"},
		{"utc_day", "9999-12-29T12:00:00.000000Z", "9999-12-30T00:00:00.000000Z", "9999-12-30T00:00:00.000000Z", "9999-12-30T00:00:00.000000Z", "9999-12-31T00:00:00.000000Z"},
	} {
		row := sourcePBTestRow()
		row.SafeInput = strings.Replace(row.SafeInput, "utc_day", item.period, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, "utc_day", item.period, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"tracking_started_at":"2026-11-01T12:00:00.000000Z"`, `"tracking_started_at":"`+item.tracking+`"`, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"effective_from":"2026-11-02T00:00:00.000000Z"`, `"effective_from":"`+item.effective+`"`, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"as_of":"2026-11-01T12:00:00.000000Z"`, `"as_of":"`+item.at+`"`, 1)
		if item.at >= item.effective {
			row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"state":"scheduled"`, `"state":"active"`, 1)
		}
		window := strings.Replace(sourcePBWindowJSON, "2026-11-01T00:00:00.000000Z", item.start, 1)
		window = strings.Replace(window, "2026-11-02T00:00:00.000000Z", item.end, 1)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"current_window":null`, `"current_window":`+window, 1)
		row.CreatedAt.Time, _ = time.Parse(sourcePBUTC6, item.at)
		sourcePBTestRehash(&row, "0", item.period, "true", "100")
		sourcePBTestValid(t, row)
		row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"end_at":"`+item.end+`"`, `"end_at":"`+item.start+`"`, 1)
		sourcePBTestInvalid(t, row)
		if item.period == "utc_month" && item.start == "2024-02-01T00:00:00.000000Z" {
			// Thirty days after February 1 in this leap year is March 2,
			// whereas a natural monthly period ends on March 1.
			row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"end_at":"`+item.start+`"`, `"end_at":"2024-03-02T00:00:00.000000Z"`, 1)
			sourcePBTestInvalid(t, row)
		}
	}
}

func TestSourcePBPayloadUTC6AndRowBindings(t *testing.T) {
	for _, bad := range []string{"2026-11-01T12:00:00Z", "2026-11-01T12:00:00.000Z", "2026-11-01T12:00:00.000000000Z", "2026-11-01T12:00:00.000000+00:00", "2026-11-01T12:00:00,000000Z", "0000-11-01T12:00:00.000000Z", "2026-02-29T12:00:00.000000Z", "2026-11-01T12:00:60.000000Z", " 2026-11-01T12:00:00.000000Z"} {
		for _, name := range []string{"as_of", "tracking_started_at", "effective_from"} {
			row := sourcePBTestRow()
			old := "2026-11-01T12:00:00.000000Z"
			if name == "effective_from" {
				old = "2026-11-02T00:00:00.000000Z"
			}
			row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"`+name+`":"`+old+`"`, `"`+name+`":"`+bad+`"`, 1)
			sourcePBTestInvalid(t, row)
		}
	}
	for _, mutate := range []func(*PeriodicBudgetOperationRow){
		func(r *PeriodicBudgetOperationRow) { r.Kind = "key_periodic_budget.configure" },
		func(r *PeriodicBudgetOperationRow) { r.Scope = "00000000-0000-0000-0000-000000000001" },
		func(r *PeriodicBudgetOperationRow) { r.Target += "/" },
		func(r *PeriodicBudgetOperationRow) { r.Target += "?q=1" },
		func(r *PeriodicBudgetOperationRow) {
			r.Target = strings.Replace(r.Target, "/tenants/", "/tenants/%30", 1)
		},
		func(r *PeriodicBudgetOperationRow) { r.Revision = "2" },
		func(r *PeriodicBudgetOperationRow) { r.CreatedAt.Time = r.CreatedAt.Time.Add(time.Microsecond) },
		func(r *PeriodicBudgetOperationRow) {
			r.ResultSnapshot = strings.Replace(r.ResultSnapshot, `"state":"scheduled"`, `"state":"active"`, 1)
		},
		func(r *PeriodicBudgetOperationRow) {
			r.ResultSnapshot = strings.Replace(r.ResultSnapshot, "2026-11-02T00:00:00", "2026-11-03T00:00:00", 1)
		},
		func(r *PeriodicBudgetOperationRow) {
			r.ResultSnapshot = strings.Replace(r.ResultSnapshot, `"as_of":"2026-11-01T12:00:00.000000Z"`, `"as_of":"2026-10-31T12:00:00.000000Z"`, 1)
			r.CreatedAt.Time = r.CreatedAt.Time.AddDate(0, 0, -1)
		},
		func(r *PeriodicBudgetOperationRow) {
			r.SafeInput = strings.Replace(r.SafeInput, `"expected_revision":"0"`, `"expected_revision":"2"`, 1)
			sourcePBTestRehash(r, "2", "utc_day", "true", "100")
		},
		func(r *PeriodicBudgetOperationRow) {
			r.SafeInput = strings.Replace(r.SafeInput, `"expected_revision":"0"`, `"expected_revision":"9223372036854775807"`, 1)
			sourcePBTestRehash(r, "9223372036854775807", "utc_day", "true", "100")
		},
		func(r *PeriodicBudgetOperationRow) {
			r.SafeInput = strings.Replace(r.SafeInput, `"limit_amount":"100"`, `"limit_amount":"101"`, 1)
			sourcePBTestRehash(r, "0", "utc_day", "true", "101")
		},
		func(r *PeriodicBudgetOperationRow) { r.InputFingerprint = strings.ToUpper(r.InputFingerprint) },
		func(r *PeriodicBudgetOperationRow) { r.Actor += "\x00" },
	} {
		row := sourcePBTestRow()
		mutate(&row)
		sourcePBTestInvalid(t, row)
	}
	row := sourcePBTestRow()
	row.CreatedAt.Time = row.CreatedAt.Time.In(time.FixedZone("offset", 8*3600))
	sourcePBTestValid(t, row) // Stored instant, rather than its Go Location, binds as_of.
	row.ResultSnapshot = strings.Replace(row.ResultSnapshot, `"as_of":"2026-11-01T12:00:00.000000Z"`, `"as_of":"2026-11-01T11:00:00.000000Z"`, 1)
	row.CreatedAt.Time = row.CreatedAt.Time.Add(-time.Hour)
	sourcePBTestValid(t, row) // Same-period wall-clock regression is allowed.
}

func TestSourcePBPayloadUnicodeAndParserIsolation(t *testing.T) {
	for _, raw := range []string{
		string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}),
		`{"x":"\ud800"}`, `{"x":"\udc00"}`, `{"x":"\ud800\u0000"}`, `{"x":"\u0000"}`, "{\"x\":\"\x00\"}",
		`{"x":{"x":{"x":"bad"}}}`, `{"x":[]}`, `{"x":1}`, `{"x":true}null`,
	} {
		value, valid, err := sourcePBParse(sourceUsageWork(t), raw, false)
		if err != nil || valid || !reflect.DeepEqual(value, sourcePBValue{}) {
			t.Fatal("bad Unicode/shape accepted or returned partial parse", raw, valid, err)
		}
	}
	value, valid, err := sourcePBParse(sourceUsageWork(t), `{"x":"�\ud83e\uddea"}`, false)
	if err != nil || !valid || value.object["x"].text != "�🧪" {
		t.Fatal("legal Unicode pair or literal replacement character was narrowed", valid, err)
	}
	if _, valid, err := sourceParsePayload(sourceUsageWork(t), sourcePBInputJSON); err != nil || valid {
		t.Fatal("ordinary procurement parser unexpectedly widened to PB booleans", valid, err)
	}
}

func TestSourcePBPayloadPhysicalPKDeduplication(t *testing.T) {
	bad := sourcePBTestRow()
	bad.SafeInput, bad.ResultSnapshot = "null", "null"
	other := bad
	other.OperationID = "00000000-0000-0000-0000-000000000008"
	rows := Rows{OriginalRows: OriginalRows{PeriodicBudgetOperations: []PeriodicBudgetOperationRow{bad, bad, other}}}
	s := sourcePayloadTestState(t, rows)
	s.mark("scalar", "audit_events", tupleKey(bad.OperationID))
	if err := s.pbPayloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("double payload errors/duplicate rows did not deduplicate by physical PK", s.bad, err)
	}
	if err := s.pbPayloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("repeated leaf invocation duplicated scalar counts", s.bad, err)
	}
}

func TestSourcePBPayloadBoundsCancellationAndZeroResults(t *testing.T) {
	row := sourcePBTestRow()
	row.SafeInput = strings.Repeat(" ", int(fieldLimit)-len(row.SafeInput)) + row.SafeInput
	sourcePBTestValid(t, row)
	for _, length := range []int{8192, 8193} {
		copy := row
		copy.ResultSnapshot = strings.Repeat(" ", length-len(copy.ResultSnapshot)) + copy.ResultSnapshot
		valid, err := sourcePBOperation(sourceUsageWork(t), copy)
		if err != nil || valid != (length == 8192) {
			t.Fatal("result_snapshot original 8192-byte bound misclassified", length, valid, err)
		}
	}
	for _, snapshot := range []bool{false, true} {
		raw := row.SafeInput + " "
		value, valid, err := sourcePBParse(sourceUsageWork(t), raw, snapshot)
		if !errors.Is(err, ErrResource) || valid || !reflect.DeepEqual(value, sourcePBValue{}) {
			t.Fatal("resource error returned a partial JSON value", valid, err)
		}
	}
	ctx := &replayCancelContext{Context: context.Background(), remaining: 8}
	value, valid, err := sourcePBParse(&replayWork{b: budget{ctx: ctx}}, row.SafeInput, false)
	if !errors.Is(err, ErrCanceled) || valid || !reflect.DeepEqual(value, sourcePBValue{}) {
		t.Fatal("mid-JSON cancellation returned a value", valid, err)
	}
	expired := &replayWork{b: budget{ctx: context.Background(), deadline: time.Now().Add(-time.Second)}}
	if value, valid, err := sourcePBParse(expired, sourcePBInputJSON, false); !errors.Is(err, ErrTimeout) || valid || !reflect.DeepEqual(value, sourcePBValue{}) {
		t.Fatal("parser renewed absolute deadline or returned a value", valid, err)
	}
	if value, valid, err := sourcePBParse(nil, sourcePBInputJSON, false); !errors.Is(err, ErrInput) || valid || !reflect.DeepEqual(value, sourcePBValue{}) {
		t.Fatal("nil work accepted", valid, err)
	}
	if err := (*sourceClosureState)(nil).pbPayloadRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil state accepted", err)
	}
	row.Actor = strings.Repeat("界", 10000)
	ctx = &replayCancelContext{Context: context.Background(), remaining: 4}
	input := sourcePBInput{expected: "0", period: "utc_day", enabled: true, limit: "100"}
	if fingerprint, err := sourcePBFingerprint(&replayWork{b: budget{ctx: ctx}}, row, input); !errors.Is(err, ErrCanceled) || fingerprint != "" {
		t.Fatal("mid-framing cancellation returned a partial fingerprint", err)
	}
	row.Actor = strings.Repeat("x", int(fieldLimit)+1)
	if fingerprint, err := sourcePBFingerprint(sourceUsageWork(t), row, input); !errors.Is(err, ErrResource) || fingerprint != "" {
		t.Fatal("oversized framing value returned partial fingerprint", err)
	}
	rows := Rows{OriginalRows: OriginalRows{PeriodicBudgetOperations: []PeriodicBudgetOperationRow{row}}}
	w := sourceUsageWork(t)
	if result, err := checkSourceClosure(w, rows); !errors.Is(err, ErrResource) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("source entry returned partial report on oversized original field", err)
	}
	row.Actor = "actor"
	rows.OriginalRows.PeriodicBudgetOperations[0] = row
	w = sourceUsageWork(t)
	w.b.ctx = &replayCancelContext{Context: context.Background(), remaining: 15}
	if result, err := checkSourceClosure(w, rows); !errors.Is(err, ErrCanceled) || !reflect.DeepEqual(result, sourceClosureResult{}) {
		t.Fatal("source entry exposed partial report on shared cancellation", err)
	}
}
