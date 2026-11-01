package fundinglotcatalog

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func beginCatalogTestTx(t *testing.T, pool *pgxpool.Pool, access pgx.TxAccessMode) pgx.Tx {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: access})
	if err != nil {
		t.Fatal("catalog test transaction")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	})
	return tx
}

func catalogTestAudit(t *testing.T, f *ordinaryVariantFixture) *pgxpool.Pool {
	t.Helper()
	p := f.pool(t, f.parentConfig.ConnConfig.User, f.parentConfig.ConnConfig.Password, false, "proof_audit")
	t.Cleanup(p.Close)
	return p
}

func TestCatalogOrdinaryPublicIssuersAndBindings(t *testing.T) {
	for _, kind := range []ordinaryVariantKind{ordinaryNamed, "named-minimal-login", "named-image-login", ordinaryPublic, ordinaryPublicImage} {
		t.Run(string(kind), func(t *testing.T) {
			f := newOrdinaryVariantFixture(t, kind)
			audit := catalogTestAudit(t, f)
			ctx := context.Background()
			for _, access := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
				tx := beginCatalogTestTx(t, audit, access)
				proof, err := VerifySnapshotTx(ctx, tx, f.pins)
				if err != nil || proof == nil || proof.VerifyTx(ctx, tx) != nil || !proof.MatchesPins(f.pins) || proof.DescriptorVersion() == 0 || len(proof.ExpectedSHA256()) != 64 || len(proof.SchemaSHA256()) != 64 || len(proof.RolesSHA256()) != 64 {
					t.Fatal("reviewed ordinary snapshot proof")
				}
				installation, err := VerifyInstallationTx(ctx, tx, f.pins)
				if err != nil || installation == nil || installation.Catalog() == nil || installation.VerifyTx(ctx, tx) != nil {
					t.Fatal("reviewed ordinary empty installation")
				}
				if p, err := VerifyRuntimeTx(ctx, tx, f.pins, Application); p != nil || err != ErrCatalog {
					t.Fatal("maintenance LOGIN accepted as runtime")
				}
				copyOfProof := *proof
				if copyOfProof.VerifyTx(ctx, tx) != nil {
					t.Fatal("copy lost original binding")
				}
				if access == pgx.ReadWrite {
					nested, err := tx.Begin(ctx)
					if err != nil {
						t.Fatal("catalog nested test wrapper")
					}
					if proof.VerifyTx(ctx, nested) != ErrCatalog || installation.VerifyTx(ctx, nested) != ErrCatalog {
						t.Fatal("proof rebound to nested wrapper")
					}
					if nested.Rollback(ctx) != nil || proof.VerifyTx(ctx, tx) != nil {
						t.Fatal("parent transaction binding lost")
					}
				}
				if tx.Commit(ctx) != nil || proof.VerifyTx(ctx, tx) != ErrCatalog || installation.VerifyTx(ctx, tx) != ErrCatalog {
					t.Fatal("proof survived ended transaction")
				}
				next := beginCatalogTestTx(t, audit, access)
				if proof.VerifyTx(ctx, next) != ErrCatalog || installation.VerifyTx(ctx, next) != ErrCatalog {
					t.Fatal("proof rebound to a later transaction on the pooled connection")
				}
				if next.Rollback(ctx) != nil {
					t.Fatal("later transaction rollback")
				}
			}
			for _, runtime := range []struct {
				pool *pgxpool.Pool
				role RuntimeRole
			}{{f.app, Application}, {f.projector, Projector}} {
				for _, access := range []pgx.TxAccessMode{pgx.ReadOnly, pgx.ReadWrite} {
					tx := beginCatalogTestTx(t, runtime.pool, access)
					p, err := VerifyRuntimeTx(ctx, tx, f.pins, runtime.role)
					if access == pgx.ReadOnly {
						if err != nil || p == nil || p.VerifyTx(ctx, tx) != nil {
							t.Fatal("actual ordinary runtime proof")
						}
					} else if p != nil || err != ErrCatalog {
						t.Fatal("runtime write transaction accepted")
					}
					if tx.Rollback(ctx) != nil {
						t.Fatal("runtime caller rollback")
					}
				}
			}
		})
	}
}

func TestCatalogCanceledAbortedAndDisconnectedTransactions(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	ctx := context.Background()
	tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	proof, err := VerifySnapshotTx(ctx, tx, f.pins)
	if err != nil || proof == nil {
		t.Fatal("liveness initial proof")
	}
	other := beginCatalogTestTx(t, f.app, pgx.ReadOnly)
	if proof.VerifyTx(ctx, other) != ErrCatalog {
		t.Fatal("proof rebound to a different physical connection")
	}
	if other.Rollback(ctx) != nil {
		t.Fatal("other transaction rollback")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if proof.VerifyTx(canceled, tx) != ErrCatalog {
		t.Fatal("canceled verification accepted")
	}
	if p, err := VerifySnapshotTx(canceled, tx, f.pins); p != nil || err != ErrCatalog || tx.Conn().PgConn().TxStatus() != 'T' {
		t.Fatal("canceled issue changed caller transaction or returned evidence")
	}
	if _, err := tx.Exec(ctx, "SELECT 1/0"); err == nil {
		t.Fatal("aborted transaction fixture")
	}
	if proof.VerifyTx(ctx, tx) != ErrCatalog {
		t.Fatal("aborted proof accepted")
	}
	if p, err := VerifySnapshotTx(ctx, tx, f.pins); p != nil || err != ErrCatalog {
		t.Fatal("aborted transaction issued proof")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("aborted caller rollback")
	}
	closed := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	if closed.Conn().Close(ctx) != nil {
		t.Fatal("physical disconnect fixture")
	}
	if p, err := VerifySnapshotTx(ctx, closed, f.pins); p != nil || err != ErrCatalog {
		t.Fatal("disconnected transaction issued proof")
	}
}

func TestCatalogRejectsSameTransactionDriftWithoutCommitting(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	ctx := context.Background()
	q := func(name string) string { return pgx.Identifier{f.schema, name}.Sanitize() }
	mutations := map[string]string{
		"extra relation":            "CREATE TABLE " + q("uncommitted_probe") + "(v integer)",
		"extra tenant column":       "ALTER TABLE " + q("tenants") + " ADD COLUMN unexpected integer",
		"function definer":          "ALTER FUNCTION " + q("fl_uuid") + "(text) SECURITY DEFINER",
		"function source":           "CREATE OR REPLACE FUNCTION " + q("fl_uuid") + "(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT SET search_path TO " + pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp AS 'SELECT false'",
		"disabled trigger":          "ALTER TABLE " + q("tenants") + " DISABLE TRIGGER a_fl_tenant_birth",
		"index predicate":           "DROP INDEX " + q("funding_lot_one_available_opening"),
		"stamp column privilege":    "GRANT INSERT(created_xid) ON " + q("funding_lot_events") + " TO " + pgx.Identifier{f.appName}.Sanitize(),
		"projector table privilege": "GRANT SELECT ON " + q("funding_lots") + " TO " + pgx.Identifier{f.projectorName}.Sanitize(),
		"proof type public usage":   "GRANT USAGE ON TYPE " + q("fl_source_proof") + " TO PUBLIC",
		"runtime high privilege":    "ALTER ROLE " + pgx.Identifier{f.appName}.Sanitize() + " CREATEDB",
		"shadow builtin function":   "CREATE FUNCTION " + q("current_setting") + "(text) RETURNS text LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'shadow executed'; END$$",
		"shadow builtin operator":   "CREATE OPERATOR " + pgx.Identifier{f.schema}.Sanitize() + ".=(LEFTARG=text,RIGHTARG=text,PROCEDURE=pg_catalog.texteq)",
	}
	for name, sql := range mutations {
		t.Run(name, func(t *testing.T) {
			tx := beginCatalogTestTx(t, audit, pgx.ReadWrite)
			if _, err := tx.Exec(ctx, sql); err != nil {
				t.Fatal("catalog drift fixture mutation")
			}
			if proof, err := VerifySnapshotTx(ctx, tx, f.pins); proof != nil || err != ErrCatalog {
				t.Fatal("catalog drift issued proof")
			}
			if tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("read-only metadata check invoked unsafe shadow or aborted caller transaction")
			}
			if tx.Rollback(ctx) != nil {
				t.Fatal("caller drift rollback")
			}
		})
	}
	tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	if p, err := VerifySnapshotTx(ctx, tx, f.pins); p == nil || err != nil {
		t.Fatal("caller rollback did not restore reviewed catalog")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("caller final rollback")
	}
}

func TestCatalogInstallationEmptinessIsAnIssueFact(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	ctx := context.Background()
	tx := beginCatalogTestTx(t, audit, pgx.ReadWrite)
	proof, err := VerifyInstallationTx(ctx, tx, f.pins)
	if err != nil || proof == nil {
		t.Fatal("initial empty installation")
	}
	// A corrupt fact is deliberately injected under the dedicated superuser in a
	// rollback-only test. Static catalog evidence does not authorize or validate it.
	if _, err = tx.Exec(ctx, "SET LOCAL session_replication_role=replica"); err != nil {
		t.Fatal("controlled nonempty fixture")
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+pgx.Identifier{f.schema, "funding_lot_legacy_receipts"}.Sanitize()+"(receipt_id,epoch_id,tenant_id,source_kind,source_id,amount,journal_id) VALUES('test-receipt','test-epoch','test-tenant','manual_credit','test-source',1,'test-journal')"); err != nil {
		t.Fatal("controlled nonempty fact")
	}
	if proof.VerifyTx(ctx, tx) != nil {
		t.Fatal("original empty-at-issue evidence invalidated by fact write")
	}
	if next, err := VerifyInstallationTx(ctx, tx, f.pins); next != nil || err != ErrCatalog {
		t.Fatal("nonempty installation reissued")
	}
	if p, err := VerifySnapshotTx(ctx, tx, f.pins); err != nil || p == nil {
		t.Fatal("static proof incorrectly became a business fact check")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("controlled fact rollback")
	}
}

func TestCatalogRejectsWrongPinsAndAuthentication(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	ctx := context.Background()
	for name, change := range map[string]func(*Pins){
		"host alias":         func(p *Pins) { p.Database.Host = "localhost" },
		"wrong database OID": func(p *Pins) { p.Database.OID++ },
		"wrong schema OID":   func(p *Pins) { p.AppSchema.OID++ },
		"wrong role OID":     func(p *Pins) { p.App.OID++ },
		"wrong role name":    func(p *Pins) { p.App.Name += "_other" },
	} {
		t.Run(name, func(t *testing.T) {
			tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
			p := copyPins(f.pins)
			change(&p)
			if proof, err := VerifySnapshotTx(ctx, tx, p); proof != nil || err != ErrCatalog {
				t.Fatal("wrong target issued proof")
			}
			if tx.Rollback(ctx) != nil {
				t.Fatal("wrong target caller rollback")
			}
		})
	}
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.Serializable} {
		tx, err := audit.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation, AccessMode: pgx.ReadOnly})
		if err != nil {
			t.Fatal("wrong isolation fixture")
		}
		if p, err := VerifySnapshotTx(ctx, tx, f.pins); p != nil || err != ErrCatalog {
			t.Fatal("wrong isolation accepted")
		}
		if tx.Rollback(ctx) != nil {
			t.Fatal("wrong isolation rollback")
		}
	}
	tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	if _, err := tx.Exec(ctx, "SET ROLE "+pgx.Identifier{f.ownerName}.Sanitize()); err != nil {
		t.Fatal("owner impersonation fixture")
	}
	if p, err := VerifySnapshotTx(ctx, tx, f.pins); p != nil || err != ErrCatalog {
		t.Fatal("SET ROLE identity accepted")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("owner impersonation rollback")
	}
	// Keep the configured literal pin while deliberately dialing another IP.
	// A Config-only comparison must not authenticate this physical endpoint.
	cfg := audit.Config().Copy()
	cfg.ConnConfig.Host = "127.0.0.2"
	cfg.ConnConfig.DialFunc = func(c context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, "tcp", "127.0.0.1:55439")
	}
	redirected, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("redirected endpoint test pool")
	}
	t.Cleanup(redirected.Close)
	redirectedTx := beginCatalogTestTx(t, redirected, pgx.ReadOnly)
	redirectedPins := copyPins(f.pins)
	redirectedPins.Database.Host = "127.0.0.2"
	if p, err := VerifySnapshotTx(ctx, redirectedTx, redirectedPins); p != nil || err != ErrCatalog {
		t.Fatal("different physical literal endpoint accepted")
	}
	if redirectedTx.Rollback(ctx) != nil {
		t.Fatal("redirected endpoint caller rollback")
	}
}
