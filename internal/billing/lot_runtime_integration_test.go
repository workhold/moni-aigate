package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Keep this fixture on the current formal migration and ordinary domain
// commands. It never installs a funding-lot candidate or backfills balances.
func walletSourcesFixture(t *testing.T) *fixture {
	t.Helper()
	base := fundingFixture(t)
	f := *base
	f.tenant, f.key = uuid.NewString(), uuid.NewString()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tenants(id,name) VALUES($1,'wallet source test')`, []any{f.tenant}},
		{`INSERT INTO memberships(tenant_id,user_id,role,allow_all_models) VALUES($1,$2,'owner',true)`, []any{f.tenant, f.user}},
		{`INSERT INTO tenant_model_grants(tenant_id,model_id) VALUES($1,$2)`, []any{f.tenant, f.model}},
		{`INSERT INTO api_keys(id,tenant_id,owner_id,name,prefix,digest,scopes,lifetime_limit) VALUES($1,$2,$3,'sources','sources',$1,$4,1000)`, []any{f.key, f.tenant, f.user, []string{f.model}}},
	} {
		if _, err = tx.Exec(f.ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	return &f
}

func walletSourceSnapshot(t *testing.T, f *fixture) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'lots',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lots x WHERE tenant_id=$1),
 'balances',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.lot_id) FROM funding_lot_balances x WHERE tenant_id=$1),
 'events',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lot_events x WHERE tenant_id=$1),
 'reserve',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.reservation_id,x.allocation_index) FROM funding_reservation_lots x WHERE tenant_id=$1),
 'settle',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.settlement_id,x.allocation_index) FROM funding_settlement_lots x WHERE tenant_id=$1),
 'refund',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.refund_order_id,x.refund_index) FROM funding_consumption_refund_lots x WHERE tenant_id=$1),
 'journals',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x WHERE tenant_id=$1),
 'accounts',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),
 'key',(SELECT to_jsonb(x) FROM api_keys x WHERE id=$2),
 'reservations',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM reservations x WHERE tenant_id=$1),
 'settlements',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM settlements x WHERE tenant_id=$1),
 'refund_orders',(SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM refund_orders x WHERE tenant_id=$1))::text`, f.tenant, f.key).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func walletSourceRefund(t *testing.T, f *fixture, settlement string, amount Money) RefundOrder {
	t.Helper()
	id := uuid.NewString()
	o, err := f.store.CreateRefund(f.ctx, RefundInput{TenantID: f.tenant, ActorID: f.operator, SettlementID: settlement, BusinessRef: "source:" + id, Reason: "source test", Amount: amount, IdempotencyKey: id, RequestHMAC: id})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestPostgresWalletSourcesRuntimeFIFOFinishAndReverseRefund(t *testing.T) {
	f := walletSourcesFixture(t)
	f.fund(t, 70)
	f.fund(t, 80)
	rows, err := f.pool.Query(f.ctx, `SELECT id FROM funding_lots WHERE tenant_id=$1 ORDER BY fifo_at,id COLLATE "C"`, f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatal(ids)
	}
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	var allocations string
	if err = f.pool.QueryRow(f.ctx, `SELECT jsonb_agg(jsonb_build_array(lot_id,amount) ORDER BY allocation_index)::text FROM funding_reservation_lots WHERE reservation_id=$1`, r.ID).Scan(&allocations); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal([][]any{{ids[0], 70}, {ids[1], 50}})
	var got, wanted any
	if json.Unmarshal([]byte(allocations), &got) != nil || json.Unmarshal(want, &wanted) != nil {
		t.Fatal(allocations)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(wanted)
	if string(gotJSON) != string(wantJSON) {
		t.Fatal(allocations, string(want))
	}
	f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 90, OutputTokens: 10, TotalTokens: 100}, "completed", "eof", "content", true)
	s, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
	if err != nil || s.ChargedAmount != 100 {
		t.Fatal(s, err)
	}
	for _, amount := range []Money{20, 25} {
		o := walletSourceRefund(t, f, s.ID, amount)
		approved, e := f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver})
		if e != nil || approved.State != "approved" {
			t.Fatal(approved, e)
		}
		before := walletSourceSnapshot(t, f)
		if _, e = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: o.ID, ActorID: f.approver}); e != nil {
			t.Fatal(e)
		}
		if walletSourceSnapshot(t, f) != before {
			t.Fatal("repeated approval changed source history")
		}
	}
	for i, want := range [][3]int64{{15, 0, 55}, {80, 0, 0}} {
		var available, held, consumed int64
		if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,held_amount,consumed_amount FROM funding_lot_balances WHERE lot_id=$1`, ids[i]).Scan(&available, &held, &consumed); err != nil || [3]int64{available, held, consumed} != want {
			t.Fatal(i, available, held, consumed, err)
		}
	}
	f.wallet(t, 95, 0)
	fundingCheck(t, f, f.tenant)
}

func TestPostgresWalletSourcesRuntimeEventFailureRollsBack(t *testing.T) {
	for _, kind := range []string{"funding", "finish", "refund"} {
		t.Run(kind, func(t *testing.T) {
			f := walletSourcesFixture(t)
			f.fund(t, 150)
			var reserve Reservation
			var refund RefundOrder
			var credit CreditOrder
			if kind == "funding" {
				credit = fundingCredit(t, f, f.tenant, 17)
			} else {
				reserve = f.reserve(t)
				defer f.store.EndRequest(reserve.RequestID)
				if kind == "refund" {
					f.final(t, reserve, confirmed(), "completed", "eof", "content", true)
					s, e := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: reserve.RequestID})
					if e != nil {
						t.Fatal(e)
					}
					refund = walletSourceRefund(t, f, s.ID, 5)
				}
			}
			before := walletSourceSnapshot(t, f)
			_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION source_event_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='`+kind+`' THEN RAISE EXCEPTION 'source event fault'; END IF; RETURN NEW; END $$; CREATE TRIGGER source_event_failure BEFORE INSERT ON funding_lot_events FOR EACH ROW EXECUTE FUNCTION source_event_failure()`)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "funding":
				_, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: credit.ID, ActorID: f.approver})
			case "finish":
				_, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: reserve.RequestID, Reason: "not_sent"})
			case "refund":
				_, err = f.store.ApproveRefund(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: refund.ID, ActorID: f.approver})
			}
			if err == nil {
				t.Fatal("event failure accepted")
			}
			if walletSourceSnapshot(t, f) != before {
				t.Fatal("source failure left partial wallet facts")
			}
		})
	}
}

func TestPostgresWalletSourcesRuntimeReserveWaitExpiresBeforeRequestInsert(t *testing.T) {
	f := walletSourcesFixture(t)
	f.fund(t, 150)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var lot string
	var blocker uint32
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT lot_id FROM funding_lot_balances WHERE tenant_id=$1 FOR UPDATE`, f.tenant).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.RequestDeadline = time.Now().Add(1500 * time.Millisecond)
	done := make(chan error, 1)
	go func() { _, e := f.store.Reserve(ctx, in); done <- e }()
	_, query := tenantPricingWaiter(t, ctx, f, blocker)
	if !strings.Contains(query, "funding_lot_balances") {
		t.Fatal("waited on another parent", query)
	}
	var requests int
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM request_records WHERE id=$1`, in.RequestID).Scan(&requests); err != nil || requests != 0 {
		t.Fatal("request inserted before lot wait", requests, err)
	}
	// An observed lock edge identifies the exact waiter. Wait for the database
	// time boundary, then release it; elapsed sleep alone never supplies a lock.
	for {
		var expired bool
		if err = f.pool.QueryRow(ctx, `SELECT clock_timestamp()>=$1`, in.RequestDeadline).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		var be *Error
		if !errors.As(err, &be) || be.Code != "request_deadline" {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err = f.pool.QueryRow(ctx, `SELECT count(*) FROM request_records WHERE id=$1`, in.RequestID).Scan(&requests); err != nil || requests != 0 {
		t.Fatal(requests, err)
	}
	f.wallet(t, 150, 0)
}

func TestPostgresWalletSourcesRuntimeReleaseAndZeroPrice(t *testing.T) {
	f := walletSourcesFixture(t)
	f.fund(t, 150)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	s, err := f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: r.RequestID, Reason: "not_sent"})
	if err != nil || s.Status != "released" || s.ChargedAmount != 0 {
		t.Fatal(s, err)
	}
	f.wallet(t, 150, 0)
	var available, held, consumed int64
	if err = f.pool.QueryRow(f.ctx, `SELECT available_amount,held_amount,consumed_amount FROM funding_lot_balances WHERE tenant_id=$1`, f.tenant).Scan(&available, &held, &consumed); err != nil || available != 150 || held != 0 || consumed != 0 {
		t.Fatal(available, held, consumed, err)
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO price_versions(model_id,input_rate,output_rate,effective_at,created_by) VALUES($1,0,0,clock_timestamp(),$2)`, f.model, f.operator); err != nil {
		t.Fatal(err)
	}
	var eventCountBefore int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM funding_lot_events WHERE tenant_id=$1`, f.tenant).Scan(&eventCountBefore); err != nil {
		t.Fatal(err)
	}
	zero := f.reserve(t)
	defer f.store.EndRequest(zero.RequestID)
	if zero.Amount != 0 {
		t.Fatal(zero)
	}
	if _, err = f.store.Release(f.ctx, ReleaseInput{TenantID: f.tenant, RequestID: zero.RequestID, Reason: "not_sent"}); err != nil {
		t.Fatal(err)
	}
	var edges, events int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM funding_reservation_lots WHERE reservation_id=$1),(SELECT count(*) FROM funding_lot_events WHERE tenant_id=$2)`, zero.ID, f.tenant).Scan(&edges, &events); err != nil || edges != 0 || events != eventCountBefore {
		t.Fatal(edges, events, eventCountBefore, err)
	}
	f.wallet(t, 150, 0)
}

func TestPostgresWalletSourcesRuntimeFinishWaitHonorsFinancialDeadline(t *testing.T) {
	f := walletSourcesFixture(t)
	f.fund(t, 150)
	r := f.reserve(t)
	defer f.store.EndRequest(r.RequestID)
	f.final(t, r, confirmed(), "completed", "eof", "content", true)
	setFixtureReservationDeadline(t, f, r.RequestID, "1.5 seconds")
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var lot string
	var blocker uint32
	if err = tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT lot_id FROM funding_lot_balances WHERE tenant_id=$1 FOR UPDATE`, f.tenant).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	type result struct {
		settlement Settlement
		err        error
	}
	done := make(chan result, 1)
	go func() {
		s, e := f.store.Settle(ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
		done <- result{s, e}
	}()
	_, query := tenantPricingWaiter(t, ctx, f, blocker)
	if !strings.Contains(query, "funding_lot_balances") {
		t.Fatal(query)
	}
	for {
		var expired bool
		if err = f.pool.QueryRow(ctx, `SELECT clock_timestamp()>=deadline FROM reservations WHERE id=$1`, r.ID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out.err != nil || out.settlement.Status != "released" || out.settlement.ChargedAmount != 0 || out.settlement.Reason != "released_deadline" {
			t.Fatal(out)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	f.wallet(t, 150, 0)
}
