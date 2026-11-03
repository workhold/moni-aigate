package fundinglotcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

// The original offline fixtures remain untouched. These tests can only verify
// reviewed fixed V3 material; neither observations nor test output fill roots.
func prefixProfileInstall(t *testing.T, f walletProfileFixture) {
	t.Helper()
	baseline := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	if migrations.VerifyOriginal22SnapshotTx(f.ctx, baseline, f.pins.AppSchema.Name) != nil {
		t.Fatal("prefix fixture must contain exact original22 history")
	}
	diagnostic := newPrefixProfileDiagnosticTx(baseline)
	if p, err := VerifyRuntimeTx(f.ctx, diagnostic, f.pins, Application); err != nil || p == nil || p.Profile() != LegacyV1 {
		diagnostic.failure(t, "prefix original LegacyV1 fixture baseline", err)
	}
	if p, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, baseline, f.pins, Application); p != nil || err != ErrCatalog {
		t.Fatal("V3 accepted original LegacyV1 layout")
	}
	if baseline.Conn().PgConn().TxStatus() != 'T' {
		t.Fatal("V3 old-layout rejection aborted caller transaction")
	}
	walletProfileRollback(t, baseline)
	walletProfileInstall(t, f)
	baseline = beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	diagnostic = newPrefixProfileDiagnosticTx(baseline)
	if p, err := VerifyRuntimeProfileTx(f.ctx, diagnostic, f.pins, Application, WalletReadV2); err != nil || p == nil || p.Profile() != WalletReadV2 {
		diagnostic.failure(t, "prefix WalletReadV2 parent fixture baseline", err)
	}
	if p, err := VerifyPeriodicPrefixSnapshotTx(f.ctx, baseline, f.pins); p != nil || err != ErrCatalog {
		t.Fatal("V3 accepted original WalletReadV2 layout")
	}
	walletProfileRollback(t, baseline)
	ddl, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal("prefix reviewed DDL material")
	}
	permissions, err := periodicbudgetprefixschema.Permissions(f.pins.App.Name, f.pins.Projector.Name)
	if err != nil {
		t.Fatal("prefix reviewed ACL material")
	}
	install := beginCatalogTestTx(t, f.owner, pgx.ReadWrite)
	if migrations.VerifyOriginal22SnapshotTx(f.ctx, install, f.pins.AppSchema.Name) != nil {
		t.Fatal("prefix installation must first verify exact original22")
	}
	if _, err = install.Exec(f.ctx, ddl); err != nil {
		t.Fatal("prefix fixed offline DDL installation")
	}
	if _, err = install.Exec(f.ctx, permissions); err != nil {
		t.Fatal("prefix fixed offline ACL installation")
	}
	if err = install.Commit(f.ctx); err != nil {
		t.Fatal("prefix fixture install outcome unknown; never retry")
	}
	for key, want := range periodicPrefixBuiltinObjects {
		var typ, identity string
		var namespace, name *string
		if f.app.QueryRow(f.ctx, `SELECT type,schema,name,identity FROM pg_catalog.pg_identify_object($1,$2,$3)`, key.class, key.oid, key.sub).Scan(&typ, &namespace, &name, &identity) != nil || namespace == nil || *namespace != "pg_catalog" {
			t.Fatal("prefix fixed PostgreSQL builtin identity query")
		}
		label := identity
		if name != nil {
			label = *name
		}
		actual, err := canonical(object{"class": typ, "scope": "pg_catalog", "name": label, "identity": identity, "oid": key.oid, "sub_id": key.sub})
		if err != nil || string(actual) != want {
			t.Fatalf("fixed PostgreSQL builtin differs: class=%d oid=%d actual=%s static=%s", key.class, key.oid, actual, want)
		}
	}
}

// The unchanged historical sandbox factory's administrator is a database-wide
// audit pool. V3 maintenance issuers additionally need the fixed APP search_path
// on an actual administrator LOGIN, just as the ordinary fixture already uses.
func prefixProfileSandbox(t *testing.T) walletProfileFixture {
	t.Helper()
	f := walletProfileSandbox(t)
	cfg := f.admin.Config().Copy()
	cfg.ConnConfig.RuntimeParams = map[string]string{
		"search_path":      pgx.Identifier{f.pins.AppSchema.Name}.Sanitize() + ",pg_catalog,pg_temp",
		"application_name": "c0_prefix_audit",
	}
	pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal("prefix sandbox actual audit LOGIN pool")
	}
	t.Cleanup(pool.Close)
	if err = pool.Ping(f.ctx); err != nil {
		prefixProfileSQLFailure(t, "prefix sandbox audit LOGIN", err)
	}
	f.admin = pool
	return f
}

func prefixProfileCompareLive(t *testing.T, f walletProfileFixture) {
	t.Helper()
	expected, _, err := loadPeriodicPrefixExpected(f.pins)
	if err != nil {
		t.Fatal("fixed prefix descriptor unavailable")
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	observed, err := observeCatalogProfileDocumentsTx(f.ctx, tx, f.pins, expected)
	if err != nil || observed == nil {
		t.Fatal("prefix independent bounded metadata observation")
	}
	if validateCatalogDocuments(observed.observedSchema, observed.observedRoles) != nil {
		walletProfileDiagnoseClosedDocuments(t, observed.observedSchema, observed.observedRoles)
		t.Fatal("prefix observed catalog DTO")
	}
	names, err := schemaIdentifiersTx(f.ctx, tx, f.pins)
	if err != nil {
		t.Fatal("prefix exact schema identifiers")
	}
	matches := 0
	for _, variant := range expected.Variants {
		if compareDescriptorValue(observed.observedSchema, variant.Schema, names, "") == nil && compareDescriptorValue(observed.observedRoles, variant.Roles, names, "") == nil {
			matches++
		}
	}
	if matches != 1 {
		// Category-level digests locate a static derivation discrepancy without
		// exposing fixture names, function text, or accepting observed metadata.
		for _, variant := range expected.Variants {
			if variant.Name != prefixProfileVariantName(f.pins, variant.Name) {
				continue
			}
			for _, family := range []struct {
				name      string
				got, want document
			}{{"schema", observed.observedSchema, variant.Schema}, {"roles", observed.observedRoles, variant.Roles}} {
				rendered := walletProfileRenderExpected(t, family.want, names, descriptorComparePath{}).(object)
				for scope, value := range family.got {
					actual := value.(object)
					want := rendered[scope].(object)
					for category, rows := range actual {
						if !same(rows, want[category]) {
							a, _ := canonical(rows)
							b, _ := canonical(want[category])
							t.Logf("fixed V3 discrepancy: variant=%s %s/%s/%s observed_sha=%s static_sha=%s", variant.Name, family.name, scope, category, digest(a), digest(b))
							prefixProfileDiagnoseRecords(t, rows, want[category])
						}
					}
				}
			}
		}
		t.Fatal("live catalog did not match exactly one independently derived V3 variant")
	}
	walletProfileRollback(t, tx)
}

func prefixProfileDiagnoseRecords(t *testing.T, actual, expected any) {
	t.Helper()
	a, ok := actual.([]any)
	b, other := expected.([]any)
	if !ok || !other {
		return
	}
	byID := map[string]object{}
	for _, value := range b {
		id, err := recordIdentity(value)
		if err == nil {
			byID[id] = value.(object)
		}
	}
	for _, value := range a {
		id, err := recordIdentity(value)
		if err != nil {
			continue
		}
		want, exists := byID[id]
		if !exists {
			t.Logf("fixed V3 unexpected normalized identity: %s", id)
			return
		}
		delete(byID, id)
		got := value.(object)
		for field, actual := range got {
			if !same(actual, want[field]) {
				a, _ := canonical(actual)
				b, _ := canonical(want[field])
				t.Logf("fixed V3 record field differs: identity=%s field=%s observed_sha=%s static_sha=%s", id, field, digest(a), digest(b))
				return
			}
		}
	}
	for id := range byID {
		t.Logf("fixed V3 missing normalized identity: %s", id)
		return
	}
}

// Diagnostic iteration keeps only the selected namespace kind; login flag
// variants still produce their own safe fixed-name digest summaries.
func prefixProfileVariantName(p Pins, name string) string {
	if p.Mode == Sandbox || strings.HasPrefix(name, "public-") == (p.AppSchema.Name == "public") {
		return name
	}
	return ""
}

func prefixProfileCheckIssued(t *testing.T, f walletProfileFixture) {
	t.Helper()
	expected, root, err := loadPeriodicPrefixExpected(f.pins)
	if err != nil {
		t.Fatal(err)
	}
	runtimes := []struct {
		pool *pgxpool.Pool
		role RuntimeRole
	}{{f.app, Application}, {f.projector, Projector}}
	if f.worker != nil {
		runtimes = append(runtimes, struct {
			pool *pgxpool.Pool
			role RuntimeRole
		}{f.worker, Worker})
	}
	for _, selected := range runtimes {
		tx := beginCatalogTestTx(t, selected.pool, pgx.ReadOnly)
		diagnostic := newPrefixProfileDiagnosticTx(tx)
		p, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, diagnostic, f.pins, selected.role)
		if err != nil || p == nil || p.Profile() != PeriodicPrefixV3 || p.DescriptorVersion() != 1 || p.ExpectedSHA256() != root || !p.MatchesPins(f.pins) {
			diagnostic.failure(t, "prefix actual selected LOGIN full-root issuance", err)
		}
		if err = p.VerifyTx(f.ctx, diagnostic); err != nil {
			diagnostic.failure(t, "prefix actual selected LOGIN original binding", err)
		}
		matched := 0
		for _, variant := range expected.Variants {
			if p.SchemaSHA256() == digest(variant.schemaCanonical) && p.RolesSHA256() == digest(variant.rolesCanonical) {
				matched++
			}
		}
		if matched != 1 {
			t.Fatal("prefix actual runtime tuple not in fixed complete root")
		}
		for _, profile := range []Profile{LegacyV1, WalletReadV2, PeriodicPrefixV3} {
			if old, err := VerifyRuntimeProfileTx(f.ctx, tx, f.pins, selected.role, profile); old != nil || err != ErrCatalog {
				t.Fatal("old generic runtime accepted candidate layout or dedicated V3 enum")
			}
		}
		wrong := Application
		if selected.role == Application {
			wrong = Projector
		}
		if proof, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, tx, f.pins, wrong); proof != nil || err != ErrCatalog {
			t.Fatal("prefix accepted a different selected LOGIN")
		}
		if f.pins.Mode == Ordinary {
			if proof, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, tx, f.pins, Worker); proof != nil || err != ErrCatalog {
				t.Fatal("ordinary prefix accepted worker")
			}
		}
		if tx.Conn().PgConn().TxStatus() != 'T' {
			t.Fatal("prefix catalog rejection aborted caller transaction")
		}
		walletProfileRollback(t, tx)
		if p.VerifyTx(f.ctx, tx) != ErrCatalog {
			t.Fatal("prefix proof survived transaction termination")
		}
		writable := beginCatalogTestTx(t, selected.pool, pgx.ReadWrite)
		if p, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, writable, f.pins, selected.role); p != nil || err != ErrCatalog {
			t.Fatal("prefix runtime accepted RR RW")
		}
		walletProfileRollback(t, writable)
		if selected.role != Application {
			for _, table := range []string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal"} {
				_, err := selected.pool.Exec(f.ctx, "SELECT * FROM "+pgx.Identifier{f.pins.AppSchema.Name, table}.Sanitize()+" LIMIT 0")
				pgerr, ok := err.(*pgconn.PgError)
				if !ok || pgerr.Code != "42501" {
					t.Fatal("prefix worker/projector actual LOGIN read new business evidence")
				}
			}
		}
	}
	for _, access := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
		tx := beginCatalogTestTx(t, f.admin, access)
		diagnostic := newPrefixProfileDiagnosticTx(tx)
		proof, err := VerifyPeriodicPrefixSnapshotTx(f.ctx, diagnostic, f.pins)
		if err != nil || proof == nil || proof.Profile() != PeriodicPrefixV3 {
			diagnostic.failure(t, "prefix actual maintenance LOGIN snapshot", err)
		}
		if err = proof.VerifyTx(f.ctx, diagnostic); err != nil {
			diagnostic.failure(t, "prefix actual maintenance original binding", err)
		}
		diagnostic = newPrefixProfileDiagnosticTx(tx)
		installation, err := VerifyPeriodicPrefixInstallationTx(f.ctx, diagnostic, f.pins)
		if err != nil || installation == nil || installation.Catalog().Profile() != PeriodicPrefixV3 {
			diagnostic.failure(t, "prefix original sixteen-output empty installation", err)
		}
		if err = installation.VerifyTx(f.ctx, diagnostic); err != nil {
			diagnostic.failure(t, "prefix installation original binding", err)
		}
		if proof, err := VerifyInstallationTx(f.ctx, tx, f.pins); proof != nil || err != ErrCatalog {
			t.Fatal("old installation API accepted V3 layout")
		}
		walletProfileRollback(t, tx)
	}
	impersonated := beginCatalogTestTx(t, f.admin, pgx.ReadOnly)
	if _, err := impersonated.Exec(f.ctx, "SET LOCAL ROLE "+pgx.Identifier{f.pins.App.Name}.Sanitize()); err != nil {
		t.Fatal("prefix SET ROLE negative setup")
	}
	if proof, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, impersonated, f.pins, Application); proof != nil || err != ErrCatalog {
		t.Fatal("prefix runtime accepted SET ROLE impersonation")
	}
	walletProfileRollback(t, impersonated)
}

func prefixProfileCompleteRead(t *testing.T, f walletProfileFixture) {
	t.Helper()
	var readset struct {
		FormatVersion  int `json:"format_version"`
		Source, Wallet struct {
			Tables []walletProfileReadTable `json:"tables"`
			Counts struct {
				Tables, Columns    int
				ColumnsWithHistory int `json:"columns_with_history"`
			} `json:"counts"`
		}
		History      walletProfileReadTable `json:"history"`
		SourceSHA256 map[string]string      `json:"source_sha256"`
	}
	raw, err := os.ReadFile(f.root + "/specs/023-periodic-products/b-prefix-readset.json")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&readset) != nil || decoder.Decode(new(any)) != io.EOF || readset.FormatVersion != 1 || readset.Wallet.Counts.Tables != 87 || readset.Wallet.Counts.Columns != 804 || readset.Wallet.Counts.ColumnsWithHistory != 806 || len(readset.Wallet.Tables) != 87 || readset.History.Name != "schema_migrations" || !same(readset.History.Columns, []string{"name", "checksum"}) {
		t.Fatal("prefix frozen complete APP readset")
	}
	for name, sha := range readset.SourceSHA256 {
		ordinaryVariantSource(t, f.root, name, sha)
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	if migrations.VerifyOriginal22SnapshotTx(f.ctx, tx, f.pins.AppSchema.Name) != nil {
		t.Fatal("prefix actual APP exact original22 history")
	}
	columns := 0
	for _, table := range append(append([]walletProfileReadTable(nil), readset.Wallet.Tables...), readset.History) {
		projection := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			projection[i] = pgx.Identifier{col}.Sanitize()
		}
		rows, err := tx.Query(f.ctx, "SELECT "+strings.Join(projection, ",")+" FROM ONLY "+pgx.Identifier{f.pins.AppSchema.Name, table.Name}.Sanitize())
		if err != nil {
			t.Fatalf("prefix actual APP explicit read denied: %s", table.Name)
		}
		fields := rows.FieldDescriptions()
		if len(fields) != len(table.Columns) {
			rows.Close()
			t.Fatal("prefix explicit projection column count")
		}
		for i, field := range fields {
			if field.Name != table.Columns[i] {
				rows.Close()
				t.Fatal("prefix explicit projection column identity")
			}
		}
		for rows.Next() {
			if _, err = rows.Values(); err != nil {
				rows.Close()
				t.Fatal("prefix actual APP row decoding")
			}
		}
		rows.Close()
		if rows.Err() != nil {
			t.Fatal("prefix actual APP physical EOF")
		}
		columns += len(table.Columns)
	}
	if columns != 806 {
		t.Fatal("prefix APP omitted a frozen column")
	}
	query := &walletProfileSourceQuery{ctx: f.ctx, tx: tx, schema: f.pins.AppSchema.Name, calls: map[string]int{}, tables: map[string]bool{}}
	for _, table := range readset.Wallet.Tables {
		query.tables[table.Name] = true
	}
	mode := fundinglotscan.ModeOrdinary
	if f.pins.Mode == Sandbox {
		mode = fundinglotscan.ModeSandbox
	}
	facts, err := fundinglotscan.ReadWalletPrefix(f.ctx, query, f.pins.AppSchema.Name, mode)
	if err != nil || !facts.Summary().PhysicalEOFComplete || facts.Summary().ScannedTables != 87 || facts.Summary().ScannedColumns != 804 || len(query.calls) != 87 {
		t.Fatal("prefix actual APP scanner did not consume every frozen physical EOF")
	}
	walletProfileRollback(t, tx)
}

func prefixProfileSeedBusinessHistory(t *testing.T, f walletProfileFixture) {
	t.Helper()
	actor, account := uuid.NewString(), uuid.NewString()
	// Provisioning identities is an offline fixture owner action. The APP
	// receives no additional identity INSERT surface for this test. A new tenant
	// would correctly require a completed C0 epoch, whereas an independent
	// procurement account needs no customer funds or fabricated initialization.
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only','admin')`, actor, actor+"@fixture.invalid"); err != nil {
		prefixProfileSQLFailure(t, "prefix offline actor identity seed", err)
	}
	if _, err := f.owner.Exec(f.ctx, `INSERT INTO procurement_accounts(id,label,provider,state,limit_amount) VALUES($1,'prefix-catalog-business-history','fixture','active',1000)`, account); err != nil {
		prefixProfileSQLFailure(t, "prefix offline procurement identity seed", err)
	}
	commander, err := periodicbudget.NewCommander(f.app)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := commander.ConfigureProcurement(f.ctx, actor, account, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000"})
	if err != nil {
		prefixProfileSQLFailure(t, "prefix real APP first configuration", err)
	}
	var window string
	if err = f.app.QueryRow(f.ctx, `SELECT pb_observe($1,pb_now())`, configured.Receipt.Operation.ResourceID).Scan(&window); err != nil || window == "" {
		prefixProfileSQLFailure(t, "prefix real APP window birth", err)
	}
	tx := beginCatalogTestTx(t, f.admin, pgx.ReadOnly)
	for _, table := range []string{"periodic_budget_observation_origins", "periodic_budget_observation_heads", "periodic_budget_observation_journal"} {
		var present bool
		if tx.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM "+pgx.Identifier{f.pins.AppSchema.Name, table}.Sanitize()+")").Scan(&present) != nil || !present {
			t.Fatal("prefix business evidence must really be nonempty")
		}
	}
	if p, err := VerifyPeriodicPrefixInstallationTx(f.ctx, tx, f.pins); err != nil || p == nil || p.VerifyTx(f.ctx, tx) != nil {
		t.Fatal("prefix installation rejected real nonempty periodic business evidence")
	}
	walletProfileRollback(t, tx)
}

func prefixProfileSQLFailure(t *testing.T, stage string, err error) {
	t.Helper()
	code := "non-postgres"
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		code = pgerr.Code
	}
	t.Fatalf("%s (sqlstate=%s)", stage, code)
}

// This test-only wrapper records where the original five-second call stopped.
// It never retries a query, extends a context, prints SQL/arguments, or changes
// the production issuer's error surface. The wrapper itself remains the proof's
// exact transaction identity, including when checking its original binding.
type prefixProfileDiagnosticTx struct {
	pgx.Tx
	started, deadline time.Time
	calls, identities int
	lastQuery         string
	driverTime        time.Duration
	driverClass       string
	sqlstate          string
	changedDeadline   bool
	queries           []prefixProfileQueryTiming
}

type prefixProfileQueryTiming struct {
	hash    string
	started time.Time
	wall    time.Duration
	driver  time.Duration
}

func newPrefixProfileDiagnosticTx(tx pgx.Tx) *prefixProfileDiagnosticTx {
	return &prefixProfileDiagnosticTx{Tx: tx, started: time.Now(), driverClass: "none", sqlstate: "none"}
}

func (x *prefixProfileDiagnosticTx) record(ctx context.Context, sql string) {
	if len(x.queries) != 0 {
		x.queries[len(x.queries)-1].wall = time.Since(x.queries[len(x.queries)-1].started)
	}
	d, ok := ctx.Deadline()
	if !ok || x.calls != 0 && !x.deadline.Equal(d) {
		x.changedDeadline = true
	}
	if x.calls == 0 {
		x.deadline = d
	}
	x.calls++
	if sql == transactionSQL {
		x.identities++
	}
	x.lastQuery = digest([]byte(sql))[:16]
	x.queries = append(x.queries, prefixProfileQueryTiming{hash: x.lastQuery, started: time.Now()})
}

func (x *prefixProfileDiagnosticTx) recordDriver(elapsed time.Duration) {
	x.driverTime += elapsed
	if len(x.queries) != 0 {
		x.queries[len(x.queries)-1].driver += elapsed
	}
}

func (x *prefixProfileDiagnosticTx) recordError(err error) {
	if err == nil || x.driverClass != "none" {
		return
	}
	x.driverClass = "driver"
	if errors.Is(err, context.DeadlineExceeded) {
		x.driverClass = "deadline"
	} else if errors.Is(err, context.Canceled) {
		x.driverClass = "canceled"
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		x.driverClass, x.sqlstate = "postgres", pgerr.Code
	}
}

func (x *prefixProfileDiagnosticTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	x.record(ctx, sql)
	start := time.Now()
	rows, err := x.Tx.Query(ctx, sql, args...)
	x.recordDriver(time.Since(start))
	x.recordError(err)
	if err != nil {
		return nil, err
	}
	return &prefixProfileDiagnosticRows{Rows: rows, owner: x}, nil
}

func (x *prefixProfileDiagnosticTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	x.record(ctx, sql)
	start := time.Now()
	row := x.Tx.QueryRow(ctx, sql, args...)
	x.recordDriver(time.Since(start))
	return prefixProfileDiagnosticRow{Row: row, owner: x}
}

type prefixProfileDiagnosticRows struct {
	pgx.Rows
	owner *prefixProfileDiagnosticTx
}

func (r *prefixProfileDiagnosticRows) Scan(dest ...any) error {
	start := time.Now()
	err := r.Rows.Scan(dest...)
	r.owner.recordDriver(time.Since(start))
	r.owner.recordError(err)
	return err
}
func (r *prefixProfileDiagnosticRows) Next() bool {
	start := time.Now()
	more := r.Rows.Next()
	r.owner.recordDriver(time.Since(start))
	return more
}
func (r *prefixProfileDiagnosticRows) Err() error {
	err := r.Rows.Err()
	r.owner.recordError(err)
	return err
}

type prefixProfileDiagnosticRow struct {
	pgx.Row
	owner *prefixProfileDiagnosticTx
}

func (r prefixProfileDiagnosticRow) Scan(dest ...any) error {
	start := time.Now()
	err := r.Row.Scan(dest...)
	r.owner.recordDriver(time.Since(start))
	r.owner.recordError(err)
	return err
}

func (x *prefixProfileDiagnosticTx) failure(t *testing.T, stage string, err error) {
	t.Helper()
	x.logTiming(t)
	elapsed := time.Since(x.started)
	expired := !x.deadline.IsZero() && !time.Now().Before(x.deadline)
	t.Fatalf("%s (catalog_error=%t elapsed_ms=%d driver_ms=%d between_driver_ms=%d total_budget_reached=%t deadline_expired=%t queries=%d identity_queries=%d last_query_sha=%s driver=%s sqlstate=%s changed_deadline=%t)",
		stage, err != nil, elapsed.Milliseconds(), x.driverTime.Milliseconds(), (elapsed - x.driverTime).Milliseconds(), elapsed >= catalogBudget, expired, x.calls, x.identities, x.lastQuery, x.driverClass, x.sqlstate, x.changedDeadline)
}

func (x *prefixProfileDiagnosticTx) logTiming(t *testing.T) {
	t.Helper()
	for i, q := range x.queries {
		if i == len(x.queries)-1 {
			q.wall = time.Since(q.started)
		}
		if q.wall >= 100*time.Millisecond {
			t.Logf("catalog timing: query=%d sql_sha=%s wall_ms=%d driver_ms=%d between_driver_ms=%d", i+1, q.hash, q.wall.Milliseconds(), q.driver.Milliseconds(), (q.wall - q.driver).Milliseconds())
		}
	}
}

// Optional profiling excludes eager expected authentication and the base
// fixture, covering the original baselines and selected issuer. It never changes
// their five-second deadline. This diagnostic is not an acceptance artifact.
func TestPeriodicPrefixCatalogTimingDiagnostic(t *testing.T) {
	if os.Getenv("C0_CATALOG_TIMING_DIAGNOSTIC") != "1" {
		t.Skip("requires explicit C0_CATALOG_TIMING_DIAGNOSTIC=1; diagnostic only")
	}
	f := prefixProfileSandbox(t)
	if path := os.Getenv("C0_CATALOG_CPU_PROFILE"); path != "" {
		file, err := os.Create(path)
		if err != nil || pprof.StartCPUProfile(file) != nil {
			t.Fatal("prefix catalog diagnostic profile")
		}
		defer file.Close()
		defer pprof.StopCPUProfile()
	}
	prefixProfileInstall(t, f)
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	diagnostic := newPrefixProfileDiagnosticTx(tx)
	proof, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, diagnostic, f.pins, Application)
	if err != nil || proof == nil {
		diagnostic.failure(t, "prefix full sandbox timing diagnostic", err)
	}
	diagnostic.logTiming(t)
	t.Logf("catalog selected issuer timing: elapsed_ms=%d driver_ms=%d queries=%d", time.Since(diagnostic.started).Milliseconds(), diagnostic.driverTime.Milliseconds(), diagnostic.calls)
	walletProfileRollback(t, tx)
}

func TestPeriodicPrefixCatalogIndependentInstallations(t *testing.T) {
	previous := map[string][3]uint32{}
	for round := 1; round <= 2; round++ {
		kinds := append(ordinaryVariantKinds(), ordinaryVariantKind("full-sandbox"))
		for _, kind := range kinds {
			t.Run(fmt.Sprintf("round_%d/%s", round, kind), func(t *testing.T) {
				var f walletProfileFixture
				if kind == "full-sandbox" {
					f = prefixProfileSandbox(t)
				} else {
					f = walletProfileOrdinary(t, kind)
				}
				prefixProfileInstall(t, f)
				var journalOID uint32
				if f.admin.QueryRow(f.ctx, `SELECT oid FROM pg_catalog.pg_class WHERE relnamespace=$1 AND relname='periodic_budget_observation_journal'`, f.pins.AppSchema.OID).Scan(&journalOID) != nil {
					t.Fatal("prefix actual object OID")
				}
				ids := [3]uint32{f.pins.Database.OID, f.pins.App.OID, journalOID}
				if old, ok := previous[string(kind)]; ok && (old[1] == ids[1] || old[2] == ids[2]) {
					t.Fatal("prefix second round reused role or relation OID")
				}
				previous[string(kind)] = ids
				prefixProfileCompareLive(t, f)
				prefixProfileCheckIssued(t, f)
				if round == 1 && (kind == ordinaryNamed || kind == "full-sandbox") {
					prefixProfileSeedBusinessHistory(t, f)
				}
				prefixProfileCompleteRead(t, f)
				t.Log("independent fixed V3 catalog: actual runtime LOGINs, exact original22, old/new isolation, fixed roots and complete APP projection; no full RuntimeProof or production acceptance")
			})
		}
	}
}

func prefixProfileRejectDrift(t *testing.T, f walletProfileFixture) {
	t.Helper()
	prefixProfileInstall(t, f)
	prefixProfileCompareLive(t, f)
	q := func(names ...string) string { return pgx.Identifier(names).Sanitize() }
	origins, journal := q(f.pins.AppSchema.Name, "periodic_budget_observation_origins"), q(f.pins.AppSchema.Name, "periodic_budget_observation_journal")
	app, projector := q(f.pins.App.Name), q(f.pins.Projector.Name)
	cases := []struct {
		name, sql string
		args      []any
		affected  int64
	}{
		{"revoke_app_select", "REVOKE SELECT ON " + origins + " FROM " + app, nil, -1},
		{"projector_content", "GRANT SELECT ON " + journal + " TO " + projector, nil, -1},
		{"source_update", "GRANT UPDATE(observation_id) ON " + q(f.pins.AppSchema.Name, "periodic_budget_operations") + " TO " + app, nil, -1},
		{"generated_insert", "GRANT INSERT(command_source_id) ON " + journal + " TO " + app, nil, -1},
		{"public_function", "GRANT EXECUTE ON FUNCTION " + q(f.pins.AppSchema.Name, "pb_prefix_immutable") + "() TO PUBLIC", nil, -1},
		{"function_body", "CREATE OR REPLACE FUNCTION " + q(f.pins.AppSchema.Name, "pb_prefix_immutable") + "() RETURNS trigger LANGUAGE plpgsql VOLATILE SECURITY INVOKER SET search_path TO " + q(f.pins.AppSchema.Name) + ",pg_catalog,pg_temp AS $$BEGIN RAISE EXCEPTION 'catalog-fixture-drift'; END $$", nil, -1},
		{"disabled_capture", "ALTER TABLE " + q(f.pins.AppSchema.Name, "periodic_budget_operations") + " DISABLE TRIGGER pb_operation_zz_prefix_capture", nil, -1},
		{"missing_fk", "ALTER TABLE " + journal + " DROP CONSTRAINT pb_observation_previous", nil, -1},
		{"missing_dependency", `DELETE FROM pg_catalog.pg_depend d USING pg_catalog.pg_constraint c,pg_catalog.pg_proc p WHERE d.classid='pg_catalog.pg_constraint'::regclass AND d.objid=c.oid AND c.connamespace=$1::oid AND c.conname='pb_operation_observation_uuid' AND d.refclassid='pg_catalog.pg_proc'::regclass AND d.refobjid=p.oid AND p.pronamespace=$1::oid AND p.proname='pb_uuid' AND d.deptype='n'`, []any{f.pins.AppSchema.OID}, 1},
		{"duplicate_dependency", `INSERT INTO pg_catalog.pg_depend SELECT d.* FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_constraint c ON d.classid='pg_catalog.pg_constraint'::regclass AND d.objid=c.oid JOIN pg_catalog.pg_proc p ON d.refclassid='pg_catalog.pg_proc'::regclass AND d.refobjid=p.oid WHERE c.connamespace=$1::oid AND c.conname='pb_operation_observation_uuid' AND p.pronamespace=$1::oid AND p.proname='pb_uuid' AND d.deptype='n'`, []any{f.pins.AppSchema.OID}, 1},
	}
	if f.pins.Mode == Sandbox {
		cases = append(cases, struct {
			name, sql string
			args      []any
			affected  int64
		}{"worker_new_content", "GRANT SELECT ON " + journal + " TO " + q(f.pins.Worker.Name), nil, -1})
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			tx := beginCatalogTestTx(t, f.admin, pgx.ReadWrite)
			diagnostic := newPrefixProfileDiagnosticTx(tx)
			if p, err := VerifyPeriodicPrefixSnapshotTx(f.ctx, diagnostic, f.pins); err != nil || p == nil {
				diagnostic.failure(t, "prefix drift original authenticated baseline", err)
			}
			tag, err := tx.Exec(f.ctx, item.sql, item.args...)
			if err != nil || item.affected >= 0 && tag.RowsAffected() != item.affected {
				t.Fatal("prefix controlled catalog drift did not fire")
			}
			if p, err := VerifyPeriodicPrefixSnapshotTx(f.ctx, tx, f.pins); p != nil || err != ErrCatalog {
				t.Fatal("V3 accepted catalog drift")
			}
			var one int
			if tx.QueryRow(f.ctx, "SELECT 1").Scan(&one) != nil || one != 1 || tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("prefix drift rejection ended caller transaction")
			}
			walletProfileRollback(t, tx)
		})
	}
	tx := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	diagnostic := newPrefixProfileDiagnosticTx(tx)
	if p, err := VerifyPeriodicPrefixRuntimeTx(f.ctx, diagnostic, f.pins, Application); err != nil || p == nil {
		diagnostic.failure(t, "prefix drift rollback did not restore reviewed full root", err)
	}
	walletProfileRollback(t, tx)
}

func TestPeriodicPrefixCatalogRejectsDrift(t *testing.T) {
	t.Run("ordinary", func(t *testing.T) { prefixProfileRejectDrift(t, walletProfileOrdinary(t, ordinaryNamed)) })
	t.Run("sandbox", func(t *testing.T) { prefixProfileRejectDrift(t, prefixProfileSandbox(t)) })
}
