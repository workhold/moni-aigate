package mockpsp

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

func controlTarget(in ControlInput) (kind, target string) {
	if in.MerchantOrderID != nil {
		return "merchant", *in.MerchantOrderID
	}
	if in.CommandID != nil {
		return "command", *in.CommandID
	}
	if in.QueryID != nil {
		return "query", *in.QueryID
	}
	if in.EventID != nil {
		return "callback", *in.EventID
	}
	return "", ""
}

func (r *Repository) Control(parent context.Context, in CanonicalControl) (ControlOutcome, error) {
	if r == nil || !sameEnvironment(in.env, r.env) {
		return ControlOutcome{}, fail("invalid_request")
	}
	checked, e := ParseControlInput(r.env, in.raw)
	if e != nil || checked.digest != in.digest || !bytes.Equal(checked.raw, in.raw) {
		return ControlOutcome{}, fail("invalid_request")
	}
	in = checked
	ctx, cancel := r.bounded(parent)
	defer cancel()
	c, e := r.acquire(ctx, lockControl, in.request.OperationID)
	if e != nil {
		return ControlOutcome{}, e
	}
	defer c.close()
	var out ControlOutcome
	var replay bool
	e = c.transact(ctx, func(tx pgx.Tx) error {
		out = ControlOutcome{}
		replay = false
		var raw, parameters []byte
		var digest string
		err := tx.QueryRow(ctx, `SELECT canonical_parameters,parameter_digest,result_body FROM mock_controls WHERE operation_id=$1`, in.request.OperationID).Scan(&parameters, &digest, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if digest != in.digest || !bytes.Equal(parameters, in.raw) {
			return fail("mock_control_conflict")
		}
		reply, err := parseStoredControlReply(raw)
		if err != nil {
			return err
		}
		if reply.OperationID != in.request.OperationID {
			return fail("mock_unavailable")
		}
		out = ControlOutcome{Reply: reply, Replayed: true}
		replay = true
		return nil
	})
	if e != nil {
		return ControlOutcome{}, e
	}
	if replay {
		return out, nil
	}
	kind, target := controlTarget(in.request)
	switch kind {
	case "command":
		e = c.gate(ctx, lockCommand, target)
	case "query":
		e = c.gate(ctx, lockQuery, target)
	case "callback":
		e = c.gate(ctx, lockCallback, target)
	}
	if e != nil {
		return ControlOutcome{}, normalizeError(e)
	}
	ids := newControlIDs()
	e = c.transact(ctx, func(tx pgx.Tx) error {
		out = ControlOutcome{}
		var reply ControlReply
		var err error
		switch in.request.Action {
		case "capture_order", "emit_test_event":
			reply, err = r.controlOrderTx(ctx, tx, in, ids)
		case "release_command":
			reply, err = r.releaseTx(ctx, tx, in, ids)
		case "arm_fault":
			reply, err = r.armFaultTx(ctx, tx, in)
		default:
			return fail("invalid_request")
		}
		if err == nil {
			out = ControlOutcome{Reply: reply}
		}
		return err
	})
	if e != nil {
		return ControlOutcome{}, e
	}
	return out, nil
}

func (r *Repository) saveControl(ctx context.Context, tx pgx.Tx, in CanonicalControl, reason string, resource *string) (ControlReply, error) {
	status := "rejected"
	if reason == "accepted" {
		status = "accepted"
	}
	reply := ControlReply{OperationID: in.request.OperationID, Status: status, Reason: reason, ResourceID: resource}
	raw, e := canonicalControlReply(reply)
	if e != nil {
		return ControlReply{}, e
	}
	_, target := controlTarget(in.request)
	_, e = tx.Exec(ctx, `INSERT INTO mock_controls(operation_id,action,scenario,target_reference,parameter_digest,canonical_parameters,result_body) VALUES($1,$2,$3,$4,$5,$6,$7)`, in.request.OperationID, in.request.Action, in.request.Scenario, target, in.digest, in.raw, raw)
	return reply, e
}

func (r *Repository) controlOrderTx(ctx context.Context, tx pgx.Tx, in CanonicalControl, ids controlIDs) (ControlReply, error) {
	order := *in.request.MerchantOrderID
	if e := r.merchantGate(ctx, tx, order); e != nil {
		return ControlReply{}, e
	}
	o, found, e := loadOrder(ctx, tx, order, false)
	if e != nil {
		return ControlReply{}, e
	}
	if !found {
		return r.saveControl(ctx, tx, in, "target_not_found", nil)
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return ControlReply{}, e
	}
	if !completeTerms(o) {
		return r.saveControl(ctx, tx, in, "state_conflict", nil)
	}
	if in.request.Action == "capture_order" {
		if o.state != "ready" || now.Before(*o.created) || !now.Before(*o.expires) {
			return r.saveControl(ctx, tx, in, "state_conflict", nil)
		}
		reply, e := r.saveControl(ctx, tx, in, "accepted", strptr(ids.capture))
		if e != nil {
			return ControlReply{}, e
		}
		n := r.eventBase(o, nil, now, ids)
		_, e = tx.Exec(ctx, `UPDATE mock_orders SET state='captured',revision=2 WHERE merchant_order_id=$1 AND state='ready'`, order)
		if e != nil {
			return ControlReply{}, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO mock_captures(id,merchant_order_id,provider_transaction_id,amount,currency,occurred_at,provider_order_revision,control_operation_id) VALUES($1,$2,$3,$4,'CNY',$5,2,$6)`, ids.capture, order, ids.transaction, *o.amount, now, in.request.OperationID)
		if e != nil {
			return ControlReply{}, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO mock_order_transitions(id,merchant_order_id,revision,kind,control_operation_id,occurred_at,fact_digest) VALUES($1,$2,2,'captured',$3,$4,mock_transition_digest($2,2,'captured',NULL,$3,$4))`, ids.transition, order, in.request.OperationID, now)
		if e != nil {
			return ControlReply{}, e
		}
		e = r.insertEvent(ctx, tx, in, ids, n, strptr(ids.capture))
		return reply, e
	}
	cap, e := optionalCapture(ctx, tx, order)
	if e != nil {
		return ControlReply{}, e
	}
	n, ok := r.scenarioEvent(o, cap, now, ids, in.request.Scenario)
	if !ok {
		return r.saveControl(ctx, tx, in, "state_conflict", nil)
	}
	reply, e := r.saveControl(ctx, tx, in, "accepted", strptr(ids.eventRow))
	if e == nil {
		e = r.insertEvent(ctx, tx, in, ids, n, nil)
	}
	return reply, e
}

func (r *Repository) releaseTx(ctx context.Context, tx pgx.Tx, in CanonicalControl, ids controlIDs) (ControlReply, error) {
	s, found, e := loadCommand(ctx, tx, *in.request.CommandID)
	if e != nil {
		return ControlReply{}, e
	}
	if !found {
		return r.saveControl(ctx, tx, in, "target_not_found", nil)
	}
	command, e := r.commandFromStored(s)
	if e != nil {
		return ControlReply{}, e
	}
	if e = r.merchantGate(ctx, tx, s.order); e != nil {
		return ControlReply{}, e
	}
	if _, _, e = loadOrder(ctx, tx, s.order, false); e != nil {
		return ControlReply{}, e
	}
	var state string
	e = tx.QueryRow(ctx, `SELECT state FROM mock_command_processing WHERE command_id=$1 FOR NO KEY UPDATE`, s.id).Scan(&state)
	if e != nil {
		return ControlReply{}, e
	}
	if state != "held" {
		return r.saveControl(ctx, tx, in, "state_conflict", nil)
	}
	reply, e := r.saveControl(ctx, tx, in, "accepted", strptr(s.id))
	if e != nil {
		return ControlReply{}, e
	}
	_, e = r.applyCommandTx(ctx, tx, command, ids.command, "", in.request.OperationID)
	return reply, e
}

func (r *Repository) armFaultTx(ctx context.Context, tx pgx.Tx, in CanonicalControl) (ControlReply, error) {
	kind, target := controlTarget(in.request)
	p, e := readFaultPlan(ctx, tx, kind, target)
	if e != nil {
		return ControlReply{}, e
	}
	if p.control != "" {
		return r.saveControl(ctx, tx, in, "state_conflict", nil)
	}
	switch kind {
	case "command":
		_, found, e := loadCommand(ctx, tx, target)
		if e != nil {
			return ControlReply{}, e
		}
		if found {
			return r.saveControl(ctx, tx, in, "state_conflict", nil)
		}
	case "query":
		var found bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM mock_queries WHERE query_id=$1)`, target).Scan(&found)
		if e != nil {
			return ControlReply{}, e
		}
		if found {
			return r.saveControl(ctx, tx, in, "state_conflict", nil)
		}
	case "callback":
		var state string
		var lease *time.Time
		e := tx.QueryRow(ctx, `SELECT d.state,d.lease_until FROM mock_notification_events e JOIN mock_notification_delivery d ON d.event_id=e.id WHERE e.event_id=$1 FOR NO KEY UPDATE OF d`, target).Scan(&state, &lease)
		if errors.Is(e, pgx.ErrNoRows) {
			return r.saveControl(ctx, tx, in, "target_not_found", nil)
		}
		if e != nil {
			return ControlReply{}, e
		}
		now, e := dbNow(ctx, tx)
		if e != nil {
			return ControlReply{}, e
		}
		if state != "pending" && state != "retry_wait" && !(state == "leased" && lease != nil && !lease.After(now)) {
			return r.saveControl(ctx, tx, in, "state_conflict", nil)
		}
	default:
		return ControlReply{}, fail("invalid_request")
	}
	reply, e := r.saveControl(ctx, tx, in, "accepted", strptr(in.request.OperationID))
	if e != nil {
		return ControlReply{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO mock_fault_plans(control_operation_id,boundary,target_kind,target_id) VALUES($1,$2,$3,$4)`, in.request.OperationID, in.request.Scenario, kind, target)
	return reply, e
}
