package billing

import (
	"math"
	"strings"
	"testing"
)

func TestBuildJournalPlanRejectsBeforeAnyDatabaseWrite(t *testing.T) {
	accounts := []lockedAccount{{id: "a", kind: "AVAILABLE", balance: 10}, {id: "b", kind: "REVENUE", balance: 0}}
	for name, deltas := range map[string]map[string]int64{
		"unbalanced":      {"AVAILABLE": -4, "REVENUE": 3},
		"missing account": {"AVAILABLE": -4, "HELD": 4},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buildJournalPlan("00000000-0000-0000-0000-000000000001", "tenant", "operation", "reserve", "request", "actor", deltas, accounts)
			if err == nil {
				t.Fatal("invalid journal plan was accepted")
			}
			if name == "unbalanced" && !strings.Contains(err.Error(), "unbalanced_journal") {
				t.Fatalf("wrong error: %v", err)
			}
			if name == "missing account" && !strings.Contains(err.Error(), "missing_account") {
				t.Fatalf("wrong error: %v", err)
			}
		})
	}
}

func TestBuildJournalPlanRejectsOverflowAndInsufficientBalanceBeforeWrite(t *testing.T) {
	tests := []struct {
		name    string
		balance int64
		delta   int64
		want    string
	}{
		{name: "overflow", balance: math.MaxInt64, delta: 1, want: "amount_overflow"},
		{name: "insufficient", balance: 2, delta: -3, want: "insufficient_balance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := buildJournalPlan("00000000-0000-0000-0000-000000000001", "tenant", "operation", "reserve", "request", "actor", map[string]int64{"AVAILABLE": test.delta, "REVENUE": -test.delta}, []lockedAccount{
				{id: "a", kind: "AVAILABLE", balance: test.balance},
				{id: "b", kind: "REVENUE", balance: 0},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %s", err, test.want)
			}
		})
	}
}

func TestBuildJournalPlanIsDeterministicAndContainsCompleteWriteSet(t *testing.T) {
	plan, err := buildJournalPlan("00000000-0000-0000-0000-000000000001", "tenant", "operation", "reserve", "request", "actor", map[string]int64{"REVENUE": 7, "AVAILABLE": -7}, []lockedAccount{
		{id: "z", kind: "REVENUE", balance: 2},
		{id: "a", kind: "AVAILABLE", balance: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.accounts) != 2 || plan.accounts[0].id != "a" || plan.accounts[1].id != "z" {
		t.Fatalf("write set is not deterministic: %+v", plan.accounts)
	}
	if plan.accounts[0].balance != 3 || plan.accounts[1].balance != 9 {
		t.Fatalf("wrong projected balances: %+v", plan.accounts)
	}
}
