package billing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// WalletSourceCheck is a current, read-only integrity observation. Counts are
// diagnostic relations, not amounts, repair instructions, or spending permits.
type WalletSourceCheck struct {
	TenantID             string    `json:"tenant_id"`
	AsOf                 time.Time `json:"as_of"`
	Balanced             bool      `json:"balanced"`
	SourceMismatches     int64     `json:"source_mismatches"`
	EventMismatches      int64     `json:"event_mismatches"`
	AllocationMismatches int64     `json:"allocation_mismatches"`
	WalletMismatches     int64     `json:"wallet_mismatches"`
}

type walletSourcesReadError struct{ cause error }

func (*walletSourcesReadError) Error() string   { return "wallet_sources_unavailable" }
func (e *walletSourcesReadError) Unwrap() error { return e.cause }

func walletSourcesUnavailable(cause error) error {
	return &walletSourcesReadError{cause: cause}
}

// Both roots and edges are visited. Calling validators only for existing lots
// would miss a receipt without a lot, an orphan edge, and a journal without an
// event. NUMERIC sums also avoid BIGINT overflow in intermediate aggregates.
const walletSourcesCheckSQL = `WITH
source_roots AS (
 SELECT c.tenant_id,'manual_credit'::text AS kind,c.id AS source_id,c.amount,
  c.journal_id AS journal_ref,NULL::text AS receipt_ref
 FROM credit_orders c WHERE c.state='approved' AND ($1='' OR c.tenant_id=$1)
 UNION ALL
 SELECT v.tenant_id,'voucher_grant',v.voucher_id,v.amount,v.journal_id,v.funding_receipt_id
 FROM voucher_redemptions v WHERE ($1='' OR v.tenant_id=$1)
 UNION ALL
 SELECT c.tenant_id,'sandbox_capture',c.id,c.wallet_credit_amount,NULL::text,NULL::text
 FROM payment_captures c WHERE ($1='' OR c.tenant_id=$1)
 UNION ALL
 SELECT o.tenant_id,'wallet_opening',o.id,o.amount,NULL::text,NULL::text
 FROM wallet_openings o WHERE ($1='' OR o.tenant_id=$1)
), source_errors AS (
 SELECT 'source'::text AS kind,s.source_id AS id FROM source_roots s
 WHERE NOT EXISTS(SELECT 1 FROM funding_receipts f JOIN funding_lots l
  ON l.id=f.id AND l.funding_receipt_id=f.id AND l.tenant_id=f.tenant_id
  WHERE f.tenant_id=s.tenant_id AND f.source_kind=s.kind AND f.source_id=s.source_id
   AND f.amount=s.amount AND (s.journal_ref IS NULL OR f.journal_id=s.journal_ref)
   AND (s.receipt_ref IS NULL OR f.id=s.receipt_ref) AND funding_journal_valid(f.journal_id))
 UNION ALL
 SELECT 'receipt',f.id FROM funding_receipts f WHERE ($1='' OR f.tenant_id=$1)
  AND (funding_journal_valid(f.journal_id) IS DISTINCT FROM true OR NOT EXISTS(
   SELECT 1 FROM funding_lots l WHERE l.id=f.id AND l.funding_receipt_id=f.id AND l.tenant_id=f.tenant_id
    AND (l.source_kind,l.source_id,l.issued_amount,l.fifo_at)=(f.source_kind,f.source_id,f.amount,f.created_at)))
 UNION ALL
 SELECT 'lot',l.id FROM funding_lots l WHERE ($1='' OR l.tenant_id=$1)
  AND (wallet_lot_valid(l.id) IS DISTINCT FROM true OR l.id<>l.funding_receipt_id
   OR l.issued_amount<=0 OR (l.source_kind='sandbox_capture') IS DISTINCT FROM (l.refund_class='sandbox_original'))
 UNION ALL
 SELECT 'balance',b.lot_id FROM funding_lot_balances b WHERE ($1='' OR b.tenant_id=$1)
  AND NOT EXISTS(SELECT 1 FROM funding_lots l WHERE l.id=b.lot_id AND l.tenant_id=b.tenant_id
   AND b.available_amount>=0 AND b.held_amount>=0 AND b.consumed_amount>=0 AND b.revision>0
   AND b.available_amount::numeric+b.held_amount::numeric+b.consumed_amount::numeric=l.issued_amount::numeric)
), event_prefix AS (
 SELECT e.*,
  sum(e.delta_available::numeric) OVER w AS prefix_available,
  sum(e.delta_held::numeric) OVER w AS prefix_held,
  sum(e.delta_consumed::numeric) OVER w AS prefix_consumed
 FROM funding_lot_events e WHERE ($1='' OR e.tenant_id=$1)
 WINDOW w AS (PARTITION BY e.tenant_id,e.lot_id ORDER BY e.revision ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)
), event_errors AS (
 SELECT 'event'::text AS kind,e.id FROM event_prefix e
 WHERE wallet_event_valid(e.id) IS DISTINCT FROM true
  OR NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=e.journal_id AND j.tenant_id=e.tenant_id
   AND j.operation_id=e.operation_id AND e.created_xid::text::numeric=j.created_txid::numeric)
  OR e.prefix_available<0 OR e.prefix_held<0 OR e.prefix_consumed<0
  OR NOT EXISTS(SELECT 1 FROM funding_lots l WHERE l.id=e.lot_id AND l.tenant_id=e.tenant_id
   AND e.prefix_available+e.prefix_held+e.prefix_consumed=l.issued_amount::numeric)
 UNION ALL
 SELECT 'journal',j.id FROM journals j WHERE ($1='' OR j.tenant_id=$1)
 AND (wallet_journal_valid(j.id) IS DISTINCT FROM true OR j.currency<>'COIN'
  OR (SELECT count(*) FROM journal_lines x WHERE x.journal_id=j.id)<2
  OR (SELECT coalesce(sum(x.signed_delta::numeric),0) FROM journal_lines x WHERE x.journal_id=j.id)<>0
  OR NOT CASE
   WHEN j.kind IN ('credit','voucher_grant','sandbox_capture','wallet_opening') THEN funding_journal_valid(j.id)
   WHEN j.kind='reserve' THEN EXISTS(SELECT 1 FROM reservations r
    WHERE r.id=j.operation_id AND r.tenant_id=j.tenant_id AND r.request_id=j.reference)
   WHEN j.kind IN ('settled','released') THEN EXISTS(SELECT 1 FROM settlements s
    WHERE s.id=j.operation_id AND s.tenant_id=j.tenant_id AND s.request_id=j.reference AND s.journal_id=j.id AND s.status=j.kind)
   WHEN j.kind='refund' THEN EXISTS(SELECT 1 FROM refund_orders r
    WHERE r.id=j.operation_id AND r.tenant_id=j.tenant_id AND r.settlement_id=j.reference AND r.journal_id=j.id AND r.state='approved')
   ELSE false END)
 UNION ALL
 SELECT 'journal_line',x.id FROM journal_lines x
 LEFT JOIN journals j ON j.id=x.journal_id LEFT JOIN ledger_accounts a ON a.id=x.account_id
 WHERE ($1='' OR j.tenant_id=$1 OR a.tenant_id=$1)
  AND (j.id IS NULL OR a.id IS NULL OR (a.tenant_id IS NOT NULL AND a.tenant_id IS DISTINCT FROM j.tenant_id))
), allocation_errors AS (
 SELECT 'request'::text AS kind,q.id FROM request_records q WHERE ($1='' OR q.tenant_id=$1)
  AND NOT EXISTS(SELECT 1 FROM reservations r WHERE r.request_id=q.id AND r.tenant_id=q.tenant_id)
 UNION ALL
 SELECT 'reservation',r.id FROM reservations r WHERE ($1='' OR r.tenant_id=$1)
  AND (wallet_reservation_valid(r.id) IS DISTINCT FROM true
   OR NOT EXISTS(SELECT 1 FROM request_records q WHERE q.id=r.request_id AND q.tenant_id=r.tenant_id)
   OR (r.status IN ('settled','released')) IS DISTINCT FROM EXISTS(
    SELECT 1 FROM settlements s WHERE s.request_id=r.request_id AND s.tenant_id=r.tenant_id)
   OR EXISTS(SELECT 1 FROM settlements s WHERE s.request_id=r.request_id AND s.tenant_id=r.tenant_id AND s.status IS DISTINCT FROM r.status))
 UNION ALL
 SELECT 'reservation_edge',a.reservation_id||':'||a.allocation_index::text
 FROM funding_reservation_lots a WHERE ($1='' OR a.tenant_id=$1)
  AND (NOT EXISTS(SELECT 1 FROM reservations r WHERE r.id=a.reservation_id AND r.tenant_id=a.tenant_id AND r.request_id=a.request_id)
   OR NOT EXISTS(SELECT 1 FROM request_records q WHERE q.id=a.request_id AND q.tenant_id=a.tenant_id)
   OR NOT EXISTS(SELECT 1 FROM funding_lots l WHERE l.id=a.lot_id AND l.tenant_id=a.tenant_id)
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=a.lot_id AND e.tenant_id=a.tenant_id
    AND e.operation_id=a.reservation_id AND e.kind='reserve' AND wallet_event_valid(e.id)))
 UNION ALL
 SELECT 'settlement',s.id FROM settlements s WHERE ($1='' OR s.tenant_id=$1)
  AND (wallet_settlement_valid(s.id) IS DISTINCT FROM true
   OR NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=s.journal_id AND j.tenant_id=s.tenant_id
    AND j.operation_id=s.id AND j.reference=s.request_id AND j.kind=s.status))
 UNION ALL
 SELECT 'settlement_edge',f.settlement_id||':'||f.allocation_index::text
 FROM funding_settlement_lots f WHERE ($1='' OR f.tenant_id=$1)
 AND (NOT EXISTS(SELECT 1 FROM settlements s JOIN reservations r ON r.request_id=s.request_id AND r.tenant_id=s.tenant_id
   WHERE s.id=f.settlement_id AND s.tenant_id=f.tenant_id AND r.id=f.reservation_id)
  OR NOT EXISTS(SELECT 1 FROM funding_reservation_lots a WHERE a.tenant_id=f.tenant_id
   AND a.reservation_id=f.reservation_id AND a.allocation_index=f.allocation_index AND a.lot_id=f.lot_id
   AND f.charged_amount::numeric+f.released_amount::numeric=a.amount::numeric)
  OR NOT EXISTS(SELECT 1 FROM funding_lots l WHERE l.id=f.lot_id AND l.tenant_id=f.tenant_id)
  OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=f.lot_id AND e.tenant_id=f.tenant_id
   AND e.operation_id=f.settlement_id AND e.kind='finish' AND wallet_event_valid(e.id)))
 UNION ALL
 SELECT 'refund',r.id FROM refund_orders r WHERE ($1='' OR r.tenant_id=$1)
  AND (wallet_refund_valid(r.id) IS DISTINCT FROM true
   OR NOT EXISTS(SELECT 1 FROM settlements s WHERE s.id=r.settlement_id AND s.tenant_id=r.tenant_id)
   OR (r.state='approved' AND NOT EXISTS(SELECT 1 FROM journals j WHERE j.id=r.journal_id AND j.tenant_id=r.tenant_id
    AND j.operation_id=r.id AND j.reference=r.settlement_id AND j.kind='refund'))
   OR (r.state<>'approved' AND r.journal_id IS NOT NULL))
 UNION ALL
 SELECT 'refund_edge',a.refund_order_id||':'||a.refund_index::text
 FROM funding_consumption_refund_lots a WHERE ($1='' OR a.tenant_id=$1)
  AND (NOT EXISTS(SELECT 1 FROM refund_orders r WHERE r.id=a.refund_order_id AND r.tenant_id=a.tenant_id
    AND r.settlement_id=a.settlement_id AND r.state='approved')
   OR NOT EXISTS(SELECT 1 FROM funding_settlement_lots f WHERE f.settlement_id=a.settlement_id
    AND f.tenant_id=a.tenant_id AND f.allocation_index=a.allocation_index AND f.lot_id=a.lot_id)
   OR NOT EXISTS(SELECT 1 FROM funding_lots l WHERE l.id=a.lot_id AND l.tenant_id=a.tenant_id)
   OR NOT EXISTS(SELECT 1 FROM funding_lot_events e WHERE e.lot_id=a.lot_id AND e.tenant_id=a.tenant_id
    AND e.operation_id=a.refund_order_id AND e.kind='refund' AND wallet_event_valid(e.id)))
), tenant_scope AS (
 SELECT id FROM tenants WHERE ($1='' OR id=$1)
 UNION SELECT tenant_id FROM funding_lots WHERE ($1='' OR tenant_id=$1)
 UNION SELECT tenant_id FROM funding_lot_balances WHERE ($1='' OR tenant_id=$1)
 UNION SELECT tenant_id FROM ledger_accounts WHERE tenant_id IS NOT NULL AND ($1='' OR tenant_id=$1)
 UNION SELECT tenant_id FROM reservations WHERE ($1='' OR tenant_id=$1)
), wallet_errors AS (
 SELECT t.id FROM tenant_scope t WHERE
  (SELECT coalesce(sum(a.balance::numeric),0) FROM ledger_accounts a WHERE a.tenant_id=t.id AND a.kind='AVAILABLE')
   IS DISTINCT FROM (SELECT coalesce(sum(b.available_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=t.id)
  OR (SELECT coalesce(sum(a.balance::numeric),0) FROM ledger_accounts a WHERE a.tenant_id=t.id AND a.kind='HELD')
   IS DISTINCT FROM (SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=t.id)
  OR (SELECT coalesce(sum(b.held_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=t.id)
   IS DISTINCT FROM (SELECT coalesce(sum(r.amount::numeric),0) FROM reservations r WHERE r.tenant_id=t.id AND r.status IN ('reserved','pending_reconciliation'))
  OR (SELECT coalesce(sum(b.consumed_amount::numeric),0) FROM funding_lot_balances b WHERE b.tenant_id=t.id)
   IS DISTINCT FROM (SELECT coalesce(sum(s.charged_amount::numeric-s.refunded_amount::numeric),0) FROM settlements s WHERE s.tenant_id=t.id)
)
SELECT ($1='' OR EXISTS(SELECT 1 FROM tenants WHERE id=$1)),
 (SELECT count(*) FROM source_errors),(SELECT count(*) FROM event_errors),
 (SELECT count(*) FROM allocation_errors),(SELECT count(*) FROM wallet_errors)`

// CheckWalletSources checks the current model in a single bounded read-only
// snapshot. Empty tenant selects the platform-wide scope; authorization belongs
// to its caller. It never writes facts, invokes a provider, or retries a query.
func (s *Store) CheckWalletSources(ctx context.Context, tenant string) (result WalletSourceCheck, err error) {
	if tenant != "" && !validID(tenant) {
		return WalletSourceCheck{}, fail("invalid_tenant", "invalid wallet source scope")
	}
	if ctx == nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(errors.New("context required"))
	}
	if err := ctx.Err(); err != nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(err)
	}
	if s == nil || s.pool == nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(errors.New("store unavailable"))
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := s.pool.Acquire(work)
	if err != nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(err)
	}
	var tx pgx.Tx
	defer func() {
		cancel()
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		var cleanupErr error
		if tx != nil {
			cleanupErr = tx.Rollback(cleanup)
		}
		if cleanupErr != nil || conn.Conn().IsClosed() || conn.Conn().PgConn().TxStatus() != 'I' {
			if cleanupErr == nil {
				cleanupErr = errors.New("wallet source connection not idle")
			}
			cleanupErr = errors.Join(cleanupErr, conn.Hijack().Close(cleanup))
		} else {
			conn.Release()
		}
		if cleanupErr != nil {
			err = walletSourcesUnavailable(errors.Join(err, cleanupErr))
		}
		if err != nil {
			result = WalletSourceCheck{}
		}
	}()
	tx, err = conn.BeginTx(work, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(err)
	}
	var lockSetting, statementSetting string
	if err = tx.QueryRow(work, `SELECT clock_timestamp(),pg_catalog.set_config('lock_timeout','3s',true),pg_catalog.set_config('statement_timeout','5s',true)`).Scan(&result.AsOf, &lockSetting, &statementSetting); err != nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(err)
	}
	var found bool
	if err = tx.QueryRow(work, walletSourcesCheckSQL, tenant).Scan(&found, &result.SourceMismatches, &result.EventMismatches, &result.AllocationMismatches, &result.WalletMismatches); err != nil {
		return WalletSourceCheck{}, walletSourcesUnavailable(err)
	}
	if !found {
		return WalletSourceCheck{}, fail("invalid_tenant", "wallet tenant does not exist")
	}
	result.TenantID = tenant
	result.AsOf = result.AsOf.UTC()
	result.Balanced = result.SourceMismatches == 0 && result.EventMismatches == 0 && result.AllocationMismatches == 0 && result.WalletMismatches == 0
	return result, nil
}
