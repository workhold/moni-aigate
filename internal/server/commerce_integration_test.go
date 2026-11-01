package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/billing"
	"github.com/moni-ai/moni-aigate/internal/security"
)

type commerceActor struct{ id, raw, csrf string }
type commerceFixture struct {
	s                                                        *Server
	pool                                                     *pgxpool.Pool
	tenant, otherTenant                                      string
	admin, maker, checker, owner, otherOwner, viewer, member commerceActor
}

func newCommerceFixture(t *testing.T, enabled bool) *commerceFixture {
	t.Helper()
	s, pool := newAuthFixture(t)
	f := &commerceFixture{s: s, pool: pool, tenant: uuid.NewString(), otherTenant: uuid.NewString()}
	authExec(t, pool, "INSERT INTO tenants(id,name) VALUES($1,'commerce test'),($2,'other commerce test')", f.tenant, f.otherTenant)
	actor := func(platformRole, tenantRole, tenant string) commerceActor {
		id := uuid.NewString()
		authExec(t, pool, "INSERT INTO users(id,email,password_hash,platform_role) VALUES($1,$2,'fixture',$3)", id, id+"@commerce.test", platformRole)
		if tenant != "" {
			authExec(t, pool, "INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,$3)", tenant, id, tenantRole)
		}
		raw, csrf := authSession(t, s, pool, id, tenant, false, true)
		return commerceActor{id, raw, csrf}
	}
	f.admin = actor("admin", "", "")
	f.maker = actor("finance_operator", "", "")
	f.checker = actor("finance_approver", "", "")
	f.owner = actor("none", "owner", f.tenant)
	f.otherOwner = actor("none", "owner", f.otherTenant)
	f.viewer = actor("none", "billing_viewer", f.tenant)
	f.member = actor("none", "member", f.tenant)
	s.Config.VouchersEnabled = enabled
	if enabled {
		keys, err := security.ParseVoucherKeyring(`{"v1":"`+base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("v", 32)))+`"}`, "v1")
		if err != nil {
			t.Fatal(err)
		}
		s.Config.VoucherKeys = keys
	}
	if err := s.InitializeVouchers(context.Background()); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *commerceFixture) call(t *testing.T, actor commerceActor, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	// Workflows are sequential; timing/rate exhaustion is tested separately.
	f.s.commerce = commerceAdmission{}
	return authHTTP(t, f.s, method, path, actor.raw, actor.csrf, f.s.Config.Origin, body)
}
func commerceEnvelope(t *testing.T, res *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var v map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func commerceReceipt(t *testing.T, res *httptest.ResponseRecorder) billing.VoucherOperationReceipt {
	t.Helper()
	var v billing.VoucherOperationReceipt
	if err := json.Unmarshal(authData(t, res), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func requireCommerceStatus(t *testing.T, res *httptest.ResponseRecorder, operation, status, reason string) billing.VoucherOperationReceipt {
	t.Helper()
	requireAuthStatus(t, res, 200)
	v := commerceReceipt(t, res)
	if v.OperationID != operation || v.Status != status || v.Reason != reason {
		t.Fatalf("unexpected command receipt: %+v", v)
	}
	if status == "rejected" && (v.ResourceID != nil || v.Amount != nil || v.FundingReceiptID != nil || v.JournalID != nil) {
		t.Fatal("rejected command exposed financial/resource association")
	}
	return v
}
func (f *commerceFixture) enable(t *testing.T) {
	t.Helper()
	requireAuthStatus(t, f.call(t, f.admin, "PUT", "/api/platform/voucher-policy", map[string]any{"expected_revision": "1", "enabled": true}), 200)
}
func (f *commerceFixture) create(t *testing.T, count int) (string, []billing.IssuedVoucherCode, map[string]any) {
	t.Helper()
	op := uuid.NewString()
	body := map[string]any{"operation_id": op, "face_amount": "37", "count": count, "expires_at": time.Now().UTC().Add(2 * time.Hour).Format("2006-01-02T15:04:05.000000Z"), "purpose_ref": "campaign:test", "reason": "integration grant"}
	res := f.call(t, f.maker, "POST", "/api/platform/voucher-batches", body)
	requireAuthStatus(t, res, 201)
	v := commerceReceipt(t, res)
	var codes []billing.IssuedVoucherCode
	envelope := commerceEnvelope(t, res)
	if v.ResourceID == nil || v.Status != "accepted" || v.Amount != nil || v.FundingReceiptID != nil || v.JournalID != nil || string(envelope["secrets_available"]) != "true" || json.Unmarshal(envelope["codes"], &codes) != nil || len(codes) != count {
		t.Fatalf("invalid initial issuance envelope: %s", res.Body.String())
	}
	return *v.ResourceID, codes, body
}

func TestHTTPCommerceIssuanceDualApprovalUniqueFundingAndOffReadback(t *testing.T) {
	f := newCommerceFixture(t, true)
	f.enable(t)
	batch, codes, creation := f.create(t, 1)
	path := "/api/platform/voucher-batches/" + batch
	get := f.call(t, f.maker, "GET", path, nil)
	requireAuthStatus(t, get, 200)
	if strings.Contains(get.Body.String(), codes[0].Code) || strings.Contains(get.Body.String(), "digest") || strings.Contains(get.Body.String(), "key_version") {
		t.Fatal("safe batch GET leaked voucher material")
	}
	var view map[string]json.RawMessage
	if err := json.Unmarshal(authData(t, get), &view); err != nil {
		t.Fatal(err)
	}
	if string(view["approver"]) != "null" || string(view["activated_at"]) != "null" || string(view["terminal_actor"]) != "null" || string(view["terminal_at"]) != "null" {
		t.Fatal("proposed state nullable contract changed")
	}
	requireAuthStatus(t, f.call(t, f.admin, "POST", path+"/approve", map[string]any{"operation_id": uuid.NewString(), "expected_revision": "1"}), 403)
	approve := uuid.NewString()
	requireCommerceStatus(t, f.call(t, f.checker, "POST", path+"/approve", map[string]any{"operation_id": approve, "expected_revision": "1"}), approve, "accepted", "accepted")
	replay := f.call(t, f.maker, "POST", "/api/platform/voucher-batches", creation)
	requireAuthStatus(t, replay, 200)
	envelope := commerceEnvelope(t, replay)
	if _, ok := envelope["codes"]; ok || string(envelope["secrets_available"]) != "false" {
		t.Fatal("issuance retry returned one-time codes")
	}
	operation := uuid.NewString()
	redeem := map[string]any{"operation_id": operation, "code": codes[0].Code}
	accepted := f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", redeem)
	r := requireCommerceStatus(t, accepted, operation, "accepted", "accepted")
	if r.ResourceID == nil || r.Amount == nil || *r.Amount != 37 || r.FundingReceiptID == nil || r.JournalID == nil {
		t.Fatal("redemption financial receipt incomplete")
	}
	if strings.Contains(accepted.Body.String(), codes[0].Code) {
		t.Fatal("redemption echoed voucher")
	}
	duplicate := uuid.NewString()
	requireCommerceStatus(t, f.call(t, f.otherOwner, "POST", "/api/wallet/voucher-redemptions", map[string]any{"operation_id": duplicate, "code": codes[0].Code}), duplicate, "rejected", "voucher_unavailable")
	requireAuthStatus(t, f.call(t, f.otherOwner, "GET", "/api/wallet/voucher-redemptions/"+*r.ResourceID, nil), 404)
	requireAuthStatus(t, f.call(t, f.member, "GET", "/api/wallet/funding-summary", nil), 403)
	requireAuthStatus(t, f.call(t, f.viewer, "POST", "/api/wallet/voucher-redemptions", redeem), 403)
	summary := f.call(t, f.viewer, "GET", "/api/wallet/funding-summary", nil)
	requireAuthStatus(t, summary, 200)
	var totals map[string]string
	if err := json.Unmarshal(authData(t, summary), &totals); err != nil || totals["total_amount"] != "37" || totals["manual_credit_amount"] != "0" || totals["voucher_grant_amount"] != "37" {
		t.Fatalf("funding classification: %v %v", totals, err)
	}
	wallet := f.call(t, f.owner, "GET", "/api/wallet", nil)
	requireAuthStatus(t, wallet, 200)
	var balance map[string]string
	if err := json.Unmarshal(authData(t, wallet), &balance); err != nil || balance["available"] != "37" || balance["total_credited"] != "37" {
		t.Fatalf("wallet omitted voucher funding: %v %v", balance, err)
	}
	page := f.call(t, f.viewer, "GET", "/api/wallet/funding-receipts?source_kind=voucher_grant", nil)
	requireAuthStatus(t, page, 200)
	if string(commerceEnvelope(t, page)["next_cursor"]) != "null" {
		t.Fatal("last cursor must be explicit null")
	}
	requireAuthStatus(t, f.call(t, f.admin, "PUT", "/api/platform/voucher-policy", map[string]any{"expected_revision": "2", "enabled": false}), 200)
	requireCommerceStatus(t, f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", redeem), operation, "accepted", "accepted")
	newOperation := uuid.NewString()
	requireCommerceStatus(t, f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", map[string]any{"operation_id": newOperation, "code": codes[0].Code}), newOperation, "rejected", "vouchers_disabled")
	f.s.Config.VouchersEnabled = false
	f.s.Config.VoucherKeys = nil
	if err := f.s.InitializeVouchers(context.Background()); err != nil {
		t.Fatal(err)
	}
	requireAuthStatus(t, f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", redeem), 409)
	requireCommerceStatus(t, f.call(t, f.owner, "GET", "/api/wallet/commerce-operations/"+operation, nil), operation, "accepted", "accepted")
	var receipts, journals, events int
	if err := f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM funding_receipts),(SELECT count(*) FROM journals),(SELECT count(*) FROM projection_outbox WHERE kind='credited')`).Scan(&receipts, &journals, &events); err != nil || receipts != 1 || journals != 1 || events != 1 {
		t.Fatalf("duplicate funding: receipts=%d journals=%d events=%d err=%v", receipts, journals, events, err)
	}
	if check, err := f.s.Billing.VerifyFunding(context.Background(), ""); err != nil || !check.Balanced {
		t.Fatalf("funding check: %+v %v", check, err)
	}
}

func TestHTTPCommerceStaleRevocationAndTargetBoundOperation(t *testing.T) {
	f := newCommerceFixture(t, true)
	f.enable(t)
	batch, codes, _ := f.create(t, 2)
	path := "/api/platform/voucher-batches/" + batch
	op := uuid.NewString()
	requireCommerceStatus(t, f.call(t, f.checker, "POST", path+"/approve", map[string]any{"operation_id": op, "expected_revision": "1"}), op, "accepted", "accepted")
	proposalOp := uuid.NewString()
	proposal := requireCommerceStatus(t, f.call(t, f.maker, "POST", path+"/revocations", map[string]any{"operation_id": proposalOp, "expected_revision": "2", "expected_redeemed_count": "0", "reason": "retire campaign"}), proposalOp, "accepted", "accepted")
	if proposal.ResourceID == nil {
		t.Fatal("missing proposal")
	}
	redeem := uuid.NewString()
	requireCommerceStatus(t, f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", map[string]any{"operation_id": redeem, "code": codes[0].Code}), redeem, "accepted", "accepted")
	decisionOp := uuid.NewString()
	decisionPath := "/api/platform/voucher-revocations/" + *proposal.ResourceID
	requireCommerceStatus(t, f.call(t, f.checker, "POST", decisionPath+"/approve", map[string]any{"operation_id": decisionOp}), decisionOp, "rejected", "voucher_evidence_stale")
	detail := f.call(t, f.admin, "GET", decisionPath, nil)
	requireAuthStatus(t, detail, 200)
	if !strings.Contains(detail.Body.String(), `"status":"rejected"`) || !strings.Contains(detail.Body.String(), `"reason":"voucher_evidence_stale"`) {
		t.Fatal("stale decision not discoverable")
	}
	other := uuid.NewString()
	requireAuthStatus(t, f.call(t, f.checker, "POST", "/api/platform/voucher-revocations/"+other+"/approve", map[string]any{"operation_id": decisionOp}), 409)
	var decisions int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM voucher_revocation_decisions").Scan(&decisions); err != nil || decisions != 1 {
		t.Fatal("stale decision duplicated")
	}
}

func TestHTTPCommerceStrictRejectionsAndFailClosedStartup(t *testing.T) {
	f := newCommerceFixture(t, false)
	requireAuthStatus(t, f.call(t, f.admin, "PUT", "/api/platform/voucher-policy", map[string]any{"expected_revision": "1", "enabled": true}), 409)
	requireAuthStatus(t, f.call(t, f.maker, "GET", "/api/platform/voucher-policy", nil), 403)
	for _, path := range []string{"/api/platform/voucher-batches?limit=50", "/api/platform/voucher-batches?cursor=x&cursor=y", "/api/platform/voucher-batches?cursor=%FF", "/api/platform/commerce-capabilities?extra=1"} {
		requireAuthStatus(t, f.call(t, f.maker, "GET", path, nil), 400)
	}
	requireAuthStatus(t, f.call(t, f.owner, "POST", "/api/wallet/voucher-redemptions", map[string]any{"operation_id": uuid.NewString(), "code": "invalid"}), 400)
	var commands int
	if err := f.pool.QueryRow(context.Background(), "SELECT count(*) FROM commerce_commands").Scan(&commands); err != nil || commands != 0 {
		t.Fatal("syntax/config failures claimed command")
	}
	f.s.Config.VouchersEnabled = true
	f.s.Config.VoucherKeys = nil
	if err := f.s.InitializeVouchers(context.Background()); err == nil {
		t.Fatal("enabled missing keys did not fail initialization")
	}
}
