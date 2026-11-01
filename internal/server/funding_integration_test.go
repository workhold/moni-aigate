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

// Exercise the existing human credit API through the new source receipt path.
// The wallet API keeps its original shape; credited includes every funding
// source, while balance and asynchronous reports keep their own meanings.
func TestHTTPFundingManualCreditPreservesWalletAndApprovalContract(t *testing.T) {
	s, pool := newAuthFixture(t)
	tenant, otherTenant := uuid.NewString(), uuid.NewString()
	operator, _ := authUser(t, s, pool, "finance_operator")
	approver, _ := authUser(t, s, pool, "finance_approver")
	owner, _ := authUser(t, s, pool, "none")
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'funding HTTP'),($2,'other funding HTTP')", tenant, otherTenant)
	authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner'),($3,$2,'owner')", tenant, owner, otherTenant)
	operatorRaw, operatorCSRF := authSession(t, s, pool, operator, "", false, true)
	approverRaw, approverCSRF := authSession(t, s, pool, approver, "", false, true)
	ownerRaw, ownerCSRF := authSession(t, s, pool, owner, tenant, false, true)
	otherRaw, _ := authSession(t, s, pool, owner, otherTenant, false, true)
	creditHTTP := func(path, raw, csrf, key string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, s.Config.Origin+path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", s.Config.Origin)
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", key)
		req.AddCookie(&http.Cookie{Name: "moni_session", Value: raw})
		res := httptest.NewRecorder()
		s.Engine().ServeHTTP(res, req)
		return res
	}
	key := uuid.NewString()
	body := map[string]any{"tenant_id": tenant, "amount": "37", "business_ref": "funding-http:" + key, "proof_ref": "funding:verified"}
	created := creditHTTP("/api/platform/credits", operatorRaw, operatorCSRF, key, body)
	requireAuthStatus(t, created, http.StatusOK)
	var order billing.CreditOrder
	if err := json.Unmarshal(authData(t, created), &order); err != nil || order.ID == "" || order.State != "pending" {
		t.Fatalf("credit proposal contract: %+v %v", order, err)
	}
	path := "/api/platform/credits/" + order.ID + "/approve"
	requireAuthStatus(t, creditHTTP(path, operatorRaw, operatorCSRF, uuid.NewString(), map[string]any{}), http.StatusForbidden)
	first := creditHTTP(path, approverRaw, approverCSRF, uuid.NewString(), map[string]any{})
	requireAuthStatus(t, first, http.StatusOK)
	var approved billing.CreditOrder
	if err := json.Unmarshal(authData(t, first), &approved); err != nil || approved.ID != order.ID || approved.JournalID == "" || approved.Amount != 37 || approved.State != "approved" {
		t.Fatalf("credit approval contract: %+v %v", approved, err)
	}
	repeated := creditHTTP(path, approverRaw, approverCSRF, uuid.NewString(), map[string]any{})
	requireAuthStatus(t, repeated, http.StatusOK)
	if !bytes.Equal(authData(t, first), authData(t, repeated)) {
		t.Fatal("repeat approval changed the original credit result")
	}
	var receipts, events, journals int
	var receiptJournal, amount, kind string
	err := pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM funding_receipts WHERE tenant_id=$1),
 (SELECT count(*) FROM projection_outbox WHERE tenant_id=$1 AND kind='credited'),
 (SELECT count(*) FROM journals WHERE tenant_id=$1),
 r.journal_id,r.amount::text,r.source_kind
 FROM funding_receipts r WHERE r.tenant_id=$1 AND r.source_id=$2`, tenant, order.ID).Scan(&receipts, &events, &journals, &receiptJournal, &amount, &kind)
	if err != nil || receipts != 1 || events != 1 || journals != 1 || receiptJournal != approved.JournalID || amount != "37" || kind != "manual_credit" {
		t.Fatalf("credit funding facts: receipts=%d events=%d journals=%d amount=%s kind=%s err=%v", receipts, events, journals, amount, kind, err)
	}
	for _, check := range []struct{ raw, credited string }{{ownerRaw, "37"}, {otherRaw, "0"}} {
		res := authHTTP(t, s, http.MethodGet, "/api/wallet", check.raw, "", "", nil)
		requireAuthStatus(t, res, http.StatusOK)
		var wallet map[string]json.RawMessage
		if err := json.Unmarshal(authData(t, res), &wallet); err != nil {
			t.Fatal(err)
		}
		if len(wallet) != 6 || string(wallet["total_credited"]) != `"`+check.credited+`"` || string(wallet["available"]) != `"`+check.credited+`"` || string(wallet["held"]) != `"0"` || string(wallet["total_charged"]) != `"0"` || string(wallet["total_refunded"]) != `"0"` {
			t.Fatalf("wallet shape or tenant funding changed: %s", authData(t, res))
		}
	}
	// A customer role does not gain platform credit rights from the new receipt.
	requireAuthStatus(t, creditHTTP(path, ownerRaw, ownerCSRF, uuid.NewString(), map[string]any{}), http.StatusForbidden)
	if check, err := s.Billing.VerifyFunding(context.Background(), tenant); err != nil || !check.Balanced {
		t.Fatalf("funding verification: %+v %v", check, err)
	}
}
