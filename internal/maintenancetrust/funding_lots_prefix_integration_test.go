package maintenancetrust

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

// This fixture explicitly installs original22 and the frozen candidate. It is
// not a production installer; actual APP business pools close before the Gate.
func newPeriodicPrefixSnapshotFixture(t *testing.T, prepare func(*snapshotFixture, *pgxpool.Pool)) *snapshotFixture {
	t.Helper()
	return newSnapshotFixtureHistoryPrepared(t, func(f *snapshotFixture, pool *pgxpool.Pool) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal("prefix fixture RR begin")
		}
		defer tx.Rollback(context.Background())
		if migrations.VerifyOriginal22SnapshotTx(ctx, tx, f.schema) != nil {
			t.Fatal("prefix fixture exact original22")
		}
		ddl, err := periodicbudgetprefixschema.SQL()
		if err != nil {
			t.Fatal("prefix candidate material")
		}
		acl, err := periodicbudgetprefixschema.Permissions(f.app, f.projector)
		if err != nil {
			t.Fatal("prefix candidate permissions")
		}
		if _, err = tx.Exec(ctx, ddl); err != nil {
			t.Fatal("prefix fixture candidate DDL")
		}
		if _, err = tx.Exec(ctx, acl); err != nil || tx.Commit(ctx) != nil {
			t.Fatal("prefix fixture candidate permissions/commit")
		}
		if prepare != nil {
			prepare(f, pool)
		}
	}, true)
}

func periodicPrefixInstalled(t *testing.T, f *snapshotFixture, initialize bool) (*Gate, *Snapshot, *gateWire, observedBackend) {
	t.Helper()
	g, wire, backend := f.acquire(t, "")
	var snapshot *Snapshot
	var err error
	if initialize {
		snapshot, err = g.BeginPeriodicPrefixInit(context.Background())
	} else {
		snapshot, err = g.BeginPeriodicPrefixPlan(context.Background())
	}
	if err != nil {
		t.Fatal("prefix original begin", err)
	}
	proof, err := snapshot.VerifyPeriodicPrefixInstallation(context.Background())
	if err != nil || proof == nil || proof.Catalog().Profile() != fundinglotcatalog.PeriodicPrefixV3 {
		t.Fatal("prefix owned installation", err)
	}
	*proof = fundinglotcatalog.InstallationProof{}
	return g, snapshot, wire, backend
}

func periodicPrefixPlan(t *testing.T, f *snapshotFixture, epoch string) FundingLotsPlanResult {
	t.Helper()
	g, snapshot, _, backend := periodicPrefixInstalled(t, f, false)
	permit, err := Attest(context.Background(), snapshot)
	if err != nil {
		t.Fatal("prefix original plan authority", err)
	}
	plan, err := permit.PlanFundingLots(context.Background(), epoch)
	if err != nil {
		t.Fatalf("prefix protected plan: %v source=%+v", err, snapshot.state.fundingLots.prefixFacts.Summary())
	}
	if plan.Expected.DescriptorDigest != fundinglotbasis.PeriodicPrefixDescriptorSHA256 || snapshot.Outcome() != OutcomeCommitted || permit.Verify(context.Background()) == nil || g.Close(nil) != nil {
		t.Fatal("prefix plan publication/profile/terminal binding")
	}
	f.gone(t, backend)
	return plan
}

func periodicPrefixInit(t *testing.T, f *snapshotFixture, want FundingLotsExpected) {
	t.Helper()
	g, snapshot, wire, backend := periodicPrefixInstalled(t, f, true)
	permit, err := Attest(context.Background(), snapshot)
	if err != nil {
		t.Fatal("prefix original init authority", err)
	}
	result, err := permit.InitializeFundingLots(context.Background(), want)
	if err != nil || result.RuntimeProofDigest != want.RuntimeProofDigest || snapshot.Outcome() != OutcomeCommitted {
		t.Fatalf("prefix protected initialize: %v outcome=%d", err, snapshot.Outcome())
	}
	queries, _ := wire.recorded()
	lock, firstRead, commits := -1, -1, 0
	began := false
	for i, sql := range queries {
		if strings.HasPrefix(strings.ToUpper(sql), "BEGIN ") {
			began = true
		}
		if strings.EqualFold(sql, "commit") {
			commits++
		}
		if strings.HasPrefix(sql, "LOCK TABLE ") {
			if lock != -1 || sql != periodicPrefixInitLockSQL(f.schema) {
				t.Fatal("prefix Init did not use one fixed complete lock")
			}
			lock = i
		}
		if began && firstRead == -1 && (strings.HasPrefix(strings.ToUpper(sql), "SELECT") || strings.HasPrefix(strings.ToUpper(sql), "WITH")) {
			firstRead = i
		}
	}
	if lock == -1 || firstRead <= lock || commits != 1 || permit.Verify(context.Background()) == nil || g.Close(nil) != nil {
		t.Fatal("prefix original lock/commit/authority lifetime")
	}
	f.gone(t, backend)
}

func TestPeriodicPrefixMaintenancePostgresNonemptyActualApplication(t *testing.T) {
	var history runtimeProcurementHistory
	f := newPeriodicPrefixSnapshotFixture(t, func(f *snapshotFixture, catalog *pgxpool.Pool) {
		cfg, err := pgxpool.ParseConfig("")
		if err != nil {
			t.Fatal("actual APP pool config")
		}
		cfg.ConnConfig = f.cfg.Copy()
		cfg.ConnConfig.User = f.app
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
		cfg.MinConns, cfg.MaxConns = 0, 2
		business, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			t.Fatal("actual APP business pool")
		}
		defer business.Close()
		var current, session string
		if business.QueryRow(context.Background(), "SELECT current_user::text,session_user::text").Scan(&current, &session) != nil || current != f.app || session != f.app {
			t.Fatal("business did not use actual restricted APP LOGIN")
		}
		commander, err := periodicbudget.NewCommander(business)
		if err != nil {
			t.Fatal("prefix business commander")
		}
		var tenant, user, key, admin, account string
		configure := func(revision, limit string) {
			in := periodicbudget.PutPeriodicBudget{ExpectedRevision: revision, PeriodKind: "utc_month", Enabled: true, LimitAmount: limit}
			in.OperationID = uuid.NewString()
			if _, e := commander.ConfigurePlatformTenant(context.Background(), admin, tenant, in); e != nil {
				t.Fatal("actual APP tenant prefix command", e)
			}
			in.OperationID = uuid.NewString()
			if _, e := commander.ConfigureKey(context.Background(), user, tenant, key, in); e != nil {
				t.Fatal("actual APP key prefix command", e)
			}
			in.OperationID = uuid.NewString()
			if _, e := commander.ConfigureProcurement(context.Background(), admin, account, in); e != nil {
				t.Fatal("actual APP procurement prefix command", e)
			}
		}
		history = runtimePrepareActiveProcurementConfigured(t, catalog, business, func(a, b, c, d, e string) {
			tenant, user, key, admin, account = a, b, c, d, e
			configure("0", "1000000")
		})
		configure("1", "1000001")
		configure("2", "1000001") // genuine no-op after nonzero financial cuts
	})
	owner := f.connect(t)
	// The four-column wallet delta is independent from candidate installation.
	runtimeFixtureGrant(t, owner, f.pins)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	countWitness := func() int64 {
		var n int64
		if owner.QueryRow(ctx, `SELECT count(*) FROM periodic_budget_observation_journal`).Scan(&n) != nil || n == 0 {
			t.Fatal("nonempty persisted observation prefix required")
		}
		return n
	}
	before := countWitness()
	plan := periodicPrefixPlan(t, f, uuid.NewString())
	if plan.Metadata.TenantCount != 1 || plan.Metadata.AvailableTotal != "988" || plan.Metadata.HeldTotal != "0" {
		t.Fatal("independent retail opening 1000-12 differs")
	}
	periodicPrefixInit(t, f, plan.Expected)
	if countWitness() != before {
		t.Fatal("fixed C0 writer unexpectedly emitted PB observations")
	}
	expected := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	conn, app := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	runtimeAssertActiveProcurement(t, app, f.schema, history)
	facts, err := fundinglotscan.ReadWalletPrefix(ctx, fundingLotsObserver{ctx, app}, f.schema, fundinglotscan.ModeOrdinary)
	r := facts.Summary()
	if err != nil || r.Status != "consistent" || !r.CoverageComplete || r.ScannedTables != 87 || r.ScannedColumns != 804 ||
		r.Periodic.Unproven.OperationSnapshotPrefixUnproven == 0 || !r.PeriodicPrefix.Balanced || r.PeriodicPrefix.Base != r.Periodic {
		t.Fatalf("actual APP complete nonempty prefix: %v report=%+v", err, r)
	}
	proof, err := fundinglotruntime.VerifyOrdinaryPeriodicPrefixApplicationTx(ctx, app, f.pins, expected)
	if err != nil || proof == nil || proof.VerifyTx(ctx, app) != nil {
		t.Fatal("actual APP V3 full composer", err)
	}
	if proof.VerifyTx(ctx, &runtimeRecordingTx{Tx: app.Tx}) == nil {
		t.Fatal("V3 runtime proof accepted another wrapper")
	}
	runtimeAssertReadOnly(t, app, false)
	tables := map[string]bool{}
	for _, sql := range app.sql {
		if strings.HasPrefix(sql, "WITH page AS MATERIALIZED") {
			rest := strings.SplitN(sql, " FROM ONLY ", 2)
			if len(rest) == 2 {
				tables[strings.SplitN(rest[1], " AS s", 2)[0]] = true
			}
		}
	}
	if len(tables) != 87 {
		t.Fatal("V3 composer did not physically read all 87 tables")
	}
	if app.Tx.Rollback(ctx) != nil || proof.VerifyTx(ctx, app) == nil {
		t.Fatal("V3 proof survived original tx end")
	}
	runtimeFixtureCleanup(t, conn, nil)
	g, wire, backend := f.acquire(t, "")
	observed, err := g.ObservePeriodicPrefixFundingLots(ctx, plan.Expected)
	if err != nil || observed.Status != FundingLotsObservationMatched || observed.Observed != plan.Expected || observed.SourceRows == 0 || g.Close(nil) != nil {
		t.Fatal("V3 independent protected observer", err)
	}
	assertObservationReadOnly(t, wire, true)
	f.gone(t, backend)
}

func TestPeriodicPrefixMaintenancePostgresSourceBudgetAbandonsInit(t *testing.T) {
	f := newPeriodicPrefixSnapshotFixture(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
		// 520 individually valid 32KiB JSON objects exceed the one shared 16MiB
		// source budget. They are unrelated audit data, never fabricated funds.
		_, err := pool.Exec(context.Background(), `INSERT INTO audit_events(actor,action,target,request_id,sanitized_diff)
 SELECT 'fixture','prefix.resource_probe','fixture','',jsonb_build_object('payload',repeat('x',32768)) FROM generate_series(1,520)`)
		if err != nil {
			t.Fatal("prefix bounded audit budget fixture")
		}
	})
	owner := f.connect(t)
	runtimeFixtureGrant(t, owner, f.pins)
	if owner.Close(context.Background()) != nil {
		t.Fatal("prefix resource fixture owner close")
	}
	g, snapshot, _, backend := periodicPrefixInstalled(t, f, true)
	permit, err := Attest(context.Background(), snapshot)
	if err != nil {
		t.Fatal("prefix resource original authority", err)
	}
	got, err := permit.InitializeFundingLots(context.Background(), FundingLotsExpected{EpochID: uuid.NewString(),
		BasisDigest: strings.Repeat("a", 64), ManifestDigest: authorityManifestSHA,
		DescriptorDigest: fundinglotbasis.PeriodicPrefixDescriptorSHA256, RuntimeProofDigest: strings.Repeat("b", 64)})
	if err != ErrResource || got != (FundingLotsInitResult{}) || snapshot.Outcome() != OutcomeAbandoned || permit.state.scanAttempted&fundingLotsOnce == 0 || permit.Verify(context.Background()) == nil {
		t.Fatal("shared source resource failure did not revoke original initialization authority", err)
	}
	f.gone(t, backend)
	owner = f.connect(t)
	for _, table := range authorityEmptyTables {
		var exists bool
		if owner.QueryRow(context.Background(), "SELECT EXISTS(SELECT 1 FROM ONLY "+pgx.Identifier{f.schema, table}.Sanitize()+")").Scan(&exists) != nil || exists {
			t.Fatal("resource failure left a committed initialization output")
		}
	}
	if g.Close(nil) == nil {
		t.Fatal("abandoned resource owner lost its terminal failure")
	}
}

func TestPeriodicPrefixMaintenancePostgresInstallationSelectionConsumesOnce(t *testing.T) {
	f := newPeriodicPrefixSnapshotFixture(t, nil)
	for _, prefixBegin := range []bool{false, true} {
		name := "legacy_begin_prefix_verify"
		if prefixBegin {
			name = "prefix_begin_legacy_verify"
		}
		t.Run(name, func(t *testing.T) {
			g, _, backend := f.acquire(t, "")
			var snapshot *Snapshot
			var err error
			if prefixBegin {
				snapshot, err = g.BeginPeriodicPrefixPlan(context.Background())
			} else {
				snapshot, err = g.BeginPlan(context.Background())
			}
			if err != nil {
				t.Fatal("original selected begin")
			}
			var proof *fundinglotcatalog.InstallationProof
			if prefixBegin {
				proof, err = snapshot.VerifyInstallation(context.Background())
			} else {
				proof, err = snapshot.VerifyPeriodicPrefixInstallation(context.Background())
			}
			if err != ErrSchema || proof != nil || !snapshot.state.catalogUsed || snapshot.state.installation != nil || snapshot.Outcome() != OutcomeAbandoned {
				t.Fatal("installation profile changed after original begin")
			}
			if proof, err = snapshot.VerifyPeriodicPrefixInstallation(context.Background()); proof != nil || err == nil {
				t.Fatal("failed installation reused original once with another profile")
			}
			if p, err := Attest(context.Background(), snapshot); p != nil || err == nil {
				t.Fatal("failed profile selection issued authority")
			}
			f.gone(t, backend)
		})
	}
}
