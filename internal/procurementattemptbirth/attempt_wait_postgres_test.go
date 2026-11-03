package procurementattemptbirth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresA2ConcurrentAccountBudgetHasOneWinner(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 300)
	f.bind(t, a.TargetID)
	requests := []string{f.reserve(t).RequestID, f.reserve(t).RequestID}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, request := range requests {
		go func(id string) { <-start; _, e := f.attempt(id); results <- e }(request)
	}
	close(start)
	winners := 0
	for range requests {
		e := <-results
		if e == nil {
			winners++
		} else {
			sqlstate(t, e, "PP115")
		}
	}
	if winners != 1 {
		t.Fatalf("expected one affordable birth, got %d", winners)
	}
	var exact bool
	must(t, "independent shared budget and cardinality oracle", f.owner.QueryRow(f.ctx, `SELECT
 a.held_amount=240 AND a.spent_amount=0 AND h.ordinal=2 AND h.held_amount=240
 AND (SELECT count(*) FROM procurement_reservations WHERE account_id=a.id)=1
 AND (SELECT count(*) FROM procurement_budget_events WHERE account_id=a.id)=1
 AND (SELECT count(*) FROM pp_attempt_origins WHERE account_id=a.id)=1
 FROM procurement_accounts a JOIN pp_account_heads h ON h.account_id=a.id WHERE a.id=$1`, a.TargetID).Scan(&exact))
	if !exact {
		t.Fatal("concurrent budget admitted excess consumption or partial sources")
	}
}

func TestPostgresA2AccountWaitRechecksQuoteAndRollsBack(t *testing.T) {
	f := newFixture(t)
	a := f.create(t, 1000)
	f.bind(t, a.TargetID)
	r := f.reserve(t)
	before := f.snapshot(t)
	// Original cost publication serializes through the earlier channel lock.
	// Publish its future-effective version before holding account N; publishing
	// from the account blocker would deliberately reverse the production order.
	var effective time.Time
	must(t, "publish authentic scheduled quote", f.owner.QueryRow(f.ctx, "INSERT INTO cost_versions(id,channel_id,upstream_model,input_rate,output_rate,basis,effective_at) VALUES($1,$2,'upstream',3000000,3000000,'contract',clock_timestamp()+interval '1500 milliseconds') RETURNING effective_at", uuid.NewString(), f.channel).Scan(&effective))
	blocker, err := f.owner.Begin(f.ctx)
	must(t, "begin real account blocker", err)
	defer blocker.Rollback(f.ctx)
	_, err = blocker.Exec(f.ctx, "SELECT id FROM procurement_accounts WHERE id=$1 FOR NO KEY UPDATE", a.TargetID)
	must(t, "hold actual account N", err)
	result := make(chan error, 1)
	go func() { _, e := f.attempt(r.RequestID); result <- e }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		must(t, "observe real APP wait", f.owner.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock')", f.b.App).Scan(&waiting))
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("attempt never reached actual account lock wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !time.Now().Before(effective) {
		t.Fatal("attempt did not reach its account wait before scheduled quote became effective")
	}
	for time.Now().Before(effective.Add(20 * time.Millisecond)) {
		time.Sleep(10 * time.Millisecond)
	}
	must(t, "release account after scheduled quote became effective", blocker.Commit(f.ctx))
	sqlstate(t, <-result, "PP412")
	if f.snapshot(t) != before {
		t.Fatal("waited stale quote left partial birth or supplier/customer funds")
	}
}
