package procurementattemptbirth_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
)

func TestPostgresA2ActualBirthEveryDMLBoundaryRollsBack(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 100000)
	f.bind(t, a.TargetID)
	commander, err := periodicbudget.NewCommander(f.app)
	must(t, "construct Commander", err)
	_, err = commander.ConfigureProcurement(f.ctx, f.admin, a.TargetID, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "100000"})
	must(t, "configure real supplier PB", err)
	first := f.reserve(t)
	_, err = f.attempt(first.RequestID)
	must(t, "retain committed managed prefix", err)
	for _, boundary := range []struct{ table, operation string }{
		{"upstream_attempts", "INSERT"}, {"procurement_reservations", "INSERT"},
		{"pp_attempt_origins", "INSERT"}, {"pp_attempt_heads", "INSERT"}, {"pp_procurement_transitions", "INSERT"},
		{"procurement_budget_events", "INSERT"}, {"procurement_accounts", "UPDATE"},
		{"periodic_budget_allocations", "INSERT"}, {"periodic_budget_events", "INSERT"},
		{"pp_account_journal", "INSERT"}, {"pp_account_heads", "UPDATE"},
		{"pp_root_steps", "INSERT"}, {"pp_attempt_nodes", "INSERT"}, {"pp_attempt_heads", "UPDATE"}, {"pp_operation_roots", "INSERT"},
	} {
		t.Run(boundary.table+"_"+boundary.operation, func(t *testing.T) {
			r := f.reserve(t)
			before := f.snapshot(t)
			_, e := f.owner.Exec(f.ctx, `CREATE FUNCTION ab_injected_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'a2_injected_fault' USING ERRCODE='PZ992'; END$$`)
			must(t, "install boundary fault", e)
			table := pgx.Identifier{boundary.table}.Sanitize()
			_, e = f.owner.Exec(f.ctx, "CREATE TRIGGER yy_ab_injected_fault BEFORE "+boundary.operation+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION ab_injected_fault()")
			must(t, "install precise fault trigger", e)
			_, e = f.attempt(r.RequestID)
			sqlstate(t, e, "PZ992")
			_, e = f.owner.Exec(f.ctx, "DROP TRIGGER yy_ab_injected_fault ON "+table)
			must(t, "remove boundary trigger", e)
			_, e = f.owner.Exec(f.ctx, "DROP FUNCTION ab_injected_fault()")
			must(t, "remove boundary function", e)
			if f.snapshot(t) != before {
				t.Fatal("failed attempt changed original/customer/PB/source state")
			}
			_, e = f.app.Exec(f.ctx, "SELECT pp_validate_account($1)", a.TargetID)
			must(t, "previous committed prefix survives", e)
		})
	}
}

func TestPostgresA2BudgetExhaustionHasNoPartialBirth(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 300)
	f.bind(t, a.TargetID)
	r := f.reserve(t)
	_, err := f.attempt(r.RequestID)
	must(t, "first independently affordable U=240", err)
	next := f.reserve(t)
	before := f.snapshot(t)
	_, err = f.attempt(next.RequestID)
	sqlstate(t, err, "PP115")
	if f.snapshot(t) != before {
		t.Fatal("budget rejection left attempted source, p, event, head or balance")
	}
}

func TestPostgresA2NoPrivateOrRawAPPBypass(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	f.bind(t, a.TargetID)
	r := f.reserve(t)
	attempt, err := f.attempt(r.RequestID)
	must(t, "commit authentic birth", err)
	for _, query := range []string{
		"UPDATE upstream_attempts SET delivery_state=delivery_state WHERE id=$1",
		"UPDATE procurement_reservations SET pp_last_journal_id=pp_last_journal_id WHERE attempt_id=$1",
		"UPDATE procurement_accounts SET created_at=created_at WHERE id=(SELECT account_id FROM procurement_reservations WHERE attempt_id=$1)",
		"UPDATE pp_attempt_heads SET ordinal=ordinal WHERE attempt_id=$1",
		"DELETE FROM pp_attempt_origins WHERE attempt_id=$1",
		"SELECT pp_complete_attempt_birth('ignored','ignored',$1)",
		"SELECT pp_account_effect('ignored',$1,'attempt_birth',$1)",
		"SELECT pp_apply_supplier_pb('ignored',$1,'ignored')",
	} {
		t.Run(query, func(t *testing.T) {
			before := f.snapshot(t)
			_, e := f.app.Exec(f.ctx, query, attempt.Attempt)
			if e == nil {
				t.Fatal("raw/private mutation accepted")
			}
			if f.snapshot(t) != before {
				t.Fatal("denied mutation changed state")
			}
		})
	}
	_, err = f.peer.Exec(f.ctx, "SELECT pp_validate_attempt($1)", attempt.Attempt)
	sqlstate(t, err, "42501")
	_, err = f.owner.Exec(f.ctx, "SELECT pp_validate_attempt($1)", attempt.Attempt)
	sqlstate(t, err, "PP901")
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin owner role substitution", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SET LOCAL ROLE "+pgx.Identifier{f.b.App}.Sanitize())
	must(t, "SET ROLE preserves session identity", err)
	_, err = tx.Exec(f.ctx, "SELECT pp_validate_attempt($1)", attempt.Attempt)
	sqlstate(t, err, "PP901")
}
