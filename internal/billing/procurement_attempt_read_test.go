package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type procurementAttemptRowsFixture struct {
	values   [][]any
	index    int
	scanned  bool
	rowsErr  error
	closeErr error
	nextErr  error
	scanErr  error
	closed   bool
	onClose  func()
}

func (r *procurementAttemptRowsFixture) Next() (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.index >= 0 && r.index < len(r.values) && !r.scanned {
		return false, errors.New("fixture row not scanned")
	}
	r.index++
	r.scanned = false
	return r.index < len(r.values), nil
}

func (r *procurementAttemptRowsFixture) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 0 || r.index >= len(r.values) || len(dest) != len(r.values[r.index]) || r.scanned {
		return errors.New("fixture scan shape")
	}
	for i, value := range r.values[r.index] {
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("fixture scan target")
		}
		if value == nil {
			target.Elem().SetZero()
			continue
		}
		actual := reflect.ValueOf(value)
		if !actual.IsValid() || !actual.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fixture scan type")
		}
		target.Elem().Set(actual)
	}
	r.scanned = true
	return nil
}

func (r *procurementAttemptRowsFixture) Err() error { return r.rowsErr }
func (r *procurementAttemptRowsFixture) Close() error {
	r.closed = true
	if r.onClose != nil {
		r.onClose()
	}
	return r.closeErr
}

type procurementAttemptQueryFixture struct {
	tables       map[string][][]any
	calls        map[string]int
	queries      map[string][]string
	firstArgs    map[string][]any
	lastArgs     map[string][]any
	queryFailure string
	rowsFailure  string
	nextFailure  string
	scanFailure  string
	closeFailure string
	onRowsClose  func(string)
}

var procurementAttemptReadTables = []string{
	"channels", "channel_credentials", "channel_versions", "channel_version_credentials",
	"tenants", "request_records", "cost_versions", "procurement_accounts",
	"credential_procurement_bindings", "upstream_attempts", "procurement_reservations", "procurement_budget_events",
	"users", "provider_cost_corrections", "procurement_cost_proposals",
	"procurement_commands", "procurement_balance_observations", "audit_events", "projection_outbox",
}

func (q *procurementAttemptQueryFixture) Query(sql string, args ...any) (fundingSourceRows, error) {
	var table string
	for _, candidate := range procurementAttemptReadTables {
		if strings.Contains(sql, `"`+candidate+`"`) {
			table = candidate
			break
		}
	}
	if table == "" {
		return nil, errors.New("fixture table missing")
	}
	if table == "channel_version_credentials" {
		if len(args) != 3 ||
			!strings.Contains(sql, `WHERE NOT $1::pg_catalog.bool OR (channel_version_id COLLATE "C", credential_id COLLATE "C") > ($2::pg_catalog.text COLLATE "C", $3::pg_catalog.text COLLATE "C")`) ||
			!strings.Contains(sql, `ORDER BY channel_version_id COLLATE "C", credential_id COLLATE "C" LIMIT 128`) {
			return nil, errors.New("fixture tuple query shape")
		}
	} else if len(args) != 2 || !strings.Contains(sql, `COLLATE "C" LIMIT 128`) {
		return nil, errors.New("fixture single cursor query shape")
	}
	if table == q.queryFailure {
		return nil, errors.New("private query detail")
	}

	hasCursor, ok := args[0].(bool)
	if !ok {
		return nil, errors.New("fixture cursor mode")
	}
	var page [][]any
	for _, row := range q.tables[table] {
		key := row[0].(string)
		if table == "channel_version_credentials" {
			version, credential := row[0].(string), row[2].(string)
			priorVersion, priorCredential := args[1].(string), args[2].(string)
			if hasCursor && !procurementAttemptTupleGreater(version, credential, priorVersion, priorCredential) {
				continue
			}
		} else {
			prior, ok := args[1].(string)
			if !ok {
				return nil, errors.New("fixture cursor key")
			}
			if hasCursor && key <= prior {
				continue
			}
		}
		page = append(page, row)
		if len(page) == fundingSourcePageSize {
			break
		}
	}
	q.calls[table]++
	if q.queries == nil {
		q.queries = map[string][]string{}
	}
	q.queries[table] = append(q.queries[table], sql)
	if q.calls[table] == 1 {
		q.firstArgs[table] = append([]any(nil), args...)
	}
	q.lastArgs[table] = append([]any(nil), args...)
	rows := &procurementAttemptRowsFixture{values: page, index: -1}
	if q.onRowsClose != nil {
		rows.onClose = func() { q.onRowsClose(table) }
	}
	if table == q.rowsFailure {
		rows.rowsErr = errors.New("private Rows detail")
	}
	if table == q.nextFailure {
		rows.nextErr = errors.New("private Next detail")
	}
	if table == q.scanFailure {
		rows.scanErr = errors.New("private Scan detail")
	}
	if table == q.closeFailure {
		rows.closeErr = errors.New("private Close detail")
	}
	return rows, nil
}

func procurementAttemptString(value string) *string { return &value }

func newProcurementAttemptQueryFixture() *procurementAttemptQueryFixture {
	created := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	return &procurementAttemptQueryFixture{
		tables: map[string][][]any{
			"channels":                        {{"channel-1"}},
			"channel_credentials":             {{"credential-1", "channel-1"}},
			"channel_versions":                {{"version-1", "channel-1", "credential-1"}},
			"channel_version_credentials":     {{"version-1", "channel-1", "credential-1", int32(1), int32(1), int32(1000)}},
			"tenants":                         {{"tenant-1"}},
			"request_records":                 {{"request-1", "tenant-1", "100", "50"}},
			"cost_versions":                   {{"cost-1", "channel-1", "upstream-model", procurementAttemptString("1000000"), procurementAttemptString("2000000"), "contract", created.Add(-time.Hour), created.Add(-2 * time.Hour)}},
			"procurement_accounts":            {{"account-1", "200", "0"}},
			"credential_procurement_bindings": {{"binding-1", "credential-1", "channel-1", "account-1", "00000000-0000-4000-8000-000000000001", created.Add(-time.Minute)}},
			"upstream_attempts":               {{"attempt-1", "tenant-1", "request-1", int32(1), "channel-1", "version-1", "credential-1", "upstream-model", "prepared", procurementAttemptString("cost-1"), procurementAttemptString("1000000"), procurementAttemptString("2000000"), "contract", "unknown", nil, created}},
			"procurement_reservations":        {{"attempt-1", "account-1", "binding-1", "100", "50", "200", "200", "0", "prepared", false}},
			"procurement_budget_events":       {{"event-1", "reserve:attempt-1", "account-1", "attempt-1", "200", "0", "reserved", ""}},
			"users":                           {{"checker"}, {"maker"}},
		},
		calls: map[string]int{}, firstArgs: map[string][]any{}, lastArgs: map[string][]any{},
	}
}

func TestReadProcurementAttemptBindingFactsScansNineteenFixedRelations(t *testing.T) {
	query := newProcurementAttemptQueryFixture()
	facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatal(err)
	}
	if budget := facts.BudgetSummary(); !budget.Balanced || budget.Scope != "procurement_budget_projection_v1" || budget.Scanned.Accounts != 1 || budget.Scanned.Reservations != 1 || budget.Scanned.Events != 1 {
		t.Fatalf("unexpected budget summary: %+v", budget)
	}
	if binding := facts.BindingSummary(); !binding.Balanced || binding.Scope != "procurement_attempt_binding_graph_v1" || binding.Scanned.Channels != 1 || binding.Scanned.Bindings != 1 || binding.Scanned.Attempts != 1 || binding.Scanned.Reservations != 1 {
		t.Fatalf("unexpected attempt binding summary: %+v", binding)
	}
	for _, table := range procurementAttemptReadTables {
		if query.calls[table] != 1 || len(query.queries[table]) != 1 {
			t.Fatalf("table %s not completely read once: calls=%d", table, query.calls[table])
		}
	}
	if approval := facts.ApprovalSummary(); !approval.Balanced || approval.Scope != "procurement_cost_approval_graph_v1" || approval.Scanned.Users != 2 {
		t.Fatalf("unexpected approval summary: %+v", approval)
	}
	if command := facts.CommandSummary(); command.Scope != "procurement_command_observation_graph_v1" || command.Scanned.Bindings != 1 || command.Mismatches.BindingCommandRows != 1 {
		t.Fatalf("missing original binding command was not retained: %+v", command)
	}
	if journal := facts.JournalSummary(); journal.Scope != "procurement_cost_journal_graph_v1" || !journal.Balanced || journal.Scanned.Reservations != 1 {
		t.Fatalf("unexpected cost journal summary: %+v", journal)
	}
	wantSelects := map[string]string{
		"procurement_commands":             `SELECT operation_id,actor,kind,target_id,input_digest,status,reason,before_state::pg_catalog.text,after_state::pg_catalog.text,created_at FROM "private_schema"."procurement_commands"`,
		"procurement_balance_observations": `SELECT id,account_id,operation_id,actor,status,amount::pg_catalog.text,currency,source,semantic,observed_at,provider_as_of,evidence_ref,created_at FROM "private_schema"."procurement_balance_observations"`,
		"audit_events":                     `SELECT id,actor,tenant_scope,action,target,sanitized_diff::pg_catalog.text,request_id,created_at FROM "private_schema"."audit_events"`,
		"projection_outbox":                `SELECT event_id,tenant_id,operation_id,kind,schema_version,sanitized_payload::pg_catalog.text,created_at FROM "private_schema"."projection_outbox"`,
		"users":                            `SELECT id FROM "private_schema"."users"`,
		"provider_cost_corrections":        `SELECT id,tenant_id,attempt_id,operation_id,amount::pg_catalog.text,basis,evidence_ref,created_at FROM "private_schema"."provider_cost_corrections"`,
		"procurement_cost_proposals":       `SELECT id,tenant_id,attempt_id,amount::pg_catalog.text,basis,evidence_ref,reason,requester,expected_reservation_revision::pg_catalog.text,expected_evidence_count::pg_catalog.text,state,approver,approval_operation_id,created_at,approved_at FROM "private_schema"."procurement_cost_proposals"`,
		"channels":                         `SELECT id FROM "private_schema"."channels"`,
		"channel_credentials":              `SELECT id,channel_id FROM "private_schema"."channel_credentials"`,
		"channel_versions":                 `SELECT id,channel_id,credential_id FROM "private_schema"."channel_versions"`,
		"channel_version_credentials":      `SELECT channel_version_id,channel_id,credential_id,position,weight,max_inflight FROM "private_schema"."channel_version_credentials"`,
		"tenants":                          `SELECT id FROM "private_schema"."tenants"`,
		"request_records":                  `SELECT id,tenant_id,input_upper_bound::pg_catalog.text,output_limit::pg_catalog.text FROM "private_schema"."request_records"`,
		"cost_versions":                    `SELECT id,channel_id,upstream_model,input_rate::pg_catalog.text,output_rate::pg_catalog.text,basis,effective_at,created_at FROM "private_schema"."cost_versions"`,
		"procurement_accounts":             `SELECT id,held_amount::pg_catalog.text,spent_amount::pg_catalog.text FROM "private_schema"."procurement_accounts"`,
		"credential_procurement_bindings":  `SELECT id,credential_id,channel_id,account_id,operation_id,bound_at FROM "private_schema"."credential_procurement_bindings"`,
		"upstream_attempts":                `SELECT id,tenant_id,request_id,sequence,channel_id,channel_version_id,credential_id,upstream_model,delivery_state,cost_version_id,cost_input_rate::pg_catalog.text,cost_output_rate::pg_catalog.text,cost_basis,cost_status,cost_amount::pg_catalog.text,created_at FROM "private_schema"."upstream_attempts"`,
		"procurement_reservations":         `SELECT attempt_id,account_id,binding_id,input_bound::pg_catalog.text,output_bound::pg_catalog.text,upper_amount::pg_catalog.text,held_amount::pg_catalog.text,spent_amount::pg_catalog.text,state,anomaly FROM "private_schema"."procurement_reservations"`,
		"procurement_budget_events":        `SELECT id,event_key,account_id,attempt_id,held_delta::pg_catalog.text,spent_delta::pg_catalog.text,type,evidence_ref FROM "private_schema"."procurement_budget_events"`,
	}
	for table, prefix := range wantSelects {
		if !strings.HasPrefix(query.queries[table][0], prefix) {
			t.Fatalf("fixed projection changed for %s: %s", table, query.queries[table][0])
		}
	}
	poolSQL := query.queries["channel_version_credentials"][0]
	if strings.Contains(poolSQL, "label") || !strings.Contains(poolSQL, `WHERE NOT $1::pg_catalog.bool OR (channel_version_id COLLATE "C", credential_id COLLATE "C") > ($2::pg_catalog.text COLLATE "C", $3::pg_catalog.text COLLATE "C")`) {
		t.Fatal("pool projection or tuple cursor changed", poolSQL)
	}
	for _, result := range []ProcurementAttemptBindingFacts{facts, {}} {
		encoded, err := result.MarshalJSON()
		if err != nil || strings.Contains(string(encoded), "attempt-1") || !strings.Contains(string(encoded), "redacted") {
			t.Fatal("fact wrapper leaked raw data", err, string(encoded))
		}
	}
}

func TestReadProcurementAttemptBindingFactsDataMismatchIsSuccessfulScan(t *testing.T) {
	query := newProcurementAttemptQueryFixture()
	query.tables["upstream_attempts"][0][5] = "other-version"
	facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != nil {
		t.Fatalf("data mismatch must not abort a complete scan: %v", err)
	}
	if facts.BindingSummary().Balanced {
		t.Fatal("mismatched attempt/version was reported balanced")
	}
}

func TestReadProcurementHistoryFactsPreservesNullableObservationAndAudit(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known=%t", known), func(t *testing.T) {
			query := newProcurementAttemptQueryFixture()
			created := query.tables["upstream_attempts"][0][15].(time.Time)
			binding := query.tables["credential_procurement_bindings"][0]
			bindingAfter, err := json.Marshal(map[string]string{
				"id": binding[0].(string), "credential_id": binding[1].(string), "channel_id": binding[2].(string),
				"account_id": binding[3].(string), "operation_id": binding[4].(string), "bound_at": binding[5].(time.Time).Format(time.RFC3339Nano), "channel_revision": "1",
			})
			if err != nil {
				t.Fatal(err)
			}
			operation := "00000000-0000-4000-8000-000000000002"
			status := "unknown"
			var amount *string
			var providerTime *time.Time
			if known {
				status, amount, providerTime = "known", procurementAttemptString("900"), &created
			}
			observationAfter, err := json.Marshal(map[string]any{
				"id": "observation-1", "account_id": "account-1", "operation_id": operation, "actor": "maker", "status": status,
				"amount": amount, "currency": "CNY", "source": "manual", "semantic": "balance", "observed_at": created.Format(time.RFC3339Nano),
				"provider_as_of": providerTime, "evidence_ref": "balance:receipt", "created_at": created.Format(time.RFC3339Nano),
			})
			if err != nil {
				t.Fatal(err)
			}
			query.tables["procurement_commands"] = [][]any{
				{binding[4], "maker", "credential_bind", binding[1], strings.Repeat("a", 64), "applied", "", "null", string(bindingAfter), created},
				{operation, "maker", "balance_observe", "account-1", strings.Repeat("b", 64), "applied", "", "null", string(observationAfter), created},
			}
			query.tables["procurement_balance_observations"] = [][]any{
				{"observation-1", "account-1", operation, "maker", status, amount, "CNY", "manual", "balance", created, providerTime, "balance:receipt", created},
			}
			query.tables["audit_events"] = [][]any{
				{"audit-1", "maker", nil, "procurement.command.applied", binding[4], `{"kind":"credential_bind","status":"applied"}`, "", created},
				{"audit-2", "maker", nil, "procurement.command.applied", operation, `{"kind":"balance_observe","status":"applied"}`, "", created},
			}
			facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
			if err != nil || !facts.CommandSummary().Balanced || !facts.JournalSummary().Balanced {
				t.Fatalf("history mapping mismatch: err=%v command=%+v journal=%+v", err, facts.CommandSummary(), facts.JournalSummary())
			}
			if facts.CommandSummary().Scanned.Observations != 1 || facts.JournalSummary().Scanned.Audits != 2 || query.calls["audit_events"] != 1 {
				t.Fatal("audit was not read once and shared with both cores")
			}
			query.tables["procurement_balance_observations"][0][11] = "changed:receipt"
			facts, err = ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
			if err != nil || facts.CommandSummary().Mismatches.AppliedObservationRows != 1 || facts.CommandSummary().Mismatches.ObservationCommandRows != 1 {
				t.Fatal("observation snapshot mismatch was not detected in both directions", err, facts.CommandSummary())
			}
		})
	}
}

func TestReadProcurementAttemptBindingFactsPoolTupleCursor(t *testing.T) {
	query := newProcurementAttemptQueryFixture()
	query.tables["channel_version_credentials"] = make([][]any, fundingSourcePageSize+1)
	for i := range query.tables["channel_version_credentials"] {
		query.tables["channel_version_credentials"][i] = []any{"version-1", "channel-1", fmt.Sprintf("credential-%03d", i), int32(1), int32(1), int32(10)}
	}
	query.tables["channel_version_credentials"][0][2] = "credential-000"
	query.tables["channel_version_credentials"][fundingSourcePageSize][2] = "credential-128"
	if _, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema"); err != nil {
		t.Fatal("tuple cursor read failed", err)
	}
	queries := query.queries["channel_version_credentials"]
	if len(queries) != 2 || !strings.Contains(queries[1], `ORDER BY channel_version_id COLLATE "C", credential_id COLLATE "C" LIMIT 128`) {
		t.Fatal("pool scan did not continue the composite keyset", queries)
	}
	if args := query.firstArgs["channel_version_credentials"]; len(args) != 3 || args[0] != false || args[1] != "" || args[2] != "" {
		t.Fatal("pool scan did not use separate empty initial tuple components", args)
	}
	if q := query.lastArgs["channel_version_credentials"]; len(q) != 3 || q[0] != true || q[1] != "version-1" || q[2] != "credential-127" {
		t.Fatal("pool continuation did not use the last tuple as two cursor parameters", q)
	}
}

func procurementAttemptReadRow(table, key string, index int) []any {
	created := time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)
	operationID := fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1)
	switch table {
	case "channels":
		return []any{key}
	case "channel_credentials":
		return []any{key, "channel"}
	case "channel_versions":
		return []any{key, "channel", "credential"}
	case "tenants":
		return []any{key}
	case "request_records":
		return []any{key, "tenant", "0", "0"}
	case "cost_versions":
		return []any{key, "channel", "model", nil, nil, "unknown", created, created}
	case "procurement_accounts":
		return []any{key, "0", "0"}
	case "credential_procurement_bindings":
		return []any{key, "credential", "channel", "account", operationID, created}
	case "upstream_attempts":
		return []any{key, "tenant", "request", int32(1), "channel", "version", "credential", "model", "prepared", nil, nil, nil, "unknown", "unknown", nil, created}
	case "procurement_reservations":
		return []any{key, "account", "binding", "1", "0", "0", "0", "0", "released", false}
	case "procurement_budget_events":
		return []any{key, key, "account", "attempt", "0", "0", "reserved", ""}
	case "procurement_commands":
		return []any{key, "maker", "account_configure", "account", strings.Repeat("a", 64), "rejected", "not_found", "null", "null", created}
	case "procurement_balance_observations":
		return []any{key, "account", operationID, "maker", "unknown", nil, "CNY", "manual", "provider balance", created, nil, "reference", created}
	case "audit_events":
		return []any{key, "maker", nil, "other.action", "target", "{}", "", created}
	case "projection_outbox":
		return []any{key, "tenant", operationID, "other_kind", int32(1), "{}", created}
	case "users":
		return []any{key}
	case "provider_cost_corrections":
		return []any{key, "tenant", "attempt", operationID, "0", "contract", "reference", created}
	case "procurement_cost_proposals":
		return []any{key, "tenant", "attempt", "0", "contract", "reference", "reason", "maker", "1", "0", "proposed", nil, nil, created, nil}
	default:
		panic("unknown fixture relation: " + table)
	}
}

func procurementAttemptScannedCount(facts ProcurementAttemptBindingFacts, table string) uint64 {
	scanned := facts.BindingSummary().Scanned
	approval := facts.ApprovalSummary().Scanned
	switch table {
	case "procurement_commands":
		return facts.CommandSummary().Scanned.Commands
	case "procurement_balance_observations":
		return facts.CommandSummary().Scanned.Observations
	case "audit_events":
		return facts.CommandSummary().Scanned.Audits
	case "projection_outbox":
		return facts.JournalSummary().Scanned.Outbox
	case "users":
		return approval.Users
	case "provider_cost_corrections":
		return approval.Corrections
	case "procurement_cost_proposals":
		return approval.Proposals
	case "channels":
		return scanned.Channels
	case "channel_credentials":
		return scanned.Credentials
	case "channel_versions":
		return scanned.Versions
	case "channel_version_credentials":
		return scanned.PoolMemberships
	case "tenants":
		return scanned.Tenants
	case "request_records":
		return scanned.Requests
	case "cost_versions":
		return scanned.CostVersions
	case "procurement_accounts":
		return scanned.Accounts
	case "credential_procurement_bindings":
		return scanned.Bindings
	case "upstream_attempts":
		return scanned.Attempts
	case "procurement_reservations":
		return scanned.Reservations
	case "procurement_budget_events":
		return scanned.BudgetEvents
	default:
		panic("unknown fixture relation: " + table)
	}
}

func TestReadProcurementAttemptBindingFactsSingleKeyPaginationBoundaries(t *testing.T) {
	for _, table := range procurementAttemptReadTables {
		if table == "channel_version_credentials" {
			continue
		}
		for _, count := range []int{127, 128, 129} {
			t.Run(fmt.Sprintf("%s/%d", table, count), func(t *testing.T) {
				query := newProcurementAttemptQueryFixture()
				for _, name := range procurementAttemptReadTables {
					query.tables[name] = nil
				}
				for i := 0; i < count; i++ {
					query.tables[table] = append(query.tables[table], procurementAttemptReadRow(table, fmt.Sprintf("key-%03d", i), i))
				}
				facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
				if err != nil {
					t.Fatal(err)
				}
				if got := procurementAttemptScannedCount(facts, table); got != uint64(count) {
					t.Fatalf("incomplete scan: got %d want %d", got, count)
				}
				wantCalls := 1
				if count >= fundingSourcePageSize {
					wantCalls = 2
				}
				if query.calls[table] != wantCalls {
					t.Fatalf("wrong page count %d, want %d", query.calls[table], wantCalls)
				}
				args := query.firstArgs[table]
				if len(args) != 2 || args[0] != false || args[1] != "" {
					t.Fatalf("first keyset page did not use explicit empty cursor: %#v", args)
				}
			})
		}
	}
}

func TestReadProcurementAttemptBindingFactsReturnsNoPartialFactsOnReaderFailures(t *testing.T) {
	setters := []struct {
		name string
		set  func(*procurementAttemptQueryFixture, string)
	}{
		{"query", func(q *procurementAttemptQueryFixture, table string) { q.queryFailure = table }},
		{"next", func(q *procurementAttemptQueryFixture, table string) { q.nextFailure = table }},
		{"scan", func(q *procurementAttemptQueryFixture, table string) { q.scanFailure = table }},
		{"rows", func(q *procurementAttemptQueryFixture, table string) { q.rowsFailure = table }},
		{"close", func(q *procurementAttemptQueryFixture, table string) { q.closeFailure = table }},
	}
	for _, failure := range setters {
		for _, table := range procurementAttemptReadTables {
			t.Run(failure.name+"/"+table, func(t *testing.T) {
				query := newProcurementAttemptQueryFixture()
				if len(query.tables[table]) == 0 {
					query.tables[table] = [][]any{procurementAttemptReadRow(table, "failure-key", 0)}
				}
				failure.set(query, table)
				facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
				if err != ErrProcurementAttemptBindingFactsExecution || facts != (ProcurementAttemptBindingFacts{}) {
					t.Fatalf("failed reader leaked facts: err=%v facts=%+v", err, facts)
				}
			})
		}
	}
}

func TestReadProcurementAttemptBindingFactsUsesOneSharedBudgetAndRejectsCursorFailures(t *testing.T) {
	query := newProcurementAttemptQueryFixture()
	for _, table := range procurementAttemptReadTables {
		query.tables[table] = nil
	}
	query.tables["channels"] = make([][]any, fundingSourceReadRowLimit-1)
	for i := range query.tables["channels"] {
		query.tables["channels"][i] = []any{fmt.Sprintf("channel-%06d", i)}
	}
	query.tables["projection_outbox"] = [][]any{procurementAttemptReadRow("projection_outbox", "outbox-a", 0), procurementAttemptReadRow("projection_outbox", "outbox-b", 1)}
	facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementAttemptBindingFactsResource || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatalf("cross-table row budget was not shared: err=%v", err)
	}

	query = newProcurementAttemptQueryFixture()
	query.tables["channels"] = [][]any{{"z"}, {"a"}}
	facts, err = ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementAttemptBindingFactsExecution || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatalf("non-increasing cursor leaked facts: err=%v", err)
	}
}

func TestReadProcurementAttemptBindingFactsCancellationAndInputs(t *testing.T) {
	if facts, err := ReadProcurementAttemptBindingFacts(context.Background(), nil, "private_schema"); err != ErrProcurementAttemptBindingFactsInput || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatal("nil query accepted")
	}
	if facts, err := ReadProcurementAttemptBindingFacts(context.Background(), newProcurementAttemptQueryFixture(), "pg_catalog"); err != ErrProcurementAttemptBindingFactsInput || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatal("system schema accepted")
	}
	if facts, err := ReadProcurementAttemptBindingFacts(nil, newProcurementAttemptQueryFixture(), "private_schema"); err != ErrProcurementAttemptBindingFactsInput || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	query := newProcurementAttemptQueryFixture()
	query.onRowsClose = func(table string) {
		if table == "channels" {
			cancel()
		}
	}
	facts, err := ReadProcurementAttemptBindingFacts(ctx, query, "private_schema")
	if err != ErrProcurementAttemptBindingFactsCanceled || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatalf("cancelled scan leaked facts: err=%v", err)
	}
	cancel()
	deadline, cancelDeadline := context.WithTimeout(context.Background(), 0)
	defer cancelDeadline()
	facts, err = ReadProcurementAttemptBindingFacts(deadline, newProcurementAttemptQueryFixture(), "private_schema")
	if err != ErrProcurementAttemptBindingFactsTimeout || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatalf("deadline scan leaked facts: err=%v", err)
	}
	for _, schema := range []string{"", "information_schema", "pg_private", "bad\x00schema"} {
		if facts, err = ReadProcurementAttemptBindingFacts(context.Background(), newProcurementAttemptQueryFixture(), schema); err != ErrProcurementAttemptBindingFactsInput || facts != (ProcurementAttemptBindingFacts{}) {
			t.Fatal("invalid schema accepted", schema, err)
		}
	}
}

func TestReadProcurementAttemptBindingFactsEnforcesSharedTextBudget(t *testing.T) {
	query := newProcurementAttemptQueryFixture()
	for _, table := range procurementAttemptReadTables {
		query.tables[table] = nil
	}
	query.tables["channels"] = [][]any{{strings.Repeat("c", fundingSourceReadByteLimit)}}
	facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != nil || facts.BindingSummary().Scanned.Channels != 1 {
		t.Fatalf("exact projection text budget should be accepted: err=%v", err)
	}
	query = newProcurementAttemptQueryFixture()
	for _, table := range procurementAttemptReadTables {
		query.tables[table] = nil
	}
	query.tables["channels"] = [][]any{{strings.Repeat("c", fundingSourceReadByteLimit-1)}}
	query.tables["projection_outbox"] = [][]any{procurementAttemptReadRow("projection_outbox", "outbox-a", 0)}
	facts, err = ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
	if err != ErrProcurementAttemptBindingFactsResource || facts != (ProcurementAttemptBindingFacts{}) {
		t.Fatalf("cross-table projection text budget was not shared: err=%v", err)
	}
}

func TestReadProcurementAttemptBindingFactsRejectsInvalidDatabaseText(t *testing.T) {
	for _, raw := range []string{"invalid-\xff", "invalid-\x00"} {
		query := newProcurementAttemptQueryFixture()
		query.tables["channels"] = [][]any{{raw}}
		facts, err := ReadProcurementAttemptBindingFacts(context.Background(), query, "private_schema")
		if err != ErrProcurementAttemptBindingFactsExecution || facts != (ProcurementAttemptBindingFacts{}) {
			t.Fatalf("invalid database text was not rejected atomically: err=%v", err)
		}
	}
}

func TestProcurementAttemptBindingFactsWrapperDoesNotExposeChecks(t *testing.T) {
	facts := ProcurementAttemptBindingFacts{budget: fundinglotverify.ProcurementBudgetProjectionCheck{Scope: "private"}}
	encoded, err := facts.MarshalJSON()
	if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "procurement_budget_projection") {
		t.Fatal("wrapper exposed summary data", err, string(encoded))
	}
}
