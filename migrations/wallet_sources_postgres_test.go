package migrations

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var walletSourceRelations = []string{
	"wallet_openings", "funding_lots", "funding_lot_balances", "funding_lot_events",
	"funding_reservation_lots", "funding_settlement_lots", "funding_consumption_refund_lots",
}

func walletMigrationOriginal22(t *testing.T, f *prefixFixture) map[string]string {
	t.Helper()
	history, exists, err := readHistory(f.ctx, f.pool)
	prefixMust(t, "read original22 migration checksums", err)
	p, err := plan("022_payment_runtime_attestation.sql", true)
	prefixMust(t, "load exact original22 migration checksums", err)
	if !exists || len(history) != 22 || len(p.prefix) != 22 {
		t.Fatal("fixture is not the exact 22-migration prefix")
	}
	for _, item := range p.prefix {
		if history[item.name] != item.checksum {
			t.Fatalf("original22 checksum mismatch for %s", item.name)
		}
	}
	return history
}

// Snapshot every original table row, not only the intentionally minimal balance
// or source fixture. The existing helper covers relation identity and function
// bodies; this also includes constraints, triggers, indexes and migration times.
func walletMigrationSnapshot(t *testing.T, f *prefixFixture) string {
	t.Helper()
	digest := sha256.New()
	fmt.Fprintf(digest, "%q\n", f.snapshot(t))
	rows, err := f.pool.Query(f.ctx, `SELECT c.relname FROM pg_catalog.pg_class c
 WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind IN ('r','p') ORDER BY c.relname`)
	prefixMust(t, "list original tables", err)
	var tables []string
	for rows.Next() {
		var table string
		prefixMust(t, "read original table name", rows.Scan(&table))
		tables = append(tables, table)
	}
	prefixMust(t, "finish original table list", rows.Err())
	rows.Close()
	for _, table := range tables {
		var data string
		query := "SELECT coalesce(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM " + pgx.Identifier{f.schema, table}.Sanitize() + " t"
		prefixMust(t, "snapshot original table rows", f.pool.QueryRow(f.ctx, query).Scan(&data))
		fmt.Fprintf(digest, "%q:%q\n", table, data)
	}
	var definitions string
	prefixMust(t, "snapshot original schema definitions", f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'constraints',coalesce((SELECT jsonb_agg(jsonb_build_array(c.oid::text,c.conname,pg_get_constraintdef(c.oid)) ORDER BY c.oid) FROM pg_constraint c WHERE c.connamespace=current_schema()::regnamespace),'[]'::jsonb),
 'triggers',coalesce((SELECT jsonb_agg(jsonb_build_array(t.oid::text,t.tgname,pg_get_triggerdef(t.oid)) ORDER BY t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE c.relnamespace=current_schema()::regnamespace),'[]'::jsonb),
 'indexes',coalesce((SELECT jsonb_agg(jsonb_build_array(c.oid::text,pg_get_indexdef(c.oid)) ORDER BY c.oid) FROM pg_class c WHERE c.relnamespace=current_schema()::regnamespace AND c.relkind='i'),'[]'::jsonb))::text`).Scan(&definitions))
	fmt.Fprintf(digest, "%q\n", definitions)
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func walletMigrationRelationCount(t *testing.T, f *prefixFixture) int {
	t.Helper()
	var count int
	prefixMust(t, "inspect wallet source relations", f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_class
 WHERE relnamespace=current_schema()::regnamespace AND relname=ANY($1::text[])`, walletSourceRelations).Scan(&count))
	return count
}

func TestPostgresWalletSourcesMigrationRejectsLegacyFinancialFactsWithoutMutation(t *testing.T) {
	for _, fixture := range []string{"nonzero_customer_balance", "nonzero_platform_balance", "pending_credit_order"} {
		t.Run(fixture, func(t *testing.T) {
			f := newPrefixFixture(t)
			prefixMust(t, "install explicit022 baseline", ApplyThrough(f.ctx, f.pool, "022_payment_runtime_attestation.sql"))
			tenant := uuid.NewString()
			f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'preserve legacy financial facts')`, tenant)
			switch fixture {
			case "nonzero_customer_balance":
				// Owner-written nonzero cache is sufficient for the 023 preflight.
				// It deliberately does not claim a full historical billing fixture.
				f.exec(t, `INSERT INTO ledger_accounts(tenant_id,kind,balance) VALUES($1,'AVAILABLE',17),($1,'HELD',0)`, tenant)
			case "nonzero_platform_balance":
				f.exec(t, `INSERT INTO ledger_accounts(kind,balance) VALUES('FUNDING_CLEARING',-17)`)
			case "pending_credit_order":
				actor := uuid.NewString()
				f.exec(t, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-fixture-hash','finance_operator')`, actor, actor+"@migration.invalid")
				f.exec(t, `INSERT INTO credit_orders(tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,17,$3,'legacy-confirmation')`, tenant, "migration:"+uuid.NewString(), actor)
			}
			prefixMust(t, "verify untouched022 baseline", VerifyThrough(f.ctx, f.pool, "022_payment_runtime_attestation.sql"))
			history := walletMigrationOriginal22(t, f)
			before := walletMigrationSnapshot(t, f)
			if walletMigrationRelationCount(t, f) != 0 {
				t.Fatal("022 fixture already contains wallet source relations")
			}
			for _, current := range []bool{true, false} {
				var err error
				if current {
					err = Apply(f.ctx, f.pool)
				} else {
					err = ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql")
				}
				var pg *pgconn.PgError
				if !errors.As(err, &pg) || pg.Code != "P0001" || !strings.Contains(pg.Message, "023 requires a fresh wallet history") || !strings.Contains(err.Error(), "023_wallet_sources.sql") {
					t.Fatalf("current=%t: expected explicit old-history rejection, received %v", current, err)
				}
				if got := walletMigrationOriginal22(t, f); !reflect.DeepEqual(got, history) {
					t.Fatal("rejected migration changed original22 versions or checksums")
				}
				if walletMigrationSnapshot(t, f) != before || walletMigrationRelationCount(t, f) != 0 {
					t.Fatal("rejected migration changed original facts or left a partial wallet schema")
				}
			}
		})
	}
}

func TestPostgresWalletSourcesMigrationAcceptsEmpty022Prefix(t *testing.T) {
	f := newPrefixFixture(t)
	prefixMust(t, "install explicit empty022 baseline", ApplyThrough(f.ctx, f.pool, "022_payment_runtime_attestation.sql"))
	original := walletMigrationOriginal22(t, f)
	var originalRows string
	prefixMust(t, "snapshot original22 version records", f.pool.QueryRow(f.ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY name)::text FROM schema_migrations m`).Scan(&originalRows))
	prefixMust(t, "install023 on empty wallet history", ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
	prefixMust(t, "verify exact023 prefix", VerifyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
	f.assertHistory(t, 23)
	current, exists, err := readHistory(f.ctx, f.pool)
	prefixMust(t, "read023 migration history", err)
	if !exists || len(current) != 23 {
		t.Fatal("023 migration did not record its exact version")
	}
	for name, checksum := range original {
		if current[name] != checksum {
			t.Fatalf("successful023 migration changed original22 checksum for %s", name)
		}
	}
	var preservedRows string
	prefixMust(t, "inspect preserved original22 version records", f.pool.QueryRow(f.ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY name)::text FROM schema_migrations m WHERE name<='022_payment_runtime_attestation.sql'`).Scan(&preservedRows))
	if preservedRows != originalRows || walletMigrationRelationCount(t, f) != len(walletSourceRelations) {
		t.Fatal("023 changed original migration records or did not install all wallet relations")
	}
	for _, table := range walletSourceRelations {
		var count int
		prefixMust(t, "inspect initially empty wallet source table", f.pool.QueryRow(f.ctx, "SELECT count(*) FROM "+pgx.Identifier{f.schema, table}.Sanitize()).Scan(&count))
		if count != 0 {
			t.Fatal("023 synthesized a funding source or balance")
		}
	}
	before := walletMigrationSnapshot(t, f)
	prefixMust(t, "idempotent exact023 migration", ApplyThrough(f.ctx, f.pool, "023_wallet_sources.sql"))
	if walletMigrationSnapshot(t, f) != before {
		t.Fatal("reapplying023 changed financial facts or schema")
	}
}
