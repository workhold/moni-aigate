package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const requestsSQL = `SELECT r.id,r.tenant_id,t.name,r.model_id,m.public_name,r.status,r.billing_status,r.created_at,
 coalesce(r.protocol,''),coalesce(r.execution_kind,''),coalesce(ms.profile_version,''),coalesce(ms.schema_id,''),coalesce(encode(ms.schema_digest,'hex'),''),coalesce(ms.variant_key,''),
 s.charged_amount::pg_catalog.text,s.refunded_amount::pg_catalog.text
 FROM request_records r JOIN tenants t ON t.id=r.tenant_id JOIN models m ON m.id=r.model_id
 LEFT JOIN request_meter_snapshots ms ON ms.tenant_id=r.tenant_id AND ms.request_id=r.id
 LEFT JOIN settlements s ON s.request_id=r.id AND s.tenant_id=r.tenant_id
 WHERE r.created_at >= $1 AND r.created_at < $2 AND ($3='' OR r.tenant_id=$3) AND ($4='' OR r.model_id=$4)
 AND ($5='' OR r.protocol=$5) AND ($6='' OR r.execution_kind=$6)
 ORDER BY r.created_at DESC,r.id COLLATE "C" DESC LIMIT 5001`

const attemptsSQL = `SELECT a.id,a.request_id,a.channel_id,c.name,a.cost_status,a.cost_amount::pg_catalog.text
 FROM upstream_attempts a JOIN channels c ON c.id=a.channel_id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) ORDER BY a.request_id COLLATE "C",a.sequence,a.id COLLATE "C"`

const mediaRequestsSQL = `SELECT q.id,q.tenant_id,t.name,q.model_key,q.model_key,q.state,
 CASE WHEN s.id IS NULL THEN q.state ELSE s.status END,q.created_at,q.operation,
 CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END,
 q.profile_version,'','', '',s.charged_amount::pg_catalog.text,
 CASE WHEN s.id IS NULL THEN NULL ELSE '0' END
 FROM media_requests q JOIN tenants t ON t.id=q.tenant_id
 LEFT JOIN media_settlements s ON s.tenant_id=q.tenant_id AND s.request_id=q.id
 WHERE q.created_at >= $1 AND q.created_at < $2 AND ($3='' OR q.tenant_id=$3)
 AND ($4='' OR q.model_key=$4) AND ($5='' OR q.operation=$5)
 AND ($6='' OR CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END=$6)
 ORDER BY q.created_at DESC,q.id COLLATE "C" DESC LIMIT 5001`

const mediaAttemptsSQL = `SELECT a.tenant_id,a.id,a.request_id,q.operation,
 CASE WHEN s.supplier_cost_status='known' THEN 'contract' ELSE 'unknown' END,s.supplier_cost_amount::pg_catalog.text
 FROM media_attempts a JOIN media_requests q ON q.tenant_id=a.tenant_id AND q.id=a.request_id
 LEFT JOIN media_settlements s ON s.tenant_id=a.tenant_id AND s.request_id=a.request_id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) AND q.created_at >= $2 AND q.created_at < $3
 AND ($4='' OR q.tenant_id=$4) AND ($5='' OR q.model_key=$5) AND ($6='' OR q.operation=$6)
 AND ($7='' OR CASE WHEN q.operation='video_generation' THEN 'asynchronous' ELSE 'synchronous' END=$7)
 ORDER BY a.request_id COLLATE "C",a.created_at,a.id COLLATE "C"`

// ceilMicro preserves [from,to) for database timestamps whose stored precision
// is a microsecond, including when an RFC3339 caller supplied nanoseconds.
func ceilMicro(at time.Time) time.Time {
	at = at.UTC()
	if remainder := at.Nanosecond() % 1000; remainder != 0 {
		at = at.Add(time.Duration(1000-remainder) * time.Nanosecond)
	}
	return at
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
		r := &fact.row
		if err = rows.Scan(&r.RequestID, &r.TenantID, &r.TenantName, &r.ModelID, &r.ModelName, &r.Status, &r.BillingStatus, &r.CreatedAt, &r.Protocol, &r.ExecutionKind, &r.ProfileVersion, &r.SchemaID, &r.SchemaDigest, &r.VariantKey, &fact.charged, &fact.refunded); err != nil {
			rows.Close()
			return Report{}, err
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
		if err = rows.Scan(&r.RequestID, &r.TenantID, &r.TenantName, &r.ModelID, &r.ModelName, &r.Status, &r.BillingStatus, &r.CreatedAt, &r.Protocol, &r.ExecutionKind, &r.ProfileVersion, &r.SchemaID, &r.SchemaDigest, &r.VariantKey, &fact.charged, &fact.refunded); err != nil {
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
			if err = rows.Scan(&fact.id, &fact.request, &fact.channel, &fact.channelName, &fact.status, &fact.amount); err != nil {
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
			var tenant, id, request, operation, status string
			var amount *string
			if err = rows.Scan(&tenant, &id, &request, &operation, &status, &amount); err != nil {
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
	report, err = aggregate(filter, asOf, facts, attempts)
	if err == nil {
		err = work.Err()
	}
	return report, err
}
