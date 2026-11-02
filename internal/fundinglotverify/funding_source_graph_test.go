package fundinglotverify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fundingGraphTestID(n int) string      { return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, n) }
func fundingGraphTestPtr(s string) *string { return &s }
func fundingGraphTestTime() *time.Time {
	t := time.Date(2026, 11, 2, 12, 0, 0, 123456000, time.UTC)
	return &t
}
func fundingGraphTestManual() FundingSourceGraphRows {
	now := *fundingGraphTestTime()
	tenant, maker, checker := fundingGraphTestID(1), fundingGraphTestID(2), fundingGraphTestID(3)
	credit, journal, receipt := "legacy-credit", "legacy-journal", fundingGraphTestID(4)
	return FundingSourceGraphRows{
		Users: []ProcurementApprovalUserRow{{maker}, {checker}}, Tenants: []ProcurementAttemptTenantRow{{tenant}},
		Accounts: []LedgerAccountRow{{"available", &tenant, "AVAILABLE", "0"}, {"manual-clearing", nil, "FUNDING_CLEARING", "0"}},
		Journals: []FundingGraphJournalRow{{ID: journal, TenantID: tenant, OperationID: credit, Kind: "credit", Reference: credit, Actor: checker, Currency: "CNY", CreatedTXID: "101", CreatedAt: now.Add(-time.Hour)}},
		Lines:    []LedgerLineRow{{"manual-plus", journal, "available", "53"}, {"manual-minus", journal, "manual-clearing", "-53"}},
		Receipts: []FundingGraphReceiptRow{{receipt, tenant, "manual_credit", credit, "53", "CNY", journal, now}},
		Credits:  []FundingGraphCreditRow{{credit, tenant, "business:manual", "53", "approved", maker, "proof:manual", &checker, &journal, now.Add(time.Hour)}},
		Audits:   []ProcurementAuditRow{{ID: "manual-created", Actor: maker, Action: "credit.created", Target: credit, Diff: `{"amount":"53"}`, TenantScope: &tenant, CreatedAt: now.Add(-2 * time.Hour)}, {ID: "manual-approved", Actor: checker, Action: "credit.approved", Target: credit, Diff: `{"amount":"53"}`, TenantScope: &tenant, CreatedAt: now.Add(-3 * time.Hour)}},
		Outbox:   []ProcurementOutboxRow{{ID: "manual-outbox", TenantID: tenant, OperationID: credit, Kind: "credited", Payload: `{"amount":"53"}`, SchemaVersion: 1, CreatedAt: now.Add(-4 * time.Hour)}},
	}
}
func fundingGraphTestFull() FundingSourceGraphRows {
	r := fundingGraphTestManual()
	now := *fundingGraphTestTime()
	tenant, maker, checker := r.Tenants[0].ID, r.Users[0].ID, r.Users[1].ID
	owner := fundingGraphTestID(5)
	r.Users = append(r.Users, ProcurementApprovalUserRow{owner})
	batch, voucher, unused, redemption, receipt := fundingGraphTestID(10), fundingGraphTestID(11), fundingGraphTestID(12), fundingGraphTestID(13), fundingGraphTestID(14)
	create, approve, redeem := fundingGraphTestID(20), fundingGraphTestID(21), fundingGraphTestID(22)
	activated := now.Add(-time.Minute)
	journal := "voucher-journal"
	version := "v1"
	r.Accounts = append(r.Accounts, LedgerAccountRow{ID: "voucher-clearing", Kind: "OPERATING_GRANT_CLEARING", Balance: "0"})
	r.Journals = append(r.Journals, FundingGraphJournalRow{ID: journal, TenantID: tenant, OperationID: redemption, Kind: "voucher_grant", Reference: voucher, Actor: owner, Currency: "CNY", CreatedTXID: "102", CreatedAt: now.Add(-24 * time.Hour)})
	r.Lines = append(r.Lines, LedgerLineRow{"voucher-plus", journal, "available", "17"}, LedgerLineRow{"voucher-minus", journal, "voucher-clearing", "-17"})
	r.Receipts = append(r.Receipts, FundingGraphReceiptRow{receipt, tenant, "voucher_grant", voucher, "17", "CNY", journal, now})
	r.Redemptions = []FundingGraphRedemptionRow{{redemption, tenant, voucher, batch, redeem, owner, "17", journal, receipt, now}}
	r.Batches = []FundingGraphBatchRow{{ID: batch, State: "active", FaceAmount: "17", Count: "2", PurposeRef: "grant:test", Reason: "test grant", Maker: maker, Revision: "2", RedeemedCount: "1", Approver: &checker, ExpiresAt: now.Add(2 * time.Hour), CreatedAt: now.Add(-2 * time.Hour), ActivatedAt: &activated}}
	r.Vouchers = []FundingGraphVoucherRow{{voucher, batch, now.Add(-3 * time.Hour)}, {unused, batch, now.Add(-4 * time.Hour)}}
	r.Commands = []FundingGraphCommerceCommandRow{{create, maker, "platform", "voucher.batch.create", strings.Repeat("a", 64), nil, now.Add(3 * time.Hour)}, {approve, checker, "platform", "voucher.batch.approve", strings.Repeat("b", 64), nil, now.Add(2 * time.Hour)}, {redeem, owner, "tenant:" + tenant, "voucher.redeem", strings.Repeat("c", 64), &version, now.Add(time.Hour)}}
	r.CommerceReceipts = []FundingGraphCommerceReceiptRow{{OperationID: create, Kind: "voucher.batch.create", Scope: "platform", Actor: maker, Status: "accepted", Reason: "accepted", ResourceID: &batch, CreatedAt: r.Commands[0].CreatedAt}, {OperationID: approve, Kind: "voucher.batch.approve", Scope: "platform", Actor: checker, Status: "accepted", Reason: "accepted", ResourceID: &batch, CreatedAt: r.Commands[1].CreatedAt}, {OperationID: redeem, Kind: "voucher.redeem", Scope: "tenant:" + tenant, Actor: owner, Status: "accepted", Reason: "accepted", ResourceID: &redemption, Amount: fundingGraphTestPtr("17"), FundingReceiptID: &receipt, JournalID: &journal, CreatedAt: r.Commands[2].CreatedAt}}
	empty := ""
	r.Audits = append(r.Audits, ProcurementAuditRow{ID: "batch-created", Actor: maker, Action: "voucher.batch.created", Target: batch, Diff: `{"face_amount":"17","count":2,"purpose_ref":"grant:test"}`, TenantScope: &empty, CreatedAt: now}, ProcurementAuditRow{ID: "batch-approved", Actor: checker, Action: "voucher.batch.approved", Target: batch, Diff: `{"maker":"` + maker + `"}`, TenantScope: &empty, CreatedAt: now}, ProcurementAuditRow{ID: "redeemed", Actor: owner, Action: "voucher.redeemed", Target: redemption, Diff: `{"amount":"17","funding_receipt_id":"` + receipt + `"}`, TenantScope: &tenant, RequestID: "voucher:request", CreatedAt: now})
	r.Outbox = append(r.Outbox, ProcurementOutboxRow{ID: "voucher-outbox", TenantID: tenant, OperationID: redemption, Kind: "credited", Payload: `{"amount":"17"}`, SchemaVersion: 1, CreatedAt: now.Add(-time.Hour)})
	return r
}
func fundingGraphTestNoRedemption(r *FundingSourceGraphRows) {
	r.Journals = r.Journals[:1]
	r.Lines = r.Lines[:2]
	r.Receipts = r.Receipts[:1]
	r.Redemptions = nil
	r.Batches[0].RedeemedCount = "0"
	r.Audits = r.Audits[:4]
	r.Outbox = r.Outbox[:1]
	r.Commands = r.Commands[:2]
	r.CommerceReceipts = r.CommerceReceipts[:2]
}
func fundingGraphTestProposal(r *FundingSourceGraphRows, n int, decision string) {
	now := *fundingGraphTestTime()
	p, op, actor, batch := fundingGraphTestID(30+n), fundingGraphTestID(40+n), r.Users[0].ID, r.Batches[0].ID
	empty := ""
	r.Revocations = append(r.Revocations, FundingGraphRevocationRow{p, batch, op, actor, "2", r.Batches[0].RedeemedCount, "revoke test", now})
	r.Commands = append(r.Commands, FundingGraphCommerceCommandRow{op, actor, "platform", "voucher.revocation.create", strings.Repeat("d", 64), nil, now})
	r.CommerceReceipts = append(r.CommerceReceipts, FundingGraphCommerceReceiptRow{OperationID: op, Kind: "voucher.revocation.create", Scope: "platform", Actor: actor, Status: "accepted", Reason: "accepted", ResourceID: &p, CreatedAt: now})
	r.Audits = append(r.Audits, ProcurementAuditRow{ID: fmt.Sprintf("proposal-%d", n), Actor: actor, TenantScope: &empty, Action: "voucher.revocation.created", Target: p, Diff: `{"batch_id":"` + batch + `","reason":"revoke test"}`, CreatedAt: now})
	if decision == "" {
		return
	}
	did, dop, checker := fundingGraphTestID(50+n), fundingGraphTestID(60+n), r.Users[1].ID
	reason := "accepted"
	if decision == "rejected" {
		reason = "voucher_evidence_stale"
		r.Revocations[len(r.Revocations)-1].ExpectedRedeemedCount = "0"
	}
	r.Decisions = append(r.Decisions, FundingGraphRevocationDecisionRow{did, p, batch, dop, checker, decision, reason, now.Add(-24 * time.Hour)})
	r.Commands = append(r.Commands, FundingGraphCommerceCommandRow{dop, checker, "platform", "voucher.revocation.approve", strings.Repeat("e", 64), nil, now})
	t := FundingGraphCommerceReceiptRow{OperationID: dop, Kind: "voucher.revocation.approve", Scope: "platform", Actor: checker, Status: decision, Reason: reason, CreatedAt: now}
	if decision == "accepted" {
		t.ResourceID = &p
		r.Batches[0].State = "revoked"
		r.Batches[0].Revision = "3"
		r.Batches[0].TerminalActor = &checker
		backward := now.Add(-time.Hour)
		r.Batches[0].TerminalAt = &backward
		r.Audits = append(r.Audits, ProcurementAuditRow{ID: fmt.Sprintf("decision-%d", n), Actor: checker, TenantScope: &empty, Action: "voucher.revocation.approved", Target: p, Diff: `{"batch_id":"` + batch + `"}`, CreatedAt: now})
	}
	r.CommerceReceipts = append(r.CommerceReceipts, t)
}
func fundingGraphTestCheck(t *testing.T, r FundingSourceGraphRows) FundingSourceGraphCheck {
	t.Helper()
	c, e := CheckFundingSourceGraph(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func fundingGraphTestGood(t *testing.T, r FundingSourceGraphRows) {
	t.Helper()
	c := fundingGraphTestCheck(t, r)
	if !c.ScopedGraphConsistent || c.Scope != "manual_voucher_funding_graph_v1" {
		t.Fatalf("unexpected graph: %+v", c)
	}
}

func TestCheckFundingSourceGraphValid(t *testing.T) {
	t.Run("empty", func(t *testing.T) { fundingGraphTestGood(t, FundingSourceGraphRows{}) })
	t.Run("manual and voucher", func(t *testing.T) { fundingGraphTestGood(t, fundingGraphTestFull()) })
	t.Run("manual pending", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Credits[0].State = "pending"
		r.Credits[0].Approver = nil
		r.Credits[0].JournalID = nil
		r.Receipts = nil
		r.Journals = nil
		r.Lines = nil
		r.Outbox = nil
		r.Audits = r.Audits[:1]
		fundingGraphTestGood(t, r)
	})
	t.Run("legacy text identities and nonmonotonic times", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Users[0].ID = "old:maker"
		r.Users[1].ID = "old:checker"
		r.Tenants[0].ID = "old:tenant"
		r.Credits[0].Requester = r.Users[0].ID
		r.Credits[0].Approver = &r.Users[1].ID
		r.Credits[0].TenantID = r.Tenants[0].ID
		r.Accounts[0].TenantID = &r.Tenants[0].ID
		r.Journals[0].TenantID = r.Tenants[0].ID
		r.Journals[0].Actor = r.Users[1].ID
		r.Receipts[0].TenantID = r.Tenants[0].ID
		for i := range r.Audits {
			r.Audits[i].TenantScope = &r.Tenants[0].ID
			r.Audits[i].Actor = r.Users[i].ID
		}
		r.Outbox[0].TenantID = r.Tenants[0].ID
		fundingGraphTestGood(t, r)
	})
	t.Run("proposed batch", func(t *testing.T) {
		r := fundingGraphTestFull()
		fundingGraphTestNoRedemption(&r)
		r.Batches[0].State = "proposed"
		r.Batches[0].Revision = "1"
		r.Batches[0].Approver = nil
		r.Batches[0].ActivatedAt = nil
		r.Commands = r.Commands[:1]
		r.CommerceReceipts = r.CommerceReceipts[:1]
		r.Audits = r.Audits[:3]
		fundingGraphTestGood(t, r)
	})
	t.Run("voided batch", func(t *testing.T) {
		r := fundingGraphTestFull()
		fundingGraphTestNoRedemption(&r)
		b := &r.Batches[0]
		b.State = "voided"
		b.Approver = nil
		b.ActivatedAt = nil
		b.TerminalActor = &b.Maker
		b.TerminalAt = fundingGraphTestTime()
		r.Commands[1].Kind = "voucher.batch.void"
		r.Commands[1].Actor = b.Maker
		r.CommerceReceipts[1].Kind = "voucher.batch.void"
		r.CommerceReceipts[1].Actor = b.Maker
		r.Audits[3].Action = "voucher.batch.voided"
		r.Audits[3].Actor = b.Maker
		r.Audits[3].Diff = `{"reason":"abandoned batch"}`
		fundingGraphTestGood(t, r)
	})
	for _, status := range []string{"", "accepted", "rejected"} {
		t.Run("revocation "+status, func(t *testing.T) {
			r := fundingGraphTestFull()
			fundingGraphTestProposal(&r, 0, status)
			fundingGraphTestGood(t, r)
		})
	}
	t.Run("many historical proposals", func(t *testing.T) {
		r := fundingGraphTestFull()
		fundingGraphTestProposal(&r, 1, "")
		r.Revocations[0].ExpectedRedeemedCount = "0"
		fundingGraphTestProposal(&r, 2, "accepted")
		fundingGraphTestGood(t, r)
	})
	t.Run("money max int64", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Credits[0].Amount = "9223372036854775807"
		r.Receipts[0].Amount = r.Credits[0].Amount
		r.Lines[0].SignedDelta = r.Credits[0].Amount
		r.Lines[1].SignedDelta = "-" + r.Credits[0].Amount
		for i := range r.Audits {
			r.Audits[i].Diff = `{"amount":"9223372036854775807"}`
		}
		r.Outbox[0].Payload = r.Audits[0].Diff
		fundingGraphTestGood(t, r)
	})
}

func TestCheckFundingSourceGraphSourceAndReverseFailures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*FundingSourceGraphRows)
		counter func(FundingSourceGraphMismatches) uint64
	}{
		{"pending effects", func(r *FundingSourceGraphRows) { r.Credits[0].State = "pending" }, func(m FundingSourceGraphMismatches) uint64 { return m.CreditShapeRows }},
		{"self approval", func(r *FundingSourceGraphRows) { r.Credits[0].Approver = &r.Credits[0].Requester }, func(m FundingSourceGraphMismatches) uint64 { return m.CreditShapeRows }},
		{"missing tenant", func(r *FundingSourceGraphRows) { r.Tenants = nil }, func(m FundingSourceGraphMismatches) uint64 { return m.IdentityRows }},
		{"missing user", func(r *FundingSourceGraphRows) { r.Users = r.Users[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.IdentityRows }},
		{"missing receipt", func(r *FundingSourceGraphRows) { r.Receipts = r.Receipts[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.CreditReceiptRows }},
		{"receipt amount", func(r *FundingSourceGraphRows) { r.Receipts[0].Amount = "52" }, func(m FundingSourceGraphMismatches) uint64 { return m.ReceiptSourceRows }},
		{"receipt source", func(r *FundingSourceGraphRows) { r.Receipts[0].SourceID = "orphan" }, func(m FundingSourceGraphMismatches) uint64 { return m.ReceiptSourceRows }},
		{"receipt currency", func(r *FundingSourceGraphRows) { r.Receipts[0].Currency = "USD" }, func(m FundingSourceGraphMismatches) uint64 { return m.InvalidEnumRows }},
		{"unknown receipt kind", func(r *FundingSourceGraphRows) { r.Receipts[0].SourceKind = "other" }, func(m FundingSourceGraphMismatches) uint64 { return m.UnknownReceiptKindRows }},
		{"journal actor", func(r *FundingSourceGraphRows) { r.Journals[0].Actor = r.Users[0].ID }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingJournalRows }},
		{"journal reference", func(r *FundingSourceGraphRows) { r.Journals[0].Reference = "another" }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingJournalSourceRows }},
		{"journal operation", func(r *FundingSourceGraphRows) { r.Journals[1].OperationID = r.Redemptions[0].OperationID }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingJournalRows }},
		{"journal tenant", func(r *FundingSourceGraphRows) { r.Journals[0].TenantID = "another" }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingJournalRows }},
		{"journal orphan", func(r *FundingSourceGraphRows) {
			j := r.Journals[0]
			j.ID = "orphan"
			j.OperationID = "orphan"
			r.Journals = append(r.Journals, j)
		}, func(m FundingSourceGraphMismatches) uint64 { return m.FundingJournalSourceRows }},
		{"third zero line", func(r *FundingSourceGraphRows) {
			r.Lines = append(r.Lines, LedgerLineRow{"extra", r.Journals[0].ID, "manual-clearing", "0"})
		}, func(m FundingSourceGraphMismatches) uint64 { return m.FundingLineRows }},
		{"canceling extra lines", func(r *FundingSourceGraphRows) {
			r.Lines = append(r.Lines, LedgerLineRow{"extra-plus", r.Journals[0].ID, "available", "1"}, LedgerLineRow{"extra-minus", r.Journals[0].ID, "manual-clearing", "-1"})
		}, func(m FundingSourceGraphMismatches) uint64 { return m.FundingLineRows }},
		{"cross tenant line", func(r *FundingSourceGraphRows) { other := "other"; r.Accounts[0].TenantID = &other }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingLineRows }},
		{"tenant clearing account", func(r *FundingSourceGraphRows) { r.Accounts[1].TenantID = &r.Tenants[0].ID }, func(m FundingSourceGraphMismatches) uint64 { return m.ClearingLineSourceRows }},
		{"clearing used by other journal", func(r *FundingSourceGraphRows) { r.Journals[0].Kind = "settled" }, func(m FundingSourceGraphMismatches) uint64 { return m.ClearingLineSourceRows }},
		{"missing account", func(r *FundingSourceGraphRows) { r.Accounts = r.Accounts[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingLineRows }},
		{"outbox missing", func(r *FundingSourceGraphRows) { r.Outbox = r.Outbox[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingOutboxRows }},
		{"outbox wrong tenant", func(r *FundingSourceGraphRows) { r.Outbox[0].TenantID = "other" }, func(m FundingSourceGraphMismatches) uint64 { return m.OutboxSourceRows }},
		{"outbox orphan", func(r *FundingSourceGraphRows) { r.Outbox[0].OperationID = "orphan" }, func(m FundingSourceGraphMismatches) uint64 { return m.OutboxSourceRows }},
		{"outbox wrong schema", func(r *FundingSourceGraphRows) { r.Outbox[0].SchemaVersion = 2 }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingOutboxRows }},
		{"audit missing", func(r *FundingSourceGraphRows) { r.Audits = r.Audits[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingAuditRows }},
		{"audit actor", func(r *FundingSourceGraphRows) { r.Audits[1].Actor = r.Users[0].ID }, func(m FundingSourceGraphMismatches) uint64 { return m.AuditSourceRows }},
		{"manual audit request", func(r *FundingSourceGraphRows) { r.Audits[0].RequestID = "unexpected" }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingAuditRows }},
		{"platform null audit scope", func(r *FundingSourceGraphRows) { r.Audits[2].TenantScope = nil }, func(m FundingSourceGraphMismatches) uint64 { return m.FundingAuditRows }},
		{"audit orphan", func(r *FundingSourceGraphRows) { r.Audits[0].Target = "orphan" }, func(m FundingSourceGraphMismatches) uint64 { return m.AuditSourceRows }},
		{"A0 amount-only redeemed fixture", func(r *FundingSourceGraphRows) { r.Audits[4].Diff = `{"amount":"17"}` }, func(m FundingSourceGraphMismatches) uint64 { return m.InvalidJSONRows }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fundingGraphTestFull()
			tc.mutate(&r)
			c := fundingGraphTestCheck(t, r)
			if c.ScopedGraphConsistent || tc.counter(c.Mismatches) == 0 {
				t.Fatalf("missing counter: %+v", c)
			}
		})
	}
}

func TestCheckFundingSourceGraphVoucherCommerceFailures(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*FundingSourceGraphRows)
		counter func(FundingSourceGraphMismatches) uint64
	}{
		{"missing batch", func(r *FundingSourceGraphRows) { r.Batches = nil }, func(m FundingSourceGraphMismatches) uint64 { return m.VoucherBatchRows }},
		{"missing voucher", func(r *FundingSourceGraphRows) { r.Vouchers = r.Vouchers[1:] }, func(m FundingSourceGraphMismatches) uint64 { return m.BatchVoucherCountRows }},
		{"wrong batch count", func(r *FundingSourceGraphRows) { r.Batches[0].RedeemedCount = "0" }, func(m FundingSourceGraphMismatches) uint64 { return m.BatchRedemptionCountRows }},
		{"wrong batch revision", func(r *FundingSourceGraphRows) { r.Batches[0].Revision = "3" }, func(m FundingSourceGraphMismatches) uint64 { return m.BatchShapeRows }},
		{"overflow batch", func(r *FundingSourceGraphRows) { r.Batches[0].FaceAmount = "9223372036854775807" }, func(m FundingSourceGraphMismatches) uint64 { return m.InvalidNumberRows }},
		{"initial expiry too short", func(r *FundingSourceGraphRows) {
			r.Batches[0].ExpiresAt = r.Batches[0].CreatedAt.Add(time.Hour - time.Microsecond)
		}, func(m FundingSourceGraphMismatches) uint64 { return m.BatchShapeRows }},
		{"redemption amount", func(r *FundingSourceGraphRows) { r.Redemptions[0].Amount = "18" }, func(m FundingSourceGraphMismatches) uint64 { return m.RedemptionEligibilityRows }},
		{"redemption batch", func(r *FundingSourceGraphRows) { r.Redemptions[0].BatchID = fundingGraphTestID(999) }, func(m FundingSourceGraphMismatches) uint64 { return m.IdentityRows }},
		{"funding now differs", func(r *FundingSourceGraphRows) {
			r.Redemptions[0].CreatedAt = r.Redemptions[0].CreatedAt.Add(time.Microsecond)
		}, func(m FundingSourceGraphMismatches) uint64 { return m.RedemptionEligibilityRows }},
		{"before activation", func(r *FundingSourceGraphRows) {
			after := r.Redemptions[0].CreatedAt.Add(time.Microsecond)
			r.Batches[0].ActivatedAt = &after
		}, func(m FundingSourceGraphMismatches) uint64 { return m.RedemptionEligibilityRows }},
		{"expired redemption", func(r *FundingSourceGraphRows) { r.Batches[0].ExpiresAt = r.Redemptions[0].CreatedAt }, func(m FundingSourceGraphMismatches) uint64 { return m.RedemptionEligibilityRows }},
		{"missing command", func(r *FundingSourceGraphRows) { r.Commands = r.Commands[:2] }, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceTerminalRows }},
		{"missing terminal", func(r *FundingSourceGraphRows) { r.CommerceReceipts = r.CommerceReceipts[:2] }, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceTerminalRows }},
		{"wrong terminal resource", func(r *FundingSourceGraphRows) {
			r.CommerceReceipts[2].ResourceID = fundingGraphTestPtr(fundingGraphTestID(99))
		}, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceSourceRows }},
		{"wrong terminal now", func(r *FundingSourceGraphRows) {
			r.CommerceReceipts[2].CreatedAt = r.CommerceReceipts[2].CreatedAt.Add(time.Microsecond)
		}, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceTerminalRows }},
		{"redeem fingerprint null", func(r *FundingSourceGraphRows) { r.Commands[2].FingerprintKeyVersion = nil }, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceShapeRows }},
		{"voucher wrong scope", func(r *FundingSourceGraphRows) { r.Commands[2].Scope = "platform" }, func(m FundingSourceGraphMismatches) uint64 { return m.CommerceShapeRows }},
		{"voucher command unknown kind", func(r *FundingSourceGraphRows) { r.Commands[2].Kind = "voucher.unknown" }, func(m FundingSourceGraphMismatches) uint64 { return m.InvalidEnumRows }},
		{"forged stale decision without terminal", func(r *FundingSourceGraphRows) {
			fundingGraphTestProposal(r, 0, "rejected")
			r.CommerceReceipts = r.CommerceReceipts[:len(r.CommerceReceipts)-1]
		}, func(m FundingSourceGraphMismatches) uint64 { return m.DecisionRows }},
		{"self revoke decision", func(r *FundingSourceGraphRows) {
			fundingGraphTestProposal(r, 0, "accepted")
			r.Decisions[0].Approver = r.Revocations[0].Maker
		}, func(m FundingSourceGraphMismatches) uint64 { return m.DecisionRows }},
		{"stale expected counter accepted", func(r *FundingSourceGraphRows) {
			fundingGraphTestProposal(r, 0, "accepted")
			r.Revocations[0].ExpectedRedeemedCount = "0"
		}, func(m FundingSourceGraphMismatches) uint64 { return m.DecisionRows }},
		{"unknown proposal", func(r *FundingSourceGraphRows) { fundingGraphTestProposal(r, 0, "accepted"); r.Revocations = nil }, func(m FundingSourceGraphMismatches) uint64 { return m.DecisionRows }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := fundingGraphTestFull()
			tc.mutate(&r)
			c := fundingGraphTestCheck(t, r)
			if c.ScopedGraphConsistent || tc.counter(c.Mismatches) == 0 {
				t.Fatalf("missing counter: %+v", c)
			}
		})
	}
}

func TestCheckFundingSourceGraphRejectedCommandMatrix(t *testing.T) {
	rows := []struct {
		kind    string
		reasons []string
	}{
		{"voucher.batch.create", []string{"vouchers_disabled", "voucher_state_conflict"}},
		{"voucher.batch.approve", []string{"voucher_revision_conflict", "voucher_state_conflict"}},
		{"voucher.batch.void", []string{"voucher_revision_conflict", "voucher_state_conflict"}},
		{"voucher.revocation.create", []string{"voucher_revision_conflict", "voucher_state_conflict", "voucher_evidence_stale"}},
		{"voucher.revocation.approve", []string{"voucher_state_conflict"}},
		{"voucher.redeem", []string{"vouchers_disabled", "voucher_unavailable", "funding_amount_overflow"}},
	}
	for _, row := range rows {
		for _, reason := range row.reasons {
			t.Run(row.kind+"/"+reason, func(t *testing.T) {
				actor, tenant, operation := fundingGraphTestID(1), fundingGraphTestID(2), fundingGraphTestID(3)
				scope := "platform"
				var version *string
				if row.kind == "voucher.redeem" {
					scope = "tenant:" + tenant
					version = fundingGraphTestPtr("v1")
				}
				r := FundingSourceGraphRows{Users: []ProcurementApprovalUserRow{{actor}}, Tenants: []ProcurementAttemptTenantRow{{tenant}}, Commands: []FundingGraphCommerceCommandRow{{operation, actor, scope, row.kind, strings.Repeat("a", 64), version, *fundingGraphTestTime()}}, CommerceReceipts: []FundingGraphCommerceReceiptRow{{OperationID: operation, Kind: row.kind, Scope: scope, Actor: actor, Status: "rejected", Reason: reason, CreatedAt: *fundingGraphTestTime()}}}
				fundingGraphTestGood(t, r)
				r.CommerceReceipts[0].Reason = "accepted"
				if fundingGraphTestCheck(t, r).Mismatches.CommerceShapeRows != 1 {
					t.Fatal("invalid rejected reason accepted")
				}
			})
		}
	}
}

func TestCheckFundingSourceGraphJSONAndShapes(t *testing.T) {
	for _, raw := range []string{`{"amount":53}`, `{"amount":null}`, `{"amount":"053"}`, `{"amount":"53","amount":"53"}`, `{"amount":"53","\u0061mount":"53"}`, `{"amount":"53","extra":"x"}`, `{"amount":"53"} {}`, `{"amount":"\ud800"}`, `{"amount":true}`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			r := fundingGraphTestManual()
			r.Audits[0].Diff = raw
			c := fundingGraphTestCheck(t, r)
			if c.Mismatches.InvalidJSONRows != 1 || c.ScopedGraphConsistent {
				t.Fatalf("bad JSON accepted: %+v", c)
			}
		})
	}
	t.Run("valid escapes", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Audits[0].Diff = " { \"\\u0061mount\":\"\\u0035\\u0033\" } "
		fundingGraphTestGood(t, r)
	})
	t.Run("canonical but incorrect amount", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Audits[0].Diff = `{"amount":"54"}`
		c := fundingGraphTestCheck(t, r)
		if c.Mismatches.InvalidJSONRows != 0 || c.Mismatches.FundingAuditRows != 1 {
			t.Fatalf("value mismatch confused with JSON: %+v", c)
		}
	})
	t.Run("count must JSON integer", func(t *testing.T) {
		r := fundingGraphTestFull()
		r.Audits[2].Diff = `{"face_amount":"17","count":"2","purpose_ref":"grant:test"}`
		c := fundingGraphTestCheck(t, r)
		if c.Mismatches.InvalidJSONRows != 1 {
			t.Fatal(c)
		}
	})
	t.Run("count exponent rejected", func(t *testing.T) {
		r := fundingGraphTestFull()
		r.Audits[2].Diff = `{"face_amount":"17","count":2e0,"purpose_ref":"grant:test"}`
		if fundingGraphTestCheck(t, r).Mismatches.InvalidJSONRows != 1 {
			t.Fatal("number exponent accepted")
		}
	})
	for _, amount := range []string{"0", "-1", "+53", "053", "53.0", "9223372036854775808"} {
		t.Run("amount "+amount, func(t *testing.T) {
			r := fundingGraphTestManual()
			r.Credits[0].Amount = amount
			if fundingGraphTestCheck(t, r).Mismatches.InvalidNumberRows == 0 {
				t.Fatal("invalid money accepted")
			}
		})
	}
	t.Run("invalid time precision", func(t *testing.T) {
		r := fundingGraphTestFull()
		r.Redemptions[0].CreatedAt = r.Redemptions[0].CreatedAt.Add(time.Nanosecond)
		if fundingGraphTestCheck(t, r).Mismatches.InvalidTimeRows != 1 {
			t.Fatal("submicro timestamp accepted")
		}
	})
	t.Run("invalid related outbox time", func(t *testing.T) {
		r := fundingGraphTestFull()
		r.Outbox[0].CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		if fundingGraphTestCheck(t, r).Mismatches.InvalidTimeRows != 1 {
			t.Fatal("outbox timestamp not checked exactly once")
		}
	})
	t.Run("invalid text once per raw row", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Credits[0].BusinessRef = "\x00"
		r.Credits[0].ProofRef = "\xff"
		if fundingGraphTestCheck(t, r).Mismatches.InvalidTextRows != 1 {
			t.Fatal("text row count inflated")
		}
	})
	t.Run("zero UUID is not a new source ID", func(t *testing.T) {
		r := fundingGraphTestManual()
		r.Receipts[0].ID = "00000000-0000-0000-0000-000000000000"
		if fundingGraphTestCheck(t, r).Mismatches.InvalidTextRows != 1 {
			t.Fatal("all-zero receipt UUID accepted")
		}
	})
}

func TestCheckFundingSourceGraphDuplicates(t *testing.T) {
	t.Run("primary first remains authoritative", func(t *testing.T) {
		r := fundingGraphTestFull()
		c := r.Credits[0]
		c.Amount = "54"
		r.Credits = append(r.Credits, c)
		check := fundingGraphTestCheck(t, r)
		if check.Mismatches.DuplicatePrimaryRows != 1 || check.Mismatches.CreditReceiptRows != 0 || check.Scanned.Credits != 2 {
			t.Fatalf("duplicate replaced first: %+v", check)
		}
	})
	t.Run("outbox effect extra row", func(t *testing.T) {
		r := fundingGraphTestFull()
		o := r.Outbox[0]
		o.ID = "second"
		r.Outbox = append(r.Outbox, o)
		if fundingGraphTestCheck(t, r).Mismatches.DuplicateOutboxEffectRows != 1 {
			t.Fatal("duplicate effect missed")
		}
	})
	t.Run("audit effect extra row", func(t *testing.T) {
		r := fundingGraphTestFull()
		a := r.Audits[0]
		a.ID = "second"
		r.Audits = append(r.Audits, a)
		if fundingGraphTestCheck(t, r).Mismatches.DuplicateAuditEffectRows != 1 {
			t.Fatal("duplicate effect missed")
		}
	})
	t.Run("receipt source and journal", func(t *testing.T) {
		r := fundingGraphTestFull()
		f := r.Receipts[0]
		f.ID = fundingGraphTestID(99)
		r.Receipts = append(r.Receipts, f)
		m := fundingGraphTestCheck(t, r).Mismatches
		if m.DuplicateReceiptSourceRows != 1 || m.DuplicateReceiptJournalRows != 1 {
			t.Fatal(m)
		}
	})
	t.Run("redemption natural keys", func(t *testing.T) {
		r := fundingGraphTestFull()
		d := r.Redemptions[0]
		d.ID = fundingGraphTestID(99)
		r.Redemptions = append(r.Redemptions, d)
		m := fundingGraphTestCheck(t, r).Mismatches
		if m.DuplicateRedemptionVoucherRows != 1 || m.DuplicateRedemptionOperationRows != 1 || m.DuplicateRedemptionJournalRows != 1 || m.DuplicateRedemptionReceiptRows != 1 {
			t.Fatal(m)
		}
	})
	t.Run("voucher accepted resource", func(t *testing.T) {
		r := fundingGraphTestFull()
		c := r.Commands[0]
		c.OperationID = fundingGraphTestID(99)
		r.Commands = append(r.Commands, c)
		p := r.CommerceReceipts[0]
		p.OperationID = c.OperationID
		r.CommerceReceipts = append(r.CommerceReceipts, p)
		if fundingGraphTestCheck(t, r).Mismatches.DuplicateAcceptedResourceRows != 1 {
			t.Fatal("accepted resource duplicate missed")
		}
	})
}

func TestCheckFundingSourceGraphOtherDomains(t *testing.T) {
	t.Run("four payment kinds and repeated resource", func(t *testing.T) {
		r := fundingGraphTestFull()
		for i, kind := range []string{"payment.sku.publish", "payment.sku.activate", "payment.order.create", "payment.order.cancel", "payment.order.cancel"} {
			op := fundingGraphTestID(100 + i)
			actor := r.Users[0].ID
			scope := "platform"
			if strings.HasPrefix(kind, "payment.order.") {
				scope = "tenant:" + r.Tenants[0].ID
			}
			r.Commands = append(r.Commands, FundingGraphCommerceCommandRow{OperationID: op, Actor: actor, Scope: scope, Kind: kind, RequestDigest: strings.Repeat("a", 64), CreatedAt: *fundingGraphTestTime()})
			r.CommerceReceipts = append(r.CommerceReceipts, FundingGraphCommerceReceiptRow{OperationID: op, Kind: kind, Actor: actor, Scope: scope, Status: "accepted", Reason: "accepted", ResourceID: fundingGraphTestPtr(fundingGraphTestID(999)), CreatedAt: time.Time{}})
		}
		fundingGraphTestGood(t, r)
	})
	t.Run("payment only common FK", func(t *testing.T) {
		r := FundingSourceGraphRows{Users: []ProcurementApprovalUserRow{{"old-user"}}, Commands: []FundingGraphCommerceCommandRow{{OperationID: fundingGraphTestID(1), Actor: "old-user", Kind: "payment.order.create", Scope: "scope-opaque", RequestDigest: "not-a-voucher-digest"}}}
		fundingGraphTestGood(t, r)
		r.CommerceReceipts = []FundingGraphCommerceReceiptRow{{OperationID: r.Commands[0].OperationID, Kind: r.Commands[0].Kind, Actor: "other", Scope: r.Commands[0].Scope}}
		if fundingGraphTestCheck(t, r).Mismatches.IdentityRows != 1 {
			t.Fatal("payment generic FK not checked")
		}
	})
	t.Run("unrelated module values", func(t *testing.T) {
		r := fundingGraphTestFull()
		r.Audits = append(r.Audits, ProcurementAuditRow{ID: "other-audit", Action: "other", Diff: "not-json"})
		r.Outbox = append(r.Outbox, ProcurementOutboxRow{ID: "other-outbox", Kind: "other", Payload: "not-json"})
		r.Journals = append(r.Journals, FundingGraphJournalRow{ID: "other-journal", Kind: "other", CreatedTXID: "opaque"})
		fundingGraphTestGood(t, r)
		o := r.Outbox[len(r.Outbox)-1]
		o.ID = "another-outbox"
		r.Outbox = append(r.Outbox, o)
		if fundingGraphTestCheck(t, r).Mismatches.DuplicateOutboxEffectRows != 1 {
			t.Fatal("empty unrelated operation pair missed")
		}
	})
	t.Run("sandbox is explicit uncovered", func(t *testing.T) {
		r := fundingGraphTestFull()
		f := FundingGraphReceiptRow{ID: fundingGraphTestID(100), TenantID: r.Tenants[0].ID, SourceKind: "sandbox_capture", SourceID: fundingGraphTestID(101), Amount: "100", Currency: "CNY", JournalID: "sandbox-journal", CreatedAt: *fundingGraphTestTime()}
		r.Receipts = append(r.Receipts, f)
		r.Journals = append(r.Journals, FundingGraphJournalRow{ID: f.JournalID, TenantID: f.TenantID, OperationID: f.SourceID, Kind: "sandbox_capture"})
		r.Outbox = append(r.Outbox, ProcurementOutboxRow{ID: "sandbox-outbox", TenantID: f.TenantID, OperationID: f.SourceID, Kind: "credited", Payload: "not-validated", SchemaVersion: 99})
		c := fundingGraphTestCheck(t, r)
		if !c.ScopedGraphConsistent || c.UncoveredSandboxReceipts != 1 {
			t.Fatal(c)
		}
		r.Outbox[len(r.Outbox)-1].TenantID = "orphan-tenant"
		if fundingGraphTestCheck(t, r).Mismatches.OutboxSourceRows != 1 {
			t.Fatal("unbound credited falsely excluded as sandbox")
		}
	})
}

type fundingGraphTestContext struct {
	calls atomic.Int32
	limit int32
	err   error
}

func (c *fundingGraphTestContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *fundingGraphTestContext) Done() <-chan struct{}       { return nil }
func (c *fundingGraphTestContext) Err() error {
	if c.calls.Add(1) >= c.limit {
		return c.err
	}
	return nil
}
func (c *fundingGraphTestContext) Value(any) any { return nil }
func TestCheckFundingSourceGraphBoundsAndCancellation(t *testing.T) {
	assertError := func(t *testing.T, ctx context.Context, r FundingSourceGraphRows, want error) {
		t.Helper()
		got, e := CheckFundingSourceGraph(ctx, r)
		if !errors.Is(e, want) || got != (FundingSourceGraphCheck{}) {
			t.Fatalf("expected zero result and %v, got %+v %v", want, got, e)
		}
	}
	t.Run("nil", func(t *testing.T) { assertError(t, nil, FundingSourceGraphRows{}, ErrFundingSourceGraphRowsInput) })
	t.Run("rows", func(t *testing.T) {
		r := FundingSourceGraphRows{Users: make([]ProcurementApprovalUserRow, ledgerRowLimit+1)}
		assertError(t, context.Background(), r, ErrFundingSourceGraphRowsResource)
	})
	t.Run("exact shared rows", func(t *testing.T) {
		r := FundingSourceGraphRows{Users: make([]ProcurementApprovalUserRow, ledgerRowLimit-1), Tenants: []ProcurementAttemptTenantRow{{"t"}}}
		for i := range r.Users {
			r.Users[i].ID = "u"
		}
		if _, e := CheckFundingSourceGraph(context.Background(), r); e != nil {
			t.Fatal(e)
		}
		r.Tenants = append(r.Tenants, ProcurementAttemptTenantRow{"extra"})
		assertError(t, context.Background(), r, ErrFundingSourceGraphRowsResource)
	})
	t.Run("bytes exact plus one", func(t *testing.T) {
		r := FundingSourceGraphRows{Audits: []ProcurementAuditRow{{ID: "i", Action: "a", Diff: strings.Repeat("x", ledgerByteLimit-2)}}}
		fundingGraphTestGood(t, r)
		r.Audits[0].Diff += "x"
		assertError(t, context.Background(), r, ErrFundingSourceGraphRowsResource)
	})
	t.Run("nullable text counted", func(t *testing.T) {
		r := FundingSourceGraphRows{Audits: []ProcurementAuditRow{{ID: "i", Action: "a", Diff: strings.Repeat("x", ledgerByteLimit-2), TenantScope: fundingGraphTestPtr("x")}}}
		assertError(t, context.Background(), r, ErrFundingSourceGraphRowsResource)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		assertError(t, ctx, fundingGraphTestFull(), ErrFundingSourceGraphRowsCanceled)
	})
	t.Run("expired", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		assertError(t, ctx, fundingGraphTestFull(), ErrFundingSourceGraphRowsTimeout)
	})
	for _, tc := range []struct {
		name      string
		err, want error
	}{{"during budget cancel", context.Canceled, ErrFundingSourceGraphRowsCanceled}, {"during budget timeout", context.DeadlineExceeded, ErrFundingSourceGraphRowsTimeout}} {
		t.Run(tc.name, func(t *testing.T) {
			r := FundingSourceGraphRows{Users: make([]ProcurementApprovalUserRow, 1024)}
			for i := range r.Users {
				r.Users[i].ID = fmt.Sprint(i)
			}
			ctx := &fundingGraphTestContext{limit: 3, err: tc.err}
			assertError(t, ctx, r, tc.want)
		})
	}
	t.Run("cancel during relation indexing", func(t *testing.T) {
		r := FundingSourceGraphRows{Users: make([]ProcurementApprovalUserRow, 4096)}
		for i := range r.Users {
			r.Users[i].ID = fmt.Sprint(i)
		}
		ctx := &fundingGraphTestContext{limit: 40, err: context.Canceled}
		assertError(t, ctx, r, ErrFundingSourceGraphRowsCanceled)
	})
}
