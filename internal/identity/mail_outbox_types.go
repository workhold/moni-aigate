package identity

import (
	"context"
	"time"
)

// MailPurposes is the effective startup policy, including transport and recovery
// mode. It pauses delivery; it never extends or revokes an identity proof.
type MailPurposes struct {
	Registration  bool
	PasswordReset bool
}

func (p MailPurposes) Allows(purpose string) bool {
	return purpose == "registration" && p.Registration || purpose == "password_reset" && p.PasswordReset
}

type MailClaim struct {
	ID, Purpose string
	Generation  int64
	Attempt     int
	LeaseUntil  time.Time
}

// MailDelivery exists only for the immediate SMTP attempt. It must not be
// logged, serialized to an API, or retained by the worker after completion.
type MailDelivery struct{ ID, To, Purpose, Link string }

type MailPreparation struct {
	Status         string // ready, stale, cancelled, lease_short, dead
	Code           string // a fixed safe code, never database or SMTP text
	Delivery       MailDelivery
	RemainingLease time.Duration // measured by PostgreSQL, not the process clock
}

type MailResult struct {
	Outcome string // accepted, retry, dead, cancelled
	Code    string // an allowlisted safe code
}

type MailCleanup struct {
	Cancelled, Dead, SecretsCleared, OutboxesDeleted, ChallengesDeleted, SubjectsDeleted int
}

// MailRepository intentionally exposes no account creation, password mutation,
// model transport or financial operation to the delivery worker.
type MailRepository interface {
	ClaimMail(context.Context, MailPurposes, int) ([]MailClaim, error)
	PrepareMail(context.Context, MailClaim) (MailPreparation, error)
	FinishMail(context.Context, MailClaim, MailResult) (bool, error)
	CleanupMail(context.Context, int) (MailCleanup, error)
}
