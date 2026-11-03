package fundinglotcatalog

import (
	"bytes"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/periodicbudgetprefixschema"
)

// Each original profile must first issue real proofs. The complete B-P2 DDL
// and ACL are then installed in that same known-good catalog, never a partial
// base fixture whose missing C0 objects or incorrect pins could reject early.
func TestCatalogOriginalProfilesRejectCompletePeriodicPrefixCandidate(t *testing.T) {
	originalOrdinary := bytes.Clone(ordinaryExpectedJSON)
	originalSandbox := bytes.Clone(sandboxExpectedJSON)
	for _, profile := range []Profile{LegacyV1, WalletReadV2} {
		name := "legacy_v1"
		if profile == WalletReadV2 {
			name = "wallet_read_v2"
		}
		t.Run(name, func(t *testing.T) {
			f := walletProfileOrdinary(t, ordinaryNamed)
			if profile == WalletReadV2 {
				walletProfileInstall(t, f)
			}
			expected, root, err := loadExpectedProfile(f.pins, profile)
			if err != nil || expected == nil {
				t.Fatal("original reviewed profile material")
			}
			runtime := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
			baseline, err := VerifyRuntimeProfileTx(f.ctx, runtime, f.pins, Application, profile)
			if err != nil || baseline == nil || baseline.Profile() != profile || baseline.ExpectedSHA256() != root || baseline.VerifyTx(f.ctx, runtime) != nil {
				t.Fatal("actual APP original profile must issue a genuine baseline")
			}
			walletProfileRollback(t, runtime)

			// ordinaryNamed has a NOLOGIN owner. The actual maintenance LOGIN issues
			// proofs; SET ROLE is used only for offline DDL ownership, never issuance.
			install := beginCatalogTestTx(t, f.admin, pgx.ReadWrite)
			before, err := VerifySnapshotProfileTx(f.ctx, install, f.pins, profile)
			if err != nil || before == nil || before.ExpectedSHA256() != root || before.VerifyTx(f.ctx, install) != nil {
				t.Fatal("same installation transaction must first prove the exact original catalog")
			}
			if profile == LegacyV1 {
				empty, err := VerifyInstallationTx(f.ctx, install, f.pins)
				if err != nil || empty == nil || empty.VerifyTx(f.ctx, install) != nil {
					t.Fatal("original empty installation baseline")
				}
				original, err := VerifySnapshotTx(f.ctx, install, f.pins)
				if err != nil || original == nil {
					t.Fatal("original snapshot API baseline")
				}
			}
			ddl, err := periodicbudgetprefixschema.SQL()
			if err != nil {
				t.Fatal("frozen prefix candidate DDL")
			}
			acl, err := periodicbudgetprefixschema.Permissions(f.pins.App.Name, f.pins.Projector.Name)
			if err != nil {
				t.Fatal("frozen prefix candidate ACL")
			}
			if _, err = install.Exec(f.ctx, "SET LOCAL ROLE "+pgx.Identifier{f.pins.AppSchema.ObjectOwner.Name}.Sanitize()); err != nil {
				t.Fatal("offline owner selection")
			}
			if _, err = install.Exec(f.ctx, ddl); err != nil {
				t.Fatal("complete prefix candidate DDL")
			}
			if _, err = install.Exec(f.ctx, acl); err != nil {
				t.Fatal("complete prefix candidate ACL")
			}
			if _, err = install.Exec(f.ctx, "RESET ROLE"); err != nil {
				t.Fatal("restore actual proof issuer")
			}
			var complete bool
			if install.QueryRow(f.ctx, `SELECT
    (SELECT count(*)=3 FROM pg_catalog.pg_class WHERE relnamespace=$1::oid AND relkind='r' AND relname IN ('periodic_budget_observation_origins','periodic_budget_observation_heads','periodic_budget_observation_journal'))
    AND (SELECT count(*)=12 FROM pg_catalog.pg_proc WHERE pronamespace=$1::oid AND left(proname,10)='pb_prefix_')
    AND (SELECT count(*)=3 FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON c.oid=a.attrelid WHERE c.relnamespace=$1::oid AND NOT a.attisdropped AND a.attnum>0 AND ((c.relname='periodic_budget_operations' AND a.attname='observation_id') OR (c.relname='periodic_budget_windows' AND a.attname IN ('observation_id','observation_created_xid'))))`, f.pins.AppSchema.OID).Scan(&complete) != nil || !complete {
				t.Fatal("negative must contain the complete candidate surface")
			}
			if proof, err := VerifySnapshotProfileTx(f.ctx, install, f.pins, profile); proof != nil || err != ErrCatalog {
				t.Fatal("original snapshot profile accepted candidate schema")
			}
			if profile == LegacyV1 {
				if proof, err := VerifySnapshotTx(f.ctx, install, f.pins); proof != nil || err != ErrCatalog {
					t.Fatal("original snapshot API accepted candidate schema")
				}
				if proof, err := VerifyInstallationTx(f.ctx, install, f.pins); proof != nil || err != ErrCatalog {
					t.Fatal("original installation API accepted candidate schema")
				}
			}
			if install.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("catalog rejection aborted the caller transaction")
			}
			if err := install.Commit(f.ctx); err != nil {
				t.Fatal("caller commits isolated candidate fixture; unknown outcome is not retried")
			}

			runtime = beginCatalogTestTx(t, f.app, pgx.ReadOnly)
			if proof, err := VerifyRuntimeProfileTx(f.ctx, runtime, f.pins, Application, profile); proof != nil || err != ErrCatalog {
				t.Fatal("actual APP original runtime profile accepted candidate schema")
			}
			if profile == LegacyV1 {
				if proof, err := VerifyRuntimeTx(f.ctx, runtime, f.pins, Application); proof != nil || err != ErrCatalog {
					t.Fatal("original runtime API accepted candidate schema")
				}
			}
			if runtime.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("runtime rejection aborted the caller transaction")
			}
			walletProfileRollback(t, runtime)
			again, againRoot, err := loadExpectedProfile(f.pins, profile)
			if err != nil || again != expected || againRoot != root || !bytes.Equal(originalOrdinary, ordinaryExpectedJSON) || !bytes.Equal(originalSandbox, sandboxExpectedJSON) {
				t.Fatal("candidate installation changed original reviewed material or roots")
			}
		})
	}
}
