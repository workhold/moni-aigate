package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/operations"
	"github.com/moni-ai/moni-aigate/internal/testsupport/meterquote"
)

// Setup and independent assertions retain the dedicated fixture's owner pool.
// Every financial command uses a separately authenticated, provisioned APP;
// SET ROLE on an owner session is deliberately insufficient for this test.
// No sandbox environment, payment policy, worker, or network is started.
func walletSourcesApplication(t *testing.T, f *fixture) *pgxpool.Pool {
	t.Helper()
	var schema string
	if err := f.pool.QueryRow(f.ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	prefix := "wallet_app_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	appRole, projectionRole := prefix+"_app", prefix+"_projection"
	var app *pgxpool.Pool
	created := []string{}
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		for _, role := range created {
			for _, statement := range []string{`DROP OWNED BY ` + pgx.Identifier{role}.Sanitize(), `DROP ROLE ` + pgx.Identifier{role}.Sanitize()} {
				if _, err := f.pool.Exec(context.Background(), statement); err != nil {
					t.Error(err)
				}
			}
		}
	})
	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	secret := hex.EncodeToString(password)
	for _, role := range []string{appRole, projectionRole} {
		login := " NOLOGIN"
		if role == appRole {
			// The value is random hexadecimal, never supplied by a caller.
			login = " LOGIN PASSWORD '" + secret + "'"
		}
		if _, err := f.pool.Exec(f.ctx, `CREATE ROLE `+pgx.Identifier{role}.Sanitize()+login+` NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`); err != nil {
			t.Fatal(err)
		}
		created = append(created, role)
	}
	if err := operations.ProvisionFor(f.ctx, f.pool, schema, appRole, projectionRole); err != nil {
		t.Fatal("provision actual wallet APP", err)
	}
	cfg := f.pool.Config().Copy()
	cfg.AfterConnect = nil
	cfg.ConnConfig.User, cfg.ConnConfig.Password = appRole, secret
	cfg.MaxConns = 4
	var err error
	app, err = pgxpool.NewWithConfig(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var current, session string
	var elevated, ownsSchema, ownsWallet bool
	err = app.QueryRow(f.ctx, `SELECT current_user,session_user,
 (r.rolsuper OR r.rolinherit OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls),
 n.nspowner=r.oid,c.relowner=r.oid
 FROM pg_roles r JOIN pg_namespace n ON n.nspname=current_schema()
 JOIN pg_class c ON c.relnamespace=n.oid AND c.relname='funding_lot_balances'
 WHERE r.rolname=current_user`).Scan(&current, &session, &elevated, &ownsSchema, &ownsWallet)
	if err != nil || current != appRole || session != appRole || elevated || ownsSchema || ownsWallet {
		t.Fatal("wallet test did not authenticate as an unprivileged APP", current, session, elevated, ownsSchema, ownsWallet, err)
	}
	if err = operations.VerifyPaymentRole(f.ctx, app, operations.PaymentApplicationRole); err != nil {
		t.Fatal("actual wallet APP privilege boundary", err)
	}
	f.store = New(app)
	return app
}

func walletApplicationChecks(t *testing.T, f *fixture) {
	t.Helper()
	walletSourcesAssertBalanced(t, f, f.tenant)
	ledger, err := f.store.VerifyLedger(f.ctx, Query{TenantID: f.tenant})
	if err != nil || !ledger.Balanced || ledger.AccountMismatches != 0 || ledger.UnbalancedJournals != 0 {
		t.Fatalf("actual APP ledger check: %+v %v", ledger, err)
	}
	funding, err := f.store.VerifyFunding(f.ctx, f.tenant)
	if err != nil || !funding.Balanced {
		t.Fatalf("actual APP funding check: %+v %v", funding, err)
	}
}

// Query only fixed projections from the independent owner connection. Expected
// small integer amounts are hand calculated, not obtained from the lot planner.
func walletApplicationRows(t *testing.T, f *fixture, query string, expected [][]string, args ...any) {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := [][]string{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(values))
		for i, value := range values {
			var ok bool
			row[i], ok = value.(string)
			if !ok {
				t.Fatalf("non-text assertion projection at column %d", i)
			}
		}
		actual = append(actual, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("actual APP financial rows=%v want=%v", actual, expected)
	}
}

func TestPostgresWalletSourcesApplicationFinancialLifecycle(t *testing.T) {
	f := walletSourcesFixture(t)
	walletSourcesApplication(t, f)
	f.wallet(t, 0, 0)
	walletApplicationChecks(t, f)

	// Both manual receipts, genesis lots, and their dual-review approvals are
	// born through the actual APP, never inserted by the fixture owner.
	lots := []string{}
	for _, amount := range []Money{70, 80} {
		order := fundingCredit(t, f, f.tenant, amount)
		approved := fundingApprove(t, f, order)
		if approved.State != "approved" || approved.Requester != f.operator || approved.Approver != f.approver || approved.JournalID == "" || approved.Requester == approved.Approver {
			t.Fatal("manual approval did not retain distinct maker and approver", approved)
		}
		var lot string
		if err := f.pool.QueryRow(f.ctx, `SELECT id FROM funding_receipts WHERE tenant_id=$1 AND source_kind='manual_credit' AND source_id=$2`, f.tenant, order.ID).Scan(&lot); err != nil {
			t.Fatal(err)
		}
		lots = append(lots, lot)
		before := walletSourceSnapshot(t, f)
		receiptsBefore := walletSourcesReadSnapshot(t, f)
		again := fundingApprove(t, f, order)
		if again.ID != approved.ID || again.JournalID != approved.JournalID || walletSourceSnapshot(t, f) != before || walletSourcesReadSnapshot(t, f) != receiptsBefore {
			t.Fatal("repeated actual APP credit approval changed financial facts")
		}
	}
	f.wallet(t, 150, 0)
	walletApplicationRows(t, f, `SELECT id,source_kind,refund_class,issued_amount::text FROM funding_lots WHERE tenant_id=$1 ORDER BY fifo_at,id COLLATE "C"`, [][]string{{lots[0], "manual_credit", "cash_original", "70"}, {lots[1], "manual_credit", "cash_original", "80"}}, f.tenant)
	walletApplicationChecks(t, f)

	// Publish a legal two-channel fallback route before freezing the request.
	// A safe retry must not reuse the first channel, even with another key.
	retryChannel, retryVersion, retryCredential, route := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO channels(id,name,current_version) VALUES($1,'wallet source retry',$2)`, []any{retryChannel, retryVersion}},
		{`INSERT INTO channel_credentials(id,channel_id,secret_ciphertext) VALUES($1,$2,'synthetic-only')`, []any{retryCredential, retryChannel}},
		{`INSERT INTO channel_versions(id,channel_id,base_url,credential_id) VALUES($1,$2,'https://provider.invalid',$3)`, []any{retryVersion, retryChannel, retryCredential}},
		{`INSERT INTO channel_version_credentials(channel_version_id,channel_id,credential_id,position,label,weight,max_inflight) VALUES($1,$2,$3,1,'primary',1,10)`, []any{retryVersion, retryChannel, retryCredential}},
		{`INSERT INTO route_policy_versions(id,model_id,revision) VALUES($1,$2,1)`, []any{route, f.model}},
		{`INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,10)`, []any{route, f.channel, f.version}},
		{`INSERT INTO model_bindings(route_policy_version_id,channel_id,channel_version_id,upstream_model,weight,max_inflight) VALUES($1,$2,$3,'upstream',1,10)`, []any{route, retryChannel, retryVersion}},
		{`UPDATE models SET active_revision=1 WHERE id=$1`, []any{f.model}},
	} {
		if _, err = tx.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	meterquote.PublishCost(t, f.ctx, tx, meterquote.CostPrice{ChannelID: retryChannel, UpstreamModel: "upstream", ModelID: f.model, Actor: f.admin, Basis: "contract", InputRate: billingRate(2000000), OutputRate: billingRate(2000000)})
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// Exercise the no-consumption finish branch while the original 150 balance
	// can cover the profile's mandatory 100+20 token reservation. Full release
	// must return both original FIFO allocations before the charge/refund case.
	releaseInput := f.input()
	releaseInput.RouteRevision = 1
	release, err := f.store.Reserve(f.ctx, releaseInput)
	if err != nil || release.Amount != 120 {
		t.Fatal("actual APP release reservation", release, err)
	}
	defer f.store.EndRequest(release.RequestID)
	f.wallet(t, 30, 120)
	walletApplicationRows(t, f, `SELECT lot_id,allocation_index::text,amount::text FROM funding_reservation_lots WHERE reservation_id=$1 ORDER BY allocation_index`, [][]string{{lots[0], "1", "70"}, {lots[1], "2", "50"}}, release.ID)
	released, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: release.RequestID, Reason: "not_sent"})
	if err != nil || released.Status != "released" || released.ChargedAmount != 0 {
		t.Fatal("actual APP release", released, err)
	}
	walletApplicationRows(t, f, `SELECT lot_id,charged_amount::text,released_amount::text FROM funding_settlement_lots WHERE settlement_id=$1 ORDER BY allocation_index`, [][]string{{lots[0], "0", "70"}, {lots[1], "0", "50"}}, released.ID)
	f.wallet(t, 150, 0)
	walletApplicationRows(t, f, `SELECT spent::text,held::text FROM api_keys WHERE id=$1`, [][]string{{"0", "0"}}, f.key)
	walletApplicationChecks(t, f)
	f.store.EndRequest(release.RequestID)

	reserveInput := f.input()
	reserveInput.RouteRevision = 1
	r, err := f.store.Reserve(f.ctx, reserveInput)
	if err != nil {
		t.Fatal("actual APP frozen two-channel route reservation", err)
	}
	defer f.store.EndRequest(r.RequestID)
	if r.Amount != 120 {
		t.Fatal("unexpected conservative reservation", r)
	}
	f.wallet(t, 30, 120)
	walletApplicationRows(t, f, `SELECT lot_id,allocation_index::text,amount::text FROM funding_reservation_lots WHERE reservation_id=$1 ORDER BY allocation_index`, [][]string{{lots[0], "1", "70"}, {lots[1], "2", "50"}}, r.ID)
	walletApplicationChecks(t, f)

	// A trustworthy no-send terminal permits one second upstream attempt.
	// Neither admission nor either evidence append can allocate customer funds
	// again. No provider is contacted by this test.
	beforeAttempts := walletSourcesReadSnapshot(t, f)
	input := AttemptInput{TenantID: f.tenant, RequestID: r.RequestID, ChannelID: f.channel, ChannelVersionID: f.version, CredentialID: f.credential, UpstreamModel: "upstream", Sequence: 1}
	first, err := f.store.RecordAttempt(f.ctx, input)
	if err != nil {
		t.Fatal("actual APP first attempt", err)
	}
	if _, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: first.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "missing"}, ProtocolOutcome: "failed", TransportEndReason: "not_sent", DeliveryStage: "none"}); err != nil {
		t.Fatal("actual APP safe retry evidence", err)
	}
	input.Sequence = 2
	input.ChannelID, input.ChannelVersionID, input.CredentialID = retryChannel, retryVersion, retryCredential
	second, err := f.store.RecordAttempt(f.ctx, input)
	if err != nil || second.Sequence != 2 || second.ID == first.ID {
		t.Fatal("actual APP second safe attempt", second, err)
	}
	if walletSourcesReadSnapshot(t, f) != beforeAttempts {
		t.Fatal("safe second attempt changed customer source allocations or money")
	}
	walletApplicationRows(t, f, `SELECT sequence::text,channel_id,channel_version_id,credential_id FROM upstream_attempts WHERE tenant_id=$1 AND request_id=$2 ORDER BY sequence`, [][]string{{"1", f.channel, f.version, f.credential}, {"2", retryChannel, retryVersion, retryCredential}}, f.tenant, r.RequestID)
	if _, err = f.store.RecordFinalization(f.ctx, FinalizationInput{TenantID: f.tenant, RequestID: r.RequestID, AttemptID: second.ID, Source: "adapter", SourceEventID: uuid.NewString(), Usage: Usage{Status: "confirmed_final", InputTokens: 90, OutputTokens: 10, TotalTokens: 100}, ProtocolOutcome: "completed", TransportEndReason: "eof", DeliveryStage: "content", ContentWritten: true}); err != nil {
		t.Fatal("actual APP final usage", err)
	}
	settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || settled.Status != "settled" || settled.ChargedAmount != 100 {
		t.Fatal("actual APP settlement", settled, err)
	}
	walletApplicationRows(t, f, `SELECT lot_id,allocation_index::text,charged_amount::text,released_amount::text FROM funding_settlement_lots WHERE settlement_id=$1 ORDER BY allocation_index`, [][]string{{lots[0], "1", "70", "0"}, {lots[1], "2", "30", "20"}}, settled.ID)
	f.wallet(t, 50, 0)
	walletApplicationChecks(t, f)
	settledBefore := walletSourceSnapshot(t, f)
	again, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || again.ID != settled.ID || walletSourceSnapshot(t, f) != settledBefore {
		t.Fatal("repeated actual APP settlement changed financial facts", again, err)
	}

	for index, amount := range []Money{20, 25} {
		refund := walletSourceRefund(t, f, settled.ID, amount)
		approved, err := f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver})
		if err != nil || approved.State != "approved" || approved.Requester != f.operator || approved.Approver != f.approver || approved.JournalID == "" {
			t.Fatal("actual APP partial refund", approved, err)
		}
		expected := [][]string{{lots[1], "2", "1", "20"}}
		if index == 1 {
			expected = [][]string{{lots[1], "2", "1", "10"}, {lots[0], "1", "2", "15"}}
		}
		walletApplicationRows(t, f, `SELECT lot_id,allocation_index::text,refund_index::text,amount::text FROM funding_consumption_refund_lots WHERE refund_order_id=$1 ORDER BY refund_index`, expected, refund.ID)
		before := walletSourceSnapshot(t, f)
		if _, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver}); err != nil || walletSourceSnapshot(t, f) != before {
			t.Fatal("repeated actual APP partial refund changed financial facts", err)
		}
		walletApplicationChecks(t, f)
	}
	f.wallet(t, 95, 0)
	walletApplicationRows(t, f, `SELECT lot_id,available_amount::text,held_amount::text,consumed_amount::text,refund_held_amount::text,retired_amount::text FROM funding_lot_balances WHERE lot_id=$1`, [][]string{{lots[0], "15", "0", "55", "0", "0"}}, lots[0])
	walletApplicationRows(t, f, `SELECT lot_id,available_amount::text,held_amount::text,consumed_amount::text,refund_held_amount::text,retired_amount::text FROM funding_lot_balances WHERE lot_id=$1`, [][]string{{lots[1], "80", "0", "0", "0", "0"}}, lots[1])
	walletApplicationRows(t, f, `SELECT spent::text,held::text FROM api_keys WHERE id=$1`, [][]string{{"100", "0"}}, f.key)
	walletApplicationRows(t, f, `SELECT charged_amount::text,refunded_amount::text FROM settlements WHERE id=$1`, [][]string{{"100", "45"}}, settled.ID)

	beforeChecks, beforeSources := walletSourceSnapshot(t, f), walletSourcesReadSnapshot(t, f)
	walletApplicationChecks(t, f)
	if walletSourceSnapshot(t, f) != beforeChecks || walletSourcesReadSnapshot(t, f) != beforeSources {
		t.Fatal("actual APP read-only checks mutated financial facts")
	}
}

func TestPostgresWalletSourcesApplicationSelfServiceCompletion(t *testing.T) {
	f := walletSourcesFixture(t)
	walletSourcesApplication(t, f)
	request, err := f.store.CreateWalletRequest(f.ctx, WalletRequestInput{TenantID: f.tenant, ActorID: f.user, Kind: "topup", Note: "self-service bank reference", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()})
	if err != nil || request.State != "open" {
		t.Fatalf("actual APP self-service request: %+v %v", request, err)
	}
	f.wallet(t, 0, 0)
	input := CreditInput{TenantID: f.tenant, ActorID: f.operator, BusinessRef: "self-service-app", ProofRef: "proof:self-service", Amount: 25, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 25, IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString(), RequestID: request.ID}
	pending, err := f.store.CreateCredit(f.ctx, input)
	if err != nil || pending.State != "pending" || pending.Requester != f.operator || pending.RequestedBy != f.user || pending.RequestID != request.ID {
		t.Fatalf("actual APP maker link: %+v %v", pending, err)
	}
	f.wallet(t, 0, 0)
	approved, err := f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: pending.ID, ActorID: f.approver})
	if err != nil || approved.State != "approved" {
		t.Fatalf("actual APP self-service approval: %+v %v", approved, err)
	}
	var state string
	if err = f.pool.QueryRow(f.ctx, `SELECT state FROM wallet_requests WHERE id=$1 AND linked_order_id=$2`, request.ID, pending.ID).Scan(&state); err != nil || state != "closed" {
		t.Fatalf("actual APP request close: state=%s err=%v", state, err)
	}
	f.wallet(t, 25, 0)
	walletApplicationChecks(t, f)
}
