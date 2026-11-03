package procurementaccountwriter_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/procurement"
	"github.com/moni-ai/moni-aigate/internal/procurementaccountwriter"
)

// These are the frozen SQL write lists, not a selection of currently authorized
// catalog columns. In particular, explicit NULL inputs still need INSERT.
var accountDMLInsertGroups = []struct {
	table, columns string
	configure      bool
}{
	{"pp_account_command_sources", "id,root_id,step_no,created_xid,created_at,operation_id,actor_id,actor_role,actor_status,kind,input_target_id,target_id,input_label,input_provider,input_state,input_limit_amount,input_digest,command_status,command_reason,command_created_at,audit_id,input_expected_revision,journal_id", true},
	{"procurement_accounts", "id,label,provider,state,limit_amount,pp_version,pp_created_xid,pp_origin_journal_id", false},
	{"pp_account_journal", "id,account_id,ordinal,previous_id,root_id,step_no,step_kind,created_xid,created_at,kind,source_kind,source_id,before_label,before_provider,before_state,before_limit_amount,before_configuration_revision,before_held_amount,before_spent_amount,before_blocking_count,after_label,after_provider,after_state,after_limit_amount,after_configuration_revision,after_held_amount,after_spent_amount,after_blocking_count,command_source_id", true},
	{"pp_account_origins", "account_id,version,create_operation_id,command_source_id,root_id,step_no,journal_id,created_xid,created_at", false},
	{"pp_account_heads", "account_id,ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count", false},
	{"procurement_commands", "operation_id,actor,kind,target_id,input_digest,status,reason,before_state,after_state,pp_source_id,pp_root_id,pp_root_step,pp_created_xid,pp_account_journal_id", true},
	{"audit_events", "id,actor,action,target,sanitized_diff,pp_root_id,pp_root_step,pp_source_kind,pp_source_id,pp_source_role,pp_created_xid", true},
	{"pp_root_steps", "root_id,step_no,kind,source_id,created_xid", true},
	{"pp_operation_roots", "id,entry_kind,scope_kind,tenant_id,request_id,attempt_id,account_target_id,created_xid,session_role_oid,step_count,semantic_result", true},
}

var accountDMLUpdateGroups = []struct{ table, columns string }{
	{"procurement_accounts", "label,provider,state,limit_amount"},
	{"pp_account_heads", "ordinal,journal_id,updated_xid,label,provider,state,limit_amount,configuration_revision,held_amount,spent_amount,blocking_count"},
}

const accountDMLUnusedJournalColumns = "attempt_id,binding_id,before_reservation_revision,before_reservation_state,before_reservation_held_amount,before_reservation_spent_amount,before_reservation_anomaly,after_reservation_revision,after_reservation_state,after_reservation_held_amount,after_reservation_spent_amount,after_reservation_anomaly,input_bound,output_bound,upper_amount,cost_version_id,cost_input_rate,cost_output_rate,cost_basis,gate_extra,gate_checked_at,pb_decision_at,pause_event_id,transition_id,delivery_source_id"

type accountDMLPrivilege struct{ table, column, privilege string }

type accountDMLCase struct {
	kind string
	accountDMLPrivilege
}

func accountDMLMatrix() []accountDMLCase {
	var matrix []accountDMLCase
	for _, kind := range []string{"create", "configure"} {
		for _, group := range accountDMLInsertGroups {
			if kind == "configure" && !group.configure {
				continue
			}
			for _, column := range strings.Split(group.columns, ",") {
				matrix = append(matrix, accountDMLCase{kind, accountDMLPrivilege{group.table, column, "INSERT"}})
			}
		}
		if kind == "configure" {
			for _, group := range accountDMLUpdateGroups {
				for _, column := range strings.Split(group.columns, ",") {
					matrix = append(matrix, accountDMLCase{kind, accountDMLPrivilege{group.table, column, "UPDATE"}})
				}
			}
		}
		// This is a lock capability, not a real users UPDATE. The other two
		// relations retain alternative UPDATE grants for their row locks.
		matrix = append(matrix, accountDMLCase{kind, accountDMLPrivilege{"users", "created_at", "UPDATE"}})
	}
	return matrix
}

// Run without a database as a coverage-drift check; it does not provide any PG
// evidence. The actual fault list remains fixed even if ACL provisioning drifts.
func TestAccountWriterDMLFrozenMatrix(t *testing.T) {
	matrix := accountDMLMatrix()
	distinct := map[accountDMLPrivilege]bool{}
	counts := map[string]int{}
	seen := map[accountDMLCase]bool{}
	for _, item := range matrix {
		if seen[item] {
			t.Fatal("duplicate fixed DML fault case")
		}
		seen[item], distinct[item.accountDMLPrivilege] = true, true
		counts[item.kind]++
	}
	if len(matrix) != 232 || len(distinct) != 138 || counts["create"] != 123 || counts["configure"] != 109 {
		t.Fatalf("fixed DML matrix drift: calls=%d grants=%d cases=%v", len(matrix), len(distinct), counts)
	}
	b := procurementaccountwriter.Bindings{Schema: "dml_frozen", App: "dml_app", Writer: "dml_writer", AppOID: 17001, WriterOID: 17002}
	sql, err := procurementaccountwriter.SQL(b)
	must(t, "authenticate frozen writer for fixed coverage", err)
	actualInsert := map[string]string{}
	for _, match := range regexp.MustCompile(`INSERT INTO "dml_frozen"\.([a-z_]+)\(([^)]*)\)`).FindAllStringSubmatch(sql, -1) {
		if _, exists := actualInsert[match[1]]; exists {
			t.Fatal("multiple SQL INSERT lists require an explicit matrix review")
		}
		actualInsert[match[1]] = match[2]
	}
	fixedInsert := map[string]string{}
	for _, group := range accountDMLInsertGroups {
		fixedInsert[group.table] = group.columns
	}
	if len(fixedInsert) != 9 || !reflect.DeepEqual(actualInsert, fixedInsert) {
		t.Fatalf("fixed INSERT coverage differs from original SQL: actual=%v fixed=%v", actualInsert, fixedInsert)
	}
	actualUpdate := map[string]string{}
	for _, match := range regexp.MustCompile(`UPDATE "dml_frozen"\.([a-z_]+) [a-z]+ SET ([^\n]+?) WHERE `).FindAllStringSubmatch(sql, -1) {
		var columns []string
		for _, assignment := range strings.Split(match[2], ",") {
			columns = append(columns, strings.SplitN(assignment, "=", 2)[0])
		}
		actualUpdate[match[1]] = strings.Join(columns, ",")
	}
	fixedUpdate := map[string]string{}
	for _, group := range accountDMLUpdateGroups {
		fixedUpdate[group.table] = group.columns
	}
	if !reflect.DeepEqual(actualUpdate, fixedUpdate) {
		t.Fatalf("fixed UPDATE coverage differs from original SQL: actual=%v fixed=%v", actualUpdate, fixedUpdate)
	}
	permissions, err := procurementaccountwriter.Permissions(b)
	must(t, "authenticate original journal grants", err)
	match := regexp.MustCompile(`GRANT INSERT\(([^)]*)\) ON "dml_frozen"\.pp_account_journal TO "dml_writer";`).FindStringSubmatch(permissions)
	if len(match) != 2 {
		t.Fatal("original fixed journal INSERT grant missing")
	}
	used := map[string]bool{}
	for _, column := range strings.Split(fixedInsert["pp_account_journal"], ",") {
		used[column] = true
	}
	var unused []string
	for _, column := range strings.Split(match[1], ",") {
		if !used[column] {
			unused = append(unused, column)
		}
	}
	if len(strings.Split(match[1], ",")) != 54 || len(unused) != 25 || strings.Join(unused, ",") != accountDMLUnusedJournalColumns {
		t.Fatalf("unused journal grant boundary drift: %v", unused)
	}
	t.Log("fixed matrix: 138 distinct grants, create 123 + configure 109 = 232 fault calls; 25 unused journal grants / 50 positive-control calls")
}

func accountDMLRoleBoundary(t *testing.T, f *fixture) {
	t.Helper()
	var safe bool
	must(t, "observe exact NOLOGIN writer boundary", f.owner.QueryRow(f.ctx, `SELECT
 NOT r.rolcanlogin AND NOT r.rolinherit AND NOT r.rolsuper AND NOT r.rolbypassrls
 AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
 AND NOT EXISTS(SELECT FROM pg_catalog.pg_auth_members m WHERE m.member=r.oid OR m.roleid=r.oid)
 AND NOT EXISTS(SELECT FROM pg_catalog.pg_namespace n WHERE n.nspname=$2 AND n.nspowner=r.oid)
 AND NOT pg_catalog.has_schema_privilege(r.oid,$2,'CREATE')
 AND NOT EXISTS(SELECT FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$2 AND c.relowner=r.oid)
 FROM pg_catalog.pg_roles r WHERE r.rolname=$1`, f.bindings.Writer, f.bindings.Schema).Scan(&safe))
	if !safe {
		t.Fatal("writer has a login, ownership, membership or schema capability outside the fixed fixture")
	}
	var roots int
	must(t, "observe original W-owned definer roots", f.owner.QueryRow(f.ctx, `SELECT count(*) FROM pg_catalog.pg_proc p
 JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname=$1 AND p.proname IN ('pp_create_account','pp_configure_account')
 AND p.proowner=$2::oid AND p.prosecdef`, f.bindings.Schema, f.bindings.WriterOID).Scan(&roots))
	if roots != 2 {
		t.Fatal("actual root ownership differs from fixed W definer contract")
	}
}

// Snapshot every relation and column ACL on this relation, including unrelated
// grantees. aclitem order is immaterial; grantor/grantee/options are not.
func accountDMLACL(ctx context.Context, f *fixture, table string) (string, error) {
	var snapshot string
	err := f.owner.QueryRow(ctx, `WITH object AS (
 SELECT c.oid,c.relowner,c.relacl FROM pg_catalog.pg_class c
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2
), entries AS (
 SELECT 0::smallint AS attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable
 FROM object o CROSS JOIN LATERAL pg_catalog.aclexplode(coalesce(o.relacl,pg_catalog.acldefault('r',o.relowner))) x
 UNION ALL
 SELECT a.attnum,x.grantor,x.grantee,x.privilege_type,x.is_grantable
 FROM object o JOIN pg_catalog.pg_attribute a ON a.attrelid=o.oid AND a.attnum>0 AND NOT a.attisdropped
 CROSS JOIN LATERAL pg_catalog.aclexplode(a.attacl) x
)
 SELECT coalesce(jsonb_agg(jsonb_build_array(attnum,grantor::text,grantee::text,privilege_type,is_grantable)
 ORDER BY attnum,grantor,grantee,privilege_type,is_grantable),'[]'::jsonb)::text FROM entries`, f.bindings.Schema, table).Scan(&snapshot)
	return snapshot, err
}

// This helper targets W explicitly. The older deferred helper intentionally
// targets APP and cannot prove a writer DML failure.
func accountDMLRevoke(t *testing.T, f *fixture, p accountDMLPrivilege) func() error {
	t.Helper()
	if p.privilege != "INSERT" && p.privilege != "UPDATE" {
		t.Fatal("unexpected fixed DML privilege")
	}
	name := pgx.Identifier{f.bindings.Schema, p.table}.Sanitize()
	var generated string
	var owned bool
	must(t, "observe fixed non-generated writer target", f.owner.QueryRow(f.ctx, `SELECT a.attgenerated::text,c.relowner=$3::oid
 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
 WHERE n.nspname=$1 AND c.relname=$2 AND a.attname=$4 AND a.attnum>0 AND NOT a.attisdropped`,
		f.bindings.Schema, p.table, f.bindings.WriterOID, p.column).Scan(&generated, &owned))
	if generated != "" || owned {
		t.Fatal("DML matrix target is generated or implicitly authorized by ownership")
	}
	before, err := accountDMLACL(f.ctx, f, p.table)
	must(t, "snapshot complete relation/column ACL", err)
	check := func(ctx context.Context, want bool) error {
		var actual bool
		if err := f.owner.QueryRow(ctx, `SELECT pg_catalog.has_column_privilege($1,$2,$3,$4)`,
			f.bindings.Writer, name, p.column, p.privilege).Scan(&actual); err != nil {
			return err
		}
		if actual != want {
			return fmt.Errorf("W %s(%s) on %s: effective permission want %t, got %t; baseline ACL=%s", p.privilege, p.column, name, want, actual, before)
		}
		return nil
	}
	must(t, "writer effective grant before revocation", check(f.ctx, true))
	clause := p.privilege + " (" + pgx.Identifier{p.column}.Sanitize() + ") ON TABLE " + name
	role := pgx.Identifier{f.bindings.Writer}.Sanitize()
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := f.owner.Exec(ctx, "GRANT "+clause+" TO "+role); err != nil {
			return err
		}
		if err := check(ctx, true); err != nil {
			return err
		}
		after, err := accountDMLACL(ctx, f, p.table)
		if err != nil {
			return err
		}
		if after != before {
			return fmt.Errorf("exact relation/column ACL not restored for %s.%s; before=%s after=%s", p.table, p.column, before, after)
		}
		restored = true
		return nil
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Errorf("independent exact writer grant cleanup: %v", err)
		}
	})
	_, err = f.owner.Exec(f.ctx, "REVOKE "+clause+" FROM "+role)
	must(t, "revoke only fixed writer column grant", err)
	must(t, "writer effective grant after revocation", check(f.ctx, false))
	return restore
}

func accountDMLBegin(t *testing.T, f *fixture) pgx.Tx {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	must(t, "begin real APP DML transaction", err)
	t.Cleanup(func() { accountDMLRollback(t, tx) })
	var session, current, isolation string
	must(t, "observe actual APP transaction identity", tx.QueryRow(f.ctx, `SELECT session_user::text,current_user::text,current_setting('transaction_isolation')`).Scan(&session, &current, &isolation))
	if session != f.bindings.App || current != f.bindings.App || isolation != "read committed" {
		t.Fatal("DML fault did not originate from actual APP READ COMMITTED")
	}
	return tx
}

func accountDMLRollback(t *testing.T, tx pgx.Tx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Errorf("independent APP DML rollback: %v", err)
	}
}

func accountDMLInvoke(f *fixture, tx pgx.Tx, kind, target, operation string) (receipt, error) {
	if kind == "create" {
		return scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,'dml-next','next-provider','active',800)`, f.admin, operation))
	}
	return scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_configure_account($1,$2,$3,2,'dml-next','next-provider','active',800)`, f.admin, operation, target))
}

func accountDMLReceipt(t *testing.T, f *fixture, r receipt, kind, operation, target string) {
	t.Helper()
	if r.Outcome != "applied" || r.Command.Status != "applied" || r.Command.Reason != "" ||
		r.Command.Kind != "account_"+kind || r.Command.OperationID != operation || r.Command.Actor != f.admin ||
		r.Command.TargetID == "" || r.Root == "" || r.Journal == nil || *r.Journal == "" ||
		(kind == "configure" && r.Command.TargetID != target) {
		t.Fatal("DML control did not return a new applied account receipt")
	}
}

func accountDMLBaseline(t *testing.T, f *fixture) string {
	t.Helper()
	accountDMLRoleBoundary(t, f)
	createOp := uuid.NewString()
	created, err := f.create(t, procurement.CreateInput{OperationID: createOp, Label: "dml-baseline", Provider: "baseline-provider", State: "active", LimitAmount: "1000"})
	must(t, "COMMIT actual APP create DML control", err)
	accountDMLReceipt(t, f, created, "create", createOp, "")
	configureOp := uuid.NewString()
	configured, err := f.configure(t, created.Command.TargetID, procurement.ConfigurationInput{
		OperationID: configureOp, ExpectedRevision: "1", Label: "dml-configured", Provider: "configured-provider", State: "paused", LimitAmount: "900",
	})
	must(t, "COMMIT actual APP changed configure DML control", err)
	accountDMLReceipt(t, f, configured, "configure", configureOp, created.Command.TargetID)
	var valid bool
	accounts := pgx.Identifier{f.bindings.Schema, "procurement_accounts"}.Sanitize()
	heads := pgx.Identifier{f.bindings.Schema, "pp_account_heads"}.Sanitize()
	journals := pgx.Identifier{f.bindings.Schema, "pp_account_journal"}.Sanitize()
	must(t, "observe committed changed configuration and history", f.owner.QueryRow(f.ctx, `SELECT
 a.revision=2 AND a.label='dml-configured' AND a.provider='configured-provider' AND a.state='paused' AND a.limit_amount=900
 AND h.ordinal=2 AND h.configuration_revision=2 AND j.ordinal=2 AND j.kind='configure' AND j.after_configuration_revision=2
 FROM `+accounts+` a JOIN `+heads+` h ON h.account_id=a.id JOIN `+journals+` j ON j.id=h.journal_id WHERE a.id=$1`, created.Command.TargetID).Scan(&valid))
	if !valid {
		t.Fatal("configure control did not commit an actual changed revision and journal")
	}
	return created.Command.TargetID
}

func accountDMLSuccessfulRollback(t *testing.T, f *fixture, kind, target string) string {
	t.Helper()
	op := uuid.NewString()
	tx := accountDMLBegin(t, f)
	r, err := accountDMLInvoke(f, tx, kind, target, op)
	must(t, "execute actual APP DML success control", err)
	accountDMLReceipt(t, f, r, kind, op, target)
	accountDMLRollback(t, tx)
	return op
}

func TestPostgresAccountWriterDMLPermissionMatrix(t *testing.T) {
	f := newFixture(t)
	target := accountDMLBaseline(t, f)
	matrix := accountDMLMatrix()
	if len(matrix) != 232 {
		t.Fatal("fixed DML fault count changed")
	}
	t.Log("232 actual APP root failures: create 123, changed configure 109; 138 distinct W column grants")
	for _, item := range matrix {
		t.Run(item.kind+"/"+item.privilege+"/"+item.table+"/"+item.column, func(t *testing.T) {
			before := accountFaultSnapshot(t, f)
			restore := accountDMLRevoke(t, f, item.accountDMLPrivilege)
			tx := accountDMLBegin(t, f)
			op := uuid.NewString()
			_, err := accountDMLInvoke(f, tx, item.kind, target, op)
			state(t, "42501", err) // Preserve the root error, not aborted COMMIT.
			accountDMLRollback(t, tx)
			must(t, "restore only original writer column grant and exact ACL", restore())
			accountFaultUnchanged(t, f, before, op)
			probe := accountDMLSuccessfulRollback(t, f, item.kind, target)
			accountFaultUnchanged(t, f, before, op, probe)
		})
	}
}

func TestPostgresAccountWriterDMLUnusedJournalControls(t *testing.T) {
	f := newFixture(t)
	target := accountDMLBaseline(t, f)
	columns := strings.Split(accountDMLUnusedJournalColumns, ",")
	if len(columns) != 25 {
		t.Fatal("fixed unused journal grant count changed")
	}
	t.Log("25 independently revoked unused journal INSERT grants, 50 actual APP success-and-rollback controls")
	for _, column := range columns {
		t.Run(column, func(t *testing.T) {
			before := accountFaultSnapshot(t, f)
			restore := accountDMLRevoke(t, f, accountDMLPrivilege{"pp_account_journal", column, "INSERT"})
			create := accountDMLSuccessfulRollback(t, f, "create", target)
			accountFaultUnchanged(t, f, before, create)
			configure := accountDMLSuccessfulRollback(t, f, "configure", target)
			accountFaultUnchanged(t, f, before, create, configure)
			must(t, "restore unused original journal grant and exact ACL", restore())
		})
	}
}
