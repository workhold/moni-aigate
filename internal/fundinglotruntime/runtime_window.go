package fundinglotruntime

import (
	"context"
	"time"
)

const runtimeBudget = 30 * time.Second
const identityBudget = 5 * time.Second

type verificationWindow struct {
	ctx      context.Context
	cancel   context.CancelFunc
	deadline time.Time
}

func newVerificationWindow(ctx context.Context, budget time.Duration) (*verificationWindow, error) {
	if ctx == nil || ctx.Err() != nil || budget <= 0 {
		return nil, ErrRuntime
	}
	work, cancel := context.WithTimeout(ctx, budget)
	deadline, ok := work.Deadline()
	w := &verificationWindow{ctx: work, cancel: cancel, deadline: deadline}
	if !ok || w.check() != nil {
		cancel()
		return nil, ErrRuntime
	}
	return w, nil
}

func (w *verificationWindow) check() error {
	if w == nil || w.ctx == nil || w.ctx.Err() != nil || w.deadline.IsZero() || !time.Now().Before(w.deadline) {
		return ErrRuntime
	}
	return nil
}
