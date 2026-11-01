package billing

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func fundingFixture(t *testing.T) *fixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated funding PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("funding tests require the dedicated local moni_test database on port 55439")
	}
	return integration(t)
}

func fundingCredit(t *testing.T, f *fixture, tenant string, amount Money) CreditOrder {
	t.Helper()
	id := uuid.NewString()
	o, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: tenant, ActorID: f.operator, BusinessRef: "funding:" + id, ProofRef: "funding:verified", Amount: amount, IdempotencyKey: id, RequestHMAC: id})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func fundingApprove(t *testing.T, f *fixture, o CreditOrder) CreditOrder {
	t.Helper()
	approved, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: o.TenantID, OrderID: o.ID, ActorID: f.approver})
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

// One statement makes failure comparisons independent of inter-statement
// snapshots. No generated IDs, timestamps, or financial facts are omitted.
func fundingSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),'[]'::jsonb),
 'credits',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM credit_orders x),'[]'::jsonb),
 'funding',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_receipts x),'[]'::jsonb),
	'commands',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.operation_id) FROM commerce_commands x),'[]'::jsonb),
	'terminals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.operation_id) FROM commerce_operation_receipts x),'[]'::jsonb),
	'batches',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM voucher_batches x),'[]'::jsonb),
	'vouchers',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM vouchers x),'[]'::jsonb),
	'redemptions',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM voucher_redemptions x),'[]'::jsonb),
	'proposals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM voucher_revocations x),'[]'::jsonb),
	'decisions',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM voucher_revocation_decisions x),'[]'::jsonb),
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x),'[]'::jsonb),
 'lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journal_lines x),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.event_id) FROM projection_outbox x),'[]'::jsonb),
 'audit',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM audit_events x),'[]'::jsonb))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func fundingCheck(t *testing.T, f *fixture, tenant string) {
	t.Helper()
	check, err := f.store.VerifyFunding(f.ctx, tenant)
	if err != nil || !check.Balanced || check.MissingReceipts != 0 || check.OrphanReceipts != 0 || check.SourceMismatches != 0 || check.JournalMismatches != 0 || check.OutboxMismatches != 0 || check.RedemptionMismatches != 0 || check.BatchCountMismatches != 0 || check.ClearingMismatches != 0 {
		t.Fatalf("funding source verification: %+v %v", check, err)
	}
	f.check(t)
}

func TestPostgresFundingManualConcurrentApprovalHasOneReceiptAndCreditedEvent(t *testing.T) {
	f := fundingFixture(t)
	o := fundingCredit(t, f, f.tenant, 37)
	const workers = 8
	start := make(chan struct{})
	results := make(chan CreditOrder, workers)
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver})
			results <- v
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var journal string
	for v := range results {
		if v.ID != o.ID || v.State != "approved" || v.Approver != f.approver || v.JournalID == "" || (journal != "" && journal != v.JournalID) {
			t.Fatalf("concurrent approval changed its original financial result: %+v", v)
		}
		journal = v.JournalID
	}
	var receipts, journals, events, audits, lines int
	var amount Money
	var currency, sourceKind, sourceID, receiptJournal, payload, operation, reference, kind string
	err := f.pool.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM funding_receipts WHERE source_kind='manual_credit' AND source_id=$1),
 (SELECT count(*) FROM journals WHERE operation_id=$1),
 (SELECT count(*) FROM projection_outbox WHERE operation_id=$1 AND kind='credited'),
 (SELECT count(*) FROM audit_events WHERE action='credit.approved' AND target=$1),
 (SELECT count(*) FROM journal_lines WHERE journal_id=$2),
 r.amount,r.currency,r.source_kind,r.source_id,r.journal_id,
 (SELECT sanitized_payload->>'amount' FROM projection_outbox WHERE operation_id=$1 AND kind='credited'),
 j.operation_id,j.reference,j.kind
 FROM funding_receipts r JOIN journals j ON j.id=r.journal_id
 WHERE r.source_kind='manual_credit' AND r.source_id=$1`, o.ID, journal).Scan(&receipts, &journals, &events, &audits, &lines, &amount, &currency, &sourceKind, &sourceID, &receiptJournal, &payload, &operation, &reference, &kind)
	if err != nil || receipts != 1 || journals != 1 || events != 1 || audits != 1 || lines != 2 || amount != 37 || currency != "CNY" || sourceKind != "manual_credit" || sourceID != o.ID || receiptJournal != journal || payload != "37" || operation != o.ID || reference != o.ID || kind != "credit" {
		t.Fatalf("manual source/receipt/journal/outbox ownership differs: receipts=%d journals=%d events=%d audits=%d lines=%d amount=%v currency=%s source=%s/%s journal=%s payload=%s operation=%s reference=%s kind=%s err=%v", receipts, journals, events, audits, lines, amount, currency, sourceKind, sourceID, receiptJournal, payload, operation, reference, kind, err)
	}
	f.wallet(t, 1037, 0)
	before := fundingSnapshot(t, f)
	fundingApprove(t, f, o)
	if fundingSnapshot(t, f) != before {
		t.Fatal("repeated approval rewrote or duplicated a committed financial fact")
	}
	fundingCheck(t, f, "")
	fundingCheck(t, f, f.tenant)
}

func TestPostgresFundingManualAuditAndOutboxFailuresRollbackAllFacts(t *testing.T) {
	for _, table := range []string{"audit_events", "projection_outbox", "funding_receipts"} {
		t.Run(table, func(t *testing.T) {
			f := fundingFixture(t)
			o := fundingCredit(t, f, f.tenant, 29)
			before := fundingSnapshot(t, f)
			if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION funding_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'funding fixture write rejected'; END $$`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "CREATE TRIGGER funding_injected_failure BEFORE INSERT ON "+pgx.Identifier{table}.Sanitize()+" FOR EACH ROW EXECUTE FUNCTION funding_injected_failure()"); err != nil {
				t.Fatal(err)
			}
			_, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver})
			var dbErr *pgconn.PgError
			if !errors.As(err, &dbErr) || dbErr.Code != "P0001" {
				t.Fatalf("infrastructure error lost or converted into business rejection: %v", err)
			}
			if fundingSnapshot(t, f) != before {
				t.Fatal("failed funding approval committed source, balance, journal, receipt, audit or outbox changes")
			}
			if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER funding_injected_failure ON "+pgx.Identifier{table}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			fundingApprove(t, f, o)
			f.wallet(t, 1029, 0)
			fundingCheck(t, f, "")
		})
	}
}

func TestPostgresFundingManualAvailableAndClearingBIGINTBoundaries(t *testing.T) {
	f := fundingFixture(t)
	for _, amount := range []Money{0, -1, Money(math.MinInt64)} {
		key := uuid.NewString()
		_, err := f.store.CreateCredit(f.ctx, CreditInput{TenantID: f.tenant, ActorID: f.operator, Amount: amount, BusinessRef: key, ProofRef: "funding:verified", IdempotencyKey: key, RequestHMAC: key})
		assertCode(t, err, "invalid_credit")
	}
	maximum := fundingCredit(t, f, f.tenant, Money(math.MaxInt64-1000))
	fundingApprove(t, f, maximum)
	f.wallet(t, Money(math.MaxInt64), 0)
	overflow := fundingCredit(t, f, f.tenant, 1)
	before := fundingSnapshot(t, f)
	_, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: overflow.ID, ActorID: f.approver})
	assertCode(t, err, "amount_overflow")
	if fundingSnapshot(t, f) != before {
		t.Fatal("AVAILABLE overflow partially changed funding facts")
	}
	// Another tenant can take the clearing account to the exact BIGINT minimum;
	// its own AVAILABLE is small, so the next failure isolates clearing overflow.
	other := accountBatchTenant(t, f)
	fundingApprove(t, f, fundingCredit(t, f, other, 1))
	var balance int64
	if err = f.pool.QueryRow(f.ctx, `SELECT balance FROM ledger_accounts WHERE tenant_id IS NULL AND kind='FUNDING_CLEARING'`).Scan(&balance); err != nil || balance != math.MinInt64 {
		t.Fatalf("exact clearing lower boundary failed: %d %v", balance, err)
	}
	clearingOverflow := fundingCredit(t, f, other, 1)
	before = fundingSnapshot(t, f)
	_, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: other, OrderID: clearingOverflow.ID, ActorID: f.approver})
	assertCode(t, err, "amount_overflow")
	if fundingSnapshot(t, f) != before {
		t.Fatal("clearing overflow partially changed funding facts")
	}
	fundingCheck(t, f, "")
	fundingCheck(t, f, f.tenant)
	fundingCheck(t, f, other)
}

func TestPostgresFundingManualReportsAndRebuildRetainOriginalConsumerContract(t *testing.T) {
	f := fundingFixture(t)
	fundingApprove(t, f, fundingCredit(t, f, f.tenant, 31))
	if ReportConsumer != "reports-v1" {
		t.Fatal("funding changed the original consumer namespace")
	}
	for i := 0; i < 2; i++ {
		n, err := f.store.Project(f.ctx, ProjectInput{})
		if err != nil || (i == 0 && n != 2) || (i == 1 && n != 0) {
			t.Fatalf("original credited projection/replay: iteration=%d events=%d err=%v", i, n, err)
		}
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1031" {
		t.Fatalf("old report omitted or doubled manual funding: %+v %v", report, err)
	}
	before := fundingSnapshot(t, f)
	if n, err := f.store.Rebuild(f.ctx, "funding-rebuild"); err != nil || n != 2 {
		t.Fatalf("manual funding rebuild: events=%d err=%v", n, err)
	}
	report, err = f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1031" {
		t.Fatalf("rebuilt old report differs: %+v %v", report, err)
	}
	// Rebuild may append its own audit; it cannot append new money or events.
	var receiptCount, journalCount, outboxCount int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM funding_receipts),(SELECT count(*) FROM journals),(SELECT count(*) FROM projection_outbox)`).Scan(&receiptCount, &journalCount, &outboxCount); err != nil || receiptCount != 2 || journalCount != 2 || outboxCount != 2 || before == "" {
		t.Fatalf("rebuild emitted funding again: %d/%d/%d %v", receiptCount, journalCount, outboxCount, err)
	}
	fundingCheck(t, f, "")
}

func fundingManualLocks(t *testing.T, f *fixture, tx pgx.Tx, o CreditOrder) {
	t.Helper()
	if _, err := creditRow(f.ctx, tx, o.TenantID, o.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []struct{ id, role string }{{f.approver, "finance_approver"}, {f.operator, "finance_operator"}} {
		if err := role(f.ctx, tx, identity.id, identity.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(f.ctx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, o.TenantID); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresFundingHelperLeavesCommitAndAuditOwnershipWithCaller(t *testing.T) {
	f := fundingFixture(t)
	o := fundingCredit(t, f, f.tenant, 43)
	before := fundingSnapshot(t, f)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	fundingManualLocks(t, f, tx, o)
	result, err := f.store.ApplyFundingTx(f.ctx, tx, FundingSourceRef{Kind: FundingManualCredit, ID: o.ID}, FundingScope{TenantID: f.tenant, ActorID: f.approver})
	if err != nil || !result.Applied || result.RedemptionID != "" || result.Receipt.SourceID != o.ID || result.Receipt.Amount != 43 || result.Receipt.TenantID != f.tenant || result.Receipt.SourceKind != FundingManualCredit || result.Receipt.Currency != "CNY" || result.Receipt.ID == "" || result.Receipt.JournalID == "" || result.Receipt.CreatedAt.IsZero() {
		t.Fatalf("manual helper contract: %+v %v", result, err)
	}
	var audits int
	if err = tx.QueryRow(f.ctx, `SELECT count(*) FROM audit_events WHERE action='credit.approved' AND target=$1`, o.ID).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("helper stole caller approval-audit ownership", audits, err)
	}
	// Another session still sees only committed facts until its caller commits.
	if fundingSnapshot(t, f) != before {
		t.Fatal("helper committed the caller's transaction")
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if fundingSnapshot(t, f) != before {
		t.Fatal("caller rollback retained funding effects")
	}
	fundingApprove(t, f, o)
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	fundingManualLocks(t, f, tx, o)
	result, err = f.store.ApplyFundingTx(f.ctx, tx, FundingSourceRef{Kind: FundingManualCredit, ID: o.ID}, FundingScope{TenantID: f.tenant, ActorID: f.approver})
	if err != nil || result.Applied || result.Receipt.SourceID != o.ID || result.Receipt.Amount != 43 {
		t.Fatalf("approved helper did not return original receipt: %+v %v", result, err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.wallet(t, 1043, 0)
	fundingCheck(t, f, "")
}

// This bypasses Go's funding helper deliberately, using ordinary valid SQL to
// prove that commit-time constraints reject a balanced but unauthoritative
// financial graph. It does not disable any database guard.
func TestPostgresFundingCommitRejectsBalancedSourceReceiptJournalAndEventMismatches(t *testing.T) {
	for _, boundary := range []string{
		"missing_receipt", "orphan_receipt", "receipt_amount", "journal_reference", "journal_operation", "journal_kind",
		"wrong_clearing", "extra_balanced_line", "missing_credited", "credited_amount", "pending_source", "orphan_credit_journal", "orphan_credited",
	} {
		t.Run(boundary, func(t *testing.T) {
			f := fundingFixture(t)
			o := fundingCredit(t, f, f.tenant, 7)
			before := fundingSnapshot(t, f)
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if boundary == "orphan_credited" {
				if err = emit(f.ctx, tx, f.tenant, uuid.NewString(), "credited", map[string]any{"amount": Money(7)}); err != nil {
					t.Fatal(err)
				}
			} else {
				operation, reference, kind := o.ID, o.ID, "credit"
				if boundary == "journal_reference" || boundary == "orphan_credit_journal" {
					reference = uuid.NewString()
				}
				if boundary == "journal_operation" || boundary == "orphan_credit_journal" {
					operation = uuid.NewString()
				}
				if boundary == "journal_kind" {
					kind = "voucher_grant"
				}
				deltas := map[string]int64{"AVAILABLE": 7, "FUNDING_CLEARING": -7}
				if boundary == "wrong_clearing" {
					deltas = map[string]int64{"AVAILABLE": 7, "REVENUE": -7}
				}
				if boundary == "extra_balanced_line" {
					deltas = map[string]int64{"AVAILABLE": 7, "FUNDING_CLEARING": -6, "REVENUE": -1}
				}
				journal, err := post(f.ctx, tx, f.tenant, operation, kind, reference, f.approver, deltas)
				if err != nil {
					t.Fatal(err)
				}
				if boundary != "orphan_credit_journal" {
					if boundary != "pending_source" {
						if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, o.ID, f.approver, journal); err != nil {
							t.Fatal(err)
						}
					}
					if boundary != "missing_receipt" {
						sourceID, amount := o.ID, int64(7)
						if boundary == "orphan_receipt" {
							sourceID = uuid.NewString()
						}
						if boundary == "receipt_amount" {
							amount = 8
						}
						if _, err = tx.Exec(f.ctx, `INSERT INTO funding_receipts(tenant_id,source_kind,source_id,amount,currency,journal_id) VALUES($1,'manual_credit',$2,$3,'CNY',$4)`, f.tenant, sourceID, amount, journal); err != nil {
							t.Fatal(err)
						}
					}
					if boundary != "missing_credited" {
						amount := Money(7)
						if boundary == "credited_amount" {
							amount = 8
						}
						if err = emit(f.ctx, tx, f.tenant, operation, "credited", map[string]any{"amount": amount}); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err = tx.Commit(f.ctx); err == nil {
				t.Fatal("balanced but mismatched funding graph was accepted at COMMIT")
			}
			if fundingSnapshot(t, f) != before {
				t.Fatal("rejected funding graph partially changed committed money or facts")
			}
			fundingCheck(t, f, "")
		})
	}
}

func TestPostgresFundingCommitRejectsEqualAmountSourceSwap(t *testing.T) {
	f := fundingFixture(t)
	orders := []CreditOrder{fundingCredit(t, f, f.tenant, 11), fundingCredit(t, f, f.tenant, 11)}
	before := fundingSnapshot(t, f)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for i, o := range orders {
		journal, err := post(f.ctx, tx, f.tenant, o.ID, "credit", o.ID, f.approver, map[string]int64{"AVAILABLE": 11, "FUNDING_CLEARING": -11})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, o.ID, f.approver, journal); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(f.ctx, `INSERT INTO funding_receipts(tenant_id,source_kind,source_id,amount,currency,journal_id) VALUES($1,'manual_credit',$2,11,'CNY',$3)`, f.tenant, orders[1-i].ID, journal); err != nil {
			t.Fatal(err)
		}
		if err = emit(f.ctx, tx, f.tenant, o.ID, "credited", map[string]any{"amount": Money(11)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err == nil {
		t.Fatal("equal totals hid two source/receipt journal swaps")
	}
	if fundingSnapshot(t, f) != before {
		t.Fatal("rejected equal-amount source swap changed committed facts")
	}
	fundingCheck(t, f, "")
}

type fundingVoucherSource struct {
	batch, voucher string
	amount         Money
}

func fundingPlatformCommand(ctx context.Context, tx pgx.Tx, operation, actor, kind, resource string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest) VALUES($1,$2,'platform',$3,repeat('c',64))`, operation, actor, kind); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id) VALUES($1,$2,'platform',$3,'accepted','accepted',$4)`, operation, kind, actor, resource)
	return err
}

// A0 intentionally uses an internal owner fixture, not an A1 issuance API.
// These fixtures prove the common funding helper and database contract only.
func fundingVoucher(t *testing.T, f *fixture, amount Money) fundingVoucherSource {
	t.Helper()
	v := fundingVoucherSource{batch: uuid.NewString(), voucher: uuid.NewString(), amount: amount}
	err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, `INSERT INTO voucher_batches(id,face_amount,count,expires_at,purpose_ref,reason,maker) VALUES($1,$2,1,clock_timestamp()+interval '2 hours','funding:test','A0 internal fixture',$3)`, v.batch, int64(amount), f.operator); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `INSERT INTO vouchers(id,batch_id,code_digest,digest_key_version) VALUES($1,$2,repeat('a',64),'fixture-v1')`, v.voucher, v.batch); err != nil {
			return err
		}
		return fundingPlatformCommand(f.ctx, tx, uuid.NewString(), f.operator, "voucher.batch.create", v.batch)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, `SELECT id FROM voucher_batches WHERE id=$1 FOR UPDATE`, v.batch); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `UPDATE voucher_batches SET state='active',approver=$2,activated_at=clock_timestamp() WHERE id=$1`, v.batch, f.approver); err != nil {
			return err
		}
		return fundingPlatformCommand(f.ctx, tx, uuid.NewString(), f.approver, "voucher.batch.approve", v.batch)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE voucher_policy SET enabled=true WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	return v
}

func fundingRedeem(f *fixture, v fundingVoucherSource, command string) (FundingResult, error) {
	var result FundingResult
	err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest,fingerprint_key_version) VALUES($1,$2,'tenant:'||$3::text,'voucher.redeem',repeat('b',64),'fixture-v1')`, command, f.user, f.tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `SELECT id FROM voucher_batches WHERE id=$1 FOR UPDATE`, v.batch); err != nil {
			return err
		}
		for _, query := range []struct {
			sql  string
			args []any
		}{
			{`SELECT enabled FROM voucher_policy WHERE singleton FOR SHARE`, nil},
			{`SELECT id FROM tenants WHERE id=$1 FOR SHARE`, []any{f.tenant}},
			{`SELECT user_id FROM memberships WHERE tenant_id=$1 AND user_id=$2 FOR SHARE`, []any{f.tenant, f.user}},
			{`SELECT id FROM users WHERE id=$1 FOR SHARE`, []any{f.user}},
		} {
			if _, err := tx.Exec(f.ctx, query.sql, query.args...); err != nil {
				return err
			}
		}
		var err error
		result, err = f.store.ApplyFundingTx(f.ctx, tx, FundingSourceRef{Kind: FundingVoucherGrant, ID: v.voucher}, FundingScope{TenantID: f.tenant, ActorID: f.user, CommandID: command})
		if err != nil {
			return err
		}
		if err = audit(f.ctx, tx, f.tenant, f.user, "voucher.redeemed", result.RedemptionID, "", map[string]any{"amount": result.Receipt.Amount}); err != nil {
			return err
		}
		_, err = tx.Exec(f.ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id) VALUES($1,'voucher.redeem','tenant:'||$2::text,$3,'accepted','accepted',$4,$5,$6,$7)`, command, f.tenant, f.user, result.RedemptionID, int64(result.Receipt.Amount), result.Receipt.ID, result.Receipt.JournalID)
		return err
	})
	return result, err
}

func TestPostgresFundingVoucherConcurrentCommandsUseOneStableSourceAndOldReports(t *testing.T) {
	f := fundingFixture(t)
	v := fundingVoucher(t, f, 53)
	type completed struct {
		result FundingResult
		err    error
	}
	start := make(chan struct{})
	results := make(chan completed, 2)
	commands := []string{uuid.NewString(), uuid.NewString()}
	for _, command := range commands {
		go func(command string) {
			<-start
			result, err := fundingRedeem(f, v, command)
			results <- completed{result: result, err: err}
		}(command)
	}
	close(start)
	success, rejected := 0, 0
	var accepted FundingResult
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			assertCode(t, got.err, "voucher_unavailable")
			rejected++
			continue
		}
		success++
		accepted = got.result
	}
	if success != 1 || rejected != 1 || !accepted.Applied || accepted.RedemptionID == "" || accepted.Receipt.Amount != 53 || accepted.Receipt.SourceKind != FundingVoucherGrant || accepted.Receipt.SourceID != v.voucher || accepted.Receipt.TenantID != f.tenant {
		t.Fatalf("voucher funding source uniqueness failed: success=%d rejected=%d result=%+v", success, rejected, accepted)
	}
	var journalOperation, reference, journalKind, eventAmount string
	var redemptions, receiptCount, terminalCount, commandCount, eventCount, batchCount int
	var grantClearing, manualClearing int64
	err := f.pool.QueryRow(f.ctx, `SELECT j.operation_id,j.reference,j.kind,
 (SELECT sanitized_payload->>'amount' FROM projection_outbox WHERE operation_id=j.operation_id AND kind='credited'),
 (SELECT count(*) FROM voucher_redemptions WHERE voucher_id=$1),
 (SELECT count(*) FROM funding_receipts WHERE source_kind='voucher_grant' AND source_id=$1),
 (SELECT count(*) FROM commerce_operation_receipts WHERE status='accepted' AND kind='voucher.redeem'),
 (SELECT count(*) FROM commerce_commands WHERE kind='voucher.redeem'),
 (SELECT count(*) FROM projection_outbox WHERE operation_id=j.operation_id AND kind='credited'),
 (SELECT redeemed_count FROM voucher_batches WHERE id=$2),
 (SELECT balance FROM ledger_accounts WHERE kind='OPERATING_GRANT_CLEARING' AND tenant_id IS NULL),
 (SELECT balance FROM ledger_accounts WHERE kind='FUNDING_CLEARING' AND tenant_id IS NULL)
 FROM journals j WHERE id=$3`, v.voucher, v.batch, accepted.Receipt.JournalID).Scan(&journalOperation, &reference, &journalKind, &eventAmount, &redemptions, &receiptCount, &terminalCount, &commandCount, &eventCount, &batchCount, &grantClearing, &manualClearing)
	if err != nil || journalOperation != accepted.RedemptionID || journalOperation == commands[0] || journalOperation == commands[1] || reference != v.voucher || journalKind != "voucher_grant" || eventAmount != "53" || redemptions != 1 || receiptCount != 1 || terminalCount != 1 || commandCount != 1 || eventCount != 1 || batchCount != 1 || grantClearing != -53 || manualClearing != -1000 {
		t.Fatalf("voucher funding ownership/clearing differs: operation=%s reference=%s kind=%s amount=%s counts=%d/%d/%d/%d/%d/%d clearing=%d/%d err=%v", journalOperation, reference, journalKind, eventAmount, redemptions, receiptCount, terminalCount, commandCount, eventCount, batchCount, grantClearing, manualClearing, err)
	}
	f.wallet(t, 1053, 0)
	fundingCheck(t, f, "")
	fundingCheck(t, f, f.tenant)
	if n, err := f.store.Project(f.ctx, ProjectInput{}); err != nil || n != 2 {
		t.Fatalf("old credited consumer did not include both source kinds exactly once: %d %v", n, err)
	}
	report, err := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1053" {
		t.Fatalf("old report missed grant funding: %+v %v", report, err)
	}
	if n, err := f.store.Rebuild(f.ctx, "funding-grant-rebuild"); err != nil || n != 2 {
		t.Fatalf("receipt-backed rebuild omitted grant source: %d %v", n, err)
	}
	report, err = f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if err != nil || report.Credited != "1053" {
		t.Fatalf("rebuilt old report missed grant funding: %+v %v", report, err)
	}
}

func TestPostgresFundingVoucherInfrastructureFailuresRollbackSourceAndCommand(t *testing.T) {
	for _, table := range []string{"audit_events", "projection_outbox", "funding_receipts", "commerce_operation_receipts"} {
		t.Run(table, func(t *testing.T) {
			f := fundingFixture(t)
			v := fundingVoucher(t, f, 17)
			before := fundingSnapshot(t, f)
			if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION funding_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'funding fixture write rejected'; END $$`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "CREATE TRIGGER funding_injected_failure BEFORE INSERT ON "+pgx.Identifier{table}.Sanitize()+" FOR EACH ROW EXECUTE FUNCTION funding_injected_failure()"); err != nil {
				t.Fatal(err)
			}
			_, err := fundingRedeem(f, v, uuid.NewString())
			var dbErr *pgconn.PgError
			if !errors.As(err, &dbErr) || dbErr.Code != "P0001" {
				t.Fatalf("voucher infrastructure failure lost: %v", err)
			}
			if fundingSnapshot(t, f) != before {
				t.Fatal("failed voucher funding retained batch count, redemption, command, terminal, money, receipt, audit or outbox")
			}
			if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER funding_injected_failure ON "+pgx.Identifier{table}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			if _, err = fundingRedeem(f, v, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			f.wallet(t, 1017, 0)
			fundingCheck(t, f, "")
		})
	}
}

func TestPostgresFundingVerifierAndRebuildRejectEqualAmountSourceCorruption(t *testing.T) {
	f := fundingFixture(t)
	orders := []CreditOrder{fundingCredit(t, f, f.tenant, 19), fundingCredit(t, f, f.tenant, 19)}
	for _, o := range orders {
		fundingApprove(t, f, o)
	}
	other := accountBatchTenant(t, f)
	fundingApprove(t, f, fundingCredit(t, f, other, 23))
	fundingCheck(t, f, "")
	fundingCheck(t, f, other)
	// Simulate retained-history corruption using the random schema owner only.
	// Financial sums, all journal lines and every outbox event stay unchanged.
	// Normal app SQL is separately tested above and cannot perform this update.
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, `ALTER TABLE funding_receipts DISABLE TRIGGER immutable_funding_receipt`); err != nil {
		t.Fatal(err)
	}
	temporary := uuid.NewString()
	for _, pair := range []struct{ old, next string }{{orders[0].ID, temporary}, {orders[1].ID, orders[0].ID}, {temporary, orders[1].ID}} {
		if _, err = tx.Exec(f.ctx, `UPDATE funding_receipts SET source_id=$2 WHERE source_kind='manual_credit' AND source_id=$1`, pair.old, pair.next); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE funding_receipts ENABLE TRIGGER immutable_funding_receipt`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", f.tenant} {
		check, err := f.store.VerifyFunding(f.ctx, scope)
		if err != nil || check.Balanced || check.SourceMismatches+check.JournalMismatches == 0 {
			t.Fatalf("equal-amount source corruption evaded scoped verifier: scope=%q check=%+v err=%v", scope, check, err)
		}
	}
	// A tenant-scoped check compares its own clearing deltas, not its subtotal
	// with the shared platform clearing account or another tenant's bad source.
	otherCheck, err := f.store.VerifyFunding(f.ctx, other)
	if err != nil || !otherCheck.Balanced {
		t.Fatalf("another tenant's source corrupted a healthy tenant scope: %+v %v", otherCheck, err)
	}
	ledger, err := f.store.VerifyLedger(f.ctx, Query{})
	if err != nil || ledger.Balanced {
		t.Fatalf("ledger ignored funding-source verification: %+v %v", ledger, err)
	}
	before := fundingSnapshot(t, f)
	if _, err = f.store.Rebuild(f.ctx, "funding-corrupt-rebuild"); err == nil {
		t.Fatal("same totals let corrupted funding sources activate a new projection")
	}
	var active string
	if err = f.pool.QueryRow(f.ctx, `SELECT consumer FROM active_report_projection WHERE singleton`).Scan(&active); err != nil || active != ReportConsumer {
		t.Fatalf("failed funding verification switched active projection: %s %v", active, err)
	}
	if fundingSnapshot(t, f) != before {
		t.Fatal("failed rebuild modified retained funding money, history, events or audit")
	}
}

func TestPostgresFundingCommitRejectsArbitraryJournalKindsTouchingClearing(t *testing.T) {
	for _, clearing := range []string{"FUNDING_CLEARING", "OPERATING_GRANT_CLEARING"} {
		t.Run(clearing, func(t *testing.T) {
			f := fundingFixture(t)
			before := fundingSnapshot(t, f)
			tx, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if _, err = post(f.ctx, tx, f.tenant, uuid.NewString(), "arbitrary-kind", "unproven-source", f.operator, map[string]int64{"AVAILABLE": 7, clearing: -7}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(f.ctx); err == nil {
				t.Fatal("an arbitrary journal kind bypassed clearing source ownership at COMMIT")
			}
			if fundingSnapshot(t, f) != before {
				t.Fatal("orphan clearing journal rejection partially changed retained financial facts")
			}
			fundingCheck(t, f, "")
		})
	}
}

func TestPostgresFundingVerifierRejectsAcceptedRedeemTerminalWithoutRedemption(t *testing.T) {
	f := fundingFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	// Corrupt only a terminal graph in this random schema owned by the fixture.
	// Its ordinary FKs reference real manual funding, so sums, money, journal
	// lines, source coverage, and credited outbox facts remain wholly correct.
	for _, table := range []string{"commerce_commands", "commerce_operation_receipts"} {
		if _, err = tx.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" DISABLE TRIGGER USER"); err != nil {
			t.Fatal(err)
		}
	}
	operation := uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest,fingerprint_key_version) VALUES($1,$2,'tenant:'||$3::text,'voucher.redeem',repeat('d',64),'fixture-v1')`, operation, f.user, f.tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id,amount,funding_receipt_id,journal_id)
 SELECT $1,'voucher.redeem','tenant:'||$2::text,$3,'accepted','accepted',$4,r.amount,r.id,r.journal_id
 FROM funding_receipts r WHERE r.tenant_id=$2 AND r.source_kind='manual_credit'`, operation, f.tenant, f.user, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"commerce_commands", "commerce_operation_receipts"} {
		if _, err = tx.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" ENABLE TRIGGER USER"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, tenant := range []string{"", f.tenant} {
		check, err := f.store.VerifyFunding(f.ctx, tenant)
		if err != nil || check.Balanced || check.RedemptionMismatches == 0 {
			t.Fatalf("accepted terminal without a redemption evaded reverse verification: scope=%q check=%+v err=%v", tenant, check, err)
		}
	}
	ledger, err := f.store.VerifyLedger(f.ctx, Query{})
	if err != nil || ledger.Balanced {
		t.Fatalf("ledger accepted a forged redeem terminal graph: %+v %v", ledger, err)
	}
	before := fundingSnapshot(t, f)
	if _, err = f.store.Rebuild(f.ctx, "orphan-redeem-rebuild"); err == nil {
		t.Fatal("orphan accepted terminal activated an otherwise equal-sum projection")
	}
	var active string
	if err = f.pool.QueryRow(f.ctx, `SELECT consumer FROM active_report_projection WHERE singleton`).Scan(&active); err != nil || active != ReportConsumer {
		t.Fatalf("orphan terminal rebuild switched active projection: %s %v", active, err)
	}
	if fundingSnapshot(t, f) != before {
		t.Fatal("failed orphan-terminal rebuild changed financial facts or audit")
	}
}

func fundingRevocationProposal(t *testing.T, f *fixture, v fundingVoucherSource) string {
	t.Helper()
	proposal, operation := uuid.NewString(), uuid.NewString()
	err := f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		if err := fundingPlatformCommand(f.ctx, tx, operation, f.operator, "voucher.revocation.create", proposal); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `SELECT id FROM voucher_batches WHERE id=$1 FOR UPDATE`, v.batch); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `INSERT INTO voucher_revocations(id,batch_id,operation_id,maker,expected_revision,expected_redeemed_count,reason)
 SELECT $1,id,$2,$3,revision,redeemed_count,'A0 revocation fixture' FROM voucher_batches WHERE id=$4`, proposal, operation, f.operator, v.batch); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return proposal
}

func fundingRevoke(f *fixture, v fundingVoucherSource, proposal, actor, commandKind string) error {
	operation := uuid.NewString()
	return f.store.transaction(f.ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(f.ctx, `SELECT id FROM voucher_batches WHERE id=$1 FOR UPDATE`, v.batch); err != nil {
			return err
		}
		var decidedAt time.Time
		if err := tx.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&decidedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(f.ctx, `UPDATE voucher_batches SET state='revoked',terminal_actor=$2,terminal_at=$3 WHERE id=$1`, v.batch, actor, decidedAt); err != nil {
			return err
		}
		if proposal == "" {
			return nil
		}
		if err := fundingPlatformCommand(f.ctx, tx, operation, actor, commandKind, proposal); err != nil {
			return err
		}
		_, err := tx.Exec(f.ctx, `INSERT INTO voucher_revocation_decisions(id,proposal_id,batch_id,operation_id,approver,status,reason,decided_at) VALUES($1,$2,$3,$4,$5,'accepted','accepted',$6)`, uuid.NewString(), proposal, v.batch, operation, actor, decidedAt)
		return err
	})
}

func TestPostgresFundingRevocationRequiresCompleteImmutableApprovalGraph(t *testing.T) {
	for _, boundary := range []string{"complete", "no_proposal", "wrong_kind", "same_maker"} {
		t.Run(boundary, func(t *testing.T) {
			f := fundingFixture(t)
			v := fundingVoucher(t, f, 13)
			proposal := ""
			if boundary != "no_proposal" {
				proposal = fundingRevocationProposal(t, f, v)
			}
			before := fundingSnapshot(t, f)
			actor, kind := f.approver, "voucher.revocation.approve"
			if boundary == "wrong_kind" {
				kind = "voucher.batch.void"
			}
			if boundary == "same_maker" {
				actor = f.operator
			}
			err := fundingRevoke(f, v, proposal, actor, kind)
			if boundary != "complete" {
				if err == nil {
					t.Fatal("incomplete or mismatched revocation approval graph committed")
				}
				if fundingSnapshot(t, f) != before {
					t.Fatal("rejected revocation rewrote batch, proposal, decision, command, terminal or money")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				var state, terminalActor, decisionActor, commandActor, commandKind, terminalResource string
				var revision, redeemed int64
				var sameTime bool
				err = f.pool.QueryRow(f.ctx, `SELECT b.state,b.terminal_actor,b.revision,b.redeemed_count,d.approver,c.actor,c.kind,r.resource_id,b.terminal_at=d.decided_at
 FROM voucher_batches b JOIN voucher_revocation_decisions d ON d.batch_id=b.id JOIN commerce_commands c ON c.operation_id=d.operation_id JOIN commerce_operation_receipts r ON r.operation_id=c.operation_id
 WHERE b.id=$1 AND d.proposal_id=$2 AND d.status='accepted' AND r.status='accepted'`, v.batch, proposal).Scan(&state, &terminalActor, &revision, &redeemed, &decisionActor, &commandActor, &commandKind, &terminalResource, &sameTime)
				if err != nil || state != "revoked" || revision != 3 || redeemed != 0 || terminalActor != f.approver || decisionActor != f.approver || commandActor != f.approver || commandKind != "voucher.revocation.approve" || terminalResource != proposal || !sameTime {
					t.Fatalf("complete revocation graph changed its immutable ownership: state=%s actors=%s/%s/%s revision=%d redeemed=%d kind=%s resource=%s sameTime=%v err=%v", state, terminalActor, decisionActor, commandActor, revision, redeemed, commandKind, terminalResource, sameTime, err)
				}
			}
			f.wallet(t, 1000, 0)
			fundingCheck(t, f, "")
		})
	}
}
