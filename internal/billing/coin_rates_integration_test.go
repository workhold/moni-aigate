package billing

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func insertCoinRateFixture(t *testing.T, f *fixture, id, currency string, numerator, denominator int64) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, `WITH n AS (SELECT greatest(clock_timestamp(),coalesce((SELECT max(effective_at)+interval '1 microsecond' FROM coin_rate_versions WHERE fiat_currency=$2),'-infinity'::timestamptz)) AS at)
 INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver,single_operator,created_at)
 SELECT $1,$2,$3,$4,n.at,'034.3b explicit test version',$5,$6,false,n.at FROM n`, id, currency, numerator, denominator, f.operator, f.approver)
	if err != nil {
		t.Fatal(err)
	}
}

func requireCoinRateSQLFailure(t *testing.T, f *fixture, code, constraint, query string, args ...any) {
	t.Helper()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, query, args...)
	if err == nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("SQL unexpectedly succeeded: %s", query)
	}
	var pgerr *pgconn.PgError
	matched := errors.As(err, &pgerr) && pgerr.Code == code && (constraint == "" || pgerr.ConstraintName == constraint)
	rollbackErr := tx.Rollback(context.Background())
	if !matched {
		t.Fatalf("SQL error = %+v, want code=%s constraint=%s", err, code, constraint)
	}
	if rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
}

func TestPostgresCoinRateGenesisPaidSourcesAndDirectSQLGuards(t *testing.T) {
	f := fundingFixture(t)
	var genesis, currency, maker, approver, reason string
	var numerator, denominator, versions int64
	var effective, created time.Time
	var single bool
	if err := f.pool.QueryRow(f.ctx, `SELECT id,fiat_currency,rate_num,rate_den,effective_at,reason,coalesce(maker,''),coalesce(approver,''),single_operator,created_at,(SELECT count(*) FROM coin_rate_versions) FROM coin_rate_versions`).Scan(&genesis, &currency, &numerator, &denominator, &effective, &reason, &maker, &approver, &single, &created, &versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || genesis == "" || currency != "CNY" || numerator != 1 || denominator != 1 || !effective.Equal(time.Unix(0, 0).UTC()) || maker != "" || approver != "" || single || reason == "" || created.IsZero() {
		t.Fatalf("invalid CNY genesis: id=%s currency=%s rate=%d/%d effective=%s actors=%s/%s single=%v versions=%d", genesis, currency, numerator, denominator, effective, maker, approver, single, versions)
	}

	order, err := f.store.CreateCredit(f.ctx, creditInputForTest(f.tenant, f.operator, "coin-rate-paid-bonus", 100, 20, "paid", 90))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	var paidRate string
	var paidFiat FiatMoney
	var bonusRate *string
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT f.buy_rate_version_id FROM funding_receipts f WHERE f.parent_ref=$1 AND f.source_kind='manual_credit'),(SELECT f.fiat_amount FROM funding_receipts f WHERE f.parent_ref=$1 AND f.source_kind='manual_credit'),(SELECT f.buy_rate_version_id FROM funding_receipts f WHERE f.parent_ref=$1 AND f.source_kind='manual_bonus')`, order.ID).Scan(&paidRate, &paidFiat, &bonusRate); err != nil {
		t.Fatal(err)
	}
	if paidRate != genesis || paidFiat != 90 || bonusRate != nil {
		t.Fatalf("paid/bonus rate freeze mismatch: paid=%s fiat=%d bonus=%v genesis=%s", paidRate, paidFiat, bonusRate, genesis)
	}
	var initialRate string
	if err = f.pool.QueryRow(f.ctx, `SELECT buy_rate_version_id FROM funding_receipts WHERE tenant_id=$1 AND source_kind='manual_credit' ORDER BY created_at LIMIT 1`, f.tenant).Scan(&initialRate); err != nil || initialRate != genesis {
		t.Fatalf("fixture paid receipt lacks CNY genesis: %s %v", initialRate, err)
	}

	usdRate := uuid.NewString()
	insertCoinRateFixture(t, f, usdRate, "USD", 7, 10)
	var usdEffective time.Time
	if err = f.pool.QueryRow(f.ctx, `SELECT effective_at FROM coin_rate_versions WHERE id=$1`, usdRate).Scan(&usdEffective); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, code, constraint, query string
		args                          []any
	}{
		{"immutable-update", "P0001", "", `UPDATE coin_rate_versions SET reason='changed' WHERE id=$1`, []any{genesis}},
		{"immutable-delete", "P0001", "", `DELETE FROM coin_rate_versions WHERE id=$1`, []any{usdRate}},
		{"backdated", "23514", "coin_rate_versions_check", `INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver) VALUES($1,'USD',1,1,clock_timestamp()-interval '1 second','backdated',$2,$3)`, []any{uuid.NewString(), f.operator, f.approver}},
		{"unique-currency-effective", "23505", "coin_rate_versions_fiat_currency_effective_at_key", `INSERT INTO coin_rate_versions(id,fiat_currency,rate_num,rate_den,effective_at,reason,maker,approver,created_at) VALUES($1,'USD',1,1,$2,'duplicate effective time',$3,$4,$2)`, []any{uuid.NewString(), usdEffective, f.operator, f.approver}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireCoinRateSQLFailure(t, f, tc.code, tc.constraint, tc.query, tc.args...)
		})
	}

	pending := fundingCredit(t, f, f.tenant, 1)
	var journal string
	if err = f.pool.QueryRow(f.ctx, `SELECT journal_id FROM funding_receipts WHERE tenant_id=$1 AND basis_kind='paid' LIMIT 1`, f.tenant).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, code, constraint, query string
		args                          []any
	}{
		{"paid-missing-rate", "23514", "funding_receipts_buy_rate_check", `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,basis_kind,fiat_currency,fiat_amount,buy_rate_version_id,parent_ref) VALUES($1,$2,'manual_credit',$3,1,'COIN',$4,'paid','CNY',1,NULL,$3)`, []any{uuid.NewString(), f.tenant, pending.ID, journal}},
		{"nonpaid-with-rate", "23514", "funding_receipts_buy_rate_check", `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,basis_kind,buy_rate_version_id,parent_ref) VALUES($1,$2,'manual_bonus',$3,1,'COIN',$4,'grant',$5,$3)`, []any{uuid.NewString(), f.tenant, pending.ID, journal, genesis}},
		{"currency-version-mismatch", "23503", "funding_receipts_buy_rate_fk", `WITH j AS (INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor,currency) VALUES($4,$2,$5,'credit',$3,$6,'COIN') RETURNING id) INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,basis_kind,fiat_currency,fiat_amount,buy_rate_version_id,parent_ref) SELECT $1,$2,'manual_credit',$3,1,'COIN',j.id,'paid','CNY',1,$7,$3 FROM j`, []any{uuid.NewString(), f.tenant, pending.ID, uuid.NewString(), uuid.NewString(), f.approver, usdRate}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireCoinRateSQLFailure(t, f, tc.code, tc.constraint, tc.query, tc.args...)
		})
	}
	var directRows int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_receipts WHERE source_id=$1`, pending.ID).Scan(&directRows); err != nil || directRows != 0 {
		t.Fatalf("failed direct SQL left receipt rows: %d %v", directRows, err)
	}
}

func TestPostgresCoinRateUSDFailClosedThenExplicitVersion(t *testing.T) {
	f := fundingFixture(t)
	in := creditInputForTest(f.tenant, f.operator, "usd-requires-version", 100, 0, "paid", 90)
	in.FiatCurrency = "USD"
	in.FxNote = "informational only; never a fallback rate"
	order, err := f.store.CreateCredit(f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver})
	assertCode(t, err, "coin_rate_unavailable")
	var state, journal string
	var receipts, journals int
	if err = f.pool.QueryRow(f.ctx, `SELECT state,coalesce(journal_id,''),(SELECT count(*) FROM funding_receipts WHERE parent_ref=$1),(SELECT count(*) FROM journals WHERE operation_id=$1) FROM credit_orders WHERE id=$1`, order.ID).Scan(&state, &journal, &receipts, &journals); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || journal != "" || receipts != 0 || journals != 0 {
		t.Fatalf("missing USD version left committed funding effects: state=%s journal=%s receipts=%d journals=%d", state, journal, receipts, journals)
	}

	firstRate := uuid.NewString()
	insertCoinRateFixture(t, f, firstRate, "USD", 7, 10)
	approved, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: order.ID, ActorID: f.approver})
	if err != nil || approved.State != "approved" {
		t.Fatalf("explicit USD version did not allow posting: %+v %v", approved, err)
	}
	var receiptRate, receiptCurrency string
	if err = f.pool.QueryRow(f.ctx, `SELECT buy_rate_version_id,fiat_currency FROM funding_receipts WHERE parent_ref=$1 AND source_kind='manual_credit'`, order.ID).Scan(&receiptRate, &receiptCurrency); err != nil || receiptRate != firstRate || receiptCurrency != "USD" {
		t.Fatalf("USD buy rate was not frozen: rate=%s currency=%s err=%v", receiptRate, receiptCurrency, err)
	}

	secondRate := uuid.NewString()
	insertCoinRateFixture(t, f, secondRate, "USD", 8, 10)
	secondInput := creditInputForTest(f.tenant, f.operator, "usd-latest-version", 50, 0, "paid", 40)
	secondInput.FiatCurrency = "USD"
	second, err := f.store.CreateCredit(f.ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: second.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT buy_rate_version_id FROM funding_receipts WHERE parent_ref=$1`, second.ID).Scan(&receiptRate); err != nil || receiptRate != secondRate {
		t.Fatalf("posting did not select latest effective USD version: %s %v", receiptRate, err)
	}
	if err = f.pool.QueryRow(f.ctx, `SELECT buy_rate_version_id FROM funding_receipts WHERE parent_ref=$1`, order.ID).Scan(&receiptRate); err != nil || receiptRate != firstRate {
		t.Fatalf("new version changed historical paid receipt: %s %v", receiptRate, err)
	}
}

func TestCoinRateLookupIsIsolatedFromRequestPaths(t *testing.T) {
	allowed := map[string]bool{"coin_rates.go": true, "credit_funding.go": true, "funding.go": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if (bytes.Contains(body, []byte("coinRateAt")) || bytes.Contains(body, []byte("coin_rate_versions"))) && !allowed[name] {
			t.Fatalf("unexpected coin-rate dependency in %s", name)
		}
	}
}

func TestPostgresReserveSettleAndConsumptionRefundWithoutCoinRateTable(t *testing.T) {
	f := fundingFixture(t)
	if _, err := f.pool.Exec(f.ctx, `ALTER TABLE coin_rate_versions RENAME TO coin_rate_versions_unavailable`); err != nil {
		t.Fatal(err)
	}
	r := f.reserve(t)
	f.final(t, r, confirmed(), "completed", "done", "none", false)
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.ChargedAmount != 15 {
		t.Fatalf("settlement read a coin-rate table: %+v %v", settled, err)
	}
	ref := uuid.NewString()
	refund, err := f.store.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settled.ID, BusinessRef: ref, Reason: "coin-rate isolation", Amount: 5, IdempotencyKey: ref, RequestHMAC: ref})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver})
	if err != nil || approved.State != "approved" {
		t.Fatalf("consumption refund read a coin-rate table: %+v %v", approved, err)
	}
	f.wallet(t, 990, 0)
}

func TestPostgresSandboxPaidReceiptFreezesCNYGenesis(t *testing.T) {
	x := sandboxFundingTest(t)
	order, evidence := x.evidence(t)
	tx, capture := x.source(t, order, evidence)
	result, err := New(x.worker).ApplyFundingTx(x.ctx, tx, FundingSourceRef{Kind: FundingSandboxCapture, ID: capture}, FundingScope{TenantID: x.tenant, ActorID: sandboxFundingActor})
	if err != nil || !result.Applied || result.Receipt.BuyRateVersionID == "" {
		t.Fatalf("sandbox paid receipt has no buy-rate version: %+v %v", result, err)
	}
	x.finish(t, tx, capture, evidence)
	var persisted, genesis string
	if err = x.app.QueryRow(x.ctx, `SELECT f.buy_rate_version_id,(SELECT r.id FROM coin_rate_versions r WHERE r.fiat_currency='CNY' AND r.rate_num=1 AND r.rate_den=1 AND r.effective_at='1970-01-01T00:00:00Z') FROM funding_receipts f WHERE f.source_kind='sandbox_capture' AND f.source_id=$1`, capture).Scan(&persisted, &genesis); err != nil || persisted == "" || persisted != genesis || persisted != result.Receipt.BuyRateVersionID {
		t.Fatalf("sandbox buy-rate persistence mismatch: persisted=%s result=%s genesis=%s err=%v", persisted, result.Receipt.BuyRateVersionID, genesis, err)
	}
	var valid bool
	if err = x.app.QueryRow(x.ctx, `SELECT payment_sandbox_journal_valid($1)`, result.Receipt.JournalID).Scan(&valid); err != nil || !valid {
		t.Fatalf("sandbox journal rejected frozen buy rate: %v valid=%v", err, valid)
	}
}
