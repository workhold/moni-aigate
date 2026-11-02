package fundinglotscan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These opt-in tests exercise the real fixed SQL and decoder on original
// 001–022 tables. They confer no installation, maintenance, payment or writer
// authority. Corrupt counterexamples are owner-only, isolated and rolled back.
type completeSourcePGFixture struct {
	ctx          context.Context
	owner, admin *pgxpool.Pool
	schema       string
}

func completeSourcePGFailure(t *testing.T, stage string, err error) {
	t.Helper()
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatalf("%s: SQLSTATE=%s", stage, pg.Code)
	}
	t.Fatal(stage + ": database operation failed")
}

func completeSourcePGMust(t *testing.T, stage string, err error) {
	t.Helper()
	if err != nil {
		completeSourcePGFailure(t, stage, err)
	}
}

func newCompleteSourcePGFixture(t *testing.T) *completeSourcePGFixture {
	t.Helper()
	raw := os.Getenv("MONI_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("MONI_TEST_DATABASE_URL required for isolated complete-source PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" || cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Fatal("complete-source tests require dedicated local moni_test on port 55439 without TLS/fallbacks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	cfg.MaxConns, cfg.MinConns = 4, 0
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams = map[string]string{"application_name": "moni-complete-source-test", "timezone": "UTC", "search_path": "pg_catalog,pg_temp"}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		cancel()
		completeSourcePGFailure(t, "open dedicated test database", err)
	}
	f := &completeSourcePGFixture{ctx: ctx, admin: admin, schema: "flscan_" + strings.ReplaceAll(uuid.NewString(), "-", "")}
	var exists bool
	err = admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, f.schema).Scan(&exists)
	if err != nil || exists {
		admin.Close()
		cancel()
		t.Fatal("isolated schema collision check failed")
	}
	// Register the exact cleanup intent before CREATE; an unknown reply never
	// causes a retry or deletion by prefix. No existing schema is repurposed.
	t.Cleanup(func() {
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		defer cancel()
		defer admin.Close()
		if _, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error("isolated complete-source schema cleanup failed")
			return
		}
		var remains bool
		if e := admin.QueryRow(cleanup, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_namespace WHERE nspname=$1)`, f.schema).Scan(&remains); e != nil || remains {
			t.Error("isolated complete-source schema cleanup was not confirmed")
		}
	})
	var version int
	completeSourcePGMust(t, "read test PostgreSQL version", admin.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version))
	if version < 160000 || version >= 170000 {
		t.Fatal("complete-source SQL fixture requires PostgreSQL 16")
	}
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	completeSourcePGMust(t, "create isolated schema", err)
	ownerCfg := cfg.Copy()
	ownerCfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp"
	f.owner, err = pgxpool.NewWithConfig(ctx, ownerCfg)
	completeSourcePGMust(t, "open isolated owner pool", err)
	completeSourcePGMust(t, "install original 001–022 migrations", migrations.ApplyThrough(ctx, f.owner, "022_payment_runtime_attestation.sql"))
	return f
}

func (f *completeSourcePGFixture) begin(t *testing.T, access pgx.TxAccessMode) pgx.Tx {
	t.Helper()
	tx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: access})
	completeSourcePGMust(t, "begin caller RR transaction", err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := tx.Rollback(cleanup); e != nil && !errors.Is(e, pgx.ErrTxClosed) {
			t.Error("isolated source-reader transaction rollback failed")
		}
	})
	return tx
}

func (f *completeSourcePGFixture) exec(t *testing.T, tx pgx.Tx, sql string, args ...any) {
	t.Helper()
	_, err := tx.Exec(f.ctx, sql, args...)
	completeSourcePGMust(t, "prepare controlled source fixture", err)
}

// Replica mode disables only this transaction's original business/FK trigger
// execution. It never changes instance settings, production ACLs or a proof.
func (f *completeSourcePGFixture) corruptionTx(t *testing.T) pgx.Tx {
	t.Helper()
	tx := f.begin(t, pgx.ReadWrite)
	f.exec(t, tx, "SET LOCAL session_replication_role = 'replica'")
	return tx
}

func (f *completeSourcePGFixture) relaxChecks(t *testing.T, tx pgx.Tx, table string) {
	t.Helper()
	name := pgx.Identifier{f.schema, table}.Sanitize()
	rows, err := tx.Query(f.ctx, `SELECT conname::pg_catalog.text FROM pg_catalog.pg_constraint WHERE conrelid=$1::pg_catalog.regclass AND contype='c' ORDER BY conname`, name)
	completeSourcePGMust(t, "inventory isolated check constraints", err)
	var constraints []string
	for rows.Next() {
		var constraint string
		completeSourcePGMust(t, "decode isolated check constraint", rows.Scan(&constraint))
		constraints = append(constraints, constraint)
	}
	rows.Close()
	completeSourcePGMust(t, "finish isolated check inventory", rows.Err())
	for _, constraint := range constraints {
		f.exec(t, tx, "ALTER TABLE "+name+" DROP CONSTRAINT "+pgx.Identifier{constraint}.Sanitize())
	}
}

type completeSourcePGQuery struct {
	ctx    context.Context
	tx     pgx.Tx
	schema string
	calls  map[string]int
}

func (q *completeSourcePGQuery) Query(sql string, args ...any) (Rows, error) {
	// Count by fixed relation identity, independently of the caller's rows.
	for _, group := range [][]binding{fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings()} {
		for _, b := range group {
			if strings.Contains(sql, " FROM ONLY "+pgx.Identifier{q.schema, b.table}.Sanitize()+" AS s ") {
				q.calls[b.table]++
				arguments := append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
				rows, err := q.tx.Query(q.ctx, sql, arguments...)
				if err != nil {
					return nil, err
				}
				return completeSourcePGRows{rows: rows}, nil
			}
		}
	}
	return nil, ErrExecution
}

type completeSourcePGRows struct{ rows pgx.Rows }

func (r completeSourcePGRows) Next() (bool, error)    { return r.rows.Next(), nil }
func (r completeSourcePGRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r completeSourcePGRows) Err() error             { return r.rows.Err() }
func (r completeSourcePGRows) Close() error           { r.rows.Close(); return nil }

func (f *completeSourcePGFixture) query(tx pgx.Tx) *completeSourcePGQuery {
	return &completeSourcePGQuery{ctx: f.ctx, tx: tx, schema: f.schema, calls: map[string]int{}}
}

func (f *completeSourcePGFixture) read(t *testing.T, tx pgx.Tx, mode Mode) (Facts, *completeSourcePGQuery) {
	t.Helper()
	query := f.query(tx)
	facts, err := Read(f.ctx, query, f.schema, mode)
	completeSourcePGMust(t, "execute complete fixed source read", err)
	if !facts.Summary().PhysicalEOFComplete || facts.Summary().ScannedTables != 68 || len(query.calls) != 68 {
		t.Fatal("real source scan did not complete all 68 physical tables")
	}
	return facts, query
}

// raw scans the same complete registry without invoking a domain algorithm.
// This lets intentionally orphaned rows test physical codecs and pagination
// without presenting fixture data as accepted business history.
func (f *completeSourcePGFixture) raw(t *testing.T, tx pgx.Tx) (*session, *completeSourcePGQuery) {
	t.Helper()
	tables, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
	completeSourcePGMust(t, "construct complete registry", err)
	if len(tables) != 68 {
		t.Fatal("complete source registry changed")
	}
	query := f.query(tx)
	s := &session{ctx: f.ctx, query: query, schema: f.schema, mode: ModeSandbox, tables: tables, index: index, rejected: map[cellRef]bool{}}
	for i := range s.tables {
		completeSourcePGMust(t, "execute fixed raw source table", s.readTable(i))
	}
	if len(query.calls) != 68 {
		t.Fatal("raw scan omitted a physical relation")
	}
	return s, query
}

func completeSourcePGCell(t *testing.T, s *session, table, column string, row int) cell {
	t.Helper()
	ti, ok := s.index[table]
	if !ok || row >= len(s.tables[ti].rows) {
		t.Fatal("expected original row missing")
	}
	ci, ok := s.tables[ti].spec.index[column]
	if !ok {
		t.Fatal("expected original column missing")
	}
	return s.tables[ti].rows[row][ci]
}

func TestPostgresCompleteSourceReader(t *testing.T) {
	f := newCompleteSourcePGFixture(t)

	t.Run("original_empty_ordinary_and_seven_algorithms", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		facts, calls := f.read(t, tx, ModeOrdinary)
		report := facts.Summary()
		if report.Status != "consistent" || !report.CoverageComplete || report.SourceRows != 1 || report.InputMismatchFields != 0 || report.OrdinaryBoundaryMismatches != 0 {
			t.Fatal("original empty ordinary database was not completely verified")
		}
		for i, state := range report.Algorithms {
			want := "executed"
			if i == 6 {
				want = "not_applicable"
			}
			if state != want {
				t.Fatalf("algorithm %d=%s, want %s", i, state, want)
			}
		}
		for _, count := range calls.calls {
			if count != 1 {
				t.Fatal("empty/single-row table was scanned more than once")
			}
		}
		// A fake sandbox mode cannot manufacture an authenticated installation:
		// the same native rows execute the stored-fact core and report mismatch.
		sandbox, _ := f.read(t, tx, ModeSandbox)
		for i, state := range sandbox.Summary().Algorithms {
			if state != "executed" {
				t.Fatalf("sandbox algorithm %d was not executed", i)
			}
		}
		if sandbox.Summary().Status != "mismatch" || sandbox.Summary().Sandbox.Scope != "sandbox_stored_verified_source_graph_v1" {
			t.Fatal("missing sandbox provenance became a successful sandbox graph")
		}
	})

	t.Run("four_tuple_keys_across_128_rows", func(t *testing.T) {
		tx := f.corruptionTx(t)
		control, actor, environment, order, attempt, channel := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		f.exec(t, tx, `INSERT INTO periodic_budget_configuration_versions(control_id,revision,operation_id,period_kind,enabled,limit_amount,actor)
 SELECT $1,n,gen_random_uuid()::text,'utc_day',false,0,$2 FROM generate_series(1,129) AS n`, control, actor)
		f.exec(t, tx, `INSERT INTO payment_provider_revisions(environment_id,merchant_id,order_id,provider_order_revision,kind,fact_digest,evidence_id)
 SELECT $1,'fixture:merchant',$2,n,'closed',repeat('a',64),gen_random_uuid()::text FROM generate_series(1,129) AS n`, environment, order)
		f.exec(t, tx, `INSERT INTO procurement_evidence_identities(attempt_id,evidence_digest,event_id)
 SELECT $1,lpad(to_hex(n),64,'0'),gen_random_uuid()::text FROM generate_series(0,128) AS n`, attempt)
		f.exec(t, tx, `INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight)
 SELECT CASE WHEN n=0 THEN '' ELSE 'version:'||lpad((n/32)::text,3,'0') END,$1,
 CASE WHEN n=0 THEN '' ELSE 'credential:'||lpad((n%32)::text,3,'0') END,(n%32)+1,'fixture',1,1 FROM generate_series(0,128) AS n`, channel)
		s, query := f.raw(t, tx)
		for _, table := range []string{"periodic_budget_configuration_versions", "payment_provider_revisions", "procurement_evidence_identities", "channel_version_credentials"} {
			if len(s.tables[s.index[table]].rows) != 129 || query.calls[table] != 2 {
				t.Fatalf("%s did not advance through its composite key", table)
			}
		}
		for _, pair := range [][2]string{{"periodic_budget_configuration_versions", "revision"}, {"payment_provider_revisions", "provider_order_revision"}} {
			for i := range 129 {
				if got := completeSourcePGCell(t, s, pair[0], pair[1], i).text; got != strconv.Itoa(i+1) {
					t.Fatalf("%s sorted revision numerically incorrectly at row %d", pair[0], i)
				}
			}
		}
		if completeSourcePGCell(t, s, "channel_version_credentials", "channel_version_id", 0).text != "" || completeSourcePGCell(t, s, "channel_version_credentials", "credential_id", 0).text != "" {
			t.Fatal("empty first tuple was skipped as a cursor sentinel")
		}
		for i := range 129 {
			if completeSourcePGCell(t, s, "procurement_evidence_identities", "evidence_digest", i).text != fmt.Sprintf("%064x", i) {
				t.Fatal("identity tuple lost C-ordered source rows")
			}
		}
	})

	t.Run("singleton_false_is_visible", func(t *testing.T) {
		tx := f.corruptionTx(t)
		f.relaxChecks(t, tx, "payment_policy")
		f.exec(t, tx, `INSERT INTO payment_policy(singleton,enabled,revision) VALUES(false,false,1)`)
		facts, _ := f.read(t, tx, ModeOrdinary)
		s := facts.state
		if len(s.tables[s.index["payment_policy"]].rows) != 2 || completeSourcePGCell(t, s, "payment_policy", "singleton", 0).boolean || !completeSourcePGCell(t, s, "payment_policy", "singleton", 1).boolean || facts.Summary().OrdinaryBoundaryMismatches == 0 || facts.Summary().CoverageComplete {
			t.Fatal("false singleton was hidden or ordinary exclusion passed")
		}
	})

	for _, native := range []struct {
		name, literal string
		infinity      int8
		valid         bool
	}{
		{"null", "NULL", 0, false}, {"positive_infinity", "'infinity'", 1, true}, {"negative_infinity", "'-infinity'", -1, true},
	} {
		t.Run("native_qualification_"+native.name, func(t *testing.T) {
			tx := f.corruptionTx(t)
			if !native.valid {
				f.exec(t, tx, `ALTER TABLE audit_events ALTER COLUMN created_at DROP NOT NULL`)
			}
			f.exec(t, tx, `INSERT INTO audit_events(actor,action,target,created_at) VALUES('fixture','fixture.source','fixture',`+native.literal+`::timestamptz)`)
			facts, _ := f.read(t, tx, ModeSandbox)
			report := facts.Summary()
			if !report.PhysicalEOFComplete || report.Status != "mismatch" || report.CoverageComplete || report.InputMismatchFields != 1 {
				t.Fatal("native mismatch was dropped, duplicated or accepted as complete coverage")
			}
			for _, i := range []int{2, 4, 6} {
				if report.Algorithms[i] != "input_mismatch" {
					t.Fatalf("time.Time algorithm %d was invoked", i)
				}
			}
			for _, i := range []int{0, 1, 3, 5} {
				if report.Algorithms[i] != "executed" {
					t.Fatalf("independent algorithm %d was skipped", i)
				}
			}
			if report.Periodic.Mismatches.InvalidTimeRows != 1 {
				t.Fatal("PB did not classify the retained native envelope")
			}
			value := completeSourcePGCell(t, facts.state, "audit_events", "created_at", 0).instant
			if value.Valid != native.valid || int8(value.InfinityModifier) != native.infinity {
				t.Fatal("original native timestamp envelope changed")
			}
		})
	}

	t.Run("finite_year_one_and_nullable_time", func(t *testing.T) {
		tx := f.corruptionTx(t)
		f.exec(t, tx, `INSERT INTO audit_events(actor,action,target,created_at) VALUES('fixture','fixture.source','fixture','0001-01-01 00:00:00+00'::timestamptz)`)
		f.exec(t, tx, `INSERT INTO payment_evidence_processing(evidence_id) VALUES($1)`, uuid.NewString())
		s, _ := f.raw(t, tx)
		periodic, qualified, err := projectPeriodic(s)
		completeSourcePGMust(t, "project native PB source", err)
		if !qualified || !periodic.Audits[0].CreatedAt.Valid || periodic.Audits[0].CreatedAt.Infinity != 0 || !periodic.Audits[0].CreatedAt.Value.IsZero() {
			t.Fatal("finite year-one source was converted to NULL")
		}
		sandbox, qualified, err := projectSandbox(s)
		completeSourcePGMust(t, "project finite and nullable sandbox source", err)
		if !qualified || !sandbox.Audits[0].CreatedAt.IsZero() || sandbox.Processing[0].LeaseUntil != nil || len(s.rejected) != 0 {
			t.Fatal("native finite/nullable source was disqualified or replaced")
		}
	})

	t.Run("finite_outside_business_year_runs_cores", func(t *testing.T) {
		tx := f.corruptionTx(t)
		// A funding-domain audit exercises that core's original scoped clock
		// qualification as well as procurement's and sandbox's raw-time checks.
		f.exec(t, tx, `INSERT INTO audit_events(actor,action,target,created_at) VALUES('fixture','voucher.batch.created','fixture','10000-01-01 00:00:00+00'::timestamptz)`)
		facts, _ := f.read(t, tx, ModeSandbox)
		report := facts.Summary()
		original := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		value := completeSourcePGCell(t, facts.state, "audit_events", "created_at", 0).instant
		if !value.Valid || value.InfinityModifier != 0 || !value.Time.Equal(original) {
			t.Fatal("finite out-of-business-range original timestamp changed")
		}
		procurement, qualified, err := projectProcurement(facts.state)
		completeSourcePGMust(t, "project original out-of-business-range procurement time", err)
		if !qualified || len(procurement.Audits) != 1 || !procurement.Audits[0].CreatedAt.Equal(original) {
			t.Fatal("finite out-of-business-range source was replaced or disqualified")
		}
		periodic, qualified, err := projectPeriodic(facts.state)
		completeSourcePGMust(t, "project original out-of-business-range periodic time", err)
		if !qualified || len(periodic.Audits) != 1 || !periodic.Audits[0].CreatedAt.Valid || periodic.Audits[0].CreatedAt.Infinity != 0 || !periodic.Audits[0].CreatedAt.Value.Equal(original) {
			t.Fatal("finite out-of-business-range PB native envelope changed")
		}
		if !report.PhysicalEOFComplete || report.ScannedTables != 68 || report.Status != "mismatch" || report.InputMismatchFields != 0 {
			t.Fatal("finite business-time mismatch became an execution or qualification failure")
		}
		for i, state := range report.Algorithms {
			if state != "executed" {
				t.Fatalf("finite representable time skipped original core %d: %s", i, state)
			}
		}
		if report.Procurement.Scope != "procurement_domain_evidence_graph_v1" || report.Procurement.Mismatches.InvalidTimeRows == 0 || report.Funding.Mismatches.InvalidTimeRows == 0 || report.Sandbox.Mismatches.InvalidTimeRows == 0 {
			t.Fatalf("original business-year cores did not classify finite time mismatches: procurement=%d funding=%d sandbox=%d", report.Procurement.Mismatches.InvalidTimeRows, report.Funding.Mismatches.InvalidTimeRows, report.Sandbox.Mismatches.InvalidTimeRows)
		}
		// PB deliberately accepts PostgreSQL's wider native finite year range;
		// its own raw-time contract must remain distinct from the legacy cores.
		if report.Periodic.Scope != "periodic_budget_graph_v1" || report.Periodic.Mismatches.InvalidTimeRows != 0 {
			t.Fatal("PB replaced its native finite-time contract with legacy business-year limits")
		}
	})

	t.Run("bytea_null_empty_binary_and_numeric100", func(t *testing.T) {
		tx := f.corruptionTx(t)
		f.relaxChecks(t, tx, "payment_command_outbox")
		originals := [][]byte{nil, {}, {0xff, 0, 0xfe, 0x7f}}
		ids := make([]string, len(originals))
		for i, original := range originals {
			ids[i] = uuid.NewString()
			f.exec(t, tx, `INSERT INTO payment_command_outbox(id,order_id,kind,external_command_id,canonical_parameters) VALUES($1,$2,'checkout',$3,$4)`, ids[i], uuid.NewString(), uuid.NewString(), original)
		}
		amount := strings.Repeat("9", 100)
		account := uuid.NewString()
		f.exec(t, tx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount,held_amount,spent_amount) VALUES($1,'fixture','fixture','active',0,$2::numeric,$2::numeric)`, account, amount)
		f.exec(t, tx, `INSERT INTO procurement_budget_events(event_key,account_id,attempt_id,held_delta,spent_delta,type) VALUES('fixture:numeric',$1,$2,$3::numeric,$4::numeric,'fixture')`, account, uuid.NewString(), "-"+amount, amount)
		s, _ := f.raw(t, tx)
		out, qualified, err := projectSandbox(s)
		completeSourcePGMust(t, "project binary originals", err)
		if !qualified || len(out.CommandOutbox) != 3 {
			t.Fatal("binary source projection failed")
		}
		byID := map[string]*[]byte{}
		for _, row := range out.CommandOutbox {
			byID[row.ID] = row.CanonicalParameters
		}
		if byID[ids[0]] != nil || byID[ids[1]] == nil || *byID[ids[1]] == nil || len(*byID[ids[1]]) != 0 || byID[ids[2]] == nil || !bytes.Equal(*byID[ids[2]], originals[2]) {
			t.Fatal("PostgreSQL NULL, empty or arbitrary bytea was changed")
		}
		procurement, qualified, err := projectProcurement(s)
		completeSourcePGMust(t, "project NUMERIC100 originals", err)
		if !qualified || procurement.Accounts[0].HeldAmount != amount || procurement.Accounts[0].SpentAmount != amount || procurement.BudgetEvents[0].HeldDelta != "-"+amount || procurement.BudgetEvents[0].SpentDelta != amount {
			t.Fatal("NUMERIC100 or signed delta lost precision")
		}
		table := &s.tables[s.index["payment_command_outbox"]]
		for _, row := range table.rows {
			if row[table.spec.index["id"]].text == ids[2] {
				row[table.spec.index["canonical_parameters"]].data[0] = 0
			}
		}
		if (*byID[ids[2]])[0] != 0xff {
			t.Fatal("projected original aliases inserted bytes")
		}
	})

	for _, field := range []string{"json_text", "raw_bytea"} {
		t.Run("bounded_case_"+field, func(t *testing.T) {
			tx := f.corruptionTx(t)
			table := "audit_events"
			if field == "json_text" {
				f.exec(t, tx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES('fixture','fixture.source','fixture',jsonb_build_object('oversized',repeat('x',65537)))`)
			} else {
				table = "payment_command_outbox"
				f.relaxChecks(t, tx, table)
				f.exec(t, tx, `INSERT INTO payment_command_outbox(id,order_id,kind,external_command_id,canonical_parameters) VALUES($1,$2,'checkout',$3,$4)`, uuid.NewString(), uuid.NewString(), uuid.NewString(), bytes.Repeat([]byte{0xff}, 65537))
			}
			tables, index, err := registry(fundingBindings(), procurementBindings(), periodicBindings(), sandboxBindings())
			completeSourcePGMust(t, "construct bounded projection", err)
			spec := tables[index[table]].spec
			rows, err := tx.Query(f.ctx, pageSQL(f.schema, spec), pgx.QueryExecModeSimpleProtocol, false, "")
			completeSourcePGMust(t, "read actual bounded CASE projection", err)
			if !rows.Next() {
				rows.Close()
				t.Fatal("oversized source row was filtered instead of qualified")
			}
			values, err := rows.Values()
			completeSourcePGMust(t, "decode bounded CASE result", err)
			if len(values) != len(spec.columns)+1 || values[0] != false {
				rows.Close()
				t.Fatal("oversized source lacked false qualification")
			}
			for _, value := range values[1:] {
				if value != nil {
					rows.Close()
					t.Fatal("oversized original crossed CASE projection")
				}
			}
			if rows.Next() {
				rows.Close()
				t.Fatal("unexpected resource fixture row")
			}
			rows.Close()
			completeSourcePGMust(t, "finish bounded CASE projection", rows.Err())
			facts, err := Read(f.ctx, f.query(tx), f.schema, ModeSandbox)
			if !errors.Is(err, ErrResource) || facts.state != nil || !reflect.DeepEqual(facts.Summary(), Report{}) {
				t.Fatal("resource refusal returned partial source facts")
			}
		})
	}

	t.Run("caller_repeatable_read_is_preserved", func(t *testing.T) {
		old := f.begin(t, pgx.ReadOnly)
		before, _ := f.read(t, old, ModeOrdinary)
		var readOnly string
		completeSourcePGMust(t, "read caller transaction mode", old.QueryRow(f.ctx, `SHOW transaction_read_only`).Scan(&readOnly))
		if readOnly != "on" {
			t.Fatal("source reader changed caller access mode")
		}
		user := uuid.NewString()
		_, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash) VALUES($1,$2,'unused-fixture-only')`, user, user+"@example.invalid")
		completeSourcePGMust(t, "commit independent fixture user", err)
		again, _ := f.read(t, old, ModeOrdinary)
		if !reflect.DeepEqual(before.Summary(), again.Summary()) {
			t.Fatal("source reader switched the caller RR snapshot")
		}
		fresh := f.begin(t, pgx.ReadOnly)
		after, _ := f.read(t, fresh, ModeOrdinary)
		if after.Summary().SourceRows != before.Summary().SourceRows+1 || after.Summary().Status != "consistent" || !after.Summary().CoverageComplete {
			t.Fatal("fresh RR did not observe the independently committed row")
		}
	})
}
