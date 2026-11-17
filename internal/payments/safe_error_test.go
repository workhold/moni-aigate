package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSafeErrorCodeNeverExposesRawCause(t *testing.T) {
	secret := "postgres://user:private-password@example.invalid/db"
	if got := SafeErrorCode(errors.New(secret)); got != ErrPaymentsUnavailable.Error() {
		t.Fatalf("raw failure classification=%q", got)
	}
	if got := SafeErrorCode(ErrPaymentsGuard); got != ErrPaymentsGuard.Error() {
		t.Fatalf("guard classification=%q", got)
	}
	if got := SafeErrorCode(&PaymentError{Code: "payments_runtime_disabled"}); got != "payments_runtime_disabled" {
		t.Fatalf("domain classification=%q", got)
	}
	if got := SafeErrorCode(context.DeadlineExceeded); got != "payment_deadline_exceeded" {
		t.Fatalf("deadline classification=%q", got)
	}
	if got := SafeErrorCode(&pgconn.PgError{Code: "55000"}); got != "payment_postgres_55000" {
		t.Fatalf("SQLSTATE classification=%q", got)
	}
	if got := SafeErrorCode(nil); got != "" {
		t.Fatalf("nil classification=%q", got)
	}
}
