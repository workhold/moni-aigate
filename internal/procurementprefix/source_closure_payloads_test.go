package procurementprefix

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourcePayloadAccountJSON = `{"id":"account","label":" original account ","provider":" supplier ","state":"active","limit_amount":"9223372036854775807","held_amount":"0","spent_amount":"3","revision":"1","created_at":"2026-11-03T08:00:00.123456+08:00"}`
const sourcePayloadBindingJSON = `{"id":"binding","credential_id":"credential","channel_id":"channel","account_id":"account","operation_id":"00000000-0000-0000-0000-000000000000","bound_at":"2026-11-03T00:00:00Z","channel_revision":"1"}`
const sourcePayloadObservationJSON = `{"id":"observation","account_id":"account","operation_id":"00000000-0000-0000-0000-000000000000","actor":"operator","status":"unknown","amount":null,"currency":"CNY","source":"supplier","semantic":"remaining","observed_at":"2026-11-03T00:00:00.000001Z","provider_as_of":null,"evidence_ref":"invoice:供应商.1","created_at":"2026-11-03T00:00:01Z"}`

func sourcePayloadTestParse(t *testing.T, raw string) (sourcePayloadValue, bool) {
	t.Helper()
	v, valid, err := sourceParsePayload(sourceUsageWork(t), raw)
	if err != nil {
		t.Fatal(err)
	}
	return v, valid
}

func sourcePayloadTestState(t *testing.T, rows Rows) *sourceClosureState {
	t.Helper()
	s := sourceTestRows(t, rows)
	// These fixtures isolate the JSON leaf from unrelated physical scalar,
	// temporal and provenance requirements, which their own tests exercise.
	s.bad = make(map[string]keySet)
	return s
}

func TestSourcePayloadSnapshotKindsAndRealNullBranches(t *testing.T) {
	for kind, raw := range map[string]string{"account_create": sourcePayloadAccountJSON, "account_configure": sourcePayloadAccountJSON, "credential_bind": sourcePayloadBindingJSON, "balance_observe": sourcePayloadObservationJSON} {
		v, ok := sourcePayloadTestParse(t, raw)
		if !ok || !sourcePayloadSnapshot(kind, v) {
			t.Fatalf("legal %s snapshot rejected", kind)
		}
	}
	known := strings.Replace(sourcePayloadObservationJSON, `"status":"unknown","amount":null`, `"status":"known","amount":"9223372036854775807"`, 1)
	known = strings.Replace(known, `"provider_as_of":null`, `"provider_as_of":"2026-11-02T23:59:59.999999Z"`, 1)
	if v, ok := sourcePayloadTestParse(t, known); !ok || !sourcePayloadSnapshot("balance_observe", v) {
		t.Fatal("known observation rejected")
	}
	rows := Rows{}
	for i, c := range []ProcurementCommandRow{
		{Kind: "account_create", Status: "applied", BeforeState: "null", AfterState: sourcePayloadAccountJSON},
		{Kind: "account_configure", Status: "applied", BeforeState: sourcePayloadAccountJSON, AfterState: sourcePayloadAccountJSON},
		{Kind: "account_configure", Status: "rejected", Reason: "revision_conflict", BeforeState: sourcePayloadAccountJSON, AfterState: "null"},
		{Kind: "account_configure", Status: "rejected", Reason: "not_found", BeforeState: "null", AfterState: "null"},
		{Kind: "credential_bind", Status: "applied", BeforeState: "null", AfterState: sourcePayloadBindingJSON},
		{Kind: "credential_bind", Status: "rejected", Reason: "already_bound", BeforeState: sourcePayloadBindingJSON, AfterState: "null"},
		{Kind: "credential_bind", Status: "rejected", Reason: "not_found", BeforeState: "null", AfterState: "null"},
		{Kind: "credential_bind", Status: "rejected", Reason: "revision_conflict", BeforeState: "null", AfterState: "null"},
		{Kind: "credential_bind", Status: "rejected", Reason: "credential_revoked", BeforeState: "null", AfterState: "null"},
		{Kind: "balance_observe", Status: "applied", BeforeState: "null", AfterState: sourcePayloadObservationJSON},
		{Kind: "balance_observe", Status: "rejected", Reason: "not_found", BeforeState: "null", AfterState: "null"},
	} {
		c.OperationID = string(rune('a' + i))
		rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, c)
	}
	s := sourcePayloadTestState(t, rows)
	if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 0 {
		t.Fatal("real command null union rejected", s.bad, err)
	}
	for _, c := range rows.OriginalRows.ProcurementCommands {
		copy := c
		if c.Status == "applied" {
			copy.AfterState = "null"
		} else {
			copy.AfterState = sourcePayloadAccountJSON
		}
		s := sourcePayloadTestState(t, Rows{OriginalRows: OriginalRows{ProcurementCommands: []ProcurementCommandRow{copy}}})
		if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 1 {
			t.Fatalf("invalid after/null branch accepted: %+v %v", copy, err)
		}
	}
}

func TestSourcePayloadSnapshotClosedMembersAndBusinessScalars(t *testing.T) {
	cases := []struct{ kind, raw string }{
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"id":"account",`, "", 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"id":"account",`, `"id":"account","id":"account",`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"id":"account",`, `"id":"account","\u0069d":"account",`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"id":"account",`, `"id":"account","extra":"x",`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"id":"account"`, `"id":null`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"revision":"1"`, `"revision":1`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"label":" original account "`, `"label":"   "`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"provider":" supplier "`, `"provider":"s\u0085p"`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"created_at":"2026-11-03T08:00:00.123456+08:00"`, `"created_at":"2026-11-03T08:00:00.123456789+08:00"`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `.123456+08:00`, `,123456+08:00`, 1)},
		{"account_create", strings.Replace(sourcePayloadAccountJSON, `"state":"active"`, `"state":"unknown"`, 1)},
		{"credential_bind", strings.Replace(sourcePayloadBindingJSON, `"operation_id":"00000000-0000-0000-0000-000000000000"`, `"operation_id":"future"`, 1)},
		{"credential_bind", strings.Replace(sourcePayloadBindingJSON, `"channel_revision":"1"`, `"channel_revision":"0"`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"amount":null`, `"amount":"0"`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"status":"unknown"`, `"status":"known"`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"provider_as_of":null`, `"provider_as_of":0`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"currency":"CNY"`, `"currency":"USD"`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"evidence_ref":"invoice:供应商.1"`, `"evidence_ref":"https://supplier.invalid/bill"`, 1)},
		{"balance_observe", strings.Replace(sourcePayloadObservationJSON, `"source":"supplier"`, `"source":"`+strings.Repeat("界", 34)+`"`, 1)},
	}
	for _, amount := range []string{"-1", "-0", "+1", "01", "1.0", "1e0", "9223372036854775808"} {
		cases = append(cases, struct{ kind, raw string }{"account_create", strings.Replace(sourcePayloadAccountJSON, `"limit_amount":"9223372036854775807"`, `"limit_amount":"`+amount+`"`, 1)})
	}
	for _, amount := range []string{strings.Repeat("9", 101), "-1", "01", "1e2"} {
		cases = append(cases, struct{ kind, raw string }{"account_create", strings.Replace(sourcePayloadAccountJSON, `"spent_amount":"3"`, `"spent_amount":"`+amount+`"`, 1)})
	}
	for i, tc := range cases {
		v, ok := sourcePayloadTestParse(t, tc.raw)
		if ok && sourcePayloadSnapshot(tc.kind, v) {
			t.Fatalf("invalid snapshot %d accepted: %s", i, tc.raw)
		}
	}
	// Existing rows can have edge whitespace, and NUMERIC buckets are wider
	// than the command's BIGINT limit. Neither is a new-input restriction.
	wide := strings.Replace(sourcePayloadAccountJSON, `"spent_amount":"3"`, `"spent_amount":"`+strings.Repeat("9", 100)+`"`, 1)
	wide = strings.Replace(wide, `"provider":" supplier "`, `"provider":"`+strings.Repeat("界", 33)+` "`, 1)
	if v, ok := sourcePayloadTestParse(t, wide); !ok || !sourcePayloadSnapshot("account_configure", v) {
		t.Fatal("historical edge whitespace or wide bucket narrowed")
	}
}

func sourcePayloadAuditVectors() map[string]string {
	return map[string]string{
		"procurement.command.applied":  `{"kind":"credential_bind","status":"applied"}`,
		"procurement.command.rejected": `{"kind":"balance_observe","status":"rejected"}`,
		"procurement.cost_proposed":    `{"attempt_id":"attempt","amount":"9223372036854775807","basis":"invoice","evidence_ref":"invoice:供应商.1"}`,
		"procurement.cost_approved":    `{"attempt_id":"attempt","amount":"0","evidence_ref":"invoice:1"}`,
		"cost.corrected":               `{"amount":"0","basis":"contract"}`,
		"reconciliation.proposed":      `{"usage":{"status":"confirmed_final","input_tokens":5,"output_tokens":3,"total_tokens":8,"cached_tokens":2,"reasoning_tokens":1},"evidence_ref":"invoice:1","reason":" \n人工核对 "}`,
		"reconciliation.approved":      `{"outcome":"pending_reconciliation"}`,
		"reconciliation.released":      `{"reason":" \n人工释放 "}`,
		"billing.settled":              `{"amount":"1","reason":"charged_usage"}`,
		"billing.released":             `{"amount":"0","reason":"released_deadline"}`,
		"billing.profile_blocked":      `{"waived":"` + strings.Repeat("9", 100) + `"}`,
	}
}

func TestSourcePayloadAuditAndOutboxExactFamilies(t *testing.T) {
	for action, raw := range sourcePayloadAuditVectors() {
		t.Run(action, func(t *testing.T) {
			v, ok := sourcePayloadTestParse(t, raw)
			if !ok || !sourcePayloadAudit(action, v) {
				t.Fatal("real audit payload rejected", raw)
			}
			for _, malformed := range []string{"null", "[]", "{}", raw + "{}", strings.Replace(raw, "{", `{"extra":null,`, 1)} {
				if v, ok := sourcePayloadTestParse(t, malformed); ok && sourcePayloadAudit(action, v) {
					t.Fatal("open audit shape accepted", malformed)
				}
			}
		})
	}
	for kind, raw := range map[string]string{
		"cost_recorded":  `{"attempt_id":"attempt","amount":"` + strings.Repeat("9", 100) + `","basis":"contract"}`,
		"cost_corrected": `{"attempt_id":"attempt","amount":"9223372036854775807","basis":"invoice"}`,
		"settled":        `{"amount":"9223372036854775807","released":"0","request_id":"request"}`,
		"released":       `{"amount":"0","released":"9223372036854775807","request_id":"request"}`,
	} {
		v, ok := sourcePayloadTestParse(t, raw)
		if !ok || !sourcePayloadOutbox(kind, v) {
			t.Fatalf("real %s outbox rejected", kind)
		}
		if kind == "cost_recorded" && sourcePayloadOutbox("cost_corrected", v) {
			t.Fatal("correction money incorrectly widened to NUMERIC")
		}
	}
	for _, amount := range []string{"-1", "-0", "+1", "01", "1.0", "1e2", "9223372036854775808"} {
		v, ok := sourcePayloadTestParse(t, `{"amount":"`+amount+`","basis":"invoice"}`)
		if ok && sourcePayloadAudit("cost.corrected", v) {
			t.Fatal("noncanonical or overflowing Money accepted", amount)
		}
	}
	for _, raw := range []string{`{"amount":0,"basis":"contract"}`, `{"amount":null,"basis":"contract"}`, `{"amount":"0","basis":"unknown"}`, `{"amount":"0","basis":"contract","amount":"0"}`, `{"amount":"0","basis":"contract","\u0061mount":"0"}`} {
		if v, ok := sourcePayloadTestParse(t, raw); ok && sourcePayloadAudit("cost.corrected", v) {
			t.Fatal("bad monetary audit accepted", raw)
		}
	}
	for _, outcome := range []string{"settled", "released", "pending_reconciliation"} {
		v, ok := sourcePayloadTestParse(t, `{"outcome":"`+outcome+`"}`)
		if !ok || !sourcePayloadAudit("reconciliation.approved", v) {
			t.Fatal("real approve outcome narrowed", outcome)
		}
	}
}

func TestSourcePayloadNestedUsageExactIntegers(t *testing.T) {
	base := sourcePayloadAuditVectors()["reconciliation.proposed"]
	for _, change := range [][2]string{
		{`"status":"confirmed_final"`, `"status":"provisional"`},
		{`"input_tokens":5`, `"input_tokens":"5"`},
		{`"input_tokens":5`, `"input_tokens":5.0`},
		{`"input_tokens":5`, `"input_tokens":5e0`},
		{`"input_tokens":5`, `"input_tokens":-0`},
		{`"input_tokens":5`, `"input_tokens":9223372036854775808`},
		{`"input_tokens":5`, `"input_tokens":null`},
		{`"input_tokens":5`, `"input_tokens":{}`},
		{`"cached_tokens":2`, `"cached_tokens":6`},
		{`"reasoning_tokens":1`, `"reasoning_tokens":4`},
		{`"total_tokens":8`, `"total_tokens":7`},
		{`"input_tokens":5`, `"input_tokens":5,"\u0069nput_tokens":5`},
		{`"input_tokens":5,`, ""},
		{`"input_tokens":5,`, `"extra":1,"input_tokens":5,`},
		{`"usage":{`, `"usage":[{`},
	} {
		raw := strings.Replace(base, change[0], change[1], 1)
		if v, ok := sourcePayloadTestParse(t, raw); ok && sourcePayloadAudit("reconciliation.proposed", v) {
			t.Fatal("bad nested Usage accepted", raw)
		}
	}
	for _, raw := range []string{
		strings.Replace(base, sourceUsagePayloadJSON, `{"status":"confirmed_final","input_tokens":9223372036854775807,"output_tokens":0,"total_tokens":9223372036854775807,"cached_tokens":0,"reasoning_tokens":0}`, 1),
		strings.Replace(base, sourceUsagePayloadJSON, `{"status":"confirmed_final","input_tokens":9007199254740993,"output_tokens":1,"total_tokens":9007199254740994,"cached_tokens":0,"reasoning_tokens":0}`, 1),
	} {
		if v, ok := sourcePayloadTestParse(t, raw); !ok || !sourcePayloadAudit("reconciliation.proposed", v) {
			t.Fatal("exact large JSON integer lost", raw)
		}
	}
	overflow := strings.Replace(base, sourceUsagePayloadJSON, `{"status":"confirmed_final","input_tokens":9223372036854775807,"output_tokens":1,"total_tokens":0,"cached_tokens":0,"reasoning_tokens":0}`, 1)
	if v, ok := sourcePayloadTestParse(t, overflow); ok && sourcePayloadAudit("reconciliation.proposed", v) {
		t.Fatal("confirmed total wrapped int64")
	}
}

func TestSourcePayloadUnicodeAndDecodedByteBoundaries(t *testing.T) {
	for _, raw := range []string{`{"reason":"literal �"}`, `{"reason":"\ud83d\ude00"}`, ` { "re\u0061son" : "valid\nreason" } `} {
		if v, ok := sourcePayloadTestParse(t, raw); !ok || !sourcePayloadAudit("reconciliation.released", v) {
			t.Fatal("legal Unicode or escaped key rejected", raw)
		}
	}
	for _, raw := range []string{
		`{"reason":"\ud800"}`, `{"reason":"\udc00"}`, `{"reason":"\ud800\u0041"}`, `{"reason":"\ud800\ud800"}`,
		`{"reason":"\u0000"}`, `{"rea\u0000son":"x"}`, `{"reason":"x","rea\u0073on":"x"}`,
		"{\"reason\":\"\xff\"}", "{\"reason\":\"x\x00\"}", `{"reason":"x"}true`, `{"reason":true}`, `{"reason":[]}`, `{"reason":{"reason":"x"}}`,
	} {
		if v, ok := sourcePayloadTestParse(t, raw); ok && sourcePayloadAudit("reconciliation.released", v) {
			t.Fatal("invalid JSON/Unicode accepted", raw)
		}
	}
	for _, n := range []int{199, 200, 201} {
		raw := `{"reason":"` + strings.Repeat("x", n) + `"}`
		v, ok := sourcePayloadTestParse(t, raw)
		if !ok || sourcePayloadAudit("reconciliation.released", v) != (n <= 200) {
			t.Fatal("reason byte boundary wrong", n)
		}
	}
	for _, n := range []int{66, 67} {
		v, ok := sourcePayloadTestParse(t, `{"reason":"`+strings.Repeat("界", n)+`"}`)
		if !ok || sourcePayloadAudit("reconciliation.released", v) != (n == 66) {
			t.Fatal("reason bound counted runes instead of UTF8 bytes", n)
		}
	}
}

func TestSourcePayloadAllReferencedOutputSlotsAndSchemaVersion(t *testing.T) {
	for name, prefix := range map[string]PrefixRows{
		"account command": {AccountCommandSources: []AccountCommandSourceRow{{ID: "source", AuditID: "audit"}}},
		"proposal":        {ProposalBirths: []ProposalBirthRow{{ID: "source", AuditID: "audit"}}},
		"approval":        {ApprovalConsumptions: []ApprovalConsumptionRow{{ID: "source", AuditID: "audit", OutboxID: "outbox"}}},
		"unmanaged":       {UnmanagedCorrectionSources: []UnmanagedCorrectionSourceRow{{ID: "source", AuditID: "audit", OutboxID: "outbox"}}},
		"reconciliation":  {ReconciliationSources: []ReconciliationSourceRow{{ID: "source", AuditID: "audit"}}},
		"billing audit":   {CustomerFinishOutcomes: []CustomerFinishOutcomeRow{{ID: "source", BillingAuditID: sourcePtr("audit")}}},
		"profile audit":   {CustomerFinishOutcomes: []CustomerFinishOutcomeRow{{ID: "source", ProfileBlockAuditID: sourcePtr("audit")}}},
		"billing outbox":  {CustomerFinishOutcomes: []CustomerFinishOutcomeRow{{ID: "source", BillingOutboxID: sourcePtr("outbox")}}},
		"cost link":       {CostOutboxLinks: []CostOutboxLinkRow{{ID: "source", OutboxID: "outbox"}}},
		"normal direct":   {NormalCostDecisions: []NormalCostDecisionRow{{ID: "source", CostRecordedOutboxID: sourcePtr("outbox")}}},
	} {
		t.Run(name, func(t *testing.T) {
			rows := Rows{PrefixRows: prefix, OriginalRows: OriginalRows{AuditEvents: []AuditEventRow{{ID: "audit", Action: "unknown", SanitizedDiff: "null"}}, ProjectionOutbox: []ProjectionOutboxRow{{EventID: "outbox", Kind: "unknown", SanitizedPayload: "null"}}}}
			s := sourcePayloadTestState(t, rows)
			want := 1
			if name == "approval" || name == "unmanaged" {
				want = 2
			}
			if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != want {
				t.Fatal("referenced output escaped after reverse pointer/action deletion", s.bad, err)
			}
		})
	}
	for _, version := range []string{"0", "01", "2", "", "1"} {
		rows := Rows{OriginalRows: OriginalRows{ProjectionOutbox: []ProjectionOutboxRow{{EventID: "cost", Kind: "cost_corrected", SchemaVersion: version, SanitizedPayload: `{"attempt_id":"a","amount":"0","basis":"contract"}`}}}}
		s := sourcePayloadTestState(t, rows)
		if err := s.payloadRows(); err != nil || (len(s.bad["scalar"]) > 0) != (version != "1") {
			t.Fatal("outbox schema version not exact", version, s.bad, err)
		}
	}
}

func TestSourcePayloadPhysicalIdentitySelectionAndLegacyBoundary(t *testing.T) {
	rows := Rows{OriginalRows: OriginalRows{
		ProcurementCommands: []ProcurementCommandRow{{OperationID: "same", Kind: "account_create", Status: "applied", BeforeState: "null", AfterState: sourcePayloadAccountJSON}},
		AuditEvents: []AuditEventRow{
			{ID: "same", Action: "cost.corrected", SanitizedDiff: `{"amount":"0","basis":"contract"}`},
			{ID: "ordinary", Action: "billing.settled", SanitizedDiff: `{"arbitrary":"ordinary checker owns this"}`},
			{ID: "other", Action: "identity.unknown", SanitizedDiff: `null`},
		},
		ProjectionOutbox: []ProjectionOutboxRow{
			{EventID: "same", Kind: "cost_recorded", SchemaVersion: "1", SanitizedPayload: `{"attempt_id":"attempt","amount":"0","basis":"contract"}`},
			{EventID: "ordinary", Kind: "settled", SchemaVersion: "2", SanitizedPayload: `null`},
			{EventID: "other", Kind: "identity.unknown", SchemaVersion: "9", SanitizedPayload: `null`},
		},
		PeriodicBudgetOperations: []PeriodicBudgetOperationRow{{OperationID: "pb", SafeInput: "null", ResultSnapshot: "null"}},
	}}
	s := sourcePayloadTestState(t, rows)
	if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 0 {
		t.Fatal("valid or unrelated family rejected", s.bad, err)
	}
	rows.OriginalRows.ProcurementCommands[0].BeforeState, rows.OriginalRows.ProcurementCommands[0].AfterState = "{}", "{}"
	rows.OriginalRows.AuditEvents[0].SanitizedDiff = "null"
	rows.OriginalRows.ProjectionOutbox[0].SanitizedPayload = "null"
	rows.OriginalRows.ProcurementCommands = append(rows.OriginalRows.ProcurementCommands, rows.OriginalRows.ProcurementCommands[0])
	s = sourcePayloadTestState(t, rows)
	if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("physical rows or two bad snapshots conflated", s.bad, err)
	}
	for _, table := range []string{"procurement_commands", "audit_events", "projection_outbox"} {
		if _, ok := s.bad["scalar"][tupleKey(table, tupleKey("same"))]; !ok {
			t.Fatal("missing natural table/PK identity", table)
		}
	}
	if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 3 {
		t.Fatal("same physical row counted twice", err)
	}
	for _, referenced := range []bool{false, true} {
		r := Rows{OriginalRows: OriginalRows{AuditEvents: []AuditEventRow{{ID: "billing-audit", Action: "billing.settled", SanitizedDiff: "null"}}, ProjectionOutbox: []ProjectionOutboxRow{{EventID: "billing-outbox", Kind: "settled", SchemaVersion: "1", SanitizedPayload: "null"}}}}
		if referenced {
			r.PrefixRows.CustomerFinishOutcomes = []CustomerFinishOutcomeRow{{ID: "finish", BillingAuditID: sourcePtr("billing-audit"), BillingOutboxID: sourcePtr("billing-outbox")}}
		} else {
			r.OriginalRows.AuditEvents[0].PPSourceKind = sourcePtr("reconciliation")
			r.OriginalRows.ProjectionOutbox[0].PPSourceKind = sourcePtr("reconciliation")
		}
		s := sourcePayloadTestState(t, r)
		if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 2 {
			t.Fatal("tracked/referenced billing payload escaped", referenced, s.bad, err)
		}
		r.OriginalRows.AuditEvents[0].Action = "unknown"
		r.OriginalRows.ProjectionOutbox[0].Kind = "unknown"
		s = sourcePayloadTestState(t, r)
		if err := s.payloadRows(); err != nil || len(s.bad["scalar"]) != 2 {
			t.Fatal("changing selected action/kind escaped", referenced, s.bad, err)
		}
	}
}

func TestSourcePayloadResourceBusinessBoundsAndCancellation(t *testing.T) {
	for _, n := range []int{16384, 16385} {
		after := strings.Repeat(" ", n-len(sourcePayloadAccountJSON)) + sourcePayloadAccountJSON
		rows := Rows{OriginalRows: OriginalRows{ProcurementCommands: []ProcurementCommandRow{{OperationID: "operation", Kind: "account_create", Status: "applied", BeforeState: "null", AfterState: after}}}}
		s := sourcePayloadTestState(t, rows)
		if err := s.payloadRows(); err != nil || (len(s.bad["scalar"]) != 0) != (n > 16384) {
			t.Fatal("16384-byte command business bound misclassified", n, s.bad, err)
		}
	}
	exact := strings.Repeat(" ", int(fieldLimit)-len(`{"reason":"x"}`)) + `{"reason":"x"}`
	if v, valid, err := sourceParsePayload(sourceUsageWork(t), exact); err != nil || !valid || !sourcePayloadAudit("reconciliation.released", v) {
		t.Fatal("exact global resource boundary rejected", valid, err)
	}
	if v, valid, err := sourceParsePayload(sourceUsageWork(t), " "+exact); !errors.Is(err, ErrResource) || valid || !reflect.DeepEqual(v, sourcePayloadValue{}) {
		t.Fatal("global oversize exposed a value", valid, err)
	}
	ctx := &replayCancelContext{Context: context.Background(), remaining: 8}
	w := &replayWork{b: budget{ctx: ctx}}
	if v, valid, err := sourceParsePayload(w, exact); !errors.Is(err, ErrCanceled) || valid || !reflect.DeepEqual(v, sourcePayloadValue{}) {
		t.Fatal("mid-byte-scan cancellation exposed a value", valid, err)
	}
	w = &replayWork{b: budget{ctx: context.Background(), deadline: time.Now().Add(-time.Second)}}
	if _, valid, err := sourceParsePayload(w, exact); !errors.Is(err, ErrTimeout) || valid {
		t.Fatal("parser renewed original deadline", valid, err)
	}
	if _, valid, err := sourceParsePayload(nil, "{}"); !errors.Is(err, ErrInput) || valid {
		t.Fatal("nil work accepted", valid, err)
	}
	if err := (*sourceClosureState)(nil).payloadRows(); !errors.Is(err, ErrInput) {
		t.Fatal("nil state accepted", err)
	}
}
