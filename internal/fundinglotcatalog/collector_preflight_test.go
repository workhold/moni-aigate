package fundinglotcatalog

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type forbidCatalogDeparseTx struct {
	pgx.Tx
	deparsed bool
}

func (tx *forbidCatalogDeparseTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, "pg_catalog.pg_get_expr(") || strings.Contains(sql, "pg_catalog.pg_get_partkeydef(") {
		tx.deparsed = true
		return nil, ErrCatalog
	}
	return tx.Tx.Query(ctx, sql, args...)
}

// These mutations live only in the caller's rollback transaction. The wrapper
// proves rejection occurs before any schema expression deparser is queried.
func TestCatalogRawPreflightRejectsBeforeDeparse(t *testing.T) {
	f := newOrdinaryVariantFixture(t, ordinaryNamed)
	audit := catalogTestAudit(t, f)
	q := func(s string) string { return pgx.Identifier{f.schema, s}.Sanitize() }
	cases := map[string]string{
		"oversized function text": "CREATE FUNCTION " + q("oversized_catalog_probe") + "(text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS 'SELECT true /*" + strings.Repeat("x", collectorRecordLimit+1) + "*/'",
		"unsupported domain":      "CREATE DOMAIN " + q("unreviewed_domain") + " AS text CHECK (VALUE IS NOT NULL)",
		"unsupported policy":      "CREATE POLICY unreviewed_policy ON " + q("tenants") + " USING (true)",
	}
	for name, sql := range cases {
		t.Run(name, func(t *testing.T) {
			tx := beginCatalogTestTx(t, audit, pgx.ReadWrite)
			if _, e := tx.Exec(context.Background(), sql); e != nil {
				t.Fatal("raw preflight fixture mutation failed")
			}
			guarded := &forbidCatalogDeparseTx{Tx: tx}
			if snapshot, e := observeCatalogTx(context.Background(), guarded, f.pins); snapshot != nil || e != ErrCatalog {
				t.Fatal("unreviewed raw metadata accepted")
			}
			if guarded.deparsed {
				t.Fatal("expression deparser queried before raw rejection")
			}
			if tx.Conn().PgConn().TxStatus() != 'T' {
				t.Fatal("metadata rejection aborted caller transaction")
			}
			if tx.Rollback(context.Background()) != nil {
				t.Fatal("caller rollback failed")
			}
		})
	}
	// The rollback restores the exact reviewed metadata rather than mutating a
	// fixture into the desired expected state.
	tx := beginCatalogTestTx(t, audit, pgx.ReadOnly)
	if s, e := observeCatalogTx(context.Background(), tx, f.pins); s == nil || e != nil {
		t.Fatal("rollback did not restore observed catalog")
	}
	if tx.Rollback(context.Background()) != nil {
		t.Fatal("baseline rollback failed")
	}
}
