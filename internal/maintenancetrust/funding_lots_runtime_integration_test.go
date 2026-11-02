package maintenancetrust

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/procurement"
)

// Owner preparation below is explicitly a fixture comparison-value source.
// Runtime never adopts an untrusted value from its own DB as expected input.
func runtimeFixtureExpected(t *testing.T, owner *pgx.Conn, schema string, want FundingLotsExpected) *fundinglotproof.CanonicalV2 {
	t.Helper()
	var raw []byte
	var digest string
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if owner.QueryRow(ctx, "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{schema, "funding_lot_runtime_attestations"}.Sanitize()).Scan(&raw, &digest) != nil {
		t.Fatal("runtime fixture owner expected originals")
	}
	p, e := fundinglotproof.DecodeV2(raw)
	if e != nil || p.SHA256() != want.RuntimeProofDigest || digest != want.RuntimeProofDigest {
		t.Fatal("runtime fixture expected differs from original plan")
	}
	return p
}
func runtimeFixtureGrant(t *testing.T, owner *pgx.Conn, pins fundinglotcatalog.Pins) {
	t.Helper()
	raw := snapshotFixedSource(t, snapshotRepositoryRoot(t), "specs/022-self-service-payments/c0-runtime-read-permissions.sql", "97abd87df7e5d0e7a03adae4158470e76ab94d58c9e9d8b92ae792aeb84867c2")
	sql := strings.ReplaceAll(string(raw), "{{schema}}", pgx.Identifier{pins.AppSchema.Name}.Sanitize())
	sql = strings.ReplaceAll(sql, "{{app}}", pgx.Identifier{pins.App.Name}.Sanitize())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if strings.Contains(sql, "{{") {
		t.Fatal("runtime fixture unresolved ACL")
	}
	if _, e := owner.Exec(ctx, sql); e != nil {
		t.Fatal("runtime fixture exact offline four-column grant")
	}
}

type runtimeRecordingTx struct {
	pgx.Tx
	sql       []string
	lifecycle int
	queryHook func(context.Context, string, []any) (pgx.Rows, error, bool)
}

func (x *runtimeRecordingTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	x.sql = append(x.sql, sql)
	if x.queryHook != nil {
		r, e, handled := x.queryHook(ctx, sql, args)
		if handled {
			return r, e
		}
	}
	return x.Tx.Query(ctx, sql, args...)
}
func (x *runtimeRecordingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	x.sql = append(x.sql, sql)
	return x.Tx.QueryRow(ctx, sql, args...)
}
func (x *runtimeRecordingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	x.sql = append(x.sql, sql)
	return x.Tx.Exec(ctx, sql, args...)
}
func (x *runtimeRecordingTx) Begin(ctx context.Context) (pgx.Tx, error) {
	x.lifecycle++
	return nil, errors.New("fixture forbids nested transactions")
}
func (x *runtimeRecordingTx) Commit(context.Context) error {
	x.lifecycle++
	return errors.New("fixture forbids composer commit")
}
func (x *runtimeRecordingTx) Rollback(context.Context) error {
	x.lifecycle++
	return errors.New("fixture forbids composer rollback")
}

func runtimeActualTx(t *testing.T, cfg *pgx.ConnConfig, opts pgx.TxOptions) (*pgx.Conn, *runtimeRecordingTx) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := pgx.ConnectConfig(ctx, cfg)
	if e != nil {
		t.Fatal("runtime actual LOGIN connect")
	}
	t.Cleanup(func() { runtimeFixtureCleanup(t, c, nil) })
	tx, e := c.BeginTx(ctx, opts)
	if e != nil {
		t.Fatal("runtime caller begin")
	}
	r := &runtimeRecordingTx{Tx: tx}
	t.Cleanup(func() { runtimeFixtureCleanup(t, c, tx) })
	var current, session string
	if tx.QueryRow(ctx, "SELECT current_user::text,session_user::text").Scan(&current, &session) != nil || current != cfg.User || session != cfg.User {
		t.Fatal("runtime proof did not use actual configured LOGIN")
	}
	return c, r
}
func runtimeFixtureCleanup(t *testing.T, c *pgx.Conn, tx pgx.Tx) {
	t.Helper()
	if c == nil || c.IsClosed() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if tx != nil {
		e := tx.Rollback(ctx)
		if e != nil && !errors.Is(e, pgx.ErrTxClosed) || c.PgConn().IsBusy() || c.PgConn().TxStatus() != 'I' {
			_ = c.Close(ctx)
		}
	}
	if !c.IsClosed() {
		if c.PgConn().IsBusy() || c.PgConn().TxStatus() != 'I' {
			t.Error("runtime caller cleanup did not confirm reusable idle connection")
		}
		if c.Close(ctx) != nil {
			t.Error("runtime caller close")
		}
	}
}
func runtimeAssertReadOnly(t *testing.T, r *runtimeRecordingTx, complete bool) {
	t.Helper()
	tables := map[string]bool{}
	for _, sql := range r.sql {
		upper := strings.ToUpper(strings.TrimSpace(sql))
		if !strings.HasPrefix(upper, "SELECT") && !strings.HasPrefix(upper, "WITH") {
			t.Fatal("composer sent nonread statement")
		}
		if strings.HasPrefix(sql, "WITH page AS MATERIALIZED") {
			parts := strings.SplitN(sql, " FROM ONLY ", 2)
			if len(parts) != 2 {
				t.Fatal("fixed scanner SQL shape")
			}
			tables[strings.SplitN(parts[1], " AS s", 2)[0]] = true
		}
	}
	if r.lifecycle != 0 {
		t.Fatal("composer took over caller lifecycle")
	}
	if complete && len(tables) != 84 {
		t.Fatalf("composer did not read all 84 tables: %d", len(tables))
	}
}
func runtimeOrdinaryConfig(f *snapshotFixture, role string) *pgx.ConnConfig {
	cfg := f.cfg.Copy()
	cfg.User = role
	return cfg
}
func runtimeIssueOrdinary(t *testing.T, r *runtimeRecordingTx, pins fundinglotcatalog.Pins, p *fundinglotproof.CanonicalV2) *fundinglotruntime.RuntimeProof {
	t.Helper()
	start := time.Now()
	proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, pins, p)
	if e != nil || proof == nil {
		t.Fatalf("actual APP complete runtime failed: %v elapsed=%s", e, time.Since(start))
	}
	runtimeAssertReadOnly(t, r, true)
	if proof.VerifyTx(context.Background(), r) != nil {
		t.Fatal("returned proof retained canceled issuance context")
	}
	t.Logf("actual APP full runtime success in %s (%d recorded SELECTs)", time.Since(start), len(r.sql))
	return proof
}
func TestFundingLotsRuntimePostgresNamedMixedAndLifetime(t *testing.T) {
	for round := 0; round < 2; round++ {
		t.Run(string(rune('A'+round)), func(t *testing.T) {
			f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
			plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
			fundingLotsInitOrdinary(t, f, plan.Expected)
			owner := f.connect(t)
			expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
			runtimeFixtureGrant(t, owner, f.pins)
			c, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			proof := runtimeIssueOrdinary(t, r, f.pins, expected)
			copyProof := *proof
			before := len(r.sql)
			if copyProof.VerifyTx(context.Background(), r) != nil {
				t.Fatal("copy lost original identity")
			}
			for _, q := range r.sql[before:] {
				if strings.Contains(q, "WITH page AS MATERIALIZED") {
					t.Fatal("VerifyTx rescanned money")
				}
			}
			alternate := &runtimeRecordingTx{Tx: r.Tx}
			if proof.VerifyTx(context.Background(), alternate) != fundinglotruntime.ErrRuntime {
				t.Fatal("proof accepted another wrapper")
			}
			if r.Tx.Rollback(context.Background()) != nil {
				t.Fatal("caller rollback")
			}
			if proof.VerifyTx(context.Background(), r) != fundinglotruntime.ErrRuntime || copyProof.VerifyTx(context.Background(), r) != fundinglotruntime.ErrRuntime {
				t.Fatal("proof survived transaction end")
			}
			next, e := c.BeginTx(context.Background(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if e != nil {
				t.Fatal(e)
			}
			r.Tx = next
			if proof.VerifyTx(context.Background(), r) != fundinglotruntime.ErrRuntime {
				t.Fatal("proof rebound to later transaction on same wrapper")
			}
			runtimeFixtureCleanup(t, c, next)
			b, _ := json.Marshal(proof)
			if string(b) != `{"redacted":true}` {
				t.Fatal("proof JSON exposes internals")
			}
		})
	}
}
func TestFundingLotsRuntimePostgresIdentityAndHistoryRefusals(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	runtimeFixtureGrant(t, owner, f.pins)
	for _, name := range []string{"owner", "projector", "read_committed", "read_write", "search_path", "pins", "nil_expected", "canceled"} {
		t.Run(name, func(t *testing.T) {
			cfg := runtimeOrdinaryConfig(f, f.app)
			opts := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
			pins := f.pins
			p := expected
			ctx := context.Background()
			switch name {
			case "owner":
				cfg.User = f.owner
			case "projector":
				cfg.User = f.projector
			case "read_committed":
				opts.IsoLevel = pgx.ReadCommitted
			case "read_write":
				opts.AccessMode = pgx.ReadWrite
			case "search_path":
				cfg.RuntimeParams["search_path"] = "pg_catalog"
			case "pins":
				pins.App.OID++
			case "nil_expected":
				p = nil
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, r := runtimeActualTx(t, cfg, opts)
			proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(ctx, r, pins, p)
			if proof != nil || e != fundinglotruntime.ErrRuntime {
				t.Fatal("invalid identity admitted")
			}
			runtimeAssertReadOnly(t, r, false)
		})
	}
	// Mutate original history in an owner-only transaction, never the expected
	// artifact. Restore before the next independent APP snapshot.
	for _, name := range []string{"missing", "extra", "checksum", "oversized_name"} {
		t.Run(name, func(t *testing.T) {
			tx, e := owner.Begin(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			var first, checksum string
			if tx.QueryRow(context.Background(), "SELECT name,checksum FROM schema_migrations ORDER BY name LIMIT 1").Scan(&first, &checksum) != nil {
				t.Fatal("history fixture")
			}
			switch name {
			case "missing":
				_, e = tx.Exec(context.Background(), "DELETE FROM schema_migrations WHERE name=$1", first)
			case "extra":
				_, e = tx.Exec(context.Background(), "INSERT INTO schema_migrations(name,checksum) VALUES('023_unreviewed.sql',$1)", checksum)
			case "checksum":
				_, e = tx.Exec(context.Background(), "UPDATE schema_migrations SET checksum=$1 WHERE name=$2", strings.Repeat("0", 64), first)
			case "oversized_name":
				_, e = tx.Exec(context.Background(), "UPDATE schema_migrations SET name=$1 WHERE name=$2", strings.Repeat("x", 65537), first)
			}
			if e != nil || tx.Commit(context.Background()) != nil {
				t.Fatal("history mutation")
			}
			_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected)
			if proof != nil || e != fundinglotruntime.ErrRuntime {
				t.Fatal("corrupt actual history admitted")
			}
			runtimeFixtureCleanup(t, r.Conn(), r.Tx)
			restore, e := owner.Begin(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = restore.Exec(context.Background(), "DELETE FROM schema_migrations WHERE name=$1 OR name='023_unreviewed.sql' OR name=$2", first, strings.Repeat("x", 65537)); e != nil {
				t.Fatal("history restore delete")
			}
			if _, e = restore.Exec(context.Background(), "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", first, checksum); e != nil || restore.Commit(context.Background()) != nil {
				t.Fatal("history restoration")
			}
		})
	}
	_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
}

func TestFundingLotsRuntimePostgresOperatingBasisAndPagination(t *testing.T) {
	f := newSnapshotFixture(t)
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	config, e := pgxpool.ParseConfig("")
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig = f.cfg.Copy()
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	config.MaxConns = 1
	pool, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal("operating runtime pool")
	}
	defer pool.Close()
	b := &fundingLotsObservationBusiness{t: t, ctx: ctx, pool: pool, tenant: uuid.NewString(), user: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString(), model: uuid.NewString(), price: uuid.NewString(), key: uuid.NewString(), epoch: plan.Expected.EpochID, accounts: map[string]string{}}
	b.run(b.seed)
	lot := uuid.NewString()
	b.run(func(tx pgx.Tx) { b.fund(tx, lot) })
	var request, reservation, allocation string
	b.run(func(tx pgx.Tx) { request, reservation, allocation = b.reserve(tx, lot) })
	final, e := billing.New(pool).RecordFinalization(ctx, billing.FinalizationInput{TenantID: b.tenant, RequestID: request, Source: "gateway", SourceEventID: uuid.NewString(), Usage: billing.Usage{Status: "confirmed_final", InputTokens: 80, TotalTokens: 80}, ProtocolOutcome: "completed", TransportEndReason: "done", DeliveryStage: "none"})
	if e != nil {
		t.Fatal("operating finalization")
	}
	var settlement, settlementLot string
	b.run(func(tx pgx.Tx) {
		settlement, settlementLot = b.finish(tx, lot, request, reservation, allocation, final.ID)
	})
	for _, amount := range []int64{30, 20} {
		b.run(func(tx pgx.Tx) { b.refund(tx, lot, request, settlement, settlementLot, amount) })
	}
	// 129 new legitimate tenants cross the original 128-row physical page. The
	// original trigger issues each tenant's real zero-value genesis operation.
	if _, e = pool.Exec(ctx, "INSERT INTO tenants(id,name) SELECT 'page-'||g::text,'runtime pagination' FROM generate_series(1,129) g"); e != nil {
		t.Fatal("real pagination tenants")
	}
	pool.Close()
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	basisTx, e := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		t.Fatal(e)
	}
	current, e := fundinglotbasis.Read(ctx, fundingLotsBasisObserver{fundingLotsObserver{ctx, basisTx}}, f.schema)
	if e != nil || current.Summary().BasisSHA256 == plan.Expected.BasisDigest {
		t.Fatal("operating current basis unexpectedly equals historical cutover")
	}
	if basisTx.Rollback(ctx) != nil {
		t.Fatal("basis rollback")
	}
	runtimeFixtureGrant(t, owner, f.pins)
	_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
	pages := 0
	for _, q := range r.sql {
		if strings.HasPrefix(q, "WITH page AS MATERIALIZED") && strings.Contains(q, " FROM ONLY "+pgx.Identifier{f.schema, "tenants"}.Sanitize()+" AS s") {
			pages++
		}
	}
	if pages < 2 {
		t.Fatal("runtime did not exercise real 128-row pagination")
	}
}

func runtimeAuxiliaryHistory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var admin, tenant string
	if pool.QueryRow(ctx, "SELECT id FROM users WHERE platform_role='admin' LIMIT 1").Scan(&admin) != nil {
		t.Fatal("auxiliary actual business authority")
	}
	tenant = uuid.NewString()
	if _, e := pool.Exec(ctx, "INSERT INTO tenants(id,name) VALUES($1,'fresh periodic scope')", tenant); e != nil {
		t.Fatal("fresh periodic scope fixture")
	}
	command, e := procurement.New(pool).CreateAccount(ctx, admin, procurement.CreateInput{OperationID: uuid.NewString(), Label: "runtime upstream", Provider: "isolated-local", State: "active", LimitAmount: "1000000"})
	if e != nil || command.Status != "applied" {
		t.Fatalf("original procurement account API: status=%s error=%v", command.Status, e)
	}
	var account procurement.Account
	if json.Unmarshal(command.After, &account) != nil || account.ID == "" {
		t.Fatal("procurement original result")
	}
	commander, e := periodicbudget.NewCommander(pool)
	if e != nil {
		t.Fatal(e)
	}
	for _, scope := range []string{"tenant", "procurement"} {
		in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000000"}
		if scope == "tenant" {
			_, e = commander.ConfigurePlatformTenant(ctx, admin, tenant, in)
		} else {
			_, e = commander.ConfigureProcurement(ctx, admin, account.ID, in)
		}
		if e != nil {
			t.Fatal("original periodic configure API", e)
		}
	}
}

func runtimeCorruptScalar(t *testing.T, f *snapshotFixture, table, column, where, expression string) func() {
	t.Helper()
	relation := pgx.Identifier{f.schema, table}.Sanitize()
	col := pgx.Identifier{column}.Sanitize()
	key := "id"
	if table == "funding_lot_balances" {
		key = "lot_id"
	}
	keyColumn := pgx.Identifier{key}.Sanitize()
	var old, selected string
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if f.admin.QueryRow(ctx, "SELECT "+col+"::text,"+keyColumn+"::text FROM "+relation+" WHERE "+where+" LIMIT 1").Scan(&old, &selected) != nil {
		t.Fatal("corruption fixture must select an existing source row")
	}
	mutate := func(value string, args ...any) {
		t.Helper()
		tx, e := f.admin.Begin(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		if _, e = tx.Exec(context.Background(), "SET LOCAL session_replication_role=replica"); e != nil {
			t.Fatal("isolated administrator corruption fixture")
		}
		if _, e = tx.Exec(context.Background(), "UPDATE "+relation+" SET "+col+"="+value+" WHERE "+keyColumn+"="+"$"+strconv.Itoa(len(args)+1), append(args, selected)...); e != nil || tx.Commit(context.Background()) != nil {
			t.Fatal("isolated source row corruption")
		}
	}
	mutate(expression)
	return func() { mutate("$1", old) }
}
func TestFundingLotsRuntimePostgresAllOrdinaryDomainsRejectCorruption(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) {
		fundingLotsMixedHistory(t, f, pool)
		runtimeAuxiliaryHistory(t, pool)
	})
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	runtimeFixtureGrant(t, owner, f.pins)
	_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
	runtimeFixtureCleanup(t, r.Conn(), r.Tx)
	for _, tc := range []struct{ name, table, column, where, expr string }{
		{"ledger", "ledger_accounts", "balance", "kind='REVENUE'", "balance+1"},
		{"links", "funding_receipts", "source_id", "source_kind='manual_credit'", "'unproven-original-source'"},
		{"funding", "funding_receipts", "amount", "source_kind='manual_credit'", "amount-1"},
		{"customer", "api_keys", "spent", "spent>0", "spent+1"},
		{"procurement", "procurement_accounts", "spent_amount", "true", "spent_amount+1"},
		{"periodic", "periodic_budget_controls", "revision", "true", "revision+1"},
		{"wallet", "funding_lot_balances", "consumed_amount", "true", "consumed_amount+1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := runtimeCorruptScalar(t, f, tc.table, tc.column, tc.where, tc.expr)
			defer restore()
			_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected)
			if proof != nil || e != fundinglotruntime.ErrRuntime {
				t.Fatal("inconsistent source graph issued proof")
			}
			runtimeAssertReadOnly(t, r, false)
			runtimeFixtureCleanup(t, r.Conn(), r.Tx)
		})
	}
	_, r = runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
	runtimeFixtureCleanup(t, r.Conn(), r.Tx)
	t.Run("unproven_legacy_periodic_membership", func(t *testing.T) {
		config, e := pgxpool.ParseConfig("")
		if e != nil {
			t.Fatal(e)
		}
		config.ConnConfig = f.cfg.Copy()
		config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		config.MaxConns = 1
		pool, e := pgxpool.NewWithConfig(context.Background(), config)
		if e != nil {
			t.Fatal(e)
		}
		defer pool.Close()
		var admin, tenant string
		if pool.QueryRow(context.Background(), "SELECT id FROM users WHERE platform_role='admin' LIMIT 1").Scan(&admin) != nil || pool.QueryRow(context.Background(), "SELECT tenant_id FROM reservations WHERE amount>0 AND status='reserved' LIMIT 1").Scan(&tenant) != nil {
			t.Fatal("legacy held periodic fixture")
		}
		commander, e := periodicbudget.NewCommander(pool)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = commander.ConfigurePlatformTenant(context.Background(), admin, tenant, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000000"}); e != nil {
			t.Fatal("original legacy periodic configuration", e)
		}
		pool.Close()
		_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		facts, e := fundinglotscan.ReadWallet(ctx, fundingLotsObserver{ctx, r.Tx}, f.schema, fundinglotscan.ModeOrdinary)
		report := facts.Summary()
		if e != nil || report.Periodic.Unproven == (fundinglotverify.PeriodicBudgetGraphGaps{}) || report.Periodic.Mismatches != (fundinglotverify.PeriodicBudgetGraphMismatches{}) {
			t.Fatalf("fixture should retain zero-mismatch unproven legacy history: error=%v periodic=%+v", e, report.Periodic)
		}
		if proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected); proof != nil || e != fundinglotruntime.ErrRuntime {
			t.Fatal("unproven history became full runtime authority")
		}
	})
}

type runtimeFaultRows struct {
	pgx.Rows
	fault         string
	nexts, closes int
	scanned       bool
	actualEOF     bool
}

func (r *runtimeFaultRows) Next() bool {
	r.nexts++
	if r.fault == "eof_error" {
		return false
	}
	more := r.Rows.Next()
	if !more && r.Rows.Err() == nil {
		r.actualEOF = true
	}
	return more
}
func (r *runtimeFaultRows) Scan(dest ...any) error {
	r.scanned = true
	if r.fault == "scan_error" {
		return errors.New("private-driver-fault")
	}
	return r.Rows.Scan(dest...)
}
func (r *runtimeFaultRows) Err() error {
	if r.fault == "eof_error" {
		return errors.New("private-driver-fault")
	}
	return r.Rows.Err()
}
func (r *runtimeFaultRows) Close() { r.closes++; r.Rows.Close() }
func TestFundingLotsRuntimePostgresRealRowsCancelAndFreshCleanup(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	runtimeFixtureGrant(t, owner, f.pins)
	for _, fault := range []string{"scan_error", "eof_error", "cancel_unread", "terminate_backend"} {
		t.Run(fault, func(t *testing.T) {
			c, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			pid := c.PgConn().PID()
			var wrapped *runtimeFaultRows
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.queryHook = func(qctx context.Context, sql string, args []any) (pgx.Rows, error, bool) {
				if wrapped != nil || !strings.HasPrefix(sql, "WITH page AS MATERIALIZED") || !strings.Contains(sql, " FROM ONLY "+pgx.Identifier{f.schema, "ledger_accounts"}.Sanitize()+" AS s") {
					return nil, nil, false
				}
				rows, e := r.Tx.Query(qctx, sql, args...)
				if e != nil {
					return rows, e, true
				}
				wrapped = &runtimeFaultRows{Rows: rows, fault: fault}
				if fault == "cancel_unread" {
					cancel()
				}
				if fault == "terminate_backend" {
					var killed bool
					if f.admin.QueryRow(context.Background(), "SELECT pg_terminate_backend($1)", pid).Scan(&killed) != nil || !killed {
						t.Fatal("terminate only captured fixture APP PID")
					}
				}
				return wrapped, nil, true
			}
			start := time.Now()
			proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(ctx, r, f.pins, expected)
			if proof != nil || e != fundinglotruntime.ErrRuntime || wrapped == nil {
				t.Fatal("actual unread driver fault did not fail closed")
			}
			if wrapped.closes != 0 && !wrapped.actualEOF {
				t.Fatal("adapter explicitly drained incomplete actual rows")
			}
			runtimeAssertReadOnly(t, r, false)
			cleanupStart := time.Now()
			runtimeFixtureCleanup(t, c, r.Tx)
			var alive bool
			ended, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			for {
				if f.admin.QueryRow(ended, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&alive) != nil {
					t.Fatal("caller cleanup backend observation")
				}
				if !alive {
					break
				}
				if ended.Err() != nil {
					t.Fatal("caller fresh cleanup did not confirm original backend gone")
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Logf("runtime fault=%s returned in %s; fresh caller cleanup=%s, original backend gone", fault, time.Since(start), time.Since(cleanupStart))
		})
	}
}

func TestFundingLotsRuntimePostgresPermissionDriftAndLockedSQL(t *testing.T) {
	f := newSnapshotFixturePrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) { fundingLotsMixedHistory(t, f, pool) })
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	fundingLotsInitOrdinary(t, f, plan.Expected)
	owner := f.connect(t)
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	checkRefused := func() {
		t.Helper()
		_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected)
		if proof != nil || e != fundinglotruntime.ErrRuntime {
			t.Fatal("unexpected catalog permission graph issued proof")
		}
		runtimeFixtureCleanup(t, r.Conn(), r.Tx)
	}
	t.Run("original_profile_missing_four_columns", func(t *testing.T) { checkRefused() })
	runtimeFixtureGrant(t, owner, f.pins)
	relation := pgx.Identifier{f.schema, "payment_query_conflicts"}.Sanitize()
	app := pgx.Identifier{f.app}.Sanitize()
	for _, tc := range []struct{ name, apply, restore string }{
		{"missing_column", "REVOKE SELECT(id) ON " + relation + " FROM " + app, "GRANT SELECT(id) ON " + relation + " TO " + app},
		{"table_wide_select", "GRANT SELECT ON " + relation + " TO " + app, "REVOKE SELECT ON " + relation + " FROM " + app},
		{"public_column", "GRANT SELECT(id) ON " + relation + " TO PUBLIC", "REVOKE SELECT(id) ON " + relation + " FROM PUBLIC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := owner.Exec(context.Background(), tc.apply); e != nil {
				t.Fatal("fixture permission delta")
			}
			defer func() {
				if _, e := owner.Exec(context.Background(), tc.restore); e != nil {
					t.Fatal("fixture permission restore")
				}
				// PostgreSQL table-level REVOKE also removes explicit column
				// SELECTs. Reinstall only the fixed reviewed delta.
				runtimeFixtureGrant(t, owner, f.pins)
			}()
			checkRefused()
		})
	}
	t.Run("set_role_is_not_actual_app_login", func(t *testing.T) {
		cfg := f.cfg.Copy()
		cfg.User = f.admin.Config().User
		cfg.Password = f.admin.Config().Password
		_, r := runtimeActualTx(t, cfg, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		if _, e := r.Tx.Exec(context.Background(), "SET LOCAL ROLE "+app); e != nil {
			t.Fatal("fixture administrator SET ROLE")
		}
		if p, e := fundinglotruntime.VerifyOrdinaryApplicationTx(context.Background(), r, f.pins, expected); p != nil || e != fundinglotruntime.ErrRuntime {
			t.Fatal("SET ROLE substituted for actual LOGIN")
		}
	})
	t.Run("cancel_observed_actual_app_lock_wait", func(t *testing.T) {
		c, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		pid := c.PgConn().PID()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var blocker pgx.Tx
		armed := false
		observed := make(chan bool, 1)
		r.queryHook = func(qctx context.Context, sql string, args []any) (pgx.Rows, error, bool) {
			if armed || !strings.HasPrefix(sql, "WITH page AS MATERIALIZED") || !strings.Contains(sql, " FROM ONLY "+pgx.Identifier{f.schema, "journal_lines"}.Sanitize()+" AS s") {
				return nil, nil, false
			}
			armed = true
			var e error
			blocker, e = owner.Begin(context.Background())
			if e != nil {
				t.Fatal("fixture blocker begin")
			}
			if _, e = blocker.Exec(context.Background(), "SET LOCAL lock_timeout='500ms'"); e != nil {
				t.Fatal("fixture bounded blocker")
			}
			if _, e = blocker.Exec(context.Background(), "LOCK TABLE "+pgx.Identifier{f.schema, "journal_lines"}.Sanitize()+" IN ACCESS EXCLUSIVE MODE"); e != nil {
				t.Fatal("fixture blocker lock")
			}
			go func() {
				monitor, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				for monitor.Err() == nil {
					var waiting bool
					e := f.admin.QueryRow(monitor, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND usename=$2 AND wait_event_type='Lock')", pid, f.app).Scan(&waiting)
					if e != nil {
						break
					}
					if waiting {
						observed <- true
						cancel()
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
				observed <- false
				cancel()
			}()
			return nil, nil, false
		}
		proof, e := fundinglotruntime.VerifyOrdinaryApplicationTx(ctx, r, f.pins, expected)
		if proof != nil || e != fundinglotruntime.ErrRuntime || !armed {
			t.Fatalf("locked actual APP query result: proof=%t error=%v armed=%t", proof != nil, e, armed)
		}
		if !<-observed {
			t.Fatal("actual APP SQL lock wait was not independently observed")
		}
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if blocker == nil || blocker.Rollback(cleanup) != nil {
			t.Fatal("fixture lock cleanup")
		}
		runtimeFixtureCleanup(t, c, r.Tx)
		runtimeAssertReadOnly(t, r, false)
	})
	_, r := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeIssueOrdinary(t, r, f.pins, expected)
}
