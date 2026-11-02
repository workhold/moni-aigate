package fundinglotruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type historyTestTx struct {
	pgx.Tx
	query func(context.Context, string, ...any) (pgx.Rows, error)
	calls []string
}

func (t *historyTestTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.calls = append(t.calls, sql)
	return t.query(ctx, sql, args...)
}

type historyTestRows struct {
	pgx.Rows
	data                 [][]any
	index, closeCount    int
	scanErr, terminalErr error
	onNext, onScan       func()
	onErr, onClose       func()
}

func (r *historyTestRows) Next() bool {
	if r.onNext != nil {
		r.onNext()
	}
	r.index++
	return r.index <= len(r.data)
}
func (r *historyTestRows) Scan(dest ...any) error {
	if r.onScan != nil {
		r.onScan()
	}
	if r.scanErr != nil {
		return r.scanErr
	}
	if r.index < 1 || r.index > len(r.data) || len(dest) != len(r.data[r.index-1]) {
		return errors.New("fake_history_scan")
	}
	for i, value := range r.data[r.index-1] {
		switch d := dest[i].(type) {
		case *bool:
			v, ok := value.(bool)
			if !ok {
				return errors.New("fake_history_bool")
			}
			*d = v
		case *string:
			v, ok := value.(string)
			if !ok {
				return errors.New("fake_history_string")
			}
			*d = v
		default:
			return errors.New("fake_history_destination")
		}
	}
	return nil
}
func (r *historyTestRows) Err() error {
	if r.onErr != nil {
		r.onErr()
	}
	return r.terminalErr
}
func (r *historyTestRows) Close() {
	r.closeCount++
	if r.onClose != nil {
		r.onClose()
	}
}

func historyTestWindow(t *testing.T) *verificationWindow {
	t.Helper()
	w, err := newVerificationWindow(context.Background(), runtimeBudget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.cancel)
	return w
}

func historyTestFixture(t *testing.T) (*historyTestTx, map[string]*historyTestRows) {
	t.Helper()
	m, err := loadMaterials()
	if err != nil {
		t.Fatal(err)
	}
	data := make([][]any, len(m.Manifest))
	for i, item := range m.Manifest {
		data[i] = []any{item.Name, item.SHA256}
	}
	rows := map[string]*historyTestRows{
		runtimeHistoryGuardSQL("fixture_app"): {data: [][]any{{true}}},
		runtimeHistorySettingsSQL:             {data: [][]any{{"repeatable read", "on"}}},
		runtimeHistoryExistsSQL:               {data: [][]any{{true}}},
		runtimeHistoryRowsSQL:                 {data: data},
	}
	tx := &historyTestTx{}
	tx.query = func(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
		if ctx == nil || ctx.Err() != nil || len(args) != 1 || args[0] != pgx.QueryExecModeSimpleProtocol {
			return nil, errors.New("fake_history_query_parameters")
		}
		r, ok := rows[sql]
		if !ok {
			return nil, errors.New("fake_history_unexpected_query")
		}
		return r, nil
	}
	return tx, rows
}

func TestRuntimeHistoryGuardAndOriginalHelper(t *testing.T) {
	w := historyTestWindow(t)
	tx, rows := historyTestFixture(t)
	if err := verifyRuntimeHistory(w, tx, "fixture_app"); err != nil {
		t.Fatal(err)
	}
	if len(tx.calls) != 4 || tx.calls[0] != runtimeHistoryGuardSQL("fixture_app") || tx.calls[3] != runtimeHistoryRowsSQL {
		t.Fatal("history guard/helper order changed")
	}
	for _, r := range rows {
		if r.index != len(r.data)+1 || r.closeCount != 1 {
			t.Fatal("history success omitted complete EOF/Close")
		}
	}
	// Identifier escaping is delegated to pgx.Identifier; the guard's fixed
	// COUNT/LIMIT/DISTINCT/null/byte bounds cannot come from caller input.
	guard := runtimeHistoryGuardSQL(`app"quoted`)
	for _, required := range []string{`"app""quoted"."schema_migrations"`, "LIMIT 23", "22::pg_catalog.int8", "35::pg_catalog.int4", "64::pg_catalog.int4", `COLLATE pg_catalog."C"`, "FROM ONLY", "name IS NOT NULL AND checksum IS NOT NULL"} {
		if !strings.Contains(guard, required) {
			t.Fatal("fixed history guard element missing")
		}
	}
}

func TestRuntimeHistoryRejectsIncompleteOrCorruptHistory(t *testing.T) {
	for _, name := range []string{"guard_false", "checksum", "missing", "duplicate", "extra", "name_36", "checksum_63", "scan", "next_err"} {
		t.Run(name, func(t *testing.T) {
			w := historyTestWindow(t)
			tx, rows := historyTestFixture(t)
			r := rows[runtimeHistoryRowsSQL]
			switch name {
			case "guard_false":
				rows[runtimeHistoryGuardSQL("fixture_app")].data[0][0] = false
			case "checksum":
				r.data[0][1] = strings.Repeat("0", 64)
			case "missing":
				r.data = r.data[:21]
			case "duplicate":
				r.data[21] = r.data[20]
			case "extra":
				r.data = append(r.data, []any{"999_unknown.sql", strings.Repeat("0", 64)})
			case "name_36":
				r.data[0][0] = strings.Repeat("n", 36)
			case "checksum_63":
				r.data[0][1] = strings.Repeat("a", 63)
			case "scan":
				r.scanErr = errors.New("fake_history_scan_error")
			case "next_err":
				r.terminalErr = errors.New("fake_history_terminal_error")
			}
			if err := verifyRuntimeHistory(w, tx, "fixture_app"); err != ErrRuntime {
				t.Fatal("invalid history accepted or leaked an error")
			}
			if w.ctx.Err() == nil {
				t.Fatal("history failure did not cancel work")
			}
			if name == "guard_false" && len(tx.calls) != 1 {
				t.Fatal("helper ran after failed guard")
			}
			if (name == "extra" || name == "scan" || name == "name_36" || name == "checksum_63" || name == "next_err") && r.closeCount != 0 {
				t.Fatal("failed history explicitly drained the driver")
			}
		})
	}
}

func TestRuntimeHistoryAbsoluteDeadlineAtDriverBoundaries(t *testing.T) {
	for _, stage := range []string{"next", "scan", "err", "close"} {
		t.Run(stage, func(t *testing.T) {
			w := historyTestWindow(t)
			tx, rows := historyTestFixture(t)
			guard := rows[runtimeHistoryGuardSQL("fixture_app")]
			expire := func() {
				if w.ctx.Err() != nil {
					t.Error("fixture requires an unsignaled context timer")
				}
				w.deadline = time.Now().Add(-time.Second)
			}
			switch stage {
			case "next":
				guard.onNext = expire
			case "scan":
				guard.onScan = expire
			case "err":
				guard.onErr = expire
			case "close":
				guard.onClose = expire
			}
			if verifyRuntimeHistory(w, tx, "fixture_app") != ErrRuntime || len(tx.calls) != 1 {
				t.Fatal("absolute deadline accepted or helper continued")
			}
		})
	}
}

func TestRuntimeHistoryPrivateQueryRejectsPendingAndArbitrarySQL(t *testing.T) {
	w := historyTestWindow(t)
	tx, _ := historyTestFixture(t)
	h := &runtimeHistoryTx{Tx: tx, window: w, guard: runtimeHistoryGuardSQL("fixture_app")}
	if _, err := h.Query(w.ctx, "SELECT secret"); err != ErrRuntime || len(tx.calls) != 0 {
		t.Fatal("arbitrary SQL reached original transaction")
	}
	w = historyTestWindow(t)
	tx, rows := historyTestFixture(t)
	h = &runtimeHistoryTx{Tx: tx, window: w, guard: runtimeHistoryGuardSQL("fixture_app")}
	r, err := h.Query(w.ctx, runtimeHistoryRowsSQL)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !h.failed || h.pending == nil || rows[runtimeHistoryRowsSQL].closeCount != 0 {
		t.Fatal("early close cleared pending or drained unread driver")
	}
	if _, err := h.Query(context.Background(), runtimeHistoryExistsSQL); err != ErrRuntime || len(tx.calls) != 1 {
		t.Fatal("poisoned history query was reused")
	}
}
