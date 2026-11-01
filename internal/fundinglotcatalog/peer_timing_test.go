package fundinglotcatalog

import (
	"context"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Diagnostic only: this package-private read cannot issue a proof. Issuing APIs
// retain their common five-second budget in their separate acceptance tests.
func TestCatalogPeerReadTimings(t *testing.T) {
	if os.Getenv("C0_CATALOG_PEER_TIMINGS") != "1" {
		t.Skip("explicit catalog performance diagnostic only")
	}
	f := newCatalogPeerFixture(t)
	tx, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("catalog timing caller RR")
	}
	defer tx.Rollback(context.Background())
	x := &catalogTimingTx{Tx: tx, times: map[string]time.Duration{}}
	start := time.Now()
	_, err = readCatalogTx(f.ctx, x, f.pins)
	total := time.Since(start)
	if err != nil {
		t.Fatal("catalog timing read-only metadata check")
	}
	type timing struct {
		name     string
		duration time.Duration
	}
	var list []timing
	var queryTotal time.Duration
	for name, duration := range x.times {
		list = append(list, timing{name, duration})
		queryTotal += duration
	}
	sort.Slice(list, func(i, j int) bool { return list[i].duration > list[j].duration })
	for i, item := range list {
		if i >= 8 {
			break
		}
		t.Logf("static category %s: %s", item.name, item.duration.Round(time.Millisecond))
	}
	t.Logf("diagnostic only: metadata total=%s; Query-through-Rows.Close=%s; normalization/canonical/comparison remainder=%s", total.Round(time.Millisecond), queryTotal.Round(time.Millisecond), (total - queryTotal).Round(time.Millisecond))
}

type catalogTimingTx struct {
	pgx.Tx
	times map[string]time.Duration
}

func (x *catalogTimingTx) name(sql string) string {
	for _, q := range schemaQueries() {
		if strings.Contains(sql, q.sql) {
			return "schema/" + q.name
		}
	}
	for _, q := range roleQueries() {
		if strings.Contains(sql, q.sql) {
			return "roles/" + q.name
		}
	}
	return "support"
}
func (x *catalogTimingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	name, start := x.name(sql), time.Now()
	rows, err := x.Tx.Query(ctx, sql, args...)
	if err != nil {
		x.times[name] += time.Since(start)
		return nil, err
	}
	return &catalogTimingRows{Rows: rows, owner: x, name: name, start: start}, nil
}
func (x *catalogTimingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return catalogTimingRow{Row: x.Tx.QueryRow(ctx, sql, args...), owner: x, name: x.name(sql), start: time.Now()}
}

type catalogTimingRows struct {
	pgx.Rows
	owner  *catalogTimingTx
	name   string
	start  time.Time
	closed bool
}

func (x *catalogTimingRows) Close() {
	x.Rows.Close()
	if !x.closed {
		x.closed = true
		x.owner.times[x.name] += time.Since(x.start)
	}
}

type catalogTimingRow struct {
	pgx.Row
	owner *catalogTimingTx
	name  string
	start time.Time
}

func (x catalogTimingRow) Scan(args ...any) error {
	err := x.Row.Scan(args...)
	x.owner.times[x.name] += time.Since(x.start)
	return err
}
