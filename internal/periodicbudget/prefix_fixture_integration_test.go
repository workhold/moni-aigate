package periodicbudget_test

import (
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These candidate tests deliberately do not follow a future current migration
// set. The existing factory is usable only while this exact embedded baseline
// guard succeeds; the additional candidate is never a formal migration.
func newPBPrefixBaseFixture(t *testing.T) *pbTxFixture {
	t.Helper()
	raw := os.Getenv("PB_PREFIX_DATABASE_URL")
	if raw == "" {
		t.Skip("PB_PREFIX_DATABASE_URL required for isolated prefix candidate PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("prefix candidate tests require the dedicated local moni_test database on port 55439")
	}
	expected := strings.Fields(`001_core.sql 002_billing.sql 003_operations.sql 004_recovery.sql 005_binding_isolation.sql 006_route_decisions.sql 007_model_discovery.sql 008_credential_pools.sql 009_channel_batch_operations.sql 010_key_access_policy.sql 011_key_runtime_limits.sql 012_key_configuration.sql 013_cost_aware_routing.sql 014_procurement_accounts.sql 015_public_catalog.sql 016_self_service_identity.sql 017_funding_vouchers.sql 018_tenant_pricing.sql 019_operations_alerts.sql 020_periodic_budgets.sql 021_payment_sandbox.sql 022_payment_runtime_attestation.sql`)
	names, err := fs.Glob(migrations.Files, "*.sql")
	pbTxMust(t, "read exact prefix candidate embedded baseline", err)
	if !reflect.DeepEqual(names, expected) {
		t.Fatal("prefix candidate requires an isolated exact original 001–022 embedded migration manifest")
	}
	t.Setenv("TEST_DATABASE_URL", raw)
	f := newPBBillingFixture(t)
	pbTxMust(t, "verify exact original 001–022 applied checksums", migrations.VerifyThrough(f.ctx, f.owner, "022_payment_runtime_attestation.sql"))
	var history []string
	rows, err := f.app.Query(f.ctx, `SELECT name FROM schema_migrations ORDER BY name`)
	pbTxMust(t, "read complete original applied history", err)
	for rows.Next() {
		var name string
		pbTxMust(t, "scan applied original migration", rows.Scan(&name))
		history = append(history, name)
	}
	err = rows.Err()
	rows.Close()
	pbTxMust(t, "finish applied original migration history", err)
	if !reflect.DeepEqual(history, expected) {
		t.Fatal("prefix candidate applied history differs from exact original22")
	}
	return f
}

func newPBPrefixFixture(t *testing.T) *pbTxFixture {
	t.Helper()
	f := newPBPrefixBaseFixture(t)
	pbPrefixInstall(t, f)
	return f
}

func pbPrefixInstall(t *testing.T, f *pbTxFixture) {
	t.Helper()
	_, err := f.owner.Exec(f.ctx, pbTxTrackedSQL(t, "b-prefix-candidate.sql"))
	pbTxMust(t, "install additional prefix SQL candidate", err)
	permissions := strings.NewReplacer("{{app}}", pgx.Identifier{f.appRole}.Sanitize(), "{{projection}}", pgx.Identifier{f.projectionRole}.Sanitize()).Replace(pbTxTrackedSQL(t, "b-prefix-permissions.sql"))
	_, err = f.owner.Exec(f.ctx, permissions)
	pbTxMust(t, "grant minimal additional candidate permissions", err)
}

type pbPrefixNativeTime struct {
	target *fundinglotverify.PeriodicBudgetGraphTime
}

func (s *pbPrefixNativeTime) ScanTimestamptz(v pgtype.Timestamptz) error {
	*s.target = fundinglotverify.PeriodicBudgetGraphTime{Value: v.Time, Valid: v.Valid, Infinity: int8(v.InfinityModifier)}
	return nil
}

// pbPrefixRead is a test-only complete fact reader. It has explicit columns,
// native timestamp envelopes and full EOF/Err/Close handling. It does not attest
// catalogs, budgets, runtime authority, or any future production scanner.
func pbPrefixRead[T any](t *testing.T, f *pbTxFixture, tx pgx.Tx, table, columns, order string) []T {
	t.Helper()
	typ := reflect.TypeOf((*T)(nil)).Elem()
	names := strings.Fields(columns)
	if typ.Kind() != reflect.Struct || typ.NumField() != len(names) {
		t.Fatal("prefix test projection shape does not match its fixed columns")
	}
	expr := make([]string, len(names))
	timeType := reflect.TypeOf(fundinglotverify.PeriodicBudgetGraphTime{})
	for i, name := range names {
		expr[i] = pgx.Identifier{name}.Sanitize()
		field := typ.Field(i).Type
		if field != timeType && field.Kind() != reflect.Bool {
			expr[i] += "::text"
		}
	}
	rows, err := tx.Query(f.ctx, "SELECT "+strings.Join(expr, ",")+" FROM ONLY "+pgx.Identifier{f.schema, table}.Sanitize()+" ORDER BY "+order)
	pbTxMust(t, "open complete candidate fact projection", err)
	defer rows.Close()
	var out []T
	for rows.Next() {
		var value T
		v := reflect.ValueOf(&value).Elem()
		dest := make([]any, len(names))
		for i := range dest {
			field := v.Field(i)
			if field.Type() == timeType {
				dest[i] = &pbPrefixNativeTime{field.Addr().Interface().(*fundinglotverify.PeriodicBudgetGraphTime)}
			} else {
				dest[i] = field.Addr().Interface()
			}
		}
		pbTxMust(t, "scan complete candidate fact row", rows.Scan(dest...))
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() == reflect.Slice && v.Field(i).Type().Elem().Kind() == reflect.Uint8 {
				v.Field(i).SetBytes(append([]byte(nil), v.Field(i).Bytes()...))
			}
		}
		out = append(out, value)
	}
	err = rows.Err()
	rows.Close()
	pbTxMust(t, "finish complete candidate fact projection", err)
	return out
}

// Call with the actual APP LOGIN's one repeatable-read read-only transaction.
// All old PB18 arrays and the five additional source groups are read together.
func pbPrefixRows(t *testing.T, f *pbTxFixture, tx pgx.Tx) periodicbudgetprefix.Rows {
	t.Helper()
	var current, session, isolation, readOnly string
	pbTxMust(t, "attest candidate fact-read transaction", tx.QueryRow(f.ctx, `SELECT current_user,session_user,current_setting('transaction_isolation'),current_setting('transaction_read_only')`).Scan(&current, &session, &isolation, &readOnly))
	if current != f.appRole || session != f.appRole || isolation != "repeatable read" || readOnly != "on" {
		t.Fatal("prefix test reader requires actual APP LOGIN in one RR read-only transaction")
	}
	var r periodicbudgetprefix.Rows
	b := &r.Base
	b.Controls = pbPrefixRead[fundinglotverify.PeriodicBudgetControlRow](t, f, tx, "periodic_budget_controls", "id scope tenant_id key_id account_id period_kind enabled limit_amount revision tracking_started_at effective_from max_observed_window_start held_amount spent_amount event_seq last_event_id created_xid configuration_operation_id", "id")
	b.Windows = pbPrefixRead[fundinglotverify.PeriodicBudgetWindowRow](t, f, tx, "periodic_budget_windows", "id control_id start_at end_at held_amount spent_amount revision last_event_id", "id")
	b.Allocations = pbPrefixRead[fundinglotverify.PeriodicBudgetAllocationRow](t, f, tx, "periodic_budget_allocations", "id control_id subject_type subject_id origin origin_source_id window_id admitted_at upper_amount baseline source_held source_spent held_amount spent_amount state revision last_event_id created_xid created_at", "id")
	b.Events = pbPrefixRead[fundinglotverify.PeriodicBudgetEventRow](t, f, tx, "periodic_budget_events", "id control_id allocation_id source_kind source_id seq allocation_before_revision window_before_revision before_held before_spent after_held after_spent after_source_spent after_state held_delta spent_delta created_xid created_at", "id")
	b.Operations = pbPrefixRead[fundinglotverify.PeriodicBudgetOperationRow](t, f, tx, "periodic_budget_operations", "operation_id kind actor scope target resource_id revision safe_input result_snapshot input_fingerprint created_xid created_at", "operation_id")
	b.Versions = pbPrefixRead[fundinglotverify.PeriodicBudgetVersionRow](t, f, tx, "periodic_budget_configuration_versions", "control_id revision operation_id period_kind enabled limit_amount actor created_xid created_at", "control_id,revision")
	b.Tenants = pbPrefixRead[fundinglotverify.ProcurementAttemptTenantRow](t, f, tx, "tenants", "id", "id")
	b.Keys = pbPrefixRead[fundinglotverify.PeriodicBudgetKeyRow](t, f, tx, "api_keys", "id tenant_id", "id")
	b.Users = pbPrefixRead[fundinglotverify.ProcurementApprovalUserRow](t, f, tx, "users", "id", "id")
	b.Requests = pbPrefixRead[fundinglotverify.PeriodicBudgetRequestRow](t, f, tx, "request_records", "id tenant_id key_id pb_created_xid pb_tenant_control pb_key_control pb_admitted_at", "id")
	b.Reservations = pbPrefixRead[fundinglotverify.CustomerKeyReservationRow](t, f, tx, "reservations", "id tenant_id request_id amount status", "id")
	b.Settlements = pbPrefixRead[fundinglotverify.PeriodicBudgetSettlementRow](t, f, tx, "settlements", "id tenant_id request_id charged_amount status", "id")
	b.Attempts = pbPrefixRead[fundinglotverify.PeriodicBudgetAttemptRow](t, f, tx, "upstream_attempts", "id tenant_id request_id credential_id channel_id delivery_state pb_created_xid pb_dispatch_checked_at", "id")
	b.Accounts = pbPrefixRead[fundinglotverify.ProcurementBudgetAccountRow](t, f, tx, "procurement_accounts", "id held_amount spent_amount", "id")
	b.Bindings = pbPrefixRead[fundinglotverify.PeriodicBudgetBindingRow](t, f, tx, "credential_procurement_bindings", "id account_id credential_id channel_id", "id")
	b.Procurement = pbPrefixRead[fundinglotverify.PeriodicBudgetSupplierRow](t, f, tx, "procurement_reservations", "attempt_id account_id binding_id upper_amount held_amount spent_amount state anomaly pb_created_xid pb_control pb_admitted_at", "attempt_id")
	b.ProcurementEvents = pbPrefixRead[fundinglotverify.PeriodicBudgetProcurementEventRow](t, f, tx, "procurement_budget_events", "id event_key account_id attempt_id held_delta spent_delta type pb_created_xid", "id")
	b.Audits = pbPrefixRead[fundinglotverify.PeriodicBudgetAuditRow](t, f, tx, "audit_events", "id actor tenant_scope action target sanitized_diff request_id created_at", "id")
	r.Origins = pbPrefixRead[periodicbudgetprefix.Origin](t, f, tx, "periodic_budget_observation_origins", "control_id version created_xid tracking_started_at", "control_id")
	r.Heads = pbPrefixRead[periodicbudgetprefix.Head](t, f, tx, "periodic_budget_observation_heads", "control_id ordinal last_observation_id updated_xid", "control_id")
	r.Observations = pbPrefixRead[periodicbudgetprefix.Observation](t, f, tx, "periodic_budget_observation_journal", "id control_id ordinal previous_id kind source_id event_seq last_event_id configuration_revision configuration_operation_id window_id window_revision window_last_event_id created_xid observed_at", "control_id,ordinal")
	r.OperationBindings = pbPrefixRead[periodicbudgetprefix.OperationBinding](t, f, tx, "periodic_budget_operations", "operation_id observation_id", "operation_id")
	r.WindowBindings = pbPrefixRead[periodicbudgetprefix.WindowBinding](t, f, tx, "periodic_budget_windows", "id observation_id observation_created_xid", "id")
	return r
}

func pbPrefixCheck(t *testing.T, f *pbTxFixture) periodicbudgetprefix.Report {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	pbTxMust(t, "begin complete candidate fact snapshot", err)
	defer tx.Rollback(f.ctx)
	rows := pbPrefixRows(t, f, tx)
	report, err := periodicbudgetprefix.Check(f.ctx, rows)
	pbTxMust(t, "check complete candidate prefix facts", err)
	pbTxMust(t, "commit readonly candidate snapshot", tx.Commit(f.ctx))
	return report
}
