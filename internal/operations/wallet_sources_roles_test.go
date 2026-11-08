package operations

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Diagnostics emit only allowlisted names, comparisons and SHA-256 values.
// Never print prosrc, DSNs, raw driver errors or arbitrary catalog strings.
func paymentRoleDiagnostics(t *testing.T, f *paymentRoleFixture) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Log("role diagnostic could not begin")
		return
	}
	defer tx.Rollback(f.ctx)
	rows, err := tx.Query(f.ctx, paymentFunctionDefinitionsSQL, f.schema)
	if err != nil {
		t.Log("role diagnostic could not read function catalog")
		return
	}
	count, mismatches := 0, 0
	for rows.Next() {
		var name, argOIDs, result, language, volatility, body string
		var args []string
		var valid *bool
		if rows.Scan(&name, &argOIDs, &args, &result, &language, &volatility, &body, &valid) != nil {
			rows.Close()
			t.Log("role diagnostic function scan failed")
			return
		}
		count++
		definition, known := paymentFunctionSpecs[name]
		if !known {
			mismatches++
			t.Log("role diagnostic found an unreviewed function")
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
		attributes := valid != nil && *valid
		if !attributes || definition.argOIDs != argOIDs || definition.resultOID != result || definition.language != language || definition.volatility != volatility || !slices.Equal(definition.argNames, args) || definition.bodySHA != hash {
			mismatches++
			t.Logf("role function %s: attributes_nonnull=%t attributes=%t arguments=%t names=%t result=%t language=%t volatility=%t body=%t expected_sha=%s actual_sha=%s", definition.name, valid != nil, attributes, definition.argOIDs == argOIDs, slices.Equal(definition.argNames, args), definition.resultOID == result, definition.language == language, definition.volatility == volatility, definition.bodySHA == hash, definition.bodySHA, hash)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Log("role diagnostic function iteration failed")
		return
	}
	t.Logf("role diagnostic function count=%d expected=%d mismatches=%d", count, len(paymentFunctionSpecs), mismatches)
	if mismatches != 0 || count != len(paymentFunctionSpecs) {
		return
	}
	for _, entry := range []struct {
		name string
		kind PaymentDatabaseRole
	}{{f.appRole, PaymentApplicationRole}, {f.projectionRole, PaymentProjectionRole}} {
		if provisionPaymentRoleACL(f.ctx, tx, f.schema, entry.name, entry.kind) != nil {
			t.Logf("role diagnostic scoped provision failed: %s", entry.kind)
			return
		}
		var schemaOID, roleOID uint32
		if tx.QueryRow(f.ctx, "SELECT n.oid,r.oid FROM pg_namespace n CROSS JOIN pg_roles r WHERE n.nspname=$1 AND r.rolname=$2", f.schema, entry.name).Scan(&schemaOID, &roleOID) != nil {
			t.Log("role diagnostic identity query failed")
			return
		}
		t.Logf("role diagnostic scoped %s: tables=%t functions=%t complete=%t", entry.kind,
			verifyPaymentTables(f.ctx, tx, schemaOID, roleOID, entry.kind, true) == nil,
			verifyPaymentFunctionPrivileges(f.ctx, tx, schemaOID, roleOID, entry.kind, true) == nil,
			verifyPaymentRole(f.ctx, tx, f.schema, entry.name, entry.kind, paymentMarker{}, false) == nil)
	}
}

func reviewedWalletFunction(t *testing.T, name string) string {
	t.Helper()
	body, err := migrations.Files.ReadFile("024_unified_text_metering.sql")
	paymentRoleMust(t, "reviewed current wallet source", err)
	definition := regexp.MustCompile(`(?s)CREATE OR REPLACE FUNCTION ` + regexp.QuoteMeta(name) + `\(.*?\$\$;`).FindString(string(body))
	if definition == "" {
		t.Fatal("reviewed wallet definition unavailable")
	}
	return definition
}

// These tests are defined for the root's serial dedicated-PostgreSQL run. Each
// connection in the shared fixture authenticates as its own restricted LOGIN.
func TestWalletSourcesRolesActualCurrentMatrix(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	for _, role := range []struct {
		name string
		kind PaymentDatabaseRole
	}{
		{f.appRole, PaymentApplicationRole}, {f.workerRole, PaymentWorkerRole}, {f.projectionRole, PaymentProjectionRole},
	} {
		for _, table := range []string{"wallet_openings", "funding_lots", "funding_lot_balances", "funding_lot_events", "funding_reservation_lots", "funding_settlement_lots", "funding_consumption_refund_lots", "ledger_accounts"} {
			t.Run(string(role.kind)+"/"+table, func(t *testing.T) {
				app := role.kind == PaymentApplicationRole
				worker := role.kind == PaymentWorkerRole
				workerSource := table == "funding_lots" || table == "funding_lot_balances" || table == "funding_lot_events"
				wantSelect := app || worker && (workerSource || table == "ledger_accounts")
				wantInsert := app && table != "wallet_openings" || worker && workerSource
				var selectAll, insertAll, updateAll, forbidden bool
				paymentRoleMust(t, "independent table privilege query", f.owner.QueryRow(f.ctx, `SELECT
 has_table_privilege($1::name,$2::text,'SELECT'),has_table_privilege($1::name,$2::text,'INSERT'),has_table_privilege($1::name,$2::text,'UPDATE'),
 has_table_privilege($1::name,$2::text,'DELETE') OR has_table_privilege($1::name,$2::text,'TRUNCATE') OR has_table_privilege($1::name,$2::text,'TRIGGER') OR has_table_privilege($1::name,$2::text,'REFERENCES')`, role.name, f.table(table)).Scan(&selectAll, &insertAll, &updateAll, &forbidden))
				if selectAll != wantSelect || insertAll != wantInsert || updateAll || forbidden {
					t.Fatal("table privileges differ from current source contract")
				}
				rows, err := f.owner.Query(f.ctx, `SELECT a.attname,
 has_column_privilege($1::name,c.oid,a.attnum,'SELECT'),has_column_privilege($1::name,c.oid,a.attnum,'INSERT'),has_column_privilege($1::name,c.oid,a.attnum,'UPDATE'),has_column_privilege($1::name,c.oid,a.attnum,'REFERENCES')
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid
 WHERE n.nspname=$2 AND c.relname=$3 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`, role.name, f.schema, table)
				paymentRoleMust(t, "independent column privilege query", err)
				defer rows.Close()
				for rows.Next() {
					var column string
					var selectCol, insertCol, updateCol, references bool
					paymentRoleMust(t, "column privilege scan", rows.Scan(&column, &selectCol, &insertCol, &updateCol, &references))
					wantInsertCol := wantInsert || worker && table == "ledger_accounts" && (column == "tenant_id" || column == "kind")
					wantUpdate := (app || worker) && (table == "ledger_accounts" && column == "balance" || table == "funding_lot_balances" && (column == "available_amount" || column == "held_amount" || column == "consumed_amount" || column == "revision"))
					if selectCol != wantSelect || insertCol != wantInsertCol || updateCol != wantUpdate || references {
						t.Fatal("column privileges differ from current source contract")
					}
				}
				paymentRoleMust(t, "column privilege iteration", rows.Err())
			})
		}
	}
	// Repeated blanket provisioning must still end at this exact boundary.
	for i := 0; i < 2; i++ {
		paymentRoleMust(t, "current repeated provisioning", ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole))
		f.valid(t, true)
	}
}

func TestWalletSourcesRolesDMLAndWorkerReadBoundary(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	denied := func(t *testing.T, role PaymentDatabaseRole, query string) {
		t.Helper()
		pool := f.app
		if role == PaymentWorkerRole {
			pool = f.worker
		} else if role == PaymentProjectionRole {
			pool = f.projection
		}
		_, err := pool.Exec(f.ctx, query)
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "42501" {
			t.Fatal("forbidden operation did not fail with insufficient privilege")
		}
	}
	for _, query := range []string{
		"INSERT INTO wallet_openings(id) VALUES('forbidden')",
		"UPDATE funding_lots SET source_id=source_id WHERE false",
		"UPDATE funding_lot_balances SET tenant_id=tenant_id WHERE false",
		"DELETE FROM funding_lot_balances WHERE false",
		"UPDATE ledger_accounts SET tenant_id=tenant_id WHERE false",
		"UPDATE ledger_accounts SET kind=kind WHERE false",
		"DELETE FROM ledger_accounts WHERE false",
	} {
		denied(t, PaymentApplicationRole, query)
	}
	for _, table := range []string{"wallet_openings", "funding_reservation_lots", "funding_settlement_lots", "funding_consumption_refund_lots", "credit_orders", "voucher_redemptions", "reservations", "settlements", "refund_orders"} {
		denied(t, PaymentWorkerRole, "SELECT * FROM "+pgx.Identifier{table}.Sanitize()+" LIMIT 0")
	}
	for _, table := range []string{"wallet_openings", "funding_lots", "funding_lot_balances", "funding_lot_events", "funding_reservation_lots", "funding_settlement_lots", "funding_consumption_refund_lots", "ledger_accounts"} {
		denied(t, PaymentProjectionRole, "SELECT * FROM "+pgx.Identifier{table}.Sanitize()+" LIMIT 0")
	}
	for _, name := range []string{"wallet_lot_valid", "wallet_event_valid", "wallet_journal_valid", "wallet_account_valid"} {
		var valid bool
		paymentRoleMust(t, "worker safe missing-fact validator", f.worker.QueryRow(f.ctx, "SELECT "+pgx.Identifier{name}.Sanitize()+"('missing')").Scan(&valid))
		if valid {
			t.Fatal("missing wallet fact qualified")
		}
	}
	for _, name := range []string{"wallet_opening_journal_valid", "wallet_reservation_valid", "wallet_settlement_valid", "wallet_refund_valid"} {
		denied(t, PaymentWorkerRole, "SELECT "+pgx.Identifier{name}.Sanitize()+"('missing')")
	}
}

func TestWalletSourcesRolesDetectPrivilegeAndFunctionDrift(t *testing.T) {
	f := newPaymentRoleFixture(t, false)
	f.marker(t)
	f.valid(t, true)
	qa, qw, qp := pgx.Identifier{f.appRole}.Sanitize(), pgx.Identifier{f.workerRole}.Sanitize(), pgx.Identifier{f.projectionRole}.Sanitize()
	for _, fault := range []struct {
		name, mutate, restore string
		role                  PaymentDatabaseRole
	}{
		{"app opening insert", "GRANT INSERT ON " + f.table("wallet_openings") + " TO " + qa, "REVOKE INSERT ON " + f.table("wallet_openings") + " FROM " + qa, PaymentApplicationRole},
		{"app source identity update", "GRANT UPDATE(source_id) ON " + f.table("funding_lots") + " TO " + qa, "REVOKE UPDATE(source_id) ON " + f.table("funding_lots") + " FROM " + qa, PaymentApplicationRole},
		{"app ledger identity update", "GRANT UPDATE(kind) ON " + f.table("ledger_accounts") + " TO " + qa, "REVOKE UPDATE(kind) ON " + f.table("ledger_accounts") + " FROM " + qa, PaymentApplicationRole},
		{"app required source insert", "REVOKE INSERT ON " + f.table("funding_lot_events") + " FROM " + qa, "GRANT INSERT ON " + f.table("funding_lot_events") + " TO " + qa, PaymentApplicationRole},
		{"worker allocation read", "GRANT SELECT ON " + f.table("funding_reservation_lots") + " TO " + qw, "REVOKE SELECT ON " + f.table("funding_reservation_lots") + " FROM " + qw, PaymentWorkerRole},
		{"worker required balance revision", "REVOKE UPDATE(revision) ON " + f.table("funding_lot_balances") + " FROM " + qw, "GRANT UPDATE(revision) ON " + f.table("funding_lot_balances") + " TO " + qw, PaymentWorkerRole},
		{"projector source column", "GRANT SELECT(lot_id) ON " + f.table("funding_lot_events") + " TO " + qp, "REVOKE SELECT(lot_id) ON " + f.table("funding_lot_events") + " FROM " + qp, PaymentProjectionRole},
		{"PUBLIC source read", "GRANT SELECT ON " + f.table("funding_lots") + " TO PUBLIC", "REVOKE SELECT ON " + f.table("funding_lots") + " FROM PUBLIC", PaymentApplicationRole},
		{"worker customer validator", "GRANT EXECUTE ON FUNCTION " + f.table("wallet_reservation_valid") + "(text) TO " + qw, "REVOKE EXECUTE ON FUNCTION " + f.table("wallet_reservation_valid") + "(text) FROM " + qw, PaymentWorkerRole},
		{"projector validator", "GRANT EXECUTE ON FUNCTION " + f.table("wallet_lot_valid") + "(text) TO " + qp, "REVOKE EXECUTE ON FUNCTION " + f.table("wallet_lot_valid") + "(text) FROM " + qp, PaymentProjectionRole},
		{"PUBLIC validator", "GRANT EXECUTE ON FUNCTION " + f.table("wallet_event_valid") + "(text) TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + f.table("wallet_event_valid") + "(text) FROM PUBLIC", PaymentWorkerRole},
		{"definer validator", "ALTER FUNCTION " + f.table("wallet_event_valid") + "(text) SECURITY DEFINER", "ALTER FUNCTION " + f.table("wallet_event_valid") + "(text) SECURITY INVOKER", PaymentWorkerRole},
		{"validator search path", "ALTER FUNCTION " + f.table("wallet_event_valid") + "(text) SET search_path TO pg_catalog", "ALTER FUNCTION " + f.table("wallet_event_valid") + "(text) SET search_path TO " + pgx.Identifier{f.schema}.Sanitize() + ",pg_catalog,pg_temp", PaymentApplicationRole},
		{"unreviewed wallet function", "CREATE FUNCTION " + f.table("wallet_unreviewed") + "() RETURNS boolean LANGUAGE sql AS 'SELECT true'", "DROP FUNCTION " + f.table("wallet_unreviewed") + "()", PaymentApplicationRole},
	} {
		t.Run(fault.name, func(t *testing.T) {
			f.exec(t, fault.mutate)
			defer f.exec(t, fault.restore)
			f.rejected(t, fault.role)
		})
	}
	f.valid(t, true)
	// Definition fingerprints must resist a same-signature replaced body. Restore
	// the exact reviewed source through this test's owner, never a live DB copy.
	fn := f.table("wallet_event_valid") + "(text)"
	body := reviewedWalletFunction(t, "wallet_event_valid")
	f.exec(t, "CREATE OR REPLACE FUNCTION "+f.table("wallet_event_valid")+"(event_ref text) RETURNS boolean LANGUAGE plpgsql STABLE AS 'BEGIN RETURN true; END'")
	f.rejected(t, PaymentWorkerRole)
	f.exec(t, strings.Replace(body, "CREATE FUNCTION wallet_event_valid(", "CREATE OR REPLACE FUNCTION "+f.table("wallet_event_valid")+"(", 1))
	f.exec(t, "ALTER FUNCTION "+fn+" SET search_path TO "+pgx.Identifier{f.schema}.Sanitize()+",pg_catalog,pg_temp")
	f.valid(t, true)
}
