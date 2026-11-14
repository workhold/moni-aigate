package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/catalog"
	"github.com/moni-ai/moni-aigate/migrations"
)

type coinPrefixFixture struct {
	ctx    context.Context
	pool   *pgxpool.Pool
	schema string
}

func coinMust(t *testing.T, step string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

func newCoinPrefixFixture(t *testing.T) *coinPrefixFixture {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL required for isolated virtual-coin migration tests")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	coinMust(t, "parse dedicated PG config", err)
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 55439 || cfg.ConnConfig.Database != "moni_test" || cfg.ConnConfig.User != "moni_test" {
		t.Fatal("virtual-coin migration tests require dedicated local moni_test on port 55439")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	admin, err := pgxpool.NewWithConfig(ctx, cfg.Copy())
	coinMust(t, "open fixture administrator", err)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	schema, role, password := "virtual_coin_"+suffix, "coin_owner_"+suffix, strings.ReplaceAll(uuid.NewString(), "-", "")
	f := &coinPrefixFixture{ctx: ctx, schema: schema}
	t.Cleanup(func() {
		if f.pool != nil {
			f.pool.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Error("virtual-coin fixture cleanup failed", err)
		}
		_, _ = admin.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{role}.Sanitize())
		if _, err := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
			t.Error("virtual-coin owner cleanup failed", err)
		}
		admin.Close()
		cancel()
	})
	if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.User = role
	cfg.ConnConfig.Password = password
	cfg.MaxConns = 4
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "8s"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "3s"
	f.pool, err = pgxpool.NewWithConfig(ctx, cfg)
	coinMust(t, "open fixture schema", err)
	return f
}

func (f *coinPrefixFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.pool.Exec(f.ctx, query, args...)
	coinMust(t, "fixture SQL", err)
}

func TestVirtualCoinMigrationPreservesAmountsAndCanonical(t *testing.T) {
	f := newCoinPrefixFixture(t)
	var currentUser string
	var superuser bool
	coinMust(t, "confirm restricted migration owner", f.pool.QueryRow(f.ctx, `SELECT current_user,(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&currentUser, &superuser))
	if currentUser == "moni_test" || superuser {
		t.Fatal("virtual-coin migration fixture must use a non-superuser schema owner")
	}
	entries, err := migrations.Files.ReadDir(".")
	coinMust(t, "list embedded current migrations", err)
	names := []string{"current_mvp.sql"}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "current_") && entry.Name() != "current_mvp.sql" && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names[1:])
	f.exec(t, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
	found := false
	for _, name := range names {
		if name == "current_004_virtual_coin_units.sql" {
			found = true
			break
		}
		body, readErr := migrations.Files.ReadFile(name)
		coinMust(t, "read pre-coin migration "+name, readErr)
		f.exec(t, string(body))
		f.exec(t, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", name, fmt.Sprintf("%x", sha256.Sum256(body)))
	}
	if !found {
		t.Fatal("virtual-coin append migration is missing from the current plan")
	}

	tenant, operator, approver, priceActor, model := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	coinMust(t, "begin pre-coin fixture", err)
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'pre-coin')`, tenant); err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct{ id, role string }{{operator, "finance_operator"}, {approver, "finance_approver"}, {priceActor, "admin"}} {
		if _, err = tx.Exec(f.ctx, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)`, user.id, user.id+"@coin.invalid", user.role); err != nil {
			t.Fatal(err)
		}
	}
	profileVersion := "10000000-0000-4000-8000-000000000001"
	profile := `{"version":"` + profileVersion + `","model":"pre-coin-model","protocol":"openai_chat","input_token_upper_bound":100,"context_limit":120,"output_limit":20,"default_output_limit":20,"output_limit_field":"max_tokens"}`
	if _, err = tx.Exec(f.ctx, `SELECT meter_insert_text_model($1,'pre-coin-model',$2::jsonb,$3)`, model, profile, profileVersion); err != nil {
		t.Fatal(err)
	}
	const amount int64 = 123456789
	creditID, journalID, receiptID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	availableID, clearingID := uuid.NewString(), uuid.NewString()
	if _, err = tx.Exec(f.ctx, `INSERT INTO credit_orders(id,tenant_id,business_ref,amount,requester,proof_ref) VALUES($1,$2,$3,$4,$5,'pre-coin-proof')`, creditID, tenant, "pre-coin:"+uuid.NewString(), amount, operator); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO ledger_accounts(id,tenant_id,kind,balance) VALUES($1,$2,'AVAILABLE',$3),($4,NULL,'FUNDING_CLEARING',$5)`, availableID, tenant, amount, clearingID, -amount); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor,currency) VALUES($1,$2,$3,'credit',$3,$4,'CNY')`, journalID, tenant, creditID, approver); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE credit_orders SET state='approved',approver=$2,journal_id=$3 WHERE id=$1`, creditID, approver, journalID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,$3),($1,$4,$5)`, journalID, availableID, amount, clearingID, -amount); err != nil {
		t.Fatal(err)
	}
	var createdAt time.Time
	if err = tx.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,created_at) VALUES($1,$2,'manual_credit',$3,$4,'CNY',$5,$6)`, receiptID, tenant, creditID, amount, journalID, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_lots(id,tenant_id,funding_receipt_id,source_kind,source_id,refund_class,issued_amount,fifo_at) VALUES($1,$2,$1,'manual_credit',$3,'nonrefundable',$4,$5)`, receiptID, tenant, creditID, amount, createdAt); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,held_amount,consumed_amount,revision) VALUES($1,$2,$3,0,0,1)`, receiptID, tenant, amount); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed) VALUES($1,$2,$3,$4,$5,'funding',1,$6,0,0)`, uuid.NewString(), tenant, receiptID, creditID, journalID, amount); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited',jsonb_build_object('amount',($3::bigint)::text))`, tenant, creditID, amount); err != nil {
		t.Fatal(err)
	}
	priceID, priceOperation := uuid.NewString(), uuid.NewString()
	var schemaID, schemaDigest string
	var canonical []byte
	if err = tx.QueryRow(f.ctx, `SELECT c.schema_id,encode(c.schema_digest,'hex'),meter_rate_canonical(c.schema_id,'retail',1,4,1,2) FROM models m JOIN model_capability_versions c ON c.id=m.profile_version AND c.model_id=m.id WHERE m.id=$1`, model).Scan(&schemaID, &schemaDigest, &canonical); err != nil {
		t.Fatal(err)
	}
	effectiveAt := createdAt.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
	safe := map[string]any{"model_id": model, "schema_id": schemaID, "schema_digest": schemaDigest, "variant_key": "default", "currency": "CNY", "rates": json.RawMessage(canonical), "effective_at": effectiveAt}
	result := map[string]any{"id": priceID, "model_id": model, "price_group_id": nil, "schema_id": schemaID, "schema_digest": schemaDigest, "variant_key": "default", "currency": "CNY", "rates": json.RawMessage(canonical), "effective_at": effectiveAt, "created_at": ""}
	safeRaw, _ := json.Marshal(safe)
	resultRaw, _ := json.Marshal(result)
	var priceCreatedAt time.Time
	var snapshot []byte
	if err = tx.QueryRow(f.ctx, `INSERT INTO pricing_operations(operation_id,kind,actor,scope,target,resource_id,safe_input,input_fingerprint,result_snapshot) VALUES($1,'default_price.publish',$2,'platform','/api/platform/price-versions',$3,$4,repeat('0',64),$5) RETURNING created_at,result_snapshot`, priceOperation, priceActor, priceID, safeRaw, resultRaw).Scan(&priceCreatedAt, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `SELECT meter_insert_token_price($1,$2,250000,500000,$3,$4,NULL,$5,$6)`, priceID, model, createdAt, priceActor, priceOperation, priceCreatedAt); err != nil {
		t.Fatal(err)
	}
	diff, _ := json.Marshal(map[string]any{"operation_id": priceOperation, "target": "/api/platform/price-versions", "after": json.RawMessage(snapshot)})
	if _, err = tx.Exec(f.ctx, `INSERT INTO audit_events(actor,action,target,sanitized_diff) VALUES($1,'pricing.price.publish','/api/platform/price-versions',$2)`, priceActor, diff); err != nil {
		t.Fatal(err)
	}
	coinMust(t, "commit complete pre-coin financial graph", tx.Commit(f.ctx))
	credit := struct{ ID, JournalID string }{ID: creditID, JournalID: journalID}
	store := billing.New(f.pool)

	var receiptAmount, journalAmount int64
	var receiptCurrency, journalCurrency, priceCurrency, canonicalBefore, digestBefore string
	coinMust(t, "read pre-coin receipt", f.pool.QueryRow(f.ctx, `SELECT amount,currency FROM funding_receipts WHERE source_kind='manual_credit' AND source_id=$1`, credit.ID).Scan(&receiptAmount, &receiptCurrency))
	coinMust(t, "read pre-coin journal", f.pool.QueryRow(f.ctx, `SELECT (SELECT currency FROM journals WHERE id=$1),(SELECT balance FROM ledger_accounts WHERE tenant_id=$2 AND kind='AVAILABLE')`, credit.JournalID, tenant).Scan(&journalCurrency, &journalAmount))
	coinMust(t, "read pre-coin price", f.pool.QueryRow(f.ctx, `SELECT currency,encode(rates_canonical,'hex'),encode(rates_digest,'hex') FROM price_versions WHERE id=$1`, priceID).Scan(&priceCurrency, &canonicalBefore, &digestBefore))
	if receiptAmount != 123456789 || journalAmount != 123456789 || receiptCurrency != "CNY" || journalCurrency != "CNY" || priceCurrency != "CNY" {
		t.Fatal("pre-coin fixture did not preserve the original CNY contract")
	}

	var fingerprintBefore string
	var originalSafe, originalResult, originalAudit []byte
	coinMust(t, "snapshot immutable pre-coin operation and audit", f.pool.QueryRow(f.ctx, `SELECT safe_input,result_snapshot,input_fingerprint,(SELECT sanitized_diff FROM audit_events WHERE action='pricing.price.publish' AND sanitized_diff->>'operation_id'=$1) FROM pricing_operations WHERE operation_id=$1`, priceOperation).Scan(&originalSafe, &originalResult, &fingerprintBefore, &originalAudit))
	coinMust(t, "apply virtual-coin unit migration", migrations.ApplyCurrent(f.ctx, f.pool))
	coinMust(t, "verify virtual-coin current schema", migrations.VerifyCurrent(f.ctx, f.pool))
	var genesisID, backfilledRate string
	var rateVersions, paidWithoutRate, nonpaidWithRate int
	coinMust(t, "read CNY genesis and populated paid backfill", f.pool.QueryRow(f.ctx, `SELECT r.id,(SELECT f.buy_rate_version_id FROM funding_receipts f WHERE f.source_kind='manual_credit' AND f.source_id=$1),(SELECT count(*) FROM coin_rate_versions),(SELECT count(*) FROM funding_receipts WHERE basis_kind='paid' AND buy_rate_version_id IS NULL),(SELECT count(*) FROM funding_receipts WHERE basis_kind<>'paid' AND buy_rate_version_id IS NOT NULL) FROM coin_rate_versions r WHERE r.fiat_currency='CNY' AND r.rate_num=1 AND r.rate_den=1 AND r.effective_at='1970-01-01T00:00:00Z' AND r.maker IS NULL AND r.approver IS NULL AND NOT r.single_operator`, credit.ID).Scan(&genesisID, &backfilledRate, &rateVersions, &paidWithoutRate, &nonpaidWithRate))
	if genesisID == "" || backfilledRate != genesisID || rateVersions != 1 || paidWithoutRate != 0 || nonpaidWithRate != 0 {
		t.Fatalf("coin-rate genesis/backfill mismatch: genesis=%s receipt=%s versions=%d paid_without=%d nonpaid_with=%d", genesisID, backfilledRate, rateVersions, paidWithoutRate, nonpaidWithRate)
	}
	var afterSafe, afterResult, afterFingerprint, afterAudit []byte
	coinMust(t, "read immutable migrated operation and audit", f.pool.QueryRow(f.ctx, `SELECT safe_input,result_snapshot,input_fingerprint,(SELECT sanitized_diff FROM audit_events WHERE action='pricing.price.publish' AND sanitized_diff->>'operation_id'=$1) FROM pricing_operations WHERE operation_id=$1`, priceOperation).Scan(&afterSafe, &afterResult, &afterFingerprint, &afterAudit))
	if !bytes.Equal(afterSafe, originalSafe) || !bytes.Equal(afterResult, originalResult) || string(afterFingerprint) != fingerprintBefore || !bytes.Equal(afterAudit, originalAudit) {
		t.Fatal("migration changed immutable pricing command, fingerprint, result, or audit bytes")
	}
	var afterReceiptAmount, afterJournalAmount int64
	var afterReceiptCurrency, afterJournalCurrency, afterPriceCurrency, canonicalAfter, digestAfter string
	coinMust(t, "read migrated receipt", f.pool.QueryRow(f.ctx, `SELECT amount,currency FROM funding_receipts WHERE source_kind='manual_credit' AND source_id=$1`, credit.ID).Scan(&afterReceiptAmount, &afterReceiptCurrency))
	coinMust(t, "read migrated journal", f.pool.QueryRow(f.ctx, `SELECT (SELECT currency FROM journals WHERE id=$1),(SELECT balance FROM ledger_accounts WHERE tenant_id=$2 AND kind='AVAILABLE')`, credit.JournalID, tenant).Scan(&afterJournalCurrency, &afterJournalAmount))
	coinMust(t, "read migrated price", f.pool.QueryRow(f.ctx, `SELECT currency,encode(rates_canonical,'hex'),encode(rates_digest,'hex') FROM price_versions WHERE id=$1`, priceID).Scan(&afterPriceCurrency, &canonicalAfter, &digestAfter))
	if afterReceiptAmount != receiptAmount || afterJournalAmount != journalAmount || afterReceiptCurrency != "COIN" || afterJournalCurrency != "COIN" || afterPriceCurrency != "COIN" {
		t.Fatalf("unit migration changed amounts or missed a retail currency: receipt=%d/%s journal=%d/%s price=%s", afterReceiptAmount, afterReceiptCurrency, afterJournalAmount, afterJournalCurrency, afterPriceCurrency)
	}
	if canonicalAfter != canonicalBefore || digestAfter != digestBefore {
		t.Fatal("unit migration changed frozen metering canonical bytes or digest")
	}
	var inputNumerator, inputDenominator, outputNumerator, outputDenominator int64
	coinMust(t, "read migrated numeric rates", f.pool.QueryRow(f.ctx, `SELECT (SELECT numerator FROM price_rate_lines WHERE price_version_id=$1 AND dimension='text_input_token'),(SELECT denominator FROM price_rate_lines WHERE price_version_id=$1 AND dimension='text_input_token'),(SELECT numerator FROM price_rate_lines WHERE price_version_id=$1 AND dimension='text_output_token'),(SELECT denominator FROM price_rate_lines WHERE price_version_id=$1 AND dimension='text_output_token')`, priceID).Scan(&inputNumerator, &inputDenominator, &outputNumerator, &outputDenominator))
	if inputNumerator != 1 || inputDenominator != 4 || outputNumerator != 1 || outputDenominator != 2 {
		t.Fatalf("unit migration rescaled retail rates: %d/%d %d/%d", inputNumerator, inputDenominator, outputNumerator, outputDenominator)
	}
	var refundHeld, retired, deltaRefundHeld, deltaRetired int64
	coinMust(t, "read zero-default refund buckets after populated upgrade", f.pool.QueryRow(f.ctx, `SELECT b.refund_held_amount,b.retired_amount,e.delta_refund_held,e.delta_retired FROM funding_lot_balances b JOIN funding_receipts f ON f.id=b.lot_id AND f.source_id=$1 JOIN funding_lot_events e ON e.lot_id=b.lot_id AND e.revision=1`, credit.ID).Scan(&refundHeld, &retired, &deltaRefundHeld, &deltaRetired))
	if refundHeld != 0 || retired != 0 || deltaRefundHeld != 0 || deltaRetired != 0 {
		t.Fatal("populated upgrade changed historical refund buckets or deltas")
	}
	var valid bool
	coinMust(t, "validate migrated funding journal", f.pool.QueryRow(f.ctx, `SELECT funding_journal_valid($1)`, credit.JournalID).Scan(&valid))
	if !valid {
		t.Fatal("migrated funding journal no longer satisfies its complete source guard")
	}
	check, err := store.VerifyFunding(f.ctx, tenant)
	coinMust(t, "verify migrated funding graph", err)
	if !check.Balanced {
		t.Fatalf("migrated funding graph is unbalanced: %+v", check)
	}
	wallet, err := store.CheckWalletSources(f.ctx, tenant)
	coinMust(t, "verify five-bucket wallet graph after populated upgrade", err)
	if !wallet.Balanced {
		t.Fatalf("migrated wallet sources are unbalanced: %+v", wallet)
	}
	pricingStore := catalog.New(f.pool, nil)
	replayed, err := pricingStore.PublishDefaultPrice(f.ctx, catalog.PricingActor{UserID: priceActor}, catalog.DefaultPriceCommand{OperationID: priceOperation, ModelID: model, SchemaID: schemaID, SchemaDigest: schemaDigest, VariantKey: "default", InputRate: 250000, OutputRate: 500000, EffectiveAt: createdAt})
	coinMust(t, "replay migrated default-price operation", err)
	if !replayed.Replayed || replayed.Result.ID != priceID || replayed.Result.Currency != "COIN" || replayed.Operation.PreCoinInputFingerprint == nil || *replayed.Operation.PreCoinInputFingerprint != fingerprintBefore || replayed.Operation.InputFingerprint == fingerprintBefore {
		t.Fatalf("migrated pricing operation did not preserve its replay identity: %+v", replayed)
	}
	var priceCount int
	coinMust(t, "count prices after migrated replay", f.pool.QueryRow(f.ctx, `SELECT count(*) FROM price_versions WHERE id=$1`, priceID).Scan(&priceCount))
	if priceCount != 1 {
		t.Fatal("migrated replay inserted a duplicate price version")
	}
	coinMust(t, "repeat virtual-coin migration idempotently", migrations.ApplyCurrent(f.ctx, f.pool))
}
