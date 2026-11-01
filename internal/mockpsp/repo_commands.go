package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

type commandInput struct {
	id, order, kind, digest string
	raw                     []byte
	checkout                *paymentproto.CheckoutRequest
}
type commandIDs struct{ checkoutRef, transition string }

func newCommandIDs() commandIDs { return commandIDs{"checkout:" + uuid.NewString(), uuid.NewString()} }

type storedCommand struct {
	id, order, kind, digest, initialDigest string
	parameters, initial                    []byte
}
type storedOrder struct {
	ref, id, state   string
	amount           *int64
	currency         string
	created, expires *time.Time
	revision         int64
	checkoutRef      *string
	closedAt         *time.Time
}

func (r *Repository) Checkout(parent context.Context, in CanonicalCheckout) (CommandOutcome, error) {
	if r == nil || !sameEnvironment(in.env, r.env) {
		return CommandOutcome{}, fail("invalid_request")
	}
	checked, err := ParseCheckoutInput(r.env, in.raw)
	if err != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return CommandOutcome{}, fail("invalid_request")
	}
	q := checked.request
	return r.command(parent, commandInput{id: q.CommandID, order: q.MerchantOrderID, kind: "checkout", digest: checked.digest, raw: checked.raw, checkout: &q})
}

func (r *Repository) Cancel(parent context.Context, in CanonicalCancel) (CommandOutcome, error) {
	if r == nil || !sameEnvironment(in.env, r.env) {
		return CommandOutcome{}, fail("invalid_request")
	}
	checked, err := ParseCancelInput(r.env, in.raw)
	if err != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return CommandOutcome{}, fail("invalid_request")
	}
	q := checked.request
	return r.command(parent, commandInput{id: q.CommandID, order: q.MerchantOrderID, kind: "cancel", digest: checked.digest, raw: checked.raw})
}

func loadCommand(ctx context.Context, tx pgx.Tx, id string) (storedCommand, bool, error) {
	var c storedCommand
	err := tx.QueryRow(ctx, `SELECT command_id,merchant_order_id,kind,parameter_digest,canonical_parameters,initial_reply,initial_reply_digest FROM mock_commands WHERE command_id=$1`, id).Scan(&c.id, &c.order, &c.kind, &c.digest, &c.parameters, &c.initial, &c.initialDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

func (r *Repository) commandFromStored(s storedCommand) (commandInput, error) {
	var in commandInput
	switch s.kind {
	case "checkout":
		p, e := ParseCheckoutInput(r.env, s.parameters)
		if e != nil {
			return in, fail("mock_unavailable")
		}
		q := p.request
		in = commandInput{id: q.CommandID, order: q.MerchantOrderID, kind: s.kind, digest: p.digest, raw: p.raw, checkout: &q}
	case "cancel":
		p, e := ParseCancelInput(r.env, s.parameters)
		if e != nil {
			return in, fail("mock_unavailable")
		}
		q := p.request
		in = commandInput{id: q.CommandID, order: q.MerchantOrderID, kind: s.kind, digest: p.digest, raw: p.raw}
	default:
		return in, fail("mock_unavailable")
	}
	if in.id != s.id || in.order != s.order || in.digest != s.digest || !bytes.Equal(in.raw, s.parameters) {
		return commandInput{}, fail("mock_unavailable")
	}
	return in, nil
}

func (r *Repository) checkedCommandReply(s storedCommand) (CanonicalCommandReply, error) {
	if _, err := r.commandFromStored(s); err != nil {
		return CanonicalCommandReply{}, err
	}
	b, err := parseStoredCommandReply(s.initial, s.initialDigest)
	if err != nil {
		return CanonicalCommandReply{}, err
	}
	if !r.replyBound(b.request, s.id, s.order, s.kind, s.digest) {
		return CanonicalCommandReply{}, fail("mock_unavailable")
	}
	return b, nil
}

func (r *Repository) replyBound(q paymentproto.CommandReply, id, order, kind, digest string) bool {
	return q.EnvironmentID == r.env.ID && q.MerchantID == r.env.MerchantID && q.CommandID == id && q.MerchantOrderID == order && q.CommandKind == kind && q.ParameterDigest == digest
}

func (r *Repository) command(parent context.Context, in commandInput) (CommandOutcome, error) {
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, err := r.acquire(ctx, lockCommand, in.id)
	if err != nil {
		return CommandOutcome{}, err
	}
	defer c.close()
	ids := newCommandIDs()
	var previous storedCommand
	var exists bool
	var plan faultPlan
	err = c.transact(ctx, func(tx pgx.Tx) error {
		var e error
		previous, exists, e = loadCommand(ctx, tx, in.id)
		if e != nil {
			return e
		}
		if exists && (previous.order != in.order || previous.kind != in.kind || previous.digest != in.digest || !bytes.Equal(previous.parameters, in.raw)) {
			return fail("command_parameter_conflict")
		}
		plan, e = readFaultPlan(ctx, tx, "command", in.id)
		return e
	})
	if err != nil {
		return CommandOutcome{}, err
	}
	if exists {
		body, e := r.checkedCommandReply(previous)
		if e != nil {
			return CommandOutcome{}, e
		}
		if e = c.finishAfterPlan(ctx, plan, "after_command_commit_drop_reply_once", false); e != nil {
			return CommandOutcome{}, e
		}
		return CommandOutcome{Body: body, Replayed: true}, nil
	}
	if plan.boundary == "hold_checkout_before_effect" && in.kind != "checkout" || plan.boundary == "hold_cancel_before_effect" && in.kind != "cancel" {
		return CommandOutcome{}, fail("invalid_request")
	}
	before := plan.boundary == "before_command_commit_disconnect_once"
	if before {
		if err = c.consumePlan(ctx, plan); err != nil {
			return CommandOutcome{}, ErrDisconnect
		}
	}
	var body CanonicalCommandReply
	err = c.transact(ctx, func(tx pgx.Tx) error {
		body = CanonicalCommandReply{}
		if e := r.merchantGate(ctx, tx, in.order); e != nil {
			return e
		}
		held := ""
		if plan.boundary == "hold_checkout_before_effect" || plan.boundary == "hold_cancel_before_effect" {
			if e := consumePlanTx(ctx, tx, plan); e != nil {
				return e
			}
			held = plan.control
		}
		var e error
		body, e = r.applyCommandTx(ctx, tx, in, ids, held, "")
		if e != nil {
			return e
		}
		if before {
			return ErrDisconnect
		}
		return nil
	})
	if err != nil {
		return CommandOutcome{}, err
	}
	if err = c.finishAfterPlan(ctx, plan, "after_command_commit_drop_reply_once", true); err != nil {
		return CommandOutcome{}, err
	}
	return CommandOutcome{Body: body}, nil
}

func loadOrder(ctx context.Context, tx pgx.Tx, ref string, share bool) (storedOrder, bool, error) {
	var o storedOrder
	lock := " FOR NO KEY UPDATE"
	if share {
		lock = " FOR SHARE"
	}
	err := tx.QueryRow(ctx, `SELECT merchant_order_id,order_id,state,amount,currency,created_at,expires_at,revision,checkout_ref,closed_at FROM mock_orders WHERE merchant_order_id=$1`+lock, ref).Scan(&o.ref, &o.id, &o.state, &o.amount, &o.currency, &o.created, &o.expires, &o.revision, &o.checkoutRef, &o.closedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, false, nil
	}
	return o, err == nil, err
}

func strptr(s string) *string { return &s }

// applyCommandTx is shared by first receipt and release. The caller already owns
// the command target and merchant gates. Release preserves the initial receipt.
func (r *Repository) applyCommandTx(ctx context.Context, tx pgx.Tx, in commandInput, ids commandIDs, heldBy, releaseBy string) (CanonicalCommandReply, error) {
	q := paymentproto.CommandReply{SchemaVersion: 1, EnvironmentID: r.env.ID, MerchantID: r.env.MerchantID, MerchantOrderID: in.order, CommandID: in.id, CommandKind: in.kind, ParameterDigest: in.digest, CommandState: "in_progress"}
	if heldBy != "" {
		body, e := canonicalCommandReply(q)
		if e != nil {
			return body, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO mock_commands(command_id,merchant_order_id,kind,parameter_digest,canonical_parameters,initial_reply,initial_reply_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.id, in.order, in.kind, in.digest, in.raw, body.raw, body.digest)
		if e == nil {
			_, e = tx.Exec(ctx, `INSERT INTO mock_command_processing(command_id,state,held_by_control_id) VALUES($1,'held',$2)`, in.id, heldBy)
		}
		return body, e
	}
	o, exists, e := loadOrder(ctx, tx, in.order, false)
	if e != nil {
		return CanonicalCommandReply{}, e
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return CanonicalCommandReply{}, e
	}
	q.CommandState = "completed"
	revision := int64(1)
	if exists {
		revision = o.revision
	}
	q.ProviderOrderRevision = strptr(strconv.FormatInt(revision, 10))
	result := paymentproto.CommandResult{}
	change := ""
	if in.kind == "checkout" {
		if !exists {
			exp, _ := time.Parse(repoTimeLayout, in.checkout.ExpiresAt)
			if now.Before(exp) {
				result.Status = "ready"
				result.CheckoutRef = strptr(ids.checkoutRef)
				change = "ready"
			} else {
				result.Status = "rejected"
				result.FailureCode = strptr("order_expired")
				change = "closed"
			}
		} else {
			result.Status = "rejected"
			reason := "checkout_rejected"
			if o.state == "closed" {
				reason = "order_closed"
			}
			result.FailureCode = &reason
		}
	} else {
		switch {
		case !exists:
			result.Status = "closed"
			result.ClosedAt = strptr(timeText(now))
			change = "closed"
		case o.state == "ready":
			revision = 2
			q.ProviderOrderRevision = strptr("2")
			result.Status = "closed"
			result.ClosedAt = strptr(timeText(now))
			change = "closed"
		case o.state == "closed":
			if o.closedAt == nil {
				return CanonicalCommandReply{}, fail("mock_unavailable")
			}
			result.Status = "closed"
			result.ClosedAt = strptr(timeText(*o.closedAt))
		case o.state == "captured":
			cap, e := readCapture(ctx, tx, in.order)
			if e != nil {
				return CanonicalCommandReply{}, e
			}
			result.Status = "captured"
			result.Capture = &cap.fact
		default:
			return CanonicalCommandReply{}, fail("mock_unavailable")
		}
	}
	q.Result = &result
	body, e := canonicalCommandReply(q)
	if e != nil {
		return CanonicalCommandReply{}, e
	}
	if releaseBy == "" {
		_, e = tx.Exec(ctx, `INSERT INTO mock_commands(command_id,merchant_order_id,kind,parameter_digest,canonical_parameters,initial_reply,initial_reply_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.id, in.order, in.kind, in.digest, in.raw, body.raw, body.digest)
		if e != nil {
			return CanonicalCommandReply{}, e
		}
	}
	if !exists {
		orderID := in.order[len(in.order)-36:]
		var amount any
		var created, expires any
		var checkout any
		var closed any
		if in.checkout != nil {
			amount, _ = strconv.ParseInt(in.checkout.Amount, 10, 64)
			created, _ = time.Parse(repoTimeLayout, in.checkout.CreatedAt)
			expires, _ = time.Parse(repoTimeLayout, in.checkout.ExpiresAt)
			checkout = ids.checkoutRef
		}
		if change == "closed" {
			closed = now
		}
		_, e = tx.Exec(ctx, `INSERT INTO mock_orders(merchant_order_id,order_id,environment_id,merchant_id,amount,created_at,expires_at,state,checkout_ref,closed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, in.order, orderID, r.env.ID, r.env.MerchantID, amount, created, expires, change, checkout, closed)
	} else if change != "" {
		_, e = tx.Exec(ctx, `UPDATE mock_orders SET state=$2,revision=$3,closed_at=$4 WHERE merchant_order_id=$1`, in.order, change, revision, now)
	}
	if e != nil {
		return CanonicalCommandReply{}, e
	}
	if change != "" {
		_, e = tx.Exec(ctx, `INSERT INTO mock_order_transitions(id,merchant_order_id,revision,kind,command_id,occurred_at,fact_digest) VALUES($1,$2,$3,$4,$5,$6,mock_transition_digest($2,$3,$4,$5,NULL,$6))`, ids.transition, in.order, revision, change, in.id, now)
		if e != nil {
			return CanonicalCommandReply{}, e
		}
	}
	if releaseBy == "" {
		_, e = tx.Exec(ctx, `INSERT INTO mock_command_processing(command_id,state,completed_at) VALUES($1,'completed',$2)`, in.id, now)
	} else {
		tag, err := tx.Exec(ctx, `UPDATE mock_command_processing SET state='completed',completed_at=$2,released_by_control_id=$3 WHERE command_id=$1 AND state='held'`, in.id, now, releaseBy)
		e = err
		if e == nil && tag.RowsAffected() != 1 {
			e = fail("mock_unavailable")
		}
	}
	if e == nil {
		_, e = tx.Exec(ctx, `INSERT INTO mock_command_results(command_id,result_body,result_digest,provider_order_revision,completed_at) VALUES($1,$2,$3,$4,$5)`, in.id, body.raw, body.digest, revision, now)
	}
	return body, e
}
