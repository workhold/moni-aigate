package payments

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type processorChild struct {
	id, supervisor, digest         string
	generation, mutationGeneration int64
	canonical                      []byte
	created                        time.Time
	request                        paymentproto.QueryRequest
}

func processorReadChild(ctx context.Context, tx pgx.Tx, id string) (processorChild, error) {
	var a processorChild
	e := tx.QueryRow(ctx, `SELECT query_id,supervisor_id,created_generation,mutation_generation,canonical_request,request_digest,created_at FROM payment_query_attempts WHERE query_id=$1`, id).Scan(&a.id, &a.supervisor, &a.generation, &a.mutationGeneration, &a.canonical, &a.digest, &a.created)
	if e != nil {
		return a, e
	}
	a.request, e = paymentproto.ParseQuery(a.canonical)
	if e != nil {
		return a, ErrPaymentsIntegrity
	}
	canonical, e := paymentproto.CanonicalQuery(a.request)
	if e != nil || !processorCanonical(a.canonical, canonical, a.digest) {
		return a, ErrPaymentsIntegrity
	}
	return a, nil
}
func processorChildBound(a processorChild, s, b processorOutbox, o processorOrder) bool {
	r := a.request
	return a.id == r.QueryID && a.supervisor == s.id && s.kind == "query" && s.original != nil && *s.original == b.id && s.order == o.id && b.order == o.id && b.command != nil && b.digest != nil && a.generation > 0 && a.generation <= s.generation && a.mutationGeneration > 0 && a.mutationGeneration <= b.generation && r.EnvironmentID == o.environment && r.MerchantID == o.merchant && r.MerchantOrderID == o.merchantOrder && r.CommandID == *b.command && r.CommandKind == b.kind && r.ParameterDigest == *b.digest
}
func processorQueryReplyBound(r paymentproto.QueryReply, a processorChild) bool {
	q := a.request
	return r.QueryID == q.QueryID && r.EnvironmentID == q.EnvironmentID && r.MerchantID == q.MerchantID && r.MerchantOrderID == q.MerchantOrderID && r.CommandID == q.CommandID && r.CommandKind == q.CommandKind && r.ParameterDigest == q.ParameterDigest
}
func processorSnapshot(ctx context.Context, tx pgx.Tx, a processorChild) (id string, canonical []byte, reply paymentproto.QueryReply, found bool, err error) {
	var digest string
	err = tx.QueryRow(ctx, `SELECT id,canonical_body,snapshot_digest FROM payment_verified_queries WHERE query_id=$1`, a.id).Scan(&id, &canonical, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, reply, false, nil
	}
	if err != nil {
		return
	}
	reply, err = paymentproto.ParseQueryReply(canonical)
	if err != nil {
		return "", nil, reply, false, ErrPaymentsIntegrity
	}
	var regenerated []byte
	regenerated, err = paymentproto.CanonicalQueryReply(reply)
	if err != nil || !processorCanonical(canonical, regenerated, digest) || !processorQueryReplyBound(reply, a) {
		return "", nil, reply, false, ErrPaymentsIntegrity
	}
	found = true
	return
}
func (p *Processor) PrepareQuery(ctx context.Context, lease QueryLease) (QueryWork, error) {
	if !p.valid(lease.binding, "query") {
		return QueryWork{}, ErrPaymentsInvalidWork
	}
	childID := uuid.NewString()
	var work QueryWork
	err := p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		s, e := processorReadOutbox(ctx, tx, lease.binding.id, "")
		if e != nil {
			return nil, e
		}
		o, e := processorLockOrder(ctx, tx, s.order)
		if e != nil {
			return nil, e
		}
		s, e = processorReadOutbox(ctx, tx, s.id, `FOR UPDATE`)
		if e != nil {
			return nil, e
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		if s.kind != "query" || s.original == nil {
			return nil, ErrPaymentsIntegrity
		}
		if !processorSendable(lease.binding, s.state, s.generation, s.until, now) {
			return nil, ErrPaymentsLeaseLost
		}
		b, e := processorReadOutbox(ctx, tx, *s.original, "")
		if e != nil {
			return nil, e
		}
		if e = p.boundMutation(ctx, tx, b, o); e != nil {
			return nil, e
		}
		var a processorChild
		var create bool
		if s.current == nil {
			create = true
		} else {
			a, e = processorReadChild(ctx, tx, *s.current)
			if e != nil {
				return nil, e
			}
			if !processorChildBound(a, s, b, o) {
				return nil, ErrPaymentsIntegrity
			}
			_, _, reply, found, e := processorSnapshot(ctx, tx, a)
			if e != nil {
				return nil, e
			}
			if found && reply.CommandState == "completed" {
				if e = processorAdvanceMutation(ctx, tx, s, b, a, reply, true); e != nil {
					return nil, e
				}
				tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET state='completed',last_error_code=NULL WHERE id=$1 AND state='leased' AND generation=$2 AND lease_until>clock_timestamp()`, s.id, lease.binding.generation)
				if e != nil {
					return nil, e
				}
				if tag.RowsAffected() != 1 {
					return nil, ErrPaymentsLeaseLost
				}
				return ErrPaymentsLeaseLost, nil
			}
			create = found && a.generation != s.generation
		}
		if create {
			q := paymentproto.QueryRequest{SchemaVersion: 1, QueryID: childID, EnvironmentID: o.environment, MerchantID: o.merchant, MerchantOrderID: o.merchantOrder, CommandID: *b.command, CommandKind: b.kind, ParameterDigest: *b.digest}
			canonical, e := paymentproto.CanonicalQuery(q)
			if e != nil {
				return nil, ErrPaymentsIntegrity
			}
			_, e = tx.Exec(ctx, `INSERT INTO payment_query_attempts(query_id,supervisor_id,created_generation,canonical_request) VALUES($1,$2,$3,$4)`, childID, s.id, s.generation, canonical)
			if e != nil {
				return nil, e
			}
			tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET current_query_id=$3 WHERE id=$1 AND state='leased' AND generation=$2 AND lease_until>clock_timestamp()`, s.id, s.generation, childID)
			if e != nil {
				return nil, e
			}
			if tag.RowsAffected() != 1 {
				return nil, ErrPaymentsLeaseLost
			}
			a, e = processorReadChild(ctx, tx, childID)
			if e != nil {
				return nil, e
			}
			if !processorChildBound(a, s, b, o) {
				return nil, ErrPaymentsIntegrity
			}
		}
		work = QueryWork{binding: lease.binding, child: a.id, request: a.request, canonical: bytes.Clone(a.canonical), digest: a.digest, createdGeneration: a.generation, mutationGeneration: a.mutationGeneration, createdAt: a.created}
		return nil, nil
	})
	if err != nil {
		return QueryWork{}, err
	}
	return work, nil
}
func (p *Processor) queryWorkValid(w QueryWork) bool {
	if !p.valid(w.binding, "query") || w.child == "" || w.createdGeneration < 1 || w.mutationGeneration < 1 || w.createdAt.IsZero() || w.child != w.request.QueryID {
		return false
	}
	canonical, e := paymentproto.CanonicalQuery(w.request)
	return e == nil && processorCanonical(w.canonical, canonical, w.digest)
}
func processorWorkMatches(w QueryWork, a processorChild) bool {
	return w.child == a.id && w.binding.id == a.supervisor && w.createdGeneration == a.generation && w.mutationGeneration == a.mutationGeneration && w.createdAt.Equal(a.created) && w.digest == a.digest && bytes.Equal(w.canonical, a.canonical) && w.request == a.request
}

func processorAdvanceMutation(ctx context.Context, tx pgx.Tx, s, b processorOutbox, a processorChild, reply paymentproto.QueryReply, completed bool) error {
	if b.state != "submission_unknown" || b.unknownAt == nil || a.mutationGeneration != b.generation || a.created.Before(*b.unknownAt) {
		return nil
	}
	state := "completed"
	if !completed {
		if reply.CommandState != "not_seen" || s.attempts >= 20 || b.attempts >= 10 || s.current == nil || *s.current != a.id || (s.state != "leased" && s.state != "retry_wait") {
			return nil
		}
		if b.kind == "checkout" {
			var cancel bool
			if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_cancel_intents WHERE order_id=$1)`, b.order).Scan(&cancel); e != nil {
				return e
			}
			if cancel {
				return nil
			}
		}
		state = "retry_wait"
	}
	tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET state=$3,last_error_code=NULL WHERE id=$1 AND state='submission_unknown' AND generation=$2 AND last_submission_unknown_at<=$4`, b.id, b.generation, state, a.created)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrPaymentsLeaseLost
	}
	return nil
}

func (p *Processor) RecordQuery(ctx context.Context, w QueryWork, headers paymentproto.SignedHeaders, raw []byte) error {
	if !p.queryWorkValid(w) {
		return ErrPaymentsInvalidWork
	}
	raw = bytes.Clone(raw)
	snapshotID, evidenceConflictID := uuid.NewString(), uuid.NewString()
	return p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		s, e := processorReadOutbox(ctx, tx, w.binding.id, "")
		if e != nil {
			return nil, e
		}
		o, e := processorLockOrder(ctx, tx, s.order)
		if e != nil {
			return nil, e
		}
		s, e = processorReadOutbox(ctx, tx, s.id, "")
		if e != nil {
			return nil, e
		}
		if s.original == nil {
			return nil, ErrPaymentsIntegrity
		}
		b, e := processorReadOutbox(ctx, tx, *s.original, "")
		if e != nil {
			return nil, e
		}
		if e = p.boundMutation(ctx, tx, b, o); e != nil {
			return nil, e
		}
		a, e := processorReadChild(ctx, tx, w.child)
		if e != nil {
			return nil, e
		}
		if !processorWorkMatches(w, a) || !processorChildBound(a, s, b, o) {
			return nil, ErrPaymentsIntegrity
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		verified, e := p.runtime.verifyQuery(now, a.request, headers, raw)
		current := processorCurrent(w.binding, s.state, s.generation, s.until, now) && s.current != nil && *s.current == a.id
		if e != nil {
			if !current {
				return nil, ErrPaymentsLeaseLost
			}
			return processorQueryFailureTx(ctx, tx, w, "protocol")
		}
		canonical, e := verified.Canonical()
		if e != nil {
			return nil, ErrPaymentsIntegrity
		}
		_, e = tx.Exec(ctx, `INSERT INTO payment_verified_queries(id,query_id,canonical_body,verified_key_id) VALUES($1,$2,$3,$4) ON CONFLICT(query_id) DO NOTHING`, snapshotID, a.id, canonical, headers.KeyID)
		if e != nil {
			return nil, e
		}
		storedID, stored, reply, found, e := processorSnapshot(ctx, tx, a)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, ErrPaymentsIntegrity
		}
		if !bytes.Equal(stored, canonical) {
			_, e = tx.Exec(ctx, `INSERT INTO payment_query_conflicts(id,verified_query_id,conflicting_digest) VALUES($1,$2,$3) ON CONFLICT(verified_query_id,conflicting_digest) DO NOTHING`, evidenceConflictID, storedID, processorDigest(canonical))
			if e != nil {
				return nil, e
			}
			return ErrPaymentsQueryConflict, nil
		}
		if _, e = normalizeQueryTx(ctx, tx, storedID); e != nil {
			return nil, e
		}
		if !current {
			return ErrPaymentsLeaseLost, nil
		}
		// Keep immutable evidence even if expiry races the final generation CAS;
		// mutable projections are a separate savepoint and roll back together.
		if _, e = tx.Exec(ctx, `SAVEPOINT query_projection`); e != nil {
			return nil, e
		}
		if e = processorAdvanceMutation(ctx, tx, s, b, a, reply, reply.CommandState == "completed"); e != nil {
			return nil, e
		}
		state := "retry_wait"
		var failure any
		if reply.CommandState == "completed" {
			state = "completed"
		} else if s.attempts == 20 {
			state = "review_required"
			failure = "attempts_exhausted"
		}
		tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET state=$3,last_error_code=$4 WHERE id=$1 AND state='leased' AND generation=$2 AND current_query_id=$5 AND lease_until>clock_timestamp()`, s.id, w.binding.generation, state, failure, a.id)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() != 1 {
			if _, e = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT query_projection`); e != nil {
				return nil, e
			}
			return ErrPaymentsLeaseLost, nil
		}
		return nil, nil
	})
}

func processorQueryFailureTx(ctx context.Context, tx pgx.Tx, w QueryWork, code string) (error, error) {
	if !processorFailure(code) {
		return nil, ErrPaymentsInvalidWork
	}
	tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET state=CASE WHEN attempt_count=20 THEN 'review_required' ELSE 'retry_wait' END,last_error_code=CASE WHEN attempt_count=20 THEN 'attempts_exhausted' ELSE $4 END WHERE id=$1 AND state='leased' AND generation=$2 AND current_query_id=$3 AND lease_until>clock_timestamp()`, w.binding.id, w.binding.generation, w.child, code)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() != 1 {
		return nil, ErrPaymentsLeaseLost
	}
	return ErrPaymentsProtocol, nil
}
func (p *Processor) recordQueryFailure(ctx context.Context, w QueryWork, f transportFailure) error {
	if !p.queryWorkValid(w) || f.seal != p.seal || f.binding != w.binding || f.child != w.child {
		return ErrPaymentsInvalidWork
	}
	if !processorFailure(f.code) {
		return ErrPaymentsInvalidWork
	}
	return p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		s, e := processorReadOutbox(ctx, tx, w.binding.id, "")
		if e != nil {
			return nil, e
		}
		if _, e = processorLockOrder(ctx, tx, s.order); e != nil {
			return nil, e
		}
		a, e := processorReadChild(ctx, tx, w.child)
		if e != nil {
			return nil, e
		}
		if !processorWorkMatches(w, a) {
			return nil, ErrPaymentsIntegrity
		}
		_, _, reply, found, e := processorSnapshot(ctx, tx, a)
		if e != nil {
			return nil, e
		}
		if found && reply.CommandState == "completed" {
			return ErrPaymentsLeaseLost, nil
		}
		return processorQueryFailureTx(ctx, tx, w, f.code)
	})
}
