package periodicbudget_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
)

const pbPrefixNoopSQL = `INSERT INTO periodic_budget_operations(operation_id,kind,actor,scope,target,resource_id,safe_input)
 SELECT $1,c.scope||'_periodic_budget.configure',$2,
 CASE WHEN c.scope='key' THEN c.tenant_id ELSE 'platform' END,
 CASE c.scope WHEN 'tenant' THEN '/api/platform/tenants/'||c.tenant_id||'/periodic-budget'
 WHEN 'key' THEN '/api/keys/'||c.key_id||'/periodic-budget'
 ELSE '/api/platform/procurement-accounts/'||c.account_id||'/periodic-budget' END,c.id,
 jsonb_build_object('expected_revision',c.revision::text,'period_kind',c.period_kind,'enabled',c.enabled,'limit_amount',c.limit_amount::text)
 FROM periodic_budget_controls c WHERE c.id=$3`

func pbPrefixNoopTx(t *testing.T, f *pbTxFixture, tx pgx.Tx, control, operation string) {
	t.Helper()
	f.exec(t, tx, pbPrefixNoopSQL, operation, f.user, control)
}

func pbPrefixSnapshot(t *testing.T, f *pbTxFixture) string {
	t.Helper()
	var s string
	pbTxMust(t, "snapshot complete prefix origin/head/journal", f.app.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'origins',coalesce((SELECT jsonb_agg(to_jsonb(o) ORDER BY control_id) FROM periodic_budget_observation_origins o),'[]'::jsonb),
 'heads',coalesce((SELECT jsonb_agg(to_jsonb(h) ORDER BY control_id) FROM periodic_budget_observation_heads h),'[]'::jsonb),
 'observations',coalesce((SELECT jsonb_agg(to_jsonb(j) ORDER BY control_id,ordinal) FROM periodic_budget_observation_journal j),'[]'::jsonb))::text`).Scan(&s))
	return pbBillProcurementSnapshot(t, f) + s
}

func pbPrefixBalanced(t *testing.T, f *pbTxFixture, commands uint64) periodicbudgetprefix.Report {
	t.Helper()
	r := pbPrefixCheck(t, f)
	if r.Scope != "periodic_budget_snapshot_prefix_v1" || !r.Balanced || !r.CoverageComplete || r.Status != "balanced" || r.Mismatches != (periodicbudgetprefix.Mismatches{}) || r.Base.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) || r.CertifiedCommands != commands {
		t.Fatalf("candidate exact prefix report does not certify expected command count: %+v", r)
	}
	return r
}

func TestPBPrefixPGCommanderAllScopes(t *testing.T) {
	for _, scope := range []string{"tenant", "key", "procurement"} {
		t.Run(scope, func(t *testing.T) {
			f := newPBPrefixFixture(t)
			call := pbCommandCallFor(pbCommander(t, f), f, scope)
			firstInput := pbCommandInput("0", "500")
			first, err := call(f.ctx, firstInput)
			pbTxMust(t, "prefix candidate first Commander configuration", err)
			pbCommandReceipt(t, f, first, firstInput, scope, "1", true)
			before := pbPrefixSnapshot(t, f)
			replay, err := call(f.ctx, firstInput)
			pbTxMust(t, "prefix candidate original operation replay", err)
			if replay.Created || !reflect.DeepEqual(replay.Receipt, first.Receipt) || before != pbPrefixSnapshot(t, f) {
				t.Fatal("replay changed the exact receipt or prefix witnesses")
			}
			noopInput := pbCommandInput("1", "500")
			noop, err := call(f.ctx, noopInput)
			pbTxMust(t, "prefix candidate new-operation no-op", err)
			pbCommandReceipt(t, f, noop, noopInput, scope, "1", false)
			changedInput := pbCommandInput("1", "700")
			changed, err := call(f.ctx, changedInput)
			pbTxMust(t, "prefix candidate changed configuration", err)
			pbCommandReceipt(t, f, changed, changedInput, scope, "2", false)
			var valid bool
			pbTxMust(t, "verify DB origin and exact command chain", f.app.QueryRow(f.ctx, `SELECT EXISTS(
 SELECT 1 FROM periodic_budget_controls c JOIN periodic_budget_observation_origins o ON o.control_id=c.id
 JOIN periodic_budget_observation_heads h ON h.control_id=c.id
 WHERE c.id=$1 AND o.version=1 AND o.created_xid=c.created_xid AND o.tracking_started_at=c.tracking_started_at
 AND h.ordinal=3 AND (SELECT count(*)=3 AND bool_and(j.kind='command' AND j.event_seq=0 AND j.last_event_id IS NULL AND j.window_id IS NULL) FROM periodic_budget_observation_journal j WHERE j.control_id=c.id)
 AND h.last_observation_id=(SELECT j.id FROM periodic_budget_observation_journal j WHERE j.control_id=c.id AND j.ordinal=3))`, first.Receipt.Operation.ResourceID).Scan(&valid))
			if !valid {
				t.Fatal("origin or first/no-op/change command observation chain differs")
			}
			pbPrefixBalanced(t, f, 3)
			f.verifyFinance(t)
		})
	}
}

func TestPBPrefixPGSameFinancialCutWindowOrder(t *testing.T) {
	for _, windowFirst := range []bool{true, false} {
		name := "command_before_window"
		if windowFirst {
			name = "window_before_command"
		}
		t.Run(name, func(t *testing.T) {
			f := newPBPrefixFixture(t)
			control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
			tx := f.begin(t)
			operation := uuid.NewString()
			var window string
			observe := func() {
				pbTxMust(t, "create zero-event current window", tx.QueryRow(f.ctx, `SELECT pb_observe($1,pb_now())`, control).Scan(&window))
			}
			if windowFirst {
				observe()
			}
			pbPrefixNoopTx(t, f, tx, control, operation)
			if !windowFirst {
				observe()
			}
			pbTxMust(t, "commit same-cut source ordering", tx.Commit(f.ctx))
			var commandOrdinal, birthOrdinal int64
			var commandWindow *string
			var financialCut string
			pbTxMust(t, "read distinct observation ordinals at identical financial cut", f.app.QueryRow(f.ctx, `SELECT c.ordinal,b.ordinal,c.window_id,c.event_seq::text FROM periodic_budget_observation_journal c JOIN periodic_budget_observation_journal b ON b.control_id=c.control_id AND b.kind='window_birth' WHERE c.source_id=$1 AND c.kind='command'`, operation).Scan(&commandOrdinal, &birthOrdinal, &commandWindow, &financialCut))
			if financialCut != "0" || (windowFirst && (birthOrdinal >= commandOrdinal || commandWindow == nil || *commandWindow != window)) || (!windowFirst && (commandOrdinal >= birthOrdinal || commandWindow != nil)) {
				t.Fatal("same financial seq lost window existence order")
			}
			var jsonWindow *string
			pbTxMust(t, "read immutable exact command JSON window", f.app.QueryRow(f.ctx, `SELECT result_snapshot->'current_window'->>'id' FROM periodic_budget_operations WHERE operation_id=$1`, operation).Scan(&jsonWindow))
			if !reflect.DeepEqual(jsonWindow, commandWindow) {
				t.Fatal("snapshot JSON window does not match its observation cut")
			}
			r := pbPrefixBalanced(t, f, 2)
			if r.Base.Unproven.OperationSnapshotPrefixUnproven == 0 {
				t.Fatal("new scope silently altered original v1 window-order gap")
			}
		})
	}
}

func TestPBPrefixPGRealCustomerSupplierEffectsAndCommands(t *testing.T) {
	f := newPBPrefixFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	controls := map[string]string{}
	for _, scope := range []string{"tenant", "key", "procurement"} {
		controls[scope] = pbBillProcurementConfigure(t, f, scope, "0", "10000")
	}
	f.setClock(t, today.AddDate(0, 0, -1))
	commands := uint64(3)
	observeCommands := func() {
		for _, scope := range []string{"tenant", "key", "procurement"} {
			pbBillProcurementConfigure(t, f, scope, "1", "10000")
			commands++
		}
		pbPrefixBalanced(t, f, commands)
	}
	r := pbBillProcurementReserve(t, f, s)
	a := pbBillProcurementAttempt(t, f, s, r, g, 1)
	observeCommands()
	_, err := s.RecordFinalization(f.ctx, pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, "done"))
	pbTxMust(t, "real supplier finalization with prefix tracking", err)
	observeCommands()
	_, err = s.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "real customer settlement with prefix tracking", err)
	observeCommands()
	pbBillProcurementCorrect(t, f, s, a.ID, 25)
	observeCommands()
	// A separate known-not-sent customer/supplier source exercises real release
	// without replaying any provider generation or substituting owner balances.
	released := pbBillProcurementReserve(t, f, s)
	attempt := pbBillProcurementAttempt(t, f, s, released, g, 1)
	_, err = s.RecordFinalization(f.ctx, pbBillProcurementFinal(released, attempt, billing.Usage{Status: "missing"}, "rejected_no_consumption"))
	pbTxMust(t, "real trusted supplier release", err)
	_, err = s.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: released.RequestID, Reason: "not_sent"})
	pbTxMust(t, "real known-not-sent customer release", err)
	observeCommands()
	report := pbPrefixBalanced(t, f, commands)
	if report.Base.Unproven.OperationSnapshotPrefixUnproven == 0 {
		t.Fatal("original operation-prefix limitation was not preserved in Base")
	}
	var actualCustomer, actualSupplier string
	pbTxMust(t, "read final customer spend from original financial source", f.app.QueryRow(f.ctx, `SELECT spent_amount::text FROM periodic_budget_controls WHERE id=$1`, controls["tenant"]).Scan(&actualCustomer))
	pbTxMust(t, "read final supplier cost after absolute correction", f.app.QueryRow(f.ctx, `SELECT spent_amount::text FROM periodic_budget_controls WHERE id=$1`, controls["procurement"]).Scan(&actualSupplier))
	if actualCustomer != "15" || actualSupplier != "25" {
		t.Fatal("candidate changed actual retail/procurement financial amounts")
	}
	f.verifyFinance(t)
}

func TestPBPrefixPGLegacyControlRemainsUncovered(t *testing.T) {
	f := newPBPrefixBaseFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	controls := []string{}
	for _, scope := range []string{"tenant", "key", "procurement"} {
		controls = append(controls, pbBillProcurementConfigure(t, f, scope, "0", "500"))
	}
	pbPrefixInstall(t, f)
	f.setClock(t, today.AddDate(0, 0, -1))
	r := pbBillProcurementReserve(t, f, s)
	a := pbBillProcurementAttempt(t, f, s, r, g, 1)
	_, err := s.RecordFinalization(f.ctx, pbBillProcurementFinal(r, a, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, "done"))
	pbTxMust(t, "legacy control continues actual supplier finalization", err)
	_, err = s.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	pbTxMust(t, "legacy control continues actual customer settlement", err)
	pbBillProcurementCorrect(t, f, s, a.ID, 25)
	for _, scope := range []string{"tenant", "key", "procurement"} {
		pbBillProcurementConfigure(t, f, scope, "1", "500")
	}
	for _, control := range controls {
		var exact bool
		pbTxMust(t, "verify pre-candidate sources remain explicitly unbound", f.app.QueryRow(f.ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM periodic_budget_observation_origins WHERE control_id=$1)
 AND NOT EXISTS(SELECT 1 FROM periodic_budget_observation_heads WHERE control_id=$1)
 AND NOT EXISTS(SELECT 1 FROM periodic_budget_observation_journal WHERE control_id=$1)
 AND (SELECT count(*)=2 AND bool_and(observation_id IS NULL) FROM periodic_budget_operations WHERE resource_id=$1)
 AND (SELECT count(*)=1 AND bool_and(observation_id IS NULL AND observation_created_xid IS NULL) FROM periodic_budget_windows WHERE control_id=$1)`, control).Scan(&exact))
		if !exact {
			t.Fatal("candidate fabricated legacy control enrollment or source creation witnesses")
		}
	}
	report := pbPrefixCheck(t, f)
	if report.Status != "indeterminate" || report.Balanced || report.CoverageComplete || report.CertifiedCommands != 0 || report.Unproven.UncoveredControls != 3 || report.Mismatches != (periodicbudgetprefix.Mismatches{}) || report.Base.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) {
		t.Fatalf("legacy absence became certification or mismatch: %+v", report)
	}
	f.verifyFinance(t)
}

func pbPrefixReject(t *testing.T, f *pbTxFixture, poolName, query string, args ...any) {
	t.Helper()
	before := pbPrefixSnapshot(t, f)
	pool := f.app
	if poolName == "projection" {
		pool = f.projection
	}
	tx, err := pool.Begin(f.ctx)
	pbTxMust(t, "begin rejected candidate mutation", err)
	_, err = tx.Exec(f.ctx, query, args...)
	if err == nil {
		err = tx.Commit(f.ctx)
	} else {
		_ = tx.Rollback(f.ctx)
	}
	if err == nil {
		t.Fatal("actual restricted LOGIN committed an invalid prefix mutation")
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || (pg.Code != "42501" && pg.Code != "23514" && pg.Code != "23503" && pg.Code != "23505") {
		if pg != nil {
			t.Fatalf("candidate mutation failed outside its intended authorization/invariant boundary (SQLSTATE %s)", pg.Code)
		}
		t.Fatalf("candidate mutation failed outside its intended authorization/invariant boundary (%T)", err)
	}
	_ = tx.Rollback(f.ctx)
	if before != pbPrefixSnapshot(t, f) {
		t.Fatal("rejected candidate mutation changed persistent sources or witness history")
	}
}

func TestPBPrefixPGRestrictedImmutabilityAndForgery(t *testing.T) {
	f := newPBPrefixFixture(t)
	control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
	other := pbBillProcurementConfigure(t, f, "key", "0", "500")
	_, err := f.app.Exec(f.ctx, `SELECT pb_observe($1,pb_now())`, control)
	pbTxMust(t, "establish covered birth source", err)
	for _, tc := range []struct {
		name, query string
		args        []any
	}{
		{"origin_update", `UPDATE periodic_budget_observation_origins SET version=version WHERE control_id=$1`, []any{control}},
		{"origin_delete", `DELETE FROM periodic_budget_observation_origins WHERE control_id=$1`, []any{control}},
		{"origin_truncate", `TRUNCATE periodic_budget_observation_origins CASCADE`, nil},
		{"journal_update", `UPDATE periodic_budget_observation_journal SET ordinal=ordinal WHERE control_id=$1`, []any{control}},
		{"journal_delete", `DELETE FROM periodic_budget_observation_journal WHERE control_id=$1`, []any{control}},
		{"journal_truncate", `TRUNCATE periodic_budget_observation_journal CASCADE`, nil},
		{"head_delete", `DELETE FROM periodic_budget_observation_heads WHERE control_id=$1`, []any{control}},
		{"head_truncate", `TRUNCATE periodic_budget_observation_heads CASCADE`, nil},
		{"head_noop", `UPDATE periodic_budget_observation_heads SET ordinal=ordinal WHERE control_id=$1`, []any{control}},
		{"head_jump", `UPDATE periodic_budget_observation_heads SET ordinal=ordinal+2,last_observation_id=$2 WHERE control_id=$1`, []any{control, uuid.NewString()}},
		{"head_backwards", `UPDATE periodic_budget_observation_heads SET ordinal=0,last_observation_id=NULL WHERE control_id=$1`, []any{control}},
		{"source_operation_pointer", `UPDATE periodic_budget_operations SET observation_id=$2 WHERE resource_id=$1`, []any{control, uuid.NewString()}},
		{"source_window_pointer", `UPDATE periodic_budget_windows SET observation_id=$2,observation_created_xid=pg_current_xact_id() WHERE control_id=$1`, []any{control, uuid.NewString()}},
		{"orphan_origin", `INSERT INTO periodic_budget_observation_origins(control_id,version,tracking_started_at) VALUES($1,1,pb_now())`, []any{uuid.NewString()}},
		{"duplicate_origin", `INSERT INTO periodic_budget_observation_origins SELECT * FROM periodic_budget_observation_origins WHERE control_id=$1`, []any{control}},
		{"orphan_head", `INSERT INTO periodic_budget_observation_heads(control_id,ordinal,last_observation_id) VALUES($1,0,NULL)`, []any{uuid.NewString()}},
		{"duplicate_head", `INSERT INTO periodic_budget_observation_heads SELECT * FROM periodic_budget_observation_heads WHERE control_id=$1`, []any{control}},
		{"orphan_command", `INSERT INTO periodic_budget_observation_journal(id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at)
 SELECT $2,c.id,h.ordinal+1,h.last_observation_id,'command',$3,c.event_seq,c.last_event_id,c.revision,c.configuration_operation_id,w.id,w.revision,w.last_event_id,pb_now() FROM periodic_budget_controls c JOIN periodic_budget_observation_heads h ON h.control_id=c.id LEFT JOIN periodic_budget_windows w ON w.control_id=c.id AND w.start_at=pb_start(c.period_kind,pb_now()) WHERE c.id=$1`, []any{control, uuid.NewString(), uuid.NewString()}},
		{"duplicate_source_wrong_kind", `INSERT INTO periodic_budget_observation_journal(id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at)
 SELECT $2,control_id,ordinal+10,id,'window_birth',source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,source_id,'0',NULL,NULL FROM periodic_budget_observation_journal WHERE control_id=$1 AND kind='command' LIMIT 1`, []any{control, uuid.NewString()}},
		{"cross_control_wrong_source", `INSERT INTO periodic_budget_observation_journal(id,control_id,ordinal,previous_id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at)
 SELECT $3,$2,ordinal+10,id,kind,source_id,event_seq,last_event_id,configuration_revision,configuration_operation_id,window_id,window_revision,window_last_event_id,observed_at FROM periodic_budget_observation_journal WHERE control_id=$1 LIMIT 1`, []any{control, other, uuid.NewString()}},
	} {
		t.Run(tc.name, func(t *testing.T) { pbPrefixReject(t, f, "app", tc.query, tc.args...) })
	}
	// Source pointer columns cannot turn a caller-created UUID into an adopted
	// witness: old operation columns still run all old guards and DB overrides.
	pbPrefixBalanced(t, f, 2)
}

func TestPBPrefixPGOriginalProjectionProfileRemainsDenied(t *testing.T) {
	f := newPBPrefixFixture(t)
	control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
	for _, table := range []string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal"} {
		for _, query := range []string{"SELECT * FROM " + pgx.Identifier{f.schema, table}.Sanitize(), "INSERT INTO " + pgx.Identifier{f.schema, table}.Sanitize() + " DEFAULT VALUES", "DELETE FROM " + pgx.Identifier{f.schema, table}.Sanitize() + " WHERE control_id=$1", "TRUNCATE " + pgx.Identifier{f.schema, table}.Sanitize() + " CASCADE"} {
			args := []any(nil)
			if strings.Contains(query, "$1") {
				args = []any{control}
			}
			pbPrefixReject(t, f, "projection", query, args...)
		}
	}
	var unsafe bool
	pbTxMust(t, "inspect original projection candidate privilege denial", f.projection.QueryRow(f.ctx, `SELECT bool_or(has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE')) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname IN ('periodic_budget_observation_origins','periodic_budget_observation_heads','periodic_budget_observation_journal')`, f.schema).Scan(&unsafe))
	if unsafe {
		t.Fatal("old projection acquired a new candidate relation privilege before explicit profile publication")
	}
}

func TestPBPrefixPGSavepointAndFullRollback(t *testing.T) {
	f := newPBPrefixFixture(t)
	control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
	before := pbPrefixSnapshot(t, f)
	tx := f.begin(t)
	f.exec(t, tx, `SAVEPOINT prefix_actions`)
	f.exec(t, tx, `SELECT pb_observe($1,pb_now())`, control)
	pbPrefixNoopTx(t, f, tx, control, uuid.NewString())
	f.exec(t, tx, `ROLLBACK TO SAVEPOINT prefix_actions`)
	pbTxMust(t, "commit after rolling back source and journal savepoint", tx.Commit(f.ctx))
	if before != pbPrefixSnapshot(t, f) {
		t.Fatal("savepoint rollback retained a source, origin/head movement or observation")
	}
	tx = f.begin(t)
	f.exec(t, tx, `SELECT pb_observe($1,pb_now())`, control)
	pbPrefixNoopTx(t, f, tx, control, uuid.NewString())
	pbTxMust(t, "rollback full source and witness transaction", tx.Rollback(f.ctx))
	if before != pbPrefixSnapshot(t, f) {
		t.Fatal("full rollback retained a source, head movement or observation")
	}
	tx = f.begin(t)
	pbPrefixNoopTx(t, f, tx, control, uuid.NewString())
	pbTxMust(t, "commit subsequent contiguous command", tx.Commit(f.ctx))
	var ordinal int64
	pbTxMust(t, "read contiguous post-rollback head", f.app.QueryRow(f.ctx, `SELECT ordinal FROM periodic_budget_observation_heads WHERE control_id=$1`, control).Scan(&ordinal))
	if ordinal != 2 {
		t.Fatal("rolled back journal actions consumed persistent ordinals")
	}
	pbPrefixBalanced(t, f, 2)
}

func TestPBPrefixPGSourceInsertOverridesCallerWitness(t *testing.T) {
	f := newPBPrefixFixture(t)
	control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
	operation, fakeObservation, window := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx := f.begin(t)
	query := strings.Replace(pbPrefixNoopSQL, "resource_id,safe_input)", "resource_id,safe_input,observation_id)", 1)
	query = strings.Replace(query, " FROM periodic_budget_controls c WHERE c.id=$3", ",$4::text FROM periodic_budget_controls c WHERE c.id=$3", 1)
	f.exec(t, tx, query, operation, f.user, control, fakeObservation)
	f.exec(t, tx, `INSERT INTO periodic_budget_windows(id,control_id,start_at,end_at,observation_id,observation_created_xid)
 SELECT $1,c.id,pb_start(c.period_kind,pb_now()),pb_end(c.period_kind,pb_start(c.period_kind,pb_now())),$3,'1'::xid8 FROM periodic_budget_controls c WHERE c.id=$2`, window, control, fakeObservation)
	pbTxMust(t, "commit DB-owned source witnesses despite supplied false input", tx.Commit(f.ctx))
	var exact bool
	pbTxMust(t, "assert caller witness was overwritten rather than adopted", f.app.QueryRow(f.ctx, `SELECT
 (SELECT observation_id IS NOT NULL AND observation_id<>$3 FROM periodic_budget_operations WHERE operation_id=$1)
 AND (SELECT observation_id IS NOT NULL AND observation_id<>$3 AND observation_created_xid<>'1'::xid8 FROM periodic_budget_windows WHERE id=$2)
 AND NOT EXISTS(SELECT 1 FROM periodic_budget_observation_journal WHERE id=$3)`, operation, window, fakeObservation).Scan(&exact))
	if !exact {
		t.Fatal("caller-created observation pointer or creation stamp became a durable source witness")
	}
	pbPrefixBalanced(t, f, 2)
}

func TestPBPrefixPGPrecreatedOriginCannotEnrollControl(t *testing.T) {
	f := newPBPrefixFixture(t)
	before := pbPrefixSnapshot(t, f)
	control, tenant := uuid.NewString(), uuid.NewString()
	tx := f.begin(t)
	f.exec(t, tx, `INSERT INTO tenants(id,name) VALUES($1,'prefix-precreation-rejected')`, tenant)
	f.exec(t, tx, `INSERT INTO periodic_budget_observation_origins(control_id,version,tracking_started_at) VALUES($1,1,pb_now())`, control)
	f.exec(t, tx, `INSERT INTO periodic_budget_observation_heads(control_id,ordinal,last_observation_id) VALUES($1,0,NULL)`, control)
	_, err := tx.Exec(f.ctx, `INSERT INTO periodic_budget_controls(id,scope,tenant_id,period_kind,enabled,limit_amount,configuration_operation_id) VALUES($1,'tenant',$2,'utc_day',true,500,$3)`, control, tenant, uuid.NewString())
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		t.Fatal("automatic original control birth adopted a caller-created origin/head instead of conflicting")
	}
	pbTxMust(t, "rollback attempted pre-created origin adoption", tx.Rollback(f.ctx))
	if before != pbPrefixSnapshot(t, f) {
		t.Fatal("rejected enrollment retained tenant/control/origin/head state")
	}
	pbPrefixBalanced(t, f, 0)
}

func TestPBPrefixPGCTECommandThenWindowAtSameCut(t *testing.T) {
	f := newPBPrefixFixture(t)
	control := pbBillProcurementConfigure(t, f, "tenant", "0", "500")
	operation := uuid.NewString()
	// The data-changing CTE finishes the command INSERT before the outer SELECT
	// creates a window. Both sources have financial seq 0, so only their shared
	// observation ordinal can preserve the original null command snapshot.
	query := "WITH prefix_command AS (" + pbPrefixNoopSQL + " RETURNING resource_id) SELECT pb_observe(resource_id,pb_now()) FROM prefix_command"
	var window string
	pbTxMust(t, "actual APP single-statement CTE command then window effect", f.app.QueryRow(f.ctx, query, operation, f.user, control).Scan(&window))
	var ordered bool
	pbTxMust(t, "verify CTE retained original null window at identical financial cut", f.app.QueryRow(f.ctx, `SELECT
 o.result_snapshot->'current_window'='null'::jsonb AND j.window_id IS NULL
 AND j.event_seq=0 AND b.event_seq=0 AND j.ordinal<b.ordinal AND b.source_id=$2
 FROM periodic_budget_operations o JOIN periodic_budget_observation_journal j ON j.id=o.observation_id
 JOIN periodic_budget_observation_journal b ON b.control_id=j.control_id AND b.kind='window_birth'
 WHERE o.operation_id=$1`, operation, window).Scan(&ordered))
	if !ordered {
		t.Fatal("CTE outer side effect changed the earlier command snapshot or lost same-cut order")
	}
	tx := f.begin(t)
	later := uuid.NewString()
	pbPrefixNoopTx(t, f, tx, control, later)
	pbTxMust(t, "commit later original command observing CTE-created window", tx.Commit(f.ctx))
	var seesWindow bool
	pbTxMust(t, "verify later command sees exactly the CTE window", f.app.QueryRow(f.ctx, `SELECT result_snapshot->'current_window'->>'id'=$2 FROM periodic_budget_operations WHERE operation_id=$1`, later, window).Scan(&seesWindow))
	if !seesWindow {
		t.Fatal("later command did not see the already born same-cut window")
	}
	pbPrefixBalanced(t, f, 3)
}

func TestPBPrefixPGCrossDayCarryKeepsOriginalCustomerAndSupplierWindows(t *testing.T) {
	f := newPBPrefixFixture(t)
	s := billing.New(f.app)
	g := pbBillProcurementCatalog(t, f)
	today := pbBillProcurementToday(t, f)
	f.setClock(t, today.AddDate(0, 0, -2))
	scopes := []string{"tenant", "key", "procurement"}
	controls := map[string]string{}
	for _, scope := range scopes {
		controls[scope] = pbBillProcurementConfigure(t, f, scope, "0", "10000")
	}
	f.setClock(t, today.AddDate(0, 0, -1))
	settled := pbBillProcurementReserve(t, f, s)
	settledAttempt := pbBillProcurementAttempt(t, f, s, settled, g, 1)
	released := pbBillProcurementReserve(t, f, s)
	releasedAttempt := pbBillProcurementAttempt(t, f, s, released, g, 1)
	sourceWindow := func(control, customer, supplier string) string {
		var window string
		pbTxMust(t, "read exact source attribution window", f.app.QueryRow(f.ctx, `SELECT window_id FROM periodic_budget_allocations WHERE control_id=$1 AND subject_id IN ($2,$3)`, control, customer, supplier).Scan(&window))
		return window
	}
	oldWindows := map[string]string{}
	for _, scope := range scopes {
		oldWindows[scope] = sourceWindow(controls[scope], settled.RequestID, settledAttempt.ID)
		if sourceWindow(controls[scope], released.RequestID, releasedAttempt.ID) != oldWindows[scope] {
			t.Fatal("prior-day original sources did not share their scope's prior-day window")
		}
	}
	commands := uint64(3)
	commandStage := func(customerCarry, supplierCarry, customerHeld, supplierHeld string, current bool) {
		for _, scope := range scopes {
			out := pbBillCustomerConfigure(t, f, scope, "1", "10000", true)
			carry, held := customerCarry, customerHeld
			if scope == "procurement" {
				carry, held = supplierCarry, supplierHeld
			}
			view := out.Receipt.Result
			if view.CarryHeld != carry || (view.CurrentWindow != nil) != current {
				t.Fatal("cross-day command snapshot has wrong carry or current-window existence")
			}
			if current && (view.CurrentWindow.HeldAmount != held || view.CurrentWindow.SpentAmount != "0") {
				t.Fatal("cross-day command inherited old-window held/spent into the current window")
			}
			commands++
		}
		pbPrefixBalanced(t, f, commands)
	}
	f.setClock(t, today)
	commandStage("240", "480", "", "", false)
	current := pbBillProcurementReserve(t, f, s)
	currentAttempt := pbBillProcurementAttempt(t, f, s, current, g, 1)
	currentWindows := map[string]string{}
	for _, scope := range scopes {
		currentWindows[scope] = sourceWindow(controls[scope], current.RequestID, currentAttempt.ID)
		if currentWindows[scope] == oldWindows[scope] {
			t.Fatal("new day reopened an old attribution window")
		}
	}
	commandStage("240", "480", "120", "240", true)
	_, err := s.RecordFinalization(f.ctx, pbBillProcurementFinal(settled, settledAttempt, billing.Usage{Status: "confirmed_final", InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, "done"))
	pbTxMust(t, "settle prior-day supplier after independent current-day admission", err)
	_, err = s.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: settled.RequestID})
	pbTxMust(t, "settle prior-day customer after independent current-day admission", err)
	commandStage("120", "240", "120", "240", true)
	_, err = s.RecordFinalization(f.ctx, pbBillProcurementFinal(released, releasedAttempt, billing.Usage{Status: "missing"}, "rejected_no_consumption"))
	pbTxMust(t, "release prior-day supplier without moving its attribution", err)
	_, err = s.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: released.RequestID, Reason: "not_sent"})
	pbTxMust(t, "release prior-day customer without moving its attribution", err)
	commandStage("0", "0", "120", "240", true)
	for _, scope := range scopes {
		held, spent := "120", "15"
		if scope == "procurement" {
			held, spent = "240", "30"
		}
		var exact bool
		pbTxMust(t, "verify old effects preserved both source attribution and current window revision", f.app.QueryRow(f.ctx, `SELECT
 (SELECT held_amount=0 AND spent_amount=$3::bigint AND revision=4 FROM periodic_budget_windows WHERE id=$1)
 AND (SELECT held_amount=$4::bigint AND spent_amount=0 AND revision=1 FROM periodic_budget_windows WHERE id=$2)
 AND (SELECT held_amount=$4::bigint AND spent_amount=$3::bigint FROM periodic_budget_controls WHERE id=$5)`, oldWindows[scope], currentWindows[scope], spent, held, controls[scope]).Scan(&exact))
		if !exact || sourceWindow(controls[scope], settled.RequestID, settledAttempt.ID) != oldWindows[scope] || sourceWindow(controls[scope], released.RequestID, releasedAttempt.ID) != oldWindows[scope] {
			t.Fatal("old source settlement/release changed the new window or its immutable attribution")
		}
	}
	_, err = s.RecordFinalization(f.ctx, pbBillProcurementFinal(current, currentAttempt, billing.Usage{Status: "missing"}, "rejected_no_consumption"))
	pbTxMust(t, "complete independent current-day supplier release", err)
	_, err = s.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: current.RequestID, Reason: "not_sent"})
	pbTxMust(t, "complete independent current-day customer release", err)
	commandStage("0", "0", "0", "0", true)
	pbBillCustomerWallet(t, f, s, 9985, 0)
	f.verifyFinance(t)
}
