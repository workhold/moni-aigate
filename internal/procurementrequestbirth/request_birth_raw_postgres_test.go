package procurementrequestbirth_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

// These tests deliberately keep the raw APP write boundary separate from a
// complete billing.Store.Reserve. They never install a fake source as owner.
const rawRequestColumns = "id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at"
const rawRequestValues = "$1,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.request_deadline,r.instance_id,r.streaming,r.route_revision,r.client_ip,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at"

func rawBegin(t *testing.T, f *fixture, level pgx.TxIsoLevel, access pgx.TxAccessMode) pgx.Tx {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: level, AccessMode: access})
	must(t, "begin actual APP raw transaction", err)
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	_, err = tx.Exec(f.ctx, "SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)")
	must(t, "retain original transaction timeout contract", err)
	return tx
}

func rawInsert(f *fixture, tx pgx.Tx, id, reference, extraColumn, extraValue string) error {
	columns, values := rawRequestColumns, rawRequestValues
	if extraColumn != "" {
		columns += "," + pgx.Identifier{extraColumn}.Sanitize()
		values += "," + extraValue
	}
	tag, err := tx.Exec(f.ctx, "INSERT INTO request_records("+columns+") SELECT "+values+" FROM request_records r WHERE r.id=$2", id, reference)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("raw reference produced %d request rows", tag.RowsAffected())
	}
	return err
}

func rawError(t *testing.T, err error, state, message string) {
	t.Helper()
	code(t, err, state)
	if message != "" {
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Message != message {
			t.Fatalf("SQLSTATE %s expected message %q: %v", state, message, err)
		}
	}
}

func rawExactBirth(t *testing.T, f *fixture, tx pgx.Tx, id string) {
	t.Helper()
	var exact bool
	must(t, "observe actual raw birth inside its APP transaction", tx.QueryRow(f.ctx, `SELECT
 (r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at)
 IS NOT DISTINCT FROM
 (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)
 AND (r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid,r.pp_last_decision_id) IS NOT DISTINCT FROM (o.root_id,1::bigint,o.created_xid,NULL::text)
 AND (o.initial_status,o.initial_billing_status,o.initial_latest_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)
 AND (h.ordinal,h.last_decision_id,h.updated_xid) IS NOT DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
 AND (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
 IS NOT DISTINCT FROM ('request_birth'::text,'request'::text,r.tenant_id,r.id,NULL::text,NULL::text,o.created_xid,$2::oid,1::bigint,'born'::text)
 AND (st.step_no,st.kind,st.source_id,st.created_xid,st.d_request_birth) IS NOT DISTINCT FROM (1::bigint,'request_birth'::text,r.id,o.created_xid,r.id)
 AND o.created_xid=pg_current_xact_id() AND r.pb_created_xid=o.created_xid
 AND (SELECT count(*) FROM pp_request_origins WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM request_records WHERE pp_origin_root_id=o.root_id)=1
 AND (SELECT count(*) FROM pp_operation_roots WHERE request_id=r.id)=1
 FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id
 JOIN pp_request_heads h ON h.request_id=r.id JOIN pp_operation_roots rt ON rt.id=o.root_id
 JOIN pp_root_steps st ON st.root_id=o.root_id WHERE r.id=$1`, id, f.b.AppOID).Scan(&exact))
	if !exact {
		t.Fatal("raw statement did not produce its own exact current-xid graph")
	}
	_, err := tx.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1)", id)
	must(t, "actual APP readonly proof before original020 COMMIT", err)
}

// A companion here satisfies the original020 no-control relation contract. It
// is not the wallet/Key/ledger/PB/audit implementation of Store.Reserve.
func rawCompanion(t *testing.T, f *fixture, tx pgx.Tx, id string) {
	t.Helper()
	_, err := tx.Exec(f.ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline)
 SELECT gen_random_uuid()::text,tenant_id,id,120,clock_timestamp()+interval '1 day'
 FROM request_records WHERE id=$1`, id)
	must(t, "write original reserved companion as actual APP", err)
}

func TestPostgresA1RawEquivalentBirthAnd020Boundary(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	f.checkBirth(t, reference.RequestID)
	t.Run("missing_reservation_is_original020_commit_error", func(t *testing.T) {
		before := f.snapshot(t)
		tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		id := uuid.NewString()
		must(t, "equivalent APP21 raw INSERT", rawInsert(f, tx, id, reference.RequestID, "", ""))
		rawExactBirth(t, f, tx, id)
		rawError(t, tx.Commit(f.ctx), "23514", "periodic source reservation missing")
		if f.snapshot(t) != before {
			t.Fatal("original020 failed COMMIT retained raw request or birth rows")
		}
	})
	t.Run("two_actual_raw_births_share_xid_but_not_root", func(t *testing.T) {
		var controls int
		must(t, "fixture has no customer PB controls", f.owner.QueryRow(f.ctx, "SELECT count(*) FROM periodic_budget_controls").Scan(&controls))
		if controls != 0 {
			t.Fatal("minimal raw companion only covers the no-control original020 branch")
		}
		tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		ids := []string{uuid.NewString(), uuid.NewString()}
		for _, id := range ids {
			must(t, "equivalent APP21 raw INSERT", rawInsert(f, tx, id, reference.RequestID, "", ""))
			rawExactBirth(t, f, tx, id)
			rawCompanion(t, f, tx, id)
		}
		var independent bool
		must(t, "two roots and one actual transaction", tx.QueryRow(f.ctx, "SELECT count(*)=2 AND count(DISTINCT root_id)=2 AND count(DISTINCT created_xid)=1 FROM pp_request_origins WHERE request_id=ANY($1::text[])", ids).Scan(&independent))
		if !independent {
			t.Fatal("raw requests borrowed another source/root or xid")
		}
		must(t, "COMMIT raw retained write boundary", tx.Commit(f.ctx))
		for _, id := range ids {
			f.checkBirth(t, id)
		}
		f.checkBirth(t, reference.RequestID)
	})
}

func TestPostgresA1RawIsolationBoundary(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	for _, level := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable, pgx.ReadUncommitted} {
		for _, operation := range []string{"insert", "tracked_update"} {
			t.Run(string(level)+"_"+operation, func(t *testing.T) {
				before := f.snapshot(t)
				tx := rawBegin(t, f, level, pgx.ReadWrite)
				defer tx.Rollback(f.ctx)
				var err error
				if operation == "insert" {
					err = rawInsert(f, tx, uuid.NewString(), reference.RequestID, "", "")
				} else {
					_, err = tx.Exec(f.ctx, "UPDATE request_records SET status=status WHERE id=$1", reference.RequestID)
				}
				rawError(t, err, "PP903", "procurement_execution_protocol")
				must(t, "rollback wrong-isolation statement", tx.Rollback(f.ctx))
				if f.snapshot(t) != before {
					t.Fatal("wrong-isolation write changed rows")
				}
			})
		}
	}
	tx := rawBegin(t, f, pgx.RepeatableRead, pgx.ReadOnly)
	defer tx.Rollback(f.ctx)
	_, err := tx.Exec(f.ctx, "SELECT pp_rb_validate_request_v1($1),pp_rb_validate_root_v1((SELECT pp_origin_root_id FROM request_records WHERE id=$1))", reference.RequestID)
	must(t, "RR readonly remains allowed", err)
	must(t, "commit RR readonly", tx.Commit(f.ctx))
}

type rawPrivilege struct{ table, column, kind string }

func rawHasPrivilege(t *testing.T, f *fixture, p rawPrivilege) bool {
	t.Helper()
	var allowed bool
	table := pgx.Identifier{f.b.Schema, p.table}.Sanitize()
	if p.column == "" {
		must(t, "observe effective table permission", f.owner.QueryRow(f.ctx, "SELECT pg_catalog.has_table_privilege($1::oid,$2::regclass,$3)", f.b.AppOID, table, p.kind).Scan(&allowed))
	} else {
		must(t, "observe effective column permission", f.owner.QueryRow(f.ctx, "SELECT pg_catalog.has_column_privilege($1::oid,$2::regclass,$3,$4)", f.b.AppOID, table, p.column, p.kind).Scan(&allowed))
	}
	return allowed
}

func rawMisgrant(t *testing.T, f *fixture, p rawPrivilege) func() {
	t.Helper()
	if rawHasPrivilege(t, f, p) {
		t.Fatalf("misgrant prerequisite was already effective: %+v", p)
	}
	priv := p.kind
	if p.column != "" {
		priv += "(" + pgx.Identifier{p.column}.Sanitize() + ")"
	}
	statement := priv + " ON TABLE " + pgx.Identifier{f.b.Schema, p.table}.Sanitize()
	_, err := f.owner.Exec(f.ctx, "GRANT "+statement+" TO "+pgx.Identifier{f.b.App}.Sanitize())
	must(t, "temporarily misgrant exact APP permission", err)
	if !rawHasPrivilege(t, f, p) {
		t.Fatal("misgrant did not become effective")
	}
	restored := false
	restore := func() {
		t.Helper()
		if restored {
			return
		}
		_, e := f.owner.Exec(f.ctx, "REVOKE "+statement+" FROM "+pgx.Identifier{f.b.App}.Sanitize())
		must(t, "restore exact APP permission", e)
		if rawHasPrivilege(t, f, p) {
			t.Fatal("misgrant remained effective after restoration")
		}
		restored = true
	}
	t.Cleanup(restore)
	return restore
}

func TestPostgresA1RawFrozenValueMutationsAndRevert(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	other := f.reserve(t)
	var otherRoot string
	must(t, "observe separate committed root", f.owner.QueryRow(f.ctx, "SELECT pp_origin_root_id FROM request_records WHERE id=$1", other.RequestID).Scan(&otherRoot))
	mutations := []struct{ column, expression string }{
		{"id", "gen_random_uuid()::text"}, {"tenant_id", "gen_random_uuid()::text"}, {"key_id", "gen_random_uuid()::text"},
		{"owner_id", "gen_random_uuid()::text"}, {"model_id", "gen_random_uuid()::text"}, {"price_version_id", "gen_random_uuid()::text"},
		{"input_rate", "input_rate+1"}, {"output_rate", "output_rate+1"}, {"input_upper_bound", "input_upper_bound+1"}, {"output_limit", "output_limit+1"},
		{"profile_version", "profile_version||':different'"}, {"policy_version", "policy_version||':different'"}, {"request_hmac", "request_hmac||':different'"},
		{"route_revision", "route_revision+1"}, {"request_deadline", "request_deadline+interval '1 second'"}, {"instance_id", "instance_id||':different'"},
		{"streaming", "NOT streaming"}, {"client_ip", "CASE WHEN client_ip IS NULL THEN '192.0.2.254'::inet ELSE NULL::inet END"},
		{"pricing_group_id", "CASE WHEN pricing_group_id IS NULL THEN gen_random_uuid()::text ELSE NULL::text END"}, {"tenant_pricing_revision", "tenant_pricing_revision+1"},
		{"retail_priced_at", "retail_priced_at+interval '1 second'"}, {"created_at", "created_at+interval '1 second'"},
		{"pb_created_xid", "'0'::xid8"}, {"pb_tenant_control", "CASE WHEN pb_tenant_control IS NULL THEN gen_random_uuid()::text ELSE NULL::text END"},
		{"pb_key_control", "CASE WHEN pb_key_control IS NULL THEN gen_random_uuid()::text ELSE NULL::text END"}, {"pb_admitted_at", "pb_admitted_at+interval '1 second'"},
		{"pp_origin_root_id", "NULL::text"}, {"pp_origin_root_step", "pp_origin_root_step+1"}, {"pp_origin_xid", "'0'::xid8"},
		{"pp_last_decision_id", "gen_random_uuid()::text"}, {"latest_finalization_id", "gen_random_uuid()::text"},
	}
	if len(mutations) != 31 {
		t.Fatal("frozen and pointer inventory changed")
	}
	for _, tc := range mutations {
		t.Run(tc.column, func(t *testing.T) {
			column := pgx.Identifier{tc.column}.Sanitize()
			query := "UPDATE request_records SET " + column + "=" + tc.expression + " WHERE id=$1"
			before := f.snapshot(t)
			_, err := f.app.Exec(f.ctx, query, r.RequestID)
			code(t, err, "42501")
			restore := rawMisgrant(t, f, rawPrivilege{"request_records", tc.column, "UPDATE"})
			tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			defer tx.Rollback(f.ctx)
			_, err = tx.Exec(f.ctx, query, r.RequestID)
			rawError(t, err, "PP902", "procurement_history_invariant")
			_, err = tx.Exec(f.ctx, "UPDATE request_records SET "+column+"="+column+" WHERE id=$1", r.RequestID)
			code(t, err, "25P02")
			must(t, "rollback refused mutation and attempted revert", tx.Rollback(f.ctx))
			restore()
			if f.snapshot(t) != before {
				t.Fatal("refused frozen mutation changed committed rows")
			}
		})
	}
	t.Run("cross_root_and_same_value_boundary", func(t *testing.T) {
		before := f.snapshot(t)
		restore := rawMisgrant(t, f, rawPrivilege{"request_records", "pp_origin_root_id", "UPDATE"})
		_, err := f.app.Exec(f.ctx, "UPDATE request_records SET pp_origin_root_id=$2 WHERE id=$1", r.RequestID, otherRoot)
		rawError(t, err, "PP902", "procurement_history_invariant")
		// The current contract is value immutability, not UPDATE OF rejection.
		_, err = f.app.Exec(f.ctx, "UPDATE request_records SET pp_origin_root_id=pp_origin_root_id WHERE id=$1", r.RequestID)
		must(t, "explicitly documented same-value no-op", err)
		restore()
		if f.snapshot(t) != before {
			t.Fatal("cross-root or no-op changed committed data")
		}
	})
	for _, sql := range []string{"UPDATE request_records SET status='succeeded' WHERE id=$1", "UPDATE request_records SET billing_status='settled' WHERE id=$1"} {
		before := f.snapshot(t)
		_, err := f.app.Exec(f.ctx, sql, r.RequestID)
		rawError(t, err, "PP902", "procurement_history_invariant")
		if f.snapshot(t) != before {
			t.Fatal("unsupported ordinary lifecycle write changed rows")
		}
	}
}

func TestPostgresA1RawDerivedInsertGuardsAndSameXIDLegacy(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	for _, tc := range []struct{ column, value string }{
		{"status", "'succeeded'::text"}, {"billing_status", "'settled'::text"}, {"latest_finalization_id", "gen_random_uuid()::text"},
		{"pp_origin_root_id", "gen_random_uuid()::text"}, {"pp_origin_root_step", "1::bigint"}, {"pp_origin_xid", "pg_current_xact_id()"}, {"pp_last_decision_id", "gen_random_uuid()::text"},
		{"pb_created_xid", "pg_current_xact_id()"}, {"pb_tenant_control", "gen_random_uuid()::text"}, {"pb_key_control", "gen_random_uuid()::text"}, {"pb_admitted_at", "clock_timestamp()"},
	} {
		t.Run(tc.column, func(t *testing.T) {
			before := f.snapshot(t)
			tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			code(t, rawInsert(f, tx, uuid.NewString(), reference.RequestID, tc.column, tc.value), "42501")
			must(t, "rollback permission-layer raw rejection", tx.Rollback(f.ctx))
			restore := rawMisgrant(t, f, rawPrivilege{"request_records", tc.column, "INSERT"})
			tx = rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			defer tx.Rollback(f.ctx)
			rawError(t, rawInsert(f, tx, uuid.NewString(), reference.RequestID, tc.column, tc.value), "PP902", "procurement_history_invariant")
			must(t, "rollback real derived-insert guard rejection", tx.Rollback(f.ctx))
			restore()
			if f.snapshot(t) != before {
				t.Fatal("derived-insert rejection retained request/source rows")
			}
		})
	}
	t.Run("created_at_insert_is_an_acl_boundary", func(t *testing.T) {
		before := f.snapshot(t)
		tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		code(t, rawInsert(f, tx, uuid.NewString(), reference.RequestID, "created_at", "clock_timestamp()"), "42501")
		must(t, "rollback created_at permission rejection", tx.Rollback(f.ctx))
		if f.snapshot(t) != before {
			t.Fatal("created_at ACL rejection changed rows")
		}
	})
	t.Run("same_xid_source_cannot_register_old_request", func(t *testing.T) {
		before := f.snapshot(t)
		restores := make([]func(), 0, 3)
		for _, column := range []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid"} {
			restores = append(restores, rawMisgrant(t, f, rawPrivilege{"request_records", column, "UPDATE"}))
		}
		tx := rawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		fresh := uuid.NewString()
		must(t, "create real same-xid fresh source", rawInsert(f, tx, fresh, reference.RequestID, "", ""))
		rawExactBirth(t, f, tx, fresh)
		_, err := tx.Exec(f.ctx, `UPDATE request_records SET
 pp_origin_root_id=(SELECT root_id FROM pp_request_origins WHERE request_id=$2),
 pp_origin_root_step=1,pp_origin_xid=pg_current_xact_id() WHERE id=$1`, f.legacy, fresh)
		rawError(t, err, "PP902", "procurement_history_invariant")
		must(t, "rollback same-xid old-row registration", tx.Rollback(f.ctx))
		for _, restore := range restores {
			restore()
		}
		if f.snapshot(t) != before {
			t.Fatal("same-xid supplement changed legacy or retained fresh source")
		}
	})
}

func TestPostgresA1RawSourceDMLGuardsAfterMisgrant(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	for _, tc := range []struct{ table, locator, where string }{
		{"pp_request_origins", "request_id", "request_id"}, {"pp_request_heads", "request_id", "request_id"},
		{"pp_operation_roots", "id", "request_id"}, {"pp_root_steps", "root_id", "source_id"},
	} {
		for _, operation := range []string{"INSERT", "UPDATE", "DELETE"} {
			t.Run(tc.table+"_"+operation, func(t *testing.T) {
				before := f.snapshot(t)
				table, locator := pgx.Identifier{tc.table}.Sanitize(), pgx.Identifier{tc.locator}.Sanitize()
				p := rawPrivilege{tc.table, tc.locator, operation}
				var sql string
				switch operation {
				case "INSERT":
					sql = "INSERT INTO " + table + "(" + locator + ") VALUES($1)"
				case "UPDATE":
					sql = "UPDATE " + table + " SET " + locator + "=" + locator + " WHERE " + pgx.Identifier{tc.where}.Sanitize() + "=$1"
				case "DELETE":
					p.column = ""
					sql = "DELETE FROM " + table + " WHERE " + pgx.Identifier{tc.where}.Sanitize() + "=$1"
				}
				_, err := f.app.Exec(f.ctx, sql, r.RequestID)
				code(t, err, "42501")
				restore := rawMisgrant(t, f, p)
				_, err = f.app.Exec(f.ctx, sql, r.RequestID)
				rawError(t, err, "PP901", "procurement_execution_forbidden")
				restore()
				if f.snapshot(t) != before {
					t.Fatal("source DML guard rejection changed committed rows")
				}
			})
		}
	}
	for _, table := range []string{"request_records", "pp_request_origins", "pp_request_heads", "pp_operation_roots", "pp_root_steps"} {
		t.Run(table+"_TRUNCATE", func(t *testing.T) {
			before := f.snapshot(t)
			query := "TRUNCATE " + pgx.Identifier{table}.Sanitize() + " CASCADE"
			_, err := f.app.Exec(f.ctx, query)
			code(t, err, "42501")
			rows, err := f.owner.Query(f.ctx, `WITH RECURSIVE target(oid) AS (
 SELECT $1::regclass::oid UNION SELECT c.conrelid FROM pg_catalog.pg_constraint c
 JOIN target x ON c.confrelid=x.oid WHERE c.contype='f')
 SELECT n.nspname,c.relname FROM target x JOIN pg_catalog.pg_class c ON c.oid=x.oid
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace ORDER BY c.relname`, pgx.Identifier{f.b.Schema, table}.Sanitize())
			must(t, "enumerate exact FK CASCADE closure", err)
			var closure []string
			for rows.Next() {
				var schema, name string
				must(t, "scan CASCADE closure", rows.Scan(&schema, &name))
				if schema != f.b.Schema {
					rows.Close()
					t.Fatal("TRUNCATE closure escaped isolated fixture")
				}
				closure = append(closure, name)
			}
			rows.Close()
			must(t, "read complete CASCADE closure", rows.Err())
			var restores []func()
			for _, name := range closure {
				restores = append(restores, rawMisgrant(t, f, rawPrivilege{name, "", "TRUNCATE"}))
			}
			_, err = f.app.Exec(f.ctx, query)
			rawError(t, err, "PP901", "procurement_execution_forbidden")
			for _, restore := range restores {
				restore()
			}
			if f.snapshot(t) != before {
				t.Fatal("TRUNCATE statement guard changed committed rows")
			}
		})
	}
}

func TestPostgresA1RawExactLinkFaultsInsideRealBirth(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	var otherRoot string
	must(t, "read committed root for wrong-root attacks", f.owner.QueryRow(f.ctx, "SELECT pp_origin_root_id FROM request_records WHERE id=$1", r.RequestID).Scan(&otherRoot))
	literal := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	for _, tc := range []struct{ table, column, expression string }{
		{"pp_request_origins", "request_id", literal(r.RequestID)}, {"pp_request_origins", "tenant_id", "gen_random_uuid()::text"},
		{"pp_request_origins", "root_id", literal(otherRoot)}, {"pp_request_origins", "input_upper_bound", "NEW.input_upper_bound+1"},
		{"pp_request_origins", "client_ip", "'192.0.2.222'::text"}, {"pp_request_origins", "input_rate", "NULL::bigint"}, {"pp_request_origins", "created_xid", "'0'::xid8"},
		{"pp_request_heads", "request_id", literal(r.RequestID)}, {"pp_request_heads", "ordinal", "1::bigint"},
		{"pp_request_heads", "last_decision_id", "gen_random_uuid()::text"}, {"pp_request_heads", "updated_xid", "'0'::xid8"},
		{"pp_root_steps", "root_id", literal(otherRoot)}, {"pp_root_steps", "source_id", literal(r.RequestID)},
		{"pp_root_steps", "kind", "'account_command'::text"}, {"pp_root_steps", "step_no", "2::bigint"}, {"pp_root_steps", "created_xid", "'0'::xid8"},
		{"pp_operation_roots", "request_id", literal(r.RequestID)}, {"pp_operation_roots", "tenant_id", "gen_random_uuid()::text"},
		{"pp_operation_roots", "semantic_result", "'unknown'::text"}, {"pp_operation_roots", "step_count", "2::bigint"},
		{"pp_operation_roots", "created_xid", "'0'::xid8"}, {"pp_operation_roots", "id", literal(otherRoot)},
	} {
		t.Run(tc.table+"_"+tc.column, func(t *testing.T) {
			before := f.snapshot(t)
			fn := pgx.Identifier{f.b.Schema, "rb_raw_fault"}.Sanitize()
			table := pgx.Identifier{f.b.Schema, tc.table}.Sanitize()
			_, err := f.owner.Exec(f.ctx, "CREATE FUNCTION "+fn+"() RETURNS trigger LANGUAGE plpgsql AS $raw_fault$ BEGIN NEW."+pgx.Identifier{tc.column}.Sanitize()+" := "+tc.expression+"; RETURN NEW; END $raw_fault$")
			must(t, "install exact NEW mutation fault", err)
			_, err = f.owner.Exec(f.ctx, "CREATE TRIGGER aa0_rb_raw_fault BEFORE INSERT ON "+table+" FOR EACH ROW EXECUTE FUNCTION "+fn+"()")
			must(t, "place fault before actual A1 guard", err)
			removed := false
			remove := func() {
				if removed {
					return
				}
				_, e := f.owner.Exec(f.ctx, "DROP TRIGGER aa0_rb_raw_fault ON "+table)
				must(t, "remove exact NEW mutation trigger", e)
				_, e = f.owner.Exec(f.ctx, "DROP FUNCTION "+fn+"()")
				must(t, "remove exact NEW mutation function", e)
				removed = true
			}
			t.Cleanup(remove)
			_, err = f.app.Exec(f.ctx, "SELECT 1")
			must(t, "actual APP remains ordinary LOGIN", err)
			_, err = billing.New(f.app).Reserve(f.ctx, f.input())
			rawError(t, err, "PP902", "procurement_history_invariant")
			remove()
			if f.snapshot(t) != before {
				t.Fatal("true birth fault retained half graph or original Reserve effects")
			}
			f.checkBirth(t, r.RequestID)
		})
	}
	f.reserve(t)
}
