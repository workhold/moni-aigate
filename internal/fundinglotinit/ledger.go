package fundinglotinit

import "math/big"

func (w *work) validateLedger() error {
	w.tenants = make(map[string]*tenant, len(w.in.Rows.Tenants))
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.tenants[r.ID]; ok {
			return ErrInput
		}
		w.tenants[r.ID] = &tenant{id: r.ID}
	}
	w.accounts = make(map[string]*account, len(w.in.Rows.Accounts))
	w.accountKinds = map[accountKey]*account{}
	for _, r := range w.in.Rows.Accounts {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.accounts[r.ID]; ok {
			return ErrInput
		}
		n, _ := signed(r.Balance)
		k := accountKey{null: r.TenantID == nil, kind: r.Kind}
		if r.TenantID != nil {
			k.tenant = *r.TenantID
			if _, ok := w.tenants[*r.TenantID]; !ok {
				return ErrInput
			}
		}
		switch r.Kind {
		case "AVAILABLE", "HELD":
			if k.null || n < 0 {
				return ErrInput
			}
		case "FUNDING_CLEARING", "REVENUE":
			if !k.null {
				return ErrInput
			}
		case "OPERATING_GRANT_CLEARING", "SANDBOX_FUNDING_CLEARING":
			if !k.null || n > 0 {
				return ErrInput
			}
		default:
			return ErrInput
		}
		if _, ok := w.accountKinds[k]; ok {
			return ErrInput
		}
		a := &account{row: r, balance: n}
		w.accounts[r.ID] = a
		w.accountKinds[k] = a
	}
	w.journals = make(map[string]*journal, len(w.in.Rows.Journals))
	w.journalOps = make(map[string]*journal, len(w.in.Rows.Journals))
	for _, r := range w.in.Rows.Journals {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := w.journals[r.ID]; ok {
			return ErrInput
		}
		if _, ok := w.journalOps[r.OperationID]; ok {
			return ErrInput
		}
		if _, ok := w.tenants[r.TenantID]; !ok {
			return ErrInput
		}
		if r.Currency != "CNY" {
			return ErrInput
		}
		j := &journal{row: r}
		w.journals[r.ID] = j
		w.journalOps[r.OperationID] = j
	}
	ids := make(map[string]struct{}, len(w.in.Rows.Lines))
	pairs := make(map[[2]string]struct{}, len(w.in.Rows.Lines))
	for _, r := range w.in.Rows.Lines {
		if err := w.check(); err != nil {
			return err
		}
		if _, ok := ids[r.ID]; ok {
			return ErrInput
		}
		ids[r.ID] = struct{}{}
		key := [2]string{r.JournalID, r.AccountID}
		if _, ok := pairs[key]; ok {
			return ErrInput
		}
		pairs[key] = struct{}{}
		a, aok := w.accounts[r.AccountID]
		j, jok := w.journals[r.JournalID]
		if !aok || !jok || a.row.TenantID != nil && *a.row.TenantID != j.row.TenantID {
			return ErrInput
		}
		n, _ := signed(r.SignedDelta)
		a.sum.Add(&a.sum, big.NewInt(n))
		j.sum.Add(&j.sum, big.NewInt(n))
		j.lines = append(j.lines, line{account: a, delta: n})
	}
	for _, r := range w.in.Rows.Accounts {
		if err := w.check(); err != nil {
			return err
		}
		a := w.accounts[r.ID]
		if a.sum.Cmp(big.NewInt(a.balance)) != 0 {
			return ErrInput
		}
	}
	for _, r := range w.in.Rows.Journals {
		if err := w.check(); err != nil {
			return err
		}
		j := w.journals[r.ID]
		if len(j.lines) < 2 || j.sum.Sign() != 0 {
			return ErrInput
		}
	}
	return nil
}

func (w *work) checkBalances() error {
	for _, r := range w.in.Rows.Tenants {
		if err := w.check(); err != nil {
			return err
		}
		t := w.tenants[r.ID]
		if t.available.Sign() < 0 || t.held.Sign() < 0 || !t.available.IsInt64() || !t.held.IsInt64() || t.held.Cmp(&t.unfinished) != 0 {
			return ErrInput
		}
		for _, p := range []struct {
			kind string
			n    *big.Int
		}{{"AVAILABLE", &t.available}, {"HELD", &t.held}} {
			if err := w.check(); err != nil {
				return err
			}
			a := w.accountKinds[accountKey{tenant: r.ID, kind: p.kind}]
			if a == nil {
				if p.n.Sign() != 0 {
					return ErrInput
				}
				continue
			}
			if a.sum.Cmp(p.n) != 0 || big.NewInt(a.balance).Cmp(p.n) != 0 {
				return ErrInput
			}
		}
	}
	return nil
}
