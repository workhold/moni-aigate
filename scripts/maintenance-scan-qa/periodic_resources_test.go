package maintenancescanqa_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/maintenancescan"
	"github.com/moni-ai/moni-aigate/internal/maintenancetrust"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

// This inventory is the independent, frozen 19-relation/146-field test oracle.
// It does not import the production guard or discover fields from the catalog.
// Only scalar sizes leave PostgreSQL; no field values, credentials or DSN do.
var periodicResourceInventory = []struct{ table, columns string }{
	{"periodic_budget_controls", "id scope tenant_id key_id account_id period_kind enabled limit_amount revision tracking_started_at effective_from max_observed_window_start held_amount spent_amount event_seq last_event_id created_xid configuration_operation_id"},
	{"periodic_budget_windows", "id control_id start_at end_at held_amount spent_amount revision last_event_id"},
	{"periodic_budget_allocations", "id control_id subject_type subject_id origin origin_source_id window_id admitted_at upper_amount baseline source_held source_spent held_amount spent_amount state revision last_event_id created_xid created_at"},
	{"periodic_budget_events", "id control_id allocation_id source_kind source_id seq allocation_before_revision window_before_revision before_held before_spent after_held after_spent after_source_spent after_state held_delta spent_delta created_xid created_at"},
	{"periodic_budget_operations", "operation_id kind actor scope target resource_id revision safe_input input_fingerprint result_snapshot created_xid created_at"},
	{"periodic_budget_configuration_versions", "control_id revision operation_id period_kind enabled limit_amount actor created_xid created_at"},
	{"request_records", "id tenant_id key_id pb_created_xid pb_tenant_control pb_key_control pb_admitted_at"},
	{"reservations", "id tenant_id request_id amount status"},
	{"settlements", "id tenant_id request_id charged_amount status"},
	{"upstream_attempts", "id tenant_id request_id credential_id channel_id pb_created_xid"},
	{"procurement_accounts", "id held_amount spent_amount"},
	{"credential_procurement_bindings", "id account_id credential_id channel_id"},
	{"procurement_reservations", "attempt_id account_id binding_id upper_amount held_amount spent_amount state pb_created_xid pb_control pb_admitted_at"},
	{"procurement_budget_events", "id event_key account_id attempt_id held_delta spent_delta type pb_created_xid"},
	{"tenants", "id"},
	{"api_keys", "id tenant_id"},
	{"users", "id"},
	{"audit_events", "id actor tenant_scope action target sanitized_diff request_id created_at"},
	{"schema_migrations", "name checksum"},
}

const (
	periodicResourceFieldLimit int64 = 65536
	periodicResourceBytesLimit int64 = 16 << 20
	periodicResourceRowsLimit  int64 = 100000
	periodicResourceAction           = "qa.periodic_resource_probe"
	periodicResourceIDPrefix         = "pb_resource_"
)

var periodicResourceTime = time.Date(2026, 11, 2, 3, 4, 5, 0, time.UTC)

type periodicResourceTotals struct {
	rows, bytes, maxField     int64
	auditRows, auditBytes     int64
	historyRows, historyBytes int64
	configuredAudits          int64
}

func measurePeriodicResources(t *testing.T, ctx context.Context, owner *pgxpool.Pool, schema string) periodicResourceTotals {
	t.Helper()
	var total periodicResourceTotals
	fields := 0
	for _, relation := range periodicResourceInventory {
		var lengths []string
		for _, column := range strings.Fields(relation.columns) {
			fields++
			lengths = append(lengths, "coalesce(pg_catalog.octet_length(pg_catalog.convert_to(source."+pgx.Identifier{column}.Sanitize()+"::pg_catalog.text,'UTF8')),0)::pg_catalog.int8")
		}
		query := "SELECT pg_catalog.count(*)::pg_catalog.int8,coalesce(pg_catalog.sum(" + strings.Join(lengths, "+") + "),0)::pg_catalog.int8,coalesce(pg_catalog.max(greatest(" + strings.Join(lengths, ",") + ")),0)::pg_catalog.int8 FROM " + pgx.Identifier{schema, relation.table}.Sanitize() + " AS source"
		var rows, bytes, maxField int64
		if err := owner.QueryRow(ctx, query).Scan(&rows, &bytes, &maxField); err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) {
				t.Fatalf("independent resource inventory measurement failed: %s SQLSTATE=%s position=%d", relation.table, pg.Code, pg.Position)
			}
			t.Fatalf("independent resource inventory measurement failed: %s error_type=%T", relation.table, err)
		}
		total.rows += rows
		total.bytes += bytes
		if maxField > total.maxField {
			total.maxField = maxField
		}
		switch relation.table {
		case "audit_events":
			total.auditRows, total.auditBytes = rows, bytes
		case "schema_migrations":
			total.historyRows, total.historyBytes = rows, bytes
		}
	}
	if len(periodicResourceInventory) != 19 || fields != 146 || total.historyRows != 22 || total.historyBytes <= 0 || total.auditRows <= 0 || total.auditBytes <= 0 {
		t.Fatal("resource oracle must include all 19 relations, 146 fields, genuine complete migration history and audits")
	}
	query := "SELECT pg_catalog.count(*) FROM " + pgx.Identifier{schema, "audit_events"}.Sanitize() + " WHERE action='periodic_budget.configured'"
	if owner.QueryRow(ctx, query).Scan(&total.configuredAudits) != nil {
		t.Fatal("independent business audit count failed")
	}
	return total
}

func openPeriodicResourceOwner(t *testing.T, f *consumerFixture, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("resource original offline owner pool failed")
	}
	f.pools = append(f.pools, owner)
	var pid int32
	if owner.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid) != nil {
		t.Fatal("resource owner backend identity failed")
	}
	f.observePID(t, ctx, pid)
	return owner
}

// These are ordinary legal INSERTs through the original QA owner. The generic
// action has no financial effect and deliberately does not satisfy the PB
// business-audit predicate. No trigger, role, schema, pin or history is changed.
func insertPeriodicResourceActors(t *testing.T, ctx context.Context, owner *pgxpool.Pool, lengths []int32) {
	t.Helper()
	_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at)
 SELECT $1::text||pg_catalog.lpad(i::text,6,'0'),pg_catalog.repeat('x',n),NULL,$2::text,'','{}'::jsonb,'',$3::timestamptz
 FROM pg_catalog.unnest($4::int4[]) WITH ORDINALITY AS payload(n,i)`, periodicResourceIDPrefix, periodicResourceAction, periodicResourceTime, lengths)
	if err != nil {
		t.Fatal("legal generic resource audit insertion failed")
	}
}

func TestMaintenancePeriodicResourcesPostgresFieldUTF8Bytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{{"below", 65535}, {"at", 65536}, {"over", 65537}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			owner := openPeriodicResourceOwner(t, f, ctx)
			before := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			payload := strings.Repeat("界", int(tc.size/3)) + strings.Repeat("a", int(tc.size%3))
			if int64(len(payload)) != tc.size || int64(utf8.RuneCountInString(payload)) >= tc.size || before.maxField >= tc.size {
				t.Fatal("UTF8 resource fixture does not isolate the byte boundary")
			}
			_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at) VALUES ($1,$2,NULL,$3,'','{}'::jsonb,'',$4)`, periodicResourceIDPrefix+"000001", payload, periodicResourceAction, periodicResourceTime)
			if err != nil {
				t.Fatal("legal multibyte resource audit insertion failed")
			}
			totals := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			if totals.maxField != tc.size || totals.rows != before.rows+1 || totals.auditRows != before.auditRows+1 || totals.bytes >= periodicResourceBytesLimit || totals.rows >= periodicResourceRowsLimit {
				t.Fatal("field fixture crossed another resource boundary")
			}
			owner.Close()
			runPeriodicResourcePublicScan(t, f, totals, tc.size <= periodicResourceFieldLimit)
		})
	}
}

func TestMaintenancePeriodicResourcesPostgresTotalBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{{"below", (16 << 20) - 1}, {"at", 16 << 20}, {"over", (16 << 20) + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			owner := openPeriodicResourceOwner(t, f, ctx)
			before := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			// Fixed-width IDs and a fixed timestamp let us account for every
			// inserted non-actor field before immutable rows are appended.
			var timestampBytes int64
			if owner.QueryRow(ctx, "SELECT pg_catalog.octet_length(pg_catalog.convert_to($1::timestamptz::text,'UTF8'))::pg_catalog.int8", periodicResourceTime).Scan(&timestampBytes) != nil {
				t.Fatal("resource timestamp size failed")
			}
			const insertedRows = 260
			overhead := int64(len(periodicResourceIDPrefix)+6+len(periodicResourceAction)+2) + timestampBytes
			remaining := tc.size - before.bytes - insertedRows*overhead
			if remaining <= 0 || remaining > insertedRows*periodicResourceFieldLimit || before.maxField > periodicResourceFieldLimit {
				t.Fatal("resource byte fixture cannot isolate total bytes")
			}
			lengths := make([]int32, insertedRows)
			for i := range lengths {
				n := remaining
				if n > periodicResourceFieldLimit {
					n = periodicResourceFieldLimit
				}
				lengths[i] = int32(n)
				remaining -= n
			}
			if remaining != 0 {
				t.Fatal("resource byte fixture allocation failed")
			}
			insertPeriodicResourceActors(t, ctx, owner, lengths)
			totals := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			if totals.bytes != tc.size || totals.rows != before.rows+insertedRows || totals.auditRows != before.auditRows+insertedRows || totals.maxField > periodicResourceFieldLimit || totals.rows >= periodicResourceRowsLimit || totals.historyBytes != before.historyBytes || totals.bytes-totals.historyBytes >= periodicResourceBytesLimit {
				t.Fatal("total-byte fixture omitted history or crossed another resource boundary")
			}
			owner.Close()
			// In the over case the fixed genuine history is decisive: excluding
			// its bytes would wrongly admit the whole source inventory.
			runPeriodicResourcePublicScan(t, f, totals, tc.size <= periodicResourceBytesLimit)
		})
	}
}

func TestMaintenancePeriodicResourcesPostgresTotalRows(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{{"below", 99999}, {"at", 100000}, {"over", 100001}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newConsumerFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			owner := openPeriodicResourceOwner(t, f, ctx)
			before := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			insertedRows := tc.size - before.rows
			if insertedRows <= 0 || insertedRows >= periodicResourceRowsLimit {
				t.Fatal("resource row fixture cannot isolate the total count")
			}
			_, err := owner.Exec(ctx, `INSERT INTO audit_events(id,actor,tenant_scope,action,target,sanitized_diff,request_id,created_at)
 SELECT $1::text||pg_catalog.lpad(i::text,6,'0'),'',NULL,$2::text,'','{}'::jsonb,'',$3::timestamptz
 FROM pg_catalog.generate_series(1,$4::integer) AS payload(i)`, periodicResourceIDPrefix, periodicResourceAction, periodicResourceTime, insertedRows)
			if err != nil {
				t.Fatal("legal resource row audit insertion failed")
			}
			totals := measurePeriodicResources(t, ctx, owner, f.env.Schemas["app"])
			if totals.rows != tc.size || totals.auditRows != before.auditRows+insertedRows || totals.bytes >= periodicResourceBytesLimit || totals.maxField >= periodicResourceFieldLimit || totals.historyRows != before.historyRows || totals.rows-totals.historyRows >= periodicResourceRowsLimit || totals.auditRows >= periodicResourceRowsLimit {
				t.Fatal("total-row fixture omitted other relations/history or crossed another resource boundary")
			}
			owner.Close()
			runPeriodicResourcePublicScan(t, f, totals, tc.size <= periodicResourceRowsLimit)
		})
	}
}

func runPeriodicResourcePublicScan(t *testing.T, f *consumerFixture, totals periodicResourceTotals, accepted bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	f.awaitOwnedGone(t)
	// The original funding digest includes all audit rows. Capture the new
	// legal setup first, then prove the scan changes neither it nor legacy A.
	fundingBefore, err := f.env.FundingSnapshot(ctx)
	if err != nil || fundingBefore == "" {
		t.Fatal("resource post-setup funding baseline failed")
	}
	legacyBefore, err := f.env.LegacyFundingSnapshot(ctx)
	if err != nil || legacyBefore == "" {
		t.Fatal("resource post-setup legacy baseline failed")
	}
	procurementBefore, err := f.procurementAttemptSnapshot(ctx)
	if err != nil {
		t.Fatal("resource post-setup procurement baseline failed")
	}
	f.awaitOwnedGone(t)
	g, err := maintenancetrust.Acquire(ctx, f.cfg, f.pins)
	if err != nil || g == nil {
		t.Fatal("resource real public Acquire failed")
	}
	f.gates = append(f.gates, g)
	f.observeMaintenance(t)
	snapshot, err := g.BeginPlan(ctx)
	if err != nil || snapshot == nil {
		t.Fatal("resource real public snapshot failed")
	}
	// All genuine migration names/checksums remain fixed. A giant or extra
	// history row cannot legally reach PB: T2 rejects it first. This test
	// counts the real history in each aggregate rather than bypassing T2.
	installation, err := snapshot.VerifyInstallation(ctx)
	if err != nil || installation == nil {
		t.Fatal("resource genuine installation proof failed", err)
	}
	permit, err := maintenancetrust.Attest(ctx, snapshot)
	if err != nil || permit == nil {
		t.Fatal("resource genuine funded predecessor attestation failed")
	}
	lease, err := permit.BeginScan(ctx, maintenancetrust.ScanPeriodicBudget)
	if err != nil || lease == nil {
		t.Fatal("resource real periodic budget lease failed")
	}
	result, scanErr := maintenancescan.VerifyMaintenance(lease)
	if accepted {
		if scanErr != nil || result == nil || result.Verify(permit) != nil {
			t.Fatal("resource boundary rejected an eligible public scan")
		}
		report, err := result.Report(permit)
		if err != nil || report.SchemaVersion != 1 || report.Scanned.Audits != totals.configuredAudits || report.Scanned.Audits >= totals.auditRows {
			t.Fatal("resource scan confused generic audits with PB business audit coverage")
		}
		summary, err := result.Summary(permit)
		if err != nil || summary.Scope != "periodic_budget_rows" {
			t.Fatal("resource scan lost its local-only scope")
		}
		if snapshot.Rollback(ctx) != nil || result.Verify(permit) == nil {
			t.Fatal("resource successful result survived original snapshot rollback")
		}
	} else {
		if result != nil || !errors.Is(scanErr, periodicbudget.ErrVerifyExecution) {
			t.Fatal("resource over-limit public scan must return zero result and execution error")
		}
		// VerifyMaintenance's deferred Abort must revoke the original Permit
		// and physically end its backend, before explicit Gate.Close cleanup.
		if permit.Verify(ctx) == nil {
			t.Fatal("resource over-limit scan left its original permit valid")
		}
		f.awaitOwnedGone(t)
	}
	closeErr := g.Close(ctx)
	if accepted && closeErr != nil || !accepted && !errors.Is(closeErr, maintenancetrust.ErrCleanup) {
		t.Fatal("resource original maintenance close did not preserve the confirmed or abandoned outcome")
	}
	f.awaitOwnedGone(t)
	fundingAfter, err := f.env.FundingSnapshot(ctx)
	if err != nil || fundingAfter != fundingBefore {
		t.Fatal("resource maintenance scan changed genuine funded facts or setup audits")
	}
	legacyAfter, err := f.env.LegacyFundingSnapshot(ctx)
	if err != nil || legacyAfter != legacyBefore {
		t.Fatal("resource maintenance scan changed post-setup legacy facts")
	}
	procurementAfter, err := f.procurementAttemptSnapshot(ctx)
	if err != nil || string(procurementAfter) != string(procurementBefore) {
		t.Fatal("resource maintenance scan changed post-setup procurement facts")
	}
	f.awaitOwnedGone(t)
}
