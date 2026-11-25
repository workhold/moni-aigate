package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moni-ai/moni-aigate/internal/economics"
)

const requestsSQL = `SELECT r.id,r.tenant_id,t.name,r.model_id,m.public_name,r.status,r.billing_status,r.created_at,
 coalesce(r.protocol,''),coalesce(r.execution_kind,''),coalesce(ms.profile_version,''),coalesce(ms.schema_id,''),coalesce(encode(ms.schema_digest,'hex'),''),coalesce(ms.variant_key,''),
 s.charged_amount::pg_catalog.text,s.refunded_amount::pg_catalog.text,coalesce(fz.source='cache',false),
 coalesce(es.offer_version_id,''),coalesce(es.task_class,''),es.fx_policy_version_id,es.valuation_currency,es.fx_snapshot,es.created_at
 FROM request_records r JOIN tenants t ON t.id=r.tenant_id JOIN models m ON m.id=r.model_id
 LEFT JOIN request_meter_snapshots ms ON ms.tenant_id=r.tenant_id AND ms.request_id=r.id
 LEFT JOIN settlements s ON s.request_id=r.id AND s.tenant_id=r.tenant_id
 LEFT JOIN finalization_records fz ON fz.tenant_id=r.tenant_id AND fz.request_id=r.id AND fz.id=r.latest_finalization_id
 LEFT JOIN request_economics_snapshots es ON es.tenant_id=r.tenant_id AND es.request_id=r.id
 WHERE r.created_at >= $1 AND r.created_at < $2 AND ($3='' OR r.tenant_id=$3) AND ($4='' OR r.model_id=$4)
 AND ($5='' OR r.protocol=$5) AND ($6='' OR r.execution_kind=$6)
 ORDER BY r.created_at DESC,r.id COLLATE "C" DESC LIMIT 5001`

const attemptsSQL = `SELECT a.id,a.request_id,a.channel_id,c.name,a.cost_status,a.cost_amount::pg_catalog.text,
 coalesce(cv.currency,'CNY'),CASE WHEN a.latest_cost_correction_id IS NOT NULL THEN pc.created_at ELSE fz.created_at END,
 coalesce(a.latest_cost_correction_id,a.latest_cost_finalization_id,'')
 FROM upstream_attempts a JOIN channels c ON c.id=a.channel_id
 LEFT JOIN attempt_meter_snapshots ms ON ms.tenant_id=a.tenant_id AND ms.attempt_id=a.id
 LEFT JOIN cost_versions cv ON cv.id=ms.cost_version_id
 LEFT JOIN provider_cost_corrections pc ON pc.tenant_id=a.tenant_id AND pc.id=a.latest_cost_correction_id AND pc.attempt_id=a.id
 LEFT JOIN finalization_records fz ON fz.tenant_id=a.tenant_id AND fz.id=a.latest_cost_finalization_id AND fz.attempt_id=a.id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) ORDER BY a.request_id COLLATE "C",a.sequence,a.id COLLATE "C"`

const attemptCostComponentsSQL = `SELECT c.tenant_id,c.attempt_id,c.component_id,c.id,c.currency,c.basis,c.amount_micro::pg_catalog.text,c.as_of,c.revision
 FROM attempt_cost_components c JOIN upstream_attempts a ON a.tenant_id=c.tenant_id AND a.id=c.attempt_id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) AND c.layer='inner'
 AND NOT EXISTS(SELECT 1 FROM attempt_cost_components newer WHERE newer.tenant_id=c.tenant_id AND newer.attempt_id=c.attempt_id AND newer.component_id=c.component_id AND newer.revision>c.revision)
 ORDER BY a.request_id COLLATE "C",c.attempt_id COLLATE "C",c.component_id COLLATE "C"`

const mediaRequestsSQL = `SELECT q.id,q.tenant_id,t.name,q.model_key,q.model_key,q.state,
 CASE WHEN s.id IS NULL THEN q.state ELSE s.status END,q.created_at,q.operation,
 CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END,
 q.profile_version,'','', '',s.charged_amount::pg_catalog.text,
 CASE WHEN s.id IS NULL THEN NULL ELSE '0' END,false
 FROM media_requests q JOIN tenants t ON t.id=q.tenant_id
 LEFT JOIN media_settlements s ON s.tenant_id=q.tenant_id AND s.request_id=q.id
 WHERE q.created_at >= $1 AND q.created_at < $2 AND ($3='' OR q.tenant_id=$3)
 AND ($4='' OR q.model_key=$4) AND ($5='' OR q.operation=$5)
 AND ($6='' OR CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END=$6)
 ORDER BY q.created_at DESC,q.id COLLATE "C" DESC LIMIT 5001`

const mediaAttemptsSQL = `SELECT a.tenant_id,a.id,a.request_id,q.operation,
 CASE WHEN s.supplier_cost_status='known' THEN 'contract' ELSE 'unknown' END,s.supplier_cost_amount::pg_catalog.text,
 'CNY',s.created_at,coalesce(s.id,'')
 FROM media_attempts a JOIN media_requests q ON q.tenant_id=a.tenant_id AND q.id=a.request_id
 LEFT JOIN media_settlements s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) AND q.created_at >= $2 AND q.created_at < $3
 AND ($4='' OR q.tenant_id=$4) AND ($5='' OR q.model_key=$5) AND ($6='' OR q.operation=$6)
 AND ($7='' OR CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END=$7)
 ORDER BY a.request_id COLLATE "C",a.created_at,a.id COLLATE "C"`

const settlementLotsSQL = `SELECT s.tenant_id,s.settlement_id,s.allocation_index,st.request_id,s.charged_amount,coalesce(f.fiat_currency,''),coalesce(f.fiat_amount,0),l.issued_amount,f.basis_kind,coalesce(f.buy_rate_version_id,''),s.lot_id,
 (SELECT coalesce(sum(r.amount),0)::pg_catalog.int8 FROM funding_consumption_refund_lots r WHERE r.tenant_id=s.tenant_id AND r.settlement_id=s.settlement_id AND r.allocation_index=s.allocation_index AND r.lot_id=s.lot_id)
 FROM funding_settlement_lots s JOIN settlements st ON st.tenant_id=s.tenant_id AND st.id=s.settlement_id
 JOIN funding_lots l ON l.tenant_id=s.tenant_id AND l.id=s.lot_id
 JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id
 WHERE st.request_id=ANY($1::pg_catalog.text[]) ORDER BY s.settlement_id,s.allocation_index`

const mediaSettlementLotsSQL = `SELECT s.tenant_id,s.settlement_id,s.allocation_index,st.request_id,s.charged_amount,coalesce(f.fiat_currency,''),coalesce(f.fiat_amount,0),l.issued_amount,f.basis_kind,coalesce(f.buy_rate_version_id,''),s.lot_id,
 (SELECT coalesce(sum(r.amount),0)::pg_catalog.int8 FROM funding_consumption_refund_lots r WHERE r.tenant_id=s.tenant_id AND r.settlement_id=s.settlement_id AND r.allocation_index=s.allocation_index AND r.lot_id=s.lot_id)
 FROM media_settlement_lots s JOIN media_settlements st ON st.tenant_id=s.tenant_id AND st.id=s.settlement_id
 JOIN funding_lots l ON l.tenant_id=s.tenant_id AND l.id=s.lot_id
 JOIN funding_receipts f ON f.tenant_id=l.tenant_id AND f.id=l.funding_receipt_id
 WHERE st.request_id=ANY($1::pg_catalog.text[]) ORDER BY s.settlement_id,s.allocation_index`

const cashRefundsSQL = `SELECT fiat_currency,coalesce(sum(paid_fiat_amount::numeric),0)::text FROM cash_refund_orders
 WHERE state='completed' AND approved_at >= $1 AND approved_at < $2 AND ($3='' OR tenant_id=$3) GROUP BY fiat_currency ORDER BY fiat_currency`

// ceilMicro preserves [from,to) for database timestamps whose stored precision
// is a microsecond, including when an RFC3339 caller supplied nanoseconds.
func ceilMicro(at time.Time) time.Time {
	at = at.UTC()
	if remainder := at.Nanosecond() % 1000; remainder != 0 {
		at = at.Add(time.Duration(1000-remainder) * time.Nanosecond)
	}
	return at
}

func readSettlementLots(ctx context.Context, tx pgx.Tx, sql string, source factSource, ids []string) ([]settlementLotFact, error) {
	rows, err := tx.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []settlementLotFact
	for rows.Next() {
		var tenant, request, currency, basis, buyRate string
		var settlement, lot string
		var index, charged, refunded, fiat, issued int64
		if err = rows.Scan(&tenant, &settlement, &index, &request, &charged, &currency, &fiat, &issued, &basis, &buyRate, &lot, &refunded); err != nil {
			return nil, err
		}
		keyID := settlement
		if source == mediaFactSource {
			keyID = tenant + "|" + settlement
			request = tenant + "|" + request
		}
		result = append(result, settlementLotFact{key: sourceKey(source, keyID+"|"+strconv.FormatInt(index, 10)), request: sourceKey(source, request), tenant: tenant, lotID: lot, currency: currency, basisKind: basis, buyRateID: buyRate, charged: charged, refunded: refunded, fiat: fiat, issued: issued})
	}
	return result, rows.Err()
}

func readCashRefunds(ctx context.Context, tx pgx.Tx, filter Filter) ([]cashRefundFact, error) {
	rows, err := tx.Query(ctx, cashRefundsSQL, ceilMicro(filter.From), ceilMicro(filter.To), filter.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []cashRefundFact
	for rows.Next() {
		var fact cashRefundFact
		if err = rows.Scan(&fact.currency, &fact.amount); err != nil {
			return nil, err
		}
		result = append(result, fact)
	}
	return result, rows.Err()
}

// Read uses current facts only. It neither depends on asynchronous projections
// nor invokes any model, money writer or compatibility/proof machinery.
func Read(ctx context.Context, pool *pgxpool.Pool, filter Filter) (report Report, err error) {
	if err = filter.Validate(); err != nil {
		return Report{}, err
	}
	if ctx == nil || pool == nil {
		return Report{}, errUnavailable
	}
	work, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pool.Acquire(work)
	if err != nil {
		return Report{}, err
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
				cleanupErr = errUnavailable
			}
			cleanupErr = errors.Join(cleanupErr, conn.Hijack().Close(cleanup))
		} else {
			conn.Release()
		}
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
		if err != nil {
			report = Report{}
		}
	}()
	tx, err = conn.BeginTx(work, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, err
	}
	var asOf time.Time
	var lockSetting, statementSetting string
	if err = tx.QueryRow(work, `SELECT clock_timestamp(),pg_catalog.set_config('lock_timeout','3s',true),pg_catalog.set_config('statement_timeout','5s',true)`).Scan(&asOf, &lockSetting, &statementSetting); err != nil {
		return Report{}, err
	}
	rows, err := tx.Query(work, requestsSQL, ceilMicro(filter.From), ceilMicro(filter.To), filter.TenantID, filter.ModelID, filter.Protocol, filter.ExecutionKind)
	if err != nil {
		return Report{}, err
	}
	facts := make([]requestFact, 0)
	textIDs := make([]string, 0)
	for rows.Next() {
		var fact requestFact
		var fxPolicyID, valuation *string
		var fxRaw []byte
		var snapshotCreated *time.Time
		r := &fact.row
		if err = rows.Scan(&r.RequestID, &r.TenantID, &r.TenantName, &r.ModelID, &r.ModelName, &r.Status, &r.BillingStatus, &r.CreatedAt, &r.Protocol, &r.ExecutionKind, &r.ProfileVersion, &r.SchemaID, &r.SchemaDigest, &r.VariantKey, &fact.charged, &fact.refunded, &fact.cache, &r.OfferVersionID, &r.TaskClass, &fxPolicyID, &valuation, &fxRaw, &snapshotCreated); err != nil {
			rows.Close()
			return Report{}, err
		}
		if fxPolicyID != nil || valuation != nil || fxRaw != nil || snapshotCreated != nil || r.OfferVersionID != "" || r.TaskClass != "" {
			if fxPolicyID == nil || valuation == nil || fxRaw == nil || snapshotCreated == nil {
				rows.Close()
				return Report{}, errUnavailable
			}
			var fx economics.FxPolicy
			if err = json.Unmarshal(fxRaw, &fx); err != nil {
				rows.Close()
				return Report{}, errUnavailable
			}
			fact.economics = &requestEconomicsFact{offerVersionID: r.OfferVersionID, taskClass: r.TaskClass, fxPolicyVersionID: *fxPolicyID, valuationCurrency: economics.Currency(*valuation), fx: fx, createdAt: *snapshotCreated}
		}
		fact.key = sourceKey(textFactSource, r.RequestID)
		fact.source = textFactSource
		facts = append(facts, fact)
		textIDs = append(textIDs, r.RequestID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}
	rows, err = tx.Query(work, mediaRequestsSQL, ceilMicro(filter.From), ceilMicro(filter.To), filter.TenantID, filter.ModelID, filter.Protocol, filter.ExecutionKind)
	if err != nil {
		return Report{}, err
	}
	mediaIDs := make([]string, 0)
	for rows.Next() {
		var fact requestFact
		r := &fact.row
		if err = rows.Scan(&r.RequestID, &r.TenantID, &r.TenantName, &r.ModelID, &r.ModelName, &r.Status, &r.BillingStatus, &r.CreatedAt, &r.Protocol, &r.ExecutionKind, &r.ProfileVersion, &r.SchemaID, &r.SchemaDigest, &r.VariantKey, &fact.charged, &fact.refunded, &fact.cache); err != nil {
			rows.Close()
			return Report{}, err
		}
		fact.key = sourceKey(mediaFactSource, r.TenantID+"|"+r.RequestID)
		fact.source = mediaFactSource
		facts = append(facts, fact)
		mediaIDs = append(mediaIDs, r.RequestID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}
	if len(facts) > MaxRequests {
		return Report{}, ErrRangeTooLarge
	}
	attempts := make([]attemptFact, 0)
	if len(textIDs) > 0 {
		rows, err = tx.Query(work, attemptsSQL, textIDs)
		if err != nil {
			return Report{}, err
		}
		for rows.Next() {
			var fact attemptFact
			if err = rows.Scan(&fact.id, &fact.request, &fact.channel, &fact.channelName, &fact.status, &fact.amount, &fact.currency, &fact.asOf, &fact.revision); err != nil {
				rows.Close()
				return Report{}, err
			}
			fact.source = textFactSource
			fact.request = sourceKey(textFactSource, fact.request)
			attempts = append(attempts, fact)
			if len(attempts) > 2*len(facts) {
				rows.Close()
				return Report{}, errUnavailable
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return Report{}, err
		}
	}
	if len(mediaIDs) > 0 {
		rows, err = tx.Query(work, mediaAttemptsSQL, mediaIDs, ceilMicro(filter.From), ceilMicro(filter.To), filter.TenantID, filter.ModelID, filter.Protocol, filter.ExecutionKind)
		if err != nil {
			return Report{}, err
		}
		for rows.Next() {
			var tenant, id, request, operation, status, currency, revision string
			var amount *string
			var costAsOf *time.Time
			if err = rows.Scan(&tenant, &id, &request, &operation, &status, &amount, &currency, &costAsOf, &revision); err != nil {
				rows.Close()
				return Report{}, err
			}
			attempts = append(attempts, attemptFact{
				id:          id,
				request:     sourceKey(mediaFactSource, tenant+"|"+request),
				channel:     "media:" + operation,
				channelName: "media/" + operation,
				status:      status,
				amount:      amount,
				currency:    currency,
				asOf:        costAsOf,
				revision:    revision,
				source:      mediaFactSource,
			})
			if len(attempts) > 2*len(facts)+MaxRequests*2 {
				rows.Close()
				return Report{}, errUnavailable
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return Report{}, err
		}
	}
	innerCosts := make([]attemptCostFact, 0)
	if len(textIDs) > 0 {
		rows, err = tx.Query(work, attemptCostComponentsSQL, textIDs)
		if err != nil {
			return Report{}, err
		}
		for rows.Next() {
			var fact attemptCostFact
			if err = rows.Scan(&fact.tenant, &fact.attempt, &fact.component, &fact.id, &fact.currency, &fact.basis, &fact.amount, &fact.asOf, &fact.revision); err != nil {
				rows.Close()
				return Report{}, err
			}
			innerCosts = append(innerCosts, fact)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return Report{}, err
		}
	}
	allocations := make([]settlementLotFact, 0)
	if len(textIDs) > 0 {
		allocations, err = readSettlementLots(work, tx, settlementLotsSQL, textFactSource, textIDs)
		if err != nil {
			return Report{}, err
		}
	}
	if len(mediaIDs) > 0 {
		var mediaAllocations []settlementLotFact
		mediaAllocations, err = readSettlementLots(work, tx, mediaSettlementLotsSQL, mediaFactSource, mediaIDs)
		if err != nil {
			return Report{}, err
		}
		allocations = append(allocations, mediaAllocations...)
	}
	refunds, err := readCashRefunds(work, tx, filter)
	if err != nil {
		return Report{}, err
	}
	report, err = aggregate(filter, asOf, facts, attempts, innerCosts, allocations, refunds)
	if err == nil {
		err = work.Err()
	}
	return report, err
}
