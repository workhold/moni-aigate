package billing

import (
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/security"
)

func TestPostgresVoucherPagesAreBoundToFilterTenantAndCurrentRights(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	if empty, e := v.ListVoucherBatches(f.ctx, f.operator, VoucherPageInput{}); e != nil || empty.Data == nil || len(empty.Data) != 0 || empty.NextCursor != nil {
		t.Fatal("empty page is not a nonnil safe list", e)
	}
	issued := voucherTestCreate(t, f, v, 51, 1, true)
	for i := 0; i < 51; i++ {
		r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[i].Code))
		if e != nil || r.Status != "accepted" {
			t.Fatal("receipt page setup", e)
		}
	}
	reader := VoucherReader{f.user, f.tenant}
	first, e := v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingVoucherGrant})
	if e != nil || len(first.Data) != 50 || first.NextCursor == nil {
		t.Fatal("first funding page", e)
	}
	second, e := v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingVoucherGrant, Cursor: *first.NextCursor})
	if e != nil || len(second.Data) != 1 || second.NextCursor != nil {
		t.Fatal("second funding page", e)
	}
	seen := map[string]bool{}
	for _, r := range append(first.Data, second.Data...) {
		if seen[r.ID] {
			t.Fatal("paging repeated a receipt")
		}
		seen[r.ID] = true
	}
	_, e = v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingManualCredit, Cursor: *first.NextCursor})
	voucherTestError(t, e, "invalid_cursor")
	other := uuid.NewString()
	if _, e = f.pool.Exec(f.ctx, `INSERT INTO tenants(id,name) VALUES($1,'other voucher tenant')`, other); e != nil {
		t.Fatal(e)
	}
	if _, e = f.pool.Exec(f.ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner')`, other, f.user); e != nil {
		t.Fatal(e)
	}
	_, e = v.ListFundingReceipts(f.ctx, VoucherReader{f.user, other}, FundingReceiptPageInput{SourceKind: FundingVoucherGrant, Cursor: *first.NextCursor})
	voucherTestError(t, e, "invalid_cursor")
	var operation, redemption string
	if e = f.pool.QueryRow(f.ctx, `SELECT operation_id,id FROM voucher_redemptions LIMIT 1`).Scan(&operation, &redemption); e != nil {
		t.Fatal(e)
	}
	_, e = v.GetWalletCommerceOperation(f.ctx, VoucherReader{f.user, other}, operation)
	voucherTestError(t, e, "not_found")
	_, e = v.GetVoucherRedemption(f.ctx, VoucherReader{f.user, other}, redemption)
	voucherTestError(t, e, "not_found")
	_, e = v.GetPlatformCommerceOperation(f.ctx, f.operator, operation)
	voucherTestError(t, e, "not_found")
	if _, e = f.pool.Exec(f.ctx, `UPDATE memberships SET role='billing_viewer' WHERE tenant_id=$1 AND user_id=$2`, f.tenant, f.user); e != nil {
		t.Fatal(e)
	}
	if _, e = v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingVoucherGrant, Cursor: *first.NextCursor}); e != nil {
		t.Fatal("billing viewer cannot read", e)
	}
	request := voucherTestRedeem(f, "mav1."+uuid.NewString()+"."+security.Random(32))
	_, e = v.RedeemVoucher(f.ctx, request)
	voucherTestError(t, e, "forbidden")
	var claims int
	if e = f.pool.QueryRow(f.ctx, `SELECT count(*) FROM commerce_commands WHERE operation_id=$1`, request.Command.OperationID).Scan(&claims); e != nil || claims != 0 {
		t.Fatal("read-only member persisted a refusal claim", e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE tenants SET status='suspended' WHERE id=$1`, f.tenant); e != nil {
		t.Fatal(e)
	}
	_, e = v.ListFundingReceipts(f.ctx, reader, FundingReceiptPageInput{SourceKind: FundingVoucherGrant, Cursor: *first.NextCursor})
	voucherTestError(t, e, "forbidden")
	for i := 0; i < 50; i++ {
		voucherTestCreate(t, f, v, 1, 1, false)
	}
	batches, e := v.ListVoucherBatches(f.ctx, f.operator, VoucherPageInput{})
	if e != nil || len(batches.Data) != 50 || batches.NextCursor == nil {
		t.Fatal("first batch page", e)
	}
	last, e := v.ListVoucherBatches(f.ctx, f.operator, VoucherPageInput{Cursor: *batches.NextCursor})
	if e != nil || len(last.Data) != 1 || last.NextCursor != nil {
		t.Fatal("last batch page", e)
	}
	if _, e = f.pool.Exec(f.ctx, `UPDATE users SET platform_role='none' WHERE id=$1`, f.operator); e != nil {
		t.Fatal(e)
	}
	_, e = v.ListVoucherBatches(f.ctx, f.operator, VoucherPageInput{Cursor: *batches.NextCursor})
	voucherTestError(t, e, "forbidden")
}

func TestPostgresVoucherRevocationPaginationBindsOriginalBatch(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	batch := voucherTestCreate(t, f, v, 1, 1, true)
	for i := 0; i < 51; i++ {
		r, e := v.ProposeVoucherRevocation(f.ctx, ProposeVoucherRevocationInput{Command: voucherTestCommand(f.operator), BatchID: batch.Batch.ID, ExpectedRevision: 2, Reason: "independent proposal"})
		if e != nil || r.Status != "accepted" {
			t.Fatal("proposal page setup", e)
		}
	}
	first, e := v.ListVoucherRevocations(f.ctx, f.approver, batch.Batch.ID, VoucherPageInput{})
	if e != nil || len(first.Data) != 50 || first.NextCursor == nil {
		t.Fatal("revocation first page", e)
	}
	last, e := v.ListVoucherRevocations(f.ctx, f.approver, batch.Batch.ID, VoucherPageInput{Cursor: *first.NextCursor})
	if e != nil || len(last.Data) != 1 || last.NextCursor != nil || last.Data[0].Decision != nil {
		t.Fatal("revocation last page", e)
	}
	other := voucherTestCreate(t, f, v, 1, 1, true)
	_, e = v.ListVoucherRevocations(f.ctx, f.approver, other.Batch.ID, VoucherPageInput{Cursor: *first.NextCursor})
	voucherTestError(t, e, "invalid_cursor")
}

func TestPostgresVoucherUnavailableReceiptsAreUniformAndDoNotPersistSecrets(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	batch := voucherTestCreate(t, f, v, 1, 1, true)
	wrong := "mav1." + batch.Codes[0].VoucherID + "." + security.Random(32)
	unknown := "mav1." + uuid.NewString() + "." + security.Random(32)
	for _, code := range []string{wrong, unknown, batch.Codes[0].Code} {
		r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, code))
		if code == batch.Codes[0].Code {
			if e != nil || r.Status != "accepted" {
				t.Fatal("valid secret", e)
			}
		} else {
			voucherTestRejected(t, r, e, "voucher_unavailable")
		}
	}
	r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, batch.Codes[0].Code))
	voucherTestRejected(t, r, e, "voucher_unavailable")
	var exposed bool
	if e = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE sanitized_diff::text LIKE '%'||$1::text||'%')`, batch.Codes[0].Code).Scan(&exposed); e != nil || exposed {
		t.Fatal("voucher secret reached durable audit", e)
	}
	voucherTestFunding(t, f)
}

func TestPostgresVoucherFundingInfrastructureAndCommitFailureRollbackAllFacts(t *testing.T) {
	for _, table := range []string{"audit_events", "projection_outbox", "funding_receipts", "commerce_operation_receipts", "deferred_terminal"} {
		t.Run(table, func(t *testing.T) {
			f := fundingFixture(t)
			v := voucherTestService(t, f)
			issued := voucherTestCreate(t, f, v, 1, 8, true)
			before := fundingSnapshot(t, f)
			if _, e := f.pool.Exec(f.ctx, `CREATE FUNCTION voucher_failure_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private fixture detail'; END $$`); e != nil {
				t.Fatal(e)
			}
			query := "CREATE TRIGGER voucher_failure_fixture BEFORE INSERT ON " + pgx.Identifier{table}.Sanitize() + " FOR EACH ROW EXECUTE FUNCTION voucher_failure_fixture()"
			if table == "deferred_terminal" {
				query = `CREATE CONSTRAINT TRIGGER voucher_failure_fixture AFTER INSERT ON commerce_operation_receipts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION voucher_failure_fixture()`
			}
			if _, e := f.pool.Exec(f.ctx, query); e != nil {
				t.Fatal(e)
			}
			_, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code))
			voucherTestError(t, e, "commerce_unavailable")
			if fundingSnapshot(t, f) != before {
				t.Fatal("failed command retained funding, redemption, audit, command or terminal")
			}
		})
	}
}

func TestPostgresVoucherFundingSummaryUsesNumericBeyondMoneyRange(t *testing.T) {
	f := fundingFixture(t)
	v := voucherTestService(t, f)
	openingBoundaryTenant(t, f)
	r := f.reserve(t)
	f.final(t, r, confirmed(), "completed", "done", "none", false)
	if s, e := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); e != nil || s.ChargedAmount != 15 {
		t.Fatal("summary spend setup", e)
	}
	issued := voucherTestCreate(t, f, v, 1, 15, true)
	if r, e := v.RedeemVoucher(f.ctx, voucherTestRedeem(f, issued.Codes[0].Code)); e != nil || r.Status != "accepted" {
		t.Fatal("summary grant", e)
	}
	summary, e := v.GetFundingSummary(f.ctx, VoucherReader{f.user, f.tenant})
	if e != nil || summary.TotalAmount != "9223372036854775822" || summary.ManualCreditAmount != "0" || summary.ManualBonusAmount != "0" || summary.ManualUnknownAmount != "0" || summary.WalletOpeningAmount != "9223372036854775807" || summary.VoucherGrantAmount != "15" {
		t.Fatal("NUMERIC summary truncated or wrapped", e)
	}
	voucherTestFunding(t, f)
}
