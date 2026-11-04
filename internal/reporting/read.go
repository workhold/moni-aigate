package reporting

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const requestsSQL = `SELECT r.id,r.tenant_id,t.name,r.model_id,m.public_name,r.status,r.billing_status,r.created_at,
 s.charged_amount::pg_catalog.text,s.refunded_amount::pg_catalog.text
 FROM request_records r JOIN tenants t ON t.id=r.tenant_id JOIN models m ON m.id=r.model_id
 LEFT JOIN settlements s ON s.request_id=r.id AND s.tenant_id=r.tenant_id
 WHERE r.created_at >= $1 AND r.created_at < $2 AND ($3='' OR r.tenant_id=$3) AND ($4='' OR r.model_id=$4)
 ORDER BY r.created_at DESC,r.id COLLATE "C" DESC LIMIT 5001`

const attemptsSQL = `SELECT a.id,a.request_id,a.channel_id,c.name,a.cost_status,a.cost_amount::pg_catalog.text
 FROM upstream_attempts a JOIN channels c ON c.id=a.channel_id
 WHERE a.request_id=ANY($1::pg_catalog.text[]) ORDER BY a.request_id COLLATE "C",a.sequence,a.id COLLATE "C"`

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
	rows, err := tx.Query(work, requestsSQL, ceilMicro(filter.From), ceilMicro(filter.To), filter.TenantID, filter.ModelID)
	if err != nil {
		return Report{}, err
	}
	facts := make([]requestFact, 0)
	ids := make([]string, 0)
	for rows.Next() {
		var fact requestFact
		r := &fact.row
		if err = rows.Scan(&r.RequestID, &r.TenantID, &r.TenantName, &r.ModelID, &r.ModelName, &r.Status, &r.BillingStatus, &r.CreatedAt, &fact.charged, &fact.refunded); err != nil {
			rows.Close()
			return Report{}, err
		}
		facts = append(facts, fact)
		ids = append(ids, r.RequestID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Report{}, err
	}
	if len(facts) > MaxRequests {
		return Report{}, ErrRangeTooLarge
	}
	attempts := make([]attemptFact, 0)
	if len(ids) > 0 {
		rows, err = tx.Query(work, attemptsSQL, ids)
		if err != nil {
			return Report{}, err
		}
		for rows.Next() {
			var fact attemptFact
			if err = rows.Scan(&fact.id, &fact.request, &fact.channel, &fact.channelName, &fact.status, &fact.amount); err != nil {
				rows.Close()
				return Report{}, err
			}
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
	report, err = aggregate(filter, asOf, facts, attempts)
	if err == nil {
		err = work.Err()
	}
	return report, err
}
