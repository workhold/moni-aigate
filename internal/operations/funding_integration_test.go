package operations

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/migrations"
)

// Funding tests only operate in randomly named schemas of the dedicated local
// database. A developer's serving instance is never a valid fixture target.
func fundingTestDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated funding PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("funding tests require the dedicated local moni_test database on port 55439")
	}
	return raw
}

type fundingUpgradeFixture struct {
	ctx   context.Context
	owner *pgxpool.Pool
}

func fundingPreMigrationDB(t *testing.T, last string) *fundingUpgradeFixture {
	t.Helper()
	raw := fundingTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	schema := "funding_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		admin.Close()
		cancel()
		t.Fatal(err)
	}
	cfg := admin.Config()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	owner, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		owner.Close()
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("funding upgrade schema cleanup failed: %v", err)
		}
		admin.Close()
		cancel()
	})
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	for _, name := range names {
		if name[:3] > last {
			break
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, string(body)); err != nil {
			t.Fatalf("pre-funding migration %s: %v", name, err)
		}
	}
	return &fundingUpgradeFixture{ctx: ctx, owner: owner}
}

func fundingApplyMigration(f *fundingUpgradeFixture, name string) error {
	body, err := migrations.Files.ReadFile(name)
	if err != nil {
		return err
	}
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, string(body)); err != nil {
		return err
	}
	return tx.Commit(f.ctx)
}

type fundingLegacyCredit struct {
	tenant, credit, pending, journal string
	amount                           int64
}

// Seed the old SQL contract directly. Calling ApproveCredit here would exercise
// the new implementation before its schema exists and miss the actual upgrade.
func fundingSeedLegacy(t *testing.T, f *fundingUpgradeFixture, defect string) fundingLegacyCredit {
	t.Helper()
	l := fundingLegacyCredit{tenant: uuid.NewString(), credit: "legacy-bank-credit", pending: "legacy-pending-credit", journal: uuid.NewString(), amount: 1000}
	maker, checker, otherTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	available, clearing := uuid.NewString(), uuid.NewString()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(f.ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO tenants(id,name) VALUES($1,'legacy-funding'),($2,'other-legacy-funding')`, l.tenant, otherTenant)
	exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'legacy-hash','finance_operator'),($3,$4,'legacy-hash','finance_approver')`, maker, maker+"@funding.invalid", checker, checker+"@funding.invalid")
	lineAmount := l.amount
	if defect == "amount_mismatch" {
		lineAmount--
	}
	exec(`INSERT INTO ledger_accounts(id,tenant_id,kind,balance) VALUES($1,$2,'AVAILABLE',$3),($4,NULL,'FUNDING_CLEARING',-$3)`, available, l.tenant, lineAmount, clearing)
	operation, reference, kind := l.credit, l.credit, "credit"
	if defect == "wrong_operation" {
		operation = "other-credit-operation"
	}
	if defect == "wrong_reference" {
		reference = "other-credit-source"
	}
	if defect == "wrong_journal_kind" {
		kind = "refund"
	}
	if defect == "non_credit_clearing_without_source" {
		kind = "legacy_adjustment"
	}
	exec(`INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor,created_at) VALUES($1,$2,$3,$4,$5,$6,'2025-01-02T03:04:05.123456Z')`, l.journal, l.tenant, operation, kind, reference, checker)
	exec(`INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3),($1,$4,-$3)`, l.journal, available, lineAmount, clearing)
	creditTenant := l.tenant
	if defect == "wrong_credit_tenant" {
		creditTenant = otherTenant
	}
	var approvedBy any = checker
	if defect == "missing_approver" {
		approvedBy = nil
	}
	if defect != "orphan_credit_journal" && defect != "non_credit_clearing_without_source" {
		exec(`INSERT INTO credit_orders(id,tenant_id,business_ref,amount,state,requester,approver,proof_ref,journal_id) VALUES($1,$2,'legacy:approved',$3,'approved',$4,$5,'proof:legacy-approved',$6)`, l.credit, creditTenant, l.amount, maker, approvedBy, l.journal)
	}
	exec(`INSERT INTO credit_orders(id,tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,'legacy:pending',37,$3,'proof:legacy-pending')`, l.pending, l.tenant, maker)
	if defect == "pending_has_journal" {
		exec(`UPDATE credit_orders SET journal_id=$2 WHERE id=$1`, l.pending, l.journal)
	}
	if defect != "missing_credited" && defect != "non_credit_clearing_without_source" {
		payload := `{"amount":"1000"}`
		if defect == "wrong_credited_amount" {
			payload = `{"amount":"999"}`
		}
		exec(`INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited',$3::jsonb)`, l.tenant, operation, payload)
	}
	if defect == "orphan_credited" || defect == "pending_has_credited" {
		orphanOperation := "orphan-credit-operation"
		if defect == "pending_has_credited" {
			orphanOperation = l.pending
		}
		exec(`INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited','{"amount":"37"}')`, l.tenant, orphanOperation)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatalf("old SQL must admit the legacy fixture before strict upgrade: %v", err)
	}
	return l
}

func fundingHistorySnapshot(t *testing.T, f *fundingUpgradeFixture) string {
	t.Helper()
	var snapshot string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id COLLATE "C") FROM ledger_accounts a),'[]'::jsonb),
 'credits',coalesce((SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id COLLATE "C") FROM credit_orders c),'[]'::jsonb),
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(j) ORDER BY j.id COLLATE "C") FROM journals j),'[]'::jsonb),
 'lines',coalesce((SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id COLLATE "C") FROM journal_lines l),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(o) ORDER BY o.event_id COLLATE "C") FROM projection_outbox o),'[]'::jsonb)
)::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestFundingMigrationBackfillsReceiptsWithoutRewritingMoney(t *testing.T) {
	for _, last := range []string{"015", "016"} {
		t.Run("from_"+last, func(t *testing.T) {
			f := fundingPreMigrationDB(t, last)
			legacy := fundingSeedLegacy(t, f, "")
			before := fundingHistorySnapshot(t, f)
			if last == "015" {
				if err := fundingApplyMigration(f, "016_self_service_identity.sql"); err != nil {
					t.Fatal(err)
				}
			}
			if err := fundingApplyMigration(f, "017_funding_vouchers.sql"); err != nil {
				t.Fatal(err)
			}
			if after := fundingHistorySnapshot(t, f); after != before {
				t.Fatal("funding upgrade rewrote original credit, journal, account, line or outbox facts")
			}
			var count int
			if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("expected exactly the approved source receipt: count=%d err=%v", count, err)
			}
			var tenant, kind, source, journal, currency string
			var amount int64
			var sameTime bool
			if err := f.owner.QueryRow(f.ctx, `SELECT r.tenant_id,r.source_kind,r.source_id,r.amount,r.currency,r.journal_id,r.created_at=j.created_at FROM funding_receipts r JOIN journals j ON j.id=r.journal_id`).Scan(&tenant, &kind, &source, &amount, &currency, &journal, &sameTime); err != nil || tenant != legacy.tenant || kind != "manual_credit" || source != legacy.credit || amount != legacy.amount || currency != "CNY" || journal != legacy.journal || !sameTime {
				t.Fatalf("legacy receipt did not preserve source, journal or observation time: %s %s %s %d %s %s %v err=%v", tenant, kind, source, amount, currency, journal, sameTime, err)
			}
			if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts WHERE source_id=$1`, legacy.pending).Scan(&count); err != nil || count != 0 {
				t.Fatalf("pending credit received funding: count=%d err=%v", count, err)
			}
		})
	}
}

func TestFundingMigrationRejectsBrokenLegacyFactsAtomically(t *testing.T) {
	for _, defect := range []string{
		"amount_mismatch", "wrong_credit_tenant", "wrong_operation", "wrong_reference", "wrong_journal_kind", "missing_approver",
		"missing_credited", "wrong_credited_amount", "orphan_credit_journal", "orphan_credited", "pending_has_journal", "pending_has_credited", "non_credit_clearing_without_source",
	} {
		t.Run(defect, func(t *testing.T) {
			f := fundingPreMigrationDB(t, "016")
			fundingSeedLegacy(t, f, defect)
			before := fundingHistorySnapshot(t, f)
			err := fundingApplyMigration(f, "017_funding_vouchers.sql")
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) {
				t.Fatalf("strict migration must reject broken retained facts in PostgreSQL: %v", err)
			}
			if after := fundingHistorySnapshot(t, f); after != before {
				t.Fatal("failed migration changed retained money or financial evidence")
			}
			var absent bool
			if err := f.owner.QueryRow(f.ctx, `SELECT to_regclass('funding_receipts') IS NULL`).Scan(&absent); err != nil || !absent {
				t.Fatalf("failed migration left a partial funding schema: absent=%v err=%v", absent, err)
			}
		})
	}
}

func fundingOperationsDB(t *testing.T) *operationFixture {
	t.Helper()
	fundingTestDatabase(t)
	return operationDB(t)
}

func fundingApproveCredit(t *testing.T, f *operationFixture, suffix string) billing.CreditOrder {
	t.Helper()
	s := billing.New(f.app)
	credit, err := s.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BusinessRef: "bank:funding:" + suffix, ProofRef: "proof:funding:" + suffix, IdempotencyKey: "funding:" + suffix, RequestHMAC: "funding-hmac:" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := s.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: credit.ID})
	if err != nil || approved.State != "approved" || approved.JournalID == "" {
		t.Fatalf("real app role could not approve funding: %+v %v", approved, err)
	}
	return approved
}

func fundingRequireSQLState(t *testing.T, err error, states ...string) {
	t.Helper()
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) {
		t.Fatalf("expected PostgreSQL rejection, got %v", err)
	}
	for _, state := range states {
		if databaseError.Code == state {
			return
		}
	}
	t.Fatalf("expected SQLSTATE %v, got %s: %v", states, databaseError.Code, err)
}

func TestFundingProvisionedRolesKeepNewTablesLeastPrivilege(t *testing.T) {
	f := fundingOperationsDB(t)
	// Provisioning starts with blanket grants. Re-running it must also remove
	// those grants again, preserving only the intended column-level writes.
	for i := 0; i < 2; i++ {
		if err := ProvisionFor(f.ctx, f.owner, f.schema, f.appRole, f.projectionRole); err != nil {
			t.Fatalf("repeated provisioning failed: %v", err)
		}
	}
	for _, table := range []string{"funding_receipts", "voucher_policy", "commerce_commands", "commerce_operation_receipts", "voucher_batches", "vouchers", "voucher_revocations", "voucher_revocation_decisions", "voucher_redemptions"} {
		t.Run(table, func(t *testing.T) {
			var canRead, canInsert, canUpdate, canDelete bool
			if err := f.app.QueryRow(f.ctx, `SELECT has_table_privilege(current_user,$1,'SELECT'),has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'UPDATE'),has_table_privilege(current_user,$1,'DELETE')`, table).Scan(&canRead, &canInsert, &canUpdate, &canDelete); err != nil || !canRead || canInsert != (table != "voucher_policy") || canUpdate || canDelete {
				t.Fatalf("unsafe app table grant for %s: select=%v insert=%v update=%v delete=%v err=%v", table, canRead, canInsert, canUpdate, canDelete, err)
			}
			for _, sql := range []string{"SELECT * FROM " + pgx.Identifier{table}.Sanitize(), "DELETE FROM " + pgx.Identifier{table}.Sanitize()} {
				_, err := f.projection.Exec(f.ctx, sql)
				fundingRequireSQLState(t, err, "42501")
			}
			_, err := f.app.Exec(f.ctx, "DELETE FROM "+pgx.Identifier{table}.Sanitize())
			fundingRequireSQLState(t, err, "42501")
		})
	}
	for table, allowed := range map[string]map[string]bool{
		"voucher_policy":  {"enabled": true},
		"voucher_batches": {"state": true, "approver": true, "activated_at": true, "terminal_actor": true, "terminal_at": true, "redeemed_count": true},
		"credit_orders":   {"state": true, "approver": true, "journal_id": true},
	} {
		rows, err := f.owner.Query(f.ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for rows.Next() {
			var column string
			if err = rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			columns = append(columns, column)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, column := range columns {
			var canUpdate bool
			if err = f.app.QueryRow(f.ctx, `SELECT has_column_privilege(current_user,$1,$2,'UPDATE')`, table, column).Scan(&canUpdate); err != nil || canUpdate != allowed[column] {
				t.Fatalf("unexpected column grant %s.%s: allowed=%v want=%v err=%v", table, column, canUpdate, allowed[column], err)
			}
		}
	}
	for _, sql := range []string{`CREATE TABLE funding_app_ddl(id int)`, `INSERT INTO voucher_policy(singleton) VALUES(true)`} {
		_, err := f.app.Exec(f.ctx, sql)
		fundingRequireSQLState(t, err, "42501")
	}
	var revision int64
	for _, step := range []struct {
		enabled bool
		want    int64
	}{{false, 1}, {true, 2}, {true, 2}, {false, 3}} {
		if err := f.app.QueryRow(f.ctx, `UPDATE voucher_policy SET enabled=$1 WHERE singleton RETURNING revision`, step.enabled).Scan(&revision); err != nil || revision != step.want {
			t.Fatalf("allowed policy mutation or trigger revision failed: revision=%d want=%d err=%v", revision, step.want, err)
		}
	}
	_, err := f.app.Exec(f.ctx, `UPDATE voucher_policy SET revision=999 WHERE singleton`)
	fundingRequireSQLState(t, err, "42501")
}

func TestFundingProvisionedManualCreditIsUniqueAndProjectable(t *testing.T) {
	f := fundingOperationsDB(t)
	approved := fundingApproveCredit(t, f, "approved")
	s := billing.New(f.app)
	replayed, err := s.ApproveCredit(f.ctx, billing.ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: approved.ID})
	if err != nil || replayed.JournalID != approved.JournalID || replayed.Approver != approved.Approver {
		t.Fatalf("repeat approval changed the original funding: %+v %v", replayed, err)
	}
	var receipts, journals, credited, audits int
	var available int64
	if err = f.app.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM funding_receipts WHERE tenant_id=$1 AND source_kind='manual_credit' AND source_id=$2 AND journal_id=$3 AND amount=1000 AND currency='COIN'),
 (SELECT count(*) FROM journals WHERE operation_id=$2),
 (SELECT count(*) FROM projection_outbox WHERE operation_id=$2 AND kind='credited' AND schema_version=1 AND sanitized_payload='{"amount":"1000"}'::jsonb),
 (SELECT count(*) FROM audit_events WHERE action='credit.approved' AND target=$2),
 (SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE')`, f.tenant, approved.ID, approved.JournalID).Scan(&receipts, &journals, &credited, &audits, &available); err != nil || receipts != 1 || journals != 1 || credited != 1 || audits != 1 || available != 1000 {
		t.Fatalf("funding was incomplete or repeated: receipts=%d journals=%d credited=%d audits=%d available=%d err=%v", receipts, journals, credited, audits, available, err)
	}
	if _, err = billing.New(f.projection).Project(f.ctx, billing.ProjectInput{}); err != nil {
		t.Fatalf("unchanged projector privileges cannot consume credited: %v", err)
	}
	report, err := s.Report(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1000" {
		t.Fatalf("reports-v1 did not retain credited meaning: %+v %v", report, err)
	}
	check, err := s.VerifyLedger(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || !check.Balanced {
		t.Fatalf("manual funding failed integrated ledger verification: %+v %v", check, err)
	}
	for _, sql := range []string{
		`UPDATE credit_orders SET amount=amount+1 WHERE id=$1`,
		`UPDATE credit_orders SET proof_ref='proof:forged' WHERE id=$1`,
		`DELETE FROM credit_orders WHERE id=$1`,
		`UPDATE funding_receipts SET amount=amount+1 WHERE source_id=$1`,
		`DELETE FROM funding_receipts WHERE source_id=$1`,
	} {
		_, err = f.app.Exec(f.ctx, sql, approved.ID)
		fundingRequireSQLState(t, err, "42501")
		_, err = f.owner.Exec(f.ctx, sql, approved.ID)
		fundingRequireSQLState(t, err, "P0001")
	}
	_, err = f.app.Exec(f.ctx, `UPDATE credit_orders SET state='pending',approver=NULL,journal_id=NULL WHERE id=$1`, approved.ID)
	fundingRequireSQLState(t, err, "P0001")
	if _, err = s.Rebuild(f.ctx, "funding-rebuild"); err != nil {
		t.Fatalf("maintenance rebuild cannot validate source receipts and old events: %v", err)
	}
	report, err = s.Report(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1000" {
		t.Fatalf("receipt-aware rebuild changed cumulative credited: %+v %v", report, err)
	}
}

func TestFundingOrphanCreditedCannotCommit(t *testing.T) {
	f := fundingOperationsDB(t)
	for _, pool := range []*pgxpool.Pool{f.app, f.owner} {
		tx, err := pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		operation := uuid.NewString()
		_, err = tx.Exec(f.ctx, `INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited','{"amount":"7"}')`, f.tenant, operation)
		if err != nil {
			_ = tx.Rollback(f.ctx)
			t.Fatalf("credited consistency must be checked against the complete transaction: %v", err)
		}
		err = tx.Commit(f.ctx)
		fundingRequireSQLState(t, err, "P0001", "23514")
		var count int
		if err = f.owner.QueryRow(f.ctx, `SELECT count(*) FROM projection_outbox WHERE operation_id=$1`, operation).Scan(&count); err != nil || count != 0 {
			t.Fatalf("orphan event survived failed commit: %d %v", count, err)
		}
	}
	if _, err := billing.New(f.projection).Project(f.ctx, billing.ProjectInput{}); err != nil {
		t.Fatal(err)
	}
	report, err := billing.New(f.app).Report(f.ctx, billing.Query{TenantID: f.tenant})
	if err != nil || report.Credited != "0" {
		t.Fatalf("rejected orphan credited changed reporting: %+v %v", report, err)
	}
}

func TestFundingPendingSourceAndIncompleteJournalCannotCommit(t *testing.T) {
	f := fundingOperationsDB(t)
	s := billing.New(f.app)
	credit, err := s.CreateCredit(f.ctx, billing.CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: 1000, BusinessRef: "bank:pending-funding", ProofRef: "proof:pending-funding", IdempotencyKey: "pending-funding", RequestHMAC: "pending-funding-hmac"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`UPDATE credit_orders SET id='forged-source-id' WHERE id=$1`,
		`UPDATE credit_orders SET tenant_id=NULL WHERE id=$1`,
		`UPDATE credit_orders SET business_ref='bank:forged' WHERE id=$1`,
		`UPDATE credit_orders SET amount=amount+1 WHERE id=$1`,
		`UPDATE credit_orders SET requester=NULL WHERE id=$1`,
		`UPDATE credit_orders SET proof_ref='proof:forged' WHERE id=$1`,
		`UPDATE credit_orders SET created_at=created_at+interval '1 second' WHERE id=$1`,
		`DELETE FROM credit_orders WHERE id=$1`,
	} {
		_, err = f.app.Exec(f.ctx, sql, credit.ID)
		fundingRequireSQLState(t, err, "42501")
		_, err = f.owner.Exec(f.ctx, sql, credit.ID)
		fundingRequireSQLState(t, err, "P0001")
	}
	for _, sourceExists := range []bool{false, true} {
		name := "orphan_journal"
		if sourceExists {
			name = "approved_without_receipt"
		}
		t.Run(name, func(t *testing.T) {
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(f.ctx)
			journal, available, clearing, operation := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			if sourceExists {
				operation = credit.ID
			}
			for _, statement := range []struct {
				sql  string
				args []any
			}{
				{`INSERT INTO ledger_accounts(id,tenant_id,kind,balance) VALUES($1,$2,'AVAILABLE',1000),($3,NULL,'FUNDING_CLEARING',-1000)`, []any{available, f.tenant, clearing}},
				{`INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,'credit',$3,$4)`, []any{journal, f.tenant, operation, f.approver}},
				{`INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,1000),($1,$3,-1000)`, []any{journal, available, clearing}},
			} {
				if _, err = tx.Exec(f.ctx, statement.sql, statement.args...); err != nil {
					t.Fatalf("balanced same-transaction journal fixture rejected before deferred source check: %v", err)
				}
			}
			if sourceExists {
				if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, credit.ID, f.approver, journal); err != nil {
					t.Fatalf("approval shape must be checked with all funding facts at commit: %v", err)
				}
			}
			err = tx.Commit(f.ctx)
			fundingRequireSQLState(t, err, "P0001", "23514")
			var state string
			var accounts, journals, receipts int
			if err = f.app.QueryRow(f.ctx, `SELECT
 (SELECT state FROM credit_orders WHERE id=$1),
 (SELECT count(*) FROM ledger_accounts),
 (SELECT count(*) FROM journals),
 (SELECT count(*) FROM funding_receipts)`, credit.ID).Scan(&state, &accounts, &journals, &receipts); err != nil || state != "pending" || accounts != 0 || journals != 0 || receipts != 0 {
				t.Fatalf("failed funding commit retained partial money/source: state=%s accounts=%d journals=%d receipts=%d err=%v", state, accounts, journals, receipts, err)
			}
		})
	}
}

func TestFundingVoidedBatchRequiresNonNullTerminalActor(t *testing.T) {
	f := fundingOperationsDB(t)
	batch, voucher, createOperation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	writeCommand := func(tx pgx.Tx, operation, kind string) {
		t.Helper()
		if _, err := tx.Exec(f.ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest) VALUES($1,$2,'platform',$3,$4)`, operation, f.operator, kind, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(f.ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id) VALUES($1,$2,'platform',$3,'accepted','accepted',$4)`, operation, kind, f.operator, batch); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `INSERT INTO voucher_batches(id,face_amount,count,expires_at,purpose_ref,reason,maker) VALUES($1,7,1,clock_timestamp()+interval '2 hours','purpose:shape-check','shape fixture',$2)`, batch, f.operator); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO vouchers(id,batch_id,code_digest,digest_key_version) VALUES($1,$2,$3,'fixture')`, voucher, batch, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	writeCommand(tx, createOperation, "voucher.batch.create")
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatalf("complete proposed batch fixture could not commit: %v", err)
	}

	for _, invalid := range []bool{true, false} {
		voidOperation := uuid.NewString()
		tx, err := f.app.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		// Both commands and receipts are present before the UPDATE, so an
		// incomplete command cannot hide a regression in the state CHECK.
		writeCommand(tx, voidOperation, "voucher.batch.void")
		var terminalActor any = f.operator
		if invalid {
			terminalActor = nil
		}
		_, err = tx.Exec(f.ctx, `UPDATE voucher_batches SET state='voided',terminal_actor=$2,terminal_at=clock_timestamp() WHERE id=$1`, batch, terminalActor)
		if invalid {
			fundingRequireSQLState(t, err, "23514")
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.TableName != "voucher_batches" {
				t.Fatalf("NULL terminal actor was rejected outside the batch shape CHECK: %v", err)
			}
			if err = tx.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			var state string
			var revision int64
			var commands int
			if err = f.app.QueryRow(f.ctx, `SELECT state,revision,(SELECT count(*) FROM commerce_commands WHERE operation_id=$2) FROM voucher_batches WHERE id=$1`, batch, voidOperation).Scan(&state, &revision, &commands); err != nil || state != "proposed" || revision != 1 || commands != 0 {
				t.Fatalf("rejected NULL actor left partial state: state=%s revision=%d commands=%d err=%v", state, revision, commands, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("valid terminal actor was rejected: %v", err)
		}
		if err = tx.Commit(f.ctx); err != nil {
			t.Fatalf("complete batch void facts could not commit: %v", err)
		}
		var state, actor string
		var revision int64
		if err = f.app.QueryRow(f.ctx, `SELECT state,terminal_actor,revision FROM voucher_batches WHERE id=$1`, batch).Scan(&state, &actor, &revision); err != nil || state != "voided" || actor != f.operator || revision != 2 {
			t.Fatalf("valid batch void did not preserve its actor/state: state=%s actor=%s revision=%d err=%v", state, actor, revision, err)
		}
	}
}
