package maintenancetrust

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/paymentsandboxschema"
)

// This file tests the private SQL/scalar decision layer with real PostgreSQL.
// Its counterexamples deliberately do not acquire an InstallationProof or call
// Attest: relaxed test-only constraints must never certify an installation.
func predecessorPGTx(t *testing.T, f *snapshotFixture) (pgx.Tx, context.Context) {
	t.Helper()
	c := f.connect(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite})
	if err != nil {
		cancel()
		t.Fatal("predecessor private real RR begin")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if tx.Rollback(cleanup) != nil && c.PgConn().TxStatus() != 'I' {
			t.Error("predecessor private RR rollback")
		}
		cancel()
	})
	return tx, ctx
}
func predecessorPGReader(f *snapshotFixture, tx pgx.Tx, ctx context.Context) *predecessorReader {
	// This incomplete private value can exercise readers only. It has no nonce,
	// installation, history, identity, authority state or route to a Permit.
	return &predecessorReader{authority: &authorityStage{snapshot: &snapshotState{rawTx: tx, gate: &gateState{pins: copyPins(f.pins)}}}, work: ctx}
}
func predecessorPGExec(t *testing.T, ctx context.Context, tx pgx.Tx, q string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, q, args...); err != nil {
		predecessorPGFailure(t, "predecessor controlled owner fixture statement", err)
	}
}

// Only PostgreSQL's safe diagnostic identifiers are reported. Message, Detail,
// Where, SQL, values, relation/schema names and connection settings stay private.
func predecessorPGFailure(t *testing.T, label string, err error) {
	t.Helper()
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		t.Fatalf("%s: SQLSTATE=%s constraint=%q", label, pg.Code, pg.ConstraintName)
	}
	t.Fatal(label + ": PostgreSQL diagnostic unavailable")
}

// A predicate counterexample needs one visible row, independently of the
// business insertion state machine. Relaxations occur only in this owner's
// random schema/transaction and are fully rolled back; no static proof is used.
func predecessorPGRelax(t *testing.T, ctx context.Context, tx pgx.Tx, schema, table string) {
	t.Helper()
	name := pgx.Identifier{schema, table}.Sanitize()
	// CREATE CONSTRAINT TRIGGER also has a pg_constraint row (contype=t),
	// but its dependency must be managed by DROP/DISABLE TRIGGER, not ALTER
	// TABLE DROP CONSTRAINT. USER below disables those exact test triggers.
	rows, err := tx.Query(ctx, `SELECT conname::text FROM pg_catalog.pg_constraint WHERE conrelid=$1::regclass AND contype<>'t' ORDER BY conname`, name)
	if err != nil {
		t.Fatal("private constraint inventory")
	}
	var constraints []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) != nil {
			rows.Close()
			t.Fatal("private constraint scalar")
		}
		constraints = append(constraints, v)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal("private constraint EOF")
	}
	for _, v := range constraints {
		predecessorPGExec(t, ctx, tx, "ALTER TABLE "+name+" DROP CONSTRAINT IF EXISTS "+pgx.Identifier{v}.Sanitize()+" CASCADE")
	}
	rows, err = tx.Query(ctx, `SELECT attname::text FROM pg_catalog.pg_attribute WHERE attrelid=$1::regclass AND attnum>0 AND NOT attisdropped AND attnotnull ORDER BY attnum`, name)
	if err != nil {
		t.Fatal("private not-null inventory")
	}
	var columns []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) != nil {
			rows.Close()
			t.Fatal("private column scalar")
		}
		columns = append(columns, v)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal("private column EOF")
	}
	for _, v := range columns {
		predecessorPGExec(t, ctx, tx, "ALTER TABLE "+name+" ALTER COLUMN "+pgx.Identifier{v}.Sanitize()+" DROP NOT NULL")
	}
	predecessorPGExec(t, ctx, tx, "ALTER TABLE "+name+" DISABLE TRIGGER USER")
}

func TestPredecessorPostgresPrivateOrdinaryAllHistoryAndBypasses(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, p *pgxpool.Pool) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, q := range []string{`INSERT INTO tenants(id,name) VALUES('predecessor-normal-tenant','ordinary baseline')`, `INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES('predecessor-normal-account','predecessor-normal-tenant','AVAILABLE')`, `INSERT INTO audit_events(actor,action,target) VALUES('private-fixture','voucher.fixture','ordinary-non-payment')`} {
			if _, err := p.Exec(ctx, q); err != nil {
				t.Fatal("predecessor original non-payment baseline")
			}
		}
	})
	t.Run("original-ordinary-off-with-commercial-rows", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		r := predecessorPGReader(f, tx, ctx)
		got, err := r.readOrdinary()
		if err != nil || !predecessorValidateOrdinary(got) || r.resources.rows != 1 {
			t.Fatal("real original ordinary-off SQL rejected", err)
		}
	})
	for i, table := range predecessorHistoryTables {
		t.Run("private-history/"+table, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			predecessorPGRelax(t, ctx, tx, f.schema, table)
			predecessorPGExec(t, ctx, tx, "INSERT INTO "+pgx.Identifier{f.schema, table}.Sanitize()+" DEFAULT VALUES")
			r := predecessorPGReader(f, tx, ctx)
			got, err := r.readOrdinary()
			if err != ErrIdentity || !got.history[i] {
				t.Fatal("real history EXISTS was not the rejecting branch", err)
			}
		})
	}
	for i, item := range predecessorBypasses {
		t.Run("private-bypass/"+item.table, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			predecessorPGRelax(t, ctx, tx, f.schema, item.table)
			column, value := "kind", "sandbox_capture"
			switch i {
			case 0, 1:
				value = "payment.fixture"
			case 2:
				column, value = "action", "payment.fixture"
			case 3:
				column = "source_kind"
			case 5:
				value = "SANDBOX_FUNDING_CLEARING"
			}
			predecessorPGExec(t, ctx, tx, "INSERT INTO "+pgx.Identifier{f.schema, item.table}.Sanitize()+"("+pgx.Identifier{column}.Sanitize()+") VALUES($1)", value)
			r := predecessorPGReader(f, tx, ctx)
			got, err := r.readOrdinary()
			if err != ErrIdentity || !got.bypasses[i] || !got.foreignKey {
				t.Fatal("real payment bypass was not the rejecting branch", err)
			}
		})
	}
}

func TestPredecessorPostgresPrivateOrdinaryForeignKeyAndPolicy(t *testing.T) {
	f := newSnapshotFixture(t)
	for _, which := range []string{"missing", "nullable", "deferrable", "deferred", "not-validated", "wrong-parent", "wrong-child-column", "wrong-parent-column", "composite"} {
		t.Run("mandatory-fk/"+which, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			child := pgx.Identifier{f.schema, "payment_query_conflicts"}.Sanitize()
			parent := pgx.Identifier{f.schema, "payment_verified_queries"}.Sanitize()
			var constraint string
			if tx.QueryRow(ctx, `SELECT conname::text FROM pg_catalog.pg_constraint WHERE conrelid=$1::regclass AND confrelid=$2::regclass AND contype='f'`, child, parent).Scan(&constraint) != nil {
				t.Fatal("original mandatory FK fixture")
			}
			switch which {
			case "nullable":
				predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" ALTER COLUMN verified_query_id DROP NOT NULL")
			case "deferrable", "deferred":
				suffix := " DEFERRABLE INITIALLY IMMEDIATE"
				if which == "deferred" {
					suffix = " DEFERRABLE INITIALLY DEFERRED"
				}
				predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" ALTER CONSTRAINT "+pgx.Identifier{constraint}.Sanitize()+suffix)
			default:
				predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" DROP CONSTRAINT "+pgx.Identifier{constraint}.Sanitize())
				target, childColumn, parentColumn, extra := parent, "verified_query_id", "id", ""
				switch which {
				case "missing":
				case "not-validated":
					extra = " NOT VALID"
				case "wrong-parent":
					target = pgx.Identifier{f.schema, "payment_inbox"}.Sanitize()
				case "wrong-child-column":
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" ADD COLUMN qa_other_query text NOT NULL")
					childColumn = "qa_other_query"
				case "wrong-parent-column":
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+parent+" ADD COLUMN qa_other_id text UNIQUE")
					parentColumn = "qa_other_id"
				case "composite":
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" ADD COLUMN qa_extra text NOT NULL")
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+parent+" ADD COLUMN qa_extra text NOT NULL")
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+parent+" ADD UNIQUE(id,qa_extra)")
					childColumn = "verified_query_id,qa_extra"
					parentColumn = "id,qa_extra"
				}
				if which != "missing" {
					predecessorPGExec(t, ctx, tx, "ALTER TABLE "+child+" ADD CONSTRAINT qa_private_fk FOREIGN KEY("+childColumn+") REFERENCES "+target+"("+parentColumn+")"+extra)
				}
			}
			r := predecessorPGReader(f, tx, ctx)
			got, err := r.readOrdinary()
			if err != ErrIdentity || got.foreignKey {
				t.Fatal("real mandatory FK structure drift accepted", err)
			}
		})
	}
	for _, which := range []string{"enabled", "revision", "missing", "multiple", "null"} {
		t.Run("policy/"+which, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			table := pgx.Identifier{f.schema, "payment_policy"}.Sanitize()
			predecessorPGExec(t, ctx, tx, "ALTER TABLE "+table+" DISABLE TRIGGER USER")
			switch which {
			case "enabled":
				predecessorPGExec(t, ctx, tx, "UPDATE "+table+" SET enabled=true")
			case "revision":
				predecessorPGExec(t, ctx, tx, "UPDATE "+table+" SET revision=2")
			case "missing":
				predecessorPGExec(t, ctx, tx, "DELETE FROM "+table)
			case "multiple":
				predecessorPGRelax(t, ctx, tx, f.schema, "payment_policy")
				predecessorPGExec(t, ctx, tx, "INSERT INTO "+table+" DEFAULT VALUES")
			case "null":
				predecessorPGExec(t, ctx, tx, "ALTER TABLE "+table+" ALTER COLUMN enabled DROP NOT NULL")
				predecessorPGExec(t, ctx, tx, "UPDATE "+table+" SET enabled=NULL")
			}
			_, err := predecessorPGReader(f, tx, ctx).readOrdinary()
			want := ErrIdentity
			if which == "multiple" {
				want = ErrExecution
			}
			if err != want {
				t.Fatal("real noninitial policy accepted", err)
			}
		})
	}
}

func predecessorPGInsertMarker(t *testing.T, ctx context.Context, tx pgx.Tx, schema, changed string, value any) {
	t.Helper()
	_, fixtures := predecessorRowsFixture(t)
	table := predecessorTables()[2]
	row := fixtures.tables[2][0]
	names, params := []string{}, []string{}
	args := []any{}
	for i, c := range table.columns {
		names = append(names, pgx.Identifier{c.name}.Sanitize())
		params = append(params, fmt.Sprintf("$%d", i+1))
		v := row[c.name]
		var arg any
		switch v.kind {
		case predecessorText:
			arg = v.text
		case predecessorBool:
			arg = v.boolean
		case predecessorOID:
			arg = v.oid
		case predecessorInt4:
			arg = v.int4
		case predecessorTimestamp:
			arg = v.instant
		}
		if c.name == changed {
			arg = value
		}
		args = append(args, arg)
	}
	predecessorPGExec(t, ctx, tx, "INSERT INTO "+pgx.Identifier{schema, table.name}.Sanitize()+"("+strings.Join(names, ",")+") VALUES("+strings.Join(params, ",")+")", args...)
}

func TestPredecessorPostgresPrivateOriginalMockSafeProjectionAndFinite(t *testing.T) {
	f := newSnapshotFixture(t)
	schema := f.sentinel(t)
	owner := f.connect(t)
	sql, err := paymentsandboxschema.SQL(paymentsandboxschema.Mock)
	if err != nil {
		t.Fatal("fixed original Mock SQL unavailable")
	}
	install, err := owner.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatal("private Mock install begin")
	}
	predecessorPGExec(t, context.Background(), install, "SET LOCAL search_path TO "+pgx.Identifier{schema}.Sanitize()+",pg_catalog,pg_temp")
	predecessorPGExec(t, context.Background(), install, sql)
	if install.Commit(context.Background()) != nil {
		t.Fatal("private original Mock install")
	}
	for _, column := range []string{"app_schema_name", "app_owner", "app_runtime", "app_projector", "mock_schema_name", "mock_owner", "mock_runtime", "receiver_schema_name", "receiver_owner", "receiver_runtime", "api_origin", "control_origin", "callback_url"} {
		t.Run("large-original-column/"+column, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			huge := strings.Repeat("x", 1<<20)
			predecessorPGInsertMarker(t, ctx, tx, schema, column, huge)
			var length int
			if tx.QueryRow(ctx, "SELECT pg_catalog.octet_length("+pgx.Identifier{column}.Sanitize()+") FROM "+pgx.Identifier{schema, "mock_environment"}.Sanitize()).Scan(&length) != nil || length != len(huge) {
				t.Fatal("real original SQL did not retain huge test field")
			}
			rows, err := tx.Query(ctx, predecessorProjectionSQL(schema, predecessorTables()[2]), pgx.QueryExecModeSimpleProtocol)
			if err != nil {
				t.Fatal("safe original Mock projection query")
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("qualification row was hidden")
			}
			var qualified pgtype.Bool
			dest := []any{&qualified}
			for range predecessorTables()[2].columns {
				dest = append(dest, new(any))
			}
			if rows.Scan(dest...) != nil || !qualified.Valid || qualified.Bool {
				rows.Close()
				t.Fatal("huge field was not rejected by SQL qualification")
			}
			for _, v := range rows.RawValues()[1:] {
				if v != nil {
					rows.Close()
					t.Fatal("server returned an unsafe scalar instead of CASE NULL")
				}
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("safe projection EOF")
			}
			rows.Close()
			r := predecessorPGReader(f, tx, ctx)
			if got, err := r.readTable(schema, predecessorTables()[2]); got != nil || err != ErrIdentity || r.resources.bytes != 1 {
				t.Fatal("private reader accepted oversized original field", err)
			}
		})
	}
	for _, instant := range []string{"infinity", "-infinity", "0001-01-01T00:00:00Z", "10000-01-01 00:00:00+00"} {
		t.Run("finite-reject/"+instant, func(t *testing.T) {
			tx, ctx := predecessorPGTx(t, f)
			predecessorPGInsertMarker(t, ctx, tx, schema, "created_at", instant)
			var actual pgtype.Timestamptz
			// Binary pgtype decoding represents PostgreSQL's finite year10000.
			// The production simple-protocol reader separately refuses its text
			// representation because pgx's text layout accepts four-digit years.
			if err := tx.QueryRow(ctx, "SELECT created_at FROM "+pgx.Identifier{schema, "mock_environment"}.Sanitize(), pgx.QueryExecModeCacheStatement, pgx.QueryResultFormats{pgx.BinaryFormatCode}).Scan(&actual); err != nil {
				predecessorPGFailure(t, "real typed nonfinite timestamp fixture", err)
			}
			if _, err := predecessorFinite(actual); err != ErrIdentity {
				t.Fatal("typed timestamp not refused")
			}
			want := ErrIdentity
			if strings.HasPrefix(instant, "10000-") {
				want = ErrExecution
			}
			if got, err := predecessorPGReader(f, tx, ctx).readTable(schema, predecessorTables()[2]); got != nil || err != want {
				t.Fatal("reader accepted nonfinite/out-of-range stored timestamp", err)
			}
		})
	}
	t.Run("finite-valid-UTC-microsecond", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		predecessorPGInsertMarker(t, ctx, tx, schema, "created_at", "0001-01-01T00:00:00.000001Z")
		got, err := predecessorPGReader(f, tx, ctx).readTable(schema, predecessorTables()[2])
		if err != nil || len(got) != 1 || got[0]["created_at"].instant.Format("2006-01-02T15:04:05.000000Z") != "0001-01-01T00:00:00.000001Z" {
			t.Fatal("typed UTC microsecond boundary rejected", err)
		}
	})
}

func TestPredecessorPostgresPrivateSentinelsEOFAndResources(t *testing.T) {
	f := newSnapshotFixture(t)
	t.Run("singletons-empty-one-two", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		table := predecessorTable{name: "qa_predecessor_singleton", columns: []predecessorColumn{{name: "value", kind: predecessorText, min: 1, max: 10}}}
		name := pgx.Identifier{f.schema, table.name}.Sanitize()
		predecessorPGExec(t, ctx, tx, "CREATE TABLE "+name+"(value text NOT NULL)")
		for count := 0; count < 3; count++ {
			r := predecessorPGReader(f, tx, ctx)
			got, err := r.readTable(f.schema, table)
			switch count {
			case 0:
				if got != nil || err != ErrIdentity {
					t.Fatal("missing singleton accepted")
				}
			case 1:
				if err != nil || len(got) != 1 || r.resources.rows != 1 || r.resources.bytes != 2 {
					t.Fatal("singleton EOF or scalar accounting", err)
				}
			case 2:
				if got != nil || err != ErrExecution || r.resources.rows != 1 || r.resources.bytes != 4 {
					t.Fatal("second-row sentinel was not fully charged/rejected", err)
				}
			}
			if count < 2 {
				predecessorPGExec(t, ctx, tx, "INSERT INTO "+name+" VALUES('x')")
			}
		}
		var live int
		if tx.QueryRow(ctx, "SELECT 1").Scan(&live) != nil || live != 1 {
			t.Fatal("Rows not fully closed after sentinel failure")
		}
	})
	t.Run("keys-128-and-129-C-order", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		table := predecessorTable{name: "qa_predecessor_keys", columns: []predecessorColumn{{name: "key_id", kind: predecessorText, min: 1, max: 32}}, keys: true}
		name := pgx.Identifier{f.schema, table.name}.Sanitize()
		predecessorPGExec(t, ctx, tx, "CREATE TABLE "+name+"(key_id text NOT NULL)")
		predecessorPGExec(t, ctx, tx, "INSERT INTO "+name+" SELECT pg_catalog.lpad(i::text,3,'0') FROM pg_catalog.generate_series(1,128) i ORDER BY i DESC")
		r := predecessorPGReader(f, tx, ctx)
		got, err := r.readTable(f.schema, table)
		if err != nil || len(got) != 128 || got[0]["key_id"].text != "001" || got[127]["key_id"].text != "128" || r.resources.rows != 128 || r.resources.bytes != 128*4 {
			t.Fatal("complete keys EOF/C order/accounting failed", err)
		}
		predecessorPGExec(t, ctx, tx, "INSERT INTO "+name+" VALUES('129')")
		r = predecessorPGReader(f, tx, ctx)
		got, err = r.readTable(f.schema, table)
		if got != nil || err != ErrExecution || r.resources.rows != 128 || r.resources.bytes != 129*4 {
			t.Fatal("129th sentinel missing or uncharged", err)
		}
		var live int
		if tx.QueryRow(ctx, "SELECT 1").Scan(&live) != nil || live != 1 {
			t.Fatal("key sentinel Rows not closed")
		}
	})
	t.Run("cumulative-bytes-and-rows", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		table := predecessorTable{name: "qa_predecessor_payloads", columns: []predecessorColumn{{name: "key_id", kind: predecessorText, min: 1, max: 32}, {name: "proof_canonical", kind: predecessorBytes, min: 1, max: 65536}}, keys: true}
		name := pgx.Identifier{f.schema, table.name}.Sanitize()
		predecessorPGExec(t, ctx, tx, "CREATE TABLE "+name+"(key_id text NOT NULL,proof_canonical bytea NOT NULL)")
		predecessorPGExec(t, ctx, tx, "INSERT INTO "+name+" SELECT pg_catalog.lpad(i::text,3,'0'),pg_catalog.decode(pg_catalog.repeat('ab',65536),'hex') FROM pg_catalog.generate_series(1,16) i")
		r := predecessorPGReader(f, tx, ctx)
		got, err := r.readTable(f.schema, table)
		if got != nil || err != ErrExecution || r.resources.rows != 15 || r.resources.bytes > predecessorByteBudget {
			t.Fatal("cumulative byte resource limit failed", err)
		}
		predecessorPGExec(t, ctx, tx, "TRUNCATE "+name)
		predecessorPGExec(t, ctx, tx, "INSERT INTO "+name+" VALUES('a',pg_catalog.decode('ab','hex')),('b',pg_catalog.decode('ab','hex'))")
		r = predecessorPGReader(f, tx, ctx)
		r.resources.rows = 261
		got, err = r.readTable(f.schema, table)
		if got != nil || err != ErrExecution || r.resources.rows != 262 || r.resources.bytes != 6 {
			t.Fatal("cumulative business row limit failed", err)
		}
	})
	t.Run("actual-postgres-rows-error", func(t *testing.T) {
		tx, ctx := predecessorPGTx(t, f)
		rows, err := tx.Query(ctx, `SELECT true,CASE WHEN i=1 THEN 'ok' ELSE (1/(i-2))::text END FROM pg_catalog.generate_series(1,3) i`, pgx.QueryExecModeSimpleProtocol)
		if err != nil {
			t.Fatal("actual Rows error fixture query")
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("actual first row missing before SQL error")
		}
		budget := predecessorResources{}
		table := predecessorTable{columns: []predecessorColumn{{name: "value", kind: predecessorText, min: 1, max: 10}}}
		if _, err := predecessorScanRow(rows, table, &budget); err != nil {
			t.Fatal("real first bounded scalar row rejected", err)
		}
		for rows.Next() {
		}
		if rows.Err() == nil {
			t.Fatal("real PostgreSQL stream error mistaken for EOF")
		}
		rows.Close()
	})
}
