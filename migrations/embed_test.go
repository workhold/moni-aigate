package migrations

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHistoricalTargetRejectedBeforeDatabase(t *testing.T) {
	for _, target := range []string{"", "019", "019_operations_alerts.sql ", " 019_operations_alerts.sql", "../019_operations_alerts.sql", "*.sql", "019_operations_alerts.SQL", "999_missing.sql"} {
		t.Run(fmt.Sprintf("%q", target), func(t *testing.T) {
			// A nil pool would panic if resolution touched Acquire/Begin/DB first.
			if e := ApplyThrough(context.Background(), nil, target); e == nil {
				t.Fatal("invalid ApplyThrough target accepted")
			}
			if e := VerifyThrough(context.Background(), nil, target); e == nil {
				t.Fatal("invalid VerifyThrough target accepted")
			}
		})
	}
}
func TestEmbeddedPrefixIsExactClosedSet(t *testing.T) {
	p, e := plan("003_operations.sql", true)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.prefix) != 3 || p.prefix[0].name != "001_core.sql" || p.prefix[2].name != "003_operations.sql" {
		t.Fatal("prefix is not the exact ordered closed set")
	}
	for _, m := range p.prefix {
		if m.checksum != fmt.Sprintf("%x", sha256.Sum256(m.body)) {
			t.Fatal("embedded checksum mismatch")
		}
	}
	all, e := plan("", false)
	if e != nil {
		t.Fatal(e)
	}
	if len(all.prefix) != len(all.all) || len(all.prefix) < 19 {
		t.Fatal("default plan is not the complete current embed")
	}
}

type prefixFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	schema string
}

func prefixMust(t *testing.T, step string, e error) {
	t.Helper()
	if e != nil {
		t.Fatalf("%s: %v", step, e)
	}
}
func newPrefixFixture(t *testing.T) *prefixFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated migration-prefix PostgreSQL tests")
	}
	cfg, e := pgxpool.ParseConfig(raw)
	prefixMust(t, "parse dedicated PG config", e)
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("migration-prefix tests require dedicated local moni_test database on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	admin, e := pgxpool.NewWithConfig(ctx, cfg.Copy())
	prefixMust(t, "open fixture administrator", e)
	schema := "migration_prefix_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &prefixFixture{ctx: ctx, schema: schema}
	t.Cleanup(func() {
		if f.pool != nil {
			f.pool.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if e != nil {
			t.Error("exact fixture schema cleanup failed")
		}
		admin.Close()
		cancel()
	})
	_, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	prefixMust(t, "create exact fixture schema", e)
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "8s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	f.pool, e = pgxpool.NewWithConfig(ctx, cfg)
	prefixMust(t, "open fixture schema", e)
	return f
}
func (f *prefixFixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	_, e := f.pool.Exec(f.ctx, q, args...)
	prefixMust(t, "fixture SQL", e)
}
func (f *prefixFixture) snapshot(t *testing.T) string {
	t.Helper()
	var catalog, seed string
	e := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'relations',coalesce((SELECT jsonb_agg(jsonb_build_array(c.oid::text,c.relname,c.relkind::text) ORDER BY c.relname) FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace),'[]'::jsonb),
 'functions',coalesce((SELECT jsonb_agg(jsonb_build_array(p.oid::text,p.proname,p.prosrc) ORDER BY p.oid) FROM pg_proc p WHERE p.pronamespace=current_schema()::regnamespace),'[]'::jsonb))::text`).Scan(&catalog)
	prefixMust(t, "snapshot schema catalogs", e)
	history, exists, e := readHistory(f.ctx, f.pool)
	prefixMust(t, "snapshot history", e)
	var tenants bool
	e = f.pool.QueryRow(f.ctx, "SELECT to_regclass('tenants') IS NOT NULL").Scan(&tenants)
	prefixMust(t, "find tenant seed", e)
	if tenants {
		e = f.pool.QueryRow(f.ctx, "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY id),'[]'::jsonb)::text FROM tenants t").Scan(&seed)
		prefixMust(t, "snapshot tenant seed", e)
	}
	// History map formatting is deterministic in fmt for string keys.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%t|%v|%s", catalog, exists, history, seed))))
}
func (f *prefixFixture) assertHistory(t *testing.T, n int) {
	t.Helper()
	var got int
	prefixMust(t, "read installed count", f.pool.QueryRow(f.ctx, "SELECT count(*) FROM schema_migrations").Scan(&got))
	if got != n {
		t.Fatalf("got %d migrations, want %d", got, n)
	}
}

func TestHistoricalPrefixEmptyIdempotentAndStrictCutoff(t *testing.T) {
	f := newPrefixFixture(t)
	before := f.snapshot(t)
	if e := VerifyThrough(f.ctx, f.pool, "001_core.sql"); e == nil {
		t.Fatal("empty schema verified")
	}
	if f.snapshot(t) != before {
		t.Fatal("read-only verify changed empty schema")
	}
	prefixMust(t, "apply empty through001", ApplyThrough(f.ctx, f.pool, "001_core.sql"))
	f.assertHistory(t, 1)
	var journals bool
	prefixMust(t, "check no migration002 effects", f.pool.QueryRow(f.ctx, "SELECT to_regclass('journals') IS NOT NULL").Scan(&journals))
	if journals {
		t.Fatal("strict001 cutoff applied migration002")
	}
	f.exec(t, "INSERT INTO tenants(name) VALUES('migration prefix sentinel')")
	prefixMust(t, "verify001", VerifyThrough(f.ctx, f.pool, "001_core.sql"))
	before = f.snapshot(t)
	prefixMust(t, "idempotent apply001", ApplyThrough(f.ctx, f.pool, "001_core.sql"))
	if f.snapshot(t) != before {
		t.Fatal("idempotent prefix apply changed facts")
	}
	if e := Verify(f.ctx, f.pool); e == nil {
		t.Fatal("default Verify tolerated an incomplete current schema")
	}
	if f.snapshot(t) != before {
		t.Fatal("default Verify mutated historical schema")
	}
	prefixMust(t, "extend through002", ApplyThrough(f.ctx, f.pool, "002_billing.sql"))
	f.assertHistory(t, 2)
	prefixMust(t, "verify002", VerifyThrough(f.ctx, f.pool, "002_billing.sql"))
	before = f.snapshot(t)
	if e := ApplyThrough(f.ctx, f.pool, "001_core.sql"); e == nil {
		t.Fatal("installer tolerated a future migration")
	}
	if e := VerifyThrough(f.ctx, f.pool, "001_core.sql"); e == nil {
		t.Fatal("verifier tolerated a future migration")
	}
	if f.snapshot(t) != before {
		t.Fatal("prefix downgrade rejection changed facts")
	}
}

func TestHistoricalPrefixPreflightRejectsBeforeAnyNewFacts(t *testing.T) {
	for _, test := range []struct {
		name, target, record string
		corruptExisting      bool
	}{
		{name: "later_bad_checksum_before_missing002", target: "003_operations.sql", record: "003_operations.sql"},
		{name: "known_future_record", target: "002_billing.sql", record: "003_operations.sql"},
		{name: "unknown_record", target: "002_billing.sql", record: "999_unknown.sql"},
		{name: "changed_existing_checksum", target: "002_billing.sql", corruptExisting: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newPrefixFixture(t)
			prefixMust(t, "install001 baseline", ApplyThrough(f.ctx, f.pool, "001_core.sql"))
			f.exec(t, "INSERT INTO tenants(name) VALUES('must survive failed preflight')")
			if test.corruptExisting {
				f.exec(t, "UPDATE schema_migrations SET checksum=$1 WHERE name='001_core.sql'", strings.Repeat("b", 64))
			} else {
				sum := strings.Repeat("c", 64)
				if test.name == "known_future_record" {
					p, e := plan("003_operations.sql", true)
					prefixMust(t, "load known future checksum", e)
					sum = p.all[test.record].checksum
				}
				f.exec(t, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", test.record, sum)
			}
			before := f.snapshot(t)
			if e := ApplyThrough(f.ctx, f.pool, test.target); e == nil {
				t.Fatal("invalid prefix history installed")
			}
			if f.snapshot(t) != before {
				t.Fatal("ApplyThrough preflight rejection executed SQL or changed facts")
			}
			if e := VerifyThrough(f.ctx, f.pool, test.target); e == nil {
				t.Fatal("invalid prefix history verified")
			}
			if f.snapshot(t) != before {
				t.Fatal("VerifyThrough changed facts")
			}
			// Regular Apply must not partially install missing002 before discovering a
			// corrupt later record, nor does Verify silently accept unknown history.
			if test.name != "known_future_record" {
				if e := Apply(f.ctx, f.pool); e == nil {
					t.Fatal("regular Apply accepted invalid history")
				}
				if f.snapshot(t) != before {
					t.Fatal("regular Apply changed facts before rejecting history")
				}
				if e := Verify(f.ctx, f.pool); e == nil {
					t.Fatal("regular Verify accepted invalid history")
				}
			}
		})
	}
}

func TestHistoricalPrefixChecksHistoryAfterSerialMigrationLock(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install001 baseline", ApplyThrough(f.ctx, f.pool, "001_core.sql"))
	lock, e := f.pool.Acquire(f.ctx)
	prefixMust(t, "get explicit migration blocker", e)
	defer lock.Release()
	_, e = lock.Exec(f.ctx, "SELECT pg_advisory_lock(782193401)")
	prefixMust(t, "hold migration lock", e)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = lock.Exec(cleanup, "SELECT pg_advisory_unlock(782193401)")
	}()
	done := make(chan error, 1)
	go func() { done <- ApplyThrough(f.ctx, f.pool, "002_billing.sql") }()
	blocked := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		prefixMust(t, "observe actual advisory wait", f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND wait_event='advisory')", f.schema).Scan(&blocked))
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("installer did not wait for the migration lock")
	}
	// This happens while the installer is waiting, so a pre-lock history read
	// would incorrectly authorize migration002 after the lock becomes available.
	f.exec(t, "INSERT INTO schema_migrations(name,checksum) VALUES('999_unknown.sql',$1)", strings.Repeat("a", 64))
	before := f.snapshot(t)
	_, e = lock.Exec(f.ctx, "SELECT pg_advisory_unlock(782193401)")
	prefixMust(t, "release migration blocker", e)
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("installer inspected history before acquiring serial lock")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("installer did not complete after lock release")
	}
	if f.snapshot(t) != before {
		t.Fatal("lock-serialized rejection changed facts")
	}
}

func TestRegularApplyAndVerifyKeepCompleteCurrentTarget(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "regular full Apply", Apply(f.ctx, f.pool))
	prefixMust(t, "regular full Verify", Verify(f.ctx, f.pool))
	p, e := plan("", false)
	prefixMust(t, "load current manifest", e)
	f.assertHistory(t, len(p.prefix))
	before := f.snapshot(t)
	prefixMust(t, "regular idempotent Apply", Apply(f.ctx, f.pool))
	if f.snapshot(t) != before {
		t.Fatal("regular idempotent Apply changed history")
	}
	if e := VerifyThrough(f.ctx, f.pool, "001_core.sql"); e == nil {
		t.Fatal("historical Verify hid newer current migrations")
	}
	if f.snapshot(t) != before {
		t.Fatal("read-only historical rejection changed current schema")
	}
}

func TestHistoricalPrefixCancelledLockWaitDestroysConnection(t *testing.T) {
	f := newPrefixFixture(t)
	blocker, e := f.pool.Acquire(f.ctx)
	prefixMust(t, "acquire cancellation blocker", e)
	released := false
	defer func() {
		if !released {
			cleanup, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			_, _ = blocker.Exec(cleanup, "SELECT pg_advisory_unlock(782193401)")
			blocker.Release()
		}
	}()
	_, e = blocker.Exec(f.ctx, "SELECT pg_advisory_lock(782193401)")
	prefixMust(t, "hold cancellation blocker", e)
	waiting, cancel := context.WithCancel(f.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- ApplyThrough(waiting, f.pool, "001_core.sql") }()
	var pid uint32
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		prefixMust(t, "observe cancelled installer PID", f.pool.QueryRow(f.ctx, "SELECT coalesce((SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND wait_event='advisory' LIMIT 1),0)", f.schema).Scan(&pid))
		if pid != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("installer did not reach the cancellable lock wait")
	}
	cancel()
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("cancelled lock wait unexpectedly applied")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled installer did not clean up promptly")
	}
	// The failed lock request is outcome-unknown. Its exact backend must be
	// destroyed, not released for another borrower to inherit its session state.
	borrower, e := f.pool.Acquire(f.ctx)
	prefixMust(t, "borrow original pool after cancellation", e)
	var borrowedPID uint32
	e = borrower.QueryRow(f.ctx, "SELECT pg_backend_pid()").Scan(&borrowedPID)
	borrower.Release()
	prefixMust(t, "read post-cancellation backend", e)
	if borrowedPID == pid {
		t.Fatal("cancelled lock backend returned to the pool")
	}
	// PostgreSQL may retain the disconnected backend while it is still inside
	// a lock wait. It must drain after the original blocker is released; its
	// mere presence before then does not mean the client returned it to pool.
	var metadata bool
	prefixMust(t, "no cancelled DDL", f.pool.QueryRow(f.ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&metadata))
	if metadata {
		t.Fatal("cancelled lock wait created metadata")
	}
	_, e = blocker.Exec(f.ctx, "SELECT pg_advisory_unlock(782193401)")
	prefixMust(t, "release cancellation blocker", e)
	blocker.Release()
	released = true
	// Qualify only the cancelled backend. Other packages legitimately use the
	// same production key, so a busy global lock alone is never a leak oracle.
	drain, stopDrain := context.WithTimeout(f.ctx, 30*time.Second)
	defer stopDrain()
	var alive, originalLock bool
	for {
		e = f.pool.QueryRow(drain, `SELECT
 EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1),
 EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND granted)`, pid).Scan(&alive, &originalLock)
		prefixMust(t, "check exact cancelled backend and its granted locks", e)
		if !alive && !originalLock {
			break
		}
		select {
		case <-drain.Done():
			t.Fatalf("cancelled backend did not drain within the bounded observation (alive=%t, holds_advisory_lock=%t)", alive, originalLock)
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Probe from a different physical pool with a normal queued lock request.
	// Its bounded timeout allows ordinary concurrent migrations to finish and
	// must not inherit this fixture's deliberately short 3s/8s lock/SQL limits.
	cfg := f.pool.Config()
	cfg.MaxConns = 1
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "0"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "30s"
	other, e := pgxpool.NewWithConfig(f.ctx, cfg)
	prefixMust(t, "open independent post-cancellation pool", e)
	defer other.Close()
	probe, stopProbe := context.WithTimeout(f.ctx, 30*time.Second)
	defer stopProbe()
	conn, e := other.Acquire(probe)
	prefixMust(t, "acquire independent connection", e)
	held := false
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if held {
			_, _ = conn.Exec(cleanup, "SELECT pg_advisory_unlock(782193401)")
		}
		// Remove the physical test connection even if a probe reply was lost.
		_ = conn.Hijack().Close(cleanup)
	}()
	_, e = conn.Exec(probe, "SELECT pg_advisory_lock(782193401)")
	prefixMust(t, "independent lock probe after normal concurrent migration queue", e)
	held = true
	_, e = conn.Exec(probe, "SELECT pg_advisory_unlock(782193401)")
	prefixMust(t, "release independent lock", e)
	held = false
	prefixMust(t, "original pool remains usable", f.pool.Ping(f.ctx))
	prefixMust(t, "cancellation made no later DDL", f.pool.QueryRow(f.ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&metadata))
	if metadata {
		t.Fatal("cancelled lock request created later metadata")
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("cancelled installer leaked a pool lease")
	}
}
