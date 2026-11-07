package billing

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type finishTailFailure struct {
	name  string
	table string
	event string
}

func finishTailSnapshot(t *testing.T, f *fixture, requestID string) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'accounts',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM ledger_accounts x),'[]'::jsonb),
 'key',coalesce((SELECT to_jsonb(x) FROM api_keys x WHERE x.id=$1),'{}'::jsonb),
 'request',coalesce((SELECT to_jsonb(x) FROM request_records x WHERE x.id=$2),'{}'::jsonb),
 'reservation',coalesce((SELECT to_jsonb(x) FROM reservations x WHERE x.request_id=$2),'{}'::jsonb),
 'settlements',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM settlements x WHERE x.request_id=$2),'[]'::jsonb),
 'journals',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM journals x WHERE x.reference=$2),'[]'::jsonb),
 'journal_lines',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.journal_id,x.account_id) FROM journal_lines x JOIN journals j ON j.id=x.journal_id WHERE j.reference=$2),'[]'::jsonb),
 'reservation_lots',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.reservation_id,x.allocation_index) FROM funding_reservation_lots x WHERE x.request_id=$2),'[]'::jsonb),
 'settlement_lots',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.settlement_id,x.allocation_index) FROM funding_settlement_lots x JOIN settlements s ON s.id=x.settlement_id WHERE s.request_id=$2),'[]'::jsonb),
 'lot_balances',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.lot_id) FROM funding_lot_balances x WHERE x.tenant_id=$3),'[]'::jsonb),
 'lot_events',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM funding_lot_events x WHERE x.tenant_id=$3),'[]'::jsonb),
 'recovery',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.request_id) FROM recovery_jobs x WHERE x.request_id=$2),'[]'::jsonb),
 'reconciliation',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.request_id) FROM reconciliation_cases x WHERE x.request_id=$2),'[]'::jsonb),
 'audit',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.id) FROM audit_events x WHERE x.request_id=$2),'[]'::jsonb),
 'outbox',coalesce((SELECT jsonb_agg(to_jsonb(x) ORDER BY x.event_id) FROM projection_outbox x WHERE x.tenant_id=$3),'[]'::jsonb)
)::text`, f.key, requestID, f.tenant).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPostgresFinishTailWriteFailuresRollbackAndRetry(t *testing.T) {
	cases := []finishTailFailure{
		{name: "settlements", table: "settlements", event: "INSERT"},
		{name: "funding_settlement_lots", table: "funding_settlement_lots", event: "INSERT"},
		{name: "funding_lot_events", table: "funding_lot_events", event: "INSERT"},
		{name: "funding_lot_balances", table: "funding_lot_balances", event: "UPDATE"},
		{name: "api_keys", table: "api_keys", event: "UPDATE"},
		{name: "reservations", table: "reservations", event: "UPDATE"},
		{name: "request_records", table: "request_records", event: "UPDATE"},
		{name: "recovery_jobs", table: "recovery_jobs", event: "DELETE"},
		{name: "audit_events", table: "audit_events", event: "INSERT"},
		{name: "projection_outbox", table: "projection_outbox", event: "INSERT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := integration(t)
			r := f.reserve(t)
			f.final(t, r, confirmed(), "completed", "done", "content", true)
			before := finishTailSnapshot(t, f, r.RequestID)

			if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION finish_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'finish tail injected failure'; END$$`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.pool.Exec(f.ctx, "CREATE TRIGGER finish_injected_failure BEFORE "+tc.event+" ON "+pgx.Identifier{tc.table}.Sanitize()+" FOR EACH ROW EXECUTE FUNCTION finish_injected_failure()"); err != nil {
				t.Fatal(err)
			}

			_, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "P0001" || !strings.Contains(databaseError.Message, "finish tail injected failure") {
				t.Fatalf("finish tail failure was lost: %v", err)
			}
			if after := finishTailSnapshot(t, f, r.RequestID); after != before {
				t.Fatalf("finish tail partially committed for %s before=%s after=%s", tc.name, before, after)
			}

			if _, err = f.pool.Exec(f.ctx, "DROP TRIGGER finish_injected_failure ON "+pgx.Identifier{tc.table}.Sanitize()); err != nil {
				t.Fatal(err)
			}
			settled, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || settled.Status != "settled" || settled.ChargedAmount != 15 {
				t.Fatalf("finish retry did not settle once: %+v %v", settled, err)
			}
			replayed, err := f.store.Settle(f.ctx, SettleInput{TenantID: f.tenant, RequestID: r.RequestID})
			if err != nil || replayed.ID != settled.ID || replayed.JournalID != settled.JournalID {
				t.Fatalf("finish replay changed financial identity: first=%+v replay=%+v err=%v", settled, replayed, err)
			}
			f.wallet(t, 985, 0)
			f.check(t)
		})
	}
}
