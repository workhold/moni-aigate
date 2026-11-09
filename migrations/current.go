package migrations

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ApplyCurrent installs the current MVP baseline into a genuinely empty
// database. The historical SQL files remain available to isolated fixtures
// through ApplyThrough, but the serving installer never upgrades or converts
// an existing database.
func ApplyCurrent(ctx context.Context, pool *pgxpool.Pool) error {
	p, err := currentPlan()
	if err != nil {
		return err
	}
	return applyPlan(ctx, pool, p, true)
}

// VerifyCurrent is the serving-process read-only gate for the current MVP
// baseline. It accepts the exact complete embedded installation only.
func VerifyCurrent(ctx context.Context, pool *pgxpool.Pool) error {
	p, err := currentPlan()
	if err != nil {
		return err
	}
	return verify(ctx, pool, p)
}

func applyPlan(ctx context.Context, pool *pgxpool.Pool, p migrationPlan, requireEmpty bool) error {
	if pool == nil {
		return fmt.Errorf("migration pool is required")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	lockConfirmed := false
	defer func() {
		if !lockConfirmed {
			return
		}
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock(782193401)")
	}()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(782193401)"); err != nil {
		return err
	}
	lockConfirmed = true
	applied, exists, err := readHistory(ctx, conn)
	if err != nil {
		return err
	}
	if requireEmpty {
		if exists {
			return fmt.Errorf("current MVP migration refuses an existing schema_migrations history")
		}
		var userRelations int
		err = conn.QueryRow(ctx, `SELECT count(*)
FROM pg_catalog.pg_class AS c
JOIN pg_catalog.pg_namespace AS n ON n.oid=c.relnamespace
WHERE n.nspname=current_schema()
  AND c.relkind IN ('r','p','v','m','f','S')`).Scan(&userRelations)
		if err != nil {
			return err
		}
		if userRelations != 0 {
			return fmt.Errorf("current MVP migration refuses a non-empty database")
		}
	}
	if err = validateHistory(p, applied, false); err != nil {
		return err
	}
	if !exists {
		if _, err = conn.Exec(ctx, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`); err != nil {
			return err
		}
	}
	for _, item := range p.prefix {
		if _, ok := applied[item.name]; ok {
			continue
		}
		tx, txErr := conn.Begin(ctx)
		if txErr != nil {
			return txErr
		}
		if _, txErr = tx.Exec(ctx, string(item.body)); txErr == nil {
			_, txErr = tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", item.name, item.checksum)
		}
		if txErr != nil {
			_ = tx.Rollback(context.Background())
			return fmt.Errorf("migration %s: %w", item.name, txErr)
		}
		if txErr = tx.Commit(ctx); txErr != nil {
			return txErr
		}
	}
	return nil
}
