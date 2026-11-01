package server

// Opt-in diagnostic instrumentation only. SQL arguments, request bodies and
// credentials are never recorded. This adds observer cost and is not a score.
import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type capacityProfileSpan struct {
	Kind       string  `json:"kind"`
	Category   string  `json:"category"`
	Stage      string  `json:"stage,omitempty"`
	PID        uint32  `json:"pid"`
	Tx         int64   `json:"tx,omitempty"`
	StartMS    float64 `json:"start_ms"`
	DurationMS float64 `json:"duration_ms"`
	FirstSeen  int     `json:"first_seen_sql_on_connection,omitempty"`
}
type capacityProfileTx struct {
	id    int64
	start time.Time
	stage string
}
type capacityProfileToken struct {
	start time.Time
	cat   string
	tx    int64
	first int
}
type capacityProfileKey string

type capacityProfiler struct {
	mu         sync.Mutex
	path       string
	mode       string
	epoch      time.Time
	enabled    atomic.Bool
	seq        int64
	txs        map[uint32]*capacityProfileTx
	stages     map[int64]string
	seen       map[uint32]map[string]bool
	templates  map[string]bool
	warmCount  int
	spans      []capacityProfileSpan
	waits      map[string]int
	walBefore  map[string]any
	walAfter   map[string]any
	stopSample chan struct{}
	sampleDone chan struct{}
	oldMutex   int
}

func configureCapacityProfiler(t *testing.T, cfg *pgxpool.Config) *capacityProfiler {
	if os.Getenv("MONI_CAPACITY_TEST") != "true" || os.Getenv("MONI_CAPACITY_PROFILE") == "" {
		return nil
	}
	p := &capacityProfiler{path: os.Getenv("MONI_CAPACITY_PROFILE"), mode: "cache_statement", txs: map[uint32]*capacityProfileTx{}, stages: map[int64]string{}, seen: map[uint32]map[string]bool{}, templates: map[string]bool{}, waits: map[string]int{}}
	cfg.ConnConfig.Tracer = p
	return p
}

func capacitySQLCategory(sql string) string {
	s := strings.ToLower(sql)
	switch {
	case strings.HasPrefix(s, "begin"):
		return "begin"
	case s == "commit" || s == "rollback":
		return s
	case strings.Contains(s, "api_keys") && strings.Contains(s, "for update"):
		return "key_update_lock"
	case strings.Contains(s, "api_keys") && strings.Contains(s, "for share"):
		return "key_share_lock"
	case strings.Contains(s, "ledger_accounts") && strings.Contains(s, "for update"):
		return "account_lock"
	case strings.Contains(s, "reservations") && strings.Contains(s, "for update"):
		return "reservation_lock"
	case strings.Contains(s, "for share"):
		return "parent_share_lock"
	case strings.Contains(s, "for update"):
		return "other_update_lock"
	case strings.Contains(s, "set_config('lock_timeout'"):
		return "transaction_settings"
	case strings.HasPrefix(s, "insert into ledger_accounts"):
		return "account_init"
	case strings.HasPrefix(s, "insert into journals"):
		return "journal_insert"
	case strings.Contains(s, "tenant_model_grants") && strings.Contains(s, "billing_profile_blocks"):
		return "fresh_grant_check"
	case strings.HasPrefix(s, "insert into request_records"):
		return "request_insert"
	case strings.HasPrefix(s, "insert into upstream_attempts"):
		return "attempt_insert"
	case strings.Contains(s, "from request_records"):
		return "request_lookup"
	case strings.Contains(s, "from cost_versions"):
		return "cost_lookup"
	case strings.Contains(s, "from price_versions"):
		return "price_lookup"
	case strings.Contains(s, "clock_timestamp"):
		return "database_clock_or_deadline"
	case strings.HasPrefix(s, "select"):
		return "other_read"
	default:
		return "other_write"
	}
}

func (p *capacityProfiler) stageLocked(pid uint32, sql string) {
	tx := p.txs[pid]
	if tx == nil {
		return
	}
	s := strings.ToLower(sql)
	switch {
	case strings.HasPrefix(s, "insert into request_records"):
		tx.stage = "reserve"
	case strings.HasPrefix(s, "insert into upstream_attempts"):
		tx.stage = "record_attempt"
	case strings.Contains(s, "select r.owner_id,r.key_id"):
		tx.stage = "prepare_dispatch"
	case strings.HasPrefix(s, "insert into finalization_records"):
		tx.stage = "finalization"
	case strings.HasPrefix(s, "insert into settlements"):
		tx.stage = "settle"
	}
	if tx.stage != "" {
		p.stages[tx.id] = tx.stage
	}
}

func (p *capacityProfiler) firstLocked(pid uint32, sql string) int {
	if p.seen[pid] == nil {
		p.seen[pid] = map[string]bool{}
	}
	if p.seen[pid][sql] {
		return 0
	}
	p.seen[pid][sql] = true
	return 1
}

func (p *capacityProfiler) TraceQueryStart(ctx context.Context, conn *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	pid := conn.PgConn().PID()
	p.mu.Lock()
	first := p.firstLocked(pid, d.SQL)
	if !p.enabled.Load() {
		p.mu.Unlock()
		return ctx
	}
	p.templates[d.SQL] = true
	token := &capacityProfileToken{start: time.Now(), cat: capacitySQLCategory(d.SQL), first: first}
	if token.cat == "begin" {
		p.seq++
		p.txs[pid] = &capacityProfileTx{id: p.seq, start: token.start}
	}
	if tx := p.txs[pid]; tx != nil {
		token.tx = tx.id
	}
	p.stageLocked(pid, d.SQL)
	p.mu.Unlock()
	return context.WithValue(ctx, capacityProfileKey("query"), token)
}
func (p *capacityProfiler) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, _ pgx.TraceQueryEndData) {
	token, _ := ctx.Value(capacityProfileKey("query")).(*capacityProfileToken)
	if token == nil || !p.enabled.Load() {
		return
	}
	pid := conn.PgConn().PID()
	p.record("query", token, pid)
	if token.cat == "commit" || token.cat == "rollback" {
		p.mu.Lock()
		if tx := p.txs[pid]; tx != nil {
			p.spans = append(p.spans, capacityProfileSpan{Kind: "transaction", Category: token.cat, PID: pid, Tx: tx.id, StartMS: float64(tx.start.Sub(p.epoch)) / 1e6, DurationMS: float64(time.Since(tx.start)) / 1e6})
			delete(p.txs, pid)
		}
		p.mu.Unlock()
	}
}
func (p *capacityProfiler) TraceBatchStart(ctx context.Context, conn *pgx.Conn, d pgx.TraceBatchStartData) context.Context {
	pid := conn.PgConn().PID()
	p.mu.Lock()
	first := 0
	for _, q := range d.Batch.QueuedQueries {
		first += p.firstLocked(pid, q.SQL)
		if p.enabled.Load() {
			p.templates[q.SQL] = true
			p.stageLocked(pid, q.SQL)
		}
	}
	if !p.enabled.Load() {
		p.mu.Unlock()
		return ctx
	}
	cat := "empty"
	if len(d.Batch.QueuedQueries) != 0 {
		cat = capacitySQLCategory(d.Batch.QueuedQueries[0].SQL)
	}
	token := &capacityProfileToken{start: time.Now(), cat: cat, first: first}
	if tx := p.txs[pid]; tx != nil {
		token.tx = tx.id
	}
	p.mu.Unlock()
	ctx = context.WithValue(ctx, capacityProfileKey("batch_total"), token)
	gap := *token
	return context.WithValue(ctx, capacityProfileKey("batch_gap"), &gap)
}
func (p *capacityProfiler) warm(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	path := os.Getenv("MONI_CAPACITY_PROFILE_PREPARE_SOURCE")
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Templates []string `json:"sql_templates_without_arguments"`
	}
	if err = json.Unmarshal(raw, &source); err != nil || len(source.Templates) == 0 {
		t.Fatal("diagnostic prepare source has no SQL templates", err)
	}
	for range pool.Config().MaxConns {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		for _, sql := range source.Templates {
			if capacitySQLCategory(sql) == "begin" || sql == "commit" || sql == "rollback" {
				continue
			}
			if _, err = conn.Conn().Prepare(ctx, sql, sql); err != nil {
				t.Fatal("diagnostic statement preparation", err)
			}
			p.mu.Lock()
			p.firstLocked(conn.Conn().PgConn().PID(), sql)
			p.warmCount++
			p.mu.Unlock()
		}
	}
}
func (p *capacityProfiler) TraceBatchQuery(ctx context.Context, conn *pgx.Conn, d pgx.TraceBatchQueryData) {
	token, _ := ctx.Value(capacityProfileKey("batch_gap")).(*capacityProfileToken)
	if token == nil || !p.enabled.Load() {
		return
	}
	token.cat = capacitySQLCategory(d.SQL)
	p.record("batch_result_gap_not_sql_execution", token, conn.PgConn().PID())
	token.start = time.Now()
	token.first = 0
}
func (p *capacityProfiler) TraceBatchEnd(ctx context.Context, conn *pgx.Conn, _ pgx.TraceBatchEndData) {
	token, _ := ctx.Value(capacityProfileKey("batch_total")).(*capacityProfileToken)
	if token != nil && p.enabled.Load() {
		p.record("batch_total", token, conn.PgConn().PID())
	}
}
func (p *capacityProfiler) TracePrepareStart(ctx context.Context, _ *pgx.Conn, d pgx.TracePrepareStartData) context.Context {
	if !p.enabled.Load() {
		return ctx
	}
	return context.WithValue(ctx, capacityProfileKey("prepare"), &capacityProfileToken{start: time.Now(), cat: capacitySQLCategory(d.SQL)})
}
func (p *capacityProfiler) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, _ pgx.TracePrepareEndData) {
	token, _ := ctx.Value(capacityProfileKey("prepare")).(*capacityProfileToken)
	if token != nil && p.enabled.Load() {
		p.record("explicit_prepare_nested_in_query", token, conn.PgConn().PID())
	}
}
func (p *capacityProfiler) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	if !p.enabled.Load() {
		return ctx
	}
	return context.WithValue(ctx, capacityProfileKey("acquire"), &capacityProfileToken{start: time.Now(), cat: "pool_acquire"})
}
func (p *capacityProfiler) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireEndData) {
	token, _ := ctx.Value(capacityProfileKey("acquire")).(*capacityProfileToken)
	if token != nil && p.enabled.Load() {
		p.record("pool", token, 0)
	}
}
func (p *capacityProfiler) record(kind string, token *capacityProfileToken, pid uint32) {
	d := time.Since(token.start)
	p.mu.Lock()
	if len(p.spans) < 100000 {
		p.spans = append(p.spans, capacityProfileSpan{Kind: kind, Category: token.cat, PID: pid, Tx: token.tx, StartMS: float64(token.start.Sub(p.epoch)) / 1e6, DurationMS: float64(d) / 1e6, FirstSeen: token.first})
	}
	p.mu.Unlock()
}
func (p *capacityProfiler) guard(check func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		start := time.Now()
		err := check(ctx)
		if p.enabled.Load() {
			p.record("guard", &capacityProfileToken{start: start, cat: "queue_plus_ping"}, 0)
		}
		return err
	}
}
func capacityWALSnapshot(conn *pgx.Conn) map[string]any {
	var syncCommit, fsync, timing string
	var writes, syncs int64
	var writeMS, syncMS float64
	err := conn.QueryRow(context.Background(), `SELECT current_setting('synchronous_commit'),current_setting('fsync'),current_setting('track_wal_io_timing'),wal_write,wal_sync,wal_write_time,wal_sync_time FROM pg_stat_wal`).Scan(&syncCommit, &fsync, &timing, &writes, &syncs, &writeMS, &syncMS)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"synchronous_commit": syncCommit, "fsync": fsync, "track_wal_io_timing": timing, "wal_write": writes, "wal_sync": syncs, "wal_write_ms": writeMS, "wal_sync_ms": syncMS}
}
func (p *capacityProfiler) start(cfg *pgx.ConnConfig, application string, admission func() bool) {
	p.epoch = time.Now()
	p.stopSample, p.sampleDone = make(chan struct{}), make(chan struct{})
	p.oldMutex = runtime.SetMutexProfileFraction(1)
	runtime.SetBlockProfileRate(1)
	p.enabled.Store(true)
	go func() {
		defer close(p.sampleDone)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cfg := cfg.Copy()
		cfg.Tracer = nil
		cfg.RuntimeParams["application_name"] = "moni-profile-observer"
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		p.walBefore = capacityWALSnapshot(conn)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-p.stopSample:
				p.walAfter = capacityWALSnapshot(conn)
				return
			case <-ticker.C:
			}
			phase := "completion"
			if admission() {
				phase = "admission"
			}
			rows, err := conn.Query(ctx, "SELECT query,coalesce(wait_event_type,''),coalesce(wait_event,'') FROM pg_stat_activity WHERE application_name=$1 AND state='active'", application)
			if err != nil {
				return
			}
			for rows.Next() {
				var sql, typ, event string
				if rows.Scan(&sql, &typ, &event) == nil {
					p.waits[phase+":"+typ+":"+event+":"+capacitySQLCategory(sql)]++
				}
			}
			rows.Close()
		}
	}()
}
func (p *capacityProfiler) stop(t *testing.T) {
	p.enabled.Store(false)
	close(p.stopSample)
	<-p.sampleDone
	runtime.SetMutexProfileFraction(p.oldMutex)
	runtime.SetBlockProfileRate(0)
	p.mu.Lock()
	defer p.mu.Unlock()
	groups := map[string][]float64{}
	firstSeen := map[string]int{}
	for i := range p.spans {
		s := &p.spans[i]
		s.Stage = p.stages[s.Tx]
		name := s.Kind + ":" + s.Stage + ":" + s.Category
		groups[name] = append(groups[name], s.DurationMS)
		firstSeen[name] += s.FirstSeen
	}
	summary := map[string]any{}
	for name, samples := range groups {
		total := 0.0
		for _, d := range samples {
			total += d
		}
		summary[name] = map[string]any{"count": len(samples), "total_ms": total, "latency": capacityDistribution(samples), "first_seen_connection_sql": firstSeen[name]}
	}
	templates := make([]string, 0, len(p.templates))
	for sql := range p.templates {
		templates = append(templates, sql)
	}
	data, err := json.MarshalIndent(map[string]any{"execution_mode": p.mode, "diagnostic_prepared_templates": p.warmCount, "sql_templates_without_arguments": templates, "observer_warning": "diagnostic only: tracer mutex/allocations, 2ms pg_stat_activity sampling and full mutex/block profiling add cost; prepare is nested in query; batch gaps are callback intervals, not server SQL timings; batch internal prepare is not reported by pgx PrepareTracer; first_seen is a connection/SQL observation, not proof of prepare; explicitly prepared comparison is warm-only and cannot replace the cold score", "summary": summary, "active_backend_samples": p.waits, "wal_before": p.walBefore, "wal_after": p.walAfter, "spans": p.spans}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(p.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mutex", "block"} {
		file, err := os.OpenFile(p.path+"."+name+".pprof", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		err = pprof.Lookup(name).WriteTo(file, 0)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
}
