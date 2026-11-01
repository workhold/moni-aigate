package migrations

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestVerifySnapshotTxRequiresCallerTransaction(t *testing.T) {
	if VerifySnapshotTx(context.Background(), nil) == nil {
		t.Fatal("nil transaction accepted")
	}
	f := newPrefixFixture(t)
	tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	prefixMust(t, "begin transaction", err)
	if VerifySnapshotTx(nil, tx) == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if VerifySnapshotTx(ctx, tx) != ErrVerifySnapshot {
		t.Fatal("canceled context accepted for active transaction")
	}
	var stillOpen int
	prefixMust(t, "pre-canceled context leaves caller transaction open", tx.QueryRow(f.ctx, `SELECT 1`).Scan(&stillOpen))
	prefixMust(t, "close transaction", tx.Rollback(f.ctx))
	if VerifySnapshotTx(f.ctx, tx) == nil {
		t.Fatal("closed transaction accepted")
	}
	if VerifySnapshotTx(ctx, tx) == nil {
		t.Fatal("canceled context accepted")
	}
}

func TestVerifySnapshotTxPostgresIsolationModesAndCallerOwnsWrites(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install current manifest", Apply(f.ctx, f.pool))
	f.exec(t, `CREATE TABLE snapshot_sentinel(id integer PRIMARY KEY, value integer NOT NULL)`)
	f.exec(t, `INSERT INTO snapshot_sentinel VALUES(1,7)`)
	for _, options := range []pgx.TxOptions{
		{}, {AccessMode: pgx.ReadOnly},
		{IsoLevel: pgx.Serializable}, {IsoLevel: pgx.Serializable, AccessMode: pgx.ReadOnly},
	} {
		tx, err := f.pool.BeginTx(f.ctx, options)
		prefixMust(t, "begin non-RR", err)
		if VerifySnapshotTx(f.ctx, tx) == nil {
			t.Fatal("non-RR accepted")
		}
		prefixMust(t, "caller rollback", tx.Rollback(f.ctx))
	}
	for _, mode := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
		tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: mode})
		prefixMust(t, "begin RR", err)
		prefixMust(t, "verify caller RR", VerifySnapshotTx(f.ctx, tx))
		if mode == pgx.ReadWrite {
			if VerifyReadOnlyTx(f.ctx, tx) == nil {
				t.Fatal("read-only helper weakened")
			}
			_, err = tx.Exec(f.ctx, `UPDATE snapshot_sentinel SET value=8 WHERE id=1`)
			prefixMust(t, "caller uncommitted write", err)
			prefixMust(t, "verify after own write", VerifySnapshotTx(f.ctx, tx))
			var outside int
			prefixMust(t, "read on independent connection", f.pool.QueryRow(f.ctx, `SELECT value FROM snapshot_sentinel WHERE id=1`).Scan(&outside))
			if outside != 7 {
				t.Fatal("helper committed caller write")
			}
		}
		var inside int
		prefixMust(t, "caller can still query", tx.QueryRow(f.ctx, `SELECT value FROM snapshot_sentinel WHERE id=1`).Scan(&inside))
		want := 7
		if mode == pgx.ReadWrite {
			want = 8
		}
		if inside != want {
			t.Fatal("helper lost original transaction")
		}
		prefixMust(t, "caller rolls back", tx.Rollback(f.ctx))
	}
	var value int
	prefixMust(t, "sentinel unchanged after rollback", f.pool.QueryRow(f.ctx, `SELECT value FROM snapshot_sentinel WHERE id=1`).Scan(&value))
	if value != 7 {
		t.Fatal("uncommitted write escaped rollback")
	}
}

func TestVerifySnapshotTxPostgresExactManifestAndMVCC(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install current manifest", Apply(f.ctx, f.pool))
	tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite})
	prefixMust(t, "begin RR write snapshot", err)
	prefixMust(t, "establish original history snapshot", VerifySnapshotTx(f.ctx, tx))
	f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES('999_unknown.sql','unknown')`)
	prefixMust(t, "same RR preserves original history", VerifySnapshotTx(f.ctx, tx))
	prefixMust(t, "caller commit", tx.Commit(f.ctx))
	assertInvalid := func(label string) {
		t.Helper()
		tx, e := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		prefixMust(t, "begin "+label, e)
		if VerifySnapshotTx(f.ctx, tx) != ErrVerifySnapshot {
			t.Fatal(label + " accepted or exposed database diagnostics")
		}
		var n int
		prefixMust(t, "failed check leaves caller transaction open", tx.QueryRow(f.ctx, `SELECT 1`).Scan(&n))
		prefixMust(t, "caller rollback "+label, tx.Rollback(f.ctx))
	}
	assertInvalid("new snapshot with unknown migration")
	f.exec(t, `DELETE FROM schema_migrations WHERE name='999_unknown.sql'`)
	tx, err = f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	prefixMust(t, "begin own history change", err)
	_, err = tx.Exec(f.ctx, `UPDATE schema_migrations SET checksum='wrong' WHERE name=(SELECT min(name) FROM schema_migrations)`)
	prefixMust(t, "modify own visible history", err)
	if VerifySnapshotTx(f.ctx, tx) == nil {
		t.Fatal("own bad checksum accepted")
	}
	prefixMust(t, "caller rollback own history", tx.Rollback(f.ctx))
	f.exec(t, `DELETE FROM schema_migrations WHERE name=(SELECT max(name) FROM schema_migrations)`)
	assertInvalid("missing latest migration")
	f.exec(t, `DROP TABLE schema_migrations`)
	assertInvalid("missing history table")
}
