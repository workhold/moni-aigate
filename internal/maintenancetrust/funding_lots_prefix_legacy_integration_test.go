package maintenancetrust

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotproof"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

// These fixtures deliberately issue a genuine historical LegacyV1 epoch before
// the candidate layout exists. The unchanged original factory currently applies
// current migrations; exact original22 is therefore checked before issuance and
// again before candidate DDL. This is an isolated compatibility test, not a
// production migration, an expected-file issuer, or supplier/payment acceptance.
type periodicPrefixLegacyFixture struct {
	fixture  *snapshotFixture
	history  runtimeProcurementHistory
	key      string
	plan     FundingLotsPlanResult
	original *fundinglotproof.CanonicalV2
	witness  string
}

func periodicPrefixLegacyWitness(t *testing.T, ctx context.Context, f *snapshotFixture, owner *pgx.Conn) ([]byte, string, string) {
	t.Helper()
	var raw []byte
	var digest, witness string
	epoch := pgx.Identifier{f.schema, "funding_lot_epoch"}.Sanitize()
	attestation := pgx.Identifier{f.schema, "funding_lot_runtime_attestations"}.Sanitize()
	// Include every immutable epoch/attestation field, not just its canonical
	// payload. The compatibility upgrade must neither replace nor re-sign them.
	sql := `SELECT a.proof_canonical,a.proof_sha256,
 encode(sha256(convert_to(jsonb_build_array(to_jsonb(e),to_jsonb(a))::text,'UTF8')),'hex')
 FROM ` + epoch + ` e CROSS JOIN ` + attestation + ` a`
	if owner.QueryRow(ctx, sql).Scan(&raw, &digest, &witness) != nil || len(raw) == 0 || len(digest) != 64 || len(witness) != 64 {
		t.Fatal("legacy prefix original complete epoch/attestation witness")
	}
	return raw, digest, witness
}

func newPeriodicPrefixLegacyFixture(t *testing.T, ctx context.Context) periodicPrefixLegacyFixture {
	t.Helper()
	var h runtimeProcurementHistory
	key := uuid.NewString()
	f := newSnapshotFixturePrepared(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
		h = runtimePrepareActiveProcurement(t, pool)
		// Prepare a source-free key before historical issuance. Configuring the
		// whole tenant after its terminal request would correctly retain the
		// original LegacyMembershipUnproven gap; prefix evidence cannot remove
		// that separate historical membership boundary.
		tag, err := pool.Exec(ctx, `INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit)
 SELECT $1,k.tenant_id,k.owner_id,'legacy prefix unused key',$1,$1,k.scopes,1000
 FROM api_keys k JOIN request_records r ON r.key_id=k.id AND r.tenant_id=k.tenant_id
 WHERE r.id=$2 AND k.tenant_id=$3`, key, h.request, h.tenant)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatal("legacy prefix pre-epoch source-free key fixture")
		}
	})
	owner := f.connect(t)
	check, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("legacy prefix original22 pre-issuance transaction")
	}
	if migrations.VerifyOriginal22SnapshotTx(ctx, check, f.schema) != nil {
		t.Fatal("legacy prefix historical issuance requires exact original22")
	}
	var zeroPB bool
	if check.QueryRow(ctx, `SELECT (SELECT count(*) FROM periodic_budget_controls)=0
 AND (SELECT count(*) FROM periodic_budget_operations)=0
 AND (SELECT count(*) FROM periodic_budget_allocations)=0
 AND (SELECT count(*) FROM periodic_budget_windows)=0`).Scan(&zeroPB) != nil || !zeroPB || check.Rollback(ctx) != nil {
		t.Fatal("legacy prefix original nonempty business must have no fabricated PB history")
	}
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	if plan.Metadata.TenantCount != 1 || plan.Metadata.ReceiptCount != 1 || plan.Metadata.AvailableTotal != "988" || plan.Metadata.HeldTotal != "0" || plan.Expected.DescriptorDigest != fundingLotsBasisDescriptor {
		t.Fatal("legacy prefix independent old109 opening: real 1000 credit less 12 retail charge")
	}
	fundingLotsInitOrdinary(t, f, plan.Expected)
	original := runtimeFixtureExpected(t, owner, f.schema, plan.Expected)
	b, err := original.Bindings()
	if err != nil || b.Mode != "ordinary" || b.BasisDescriptorDigest != fundingLotsBasisDescriptor || b.BasisDigest != plan.Expected.BasisDigest || b.ManifestDigest != authorityManifestSHA {
		t.Fatal("legacy prefix expected must be the actual historical109 protected original")
	}
	check, err = owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("legacy prefix original catalog/basis transaction")
	}
	catalog, err := fundinglotcatalog.VerifySnapshotTx(ctx, check, f.pins)
	if err != nil || catalog == nil || catalog.Profile() != fundinglotcatalog.LegacyV1 ||
		catalog.ExpectedSHA256() != b.CatalogExpectedDigest || catalog.SchemaSHA256() != b.CatalogSchemaDigest || catalog.RolesSHA256() != b.CatalogRolesDigest {
		t.Fatal("legacy prefix historical tuple was not issued under the actual old catalog")
	}
	basis, err := fundinglotbasis.Read(ctx, fundingLotsBasisObserver{fundingLotsObserver{ctx, check}}, f.schema)
	if err != nil || basis.Summary().ScannedTables != 109 || basis.Summary().DescriptorSHA256 != fundingLotsBasisDescriptor || basis.Summary().BasisSHA256 != plan.Expected.BasisDigest || check.Rollback(ctx) != nil {
		t.Fatal("legacy prefix old109 basis differs before any operating upgrade")
	}
	raw, digest, witness := periodicPrefixLegacyWitness(t, ctx, f, owner)
	if !bytes.Equal(raw, original.Bytes()) || digest != original.SHA256() || owner.Close(ctx) != nil {
		t.Fatal("legacy prefix original capture/owner cleanup")
	}
	return periodicPrefixLegacyFixture{fixture: f, history: h, key: key, plan: plan, original: original, witness: witness}
}

func periodicPrefixLegacyInstall(t *testing.T, ctx context.Context, f *snapshotFixture) {
	t.Helper()
	owner := f.connect(t)
	defer func() { runtimeFixtureCleanup(t, owner, nil) }()
	tx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite})
	if err != nil {
		t.Fatal("legacy prefix offline owner candidate transaction")
	}
	defer func() { runtimeFixtureCleanup(t, owner, tx) }()
	if migrations.VerifyOriginal22SnapshotTx(ctx, tx, f.schema) != nil {
		t.Fatal("legacy prefix candidate requires original22 in the same owner RR")
	}
	ddl, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal("legacy prefix fixed candidate DDL material")
	}
	permissions, err := periodicbudgetprefixschema.Permissions(f.app, f.projector)
	if err != nil {
		t.Fatal("legacy prefix fixed candidate ACL material")
	}
	if _, err = tx.Exec(ctx, ddl); err != nil {
		t.Fatal("legacy prefix candidate installation after original epoch", err)
	}
	if _, err = tx.Exec(ctx, permissions); err != nil {
		t.Fatal("legacy prefix exact new APP permissions")
	}
	raw := snapshotFixedSource(t, snapshotRepositoryRoot(t), "specs/022-self-service-payments/c0-runtime-read-permissions.sql", runtimeProfileReadPermissionsSHA)
	delta := strings.NewReplacer("{{schema}}", pgx.Identifier{f.schema}.Sanitize(), "{{app}}", pgx.Identifier{f.app}.Sanitize()).Replace(string(raw))
	if strings.Contains(delta, "{{") || strings.Contains(delta, "}}") {
		t.Fatal("legacy prefix unresolved exact wallet4 material")
	}
	if _, err = tx.Exec(ctx, delta); err != nil || tx.Commit(ctx) != nil {
		t.Fatal("legacy prefix candidate/APP wallet4 atomic installation")
	}
}

func periodicPrefixLegacyAPP(t *testing.T, ctx context.Context, f *snapshotFixture) (*pgxpool.Pool, observedBackend) {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal("legacy prefix actual APP pool configuration")
	}
	cfg.ConnConfig = runtimeOrdinaryConfig(f, f.app)
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	cfg.MinConns, cfg.MaxConns = 0, 1
	app, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("legacy prefix actual APP business pool")
	}
	t.Cleanup(app.Close)
	var backend observedBackend
	var actual bool
	if app.QueryRow(ctx, `SELECT current_user=session_user AND current_user=$1::name,
 pg_backend_pid(),backend_start FROM pg_catalog.pg_stat_activity WHERE pid=pg_backend_pid()`, f.app).Scan(&actual, &backend.pid, &backend.start) != nil || !actual {
		t.Fatal("legacy prefix business must run as actual APP LOGIN")
	}
	return app, backend
}

func periodicPrefixLegacyConfigure(t *testing.T, ctx context.Context, f *snapshotFixture, h runtimeProcurementHistory, key string, withPrefix bool) {
	t.Helper()
	app, backend := periodicPrefixLegacyAPP(t, ctx, f)
	defer func() { app.Close(); f.gone(t, backend) }()
	var actor string
	if key == "" {
		if app.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='admin' ORDER BY id LIMIT 1`).Scan(&actor) != nil {
			t.Fatal("legacy prefix original administrator identity")
		}
	} else if app.QueryRow(ctx, `SELECT owner_id FROM api_keys WHERE id=$1 AND tenant_id=$2`, key, h.tenant).Scan(&actor) != nil {
		t.Fatal("legacy prefix original key owner identity")
	}
	commander, err := periodicbudget.NewCommander(app)
	if err != nil {
		t.Fatal("legacy prefix actual APP Commander")
	}
	in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000000"}
	configure := func(in periodicbudget.PutPeriodicBudget) (periodicbudget.ConfigureResult, error) {
		if key == "" {
			return commander.ConfigurePlatformTenant(ctx, actor, h.tenant, in)
		}
		return commander.ConfigureKey(ctx, actor, h.tenant, key, in)
	}
	first, err := configure(in)
	if err != nil || !first.Created || first.Receipt.Operation.Revision != "1" {
		t.Fatal("legacy prefix real first configuration", err)
	}
	var imports int
	if app.QueryRow(ctx, `SELECT count(*) FROM periodic_budget_allocations WHERE control_id=$1`, first.Receipt.Operation.ResourceID).Scan(&imports) != nil || imports != 0 {
		t.Fatal("legacy prefix terminal old reservation must not become an unproven legacy import")
	}
	if !withPrefix {
		return // A real pre-candidate control must remain without an origin.
	}
	var window string
	if app.QueryRow(ctx, `SELECT pb_observe($1,pb_now())`, first.Receipt.Operation.ResourceID).Scan(&window) != nil || window == "" {
		t.Fatal("legacy prefix real subsequent same-cut empty window")
	}
	in.OperationID, in.ExpectedRevision = uuid.NewString(), "1"
	noop, err := configure(in)
	if err != nil || noop.Created || noop.Receipt.Operation.Revision != "1" {
		t.Fatal("legacy prefix real later no-op command", err)
	}
}

func periodicPrefixLegacyCurrentBasis(t *testing.T, ctx context.Context, f *snapshotFixture, original *fundinglotproof.CanonicalV2, requireComplete bool) fundinglotbasis.Report {
	t.Helper()
	owner := f.connect(t)
	defer func() { runtimeFixtureCleanup(t, owner, nil) }()
	tx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("legacy prefix current V3 catalog/basis transaction")
	}
	defer func() { runtimeFixtureCleanup(t, owner, tx) }()
	catalog, err := fundinglotcatalog.VerifyPeriodicPrefixSnapshotTx(ctx, tx, f.pins)
	b, bindingErr := original.Bindings()
	if err != nil || bindingErr != nil || catalog == nil || catalog.Profile() != fundinglotcatalog.PeriodicPrefixV3 || catalog.ExpectedSHA256() == b.CatalogExpectedDigest || catalog.SchemaSHA256() == b.CatalogSchemaDigest || catalog.RolesSHA256() == b.CatalogRolesDigest {
		t.Fatal("legacy prefix actual current V3 must differ from immutable historical tuple")
	}
	if requireComplete {
		// Diagnose source qualification independently before the protected
		// observer. A valid tuple mapping cannot conceal a legacy source gap.
		wallet, readErr := fundinglotscan.ReadWalletPrefix(ctx, fundingLotsObserver{ctx, tx}, f.schema, fundinglotscan.ModeOrdinary)
		summary := wallet.Summary()
		if readErr != nil || summary.Status != "consistent" || !summary.CoverageComplete || summary.Periodic.Unproven.LegacyMembershipUnproven != 0 || summary.Periodic.Unproven.LegacyEffectBoundaryUnproven != 0 {
			t.Fatalf("legacy prefix source-free key complete87 preflight: error=%v report=%+v", readErr, summary)
		}
		rows, projectErr := fundinglotscan.ProjectEpochProofPrefix(ctx, wallet)
		manifest, manifestErr := migrations.Original22Manifest()
		observed, proofErr := fundingLotsObservedProof(rows, f.pins, catalog, manifest, fundinglotcatalog.Ordinary, nil, "")
		if projectErr != nil || manifestErr != nil || proofErr != nil || observed.DescriptorDigest != b.BasisDescriptorDigest || observed.BasisDigest != b.BasisDigest || observed.RuntimeProofDigest != original.SHA256() {
			t.Fatal("legacy prefix independently authenticated current87/history109 originals", projectErr, manifestErr, proofErr)
		}
	}
	facts, err := fundinglotbasis.ReadPrefix(ctx, fundingLotsBasisObserver{fundingLotsObserver{ctx, tx}}, f.schema)
	r := facts.Summary()
	if err != nil || !r.PhysicalEOFComplete || r.ScannedTables != 112 || r.ScannedColumns != 1004 || r.DescriptorSHA256 != fundinglotbasis.PeriodicPrefixDescriptorSHA256 || tx.Rollback(ctx) != nil {
		t.Fatal("legacy prefix current physical112 basis incomplete")
	}
	return r
}

func periodicPrefixLegacyAssertOriginal(t *testing.T, ctx context.Context, prepared periodicPrefixLegacyFixture) {
	t.Helper()
	f := prepared.fixture
	owner := f.connect(t)
	defer func() { runtimeFixtureCleanup(t, owner, nil) }()
	raw, digest, witness := periodicPrefixLegacyWitness(t, ctx, f, owner)
	if !bytes.Equal(raw, prepared.original.Bytes()) || digest != prepared.original.SHA256() || witness != prepared.witness || digest != prepared.plan.Expected.RuntimeProofDigest {
		t.Fatal("legacy prefix changed, replaced, or re-signed historical epoch/proof")
	}
}

func periodicPrefixLegacyConnectionBackend(t *testing.T, ctx context.Context, f *snapshotFixture, conn *pgx.Conn) observedBackend {
	t.Helper()
	backend := observedBackend{pid: conn.PgConn().PID()}
	if conn.Config().User != f.app || f.admin.QueryRow(ctx, `SELECT backend_start FROM pg_catalog.pg_stat_activity
 WHERE pid=$1 AND datid=$2 AND usename=$3`, backend.pid, f.pins.Database.OID, f.app).Scan(&backend.start) != nil {
		t.Fatal("legacy prefix actual APP backend identity witness")
	}
	return backend
}

func TestPeriodicPrefixMaintenancePostgresLegacyEpochCompatibility(t *testing.T) {
	for _, missingOrigin := range []bool{false, true} {
		name := "original109_epoch_current_v3_with_new_observations"
		if missingOrigin {
			name = "real_preexisting_control_without_origin_remains_rejected"
		}
		t.Run(name, func(t *testing.T) {
			// This outer budget covers independent old issuance, offline install,
			// operating commands and fresh checks; product budgets stay unchanged.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			prepared := newPeriodicPrefixLegacyFixture(t, ctx)
			f := prepared.fixture
			if missingOrigin {
				periodicPrefixLegacyConfigure(t, ctx, f, prepared.history, "", false)
			}
			periodicPrefixLegacyInstall(t, ctx, f)
			before := periodicPrefixLegacyCurrentBasis(t, ctx, f, prepared.original, false)
			if !missingOrigin {
				periodicPrefixLegacyConfigure(t, ctx, f, prepared.history, prepared.key, true)
				after := periodicPrefixLegacyCurrentBasis(t, ctx, f, prepared.original, true)
				if after.DescriptorSHA256 != before.DescriptorSHA256 || after.BasisSHA256 == before.BasisSHA256 || after.BasisSHA256 == prepared.plan.Expected.BasisDigest || after.SourceRows <= before.SourceRows {
					t.Fatal("legacy prefix must accept changed operating basis under the same V3 descriptor")
				}
			}
			periodicPrefixLegacyAssertOriginal(t, ctx, prepared)

			// Old composer stays WalletReadV2 and must reject the new full layout;
			// historical compatibility is reachable only through the explicit V3 API.
			conn, old := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			oldBackend := periodicPrefixLegacyConnectionBackend(t, ctx, f, conn)
			if proof, err := fundinglotruntime.VerifyOrdinaryApplicationTx(ctx, old, f.pins, prepared.original); proof != nil || err != fundinglotruntime.ErrRuntime {
				t.Fatal("legacy composer silently upgraded to current V3")
			}
			runtimeAssertReadOnly(t, old, false)
			runtimeFixtureCleanup(t, conn, old.Tx)
			f.gone(t, oldBackend)

			// All APP connections are closed before this new stop-the-world owner.
			gate, wire, backend := f.acquire(t, "")
			observed, err := gate.ObservePeriodicPrefixFundingLots(ctx, prepared.plan.Expected)
			if missingOrigin {
				if observed != (FundingLotsObservation{}) || err != ErrSchema {
					t.Fatal("V3 observer authenticated genuine old control without origin", err)
				}
				assertObservationReadOnly(t, wire, false)
			} else {
				if err != nil || observed.Status != FundingLotsObservationMatched || observed.Observed != prepared.plan.Expected || observed.SourceRows == 0 || gate.Close(nil) != nil {
					t.Fatal("V3 observer rejected actual unchanged historical109 epoch", err)
				}
				assertObservationReadOnly(t, wire, true)
			}
			f.gone(t, backend)
			if again, e := gate.ObservePeriodicPrefixFundingLots(ctx, prepared.plan.Expected); again != (FundingLotsObservation{}) || e != ErrTransaction {
				t.Fatal("legacy prefix observer reused its original begin opportunity")
			}

			conn, app := runtimeActualTx(t, runtimeOrdinaryConfig(f, f.app), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			appBackend := periodicPrefixLegacyConnectionBackend(t, ctx, f, conn)
			runtimeAssertActiveProcurement(t, app, f.schema, prepared.history)
			catalog, err := fundinglotcatalog.VerifyPeriodicPrefixRuntimeTx(ctx, app, f.pins, fundinglotcatalog.Application)
			if err != nil || catalog == nil || catalog.Profile() != fundinglotcatalog.PeriodicPrefixV3 || catalog.VerifyTx(ctx, app) != nil {
				t.Fatal("legacy prefix actual APP current V3 catalog preflight", err)
			}
			facts, err := fundinglotscan.ReadWalletPrefix(ctx, fundingLotsObserver{ctx, app}, f.schema, fundinglotscan.ModeOrdinary)
			r := facts.Summary()
			if err != nil || !r.PhysicalEOFComplete || r.ScannedTables != 87 || r.ScannedColumns != 804 || !r.Wallet.RowsConsistent || r.PeriodicPrefix.Base != r.Periodic {
				t.Fatal("legacy prefix APP full nonempty source/wallet/prefix preflight", err)
			}
			if missingOrigin {
				if r.Status != "indeterminate" || r.CoverageComplete || r.PeriodicPrefix.Balanced || r.PeriodicPrefix.CoverageComplete || r.PeriodicPrefix.Unproven.UncoveredControls != 1 || r.PeriodicPrefix.Scanned.Origins != 0 || r.PeriodicPrefix.Scanned.Observations != 0 || r.PeriodicPrefix.Mismatches != (periodicbudgetprefix.Mismatches{}) {
					t.Fatalf("legacy prefix missing origin was backfilled or concealed: %+v", r.PeriodicPrefix)
				}
				if proof, err := fundinglotruntime.VerifyOrdinaryPeriodicPrefixApplicationTx(ctx, app, f.pins, prepared.original); proof != nil || err != fundinglotruntime.ErrRuntime {
					t.Fatal("V3 APP composer authenticated real missing-origin history")
				}
			} else {
				if r.Status != "consistent" || !r.CoverageComplete || r.Periodic.Unproven.OperationSnapshotPrefixUnproven != 2 || !r.PeriodicPrefix.Balanced || r.PeriodicPrefix.CertifiedCommands != 2 || r.PeriodicPrefix.Scanned.Origins != 1 || r.PeriodicPrefix.Scanned.Observations != 3 {
					t.Fatalf("legacy prefix authenticated new observations lost old Base: %+v", r)
				}
				proof, err := fundinglotruntime.VerifyOrdinaryPeriodicPrefixApplicationTx(ctx, app, f.pins, prepared.original)
				if err != nil || proof == nil || proof.VerifyTx(ctx, app) != nil {
					t.Fatal("actual APP V3 composer rejected genuine historical109 original", err)
				}
				copyProof := *proof
				if copyProof.VerifyTx(ctx, app) != nil || proof.VerifyTx(ctx, &runtimeRecordingTx{Tx: app.Tx}) != fundinglotruntime.ErrRuntime {
					t.Fatal("legacy prefix RuntimeProof changed original wrapper binding")
				}
				if app.Tx.Rollback(ctx) != nil || proof.VerifyTx(ctx, app) != fundinglotruntime.ErrRuntime || copyProof.VerifyTx(ctx, app) != fundinglotruntime.ErrRuntime || conn.PgConn().IsBusy() || conn.PgConn().TxStatus() != 'I' {
					t.Fatal("legacy prefix RuntimeProof survived caller rollback or connection remained busy")
				}
			}
			runtimeAssertReadOnly(t, app, false)
			tables := map[string]bool{}
			for _, sql := range app.sql {
				if strings.HasPrefix(sql, "WITH page AS MATERIALIZED") {
					parts := strings.SplitN(sql, " FROM ONLY ", 2)
					if len(parts) == 2 {
						tables[strings.SplitN(parts[1], " AS s", 2)[0]] = true
					}
				}
			}
			if len(tables) != 87 {
				t.Fatal("legacy prefix APP did not physically enumerate all 87 tables")
			}
			runtimeFixtureCleanup(t, conn, app.Tx)
			f.gone(t, appBackend)
			periodicPrefixLegacyAssertOriginal(t, ctx, prepared)
		})
	}
}
