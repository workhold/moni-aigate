package payments

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func (p *Processor) PrepareMutation(ctx context.Context, lease MutationLease) (MutationWork, error) {
	if !p.validMutation(lease.binding) {
		return MutationWork{}, ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return MutationWork{}, ErrPaymentsRecovery
	}
	var work MutationWork
	err := p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		b, e := processorReadOutbox(ctx, tx, lease.binding.id, "")
		if e != nil {
			return nil, e
		}
		o, e := processorLockOrder(ctx, tx, b.order)
		if e != nil {
			return nil, e
		}
		b, e = processorReadOutbox(ctx, tx, b.id, "")
		if e != nil {
			return nil, e
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		if b.kind != lease.binding.kind || !processorSendable(lease.binding, b.state, b.generation, b.until, now) {
			return nil, ErrPaymentsLeaseLost
		}
		if e = p.boundMutation(ctx, tx, b, o); e != nil {
			return nil, e
		}
		work = MutationWork{binding: lease.binding, canonical: bytes.Clone(b.canonical), digest: *b.digest}
		return nil, nil
	})
	if err != nil {
		return MutationWork{}, err
	}
	return work, nil
}

func (p *Processor) mutationWorkValid(w MutationWork) bool {
	return p.validMutation(w.binding) && len(w.canonical) > 0 && processorDigest(w.canonical) == w.digest
}
func (p *Processor) sendMutation(parent context.Context, w MutationWork) (MutationResponse, error) {
	if !p.mutationWorkValid(w) {
		return MutationResponse{}, ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return MutationResponse{}, ErrPaymentsRecovery
	}
	err := p.transaction(parent, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		b, e := processorReadOutbox(ctx, tx, w.binding.id, "")
		if e != nil {
			return nil, e
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		if !processorSendable(w.binding, b.state, b.generation, b.until, now) {
			return nil, ErrPaymentsLeaseLost
		}
		if b.kind != w.binding.kind || b.digest == nil || *b.digest != w.digest || !bytes.Equal(b.canonical, w.canonical) {
			return nil, ErrPaymentsIntegrity
		}
		return nil, nil
	})
	if err != nil {
		return MutationResponse{}, err
	}
	ctx, leave, err := p.enter(parent)
	if err != nil {
		return MutationResponse{}, err
	}
	defer leave()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = p.runtime.check(ctx); err != nil {
		return MutationResponse{}, err
	}
	response := MutationResponse{seal: p.seal, binding: w.binding, requestDigest: w.digest}
	status, _, raw, err := p.runtime.doSandboxRequest(ctx, w.binding.kind, bytes.Clone(w.canonical))
	if parent.Err() != nil || p.lifetime.Err() != nil {
		if parent.Err() != nil {
			return MutationResponse{}, parent.Err()
		}
		return MutationResponse{}, context.Canceled
	}
	if err != nil {
		if errors.Is(err, errSandboxTimeout) {
			response.failure = "timeout"
		} else if errors.Is(err, errSandboxNetwork) {
			response.failure = "network"
		} else {
			return MutationResponse{}, processorError(ctx, err)
		}
		return response, nil
	}
	if status < 200 || status >= 300 {
		response.failure = processorHTTPFailure(status)
		return response, nil
	}
	reply, err := paymentproto.ParseCommandReply(raw)
	if err != nil {
		response.failure = "protocol"
		return response, nil
	}
	canonical, err := paymentproto.CanonicalCommandReply(reply)
	if err != nil {
		response.failure = "protocol"
		return response, nil
	}
	// Record binds the complete tuple to the original locked order. The adapter
	// only creates a sealed typed observation, never a verified funding fact.
	response.canonical = canonical
	return response, nil
}
func processorHTTPFailure(status int) string {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "auth"
	}
	if status >= 500 {
		return "temporary"
	}
	return "protocol"
}
func processorFailure(code string) bool {
	switch code {
	case "timeout", "network", "auth", "protocol", "environment", "temporary":
		return true
	}
	return false
}
func processorReplyBound(r paymentproto.CommandReply, b processorOutbox, o processorOrder) bool {
	return b.command != nil && b.digest != nil && r.EnvironmentID == o.environment && r.MerchantID == o.merchant && r.MerchantOrderID == o.merchantOrder && r.CommandID == *b.command && r.CommandKind == b.kind && r.ParameterDigest == *b.digest
}

func (p *Processor) RecordMutation(ctx context.Context, lease MutationLease, response MutationResponse) error {
	if !p.validMutation(lease.binding) || response.seal != p.seal || response.binding != lease.binding || response.requestDigest == "" || (len(response.canonical) == 0) == (response.failure == "") || (response.failure != "" && !processorFailure(response.failure)) {
		return ErrPaymentsInvalidWork
	}
	if p.runtime.recovery() {
		return ErrPaymentsRecovery
	}
	observationID, supervisorID := uuid.NewString(), uuid.NewString()
	return p.transaction(ctx, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		b, e := processorReadOutbox(ctx, tx, lease.binding.id, "")
		if e != nil {
			return nil, e
		}
		o, e := processorLockOrder(ctx, tx, b.order)
		if e != nil {
			return nil, e
		}
		b, e = processorReadOutbox(ctx, tx, b.id, "")
		if e != nil {
			return nil, e
		}
		if e = p.boundMutation(ctx, tx, b, o); e != nil {
			return nil, e
		}
		if b.kind != lease.binding.kind || *b.digest != response.requestDigest || lease.binding.generation > b.generation {
			return nil, ErrPaymentsIntegrity
		}
		failure := response.failure
		var reply paymentproto.CommandReply
		validReply := false
		if len(response.canonical) > 0 {
			reply, e = paymentproto.ParseCommandReply(response.canonical)
			if e != nil {
				return nil, ErrPaymentsInvalidWork
			}
			canonical, e := paymentproto.CanonicalCommandReply(reply)
			if e != nil || !bytes.Equal(canonical, response.canonical) {
				return nil, ErrPaymentsInvalidWork
			}
			if processorReplyBound(reply, b, o) {
				_, e = tx.Exec(ctx, `INSERT INTO payment_command_observations(id,outbox_id,generation,canonical_reply) VALUES($1,$2,$3,$4) ON CONFLICT(outbox_id,generation,reply_digest) DO NOTHING`, observationID, b.id, lease.binding.generation, canonical)
				if e != nil {
					return nil, e
				}
				validReply = true
			} else {
				failure = "protocol"
			}
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		if !processorCurrent(lease.binding, b.state, b.generation, b.until, now) {
			if validReply {
				return ErrPaymentsLeaseLost, nil
			}
			return nil, ErrPaymentsLeaseLost
		}
		if _, e = tx.Exec(ctx, `SAVEPOINT mutation_projection`); e != nil {
			return nil, e
		}
		state := "submission_unknown"
		if validReply {
			state = "completed"
			if reply.CommandState == "in_progress" || (reply.Result != nil && reply.Result.Status == "captured") {
				if e = processorSupervisor(ctx, tx, b, supervisorID); e != nil {
					return nil, e
				}
			}
		} else {
			if !processorFailure(failure) {
				return nil, ErrPaymentsIntegrity
			}
			if e = processorSupervisor(ctx, tx, b, supervisorID); e != nil {
				return nil, e
			}
		}
		tag, e := tx.Exec(ctx, `UPDATE payment_command_outbox SET state=$3,last_error_code=$4 WHERE id=$1 AND state='leased' AND generation=$2 AND lease_until>clock_timestamp()`, b.id, lease.binding.generation, state, processorNullable(failure))
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() != 1 {
			if validReply {
				if _, e = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT mutation_projection`); e != nil {
					return nil, e
				}
				return ErrPaymentsLeaseLost, nil
			}
			return nil, ErrPaymentsLeaseLost
		}
		return nil, nil
	})
}
func processorNullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
