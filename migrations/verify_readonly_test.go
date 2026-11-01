package migrations

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestVerifyReadOnlyTxRequiresExistingSnapshot(t *testing.T) {
	if VerifyReadOnlyTx(context.Background(), nil) == nil {
		t.Fatal("nil transaction accepted")
	}
}

func TestVerifyReadOnlyTxPostgresClosedManifestAndCallerOwnership(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install complete current manifest", Apply(f.ctx, f.pool))
	for _, options := range []pgx.TxOptions{{}, {IsoLevel: pgx.RepeatableRead}, {AccessMode: pgx.ReadOnly}} {
		tx, e := f.pool.BeginTx(f.ctx, options)
		prefixMust(t, "begin incompatible snapshot", e)
		if VerifyReadOnlyTx(f.ctx, tx) == nil {
			t.Fatal("non-RR-readonly transaction accepted")
		}
		prefixMust(t, "caller rolls back incompatible snapshot", tx.Rollback(f.ctx))
	}
	tx, e := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	prefixMust(t, "begin RR readonly snapshot", e)
	prefixMust(t, "verify full current manifest in caller snapshot", VerifyReadOnlyTx(f.ctx, tx))
	var count int
	prefixMust(t, "caller snapshot is still open", tx.QueryRow(f.ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count))
	if count < 20 {
		t.Fatal("verification tolerated old prefix")
	}
	f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES('999_unknown.sql','unknown')`)
	// The existing RR snapshot must retain its complete original manifest.
	prefixMust(t, "same RR still sees original manifest", VerifyReadOnlyTx(f.ctx, tx))
	prefixMust(t, "caller commits original snapshot", tx.Commit(f.ctx))
	tx, e = f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	prefixMust(t, "begin fresh readonly snapshot", e)
	if VerifyReadOnlyTx(f.ctx, tx) == nil {
		t.Fatal("new RR accepted unknown migration")
	}
	prefixMust(t, "caller rolls back unknown manifest", tx.Rollback(f.ctx))
	f.exec(t, `DELETE FROM schema_migrations WHERE name='999_unknown.sql'`)
	f.exec(t, `DELETE FROM schema_migrations WHERE name=(SELECT max(name) FROM schema_migrations)`)
	tx, e = f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	prefixMust(t, "begin missing latest manifest snapshot", e)
	if VerifyReadOnlyTx(f.ctx, tx) == nil {
		t.Fatal("complete-only verifier tolerated historical prefix")
	}
	prefixMust(t, "caller rolls back missing manifest", tx.Rollback(f.ctx))
}
