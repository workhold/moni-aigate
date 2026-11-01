package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type accountBatchResult struct {
	accounts []lockedAccount
	err      error
}

func accountBatchTenant(t *testing.T, f *fixture) string {
	t.Helper()
	tenant := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, "INSERT INTO tenants(id,name) VALUES($1,'account batch')", tenant); err != nil {
		t.Fatal(err)
	}
	return tenant
}

func receiveAccountBatch(t *testing.T, result <-chan accountBatchResult) accountBatchResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("account batch did not return")
		return accountBatchResult{}
	}
}

func accountBatchAssertPoolUsable(t *testing.T, f *fixture) {
	t.Helper()
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	connection, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	var one int
	if err := connection.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("connection remained busy or aborted: value=%d error=%v", one, err)
	}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	accounts, err := lockAccounts(ctx, tx, f.tenant, "AVAILABLE", "HELD")
	if err != nil || len(accounts) != 2 {
		t.Fatalf("subsequent account batch failed: accounts=%+v error=%v", accounts, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresAccountBatchInitializationConflictReadsFreshCommittedBalance(t *testing.T) {
	f := integration(t)
	tenant := accountBatchTenant(t, f)
	credit := fundingCredit(t, f, tenant, 777)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	blocker, err := f.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	// Retain the production source/authorization-before-accounts order even in
	// this account-initialization fixture; a credit journal cannot be orphaned.
	fundingManualLocks(t, f, blocker, credit)
	var pid int
	if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, "INSERT INTO ledger_accounts(tenant_id,kind) VALUES($1,'AVAILABLE')", tenant); err != nil {
		t.Fatal(err)
	}
	result := make(chan accountBatchResult, 1)
	go func() {
		var accounts []lockedAccount
		err := f.store.transaction(ctx, func(tx pgx.Tx) error {
			var err error
			accounts, err = lockAccounts(ctx, tx, tenant, "HELD", "AVAILABLE", "AVAILABLE")
			return err
		})
		result <- accountBatchResult{accounts: accounts, err: err}
	}()
	// The initialization INSERT has already taken its statement snapshot and
	// is waiting on the uncommitted unique account, rather than on a guessed
	// goroutine schedule. Commit a balanced credit only after that barrier.
	waitForBatchBlocker(t, f, pid)
	if _, err := f.store.ApplyFundingTx(ctx, blocker, FundingSourceRef{Kind: FundingManualCredit, ID: credit.ID}, FundingScope{TenantID: tenant, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	if err := audit(ctx, blocker, tenant, f.approver, "credit.approved", credit.ID, "", map[string]any{"amount": credit.Amount}); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := receiveAccountBatch(t, result)
	if got.err != nil || len(got.accounts) != 2 {
		t.Fatalf("initialization conflict did not expose complete accounts: %+v %v", got.accounts, got.err)
	}
	balances := map[string]int64{}
	for i, account := range got.accounts {
		if i > 0 && got.accounts[i-1].id >= account.id {
			t.Fatal("returned accounts did not retain the global ID order")
		}
		balances[account.kind] = account.balance
	}
	if balances["AVAILABLE"] != 777 || balances["HELD"] != 0 {
		t.Fatalf("lock query reused the initialization snapshot: %+v", got.accounts)
	}
	accountBatchAssertPoolUsable(t, f)
	f.check(t)
}

func TestPostgresAccountBatchEarlyErrorsDrainAndRollback(t *testing.T) {
	for _, boundary := range []string{"invalid_kind", "initialization_foreign_key"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
			defer cancel()
			connection, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			if boundary == "invalid_kind" {
				_, err = lockAccounts(ctx, tx, f.tenant, "UNKNOWN")
				assertCode(t, err, "invalid_account")
			} else {
				_, err = lockAccounts(ctx, tx, uuid.NewString(), "AVAILABLE", "HELD")
				var databaseError *pgconn.PgError
				if !errors.As(err, &databaseError) || databaseError.Code != "23503" {
					t.Fatalf("lost initialization database error: %v", err)
				}
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal("failed batch did not drain for rollback", err)
			}
			// Reuse this exact physical connection, rather than allowing a
			// different idle pool connection to hide undrained results.
			var one int
			if err := connection.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
				t.Fatalf("same connection was not reusable: value=%d error=%v", one, err)
			}
			connection.Release()
			accountBatchAssertPoolUsable(t, f)
			f.wallet(t, 1000, 0)
			f.check(t)
		})
	}
}

func TestPostgresAccountBatchScanErrorIsPreservedAndDrained(t *testing.T) {
	f := integration(t)
	ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancel()
	connection, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	// A connection-local temporary table injects a real pgx scan failure
	// without changing production tables, schema contracts, or code hooks.
	if _, err := connection.Exec(ctx, `CREATE TEMP TABLE ledger_accounts (
 id text NOT NULL DEFAULT gen_random_uuid()::text,
 tenant_id text,kind text NOT NULL,balance text NOT NULL DEFAULT 'invalid-balance',
 UNIQUE(tenant_id,kind))`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = connection.Exec(cleanup, "DROP TABLE IF EXISTS pg_temp.ledger_accounts")
		_ = connection.Conn().DeallocateAll(cleanup)
	}()
	// A reused pool connection may have already prepared these SQL strings
	// against the real table. Resolve them anew against this local fixture.
	if err := connection.Conn().DeallocateAll(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	accounts, err := lockAccounts(ctx, tx, f.tenant, "AVAILABLE", "HELD")
	var scanError pgx.ScanArgError
	if !errors.As(err, &scanError) || scanError.ColumnIndex != 2 || accounts != nil {
		t.Fatalf("scan error was replaced or partial accounts escaped: accounts=%+v error=%v", accounts, err)
	}
	// A decoding error does not abort the database transaction. This query
	// can run only after both rows and batch results have been closed.
	var one int
	if err := tx.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("scan failure left the batch busy: value=%d error=%v", one, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, "DROP TABLE pg_temp.ledger_accounts"); err != nil {
		t.Fatal(err)
	}
	if err := connection.Conn().DeallocateAll(ctx); err != nil {
		t.Fatal(err)
	}
	accountBatchAssertPoolUsable(t, f)
	f.check(t)
}

func TestPostgresAccountBatchCancellationDrainsAndReturnsConnection(t *testing.T) {
	for _, boundary := range []string{"initialization", "lock_query"} {
		t.Run(boundary, func(t *testing.T) {
			f := integration(t)
			tenant := accountBatchTenant(t, f)
			ctx, timeout := context.WithTimeout(f.ctx, 8*time.Second)
			defer timeout()
			if boundary == "lock_query" {
				if _, err := f.pool.Exec(ctx, "INSERT INTO ledger_accounts(tenant_id,kind) VALUES($1,'AVAILABLE'),($1,'HELD')", tenant); err != nil {
					t.Fatal(err)
				}
			}
			blocker, err := f.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(context.Background())
			var pid int
			if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if boundary == "initialization" {
				_, err = blocker.Exec(ctx, "INSERT INTO ledger_accounts(tenant_id,kind) VALUES($1,'AVAILABLE')", tenant)
			} else {
				_, err = blocker.Exec(ctx, "SELECT id FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE' FOR UPDATE", tenant)
			}
			if err != nil {
				t.Fatal(err)
			}
			connection, err := f.pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Release()
			tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background())
			waitCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan accountBatchResult, 1)
			go func() {
				accounts, err := lockAccounts(waitCtx, tx, tenant, "HELD", "AVAILABLE")
				result <- accountBatchResult{accounts: accounts, err: err}
			}()
			waitForBatchBlocker(t, f, pid)
			cancel()
			got := receiveAccountBatch(t, result)
			if !errors.Is(got.err, context.Canceled) || got.accounts != nil {
				t.Fatalf("cancelled account batch: accounts=%+v error=%v", got.accounts, got.err)
			}
			// pgx may discard a socket cancelled during I/O; either a clean
			// rollback or discarding that socket must free the pool lease.
			cleanup, cancelCleanup := context.WithTimeout(context.Background(), 3*time.Second)
			_ = tx.Rollback(cleanup)
			cancelCleanup()
			connection.Release()
			if err := blocker.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var accounts int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM ledger_accounts WHERE tenant_id=$1", tenant).Scan(&accounts); err != nil {
				t.Fatal(err)
			}
			want := 0
			if boundary == "lock_query" {
				want = 2
			}
			if accounts != want {
				t.Fatalf("cancelled initialization partially persisted: accounts=%d want=%d", accounts, want)
			}
			accountBatchAssertPoolUsable(t, f)
			f.wallet(t, 1000, 0)
			f.check(t)
		})
	}
}

func TestPostgresAccountBatchLocksAccountsInGlobalIDOrder(t *testing.T) {
	f := integration(t)
	tenant := accountBatchTenant(t, f)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	low := "00000000-0000-0000-0000-000000000001"
	high := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if _, err := f.pool.Exec(ctx, "INSERT INTO ledger_accounts(id,tenant_id,kind) VALUES($1,$3,'AVAILABLE'),($2,$3,'HELD')", low, high, tenant); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	var pid int
	if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM ledger_accounts WHERE id=$1 FOR UPDATE", high).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	connection, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	result := make(chan accountBatchResult, 1)
	go func() {
		// Reverse kind order must not determine row-lock order.
		accounts, err := lockAccounts(ctx, tx, tenant, "HELD", "AVAILABLE")
		result <- accountBatchResult{accounts: accounts, err: err}
	}()
	waitForBatchBlocker(t, f, pid)
	probe, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback(context.Background())
	var account string
	err = probe.QueryRow(ctx, "SELECT id FROM ledger_accounts WHERE id=$1 FOR UPDATE NOWAIT", low).Scan(&account)
	var lockError *pgconn.PgError
	if !errors.As(err, &lockError) || lockError.Code != "55P03" {
		t.Fatalf("batch waited on the higher ID before locking the lower ID: id=%s error=%v", account, err)
	}
	if err := probe.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := receiveAccountBatch(t, result)
	if got.err != nil || len(got.accounts) != 2 || got.accounts[0].id != low || got.accounts[1].id != high {
		t.Fatalf("account locks did not complete in global ID order: %+v %v", got.accounts, got.err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	connection.Release()
	accountBatchAssertPoolUsable(t, f)
	f.check(t)
}
