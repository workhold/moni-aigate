package fundinglotinit

import (
	"math/big"

	"github.com/moni-ai/moni-aigate/internal/fundinglotverify"
)

type expectedLine struct {
	key   accountKey
	delta int64
}

func customer(tenant, kind string, delta int64) expectedLine {
	return expectedLine{accountKey{tenant: tenant, kind: kind}, delta}
}
func platform(kind string, delta int64) expectedLine {
	return expectedLine{accountKey{null: true, kind: kind}, delta}
}
func unfinished(status string) bool {
	return status == "reserved" || status == "pending_reconciliation"
}
func walletState(status string) bool {
	return unfinished(status) || status == "settled" || status == "released"
}
func walletJournal(kind string) bool {
	switch kind {
	case "credit", "voucher_grant", "sandbox_capture", "reserve", "settled", "released", "refund":
		return true
	}
	return false
}
func pointer(s string) *string { return &s }

func (w *work) validateSources() error {
	w.requests = make(map[string]fundinglotverify.WalletLotsRequestRow, len(w.in.Rows.Requests))
	for _, r := range w.in.Rows.Requests {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.requests[r.ID]; ok {
			return ErrInput
		}
		if _, ok := w.tenants[r.TenantID]; !ok || !walletState(r.BillingStatus) {
			return ErrInput
		}
		w.requests[r.ID] = r
	}
	w.reservations = make(map[string]*reservation, len(w.in.Rows.Reservations))
	w.resByRequest = map[string]*reservation{}
	for _, r := range w.in.Rows.Reservations {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.reservations[r.ID]; ok {
			return ErrInput
		}
		if _, ok := w.resByRequest[r.RequestID]; ok {
			return ErrInput
		}
		q, ok := w.requests[r.RequestID]
		if !ok || q.TenantID != r.TenantID || q.BillingStatus != r.Status || !walletState(r.Status) {
			return ErrInput
		}
		n, _ := amount(r.Amount, false)
		j := w.journalOps[r.ID]
		if err := w.sourceJournal(j, r.TenantID, r.ID, "reserve", &r.RequestID, &q.OwnerID, []expectedLine{customer(r.TenantID, "AVAILABLE", -n), customer(r.TenantID, "HELD", n)}); err != nil {
			return err
		}
		v := &reservation{row: r, amount: n, journal: j}
		w.reservations[r.ID] = v
		w.resByRequest[r.RequestID] = v
		t := w.tenants[r.TenantID]
		t.available.Sub(&t.available, big.NewInt(n))
		t.held.Add(&t.held, big.NewInt(n))
		if unfinished(r.Status) {
			t.unfinished.Add(&t.unfinished, big.NewInt(n))
		}
		w.sources = append(w.sources, source{kind: "reserve", id: r.ID, tenant: r.TenantID, amount: r.Amount, journal: j, request: pointer(r.RequestID), reservation: pointer(r.ID)})
	}
	for _, r := range w.in.Rows.Requests {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.resByRequest[r.ID]; !ok {
			return ErrInput
		}
	}
	w.settlements = make(map[string]*settlement, len(w.in.Rows.Settlements))
	w.settByRequest = map[string]*settlement{}
	for _, r := range w.in.Rows.Settlements {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.settlements[r.ID]; ok {
			return ErrInput
		}
		if _, ok := w.settByRequest[r.RequestID]; ok {
			return ErrInput
		}
		v, ok := w.resByRequest[r.RequestID]
		if !ok || v.row.TenantID != r.TenantID || v.row.Status != r.Status || r.Status != "settled" && r.Status != "released" {
			return ErrInput
		}
		c, _ := amount(r.ChargedAmount, false)
		f, _ := amount(r.RefundedAmount, false)
		if c > v.amount || f > c || r.Status == "released" && c != 0 {
			return ErrInput
		}
		lines := []expectedLine{customer(r.TenantID, "AVAILABLE", v.amount-c), customer(r.TenantID, "HELD", -v.amount)}
		if c > 0 {
			lines = append(lines, platform("REVENUE", c))
		}
		j := w.journals[r.JournalID]
		actor := "system"
		if err := w.sourceJournal(j, r.TenantID, r.ID, r.Status, &r.RequestID, &actor, lines); err != nil {
			return err
		}
		s := &settlement{row: r, charged: c, refunded: f, reservation: v}
		w.settlements[r.ID] = s
		w.settByRequest[r.RequestID] = s
		t := w.tenants[r.TenantID]
		t.available.Add(&t.available, big.NewInt(v.amount-c))
		t.held.Sub(&t.held, big.NewInt(v.amount))
		w.sources = append(w.sources, source{kind: "finish", id: r.ID, tenant: r.TenantID, amount: r.ChargedAmount, journal: j, request: pointer(r.RequestID), reservation: pointer(v.row.ID), settlement: pointer(r.ID)})
	}
	for _, r := range w.in.Rows.Reservations {
		if err := w.check(); err != nil {
			return err
		}
		_, terminal := w.settByRequest[r.RequestID]
		if unfinished(r.Status) == terminal {
			return ErrInput
		}
	}
	if err := w.receiptSources(); err != nil {
		return err
	}
	if err := w.refundSources(); err != nil {
		return err
	}
	for _, r := range w.in.Rows.Settlements {
		if err := w.check(); err != nil {
			return err
		}
		s := w.settlements[r.ID]
		if s.approved.Cmp(big.NewInt(s.refunded)) != 0 || s.approved.Cmp(big.NewInt(s.charged)) > 0 {
			return ErrInput
		}
	}
	// Every customer-affecting journal has exactly one explanation. Claims are
	// unique regardless of zero deltas or the declared journal kind.
	for _, r := range w.in.Rows.Journals {
		if err := w.check(); err != nil {
			return err
		}
		j := w.journals[r.ID]
		wallet := walletJournal(r.Kind)
		for _, l := range j.lines {
			if err := w.check(); err != nil {
				return err
			}
			if l.account.row.Kind == "AVAILABLE" || l.account.row.Kind == "HELD" {
				wallet = true
			}
		}
		if wallet && !j.claimed {
			return ErrInput
		}
	}
	return nil
}

func (w *work) receiptSources() error {
	ids := make(map[string]struct{}, len(w.in.Rows.Receipts))
	keys := make(map[[2]string]struct{}, len(w.in.Rows.Receipts))
	for _, r := range w.in.Rows.Receipts {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := ids[r.ID]; ok {
			return ErrInput
		}
		ids[r.ID] = struct{}{}
		key := [2]string{r.SourceKind, r.SourceID}
		if _, ok := keys[key]; ok {
			return ErrInput
		}
		keys[key] = struct{}{}
		t, ok := w.tenants[r.TenantID]
		if !ok || r.Currency != "CNY" {
			return ErrInput
		}
		n, ok := amount(r.Amount, true)
		if !ok {
			return ErrInput
		}
		j := w.journals[r.JournalID]
		operation := r.SourceID
		reference := &r.SourceID
		kind, clearing := "credit", "FUNDING_CLEARING"
		switch r.SourceKind {
		case "manual_credit":
		case "voucher_grant":
			kind, clearing = "voucher_grant", "OPERATING_GRANT_CLEARING"
			if j == nil {
				return ErrInput
			}
			operation = j.row.OperationID
		case "sandbox_capture":
			if w.in.Mode != "sandbox" {
				return ErrInput
			}
			kind, clearing = "sandbox_capture", "SANDBOX_FUNDING_CLEARING"
			reference = nil
		default:
			return ErrInput
		}
		if err := w.sourceJournal(j, r.TenantID, operation, kind, reference, nil, []expectedLine{customer(r.TenantID, "AVAILABLE", n), platform(clearing, -n)}); err != nil {
			return err
		}
		t.available.Add(&t.available, big.NewInt(n))
		w.sources = append(w.sources, source{kind: r.SourceKind, id: r.ID, tenant: r.TenantID, amount: r.Amount, journal: j})
	}
	return nil
}

func (w *work) refundSources() error {
	ids := make(map[string]struct{}, len(w.in.Rows.Refunds))
	for _, r := range w.in.Rows.Refunds {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := ids[r.ID]; ok {
			return ErrInput
		}
		ids[r.ID] = struct{}{}
		n, ok := amount(r.Amount, true)
		s, sok := w.settlements[r.SettlementID]
		if !ok || !sok || s.row.TenantID != r.TenantID {
			return ErrInput
		}
		if r.State == "pending" {
			if r.Approver != nil || r.JournalID != nil {
				return ErrInput
			}
			continue
		}
		if r.State != "approved" || r.Approver == nil || *r.Approver == r.Requester || r.JournalID == nil || n > s.charged {
			return ErrInput
		}
		j := w.journals[*r.JournalID]
		if err := w.sourceJournal(j, r.TenantID, r.ID, "refund", &r.SettlementID, r.Approver, []expectedLine{customer(r.TenantID, "AVAILABLE", n), platform("REVENUE", -n)}); err != nil {
			return err
		}
		s.approved.Add(&s.approved, big.NewInt(n))
		t := w.tenants[r.TenantID]
		t.available.Add(&t.available, big.NewInt(n))
		w.sources = append(w.sources, source{kind: "consumption_refund", id: r.ID, tenant: r.TenantID, amount: r.Amount, journal: j, request: pointer(s.row.RequestID), reservation: pointer(s.reservation.row.ID), settlement: pointer(r.SettlementID)})
	}
	return nil
}

func (w *work) sourceJournal(j *journal, tenant, operation, kind string, reference, actor *string, expected []expectedLine) error {
	if err := w.check(); err != nil {
		return err
	}
	if j == nil || j.claimed || j.row.TenantID != tenant || j.row.OperationID != operation || j.row.Kind != kind || j.row.Currency != "CNY" || reference != nil && j.row.Reference != *reference || actor != nil && j.row.Actor != *actor || len(j.lines) != len(expected) {
		return ErrInput
	}
	seen := [3]bool{}
	for _, l := range j.lines {
		if err := w.check(); err != nil {
			return err
		}
		found := false
		for i, e := range expected {
			if err := w.check(); err != nil {
				return err
			}
			key := accountKey{null: l.account.row.TenantID == nil, kind: l.account.row.Kind}
			if l.account.row.TenantID != nil {
				key.tenant = *l.account.row.TenantID
			}
			if !seen[i] && key == e.key && l.delta == e.delta {
				seen[i] = true
				found = true
				break
			}
		}
		if !found {
			return ErrInput
		}
	}
	j.claimed = true
	return nil
}
