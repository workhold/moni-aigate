package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/billing"
)

func TestHTTPCashRefundCustomerRequestCancelAndFail(t *testing.T) {
	s, pool := newAuthFixture(t)
	tenant := uuid.NewString()
	operator, _ := authUser(t, s, pool, "finance_operator")
	approver, _ := authUser(t, s, pool, "finance_approver")
	owner, _ := authUser(t, s, pool, "none")
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'cash refund request HTTP')", tenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')", tenant, owner)
	operatorRaw, operatorCSRF := authSession(t, s, pool, operator, "", false, true)
	approverRaw, approverCSRF := authSession(t, s, pool, approver, "", false, true)
	ownerRaw, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	key := uuid.NewString()
	credit, err := s.Billing.CreateCredit(context.Background(), billing.CreditInput{TenantID: tenant, ActorID: operator, BusinessRef: key, ProofRef: "proof:cash-request-http", Amount: 1000, BasisKind: "paid", FiatCurrency: "CNY", FiatAmount: 1000, IdempotencyKey: key, RequestHMAC: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Billing.ApproveCredit(context.Background(), billing.ApprovalInput{TenantID: tenant, OrderID: credit.ID, ActorID: approver}); err != nil {
		t.Fatal(err)
	}
	var lot string
	if err = pool.QueryRow(context.Background(), `SELECT id FROM funding_lots WHERE source_kind='manual_credit' AND source_id=$1`, credit.ID).Scan(&lot); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, raw, csrf, operation string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, s.Config.Origin+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", operation)
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}
	var createdRequest billing.WalletRequest
	requestResponse := call(http.MethodPost, "/api/wallet/requests", ownerRaw, ownerCSRF, uuid.NewString(), map[string]any{"kind": "cash_refund", "note": "please refund my paid coins"})
	requireAuthStatus(t, requestResponse, http.StatusOK)
	if err = json.Unmarshal(authData(t, requestResponse), &createdRequest); err != nil || createdRequest.Kind != "cash_refund" || createdRequest.State != "open" {
		t.Fatalf("customer request DTO: %+v %v", createdRequest, err)
	}
	create := func(business string) billing.CashRefundOrder {
		t.Helper()
		response := call(http.MethodPost, "/api/platform/cash-refunds", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{"tenant_id": tenant, "lot_id": lot, "business_ref": business, "reason": "verified customer request", "request_id": createdRequest.ID})
		requireAuthStatus(t, response, http.StatusOK)
		var order billing.CashRefundOrder
		if err = json.Unmarshal(authData(t, response), &order); err != nil || order.RequestID != createdRequest.ID || order.RequestedBy != owner {
			t.Fatalf("linked cash-refund DTO: %+v %v", order, err)
		}
		return order
	}
	approve := func(order billing.CashRefundOrder) billing.CashRefundOrder {
		t.Helper()
		response := call(http.MethodPost, "/api/platform/cash-refunds/"+order.ID+"/approve", approverRaw, approverCSRF, uuid.NewString(), map[string]any{})
		requireAuthStatus(t, response, http.StatusOK)
		var approved billing.CashRefundOrder
		if err = json.Unmarshal(authData(t, response), &approved); err != nil {
			t.Fatal(err)
		}
		return approved
	}
	first := create("cash-refund:http-customer-cancel")
	approved := approve(first)
	if approved.State != "approved" {
		t.Fatalf("approval state=%s", approved.State)
	}
	var requestState string
	if err = pool.QueryRow(context.Background(), `SELECT state FROM wallet_requests WHERE id=$1`, createdRequest.ID).Scan(&requestState); err != nil || requestState != "linked" {
		t.Fatalf("approval closed request early: %s %v", requestState, err)
	}
	cancelledResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+first.ID+"/cancel", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{"reason": "customer resolved without transfer"})
	requireAuthStatus(t, cancelledResponse, http.StatusOK)
	var cancelled billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, cancelledResponse), &cancelled); err != nil || cancelled.State != "cancelled" || cancelled.ReleaseJournalID == "" {
		t.Fatalf("cancel DTO: %+v %v", cancelled, err)
	}
	if err = pool.QueryRow(context.Background(), `SELECT state FROM wallet_requests WHERE id=$1`, createdRequest.ID).Scan(&requestState); err != nil || requestState != "closed" {
		t.Fatalf("cancel did not close request: %s %v", requestState, err)
	}

	secondRequest, err := s.Billing.CreateWalletRequest(context.Background(), billing.WalletRequestInput{TenantID: tenant, ActorID: owner, Kind: "cash_refund", Note: "new request after cancelled order", IdempotencyKey: uuid.NewString(), RequestHMAC: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	createdRequest = secondRequest
	second := create("cash-refund:http-customer-fail")
	approved = approve(second)
	executedResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+second.ID+"/execute", operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{"transfer_order_ref": "transfer:http-failure"})
	requireAuthStatus(t, executedResponse, http.StatusOK)
	failedResponse := call(http.MethodPost, "/api/platform/cash-refunds/"+second.ID+"/fail", approverRaw, approverCSRF, uuid.NewString(), map[string]any{"reason": "bank rejected transfer"})
	requireAuthStatus(t, failedResponse, http.StatusOK)
	var failed billing.CashRefundOrder
	if err = json.Unmarshal(authData(t, failedResponse), &failed); err != nil || failed.State != "failed" || failed.ReleaseJournalID == "" {
		t.Fatalf("fail DTO: %+v %v", failed, err)
	}
	if err = pool.QueryRow(context.Background(), `SELECT state FROM wallet_requests WHERE id=$1`, createdRequest.ID).Scan(&requestState); err != nil || requestState != "closed" {
		t.Fatalf("failure did not close request: %s %v", requestState, err)
	}
	check, err := s.Billing.CheckWalletSources(context.Background(), tenant)
	if err != nil || !check.Balanced {
		t.Fatalf("cancel/fail sources unbalanced: %+v %v", check, err)
	}
}
