package maintenancetrust

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/fundinglotscan"
)

// Empty and Unicode tenant IDs are legal historical TEXT. This deliberately
// constructs that history with original SQL, before C0 is installed; it does
// not claim that today's online funding API accepts those tenant identities.
// Every original FK, transition, immutable-history and deferred source trigger
// remains enabled. No candidate/production SQL or role is changed by the test.
func TestFundingLotsPhasePostgresFundedLegacyTextTenants(t *testing.T) {
	type legacyCredit struct {
		tenant, account, credit, journal, receipt string
		amount                                    int64
	}
	history := []legacyCredit{
		{"", uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), 100},
		{"旧/租户", uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), 40},
	}
	clearing, maker, checker := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f := newSnapshotFixturePrepared(t, func(_ *snapshotFixture, pool *pgxpool.Pool) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			t.Fatal("begin original legacy TEXT history")
		}
		defer tx.Rollback(context.Background())
		exec := func(sql string, args ...any) {
			t.Helper()
			if _, err := tx.Exec(ctx, sql, args...); err != nil {
				t.Fatal("original guarded legacy history SQL")
			}
		}
		exec(`INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'unused-legacy-fixture','finance_operator'),($3,$4,'unused-legacy-fixture','finance_approver')`, maker, maker+"@example.invalid", checker, checker+"@example.invalid")
		// A real NULL platform identity must never collapse into tenant ID "".
		exec(`INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,NULL,'FUNDING_CLEARING')`, clearing)
		for _, row := range history {
			exec(`INSERT INTO tenants(id,name) VALUES($1,'guarded historical TEXT tenant')`, row.tenant)
			exec(`INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,$2,'AVAILABLE')`, row.account, row.tenant)
			// Respect the original pending -> approved transition rather than
			// inserting an already-approved credit through a disabled guard.
			exec(`INSERT INTO credit_orders(id,tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,$1,$3,$4,'receipt:legacy-text-test')`, row.credit, row.tenant, row.amount, maker)
			exec(`INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,$2,'credit.created',$3,jsonb_build_object('amount',$4::pg_catalog.text))`, maker, row.tenant, row.credit, strconv.FormatInt(row.amount, 10))
			exec(`INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,'credit',$3,$4)`, row.journal, row.tenant, row.credit, checker)
			exec(`INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3),($1,$4,-$3::pg_catalog.int8)`, row.journal, row.account, row.amount, clearing)
			exec(`UPDATE ledger_accounts SET balance=balance+$2 WHERE id=$1`, row.account, row.amount)
			exec(`UPDATE ledger_accounts SET balance=balance-$2 WHERE id=$1`, clearing, row.amount)
			exec(`UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, row.credit, checker, row.journal)
			exec(`INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,journal_id) VALUES($1,$2,'manual_credit',$3,$4,$5)`, row.receipt, row.tenant, row.credit, row.amount, row.journal)
			exec(`INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited',jsonb_build_object('amount',$3::pg_catalog.text))`, row.tenant, row.credit, strconv.FormatInt(row.amount, 10))
			exec(`INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES($1,$2,'credit.approved',$3,jsonb_build_object('amount',$4::pg_catalog.text))`, checker, row.tenant, row.credit, strconv.FormatInt(row.amount, 10))
		}
		exec(`SET CONSTRAINTS ALL IMMEDIATE`)
		if err = tx.Commit(ctx); err != nil {
			t.Fatal("commit original fully guarded legacy funding graph")
		}
	})

	// Hand calculation: T=2,F=2,U=S=Q=0,a=L=2,h=0. Thus A=140,
	// H=0, G=3T+2F+3L+2=18, and eight data rows for each old tenant.
	plan := fundingLotsPlanOrdinary(t, f, uuid.NewString())
	m := plan.Metadata
	if m.TenantCount != 2 || m.ReceiptCount != 2 || m.SettlementCount != 0 || m.ApprovedRefundCount != 0 ||
		m.OpeningCount != 2 || m.LegacySourceCount != 2 || m.AvailableTotal != "140" || m.HeldTotal != "0" || m.DataRows != 16 || m.GeneratedRows != 18 {
		t.Fatalf("legacy TEXT opening arithmetic: %+v", m)
	}
	result := fundingLotsInitOrdinary(t, f, plan.Expected)
	if result.Metadata.AvailableTotal != "140" || result.Metadata.HeldTotal != "0" || result.Metadata.DataRows != 16 {
		t.Fatal("initialization changed legacy TEXT principal")
	}
	c := f.connect(t)
	// This independent connection rereads the actual 84-table graph, strict
	// v2 and complete 109-table basis; no phase-owned Plan supplies its facts.
	report := fundingLotsObserveCommitted(t, c, f.schema, fundinglotscan.ModeOrdinary, plan.Expected)
	w := report.Wallet.Scanned
	if w.Tenants != 2 || w.Accounts != 3 || w.Journals != 2 || w.Lines != 4 || w.Receipts != 2 ||
		w.LegacyReceipts != 2 || w.LegacyOpenings != 2 || w.LegacySources != 2 || w.Operations != 2 || w.Heads != 2 ||
		w.Lots != 2 || w.Events != 2 || w.Balances != 2 || w.Reservations != 0 || w.ReservationLots != 0 || w.SettlementLots != 0 || w.RefundLots != 0 {
		t.Fatal("committed legacy TEXT source or opening set changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	qualified := func(table string) string { return pgx.Identifier{f.schema, table}.Sanitize() }
	var platformTenant pgtype.Text
	var platformKind, platformBalance string
	if c.QueryRow(ctx, `SELECT tenant_id,kind,balance::pg_catalog.text FROM `+qualified("ledger_accounts")+` WHERE id=$1`, clearing).Scan(&platformTenant, &platformKind, &platformBalance) != nil ||
		platformTenant.Valid || platformKind != "FUNDING_CLEARING" || platformBalance != "-140" {
		t.Fatal("platform NULL clearing identity or principal changed")
	}
	for _, row := range history {
		var originalTenant pgtype.Text
		var originalBalance string
		if c.QueryRow(ctx, `SELECT tenant_id,balance::pg_catalog.text FROM `+qualified("ledger_accounts")+` WHERE id=$1`, row.account).Scan(&originalTenant, &originalBalance) != nil ||
			!originalTenant.Valid || originalTenant.String != row.tenant || originalBalance != strconv.FormatInt(row.amount, 10) {
			t.Fatal("legacy tenant TEXT collapsed into SQL NULL or its balance changed")
		}
		var tenant, openingAccount, openingAmount, origin, issued, refundClass string
		var available, held, consumed, revision, sequence, source string
		var originalReceiptNull, originalRefundNull, commonCutover bool
		sql := `SELECT o.tenant_id,o.account_id,o.amount::pg_catalog.text,l.origin_kind,l.issued_amount::pg_catalog.text,l.refund_class,
 b.available_amount::pg_catalog.text,b.inference_held_amount::pg_catalog.text,b.consumed_amount::pg_catalog.text,b.revision::pg_catalog.text,
 h.tail_seq::pg_catalog.text,op.source_id,l.funding_receipt_id IS NULL,l.legacy_refund_order_id IS NULL,o.observed_at=l.fifo_at
 FROM ` + qualified("funding_lot_legacy_openings") + ` o
 JOIN ` + qualified("funding_lots") + ` l ON l.legacy_opening_id=o.id AND l.tenant_id=o.tenant_id
 JOIN ` + qualified("funding_lot_balances") + ` b ON b.lot_id=l.id AND b.tenant_id=o.tenant_id
 JOIN ` + qualified("funding_wallet_heads") + ` h ON h.tenant_id=o.tenant_id
 JOIN ` + qualified("funding_wallet_operations") + ` op ON op.id=h.tail_operation_id AND op.tenant_id=o.tenant_id
 WHERE o.tenant_id=$1 AND o.kind='available'`
		if c.QueryRow(ctx, sql, row.tenant).Scan(&tenant, &openingAccount, &openingAmount, &origin, &issued, &refundClass,
			&available, &held, &consumed, &revision, &sequence, &source, &originalReceiptNull, &originalRefundNull, &commonCutover) != nil ||
			tenant != row.tenant || openingAccount != row.account || openingAmount != strconv.FormatInt(row.amount, 10) ||
			origin != "legacy_available" || issued != openingAmount || refundClass != "nonrefundable" || available != openingAmount ||
			held != "0" || consumed != "0" || revision != "1" || sequence != "0" || source != row.tenant ||
			!originalReceiptNull || !originalRefundNull || !commonCutover {
			t.Fatal("legacy TEXT opening, lot or projection differs from independent arithmetic")
		}
		var receiptTenant, receiptSource, legacySource, legacyJournal string
		if c.QueryRow(ctx, `SELECT r.tenant_id,r.source_id,s.source_id,s.journal_id FROM `+qualified("funding_lot_legacy_receipts")+` r JOIN `+qualified("funding_wallet_legacy_sources")+` s ON s.source_id=r.receipt_id AND s.tenant_id=r.tenant_id AND s.kind='manual_credit' WHERE r.receipt_id=$1`, row.receipt).
			Scan(&receiptTenant, &receiptSource, &legacySource, &legacyJournal) != nil || receiptTenant != row.tenant || receiptSource != row.credit || legacySource != row.receipt || legacyJournal != row.journal {
			t.Fatal("legacy membership confused funding source ID with receipt ID")
		}
	}
}
