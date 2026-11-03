package fundinglotscan

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefix"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
)

// This suite reuses the dedicated 127.0.0.1:55439/moni_test original22 owner
// fixture and its real credited C0 history. SHA-authenticated candidate SQL is
// installed only in its exclusively owned random flscan schema. This is a
// test-only physical layout, not the pb_tx candidate installer, catalog/APP
// authority, production deployment, or a supplier/payment acceptance result.
// Root runs this suite serially with the other explicit PostgreSQL opt-ins.
type prefixPGQuery struct {
	ctx    context.Context
	tx     pgx.Tx
	schema string
	tables []tableData
	calls  map[string]int
	opened []*prefixPGRows
}
type prefixPGRows struct {
	rows                 pgx.Rows
	eof, checked, closed bool
}

func (q *prefixPGQuery) Query(sql string, args ...any) (Rows, error) {
	for _, table := range q.tables {
		if strings.Contains(sql, " FROM ONLY "+pgx.Identifier{q.schema, table.spec.name}.Sanitize()+" AS s ") {
			q.calls[table.spec.name]++
			rows, err := q.tx.Query(q.ctx, sql, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...)
			if err != nil {
				return nil, err
			}
			result := &prefixPGRows{rows: rows}
			q.opened = append(q.opened, result)
			return result, nil
		}
	}
	return nil, ErrExecution
}
func (r *prefixPGRows) Next() (bool, error) {
	next := r.rows.Next()
	if !next {
		r.eof = true
	}
	return next, nil
}
func (r *prefixPGRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *prefixPGRows) Err() error             { r.checked = true; return r.rows.Err() }
func (r *prefixPGRows) Close() error           { r.rows.Close(); r.closed = true; return nil }
func newPrefixPGQuery(t *testing.T, f *completeSourcePGFixture, tx pgx.Tx, wallet bool) *prefixPGQuery {
	t.Helper()
	tables, _, err := prefixRegistry(wallet)
	completeSourcePGMust(t, "build fixed prefix reader registry", err)
	return &prefixPGQuery{ctx: f.ctx, tx: tx, schema: f.schema, tables: tables, calls: map[string]int{}}
}
func (q *prefixPGQuery) assertEOF(t *testing.T, tables int) {
	t.Helper()
	if len(q.calls) != tables {
		t.Fatalf("real prefix read omitted physical tables: %d want%d", len(q.calls), tables)
	}
	for _, rows := range q.opened {
		if !rows.eof || !rows.checked || !rows.closed {
			t.Fatal("real prefix reader omitted EOF/Err/Close")
		}
	}
}
func prefixPGSource(t *testing.T, f *completeSourcePGFixture, tx pgx.Tx) (PrefixFacts, *prefixPGQuery) {
	t.Helper()
	q := newPrefixPGQuery(t, f, tx, false)
	facts, err := ReadPrefix(f.ctx, q, f.schema, ModeOrdinary)
	completeSourcePGMust(t, "read real 71-table prefix source union", err)
	q.assertEOF(t, 71)
	if facts.Summary().ScannedTables != 71 || facts.Summary().ScannedColumns != 635 {
		t.Fatal("real source prefix dimensions changed")
	}
	return facts, q
}
func prefixPGWallet(t *testing.T, f *completeSourcePGFixture, tx pgx.Tx, mode Mode) (PrefixWalletFacts, *prefixPGQuery) {
	t.Helper()
	q := newPrefixPGQuery(t, f, tx, true)
	facts, err := ReadWalletPrefix(f.ctx, q, f.schema, mode)
	completeSourcePGMust(t, "read real 87-table prefix source-wallet union", err)
	q.assertEOF(t, 87)
	if facts.Summary().ScannedTables != 87 || facts.Summary().ScannedColumns != 804 {
		t.Fatal("real source-wallet prefix dimensions changed")
	}
	return facts, q
}

func TestPostgresPrefixSourceWalletReader(t *testing.T) {
	f := newWalletPGFixture(t)
	sql, err := periodicbudgetprefixschema.SQL()
	completeSourcePGMust(t, "load frozen candidate material for isolated reader fixture", err)
	_, err = f.owner.Exec(f.ctx, sql)
	completeSourcePGMust(t, "install test-only prefix physical layout in isolated owner fixture", err)

	t.Run("funded_originals_and_three_no_query_projections", func(t *testing.T) {
		tx := f.begin(t, pgx.ReadOnly)
		source, sq := prefixPGSource(t, f.completeSourcePGFixture, tx)
		wallet, wq := prefixPGWallet(t, f.completeSourcePGFixture, tx, ModeOrdinary)
		sr, wr := source.Summary(), wallet.Summary()
		if sr.Status != "consistent" || !sr.CoverageComplete || wr.Status != "consistent" || !wr.CoverageComplete || sr.PeriodicPrefix.Base != sr.Periodic || wr.PeriodicPrefix.Base != wr.Periodic {
			t.Fatal("actual empty prefix and funded business graphs did not qualify")
		}
		for _, q := range []*prefixPGQuery{sq, wq} {
			for _, count := range q.calls {
				if count != 1 {
					t.Fatal("shared physical table read more than once")
				}
			}
		}
		seed, err := ProjectLegacySeedPrefix(f.ctx, source)
		completeSourcePGMust(t, "project actual prefix-qualified legacy source originals", err)
		if len(seed.Tenants) != 1 || seed.Tenants[0].ID != f.tenant || len(seed.Receipts) != 1 || seed.Receipts[0].Amount != "40" || len(seed.Accounts) == 0 || len(seed.Journals) == 0 || len(seed.Lines) == 0 {
			t.Fatal("actual nonempty seed source values lost")
		}
		epoch, err := ProjectEpochProofPrefix(f.ctx, wallet)
		completeSourcePGMust(t, "project actual prefix epoch originals", err)
		if epoch.Epoch.ID != f.epoch || epoch.Attestation.EpochID != f.epoch || !bytes.Equal(epoch.Attestation.ProofCanonical, []byte(`{"scope":"reader_fixture_only_not_runtime_proof"}`)) {
			t.Fatal("actual epoch original bytes changed")
		}
		app, err := ProjectApplicationPaymentProofPrefix(f.ctx, wallet)
		completeSourcePGMust(t, "project actual ordinary absence of APP originals", err)
		if app != (ApplicationPaymentProofRows{}) {
			t.Fatal("ordinary prefix fabricated APP originals")
		}
		epoch.Attestation.ProofCanonical[0] ^= 1
		again, err := ProjectEpochProofPrefix(f.ctx, wallet)
		completeSourcePGMust(t, "repeat actual prefix epoch deep copy", err)
		if bytes.Equal(epoch.Attestation.ProofCanonical, again.Attestation.ProofCanonical) || source.Summary() != sr || wallet.Summary() != wr || len(sq.calls) != 71 || len(wq.calls) != 87 {
			t.Fatal("DTO mutation/query changed retained actual facts")
		}
		old, _ := f.completeSourcePGFixture.read(t, tx, ModeOrdinary)
		oldWallet := f.read(t, tx)
		if old.Summary().ScannedTables != 68 || old.Summary().Status != "consistent" || oldWallet.Summary().ScannedTables != 84 || oldWallet.Summary().Status != "consistent" {
			t.Fatal("old source/wallet reader changed under new physical layout")
		}
	})

	t.Run("real_first_configure_then_same_cut_empty_window", func(t *testing.T) {
		actor := uuid.NewString()
		_, err := f.owner.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$1||'@example.invalid','fixture-only','admin')`, actor)
		completeSourcePGMust(t, "seed actual periodic configuration actor", err)
		commander, err := periodicbudget.NewCommander(f.owner)
		completeSourcePGMust(t, "construct original periodic commander", err)
		first, err := commander.ConfigurePlatformTenant(f.ctx, actor, f.tenant, periodicbudget.PutPeriodicBudget{OperationID: uuid.NewString(), ExpectedRevision: "0", PeriodKind: "utc_month", Enabled: true, LimitAmount: "1000"})
		completeSourcePGMust(t, "commit original first configuration with prefix trigger capture", err)
		var window string
		completeSourcePGMust(t, "commit later same-cut empty window through original function", f.owner.QueryRow(f.ctx, `SELECT pb_observe($1,pb_now())`, first.Receipt.Operation.ResourceID).Scan(&window))
		if window == "" {
			t.Fatal("original window function returned no window")
		}
		tx := f.begin(t, pgx.ReadOnly)
		source, _ := prefixPGSource(t, f.completeSourcePGFixture, tx)
		wallet, _ := prefixPGWallet(t, f.completeSourcePGFixture, tx, ModeOrdinary)
		r := source.Summary()
		if r.Status != "consistent" || !r.CoverageComplete || r.Periodic.Status != "indeterminate" || r.Periodic.CoverageComplete || r.Periodic.Unproven.OperationSnapshotPrefixUnproven != 1 || r.PeriodicPrefix.Base != r.Periodic || !r.PeriodicPrefix.Balanced || r.PeriodicPrefix.CertifiedCommands != 1 || r.PeriodicPrefix.Scanned.Observations != 2 || wallet.Summary().Status != "consistent" {
			t.Fatalf("actual stronger prefix scope lost old gap or chronology: %+v", r)
		}
		var firstSawNull, generated bool
		completeSourcePGMust(t, "inspect independently stored earlier receipt", tx.QueryRow(f.ctx, `SELECT result_snapshot->'current_window'='null'::jsonb FROM periodic_budget_operations WHERE operation_id=$1`, first.Receipt.Operation.OperationID).Scan(&firstSawNull))
		completeSourcePGMust(t, "inspect both actual generated discriminators", tx.QueryRow(f.ctx, `SELECT bool_and(CASE kind WHEN 'command' THEN command_source_id=source_id AND window_source_id IS NULL WHEN 'window_birth' THEN command_source_id IS NULL AND window_source_id=source_id ELSE false END) FROM periodic_budget_observation_journal`).Scan(&generated))
		if !firstSawNull || !generated {
			t.Fatal("actual command/window source chronology or generated values differ")
		}
		if _, err := ProjectLegacySeedPrefix(f.ctx, source); err != nil {
			t.Fatal("retained old gap blocked new-scope legacy DTO", err)
		}
		if _, err := ProjectEpochProofPrefix(f.ctx, wallet); err != nil {
			t.Fatal("retained old gap blocked new-scope epoch DTO", err)
		}
		old, _ := f.completeSourcePGFixture.read(t, tx, ModeOrdinary)
		if old.Summary().Status != "indeterminate" || old.Summary().Periodic != r.Periodic || old.Summary().ScannedTables != 68 {
			t.Fatal("old 68-table gap was rewritten")
		}
	})

	t.Run("three_new_tables_129_rows_and_native_generated_transport", func(t *testing.T) {
		tx := f.corruptionTx(t)
		// Owner-only orphan records isolate the physical codec and page cursor. The
		// resulting graph must remain mismatch, never accepted business evidence.
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_origins(control_id,tracking_started_at)
 SELECT md5('reader-control:'||n)::uuid::text,'2026-11-03 01:02:03.123456+00'::timestamptz FROM generate_series(1,129) AS n`)
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_heads(control_id)
 SELECT md5('reader-control:'||n)::uuid::text FROM generate_series(1,129) AS n`)
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_journal
 (id,control_id,ordinal,kind,source_id,event_seq,configuration_revision,configuration_operation_id,observed_at)
 SELECT md5('reader-command:'||n)::uuid::text,md5('reader-control:'||n)::uuid::text,1,'command',md5('reader-operation:'||n)::uuid::text,0,1,
 md5('reader-operation:'||n)::uuid::text,'2026-11-03 01:02:03.123456+00'::timestamptz FROM generate_series(1,129) AS n`)
		f.exec(t, tx, `INSERT INTO periodic_budget_observation_journal
 (id,control_id,ordinal,previous_id,kind,source_id,event_seq,configuration_revision,configuration_operation_id,window_id,window_revision)
 SELECT md5('reader-birth:'||n)::uuid::text,md5('reader-control:'||n)::uuid::text,2,md5('reader-command:'||n)::uuid::text,'window_birth',md5('reader-window:'||n)::uuid::text,0,1,
 md5('reader-operation:'||n)::uuid::text,md5('reader-window:'||n)::uuid::text,0 FROM generate_series(1,129) AS n`)
		source, sq := prefixPGSource(t, f.completeSourcePGFixture, tx)
		wallet, wq := prefixPGWallet(t, f.completeSourcePGFixture, tx, ModeOrdinary)
		for _, q := range []*prefixPGQuery{sq, wq} {
			if q.calls["periodic_budget_observation_origins"] != 2 || q.calls["periodic_budget_observation_heads"] != 2 || q.calls["periodic_budget_observation_journal"] != 3 {
				t.Fatal("new physical cursor did not cross 128 rows")
			}
		}
		if source.Summary().Status != "mismatch" || wallet.Summary().Status != "mismatch" || source.Summary().ObservationSourceMismatches != 0 || wallet.Summary().ObservationSourceMismatches != 0 {
			t.Fatal("actual physical orphan graph was admitted or generated values changed")
		}
		for _, r := range []periodicbudgetprefix.Report{source.Summary().PeriodicPrefix, wallet.Summary().PeriodicPrefix} {
			if r.Scanned.Origins != 130 || r.Scanned.Heads != 130 || r.Scanned.Observations != 260 {
				t.Fatal("actual physical orphan rows were omitted")
			}
		}
		raw := source.state.raw
		var commands, births int
		table := raw.tables[raw.index["periodic_budget_observation_journal"]]
		for _, row := range table.rows {
			kind, source, command, window := row[table.spec.index["kind"]], row[table.spec.index["source_id"]], row[table.spec.index["command_source_id"]], row[table.spec.index["window_source_id"]]
			if kind.text == "command" {
				commands++
				if !command.valid || command.text != source.text || window.valid {
					t.Fatal("native command discriminator lost")
				}
			} else {
				births++
				if command.valid || !window.valid || window.text != source.text || row[table.spec.index["observed_at"]].instant.Valid {
					t.Fatal("native window discriminator/NULL timestamp lost")
				}
			}
		}
		if commands != 130 || births != 130 || !wallet.Summary().Wallet.RowsConsistent {
			t.Fatal("native rows or independent wallet check suppressed")
		}
	})

	t.Run("owner_changed_generated_value_is_deterministic_mismatch", func(t *testing.T) {
		tx := f.corruptionTx(t)
		// Generated normally prevents such writes. The untrusted scanner still
		// verifies actual transported values if an owner changes that physical DDL.
		f.exec(t, tx, `ALTER TABLE periodic_budget_observation_journal ALTER COLUMN command_source_id DROP EXPRESSION`)
		f.exec(t, tx, `UPDATE periodic_budget_observation_journal SET command_source_id=NULL WHERE kind='command'`)
		source, _ := prefixPGSource(t, f.completeSourcePGFixture, tx)
		wallet, _ := prefixPGWallet(t, f.completeSourcePGFixture, tx, ModeOrdinary)
		if source.Summary().ObservationSourceMismatches != 1 || source.Summary().InputMismatchFields != 0 || source.Summary().Status != "mismatch" || wallet.Summary().ObservationSourceMismatches != 1 || wallet.Summary().Algorithms[7] != "executed" || wallet.Summary().Algorithms[8] != "executed" {
			t.Fatal("actual bad generated discriminator became qualification error or skipped core")
		}
	})

	t.Run("native_sandbox_originals_are_copied_as_untrusted_data", func(t *testing.T) {
		// The independently pinned historical wire is ordinary supplied content in
		// this owner-only fixture. It is deliberately not an actual runtime proof of
		// this random schema; only the scanner and DTO-copy contract are under test.
		_, want := applicationPaymentProjectionQuery(t)
		tx := f.corruptionTx(t)
		e, a := want.Environment, want.Attestation
		f.exec(t, tx, `INSERT INTO payment_sandbox_environment(singleton,id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role,protocol_version,created_at)
 VALUES(true,$1,$2,$3,$4::oid,$5::oid,$6,$7,$8,$9,$10::integer,$11)`, e.ID, e.MerchantID, e.PayeeLabel, e.DatabaseOID, e.SchemaOID, e.NonceDigest, e.AppRole, e.WorkerRole, e.ProjectorRole, e.ProtocolVersion, e.CreatedAt)
		f.exec(t, tx, `INSERT INTO payment_runtime_attestations(singleton,environment_id,database_oid,schema_oid,schema_owner_oid,app_role_oid,worker_role_oid,projector_role_oid,app_nonce_digest,worker_nonce_digest,proof_version,proof_canonical,proof_sha256,created_at)
 VALUES(true,$1,$2::oid,$3::oid,$4::oid,$5::oid,$6::oid,$7::oid,$8,$9,1,$10,$11,$12)`, a.EnvironmentID, a.DatabaseOID, a.SchemaOID, a.SchemaOwnerOID, a.AppRoleOID, a.WorkerRoleOID, a.ProjectorRoleOID, a.AppNonceDigest, a.WorkerNonceDigest, a.ProofCanonical, a.ProofSHA256, a.CreatedAt)
		f.exec(t, tx, `UPDATE funding_lot_runtime_attestations SET mode='sandbox',worker_role_oid=$1::oid,predecessor_payment_digest=$2`, a.WorkerRoleOID, a.ProofSHA256)
		wallet, q := prefixPGWallet(t, f.completeSourcePGFixture, tx, ModeSandbox)
		if wallet.Summary().Status != "consistent" || !wallet.Summary().CoverageComplete {
			t.Fatalf("native descriptive sandbox prefix graph did not qualify: %+v", wallet.Summary())
		}
		before := wallet.Summary()
		out, err := ProjectApplicationPaymentProofPrefix(f.ctx, wallet)
		completeSourcePGMust(t, "project native historical APP original bytes without authority", err)
		if !reflect.DeepEqual(out, want) {
			t.Fatal("native sandbox originals changed")
		}
		out.Environment.ID = "changed"
		out.Attestation.ProofCanonical[0] ^= 1
		again, err := ProjectApplicationPaymentProofPrefix(f.ctx, wallet)
		completeSourcePGMust(t, "repeat actual historical APP deep copy", err)
		if !reflect.DeepEqual(again, want) || wallet.Summary() != before || len(q.calls) != 87 {
			t.Fatal("native APP DTO aliases retained facts")
		}
	})
}
