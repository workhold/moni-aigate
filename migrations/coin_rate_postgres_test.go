package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/operations"
	p "github.com/moni-ai/moni-aigate/internal/paymentproto"
	"github.com/moni-ai/moni-aigate/migrations"
)

const coinSandboxTimeLayout = "2006-01-02T15:04:05.000000Z"

func coinSandboxExec(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func coinSandboxCommand(t *testing.T, tx pgx.Tx, operation, kind, actor, scope string) {
	t.Helper()
	coinSandboxExec(t, tx, `INSERT INTO commerce_commands(operation_id,actor,scope,kind,request_digest) VALUES($1,$2,$3,$4,$5)`, operation, actor, scope, kind, strings.Repeat("a", 64))
}

func coinSandboxTerminal(t *testing.T, tx pgx.Tx, operation, resource string) {
	t.Helper()
	coinSandboxExec(t, tx, `INSERT INTO commerce_operation_receipts(operation_id,kind,scope,actor,status,reason,resource_id) SELECT operation_id,kind,scope,actor,'accepted','accepted',$2 FROM commerce_commands WHERE operation_id=$1`, operation, resource)
	coinSandboxExec(t, tx, `INSERT INTO audit_events(actor,tenant_scope,action,target) SELECT actor,CASE WHEN scope='platform' THEN NULL ELSE substr(scope,8) END,kind,operation_id FROM commerce_commands WHERE operation_id=$1`, operation)
}

func TestCoinRateMigrationBackfillsCommittedSandboxCapture(t *testing.T) {
	f := newCoinPrefixFixture(t)
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"current_mvp.sql"}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "current_") && entry.Name() != "current_mvp.sql" && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names[1:])
	f.exec(t, `CREATE TABLE schema_migrations(name TEXT PRIMARY KEY,checksum TEXT NOT NULL,applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp())`)
	installed := false
	for _, name := range names {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(body))
		f.exec(t, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, name, fmt.Sprintf("%x", sha256.Sum256(body)))
		if name == "current_007_refund_buckets.sql" {
			installed = true
			break
		}
	}
	if !installed {
		t.Fatal("current_007 migration is missing")
	}
	tenant, user, adminActor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO tenants(id,name) VALUES($1,'coin-rate-sandbox')`, tenant)
	f.exec(t, `INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture','none'),($3,$4,'fixture','admin')`, user, user+"@coin.invalid", adminActor, adminActor+"@coin.invalid")
	f.exec(t, `INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, tenant, user)

	admin, err := pgxpool.New(f.ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	roles := []string{"coin_app_" + suffix, "coin_worker_" + suffix, "coin_projection_" + suffix}
	secrets := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	var app, worker *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if worker != nil {
			worker.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		for _, role := range roles {
			quoted := pgx.Identifier{role}.Sanitize()
			if _, err := admin.Exec(cleanup, "DROP OWNED BY "+quoted); err != nil {
				t.Error(err)
			}
			if _, err := admin.Exec(cleanup, "DROP ROLE IF EXISTS "+quoted); err != nil {
				t.Error(err)
			}
		}
		admin.Close()
	})
	for i, role := range roles {
		if _, err = admin.Exec(f.ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+secrets[i]+"'"); err != nil {
			t.Fatal(err)
		}
	}
	environment := uuid.NewString()
	if err = operations.ProvisionFor(f.ctx, f.pool, f.schema, roles[0], roles[2]); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO payment_sandbox_environment(id,merchant_id,payee_label,database_oid,schema_oid,nonce_digest,app_role,worker_role,projector_role) SELECT $1,'fixture','fixture',d.oid,n.oid,$2,$3,$4,$5 FROM pg_database d CROSS JOIN pg_namespace n WHERE d.datname=current_database() AND n.nspname=$6`, environment, strings.Repeat("1", 64), roles[0], roles[1], roles[2], f.schema)
	if err = operations.ProvisionPaymentWorkerFor(f.ctx, f.pool, f.schema, roles[0], roles[1], roles[2]); err != nil {
		t.Fatal(err)
	}
	for i, target := range []**pgxpool.Pool{&app, &worker} {
		cfg := f.pool.Config().Copy()
		cfg.ConnConfig.User = roles[i]
		cfg.ConnConfig.Password = secrets[i]
		cfg.MaxConns = 2
		pool, err := pgxpool.NewWithConfig(f.ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		*target = pool
	}

	sku, version := uuid.NewString(), uuid.NewString()
	tx, err := app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	operation := uuid.NewString()
	coinSandboxCommand(t, tx, operation, "payment.sku.publish", adminActor, "platform")
	coinSandboxExec(t, tx, `INSERT INTO payment_skus(id,created_by) VALUES($1,$2)`, sku, adminActor)
	coinSandboxExec(t, tx, `INSERT INTO payment_sku_versions(id,sku_id,version,display_name,amount,wallet_credit_amount,environment_id,merchant_id,payee_label,publish_operation_id,created_by) VALUES($1,$2,1,'fixture',100,100,$3,'fixture','fixture',$4,$5)`, version, sku, environment, operation, adminActor)
	coinSandboxExec(t, tx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,published_version_id,before_enabled,after_enabled) VALUES($1,$2,'payment.sku.publish',0,1,$3,false,false)`, operation, sku, version)
	coinSandboxTerminal(t, tx, operation, version)
	operation = uuid.NewString()
	coinSandboxCommand(t, tx, operation, "payment.sku.activate", adminActor, "platform")
	coinSandboxExec(t, tx, `INSERT INTO payment_sku_changes(operation_id,sku_id,kind,before_revision,after_revision,before_enabled,after_enabled,after_version_id) VALUES($1,$2,'payment.sku.activate',1,2,false,true,$3)`, operation, sku, version)
	coinSandboxTerminal(t, tx, operation, sku)
	coinSandboxExec(t, tx, `UPDATE payment_policy SET enabled=true`)

	order, evidence := uuid.NewString(), uuid.NewString()
	coinSandboxCommand(t, tx, order, "payment.order.create", user, "tenant:"+tenant)
	coinSandboxExec(t, tx, `INSERT INTO payment_orders(id,tenant_id,creator,sku_version_id,display_name,amount,wallet_credit_amount,environment_id,merchant_id,payee_label,merchant_order_id,created_at,expires_at) VALUES($1,$2,$3,$4,'ignored',1,1,$5,'ignored','ignored','ignored',clock_timestamp(),clock_timestamp())`, order, tenant, user, version, environment)
	var merchant, transaction string
	var created, expires, now time.Time
	if err = tx.QueryRow(f.ctx, `SELECT merchant_order_id,created_at,expires_at,clock_timestamp() FROM payment_orders WHERE id=$1`, order).Scan(&merchant, &created, &expires, &now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	command, outbox := uuid.NewString(), uuid.NewString()
	body, err := p.CanonicalCheckout(p.CheckoutRequest{SchemaVersion: 1, EnvironmentID: environment, MerchantID: "fixture", CommandID: command, MerchantOrderID: merchant, Amount: "100", Currency: "CNY", CreatedAt: created.UTC().Format(coinSandboxTimeLayout), ExpiresAt: expires.UTC().Format(coinSandboxTimeLayout)})
	if err != nil {
		t.Fatal(err)
	}
	coinSandboxExec(t, tx, `INSERT INTO payment_command_outbox(id,order_id,kind,external_command_id,canonical_parameters) VALUES($1,$2,'checkout',$3,$4)`, outbox, order, command, body)
	coinSandboxTerminal(t, tx, order, order)
	transaction = "fixture:" + uuid.NewString()
	body, err = p.CanonicalNotification(p.Notification{SchemaVersion: 1, EventID: "event:" + uuid.NewString(), EventType: "capture.confirmed", EnvironmentID: environment, MerchantID: "fixture", OrderID: order, ProviderTransactionID: &transaction, Amount: "100", Currency: "CNY", OccurredAt: now.UTC().Format(coinSandboxTimeLayout), ProviderOrderRevision: "2"})
	if err != nil {
		t.Fatal(err)
	}
	signing := bytes.Repeat([]byte{7}, 32)
	keys, err := p.NewKeyring("test", []p.KeySpec{{ID: "test", Key: signing, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	env := p.Environment{ID: environment, MerchantID: "fixture", PayeeLabel: "fixture"}
	headers, err := keys.SignNotification(env, p.SignedHeaders{KeyID: "test", DeliveryID: uuid.NewString(), DeliveryTime: strconv.FormatInt(now.Unix(), 10)}, body)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := keys.VerifyNotification(env, now, headers, body)
	if err != nil {
		t.Fatal(err)
	}
	body, err = verified.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	inbox := uuid.NewString()
	coinSandboxExec(t, tx, `INSERT INTO payment_inbox(id,environment_id,merchant_id,event_id,canonical_body,verified_key_id) VALUES($1,$2,'fixture','ignored',$3,'test')`, inbox, environment, body)
	coinSandboxExec(t, tx, `INSERT INTO payment_verified_evidence(id,inbox_id,environment_id,merchant_id,order_id,kind,fact_digest,verified_at) VALUES($1,$2,$3,'fixture',$4,'capture',$5,clock_timestamp())`, evidence, inbox, environment, order, strings.Repeat("0", 64))
	coinSandboxExec(t, tx, `INSERT INTO payment_evidence_processing(evidence_id) VALUES($1)`, evidence)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	coinSandboxExec(t, worker, `UPDATE payment_evidence_processing SET state='leased',generation=generation+1,attempt_count=attempt_count+1 WHERE evidence_id=$1`, evidence)

	tx, err = worker.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	coinSandboxExec(t, tx, `SELECT id FROM payment_orders WHERE id=$1 FOR NO KEY UPDATE`, order)
	coinSandboxExec(t, tx, `INSERT INTO payment_provider_revisions(environment_id,merchant_id,order_id,provider_order_revision,kind,fact_digest,evidence_id) VALUES($1,'fixture',$2,2,'capture',$3,$4)`, environment, order, strings.Repeat("0", 64), evidence)
	identity, capture := uuid.NewString(), uuid.NewString()
	coinSandboxExec(t, tx, `INSERT INTO payment_transaction_identities(id,environment_id,merchant_id,provider_transaction_id,order_id,tenant_id,amount,currency,captured_at,provider_order_revision,evidence_id,processing_generation) SELECT $1,environment_id,merchant_id,provider_transaction_id,order_id,$2,amount,currency,happened_at,provider_order_revision,id,1 FROM payment_verified_evidence WHERE id=$3`, identity, tenant, evidence)
	coinSandboxExec(t, tx, `INSERT INTO payment_order_capture_selections(order_id,identity_id,first_evidence_id) VALUES($1,$2,$3)`, order, identity, evidence)
	coinSandboxExec(t, tx, `SELECT id FROM tenants WHERE id=$1 FOR SHARE`, tenant)
	coinSandboxExec(t, tx, `INSERT INTO payment_captures(id,order_id,identity_id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,wallet_credit_amount,currency,captured_at,provider_order_revision,verified_evidence_id,processing_generation) SELECT $1,order_id,id,tenant_id,environment_id,merchant_id,provider_transaction_id,amount,amount,currency,captured_at,provider_order_revision,evidence_id,1 FROM payment_transaction_identities WHERE id=$2`, capture, identity)
	coinSandboxExec(t, tx, `INSERT INTO payment_effects(capture_id,identity_id,environment_id,merchant_id,provider_transaction_id) SELECT $1,id,environment_id,merchant_id,provider_transaction_id FROM payment_transaction_identities WHERE id=$2`, capture, identity)
	coinSandboxExec(t, tx, `INSERT INTO ledger_accounts(tenant_id,kind) VALUES($1,'AVAILABLE'),(NULL,'SANDBOX_FUNDING_CLEARING') ON CONFLICT DO NOTHING`, tenant)
	var availableAccount, clearingAccount string
	if err = tx.QueryRow(f.ctx, `SELECT (SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE'),(SELECT id FROM ledger_accounts WHERE tenant_id IS NULL AND kind='SANDBOX_FUNDING_CLEARING')`, tenant).Scan(&availableAccount, &clearingAccount); err != nil {
		t.Fatal(err)
	}
	journal, receipt := uuid.NewString(), uuid.NewString()
	coinSandboxExec(t, tx, `INSERT INTO journals(id,tenant_id,operation_id,kind,reference,actor) VALUES($1,$2,$3,'sandbox_capture',$4,'payment-sandbox-worker')`, journal, tenant, capture, order)
	coinSandboxExec(t, tx, `UPDATE ledger_accounts SET balance=balance+100 WHERE id=$1`, availableAccount)
	coinSandboxExec(t, tx, `UPDATE ledger_accounts SET balance=balance-100 WHERE id=$1`, clearingAccount)
	coinSandboxExec(t, tx, `INSERT INTO journal_lines(journal_id,account_id,signed_delta) VALUES($1,$2,100),($1,$3,-100)`, journal, availableAccount, clearingAccount)
	coinSandboxExec(t, tx, `INSERT INTO funding_receipts(id,tenant_id,source_kind,source_id,amount,currency,journal_id,basis_kind,fiat_currency,fiat_amount,parent_ref) VALUES($1,$2,'sandbox_capture',$3,100,'COIN',$4,'paid','CNY',100,$3)`, receipt, tenant, capture, journal)
	coinSandboxExec(t, tx, `INSERT INTO funding_lots(id,tenant_id,funding_receipt_id,source_kind,source_id,refund_class,issued_amount,fifo_at) SELECT id,tenant_id,id,'sandbox_capture',source_id,'sandbox_original',100,created_at FROM funding_receipts WHERE id=$1`, receipt)
	coinSandboxExec(t, tx, `INSERT INTO funding_lot_balances(lot_id,tenant_id,available_amount,held_amount,consumed_amount,revision) VALUES($1,$2,100,0,0,1)`, receipt, tenant)
	coinSandboxExec(t, tx, `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed) VALUES($1,$2,$3,$4,$5,'funding',1,100,0,0)`, uuid.NewString(), tenant, receipt, capture, journal)
	coinSandboxExec(t, tx, `INSERT INTO projection_outbox(tenant_id,operation_id,kind,sanitized_payload) VALUES($1,$2,'credited','{"amount":"100"}')`, tenant, capture)
	coinSandboxExec(t, tx, `INSERT INTO audit_events(actor,tenant_scope,action,target,sanitized_diff) VALUES('payment-sandbox-worker',$1,'payment.capture.credited',$2,'{"amount":"100"}')`, tenant, capture)
	if tag, err := tx.Exec(f.ctx, `UPDATE payment_evidence_processing SET state='processed' WHERE evidence_id=$1 AND state='leased' AND generation=1 AND lease_until>clock_timestamp()`, evidence); err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("processing CAS failed: %v rows=%d", err, tag.RowsAffected())
	}
	var preMigrationValid bool
	if err = tx.QueryRow(f.ctx, `SELECT payment_sandbox_journal_valid($1)`, journal).Scan(&preMigrationValid); err != nil || !preMigrationValid {
		t.Fatalf("pre-migration sandbox graph is invalid: %v valid=%v", err, preMigrationValid)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(fmt.Errorf("commit complete current_007 sandbox graph: %w", err))
	}

	if err = migrations.ApplyCurrent(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	if err = migrations.VerifyCurrent(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	var backfilled, genesis string
	if err = f.pool.QueryRow(f.ctx, `SELECT f.buy_rate_version_id,(SELECT r.id FROM coin_rate_versions r WHERE r.fiat_currency='CNY' AND r.rate_num=1 AND r.rate_den=1 AND r.effective_at='1970-01-01T00:00:00Z') FROM funding_receipts f WHERE f.id=$1`, receipt).Scan(&backfilled, &genesis); err != nil || backfilled == "" || backfilled != genesis {
		t.Fatalf("sandbox backfill mismatch: backfilled=%s genesis=%s err=%v", backfilled, genesis, err)
	}
	var valid bool
	if err = f.pool.QueryRow(f.ctx, `SELECT payment_sandbox_journal_valid($1)`, journal).Scan(&valid); err != nil || !valid {
		t.Fatalf("migrated sandbox journal is invalid: %v valid=%v", err, valid)
	}
	check, err := billing.New(f.pool).CheckWalletSources(f.ctx, tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("migrated sandbox wallet graph is unbalanced: %+v %v", check, err)
	}
	if err = migrations.ApplyCurrent(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
}
