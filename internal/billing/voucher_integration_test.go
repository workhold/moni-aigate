package billing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func voucherTestKeys(t *testing.T, active string, versions ...string) *security.VoucherKeyring {
	t.Helper()
	m := map[string]string{}
	for i, v := range versions {
		m[v] = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat(strconv.Itoa(i+1), 32)))
	}
	b, _ := json.Marshal(m)
	k, e := security.ParseVoucherKeyring(string(b), active)
	if e != nil {
		t.Fatal(e)
	}
	return k
}
func voucherTestService(t *testing.T, f *fixture) *VoucherService {
	t.Helper()
	v, e := NewVoucherService(f.ctx, f.store, VoucherOptions{RuntimeAllowed: true, Keys: voucherTestKeys(t, "v1", "v1")})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = v.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.admin, ExpectedRevision: 1, Enabled: true}); e != nil {
		t.Fatal(e)
	}
	return v
}
func voucherTestCommand(actor string) VoucherCommandContext {
	return VoucherCommandContext{OperationID: uuid.NewString(), ActorID: actor, RequestID: uuid.NewString()}
}
func voucherTestRedeem(f *fixture, code string) RedeemVoucherInput {
	c := voucherTestCommand(f.user)
	c.TenantID = f.tenant
	return RedeemVoucherInput{Command: c, Code: code}
}
func voucherTestInput(f *fixture, count int, amount Money) CreateVoucherBatchInput {
	return CreateVoucherBatchInput{Command: voucherTestCommand(f.operator), FaceAmount: amount, Count: count, ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond), PurposeRef: "grant:test", Reason: "voucher test"}
}
func voucherTestCreate(t *testing.T, f *fixture, v *VoucherService, count int, amount Money, active bool) CreateVoucherBatchResult {
	t.Helper()
	r, e := v.CreateVoucherBatch(f.ctx, voucherTestInput(f, count, amount))
	if e != nil || r.Receipt.Status != "accepted" || !r.Created || !r.SecretsAvailable || len(r.Codes) != count || r.Batch == nil {
		t.Fatalf("create failed: error=%v status=%s", e, r.Receipt.Status)
	}
	if active {
		a, e := v.ApproveVoucherBatch(f.ctx, ApproveVoucherBatchInput{Command: voucherTestCommand(f.approver), BatchID: r.Batch.ID, ExpectedRevision: 1})
		if e != nil || a.Status != "accepted" {
			t.Fatalf("activation failed: %v status=%s", e, a.Status)
		}
	}
	return r
}
func voucherTestError(t *testing.T, e error, code string) {
	t.Helper()
	var v *VoucherError
	if !errors.As(e, &v) || v.Code != code || e.Error() != code {
		t.Fatalf("want safe error %s, got %v", code, e)
	}
}
func voucherTestRejected(t *testing.T, r VoucherOperationReceipt, e error, reason string) {
	t.Helper()
	if e != nil || r.Status != "rejected" || r.Reason != reason || r.ResourceID != nil || r.Amount != nil || r.FundingReceiptID != nil || r.JournalID != nil {
		t.Fatalf("want safe rejected %s, got status=%s reason=%s error=%v", reason, r.Status, r.Reason, e)
	}
}
func voucherTestFunding(t *testing.T, f *fixture) {
	t.Helper()
	f.check(t)
	for _, scope := range []string{"", f.tenant} {
		c, e := f.store.VerifyFunding(f.ctx, scope)
		if e != nil || !c.Balanced {
			t.Fatalf("funding verification %+v %v", c, e)
		}
	}
}

func TestPostgresVoucherLifecycleAndFundingProjection(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	issued := voucherTestCreate(t, f, v, 2, 17, true)
	in := voucherTestRedeem(f, issued.Codes[0].Code)
	r, e := v.RedeemVoucher(f.ctx, in)
	if e != nil || r.Status != "accepted" || r.Amount == nil || *r.Amount != 17 || r.ResourceID == nil || r.Scope != f.tenant {
		t.Fatalf("redemption failed: status=%s error=%v", r.Status, e)
	}
	r2, e := v.RedeemVoucher(f.ctx, in)
	if e != nil || r2.OperationID != r.OperationID || r2.ResourceID == nil || *r2.ResourceID != *r.ResourceID {
		t.Fatal("redemption replay changed receipt")
	}
	view, e := v.GetVoucherRedemption(f.ctx, VoucherReader{f.user, f.tenant}, *r.ResourceID)
	if e != nil || view.Amount != 17 || view.Currency != "COIN" {
		t.Fatal("safe redemption query failed", e)
	}
	batch, e := v.GetVoucherBatch(f.ctx, f.approver, issued.Batch.ID)
	if e != nil || batch.RedeemedCount != "1" || batch.RemainingCount != "1" || batch.Revision != "2" {
		t.Fatal("batch counter/revision incorrect", e)
	}
	summary, e := v.GetFundingSummary(f.ctx, VoucherReader{f.user, f.tenant})
	if e != nil || summary.TotalAmount != "1017" || summary.ManualCreditAmount != "1000" || summary.VoucherGrantAmount != "17" {
		t.Fatal("funding source summary incorrect", e)
	}
	page, e := v.ListFundingReceipts(f.ctx, VoucherReader{f.user, f.tenant}, FundingReceiptPageInput{SourceKind: FundingVoucherGrant})
	if e != nil || len(page.Data) != 1 || page.Data[0].SourceID != issued.Codes[0].VoucherID {
		t.Fatal("funding receipt source filter incorrect", e)
	}
	voucherTestFunding(t, f)
	if n, e := f.store.Project(f.ctx, ProjectInput{}); e != nil || n != 2 {
		t.Fatal("credited projection incorrect", n, e)
	}
	if n, e := f.store.Rebuild(f.ctx, "voucher-rebuild"); e != nil || n != 2 {
		t.Fatal("credited rebuild incorrect", n, e)
	}
	report, e := f.store.Report(f.ctx, Query{TenantID: f.tenant})
	if e != nil || report.Credited != "1017" {
		t.Fatal("voucher missing from legacy report", e)
	}
	f.wallet(t, 1017, 0)
}

func TestPostgresVoucherOneHundredRedemptionsHaveOneFunding(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	issued := voucherTestCreate(t, f, v, 1, 23, true)
	const n = 100
	var wg sync.WaitGroup
	receipts := make([]VoucherOperationReceipt, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			receipts[i], errs[i] = v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code))
		}(i)
	}
	wg.Wait()
	accepted := 0
	for i, r := range receipts {
		if errs[i] != nil {
			t.Fatalf("concurrent redemption error at %d: %v", i, errs[i])
		}
		if r.Status == "accepted" {
			accepted++
		} else {
			voucherTestRejected(t, r, nil, "voucher_unavailable")
		}
	}
	if accepted != 1 {
		t.Fatal("accepted redemption count", accepted)
	}
	var commands, terminals, redemptions, journals, events int
	e := f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM commerce_commands WHERE kind='voucher.redeem'),(SELECT count(*) FROM commerce_operation_receipts WHERE kind='voucher.redeem'),(SELECT count(*) FROM voucher_redemptions),(SELECT count(*) FROM journals WHERE kind='voucher_grant'),(SELECT count(*) FROM projection_outbox o JOIN journals j ON j.operation_id=o.operation_id WHERE j.kind='voucher_grant')`).Scan(&commands, &terminals, &redemptions, &journals, &events)
	if e != nil || commands != 100 || terminals != 100 || redemptions != 1 || journals != 1 || events != 1 {
		t.Fatal("unique funding/terminal facts failed", commands, terminals, redemptions, journals, events, e)
	}
	f.wallet(t, 1023, 0)
	voucherTestFunding(t, f)
}

func TestPostgresVoucherCreateReplayRotationAndCurrentRights(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	in := voucherTestInput(f, 2, 13)
	var wg sync.WaitGroup
	results := make([]CreateVoucherBatchResult, 8)
	errs := make([]error, 8)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(len(results))
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ready.Done()
			<-start
			results[i], errs[i] = v.CreateVoucherBatch(f.ctx, in)
		}(i)
	}
	ready.Wait()
	close(start)
	wg.Wait()
	winners := 0
	var issued CreateVoucherBatchResult
	for i, r := range results {
		if errs[i] != nil || r.Receipt.Status != "accepted" {
			t.Fatal("create replay failed", errs[i])
		}
		if r.Created {
			winners++
			issued = r
		} else if r.SecretsAvailable || len(r.Codes) != 0 || r.Batch == nil {
			t.Fatal("create replay exposed secrets or lost safe batch")
		}
	}
	if winners != 1 {
		t.Fatal("one-time response winners", winners)
	}
	changed := in
	changed.Reason = "changed"
	_, e := v.CreateVoucherBatch(f.ctx, changed)
	voucherTestError(t, e, "commerce_operation_conflict")
	if _, e = v.ApproveVoucherBatch(f.ctx, ApproveVoucherBatchInput{Command: voucherTestCommand(f.approver), BatchID: issued.Batch.ID, ExpectedRevision: 1}); e != nil {
		t.Fatal(e)
	}
	redeem := voucherTestRedeem(f, issued.Codes[0].Code)
	first, e := v.RedeemVoucher(f.ctx, redeem)
	if e != nil || first.Status != "accepted" {
		t.Fatal("first redemption", e)
	}
	rotated, e := NewVoucherService(f.ctx, f.store, VoucherOptions{RuntimeAllowed: true, Keys: voucherTestKeys(t, "v2", "v1", "v2")})
	if e != nil {
		t.Fatal(e)
	}
	replay, e := rotated.RedeemVoucher(f.ctx, redeem)
	if e != nil || replay.OperationID != first.OperationID || replay.Status != "accepted" {
		t.Fatal("old fingerprint replay after rotation failed", e)
	}
	missing := voucherTestKeys(t, "v2", "v2")
	_, e = NewVoucherService(f.ctx, f.store, VoucherOptions{RuntimeAllowed: true, Keys: missing})
	voucherTestError(t, e, "voucher_key_unavailable")
	if _, e = f.pool.Exec(f.ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.operator); e != nil {
		t.Fatal(e)
	}
	_, e = v.CreateVoucherBatch(f.ctx, in)
	voucherTestError(t, e, "forbidden")
	if _, e = f.pool.Exec(f.ctx, `UPDATE memberships SET status='suspended' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user); e != nil {
		t.Fatal(e)
	}
	_, e = rotated.RedeemVoucher(f.ctx, redeem)
	voucherTestError(t, e, "forbidden")
	voucherTestFunding(t, f)
}

func TestPostgresVoucherRevocationStaleDecisionAndRuntimeOffClose(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	issued := voucherTestCreate(t, f, v, 3, 11, true)
	proposal, e := v.ProposeVoucherRevocation(f.ctx, ProposeVoucherRevocationInput{Command: voucherTestCommand(f.operator), BatchID: issued.Batch.ID, ExpectedRevision: 2, Reason: "revoke old evidence"})
	if e != nil || proposal.ResourceID == nil {
		t.Fatal("proposal", e)
	}
	if r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code)); e != nil || r.Status != "accepted" {
		t.Fatal("interleaved redemption", e)
	}
	approve := ApproveVoucherRevocationInput{Command: voucherTestCommand(f.approver), ProposalID: *proposal.ResourceID}
	r, e := v.ApproveVoucherRevocation(f.ctx, approve)
	voucherTestRejected(t, r, e, "voucher_evidence_stale")
	decision, e := v.GetVoucherRevocation(f.ctx, f.operator, *proposal.ResourceID)
	if e != nil || decision.Decision == nil || decision.Decision.Status != "rejected" {
		t.Fatal("stale decision not discoverable", e)
	}
	r, e = v.ApproveVoucherRevocation(f.ctx, approve)
	voucherTestRejected(t, r, e, "voucher_evidence_stale")
	approve.Command = voucherTestCommand(f.approver)
	r, e = v.ApproveVoucherRevocation(f.ctx, approve)
	voucherTestRejected(t, r, e, "voucher_state_conflict")
	closed, e := NewVoucherService(f.ctx, f.store, VoucherOptions{})
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := closed.ProposeVoucherRevocation(f.ctx, ProposeVoucherRevocationInput{Command: voucherTestCommand(f.operator), BatchID: issued.Batch.ID, ExpectedRevision: 2, ExpectedRedeemedCount: 1, Reason: "current evidence"})
	if e != nil || fresh.ResourceID == nil {
		t.Fatal("runtime off proposal", e)
	}
	r, e = closed.ApproveVoucherRevocation(f.ctx, ApproveVoucherRevocationInput{Command: voucherTestCommand(f.approver), ProposalID: *fresh.ResourceID})
	if e != nil || r.Status != "accepted" {
		t.Fatal("runtime off revoke", e)
	}
	r, e = v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[1].Code))
	voucherTestRejected(t, r, e, "voucher_unavailable")
	_, e = closed.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[2].Code))
	voucherTestError(t, e, "vouchers_runtime_disabled")
	other := voucherTestCreate(t, f, v, 1, 1, false)
	r, e = closed.VoidVoucherBatch(f.ctx, VoidVoucherBatchInput{Command: voucherTestCommand(f.operator), BatchID: other.Batch.ID, ExpectedRevision: 1, Reason: "close unpublished"})
	if e != nil || r.Status != "accepted" {
		t.Fatal("runtime off void", e)
	}
	f.wallet(t, 1011, 0)
	voucherTestFunding(t, f)
}

func TestPostgresVoucherOverflowAndInfrastructureRollback(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	openingBoundaryTenant(t, f)
	issued := voucherTestCreate(t, f, v, 1, 1, true)
	before := fundingSnapshot(t, f)
	r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code))
	voucherTestRejected(t, r, e, "funding_amount_overflow")
	var count int
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM voucher_redemptions`).Scan(&count); e != nil || count != 0 {
		t.Fatal("overflow recorded redemption", e)
	}
	batch, e := v.GetVoucherBatch(f.ctx, f.operator, issued.Batch.ID)
	if e != nil || batch.RedeemedCount != "0" {
		t.Fatal("overflow incremented batch", e)
	}
	if before == "" {
		t.Fatal("empty pre-state")
	}
	voucherTestFunding(t, f)
	// A failing audit must roll back issuance, command, all digests and codes.
	if _, e = f.pool.Exec(f.ctx, `CREATE FUNCTION voucher_test_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='voucher.batch.created' THEN RAISE EXCEPTION 'fixture private detail'; END IF; RETURN NEW; END $$; CREATE TRIGGER voucher_test_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION voucher_test_audit_failure()`); e != nil {
		t.Fatal(e)
	}
	in := voucherTestInput(f, 2, 3)
	failed, e := v.CreateVoucherBatch(f.ctx, in)
	voucherTestError(t, e, "commerce_unavailable")
	if failed.Created || failed.SecretsAvailable || len(failed.Codes) != 0 || failed.Batch != nil {
		t.Fatal("uncommitted secrets escaped")
	}
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM commerce_commands WHERE operation_id=$1`, in.Command.OperationID).Scan(&count); e != nil || count != 0 {
		t.Fatal("failed issuance retained command", e)
	}
}

func TestPostgresVoucherPolicyRejectsAndNoop(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	_, e := v.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.operator, ExpectedRevision: 2, Enabled: false})
	voucherTestError(t, e, "forbidden")
	_, e = v.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.admin, ExpectedRevision: 1, Enabled: false})
	voucherTestError(t, e, "voucher_policy_conflict")
	p, e := v.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.admin, ExpectedRevision: 2, Enabled: true})
	if e != nil || p.Revision != "2" {
		t.Fatal("policy no-op incremented revision", e)
	}
	issued := voucherTestCreate(t, f, v, 1, 3, true)
	p, e = v.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.admin, ExpectedRevision: 2, Enabled: false})
	if e != nil || p.Revision != "3" {
		t.Fatal("policy disable", e)
	}
	r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code))
	voucherTestRejected(t, r, e, "vouchers_disabled")
	create, e := v.CreateVoucherBatch(f.ctx, voucherTestInput(f, 1, 1))
	voucherTestRejected(t, create.Receipt, e, "vouchers_disabled")
	closed, e := NewVoucherService(f.ctx, f.store, VoucherOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if cap, e := closed.WalletCommerceCapabilities(f.ctx, VoucherReader{f.user, f.tenant}); e != nil || cap.VouchersEnabled || cap.PaymentsMode != "off" {
		t.Fatal("off capabilities", e)
	}
	if _, e = closed.GetPlatformCommerceOperation(f.ctx, f.operator, issued.Receipt.OperationID); e != nil {
		t.Fatal("off receipt read required keys", e)
	}
	_, e = closed.UpdateVoucherPolicy(f.ctx, UpdateVoucherPolicyInput{ActorID: f.admin, ExpectedRevision: 3, Enabled: true})
	voucherTestError(t, e, "vouchers_runtime_disabled")
	recovery, e := NewVoucherService(f.ctx, f.store, VoucherOptions{RecoveryMode: true})
	if e != nil {
		t.Fatal(e)
	}
	_, e = recovery.VoidVoucherBatch(f.ctx, VoidVoucherBatchInput{Command: voucherTestCommand(f.operator), BatchID: issued.Batch.ID, ExpectedRevision: 2, Reason: "attempt"})
	voucherTestError(t, e, "commerce_unavailable")
}

func TestVoucherCursorStrictBindingAndCanonicalTime(t *testing.T) {
	filter := voucherFilter("platform", "batches", "")
	base := voucherCursor{V: 1, CreatedAt: "2026-11-01T00:00:00.000000Z", ID: uuid.NewString(), Filter: filter}
	b, _ := json.Marshal(base)
	raw := base64.RawURLEncoding.EncodeToString(b)
	if at, id, e := decodeVoucherCursor(raw, filter); e != nil || id != base.ID || at == nil {
		t.Fatal("valid cursor rejected", e)
	}
	_, _, e := decodeVoucherCursor(raw, voucherFilter("tenant:x", "batches", ""))
	voucherTestError(t, e, "invalid_cursor")
	for _, payload := range []string{string(b) + `{}`, `{"v":1,"v":1,"created_at":"` + base.CreatedAt + `","id":"` + base.ID + `","filter":"` + filter + `"}`, strings.Replace(string(b), ".000000Z", "Z", 1), strings.Replace(string(b), `"v":1`, `"v":1.0`, 1)} {
		_, _, e = decodeVoucherCursor(base64.RawURLEncoding.EncodeToString([]byte(payload)), filter)
		voucherTestError(t, e, "invalid_cursor")
	}
}

func TestPostgresVoucherCancelledBudgetDoesNotRetainPoolTransaction(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	ctx, cancel := context.WithCancel(f.ctx)
	e := v.transact(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT 1`); e != nil {
			return e
		}
		cancel()
		return context.Canceled
	})
	voucherTestError(t, e, "commerce_unavailable")
	// puddle destroys failed connections asynchronously; the caller must return
	// promptly, and the resource must then leave the acquired set.
	until := time.Now().Add(time.Second)
	for f.pool.Stat().AcquiredConns() != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if f.pool.Stat().AcquiredConns() != 0 {
		t.Fatal("cancelled transaction retained a pool connection")
	}
	if _, e = f.pool.Exec(f.ctx, `SELECT 1`); e != nil {
		t.Fatal("pool unavailable after cancellation", e)
	}
}
