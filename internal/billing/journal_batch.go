package billing

import (
	"math/big"
	"sort"

	"github.com/google/uuid"
)

// journalPlan is the complete write set for one balanced ledger journal.  It
// is built after the account locks have been acquired, but before any journal
// or balance statement is sent to PostgreSQL.  Keeping this value separate
// from pgx makes the 008 invariant testable without a database and prevents a
// late balance/overflow error from producing a partial batch.
type journalPlan struct {
	id        string
	tenant    string
	operation string
	kind      string
	reference string
	actor     string
	accounts  []journalPlanAccount
}

type journalPlanAccount struct {
	id, kind string
	balance  int64
	delta    int64
}

func buildJournalPlan(id, tenant, operation, kind, reference, actor string, deltas map[string]int64, locked []lockedAccount) (journalPlan, error) {
	var plan journalPlan
	if _, err := uuid.Parse(id); err != nil || tenant == "" || operation == "" || kind == "" || reference == "" || actor == "" {
		return plan, fail("invalid_journal", "journal identity is incomplete")
	}
	if len(deltas) < 2 {
		return plan, fail("unbalanced_journal", "journal entries must balance")
	}

	total := new(big.Int)
	for account, delta := range deltas {
		if account == "" {
			return plan, fail("invalid_account", "journal account is empty")
		}
		total.Add(total, big.NewInt(delta))
	}
	if total.Sign() != 0 {
		return plan, fail("unbalanced_journal", "journal entries must balance")
	}

	accounts := append([]lockedAccount(nil), locked...)
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].id < accounts[j].id })
	seen := make(map[string]bool, len(accounts))
	seenIDs := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		if account.id == "" || account.kind == "" || seen[account.kind] || seenIDs[account.id] {
			return plan, fail("invalid_account", "journal accounts are not unique")
		}
		seen[account.kind] = true
		seenIDs[account.id] = true
	}
	for account := range deltas {
		if !seen[account] {
			return plan, fail("missing_account", "journal account is not locked")
		}
	}

	plan = journalPlan{
		id: id, tenant: tenant, operation: operation, kind: kind,
		reference: reference, actor: actor,
		accounts: make([]journalPlanAccount, 0, len(deltas)),
	}
	for _, account := range accounts {
		delta, ok := deltas[account.kind]
		if !ok {
			continue
		}
		balance, err := checkedAdd(account.balance, delta)
		if err != nil {
			return journalPlan{}, err
		}
		if (account.kind == "AVAILABLE" || account.kind == "HELD" || account.kind == "REFUND_HELD") && balance < 0 {
			return journalPlan{}, fail("insufficient_balance", "insufficient wallet balance")
		}
		plan.accounts = append(plan.accounts, journalPlanAccount{
			id: account.id, kind: account.kind, balance: balance, delta: delta,
		})
	}
	if len(plan.accounts) != len(deltas) {
		return journalPlan{}, fail("missing_account", "journal account is not locked")
	}
	return plan, nil
}
