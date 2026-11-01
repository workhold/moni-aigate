package funding019

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/migrations"
)

func TestHistoricalPredicateIsStatic017Copy(t *testing.T) {
	_, original, err := baseline()
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(fundingSQL, "WITH valid_funding AS (\n")
	if !ok {
		t.Fatal("missing historical CTE")
	}
	body, _, ok = strings.Cut(body, "\n)\nSELECT\n")
	if !ok {
		t.Fatal("missing historical CTE boundary")
	}
	body = strings.ReplaceAll(body, "@", "")
	body = strings.Replace(body, "SELECT j.id FROM", "SELECT 1 FROM", 1)
	body = strings.Replace(body, "WHERE j.tenant_id=f.tenant_id", "WHERE j.id=journal_ref AND j.tenant_id=f.tenant_id", 1)
	if strings.TrimSpace(original) != "SELECT EXISTS("+strings.TrimSpace(body)+")" {
		t.Fatal("historical CTE changed from static 017 predicate")
	}
}

func TestInvalidInputHasNoDatabaseAccess(t *testing.T) {
	for _, tenant := range []string{"", "tenant", uuid.Nil.String(), strings.ToUpper(uuid.NewString()), " " + uuid.NewString()} {
		out, err := Verify(context.Background(), nil, tenant)
		if !errors.Is(err, ErrInput) || out != (Check{}) {
			t.Fatal("nil pool or invalid scope did not return a zero check")
		}
	}
	if out, err := Verify(nil, nil, ""); !errors.Is(err, ErrInput) || out != (Check{}) {
		t.Fatal("nil context accepted")
	}
}

type fixture struct {
	ctx                                        context.Context
	owner, app                                 *pgxpool.Pool
	schema, tenant, operator, approver, credit string
}

func must(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed (%T)", step, err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for historical funding QA PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("historical funding QA requires the dedicated local moni_test database on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	must(t, "open dedicated administrator", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &fixture{ctx: ctx, schema: "funding019_" + suffix, tenant: uuid.NewString(), operator: uuid.NewString(), approver: uuid.NewString()}
	appRole, projectionRole := "f019_app_"+suffix, "f019_projection_"+suffix
	var roles []string
	t.Cleanup(func() {
		if f.app != nil {
			f.app.Close()
		}
		if f.owner != nil {
			f.owner.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_, e := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{f.schema}.Sanitize()+" CASCADE")
		if e != nil {
			t.Error("historical schema cleanup failed")
		}
		for _, role := range roles {
			_, e = admin.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize())
			if e != nil {
				t.Error("historical role cleanup failed")
			}
		}
		admin.Close()
		cancel()
	})
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{f.schema}.Sanitize())
	must(t, "create isolated schema", err)
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	f.owner, err = pgxpool.NewWithConfig(ctx, cfg.Copy())
	must(t, "open historical schema owner", err)
	must(t, "install exact 001-019", migrations.ApplyThrough(ctx, f.owner, Through))
	secret := make([]byte, 32)
	_, err = rand.Read(secret)
	must(t, "generate in-memory LOGIN secret", err)
	password := hex.EncodeToString(secret)
	for _, role := range []string{appRole, projectionRole} {
		_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'")
		must(t, "create isolated runtime LOGIN", err)
		roles = append(roles, role)
	}
	must(t, "provision actual historical APP", operations.ProvisionFor(ctx, f.owner, f.schema, appRole, projectionRole))
	appCfg := cfg.Copy()
	appCfg.ConnConfig.User, appCfg.ConnConfig.Password = appRole, password
	f.app, err = pgxpool.NewWithConfig(ctx, appCfg)
	must(t, "open actual historical APP LOGIN", err)
	var current, session string
	must(t, "attest actual APP LOGIN", f.app.QueryRow(ctx, `SELECT current_user,session_user`).Scan(&current, &session))
	if current != appRole || session != appRole {
		t.Fatal("historical APP was not an actual LOGIN")
	}
	f.exec(t, f.app, `INSERT INTO tenants(id,name) VALUES($1,'historical funding QA')`, f.tenant)
	for _, u := range []struct{ id, role string }{{f.operator, "finance_operator"}, {f.approver, "finance_approver"}} {
		f.exec(t, f.app, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture-only',$3)`, u.id, u.id+"@funding019.invalid", u.role)
	}
	store := billing.New(f.app)
	id := uuid.NewString()
	credit, err := store.CreateCredit(ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BusinessRef: "historical:" + id, ProofRef: "historical:verified", IdempotencyKey: id, RequestHMAC: id})
	must(t, "APP create complete funding source", err)
	approved, err := store.ApproveCredit(ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: credit.ID})
	must(t, "APP approve complete funding source", err)
	if approved.State != "approved" || approved.JournalID == "" {
		t.Fatal("incomplete historical funding seed")
	}
	f.credit = credit.ID
	return f
}

func (f *fixture) exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	_, err := pool.Exec(f.ctx, sql, args...)
	must(t, "historical isolated fixture statement", err)
}

func (f *fixture) snapshot(t *testing.T) string {
	t.Helper()
	var digest string
	must(t, "read financial snapshot", f.owner.QueryRow(f.ctx, `SELECT md5(jsonb_build_object(
 'accounts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM ledger_accounts x),
 'journals',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM journals x),
 'lines',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM journal_lines x),
 'funding',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM funding_receipts x),
 'credits',(SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM credit_orders x),
 'outbox',(SELECT jsonb_agg(to_jsonb(x) ORDER BY event_id) FROM projection_outbox x),
 'history',(SELECT jsonb_agg(to_jsonb(x) ORDER BY name) FROM schema_migrations x))::text)`).Scan(&digest))
	return digest
}

func TestPostgresHistoricalFundingActualAPPReadOnlyAndPeriodicPrototype(t *testing.T) {
	f := newFixture(t)
	f.exec(t, f.owner, `CREATE TABLE periodic_budget_qa_fixture(id text PRIMARY KEY)`)
	before := f.snapshot(t)
	for _, tenant := range []string{"", f.tenant, uuid.NewString()} {
		check, err := Verify(f.ctx, f.app, tenant)
		must(t, "historical independent A funding/ledger verification", err)
		if !check.Balanced {
			t.Fatal("complete historical A facts rejected")
		}
	}
	if f.snapshot(t) != before {
		t.Fatal("read-only historical verifier changed facts")
	}
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	if out, err := Verify(canceled, f.app, f.tenant); err == nil || out != (Check{}) {
		t.Fatal("canceled check returned facts")
	}
	for _, tenant := range []string{uuid.Nil.String(), strings.ToUpper(f.tenant), " " + f.tenant, "tenant"} {
		if out, err := Verify(f.ctx, f.app, tenant); !errors.Is(err, ErrInput) || out != (Check{}) {
			t.Fatal("noncanonical tenant accepted")
		}
	}
}

func TestPostgresHistoricalFundingRejectsNon019OrPartiallyUpgradedBaseline(t *testing.T) {
	f := newFixture(t)
	original, err := migrations.Files.ReadFile("017_funding_vouchers.sql")
	must(t, "load static historical function restore", err)
	_, tail, ok := strings.Cut(string(original), "CREATE FUNCTION funding_journal_valid(journal_ref TEXT)")
	if !ok {
		t.Fatal("missing static function")
	}
	definition, _, ok := strings.Cut(tail, "$$;")
	if !ok {
		t.Fatal("missing static function boundary")
	}
	functionRestore := "CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref TEXT)" + definition + "$$;"
	var sum string
	must(t, "read original historical checksum", f.owner.QueryRow(f.ctx, `SELECT checksum FROM schema_migrations WHERE name=$1`, Through).Scan(&sum))
	tests := []struct {
		name, damage, restore string
		args                  []any
	}{
		{"missing migration", `DELETE FROM schema_migrations WHERE name=$1`, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, []any{Through, sum}},
		{"unknown migration", `INSERT INTO schema_migrations(name,checksum) VALUES('999_unknown.sql','bad')`, `DELETE FROM schema_migrations WHERE name='999_unknown.sql'`, nil},
		{"known future migration", `INSERT INTO schema_migrations(name,checksum) VALUES('020_periodic_budgets.sql','bad')`, `DELETE FROM schema_migrations WHERE name='020_periodic_budgets.sql'`, nil},
		{"checksum mismatch", `UPDATE schema_migrations SET checksum='bad' WHERE name=$1`, `UPDATE schema_migrations SET checksum=$2 WHERE name=$1`, []any{Through, sum}},
		{"payment table", `CREATE TABLE payment_qa_fixture(id text)`, `DROP TABLE payment_qa_fixture`, nil},
		{"payment function", `CREATE FUNCTION payment_qa_fixture() RETURNS boolean LANGUAGE sql AS $$ SELECT true $$`, `DROP FUNCTION payment_qa_fixture()`, nil},
		{"replaced historical predicate", `CREATE OR REPLACE FUNCTION funding_journal_valid(journal_ref TEXT) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$`, functionRestore, nil},
		{"historical security definer", `ALTER FUNCTION funding_journal_valid(text) SECURITY DEFINER`, `ALTER FUNCTION funding_journal_valid(text) SECURITY INVOKER`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Damage statements use only their required bind parameters.
			args := tc.args
			if tc.name == "missing migration" || tc.name == "checksum mismatch" {
				args = args[:1]
			}
			f.exec(t, f.owner, tc.damage, args...)
			defer f.exec(t, f.owner, tc.restore, tc.args...)
			before := f.snapshot(t)
			out, err := Verify(f.ctx, f.app, "")
			if !errors.Is(err, ErrBaseline) || out != (Check{}) {
				t.Fatal("incompatible historical baseline accepted")
			}
			if f.snapshot(t) != before {
				t.Fatal("rejected historical check changed facts")
			}
		})
	}
	check, err := Verify(f.ctx, f.app, "")
	must(t, "restored baseline verification", err)
	if !check.Balanced {
		t.Fatal("restored historical baseline not balanced")
	}
}

func TestPostgresHistoricalFundingNumericLedgerAndCorruptionDetection(t *testing.T) {
	f := newFixture(t)
	var available, revenue string
	must(t, "find historical AVAILABLE", f.owner.QueryRow(f.ctx, `SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'`, f.tenant).Scan(&available))
	must(t, "create isolated REVENUE account", f.owner.QueryRow(f.ctx, `INSERT INTO ledger_accounts(kind) VALUES('REVENUE') RETURNING id`).Scan(&revenue))
	tx, err := f.owner.Begin(f.ctx)
	must(t, "begin explicit owner numeric fixture", err)
	defer tx.Rollback(f.ctx)
	delta := int64(math.MaxInt64 - 1000)
	for _, amount := range []int64{delta, delta, -delta} {
		journal := uuid.NewString()
		_, err = tx.Exec(f.ctx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$1,'qa_numeric',$1,'isolated-owner-fixture')`, journal, f.tenant)
		must(t, "insert isolated numeric journal", err)
		_, err = tx.Exec(f.ctx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$4),($1,$3,-$4::bigint)`, journal, available, revenue, amount)
		must(t, "insert balanced isolated numeric lines", err)
	}
	_, err = tx.Exec(f.ctx, `UPDATE ledger_accounts SET balance=$2 WHERE id=$1`, available, int64(math.MaxInt64))
	must(t, "align isolated AVAILABLE cache", err)
	_, err = tx.Exec(f.ctx, `UPDATE ledger_accounts SET balance=$2 WHERE id=$1`, revenue, -delta)
	must(t, "align isolated REVENUE cache", err)
	must(t, "commit balanced numeric fixture", tx.Commit(f.ctx))
	var exceeds bool
	must(t, "prove historical aggregate exceeds int64", f.owner.QueryRow(f.ctx, `SELECT sum(abs(signed_delta::numeric))>9223372036854775807::numeric FROM journal_lines WHERE account_id=$1`, available).Scan(&exceeds))
	if !exceeds {
		t.Fatal("numeric fixture did not exceed int64 aggregate")
	}
	for _, tenant := range []string{"", f.tenant} {
		check, err := Verify(f.ctx, f.app, tenant)
		must(t, "numeric historical ledger verification", err)
		if !check.Balanced {
			t.Fatal("valid numeric historical ledger rejected")
		}
	}
	f.exec(t, f.owner, `UPDATE ledger_accounts SET balance=balance-1 WHERE id=$1`, available)
	check, err := Verify(f.ctx, f.app, "")
	must(t, "detect historical balance corruption", err)
	if check.Balanced || check.AccountMismatches != 1 {
		t.Fatal("historical account corruption missed")
	}
	f.exec(t, f.owner, `UPDATE ledger_accounts SET balance=balance+1 WHERE id=$1`, available)
	f.exec(t, f.owner, `ALTER TABLE projection_outbox DISABLE TRIGGER USER`)
	f.exec(t, f.owner, `UPDATE projection_outbox SET sanitized_payload='{"amount":"999"}'::jsonb WHERE operation_id=$1 AND kind='credited'`, f.credit)
	f.exec(t, f.owner, `ALTER TABLE projection_outbox ENABLE TRIGGER USER`)
	check, err = Verify(f.ctx, f.app, f.tenant)
	must(t, "detect historical source/outbox corruption", err)
	if check.Balanced || check.JournalMismatches == 0 || check.OutboxMismatches == 0 || check.ClearingMismatches == 0 {
		t.Fatal("historical A bidirectional corruption missed")
	}
	f.exec(t, f.owner, `ALTER TABLE projection_outbox DISABLE TRIGGER USER`)
	f.exec(t, f.owner, `UPDATE projection_outbox SET sanitized_payload='{"amount":"1000"}'::jsonb WHERE operation_id=$1 AND kind='credited'`, f.credit)
	f.exec(t, f.owner, `ALTER TABLE projection_outbox ENABLE TRIGGER USER`)
	check, err = Verify(f.ctx, f.app, "")
	must(t, "final restored historical verification", err)
	if !check.Balanced {
		t.Fatal("restored historical facts not balanced")
	}
}
