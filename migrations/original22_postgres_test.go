package migrations

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func original22PostgresFixture(t *testing.T) *prefixFixture {
	t.Helper()
	f := newPrefixFixture(t)
	prefixMust(t, "install exact original22", ApplyThrough(f.ctx, f.pool, "022_payment_runtime_attestation.sql"))
	return f
}

func original22Begin(t *testing.T, f *prefixFixture, options pgx.TxOptions) pgx.Tx {
	t.Helper()
	tx, err := f.pool.BeginTx(f.ctx, options)
	prefixMust(t, "begin original22 caller transaction", err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	})
	return tx
}

func original22AssertOpen(t *testing.T, f *prefixFixture, tx pgx.Tx) {
	t.Helper()
	var one int
	prefixMust(t, "caller retains active transaction", tx.QueryRow(f.ctx, "SELECT 1").Scan(&one))
	if one != 1 {
		t.Fatal("caller transaction query changed")
	}
}

func TestOriginal22SnapshotPostgresIsolationContextAndCallerOwnership(t *testing.T) {
	f := original22PostgresFixture(t)
	f.exec(t, `CREATE TABLE original22_sentinel(id integer PRIMARY KEY,value integer NOT NULL)`)
	f.exec(t, `INSERT INTO original22_sentinel VALUES(1,7)`)
	for _, options := range []pgx.TxOptions{
		{}, {AccessMode: pgx.ReadOnly},
		{IsoLevel: pgx.Serializable}, {IsoLevel: pgx.Serializable, AccessMode: pgx.ReadOnly},
	} {
		tx := original22Begin(t, f, options)
		if VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("non-RR accepted or unsafe error returned")
		}
		original22AssertOpen(t, f, tx)
		prefixMust(t, "caller rolls back non-RR", tx.Rollback(f.ctx))
	}
	for _, access := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
		tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: access})
		prefixMust(t, "verify original22 in caller RR", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
		if VerifyOriginal22SnapshotTx(nil, tx, f.schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("nil context accepted")
		}
		canceled, cancel := context.WithCancel(f.ctx)
		cancel()
		if VerifyOriginal22SnapshotTx(canceled, tx, f.schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("canceled context accepted")
		}
		expired, stop := context.WithDeadline(f.ctx, time.Now().Add(-time.Second))
		if VerifyOriginal22SnapshotTx(expired, tx, f.schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("expired context accepted")
		}
		stop()
		original22AssertOpen(t, f, tx)
		if access == pgx.ReadWrite {
			_, err := tx.Exec(f.ctx, `UPDATE original22_sentinel SET value=8 WHERE id=1`)
			prefixMust(t, "write caller sentinel", err)
			prefixMust(t, "verify after uncommitted caller write", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
			var outside int
			prefixMust(t, "inspect sentinel outside caller", f.pool.QueryRow(f.ctx, `SELECT value FROM original22_sentinel WHERE id=1`).Scan(&outside))
			if outside != 7 {
				t.Fatal("historical verifier committed caller write")
			}
		}
		prefixMust(t, "caller rollback RR", tx.Rollback(f.ctx))
		if VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema) != ErrVerifyOriginal22Snapshot {
			t.Fatal("finished transaction accepted")
		}
	}
	var unchanged int
	prefixMust(t, "inspect sentinel after caller rollback", f.pool.QueryRow(f.ctx, `SELECT value FROM original22_sentinel WHERE id=1`).Scan(&unchanged))
	if unchanged != 7 {
		t.Fatal("caller rollback did not preserve original sentinel")
	}
}

func TestOriginal22SnapshotPostgresMVCCAndExplicitSchemaIgnoresTempShadow(t *testing.T) {
	f := original22PostgresFixture(t)
	tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	prefixMust(t, "establish historical RR snapshot", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
	f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES('023_future.sql',$1)`, strings.Repeat("a", 64))
	prefixMust(t, "same RR keeps original committed history", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
	prefixMust(t, "caller commits original snapshot", tx.Commit(f.ctx))
	fresh := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if VerifyOriginal22SnapshotTx(f.ctx, fresh, f.schema) != ErrVerifyOriginal22Snapshot {
		t.Fatal("new snapshot accepted future history")
	}
	original22AssertOpen(t, f, fresh)
	prefixMust(t, "caller rollback future snapshot", fresh.Rollback(f.ctx))
	f.exec(t, `DELETE FROM schema_migrations WHERE name='023_future.sql'`)

	tx = original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	_, err := tx.Exec(f.ctx, `CREATE TEMP TABLE schema_migrations(name text,checksum text) ON COMMIT DROP`)
	prefixMust(t, "create temporary bad history shadow", err)
	_, err = tx.Exec(f.ctx, `INSERT INTO pg_temp.schema_migrations VALUES('fake.sql','wrong'); SET LOCAL search_path=pg_temp`)
	prefixMust(t, "select hostile temporary search path", err)
	prefixMust(t, "qualified history bypasses temporary shadow", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
	_, err = tx.Exec(f.ctx, "UPDATE "+pgx.Identifier{f.schema, "schema_migrations"}.Sanitize()+" SET checksum='wrong' WHERE name='001_core.sql'")
	prefixMust(t, "change original history in caller transaction", err)
	if VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema) != ErrVerifyOriginal22Snapshot {
		t.Fatal("temporary shadow concealed own bad original history")
	}
	original22AssertOpen(t, f, tx)
	prefixMust(t, "caller rollback own bad history and temp shadow", tx.Rollback(f.ctx))
}

func TestOriginal22SnapshotPostgresRejectsPhysicalHistoryCorruption(t *testing.T) {
	f := original22PostgresFixture(t)
	for _, test := range []struct {
		name string
		sql  string
	}{
		{"missing", `DELETE FROM schema_migrations WHERE name='022_payment_runtime_attestation.sql'`},
		{"future", `INSERT INTO schema_migrations(name,checksum) VALUES('023_future.sql',repeat('a',64))`},
		{"wrong_checksum", `UPDATE schema_migrations SET checksum=repeat('a',64) WHERE name='001_core.sql'`},
		{"huge_name", `ALTER TABLE schema_migrations DROP CONSTRAINT schema_migrations_pkey; UPDATE schema_migrations SET name=repeat('a',2000000) WHERE name='001_core.sql'`},
		{"huge_checksum", `UPDATE schema_migrations SET checksum=repeat('a',2000000) WHERE name='001_core.sql'`},
		{"null_checksum", `ALTER TABLE schema_migrations ALTER COLUMN checksum DROP NOT NULL; UPDATE schema_migrations SET checksum=NULL WHERE name='001_core.sql'`},
		{"duplicate_replaces_missing", `ALTER TABLE schema_migrations DROP CONSTRAINT schema_migrations_pkey; DELETE FROM schema_migrations WHERE name='022_payment_runtime_attestation.sql'; INSERT INTO schema_migrations SELECT * FROM schema_migrations WHERE name='001_core.sql'`},
		{"null_name", `ALTER TABLE schema_migrations DROP CONSTRAINT schema_migrations_pkey; ALTER TABLE schema_migrations ALTER COLUMN name DROP NOT NULL; UPDATE schema_migrations SET name=NULL WHERE name='001_core.sql'`},
		{"more_than_extra_row", `INSERT INTO schema_migrations(name,checksum) SELECT 'future-'||n||'.sql',repeat('a',64) FROM generate_series(1,100) AS n`},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			_, err := tx.Exec(f.ctx, test.sql)
			prefixMust(t, "prepare own corrupt physical history", err)
			if VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema) != ErrVerifyOriginal22Snapshot {
				t.Fatal("corrupt historical rows accepted or diagnostics exposed")
			}
			original22AssertOpen(t, f, tx)
			prefixMust(t, "caller rollback physical corruption", tx.Rollback(f.ctx))
		})
	}
	for _, mode := range []string{"missing_table", "missing_schema"} {
		t.Run(mode, func(t *testing.T) {
			tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			schema := f.schema
			if mode == "missing_table" {
				_, err := tx.Exec(f.ctx, `ALTER TABLE schema_migrations RENAME TO original22_missing_history`)
				prefixMust(t, "hide original history table", err)
			} else {
				schema = f.schema + "_absent"
			}
			if VerifyOriginal22SnapshotTx(f.ctx, tx, schema) != ErrVerifyOriginal22Snapshot {
				t.Fatal("missing target accepted or database diagnostic exposed")
			}
			// SQL errors may abort the original transaction; caller owns cleanup.
			prefixMust(t, "caller rollback absent target", tx.Rollback(f.ctx))
		})
	}
}

func TestOriginal22SnapshotPostgresOnlyIgnoresInheritedHistory(t *testing.T) {
	f := original22PostgresFixture(t)
	f.exec(t, `CREATE TABLE original22_history_child() INHERITS(schema_migrations)`)
	f.exec(t, `INSERT INTO original22_history_child(name,checksum) VALUES('inherited.sql',repeat('b',64))`)
	tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	prefixMust(t, "verify only the selected physical history", VerifyOriginal22SnapshotTx(f.ctx, tx, f.schema))
	prefixMust(t, "caller commit selected physical history", tx.Commit(f.ctx))
}

func TestOriginal22SnapshotPostgresCancellationReturnsSafeErrorAndCallerRollsBack(t *testing.T) {
	f := original22PostgresFixture(t)
	blocker := original22Begin(t, f, pgx.TxOptions{})
	_, err := blocker.Exec(f.ctx, `LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE`)
	prefixMust(t, "block original history read", err)
	tx := original22Begin(t, f, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	canceledConn := tx.Conn()
	canceledPID := canceledConn.PgConn().PID()
	work, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer cancel()
	if VerifyOriginal22SnapshotTx(work, tx, f.schema) != ErrVerifyOriginal22Snapshot || work.Err() == nil {
		t.Fatal("blocked canceled history verification did not fail safely")
	}
	cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	destroyed := canceledConn.IsClosed()
	if tx.Rollback(cleanup) != nil {
		// Cancellation may make pgx discard its connection. If rollback cannot
		// confirm idle, the caller closes it instead of returning it for reuse.
		prefixMust(t, "caller closes uncertain canceled connection", canceledConn.Close(cleanup))
		if !canceledConn.IsClosed() {
			t.Fatal("uncertain canceled connection remained reusable")
		}
		destroyed = true
	}
	prefixMust(t, "caller releases original history blocker", blocker.Rollback(cleanup))
	// pgxpool releases a broken connection through puddle.Destroy, whose
	// destructor runs asynchronously and decrements acquired only after pgconn
	// CleanupDone. Observe that bounded lifecycle instead of treating its first
	// post-Rollback statistic as a permanently retained application lease.
	initialPending := f.pool.Stat().AcquiredConns()
	started := time.Now()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		alive := false
		if destroyed {
			prefixMust(t, "observe exact canceled backend drain", f.pool.QueryRow(cleanup,
				`SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND application_name=$2)`, canceledPID, f.schema).Scan(&alive))
		}
		pending := f.pool.Stat().AcquiredConns()
		if pending == 0 && !alive {
			break
		}
		select {
		case <-cleanup.Done():
			t.Fatalf("canceled connection did not finish bounded cleanup (acquired=%d, backend_alive=%t)", pending, alive)
		case <-ticker.C:
		}
	}
	t.Logf("canceled connection cleanup completed: destroyed=%t initial_acquired=%d elapsed=%s", destroyed, initialPending, time.Since(started))
	borrower, err := f.pool.Acquire(cleanup)
	prefixMust(t, "borrow after canceled connection cleanup", err)
	if borrower.Conn().PgConn().TxStatus() != 'I' || destroyed && borrower.Conn().PgConn().PID() == canceledPID {
		borrower.Release()
		t.Fatal("pool reused a canceled or non-idle connection")
	}
	borrower.Release()
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("post-cancellation borrower retained a pool lease")
	}
}
