package fundinglotverify

import (
	"encoding/json"
	"io"
	"math/big"
	"strings"
	"time"
)

func sandboxGraphIdentityTuple(i SandboxGraphIdentityRow, e SandboxGraphEvidenceRow, o SandboxGraphOrderRow) bool {
	return e.Kind == "capture" && e.ProviderTransactionID != nil && e.Amount != nil && e.Currency != nil && e.HappenedAt != nil && e.ProviderOrderRevision != nil && i.EnvironmentID == e.EnvironmentID && i.MerchantID == e.MerchantID && i.OrderID == e.OrderID && i.ProviderTransactionID == *e.ProviderTransactionID && i.Amount == *e.Amount && i.Currency == *e.Currency && i.CapturedAt.Equal(*e.HappenedAt) && i.ProviderOrderRevision == *e.ProviderOrderRevision && i.TenantID == o.TenantID && i.OrderID == o.ID && i.EnvironmentID == o.EnvironmentID && i.MerchantID == o.MerchantID && i.Amount == o.Amount && i.Currency == o.Currency
}
func (g *sandboxGraphData) revision(e SandboxGraphEvidenceRow) (SandboxGraphProviderRevisionRow, bool) {
	key := sandboxGraphKey(SandboxGraphProviderRevisionRow{EnvironmentID: e.EnvironmentID, MerchantID: e.MerchantID, OrderID: e.OrderID, ProviderOrderRevision: sandboxGraphPtr(e.ProviderOrderRevision)}, "EnvironmentID", "MerchantID", "OrderID", "ProviderOrderRevision")
	r, ok := g.index["ProviderRevisions"][key]
	if !ok {
		return SandboxGraphProviderRevisionRow{}, false
	}
	return r.(SandboxGraphProviderRevisionRow), true
}

// Independent extrema keep closed/capture compatibility linear even when one
// order has many retained reviews or identities.
type sandboxGraphBounds struct {
	at       time.Time
	revision int64
	set      bool
}

func (g *sandboxGraphData) closedConflict(e SandboxGraphEvidenceRow) bool {
	if e.HappenedAt == nil || e.ProviderOrderRevision == nil {
		return false
	}
	if g.closedBounds == nil {
		g.closedBounds = map[string]sandboxGraphBounds{}
		g.identityBounds = map[string]sandboxGraphBounds{}
		for _, r := range sandboxGraphRows[SandboxGraphProviderRevisionRow](g, "ProviderRevisions") {
			if !g.tick() {
				return false
			}
			if r.Kind != "closed" {
				continue
			}
			f, ok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", r.EvidenceID)
			if !ok || f.HappenedAt == nil {
				continue
			}
			v := g.closedBounds[r.OrderID]
			n := sandboxGraphN(r.ProviderOrderRevision)
			if !v.set || f.HappenedAt.Before(v.at) {
				v.at = *f.HappenedAt
			}
			if !v.set || n < v.revision {
				v.revision = n
			}
			v.set = true
			g.closedBounds[r.OrderID] = v
		}
		for _, i := range sandboxGraphRows[SandboxGraphIdentityRow](g, "Identities") {
			if !g.tick() {
				return false
			}
			v := g.identityBounds[i.OrderID]
			n := sandboxGraphN(i.ProviderOrderRevision)
			if !v.set || i.CapturedAt.After(v.at) {
				v.at = i.CapturedAt
			}
			if !v.set || n > v.revision {
				v.revision = n
			}
			v.set = true
			g.identityBounds[i.OrderID] = v
		}
	}
	if e.Kind == "closed" {
		v := g.identityBounds[e.OrderID]
		return v.set && (!v.at.Before(*e.HappenedAt) || v.revision >= sandboxGraphN(*e.ProviderOrderRevision))
	}
	if e.Kind == "capture" {
		v := g.closedBounds[e.OrderID]
		return v.set && (!e.HappenedAt.Before(v.at) || sandboxGraphN(*e.ProviderOrderRevision) >= v.revision)
	}
	return false
}
func (g *sandboxGraphData) identitiesAndReviews() {
	for _, r := range sandboxGraphRows[SandboxGraphProviderRevisionRow](g, "ProviderRevisions") {
		if !g.tick() {
			return
		}
		e, ok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", r.EvidenceID)
		p, pok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", r.EvidenceID)
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", r.OrderID)
		valid := ok && pok && ook && g.facts[e.ID].valid && sandboxGraphTerminal(p) && sandboxGraphHas(r.Kind, "capture", "closed") && r.EnvironmentID == e.EnvironmentID && r.MerchantID == e.MerchantID && r.OrderID == e.OrderID && r.ProviderOrderRevision == sandboxGraphPtr(e.ProviderOrderRevision) && r.Kind == e.Kind && r.FactDigest == e.FactDigest && r.EnvironmentID == o.EnvironmentID && r.MerchantID == o.MerchantID
		if e.Kind == "closed" {
			valid = valid && !g.closedConflict(e)
		}
		if e.Kind == "capture" {
			valid = valid && e.Amount != nil && *e.Amount == o.Amount && e.HappenedAt != nil && !e.HappenedAt.Before(o.CreatedAt) && e.HappenedAt.Before(o.ExpiresAt)
		}
		if !valid {
			g.bad("ProviderRevisionRows", "ProviderRevisions", sandboxGraphKey(r, "EnvironmentID", "MerchantID", "OrderID", "ProviderOrderRevision"))
		}
	}
	for _, i := range sandboxGraphRows[SandboxGraphIdentityRow](g, "Identities") {
		if !g.tick() {
			return
		}
		e, eok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", i.EvidenceID)
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", i.OrderID)
		p, pok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", i.EvidenceID)
		r, rok := g.revision(e)
		valid := fundingGraphUUID(i.ID) && eok && ook && pok && rok && g.facts[e.ID].valid && sandboxGraphIdentityTuple(i, e, o) && sandboxGraphMoney(i.Amount) && sandboxGraphRef(i.ProviderTransactionID) && sandboxGraphMoney(i.ProcessingGeneration) && p.Generation == i.ProcessingGeneration && sandboxGraphTerminal(p) && r.Kind == "capture" && r.FactDigest == e.FactDigest && !i.CapturedAt.Before(o.CreatedAt) && i.CapturedAt.Before(o.ExpiresAt) && !g.closedConflict(e)
		g.identities[i.ID] = valid
		if !valid {
			g.bad("TransactionIdentityRows", "Identities", i.ID)
		}
	}
	for _, r := range sandboxGraphRows[SandboxGraphReviewRow](g, "Reviews") {
		if !g.tick() {
			return
		}
		e, eok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", r.EvidenceID)
		p, pok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", r.EvidenceID)
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", r.OrderID)
		base := fundingGraphUUID(r.ID) && eok && pok && g.facts[e.ID].valid && r.OrderID == e.OrderID && p.State == "processing_review" && sandboxGraphCode(p.LastErrorCode, "permanent")
		if r.IdentityID != nil {
			i, ok := sandboxGraphGet[SandboxGraphIdentityRow](g, "Identities", *r.IdentityID)
			base = base && ok && i.OrderID == r.OrderID
		}
		if r.SelectionOrderID != nil {
			base = base && *r.SelectionOrderID == r.OrderID && g.exists("Selections", *r.SelectionOrderID)
		}
		valid := false
		nilRefs := r.IdentityID == nil && r.SelectionOrderID == nil
		switch r.Reason {
		case "unknown_order":
			valid = nilRefs
		case "unsupported_effect_review":
			valid = nilRefs && e.Kind == "unsupported"
		case "amount_mismatch":
			valid = nilRefs && ook && e.Kind == "capture" && e.Amount != nil && *e.Amount != o.Amount
		case "capture_outside_window":
			valid = nilRefs && ook && e.Kind == "capture" && e.HappenedAt != nil && (e.HappenedAt.Before(o.CreatedAt) || !e.HappenedAt.Before(o.ExpiresAt))
		case "provider_revision_conflict":
			revision, found := g.revision(e)
			valid = nilRefs && ook && sandboxGraphHas(e.Kind, "capture", "closed") && (found && (revision.Kind != e.Kind || revision.FactDigest != e.FactDigest) || g.closedConflict(e))
		case "transaction_conflict":
			if nilRefs && ook && e.Kind == "capture" && e.ProviderTransactionID != nil {
				for _, i := range sandboxGraphRelated[SandboxGraphIdentityRow](g, "Identities", []string{"EnvironmentID", "MerchantID", "ProviderTransactionID"}, e.EnvironmentID, e.MerchantID, *e.ProviderTransactionID) {
					if !g.tick() {
						return
					}
					if !sandboxGraphIdentityTuple(i, e, o) {
						valid = true
					}
				}
			}
		case "duplicate_order_capture", "funding_amount_overflow":
			i, iok := sandboxGraphGet[SandboxGraphIdentityRow](g, "Identities", sandboxGraphPtr(r.IdentityID))
			s, sok := sandboxGraphGet[SandboxGraphSelectionRow](g, "Selections", r.OrderID)
			valid = ook && iok && sok && g.identities[i.ID] && r.IdentityID != nil && r.SelectionOrderID != nil && *r.SelectionOrderID == r.OrderID && sandboxGraphIdentityTuple(i, e, o)
			if r.Reason == "duplicate_order_capture" {
				valid = valid && s.IdentityID != i.ID
			} else {
				valid = valid && s.IdentityID == i.ID && len(sandboxGraphRelated[SandboxGraphCaptureRow](g, "Captures", []string{"OrderID"}, r.OrderID)) == 0
			}
		case "processing_attempts_exhausted", "command_attempts_exhausted", "query_attempts_exhausted":
			// Reserved rows are not silently blessed. Their common graph remains
			// checked, while no new semantics is invented for a dormant reason.
			g.result.Unproven.ReservedReviewSemanticsUnproven++
			valid = nilRefs
		case "currency_mismatch", "provider_time_invalid", "provider_protocol_invalid":
			valid = false
		default:
			g.bad("InvalidEnumRows", "Reviews", r.ID)
		}
		g.reviews[r.ID] = base && valid
		if !g.reviews[r.ID] {
			g.bad("ReviewRows", "Reviews", r.ID)
		}
	}
	for _, i := range sandboxGraphRows[SandboxGraphIdentityRow](g, "Identities") {
		if !g.tick() {
			return
		}
		selected := false
		for _, s := range sandboxGraphRelated[SandboxGraphSelectionRow](g, "Selections", []string{"IdentityID"}, i.ID) {
			selected = selected || s.OrderID == i.OrderID
		}
		review := false
		for _, r := range sandboxGraphRelated[SandboxGraphReviewRow](g, "Reviews", []string{"IdentityID", "Reason"}, i.ID, "duplicate_order_capture") {
			review = review || g.reviews[r.ID]
		}
		if !selected && !review {
			g.bad("TransactionIdentityRows", "Identities", i.ID)
		}
	}
	for _, s := range sandboxGraphRows[SandboxGraphSelectionRow](g, "Selections") {
		if !g.tick() {
			return
		}
		i, iok := sandboxGraphGet[SandboxGraphIdentityRow](g, "Identities", s.IdentityID)
		e, eok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", s.FirstEvidenceID)
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", s.OrderID)
		p, pok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", s.FirstEvidenceID)
		valid := iok && eok && ook && pok && sandboxGraphTerminal(p) && g.identities[i.ID] && g.facts[e.ID].valid && i.OrderID == s.OrderID && sandboxGraphIdentityTuple(i, e, o)
		captures := sandboxGraphRelated[SandboxGraphCaptureRow](g, "Captures", []string{"OrderID"}, s.OrderID)
		overflow := false
		for _, r := range sandboxGraphRelated[SandboxGraphReviewRow](g, "Reviews", []string{"SelectionOrderID", "Reason"}, s.OrderID, "funding_amount_overflow") {
			if !g.tick() {
				return
			}
			overflow = overflow || g.reviews[r.ID] && sandboxGraphPtr(r.IdentityID) == s.IdentityID
		}
		valid = valid && (len(captures) == 1 && !overflow && captures[0].IdentityID == s.IdentityID || len(captures) == 0 && overflow)
		if !valid {
			g.bad("SelectionRows", "Selections", s.OrderID)
		}
	}
}
func (g *sandboxGraphData) captureFunding() {
	for _, c := range sandboxGraphRows[SandboxGraphCaptureRow](g, "Captures") {
		if !g.tick() {
			return
		}
		o, ook := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", c.OrderID)
		i, iok := sandboxGraphGet[SandboxGraphIdentityRow](g, "Identities", c.IdentityID)
		s, sok := sandboxGraphGet[SandboxGraphSelectionRow](g, "Selections", c.OrderID)
		e, eok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", c.VerifiedEvidenceID)
		p, pok := sandboxGraphGet[SandboxGraphProcessingRow](g, "Processing", c.VerifiedEvidenceID)
		valid := fundingGraphUUID(c.ID) && ook && iok && sok && eok && pok && g.identities[i.ID] && g.facts[e.ID].valid && s.IdentityID == i.ID && sandboxGraphIdentityTuple(i, e, o) && c.TenantID == i.TenantID && c.EnvironmentID == i.EnvironmentID && c.MerchantID == i.MerchantID && c.ProviderTransactionID == i.ProviderTransactionID && c.Amount == i.Amount && c.WalletCreditAmount == o.WalletCreditAmount && c.Amount == c.WalletCreditAmount && c.Currency == i.Currency && c.CapturedAt.Equal(i.CapturedAt) && c.ProviderOrderRevision == i.ProviderOrderRevision && p.State == "processed" && p.Generation == c.ProcessingGeneration && sandboxGraphMoney(c.ProcessingGeneration)
		if !valid {
			g.bad("CaptureRows", "Captures", c.ID)
		}
		g.validCaptures[c.ID] = valid
		x, xok := sandboxGraphGet[SandboxGraphEffectRow](g, "Effects", c.ID)
		if !xok || x.IdentityID != c.IdentityID || x.EnvironmentID != c.EnvironmentID || x.MerchantID != c.MerchantID || x.ProviderTransactionID != c.ProviderTransactionID || x.EffectKind != "capture" || x.EffectID != "capture" {
			g.bad("EffectRows", "Captures", c.ID)
		}
		receipts := sandboxGraphRelated[FundingGraphReceiptRow](g, "Receipts", []string{"SourceKind", "SourceID"}, "sandbox_capture", c.ID)
		if len(receipts) != 1 {
			g.bad("ReceiptRows", "Captures", c.ID)
		}
		var receipt FundingGraphReceiptRow
		if len(receipts) > 0 {
			receipt = receipts[0]
		}
		if len(receipts) == 1 && (receipt.TenantID != c.TenantID || receipt.Amount != c.WalletCreditAmount || receipt.Currency != "CNY") {
			g.bad("ReceiptRows", "Receipts", receipt.ID)
		}
		j, jok := sandboxGraphGet[FundingGraphJournalRow](g, "Journals", receipt.JournalID)
		journalOK := len(receipts) == 1 && jok && j.TenantID == c.TenantID && j.OperationID == c.ID && j.Kind == "sandbox_capture" && j.Reference == c.OrderID && j.Actor == "payment-sandbox-worker" && j.Currency == "CNY"
		if !journalOK {
			g.bad("JournalRows", "Captures", c.ID)
		}
		lines := sandboxGraphRelated[LedgerLineRow](g, "Lines", []string{"JournalID"}, j.ID)
		lineOK := jok && len(lines) == 2
		available, clearing := false, false
		amount, aok := procurementAttemptInt64(c.WalletCreditAmount)
		for _, l := range lines {
			if !g.tick() {
				return
			}
			a, ok := sandboxGraphGet[LedgerAccountRow](g, "Accounts", l.AccountID)
			n, nok := ledgerInteger(l.SignedDelta)
			if !ok || !nok || !aok {
				lineOK = false
				continue
			}
			delta := big.NewInt(n)
			if a.Kind == "AVAILABLE" && a.TenantID != nil && *a.TenantID == c.TenantID && delta.Cmp(amount) == 0 && !available {
				available = true
			} else if a.Kind == "SANDBOX_FUNDING_CLEARING" && a.TenantID == nil && delta.Cmp(new(big.Int).Neg(amount)) == 0 && !clearing {
				clearing = true
			} else {
				lineOK = false
			}
		}
		if !lineOK || !available || !clearing {
			g.bad("JournalLineRows", "Captures", c.ID)
		}
		outbox := sandboxGraphRelated[ProcurementOutboxRow](g, "Outbox", []string{"OperationID"}, c.ID)
		credited := 0
		for _, b := range outbox {
			if !g.tick() {
				return
			}
			if b.Kind == "credited" {
				credited++
				if b.TenantID != c.TenantID || b.SchemaVersion != 1 || !sandboxGraphJSONAmount(b.Payload, c.WalletCreditAmount) {
					g.bad("CreditedRows", "Outbox", b.ID)
				}
			} else {
				g.bad("CreditedRows", "Outbox", b.ID)
			}
		}
		if credited != 1 {
			g.bad("CreditedRows", "Captures", c.ID)
		}
		audits := g.audit("payment.capture.credited", c.ID)
		if len(audits) != 1 {
			g.bad("CaptureAuditRows", "Captures", c.ID)
		}
		for _, a := range audits {
			if a.Actor != "payment-sandbox-worker" || a.TenantScope == nil || *a.TenantScope != c.TenantID || a.RequestID != "" || !sandboxGraphJSONAmount(a.Diff, c.WalletCreditAmount) {
				g.bad("CaptureAuditRows", "Audits", a.ID)
			}
		}
	}
	for _, p := range sandboxGraphRows[SandboxGraphProcessingRow](g, "Processing") {
		if !g.tick() {
			return
		}
		e, eok := sandboxGraphGet[SandboxGraphEvidenceRow](g, "Evidence", p.EvidenceID)
		outcome := eok && g.facts[e.ID].valid
		reviews := sandboxGraphRelated[SandboxGraphReviewRow](g, "Reviews", []string{"EvidenceID"}, p.EvidenceID)
		legalReviews := 0
		for _, r := range reviews {
			if g.reviews[r.ID] {
				legalReviews++
			}
		}
		if p.State == "processing_review" {
			outcome = outcome && (sandboxGraphCode(p.LastErrorCode, "permanent") && legalReviews == 1 && len(reviews) == 1 || p.AttemptCount == "20" && sandboxGraphCode(p.LastErrorCode, "attempts_exhausted") && len(reviews) == 0)
		} else if p.State == "processed" {
			outcome = outcome && len(reviews) == 0
			switch e.Kind {
			case "observation":
			case "closed":
				r, ok := g.revision(e)
				outcome = outcome && ok && r.Kind == "closed" && r.FactDigest == e.FactDigest
			case "capture":
				found := false
				for _, i := range sandboxGraphRelated[SandboxGraphIdentityRow](g, "Identities", []string{"EnvironmentID", "MerchantID", "ProviderTransactionID"}, e.EnvironmentID, e.MerchantID, sandboxGraphPtr(e.ProviderTransactionID)) {
					if !g.tick() {
						return
					}
					o, ok := sandboxGraphGet[SandboxGraphOrderRow](g, "Orders", e.OrderID)
					if ok && g.identities[i.ID] && sandboxGraphIdentityTuple(i, e, o) {
						for _, c := range sandboxGraphRelated[SandboxGraphCaptureRow](g, "Captures", []string{"IdentityID"}, i.ID) {
							found = found || g.validCaptures[c.ID]
						}
					}
				}
				outcome = outcome && found
			default:
				outcome = false
			}
		} else {
			outcome = outcome && len(reviews) == 0 && len(sandboxGraphRelated[SandboxGraphIdentityRow](g, "Identities", []string{"EvidenceID"}, p.EvidenceID)) == 0 && len(sandboxGraphRelated[SandboxGraphCaptureRow](g, "Captures", []string{"VerifiedEvidenceID"}, p.EvidenceID)) == 0 && len(sandboxGraphRelated[SandboxGraphProviderRevisionRow](g, "ProviderRevisions", []string{"EvidenceID"}, p.EvidenceID)) == 0
		}
		if !outcome {
			g.bad("ProcessingOutcomeRows", "Processing", p.EvidenceID)
		}
	}
}
func (g *sandboxGraphData) reverse() {
	for _, x := range sandboxGraphRows[SandboxGraphEffectRow](g, "Effects") {
		if !g.tick() {
			return
		}
		if !g.exists("Captures", x.CaptureID) {
			g.bad("EffectRows", "Effects", x.CaptureID)
		}
	}
	for _, r := range sandboxGraphRows[FundingGraphReceiptRow](g, "Receipts") {
		if !g.tick() {
			return
		}
		if r.SourceKind == "sandbox_capture" {
			if !g.exists("Captures", r.SourceID) {
				g.bad("ReverseSourceRows", "Receipts", r.ID)
			}
		} else if !sandboxGraphHas(r.SourceKind, "manual_credit", "voucher_grant") {
			g.bad("UnknownSourceKindRows", "Receipts", r.ID)
		}
	}
	for _, j := range sandboxGraphRows[FundingGraphJournalRow](g, "Journals") {
		if !g.tick() {
			return
		}
		if j.Kind == "sandbox_capture" {
			c, ok := sandboxGraphGet[SandboxGraphCaptureRow](g, "Captures", j.OperationID)
			rs := sandboxGraphRelated[FundingGraphReceiptRow](g, "Receipts", []string{"JournalID"}, j.ID)
			if !ok || !g.validCaptures[c.ID] || len(rs) != 1 || rs[0].SourceKind != "sandbox_capture" || rs[0].SourceID != c.ID {
				g.bad("ReverseSourceRows", "Journals", j.ID)
			}
		}
	}
	for _, l := range sandboxGraphRows[LedgerLineRow](g, "Lines") {
		if !g.tick() {
			return
		}
		a, aok := sandboxGraphGet[LedgerAccountRow](g, "Accounts", l.AccountID)
		j, jok := sandboxGraphGet[FundingGraphJournalRow](g, "Journals", l.JournalID)
		if !aok || !jok || a.TenantID != nil && *a.TenantID != j.TenantID {
			g.bad("LineIdentityRows", "Lines", l.ID)
		}
		if aok && a.Kind == "SANDBOX_FUNDING_CLEARING" && (a.TenantID != nil || !jok || j.Kind != "sandbox_capture" || !g.exists("Captures", j.OperationID)) {
			g.bad("ReverseSourceRows", "Lines", l.ID)
		}
	}
	for _, a := range sandboxGraphRows[LedgerAccountRow](g, "Accounts") {
		if !g.tick() {
			return
		}
		n, ok := ledgerInteger(a.Balance)
		if !ok {
			g.bad("InvalidNumberRows", "Accounts", a.ID)
		}
		if a.Kind == "SANDBOX_FUNDING_CLEARING" && (a.TenantID != nil || !ok || n > 0) {
			g.bad("ReverseSourceRows", "Accounts", a.ID)
		}
	}
	for _, a := range sandboxGraphRows[ProcurementAuditRow](g, "Audits") {
		if !g.tick() {
			return
		}
		if sandboxGraphPaymentKind(a.Action) {
			c, ok := sandboxGraphGet[FundingGraphCommerceCommandRow](g, "Commands", a.Target)
			r, rok := sandboxGraphGet[FundingGraphCommerceReceiptRow](g, "CommerceReceipts", a.Target)
			if !ok || !rok || c.Kind != a.Action || r.Status != "accepted" {
				g.bad("HumanAuditRows", "Audits", a.ID)
			}
		} else if a.Action == "payment.capture.credited" {
			if !g.exists("Captures", a.Target) {
				g.bad("ReverseSourceRows", "Audits", a.ID)
			}
		} else if a.Action == "payment.policy.update" {
			valid := sandboxGraphPolicyDiff(a.Diff) && a.TenantScope == nil && a.Target == "payment_policy" && g.exists("Users", a.Actor)
			if !valid {
				g.bad("HumanAuditRows", "Audits", a.ID)
			}
		} else if strings.HasPrefix(a.Action, "payment.") {
			g.bad("UnknownSourceKindRows", "Audits", a.ID)
		}
	}
	for _, b := range sandboxGraphRows[ProcurementOutboxRow](g, "Outbox") {
		if !g.tick() {
			return
		}
		if b.Kind == "credited" && !g.exists("Captures", b.OperationID) {
			js := sandboxGraphRelated[FundingGraphJournalRow](g, "Journals", []string{"OperationID"}, b.OperationID)
			if len(js) > 0 && js[0].Kind == "sandbox_capture" {
				g.bad("ReverseSourceRows", "Outbox", b.ID)
			}
		}
	}
}
func sandboxGraphPolicyDiff(raw string) bool {
	if !procurementCommandJSONUnicode(raw) {
		return false
	}
	d := json.NewDecoder(strings.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return false
	}
	m := map[string]bool{}
	for d.More() {
		t, e := d.Token()
		k, ok := t.(string)
		if e != nil || !ok || k != "before" && k != "after" {
			return false
		}
		if _, ok := m[k]; ok {
			return false
		}
		t, e = d.Token()
		b, ok := t.(bool)
		if e != nil || !ok {
			return false
		}
		m[k] = b
	}
	t, e = d.Token()
	if e != nil || t != json.Delim('}') || len(m) != 2 {
		return false
	}
	if _, e = d.Token(); e != io.EOF {
		return false
	}
	return m["before"] != m["after"]
}
