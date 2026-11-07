package procurementattemptbirth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/provider"
)

func historicalPublishPrice(ctx context.Context, store *catalog.Store, actor, model, input, output string, effective time.Time) (string, error) {
	in, err := strconv.ParseInt(input, 10, 64)
	if err != nil {
		return "", err
	}
	out, err := strconv.ParseInt(output, 10, 64)
	if err != nil {
		return "", err
	}
	var schema, digest string
	if err = store.Pool.QueryRow(ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex') FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schema, &digest); err != nil {
		return "", err
	}
	result, err := store.PublishDefaultPrice(ctx, catalog.PricingActor{UserID: actor}, catalog.DefaultPriceCommand{OperationID: uuid.NewString(), ModelID: model, SchemaID: schema, SchemaDigest: digest, VariantKey: "default", InputRate: in, OutputRate: out, EffectiveAt: effective.UTC().Truncate(time.Microsecond)})
	return result.Result.ID, err
}

// A2 differential request acceptance. Every tested mutation uses actual APP;
// owner prepares original parents, observes rows, and injects exact faults.
// This file covers the declared groups below, not the entire historical A1
// suite: concurrent waits, lost COMMIT, all grant deltas, every source attack
// and pre-020 unreserved legacy DELETE remain separate acceptance obligations.

func abCompatInput(f *fixture) billing.ReserveInput {
	return billing.ReserveInput{Principal: billing.Principal{TenantID: f.tenant, UserID: f.user, KeyID: f.key}, RequestID: uuid.NewString(), ModelID: f.model, IdempotencyKey: uuid.NewString(), RequestHMAC: "a2-request-compat", ProfileVersion: "v1", InputUpperBound: 100, OutputLimit: 20, RequestDeadline: time.Now().UTC().Add(5 * time.Minute)}
}

// The current reservation receipt no longer mirrors two prices. Keep this
// frozen fixture's rate evidence on its original request columns, not 024.
func abCompatOriginalRates(t *testing.T, f *fixture, request, input, output string) bool {
	t.Helper()
	var exact bool
	must(t, "read original request rate evidence", f.owner.QueryRow(f.ctx, `SELECT input_rate::text=$2 AND output_rate::text=$3 FROM request_records WHERE id=$1`, request, input, output).Scan(&exact))
	return exact
}

func abCompatLegacyFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	var legacy string
	f := newFixture(t, func(before *fixture) {
		in := abCompatInput(before)
		var policy strings.Builder
		for i := 0; i < 160; i++ {
			sum := sha256.Sum256([]byte(fmt.Sprint(i)))
			policy.WriteString(hex.EncodeToString(sum[:]))
		}
		in.PolicyVersion = policy.String()
		r, e := billing.New(before.owner).Reserve(before.ctx, in)
		must(t, "real original Reserve before A2 installation", e)
		legacy = r.RequestID
	})
	if legacy == "" {
		t.Fatal("before-install legacy callback did not execute")
	}
	return f, legacy
}

const abCompatRawRequestColumns = "id,tenant_id,key_id,owner_id,model_id,price_version_id,input_rate,output_rate,input_upper_bound,output_limit,profile_version,policy_version,request_hmac,request_deadline,instance_id,streaming,route_revision,client_ip,pricing_group_id,tenant_pricing_revision,retail_priced_at"
const abCompatRawRequestValues = "$1,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.request_deadline,r.instance_id,r.streaming,r.route_revision,r.client_ip,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at"

func abCompatCheckBirth(t *testing.T, f *fixture, id string) {
	t.Helper()
	var exact bool
	must(t, "independent full birth mapping", f.owner.QueryRow(f.ctx, `SELECT
 (o.request_id,o.tenant_id,o.key_id,o.owner_id,o.model_id,o.price_version_id,o.input_rate,o.output_rate,o.input_upper_bound,o.output_limit,o.profile_version,o.policy_version,o.request_hmac,o.route_revision,o.request_deadline,o.instance_id,o.streaming,o.client_ip,o.pricing_group_id,o.tenant_pricing_revision,o.retail_priced_at,o.request_created_at,o.pb_created_xid,o.pb_tenant_control,o.pb_key_control,o.pb_admitted_at)
 IS NOT DISTINCT FROM
 (r.id,r.tenant_id,r.key_id,r.owner_id,r.model_id,r.price_version_id,r.input_rate,r.output_rate,r.input_upper_bound,r.output_limit,r.profile_version,r.policy_version,r.request_hmac,r.route_revision,r.request_deadline,r.instance_id,r.streaming,r.client_ip::text,r.pricing_group_id,r.tenant_pricing_revision,r.retail_priced_at,r.created_at,r.pb_created_xid,r.pb_tenant_control,r.pb_key_control,r.pb_admitted_at)
 AND (r.pp_origin_root_id,r.pp_origin_root_step,r.pp_origin_xid,r.pp_last_decision_id) IS NOT DISTINCT FROM (o.root_id,1::bigint,o.created_xid,NULL::text)
 AND (o.initial_status,o.initial_billing_status,o.initial_latest_id) IS NOT DISTINCT FROM ('accepted'::text,'reserved'::text,NULL::text)
 AND (h.ordinal,h.last_decision_id,h.updated_xid) IS NOT DISTINCT FROM (0::bigint,NULL::text,o.created_xid)
 AND (rt.entry_kind,rt.scope_kind,rt.tenant_id,rt.request_id,rt.attempt_id,rt.account_target_id,rt.created_xid,rt.session_role_oid,rt.step_count,rt.semantic_result)
 IS NOT DISTINCT FROM ('request_birth'::text,'request'::text,r.tenant_id,r.id,NULL::text,NULL::text,o.created_xid,$2::oid,1::bigint,'born'::text)
 AND (st.step_no,st.kind,st.source_id,st.created_xid) IS NOT DISTINCT FROM (1::bigint,'request_birth'::text,r.id,o.created_xid)
 AND (SELECT count(*) FROM pp_request_origins WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM pp_root_steps WHERE root_id=o.root_id)=1
 AND (SELECT count(*) FROM request_records WHERE pp_origin_root_id=o.root_id)=1
 FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id JOIN pp_request_heads h ON h.request_id=r.id JOIN pp_operation_roots rt ON rt.id=o.root_id JOIN pp_root_steps st ON st.root_id=o.root_id WHERE r.id=$1`, id, f.b.AppOID).Scan(&exact))
	if !exact {
		t.Fatal("actual source graph differs from original frozen facts")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pp_validate_request($1)", id)
	must(t, "APP invoker readonly proof", err)
}

func abCompatRawBegin(t *testing.T, f *fixture, level pgx.TxIsoLevel, access pgx.TxAccessMode) pgx.Tx {
	t.Helper()
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: level, AccessMode: access})
	must(t, "begin actual APP raw transaction", err)
	t.Cleanup(func() { _ = tx.Rollback(f.ctx) })
	_, err = tx.Exec(f.ctx, "SELECT set_config('lock_timeout','3s',true),set_config('statement_timeout','5s',true)")
	must(t, "retain original transaction timeout contract", err)
	return tx
}

func abCompatRawInsert(f *fixture, tx pgx.Tx, id, reference, extraColumn, extraValue string) error {
	columns, values := abCompatRawRequestColumns, abCompatRawRequestValues
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

func abCompatRawError(t *testing.T, err error, state, message string) {
	t.Helper()
	sqlstate(t, err, state)
	if message != "" {
		var p *pgconn.PgError
		if !errors.As(err, &p) || p.Message != message {
			t.Fatalf("SQLSTATE %s expected message %q: %v", state, message, err)
		}
	}
}

func abCompatRawExactBirth(t *testing.T, f *fixture, tx pgx.Tx, id string) {
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
 AND (SELECT count(*) FROM pp_operation_roots WHERE request_id=r.id AND entry_kind='request_birth' AND attempt_id IS NULL)=1
 FROM request_records r JOIN pp_request_origins o ON o.request_id=r.id
 JOIN pp_request_heads h ON h.request_id=r.id JOIN pp_operation_roots rt ON rt.id=o.root_id
 JOIN pp_root_steps st ON st.root_id=o.root_id WHERE r.id=$1`, id, f.b.AppOID).Scan(&exact))
	if !exact {
		t.Fatal("raw statement did not produce its own exact current-xid graph")
	}
	_, err := tx.Exec(f.ctx, "SELECT pp_validate_request($1)", id)
	must(t, "actual APP readonly proof before original020 COMMIT", err)
}

// A companion here satisfies the original020 no-control relation contract. It
// is not the wallet/Key/ledger/PB/audit implementation of Store.Reserve.
func abCompatRawCompanion(t *testing.T, f *fixture, tx pgx.Tx, id string) {
	t.Helper()
	_, err := tx.Exec(f.ctx, `INSERT INTO reservations(id,tenant_id,request_id,amount,deadline)
 SELECT gen_random_uuid()::text,tenant_id,id,120,clock_timestamp()+interval '1 day'
 FROM request_records WHERE id=$1`, id)
	must(t, "write original reserved companion as actual APP", err)
}

func TestPostgresA2RequestCompatRawEquivalentBirthAnd020Boundary(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	abCompatCheckBirth(t, f, reference.RequestID)
	t.Run("missing_reservation_is_original020_commit_error", func(t *testing.T) {
		before := abCompatSnapshot(t, f)
		tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		id := uuid.NewString()
		must(t, "equivalent APP21 raw INSERT", abCompatRawInsert(f, tx, id, reference.RequestID, "", ""))
		abCompatRawExactBirth(t, f, tx, id)
		abCompatRawError(t, tx.Commit(f.ctx), "23514", "periodic source reservation missing")
		if abCompatSnapshot(t, f) != before {
			t.Fatal("original020 failed COMMIT retained raw request or birth rows")
		}
	})
	t.Run("two_actual_raw_births_share_xid_but_not_root", func(t *testing.T) {
		var controls int
		must(t, "fixture has no customer PB controls", f.owner.QueryRow(f.ctx, "SELECT count(*) FROM periodic_budget_controls").Scan(&controls))
		if controls != 0 {
			t.Fatal("minimal raw companion only covers the no-control original020 branch")
		}
		tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		ids := []string{uuid.NewString(), uuid.NewString()}
		for _, id := range ids {
			must(t, "equivalent APP21 raw INSERT", abCompatRawInsert(f, tx, id, reference.RequestID, "", ""))
			abCompatRawExactBirth(t, f, tx, id)
			abCompatRawCompanion(t, f, tx, id)
		}
		var independent bool
		must(t, "two roots and one actual transaction", tx.QueryRow(f.ctx, "SELECT count(*)=2 AND count(DISTINCT root_id)=2 AND count(DISTINCT created_xid)=1 FROM pp_request_origins WHERE request_id=ANY($1::text[])", ids).Scan(&independent))
		if !independent {
			t.Fatal("raw requests borrowed another source/root or xid")
		}
		must(t, "COMMIT raw retained write boundary", tx.Commit(f.ctx))
		for _, id := range ids {
			abCompatCheckBirth(t, f, id)
		}
		abCompatCheckBirth(t, f, reference.RequestID)
	})
}

func TestPostgresA2RequestCompatRawIsolationBoundary(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	for _, level := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable, pgx.ReadUncommitted} {
		for _, operation := range []string{"insert", "tracked_update"} {
			t.Run(string(level)+"_"+operation, func(t *testing.T) {
				before := abCompatSnapshot(t, f)
				tx := abCompatRawBegin(t, f, level, pgx.ReadWrite)
				defer tx.Rollback(f.ctx)
				var err error
				if operation == "insert" {
					err = abCompatRawInsert(f, tx, uuid.NewString(), reference.RequestID, "", "")
				} else {
					_, err = tx.Exec(f.ctx, "UPDATE request_records SET status=status WHERE id=$1", reference.RequestID)
				}
				abCompatRawError(t, err, "PP903", "procurement_execution_protocol")
				must(t, "rollback wrong-isolation statement", tx.Rollback(f.ctx))
				if abCompatSnapshot(t, f) != before {
					t.Fatal("wrong-isolation write changed rows")
				}
			})
		}
	}
	tx := abCompatRawBegin(t, f, pgx.RepeatableRead, pgx.ReadOnly)
	defer tx.Rollback(f.ctx)
	_, err := tx.Exec(f.ctx, "SELECT pp_validate_request($1),pp_validate_root((SELECT pp_origin_root_id FROM request_records WHERE id=$1))", reference.RequestID)
	must(t, "RR readonly remains allowed", err)
	must(t, "commit RR readonly", tx.Commit(f.ctx))
}

type abCompatPrivilege struct{ table, column, kind string }

func abCompatHasPrivilege(t *testing.T, f *fixture, p abCompatPrivilege) bool {
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

func abCompatMisgrant(t *testing.T, f *fixture, p abCompatPrivilege) func() {
	t.Helper()
	if abCompatHasPrivilege(t, f, p) {
		t.Fatalf("misgrant prerequisite was already effective: %+v", p)
	}
	priv := p.kind
	if p.column != "" {
		priv += "(" + pgx.Identifier{p.column}.Sanitize() + ")"
	}
	statement := priv + " ON TABLE " + pgx.Identifier{f.b.Schema, p.table}.Sanitize()
	_, err := f.owner.Exec(f.ctx, "GRANT "+statement+" TO "+pgx.Identifier{f.b.App}.Sanitize())
	must(t, "temporarily misgrant exact APP permission", err)
	if !abCompatHasPrivilege(t, f, p) {
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
		if abCompatHasPrivilege(t, f, p) {
			t.Fatal("misgrant remained effective after restoration")
		}
		restored = true
	}
	t.Cleanup(restore)
	return restore
}

func TestPostgresA2RequestCompatRawFrozenValueMutationsAndRevert(t *testing.T) {
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
			before := abCompatSnapshot(t, f)
			_, err := f.app.Exec(f.ctx, query, r.RequestID)
			sqlstate(t, err, "42501")
			restore := abCompatMisgrant(t, f, abCompatPrivilege{"request_records", tc.column, "UPDATE"})
			tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			defer tx.Rollback(f.ctx)
			_, err = tx.Exec(f.ctx, query, r.RequestID)
			abCompatRawError(t, err, "PP902", "procurement_history_invariant")
			_, err = tx.Exec(f.ctx, "UPDATE request_records SET "+column+"="+column+" WHERE id=$1", r.RequestID)
			sqlstate(t, err, "25P02")
			must(t, "rollback refused mutation and attempted revert", tx.Rollback(f.ctx))
			restore()
			if abCompatSnapshot(t, f) != before {
				t.Fatal("refused frozen mutation changed committed rows")
			}
		})
	}
	t.Run("cross_root_and_same_value_boundary", func(t *testing.T) {
		before := abCompatSnapshot(t, f)
		restore := abCompatMisgrant(t, f, abCompatPrivilege{"request_records", "pp_origin_root_id", "UPDATE"})
		_, err := f.app.Exec(f.ctx, "UPDATE request_records SET pp_origin_root_id=$2 WHERE id=$1", r.RequestID, otherRoot)
		abCompatRawError(t, err, "PP902", "procurement_history_invariant")
		// The current contract is value immutability, not UPDATE OF rejection.
		_, err = f.app.Exec(f.ctx, "UPDATE request_records SET pp_origin_root_id=pp_origin_root_id WHERE id=$1", r.RequestID)
		must(t, "explicitly documented same-value no-op", err)
		restore()
		if abCompatSnapshot(t, f) != before {
			t.Fatal("cross-root or no-op changed committed data")
		}
	})
	for _, sql := range []string{"UPDATE request_records SET status='succeeded' WHERE id=$1", "UPDATE request_records SET billing_status='settled' WHERE id=$1"} {
		before := abCompatSnapshot(t, f)
		_, err := f.app.Exec(f.ctx, sql, r.RequestID)
		abCompatRawError(t, err, "PP902", "procurement_history_invariant")
		if abCompatSnapshot(t, f) != before {
			t.Fatal("unsupported ordinary lifecycle write changed rows")
		}
	}
}

func TestPostgresA2RequestCompatRawDerivedInsertGuardsAndSameXIDLegacy(t *testing.T) {
	f, legacy := abCompatLegacyFixture(t)
	reference := f.reserve(t)
	for _, tc := range []struct{ column, value string }{
		{"status", "'succeeded'::text"}, {"billing_status", "'settled'::text"}, {"latest_finalization_id", "gen_random_uuid()::text"},
		{"pp_origin_root_id", "gen_random_uuid()::text"}, {"pp_origin_root_step", "1::bigint"}, {"pp_origin_xid", "pg_current_xact_id()"}, {"pp_last_decision_id", "gen_random_uuid()::text"},
		{"pb_created_xid", "pg_current_xact_id()"}, {"pb_tenant_control", "gen_random_uuid()::text"}, {"pb_key_control", "gen_random_uuid()::text"}, {"pb_admitted_at", "clock_timestamp()"},
	} {
		t.Run(tc.column, func(t *testing.T) {
			before := abCompatSnapshot(t, f)
			tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			sqlstate(t, abCompatRawInsert(f, tx, uuid.NewString(), reference.RequestID, tc.column, tc.value), "42501")
			must(t, "rollback permission-layer raw rejection", tx.Rollback(f.ctx))
			restore := abCompatMisgrant(t, f, abCompatPrivilege{"request_records", tc.column, "INSERT"})
			tx = abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			defer tx.Rollback(f.ctx)
			abCompatRawError(t, abCompatRawInsert(f, tx, uuid.NewString(), reference.RequestID, tc.column, tc.value), "PP902", "procurement_history_invariant")
			must(t, "rollback real derived-insert guard rejection", tx.Rollback(f.ctx))
			restore()
			if abCompatSnapshot(t, f) != before {
				t.Fatal("derived-insert rejection retained request/source rows")
			}
		})
	}
	t.Run("created_at_insert_is_an_acl_boundary", func(t *testing.T) {
		before := abCompatSnapshot(t, f)
		tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		sqlstate(t, abCompatRawInsert(f, tx, uuid.NewString(), reference.RequestID, "created_at", "clock_timestamp()"), "42501")
		must(t, "rollback created_at permission rejection", tx.Rollback(f.ctx))
		if abCompatSnapshot(t, f) != before {
			t.Fatal("created_at ACL rejection changed rows")
		}
	})
	t.Run("same_xid_source_cannot_register_old_request", func(t *testing.T) {
		before := abCompatSnapshot(t, f)
		restores := make([]func(), 0, 3)
		for _, column := range []string{"pp_origin_root_id", "pp_origin_root_step", "pp_origin_xid"} {
			restores = append(restores, abCompatMisgrant(t, f, abCompatPrivilege{"request_records", column, "UPDATE"}))
		}
		tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
		defer tx.Rollback(f.ctx)
		fresh := uuid.NewString()
		must(t, "create real same-xid fresh source", abCompatRawInsert(f, tx, fresh, reference.RequestID, "", ""))
		abCompatRawExactBirth(t, f, tx, fresh)
		_, err := tx.Exec(f.ctx, `UPDATE request_records SET
 pp_origin_root_id=(SELECT root_id FROM pp_request_origins WHERE request_id=$2),
 pp_origin_root_step=1,pp_origin_xid=pg_current_xact_id() WHERE id=$1`, legacy, fresh)
		abCompatRawError(t, err, "PP902", "procurement_history_invariant")
		must(t, "rollback same-xid old-row registration", tx.Rollback(f.ctx))
		for _, restore := range restores {
			restore()
		}
		if abCompatSnapshot(t, f) != before {
			t.Fatal("same-xid supplement changed legacy or retained fresh source")
		}
	})
}

func abCompatBaseline(t *testing.T, f *fixture) string {
	t.Helper()
	abCompatWallet(t, f, 10000, 0)
	return f.admin
}

func abCompatWallet(t *testing.T, f *fixture, available, held int64) {
	t.Helper()
	store := billing.New(f.app)
	w, err := store.Wallet(f.ctx, billing.Query{TenantID: f.tenant})
	must(t, "read actual APP wallet", err)
	if int64(w.Available) != available || int64(w.Held) != held {
		t.Fatal("single wallet differs from independent reservation arithmetic")
	}
	var keyHeld, keySpent int64
	must(t, "read actual Key budget", f.owner.QueryRow(f.ctx, "SELECT held,spent FROM api_keys WHERE id=$1", f.key).Scan(&keyHeld, &keySpent))
	if keyHeld != held || keySpent != 0 {
		t.Fatal("Key held/spent differs from single customer financial effect")
	}
	ledger, err := store.VerifyLedger(f.ctx, billing.Query{TenantID: f.tenant})
	must(t, "run original ledger and funding verification", err)
	if !ledger.Balanced || ledger.AccountMismatches != 0 || ledger.UnbalancedJournals != 0 {
		t.Fatal("original financial verification failed")
	}
}

func abCompatClock(t *testing.T, f *fixture) time.Time {
	t.Helper()
	var at time.Time
	must(t, "read real database clock", f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&at))
	return at.UTC()
}

func abCompatConfigure(t *testing.T, f *fixture, admin, tenantState, keyState string) map[string]string {
	t.Helper()
	commander, err := periodicbudget.NewCommander(f.app)
	must(t, "construct real APP Commander", err)
	controls := map[string]string{}
	for _, item := range []struct{ scope, state string }{{"tenant", tenantState}, {"key", keyState}} {
		if item.state == "none" {
			continue
		}
		limit := "100000"
		if item.state == "disabled" {
			limit = "0"
		}
		in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_day", Enabled: item.state == "enabled", LimitAmount: limit}
		var result periodicbudget.ConfigureResult
		if item.scope == "tenant" {
			result, err = commander.ConfigurePlatformTenant(f.ctx, admin, f.tenant, in)
		} else {
			result, err = commander.ConfigureKey(f.ctx, f.user, f.tenant, f.key, in)
		}
		must(t, "configure real "+item.scope+" "+item.state, err)
		must(t, "validate original immutable Commander receipt", result.Receipt.Validate())
		if !result.Created || result.Receipt.Operation.Revision != "1" || result.Receipt.Result.Enabled != in.Enabled {
			t.Fatal("new control configuration differs from original API receipt")
		}
		control := result.Receipt.Operation.ResourceID
		controls[item.scope] = control
		var tracking, effective time.Time
		var enabled bool
		var actualLimit string
		must(t, "observe actual control calendar without replacing pb_now", f.owner.QueryRow(f.ctx, "SELECT tracking_started_at,effective_from,enabled,limit_amount::text FROM periodic_budget_controls WHERE id=$1", control).Scan(&tracking, &effective, &enabled, &actualLimit))
		tracking = tracking.UTC()
		next := time.Date(tracking.Year(), tracking.Month(), tracking.Day()+1, 0, 0, 0, 0, time.UTC)
		if !effective.Equal(next) || enabled != in.Enabled || actualLimit != limit {
			t.Fatal("control changed original next-UTC-boundary or enabled/limit contract")
		}
	}
	return controls
}

func abCompatReserve(t *testing.T, f *fixture, in billing.ReserveInput, amount int64) billing.Reservation {
	t.Helper()
	before := abCompatClock(t, f)
	r, err := billing.New(f.app).Reserve(f.ctx, in)
	must(t, "actual APP Reserve under the A2 APP profile", err)
	after := abCompatClock(t, f)
	if int64(r.Amount) != amount {
		t.Fatalf("actual reservation=%d, independent expected=%d", r.Amount, amount)
	}
	abCompatCheckBirth(t, f, r.RequestID)
	var priced, admitted time.Time
	must(t, "observe original separate retail and PB clocks", f.owner.QueryRow(f.ctx, "SELECT retail_priced_at,pb_admitted_at FROM request_records WHERE id=$1", r.RequestID).Scan(&priced, &admitted))
	if priced.Before(before) || priced.After(after) || admitted.Before(before) || admitted.After(after) || !priced.Equal(r.RetailPricedAt) {
		t.Fatal("actual stage clocks are not inside the original invocation")
	}
	abCompatWallet(t, f, 10000-amount, amount)
	return r
}

func abCompatRows(t *testing.T, f *fixture, tables []string) string {
	t.Helper()
	parts := make([]string, 0, len(tables))
	for _, table := range tables {
		parts = append(parts, "('"+table+"',(SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY to_jsonb(x)::text),'[]'::jsonb) FROM "+pgx.Identifier{f.b.Schema, table}.Sanitize()+" x))")
	}
	var value string
	must(t, "read independent full-row business snapshot", f.owner.QueryRow(f.ctx, "SELECT jsonb_object_agg(k,v)::text FROM (VALUES "+strings.Join(parts, ",")+") actual(k,v)").Scan(&value))
	return value
}

func abCompatSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	return f.snapshot(t) + abCompatRows(t, f, []string{"settlements", "reconciliation_cases", "billing_profile_blocks", "periodic_budget_operations", "periodic_budget_configuration_versions", "price_groups", "pricing_operations", "tenant_pricing_assignments", "price_versions"})
}

func abCompatPeriodic(t *testing.T, f *fixture) string {
	t.Helper()
	return abCompatRows(t, f, []string{"periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events"})
}

func abCompatFrozen(t *testing.T, f *fixture, request string) string {
	t.Helper()
	var result string
	must(t, "retain immutable complete birth graph", f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object('origin',to_jsonb(o),'head',to_jsonb(h),'step',to_jsonb(s),'root',to_jsonb(rt))::text
 FROM pp_request_origins o JOIN pp_request_heads h ON h.request_id=o.request_id JOIN pp_root_steps s ON s.root_id=o.root_id AND s.step_no=o.step_no JOIN pp_operation_roots rt ON rt.id=o.root_id WHERE o.request_id=$1`, request).Scan(&result))
	return result
}

// One new request in a fresh scope: every count and amount has an independent
// expected value. Disabled controls still track, and none has no substitute ID.
func abCompatEffects(t *testing.T, f *fixture, r billing.Reservation, controls map[string]string, released bool) {
	t.Helper()
	var tenantMarker, keyMarker *string
	must(t, "read exact nullable request markers", f.owner.QueryRow(f.ctx, "SELECT pb_tenant_control,pb_key_control FROM request_records WHERE id=$1", r.RequestID).Scan(&tenantMarker, &keyMarker))
	for _, item := range []struct {
		scope  string
		marker *string
	}{{"tenant", tenantMarker}, {"key", keyMarker}} {
		id, configured := controls[item.scope]
		if !configured {
			var absent bool
			must(t, "unconfigured scope remains absent", f.owner.QueryRow(f.ctx, "SELECT NOT EXISTS(SELECT 1 FROM periodic_budget_controls WHERE scope=$1 AND tenant_id=$2)", item.scope, f.tenant).Scan(&absent))
			if item.marker != nil || !absent {
				t.Fatal("unconfigured scope acquired a marker or control")
			}
			continue
		}
		if item.marker == nil || *item.marker != id {
			t.Fatal("request marker differs from real configured control identity")
		}
		phase, held, state := int64(1), int64(r.Amount), "held"
		if released {
			phase, held, state = 2, 0, "released"
		}
		var exact bool
		must(t, "independent control/window/allocation/effect conservation", f.owner.QueryRow(f.ctx, `SELECT
 c.held_amount=$3::numeric AND c.spent_amount=0 AND c.event_seq=$4
 AND a.origin='new' AND a.origin_source_id=$5 AND a.upper_amount=$6::bigint AND a.baseline=0
 AND a.held_amount=$3::numeric AND a.spent_amount=0 AND a.source_held=$3::numeric AND a.source_spent=0 AND a.revision=$4 AND a.state=$7
 AND a.admitted_at=r.pb_admitted_at AND w.control_id=c.id AND w.start_at<=r.pb_admitted_at AND r.pb_admitted_at<w.end_at AND w.held_amount=$3::numeric AND w.spent_amount=0
 AND (SELECT count(*) FROM periodic_budget_allocations WHERE control_id=c.id AND subject_id=r.id)=1
 AND (SELECT count(*) FROM periodic_budget_events WHERE control_id=c.id AND allocation_id=a.id)=$4
 AND EXISTS(SELECT 1 FROM periodic_budget_events e WHERE e.control_id=c.id AND e.allocation_id=a.id AND e.source_kind='reservation' AND e.source_id=$5 AND e.allocation_before_revision=0 AND e.before_held=0 AND e.before_spent=0 AND e.after_held=$6::numeric AND e.after_spent=0 AND e.after_source_spent=0 AND e.after_state='held')
 AND (NOT $8::boolean OR EXISTS(SELECT 1 FROM periodic_budget_events e JOIN settlements z ON z.id=e.source_id WHERE e.control_id=c.id AND e.allocation_id=a.id AND e.source_kind='settlement' AND z.request_id=r.id AND z.status='released' AND z.charged_amount=0 AND e.allocation_before_revision=1 AND e.before_held=$6::numeric AND e.after_held=0 AND e.after_spent=0 AND e.after_source_spent=0 AND e.after_state='released'))
 FROM periodic_budget_controls c JOIN periodic_budget_allocations a ON a.control_id=c.id JOIN request_records r ON r.id=a.subject_id JOIN periodic_budget_windows w ON w.id=a.window_id
 WHERE c.id=$1 AND a.subject_type='customer_request' AND a.subject_id=$2`, id, r.RequestID, strconv.FormatInt(held, 10), phase, r.ID, strconv.FormatInt(int64(r.Amount), 10), state, released).Scan(&exact))
		if !exact {
			t.Fatal("periodic scope duplicated money, lost its original window, or changed exact source/effect fields")
		}
	}
	var allocations int
	must(t, "count scopes independently of wallet", f.owner.QueryRow(f.ctx, "SELECT count(*) FROM periodic_budget_allocations WHERE subject_type='customer_request' AND subject_id=$1", r.RequestID).Scan(&allocations))
	if allocations != len(controls) {
		t.Fatal("scope count differs from independently configured tenant and Key")
	}
	_, err := f.app.Exec(f.ctx, "SELECT pb_check_customer($1)", r.RequestID)
	must(t, "run unchanged original020 customer checker", err)
}

func abCompatPendingAndRelease(t *testing.T, f *fixture, r billing.Reservation, controls map[string]string) {
	t.Helper()
	store := billing.New(f.app)
	frozen, periodic := abCompatFrozen(t, f, r.RequestID), abCompatPeriodic(t, f)
	pending, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	must(t, "live Settle with no finalization follows actual pending path", err)
	if pending.Status != "pending_reconciliation" || pending.Reason != "delivery_unknown" {
		t.Fatal("no finalization was treated as known settled consumption")
	}
	must(t, "actual MarkPending existing-case path", store.MarkPending(f.ctx, billing.PendingInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "a2-compat-followup"}))
	var pendingExact bool
	must(t, "pending case exists only after original status updates", f.owner.QueryRow(f.ctx, `SELECT r.status='accepted' AND r.billing_status='pending_reconciliation' AND v.status='pending_reconciliation' AND (SELECT count(*) FROM reconciliation_cases WHERE tenant_id=r.tenant_id AND request_id=r.id AND reason='a2-compat-followup')=1 AND NOT EXISTS(SELECT 1 FROM settlements WHERE request_id=r.id) FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=$1`, r.RequestID).Scan(&pendingExact))
	if !pendingExact || abCompatFrozen(t, f, r.RequestID) != frozen || abCompatPeriodic(t, f) != periodic {
		t.Fatal("pending changed frozen initial facts or financial/PB effects")
	}
	abCompatWallet(t, f, 10000-int64(r.Amount), int64(r.Amount))
	abCompatCheckBirth(t, f, r.RequestID)
	abCompatEffects(t, f, r, controls, false)
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	must(t, "actual APP Release from pending", err)
	if released.Status != "released" || released.Reason != "released_not_sent" || released.ChargedAmount != 0 || released.RawRetailAmount != "0" || released.WaivedRetailAmount != "0" {
		t.Fatal("original release did not produce the unique zero-charge settlement")
	}
	var terminal bool
	must(t, "original terminal companions are complete", f.owner.QueryRow(f.ctx, `SELECT r.status='failed' AND r.billing_status='released' AND v.status='released' AND (SELECT count(*) FROM settlements WHERE request_id=r.id)=1 AND EXISTS(SELECT 1 FROM reconciliation_cases WHERE request_id=r.id AND state='closed') AND NOT EXISTS(SELECT 1 FROM recovery_jobs WHERE request_id=r.id) FROM request_records r JOIN reservations v ON v.request_id=r.id WHERE r.id=$1`, r.RequestID).Scan(&terminal))
	if !terminal || abCompatFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("release changed immutable birth or left incomplete terminal companions")
	}
	abCompatWallet(t, f, 10000, 0)
	abCompatCheckBirth(t, f, r.RequestID)
	abCompatEffects(t, f, r, controls, true)
	before := abCompatSnapshot(t, f)
	for _, call := range []func() (billing.Settlement, error){
		func() (billing.Settlement, error) {
			return store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
		},
		func() (billing.Settlement, error) {
			return store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
		},
	} {
		again, e := call()
		must(t, "read unique existing settlement without replay effects", e)
		if again.ID != released.ID || again.Status != "released" || abCompatSnapshot(t, f) != before {
			t.Fatal("terminal replay generated another source or financial effect")
		}
	}
}

func TestPostgresA2RequestCompatBudgetScopeMatrixPendingAndRelease(t *testing.T) {
	for _, tenantState := range []string{"none", "enabled", "disabled"} {
		for _, keyState := range []string{"none", "enabled", "disabled"} {
			t.Run(tenantState+"_"+keyState, func(t *testing.T) {
				f := newFixture(t)
				admin := abCompatBaseline(t, f)
				controls := abCompatConfigure(t, f, admin, tenantState, keyState)
				r := abCompatReserve(t, f, abCompatInput(f), 120)
				abCompatEffects(t, f, r, controls, false)
				abCompatPendingAndRelease(t, f, r, controls)
			})
		}
	}
}

func TestPostgresA2RequestCompatBudgetZeroPriceAndInputOnly(t *testing.T) {
	for _, kind := range []string{"zero_price", "input_only"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			admin := abCompatBaseline(t, f)
			controls := abCompatConfigure(t, f, admin, "enabled", "disabled")
			store := catalog.New(f.app, nil)
			in, amount, inputRate := abCompatInput(f), int64(0), "0"
			if kind == "input_only" {
				model, err := store.CreateModel(f.ctx, admin, catalog.ModelInput{PublicName: "a1-embeddings", Protocol: provider.ProtocolEmbeddings, ContextLimit: 100, InputUpperBound: 100})
				must(t, "create original valid input-only profile", err)
				_, err = f.owner.Exec(f.ctx, "INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)", f.tenant, model)
				must(t, "prepare legitimate input-only tenant grant", err)
				_, err = f.owner.Exec(f.ctx, "UPDATE api_keys SET scopes=array_append(scopes,$2) WHERE id=$1", f.key, model)
				must(t, "prepare legitimate input-only Key scope", err)
				in.ModelID, in.OutputLimit = model, 0
				must(t, "read actually published profile version", f.app.QueryRow(f.ctx, "SELECT profile_version FROM models WHERE id=$1", model).Scan(&in.ProfileVersion))
				amount, inputRate = 200, "2000000"
			}
			price, err := historicalPublishPrice(f.ctx, store, admin, in.ModelID, inputRate, "0", time.Now())
			must(t, "publish actual effective zero/output-zero price", err)
			if kind == "input_only" {
				before := abCompatSnapshot(t, f)
				bad := in
				bad.OutputLimit = 1
				_, err = billing.New(f.app).Reserve(f.ctx, bad)
				var business *billing.Error
				if !errors.As(err, &business) || business.Code != "invalid_bounds" || abCompatSnapshot(t, f) != before {
					t.Fatal("input-only nonzero output was accepted or left partial facts")
				}
				_, err = historicalPublishPrice(f.ctx, store, admin, in.ModelID, inputRate, "1", time.Now())
				if !errors.Is(err, catalog.ErrInvalid) || abCompatSnapshot(t, f) != before {
					t.Fatal("original catalog accepted input-only output price or wrote partial configuration")
				}
			}
			r := abCompatReserve(t, f, in, amount)
			if r.PriceVersionID != price || !abCompatOriginalRates(t, f, r.RequestID, inputRate, "0") {
				t.Fatal("zero/input-only request did not freeze the real published price")
			}
			abCompatEffects(t, f, r, controls, false)
			abCompatPendingAndRelease(t, f, r, controls)
		})
	}
}

func TestPostgresA2RequestCompatBudgetPriceGroupSnapshotAndNoFallback(t *testing.T) {
	f := newFixture(t)
	admin := abCompatBaseline(t, f)
	store, actor := catalog.New(f.app, nil), catalog.PricingActor{UserID: admin}
	group, err := store.CreatePriceGroup(f.ctx, actor, catalog.GroupCreateCommand{OperationID: uuid.NewString(), Label: "A2 compatibility exact group"})
	must(t, "create actual price group", err)
	assigned, err := store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 1, GroupID: &group.Result.ID})
	must(t, "assign actual tenant price domain", err)
	before := abCompatSnapshot(t, f)
	_, err = billing.New(f.app).Reserve(f.ctx, abCompatInput(f))
	var business *billing.Error
	if !errors.As(err, &business) || business.Code != "price_unavailable" || abCompatSnapshot(t, f) != before {
		t.Fatal("missing group price fell back to default or left partial birth/finances")
	}
	price, err := store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group.Result.ID, ModelID: f.model, InputRate: 2000000, OutputRate: 3000000, EffectiveAt: abCompatClock(t, f).Add(-time.Minute)})
	must(t, "publish actual immutable group price", err)
	r := abCompatReserve(t, f, abCompatInput(f), 260)
	if r.PriceVersionID != price.Result.ID || r.PricingGroupID == nil || *r.PricingGroupID != group.Result.ID || strconv.FormatInt(r.TenantPricingRevision, 10) != assigned.Result.PricingRevision || !abCompatOriginalRates(t, f, r.RequestID, "2000000", "3000000") {
		t.Fatal("group price ID, exact dimensions or tenant revision differ from real command receipts")
	}
	frozen := abCompatFrozen(t, f, r.RequestID)
	_, err = store.PublishGroupPrice(f.ctx, actor, catalog.GroupPriceCommand{OperationID: uuid.NewString(), GroupID: group.Result.ID, ModelID: f.model, InputRate: 7000000, OutputRate: 9000000, EffectiveAt: abCompatClock(t, f)})
	must(t, "append later group price without rewriting request", err)
	_, err = store.AssignTenantPricing(f.ctx, actor, catalog.TenantPricingCommand{OperationID: uuid.NewString(), TenantID: f.tenant, ExpectedRevision: 2})
	must(t, "return tenant to default domain through actual command", err)
	if abCompatFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("current group assignment or new price rewrote immutable old snapshot")
	}
	abCompatPendingAndRelease(t, f, r, map[string]string{})
}

func TestPostgresA2RequestCompatBudgetRecoveryWithoutAttemptReleases(t *testing.T) {
	f := newFixture(t)
	admin := abCompatBaseline(t, f)
	controls := abCompatConfigure(t, f, admin, "disabled", "enabled")
	r := abCompatReserve(t, f, abCompatInput(f), 120)
	frozen := abCompatFrozen(t, f, r.RequestID)
	settled, err := billing.New(f.app).Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "recovery"})
	must(t, "actual recovery API with no attempt", err)
	if settled.Status != "released" || settled.Reason != "released_no_delivery" || settled.ChargedAmount != 0 || abCompatFrozen(t, f, r.RequestID) != frozen {
		t.Fatal("no-attempt recovery invented consumption or changed birth")
	}
	abCompatWallet(t, f, 10000, 0)
	abCompatEffects(t, f, r, controls, true)
	abCompatCheckBirth(t, f, r.RequestID)
	var noGeneration bool
	must(t, "recovery created no attempt, finalization or pending case", f.owner.QueryRow(f.ctx, "SELECT NOT EXISTS(SELECT 1 FROM upstream_attempts WHERE request_id=$1) AND NOT EXISTS(SELECT 1 FROM finalization_records WHERE request_id=$1) AND NOT EXISTS(SELECT 1 FROM reconciliation_cases WHERE request_id=$1)", r.RequestID).Scan(&noGeneration))
	if !noGeneration {
		t.Fatal("no-evidence recovery synthesized a generation or case")
	}
}

func abCompatFault(t *testing.T, f *fixture, table, condition, state string) func() {
	t.Helper()
	body := fmt.Sprintf("CREATE FUNCTION ab_compat_budget_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF %s THEN RAISE EXCEPTION 'ab_compat_budget_injected_fault' USING ERRCODE='%s'; END IF; RETURN NEW; END$$", condition, state)
	_, err := f.owner.Exec(f.ctx, body)
	must(t, "install isolated business effect fault", err)
	quoted := pgx.Identifier{table}.Sanitize()
	_, err = f.owner.Exec(f.ctx, "CREATE TRIGGER zz_ab_compat_budget_fault BEFORE INSERT ON "+quoted+" FOR EACH ROW EXECUTE FUNCTION ab_compat_budget_fault()")
	must(t, "attach exact business effect fault", err)
	removed := false
	remove := func() {
		if removed {
			return
		}
		removed = true
		_, e := f.owner.Exec(f.ctx, "DROP TRIGGER zz_ab_compat_budget_fault ON "+quoted)
		must(t, "remove exact effect fault trigger", e)
		_, e = f.owner.Exec(f.ctx, "DROP FUNCTION ab_compat_budget_fault()")
		must(t, "remove exact effect fault function", e)
	}
	t.Cleanup(remove)
	return remove
}

func TestPostgresA2RequestCompatBudgetBusinessEffectFailuresRollback(t *testing.T) {
	f := newFixture(t)
	admin := abCompatBaseline(t, f)
	abCompatConfigure(t, f, admin, "enabled", "disabled")
	store := billing.New(f.app)
	anchor := abCompatReserve(t, f, abCompatInput(f), 120)
	_, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: anchor.RequestID, Reason: "not_sent"})
	must(t, "retain an already committed tracked root before every fault", err)
	anchorFrozen := abCompatFrozen(t, f, anchor.RequestID)
	in := abCompatInput(f)
	remove := abCompatFault(t, f, "periodic_budget_events", "NEW.source_kind='reservation'", "PZ981")
	before := abCompatSnapshot(t, f)
	_, err = store.Reserve(f.ctx, in)
	sqlstate(t, err, "PZ981")
	if abCompatSnapshot(t, f) != before {
		t.Fatal("failed real reservation effect retained birth, idempotency or financial/PB writes")
	}
	remove()
	r, err := store.Reserve(f.ctx, in)
	must(t, "same original Reserve succeeds after exact fault removal", err)
	abCompatCheckBirth(t, f, r.RequestID)
	remove = abCompatFault(t, f, "reconciliation_cases", "true", "PZ982")
	before = abCompatSnapshot(t, f)
	_, err = store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	sqlstate(t, err, "PZ982")
	if abCompatSnapshot(t, f) != before {
		t.Fatal("failed pending case retained a half status/case or changed funds")
	}
	remove()
	pending, err := store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
	must(t, "same live Settle succeeds after pending fault removal", err)
	if pending.Status != "pending_reconciliation" {
		t.Fatal("fault-free no-finalization Settle was not pending")
	}
	remove = abCompatFault(t, f, "periodic_budget_events", "NEW.source_kind='settlement'", "PZ983")
	before = abCompatSnapshot(t, f)
	_, err = store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	sqlstate(t, err, "PZ983")
	if abCompatSnapshot(t, f) != before {
		t.Fatal("failed release PB effect retained settlement, journal, Key or case changes")
	}
	remove()
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	must(t, "same real Release succeeds after exact effect fault removal", err)
	if released.Status != "released" || released.ChargedAmount != 0 || abCompatFrozen(t, f, anchor.RequestID) != anchorFrozen {
		t.Fatal("failure/recovery changed prior committed root or unique release")
	}
	abCompatCheckBirth(t, f, anchor.RequestID)
	abCompatCheckBirth(t, f, r.RequestID)
	abCompatWallet(t, f, 10000, 0)
}

func TestPostgresA2RequestCompatActualReserveInternalDMLFailuresRollback(t *testing.T) {
	f := newFixture(t)
	f.reserve(t)
	// Each real boundary is reached by original Reserve through the automatic
	// hook; owner only installs a fault trigger and independently observes rows.
	for _, tc := range []struct{ table, event string }{{"pp_request_origins", "INSERT"}, {"pp_request_heads", "INSERT"}, {"request_records", "UPDATE"}, {"pp_root_steps", "INSERT"}, {"pp_operation_roots", "INSERT"}, {"reservations", "INSERT"}, {"journal_lines", "INSERT"}, {"audit_events", "INSERT"}, {"projection_outbox", "INSERT"}} {
		t.Run(tc.table+tc.event, func(t *testing.T) {
			before := abCompatSnapshot(t, f)
			_, err := f.owner.Exec(f.ctx, `CREATE FUNCTION ab_compat_request_fault() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'ab_compat_request_injected_fault' USING ERRCODE='PZ991'; END$$`)
			must(t, "install fixture fault function", err)
			table := pgx.Identifier{tc.table}.Sanitize()
			_, err = f.owner.Exec(f.ctx, "CREATE TRIGGER zz_ab_compat_request_fault BEFORE "+tc.event+" ON "+table+" FOR EACH ROW EXECUTE FUNCTION ab_compat_request_fault()")
			must(t, "install exact fault boundary", err)
			_, err = billing.New(f.app).Reserve(f.ctx, abCompatInput(f))
			sqlstate(t, err, "PZ991")
			_, err = f.owner.Exec(f.ctx, "DROP TRIGGER zz_ab_compat_request_fault ON "+table)
			must(t, "remove exact fault", err)
			_, err = f.owner.Exec(f.ctx, "DROP FUNCTION ab_compat_request_fault()")
			must(t, "remove fixture fault function", err)
			if abCompatSnapshot(t, f) != before {
				t.Fatal("failed birth/Reserve left partial graph or financial effects")
			}
			f.reserve(t)
		})
	}
}

func TestPostgresA2RequestCompatRealReserveIPAndIdempotencyPriority(t *testing.T) {
	f := newFixture(t)
	store := billing.New(f.app)
	for _, ip := range []string{"", "192.0.2.12", "2001:db8::12"} {
		t.Run("client_"+ip, func(t *testing.T) {
			in := abCompatInput(f)
			in.Principal.ClientIP, in.Streaming = ip, ip != ""
			r, err := store.Reserve(f.ctx, in)
			must(t, "actual APP Reserve source representation", err)
			if r.Amount != 120 {
				t.Fatal("independent 100 input plus 20 output reserve arithmetic changed")
			}
			abCompatCheckBirth(t, f, r.RequestID)
			var actualIP *string
			var streaming bool
			must(t, "observe exact nullable client source", f.owner.QueryRow(f.ctx, "SELECT host(client_ip),streaming FROM request_records WHERE id=$1", r.RequestID).Scan(&actualIP, &streaming))
			if (ip == "" && actualIP != nil) || (ip != "" && (actualIP == nil || *actualIP != ip)) || streaming != in.Streaming {
				t.Fatal("client source or streaming freeze differs from original input")
			}
			before := abCompatSnapshot(t, f)
			for _, conflict := range []bool{false, true} {
				replay := in
				replay.RequestID, replay.ModelID, replay.Principal.UserID = uuid.NewString(), uuid.NewString(), uuid.NewString()
				replay.ProfileVersion, replay.InputUpperBound = "not-the-current-profile", -1
				want := "request_exists"
				if conflict {
					replay.RequestHMAC += ":different"
					want = "idempotency_conflict"
				}
				_, err = store.Reserve(f.ctx, replay)
				var business *billing.Error
				if !errors.As(err, &business) || business.Code != want || business.ResourceID != r.RequestID || abCompatSnapshot(t, f) != before {
					t.Fatalf("original idempotency-before-principal/profile/bounds order: want %s for %s; got %v", want, r.RequestID, err)
				}
			}
		})
	}
}

func TestPostgresA2RequestCompatLegacyReleaseNeverBackfills(t *testing.T) {
	f, legacy := abCompatLegacyFixture(t)
	assertLegacy := func() {
		t.Helper()
		var intact bool
		must(t, "observe genuine pre-A2 original without retrospective source", f.owner.QueryRow(f.ctx, `SELECT
 r.pp_origin_root_id IS NULL AND r.pp_origin_root_step IS NULL AND r.pp_origin_xid IS NULL AND r.pp_last_decision_id IS NULL
 AND octet_length(r.policy_version)=10240
 AND NOT EXISTS(SELECT 1 FROM pp_request_origins WHERE request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM pp_request_heads WHERE request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM pp_operation_roots WHERE request_id=r.id)
 AND NOT EXISTS(SELECT 1 FROM pp_root_steps WHERE kind='request_birth' AND source_id=r.id)
 FROM request_records r WHERE r.id=$1`, legacy).Scan(&intact))
		if !intact {
			t.Fatal("installation or original lifecycle changed long legacy input or backfilled its source")
		}
		_, err := f.app.Exec(f.ctx, "SELECT pp_validate_request($1)", legacy)
		sqlstate(t, err, "PP906")
	}
	assertLegacy()
	store := billing.New(f.app)
	must(t, "original APP pending on untracked hold", store.MarkPending(f.ctx, billing.PendingInput{TenantID: f.tenant, RequestID: legacy, Reason: "legacy-unknown"}))
	assertLegacy()
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: legacy, Reason: "not_sent"})
	must(t, "original APP Release on genuine legacy hold", err)
	if released.Status != "released" || released.ChargedAmount != 0 {
		t.Fatal("legacy release lost original financial behavior")
	}
	assertLegacy()
	abCompatWallet(t, f, 10000, 0)
}

func TestPostgresA2RequestCompatEarlyConstraintsRollbackAndClosedGraph(t *testing.T) {
	f := newFixture(t)
	reference := f.reserve(t)
	for _, phase := range []string{"immediate_before_birth", "origin_immediate_before_completion", "immediate_without_original_companion", "immediate_complete"} {
		t.Run(phase, func(t *testing.T) {
			before := abCompatSnapshot(t, f)
			tx := abCompatRawBegin(t, f, pgx.ReadCommitted, pgx.ReadWrite)
			id := uuid.NewString()
			if phase == "immediate_before_birth" || phase == "origin_immediate_before_completion" {
				mode := "ALL"
				if phase == "origin_immediate_before_completion" {
					mode = "zz_pp_ab_request_origin_complete"
				}
				_, err := tx.Exec(f.ctx, "SET CONSTRAINTS "+mode+" IMMEDIATE")
				must(t, "set exact immediate mode before birth", err)
				err = abCompatRawInsert(f, tx, id, reference.RequestID, "", "")
				if phase == "immediate_before_birth" {
					// Original020 pb_request_source_check runs before the
					// zz_pp_ab_request_birth capture. Its exact missing
					// reservation error precedes every new graph FK check.
					abCompatRawError(t, err, "23514", "periodic source reservation missing")
				} else {
					// With only the origin completion trigger immediate,
					// original020 remains deferred and the source cannot
					// certify a graph whose head/root/pointers are unborn.
					abCompatRawError(t, err, "PP902", "procurement_history_invariant")
				}
			} else {
				must(t, "raw birth with default deferred ordering", abCompatRawInsert(f, tx, id, reference.RequestID, "", ""))
				abCompatRawExactBirth(t, f, tx, id)
				if phase == "immediate_complete" {
					abCompatRawCompanion(t, f, tx, id)
					_, err := tx.Exec(f.ctx, "SET CONSTRAINTS ALL IMMEDIATE")
					must(t, "complete graph accepts real immediate validation", err)
					must(t, "COMMIT exact complete raw boundary", tx.Commit(f.ctx))
					abCompatCheckBirth(t, f, id)
					return
				}
				_, err := tx.Exec(f.ctx, "SET CONSTRAINTS ALL IMMEDIATE")
				abCompatRawError(t, err, "23514", "periodic source reservation missing")
			}
			must(t, "rollback early-constraint failure", tx.Rollback(f.ctx))
			if abCompatSnapshot(t, f) != before {
				t.Fatal("early validation retained a partial raw birth or customer financial effects")
			}
			abCompatCheckBirth(t, f, reference.RequestID)
		})
	}
}

func TestPostgresA2RequestCompatPendingReasonPriorityAndTerminalReplay(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	store := billing.New(f.app)
	frozen, periodic := abCompatFrozen(t, f, r.RequestID), abCompatPeriodic(t, f)
	for _, reason := range []string{"delivery_unknown", "metering_conflict", "later-recovery"} {
		must(t, "original MarkPending status/status/case order", store.MarkPending(f.ctx, billing.PendingInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: reason}))
	}
	var reason string
	var exact bool
	must(t, "original conflict reason is sticky and unique", f.owner.QueryRow(f.ctx, `SELECT c.reason,r.status='accepted' AND r.billing_status='pending_reconciliation' AND v.status='pending_reconciliation' AND (SELECT count(*) FROM reconciliation_cases WHERE request_id=r.id)=1
 FROM request_records r JOIN reservations v ON v.request_id=r.id JOIN reconciliation_cases c ON c.request_id=r.id WHERE r.id=$1`, r.RequestID).Scan(&reason, &exact))
	if reason != "metering_conflict" || !exact || abCompatFrozen(t, f, r.RequestID) != frozen || abCompatPeriodic(t, f) != periodic {
		t.Fatal("pending overwrote higher-priority reason, duplicated case or changed birth/PB facts")
	}
	abCompatWallet(t, f, 9880, 120)
	released, err := store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	must(t, "release exact pending reservation", err)
	before := abCompatSnapshot(t, f)
	must(t, "MarkPending after terminal remains an original no-op", store.MarkPending(f.ctx, billing.PendingInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "cannot-reopen"}))
	for _, call := range []func() (billing.Settlement, error){
		func() (billing.Settlement, error) {
			return store.Release(f.ctx, billing.ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
		},
		func() (billing.Settlement, error) {
			return store.Settle(f.ctx, billing.SettleInput{TenantID: f.tenant, RequestID: r.RequestID, Mode: "live"})
		},
	} {
		again, err := call()
		must(t, "terminal operation returns existing settlement", err)
		if again.ID != released.ID || again.Status != "released" || abCompatSnapshot(t, f) != before {
			t.Fatal("terminal replay reopened case or duplicated settlement/funds/PB/source")
		}
	}
	abCompatWallet(t, f, 10000, 0)
	abCompatCheckBirth(t, f, r.RequestID)
}

func TestPostgresA2RequestCompatProtectedRawWritesAndRolesRollback(t *testing.T) {
	f := newFixture(t)
	r := f.reserve(t)
	abCompatCheckBirth(t, f, r.RequestID)
	for _, sql := range []string{
		"UPDATE request_records SET status='succeeded' WHERE id=$1",
		"UPDATE request_records SET billing_status='settled' WHERE id=$1",
		"UPDATE request_records SET latest_finalization_id=NULL WHERE id=$1",
		"UPDATE request_records SET input_upper_bound=input_upper_bound+1 WHERE id=$1",
		"UPDATE request_records SET pp_origin_root_id=NULL WHERE id=$1",
		"DELETE FROM request_records WHERE id=$1",
		"UPDATE pp_request_origins SET request_id=request_id WHERE request_id=$1",
		"DELETE FROM pp_request_heads WHERE request_id=$1",
		"UPDATE pp_root_steps SET root_id=root_id WHERE source_id=$1",
		"DELETE FROM pp_operation_roots WHERE request_id=$1",
	} {
		t.Run(sql, func(t *testing.T) {
			before := abCompatSnapshot(t, f)
			_, err := f.app.Exec(f.ctx, sql, r.RequestID)
			if err == nil {
				t.Fatal("protected write accepted")
			}
			if abCompatSnapshot(t, f) != before {
				t.Fatal("protected write changed committed rows")
			}
		})
	}
	for _, table := range []string{"request_records", "pp_request_origins", "pp_request_heads", "pp_root_steps", "pp_operation_roots"} {
		before := abCompatSnapshot(t, f)
		_, err := f.app.Exec(f.ctx, "TRUNCATE "+pgx.Identifier{table}.Sanitize()+" CASCADE")
		sqlstate(t, err, "42501")
		if abCompatSnapshot(t, f) != before {
			t.Fatal("TRUNCATE changed rows")
		}
	}
	_, err := f.peer.Exec(f.ctx, "SELECT pp_validate_request($1)", r.RequestID)
	sqlstate(t, err, "42501")
	_, err = f.owner.Exec(f.ctx, "SELECT pp_validate_request($1)", r.RequestID)
	sqlstate(t, err, "PP901")
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin owner SET ROLE attack", err)
	defer tx.Rollback(f.ctx)
	_, err = tx.Exec(f.ctx, "SET LOCAL ROLE "+pgx.Identifier{f.b.App}.Sanitize())
	must(t, "set role does not change LOGIN", err)
	_, err = tx.Exec(f.ctx, "SELECT pp_validate_request($1)", r.RequestID)
	sqlstate(t, err, "PP901")
	_ = tx.Rollback(f.ctx)
}
