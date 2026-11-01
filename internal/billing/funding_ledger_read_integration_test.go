package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
)

type fundingLedgerReadFixture struct {
	*fixture
	app, projector *pgxpool.Pool
	schema         string
	roles          []string
}

func fundingLedgerReadMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal("isolated reader fixture operation failed")
	}
}

func fundingLedgerReadReal(t *testing.T) *fundingLedgerReadFixture {
	t.Helper()
	f := fundingFixture(t)
	x := &fundingLedgerReadFixture{fixture: f}
	fundingLedgerReadMust(t, f.pool.QueryRow(f.ctx, `SELECT pg_catalog.current_schema()`).Scan(&x.schema))
	var pools []*pgxpool.Pool
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, p := range pools {
			p.Close()
		}
		var remaining int
		fundingLedgerReadMust(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_stat_activity WHERE usename=ANY($1::pg_catalog.text[])`, x.roles).Scan(&remaining))
		if remaining != 0 {
			t.Error("reader role backends not closed")
		}
		for _, r := range x.roles {
			_, e := f.pool.Exec(ctx, "DROP OWNED BY "+pgx.Identifier{r}.Sanitize())
			fundingLedgerReadMust(t, e)
			_, e = f.pool.Exec(ctx, "DROP ROLE "+pgx.Identifier{r}.Sanitize())
			fundingLedgerReadMust(t, e)
		}
		fundingLedgerReadMust(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_roles WHERE rolname=ANY($1::pg_catalog.text[])`, x.roles).Scan(&remaining))
		if remaining != 0 {
			t.Error("reader roles not removed")
		}
		_, e := f.pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{x.schema}.Sanitize()+" CASCADE")
		fundingLedgerReadMust(t, e)
		fundingLedgerReadMust(t, f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspname=$1`, x.schema).Scan(&remaining))
		if remaining != 0 {
			t.Error("reader schema not removed")
		}
	})
	for _, suffix := range []string{"app", "projector"} {
		r := "flread_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_" + suffix
		var b [24]byte
		_, e := rand.Read(b[:])
		fundingLedgerReadMust(t, e)
		secret := hex.EncodeToString(b[:])
		_, e = f.pool.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{r}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+secret+"'")
		fundingLedgerReadMust(t, e)
		x.roles = append(x.roles, r)
		c := f.pool.Config().Copy()
		c.AfterConnect = nil
		c.ConnConfig.User = r
		c.ConnConfig.Password = secret
		c.MaxConns = 3
		c.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{x.schema}.Sanitize() + ",pg_catalog,pg_temp"
		p, e := pgxpool.NewWithConfig(f.ctx, c)
		fundingLedgerReadMust(t, e)
		pools = append(pools, p)
	}
	x.app, x.projector = pools[0], pools[1]
	fundingLedgerReadMust(t, operations.ProvisionFor(f.ctx, f.pool, x.schema, x.roles[0], x.roles[1]))
	for i, p := range pools {
		var current, session string
		fundingLedgerReadMust(t, p.QueryRow(f.ctx, `SELECT current_user,session_user`).Scan(&current, &session))
		if current != x.roles[i] || current != session || p.Config().ConnConfig.User != current {
			t.Fatal("fixture is not actual restricted LOGIN")
		}
	}
	return x
}

func (x *fundingLedgerReadFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, e := x.pool.Exec(x.ctx, q, args...)
	fundingLedgerReadMust(t, e)
}

func fundingLedgerReadBegin(t *testing.T, p *pgxpool.Pool, opts pgx.TxOptions) pgx.Tx {
	t.Helper()
	tx, e := p.BeginTx(context.Background(), opts)
	fundingLedgerReadMust(t, e)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(ctx)
	})
	return tx
}

func (x *fundingLedgerReadFixture) snapshot(t *testing.T, tx pgx.Tx) fundingLedgerSnapshot {
	t.Helper()
	s, e := readFundingLedgerSnapshotTx(x.ctx, tx)
	fundingLedgerReadMust(t, e)
	if s.check.Scope != "supplied_ledger_rows" || len(s.journals) != len(s.rows.Journals) {
		t.Fatal("reader lost declared scope or journal facts")
	}
	for i, j := range s.journals {
		r := s.rows.Journals[i]
		if j.ID != r.ID || j.TenantID != r.TenantID || j.OperationID != r.OperationID || j.Currency != r.Currency || j.CreatedAt.Location() != time.UTC {
			t.Fatal("journal facts not same-row/UTC")
		}
	}
	return s
}

func TestPostgresFundingLedgerReadBusinessAndCallerRR(t *testing.T) {
	x := fundingLedgerReadReal(t)
	r := x.reserve(t)
	defer x.store.EndRequest(r.RequestID)
	x.final(t, r, confirmed(), "completed", "done", "none", false)
	s, e := x.store.Settle(x.ctx, SettleInput{TenantID: x.tenant, RequestID: r.RequestID})
	fundingLedgerReadMust(t, e)
	ref := uuid.NewString()
	refund, e := x.store.CreateRefund(x.ctx, RefundInput{TenantID: x.tenant, ActorID: x.operator, SettlementID: s.ID, BusinessRef: ref, Reason: "reader fixture", Amount: 5, IdempotencyKey: ref, RequestHMAC: ref})
	fundingLedgerReadMust(t, e)
	_, e = x.store.ApproveRefund(x.ctx, ApprovalInput{TenantID: x.tenant, OrderID: refund.ID, ActorID: x.approver})
	fundingLedgerReadMust(t, e)
	x.exec(t, `CREATE TABLE reader_sentinel(id integer PRIMARY KEY,value integer NOT NULL); INSERT INTO reader_sentinel VALUES(1,7)`)
	x.exec(t, "GRANT SELECT,UPDATE ON reader_sentinel TO "+pgx.Identifier{x.roles[0]}.Sanitize())
	for _, mode := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
		tx := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: mode})
		if mode == pgx.ReadWrite {
			_, e = tx.Exec(x.ctx, `UPDATE reader_sentinel SET value=8 WHERE id=1`)
			fundingLedgerReadMust(t, e)
		}
		got := x.snapshot(t, tx)
		if !got.check.RowsConsistent || got.check.Scanned.Journals < 4 || got.check.Scanned.Lines < 8 {
			t.Fatal("real credit/reserve/settle/refund missing or inconsistent")
		}
		var outside int
		fundingLedgerReadMust(t, x.pool.QueryRow(x.ctx, `SELECT value FROM reader_sentinel`).Scan(&outside))
		if outside != 7 {
			t.Fatal("reader committed caller write")
		}
		var inside int
		fundingLedgerReadMust(t, tx.QueryRow(x.ctx, `SELECT value FROM reader_sentinel`).Scan(&inside))
		if mode == pgx.ReadWrite && inside != 8 {
			t.Fatal("reader changed caller snapshot")
		}
		fundingLedgerReadMust(t, tx.Rollback(x.ctx))
	}
	old := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	before := x.snapshot(t, old)
	x.fund(t, 1)
	again := x.snapshot(t, old)
	if !reflect.DeepEqual(before, again) {
		t.Fatal("reader switched caller snapshot")
	}
	newtx := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if x.snapshot(t, newtx).check.Scanned.Journals != before.check.Scanned.Journals+1 {
		t.Fatal("new RR did not observe committed journal")
	}
}

func TestPostgresFundingLedgerReadIdentityAndPhysicalPrivileges(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, opts := range []pgx.TxOptions{{}, {IsoLevel: pgx.Serializable}} {
		tx := fundingLedgerReadBegin(t, x.app, opts)
		got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
		fundingLedgerRequireZero(t, got, e, fundingLedgerReadInput)
	}
	tx := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	fundingLedgerReadMust(t, tx.Rollback(x.ctx))
	got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadInput)
	owner := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e = owner.Exec(x.ctx, "SET ROLE "+pgx.Identifier{x.roles[0]}.Sanitize())
	fundingLedgerReadMust(t, e)
	got, e = readFundingLedgerSnapshotTx(x.ctx, owner)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadInput)
	fundingLedgerReadMust(t, owner.Rollback(x.ctx))
	aborted := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e = aborted.Exec(x.ctx, `SELECT 1/0`)
	if e == nil {
		t.Fatal("aborted fixture did not fail")
	}
	got, e = readFundingLedgerSnapshotTx(x.ctx, aborted)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadInput)
	fundingLedgerReadMust(t, aborted.Rollback(x.ctx))
	projection := fundingLedgerReadBegin(t, x.projector, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	got, e = readFundingLedgerSnapshotTx(x.ctx, projection)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
	fundingLedgerReadMust(t, projection.Rollback(x.ctx))
	x.exec(t, "REVOKE SELECT ON ledger_accounts FROM "+pgx.Identifier{x.roles[0]}.Sanitize())
	app := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	got, e = readFundingLedgerSnapshotTx(x.ctx, app)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
}

func TestPostgresFundingLedgerReadCatalogAndHistoryRefusals(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, tc := range []struct{ name, sql string }{
		{"rls", `ALTER TABLE ledger_accounts ENABLE ROW LEVEL SECURITY`},
		{"forced_rls", `ALTER TABLE ledger_accounts FORCE ROW LEVEL SECURITY`},
		{"view", `ALTER TABLE journal_lines RENAME TO original_lines; CREATE VIEW journal_lines AS SELECT * FROM original_lines`},
		{"inheritance", `CREATE TABLE child_accounts() INHERITS(ledger_accounts)`},
		{"column_domain", `CREATE DOMAIN ledger_amount AS bigint; ALTER TABLE ledger_accounts ALTER COLUMN balance TYPE ledger_amount`},
		{"missing_history", `DELETE FROM schema_migrations WHERE name=(SELECT max(name) FROM schema_migrations)`},
		{"checksum", `UPDATE schema_migrations SET checksum='wrong' WHERE name=(SELECT min(name) FROM schema_migrations)`},
		{"missing_pk", `ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_pkey`},
		{"deferred_pk", `ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_pkey; ALTER TABLE journal_lines ADD PRIMARY KEY(id) DEFERRABLE`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, e := tx.Exec(x.ctx, tc.sql)
			fundingLedgerReadMust(t, e)
			got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
			fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
			var one int
			fundingLedgerReadMust(t, tx.QueryRow(x.ctx, `SELECT 1`).Scan(&one))
			fundingLedgerReadMust(t, tx.Rollback(x.ctx))
		})
	}
	tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e := tx.Exec(x.ctx, `CREATE TEMP TABLE schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL); INSERT INTO pg_temp.schema_migrations SELECT * FROM `+pgx.Identifier{x.schema, "schema_migrations"}.Sanitize())
	fundingLedgerReadMust(t, e)
	got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
	fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
	fundingLedgerReadMust(t, tx.Rollback(x.ctx))
}

func TestPostgresFundingLedgerReadHistoryFunctionsCannotExecuteShadows(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, tc := range []struct{ name, signature, returns string }{
		{"current_setting_exact", "current_setting(pg_catalog.text)", "pg_catalog.text"},
		{"to_regclass_exact", "to_regclass(pg_catalog.text)", "pg_catalog.regclass"},
		{"current_setting_default", "current_setting(pg_catalog.text, pg_catalog.bool DEFAULT false)", "pg_catalog.text"},
		{"to_regclass_variadic", "to_regclass(VARIADIC pg_catalog.text[])", "pg_catalog.regclass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, e := tx.Exec(x.ctx, `CREATE TABLE shadow_sentinel(value integer NOT NULL); INSERT INTO shadow_sentinel VALUES(0); CREATE SEQUENCE shadow_calls`)
			fundingLedgerReadMust(t, e)
			q := "CREATE FUNCTION " + pgx.Identifier{x.schema}.Sanitize() + "." + tc.signature + " RETURNS " + tc.returns + " LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_catalog.nextval('" + pgx.Identifier{x.schema, "shadow_calls"}.Sanitize() + "'::pg_catalog.regclass); UPDATE " + pgx.Identifier{x.schema, "shadow_sentinel"}.Sanitize() + " SET value=1; RAISE EXCEPTION 'shadow must not run'; END $$"
			_, e = tx.Exec(x.ctx, q)
			fundingLedgerReadMust(t, e)
			got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
			fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
			var value int
			var called bool
			fundingLedgerReadMust(t, tx.QueryRow(x.ctx, `SELECT value FROM shadow_sentinel`).Scan(&value))
			fundingLedgerReadMust(t, tx.QueryRow(x.ctx, `SELECT is_called FROM shadow_calls`).Scan(&called))
			if value != 0 || called || tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("shadow function was executed")
			}
			fundingLedgerReadMust(t, tx.Rollback(x.ctx))
		})
	}
}

func TestPostgresFundingLedgerReadCanonicalExtraFacts(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, tc := range []struct{ name, assignment string }{
		{"zero_xid", `created_txid=0`}, {"negative_xid", `created_txid=-1`}, {"zero_time", `created_at='0001-01-01 00:00:00+00'`}, {"year_overflow", `created_at='10000-01-01 00:00:00+00'`}, {"infinity", `created_at='infinity'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, e := tx.Exec(x.ctx, `ALTER TABLE journals DISABLE TRIGGER USER; UPDATE journals SET `+tc.assignment)
			fundingLedgerReadMust(t, e)
			got, e := readFundingLedgerSnapshotTx(x.ctx, tx)
			fundingLedgerRequireZero(t, got, e, fundingLedgerReadExecution)
			fundingLedgerReadMust(t, tx.Rollback(x.ctx))
		})
	}
	tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e := tx.Exec(x.ctx, `SET LOCAL TIME ZONE 'Asia/Shanghai'; ALTER TABLE journals DISABLE TRIGGER USER; UPDATE journals SET kind='',reference='',actor='',created_at='2020-01-01 12:00:00.123456+08'`)
	fundingLedgerReadMust(t, e)
	s := x.snapshot(t, tx)
	if s.journals[0].CreatedAt.Hour() != 4 || s.journals[0].CreatedAt.Nanosecond() != 123456000 || s.journals[0].Kind != "" {
		t.Fatal("valid empty fields/local timezone not preserved")
	}
}

func TestPostgresFundingLedgerReadBadRowsAreNotJoinedAway(t *testing.T) {
	x := fundingLedgerReadReal(t)
	tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e := tx.Exec(x.ctx, `SET LOCAL session_replication_role=replica; DROP INDEX ledger_customer_account; ALTER TABLE journals DROP CONSTRAINT journals_operation_id_key; ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_journal_id_account_id_key;
 INSERT INTO ledger_accounts(id,tenant_id,kind,balance) VALUES('foreign-account','foreign-tenant','AVAILABLE',0);
 INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) SELECT 'bad-journal',tenant_id,operation_id,'','','' FROM journals LIMIT 1;
 INSERT INTO journal_lines(id,journal_id,account_id,signed_delta) VALUES('cross','bad-journal','foreign-account',1),('missing-account','bad-journal','missing-account',2),('missing-journal','missing-journal','foreign-account',3),('same-pair','bad-journal','foreign-account',4)`)
	fundingLedgerReadMust(t, e)
	s := x.snapshot(t, tx)
	m := s.check.Mismatches
	if s.check.RowsConsistent || m.MissingAccounts != 1 || m.MissingJournals != 1 || m.CrossTenantLines != 2 || m.DuplicateOperationIDs != 1 || m.DuplicateJournalAccounts != 1 || m.UnbalancedJournals != 1 || m.AccountBalanceMismatches != 1 {
		t.Fatal("independent mismatch directions lost raw rows")
	}
	if s.check.Scanned.Lines != 6 {
		t.Fatal("dangling lines filtered")
	}
}

type fundingLedgerCountingTx struct {
	pgx.Tx
	pageCalls map[string]int
}

func (tx *fundingLedgerCountingTx) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	for _, table := range []string{"ledger_accounts", "journals", "journal_lines"} {
		if strings.HasPrefix(q, "SELECT ") && strings.Contains(q, ` FROM `) && strings.HasSuffix(q, fundingLedgerCursorSQL) && strings.Contains(q, `"`+table+`"`) {
			tx.pageCalls[table]++
		}
	}
	return tx.Tx.Query(ctx, q, args...)
}

func fundingLedgerSyntheticTx(t *testing.T, x *fundingLedgerReadFixture) pgx.Tx {
	t.Helper()
	tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e := tx.Exec(x.ctx, `SET LOCAL session_replication_role=replica; TRUNCATE ledger_accounts,journals,journal_lines CASCADE; DROP INDEX ledger_platform_account; DROP INDEX ledger_customer_account`)
	fundingLedgerReadMust(t, e)
	return tx
}

func TestPostgresFundingLedgerReadPaginationAndExplain(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, n := range []int{0, 1, 128, 129, 256, 300} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			tx := fundingLedgerSyntheticTx(t, x)
			// Owner-only malformed rows exercise complete table enumeration. PKs
			// remain intact; no production write validator is used as an oracle.
			_, e := tx.Exec(x.ctx, `INSERT INTO ledger_accounts(id,kind,balance) SELECT CASE WHEN i=0 THEN '' WHEN i=$1-1 THEN '模型🦋' ELSE pg_catalog.lpad(i::pg_catalog.text,6,'0') END,'REVENUE',0 FROM pg_catalog.generate_series(0,$1::integer-1) i`, n)
			fundingLedgerReadMust(t, e)
			_, e = tx.Exec(x.ctx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) SELECT id,'tenant',id,'','','' FROM ledger_accounts;
 INSERT INTO journal_lines(id,journal_id,account_id,signed_delta) SELECT id,id,id,0 FROM ledger_accounts`)
			fundingLedgerReadMust(t, e)
			wrapped := &fundingLedgerCountingTx{Tx: tx, pageCalls: map[string]int{}}
			s := x.snapshot(t, wrapped)
			if len(s.rows.Accounts) != n || len(s.rows.Journals) != n || len(s.rows.Lines) != n {
				t.Fatal("real keyset lost rows")
			}
			for _, table := range []string{"ledger_accounts", "journals", "journal_lines"} {
				if wrapped.pageCalls[table] != n/128+1 {
					t.Fatal("real exact-tail EOF not queried")
				}
			}
			if n > 0 && (s.rows.Accounts[0].ID != "" || s.rows.Accounts[0].TenantID != nil) {
				t.Fatal("empty id or NULL tenant lost")
			}
			if n == 300 {
				rows, e := tx.Query(x.ctx, "EXPLAIN (FORMAT TEXT) "+fundingLedgerPageSQL(x.schema, "ledger_accounts"), false, "000127")
				fundingLedgerReadMust(t, e)
				for rows.Next() {
					var line string
					fundingLedgerReadMust(t, rows.Scan(&line))
					line = strings.ReplaceAll(line, x.schema, "fixture_schema")
					t.Log("keyset explain:", line)
				}
				rows.Close()
				fundingLedgerReadMust(t, rows.Err())
			}
			fundingLedgerReadMust(t, tx.Rollback(x.ctx))
		})
	}
	// More than one page of journals contributes to the same two accounts;
	// journal/line sums must be checked only after all tables reach EOF.
	tx := fundingLedgerSyntheticTx(t, x)
	_, e := tx.Exec(x.ctx, `INSERT INTO ledger_accounts(id,kind,balance) VALUES('revenue','REVENUE',300),('clearing','FUNDING_CLEARING',-300);
 INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) SELECT i::text,'tenant',i::text,'','','' FROM generate_series(1,300) i;
 INSERT INTO journal_lines(id,journal_id,account_id,signed_delta) SELECT 'a'||id,id,'revenue',1 FROM journals UNION ALL SELECT 'b'||id,id,'clearing',-1 FROM journals`)
	fundingLedgerReadMust(t, e)
	if !x.snapshot(t, tx).check.RowsConsistent {
		t.Fatal("cross-page arithmetic not combined globally")
	}
}

func TestPostgresFundingLedgerReadResourceLimits(t *testing.T) {
	x := fundingLedgerReadReal(t)
	for _, tc := range []struct{ name, sql string }{
		{"rows", `INSERT INTO ledger_accounts(id,kind,balance) SELECT i::text,'REVENUE',0 FROM generate_series(1,100001) i`},
		{"extra_journal_bytes", `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES('j','tenant','o','',repeat('x',16777216),'')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := fundingLedgerSyntheticTx(t, x)
			if tc.name == "rows" {
				// Only this resource fixture gets a supporting C-order index so
				// 782 pages do not turn a row-limit test into a repeated-sort load.
				_, e := tx.Exec(x.ctx, `CREATE INDEX reader_resource_c ON ledger_accounts(id COLLATE pg_catalog."C")`)
				fundingLedgerReadMust(t, e)
			}
			_, e := tx.Exec(x.ctx, tc.sql)
			fundingLedgerReadMust(t, e)
			s, e := readFundingLedgerSnapshotTx(x.ctx, tx)
			fundingLedgerRequireZero(t, s, e, fundingLedgerReadResource)
			fundingLedgerReadMust(t, tx.Rollback(x.ctx))
		})
	}
}

func TestPostgresFundingLedgerReadBuiltinPagingCannotBeShadowed(t *testing.T) {
	x := fundingLedgerReadReal(t)
	tx := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, e := tx.Exec(x.ctx, `CREATE DOMAIN text AS pg_catalog.int8;
 CREATE COLLATION "C" FROM pg_catalog."POSIX";
 CREATE FUNCTION wrong_compare(pg_catalog.text,pg_catalog.text) RETURNS pg_catalog.bool LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'custom compare must not run'; END $$;
 CREATE OPERATOR > (FUNCTION=wrong_compare,LEFTARG=pg_catalog.text,RIGHTARG=pg_catalog.text);
 CREATE OPERATOR < (FUNCTION=wrong_compare,LEFTARG=pg_catalog.text,RIGHTARG=pg_catalog.text)`)
	fundingLedgerReadMust(t, e)
	if !x.snapshot(t, tx).check.RowsConsistent {
		t.Fatal("builtin registry shadow changed ledger")
	}
	// Exercise a real cursor page too: the first-page boolean can short-circuit
	// the comparison and alone would not probe the qualified > operator.
	rows, e := tx.Query(x.ctx, fundingLedgerPageSQL(x.schema, "ledger_accounts"), false, "")
	fundingLedgerReadMust(t, e)
	count := 0
	for rows.Next() {
		var id, kind, balance string
		var tenant *string
		fundingLedgerReadMust(t, rows.Scan(&id, &tenant, &kind, &balance))
		count++
	}
	rows.Close()
	fundingLedgerReadMust(t, rows.Err())
	if count == 0 {
		t.Fatal("operator shadow probe did not read a cursor page")
	}
	var one int
	fundingLedgerReadMust(t, tx.QueryRow(x.ctx, `SELECT 1`).Scan(&one))
}

func TestPostgresFundingLedgerReadQueryCancellation(t *testing.T) {
	x := fundingLedgerReadReal(t)
	block := fundingLedgerReadBegin(t, x.pool, pgx.TxOptions{})
	_, e := block.Exec(x.ctx, `LOCK TABLE ledger_accounts IN ACCESS EXCLUSIVE MODE`)
	fundingLedgerReadMust(t, e)
	tx := fundingLedgerReadBegin(t, x.app, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	pid := tx.Conn().PgConn().PID()
	ctx, cancel := context.WithCancel(x.ctx)
	defer cancel()
	type result struct {
		s fundingLedgerSnapshot
		e error
	}
	done := make(chan result, 1)
	go func() { s, e := readFundingLedgerSnapshotTx(ctx, tx); done <- result{s, e} }()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		fundingLedgerReadMust(t, x.pool.QueryRow(x.ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock')`, pid).Scan(&waiting))
		if waiting {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("reader did not reach real cancellable query wait")
	}
	cancel()
	select {
	case got := <-done:
		fundingLedgerRequireZero(t, got.s, got.e, fundingLedgerReadCanceled)
	case <-time.After(4 * time.Second):
		t.Fatal("reader cancellation did not return")
	}
	fundingLedgerReadMust(t, block.Rollback(x.ctx))
	_ = tx.Rollback(x.ctx)
}
