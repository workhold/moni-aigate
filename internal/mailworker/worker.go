// Package mailworker delivers durable identity proofs with bounded SMTP IO.
// It never creates a proof, an account, a payment or a model request.
package mailworker

import (
	"context"
	"encoding/base64"
	"errors"
	"net/mail"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/moni-ai/moni-aigate/internal/identity"
	"github.com/moni-ai/moni-aigate/internal/maildeliver"
)

var (
	ErrConfiguration = errors.New("mail_worker_configuration")
	ErrGuard         = errors.New("mail_worker_guard")
)

type Sender interface {
	Send(context.Context, maildeliver.Message) error
}

type Options struct {
	Enabled func() identity.MailPurposes
	Guard   func(context.Context) error
	Origin  string
	OnError func(string)
}

type Worker struct {
	repository identity.MailRepository
	sender     Sender
	options    Options
	interval   time.Duration
	cleanup    time.Duration
}

func New(repository identity.MailRepository, sender Sender, options Options) (*Worker, error) {
	u, err := url.Parse(options.Origin)
	if repository == nil || sender == nil || options.Enabled == nil || err != nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.Scheme+"://"+u.Host != options.Origin {
		return nil, ErrConfiguration
	}
	loopback := false
	if address, err := netip.ParseAddr(u.Hostname()); err == nil {
		loopback = address.IsLoopback() && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, ErrConfiguration
	}
	return &Worker{repository: repository, sender: sender, options: options, interval: time.Second, cleanup: time.Minute}, nil
}

// NewCleanup retains guarded expiry/retention cleanup when no mail transport
// is configured. It has no Sender, origin, claim or delivery capability.
func NewCleanup(repository identity.MailRepository, guard func(context.Context) error, onError func(string)) (*Worker, error) {
	if repository == nil {
		return nil, ErrConfiguration
	}
	return &Worker{repository: repository, options: Options{Guard: guard, OnError: onError, Enabled: func() identity.MailPurposes { return identity.MailPurposes{} }}, interval: time.Second, cleanup: time.Minute}, nil
}

func (w *Worker) report(code string) {
	if w.options.OnError != nil {
		w.options.OnError(code)
	}
}

func (w *Worker) guard(ctx context.Context) bool {
	if w.options.Guard == nil {
		return ctx.Err() == nil
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return w.options.Guard(bounded) == nil && ctx.Err() == nil
}

func (w *Worker) validDelivery(claim identity.MailClaim, d identity.MailDelivery) bool {
	id, err := uuid.Parse(d.ID)
	if err != nil || id == uuid.Nil || id.String() != d.ID || d.ID != claim.ID || d.Purpose != claim.Purpose || len(d.To) > 254 {
		return false
	}
	address, err := mail.ParseAddress(d.To)
	if err != nil || address.Name != "" || address.Address != d.To {
		return false
	}
	for _, r := range d.To {
		if r < 33 || r > 126 {
			return false
		}
	}
	path := "/register/complete"
	if d.Purpose == "password_reset" {
		path = "/password/reset"
	} else if d.Purpose != "registration" {
		return false
	}
	u, err := url.Parse(d.Link)
	base, baseErr := url.Parse(w.options.Origin)
	if err != nil || baseErr != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || u.Path != path || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.RawFragment != "" || !strings.HasPrefix(u.Fragment, "token=ic1_") {
		return false
	}
	token := strings.TrimPrefix(u.Fragment, "token=ic1_")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == token
}

func (w *Worker) finish(ctx context.Context, claim identity.MailClaim, result identity.MailResult) {
	if ctx.Err() != nil {
		return // Leave the original claim for lease recovery after cancellation.
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := w.repository.FinishMail(bounded, claim, result); err != nil {
		w.report("mail_store")
	}
}

func sendSafely(ctx context.Context, sender Sender, message maildeliver.Message) (err error) {
	defer func() {
		if recover() != nil {
			err = &maildeliver.Failure{Code: "mail_network", Retryable: true}
		}
	}()
	return sender.Send(ctx, message)
}

func (w *Worker) deliver(ctx context.Context, claim identity.MailClaim) {
	if !w.options.Enabled().Allows(claim.Purpose) || !w.guard(ctx) {
		return
	}
	started := time.Now()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	prepared, err := w.repository.PrepareMail(bounded, claim)
	cancel()
	if err != nil {
		w.report("mail_store")
		return
	}
	switch prepared.Status {
	case "stale":
		return
	case "cancelled":
		w.finish(ctx, claim, identity.MailResult{Outcome: "cancelled", Code: "mail_proof_inactive"})
		return
	case "lease_short":
		w.finish(ctx, claim, identity.MailResult{Outcome: "retry", Code: "mail_network"})
		return
	case "dead":
		code := "mail_payload_invalid"
		if prepared.Code == "mail_decrypt" {
			code = prepared.Code
		}
		w.finish(ctx, claim, identity.MailResult{Outcome: "dead", Code: code})
		return
	case "ready":
	default:
		w.report("mail_preparation_invalid")
		return
	}
	if !w.validDelivery(claim, prepared.Delivery) {
		w.finish(ctx, claim, identity.MailResult{Outcome: "dead", Code: "mail_payload_invalid"})
		return
	}
	// Subtract time spent in the read/decrypt and Guard checks conservatively.
	// PostgreSQL supplies the initial duration, avoiding clock-skew assumptions.
	if !w.options.Enabled().Allows(claim.Purpose) || !w.guard(ctx) {
		return
	}
	remaining := prepared.RemainingLease - time.Since(started)
	if remaining < 25*time.Second {
		w.finish(ctx, claim, identity.MailResult{Outcome: "retry", Code: "mail_network"})
		return
	}
	timeout := remaining - 5*time.Second
	if timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	sending, stop := context.WithTimeout(ctx, timeout)
	err = sendSafely(sending, w.sender, maildeliver.Message{ID: prepared.Delivery.ID, To: prepared.Delivery.To, Purpose: prepared.Delivery.Purpose, Link: prepared.Delivery.Link})
	stop()
	if ctx.Err() != nil {
		return
	}
	if err == nil {
		w.finish(ctx, claim, identity.MailResult{Outcome: "accepted"})
		return
	}
	result := identity.MailResult{Outcome: "retry", Code: "mail_network"}
	var failure *maildeliver.Failure
	if errors.As(err, &failure) {
		if !failure.Retryable {
			result.Outcome = "dead"
		}
		switch failure.Code {
		case "mail_configuration", "mail_payload_invalid", "mail_rejected", "mail_temporary", "mail_network", "mail_tls", "mail_tls_required":
			result.Code = failure.Code
		}
	}
	w.finish(ctx, claim, result)
}

type activeMail struct {
	purpose string
	cancel  context.CancelFunc
}

// Run owns exactly two delivery slots. A disabled purpose pauses its claims and
// cancels its IO, while expiry-only secret cleanup remains bounded and guarded.
func (w *Worker) Run(ctx context.Context) error {
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	var jobs sync.WaitGroup
	active := map[string]activeMail{}
	done := make(chan string, 2)
	defer func() { cancel(); jobs.Wait() }()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	lastCleanup := time.Now()
	first := true
	for {
		if !first {
			select {
			case <-ctx.Done():
				return nil
			case id := <-done:
				delete(active, id)
				continue // The next one-second tick performs further claims.
			case <-ticker.C:
			}
		}
		first = false
		if !w.guard(run) {
			if ctx.Err() != nil {
				return nil
			}
			w.report("mail_guard")
			return ErrGuard
		}
		enabled := w.options.Enabled()
		for _, item := range active {
			if !enabled.Allows(item.purpose) {
				item.cancel()
			}
		}
		if time.Since(lastCleanup) >= w.cleanup {
			bounded, stop := context.WithTimeout(run, 5*time.Second)
			_, err := w.repository.CleanupMail(bounded, 100)
			stop()
			if err != nil {
				w.report("mail_store")
			}
			lastCleanup = time.Now()
		}
		capacity := 2 - len(active)
		if capacity <= 0 || !enabled.Registration && !enabled.PasswordReset {
			continue
		}
		if !w.guard(run) {
			if ctx.Err() != nil {
				return nil
			}
			w.report("mail_guard")
			return ErrGuard
		}
		bounded, stop := context.WithTimeout(run, 5*time.Second)
		claims, err := w.repository.ClaimMail(bounded, enabled, capacity)
		stop()
		if err != nil {
			w.report("mail_store")
			continue
		}
		for i, claim := range claims {
			if i >= capacity || active[claim.ID].cancel != nil || !enabled.Allows(claim.Purpose) {
				w.report("mail_claim_invalid")
				continue
			}
			job, stopJob := context.WithCancel(run)
			active[claim.ID] = activeMail{purpose: claim.Purpose, cancel: stopJob}
			jobs.Add(1)
			go func() {
				defer jobs.Done()
				defer stopJob()
				defer func() { done <- claim.ID }()
				w.deliver(job, claim)
			}()
		}
	}
}
