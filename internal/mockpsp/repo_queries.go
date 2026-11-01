package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func (r *Repository) Query(parent context.Context, in CanonicalQuery) (QueryOutcome, error) {
	if r == nil || !sameEnvironment(in.env, r.env) {
		return QueryOutcome{}, fail("invalid_request")
	}
	checked, err := ParseQueryInput(r.env, in.raw)
	if err != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return QueryOutcome{}, fail("invalid_request")
	}
	in = checked
	ctx, cancel := r.bounded(parent)
	defer cancel()
	q := in.request
	c, err := r.acquire(ctx, lockQuery, q.QueryID)
	if err != nil {
		return QueryOutcome{}, err
	}
	defer c.close()
	var out QueryOutcome
	var plan faultPlan
	err = c.transact(ctx, func(tx pgx.Tx) error {
		out = QueryOutcome{}
		plan = faultPlan{}
		var parameters, raw []byte
		var requestDigest, bodyDigest string
		e := tx.QueryRow(ctx, `SELECT canonical_parameters,request_digest,snapshot_body,snapshot_digest FROM mock_queries WHERE query_id=$1`, q.QueryID).Scan(&parameters, &requestDigest, &raw, &bodyDigest)
		if e == nil {
			if requestDigest != in.digest || !bytes.Equal(parameters, in.raw) {
				return fail("query_parameter_conflict")
			}
			body, e := parseStoredQueryReply(raw, bodyDigest)
			if e != nil {
				return e
			}
			if !r.queryBound(q, body.request) {
				return fail("mock_unavailable")
			}
			out = QueryOutcome{Body: body, Replayed: true}
			plan, e = readFaultPlan(ctx, tx, "query", q.QueryID)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		// Existing command identity is immutable. Reject a cross-order reference
		// before waiting on the other order's merchant gate.
		s, found, e := loadCommand(ctx, tx, q.CommandID)
		if e != nil {
			return e
		}
		if found && (s.order != q.MerchantOrderID || s.kind != q.CommandKind || s.digest != q.ParameterDigest) {
			return fail("query_parameter_conflict")
		}
		if e = r.merchantGate(ctx, tx, q.MerchantOrderID); e != nil {
			return e
		}
		o, orderExists, e := loadOrder(ctx, tx, q.MerchantOrderID, true)
		if e != nil {
			return e
		}
		s, found, e = loadCommand(ctx, tx, q.CommandID)
		if e != nil {
			return e
		}
		reply := paymentproto.QueryReply{SchemaVersion: 1, QueryID: q.QueryID, EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, MerchantOrderID: q.MerchantOrderID, CommandID: q.CommandID, CommandKind: q.CommandKind, ParameterDigest: q.ParameterDigest, CommandState: "not_seen"}
		if found {
			if s.order != q.MerchantOrderID || s.kind != q.CommandKind || s.digest != q.ParameterDigest {
				return fail("query_parameter_conflict")
			}
			if _, e = r.commandFromStored(s); e != nil {
				return e
			}
			var state string
			e = tx.QueryRow(ctx, `SELECT state FROM mock_command_processing WHERE command_id=$1`, q.CommandID).Scan(&state)
			if e != nil {
				return e
			}
			switch state {
			case "held":
				reply.CommandState = "in_progress"
			case "completed":
				var finalRaw []byte
				var finalDigest string
				e = tx.QueryRow(ctx, `SELECT result_body,result_digest FROM mock_command_results WHERE command_id=$1`, q.CommandID).Scan(&finalRaw, &finalDigest)
				if e != nil {
					return e
				}
				final, e := parseStoredCommandReply(finalRaw, finalDigest)
				if e != nil {
					return e
				}
				if !r.replyBound(final.request, s.id, s.order, s.kind, s.digest) || final.request.CommandState != "completed" || !orderExists {
					return fail("mock_unavailable")
				}
				reply.CommandState = "completed"
				reply.Result = final.request.Result
				reply.ProviderOrderRevision = final.request.ProviderOrderRevision
				if o.state == "captured" {
					cap, e := readCapture(ctx, tx, q.MerchantOrderID)
					if e != nil {
						return e
					}
					reply.ProviderOrderRevision = strptr(strconv.FormatInt(o.revision, 10))
					reply.Result = &paymentproto.CommandResult{Status: "captured", Capture: &cap.fact}
				}
				if o.state == "closed" {
					if o.closedAt == nil {
						return fail("mock_unavailable")
					}
					reply.ProviderOrderRevision = strptr(strconv.FormatInt(o.revision, 10))
					reply.Result = &paymentproto.CommandResult{Status: "closed", ClosedAt: strptr(timeText(*o.closedAt))}
				}
			default:
				return fail("mock_unavailable")
			}
		}
		body, e := canonicalQueryReply(reply)
		if e != nil {
			return e
		}
		now, e := dbNow(ctx, tx)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO mock_queries(query_id,command_id,merchant_order_id,command_kind,expected_parameter_digest,request_digest,canonical_parameters,snapshot_body,snapshot_digest,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, q.QueryID, q.CommandID, q.MerchantOrderID, q.CommandKind, q.ParameterDigest, in.digest, in.raw, body.raw, body.digest, now)
		if e != nil {
			return e
		}
		out = QueryOutcome{Body: body}
		plan, e = readFaultPlan(ctx, tx, "query", q.QueryID)
		return e
	})
	if err != nil {
		return QueryOutcome{}, err
	}
	if err = c.finishAfterPlan(ctx, plan, "after_query_snapshot_commit_drop_reply_once", !out.Replayed); err != nil {
		return QueryOutcome{}, err
	}
	return out, nil
}

func (r *Repository) queryBound(q paymentproto.QueryRequest, b paymentproto.QueryReply) bool {
	return b.EnvironmentID == r.env.ID && b.MerchantID == r.env.MerchantID && b.QueryID == q.QueryID && b.CommandID == q.CommandID && b.MerchantOrderID == q.MerchantOrderID && b.CommandKind == q.CommandKind && b.ParameterDigest == q.ParameterDigest
}

func (r *Repository) SignQueryOutcome(parent context.Context, in CanonicalQuery, out QueryOutcome) (SignedQueryOutcome, error) {
	if r == nil || !sameEnvironment(in.env, r.env) {
		return SignedQueryOutcome{}, fail("invalid_request")
	}
	checked, e := ParseQueryInput(r.env, in.raw)
	if e != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return SignedQueryOutcome{}, fail("invalid_request")
	}
	body, e := parseStoredQueryReply(out.Body.raw, out.Body.digest)
	if e != nil || !r.queryBound(checked.request, body.request) {
		return SignedQueryOutcome{}, fail("mock_unavailable")
	}
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, e := r.connection(ctx)
	if e != nil {
		return SignedQueryOutcome{}, e
	}
	defer c.close()
	var h paymentproto.SignedHeaders
	deliveryID := uuid.NewString()
	e = c.transact(ctx, func(tx pgx.Tx) error {
		h = paymentproto.SignedHeaders{}
		now, e := dbNow(ctx, tx)
		if e != nil {
			return e
		}
		h = paymentproto.SignedHeaders{DeliveryID: deliveryID, DeliveryTime: strconv.FormatInt(now.Unix(), 10)}
		var err error
		h, err = r.keyring.SignQuery(r.env, checked.request, h, body.raw)
		return err
	})
	if e != nil {
		return SignedQueryOutcome{}, e
	}
	return SignedQueryOutcome{Body: body, Headers: h}, nil
}
