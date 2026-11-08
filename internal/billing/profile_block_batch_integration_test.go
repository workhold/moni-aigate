package billing

import "testing"

type profileBlockSettlementState struct {
	held, spent                       int64
	reservationStatus, billingStatus  string
	settlements, settlementJournals   int64
	profileBlocks, profileBlockAudits int64
}

func profileBlockSettlementSnapshot(t *testing.T, f *fixture, request string) profileBlockSettlementState {
	t.Helper()
	var state profileBlockSettlementState
	if err := f.pool.QueryRow(f.ctx, `
SELECT k.held,k.spent,r.status,rr.billing_status,
 (SELECT count(*) FROM settlements WHERE request_id=$2),
 (SELECT count(*) FROM journals WHERE reference=$2 AND kind='settled'),
 (SELECT count(*) FROM billing_profile_blocks WHERE request_id=$2),
 (SELECT count(*) FROM audit_events WHERE action='billing.profile_blocked' AND request_id=$2)
FROM api_keys k
JOIN reservations r ON r.tenant_id=k.tenant_id AND r.request_id=$2
JOIN request_records rr ON rr.tenant_id=r.tenant_id AND rr.id=r.request_id
WHERE k.id=$1`, f.key, request).Scan(
		&state.held, &state.spent, &state.reservationStatus, &state.billingStatus,
		&state.settlements, &state.settlementJournals, &state.profileBlocks, &state.profileBlockAudits,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestPostgresProfileBlockBatchRollsBackAndRetriesOnce(t *testing.T) {
	for _, tc := range []struct {
		name, table, function, triggerSQL, dropSQL string
	}{
		{
			name:     "profile_insert",
			table:    "billing_profile_blocks",
			function: "fail_profile_insert",
			triggerSQL: `CREATE FUNCTION fail_profile_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'profile block injection'; END$$;`,
			dropSQL: "DROP FUNCTION fail_profile_insert()",
		},
		{
			name:     "profile_audit",
			table:    "audit_events",
			function: "fail_profile_audit",
			triggerSQL: `CREATE FUNCTION fail_profile_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.action='billing.profile_blocked' THEN RAISE EXCEPTION 'profile audit injection'; END IF;
 RETURN NEW;
END$$;`,
			dropSQL: "DROP FUNCTION fail_profile_audit()",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			defer f.store.EndRequest(r.RequestID)
			f.final(t, r, Usage{Status: "confirmed_final", InputTokens: 200, OutputTokens: 20, TotalTokens: 220}, "completed", "done", "content", true)

			if _, err := f.pool.Exec(f.ctx, tc.triggerSQL+" CREATE TRIGGER injected BEFORE INSERT ON "+tc.table+" FOR EACH ROW EXECUTE FUNCTION "+tc.function+"()"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = f.pool.Exec(f.ctx, "DROP TRIGGER IF EXISTS injected ON "+tc.table)
				_, _ = f.pool.Exec(f.ctx, tc.dropSQL)
			})

			before := profileBlockSettlementSnapshot(t, f, r.RequestID)
			if _, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID}); err == nil {
				t.Fatal("profile block failure unexpectedly committed")
			}
			if after := profileBlockSettlementSnapshot(t, f, r.RequestID); after != before {
				t.Fatalf("profile block failure partially committed: before=%+v after=%+v", before, after)
			}

			if _, err := f.pool.Exec(f.ctx, "DROP TRIGGER injected ON "+tc.table); err != nil {
				t.Fatal(err)
			}
			settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || settled.Status != "settled" || settled.ChargedAmount != r.Amount {
				t.Fatalf("retry did not settle exactly once: %+v %v", settled, err)
			}
			replay, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || replay.ID != settled.ID {
				t.Fatalf("settlement replay changed identity: %+v %v", replay, err)
			}
			final := profileBlockSettlementSnapshot(t, f, r.RequestID)
			if final.held != 0 || final.spent != int64(r.Amount) || final.reservationStatus != "settled" || final.billingStatus != "settled" || final.settlements != 1 || final.settlementJournals != 1 || final.profileBlocks != 1 || final.profileBlockAudits != 1 {
				t.Fatalf("unexpected profile block settlement facts: %+v", final)
			}
			f.check(t)
		})
	}
}
