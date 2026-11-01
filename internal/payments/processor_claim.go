package payments

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func processorInt(v int64) string      { return strconv.FormatInt(v, 10) }
func processorTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

func (p *Processor) ClaimMutation(ctx context.Context) (MutationLease, bool, error) {
	if p == nil || p.runtime == nil {
		return MutationLease{}, false, ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return MutationLease{}, false, ErrPaymentsRecovery
	}
	b, ok, err := p.claimOutbox(ctx, []string{"checkout", "cancel"}, 10)
	if err != nil {
		return MutationLease{}, false, err
	}
	return MutationLease{b}, ok, nil
}
func (p *Processor) ClaimQuery(ctx context.Context) (QueryLease, bool, error) {
	b, ok, err := p.claimOutbox(ctx, []string{"query"}, 20)
	if err != nil {
		return QueryLease{}, false, err
	}
	return QueryLease{b}, ok, nil
}
func (p *Processor) claimOutbox(ctx context.Context, kinds []string, budget int) (leaseBinding, bool, error) {
	var out leaseBinding
	var found bool
	err := p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		e := tx.QueryRow(ctx, `WITH picked AS (
 SELECT id FROM payment_command_outbox WHERE kind=ANY($1::text[]) AND state IN ('pending','retry_wait')
 AND next_attempt_at<=clock_timestamp() AND attempt_count<$2
 ORDER BY next_attempt_at,id COLLATE "C" FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE payment_command_outbox b SET state='leased',generation=b.generation+1,attempt_count=b.attempt_count+1
 FROM picked p WHERE b.id=p.id RETURNING b.id,b.kind,b.generation,b.lease_until`, kinds, budget).Scan(&out.id, &out.kind, &out.generation, &out.until)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		out.seal = p.seal
		found = true
		return nil, nil
	})
	if err != nil {
		return leaseBinding{}, false, err
	}
	return out, found, nil
}
func (p *Processor) ClaimEvidence(ctx context.Context) (EvidenceLease, bool, error) {
	if p == nil || p.runtime == nil {
		return EvidenceLease{}, false, ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return EvidenceLease{}, false, ErrPaymentsRecovery
	}
	var out leaseBinding
	var found bool
	err := p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		e := tx.QueryRow(ctx, `WITH picked AS (
 SELECT evidence_id FROM payment_evidence_processing WHERE state IN ('pending','retry_wait')
 AND next_attempt_at<=clock_timestamp() AND attempt_count<20 ORDER BY next_attempt_at,evidence_id COLLATE "C"
 FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE payment_evidence_processing b SET state='leased',generation=b.generation+1,attempt_count=b.attempt_count+1
 FROM picked p WHERE b.evidence_id=p.evidence_id RETURNING b.evidence_id,b.generation,b.lease_until`).Scan(&out.id, &out.generation, &out.until)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, nil
		}
		if e != nil {
			return nil, e
		}
		out.seal = p.seal
		out.kind = "evidence"
		found = true
		return nil, nil
	})
	if err != nil {
		return EvidenceLease{}, false, err
	}
	return EvidenceLease{out}, found, nil
}

func processorSupervisor(ctx context.Context, tx pgx.Tx, b processorOutbox, id string) error {
	_, err := tx.Exec(ctx, `INSERT INTO payment_command_outbox(id,order_id,kind,original_outbox_id) VALUES($1,$2,'query',$3)
 ON CONFLICT(original_outbox_id) WHERE kind='query' DO NOTHING`, id, b.order, b.id)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT order_id=$2 AND kind='query' AND original_outbox_id=$1 FROM payment_command_outbox WHERE original_outbox_id=$1 AND kind='query'`, b.id, b.order).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrPaymentsIntegrity
	}
	return nil
}

type expiredLocator struct {
	id, kind, order string
	generation      int64
}

func (p *Processor) ExpireLeases(ctx context.Context) error {
	if p == nil || p.runtime == nil {
		return ErrPaymentsInvalidWork
	}
	ctx, leave, err := p.enter(ctx)
	if err != nil {
		return err
	}
	defer leave()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = p.runtime.check(ctx); err != nil {
		return err
	}
	var candidates []expiredLocator
	// Locators do not confer authority. Each subsequent transaction rechecks the
	// actual generation and DB clock after obtaining the prescribed locks.
	err = p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		rows, e := tx.Query(ctx, `SELECT id,kind,order_id,generation FROM (
 SELECT id,kind,order_id,generation,lease_until FROM payment_command_outbox
 WHERE state='leased' AND lease_until<=clock_timestamp() AND ($1=false OR kind='query')
 UNION ALL SELECT evidence_id,'evidence','',generation,lease_until FROM payment_evidence_processing
 WHERE state='leased' AND lease_until<=clock_timestamp() AND $1=false
 ) all_leases ORDER BY lease_until,kind COLLATE "C",id COLLATE "C" LIMIT 100`, p.runtime.recovery())
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		for rows.Next() {
			var x expiredLocator
			if e = rows.Scan(&x.id, &x.kind, &x.order, &x.generation); e != nil {
				return nil, e
			}
			candidates = append(candidates, x)
		}
		return nil, rows.Err()
	})
	if err != nil {
		return err
	}
	for _, x := range candidates {
		supervisorID := uuid.NewString()
		err = p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
			if x.kind == "checkout" || x.kind == "cancel" {
				if _, e := processorLockOrder(ctx, tx, x.order); e != nil {
					return nil, e
				}
				b, e := processorReadOutbox(ctx, tx, x.id, `FOR UPDATE SKIP LOCKED`)
				if errors.Is(e, pgx.ErrNoRows) {
					return nil, nil
				}
				if e != nil {
					return nil, e
				}
				now, e := p.runtime.databaseTime(ctx, tx)
				if e != nil {
					return nil, e
				}
				if b.state != "leased" || b.generation != x.generation || b.until == nil || b.until.After(now) {
					return nil, nil
				}
				if e = processorSupervisor(ctx, tx, b, supervisorID); e != nil {
					return nil, e
				}
				_, e = tx.Exec(ctx, `UPDATE payment_command_outbox SET state='submission_unknown',last_error_code='timeout' WHERE id=$1 AND state='leased' AND generation=$2 AND lease_until<=clock_timestamp()`, x.id, x.generation)
				return nil, e
			}
			table, key := "payment_command_outbox", "id"
			terminal := "review_required"
			retryError := "timeout"
			if x.kind == "evidence" {
				table, key = "payment_evidence_processing", "evidence_id"
				terminal = "processing_review"
				retryError = "temporary"
			}
			// Both table and key above are internal constants, never identifiers from a request.
			_, e := tx.Exec(ctx, `WITH picked AS (SELECT `+key+` FROM `+table+` WHERE `+key+`=$1 AND state='leased' AND generation=$2 AND lease_until<=clock_timestamp() FOR UPDATE SKIP LOCKED)
 UPDATE `+table+` b SET state=CASE WHEN b.attempt_count=20 THEN $3 ELSE 'retry_wait' END,last_error_code=CASE WHEN b.attempt_count=20 THEN 'attempts_exhausted' ELSE $4 END FROM picked p WHERE b.`+key+`=p.`+key, x.id, x.generation, terminal, retryError)
			return nil, e
		})
		if err != nil {
			return err
		}
	}
	return nil
}
