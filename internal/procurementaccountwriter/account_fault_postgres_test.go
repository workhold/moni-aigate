package procurementaccountwriter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// The owner observes complete rows, including generated evidence, independently
// of APP's deliberately incomplete column grants. No business action uses owner.
var accountFaultTables = []string{
	"procurement_accounts", "procurement_commands", "audit_events",
	"pp_operation_roots", "pp_root_steps", "pp_account_command_sources",
	"pp_account_origins", "pp_account_heads", "pp_account_journal",
}

func accountFaultSnapshot(t *testing.T, f *fixture) map[string]string {
	t.Helper()
	result := make(map[string]string, len(accountFaultTables))
	for _, table := range accountFaultTables {
		var value string
		name := pgx.Identifier{f.bindings.Schema, table}.Sanitize()
		must(t, "snapshot complete "+table, f.owner.QueryRow(f.ctx,
			`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb)::text FROM `+name+` x`).Scan(&value))
		result[table] = value
	}
	return result
}

func accountFaultUnchanged(t *testing.T, f *fixture, before map[string]string, operations ...string) {
	t.Helper()
	after := accountFaultSnapshot(t, f)
	for _, table := range accountFaultTables {
		if before[table] != after[table] {
			t.Errorf("failed root changed complete %s rows", table)
		}
	}
	var n int
	commands := pgx.Identifier{f.bindings.Schema, "procurement_commands"}.Sanitize()
	sources := pgx.Identifier{f.bindings.Schema, "pp_account_command_sources"}.Sanitize()
	must(t, "observe absent failed operations", f.owner.QueryRow(f.ctx,
		`SELECT (SELECT count(*) FROM `+commands+` WHERE operation_id=ANY($1::text[])) +
 (SELECT count(*) FROM `+sources+` WHERE operation_id=ANY($1::text[]))`, operations).Scan(&n))
	if n != 0 {
		t.Fatal("failed operation retained command or typed source")
	}
}

func accountFaultRollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

type accountFaultPrivilege struct {
	name, object, privilege, probe string
	args                           []any
}

// Each case must remove an effective grant, rather than REVOKE a column whose
// table-level or inherited privilege still authorizes the deferred validator.
func accountFaultRevoke(t *testing.T, f *fixture, p accountFaultPrivilege) func() {
	t.Helper()
	check := func(ctx context.Context, want bool) error {
		var actual bool
		if err := f.owner.QueryRow(ctx, p.probe, p.args...).Scan(&actual); err != nil {
			return err
		}
		if actual != want {
			return fmt.Errorf("effective permission for %s: want %t, got %t", p.name, want, actual)
		}
		return nil
	}
	must(t, "effective permission before revocation", check(f.ctx, true))
	role := pgx.Identifier{f.bindings.App}.Sanitize()
	_, err := f.owner.Exec(f.ctx, "REVOKE "+p.privilege+" ON "+p.object+" FROM "+role)
	must(t, "revoke exact "+p.name, err)
	restored := false
	restore := func() {
		if restored {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := f.owner.Exec(ctx, "GRANT "+p.privilege+" ON "+p.object+" TO "+role); err != nil {
			t.Errorf("restore exact %s: %v", p.name, err)
			return
		}
		if err := check(ctx, true); err != nil {
			t.Error(err)
			return
		}
		restored = true
	}
	t.Cleanup(restore)
	must(t, "effective permission after revocation", check(f.ctx, false))
	return restore
}

func TestPostgresAccountWriterDeferredPermissionMatrix(t *testing.T) {
	f := newFixture(t)
	var matrix []accountFaultPrivilege
	for _, signature := range []string{
		"pp_ac_validate_root_v1(text)", "pp_ac_validate_command_v1(text)", "pp_ac_validate_account_v1(text)",
		"pp_ac_uuid_v1(text,boolean)", "pp_ac_text_v1(text)",
		"pp_ac_command_digest_v1(text,text,text,bigint,text,text,text,bigint)", "pp_ac_time_v1(timestamptz)",
		"pp_ac_snapshot_v1(text,text,text,text,bigint,numeric,numeric,bigint,timestamptz)", "pp_json_string_v1(text)",
	} {
		i := strings.IndexByte(signature, '(')
		fn := pgx.Identifier{f.bindings.Schema, signature[:i]}.Sanitize() + signature[i:]
		matrix = append(matrix, accountFaultPrivilege{
			name: signature, object: "FUNCTION " + fn, privilege: "EXECUTE",
			probe: `SELECT pg_catalog.has_function_privilege($1,$2,'EXECUTE')`, args: []any{f.bindings.App, fn},
		})
	}
	// SELECT * in the frozen validators requires every physical column of these
	// eight relations. Discover the exact installed columns; do not infer them
	// from a passing subset or alter the grant surface to manufacture denial.
	for _, table := range accountFaultTables {
		if table == "pp_root_steps" {
			continue
		}
		rows, err := f.owner.Query(f.ctx, `SELECT a.attname FROM pg_catalog.pg_attribute a
 JOIN pg_catalog.pg_class c ON c.oid=a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, f.bindings.Schema, table)
		must(t, "enumerate frozen validator columns", err)
		columns, err := pgx.CollectRows(rows, pgx.RowTo[string])
		must(t, "collect validator columns", err)
		if len(columns) == 0 {
			t.Fatal("validator table has no columns")
		}
		for _, column := range columns {
			matrix = append(matrix, accountFaultColumn(f, table, column))
		}
	}
	for _, group := range []struct{ table, columns string }{
		{"pp_root_steps", "root_id,step_no,kind,source_id,created_xid"},
		{"procurement_reservations", "account_id,state,anomaly,held_amount,spent_amount"},
		{"procurement_budget_events", "account_id,held_delta,spent_delta"},
		{"projection_outbox", "pp_root_id"},
	} {
		for _, column := range strings.Split(group.columns, ",") {
			matrix = append(matrix, accountFaultColumn(f, group.table, column))
		}
	}
	for _, table := range []string{
		"pp_request_origins", "pp_attempt_origins", "pp_attempt_nodes", "pp_attempt_delivery_sources",
		"pp_incoming_sources", "pp_meter_choices", "pp_meter_origins", "pp_conflict_sources", "pp_request_decisions",
		"pp_normal_cost_decisions", "pp_cost_mutations", "pp_cost_reset_decisions", "pp_evidence_choices", "pp_evidence_members",
		"pp_procurement_transitions", "pp_proposal_births", "pp_approval_consumptions", "pp_unmanaged_correction_sources",
		"pp_recovery_sources", "pp_cost_outbox_links", "pp_reconciliation_sources", "pp_customer_finish_outcomes",
		"pp_customer_finish_ledger_effects", "pp_customer_finish_pb_effects",
	} {
		matrix = append(matrix, accountFaultColumn(f, table, "root_id"))
	}
	t.Logf("deferred permission matrix: %d independently revoked effective grants", len(matrix))
	for _, p := range matrix {
		t.Run(p.name, func(t *testing.T) {
			before := accountFaultSnapshot(t, f)
			tx, err := f.app.Begin(f.ctx)
			must(t, "begin actual APP root", err)
			defer accountFaultRollback(tx)
			op := uuid.NewString()
			_, err = scanReceipt(tx.QueryRow(f.ctx, `SELECT * FROM pp_create_account($1,$2,'fault','fixture','active',1)`, f.admin, op))
			must(t, "finish valid root before revocation", err)
			restore := accountFaultRevoke(t, f, p)
			state(t, "42501", tx.Commit(f.ctx))
			restore()
			accountFaultUnchanged(t, f, before, op)
		})
	}
}

func accountFaultColumn(f *fixture, table, column string) accountFaultPrivilege {
	name := pgx.Identifier{f.bindings.Schema, table}.Sanitize()
	return accountFaultPrivilege{
		name: table + "." + column, object: "TABLE " + name, privilege: "SELECT (" + pgx.Identifier{column}.Sanitize() + ")",
		probe: `SELECT pg_catalog.has_column_privilege($1,$2,$3,'SELECT')`, args: []any{f.bindings.App, name, column},
	}
}

func TestPostgresAccountWriterLockFaultMatrix(t *testing.T) {
	f := newFixture(t)
	created, err := f.create(t, procurement.CreateInput{OperationID: uuid.NewString(), Label: "baseline", Provider: "fixture", State: "active", LimitAmount: "1000"})
	must(t, "commit real account baseline", err)
	for _, kind := range []string{"operation", "account"} {
		for _, cancelQuery := range []bool{false, true} {
			mode := "timeout"
			if cancelQuery {
				mode = "cancel"
			}
			t.Run(kind+"_"+mode, func(t *testing.T) {
				before := accountFaultSnapshot(t, f)
				first, err := f.app.Begin(f.ctx)
				must(t, "begin first actual APP", err)
				defer accountFaultRollback(first)
				second, err := f.app.Begin(f.ctx)
				must(t, "begin waiting actual APP", err)
				defer accountFaultRollback(second)
				op1, op2 := uuid.NewString(), uuid.NewString()
				query := `SELECT * FROM pp_create_account($1,$2,'waiting','fixture','active',1)`
				args1, args2 := []any{f.admin, op1}, []any{f.admin, op1}
				if kind == "operation" {
					op2 = op1
				} else {
					query = `SELECT * FROM pp_configure_account($1,$2,$3,1,'uncommitted','fixture','active',900)`
					args1, args2 = []any{f.admin, op1, created.Command.TargetID}, []any{f.admin, op2, created.Command.TargetID}
				}
				_, err = scanReceipt(first.QueryRow(f.ctx, query, args1...))
				must(t, "hold real root lock without COMMIT", err)
				queryCtx, stop := context.WithTimeout(f.ctx, 10*time.Second)
				done := make(chan error, 1)
				finished := make(chan struct{})
				go func() {
					defer close(finished)
					_, err := scanReceipt(second.QueryRow(queryCtx, query, args2...))
					done <- err
				}()
				defer func() {
					stop()
					accountFaultRollback(first)
					<-finished
				}()
				must(t, "observe exact real blocker", waitForBlock(f, second.Conn().PgConn().PID(), first.Conn().PgConn().PID()))
				want := "55P03"
				if cancelQuery {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					err := second.Conn().PgConn().CancelRequest(ctx)
					cancel()
					must(t, "cancel exact blocked test connection", err)
					want = "57014"
				}
				state(t, want, <-done)
				accountFaultRollback(second)
				accountFaultRollback(first)
				accountFaultUnchanged(t, f, before, op1, op2)
			})
		}
	}
}
