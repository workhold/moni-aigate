package procurementprefix

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSourcePhysicalRegistryAll72Layouts(t *testing.T) {
	types := map[string]byte{"TEXT": 'T', "INET": 'A', "JSONB": 'J', "SMALLINT": 'H', "INTEGER": 'I', "BIGINT": 'L', "NUMERIC(100,0)": 'N', "XID8": 'X', "OID": 'O', "BOOLEAN": 'B', "TIMESTAMPTZ": 'D'}
	seen := make(keySet)
	for _, file := range []string{"b-procurement-original-core-layout-v1.json", "b-procurement-column-layout-v1.json"} {
		layout, _ := readLayout(t, file)
		for _, table := range layout.Tables {
			got, ok := sourcePhysicalLayouts[table.Name]
			if !ok || len(got.kinds) != len(table.Columns) {
				t.Fatalf("layout missing %s", table.Name)
			}
			fields := make(keySet)
			for i, col := range table.Columns {
				want, ok := types[col.Type]
				if !ok {
					t.Fatalf("unknown physical type %s", col.Type)
				}
				if col.Nullable {
					want += 'a' - 'A'
				}
				if got.kinds[i] != want {
					t.Fatalf("%s.%s physical kind differs", table.Name, col.Name)
				}
				fields[tupleKey(col.Name)] = struct{}{}
			}
			for _, pk := range strings.Fields(got.pk) {
				if _, ok := fields[tupleKey(pk)]; !ok {
					t.Fatalf("%s missing PK %s", table.Name, pk)
				}
			}
			seen[tupleKey(table.Name)] = struct{}{}
		}
	}
	if len(seen) != 72 || len(sourcePhysicalLayouts) != 72 {
		t.Fatal("physical inventory must be exactly 72 tables")
	}
}

func TestSourcePhysicalValuesAndDuplicateNaturalKeys(t *testing.T) {
	for _, tc := range []struct {
		kind  byte
		value string
		valid bool
	}{{'L', "0", true}, {'L', "-1", true}, {'L', "01", false}, {'L', "+1", false}, {'L', "9223372036854775808", false}, {'I', "2147483648", false}, {'X', "18446744073709551615", true}, {'X', "18446744073709551616", false}, {'N', strings.Repeat("9", 100), true}, {'N', strings.Repeat("9", 101), false}, {'N', "-0", false}, {'N', "1.0", false}, {'J', "null", true}, {'J', "{", false}, {'T', "a\x00b", false}} {
		if got := sourcePhysicalCell(reflect.ValueOf(tc.value), tc.kind); got != tc.valid {
			t.Fatalf("%c %q got=%v", tc.kind, tc.value, got)
		}
	}
	rows := Rows{OriginalRows: OriginalRows{ProcurementEvidenceIdentities: []ProcurementEvidenceIdentityRow{{AttemptID: "a:b", EvidenceDigest: "c"}, {AttemptID: "a", EvidenceDigest: "b:c"}, {AttemptID: "a:b", EvidenceDigest: "c"}}}}
	s := sourceTestRows(t, rows)
	if len(s.bad["duplicate"]) != 1 {
		t.Fatalf("natural tuple duplicate count %d", len(s.bad["duplicate"]))
	}
	w, _, err := newReplayWork(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	result, err := checkSourceClosure(w, rows)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.DuplicateRows != 1 {
		t.Fatal("duplicate phase not executed by entry")
	}
}

func sourcePointerFixture() Rows {
	p := sourcePtr
	return Rows{PrefixRows: PrefixRows{AccountCommandSources: []AccountCommandSourceRow{{ID: "source", RootID: "root", StepNo: "1", StepKind: "account_command", CreatedXID: "12", OperationID: "operation", AuditID: "audit"}}}, OriginalRows: OriginalRows{ProcurementCommands: []ProcurementCommandRow{{OperationID: "operation", PPSourceID: p("source"), PPRootID: p("root"), PPRootStep: p("1"), PPCreatedXID: p("12")}}, AuditEvents: []AuditEventRow{{ID: "audit", PPRootID: p("root"), PPRootStep: p("1"), PPSourceKind: p("account_command"), PPSourceID: p("source"), PPSourceRole: p("account_command"), PPCreatedXID: p("12")}}}}
}
func TestSourceOriginalPointerSlotsAndEntryCall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Rows)
		bad    bool
	}{
		{"exact slot", func(*Rows) {}, false},
		{"wrong role", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPSourceRole = sourcePtr("proposal") }, true},
		{"wrong source", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPSourceID = sourcePtr("other") }, true},
		{"wrong root", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPRootID = sourcePtr("other") }, true},
		{"wrong step", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPRootStep = sourcePtr("2") }, true},
		{"wrong xid", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPCreatedXID = sourcePtr("11") }, true},
		{"partial NULL", func(r *Rows) { r.OriginalRows.AuditEvents[0].PPSourceRole = nil }, true},
		{"source requires reverse slot", func(r *Rows) { r.OriginalRows.AuditEvents = nil }, true},
		{"tracked orphan", func(r *Rows) { r.PrefixRows.AccountCommandSources = nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := sourcePointerFixture()
			tc.mutate(&rows)
			s := sourceTestRows(t, rows)
			if err := s.originalPointers(); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["pointer"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["pointer"])
			}
			w, _, err := newReplayWork(context.Background(), rows)
			if err != nil {
				t.Fatal(err)
			}
			result, err := checkSourceClosure(w, rows)
			if err != nil {
				t.Fatal(err)
			}
			if (result.Stats.OriginalPointerRows > 0) != tc.bad {
				t.Fatal("originalPointers phase missing from core entry")
			}
		})
	}
	legacy := sourceTestRows(t, Rows{OriginalRows: OriginalRows{AuditEvents: []AuditEventRow{{ID: "old"}}}})
	if err := legacy.originalPointers(); err != nil {
		t.Fatal(err)
	}
	if len(legacy.bad["pointer"]) != 0 {
		t.Fatal("all-NULL historical boundary must not become a fabricated mismatch or proof")
	}
}
func TestSourceCompanionBothDirections(t *testing.T) {
	fixture := func() PrefixRows {
		return PrefixRows{MeterChoices: []MeterChoiceRow{{ID: "choice", RootID: "root", StepNo: "2", StepKind: "meter_choice", CreatedXID: "12", IncomingSourceID: "incoming", MeterEventID: "meter", Disposition: "created", CollisionKind: "none"}}, MeterOrigins: []MeterOriginRow{{MeterEventID: "meter", RootID: "root", StepNo: "2", StepKind: "meter_choice", SourceID: "choice", CreatedXID: "12", IncomingSourceID: "incoming"}}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*PrefixRows)
		bad    bool
	}{
		{"created companion", func(*PrefixRows) {}, false},
		{"missing reverse", func(r *PrefixRows) { r.MeterOrigins = nil }, true},
		{"orphan companion", func(r *PrefixRows) { r.MeterChoices = nil }, true},
		{"reused cannot birth", func(r *PrefixRows) { r.MeterChoices[0].Disposition = "reused" }, true},
		{"wrong root", func(r *PrefixRows) { r.MeterOrigins[0].RootID = "other" }, true},
		{"wrong source", func(r *PrefixRows) { r.MeterOrigins[0].SourceID = "other" }, true},
		{"conflict missing companion", func(r *PrefixRows) { r.MeterChoices[0].CollisionKind = "source_payload_mismatch" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := fixture()
			tc.mutate(&rows)
			s := sourceNewTestState(t, rows)
			if err := s.companions(); err != nil {
				t.Fatal(err)
			}
			if (len(s.bad["companion"]) > 0) != tc.bad {
				t.Fatalf("bad=%v keys=%v", tc.bad, s.bad["companion"])
			}
		})
	}
}

func TestSourceClosedNullableUnions(t *testing.T) {
	finiteTime := Time{Valid: true, Time: time.Date(2026, 11, 3, 1, 2, 3, 0, time.UTC)}
	cases := []struct {
		name, table string
		row         any
		valid       bool
	}{
		{"unmanaged birth", "pp_attempt_origins", AttemptOriginRow{InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged", CostBasis: "unknown"}, true},
		{"unmanaged partial account", "pp_attempt_origins", AttemptOriginRow{InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "unmanaged", CostBasis: "unknown", AccountID: sourcePtr("account")}, false},
		{"managed birth", "pp_attempt_origins", AttemptOriginRow{InitialDeliveryState: "prepared", InitialCostStatus: "unknown", ManagementMode: "managed", CostBasis: "contract", CostVersionID: sourcePtr("quote"), CostInputRate: sourcePtr("1"), CostOutputRate: sourcePtr("1"), AccountID: sourcePtr("account"), BindingID: sourcePtr("binding"), ProcurementTransitionID: sourcePtr("transition"), ReservedEventID: sourcePtr("event"), AccountJournalID: sourcePtr("journal"), AdmittedAt: finiteTime}, true},
		{"unknown means NULL amount", "pp_cost_mutations", CostMutationRow{CauseKind: "normal_cost_decision", BeforeStatus: "unknown", AfterStatus: "contract", AfterAmount: sourcePtr("0")}, true},
		{"zero is not NULL", "pp_cost_mutations", CostMutationRow{CauseKind: "normal_cost_decision", BeforeStatus: "unknown", BeforeAmount: sourcePtr("0"), AfterStatus: "contract", AfterAmount: sourcePtr("0")}, false},
		{"skip has no outputs", "pp_normal_cost_decisions", NormalCostDecisionRow{ManagementMode: "unmanaged", BeforeCostStatus: "unknown", Decision: "skip_no_final_usage_or_rate"}, true},
		{"skip cannot write", "pp_normal_cost_decisions", NormalCostDecisionRow{ManagementMode: "unmanaged", BeforeCostStatus: "unknown", Decision: "skip_no_final_usage_or_rate", CostMutationID: sourcePtr("mutation")}, false},
		{"reset suppressed", "pp_cost_reset_decisions", CostResetDecisionRow{CauseKind: "incoming_conflict", Policy: "suppressed_adjudicated"}, true},
		{"suppressed cannot mutate", "pp_cost_reset_decisions", CostResetDecisionRow{CauseKind: "incoming_conflict", Policy: "suppressed_adjudicated", CostMutationID: sourcePtr("mutation")}, false},
		{"normal request", "pp_request_decisions", RequestDecisionRow{Branch: "normal", NormalizedDeliveryStage: "none", FinalizationID: sourcePtr("f"), AfterLatestID: sourcePtr("f")}, true},
		{"normal conflict mixed", "pp_request_decisions", RequestDecisionRow{Branch: "normal", NormalizedDeliveryStage: "none", FinalizationID: sourcePtr("f"), AfterLatestID: sourcePtr("f"), ConflictSourceID: sourcePtr("c")}, false},
		{"pending finance", "pp_customer_finish_outcomes", CustomerFinishOutcomeRow{ResultKind: "pending", SettlementDecisionCheckedAt: finiteTime}, true},
		{"pending cannot settle", "pp_customer_finish_outcomes", CustomerFinishOutcomeRow{ResultKind: "pending", SettlementDecisionCheckedAt: finiteTime, SettlementID: sourcePtr("settlement")}, false},
		{"platform ledger", "pp_customer_finish_ledger_effects", CustomerFinishLedgerEffectRow{TenantID: "tenant", AccountKind: "REVENUE"}, true},
		{"platform is not tenant account", "pp_customer_finish_ledger_effects", CustomerFinishLedgerEffectRow{TenantID: "tenant", AccountKind: "REVENUE", AccountTenantID: sourcePtr("tenant")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceUnion(tc.table, reflect.ValueOf(tc.row)); got != tc.valid {
				t.Fatalf("valid=%v got=%v", tc.valid, got)
			}
		})
	}
}

func TestSourceRangesPreserveZeroRouteRevision(t *testing.T) {
	row := RequestOriginRow{RootID: "11111111-1111-4111-8111-111111111111", StepNo: "1", CreatedXID: "1", PBCreatedXID: "1", InputRate: "0", OutputRate: "0", InputUpperBound: "0", OutputLimit: "0", RouteRevision: "0", TenantPricingRevision: "1", InitialStatus: "accepted", InitialBillingStatus: "reserved"}
	s := sourceGeneratedTestState(t, "pp_request_origins", reflect.ValueOf(row))
	if err := s.semanticRows(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["scalar"]) != 0 {
		t.Fatal("route revision and request hard bound preserve original nonnegative ranges")
	}
	row.RouteRevision = "-1"
	s = sourceGeneratedTestState(t, "pp_request_origins", reflect.ValueOf(row))
	if err := s.semanticRows(); err != nil {
		t.Fatal(err)
	}
	if len(s.bad["scalar"]) != 1 {
		t.Fatal("negative route revision accepted")
	}
}
