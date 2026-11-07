package billing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestWalletSourceCheckRejectsWithoutDatabase(t *testing.T) {
	var store *Store
	check, err := store.CheckWalletSources(context.Background(), "not-a-tenant")
	assertCode(t, err, "invalid_tenant")
	if check != (WalletSourceCheck{}) {
		t.Fatalf("invalid scope returned a partial check: %+v", check)
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		check, err = store.CheckWalletSources(ctx, "")
		if err == nil || err.Error() != "wallet_sources_unavailable" || check != (WalletSourceCheck{}) {
			t.Fatalf("unavailable store: check=%+v error=%v", check, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	check, err = store.CheckWalletSources(ctx, uuid.NewString())
	if !errors.Is(err, context.Canceled) || check != (WalletSourceCheck{}) {
		t.Fatalf("canceled check lost its cause or returned data: %+v %v", check, err)
	}
}

func TestWalletSourceCheckSafeDTOAndError(t *testing.T) {
	cause := errors.New("postgres://user:secret@private.invalid/db: SELECT sensitive")
	err := walletSourcesUnavailable(cause)
	if err.Error() != "wallet_sources_unavailable" || !errors.Is(err, cause) {
		t.Fatalf("read failure leaks diagnostic text or loses its cause: %v", err)
	}
	raw, err := json.Marshal(WalletSourceCheck{TenantID: uuid.NewString(), AsOf: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"allocation_mismatches", "as_of", "balanced", "event_mismatches", "source_mismatches", "tenant_id", "wallet_mismatches"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected public integrity fields: %v", names)
	}
}

func walletSourcesAssertBalanced(t *testing.T, f *fixture, tenant string) WalletSourceCheck {
	t.Helper()
	got, err := f.store.CheckWalletSources(f.ctx, tenant)
	if err != nil || !got.Balanced || got.TenantID != tenant || got.AsOf.IsZero() ||
		got.SourceMismatches != 0 || got.EventMismatches != 0 || got.AllocationMismatches != 0 || got.WalletMismatches != 0 {
		t.Fatalf("current wallet source check: %+v %v", got, err)
	}
	return got
}

func walletSourcesReadSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'lots',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lots x),
 'balances',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.lot_id) FROM funding_lot_balances x),
 'events',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lot_events x),
 'allocations',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.reservation_id,x.allocation_index) FROM funding_reservation_lots x),
 'finishes',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.settlement_id,x.allocation_index) FROM funding_settlement_lots x),
 'refunds',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.refund_order_id,x.refund_index) FROM funding_consumption_refund_lots x),
 'receipts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_receipts x),
 'accounts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),
 'journals',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x),
 'lines',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journal_lines x))::text`).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func walletSourcesReadFinishAndRefund(t *testing.T, f *fixture) {
	t.Helper()
	r := f.reserve(t)
	f.final(t, r, confirmed(), "completed", "done", "none", false)
	s, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil {
		t.Fatal(err)
	}
	op := uuid.NewString()
	refund, err := f.store.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: s.ID,
		BusinessRef: "source-check:" + op, Reason: "approved support refund", Amount: 10, IdempotencyKey: op, RequestHMAC: op})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, ActorID: f.approver, OrderID: refund.ID}); err != nil {
		t.Fatal(err)
	}
}

// These tests require the dedicated PostgreSQL fixture. They are deliberately
// not replaced by mocked monetary results or counted as executed when skipped.
func TestPostgresWalletSourcesReadLifecycle(t *testing.T) {
	f := fundingFixture(t)
	walletSourcesAssertBalanced(t, f, f.tenant)
	walletSourcesAssertBalanced(t, f, "")
	empty := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'empty source-check tenant')`, empty); err != nil {
		t.Fatal(err)
	}
	walletSourcesAssertBalanced(t, f, empty)
	r := f.reserve(t)
	walletSourcesAssertBalanced(t, f, f.tenant)
	if _, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "no_delivery"}); err != nil {
		t.Fatal(err)
	}
	walletSourcesReadFinishAndRefund(t, f)
	before := walletSourcesReadSnapshot(t, f)
	walletSourcesAssertBalanced(t, f, f.tenant)
	walletSourcesAssertBalanced(t, f, "")
	if after := walletSourcesReadSnapshot(t, f); before != after {
		t.Fatal("read-only check changed current wallet or ledger facts")
	}
	publishBillingDefault(t, f, f.model, 0, 0, time.Now().UTC().Add(-time.Hour))
	zero := f.reserve(t)
	if zero.Amount != 0 {
		t.Fatalf("zero-price fixture reserved %v", zero.Amount)
	}
	if _, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: zero.RequestID, Reason: "no_delivery"}); err != nil {
		t.Fatal(err)
	}
	walletSourcesAssertBalanced(t, f, f.tenant)
	var edges int64
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_reservation_lots WHERE request_id=$1`, zero.RequestID).Scan(&edges); err != nil || edges != 0 {
		t.Fatalf("zero-price reservation fabricated allocation edges: %d %v", edges, err)
	}
	got, err := f.store.CheckWalletSources(f.ctx, uuid.NewString())
	if got != (WalletSourceCheck{}) {
		t.Fatalf("nonexistent tenant returned data: %+v", got)
	}
	assertCode(t, err, "invalid_tenant")
}

// Only an owner fixture removes constraints to create committed corrupt
// history. The application has no ability to disable guards or repair it.
func walletSourcesReadDropFK(t *testing.T, f *fixture, table, referenced string) {
	t.Helper()
	rows, err := f.pool.Query(f.ctx, `SELECT n.nspname,c.relname,k.conname
 FROM pg_catalog.pg_constraint k JOIN pg_catalog.pg_class c ON c.oid=k.conrelid
 JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
 WHERE k.contype='f' AND (($1<>'' AND k.conrelid=to_regclass(NULLIF($1,''))) OR ($2<>'' AND k.confrelid=to_regclass(NULLIF($2,''))))`, table, referenced)
	if err != nil {
		t.Fatal(err)
	}
	var keys [][3]string
	for rows.Next() {
		var key [3]string
		if err = rows.Scan(&key[0], &key[1], &key[2]); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err = f.pool.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{key[0], key[1]}.Sanitize()+" DROP CONSTRAINT "+pgx.Identifier{key[2]}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
}

func walletSourcesReadDisable(t *testing.T, f *fixture, table string) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" DISABLE TRIGGER USER"); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresWalletSourcesReadFindsBothDirections(t *testing.T) {
	tests := []struct {
		name, sql, category string
		prepare             func(*testing.T, *fixture)
	}{
		{name: "receipt_without_lot", sql: `TRUNCATE funding_lots CASCADE`, category: "source"},
		{name: "source_without_receipt", sql: `TRUNCATE funding_receipts CASCADE`, category: "source"},
		{name: "bad_balance_principal", sql: `UPDATE funding_lot_balances SET consumed_amount=consumed_amount+1`, category: "source",
			prepare: func(t *testing.T, f *fixture) { walletSourcesReadDisable(t, f, "funding_lot_balances") }},
		{name: "balance_without_lot", sql: `INSERT INTO funding_lot_balances SELECT gen_random_uuid()::text,tenant_id,available_amount,held_amount,consumed_amount,revision FROM funding_lot_balances LIMIT 1`, category: "source",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "funding_lot_balances", "")
				walletSourcesReadDisable(t, f, "funding_lot_balances")
			}},
		{name: "event_without_lot_or_journal", sql: `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 SELECT gen_random_uuid()::text,tenant_id,gen_random_uuid()::text,gen_random_uuid()::text,gen_random_uuid()::text,'funding',1,1,0,0 FROM funding_lots LIMIT 1`, category: "event",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "funding_lot_events", "")
				walletSourcesReadDisable(t, f, "funding_lot_events")
			}},
		{name: "missing_reservation_allocations", sql: `TRUNCATE funding_reservation_lots CASCADE`, category: "allocation"},
		{name: "reservation_edge_without_root", sql: `INSERT INTO funding_reservation_lots SELECT tenant_id,gen_random_uuid()::text,request_id,lot_id,1,amount FROM funding_reservation_lots LIMIT 1`, category: "allocation",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "funding_reservation_lots", "")
				walletSourcesReadDisable(t, f, "funding_reservation_lots")
			}},
		{name: "settlement_edge_without_root", sql: `INSERT INTO funding_settlement_lots SELECT tenant_id,gen_random_uuid()::text,reservation_id,allocation_index,lot_id,charged_amount,released_amount FROM funding_settlement_lots LIMIT 1`, category: "allocation",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "funding_settlement_lots", "")
				walletSourcesReadDisable(t, f, "funding_settlement_lots")
			}},
		{name: "refund_edge_without_root", sql: `INSERT INTO funding_consumption_refund_lots SELECT tenant_id,gen_random_uuid()::text,settlement_id,allocation_index,lot_id,1,amount FROM funding_consumption_refund_lots LIMIT 1`, category: "allocation",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "funding_consumption_refund_lots", "")
				walletSourcesReadDisable(t, f, "funding_consumption_refund_lots")
			}},
		{name: "missing_funding_journal", sql: `DELETE FROM journals WHERE kind='credit'`, category: "event",
			prepare: func(t *testing.T, f *fixture) {
				walletSourcesReadDropFK(t, f, "", "journals")
				walletSourcesReadDisable(t, f, "journals")
			}},
		{name: "wallet_balance_drift", sql: `UPDATE ledger_accounts SET balance=balance+1 WHERE kind='AVAILABLE'`, category: "wallet",
			prepare: func(t *testing.T, f *fixture) { walletSourcesReadDisable(t, f, "ledger_accounts") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := fundingFixture(t)
			walletSourcesReadFinishAndRefund(t, f)
			walletSourcesAssertBalanced(t, f, f.tenant)
			empty := uuid.NewString()
			if _, err := f.pool.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'unaffected source-check tenant')`, empty); err != nil {
				t.Fatal(err)
			}
			if tc.prepare != nil {
				tc.prepare(t, f)
			}
			if _, err := f.pool.Exec(f.ctx, tc.sql); err != nil {
				t.Fatal(err)
			}
			before := walletSourcesReadSnapshot(t, f)
			got, err := f.store.CheckWalletSources(f.ctx, f.tenant)
			if err != nil || got.Balanced {
				t.Fatalf("corrupt history accepted or query failed: %+v %v", got, err)
			}
			count := map[string]int64{"source": got.SourceMismatches, "event": got.EventMismatches, "allocation": got.AllocationMismatches, "wallet": got.WalletMismatches}[tc.category]
			if count == 0 {
				t.Fatalf("expected %s mismatch, got %+v", tc.category, got)
			}
			all, err := f.store.CheckWalletSources(f.ctx, "")
			if err != nil || all.Balanced {
				t.Fatalf("platform scope missed corrupt tenant: %+v %v", all, err)
			}
			walletSourcesAssertBalanced(t, f, empty)
			if after := walletSourcesReadSnapshot(t, f); before != after {
				t.Fatal("integrity reader repaired or changed corrupt history")
			}
		})
	}
}

func TestPostgresWalletSourcesReadCancellationReturnsNoPartialResult(t *testing.T) {
	f := fundingFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	if _, err = tx.Exec(f.ctx, `LOCK TABLE funding_lot_balances IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer cancel()
	got, err := f.store.CheckWalletSources(ctx, f.tenant)
	if !errors.Is(err, context.DeadlineExceeded) || got != (WalletSourceCheck{}) || err.Error() != "wallet_sources_unavailable" {
		t.Fatalf("canceled read published partial counts or leaked a cause: %+v %v", got, err)
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	walletSourcesAssertBalanced(t, f, f.tenant)
}
