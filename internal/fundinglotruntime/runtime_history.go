package fundinglotruntime

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Only the already-authenticated schema identifier is substituted. Keep the
// extra-row probe, null checks, and bounded C-collation distinct comparison.
func runtimeHistoryGuardSQL(schema string) string {
	return `SELECT pg_catalog.count(*) OPERATOR(pg_catalog.=) 22::pg_catalog.int8
 AND COALESCE(pg_catalog.bool_and(
  name IS NOT NULL AND checksum IS NOT NULL
  AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.>=) 1::pg_catalog.int4
  AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4
  AND pg_catalog.octet_length(checksum) OPERATOR(pg_catalog.=) 64::pg_catalog.int4
 ), false)
 AND pg_catalog.count(DISTINCT CASE
  WHEN pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4
  THEN name COLLATE pg_catalog."C" ELSE NULL END)
  OPERATOR(pg_catalog.=) 22::pg_catalog.int8
 FROM (SELECT name, checksum FROM ONLY ` + pgx.Identifier{schema, "schema_migrations"}.Sanitize() + ` LIMIT 23) AS guarded_history`
}

const runtimeHistorySettingsSQL = `SELECT pg_catalog.current_setting('transaction_isolation'),pg_catalog.current_setting('transaction_read_only')`

func runtimeHistoryRowsSQL(schema string) string {
	return `SELECT CASE WHEN pg_catalog.octet_length(name) OPERATOR(pg_catalog.>=) 1::pg_catalog.int4
 AND pg_catalog.octet_length(name) OPERATOR(pg_catalog.<=) 35::pg_catalog.int4 THEN name ELSE NULL END,
 CASE WHEN pg_catalog.octet_length(checksum) OPERATOR(pg_catalog.=) 64::pg_catalog.int4 THEN checksum ELSE NULL END
 FROM ONLY ` + pgx.Identifier{schema, "schema_migrations"}.Sanitize() + ` LIMIT 23`
}

// The guard authenticates complete physical uniqueness and bounds before the
// historical helper may build its map. Both reads use the same caller-owned RR
// snapshot; the helper remains the authority on the frozen original 001-022
// checksums, independently of later current migrations.
func verifyRuntimeHistory(w *verificationWindow, tx pgx.Tx, schema string) error {
	if w.check() != nil || tx == nil {
		return ErrRuntime
	}
	m, err := loadMaterials()
	if err != nil || m == nil || m.ManifestBytes != 2459 || len(m.Manifest) != 22 || m.MaxNameBytes != 35 || w.check() != nil {
		return ErrRuntime
	}
	h := &runtimeHistoryTx{Tx: tx, window: w, guard: runtimeHistoryGuardSQL(schema), history: runtimeHistoryRowsSQL(schema)}
	var bounded bool
	if h.QueryRow(w.ctx, h.guard).Scan(&bounded) != nil || !bounded || h.check() != nil || h.pending != nil {
		h.fail()
		return ErrRuntime
	}
	if migrations.VerifyOriginal22SnapshotTx(w.ctx, h, schema) != nil || h.check() != nil || h.pending != nil || w.check() != nil {
		h.fail()
		return ErrRuntime
	}
	return nil
}

// This private wrapper is used only by the fixed read-only migration helper.
// Catalog authentication and proof identity always receive the original tx.
// It never begins/ends a transaction or changes the caller's connection.
type runtimeHistoryTx struct {
	pgx.Tx
	window  *verificationWindow
	guard   string
	history string
	pending *runtimeHistoryRows
	failed  bool
}

func (h *runtimeHistoryTx) check() error {
	if h == nil || h.failed || h.Tx == nil || h.window.check() != nil {
		return ErrRuntime
	}
	return nil
}
func (h *runtimeHistoryTx) fail() error {
	if h != nil {
		h.failed = true
		if h.window != nil && h.window.cancel != nil {
			h.window.cancel()
		}
	}
	return ErrRuntime
}

func (h *runtimeHistoryTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if h.check() != nil || h.pending != nil || ctx == nil || ctx.Err() != nil ||
		sql != h.guard && sql != runtimeHistorySettingsSQL && sql != h.history {
		return nil, h.fail()
	}
	if len(args) != 0 {
		if len(args) != 1 || sql != h.history {
			return nil, h.fail()
		}
		mode, ok := args[0].(pgx.QueryExecMode)
		if !ok || mode != pgx.QueryExecModeSimpleProtocol {
			return nil, h.fail()
		}
	}
	driver, err := h.Tx.Query(h.window.ctx, sql, pgx.QueryExecModeSimpleProtocol)
	if err != nil || nilDriver(driver) {
		return nil, h.fail()
	}
	r := &runtimeHistoryRows{Rows: driver, owner: h, source: sql}
	h.pending = r
	if h.check() != nil {
		return nil, h.fail()
	}
	return r, nil
}

func (h *runtimeHistoryTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	r, err := h.Query(ctx, sql, args...)
	return &runtimeHistoryRow{owner: h, rows: r, err: err}
}

type runtimeHistoryRow struct {
	owner *runtimeHistoryTx
	rows  pgx.Rows
	err   error
	used  bool
}

func (r *runtimeHistoryRow) Scan(dest ...any) error {
	if r == nil || r.owner == nil || r.err != nil || r.used || r.owner.check() != nil || nilDriver(r.rows) {
		if r != nil && r.owner != nil {
			return r.owner.fail()
		}
		return ErrRuntime
	}
	r.used = true
	more := r.rows.Next()
	if r.owner.check() != nil {
		return r.owner.fail()
	}
	if !more {
		err := r.rows.Err()
		if err != nil || r.owner.check() != nil {
			return r.owner.fail()
		}
		r.rows.Close()
		return r.owner.fail()
	}
	if r.rows.Scan(dest...) != nil || r.owner.check() != nil {
		return r.owner.fail()
	}
	// QueryRow normally closes after its first row. Here the fixed queries must
	// also reach their complete EOF, so neither truncation nor unread draining
	// can be hidden behind a successful scalar result.
	more = r.rows.Next()
	if r.owner.check() != nil || more {
		return r.owner.fail()
	}
	err := r.rows.Err()
	if err != nil || r.owner.check() != nil {
		return r.owner.fail()
	}
	r.rows.Close()
	return r.owner.check()
}

type runtimeHistoryRows struct {
	pgx.Rows
	owner                           *runtimeHistoryTx
	source                          string
	current, consumed, eof, errSeen bool
	released                        bool
	count                           int
}

func (r *runtimeHistoryRows) valid() error {
	if r == nil || r.owner == nil {
		return ErrRuntime
	}
	if r.owner.check() != nil || nilDriver(r.Rows) || r.released || r.owner.pending != r {
		return r.owner.fail()
	}
	return nil
}
func (r *runtimeHistoryRows) Next() bool {
	if r.valid() != nil || r.current && !r.consumed {
		if r != nil && r.owner != nil {
			r.owner.fail()
		}
		return false
	}
	if r.eof {
		return false
	}
	more := r.Rows.Next()
	if r.owner.check() != nil {
		r.owner.fail()
		return false
	}
	if !more {
		err := r.Rows.Err()
		if err != nil || r.owner.check() != nil {
			r.owner.fail()
			return false
		}
	}
	if more {
		r.count++
		if r.count > 22 {
			r.owner.fail()
			return false
		}
	}
	r.current, r.consumed, r.eof = more, false, !more
	if r.owner.check() != nil {
		r.owner.fail()
		return false
	}
	return more
}
func (r *runtimeHistoryRows) Scan(dest ...any) error {
	if r.valid() != nil {
		return ErrRuntime
	}
	if !r.current || r.consumed || r.eof || len(dest) < 1 || len(dest) > 2 {
		return r.owner.fail()
	}
	local := make([]any, len(dest))
	for i, d := range dest {
		switch v := d.(type) {
		case *string:
			if v != nil {
				local[i] = new(string)
			}
		case **string:
			if v != nil && r.source == r.owner.history {
				local[i] = new(*string)
			}
		case *bool:
			if v != nil {
				local[i] = new(bool)
			}
		}
		if local[i] == nil {
			return r.owner.fail()
		}
	}
	if r.Rows.Scan(local...) != nil || r.owner.check() != nil {
		return r.owner.fail()
	}
	for i, value := range local {
		var text *string
		switch pointer := value.(type) {
		case *string:
			text = pointer
		case **string:
			if *pointer == nil {
				return r.owner.fail()
			}
			text = *pointer
		}
		if text != nil {
			// Actual history fields have already passed the tighter 35/64 guard.
			// The remaining strings are built-in isolation/access constants.
			if len(*text) > 65536 || r.source == r.owner.history && (i == 0 && (len(*text) < 1 || len(*text) > 35) || i == 1 && len(*text) != 64) {
				return r.owner.fail()
			}
			local[i] = new(string)
			*local[i].(*string) = strings.Clone(*text)
		}
	}
	// The shared original22 helper also serves offline RW snapshots. This
	// runtime composer retains its stricter caller-owned RR READ ONLY boundary.
	if r.source == runtimeHistorySettingsSQL {
		if len(local) != 2 {
			return r.owner.fail()
		}
		isolation, isolationOK := local[0].(*string)
		access, accessOK := local[1].(*string)
		if !isolationOK || !accessOK || *isolation != "repeatable read" || *access != "on" {
			return r.owner.fail()
		}
	}
	if r.owner.check() != nil {
		return r.owner.fail()
	}
	for i, d := range dest {
		switch v := d.(type) {
		case *string:
			*v = *local[i].(*string)
		case **string:
			*v = local[i].(*string)
		case *bool:
			*v = *local[i].(*bool)
		}
	}
	if r.owner.check() != nil {
		return r.owner.fail()
	}
	r.consumed = true
	return nil
}
func (r *runtimeHistoryRows) Err() error {
	if r != nil && r.released {
		return r.owner.check()
	}
	if r.valid() != nil {
		return ErrRuntime
	}
	err := r.Rows.Err()
	if err != nil || r.owner.check() != nil {
		return r.owner.fail()
	}
	if r.eof {
		r.errSeen = true
	}
	return nil
}
func (r *runtimeHistoryRows) Close() {
	if r != nil && r.released {
		if r.owner.check() != nil {
			r.owner.fail()
		}
		return
	}
	if r.valid() != nil {
		return
	}
	if !r.eof || !r.errSeen {
		// Latch and cancel before returning. Never explicitly close/drain an
		// unread driver result; fresh bounded cleanup belongs to the caller.
		r.owner.fail()
		return
	}
	r.Rows.Close()
	if r.owner.check() != nil {
		r.owner.fail()
		return
	}
	err := r.Rows.Err()
	if err != nil || r.owner.check() != nil {
		r.owner.fail()
		return
	}
	r.released = true
	r.owner.pending = nil
}
