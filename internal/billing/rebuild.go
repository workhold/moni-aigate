package billing

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Rebuild requires the process-wide maintenance gate: all business writers and
// workers must be stopped while the caller retains the exclusive instance lock.
// The old projection remains active until receipts, source totals, and ledger
// checks succeed. Crashes leave a resumable inactive consumer.
func (s *Store) Rebuild(ctx context.Context, consumer string) (int, error) {
	if !boundedString(consumer, 80) {
		return 0, fail("invalid_consumer", "fresh consumer name required")
	}
	var current string
	if err := s.pool.QueryRow(ctx, "SELECT consumer FROM active_report_projection WHERE singleton").Scan(&current); err != nil {
		return 0, err
	}
	if current == consumer {
		return 0, fail("active_consumer", "rebuild requires an inactive projection")
	}
	total := 0
	for {
		n, err := s.Project(ctx, ProjectInput{Consumer: consumer, Limit: 200})
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			break
		}
	}
	err := s.transaction(ctx, func(tx pgx.Tx) error {
		var actual string
		if err := tx.QueryRow(ctx, "SELECT consumer FROM active_report_projection WHERE singleton FOR UPDATE").Scan(&actual); err != nil {
			return err
		}
		if actual != current {
			return fail("projection_changed", "active projection changed during rebuild")
		}
		var missing bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projection_outbox o WHERE NOT EXISTS(SELECT 1 FROM projection_receipts r WHERE r.event_id=o.event_id AND r.consumer=$1))`, consumer).Scan(&missing); err != nil {
			return err
		}
		if missing {
			return fail("projection_incomplete", "outbox contains unprocessed facts")
		}
		funding, err := verifyFundingTx(ctx, tx, "")
		if err != nil {
			return err
		}
		if !funding.Balanced {
			return fail("funding_mismatch", "funding sources failed verification")
		}
		// Compare every tenant, including empty tenants, against synchronous financial facts.
		var mismatch bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
   SELECT 1 FROM tenants t LEFT JOIN report_totals p ON p.tenant_id=t.id AND p.consumer=$1 WHERE
   coalesce(p.credited,0)<>coalesce((SELECT sum(amount::numeric) FROM funding_receipts f WHERE f.tenant_id=t.id),0)
   OR coalesce(p.charged,0)<>coalesce((SELECT sum(charged_amount::numeric) FROM settlements s WHERE s.tenant_id=t.id),0)
   OR coalesce(p.refunded,0)<>coalesce((SELECT sum(amount::numeric) FROM refund_orders f WHERE f.tenant_id=t.id AND f.state='approved'),0)
   OR coalesce(p.released,0)<>coalesce((SELECT sum(v.amount::numeric-s.charged_amount::numeric) FROM settlements s JOIN reservations v ON v.request_id=s.request_id WHERE s.tenant_id=t.id),0)
   OR coalesce(p.requests,0)<>(SELECT count(*) FROM request_records r WHERE r.tenant_id=t.id)
  )`, consumer).Scan(&mismatch); err != nil {
			return err
		}
		if mismatch {
			return fail("projection_mismatch", "rebuilt totals differ from retained financial facts")
		}
		var badLedger bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ledger_accounts a WHERE a.balance::numeric<>coalesce((SELECT sum(signed_delta::numeric) FROM journal_lines l WHERE l.account_id=a.id),0)) OR EXISTS(SELECT 1 FROM journals j WHERE (SELECT count(*) FROM journal_lines l WHERE l.journal_id=j.id)<2 OR (SELECT coalesce(sum(signed_delta::numeric),0) FROM journal_lines l WHERE l.journal_id=j.id)<>0)`).Scan(&badLedger); err != nil {
			return err
		}
		if badLedger {
			return fail("ledger_mismatch", "financial ledger failed verification")
		}
		if _, err := tx.Exec(ctx, "UPDATE active_report_projection SET consumer=$1,switched_at=clock_timestamp() WHERE singleton", consumer); err != nil {
			return err
		}
		return audit(ctx, tx, "", "system", "projection.switched", consumer, "", map[string]any{"previous": current, "consumer": consumer})
	})
	return total, err
}
