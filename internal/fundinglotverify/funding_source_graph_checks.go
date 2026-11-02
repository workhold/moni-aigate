package fundinglotverify

import (
	"strings"
	"time"
)

type fundingGraphState struct {
	work                 *fundingGraphWork
	input                FundingSourceGraphRows
	check                FundingSourceGraphCheck
	users                map[string]ProcurementApprovalUserRow
	tenants              map[string]ProcurementAttemptTenantRow
	accounts             map[string]LedgerAccountRow
	journals             map[string]FundingGraphJournalRow
	lines                map[string]LedgerLineRow
	audits               map[string]ProcurementAuditRow
	outbox               map[string]ProcurementOutboxRow
	receipts             map[string]FundingGraphReceiptRow
	credits              map[string]FundingGraphCreditRow
	redemptions          map[string]FundingGraphRedemptionRow
	batches              map[string]FundingGraphBatchRow
	vouchers             map[string]FundingGraphVoucherRow
	revocations          map[string]FundingGraphRevocationRow
	decisions            map[string]FundingGraphRevocationDecisionRow
	commands             map[string]FundingGraphCommerceCommandRow
	terminals            map[string]FundingGraphCommerceReceiptRow
	receiptSources       map[fundingGraphPair][]FundingGraphReceiptRow
	receiptJournals      map[string][]FundingGraphReceiptRow
	redemptionVouchers   map[string][]FundingGraphRedemptionRow
	redemptionOperations map[string][]FundingGraphRedemptionRow
	redemptionBatches    map[string][]FundingGraphRedemptionRow
	batchVouchers        map[string][]FundingGraphVoucherRow
	proposalOperations   map[string][]FundingGraphRevocationRow
	decisionOperations   map[string][]FundingGraphRevocationDecisionRow
	decisionBatches      map[string][]FundingGraphRevocationDecisionRow
	journalOperations    map[string][]FundingGraphJournalRow
	journalLines         map[string][]LedgerLineRow
	accountKinds         map[fundingGraphAccountKind][]LedgerAccountRow
	auditEffects         map[fundingGraphPair][]ProcurementAuditRow
	outboxEffects        map[fundingGraphPair][]ProcurementOutboxRow
	acceptedResources    map[fundingGraphPair][]FundingGraphCommerceReceiptRow
	auditValues          map[string]map[string]fundingGraphJSONValue
	outboxValues         map[string]map[string]fundingGraphJSONValue
	outboxDecoded        map[string]bool
	expectedAudits       map[fundingGraphPair]fundingGraphAudit
	expectedCommerce     map[fundingGraphPair]fundingGraphCommerce
}

func (g *fundingGraphState) shapes() error {
	w, m, r := g.work, &g.check.Mismatches, g.input
	checks := []func() error{
		func() error {
			return fundingGraphShapes(w, r.Users, m, func(r ProcurementApprovalUserRow) bool { return fundingGraphID(r.ID) }, nil, nil, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Tenants, m, func(r ProcurementAttemptTenantRow) bool { return legacyTenantText(r.ID) }, nil, nil, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Accounts, m, func(r LedgerAccountRow) bool {
				return fundingGraphRequired(r.ID, r.Kind) && (r.TenantID == nil || legacyTenantText(*r.TenantID))
			}, nil, func(r LedgerAccountRow) bool { return fundingGraphNumber(r.Balance, false) }, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Lines, m, func(r LedgerLineRow) bool { return fundingGraphRequired(r.ID, r.JournalID, r.AccountID) }, nil, func(r LedgerLineRow) bool { return fundingGraphNumber(r.SignedDelta, false) }, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Journals, m, func(r FundingGraphJournalRow) bool { return fundingGraphID(r.ID) }, func(r FundingGraphJournalRow) bool {
				return !fundingGraphFundingKind(r.Kind) || fundingGraphTime(r.CreatedAt)
			}, func(r FundingGraphJournalRow) bool {
				return !fundingGraphFundingKind(r.Kind) || fundingGraphNumber(r.CreatedTXID, true)
			}, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Audits, m, func(r ProcurementAuditRow) bool { return fundingGraphRequired(r.ID, r.Action) }, func(r ProcurementAuditRow) bool {
				return !fundingGraphAction(r.Action) || fundingGraphTime(r.CreatedAt)
			}, nil, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Outbox, m, func(r ProcurementOutboxRow) bool { return fundingGraphRequired(r.ID, r.Kind) }, nil, nil, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Receipts, m, func(r FundingGraphReceiptRow) bool {
				return fundingGraphUUID(r.ID) && legacyTenantText(r.TenantID) && fundingGraphRequired(r.SourceKind, r.SourceID, r.Currency, r.JournalID)
			}, func(r FundingGraphReceiptRow) bool { return fundingGraphTime(r.CreatedAt) }, func(r FundingGraphReceiptRow) bool { return fundingGraphNumber(r.Amount, true) }, func(r FundingGraphReceiptRow) bool { return r.Currency == "CNY" })
		},
		func() error {
			return fundingGraphShapes(w, r.Credits, m, func(r FundingGraphCreditRow) bool {
				return legacyTenantText(r.TenantID) && fundingGraphRequired(r.ID, r.BusinessRef, r.Amount, r.State, r.Requester, r.ProofRef) && fundingGraphOptional(r.Approver, r.JournalID)
			}, func(r FundingGraphCreditRow) bool { return fundingGraphTime(r.CreatedAt) }, func(r FundingGraphCreditRow) bool { return fundingGraphNumber(r.Amount, true) }, func(r FundingGraphCreditRow) bool { return r.State == "pending" || r.State == "approved" })
		},
		func() error {
			return fundingGraphShapes(w, r.Redemptions, m, func(r FundingGraphRedemptionRow) bool {
				return fundingGraphUUID(r.ID) && fundingGraphUUID(r.VoucherID) && fundingGraphUUID(r.BatchID) && fundingGraphUUID(r.OperationID) && fundingGraphUUID(r.FundingReceiptID) && legacyTenantText(r.TenantID) && fundingGraphRequired(r.Actor, r.JournalID)
			}, func(r FundingGraphRedemptionRow) bool { return fundingGraphTime(r.CreatedAt) }, func(r FundingGraphRedemptionRow) bool { return fundingGraphNumber(r.Amount, true) }, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Vouchers, m, func(r FundingGraphVoucherRow) bool {
				return fundingGraphUUID(r.ID) && fundingGraphUUID(r.BatchID)
			}, func(r FundingGraphVoucherRow) bool { return fundingGraphTime(r.CreatedAt) }, nil, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Batches, m, func(r FundingGraphBatchRow) bool {
				return fundingGraphUUID(r.ID) && fundingGraphRequired(r.State, r.Maker) && fundingGraphOptional(r.Approver, r.TerminalActor) && procurementCommandMetadata(r.PurposeRef, 256, false) && procurementCommandMetadata(r.Reason, 200, false)
			}, func(r FundingGraphBatchRow) bool {
				return fundingGraphTime(r.ExpiresAt, r.CreatedAt) && fundingGraphOptionalTime(r.ActivatedAt, r.TerminalAt)
			}, func(r FundingGraphBatchRow) bool {
				c, ok := ledgerInteger(r.Count)
				n, ok2 := ledgerInteger(r.RedeemedCount)
				return fundingGraphNumber(r.FaceAmount, true) && fundingGraphNumber(r.Revision, true) && ok && c >= 1 && c <= 100 && ok2 && n >= 0 && n <= c && fundingGraphProductFits(r.FaceAmount, r.Count)
			}, func(r FundingGraphBatchRow) bool {
				return r.State == "proposed" || r.State == "active" || r.State == "voided" || r.State == "revoked"
			})
		},
		func() error {
			return fundingGraphShapes(w, r.Revocations, m, func(r FundingGraphRevocationRow) bool {
				return fundingGraphUUID(r.ID) && fundingGraphUUID(r.BatchID) && fundingGraphUUID(r.OperationID) && fundingGraphID(r.Maker) && procurementCommandMetadata(r.Reason, 200, false)
			}, func(r FundingGraphRevocationRow) bool { return fundingGraphTime(r.CreatedAt) }, func(r FundingGraphRevocationRow) bool {
				return fundingGraphNumber(r.ExpectedRevision, true) && fundingGraphNonnegative(r.ExpectedRedeemedCount)
			}, nil)
		},
		func() error {
			return fundingGraphShapes(w, r.Decisions, m, func(r FundingGraphRevocationDecisionRow) bool {
				return fundingGraphUUID(r.ID) && fundingGraphUUID(r.ProposalID) && fundingGraphUUID(r.BatchID) && fundingGraphUUID(r.OperationID) && fundingGraphID(r.Approver)
			}, func(r FundingGraphRevocationDecisionRow) bool { return fundingGraphTime(r.DecidedAt) }, nil, func(r FundingGraphRevocationDecisionRow) bool {
				return r.Status == "accepted" && r.Reason == "accepted" || r.Status == "rejected" && r.Reason == "voucher_evidence_stale"
			})
		},
		func() error {
			return fundingGraphShapes(w, r.Commands, m, func(r FundingGraphCommerceCommandRow) bool {
				return fundingGraphUUID(r.OperationID) && fundingGraphID(r.Kind) && (!fundingGraphVoucherKind(r.Kind) || fundingGraphUUID(r.Actor) && procurementCommandHex(r.RequestDigest))
			}, func(r FundingGraphCommerceCommandRow) bool {
				return !fundingGraphVoucherKind(r.Kind) || fundingGraphTime(r.CreatedAt)
			}, nil, func(r FundingGraphCommerceCommandRow) bool {
				return fundingGraphVoucherKind(r.Kind) || fundingGraphPaymentKind(r.Kind)
			})
		},
		func() error {
			return fundingGraphShapes(w, r.CommerceReceipts, m, func(r FundingGraphCommerceReceiptRow) bool {
				return fundingGraphUUID(r.OperationID) && fundingGraphID(r.Kind) && (!fundingGraphVoucherKind(r.Kind) || fundingGraphOptional(r.ResourceID, r.FundingReceiptID, r.JournalID) && (r.ResourceID == nil || fundingGraphUUID(*r.ResourceID)))
			}, func(r FundingGraphCommerceReceiptRow) bool {
				return !fundingGraphVoucherKind(r.Kind) || fundingGraphTime(r.CreatedAt)
			}, func(r FundingGraphCommerceReceiptRow) bool {
				return !fundingGraphVoucherKind(r.Kind) || r.Amount == nil || fundingGraphNumber(*r.Amount, true)
			}, func(r FundingGraphCommerceReceiptRow) bool {
				return fundingGraphVoucherKind(r.Kind) || fundingGraphPaymentKind(r.Kind)
			})
		},
	}
	for _, f := range checks {
		if e := f(); e != nil {
			return e
		}
	}
	return nil
}
func fundingGraphFundingKind(k string) bool { return k == "credit" || k == "voucher_grant" }
func (g *fundingGraphState) indexes() error {
	w, m, r := g.work, &g.check.Mismatches, g.input
	var e error
	if g.users, e = fundingGraphIndex(w, r.Users, func(r ProcurementApprovalUserRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.tenants, e = fundingGraphTenantIndex(w, r.Tenants, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.accounts, e = fundingGraphIndex(w, r.Accounts, func(r LedgerAccountRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.journals, e = fundingGraphIndex(w, r.Journals, func(r FundingGraphJournalRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.lines, e = fundingGraphIndex(w, r.Lines, func(r LedgerLineRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.audits, e = fundingGraphIndex(w, r.Audits, func(r ProcurementAuditRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.outbox, e = fundingGraphIndex(w, r.Outbox, func(r ProcurementOutboxRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.receipts, e = fundingGraphIndex(w, r.Receipts, func(r FundingGraphReceiptRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.credits, e = fundingGraphIndex(w, r.Credits, func(r FundingGraphCreditRow) string { return r.ID }, false, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.redemptions, e = fundingGraphIndex(w, r.Redemptions, func(r FundingGraphRedemptionRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.batches, e = fundingGraphIndex(w, r.Batches, func(r FundingGraphBatchRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.vouchers, e = fundingGraphIndex(w, r.Vouchers, func(r FundingGraphVoucherRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.revocations, e = fundingGraphIndex(w, r.Revocations, func(r FundingGraphRevocationRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.decisions, e = fundingGraphIndex(w, r.Decisions, func(r FundingGraphRevocationDecisionRow) string { return r.ID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.commands, e = fundingGraphIndex(w, r.Commands, func(r FundingGraphCommerceCommandRow) string { return r.OperationID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.terminals, e = fundingGraphIndex(w, r.CommerceReceipts, func(r FundingGraphCommerceReceiptRow) string { return r.OperationID }, true, &m.DuplicatePrimaryRows); e != nil {
		return e
	}
	if g.receiptSources, e = fundingGraphGroup(w, g.receipts, func(r FundingGraphReceiptRow) (fundingGraphPair, bool) {
		return fundingGraphPair{r.SourceKind, r.SourceID}, true
	}, &m.DuplicateReceiptSourceRows); e != nil {
		return e
	}
	if g.receiptJournals, e = fundingGraphGroup(w, g.receipts, func(r FundingGraphReceiptRow) (string, bool) { return r.JournalID, true }, &m.DuplicateReceiptJournalRows); e != nil {
		return e
	}
	if _, e = fundingGraphGroup(w, g.credits, func(r FundingGraphCreditRow) (string, bool) { return r.BusinessRef, true }, &m.DuplicateCreditBusinessRefRows); e != nil {
		return e
	}
	if _, e = fundingGraphGroup(w, g.credits, func(r FundingGraphCreditRow) (string, bool) {
		return fundingGraphPointer(r.JournalID), r.JournalID != nil
	}, &m.DuplicateCreditJournalRows); e != nil {
		return e
	}
	if g.redemptionVouchers, e = fundingGraphGroup(w, g.redemptions, func(r FundingGraphRedemptionRow) (string, bool) { return r.VoucherID, true }, &m.DuplicateRedemptionVoucherRows); e != nil {
		return e
	}
	if g.redemptionOperations, e = fundingGraphGroup(w, g.redemptions, func(r FundingGraphRedemptionRow) (string, bool) { return r.OperationID, true }, &m.DuplicateRedemptionOperationRows); e != nil {
		return e
	}
	if g.redemptionBatches, e = fundingGraphGroup(w, g.redemptions, func(r FundingGraphRedemptionRow) (string, bool) { return r.BatchID, true }, nil); e != nil {
		return e
	}
	if _, e = fundingGraphGroup(w, g.redemptions, func(r FundingGraphRedemptionRow) (string, bool) { return r.JournalID, true }, &m.DuplicateRedemptionJournalRows); e != nil {
		return e
	}
	if _, e = fundingGraphGroup(w, g.redemptions, func(r FundingGraphRedemptionRow) (string, bool) { return r.FundingReceiptID, true }, &m.DuplicateRedemptionReceiptRows); e != nil {
		return e
	}
	if g.batchVouchers, e = fundingGraphGroup(w, g.vouchers, func(r FundingGraphVoucherRow) (string, bool) { return r.BatchID, true }, nil); e != nil {
		return e
	}
	if g.proposalOperations, e = fundingGraphGroup(w, g.revocations, func(r FundingGraphRevocationRow) (string, bool) { return r.OperationID, true }, &m.DuplicateRevocationOperationRows); e != nil {
		return e
	}
	if g.decisionOperations, e = fundingGraphGroup(w, g.decisions, func(r FundingGraphRevocationDecisionRow) (string, bool) { return r.OperationID, true }, &m.DuplicateDecisionOperationRows); e != nil {
		return e
	}
	if _, e = fundingGraphGroup(w, g.decisions, func(r FundingGraphRevocationDecisionRow) (string, bool) { return r.ProposalID, true }, &m.DuplicateDecisionProposalRows); e != nil {
		return e
	}
	if g.decisionBatches, e = fundingGraphGroup(w, g.decisions, func(r FundingGraphRevocationDecisionRow) (string, bool) { return r.BatchID, r.Status == "accepted" }, nil); e != nil {
		return e
	}
	if g.journalOperations, e = fundingGraphGroup(w, g.journals, func(r FundingGraphJournalRow) (string, bool) { return r.OperationID, true }, nil); e != nil {
		return e
	}
	if g.journalLines, e = fundingGraphGroup(w, g.lines, func(r LedgerLineRow) (string, bool) { return r.JournalID, true }, nil); e != nil {
		return e
	}
	if g.accountKinds, e = fundingGraphGroup(w, g.accounts, func(r LedgerAccountRow) (fundingGraphAccountKind, bool) {
		return fundingGraphAccountKind{fundingGraphPointer(r.TenantID), r.Kind, r.TenantID != nil}, true
	}, nil); e != nil {
		return e
	}
	if g.auditEffects, e = fundingGraphGroup(w, g.audits, func(r ProcurementAuditRow) (fundingGraphPair, bool) {
		return fundingGraphPair{r.Action, r.Target}, fundingGraphAction(r.Action)
	}, &m.DuplicateAuditEffectRows); e != nil {
		return e
	}
	if g.outboxEffects, e = fundingGraphGroup(w, g.outbox, func(r ProcurementOutboxRow) (fundingGraphPair, bool) {
		return fundingGraphPair{r.OperationID, r.Kind}, true
	}, &m.DuplicateOutboxEffectRows); e != nil {
		return e
	}
	if g.acceptedResources, e = fundingGraphGroup(w, g.terminals, func(r FundingGraphCommerceReceiptRow) (fundingGraphPair, bool) {
		return fundingGraphPair{r.Kind, fundingGraphPointer(r.ResourceID)}, fundingGraphVoucherKind(r.Kind) && r.Kind != "voucher.redeem" && r.Status == "accepted" && r.ResourceID != nil
	}, &m.DuplicateAcceptedResourceRows); e != nil {
		return e
	}
	g.auditValues = make(map[string]map[string]fundingGraphJSONValue, len(g.audits))
	g.outboxValues = make(map[string]map[string]fundingGraphJSONValue)
	g.outboxDecoded = make(map[string]bool)
	g.expectedAudits = make(map[fundingGraphPair]fundingGraphAudit)
	g.expectedCommerce = make(map[fundingGraphPair]fundingGraphCommerce)
	return nil
}
func fundingGraphPointer(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (g *fundingGraphState) hasUser(s string) bool   { _, ok := g.users[s]; return ok }
func (g *fundingGraphState) hasTenant(s string) bool { _, ok := g.tenants[s]; return ok }
func (g *fundingGraphState) identity(ok bool) {
	if !ok {
		g.check.Mismatches.IdentityRows++
	}
}
func (g *fundingGraphState) run() error {
	if e := g.shapes(); e != nil {
		return e
	}
	if e := g.indexes(); e != nil {
		return e
	}
	steps := []func() error{g.sourceRows, g.batchRows, g.revocationRows, g.commerceRows, g.auditRows, g.reverseRows}
	for _, step := range steps {
		if e := step(); e != nil {
			return e
		}
	}
	return nil
}

type fundingGraphEffect struct{ tenant, amount, journal, operation, kind, reference, actor, clearing string }

func (g *fundingGraphState) manualEffect(c FundingGraphCreditRow) (fundingGraphEffect, bool) {
	x := fundingGraphEffect{c.TenantID, c.Amount, fundingGraphPointer(c.JournalID), c.ID, "credit", c.ID, fundingGraphPointer(c.Approver), "FUNDING_CLEARING"}
	return x, c.State == "approved" && c.Approver != nil && c.JournalID != nil && *c.Approver != "" && *c.JournalID != "" && *c.Approver != c.Requester && fundingGraphNumber(c.Amount, true)
}
func (g *fundingGraphState) voucherEffect(r FundingGraphRedemptionRow) (fundingGraphEffect, bool) {
	x := fundingGraphEffect{r.TenantID, r.Amount, r.JournalID, r.ID, "voucher_grant", r.VoucherID, r.Actor, "OPERATING_GRANT_CLEARING"}
	return x, fundingGraphNumber(r.Amount, true)
}
func fundingGraphReceiptMatches(f FundingGraphReceiptRow, x fundingGraphEffect, kind, source, id string) bool {
	return (id == "" || f.ID == id) && f.TenantID == x.tenant && f.Amount == x.amount && f.JournalID == x.journal && f.SourceKind == kind && f.SourceID == source && f.Currency == "CNY" && fundingGraphNumber(f.Amount, true)
}
func (g *fundingGraphState) journalMatches(j FundingGraphJournalRow, x fundingGraphEffect) bool {
	return j.ID == x.journal && j.TenantID == x.tenant && j.OperationID == x.operation && j.Kind == x.kind && j.Reference == x.reference && j.Actor == x.actor && j.Currency == "CNY" && len(g.journalOperations[x.operation]) == 1
}
func (g *fundingGraphState) linesMatch(x fundingGraphEffect) bool {
	rows := g.journalLines[x.journal]
	if len(rows) != 2 || rows[0].AccountID == rows[1].AccountID || !fundingGraphNumber(x.amount, true) {
		return false
	}
	if len(g.accountKinds[fundingGraphAccountKind{x.tenant, "AVAILABLE", true}]) != 1 || len(g.accountKinds[fundingGraphAccountKind{"", x.clearing, false}]) != 1 {
		return false
	}
	plus, minus := false, false
	for _, l := range rows {
		a, ok := g.accounts[l.AccountID]
		if !ok {
			return false
		}
		if a.Kind == "AVAILABLE" && a.TenantID != nil && *a.TenantID == x.tenant && l.SignedDelta == x.amount {
			plus = true
		} else if a.Kind == x.clearing && a.TenantID == nil && l.SignedDelta == "-"+x.amount {
			minus = true
		} else {
			return false
		}
	}
	return plus && minus
}
func (g *fundingGraphState) outboxJSON(o ProcurementOutboxRow) map[string]fundingGraphJSONValue {
	if !g.outboxDecoded[o.ID] {
		g.outboxDecoded[o.ID] = true
		if !fundingGraphTime(o.CreatedAt) {
			g.check.Mismatches.InvalidTimeRows++
		}
		keys, num := fundingGraphJSONKeys("credited")
		values, ok := fundingGraphJSON(o.Payload, keys, num)
		if !ok {
			g.check.Mismatches.InvalidJSONRows++
		} else {
			g.outboxValues[o.ID] = values
		}
	}
	return g.outboxValues[o.ID]
}
func (g *fundingGraphState) outboxMatches(o ProcurementOutboxRow, x fundingGraphEffect) bool {
	values := g.outboxJSON(o)
	return o.TenantID == x.tenant && o.OperationID == x.operation && o.Kind == "credited" && o.SchemaVersion == 1 && values != nil && values["amount"].value == x.amount && fundingGraphTime(o.CreatedAt)
}
func (g *fundingGraphState) checkEffect(x fundingGraphEffect) {
	m := &g.check.Mismatches
	j, ok := g.journals[x.journal]
	if !ok || !g.journalMatches(j, x) {
		m.FundingJournalRows++
	}
	if !g.linesMatch(x) {
		m.FundingLineRows++
	}
	rows := g.outboxEffects[fundingGraphPair{x.operation, "credited"}]
	if len(rows) != 1 || !g.outboxMatches(rows[0], x) {
		m.FundingOutboxRows++
	}
}
func (g *fundingGraphState) receiptEffect(f FundingGraphReceiptRow) (fundingGraphEffect, bool) {
	switch f.SourceKind {
	case "manual_credit":
		c, ok := g.credits[f.SourceID]
		x, shape := g.manualEffect(c)
		return x, ok && shape && fundingGraphReceiptMatches(f, x, "manual_credit", c.ID, "")
	case "voucher_grant":
		rows := g.redemptionVouchers[f.SourceID]
		if len(rows) != 1 {
			return fundingGraphEffect{}, false
		}
		r := rows[0]
		x, shape := g.voucherEffect(r)
		return x, shape && fundingGraphReceiptMatches(f, x, "voucher_grant", r.VoucherID, r.FundingReceiptID)
	}
	return fundingGraphEffect{}, false
}
func (g *fundingGraphState) sourceRows() error {
	m := &g.check.Mismatches
	for _, a := range g.accounts {
		if e := g.work.tick(); e != nil {
			return e
		}
		g.identity(a.TenantID == nil || g.hasTenant(*a.TenantID))
	}
	for _, c := range g.credits {
		if e := g.work.tick(); e != nil {
			return e
		}
		g.identity(g.hasTenant(c.TenantID) && g.hasUser(c.Requester) && (c.Approver == nil || g.hasUser(*c.Approver)))
		g.expectAudit("credit.created", c.ID, c.Requester, c.TenantID, true, map[string]string{"amount": c.Amount}, c.State == "pending" || c.State == "approved")
		rows := g.receiptSources[fundingGraphPair{"manual_credit", c.ID}]
		if c.State == "pending" {
			bad := c.Approver != nil || c.JournalID != nil || len(rows) != 0 || len(g.outboxEffects[fundingGraphPair{c.ID, "credited"}]) != 0 || len(g.auditEffects[fundingGraphPair{"credit.approved", c.ID}]) != 0
			for _, j := range g.journalOperations[c.ID] {
				if j.Kind == "credit" {
					bad = true
				}
			}
			if bad {
				m.CreditShapeRows++
			}
			continue
		}
		x, ok := g.manualEffect(c)
		if !ok {
			m.CreditShapeRows++
			continue
		}
		if len(rows) != 1 || !fundingGraphReceiptMatches(rows[0], x, "manual_credit", c.ID, "") {
			m.CreditReceiptRows++
		}
		g.checkEffect(x)
		g.expectAudit("credit.approved", c.ID, x.actor, c.TenantID, true, map[string]string{"amount": c.Amount}, true)
	}
	for _, r := range g.redemptions {
		if e := g.work.tick(); e != nil {
			return e
		}
		v, vok := g.vouchers[r.VoucherID]
		b, bok := g.batches[r.BatchID]
		g.identity(g.hasTenant(r.TenantID) && g.hasUser(r.Actor) && vok && bok && v.BatchID == r.BatchID)
		x, shape := g.voucherEffect(r)
		f, fok := g.receipts[r.FundingReceiptID]
		sources := g.receiptSources[fundingGraphPair{"voucher_grant", r.VoucherID}]
		if !shape || !fok || len(sources) != 1 || !fundingGraphReceiptMatches(f, x, "voucher_grant", r.VoucherID, r.FundingReceiptID) {
			m.RedemptionReceiptRows++
		}
		if !bok || !vok || v.BatchID != r.BatchID || (b.State != "active" && b.State != "revoked") || r.Amount != b.FaceAmount || b.ActivatedAt == nil || r.CreatedAt.Before(fundingGraphTimeValue(b.ActivatedAt)) || !r.CreatedAt.Before(b.ExpiresAt) || !fok || !r.CreatedAt.Equal(f.CreatedAt) {
			m.RedemptionEligibilityRows++
		}
		g.checkEffect(x)
		g.expectAudit("voucher.redeemed", r.ID, r.Actor, r.TenantID, false, map[string]string{"amount": r.Amount, "funding_receipt_id": r.FundingReceiptID}, shape)
		g.expectCommerce(fundingGraphCommerce{kind: "voucher.redeem", resource: r.ID, actor: r.Actor, scope: "tenant:" + r.TenantID, operation: r.OperationID, amount: r.Amount, funding: r.FundingReceiptID, journal: r.JournalID, valid: shape})
	}
	return nil
}
func fundingGraphTimeValue(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return *p
}
func (g *fundingGraphState) batchRows() error {
	m := &g.check.Mismatches
	for _, v := range g.vouchers {
		if e := g.work.tick(); e != nil {
			return e
		}
		if _, ok := g.batches[v.BatchID]; !ok {
			m.VoucherBatchRows++
		}
	}
	for _, b := range g.batches {
		if e := g.work.tick(); e != nil {
			return e
		}
		g.identity(g.hasUser(b.Maker) && (b.Approver == nil || g.hasUser(*b.Approver)) && (b.TerminalActor == nil || g.hasUser(*b.TerminalActor)))
		c, ok := ledgerInteger(b.Count)
		n, nok := ledgerInteger(b.RedeemedCount)
		if !ok || int64(len(g.batchVouchers[b.ID])) != c {
			m.BatchVoucherCountRows++
		}
		if !nok || int64(len(g.redemptionBatches[b.ID])) != n {
			m.BatchRedemptionCountRows++
		}
		good := fundingGraphProductFits(b.FaceAmount, b.Count) && ok && c >= 1 && c <= 100 && nok && n >= 0 && n <= c && !b.ExpiresAt.Before(b.CreatedAt.Add(time.Hour)) && !b.ExpiresAt.After(b.CreatedAt.Add(365*24*time.Hour))
		approved := b.Approver != nil && *b.Approver != "" && *b.Approver != b.Maker && b.ActivatedAt != nil && b.ActivatedAt.Before(b.ExpiresAt)
		switch b.State {
		case "proposed":
			good = good && b.Approver == nil && b.ActivatedAt == nil && b.TerminalActor == nil && b.TerminalAt == nil && b.Revision == "1" && n == 0
		case "active":
			good = good && approved && b.TerminalActor == nil && b.TerminalAt == nil && b.Revision == "2"
		case "voided":
			good = good && b.Approver == nil && b.ActivatedAt == nil && b.TerminalActor != nil && *b.TerminalActor == b.Maker && b.TerminalAt != nil && b.Revision == "2" && n == 0
		case "revoked":
			good = good && approved && b.TerminalActor != nil && fundingGraphID(*b.TerminalActor) && b.TerminalAt != nil && b.Revision == "3" && len(g.decisionBatches[b.ID]) == 1
		default:
			good = false
		}
		if !good {
			m.BatchShapeRows++
		}
		g.expectAudit("voucher.batch.created", b.ID, b.Maker, "", false, map[string]string{"face_amount": b.FaceAmount, "count": b.Count, "purpose_ref": b.PurposeRef}, true)
		g.expectCommerce(fundingGraphCommerce{kind: "voucher.batch.create", resource: b.ID, actor: b.Maker, scope: "platform", valid: true})
		if b.State == "active" || b.State == "revoked" {
			g.expectAudit("voucher.batch.approved", b.ID, fundingGraphPointer(b.Approver), "", false, map[string]string{"maker": b.Maker}, approved)
			g.expectCommerce(fundingGraphCommerce{kind: "voucher.batch.approve", resource: b.ID, actor: fundingGraphPointer(b.Approver), scope: "platform", valid: approved})
		}
		if b.State == "voided" {
			g.expectAudit("voucher.batch.voided", b.ID, b.Maker, "", false, map[string]string{"reason": ""}, good)
			g.expectCommerce(fundingGraphCommerce{kind: "voucher.batch.void", resource: b.ID, actor: b.Maker, scope: "platform", valid: good})
		}
	}
	return nil
}
func (g *fundingGraphState) revocationRows() error {
	m := &g.check.Mismatches
	for _, p := range g.revocations {
		if e := g.work.tick(); e != nil {
			return e
		}
		b, ok := g.batches[p.BatchID]
		g.identity(ok && g.hasUser(p.Maker))
		n, nok := ledgerInteger(p.ExpectedRedeemedCount)
		count, cok := ledgerInteger(b.Count)
		redeemed, rok := ledgerInteger(b.RedeemedCount)
		good := ok && (b.State == "active" || b.State == "revoked") && p.ExpectedRevision == "2" && nok && cok && rok && n >= 0 && n <= count && n <= redeemed
		if !good {
			m.RevocationRows++
		}
		g.expectAudit("voucher.revocation.created", p.ID, p.Maker, "", false, map[string]string{"batch_id": p.BatchID, "reason": p.Reason}, good)
		g.expectCommerce(fundingGraphCommerce{kind: "voucher.revocation.create", resource: p.ID, actor: p.Maker, scope: "platform", operation: p.OperationID, valid: good})
	}
	for _, d := range g.decisions {
		if e := g.work.tick(); e != nil {
			return e
		}
		p, pok := g.revocations[d.ProposalID]
		b, bok := g.batches[d.BatchID]
		g.identity(pok && bok && p.BatchID == d.BatchID && g.hasUser(d.Approver))
		good := pok && bok && p.BatchID == d.BatchID && d.Approver != p.Maker && d.OperationID != p.OperationID
		c, cok := g.commands[d.OperationID]
		t, tok := g.terminals[d.OperationID]
		good = good && cok && tok && c.Kind == "voucher.revocation.approve" && c.Scope == "platform" && c.Actor == d.Approver && t.Kind == c.Kind && t.Scope == c.Scope && t.Actor == c.Actor && t.Status == d.Status && t.Reason == d.Reason
		if d.Status == "accepted" {
			good = good && d.Reason == "accepted" && b.State == "revoked" && fundingGraphPointer(b.TerminalActor) == d.Approver && b.Revision == "3" && p.ExpectedRevision == "2" && b.RedeemedCount == p.ExpectedRedeemedCount && t.ResourceID != nil && *t.ResourceID == p.ID
			g.expectAudit("voucher.revocation.approved", p.ID, d.Approver, "", false, map[string]string{"batch_id": d.BatchID}, good)
			g.expectCommerce(fundingGraphCommerce{kind: "voucher.revocation.approve", resource: p.ID, actor: d.Approver, scope: "platform", operation: d.OperationID, valid: good})
		} else {
			good = good && d.Status == "rejected" && d.Reason == "voucher_evidence_stale" && t.ResourceID == nil && t.Amount == nil && t.FundingReceiptID == nil && t.JournalID == nil
		}
		if !good {
			m.DecisionRows++
		}
	}
	return nil
}

type fundingGraphAudit struct {
	actor, tenant string
	manual, valid bool
	values        map[string]string
}

func (g *fundingGraphState) expectAudit(action, target, actor, tenant string, manual bool, values map[string]string, valid bool) {
	key := fundingGraphPair{action, target}
	if _, ok := g.expectedAudits[key]; !ok {
		g.expectedAudits[key] = fundingGraphAudit{actor, tenant, manual, valid, values}
	}
}
func (g *fundingGraphState) auditMatches(action string, a ProcurementAuditRow, x fundingGraphAudit) bool {
	values := g.auditValues[a.ID]
	if !x.valid || a.Actor != x.actor || a.TenantScope == nil || *a.TenantScope != x.tenant || x.manual && a.RequestID != "" || values == nil {
		return false
	}
	for k, want := range x.values {
		got := values[k].value
		if action == "voucher.batch.voided" && k == "reason" {
			if !procurementCommandMetadata(got, 200, false) {
				return false
			}
		} else if got != want {
			return false
		}
	}
	return true
}
func (g *fundingGraphState) auditRows() error {
	m := &g.check.Mismatches
	for id, a := range g.audits {
		if e := g.work.tick(); e != nil {
			return e
		}
		if !fundingGraphAction(a.Action) {
			continue
		}
		keys, num := fundingGraphJSONKeys(a.Action)
		v, ok := fundingGraphJSON(a.Diff, keys, num)
		if !ok {
			m.InvalidJSONRows++
		} else {
			g.auditValues[id] = v
		}
	}
	for key, x := range g.expectedAudits {
		if e := g.work.tick(); e != nil {
			return e
		}
		rows := g.auditEffects[key]
		if len(rows) != 1 || !g.auditMatches(key.first, rows[0], x) {
			m.FundingAuditRows++
		}
	}
	for _, a := range g.audits {
		if e := g.work.tick(); e != nil {
			return e
		}
		if !fundingGraphAction(a.Action) {
			continue
		}
		x, ok := g.expectedAudits[fundingGraphPair{a.Action, a.Target}]
		if !ok || !g.auditMatches(a.Action, a, x) {
			m.AuditSourceRows++
		}
	}
	return nil
}

type fundingGraphCommerce struct {
	kind, resource, actor, scope, operation, amount, funding, journal string
	valid                                                             bool
}

func (g *fundingGraphState) expectCommerce(x fundingGraphCommerce) {
	key := fundingGraphPair{x.kind, x.resource}
	if _, ok := g.expectedCommerce[key]; !ok {
		g.expectedCommerce[key] = x
	}
}
func fundingGraphRejectedReason(kind, reason string) bool {
	switch kind {
	case "voucher.batch.create":
		return reason == "vouchers_disabled" || reason == "voucher_state_conflict"
	case "voucher.batch.approve", "voucher.batch.void":
		return reason == "voucher_revision_conflict" || reason == "voucher_state_conflict"
	case "voucher.revocation.create":
		return reason == "voucher_revision_conflict" || reason == "voucher_state_conflict" || reason == "voucher_evidence_stale"
	case "voucher.revocation.approve":
		return reason == "voucher_state_conflict" || reason == "voucher_evidence_stale"
	case "voucher.redeem":
		return reason == "vouchers_disabled" || reason == "voucher_unavailable" || reason == "funding_amount_overflow"
	}
	return false
}
func (g *fundingGraphState) commandShape(c FundingGraphCommerceCommandRow) bool {
	if c.Kind == "voucher.redeem" {
		return strings.HasPrefix(c.Scope, "tenant:") && fundingGraphUUID(strings.TrimPrefix(c.Scope, "tenant:")) && g.hasTenant(strings.TrimPrefix(c.Scope, "tenant:")) && c.FingerprintKeyVersion != nil && fundingGraphVersion(*c.FingerprintKeyVersion)
	}
	return c.Scope == "platform" && c.FingerprintKeyVersion == nil
}
func fundingGraphTerminalShape(t FundingGraphCommerceReceiptRow) bool {
	if t.Status == "rejected" {
		return fundingGraphRejectedReason(t.Kind, t.Reason) && t.ResourceID == nil && t.Amount == nil && t.FundingReceiptID == nil && t.JournalID == nil
	}
	if t.Status != "accepted" || t.Reason != "accepted" || t.ResourceID == nil || !fundingGraphUUID(*t.ResourceID) {
		return false
	}
	if t.Kind == "voucher.redeem" {
		return t.Amount != nil && fundingGraphNumber(*t.Amount, true) && t.FundingReceiptID != nil && fundingGraphUUID(*t.FundingReceiptID) && t.JournalID != nil && fundingGraphID(*t.JournalID)
	}
	return t.Amount == nil && t.FundingReceiptID == nil && t.JournalID == nil
}
func (g *fundingGraphState) terminalMatches(t FundingGraphCommerceReceiptRow, x fundingGraphCommerce) bool {
	c, ok := g.commands[t.OperationID]
	if !ok || !x.valid || t.Status != "accepted" || t.Reason != "accepted" || t.Kind != x.kind || t.ResourceID == nil || *t.ResourceID != x.resource || t.Actor != x.actor || t.Scope != x.scope || x.operation != "" && t.OperationID != x.operation || c.Kind != t.Kind || c.Actor != t.Actor || c.Scope != t.Scope || !c.CreatedAt.Equal(t.CreatedAt) || !g.commandShape(c) || !fundingGraphTerminalShape(t) {
		return false
	}
	if x.kind == "voucher.redeem" {
		return fundingGraphPointer(t.Amount) == x.amount && fundingGraphPointer(t.FundingReceiptID) == x.funding && fundingGraphPointer(t.JournalID) == x.journal
	}
	return true
}
func (g *fundingGraphState) commerceRows() error {
	m := &g.check.Mismatches
	for _, c := range g.commands {
		if e := g.work.tick(); e != nil {
			return e
		}
		g.identity(g.hasUser(c.Actor))
		if !fundingGraphVoucherKind(c.Kind) {
			continue
		}
		if !g.commandShape(c) {
			m.CommerceShapeRows++
		}
		t, ok := g.terminals[c.OperationID]
		if !ok || t.Kind != c.Kind || t.Scope != c.Scope || t.Actor != c.Actor || !t.CreatedAt.Equal(c.CreatedAt) {
			m.CommerceTerminalRows++
		}
	}
	for _, t := range g.terminals {
		if e := g.work.tick(); e != nil {
			return e
		}
		c, ok := g.commands[t.OperationID]
		fk := ok && c.Kind == t.Kind && c.Scope == t.Scope && c.Actor == t.Actor
		g.identity(fk)
		if !fundingGraphVoucherKind(t.Kind) {
			continue
		}
		if !fundingGraphTerminalShape(t) {
			m.CommerceShapeRows++
		}
		if !fk || !t.CreatedAt.Equal(c.CreatedAt) {
			m.CommerceTerminalRows++
		}
		if t.Status == "accepted" {
			x, ok := g.expectedCommerce[fundingGraphPair{t.Kind, fundingGraphPointer(t.ResourceID)}]
			if !ok || !g.terminalMatches(t, x) {
				m.CommerceSourceRows++
			}
			continue
		}
		good := len(g.redemptionOperations[t.OperationID]) == 0 && len(g.proposalOperations[t.OperationID]) == 0
		ds := g.decisionOperations[t.OperationID]
		if t.Kind == "voucher.revocation.approve" && t.Reason == "voucher_evidence_stale" {
			good = good && len(ds) == 1
			if len(ds) == 1 {
				d := ds[0]
				good = good && d.Status == "rejected" && d.Reason == t.Reason && d.Approver == t.Actor
			}
		} else {
			good = good && len(ds) == 0
		}
		if !good {
			m.CommerceSourceRows++
		}
	}
	for key, x := range g.expectedCommerce {
		if e := g.work.tick(); e != nil {
			return e
		}
		var rows []FundingGraphCommerceReceiptRow
		if x.operation != "" {
			if t, ok := g.terminals[x.operation]; ok {
				rows = []FundingGraphCommerceReceiptRow{t}
			}
		} else {
			rows = g.acceptedResources[key]
		}
		if len(rows) != 1 || !g.terminalMatches(rows[0], x) {
			m.CommerceSourceRows++
		}
	}
	return nil
}
func (g *fundingGraphState) sandboxOutbox(o ProcurementOutboxRow) bool {
	js := g.journalOperations[o.OperationID]
	if len(js) != 1 || js[0].Kind != "sandbox_capture" {
		return false
	}
	fs := g.receiptJournals[js[0].ID]
	if len(fs) != 1 {
		return false
	}
	f := fs[0]
	return f.SourceKind == "sandbox_capture" && g.hasTenant(f.TenantID) && fundingGraphNumber(f.Amount, true) && f.Currency == "CNY" && fundingGraphRequired(f.SourceID, f.JournalID) && f.TenantID == js[0].TenantID && o.TenantID == f.TenantID
}
func (g *fundingGraphState) reverseRows() error {
	m := &g.check.Mismatches
	for _, f := range g.receipts {
		if e := g.work.tick(); e != nil {
			return e
		}
		g.identity(g.hasTenant(f.TenantID))
		switch f.SourceKind {
		case "manual_credit", "voucher_grant":
			if _, ok := g.receiptEffect(f); !ok {
				m.ReceiptSourceRows++
			}
		case "sandbox_capture":
			// All raw sandbox rows, including duplicates, were counted before
			// indexing; this branch grants no sandbox business coverage.
		default:
			m.UnknownReceiptKindRows++
		}
	}
	for _, j := range g.journals {
		if e := g.work.tick(); e != nil {
			return e
		}
		if !fundingGraphFundingKind(j.Kind) {
			continue
		}
		fs := g.receiptJournals[j.ID]
		good := len(fs) == 1
		if good {
			x, ok := g.receiptEffect(fs[0])
			good = ok && g.journalMatches(j, x)
		}
		if !good {
			m.FundingJournalSourceRows++
		}
	}
	for _, l := range g.lines {
		if e := g.work.tick(); e != nil {
			return e
		}
		a, ok := g.accounts[l.AccountID]
		if !ok || (a.Kind != "FUNDING_CLEARING" && a.Kind != "OPERATING_GRANT_CLEARING") {
			continue
		}
		j, jok := g.journals[l.JournalID]
		fs := g.receiptJournals[l.JournalID]
		good := jok && len(fs) == 1
		if good {
			x, shape := g.receiptEffect(fs[0])
			good = shape && a.Kind == x.clearing && a.TenantID == nil && l.SignedDelta == "-"+x.amount && g.journalMatches(j, x) && g.linesMatch(x)
		}
		if !good {
			m.ClearingLineSourceRows++
		}
	}
	for _, o := range g.outbox {
		if e := g.work.tick(); e != nil {
			return e
		}
		if o.Kind != "credited" {
			continue
		}
		if g.sandboxOutbox(o) {
			continue
		}
		js := g.journalOperations[o.OperationID]
		good := len(js) == 1
		if good {
			j := js[0]
			fs := g.receiptJournals[j.ID]
			good = len(fs) == 1
			if good {
				x, ok := g.receiptEffect(fs[0])
				good = ok && g.journalMatches(j, x) && g.outboxMatches(o, x)
			}
		}
		if !good {
			m.OutboxSourceRows++
		}
	}
	return nil
}
