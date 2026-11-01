package payments

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/moni-ai/moni-aigate/internal/paymentproto"
)

func (p *Processor) sendQuery(parent context.Context, w QueryWork) (paymentproto.SignedHeaders, []byte, *transportFailure, error) {
	if p == nil {
		return paymentproto.SignedHeaders{}, nil, nil, ErrPaymentsInvalidWork
	}
	failure := func(code string) *transportFailure {
		return &transportFailure{seal: p.seal, binding: w.binding, child: w.child, code: code}
	}
	// Local lifecycle and contract failures are not remote observations. The
	// error return keeps them separate from remote failure; neither produces a fake
	// verified query or caller-selected persisted failure code.
	if !p.queryWorkValid(w) {
		return paymentproto.SignedHeaders{}, nil, nil, ErrPaymentsInvalidWork
	}
	err := p.transaction(parent, false, func(ctx context.Context, tx pgx.Tx) (error, error) {
		s, e := processorReadOutbox(ctx, tx, w.binding.id, "")
		if e != nil {
			return nil, e
		}
		a, e := processorReadChild(ctx, tx, w.child)
		if e != nil {
			return nil, e
		}
		now, e := p.runtime.databaseTime(ctx, tx)
		if e != nil {
			return nil, e
		}
		if !processorWorkMatches(w, a) {
			return nil, ErrPaymentsIntegrity
		}
		if s.current == nil || *s.current != a.id || !processorSendable(w.binding, s.state, s.generation, s.until, now) {
			return nil, ErrPaymentsLeaseLost
		}
		return nil, nil
	})
	if err != nil {
		return paymentproto.SignedHeaders{}, nil, nil, err
	}
	ctx, leave, err := p.enter(parent)
	if err != nil {
		return paymentproto.SignedHeaders{}, nil, nil, err
	}
	defer leave()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err = p.runtime.check(ctx); err != nil {
		return paymentproto.SignedHeaders{}, nil, nil, err
	}
	status, headers, raw, err := p.runtime.doSandboxRequest(ctx, "query", bytes.Clone(w.canonical))
	if parent.Err() != nil || p.lifetime.Err() != nil {
		if parent.Err() != nil {
			return paymentproto.SignedHeaders{}, nil, nil, parent.Err()
		}
		return paymentproto.SignedHeaders{}, nil, nil, context.Canceled
	}
	if err != nil {
		if errors.Is(err, errSandboxTimeout) {
			return paymentproto.SignedHeaders{}, nil, failure("timeout"), nil
		}
		if errors.Is(err, errSandboxNetwork) {
			return paymentproto.SignedHeaders{}, nil, failure("network"), nil
		}
		return paymentproto.SignedHeaders{}, nil, nil, processorError(ctx, err)
	}
	if status < 200 || status >= 300 {
		return paymentproto.SignedHeaders{}, nil, failure(processorHTTPFailure(status)), nil
	}
	signed, err := paymentproto.ReadSignedHeaders(headers)
	if err != nil {
		return paymentproto.SignedHeaders{}, nil, failure("protocol"), nil
	}
	return signed, bytes.Clone(raw), nil, nil
}
