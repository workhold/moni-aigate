package periodicbudget

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type verifierTestRow struct {
	values []any
	err    error
}

func (r verifierTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("test scalar arity")
	}
	for i, v := range r.values {
		d := reflect.ValueOf(dest[i]).Elem()
		if v == nil {
			d.SetZero()
		} else {
			d.Set(reflect.ValueOf(v))
		}
	}
	return nil
}

type verifierTestRows struct {
	values                              [][]any
	index                               int
	nextErr, scanErr, rowsErr, closeErr error
	closed                              int
	afterScan                           func()
}

func (r *verifierTestRows) Next() (bool, error) {
	if r.nextErr != nil {
		return false, r.nextErr
	}
	if r.index == len(r.values) {
		return false, nil
	}
	r.index++
	return true, nil
}
func (r *verifierTestRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	err := (verifierTestRow{values: r.values[r.index-1]}).Scan(dest...)
	if r.afterScan != nil {
		r.afterScan()
	}
	return err
}
func (r *verifierTestRows) Err() error   { return r.rowsErr }
func (r *verifierTestRows) Close() error { r.closed++; return r.closeErr }

type verifierTestQuery struct {
	rows       []*verifierTestRow
	operations *verifierTestRows
	sql        []string
	checkErr   error
	index      int
}

func (q *verifierTestQuery) Check() error { return q.checkErr }
func (q *verifierTestQuery) QueryRow(sql string, args ...any) verifierRow {
	q.sql = append(q.sql, sql)
	if len(args) > 0 || q.index >= len(q.rows) {
		return verifierTestRow{err: errors.New("unexpected test scalar")}
	}
	r := q.rows[q.index]
	q.index++
	return r
}
func (q *verifierTestQuery) Query(sql string, args ...any) (verifierRows, error) {
	q.sql = append(q.sql, sql)
	if len(args) > 0 || q.operations == nil {
		return nil, errors.New("unexpected test rows")
	}
	return q.operations, nil
}
func verifierTestZeros(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = int64(0)
	}
	return out
}
func verifierTestCoreQuery() *verifierTestQuery {
	q := &verifierTestQuery{operations: &verifierTestRows{}}
	q.rows = append(q.rows, &verifierTestRow{values: []any{time.Date(2026, 11, 2, 3, 4, 5, 123456000, time.UTC)}})
	for _, n := range []int{10, 6, 4, 3, 3, 2, 1} {
		q.rows = append(q.rows, &verifierTestRow{values: verifierTestZeros(n)})
	}
	return q
}
func verifierTestOperation(t *testing.T) verifierOperation {
	t.Helper()
	p := func(s string) *string { return &s }
	enabled := true
	o := verifierOperation{id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", kind: "tenant_periodic_budget.configure", actor: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", scope: "platform", target: "/api/platform/tenants/cccccccc-cccc-4ccc-8ccc-cccccccccccc/periodic-budget", resource: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", revision: "1", at: "2026-11-01T00:00:00.000000Z", control: p("dddddddd-dddd-4ddd-8ddd-dddddddddddd"), controlScope: p("tenant"), scopeID: p("cccccccc-cccc-4ccc-8ccc-cccccccccccc"), period: p("utc_month"), tracking: p("2026-11-01T00:00:00.000000Z"), effective: p("2026-12-01T00:00:00.000000Z"), version: p("1"), versionPeriod: p("utc_month"), versionLimit: p("100"), versionEnabled: &enabled, changed: true, actorExists: true, zeroHeld: false}
	o.input = []byte(`{"expected_revision":"0","period_kind":"utc_month","enabled":true,"limit_amount":"100"}`)
	var err error
	o.fingerprint, err = Fingerprint(o.kind, o.actor, o.scope, o.target, PutPeriodicBudget{o.id, "0", "utc_month", true, "100"})
	if err != nil {
		t.Fatal(err)
	}
	o.snapshot = []byte(`{"scope":"tenant","scope_id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","revision":"1","period_kind":"utc_month","enabled":true,"limit_amount":"100","state":"scheduled","tracking_started_at":"2026-11-01T00:00:00.000000Z","effective_from":"2026-12-01T00:00:00.000000Z","current_window":null,"carry_held":"0","as_of":"2026-11-01T00:00:00.000000Z"}`)
	return o
}
func verifierTestOperationValues(o verifierOperation) []any {
	return []any{o.id, o.kind, o.actor, o.scope, o.target, o.resource, o.revision, o.fingerprint, o.input, o.snapshot, o.at, o.control, o.controlScope, o.scopeID, o.tenant, o.period, o.tracking, o.effective, o.version, o.versionPeriod, o.versionEnabled, o.versionLimit, o.changed, o.actorExists, o.window, o.windowStart, o.windowEnd, o.zeroHeld}
}

func TestVerifierMaintenanceCorePreservesThreeStatesAndDedup(t *testing.T) {
	for _, state := range []VerificationStatus{VerificationBalanced, VerificationIndeterminate, VerificationMismatch} {
		t.Run(string(state), func(t *testing.T) {
			q := verifierTestCoreQuery()
			for i := range q.rows[1].values {
				q.rows[1].values[i] = int64(i + 1)
			}
			if state != VerificationBalanced {
				q.rows[2].values[5] = int64(2)
				q.rows[5].values[2] = int64(3)
				o := verifierTestOperation(t)
				v := verifierTestOperationValues(o)
				q.operations.values = [][]any{v, v}
			}
			if state == VerificationMismatch {
				q.rows[3].values[1] = int64(7)
			}
			got, err := readVerification(q, verifierRenderer("frozen_schema"))
			if err != nil || got.Status != state || got.Balanced != (state == VerificationBalanced) || got.CoverageComplete != (state == VerificationBalanced) {
				t.Fatal("three-state report changed", got, err)
			}
			if got.AsOf != "2026-11-02T03:04:05.123456Z" || got.SchemaVersion != 1 || got.Scanned.Controls != 1 || got.Scanned.Audits != 10 {
				t.Fatal("scan/time protocol changed", got)
			}
			if state != VerificationBalanced && (got.Unproven.LegacyMembershipUnproven != 2 || got.Unproven.LegacyEffectBoundaryUnproven != 3 || got.Unproven.OperationSnapshotPrefixUnproven != 1) {
				t.Fatal("historical gaps or operation dedup lost", got)
			}
			if state == VerificationMismatch && got.Mismatches.OrphanEffects != 7 {
				t.Fatal("finding mapping changed", got)
			}
			if len(q.sql) != 9 || q.operations.closed != 1 || q.index != 8 {
				t.Fatal("incomplete query/EOF/close")
			}
			if q.sql[2] != verifierRenderer("frozen_schema")(verifierSourcesSQL) || q.sql[8] != verifierRenderer("frozen_schema")(verifierOperationsSQL) {
				t.Fatal("independent SQL changed")
			}
		})
	}
}

func TestVerifierMaintenanceCoreFailureNeverReturnsPartialReport(t *testing.T) {
	fault := errors.New("private-driver-detail")
	for _, stage := range []string{"scalar", "next", "scan", "rows-error", "close", "cpu-check"} {
		t.Run(stage, func(t *testing.T) {
			q := verifierTestCoreQuery()
			o := verifierTestOperation(t)
			q.operations.values = [][]any{verifierTestOperationValues(o)}
			switch stage {
			case "scalar":
				q.rows[4].err = fault
			case "next":
				q.operations.nextErr = fault
			case "scan":
				q.operations.scanErr = fault
			case "rows-error":
				q.operations.rowsErr = fault
			case "close":
				q.operations.closeErr = fault
			case "cpu-check":
				q.operations.afterScan = func() { q.checkErr = fault }
			}
			got, err := readVerification(q, verifierRenderer("fixed"))
			if !errors.Is(err, fault) || got != (Verification{}) {
				t.Fatal("failed core returned partial result", got, err)
			}
			if stage != "scalar" && q.operations.closed != 1 {
				t.Fatal("query ownership not closed")
			}
		})
	}
}

func TestVerifierMaintenanceGuardCoversWholeFrozenInventory(t *testing.T) {
	if len(verifierTables) != 19 || len(verifierColumns) != 19 {
		t.Fatal("frozen relation count changed")
	}
	fields := 0
	rendered := verifierRenderer(`schema"quoted`)(maintenanceVerifierResourcesSQL)
	for alias, table := range verifierTables {
		if !strings.Contains(maintenanceVerifierResourcesSQL, "FROM @"+alias+"@ AS source") {
			t.Fatal("relation omitted", table)
		}
		for _, column := range strings.Fields(verifierColumns[alias]) {
			fields++
			if !strings.Contains(maintenanceVerifierResourcesSQL, `source."`+column+`"::pg_catalog.text,'UTF8'`) {
				t.Fatal("declared field omitted", table, column)
			}
		}
	}
	if fields != 146 || strings.Count(maintenanceVerifierResourcesSQL, " UNION ALL ") != 18 || strings.Contains(maintenanceVerifierResourcesSQL, " WHERE ") || strings.Contains(maintenanceVerifierResourcesSQL, " LIMIT ") {
		t.Fatal("guard narrowed full inventory")
	}
	if strings.Contains(rendered, "@") || !strings.Contains(rendered, `"schema""quoted"."audit_events"`) {
		t.Fatal("unsafe schema rendering")
	}
	for _, bound := range []string{fmt.Sprint(maintenanceVerifierMaxRows), fmt.Sprint(maintenanceVerifierMaxFieldBytes), fmt.Sprint(maintenanceVerifierMaxBytes)} {
		if !strings.Contains(maintenanceVerifierResourcesSQL, "<="+bound) {
			t.Fatal("bound missing", bound)
		}
	}
	for _, eligible := range []bool{false, true} {
		q := &verifierTestQuery{rows: []*verifierTestRow{{values: []any{eligible}}}}
		err := verifyMaintenanceResources(q, verifierRenderer("fixed"))
		if eligible && err != nil || !eligible && !errors.Is(err, ErrVerifyExecution) || len(q.sql) != 1 || q.operations != nil {
			t.Fatal("resource classification or partial scan", err)
		}
	}
}

func TestReadBoundedVerificationRowsRejectsInvalidInputBeforeQuery(t *testing.T) {
	if got, err := ReadBoundedVerificationRows(nil, "fixed"); !errors.Is(err, ErrVerifyInput) || got != (Verification{}) {
		t.Fatal("nil raw reader accepted", got, err)
	}
	for _, schema := range []string{"", "pg_catalog", "pg_temp", "pg_temp_1", "bad\x00schema"} {
		q := verifierTestCoreQuery()
		if got, err := ReadBoundedVerificationRows(q, schema); !errors.Is(err, ErrVerifySchema) || got != (Verification{}) || len(q.sql) != 0 {
			t.Fatal("invalid raw schema accepted or queried", schema, got, err)
		}
	}
}

func TestReadBoundedVerificationRowsGuardsBeforeOriginalCore(t *testing.T) {
	for _, eligible := range []bool{false, true} {
		q := verifierTestCoreQuery()
		q.rows = append([]*verifierTestRow{{values: []any{eligible}}}, q.rows...)
		got, err := ReadBoundedVerificationRows(q, `schema"quoted`)
		if !eligible {
			if !errors.Is(err, ErrVerifyExecution) || got != (Verification{}) || len(q.sql) != 1 || q.operations.closed != 0 {
				t.Fatal("ineligible raw facts reached core or returned result", got, err)
			}
			continue
		}
		if err != nil || got.SchemaVersion != 1 || !got.Balanced || len(q.sql) != 10 || q.index != 9 || q.operations.closed != 1 {
			t.Fatal("bounded raw entry changed original core", got, err)
		}
		if q.sql[0] != verifierRenderer(`schema"quoted`)(maintenanceVerifierResourcesSQL) || q.sql[1] != `SELECT pg_catalog.clock_timestamp()` {
			t.Fatal("resource guard did not precede original algorithm")
		}
	}
	fault := errors.New("opaque-raw-facts-driver-error")
	q := verifierTestCoreQuery()
	q.checkErr = fault
	if got, err := ReadBoundedVerificationRows(q, "fixed"); !errors.Is(err, fault) || got != (Verification{}) || len(q.sql) != 0 {
		t.Fatal("raw check failure lost or queried", got, err)
	}
	q = verifierTestCoreQuery()
	q.rows = append([]*verifierTestRow{{values: []any{true}}}, q.rows...)
	q.rows[4].err = fault
	if got, err := ReadBoundedVerificationRows(q, "fixed"); !errors.Is(err, fault) || got != (Verification{}) {
		t.Fatal("bounded raw core returned partial report", got, err)
	}
}
