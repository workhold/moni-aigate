package billing

import (
	"strings"
	"testing"
)

func TestPostgresWalletSourcesConstraintsRejectUnjournaledMutation(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"account_balance", `UPDATE ledger_accounts SET balance=balance+1 WHERE tenant_id=$1 AND kind='AVAILABLE'`},
		{"lot_without_event", `UPDATE funding_lot_balances SET available_amount=available_amount-1,consumed_amount=consumed_amount+1,revision=revision+1 WHERE tenant_id=$1`},
		{"source_identity", `UPDATE funding_lots SET refund_class='sandbox_original' WHERE tenant_id=$1`},
		{"old_journal_event", `INSERT INTO funding_lot_events(id,tenant_id,lot_id,operation_id,journal_id,kind,revision,delta_available,delta_held,delta_consumed)
 SELECT gen_random_uuid()::text,f.tenant_id,f.id,j.operation_id,j.id,'reserve',2,-1,1,0 FROM funding_receipts f JOIN journals j ON j.id=f.journal_id WHERE f.tenant_id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := walletSourcesFixture(t)
			f.fund(t, 150)
			before := walletSourceSnapshot(t, f)
			if _, err := f.pool.Exec(f.ctx, tc.sql, f.tenant); err == nil {
				t.Fatal("unbalanced money mutation committed")
			}
			if walletSourceSnapshot(t, f) != before {
				t.Fatal("rejected mutation changed wallet source facts")
			}
		})
	}
}

func TestPostgresWalletSourcesConstraintsRejectSkippedFIFO(t *testing.T) {
	f := walletSourcesFixture(t)
	f.fund(t, 70)
	f.fund(t, 80)
	before := walletSourceSnapshot(t, f)
	// A deliberately faulty writer changes the selected source before the real
	// FIFO trigger executes. The original financial transaction must roll back.
	_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION corrupt_wallet_fifo_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.allocation_index=1 THEN SELECT id INTO NEW.lot_id FROM funding_lots WHERE tenant_id=NEW.tenant_id ORDER BY fifo_at DESC,id DESC LIMIT 1; END IF; RETURN NEW; END $$;
 CREATE TRIGGER aaa_corrupt_wallet_fifo_test BEFORE INSERT ON funding_reservation_lots FOR EACH ROW EXECUTE FUNCTION corrupt_wallet_fifo_test()`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Reserve(f.ctx, f.input())
	if err == nil || !strings.Contains(err.Error(), "requires FIFO") {
		t.Fatal("FIFO bypass was not rejected", err)
	}
	if walletSourceSnapshot(t, f) != before {
		t.Fatal("FIFO rejection left partial financial facts")
	}
}
