package migrations

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var Files embed.FS

const currentBaselineName = "current_mvp.sql"
const currentAppendPrefix = "current_"

type migration struct {
	name     string
	body     []byte
	checksum string
}
type migrationPlan struct {
	all    map[string]migration
	prefix []migration
}

// plan resolves the exact embedded target before acquiring any connection. An
// empty target is reserved internally for the complete current migration set.
func plan(through string, exact bool) (migrationPlan, error) {
	var result migrationPlan
	names, err := fs.Glob(Files, "*.sql")
	if err != nil {
		return result, err
	}
	sort.Strings(names)
	names = func() []string {
		filtered := make([]string, 0, len(names))
		for _, name := range names {
			if name != currentBaselineName {
				filtered = append(filtered, name)
			}
		}
		return filtered
	}()
	last := len(names) - 1
	if exact {
		last = sort.SearchStrings(names, through)
		if through == "" || isCurrentMigration(through) || last == len(names) || names[last] != through {
			return result, fmt.Errorf("migration target must be an exact non-empty embedded filename")
		}
	}
	result.all = make(map[string]migration, len(names))
	for i, name := range names {
		if isCurrentMigration(name) {
			continue
		}
		body, e := Files.ReadFile(name)
		if e != nil {
			return migrationPlan{}, e
		}
		item := migration{name: name, body: body, checksum: fmt.Sprintf("%x", sha256.Sum256(body))}
		result.all[name] = item
		if i <= last {
			result.prefix = append(result.prefix, item)
		}
	}
	return result, nil
}

func currentPlan() (migrationPlan, error) {
	names, err := fs.Glob(Files, currentAppendPrefix+"*.sql")
	if err != nil {
		return migrationPlan{}, err
	}
	sort.Strings(names)
	ordered := make([]string, 0, len(names)+1)
	ordered = append(ordered, currentBaselineName)
	for _, name := range names {
		if name != currentBaselineName {
			ordered = append(ordered, name)
		}
	}
	result := migrationPlan{all: make(map[string]migration, len(ordered))}
	for _, name := range ordered {
		body, e := Files.ReadFile(name)
		if e != nil {
			return migrationPlan{}, e
		}
		item := migration{name: name, body: body, checksum: fmt.Sprintf("%x", sha256.Sum256(body))}
		result.all[name] = item
		result.prefix = append(result.prefix, item)
	}
	return result, nil
}

func isCurrentMigration(name string) bool {
	return name == currentBaselineName || strings.HasPrefix(name, currentAppendPrefix)
}

type historyReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readHistory(ctx context.Context, reader historyReader) (map[string]string, bool, error) {
	var exists bool
	if e := reader.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); e != nil {
		return nil, false, e
	}
	applied := make(map[string]string)
	if !exists {
		return applied, false, nil
	}
	rows, e := reader.Query(ctx, "SELECT name,checksum FROM schema_migrations ORDER BY name")
	if e != nil {
		return nil, true, e
	}
	defer rows.Close()
	for rows.Next() {
		var name, sum string
		if e = rows.Scan(&name, &sum); e != nil {
			return nil, true, e
		}
		applied[name] = sum
	}
	return applied, true, rows.Err()
}

// Validate the entire history before applying even the first missing file.
// A correct checksum outside the requested prefix is still an unsafe baseline.
func validateHistory(p migrationPlan, applied map[string]string, complete bool) error {
	allowed := make(map[string]migration, len(p.prefix))
	for _, item := range p.prefix {
		allowed[item.name] = item
	}
	names := make([]string, 0, len(applied))
	for name := range applied {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, known := p.all[name]; !known {
			return fmt.Errorf("unknown applied migration: %s", name)
		}
		item, ok := allowed[name]
		if !ok {
			return fmt.Errorf("applied migration outside requested prefix: %s", name)
		}
		if applied[name] != item.checksum {
			return fmt.Errorf("migration checksum mismatch: %s", name)
		}
	}
	if complete {
		for _, item := range p.prefix {
			if _, ok := applied[item.name]; !ok {
				return fmt.Errorf("migration missing: %s", item.name)
			}
		}
	}
	return nil
}

// Apply serializes the historical migration set for isolated fixture tests.
// The serving installer uses ApplyCurrent and current_mvp.sql instead.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	p, e := plan("", false)
	if e != nil {
		return e
	}
	return apply(ctx, pool, p)
}

// ApplyThrough is an explicit offline historical-prefix installer. It rejects
// unknown targets before DB access and rejects incompatible history before DDL.
// It is not a serving-process downgrade or skip-migrations option.
func ApplyThrough(ctx context.Context, pool *pgxpool.Pool, through string) error {
	p, e := plan(through, true)
	if e != nil {
		return e
	}
	return apply(ctx, pool, p)
}

func apply(ctx context.Context, pool *pgxpool.Pool, p migrationPlan) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	lockConfirmed := false
	// Install cleanup before the lock request: cancellation or a lost reply can
	// leave PostgreSQL holding a session lock without a confirmed local result.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if !lockConfirmed {
			_ = conn.Hijack().Close(cleanup)
			return
		}
		var unlocked bool
		e := conn.QueryRow(cleanup, "SELECT pg_advisory_unlock(782193401)").Scan(&unlocked)
		// Hijack removes the connection from the pool even if closing fails.
		if e != nil || !unlocked {
			_ = conn.Hijack().Close(cleanup)
		}
	}()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(782193401)"); err != nil {
		return err
	}
	lockConfirmed = true
	applied, exists, err := readHistory(ctx, conn)
	if err != nil {
		return err
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
		tx, e := conn.Begin(ctx)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, string(item.body)); e == nil {
			_, e = tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", item.name, item.checksum)
		}
		if e != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tx.Rollback(cleanup)
			cancel()
			return fmt.Errorf("migration %s: %w", item.name, e)
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
	}
	return nil
}

// Verify is read-only and checks the complete current embedded set. The serving
// role never needs schema privileges or an option to tolerate an older prefix.
func Verify(ctx context.Context, pool *pgxpool.Pool) error {
	p, e := plan("", false)
	if e != nil {
		return e
	}
	return verify(ctx, pool, p)
}

// VerifyThrough checks one exact historical prefix without changing the schema.
func VerifyThrough(ctx context.Context, pool *pgxpool.Pool, through string) error {
	p, e := plan(through, true)
	if e != nil {
		return e
	}
	return verify(ctx, pool, p)
}

func verify(ctx context.Context, pool *pgxpool.Pool, p migrationPlan) error {
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	applied, _, e := readHistory(ctx, tx)
	if e != nil {
		return e
	}
	if e = validateHistory(p, applied, true); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
