package maintenancetrust

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/fundinglotbasis"
	"github.com/moni-ai/moni-aigate/internal/fundinglotcatalog"
	"github.com/moni-ai/moni-aigate/internal/fundinglotruntime"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
	"github.com/moni-ai/moni-aigate/migrations"
)

// The original genuine QA factory is deliberately reused unchanged: immutable
// v1/local manifests, actual APP/worker/peer LOGINs and its real dual-approved
// manual credit remain the business source. The factory currently applies the
// current migration set. This candidate-only test explicitly rejects any drift
// from exact original22 before installing its DDL; it is not a future-compatible
// historical installer or formal production migration/operations acceptance.
func periodicPrefixPrepareSandbox(t *testing.T, ctx context.Context, f *authoritySandboxFixture) {
	t.Helper()
	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("prefix sandbox genuine offline owner pool")
	}
	f.pools = append(f.pools, owner)
	backend := f.observePool(t, ctx, owner, f.env.Roles["app_owner"], "moni_test")
	defer func() { owner.Close(); f.gone(t, backend) }()
	tx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite})
	if err != nil {
		t.Fatal("prefix sandbox owner installation RR")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if migrations.VerifyOriginal22SnapshotTx(ctx, tx, f.pins.AppSchema.Name) != nil {
		t.Fatal("prefix sandbox requires exact original22 before any candidate DDL")
	}
	ddl, err := periodicbudgetprefixschema.SQL()
	if err != nil {
		t.Fatal("prefix sandbox frozen candidate material")
	}
	acl, err := periodicbudgetprefixschema.Permissions(f.pins.App.Name, f.pins.Projector.Name)
	if err != nil {
		t.Fatal("prefix sandbox frozen candidate permissions")
	}
	if _, err = tx.Exec(ctx, ddl); err != nil {
		t.Fatal("prefix sandbox isolated candidate DDL")
	}
	if _, err = tx.Exec(ctx, acl); err != nil {
		t.Fatal("prefix sandbox isolated exact APP delta")
	}
	raw := snapshotFixedSource(t, snapshotRepositoryRoot(t), "specs/022-self-service-payments/c0-runtime-read-permissions.sql", runtimeProfileReadPermissionsSHA)
	delta := strings.NewReplacer("{{schema}}", pgx.Identifier{f.pins.AppSchema.Name}.Sanitize(), "{{app}}", pgx.Identifier{f.pins.App.Name}.Sanitize()).Replace(string(raw))
	if strings.Contains(delta, "{{") || strings.Contains(delta, "}}") {
		t.Fatal("prefix sandbox unresolved four-column read grant")
	}
	if _, err = tx.Exec(ctx, delta); err != nil || tx.Commit(ctx) != nil {
		t.Fatal("prefix sandbox exact wallet4 grant and candidate commit")
	}
}

func periodicPrefixSandboxAcquire(t *testing.T, ctx context.Context, f *authoritySandboxFixture) (*Gate, *gateWire, observedBackend) {
	t.Helper()
	cfg := f.cfg.Copy()
	dial := cfg.DialFunc
	ready := make(chan *gateWire, 1)
	cfg.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		wire := newGateWire(conn)
		ready <- wire
		return wire, nil
	}
	gate, err := Acquire(ctx, cfg, f.pins)
	if err != nil || gate == nil {
		t.Fatal("prefix sandbox independent maintenance Gate", err)
	}
	f.gates = append(f.gates, gate)
	backend := observedBackend{pid: gate.state.pid, start: gate.state.backendStart}
	f.backends = append(f.backends, backend)
	var actual bool
	if f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE pid=$1 AND backend_start=$2 AND datid=$3 AND usename='moni_test')`, backend.pid, backend.start, f.pins.Database.OID).Scan(&actual) != nil || !actual || backend.pid == f.admin.PgConn().PID() {
		t.Fatal("prefix sandbox actual dedicated maintenance backend")
	}
	return gate, awaitWire(t, ready), backend
}

func periodicPrefixSandboxInstalled(t *testing.T, ctx context.Context, f *authoritySandboxFixture, initialize bool) (*Gate, *Snapshot, *gateWire, observedBackend) {
	t.Helper()
	gate, wire, backend := periodicPrefixSandboxAcquire(t, ctx, f)
	var snapshot *Snapshot
	var err error
	if initialize {
		snapshot, err = gate.BeginPeriodicPrefixInit(ctx)
	} else {
		snapshot, err = gate.BeginPeriodicPrefixPlan(ctx)
	}
	if err != nil || snapshot == nil || snapshot.Outcome() != OutcomeOpen {
		t.Fatal("prefix sandbox original V3 begin", err)
	}
	proof, err := snapshot.VerifyPeriodicPrefixInstallation(ctx)
	if err != nil || proof == nil || proof.Catalog() == nil || proof.Catalog().Profile() != fundinglotcatalog.PeriodicPrefixV3 {
		t.Fatal("prefix sandbox owned V3 complete installation", err)
	}
	*proof = fundinglotcatalog.InstallationProof{}
	return gate, snapshot, wire, backend
}

func periodicPrefixSandboxPhase(t *testing.T, snapshot *Snapshot, wire *gateWire, initialize bool) {
	t.Helper()
	phase := snapshot.state.fundingLots
	if phase == nil || phase.stage != fundingLotsDone || !phase.released || !phase.readyConsumed || snapshot.Outcome() != OutcomeCommitted || snapshot.state.profile != fundinglotcatalog.PeriodicPrefixV3 {
		t.Fatal("prefix sandbox phase did not reach unique terminal commit")
	}
	source := phase.prefixFacts.Summary()
	if source.Scope != "complete_business_source_prefix_graphs_v1" || source.ScannedTables != 71 || source.ScannedColumns != 635 || source.Status != "consistent" || !source.CoverageComplete || source.Periodic.Unproven.OperationSnapshotPrefixUnproven != 2 || source.PeriodicPrefix.Base != source.Periodic || !source.PeriodicPrefix.Balanced || source.PeriodicPrefix.CertifiedCommands != 2 {
		t.Fatalf("prefix sandbox complete pre71 and old PB gap: %+v", source)
	}
	if phase.basis.Scope != "fixed_112_table_prefix_basis_v1" || phase.basis.ScannedTables != 112 || phase.basis.ScannedColumns != 1004 || !phase.basis.PhysicalEOFComplete || phase.basis.DescriptorSHA256 != fundinglotbasis.PeriodicPrefixDescriptorSHA256 {
		t.Fatal("prefix sandbox full selected basis missing")
	}
	if initialize {
		post := phase.prefixPost.Summary()
		if post.Status != "consistent" || !post.CoverageComplete || post.ScannedTables != 87 || post.ScannedColumns != 804 || !post.Wallet.RowsConsistent || post.Periodic != source.Periodic || post.PeriodicPrefix != source.PeriodicPrefix {
			t.Fatal("prefix sandbox post87/wallet/unchanged PB proof incomplete")
		}
	}
	queries, _ := wire.recorded()
	commits, locks := 0, 0
	firstRead, lockAt := -1, -1
	began := false
	for i, sql := range queries {
		upper := strings.ToUpper(strings.TrimSpace(sql))
		if strings.HasPrefix(upper, "BEGIN ") {
			began = true
		}
		if upper == "COMMIT" {
			commits++
		}
		if strings.HasPrefix(upper, "LOCK TABLE ") {
			locks++
			lockAt = i
			if !initialize || sql != periodicPrefixInitLockSQL(snapshot.state.gate.pins.AppSchema.Name) {
				t.Fatal("prefix sandbox init lock set changed")
			}
		}
		if began && firstRead == -1 && (strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")) {
			firstRead = i
		}
	}
	if commits != 1 || initialize && (locks != 1 || firstRead <= lockAt) || !initialize && locks != 0 {
		t.Fatal("prefix sandbox unique commit or lock-before-first-read ordering")
	}
}

func periodicPrefixSandboxPB(t *testing.T, ctx context.Context, f *authoritySandboxFixture) {
	t.Helper()
	app, backend := f.runtime(t, "app", "app_runtime")
	defer func() { app.Close(); f.gone(t, backend) }()
	var actor, tenant string
	if app.QueryRow(ctx, `SELECT id FROM users WHERE platform_role='admin' ORDER BY id LIMIT 1`).Scan(&actor) != nil || app.QueryRow(ctx, `SELECT tenant_id FROM funding_receipts WHERE source_kind='manual_credit' AND amount=1000 ORDER BY id LIMIT 1`).Scan(&tenant) != nil {
		t.Fatal("prefix sandbox genuine funded tenant/admin originals")
	}
	var requests, allocations int
	if app.QueryRow(ctx, `SELECT (SELECT count(*) FROM reservations),(SELECT count(*) FROM periodic_budget_allocations)`).Scan(&requests, &allocations) != nil || requests != 0 || allocations != 0 {
		t.Fatal("prefix sandbox first configuration must precede reservations/imports")
	}
	commander, err := periodicbudget.NewCommander(app)
	if err != nil {
		t.Fatal("prefix sandbox actual APP commander")
	}
	in := periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000000"}
	first, err := commander.ConfigurePlatformTenant(ctx, actor, tenant, in)
	if err != nil || !first.Created {
		t.Fatal("prefix sandbox actual APP first configuration", err)
	}
	var window string
	if app.QueryRow(ctx, `SELECT pb_observe($1,pb_now())`, first.Receipt.Operation.ResourceID).Scan(&window) != nil || window == "" {
		t.Fatal("prefix sandbox actual APP later same-cut empty window")
	}
	in.OperationID, in.ExpectedRevision = uuid.NewString(), "1"
	noop, err := commander.ConfigurePlatformTenant(ctx, actor, tenant, in)
	if err != nil || noop.Created || noop.Receipt.Operation.Revision != "1" {
		t.Fatal("prefix sandbox actual APP no-op receipt", err)
	}
	tx, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("prefix sandbox source preflight actual APP RR RO")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	facts, err := fundinglotscan.ReadPrefix(ctx, fundingLotsObserver{ctx, tx}, f.pins.AppSchema.Name, fundinglotscan.ModeSandbox)
	r := facts.Summary()
	if err != nil || r.Status != "consistent" || !r.CoverageComplete || r.Periodic.Status != "indeterminate" || r.Periodic.Unproven.OperationSnapshotPrefixUnproven != 2 || r.PeriodicPrefix.Base != r.Periodic || !r.PeriodicPrefix.Balanced || r.PeriodicPrefix.CertifiedCommands != 2 || r.PeriodicPrefix.Scanned.Observations != 3 {
		t.Fatalf("prefix sandbox real PB source preflight: %v / %+v", err, r)
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("prefix sandbox source preflight caller rollback")
	}
}

func periodicPrefixSandboxWitness(t *testing.T, ctx context.Context, f *authoritySandboxFixture) ([]byte, string, string) {
	t.Helper()
	schema := f.pins.AppSchema.Name
	var raw []byte
	var digest, prefix string
	if f.admin.QueryRow(ctx, "SELECT proof_canonical,proof_sha256 FROM "+pgx.Identifier{schema, "payment_runtime_attestations"}.Sanitize()).Scan(&raw, &digest) != nil {
		t.Fatal("prefix sandbox immutable v1 original witness")
	}
	q := func(table string) string { return pgx.Identifier{schema, table}.Sanitize() }
	sql := `SELECT encode(sha256(convert_to(jsonb_build_object(
 'origins',(SELECT jsonb_agg(to_jsonb(x) ORDER BY control_id) FROM ` + q("periodic_budget_observation_origins") + ` x),
 'heads',(SELECT jsonb_agg(to_jsonb(x) ORDER BY control_id) FROM ` + q("periodic_budget_observation_heads") + ` x),
 'journal',(SELECT jsonb_agg(to_jsonb(x) ORDER BY control_id,ordinal) FROM ` + q("periodic_budget_observation_journal") + ` x),
 'operations',(SELECT jsonb_agg(jsonb_build_array(operation_id,observation_id) ORDER BY operation_id) FROM ` + q("periodic_budget_operations") + `),
 'windows',(SELECT jsonb_agg(jsonb_build_array(id,observation_id,observation_created_xid) ORDER BY id) FROM ` + q("periodic_budget_windows") + `)
 )::text,'UTF8')),'hex')`
	if f.admin.QueryRow(ctx, sql).Scan(&prefix) != nil || len(raw) == 0 || len(digest) != 64 || len(prefix) != 64 {
		t.Fatal("prefix sandbox complete observation witness")
	}
	return raw, digest, prefix
}

func TestPeriodicPrefixMaintenancePostgresGenuineSandboxActualApplication(t *testing.T) {
	// Each protected operation retains its own internal absolute thirty-second
	// budget; the outer bound covers this sequence of independent full flows.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := newAuthoritySandboxFixture(t)
	periodicPrefixPrepareSandbox(t, ctx, f)
	periodicPrefixSandboxPB(t, ctx, f)
	f.runtimesGone(t)
	before, v1SHA, prefixBefore := periodicPrefixSandboxWitness(t, ctx, f)
	accounts, accountDigest := f.accountWitness(t)
	if accounts == 0 {
		t.Fatal("prefix sandbox requires original nonempty money accounts")
	}

	gate, snapshot, wire, backend := periodicPrefixSandboxInstalled(t, ctx, f, false)
	permit, err := Attest(ctx, snapshot)
	if err != nil || permit == nil {
		t.Fatal("prefix sandbox original Plan authority", err)
	}
	if permit.state.authority.predecessor == nil || permit.state.authority.predecessor.v1SHA256 != v1SHA || !bytes.Equal(permit.state.authority.predecessor.canonical, before) {
		t.Fatal("prefix sandbox Plan lost genuine immutable predecessor")
	}
	plan, err := permit.PlanFundingLots(ctx, uuid.NewString())
	if err != nil {
		t.Fatal("prefix sandbox protected full Plan", err)
	}
	periodicPrefixSandboxPhase(t, snapshot, wire, false)
	if plan.Metadata.TenantCount != 2 || plan.Metadata.ReceiptCount != 1 || plan.Metadata.AvailableTotal != "1000" || plan.Metadata.HeldTotal != "0" || plan.Expected.DescriptorDigest != fundinglotbasis.PeriodicPrefixDescriptorSHA256 || permit.Verify(ctx) == nil || gate.Close(nil) != nil {
		t.Fatal("prefix sandbox Plan nonempty independent funding arithmetic or authority lifetime")
	}
	f.gone(t, backend)

	gate, snapshot, wire, backend = periodicPrefixSandboxInstalled(t, ctx, f, true)
	permit, err = Attest(ctx, snapshot)
	if err != nil || permit == nil || permit.state.authority.accounts == nil || !permit.state.authority.accounts.complete || permit.state.authority.accounts.count != accounts || permit.state.authority.accounts.digest != accountDigest {
		t.Fatal("prefix sandbox fresh Init original complete account-lock authority", err)
	}
	result, err := permit.InitializeFundingLots(ctx, plan.Expected)
	if err != nil || result.RuntimeProofDigest != plan.Expected.RuntimeProofDigest || result.Metadata.TenantCount != plan.Metadata.TenantCount || result.Metadata.ReceiptCount != 1 || result.Metadata.AvailableTotal != "1000" {
		t.Fatal("prefix sandbox protected write/post/basis/catalog Init", err)
	}
	periodicPrefixSandboxPhase(t, snapshot, wire, true)
	if permit.Verify(ctx) == nil || gate.Close(nil) != nil {
		t.Fatal("prefix sandbox completed Init retained authority")
	}
	f.gone(t, backend)
	after, afterSHA, prefixAfter := periodicPrefixSandboxWitness(t, ctx, f)
	if !bytes.Equal(before, after) || v1SHA != afterSHA || prefixBefore != prefixAfter {
		t.Fatal("fixed C0 writer changed immutable predecessor or PB witness")
	}

	owner, err := f.env.OpenPool(ctx, "app_owner")
	if err != nil {
		t.Fatal("prefix sandbox offline expected-original reader")
	}
	f.pools = append(f.pools, owner)
	ownerBackend := f.observePool(t, ctx, owner, f.env.Roles["app_owner"], "moni_test")
	expected := runtimeProfileExpected(t, owner, f.pins, plan.Expected)
	bindings, err := expected.Bindings()
	if err != nil || bindings.Mode != "sandbox" || bindings.Sandbox == nil || bindings.PredecessorPaymentDigest == nil || *bindings.PredecessorPaymentDigest != v1SHA || bindings.PredecessorPaymentCanonicalBase64URL == nil {
		t.Fatal("prefix sandbox V2 full predecessor/profile binding")
	}
	embedded, err := base64.RawURLEncoding.DecodeString(*bindings.PredecessorPaymentCanonicalBase64URL)
	if err != nil || !bytes.Equal(embedded, before) {
		t.Fatal("prefix sandbox V2 changed exact predecessor bytes")
	}
	owner.Close()
	f.gone(t, ownerBackend)
	local, err := fundinglotruntime.LoadSandboxApplicationState(ctx, f.env.ApplicationManifestPath)
	if err != nil || local == nil {
		t.Fatal("prefix sandbox must load original local APP manifest/nonce material", err)
	}

	gate, wire, backend = periodicPrefixSandboxAcquire(t, ctx, f)
	observed, err := gate.ObservePeriodicPrefixFundingLots(ctx, plan.Expected)
	if err != nil || observed.Status != FundingLotsObservationMatched || observed.Observed != plan.Expected || observed.SourceRows == 0 || gate.Close(nil) != nil {
		t.Fatal("prefix sandbox fresh complete protected observer", err)
	}
	assertObservationReadOnly(t, wire, true)
	f.gone(t, backend)
	if again, e := gate.ObservePeriodicPrefixFundingLots(ctx, plan.Expected); again != (FundingLotsObservation{}) || e != ErrTransaction {
		t.Fatal("prefix sandbox observer reused unique Begin")
	}

	app, appBackend := f.runtime(t, "app", "app_runtime")
	tx, err := app.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal("prefix sandbox actual APP runtime original RR RO")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
		app.Close()
	}()
	recording := &runtimeRecordingTx{Tx: tx}
	var actual bool
	if recording.QueryRow(ctx, `SELECT current_user=session_user AND current_user=$1::name AND r.oid=$2::oid AND r.rolcanlogin AND NOT r.rolsuper AND current_setting('transaction_isolation')='repeatable read' AND current_setting('transaction_read_only')='on' FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, f.pins.App.Name, f.pins.App.OID).Scan(&actual) != nil || !actual {
		t.Fatal("prefix sandbox composer actual restricted APP LOGIN/transaction required")
	}
	facts, err := fundinglotscan.ReadWalletPrefix(ctx, fundingLotsObserver{ctx, recording}, f.pins.AppSchema.Name, fundinglotscan.ModeSandbox)
	report := facts.Summary()
	if err != nil || report.Status != "consistent" || !report.CoverageComplete || report.ScannedTables != 87 || report.ScannedColumns != 804 || !report.Wallet.RowsConsistent || report.Periodic.Unproven.OperationSnapshotPrefixUnproven != 2 || !report.PeriodicPrefix.Balanced || report.PeriodicPrefix.Base != report.Periodic {
		t.Fatal("prefix sandbox actual APP complete nonempty source/wallet/prefix", err)
	}
	proof, err := fundinglotruntime.VerifySandboxPeriodicPrefixApplicationTx(ctx, recording, f.pins, expected, local)
	if err != nil || proof == nil || proof.VerifyTx(ctx, recording) != nil {
		t.Fatal("prefix sandbox full actual APP V3 composer with local originals", err)
	}
	copyProof := *proof
	if copyProof.VerifyTx(ctx, recording) != nil || proof.VerifyTx(ctx, &runtimeRecordingTx{Tx: tx}) != fundinglotruntime.ErrRuntime {
		t.Fatal("prefix sandbox runtime proof wrapper identity binding")
	}
	for _, missing := range []*fundinglotruntime.ApplicationState{nil, {}} {
		rejected, e := fundinglotruntime.VerifySandboxPeriodicPrefixApplicationTx(ctx, recording, f.pins, expected, missing)
		if rejected != nil || e != fundinglotruntime.ErrRuntime {
			t.Fatal("prefix sandbox adopted DB originals without complete original local APP loading")
		}
	}
	runtimeAssertReadOnly(t, recording, false)
	tables := map[string]bool{}
	for _, sql := range recording.sql {
		if strings.HasPrefix(sql, "WITH page AS MATERIALIZED") {
			parts := strings.SplitN(sql, " FROM ONLY ", 2)
			if len(parts) == 2 {
				tables[strings.SplitN(parts[1], " AS s", 2)[0]] = true
			}
		}
	}
	if len(tables) != 87 {
		t.Fatal("prefix sandbox actual APP omitted physical tables")
	}
	if tx.Rollback(ctx) != nil || proof.VerifyTx(ctx, recording) != fundinglotruntime.ErrRuntime || copyProof.VerifyTx(ctx, recording) != fundinglotruntime.ErrRuntime {
		t.Fatal("prefix sandbox proof survived original APP tx end")
	}
	if tx.Conn().PgConn().IsBusy() || tx.Conn().PgConn().TxStatus() != 'I' {
		t.Fatal("prefix sandbox successful caller rollback did not establish idle connection")
	}
	app.Close()
	f.gone(t, appBackend)
	after, afterSHA, prefixAfter = periodicPrefixSandboxWitness(t, ctx, f)
	if !bytes.Equal(before, after) || v1SHA != afterSHA || prefixBefore != prefixAfter {
		t.Fatal("prefix sandbox observer/composer changed original predecessor or PB evidence")
	}
	if f.env.VerifyLegacyFundingUnchanged(ctx) != nil {
		t.Fatal("prefix sandbox complete flow changed genuine dual-approved legacy money")
	}
	f.runtimesGone(t)
}
