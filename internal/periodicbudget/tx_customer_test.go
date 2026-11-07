package periodicbudget_test

import (
	"errors"
	. "github.com/moni-ai/moni-aigate/internal/periodicbudget"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func pbTxCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || ErrorCode(err) != want {
		t.Fatalf("Tx helper code=%q, want %q", ErrorCode(err), want)
	}
}

func pbTxCustomerLocks(t *testing.T, f *pbTxFixture, tx pgx.Tx, request string) CustomerLocks {
	t.Helper()
	f.customerRoots(t, tx, request)
	locks, err := LockCustomerTx(f.ctx, tx, f.tenant, f.key)
	pbTxMust(t, "lock exact customer scopes", err)
	return locks
}

func TestTxCustomerDoubleScopeSingleWallet(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	tenantControl := f.configure(t, "tenant", true, 120)
	keyControl := f.configure(t, "key", true, 120)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 123456000, time.UTC))
	tx := f.begin(t)
	locks := pbTxCustomerLocks(t, f, tx, "")
	request, admission := f.newCustomer(t, tx, locks)
	if len(admission.WindowIDs) != 2 || admission.At.Format(utc6Layout) != "2026-12-10T12:00:00.123456Z" {
		t.Fatal("admission did not retain the single DB time and both original windows")
	}
	// Repeating a reserve mirror in the same transaction is an exact read, not
	// another reserve, event, wallet debit or period observation.
	pbTxMust(t, "repeat exact reserve effect", RecordCustomerReserveTx(f.ctx, tx, locks, admission, request))
	pbTxMust(t, "commit single wallet and dual budget reserve", tx.Commit(f.ctx))
	var holds, allocations, events, reserves int
	pbTxMust(t, "read complete double-scope reserve", f.app.QueryRow(f.ctx, `SELECT
 (SELECT held FROM api_keys WHERE id=$1),
 (SELECT count(*) FROM periodic_budget_allocations WHERE subject_id=$2 AND held_amount=120 AND spent_amount=0 AND origin='new'),
 (SELECT count(*) FROM periodic_budget_events WHERE source_kind='reservation'),
 (SELECT count(*) FROM journals WHERE reference=$2 AND kind='reserve')`, f.key, request).Scan(&holds, &allocations, &events, &reserves))
	if holds != 120 || allocations != 2 || events != 2 || reserves != 1 {
		t.Fatal("dual scopes duplicated the customer financial reserve or lost an allocation")
	}
	f.verifyFinance(t)
	// Tightening both caps and moving into a new month must not block or move
	// the original favorable settlement into that new period.
	f.configure(t, "tenant", true, 0)
	f.configure(t, "key", true, 0)
	f.setClock(t, time.Date(2027, 1, 10, 12, 0, 0, 123456000, time.UTC))
	finish := f.begin(t)
	finishLocks := pbTxCustomerLocks(t, f, finish, request)
	f.walletLocks(t, finish)
	settlement := f.finishCustomerSource(t, finish, request, 70)
	pbTxMust(t, "settle original dual-scope allocations", FinishCustomerTx(f.ctx, finish, finishLocks, request, settlement))
	pbTxMust(t, "repeat same settlement effect", FinishCustomerTx(f.ctx, finish, finishLocks, request, settlement))
	pbTxMust(t, "commit unique customer settlement", finish.Commit(f.ctx))
	var balanced bool
	pbTxMust(t, "verify original period and single-wallet settlement", f.app.QueryRow(f.ctx, `SELECT
 (SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='AVAILABLE')=9930
 AND (SELECT balance FROM ledger_accounts WHERE tenant_id=$1 AND kind='HELD')=0
 AND (SELECT balance FROM ledger_accounts WHERE tenant_id IS NULL AND kind='REVENUE')=70
 AND (SELECT held=0 AND spent=70 FROM api_keys WHERE id=$2)
 AND (SELECT count(*)=1 FROM settlements WHERE request_id=$3 AND charged_amount=70)
 AND (SELECT count(*)=2 AND bool_and(held_amount=0 AND spent_amount=70) FROM periodic_budget_controls WHERE id=ANY($4::text[]))
 AND (SELECT count(*)=2 AND bool_and(start_at='2026-12-01T00:00:00Z'::timestamptz AND held_amount=0 AND spent_amount=70) FROM periodic_budget_windows)
 AND (SELECT count(*)=2 AND bool_and(state='settled' AND held_amount=0 AND spent_amount=70 AND revision=2) FROM periodic_budget_allocations WHERE subject_id=$3)
 AND (SELECT count(*)=4 FROM periodic_budget_events)`, f.tenant, f.key, request, []string{tenantControl, keyControl}).Scan(&balanced))
	if !balanced {
		t.Fatal("settlement changed period, duplicated money/effects or lost conservation")
	}
	f.verifyFinance(t)
	before := f.snapshot(t)
	replay := f.begin(t)
	replayLocks := pbTxCustomerLocks(t, f, replay, request)
	pbTxMust(t, "re-read committed settlement effect", FinishCustomerTx(f.ctx, replay, replayLocks, request, settlement))
	pbTxMust(t, "commit original-effect replay", replay.Commit(f.ctx))
	if before != f.snapshot(t) {
		t.Fatal("committed-effect replay changed financial or budget facts")
	}
}

func TestTxCustomerAdmissionTamperingRollsBackWholeSource(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	f.configure(t, "tenant", false, 10000)
	f.configure(t, "key", false, 10000)
	before := f.snapshot(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Admission)
	}{
		{"zero", func(a *Admission) { *a = Admission{} }},
		{"clock", func(a *Admission) { a.At = a.At.Add(time.Microsecond) }},
		{"window_missing", func(a *Admission) { a.WindowIDs = a.WindowIDs[:1] }},
		{"window_extra", func(a *Admission) { a.WindowIDs = append(a.WindowIDs, uuid.NewString()) }},
		{"window_swapped", func(a *Admission) { a.WindowIDs[0], a.WindowIDs[1] = a.WindowIDs[1], a.WindowIDs[0] }},
		{"window_wrong", func(a *Admission) { a.WindowIDs[0] = uuid.NewString() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := f.begin(t)
			locks := pbTxCustomerLocks(t, f, tx, "")
			f.walletLocks(t, tx)
			request := f.createCustomerSource(t, tx)
			admission, err := AdmitCustomerTx(f.ctx, tx, locks, request, Amount{Decimal: "120"})
			pbTxMust(t, "admit source before tampering", err)
			// Mutate the actual public slice to catch an internal alias as well.
			tc.mutate(&admission)
			pbTxCode(t, RecordCustomerReserveTx(f.ctx, tx, locks, admission, request), "periodic_budget_invariant")
			pbTxMust(t, "rollback tampered admission and whole wallet source", tx.Rollback(f.ctx))
			if before != f.snapshot(t) {
				t.Fatal("tampered admission left a wallet, request or budget fact")
			}
		})
	}
	t.Run("different_subject", func(t *testing.T) {
		tx := f.begin(t)
		locks := pbTxCustomerLocks(t, f, tx, "")
		f.walletLocks(t, tx)
		original := f.createCustomerSource(t, tx)
		admission, err := AdmitCustomerTx(f.ctx, tx, locks, original, Amount{Decimal: "120"})
		pbTxMust(t, "admit original subject", err)
		other := f.createCustomerSource(t, tx)
		pbTxCode(t, RecordCustomerReserveTx(f.ctx, tx, locks, admission, other), "periodic_budget_invariant")
		pbTxMust(t, "rollback cross-subject use", tx.Rollback(f.ctx))
		if before != f.snapshot(t) {
			t.Fatal("cross-subject admission changed facts")
		}
	})
	f.verifyFinance(t)
}

func TestTxCustomerAmountZeroTokenAndCapFailure(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	f.configure(t, "tenant", true, 119)
	f.configure(t, "key", true, 120)
	f.setClock(t, time.Date(2026, 12, 10, 12, 0, 0, 0, time.UTC))
	before := f.snapshot(t)
	for _, tc := range []struct {
		amount, code string
		zero         bool
	}{
		{"120", "periodic_budget_invariant", true},
		{"119", "periodic_budget_invariant", false},
		{"0", "periodic_budget_invariant", false},
		{"01", "invalid_request", false},
		{"-1", "invalid_request", false},
		{"9223372036854775808", "invalid_request", false},
		{"120", "periodic_budget_exceeded", false},
	} {
		t.Run(tc.amount+"/"+tc.code, func(t *testing.T) {
			tx := f.begin(t)
			locks := pbTxCustomerLocks(t, f, tx, "")
			f.walletLocks(t, tx)
			request := f.createCustomerSource(t, tx)
			if tc.zero {
				locks = CustomerLocks{}
			}
			admission, err := AdmitCustomerTx(f.ctx, tx, locks, request, Amount{Decimal: tc.amount})
			pbTxCode(t, err, tc.code)
			if !admission.At.IsZero() || len(admission.WindowIDs) != 0 {
				t.Fatal("failed admission returned an apparent permit")
			}
			pbTxMust(t, "rollback rejected source and wallet", tx.Rollback(f.ctx))
			if before != f.snapshot(t) {
				t.Fatal("rejected admission committed a partial source or money effect")
			}
		})
	}
	// Independently reject a Key cap while the tenant cap would permit the R.
	f.configure(t, "tenant", true, 120)
	f.configure(t, "key", true, 119)
	tx := f.begin(t)
	locks := pbTxCustomerLocks(t, f, tx, "")
	f.walletLocks(t, tx)
	request := f.createCustomerSource(t, tx)
	_, err := AdmitCustomerTx(f.ctx, tx, locks, request, Amount{Decimal: "120"})
	pbTxCode(t, err, "periodic_budget_exceeded")
	pbTxMust(t, "rollback Key-cap rejection", tx.Rollback(f.ctx))
	f.verifyFinance(t)
}

// A pgx.Tx interface can contain a noncomparable dynamic value. The helper
// must reject unsupported identity rather than panic when comparing it.
type pbTxNoncomparable struct {
	pgx.Tx
	marker []byte
}

func TestTxCustomerTransactionIdentityAndClosedSource(t *testing.T) {
	pbHandwrittenSource(t)
	f := newPBTxFixture(t)
	f.configure(t, "tenant", false, 10000)
	f.configure(t, "key", false, 10000)
	conn, err := f.app.Acquire(f.ctx)
	pbTxMust(t, "acquire same physical session", err)
	defer conn.Release()
	first, err := conn.Begin(f.ctx)
	pbTxMust(t, "begin first transaction", err)
	firstLocks := pbTxCustomerLocks(t, f, first, "")
	pbTxMust(t, "end first transaction", first.Rollback(f.ctx))
	_, err = AdmitCustomerTx(f.ctx, first, firstLocks, uuid.NewString(), Amount{Decimal: "120"})
	pbTxCode(t, err, "periodic_budget_invariant")
	second, err := conn.Begin(f.ctx)
	pbTxMust(t, "begin second transaction on same session", err)
	defer second.Rollback(f.ctx)
	secondLocks := pbTxCustomerLocks(t, f, second, "")
	f.walletLocks(t, second)
	request := f.createCustomerSource(t, second)
	_, err = AdmitCustomerTx(f.ctx, second, firstLocks, request, Amount{Decimal: "120"})
	pbTxCode(t, err, "periodic_budget_invariant")
	_, err = LockCustomerTx(f.ctx, pbTxNoncomparable{Tx: second, marker: []byte{1}}, f.tenant, f.key)
	pbTxCode(t, err, "periodic_budget_invariant")
	// A valid token remains usable after a rejected alien token; the helper
	// itself must not silently rollback or replace the caller's transaction.
	admission, err := AdmitCustomerTx(f.ctx, second, secondLocks, request, Amount{Decimal: "120"})
	pbTxMust(t, "admit with real second transaction", err)
	pbTxMust(t, "record with exact second transaction", RecordCustomerReserveTx(f.ctx, second, secondLocks, admission, request))
	pbTxMust(t, "commit second transaction", second.Commit(f.ctx))
	conn.Release()
	// Avoid keeping the acquired pool slot through the next fresh transaction.
	committed := f.begin(t)
	committedLocks := pbTxCustomerLocks(t, f, committed, request)
	_, err = AdmitCustomerTx(f.ctx, committed, committedLocks, request, Amount{Decimal: "120"})
	pbTxCode(t, err, "periodic_budget_invariant")
	pbTxMust(t, "rollback committed-source re-admission", committed.Rollback(f.ctx))
	f.verifyFinance(t)
}

func TestTxCustomerNoControlAndLegacyFinish(t *testing.T) {
	pbHandwrittenSource(t)
	t.Run("closed_before_configuration_has_no_catchup", func(t *testing.T) {
		f := newPBTxFixture(t)
		tx := f.begin(t)
		locks := pbTxCustomerLocks(t, f, tx, "")
		request, admission := f.newCustomer(t, tx, locks)
		if len(admission.WindowIDs) != 0 {
			t.Fatal("no-control source unexpectedly created a window")
		}
		pbTxMust(t, "commit source with no configured budget", tx.Commit(f.ctx))
		finish := f.begin(t)
		finishLocks := pbTxCustomerLocks(t, f, finish, request)
		f.walletLocks(t, finish)
		settlement := f.finishCustomerSource(t, finish, request, 0)
		pbTxMust(t, "finish source with no control", FinishCustomerTx(f.ctx, finish, finishLocks, request, settlement))
		pbTxMust(t, "commit no-control finish", finish.Commit(f.ctx))
		f.configure(t, "tenant", false, 10000)
		f.configure(t, "key", false, 10000)
		late := f.begin(t)
		lateLocks := pbTxCustomerLocks(t, f, late, request)
		pbTxMust(t, "old closed source has no customer catchup", FinishCustomerTx(f.ctx, late, lateLocks, request, settlement))
		pbTxMust(t, "commit no-catchup read", late.Commit(f.ctx))
		var count int
		pbTxMust(t, "read no-catchup allocation count", f.app.QueryRow(f.ctx, `SELECT count(*) FROM periodic_budget_allocations WHERE subject_id=$1`, request).Scan(&count))
		if count != 0 {
			t.Fatal("closed customer source was imported after its completion")
		}
		f.verifyFinance(t)
	})
	t.Run("existing_unresolved_import_keeps_null_window", func(t *testing.T) {
		f := newPBTxFixture(t)
		tx := f.begin(t)
		locks := pbTxCustomerLocks(t, f, tx, "")
		request, _ := f.newCustomer(t, tx, locks)
		pbTxMust(t, "commit pre-control source", tx.Commit(f.ctx))
		f.configure(t, "tenant", false, 10000)
		f.configure(t, "key", false, 10000)
		finish := f.begin(t)
		finishLocks := pbTxCustomerLocks(t, f, finish, request)
		f.walletLocks(t, finish)
		settlement := f.finishCustomerSource(t, finish, request, 0)
		pbTxMust(t, "finish exact imported legacy allocations", FinishCustomerTx(f.ctx, finish, finishLocks, request, settlement))
		pbTxMust(t, "commit legacy finish", finish.Commit(f.ctx))
		var exact bool
		pbTxMust(t, "read legacy source result", f.app.QueryRow(f.ctx, `SELECT count(*)=2 AND bool_and(origin='legacy_import' AND window_id IS NULL AND held_amount=0 AND spent_amount=0 AND state='released') FROM periodic_budget_allocations WHERE subject_id=$1`, request).Scan(&exact))
		if !exact {
			t.Fatal("legacy customer finish invented a window or lost its origin")
		}
		f.verifyFinance(t)
	})
}

func TestTxFixtureActualRuntimeRoleBoundaries(t *testing.T) {
	f := newPBTxFixture(t)
	control := f.configure(t, "tenant", false, 10000)
	for _, table := range []string{"periodic_budget_controls", "periodic_budget_windows", "periodic_budget_allocations", "periodic_budget_events", "periodic_budget_operations", "periodic_budget_configuration_versions"} {
		var selectOK, insertOK, deleteOK, truncateOK bool
		pbTxMust(t, "read APP exact table grants", f.app.QueryRow(f.ctx, `SELECT has_table_privilege(current_user,$1,'SELECT'),has_table_privilege(current_user,$1,'INSERT'),has_table_privilege(current_user,$1,'DELETE'),has_table_privilege(current_user,$1,'TRUNCATE')`, f.schema+"."+table).Scan(&selectOK, &insertOK, &deleteOK, &truncateOK))
		if !selectOK || !insertOK || deleteOK || truncateOK {
			t.Fatal("APP B table grants are broader or narrower than the tracked contract")
		}
		_, err := f.projection.Exec(f.ctx, "SELECT 1 FROM "+pgx.Identifier{f.schema, table}.Sanitize()+" LIMIT 1")
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Fatal("actual projection LOGIN can read a B table")
		}
	}
	tx := f.begin(t)
	_, err := tx.Exec(f.ctx, `UPDATE periodic_budget_controls SET tenant_id=$2 WHERE id=$1`, control, f.tenant)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatal("actual APP LOGIN can update immutable control identity")
	}
	pbTxMust(t, "rollback denied identity edit", tx.Rollback(f.ctx))
}
