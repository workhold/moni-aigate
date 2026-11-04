package billing

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func openingInput(f *fixture) WalletOpeningInput {
	return WalletOpeningInput{TenantID: f.tenant, ActorID: f.admin, ExternalRef: "opening:" + uuid.NewString(), Amount: 157, MaintenanceMode: true}
}

func openingError(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("wanted safe %s, received %v", code, err)
	}
}

func TestPostgresWalletOpeningOneNoncashSourceAndIntentReplay(t *testing.T) {
	f := walletSourcesFixture(t)
	in := openingInput(f)
	out, err := f.store.OpenWallet(f.ctx, in)
	if err != nil || out.Replayed || out.Receipt.SourceKind != FundingWalletOpening || out.Receipt.Amount != 157 || out.OpeningID != out.Receipt.SourceID {
		t.Fatal(out, err)
	}
	var class string
	var amount, available, held, consumed Money
	var valid bool
	if err = f.pool.QueryRow(f.ctx, `SELECT l.refund_class,l.issued_amount,b.available_amount,b.held_amount,b.consumed_amount,wallet_opening_journal_valid(f.journal_id)
 FROM funding_lots l JOIN funding_lot_balances b ON b.lot_id=l.id JOIN funding_receipts f ON f.id=l.id WHERE l.id=$1`, out.Receipt.ID).Scan(&class, &amount, &available, &held, &consumed, &valid); err != nil || class != "nonrefundable" || amount != 157 || available != 157 || held != 0 || consumed != 0 || !valid {
		t.Fatal(class, amount, available, held, consumed, valid, err)
	}
	before := walletSourceSnapshot(t, f)
	replay, err := f.store.OpenWallet(f.ctx, in)
	if err != nil || !replay.Replayed || replay.OpeningID != out.OpeningID || replay.Receipt.ID != out.Receipt.ID {
		t.Fatal(replay, err)
	}
	if walletSourceSnapshot(t, f) != before {
		t.Fatal("opening replay changed financial facts")
	}
	changed := in
	changed.Amount++
	_, err = f.store.OpenWallet(f.ctx, changed)
	openingError(t, err, "wallet_opening_conflict")
	changed = in
	changed.ExternalRef = "opening:" + uuid.NewString()
	_, err = f.store.OpenWallet(f.ctx, changed)
	openingError(t, err, "wallet_opening_conflict")
	if walletSourceSnapshot(t, f) != before {
		t.Fatal("opening conflict changed financial facts")
	}
	check, err := f.store.CheckWalletSources(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		t.Fatal(check, err)
	}
	f.wallet(t, 157, 0)
}

func TestPostgresWalletOpeningRejectsServiceOwnerAndUsedWallet(t *testing.T) {
	t.Run("service lock", func(t *testing.T) {
		f := walletSourcesFixture(t)
		conn, err := f.pool.Acquire(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		var held bool
		if err = conn.QueryRow(f.ctx, `SELECT pg_try_advisory_lock(782193402)`).Scan(&held); err != nil || !held {
			t.Fatal(err)
		}
		defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(782193402)`)
		_, err = f.store.OpenWallet(f.ctx, openingInput(f))
		openingError(t, err, "wallet_opening_service_active")
	})
	t.Run("non-owner", func(t *testing.T) {
		f := walletSourcesFixture(t)
		app := tenantPricingApplication(t, f)
		_, err := New(app).OpenWallet(f.ctx, openingInput(f))
		openingError(t, err, "wallet_opening_owner_required")
	})
	t.Run("used wallet", func(t *testing.T) {
		f := walletSourcesFixture(t)
		f.fund(t, 17)
		before := walletSourceSnapshot(t, f)
		_, err := f.store.OpenWallet(f.ctx, openingInput(f))
		openingError(t, err, "wallet_opening_not_empty")
		if walletSourceSnapshot(t, f) != before {
			t.Fatal("refused opening changed used wallet")
		}
	})
	t.Run("inactive administrator", func(t *testing.T) {
		f := walletSourcesFixture(t)
		in := openingInput(f)
		in.ActorID = f.operator
		_, err := f.store.OpenWallet(f.ctx, in)
		openingError(t, err, "wallet_opening_forbidden")
	})
}

func TestPostgresWalletOpeningAuditFailureRollsBackSource(t *testing.T) {
	f := walletSourcesFixture(t)
	if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION reject_opening_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='wallet.opened' THEN RAISE EXCEPTION 'opening audit fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_opening_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_opening_audit()`); err != nil {
		t.Fatal(err)
	}
	out, err := f.store.OpenWallet(f.ctx, openingInput(f))
	openingError(t, err, "wallet_opening_failed")
	if out.OpeningID != "" || out.Receipt.ID != "" {
		t.Fatal("failed opening published a receipt")
	}
	var originals, receipts, lots, journals int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM wallet_openings WHERE tenant_id=$1),(SELECT count(*) FROM funding_receipts WHERE tenant_id=$1),(SELECT count(*) FROM funding_lots WHERE tenant_id=$1),(SELECT count(*) FROM journals WHERE tenant_id=$1)`, f.tenant).Scan(&originals, &receipts, &lots, &journals); err != nil || originals != 0 || receipts != 0 || lots != 0 || journals != 0 {
		t.Fatal(originals, receipts, lots, journals, err)
	}
	f.wallet(t, 0, 0)
}

func TestPostgresWalletOpeningUnknownCommitDoesNotPublishOrRepeat(t *testing.T) {
	f := walletSourcesFixture(t)
	pool, armed, lost := voucherCommitLossPool(t, f)
	in := openingInput(f)
	armed.Store(true)
	out, err := New(pool).OpenWallet(f.ctx, in)
	openingError(t, err, "wallet_opening_commit_unknown")
	if out.OpeningID != "" || out.Receipt.ID != "" || lost.Load() != 1 {
		t.Fatal(out, lost.Load())
	}
	var originals, receipts int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM wallet_openings WHERE external_ref=$1),(SELECT count(*) FROM funding_receipts WHERE tenant_id=$2 AND source_kind='wallet_opening')`, in.ExternalRef, f.tenant).Scan(&originals, &receipts); err != nil || originals != 1 || receipts != 1 {
		t.Fatal(originals, receipts, err)
	}
	// Explicit inspection of the original intent retrieves the committed
	// receipt; the command never starts a second opening automatically.
	replay, err := f.store.OpenWallet(f.ctx, in)
	if err != nil || !replay.Replayed {
		t.Fatal(replay, err)
	}
	f.wallet(t, 157, 0)
}
