package migrations_test

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestCoinDisplayMigrationGenesisValidationAndImmutableHistory(t *testing.T) {
	f := newCoinPrefixFixture(t)
	coinMust(t, "apply current display migration", migrations.ApplyCurrent(f.ctx, f.pool))
	var version int64
	var name string
	var currency, actor *string
	coinMust(t, "read display genesis", f.pool.QueryRow(f.ctx, `SELECT version,display_name,fiat_currency,actor FROM coin_display_versions`).Scan(&version, &name, &currency, &actor))
	if version != 1 || name != "币" || currency != nil || actor != nil {
		t.Fatalf("invalid display genesis: %d %q %v %v", version, name, currency, actor)
	}
	for _, tc := range []struct{ name, sql string }{
		{"blank name", `INSERT INTO coin_display_versions(operation_id,version,display_name,reason) VALUES($1,2,'','bad')`},
		{"untrimmed name", `INSERT INTO coin_display_versions(operation_id,version,display_name,reason) VALUES($1,2,' 点','bad')`},
		{"unknown currency", `INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,rate_den,reason) VALUES($1,2,'点','EUR',1,1,'bad')`},
		{"partial rate", `INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,reason) VALUES($1,2,'点','CNY',1,'bad')`},
		{"zero rate", `INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,rate_den,reason) VALUES($1,2,'点','CNY',0,1,'bad')`},
		{"missing actor", `INSERT INTO coin_display_versions(operation_id,version,display_name,reason) VALUES($1,2,'点','bad')`},
	} {
		if _, err := f.pool.Exec(f.ctx, tc.sql, uuid.NewString()); err == nil {
			t.Fatalf("%s was accepted", tc.name)
		}
	}
	admin := uuid.NewString()
	f.exec(t, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','admin')`, admin, admin+"@display.invalid")
	f.exec(t, `INSERT INTO coin_display_versions(operation_id,version,display_name,fiat_currency,rate_num,rate_den,actor,reason) VALUES($1,2,'点数','CNY',7,10,$2,'display test')`, uuid.NewString(), admin)
	if _, err := f.pool.Exec(f.ctx, `UPDATE coin_display_versions SET display_name='改写' WHERE version=1`); err == nil {
		t.Fatal("display history update was accepted")
	}
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM coin_display_versions WHERE version=1`); err == nil {
		t.Fatal("display history delete was accepted")
	}
	coinMust(t, "repeat current display migration idempotently", migrations.ApplyCurrent(f.ctx, f.pool))
}

func TestCoinDisplayMigrationUpgrades009(t *testing.T) {
	f := newCoinPrefixFixture(t)
	entries, err := migrations.Files.ReadDir(".")
	coinMust(t, "list current migrations", err)
	names := []string{"current_mvp.sql"}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "current_") && entry.Name() != "current_mvp.sql" && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names[1:])
	f.exec(t, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
	upgraded := false
	for _, name := range names {
		body, err := migrations.Files.ReadFile(name)
		coinMust(t, "read current migration", err)
		f.exec(t, string(body))
		f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body)))
		if name == "current_009_cash_refund_workflow.sql" {
			upgraded = true
			break
		}
	}
	if !upgraded {
		t.Fatal("009 baseline not installed")
	}
	coinMust(t, "upgrade 009 to display configuration", migrations.ApplyCurrent(f.ctx, f.pool))
	var migrationsCount, genesis int
	coinMust(t, "verify display migration and genesis", f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM schema_migrations WHERE name='current_010_coin_display.sql'),(SELECT count(*) FROM coin_display_versions WHERE version=1 AND display_name='币' AND fiat_currency IS NULL)`).Scan(&migrationsCount, &genesis))
	if migrationsCount != 1 || genesis != 1 {
		t.Fatalf("display upgrade facts: migrations=%d genesis=%d", migrationsCount, genesis)
	}
}
