package billing

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func walletRequestForTest(tenant, actor, kind, note string) WalletRequestInput {
	return WalletRequestInput{TenantID: tenant, ActorID: actor, Kind: kind, Note: note, IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()}
}

func TestPostgresWalletRequestsLifecycleIsolationAndDirectSQLLimit(t *testing.T) {
	f := fundingFixture(t)
	input := walletRequestForTest(f.tenant, f.user, "topup", "customer bank reference")
	created, err := f.store.CreateWalletRequest(f.ctx, input)
	if err != nil || created.State != "open" || created.LinkedOrderID != nil {
		t.Fatalf("create wallet request: %+v %v", created, err)
	}
	replayed, err := f.store.CreateWalletRequest(f.ctx, input)
	if err != nil || !reflect.DeepEqual(created, replayed) {
		t.Fatalf("request replay changed the original: %+v %+v %v", created, replayed, err)
	}
	changed := input
	changed.Note = "different customer reference"
	if _, err = f.store.CreateWalletRequest(f.ctx, changed); err == nil {
		t.Fatal("same request identity accepted different content")
	} else {
		assertCode(t, err, "idempotency_conflict")
	}
	other := accountBatchTenant(t, f)
	if _, err = f.store.CreateWalletRequest(f.ctx, walletRequestForTest(other, f.user, "topup", "cross tenant")); err == nil {
		t.Fatal("cross-tenant request was accepted")
	}
	closeInput := WalletRequestCloseInput{TenantID: f.tenant, RequestID: created.ID, ActorID: f.user, Reason: "customer cancelled", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()}
	closed, err := f.store.CloseWalletRequest(f.ctx, closeInput)
	if err != nil || closed.State != "closed" {
		t.Fatalf("close request: %+v %v", closed, err)
	}
	closeReplay, err := f.store.CloseWalletRequest(f.ctx, closeInput)
	if err != nil || !reflect.DeepEqual(closed, closeReplay) {
		t.Fatalf("close replay changed the request: %+v %+v %v", closed, closeReplay, err)
	}
	closeInput.IdempotencyKey = uuid.NewString()
	closeInput.RequestHMAC = uuid.NewString()
	if _, err = f.store.CloseWalletRequest(f.ctx, closeInput); err == nil {
		t.Fatal("closed request was closed again with a new operation")
	} else {
		assertCode(t, err, "request_state_conflict")
	}

	cash, err := f.store.CreateWalletRequest(f.ctx, walletRequestForTest(f.tenant, f.user, "cash_refund", "future cash refund"))
	if err != nil {
		t.Fatal(err)
	}
	credit := creditInputForTest(f.tenant, f.operator, "cash-request-not-linkable", 10, 0, "paid", 10)
	credit.RequestID = cash.ID
	if _, err = f.store.CreateCredit(f.ctx, credit); err == nil {
		t.Fatal("cash refund request was linked before 034.4")
	} else {
		assertCode(t, err, "request_link_unavailable")
	}
	var credits int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM credit_orders WHERE business_ref=$1`, credit.BusinessRef).Scan(&credits); err != nil || credits != 0 {
		t.Fatalf("failed request link retained credit: %d %v", credits, err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE wallet_requests SET note='rewrite' WHERE id=$1`, created.ID); err == nil {
		t.Fatal("request note was mutable")
	}
	if _, err = f.pool.Exec(f.ctx, `DELETE FROM wallet_requests WHERE id=$1`, created.ID); err == nil {
		t.Fatal("request was deletable")
	}

	for i := 0; i < 19; i++ {
		if _, err = f.pool.Exec(f.ctx, `INSERT INTO wallet_requests(id,tenant_id,kind,requested_by,note) VALUES($1,$2,'cash_refund',$3,$4)`, uuid.NewString(), f.tenant, f.user, "direct SQL open request"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO wallet_requests(id,tenant_id,kind,requested_by,note) VALUES($1,$2,'topup',$3,'over limit')`, uuid.NewString(), f.tenant, f.user); err == nil {
		t.Fatal("direct SQL bypassed the 20 open/linked request limit")
	}
	var open int
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM wallet_requests WHERE tenant_id=$1 AND state<>'closed'`, f.tenant).Scan(&open); err != nil || open != 20 {
		t.Fatalf("open request limit=%d %v", open, err)
	}
}

func TestPostgresWalletRequestConcurrentLinkAndApprovalClose(t *testing.T) {
	f := fundingFixture(t)
	request, err := f.store.CreateWalletRequest(f.ctx, walletRequestForTest(f.tenant, f.user, "topup", "one payment request"))
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type outcome struct {
		order CreditOrder
		err   error
	}
	results := make(chan outcome, 2)
	for _, business := range []string{"request-link-a", "request-link-b"} {
		business := business
		go func() {
			<-start
			input := creditInputForTest(f.tenant, f.operator, business, 25, 0, "paid", 25)
			input.RequestID = request.ID
			order, err := f.store.CreateCredit(f.ctx, input)
			results <- outcome{order, err}
		}()
	}
	close(start)
	var approved CreditOrder
	successes, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		result := <-results
		if result.err == nil {
			successes++
			approved = result.order
			continue
		}
		assertCode(t, result.err, "request_revision_conflict")
		conflicts++
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent request links: success=%d conflicts=%d", successes, conflicts)
	}
	var credits, receipts int
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM credit_orders WHERE business_ref IN ('request-link-a','request-link-b')),(SELECT count(*) FROM funding_receipts WHERE parent_ref=$1)`, approved.ID).Scan(&credits, &receipts); err != nil || credits != 1 || receipts != 0 {
		t.Fatalf("concurrent link left partial facts: credits=%d receipts=%d err=%v", credits, receipts, err)
	}
	if _, err = f.store.ApproveCredit(f.ctx, ApprovalInput{TenantID: f.tenant, OrderID: approved.ID, ActorID: f.approver}); err != nil {
		t.Fatal(err)
	}
	var state, linked string
	if err = f.pool.QueryRow(f.ctx, `SELECT state,linked_order_id FROM wallet_requests WHERE id=$1`, request.ID).Scan(&state, &linked); err != nil || state != "closed" || linked != approved.ID {
		t.Fatalf("approval did not close the original link: %s %s %v", state, linked, err)
	}
	if _, err = f.pool.Exec(f.ctx, `UPDATE wallet_requests SET state='open' WHERE id=$1`, request.ID); err == nil {
		t.Fatal("closed request was reopened")
	}
	check, err := f.store.CheckWalletSources(f.ctx, f.tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("request approval left sources unbalanced: %+v %v", check, err)
	}
}
