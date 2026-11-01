// Package alertworker schedules bounded, local database observations. It has
// no model, payment, email or other outbound client.
package alertworker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/moni-ai/moni-aigate/internal/alerts"
)

var ErrConfiguration = errors.New("alert_worker_configuration")
var ErrGuard = errors.New("alert_worker_guard")

type Options struct {
	Guard   func(context.Context) error
	OnError func(string)
}
type Worker struct {
	repository                           alerts.EvaluationRepository
	options                              Options
	interval, initialBackoff, maxBackoff time.Duration
}

func New(repository alerts.EvaluationRepository, options Options) (*Worker, error) {
	if repository == nil || options.Guard == nil {
		return nil, ErrConfiguration
	}
	return &Worker{repository: repository, options: options, interval: time.Second, initialBackoff: time.Second, maxBackoff: 30 * time.Second}, nil
}
func (w *Worker) report(code string) {
	if w.options.OnError != nil {
		w.options.OnError(code)
	}
}
func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}
func (w *Worker) slot(ctx context.Context) error {
	backoff := w.initialBackoff
	for ctx.Err() == nil {
		check, stop := context.WithTimeout(ctx, time.Second)
		err := w.options.Guard(check)
		stop()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			w.report("alert_guard")
			return ErrGuard
		}
		job, cancel := context.WithTimeout(ctx, 2*time.Second)
		result, err := w.repository.EvaluateDue(job)
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		delay := w.interval
		if err != nil {
			w.report("evaluation_failed")
			delay = backoff
			backoff *= 2
			if backoff > w.maxBackoff {
				backoff = w.maxBackoff
			}
		} else {
			switch result.Outcome {
			case "idle", "evaluated", "failed_recorded", "stale_discarded":
				backoff = w.initialBackoff
			default:
				w.report("evaluation_failed")
				delay = backoff
				backoff *= 2
				if backoff > w.maxBackoff {
					backoff = w.maxBackoff
				}
			}
		}
		if !pause(ctx, delay) {
			return nil
		}
	}
	return nil
}

// Run owns exactly two fixed slots. A slow evaluation cannot queue ticks or
// create another task in its slot. Cancellation is joined before returning.
func (w *Worker) Run(ctx context.Context) error {
	run, stop := context.WithCancel(ctx)
	defer stop()
	var jobs sync.WaitGroup
	done := make(chan error, 2)
	for range 2 {
		jobs.Add(1)
		go func() { defer jobs.Done(); done <- w.slot(run) }()
	}
	var result error
	select {
	case <-ctx.Done():
	case result = <-done:
	}
	stop()
	jobs.Wait()
	return result
}
